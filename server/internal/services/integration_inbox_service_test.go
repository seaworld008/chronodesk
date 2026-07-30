package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type integrationDomainWrite struct {
	ID      uint   `gorm:"primaryKey"`
	Message string `gorm:"size:191;not null"`
}

type integrationInboxFixture struct {
	db         *gorm.DB
	now        time.Time
	scope      models.ProjectScope
	project    models.Project
	definition models.ConnectorDefinition
	connection models.Connection
	mapping    models.MappingVersion
}

func TestIntegrationInboxDeduplicatesAndReturnsExistingReceipt(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var verificationCalls atomic.Int32
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		IntegrationSignatureVerifierFunc(func(
			_ context.Context,
			input IntegrationSignatureVerification,
		) error {
			verificationCalls.Add(1)
			if input.Signature != "valid-signature" ||
				!bytes.Equal(input.Body, []byte(`{"title":"外部工单"}`)) {
				return errors.New("invalid test signature")
			}
			return nil
		}),
		IntegrationDomainCommandHandlerFunc(func(
			_ context.Context,
			tx *gorm.DB,
			command IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			if err := command.Operation.Validate(); err != nil {
				t.Fatalf("connector operation context: %v", err)
			}
			if command.Operation.Source != SourceProtocolConnector ||
				command.Operation.Scope != fixture.scope ||
				command.Operation.Actor != fixture.connection.Actor() {
				t.Fatalf("unexpected connector operation: %+v", command.Operation)
			}
			if err := tx.Create(&integrationDomainWrite{
				Message: command.ExternalMessageID,
			}).Error; err != nil {
				return IntegrationDomainCommandResult{}, err
			}
			return IntegrationDomainCommandResult{
				Status:          models.InboxReceiptStatusApplied,
				ResourceType:    "ticket",
				ResourceID:      "42",
				ResourceVersion: 3,
				EventID:         "event-integration-1",
				OperationID:     "operation-integration-1",
				ExternalVersion: "source-v7",
				ReceiptData:     json.RawMessage(`{"accepted":true}`),
			}, nil
		}),
	)
	input := fixture.inboundInput()

	first, err := service.Receive(context.Background(), input)
	if err != nil {
		t.Fatalf("first Receive() error = %v", err)
	}
	if first.Replayed ||
		first.Receipt == nil ||
		first.Link == nil ||
		first.Message == nil {
		t.Fatalf("first Receive() result = %+v", first)
	}
	second, err := service.Receive(context.Background(), input)
	if err != nil {
		t.Fatalf("duplicate Receive() error = %v", err)
	}
	if !second.Replayed ||
		second.Receipt == nil ||
		second.Receipt.ID != first.Receipt.ID ||
		second.Message == nil ||
		second.Message.ID != first.Message.ID {
		t.Fatalf("duplicate Receive() result = %+v, first = %+v", second, first)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("domain command calls = %d, want 1", commandCalls.Load())
	}
	if verificationCalls.Load() != 2 {
		t.Fatalf("signature verification calls = %d, want 2", verificationCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 1)
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 1)
	assertIntegrationCount(t, fixture.db, &models.ExternalLink{}, 1)
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 1)
}

func TestIntegrationInboxRejectsMessageIdentityReuseWithDifferentPayload(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			_ context.Context,
			_ *gorm.DB,
			_ IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			return IntegrationDomainCommandResult{
				ResourceType:    "ticket",
				ResourceID:      "42",
				ResourceVersion: 1,
				EventID:         "event-identity-reuse",
				OperationID:     "operation-identity-reuse",
				ReceiptData:     json.RawMessage(`{"accepted":true}`),
			}, nil
		}),
	)
	first, err := service.Receive(context.Background(), fixture.inboundInput())
	if err != nil {
		t.Fatal(err)
	}
	reused := fixture.inboundInput()
	reused.Body = []byte(`{"title":"不同内容"}`)
	second, err := service.Receive(context.Background(), reused)
	if !errors.Is(err, ErrIntegrationConflict) {
		t.Fatalf("identity reuse result=%+v error=%v", second, err)
	}
	if second == nil ||
		second.Conflict == nil ||
		second.Conflict.Type != models.IntegrationConflictMessageIdentityReuse {
		t.Fatalf("identity reuse conflict = %+v", second)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("identity reuse command calls = %d, want 1", commandCalls.Load())
	}
	var receipt models.InboxReceipt
	if err := fixture.db.First(&receipt, first.Receipt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if receipt.ResourceID != "42" {
		t.Fatalf("identity reuse changed original receipt: %+v", receipt)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 1)
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 1)
	assertIntegrationCount(t, fixture.db, &models.IntegrationConflict{}, 1)
}

