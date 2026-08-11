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

// WebhookAdminVersionConflictError reports the durable version observed after
// a failed WebhookConfig compare-and-swap.
type WebhookAdminVersionConflictError struct {
	Expected uint64
	Current  uint64
}

func (err *WebhookAdminVersionConflictError) Error() string {
	return fmt.Sprintf(
		"%s: expected %d, actual %d",
		ErrVersionConflict,
		err.Expected,
		err.Current,
	)
}

func (err *WebhookAdminVersionConflictError) Unwrap() error {
	return ErrVersionConflict
}

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

// CurrentWebhookAdminResourceVersionTx returns the persisted Webhook
// configuration generation. A missing anchor falls back to immutable
// administrator events and then to generation one.
func CurrentWebhookAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
) (uint64, error) {
	if ctx == nil || tx == nil || configID == 0 {
		return 0, errors.New("webhook resource version transaction is required")
	}
	if err := scope.Validate(); err != nil {
		return 0, fmt.Errorf("webhook resource version scope: %w", err)
	}
	subject := WebhookAdminSubject(configID)
	var row models.SystemConfig
	err := tx.WithContext(ctx).
		Select("version").
		First(
			&row,
			"key = ?",
			AdminResourceVersionKey(scope, subject),
		).Error
	if err == nil {
		if row.Version <= 0 {
			return 1, nil
		}
		return uint64(row.Version), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf(
			"load Webhook administrator resource version: %w",
			err,
		)
	}
	var eventVersion uint64
	if err = tx.WithContext(ctx).
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
	return eventVersion, nil
}

// CompareAndSwapWebhookAdminResourceVersionTx advances the WebhookConfig
// generation iff expected is still current. It is the common serialization
// point for ordinary edits, ordinary soft-deletes, and emergency revoke.
func CompareAndSwapWebhookAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	configID uint,
	expected uint64,
	updatedBy uint,
) (uint64, error) {
	if expected == 0 {
		return 0, &WebhookAdminVersionConflictError{
			Expected: expected,
			Current:  1,
		}
	}
	if ctx == nil || tx == nil || configID == 0 {
		return 0, errors.New("webhook resource version transaction is required")
	}
	if err := scope.Validate(); err != nil {
		return 0, fmt.Errorf("webhook resource version scope: %w", err)
	}
	subject := WebhookAdminSubject(configID)
	eventVersion, err := CurrentWebhookAdminResourceVersionTx(
		ctx,
		tx,
		scope,
		configID,
	)
	if err != nil {
		return 0, err
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
	var anchor models.SystemConfig
	anchorErr := tx.WithContext(ctx).
		Select("id").
		First(&anchor, "key = ?", row.Key).Error
	if errors.Is(anchorErr, gorm.ErrRecordNotFound) {
		if err := tx.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&row).Error; err != nil {
			return 0, fmt.Errorf(
				"initialize Webhook administrator resource version: %w",
				err,
			)
		}
	} else if anchorErr != nil {
		return 0, fmt.Errorf(
			"inspect Webhook administrator resource version anchor: %w",
			anchorErr,
		)
	}
	now := time.Now().UTC()
	update := tx.WithContext(ctx).
		Model(&models.SystemConfig{}).
		Where("key = ? AND version = ?", row.Key, expected).
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
		current, currentErr := CurrentWebhookAdminResourceVersionTx(
			ctx,
			tx,
			scope,
			configID,
		)
		if currentErr != nil {
			return 0, currentErr
		}
		return 0, &WebhookAdminVersionConflictError{
			Expected: expected,
			Current:  current,
		}
	}
	return expected + 1, nil
}
