package database

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
)

func exercisePostgresProjectStatusColumnContractMatrix(
	t *testing.T,
	owner *gorm.DB,
) {
	t.Helper()
	rollback := errors.New("rollback Project status catalog mutation")
	if err := owner.Connection(func(pinned *gorm.DB) error {
		if err := pinned.Exec(`
			CREATE TEMP TABLE projects (
				id BIGINT PRIMARY KEY,
				organization_id BIGINT NOT NULL,
				status VARCHAR(20) NOT NULL DEFAULT 'active'
			)
		`).Error; err != nil {
			return err
		}
		if err := pinned.Exec("SET search_path TO pg_temp").Error; err != nil {
			return err
		}
		defer func() {
			if err := pinned.Session(&gorm.Session{NewDB: true}).
				Exec("RESET search_path").Error; err != nil {
				t.Errorf("reset Project status search_path: %v", err)
			}
			if err := pinned.Session(&gorm.Session{NewDB: true}).Exec(
				"DROP TABLE IF EXISTS pg_temp.projects",
			).Error; err != nil {
				t.Errorf("drop Project status fixture: %v", err)
			}
		}()
		if err := validateWebhookProjectStatusColumnContract(
			pinned,
		); err != nil {
			return fmt.Errorf("exact Project status fixture: %w", err)
		}
		tests := []struct {
			name     string
			mutation string
		}{
			{
				name: "nullable",
				mutation: "ALTER TABLE projects " +
					"ALTER COLUMN status DROP NOT NULL",
			},
			{
				name: "wrong length",
				mutation: "ALTER TABLE projects " +
					"ALTER COLUMN status TYPE VARCHAR(19)",
			},
			{
				name: "missing default",
				mutation: "ALTER TABLE projects " +
					"ALTER COLUMN status DROP DEFAULT",
			},
			{
				name: "wrong default",
				mutation: "ALTER TABLE projects " +
					"ALTER COLUMN status SET DEFAULT 'archived'",
			},
			{
				name: "generated",
				mutation: "ALTER TABLE projects DROP COLUMN status; " +
					"ALTER TABLE projects ADD COLUMN status VARCHAR(20) " +
					"GENERATED ALWAYS AS ('active') STORED",
			},
			{
				name: "identity",
				mutation: "ALTER TABLE projects DROP COLUMN status; " +
					"ALTER TABLE projects ADD COLUMN status BIGINT " +
					"GENERATED ALWAYS AS IDENTITY",
			},
		}
		for _, test := range tests {
			t.Run("postgres Project status "+test.name, func(t *testing.T) {
				err := pinned.Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec(test.mutation).Error; err != nil {
						return err
					}
					if err := validateWebhookProjectStatusColumnContract(
						tx,
					); err == nil {
						return errors.New(
							"unsafe PostgreSQL Project status contract was accepted",
						)
					}
					return rollback
				})
				if !errors.Is(err, rollback) {
					t.Fatalf("Project status mutation %q: %v", test.name, err)
				}
			})
		}
		required := webhookCredentialConstraintDefinitions["chk_projects_status"].expression
		probe := pinned.Session(&gorm.Session{NewDB: true})
		if err := probe.Exec(
			"CREATE TEMP TABLE project_status_null_probe (" +
				"status VARCHAR(20), CONSTRAINT chk_projects_status " +
				"CHECK (" + required + "))",
		).Error; err != nil {
			return err
		}
		probeErr := probe.Session(&gorm.Session{NewDB: true}).
			Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec(
					"INSERT INTO project_status_null_probe (status) VALUES (NULL)",
				).Error; err == nil {
					return errors.New(
						"PostgreSQL Project status CHECK accepted raw NULL",
					)
				}
				return rollback
			})
		if !errors.Is(probeErr, rollback) {
			return fmt.Errorf(
				"PostgreSQL Project status NULL behavior: %w",
				probeErr,
			)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
