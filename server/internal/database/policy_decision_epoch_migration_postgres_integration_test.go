package database

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresPolicyDecisionEpochMigrationIntegration(t *testing.T) {
	dsn := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_MIGRATION_TEST_DSN"),
	)
	if dsn == "" {
		t.Skip(
			"set CHRONODESK_POSTGRES_MIGRATION_TEST_DSN for the PostgreSQL migration test",
		)
	}
	owner, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL migration database: %v", err)
	}

	testCases := []struct {
		name       string
		prepareDDL string
		insertDDL  string
		wantEpochs []int64
		reproduces bool
	}{
		{
			name: "column_missing",
			prepareDDL: `
				DROP INDEX IF EXISTS idx_policy_decisions_policy_epoch;
				ALTER TABLE policy_decisions DROP COLUMN policy_epoch
			`,
			insertDDL: `
				INSERT INTO policy_decisions (
					id, organization_id, project_id,
					actor_type, actor_id, scope, allowed, reason_code
				) VALUES
					('legacy-a', 1, 1, 'service_principal', 'principal-1',
					 'tickets:read', TRUE, 'allowed'),
					('legacy-b', 1, 1, 'service_principal', 'principal-1',
					 'tickets:write', FALSE, 'policy_denied')
			`,
			wantEpochs: []int64{1, 1},
			reproduces: true,
		},
		{
			name: "nullable_interrupted_column",
			prepareDDL: `
				ALTER TABLE policy_decisions
					ALTER COLUMN policy_epoch DROP NOT NULL,
					ALTER COLUMN policy_epoch SET DEFAULT 0
			`,
			insertDDL: `
				INSERT INTO policy_decisions (
					id, organization_id, project_id,
					actor_type, actor_id, scope, allowed, reason_code,
					policy_epoch
				) VALUES
					('current-positive', 1, 1, 'service_principal', 'principal-1',
					 'tickets:read', TRUE, 'allowed', 7),
					('legacy-null', 1, 1, 'service_principal', 'principal-1',
					 'tickets:read', TRUE, 'allowed', NULL),
					('legacy-zero', 1, 1, 'service_principal', 'principal-1',
					 'tickets:write', FALSE, 'policy_denied', 0)
			`,
			wantEpochs: []int64{7, 1, 1},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tx := owner.Begin()
			if tx.Error != nil {
				t.Fatalf("begin PostgreSQL migration fixture: %v", tx.Error)
			}
			defer tx.Rollback()

			schemaName := fmt.Sprintf(
				"policy_decision_epoch_%s_%d",
				testCase.name,
				time.Now().UnixNano(),
			)
			if err := tx.Exec(
				`CREATE SCHEMA "` + schemaName + `"`,
			).Error; err != nil {
				t.Fatalf("create isolated PostgreSQL schema: %v", err)
			}
			if err := tx.Exec(
				`SET LOCAL search_path TO "` + schemaName + `"`,
			).Error; err != nil {
				t.Fatalf("select isolated PostgreSQL schema: %v", err)
			}
			if err := tx.AutoMigrate(&models.PolicyDecision{}); err != nil {
				t.Fatalf("create current policy decision fixture: %v", err)
			}
			if err := tx.Exec(testCase.prepareDDL).Error; err != nil {
				t.Fatalf("prepare historical PostgreSQL shape: %v", err)
			}
			if err := tx.Exec(testCase.insertDDL).Error; err != nil {
				t.Fatalf("seed historical PostgreSQL decisions: %v", err)
			}

			if testCase.reproduces {
				directErr := tx.Transaction(func(attempt *gorm.DB) error {
					return attempt.AutoMigrate(&models.PolicyDecision{})
				})
				if directErr == nil ||
					!strings.Contains(directErr.Error(), "policy_epoch") {
					t.Fatalf(
						"direct canonical migration error = %v, want non-empty-table policy_epoch failure",
						directErr,
					)
				}
			}

			for run := 1; run <= 2; run++ {
				if err := PreparePolicyDecisionEpochColumn(tx); err != nil {
					t.Fatalf("prepare PostgreSQL epoch run %d: %v", run, err)
				}
				if err := tx.AutoMigrate(&models.PolicyDecision{}); err != nil {
					t.Fatalf("canonical PostgreSQL migration run %d: %v", run, err)
				}
				if err := MigratePolicyDecisionEpochContract(tx); err != nil {
					t.Fatalf("finalize PostgreSQL epoch run %d: %v", run, err)
				}
				if err := ValidatePolicyDecisionEpochContract(tx); err != nil {
					t.Fatalf("validate PostgreSQL epoch run %d: %v", run, err)
				}
			}
			if err := tx.Exec(
				"ALTER TABLE policy_decisions " +
					"ALTER COLUMN policy_epoch SET DEFAULT 0",
			).Error; err != nil {
				t.Fatalf("restore dangerous PostgreSQL epoch default: %v", err)
			}
			if err := tx.Exec(
				"DROP INDEX " + policyDecisionEpochIndex,
			).Error; err != nil {
				t.Fatalf("drop PostgreSQL epoch index before repair: %v", err)
			}
			if err := MigratePolicyDecisionEpochContract(tx); err != nil {
				t.Fatalf("repair PostgreSQL epoch index: %v", err)
			}
			if !tx.Migrator().HasIndex(
				&models.PolicyDecision{},
				policyDecisionEpochIndex,
			) {
				t.Fatal("PostgreSQL epoch contract did not repair its index")
			}

			var epochs []int64
			if err := tx.Table("policy_decisions").
				Order("id ASC").
				Pluck("policy_epoch", &epochs).Error; err != nil {
				t.Fatalf("read migrated PostgreSQL epochs: %v", err)
			}
			if !reflect.DeepEqual(epochs, testCase.wantEpochs) {
				t.Fatalf(
					"migrated PostgreSQL epochs = %v, want %v",
					epochs,
					testCase.wantEpochs,
				)
			}

			var contract struct {
				DataType      string  `gorm:"column:data_type"`
				IsNullable    string  `gorm:"column:is_nullable"`
				ColumnDefault *string `gorm:"column:column_default"`
			}
			if err := tx.Raw(`
				SELECT data_type, is_nullable, column_default
				FROM information_schema.columns
				WHERE table_schema = CURRENT_SCHEMA()
				  AND table_name = 'policy_decisions'
				  AND column_name = 'policy_epoch'
			`).Scan(&contract).Error; err != nil {
				t.Fatalf("read PostgreSQL epoch contract: %v", err)
			}
			if contract.DataType != "bigint" ||
				contract.IsNullable != "NO" ||
				contract.ColumnDefault != nil {
				t.Fatalf("unexpected PostgreSQL epoch contract: %+v", contract)
			}

			missingEpochErr := tx.Transaction(func(probe *gorm.DB) error {
				return probe.Exec(`
					INSERT INTO policy_decisions (
						id, organization_id, project_id,
						actor_type, actor_id, scope, allowed, reason_code
					) VALUES (
						'missing-epoch', 1, 1, 'service_principal',
						'principal-1', 'tickets:read', TRUE, 'allowed'
					)
				`).Error
			})
			if missingEpochErr == nil ||
				!strings.Contains(missingEpochErr.Error(), "policy_epoch") {
				t.Fatalf(
					"missing epoch insert error = %v, want policy_epoch rejection",
					missingEpochErr,
				)
			}
		})
	}
}
