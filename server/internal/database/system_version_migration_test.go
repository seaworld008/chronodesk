package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateSystemVersionCreatesUpgradesAndRerunsIdempotently(
	t *testing.T,
) {
	tests := []struct {
		name        string
		seed        *models.SystemConfig
		wantVersion int
	}{
		{
			name:        "new database",
			wantVersion: 1,
		},
		{
			name: "legacy release",
			seed: &models.SystemConfig{
				Key:          models.SystemConfigKeySystemVersion,
				Value:        "0.1.0",
				ValueType:    "string",
				Description:  "系统版本",
				Category:     "system",
				Group:        "basic",
				IsRequired:   true,
				IsActive:     true,
				DefaultValue: "0.1.0",
				Version:      7,
			},
			wantVersion: 8,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(
				sqlite.Open(
					"file:system-version-"+
						strings.ReplaceAll(test.name, " ", "-")+
						"?mode=memory&cache=shared",
				),
				&gorm.Config{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
				t.Fatalf("migrate system config table: %v", err)
			}
			if test.seed != nil {
				updatedBy := uint(42)
				test.seed.UpdatedBy = &updatedBy
				if err := db.Create(test.seed).Error; err != nil {
					t.Fatalf("seed legacy system version: %v", err)
				}
			}

			if err := migrateSystemVersion(db, "0.2.0"); err != nil {
				t.Fatalf("migrate system version: %v", err)
			}
			var first models.SystemConfig
			if err := db.Where(
				"key = ?",
				models.SystemConfigKeySystemVersion,
			).First(&first).Error; err != nil {
				t.Fatalf("load migrated system version: %v", err)
			}
			assertCanonicalSystemVersion(t, first, "0.2.0", test.wantVersion)

			firstUpdatedAt := first.UpdatedAt
			if err := migrateSystemVersion(db, "0.2.0"); err != nil {
				t.Fatalf("rerun system version migration: %v", err)
			}
			var rerun models.SystemConfig
			if err := db.First(&rerun, first.ID).Error; err != nil {
				t.Fatalf("reload rerun system version: %v", err)
			}
			assertCanonicalSystemVersion(t, rerun, "0.2.0", test.wantVersion)
			if !rerun.UpdatedAt.Equal(firstUpdatedAt) {
				t.Fatalf(
					"idempotent rerun changed updated_at from %s to %s",
					firstUpdatedAt,
					rerun.UpdatedAt,
				)
			}
		})
	}
}

func TestMigrateSystemVersionPropagatesErrorsAndRollsBack(t *testing.T) {
	t.Run("missing table", func(t *testing.T) {
		db, err := gorm.Open(
			sqlite.Open("file:system-version-missing?mode=memory&cache=shared"),
			&gorm.Config{},
		)
		if err != nil {
			t.Fatal(err)
		}
		err = migrateSystemVersion(db, "0.2.0")
		if err == nil || !strings.Contains(err.Error(), "system.version") {
			t.Fatalf("migration error = %v, want contextual persistence error", err)
		}
	})

	t.Run("outer migration transaction rollback", func(t *testing.T) {
		db, err := gorm.Open(
			sqlite.Open("file:system-version-rollback?mode=memory&cache=shared"),
			&gorm.Config{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
			t.Fatal(err)
		}
		legacy := models.SystemConfig{
			Key:          models.SystemConfigKeySystemVersion,
			Value:        "0.1.0",
			ValueType:    "string",
			Description:  "系统版本",
			Category:     "system",
			Group:        "basic",
			IsRequired:   true,
			IsActive:     true,
			DefaultValue: "0.1.0",
			Version:      3,
		}
		if err := db.Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}

		injected := errors.New("injected migration failure")
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := migrateSystemVersion(tx, "0.2.0"); err != nil {
				return err
			}
			return injected
		})
		if !errors.Is(err, injected) {
			t.Fatalf("transaction error = %v, want injected error", err)
		}
		var persisted models.SystemConfig
		if err := db.First(&persisted, legacy.ID).Error; err != nil {
			t.Fatal(err)
		}
		assertCanonicalSystemVersion(t, persisted, "0.1.0", 3)
	})
}

