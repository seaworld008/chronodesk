package database

import (
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// migrateSystemVersion reconciles the persisted informational version with the
// immutable build identity. Runtime adapters continue to report version.Version;
// this row is migration-owned and cannot override the running binary.
func migrateSystemVersion(db *gorm.DB, buildVersion string) error {
	if db == nil {
		return errors.New("migrate system.version: database is required")
	}
	if buildVersion == "" || strings.TrimSpace(buildVersion) != buildVersion {
		return errors.New("migrate system.version: build version is invalid")
	}

	canonical := models.DefaultSystemVersionConfig(buildVersion)
	var persisted models.SystemConfig
	read := db
	if db.Dialector.Name() == "postgres" {
		read = read.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := read.Where(
		"key = ?",
		models.SystemConfigKeySystemVersion,
	).First(&persisted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := db.Create(&canonical).Error; err != nil {
			return fmt.Errorf("create system.version: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read system.version: %w", err)
	}

	nextVersion := persisted.Version
	if nextVersion < 1 {
		nextVersion = 1
	}
	identityChanged := persisted.Value != canonical.Value
	if identityChanged && persisted.Version >= 1 {
		nextVersion = persisted.Version + 1
	}
	metadataChanged := persisted.ValueType != canonical.ValueType ||
		persisted.Description != canonical.Description ||
		persisted.Category != canonical.Category ||
		persisted.Group != canonical.Group ||
		persisted.IsRequired != canonical.IsRequired ||
		persisted.IsActive != canonical.IsActive ||
		persisted.DefaultValue != canonical.DefaultValue ||
		persisted.MinValue != nil ||
		persisted.MaxValue != nil ||
		persisted.ValidValues != canonical.ValidValues ||
		persisted.UpdatedBy != nil ||
		persisted.Version != nextVersion
	if !identityChanged && !metadataChanged {
		return nil
	}

	result := db.Model(&models.SystemConfig{}).
		Where(
			"id = ? AND key = ?",
			persisted.ID,
			models.SystemConfigKeySystemVersion,
		).
		Updates(map[string]any{
			"value":         canonical.Value,
			"value_type":    canonical.ValueType,
			"description":   canonical.Description,
			"category":      canonical.Category,
			"group":         canonical.Group,
			"is_required":   canonical.IsRequired,
			"is_active":     canonical.IsActive,
			"default_value": canonical.DefaultValue,
			"min_value":     nil,
			"max_value":     nil,
			"valid_values":  canonical.ValidValues,
			"updated_by":    nil,
			"version":       nextVersion,
		})
	if result.Error != nil {
		return fmt.Errorf("update system.version: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf(
			"update system.version affected %d rows, want 1",
			result.RowsAffected,
		)
	}
	return nil
}
