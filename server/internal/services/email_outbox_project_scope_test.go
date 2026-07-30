package services

import (
	"context"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

func TestAppendEmailOutboxTxRequiresTrustedProjectScopedTransaction(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	actor := models.SystemActor("email-outbox-scope-test")
	projectCtx := testProjectOperationContext(t, db, actor)
	operation, err := OperationContextFromContext(projectCtx)
	if err != nil {
		t.Fatal(err)
	}
	validInput := EmailOutboxEventInput{
		Scope:         operation.Scope,
		Type:          "io.chronodesk.email.scope.test.v1",
		Subject:       "user/1",
		Actor:         actor,
		Data:          EmailIntentReference{UserID: 1},
		DestinationID: AuthWelcomeEmailDestinationPrefix + "1",
	}

	assertRejected := func(
		name string,
		expected string,
		run func() error,
	) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("AppendEmailOutboxTx() error = %v, want %q", err, expected)
			}
			var eventCount, deliveryCount int64
			if err := db.Model(&models.DomainEvent{}).Count(&eventCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.OutboxDelivery{}).
				Count(&deliveryCount).Error; err != nil {
				t.Fatal(err)
			}
			if eventCount != 0 || deliveryCount != 0 {
				t.Fatalf(
					"rejected email intent persisted rows: events=%d deliveries=%d",
					eventCount,
					deliveryCount,
				)
			}
		})
	}

	assertRejected(
		"zero scope",
		"project scope is required",
		func() error {
			input := validInput
			input.Scope = models.ProjectScope{}
			return db.Transaction(func(tx *gorm.DB) error {
				_, err := AppendEmailOutboxTx(projectCtx, tx, input)
				return err
			})
		},
	)
	assertRejected(
		"missing trusted operation",
		"trusted operation context is required",
		func() error {
			return scopeddb.WithProjectScopeContextTransaction(
				context.Background(),
				db,
				operation.Scope,
				func(scopedCtx context.Context) error {
					_, err := AppendEmailOutboxTx(
						scopedCtx,
						db.WithContext(scopedCtx),
						validInput,
					)
					return err
				},
			)
		},
	)
	assertRejected(
		"missing scoped transaction",
		"active project-scoped transaction",
		func() error {
			return db.WithContext(projectCtx).Transaction(func(tx *gorm.DB) error {
				_, err := AppendEmailOutboxTx(projectCtx, tx, validInput)
				return err
			})
		},
	)
	assertRejected(
		"scope mismatch",
		"does not match trusted operation context",
		func() error {
			input := validInput
			input.Scope.ProjectID++
			return scopeddb.WithProjectScopeContextTransaction(
				projectCtx,
				db,
				operation.Scope,
				func(scopedCtx context.Context) error {
					_, err := AppendEmailOutboxTx(
						scopedCtx,
						db.WithContext(scopedCtx),
						input,
					)
					return err
				},
			)
		},
	)

	err = scopeddb.WithProjectScopeContextTransaction(
		projectCtx,
		db,
		operation.Scope,
		func(scopedCtx context.Context) error {
			_, appendErr := AppendEmailOutboxTx(
				scopedCtx,
				db.WithContext(scopedCtx),
				validInput,
			)
			return appendErr
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var event models.DomainEvent
	if err := db.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if event.OrganizationID != operation.Scope.OrganizationID ||
		event.ProjectID != operation.Scope.ProjectID ||
		delivery.OrganizationID != operation.Scope.OrganizationID ||
		delivery.ProjectID != operation.Scope.ProjectID ||
		delivery.EventID != event.ID {
		t.Fatalf(
			"email event/delivery scope mismatch: event=%+v delivery=%+v",
			event,
			delivery,
		)
	}
}
