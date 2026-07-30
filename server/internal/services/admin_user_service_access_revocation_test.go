package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type persistingAdminUserAccessEventAppender struct {
	sequence atomic.Uint64
	fail     error
}

func (appender *persistingAdminUserAccessEventAppender) AppendDomainEventTx(
	ctx context.Context,
	tx *gorm.DB,
	input DomainEventInput,
	targets []OutboxTarget,
) (*models.DomainEvent, error) {
	if appender.fail != nil {
		return nil, appender.fail
	}
	payload, err := json.Marshal(input.Data)
	if err != nil {
		return nil, err
	}
	event := &models.DomainEvent{
		ID:              fmt.Sprintf("access-event-%d", appender.sequence.Add(1)),
		OrganizationID:  input.Scope.OrganizationID,
		ProjectID:       input.Scope.ProjectID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test",
		Type:            input.Type,
		Subject:         input.Subject,
		Time:            time.Now().UTC(),
		DataContentType: "application/json",
		Data:            datatypes.JSON(payload),
		ActorType:       input.Actor.Type,
		ActorID:         input.Actor.ID,
		ResourceVersion: 1,
	}
	if err := tx.WithContext(ctx).Create(event).Error; err != nil {
		return nil, err
	}
	for index, target := range targets {
		delivery := &models.OutboxDelivery{
			ID:              fmt.Sprintf("%s-delivery-%d", event.ID, index),
			OrganizationID:  event.OrganizationID,
			ProjectID:       event.ProjectID,
			EventID:         event.ID,
			DestinationType: target.Type,
			DestinationID:   target.ID,
			Status:          models.OutboxDeliveryPending,
			MaxAttempts:     target.MaxAttempts,
			NextAttemptAt:   time.Now().UTC(),
		}
		if err := tx.WithContext(ctx).Create(delivery).Error; err != nil {
			return nil, err
		}
	}
	return event, nil
}

func TestAdminUserStatusRevocationCommitsEventAndOutboxAtomically(
	t *testing.T,
) {
	db := openAdminUserAccessRevocationTestDB(t)
	user := seedAdminUserAccessRevocationUser(t, db)
	service, err := NewAdminUserServiceWithAccessRevocationOutbox(
		db,
		&persistingAdminUserAccessEventAppender{},
	)
	if err != nil {
		t.Fatal(err)
	}

	suspended := models.UserStatusSuspended
	updated, err := service.UpdateUser(
		context.Background(),
		user.ID,
		&models.UserUpdateRequest{Status: &suspended},
	)
	if err != nil {
		t.Fatalf("suspend user with revocation Outbox: %v", err)
	}
	if updated.Status != suspended {
		t.Fatalf("updated user status = %q, want %q", updated.Status, suspended)
	}

	var event models.DomainEvent
	if err := db.Where(
		"type = ?",
		UserAccessRevokedEventType,
	).First(&event).Error; err != nil {
		t.Fatalf("load access-revoked event: %v", err)
	}
	var data struct {
		UserID uint              `json:"user_id"`
		Status models.UserStatus `json:"status"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.UserID != user.ID || data.Status != suspended {
		t.Fatalf("access-revoked event data = %+v", data)
	}
	if event.OrganizationID != 7 || event.ProjectID != 70 {
		t.Fatalf(
			"access-revoked control scope = %d/%d, want 7/70",
			event.OrganizationID,
			event.ProjectID,
		)
	}
	var delivery models.OutboxDelivery
	if err := db.Where("event_id = ?", event.ID).
		First(&delivery).Error; err != nil {
		t.Fatalf("load access-revocation delivery: %v", err)
	}
	if delivery.DestinationType != "event_stream" ||
		delivery.DestinationID != adminUserAccessEventDestinationID ||
		delivery.Status != models.OutboxDeliveryPending {
		t.Fatalf("access-revocation delivery = %+v", delivery)
	}
}

func TestAdminUserRevocationRollsBackWhenOutboxAppendFails(t *testing.T) {
	db := openAdminUserAccessRevocationTestDB(t)
	user := seedAdminUserAccessRevocationUser(t, db)
	appendFailure := errors.New("outbox unavailable")
	service, err := NewAdminUserServiceWithAccessRevocationOutbox(
		db,
		&persistingAdminUserAccessEventAppender{fail: appendFailure},
	)
	if err != nil {
		t.Fatal(err)
	}

	suspended := models.UserStatusSuspended
	if _, err := service.UpdateUser(
		context.Background(),
		user.ID,
		&models.UserUpdateRequest{Status: &suspended},
	); !errors.Is(err, appendFailure) {
		t.Fatalf("revocation append error = %v, want %v", err, appendFailure)
	}
	var persisted models.User
	if err := db.First(&persisted, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.UserStatusActive {
		t.Fatalf(
			"failed Outbox append committed user status %q",
			persisted.Status,
		)
	}
	var eventCount int64
	if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("failed command committed %d event(s)", eventCount)
	}
}

func TestAdminUserDeleteCommitsAccessRevocationOutbox(t *testing.T) {
	db := openAdminUserAccessRevocationTestDB(t)
	user := seedAdminUserAccessRevocationUser(t, db)
	service, err := NewAdminUserServiceWithAccessRevocationOutbox(
		db,
		&persistingAdminUserAccessEventAppender{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteUser(context.Background(), user.ID); err != nil {
		t.Fatalf("delete user with access-revocation Outbox: %v", err)
	}

	var event models.DomainEvent
	if err := db.Where(
		"type = ?",
		UserAccessRevokedEventType,
	).First(&event).Error; err != nil {
		t.Fatalf("load delete access-revoked event: %v", err)
	}
	var data struct {
		UserID uint              `json:"user_id"`
		Status models.UserStatus `json:"status"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.UserID != user.ID || data.Status != models.UserStatusDeleted {
		t.Fatalf("delete access-revoked event data = %+v", data)
	}
	var deliveryCount int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where(
			"event_id = ? AND destination_type = ?",
			event.ID,
			"event_stream",
		).
		Count(&deliveryCount).Error; err != nil {
		t.Fatal(err)
	}
	if deliveryCount != 1 {
		t.Fatalf(
			"delete access-revocation deliveries = %d, want 1",
			deliveryCount,
		)
	}
}

var adminUserAccessRevocationDBSequence atomic.Uint64

func openAdminUserAccessRevocationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:admin-user-access-revocation-%d?mode=memory&cache=shared",
			adminUserAccessRevocationDBSequence.Add(1),
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.LoginHistory{},
		&models.OTPTrustedDevice{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE refresh_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL,
			revoked BOOLEAN NOT NULL DEFAULT FALSE,
			revoked_at DATETIME
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			organization_id INTEGER NOT NULL,
			key TEXT NOT NULL,
			status TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO projects (id, organization_id, key, status) VALUES (?, ?, ?, ?)",
		70,
		7,
		models.ProjectKey("DEFAULT"),
		models.ProjectStatusActive,
	).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func seedAdminUserAccessRevocationUser(
	t *testing.T,
	db *gorm.DB,
) models.User {
	t.Helper()
	user := models.User{
		Username:     "revoked-user",
		Email:        "revoked-user@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}