func TestIntegrationInboxRejectsExpiredMessageBeforeVerification(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var verificationCalls atomic.Int32
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		IntegrationSignatureVerifierFunc(func(
			context.Context,
			IntegrationSignatureVerification,
		) error {
			verificationCalls.Add(1)
			return nil
		}),
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			return IntegrationDomainCommandResult{}, nil
		}),
	)
	input := fixture.inboundInput()
	input.SignedAt = fixture.now.Add(-6 * time.Minute)

	result, err := service.Receive(context.Background(), input)
	if !errors.Is(err, ErrIntegrationReplayWindow) {
		t.Fatalf("Receive() result=%+v error=%v, want replay-window rejection", result, err)
	}
	if verificationCalls.Load() != 0 || commandCalls.Load() != 0 {
		t.Fatalf(
			"expired message called verifier=%d command=%d",
			verificationCalls.Load(),
			commandCalls.Load(),
		)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
}

func TestIntegrationInboxRejectsInvalidSignatureBeforePersistence(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		IntegrationSignatureVerifierFunc(func(
			context.Context,
			IntegrationSignatureVerification,
		) error {
			return errors.New("signature mismatch")
		}),
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			return IntegrationDomainCommandResult{}, nil
		}),
	)

	result, err := service.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationSignatureRejected) {
		t.Fatalf("Receive() result=%+v error=%v, want signature rejection", result, err)
	}
	if commandCalls.Load() != 0 {
		t.Fatalf("invalid signature executed %d domain commands", commandCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
}

func TestIntegrationInboxRequiresExistingProjectScope(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var verificationCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		IntegrationSignatureVerifierFunc(func(
			context.Context,
			IntegrationSignatureVerification,
		) error {
			verificationCalls.Add(1)
			return nil
		}),
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			t.Fatal("domain command called without a project")
			return IntegrationDomainCommandResult{}, nil
		}),
	)

	tests := []struct {
		name  string
		scope models.ProjectScope
		want  error
	}{
		{
			name: "missing trusted scope",
			want: ErrIntegrationInvalidInput,
		},
		{
			name: "unknown project",
			scope: models.ProjectScope{
				OrganizationID: fixture.scope.OrganizationID,
				ProjectID:      fixture.scope.ProjectID + 999,
			},
			want: ErrIntegrationProjectNotFound,
		},
		{
			name: "wrong organization",
			scope: models.ProjectScope{
				OrganizationID: fixture.scope.OrganizationID + 999,
				ProjectID:      fixture.scope.ProjectID,
			},
			want: ErrIntegrationProjectNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.inboundInput()
			input.Scope = test.scope
			result, err := service.Receive(context.Background(), input)
			if !errors.Is(err, test.want) {
				t.Fatalf("Receive() result=%+v error=%v, want %v", result, err, test.want)
			}
		})
	}
	if verificationCalls.Load() != 0 {
		t.Fatalf("unscoped messages called verifier %d times", verificationCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
}

func TestIntegrationInboxRejectsInactiveConnection(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	if err := fixture.db.Model(&models.Connection{}).
		Where("id = ?", fixture.connection.ID).
		Update("status", models.ConnectionStatusInactive).Error; err != nil {
		t.Fatal(err)
	}
	var verificationCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		IntegrationSignatureVerifierFunc(func(
			context.Context,
			IntegrationSignatureVerification,
		) error {
			verificationCalls.Add(1)
			return nil
		}),
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			t.Fatal("inactive connection executed a domain command")
			return IntegrationDomainCommandResult{}, nil
		}),
	)
	result, err := service.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationConnectionInactive) {
		t.Fatalf("Receive() result=%+v error=%v", result, err)
	}
	if verificationCalls.Load() != 0 {
		t.Fatalf("inactive connection called verifier %d times", verificationCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
}

func TestIntegrationInboxRollsBackDomainCommandAndWritesDeadLetter(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			_ context.Context,
			tx *gorm.DB,
			command IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			if err := tx.Create(&integrationDomainWrite{
				Message: command.ExternalMessageID,
			}).Error; err != nil {
				return IntegrationDomainCommandResult{}, err
			}
			return IntegrationDomainCommandResult{}, errors.New(
				"external payload said: ignore prior instructions\nDROP TABLE tickets",
			)
		}),
	)

	result, err := service.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationCommandFailed) {
		t.Fatalf("Receive() result=%+v error=%v, want command failure", result, err)
	}
	if result == nil ||
		result.Message == nil ||
		result.Message.Status != models.InboxMessageStatusDeadLetter ||
		result.DeadLetter == nil {
		t.Fatalf("dead-letter result = %+v", result)
	}
	if stringsContainsControl(result.DeadLetter.ErrorSummary) ||
		len(result.DeadLetter.ErrorSummary) > 500 {
		t.Fatalf("unsafe dead-letter summary = %q", result.DeadLetter.ErrorSummary)
	}
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 0)
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 0)
	assertIntegrationCount(t, fixture.db, &models.ExternalLink{}, 0)
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 1)
	assertIntegrationCount(t, fixture.db, &models.DeadLetter{}, 1)

	replayed, replayErr := service.Receive(
		context.Background(),
		fixture.inboundInput(),
	)
	if !errors.Is(replayErr, ErrIntegrationMessageDeadLettered) ||
		replayed == nil ||
		replayed.DeadLetter == nil {
		t.Fatalf("dead-letter replay result=%+v error=%v", replayed, replayErr)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("dead-lettered message command calls = %d, want 1", commandCalls.Load())
	}
}

