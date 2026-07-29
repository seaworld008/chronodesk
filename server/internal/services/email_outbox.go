package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	// EmailOutboxDestination is the only route allowed to perform business
	// email delivery. Producers persist this target in the same transaction as
	// the state or one-time credential that caused the message.
	EmailOutboxDestination = "email"

	AuthVerificationEmailDestinationPrefix  = "auth-verification:"
	AuthPasswordResetEmailDestinationPrefix = "auth-password-reset:"
	AuthWelcomeEmailDestinationPrefix       = "auth-welcome:"
	NotificationEmailDestinationPrefix      = "notification:"

	emailOutboxMaxAttempts = 8
)

// EmailIntentReference deliberately contains only durable internal record
// references. Recipient addresses, reset/verification tokens, credentials and
// rendered message bodies never belong in a DomainEvent.
type EmailIntentReference struct {
	UserID           uint   `json:"user_id,omitempty"`
	VerificationID   uint   `json:"verification_id,omitempty"`
	PasswordResetID  uint   `json:"password_reset_id,omitempty"`
	NotificationID   uint   `json:"notification_id,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
	RequestReason    string `json:"request_reason,omitempty"`
}

// NotificationEmailEventID is stable so legacy recovery scans and concurrent
// enqueue attempts converge on the same DomainEvent instead of creating
// parallel email intents.
func NotificationEmailEventID(notificationID uint) string {
	return uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte("chronodesk:notification-email:"+strconv.FormatUint(uint64(notificationID), 10)),
	).String()
}

type EmailOutboxEventInput struct {
	ID              string
	Source          string
	Type            string
	Subject         string
	Actor           models.ActorRef
	ResourceVersion uint64
	Data            EmailIntentReference
	DestinationID   string
	AvailableAt     time.Time
}

// AppendEmailOutboxTx appends one private-reference DomainEvent and one email
// Outbox target to an existing business transaction. It uses the canonical
// DomainEvent/Outbox tables rather than introducing a feature-local queue.
func AppendEmailOutboxTx(
	ctx context.Context,
	tx *gorm.DB,
	input EmailOutboxEventInput,
) (*models.DomainEvent, error) {
	if tx == nil {
		return nil, errors.New("email Outbox transaction is required")
	}
	if strings.TrimSpace(input.Type) == "" {
		return nil, errors.New("email DomainEvent type is required")
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("invalid email DomainEvent actor: %w", err)
	}
	input.DestinationID = strings.TrimSpace(input.DestinationID)
	if input.DestinationID == "" || len(input.DestinationID) > 128 {
		return nil, errors.New("email Outbox destination is invalid")
	}
	if input.Data.UserID == 0 &&
		input.Data.VerificationID == 0 &&
		input.Data.PasswordResetID == 0 &&
		input.Data.NotificationID == 0 {
		return nil, errors.New("email intent must reference a durable record")
	}

	now := time.Now().UTC()
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if input.Source == "" {
		input.Source = "urn:chronodesk:email"
	}
	if input.ResourceVersion == 0 {
		input.ResourceVersion = 1
	}
	if input.AvailableAt.IsZero() {
		input.AvailableAt = now
	} else {
		input.AvailableAt = input.AvailableAt.UTC()
	}
	payload, err := json.Marshal(input.Data)
	if err != nil {
		return nil, fmt.Errorf("encode email intent reference: %w", err)
	}

	event := &models.DomainEvent{
		ID:              input.ID,
		SpecVersion:     "1.0",
		Source:          input.Source,
		Type:            input.Type,
		Subject:         input.Subject,
		Time:            now,
		DataContentType: "application/json",
		DataSchema:      "urn:chronodesk:schema:email-intent-reference:v1",
		Data:            datatypes.JSON(payload),
		ActorType:       input.Actor.Type,
		ActorID:         input.Actor.ID,
		ResourceVersion: input.ResourceVersion,
	}
	if err := tx.WithContext(ctx).Create(event).Error; err != nil {
		return nil, fmt.Errorf("create email DomainEvent: %w", err)
	}

	delivery := models.OutboxDelivery{
		ID:              uuid.NewString(),
		EventID:         event.ID,
		DestinationType: EmailOutboxDestination,
		DestinationID:   input.DestinationID,
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     emailOutboxMaxAttempts,
		NextAttemptAt:   input.AvailableAt,
	}
	if err := tx.WithContext(ctx).Create(&delivery).Error; err != nil {
		return nil, fmt.Errorf("create email Outbox delivery: %w", err)
	}
	event.Deliveries = []models.OutboxDelivery{delivery}
	return event, nil
}
