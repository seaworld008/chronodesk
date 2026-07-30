package scopeddb

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type transactionScopeTestRecord struct {
	ID             uint `gorm:"primaryKey"`
	OrganizationID uint
	ProjectID      uint
	Value          string
}

var transactionScopeTestDBSequence atomic.Uint64

func TestInstallIsConcurrentSafeAndIdempotentAcrossDatabases(t *testing.T) {
	databases := []*gorm.DB{
		openUninstalledTransactionScopeTestDB(t),
		openUninstalledTransactionScopeTestDB(t),
	}

	const installersPerDatabase = 16
	start := make(chan struct{})
	errs := make(chan error, len(databases)*installersPerDatabase)
	var installers sync.WaitGroup
	for _, db := range databases {
		for index := 0; index < installersPerDatabase; index++ {
			handle := db
			if index%2 != 0 {
				// GORM sessions shallow-copy Config: the clone has a distinct
				// Config pointer but shares the plugin map and callbacks.
				handle = db.Session(&gorm.Session{})
			}
			installers.Add(1)
			go func() {
				defer installers.Done()
				<-start
				errs <- Install(handle)
			}()
		}
	}

	close(start)
	installers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent install: %v", err)
		}
	}

	for _, db := range databases {
		if _, installed := db.Plugins[pluginName]; !installed {
			t.Fatal("routing plugin was not installed")
		}
		for operation, callback := range map[string]func(*gorm.DB){
			"create": db.Callback().Create().Get(pluginName + ":create"),
			"update": db.Callback().Update().Get(pluginName + ":update"),
			"delete": db.Callback().Delete().Get(pluginName + ":delete"),
			"query":  db.Callback().Query().Get(pluginName + ":query"),
			"row":    db.Callback().Row().Get(pluginName + ":row"),
			"raw":    db.Callback().Raw().Get(pluginName + ":raw"),
		} {
			if callback == nil {
				t.Fatalf("%s routing callback was not installed", operation)
			}
		}
	}
}

func TestCanReuseProjectScopeTransactionRequiresExactSingleProject(
	t *testing.T,
) {
	db := openTransactionScopeTestDB(t)
	scope := models.ProjectScope{OrganizationID: 7, ProjectID: 11}

	reusable, err := CanReuseProjectScopeTransaction(
		context.Background(),
		scope,
	)
	if err != nil {
		t.Fatalf("unbound context: %v", err)
	}
	if reusable {
		t.Fatal("unbound context unexpectedly reported a reusable transaction")
	}

	if err := WithProjectScopeContextTransaction(
		context.Background(),
		db,
		scope,
		func(scopedCtx context.Context) error {
			reusable, reuseErr := CanReuseProjectScopeTransaction(
				scopedCtx,
				scope,
			)
			if reuseErr != nil {
				return reuseErr
			}
			if !reusable {
				t.Fatal("exact single-project transaction was not reusable")
			}

			for name, mismatched := range map[string]models.ProjectScope{
				"different project": {
					OrganizationID: scope.OrganizationID,
					ProjectID:      scope.ProjectID + 1,
				},
				"different organization": {
					OrganizationID: scope.OrganizationID + 1,
					ProjectID:      scope.ProjectID,
				},
			} {
				t.Run(name, func(t *testing.T) {
					matched, mismatchErr := CanReuseProjectScopeTransaction(
						scopedCtx,
						mismatched,
					)
					if mismatchErr == nil {
						t.Fatal("mismatched transaction binding was accepted")
					}
					if matched {
						t.Fatal("mismatched transaction reported reusable")
					}
				})
			}
			return nil
		},
	); err != nil {
		t.Fatalf("single-project transaction: %v", err)
	}
}

func TestCanReuseProjectScopeTransactionRejectsAuthorizedProjectSets(
	t *testing.T,
) {
	db := openTransactionScopeTestDB(t)
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
		VALUES (11, 7), (12, 7)
	`).Error; err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		projectIDs []uint
	}{
		{name: "empty authorized set", projectIDs: nil},
		{name: "multi-project authorized set", projectIDs: []uint{11, 12}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := WithAuthorizedProjectScopeTransaction(
				context.Background(),
				db,
				7,
				test.projectIDs,
				func(scopedCtx context.Context) error {
					reusable, reuseErr := CanReuseProjectScopeTransaction(
						scopedCtx,
						models.ProjectScope{
							OrganizationID: 7,
							ProjectID:      11,
						},
					)
					if reuseErr == nil {
						t.Fatal("authorized project-set transaction was accepted")
					}
					if reusable {
						t.Fatal("authorized project set reported reusable")
					}
					if !strings.Contains(
						reuseErr.Error(),
						"does not exactly match",
					) {
						t.Fatalf("unexpected mismatch error: %v", reuseErr)
					}
					return nil
				},
			)
			if err != nil {
				t.Fatalf("authorized transaction: %v", err)
			}
		})
	}
}

func openTransactionScopeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openUninstalledTransactionScopeTestDB(t)
	if err := Install(db); err != nil {
		t.Fatalf("install transaction routing: %v", err)
	}
	return db
}

func openUninstalledTransactionScopeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	databaseID := transactionScopeTestDBSequence.Add(1)
	db, err := gorm.Open(
		sqlite.Open(
			"file:transaction-scope-test-"+
				strconv.FormatUint(databaseID, 10)+
				"?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open transaction scope test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open SQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close transaction scope test database: %v", err)
		}
	})
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&transactionScopeTestRecord{}); err != nil {
		t.Fatalf("migrate transaction scope test database: %v", err)
	}
	return db
}