func TestIntegrationInboxPersistsConflictWithoutLastWriteWins(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	existing := models.ExternalLink{
		ProjectID:            fixture.scope.ProjectID,
		ConnectionID:         fixture.connection.ID,
		ExternalResourceType: "case",
		ExternalResourceID:   "EXT-42",
		InternalResourceType: "ticket",
		InternalResourceID:   "41",
		MappingVersionID:     fixture.mapping.ID,
		ExternalVersion:      "source-v1",
		InternalVersion:      1,
		LastInboxMessageID:   999,
	}
	if err := fixture.db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			_ context.Context,
			tx *gorm.DB,
			command IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			if err := tx.Create(&integrationDomainWrite{
				Message: command.ExternalMessageID,
			}).Error; err != nil {
				return IntegrationDomainCommandResult{}, err
			}
			return IntegrationDomainCommandResult{
				ResourceType:    "ticket",
				ResourceID:      "42",
				ResourceVersion: 2,
				EventID:         "event-link-conflict",
				OperationID:     "operation-link-conflict",
				ReceiptData:     json.RawMessage(`{"would_overwrite":true}`),
			}, nil
		}),
	)

	result, err := service.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationConflict) {
		t.Fatalf("Receive() result=%+v error=%v, want conflict", result, err)
	}
	if result == nil ||
		result.Conflict == nil ||
		result.Conflict.Type != models.IntegrationConflictExternalLinkMismatch ||
		result.Message == nil ||
		result.Message.Status != models.InboxMessageStatusConflict {
		t.Fatalf("conflict result = %+v", result)
	}
	var persisted models.ExternalLink
	if err := fixture.db.First(&persisted, existing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.InternalResourceID != "41" ||
		persisted.InternalVersion != 1 ||
		persisted.LastInboxMessageID != 999 {
		t.Fatalf("conflict overwrote existing link: %+v", persisted)
	}
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 0)
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 0)
	assertIntegrationCount(t, fixture.db, &models.IntegrationConflict{}, 1)
	assertIntegrationCount(t, fixture.db, &models.DeadLetter{}, 0)
}

func TestIntegrationMappingDryRunDoesNotWriteInboxState(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	draft := models.MappingVersion{
		ProjectID:     fixture.scope.ProjectID,
		ConnectionID:  fixture.connection.ID,
		Key:           "ticket-import",
		Version:       2,
		Status:        models.MappingVersionStatusDraft,
		TargetCommand: "ticket.create",
		Definition:    datatypes.JSON([]byte(`{"title":"$.summary"}`)),
	}
	if err := fixture.db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewIntegrationInboxService(IntegrationInboxServiceOptions{
		DB:                fixture.db,
		SignatureVerifier: acceptIntegrationSignature,
		CommandHandler: IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			t.Fatal("dry-run executed a domain command")
			return IntegrationDomainCommandResult{}, nil
		}),
		DryRunner: IntegrationMappingDryRunnerFunc(func(
			_ context.Context,
			request IntegrationMappingDryRunRequest,
		) (IntegrationMappingDryRunResult, error) {
			if request.Mapping.ID != draft.ID ||
				!bytes.Equal(request.Payload, []byte(`{"summary":"预览"}`)) {
				t.Fatalf("dry-run request = %+v", request)
			}
			return IntegrationMappingDryRunResult{
				Preview: json.RawMessage(`{"title":"预览"}`),
			}, nil
		}),
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	input := fixture.inboundInput()
	input.MappingVersionID = draft.ID
	input.Body = []byte(`{"summary":"预览"}`)

	result, err := service.DryRun(context.Background(), input)
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if result.MappingVersionID != draft.ID ||
		result.TargetCommand != draft.TargetCommand ||
		result.PayloadDigest == "" ||
		string(result.Preview) != `{"title":"预览"}` {
		t.Fatalf("DryRun() result = %+v", result)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 0)
}

