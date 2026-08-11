package database

import (
	"errors"
	"strings"
	"testing"

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
		config.UpdatedBy != nil ||
		config.Version != wantVersion {
		t.Fatalf("system.version = %+v", config)
	}
}