func TestMigrateSystemVersionRepairsMetadataWithoutChangingIdentityVersion(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open("file:system-version-metadata?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	minimum := 1
	maximum := 9
	updatedBy := uint(42)
	oldUpdatedAt := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	metadataDrift := models.SystemConfig{
		Key:          models.SystemConfigKeySystemVersion,
		Value:        "0.2.0",
		ValueType:    "int",
		Description:  "editable runtime override",
		Category:     "security",
		Group:        "runtime",
		IsRequired:   false,
		IsActive:     false,
		DefaultValue: "0.1.0",
		MinValue:     &minimum,
		MaxValue:     &maximum,
		ValidValues:  `["0.1.0","0.2.0"]`,
		UpdatedBy:    &updatedBy,
		Version:      11,
		UpdatedAt:    oldUpdatedAt,
	}
	if err := db.Create(&metadataDrift).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrateSystemVersion(db, "0.2.0"); err != nil {
		t.Fatalf("repair metadata: %v", err)
	}
	var repaired models.SystemConfig
	if err := db.First(&repaired, metadataDrift.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCanonicalSystemVersion(t, repaired, "0.2.0", 11)
	if !repaired.UpdatedAt.After(oldUpdatedAt) {
		t.Fatalf(
			"metadata repair updated_at = %s, want after %s",
			repaired.UpdatedAt,
			oldUpdatedAt,
		)
	}

	repairedAt := repaired.UpdatedAt
	if err := migrateSystemVersion(db, "0.2.0"); err != nil {
		t.Fatalf("idempotent metadata rerun: %v", err)
	}
	if err := db.First(&repaired, metadataDrift.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertCanonicalSystemVersion(t, repaired, "0.2.0", 11)
	if !repaired.UpdatedAt.Equal(repairedAt) {
		t.Fatalf(
			"canonical rerun changed updated_at from %s to %s",
			repairedAt,
			repaired.UpdatedAt,
		)
	}
}

func TestMigrateSystemVersionNormalizesCorruptIdentityVersions(t *testing.T) {
	tests := []struct {
		name           string
		value          string
		persistVersion int
	}{
		{
			name:           "canonical identity with zero version",
			value:          "0.2.0",
			persistVersion: 0,
		},
		{
			name:           "canonical identity with negative version",
			value:          "0.2.0",
			persistVersion: -3,
		},
		{
			name:           "stale identity with zero version",
			value:          "0.1.0",
			persistVersion: 0,
		},
		{
			name:           "stale identity with negative version",
			value:          "0.1.0",
			persistVersion: -3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(
				sqlite.Open(
					"file:system-version-corrupt-version-"+
						strings.ReplaceAll(test.name, " ", "-")+
						"?mode=memory&cache=shared",
				),
				&gorm.Config{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
				t.Fatal(err)
			}
			corrupt := models.DefaultSystemVersionConfig(test.value)
			corrupt.Version = test.persistVersion
			if err := db.Create(&corrupt).Error; err != nil {
				t.Fatal(err)
			}
			// GORM applies the model default when the Go value is zero. Force
			// the persisted corruption after creation so the migration sees
			// the same invalid row that an old/manual database can contain.
			if test.persistVersion == 0 {
				if err := db.Model(&models.SystemConfig{}).
					Where("id = ?", corrupt.ID).
					UpdateColumn("version", 0).Error; err != nil {
					t.Fatal(err)
				}
			}

			if err := migrateSystemVersion(db, "0.2.0"); err != nil {
				t.Fatalf("normalize corrupt identity version: %v", err)
			}
			var repaired models.SystemConfig
			if err := db.First(&repaired, corrupt.ID).Error; err != nil {
				t.Fatal(err)
			}
			assertCanonicalSystemVersion(t, repaired, "0.2.0", 1)
		})
	}
}

func TestMigrateSystemVersionFailsClosedWhenLockedTargetMutatesBeforeUpdate(
	t *testing.T,
) {
	for _, mutation := range []string{"delete", "rekey"} {
		t.Run(mutation, func(t *testing.T) {
			db, err := gorm.Open(
				sqlite.Open(
					"file:system-version-update-race-"+
						mutation+
						"?mode=memory&cache=shared",
				),
				&gorm.Config{},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
				t.Fatal(err)
			}
			legacy := models.DefaultSystemVersionConfig("0.1.0")
			legacy.Version = 4
			if err := db.Create(&legacy).Error; err != nil {
				t.Fatal(err)
			}

			callbackName := "test:mutate-system-version-before-update:" + mutation
			if err := db.Callback().Update().Before("gorm:update").Register(
				callbackName,
				func(tx *gorm.DB) {
					mutator := tx.Session(&gorm.Session{NewDB: true})
					var mutationErr error
					switch mutation {
					case "delete":
						mutationErr = mutator.Exec(
							"DELETE FROM system_configs WHERE id = ?",
							legacy.ID,
						).Error
					case "rekey":
						mutationErr = mutator.Exec(
							"UPDATE system_configs SET key = ? WHERE id = ?",
							"system.version.rekeyed",
							legacy.ID,
						).Error
					default:
						mutationErr = fmt.Errorf(
							"unsupported test mutation %q",
							mutation,
						)
					}
					if mutationErr != nil {
						tx.AddError(mutationErr)
					}
				},
			); err != nil {
				t.Fatalf("register mutation callback: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Callback().Update().Remove(callbackName); err != nil {
					t.Errorf("remove mutation callback: %v", err)
				}
			})

			err = db.Transaction(func(tx *gorm.DB) error {
				return migrateSystemVersion(tx, "0.2.0")
			})
			if err == nil ||
				!strings.Contains(err.Error(), "system.version") ||
				!strings.Contains(err.Error(), "affected") {
				t.Fatalf(
					"migration error = %v, want stable zero-row fail-closed error",
					err,
				)
			}

			var persisted models.SystemConfig
			if err := db.First(&persisted, legacy.ID).Error; err != nil {
				t.Fatalf("load rolled-back system.version: %v", err)
			}
			assertCanonicalSystemVersion(t, persisted, "0.1.0", 4)
		})
	}
}

func TestPostgresMigrateSystemVersionLocksCanonicalRow(t *testing.T) {
	db, _, _ := openPostgresMembershipReleaseTestDB(t, "sysverlock")
	if err := db.AutoMigrate(&models.SystemConfig{}); err != nil {
		t.Fatalf("migrate PostgreSQL system config: %v", err)
	}
	canonical := models.DefaultSystemVersionConfig("0.2.0")
	if err := db.Create(&canonical).Error; err != nil {
		t.Fatalf("seed PostgreSQL system.version: %v", err)
	}

	locked := db.Begin()
	if locked.Error != nil {
		t.Fatalf("begin locking transaction: %v", locked.Error)
	}
	defer locked.Rollback()
	if err := migrateSystemVersion(locked, "0.2.0"); err != nil {
		t.Fatalf("lock canonical system.version: %v", err)
	}

	contender := db.Begin()
	if contender.Error != nil {
		t.Fatalf("begin contender transaction: %v", contender.Error)
	}
	defer contender.Rollback()
	if err := contender.Exec(`SET LOCAL lock_timeout = '100ms'`).Error; err != nil {
		t.Fatalf("set contender lock timeout: %v", err)
	}
	err := contender.Model(&models.SystemConfig{}).
		Where("id = ?", canonical.ID).
		UpdateColumn("description", "concurrent mutation").Error
	if err == nil {
		t.Fatal("concurrent PostgreSQL update bypassed system.version row lock")
	}
}

func assertCanonicalSystemVersion(
	t *testing.T,
	config models.SystemConfig,
	wantValue string,
	wantVersion int,
) {
	t.Helper()
	if config.Key != models.SystemConfigKeySystemVersion ||
		config.Value != wantValue ||
		config.ValueType != "string" ||
		config.Category != "system" ||
		config.Group != "basic" ||
		!config.IsRequired ||
		!config.IsActive ||
		config.DefaultValue != wantValue ||
		config.MinValue != nil ||
		config.MaxValue != nil ||
		config.ValidValues != "" ||
		config.UpdatedBy != nil ||
		config.Version != wantVersion {
		t.Fatalf("system.version = %+v", config)
	}
}
