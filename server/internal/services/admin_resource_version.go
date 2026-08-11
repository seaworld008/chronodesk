package services

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const adminResourceVersionKeyPrefix = "agent.resource_version."

// AdminResourceVersionKey is the shared durable version-anchor identity used
// by Human configuration CRUD and administrator commands. Keeping the key
// derivation here prevents a transport from maintaining a second, divergent
// preflight version.
func AdminResourceVersionKey(
	scope models.ProjectScope,
	subject string,
) string {
	scopeSubject := strconv.FormatUint(uint64(scope.OrganizationID), 10) +
		"/" +
		strconv.FormatUint(uint64(scope.ProjectID), 10) +
		"/" +
		strings.TrimSpace(subject)
	sum := sha256.Sum256([]byte(scopeSubject))
	return adminResourceVersionKeyPrefix +
		base64.RawURLEncoding.EncodeToString(sum[:])
}

// AdvanceWebhookAdminResourceVersionTx advances the same version anchor used
// by emergency-revoke If-Match. Callers must execute this in the transaction
// that mutates or soft-deletes the WebhookConfig row.
func AdvanceWebhookAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
	updatedBy uint,
) (uint64, error) {
	if ctx == nil || tx == nil || configID == 0 {
		return 0, errors.New("webhook resource version transaction is required")
	}
	if err := scope.Validate(); err != nil {
		return 0, fmt.Errorf("webhook resource version scope: %w", err)
	}
	subject := WebhookAdminSubject(configID)
	var eventVersion uint64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("COALESCE(MAX(resource_version), 0)").
		Where(
			"organization_id = ? AND project_id = ? AND subject = ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			subject,
			"io.chronodesk.admin.%",
		).
		Scan(&eventVersion).Error; err != nil {
		return 0, fmt.Errorf(
			"load Webhook administrator resource version: %w",
			err,
		)
	}
	if eventVersion == 0 {
		eventVersion = 1
	}
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	row := models.SystemConfig{
		Key:          AdminResourceVersionKey(scope, subject),
		Value:        subject,
		ValueType:    "string",
		Description:  "Administrator command resource version",
		Category:     "security",
		Group:        "agent-resource-version",
		IsActive:     true,
		DefaultValue: subject,
		UpdatedBy:    userID,
		Version:      int(eventVersion),
	}
	if err := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error; err != nil {
		return 0, fmt.Errorf(
			"initialize Webhook administrator resource version: %w",
			err,
		)
	}
	now := time.Now().UTC()
	update := tx.WithContext(ctx).
		Model(&models.SystemConfig{}).
		Where("key = ?", row.Key).
		Updates(map[string]any{
			"version":    gorm.Expr("version + 1"),
			"updated_at": now,
			"updated_by": userID,
		})
	if update.Error != nil {
		return 0, fmt.Errorf(
			"advance Webhook administrator resource version: %w",
			update.Error,
		)
	}
	if update.RowsAffected != 1 {
		return 0, errors.New(
			"webhook administrator resource version anchor was not advanced",
		)
	}
	var current models.SystemConfig
	if err := tx.WithContext(ctx).
		Select("version").
		First(&current, "key = ?", row.Key).Error; err != nil {
		return 0, fmt.Errorf(
			"reload Webhook administrator resource version: %w",
			err,
		)
	}
	if current.Version <= 1 {
		return 0, errors.New(
			"webhook administrator resource version did not advance",
		)
	}
	return uint64(current.Version), nil
}