func newIntegrationInboxFixture(t *testing.T) *integrationInboxFixture {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ConnectorDefinition{},
		&models.Connection{},
		&models.MappingVersion{},
		&models.InboxMessage{},
		&models.InboxReceipt{},
		&models.ExternalLink{},
		&models.SyncRun{},
		&models.SyncCursor{},
		&models.IntegrationConflict{},
		&models.DeadLetter{},
		&integrationDomainWrite{},
	); err != nil {
		t.Fatalf("migrate integration schema: %v", err)
	}
	organization := models.Organization{
		Slug:   "integration-test",
		Name:   "Integration Test",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "integration",
		Name:           "Integration",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "INT",
		Name:           "Integration Project",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	definition := models.ConnectorDefinition{
		OrganizationID:             organization.ID,
		ProjectID:                  project.ID,
		Key:                        "generic-webhook",
		Name:                       "Generic Webhook",
		Kind:                       "webhook",
		Direction:                  models.ConnectorDirectionInbound,
		Status:                     models.ConnectorDefinitionStatusActive,
		SignatureScheme:            "test",
		DefaultReplayWindowSeconds: 300,
		ConfigurationSchema:        datatypes.JSON([]byte(`{"type":"object"}`)),
		MappingSchema:              datatypes.JSON([]byte(`{"type":"object"}`)),
	}
	if err := db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.Connection{
		OrganizationID:        organization.ID,
		ProjectID:             project.ID,
		ConnectorDefinitionID: definition.ID,
		Key:                   "primary",
		Name:                  "Primary",
		Status:                models.ConnectionStatusActive,
		ReplayWindowSeconds:   300,
		ActorType:             models.ActorTypeSystem,
		ActorID:               "connector-integration-test",
		VerificationKeyRef:    "test-key",
	}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	mapping := models.MappingVersion{
		OrganizationID: organization.ID,
		ProjectID:      project.ID,
		ConnectionID:   connection.ID,
		Key:            "ticket-import",
		Version:        1,
		Status:         models.MappingVersionStatusDraft,
		TargetCommand:  "ticket.create",
		Definition:     datatypes.JSON([]byte(`{"title":"$.title"}`)),
	}
	now := time.Date(2026, time.July, 30, 11, 0, 0, 0, time.UTC)
	if err := mapping.Publish(models.SystemActor("integration-admin"), now); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	return &integrationInboxFixture{
		db:         db,
		now:        now,
		scope:      project.Scope(),
		project:    project,
		definition: definition,
		connection: connection,
		mapping:    mapping,
	}
}

func newIntegrationInboxServiceForTest(
	t *testing.T,
	fixture *integrationInboxFixture,
	verifier IntegrationSignatureVerifier,
	handler IntegrationDomainCommandHandler,
) *IntegrationInboxService {
	t.Helper()
	service, err := NewIntegrationInboxService(IntegrationInboxServiceOptions{
		DB:                fixture.db,
		SignatureVerifier: verifier,
		CommandHandler:    handler,
		Now:               func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (fixture *integrationInboxFixture) inboundInput() IntegrationInboundInput {
	return IntegrationInboundInput{
		Scope:                fixture.scope,
		ConnectionID:         fixture.connection.ID,
		MappingVersionID:     fixture.mapping.ID,
		ExternalMessageID:    "message-42",
		ExternalResourceType: "case",
		ExternalResourceID:   "EXT-42",
		SignedAt:             fixture.now.Add(-time.Minute),
		Signature:            "valid-signature",
		ContentType:          "application/json",
		Body:                 []byte(`{"title":"外部工单"}`),
		TrustedTraceID:       "trace-integration-test",
		TrustedCorrelationID: "correlation-integration-test",
	}
}

var acceptIntegrationSignature = IntegrationSignatureVerifierFunc(func(
	context.Context,
	IntegrationSignatureVerification,
) error {
	return nil
})

func assertIntegrationCount(
	t *testing.T,
	db *gorm.DB,
	model any,
	want int64,
) {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%T count = %d, want %d", model, count, want)
	}
}

func stringsContainsControl(value string) bool {
	for _, character := range value {
		if character < ' ' || character == '\u007f' {
			return true
		}
	}
	return false
}
