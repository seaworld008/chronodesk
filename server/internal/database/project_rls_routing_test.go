package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type projectRLSRoutingRecord struct {
	ID        uint `gorm:"primaryKey"`
	ProjectID uint
	Value     string
}

func TestProjectScopeContextRoutingUsesOneTransactionAndRollsBack(t *testing.T) {
	db := openProjectRLSRoutingTestDB(t)
	scope := models.ProjectScope{OrganizationID: 11, ProjectID: 22}
	rollback := errors.New("rollback scoped request")

	err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			record := projectRLSRoutingRecord{
				ProjectID: scope.ProjectID,
				Value:     "created",
			}
			if err := db.WithContext(scopedContext).Create(&record).Error; err != nil {
				return fmt.Errorf("create through root handle: %w", err)
			}

			var queried projectRLSRoutingRecord
			if err := db.WithContext(scopedContext).
				Where("id = ?", record.ID).
				First(&queried).Error; err != nil {
				return fmt.Errorf("query through root handle: %w", err)
			}
			if queried.Value != "created" {
				return fmt.Errorf("query returned value %q", queried.Value)
			}

			var rawValue string
			if err := db.WithContext(scopedContext).
				Raw(
					"SELECT value FROM project_rls_routing_records WHERE id = ?",
					record.ID,
				).
				Scan(&rawValue).Error; err != nil {
				return fmt.Errorf("raw query through root handle: %w", err)
			}
			if rawValue != "created" {
				return fmt.Errorf("raw query returned value %q", rawValue)
			}

			var rowValue string
			if err := db.WithContext(scopedContext).
				Model(&projectRLSRoutingRecord{}).
				Select("value").
				Where("id = ?", record.ID).
				Row().
				Scan(&rowValue); err != nil {
				return fmt.Errorf("row query through root handle: %w", err)
			}
			if rowValue != "created" {
				return fmt.Errorf("row query returned value %q", rowValue)
			}

			if err := db.WithContext(scopedContext).
				Model(&projectRLSRoutingRecord{}).
				Where("id = ?", record.ID).
				Update("value", "updated").Error; err != nil {
				return fmt.Errorf("update through root handle: %w", err)
			}
			if err := db.WithContext(scopedContext).
				Delete(&projectRLSRoutingRecord{}, record.ID).Error; err != nil {
				return fmt.Errorf("delete through root handle: %w", err)
			}
			return rollback
		},
	)
	if !errors.Is(err, rollback) {
		t.Fatalf("scoped transaction error = %v, want rollback sentinel", err)
	}

	var count int64
	if err := db.Model(&projectRLSRoutingRecord{}).Count(&count).Error; err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("scoped request rollback left %d records", count)
	}
}

func TestTransactionForContextReusesOuterProjectTransaction(t *testing.T) {
	db := openProjectRLSRoutingTestDB(t)
	scope := models.ProjectScope{OrganizationID: 31, ProjectID: 41}

	if err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			if err := TransactionForContext(
				scopedContext,
				db,
				func(tx *gorm.DB) error {
					return tx.Create(&projectRLSRoutingRecord{
						ProjectID: scope.ProjectID,
						Value:     "committed",
					}).Error
				},
			); err != nil {
				return err
			}
			var count int64
			return db.WithContext(scopedContext).
				Model(&projectRLSRoutingRecord{}).
				Where("value = ?", "committed").
				Count(&count).
				Error
		},
	); err != nil {
		t.Fatalf("commit scoped transaction: %v", err)
	}

	var count int64
	if err := db.Model(&projectRLSRoutingRecord{}).
		Where("value = ?", "committed").
		Count(&count).Error; err != nil {
		t.Fatalf("count committed record: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed record count = %d, want 1", count)
	}
}

