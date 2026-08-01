package database

import (
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPreparePolicyDecisionEpochColumnBackfillsLegacyRows(t *testing.T) {
	testCases := []struct {
		name       string
		columnDDL  string
		insertDDL  string
		wantEpochs []int64
	}{
		{
			name:      "column_missing",
			columnDDL: "",
			insertDDL: `
				INSERT INTO policy_decisions (id)
				VALUES ('legacy-a'), ('legacy-b')
			`,
			wantEpochs: []int64{1, 1},
		},
		{
			name:      "nullable_interrupted_column",
			columnDDL: ", policy_epoch INTEGER DEFAULT 0",
			insertDDL: `
				INSERT INTO policy_decisions (id, policy_epoch)
				VALUES
					('legacy-null', NULL),
					('legacy-zero', 0),
					('current-positive', 7)
			`,
			wantEpochs: []int64{7, 1, 1},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := openPolicyDecisionEpochSQLiteTestDB(t, testCase.name)
			if err := db.Exec(`
				CREATE TABLE policy_decisions (
					id TEXT PRIMARY KEY
			` + testCase.columnDDL + `
				)
			`).Error; err != nil {
				t.Fatalf("create legacy policy decisions: %v", err)
			}
			if err := db.Exec(testCase.insertDDL).Error; err != nil {
				t.Fatalf("seed legacy policy decisions: %v", err)
			}

			for run := 1; run <= 2; run++ {
				if err := PreparePolicyDecisionEpochColumn(db); err != nil {
					t.Fatalf("prepare policy decision epoch run %d: %v", run, err)
				}
			}

			var epochs []int64
			if err := db.Table("policy_decisions").
				Order("id ASC").
				Pluck("policy_epoch", &epochs).Error; err != nil {
				t.Fatalf("read prepared policy epochs: %v", err)
			}
			if fmt.Sprint(epochs) != fmt.Sprint(testCase.wantEpochs) {
				t.Fatalf(
					"prepared policy epochs = %v, want %v",
					epochs,
					testCase.wantEpochs,
				)
			}
		})
	}
}

func TestPolicyDecisionEpochMigrationCompletesCanonicalSQLiteContract(
	t *testing.T,
) {
	db := openPolicyDecisionEpochSQLiteTestDB(t, "canonical-contract")
	if err := db.AutoMigrate(&models.PolicyDecision{}); err != nil {
		t.Fatalf("create canonical policy decisions fixture: %v", err)
	}
	if err := db.Migrator().DropIndex(
		&models.PolicyDecision{},
		"idx_policy_decisions_policy_epoch",
	); err != nil {
		t.Fatalf("drop current policy epoch index: %v", err)
	}
	if err := db.Migrator().DropColumn(
		&models.PolicyDecision{},
		"PolicyEpoch",
	); err != nil {
		t.Fatalf("remove current policy epoch column: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO policy_decisions (
			id, created_at, organization_id, project_id,
			actor_type, actor_id, scope, allowed, reason_code
		) VALUES (
			'legacy-decision', CURRENT_TIMESTAMP, 1, 1,
			'service_principal', 'principal-1', 'tickets:read', TRUE, 'allowed'
		)
	`).Error; err != nil {
		t.Fatalf("seed pre-epoch policy decision: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := PreparePolicyDecisionEpochColumn(db); err != nil {
			t.Fatalf("prepare policy decision epoch run %d: %v", run, err)
		}
		if err := db.AutoMigrate(&models.PolicyDecision{}); err != nil {
			t.Fatalf("canonical policy decision migration run %d: %v", run, err)
		}
		if err := MigratePolicyDecisionEpochContract(db); err != nil {
			t.Fatalf("finalize policy decision epoch run %d: %v", run, err)
		}
		if err := ValidatePolicyDecisionEpochContract(db); err != nil {
			t.Fatalf("validate policy decision epoch run %d: %v", run, err)
		}
	}
	if err := db.Migrator().DropIndex(
		&models.PolicyDecision{},
		"idx_policy_decisions_policy_epoch",
	); err != nil {
		t.Fatalf("drop policy epoch index before repair: %v", err)
	}
	if err := MigratePolicyDecisionEpochContract(db); err != nil {
		t.Fatalf("repair policy decision epoch index: %v", err)
	}
	if !db.Migrator().HasIndex(
		&models.PolicyDecision{},
		"idx_policy_decisions_policy_epoch",
	) {
		t.Fatal("policy decision epoch contract did not repair its index")
	}

	var epoch int64
	if err := db.Table("policy_decisions").
		Select("policy_epoch").
		Where("id = ?", "legacy-decision").
		Scan(&epoch).Error; err != nil {
		t.Fatalf("read canonical policy decision epoch: %v", err)
	}
	if epoch != legacyPolicyDecisionEpoch {
		t.Fatalf(
			"canonical policy decision epoch = %d, want %d",
			epoch,
			legacyPolicyDecisionEpoch,
		)
	}
	if err := db.Exec(`
		INSERT INTO policy_decisions (
			id, created_at, organization_id, project_id,
			actor_type, actor_id, scope, allowed, reason_code
		) VALUES (
			'missing-epoch', CURRENT_TIMESTAMP, 1, 1,
			'service_principal', 'principal-1', 'tickets:read', TRUE, 'allowed'
		)
	`).Error; err == nil {
		t.Fatal("canonical policy decision contract accepted a missing epoch")
	}
}

func TestRunMigrationsBackfillsLegacyPolicyDecisionEpoch(t *testing.T) {
	db := openPolicyDecisionEpochSQLiteTestDB(t, "run-migrations")
	if err := db.AutoMigrate(&models.PolicyDecision{}); err != nil {
		t.Fatalf("create current policy decisions fixture: %v", err)
	}
	if err := db.Migrator().DropIndex(
		&models.PolicyDecision{},
		"idx_policy_decisions_policy_epoch",
	); err != nil {
		t.Fatalf("drop current policy epoch index: %v", err)
	}
	if err := db.Migrator().DropColumn(
		&models.PolicyDecision{},
		"PolicyEpoch",
	); err != nil {
		t.Fatalf("remove current policy epoch column: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO policy_decisions (
			id, created_at, organization_id, project_id,
			actor_type, actor_id, scope, allowed, reason_code
		) VALUES (
			'legacy-run-migrations', CURRENT_TIMESTAMP, 0, 0,
			'service_principal', 'principal-1', 'tickets:read', TRUE, 'allowed'
		)
	`).Error; err != nil {
		t.Fatalf("seed legacy migration entrypoint decision: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := RunMigrations(db); err != nil {
			t.Fatalf("run full migration %d: %v", run, err)
		}
	}

	var decision struct {
		OrganizationID uint
		ProjectID      uint
		PolicyEpoch    int64
	}
	if err := db.Table("policy_decisions").
		Where("id = ?", "legacy-run-migrations").
		First(&decision).Error; err != nil {
		t.Fatalf("read fully migrated policy decision: %v", err)
	}
	if decision.OrganizationID == 0 ||
		decision.ProjectID == 0 ||
		decision.PolicyEpoch != legacyPolicyDecisionEpoch {
		t.Fatalf("fully migrated policy decision = %+v", decision)
	}
}

func openPolicyDecisionEpochSQLiteTestDB(
	t *testing.T,
	suffix string,
) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:policy-decision-epoch-%s-%d?mode=memory&cache=shared",
		suffix,
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open SQLite policy decision epoch database: %v", err)
	}
	return db
}