func TestTransactionForContextUsesSavepointForDomainRollback(t *testing.T) {
	db := openProjectRLSRoutingTestDB(t)
	scope := models.ProjectScope{OrganizationID: 42, ProjectID: 52}
	domainFailure := errors.New("domain command rejected")

	if err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			err := TransactionForContext(
				scopedContext,
				db,
				func(tx *gorm.DB) error {
					if err := tx.Create(&projectRLSRoutingRecord{
						ProjectID: scope.ProjectID,
						Value:     "partial-domain-write",
					}).Error; err != nil {
						return err
					}
					return domainFailure
				},
			)
			if !errors.Is(err, domainFailure) {
				return fmt.Errorf(
					"domain transaction error = %v",
					err,
				)
			}
			// This represents a denied PolicyDecision or failed idempotency
			// receipt that must survive the command SAVEPOINT rollback.
			return db.WithContext(scopedContext).
				Create(&projectRLSRoutingRecord{
					ProjectID: scope.ProjectID,
					Value:     "auditable-denial",
				}).Error
		},
	); err != nil {
		t.Fatalf("outer scoped transaction: %v", err)
	}

	var values []string
	if err := db.Model(&projectRLSRoutingRecord{}).
		Order("id").
		Pluck("value", &values).Error; err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0] != "auditable-denial" {
		t.Fatalf("savepoint persisted unexpected values %v", values)
	}
}

func TestProjectScopeRoutingRejectsIndependentNestedTransaction(t *testing.T) {
	db := openProjectRLSRoutingTestDB(t)
	scope := models.ProjectScope{OrganizationID: 51, ProjectID: 61}

	err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			nestedErr := db.WithContext(scopedContext).Transaction(
				func(independent *gorm.DB) error {
					return independent.Create(&projectRLSRoutingRecord{
						ProjectID: scope.ProjectID,
						Value:     "must-not-commit",
					}).Error
				},
			)
			if nestedErr == nil {
				return errors.New("independent nested transaction was accepted")
			}
			if got := nestedErr.Error(); !strings.Contains(
				got,
				"use scopeddb.TransactionForContext",
			) {
				return fmt.Errorf("unexpected nested transaction error: %w", nestedErr)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("outer scoped transaction: %v", err)
	}
}

func TestProjectScopeContextRoutingRejectsNestedScope(t *testing.T) {
	db := openProjectRLSRoutingTestDB(t)
	scope := models.ProjectScope{OrganizationID: 71, ProjectID: 81}

	err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedContext context.Context) error {
			nestedErr := WithProjectScopeContextTransaction(
				scopedContext,
				db,
				scope,
				func(context.Context) error { return nil },
			)
			if nestedErr == nil {
				return errors.New("nested project scope was accepted")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("outer scoped transaction: %v", err)
	}
}

func TestAuthorizedProjectSetValidatesMembershipResultAndRollsBack(
	t *testing.T,
) {
	db := openProjectRLSRoutingTestDB(t)
	if err := db.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		INSERT INTO projects (id, organization_id)
		VALUES (101, 91), (102, 91), (201, 92)
	`).Error; err != nil {
		t.Fatal(err)
	}
	for name, projectIDs := range map[string][]uint{
		"duplicate":          {101, 101},
		"zero":               {0},
		"cross organization": {101, 201},
	} {
		t.Run(name, func(t *testing.T) {
			err := WithAuthorizedProjectSetContextTransaction(
				context.Background(),
				db,
				91,
				projectIDs,
				func(context.Context) error {
					return errors.New("callback must not execute")
				},
			)
			if err == nil {
				t.Fatalf("invalid authorized set %v was accepted", projectIDs)
			}
		})
	}

	rollback := errors.New("rollback authorized project set")
	err := WithAuthorizedProjectSetContextTransaction(
		context.Background(),
		db,
		91,
		[]uint{102, 101},
		func(scopedContext context.Context) error {
			if err := db.WithContext(scopedContext).
				Create(&projectRLSRoutingRecord{
					ProjectID: 101,
					Value:     "cross-project",
				}).Error; err != nil {
				return err
			}
			return rollback
		},
	)
	if !errors.Is(err, rollback) {
		t.Fatalf("authorized set rollback error = %v", err)
	}
	var count int64
	if err := db.Model(&projectRLSRoutingRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("authorized project transaction leaked %d rows", count)
	}
}

func openProjectRLSRoutingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open routing test database: %v", err)
	}
	if err := db.AutoMigrate(&projectRLSRoutingRecord{}); err != nil {
		t.Fatalf("migrate routing test database: %v", err)
	}
	if err := InstallProjectScopeTransactionRouting(db); err != nil {
		t.Fatalf("install project scope routing: %v", err)
	}
	return db
}
