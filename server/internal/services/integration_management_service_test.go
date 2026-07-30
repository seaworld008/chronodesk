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

func TestIntegrationManagementScopesResourcesAndPublishesWithoutLastWriteWins(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	inbox, err := NewIntegrationInboxService(IntegrationInboxServiceOptions{
		DB:                fixture.db,
		SignatureVerifier: acceptIntegrationSignature,
		CommandHandler: IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			return IntegrationDomainCommandResult{}, errors.New("not used")
		}),
		DryRunner: IntegrationMappingDryRunnerFunc(func(
			_ context.Context,
			request IntegrationMappingDryRunRequest,
		) (IntegrationMappingDryRunResult, error) {
			return IntegrationMappingDryRunResult{
				Preview: json.RawMessage(`{"valid":true}`),
			}, nil
		}),
		Now: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewIntegrationManagementService(fixture.db, inbox)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return fixture.now.Add(time.Hour) }
	ctx := integrationManagementTestContext(t, fixture.scope, 9)

	definition, err := service.CreateConnectorDefinition(ctx, ConnectorDefinitionInput{
		Key:                        "managed-webhook",
		Name:                       "Managed Webhook",
		Description:                "Project-local connector",
		Kind:                       "webhook",
		Direction:                  models.ConnectorDirectionInbound,
		SignatureScheme:            "hmac-sha256",
		DefaultReplayWindowSeconds: 600,
		ConfigurationSchema:        json.RawMessage(`{"type":"object"}`),
		MappingSchema:              json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("create connector definition: %v", err)
	}
	if definition.OrganizationID != fixture.scope.OrganizationID ||
		definition.ProjectID != fixture.scope.ProjectID {
		t.Fatalf("connector scope=%d/%d", definition.OrganizationID, definition.ProjectID)
	}

	if _, err := service.CreateConnection(ctx, ConnectionInput{
		ConnectorDefinitionPublicID: definition.PublicID,
		Key:                         "inline-secret",
		Name:                        "Inline secret",
		Configuration:               json.RawMessage(`{"api_key":"plaintext-secret"}`),
		VerificationKeyRef:          "vault://integration/key",
	}); !errors.Is(err, ErrIntegrationManagementInvalidInput) {
		t.Fatalf("inline secret error=%v, want invalid input", err)
	}
	connection, err := service.CreateConnection(ctx, ConnectionInput{
		ConnectorDefinitionPublicID: definition.PublicID,
		Key:                         "managed-primary",
		Name:                        "Managed Primary",
		Configuration:               json.RawMessage(`{"base_url":"https://connector.example.test"}`),
		VerificationKeyRef:          "vault://integration/key",
		ReplayWindowSeconds:         600,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if connection.OrganizationID != fixture.scope.OrganizationID ||
		connection.ProjectID != fixture.scope.ProjectID ||
		connection.ActorType != models.ActorTypeSystem ||
		connection.VerificationKeyRef != "vault://integration/key" {
		t.Fatalf("connection=%+v", connection)
	}
	inconsistentOrganization := models.Connection{
		OrganizationID:        fixture.scope.OrganizationID + 500,
		ProjectID:             fixture.scope.ProjectID,
		ConnectorDefinitionID: definition.ID,
		Key:                   "wrong-organization",
		Name:                  "Wrong organization",
		Status:                models.ConnectionStatusActive,
		ReplayWindowSeconds:   300,
		ActorType:             models.ActorTypeSystem,
		ActorID:               "connector:wrong-organization",
	}
	if err := fixture.db.Create(&inconsistentOrganization).Error; err != nil {
		t.Fatal(err)
	}
	scopedConnections, err := service.ListConnections(ctx, IntegrationListOptions{
		PageSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scopedConnections.Total != 2 {
		t.Fatalf(
			"organization/project double filter returned %d connections, want 2",
			scopedConnections.Total,
		)
	}

	if _, err := service.CreateMappingDraft(ctx, MappingDraftInput{
		ConnectionPublicID: connection.PublicID,
		Key:                "unsafe",
		TargetCommand:      "shell.execute",
		Definition:         json.RawMessage(`{"value":"$.value"}`),
	}); !errors.Is(err, ErrIntegrationTargetCommandDenied) {
		t.Fatalf("unsafe target error=%v, want denied", err)
	}
	mapping, err := service.CreateMappingDraft(ctx, MappingDraftInput{
		ConnectionPublicID: connection.PublicID,
		Key:                "ticket-import",
		SourceSchema:       json.RawMessage(`{"type":"object"}`),
		TargetCommand:      "ticket.create",
		Definition:         json.RawMessage(`{"title":"$.title"}`),
	})
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	originalUpdatedAt := mapping.UpdatedAt
	originalDigest := mapping.DefinitionDigest
	updated, err := service.UpdateMappingDraft(ctx, mapping.PublicID, MappingDraftUpdateInput{
		SourceSchema:             json.RawMessage(`{"type":"object","required":["title"]}`),
		TargetCommand:            "ticket.create",
		Definition:               json.RawMessage(`{"title":"$.subject"}`),
		ExpectedDefinitionDigest: originalDigest,
		ExpectedUpdatedAt:        originalUpdatedAt,
	})
	if err != nil {
		t.Fatalf("update mapping: %v", err)
	}
	if updated.DefinitionDigest == originalDigest ||
		!updated.UpdatedAt.After(originalUpdatedAt) {
		t.Fatalf("mapping did not advance optimistic token: %+v", updated)
	}
	if _, err := service.UpdateMappingDraft(ctx, mapping.PublicID, MappingDraftUpdateInput{
		SourceSchema:             json.RawMessage(`{}`),
		TargetCommand:            "ticket.create",
		Definition:               json.RawMessage(`{"title":"$.stale"}`),
		ExpectedDefinitionDigest: originalDigest,
		ExpectedUpdatedAt:        originalUpdatedAt,
	}); !errors.Is(err, ErrIntegrationManagementConflict) {
		t.Fatalf("stale mapping update error=%v, want conflict", err)
	}
	published, err := service.PublishMapping(ctx, mapping.PublicID, MappingPublishInput{
		ExpectedDefinitionDigest: updated.DefinitionDigest,
		ExpectedUpdatedAt:        updated.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("publish mapping: %v", err)
	}
	if published.Status != models.MappingVersionStatusPublished ||
		published.PublishedByType != models.ActorTypeHuman ||
		published.PublishedByID != "9" {
		t.Fatalf("published mapping=%+v", published)
	}
	if _, err := service.UpdateMappingDraft(ctx, mapping.PublicID, MappingDraftUpdateInput{
		SourceSchema:             json.RawMessage(`{}`),
		TargetCommand:            "ticket.create",
		Definition:               json.RawMessage(`{"title":"$.mutated"}`),
		ExpectedDefinitionDigest: published.DefinitionDigest,
		ExpectedUpdatedAt:        published.UpdatedAt,
	}); !errors.Is(err, ErrIntegrationManagementImmutable) {
		t.Fatalf("published mapping mutation error=%v, want immutable", err)
	}

	dryRun, err := service.DryRunMapping(
		ctx,
		published.PublicID,
		[]byte(`{"title":"preview"}`),
	)
	if err != nil {
		t.Fatalf("dry-run mapping: %v", err)
	}
	if dryRun.MappingVersionID != published.ID ||
		dryRun.TargetCommand != "ticket.create" ||
		len(dryRun.PayloadDigest) != 64 {
		t.Fatalf("dry-run result=%+v", dryRun)
	}

	otherScope := models.ProjectScope{
		OrganizationID: fixture.scope.OrganizationID + 100,
		ProjectID:      fixture.scope.ProjectID,
	}
	otherCtx := integrationManagementTestContext(t, otherScope, 10)
	list, err := service.ListConnections(otherCtx, IntegrationListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("cross-project list leaked connections: %+v", list)
	}
	if _, err := service.UpdateConnection(otherCtx, connection.PublicID, ConnectionUpdateInput{
		Name:                "Cross project",
		Status:              models.ConnectionStatusActive,
		Configuration:       json.RawMessage(`{}`),
		VerificationKeyRef:  "vault://other/key",
		ReplayWindowSeconds: 300,
		ExpectedUpdatedAt:   connection.UpdatedAt,
	}); !errors.Is(err, ErrIntegrationManagementNotFound) {
		t.Fatalf("cross-project update error=%v, want not found", err)
	}
}

func TestIntegrationInboxRejectsPublishedMappingOutsideTargetAllowlist(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	if err := fixture.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&models.MappingVersion{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			fixture.mapping.ID,
			fixture.scope.OrganizationID,
			fixture.scope.ProjectID,
		).
		UpdateColumn("target_command", "shell.execute").Error; err != nil {
		t.Fatal(err)
	}
	var commandCalls atomic.Int32
	inbox := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			return IntegrationDomainCommandResult{}, nil
		}),
	)
	result, err := inbox.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationTargetCommandDenied) {
		t.Fatalf("unsafe published mapping result=%+v err=%v", result, err)
	}
	if commandCalls.Load() != 0 {
		t.Fatalf("unsafe target executed %d domain commands", commandCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 0)
}

func TestIntegrationDeadLetterReplayUsesFrozenMappingAndIsIdempotent(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	var commandCalls atomic.Int32
	inbox := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			return IntegrationDomainCommandResult{}, errors.New("initial worker failure")
		}),
	)
	failed, err := inbox.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationCommandFailed) ||
		failed == nil || failed.DeadLetter == nil || failed.Message == nil {
		t.Fatalf("seed dead letter result=%+v err=%v", failed, err)
	}
	originalPayload := append([]byte(nil), failed.Message.Payload...)
	frozenMappingID := failed.Message.MappingVersionID

	newer := models.MappingVersion{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		ConnectionID:   fixture.connection.ID,
		Key:            fixture.mapping.Key,
		Version:        fixture.mapping.Version + 1,
		Status:         models.MappingVersionStatusDraft,
		TargetCommand:  "ticket.update",
		Definition:     datatypes.JSON([]byte(`{"title":"$.new_title"}`)),
	}
	if err := newer.Publish(models.SystemActor("integration-admin"), fixture.now); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}
	inbox.commandHandler = IntegrationDomainCommandHandlerFunc(func(
		_ context.Context,
		tx *gorm.DB,
		command IntegrationDomainCommand,
	) (IntegrationDomainCommandResult, error) {
		commandCalls.Add(1)
		if command.Mapping.ID != frozenMappingID ||
			command.Mapping.ID == newer.ID ||
			!bytes.Equal(command.Payload, originalPayload) ||
			command.Operation.Source != SourceProtocolConnector {
			t.Fatalf("replay changed frozen command: %+v", command)
		}
		if err := tx.Create(&integrationDomainWrite{
			Message: command.ExternalMessageID,
		}).Error; err != nil {
			return IntegrationDomainCommandResult{}, err
		}
		return IntegrationDomainCommandResult{
			ResourceType:    "ticket",
			ResourceID:      "replayed-42",
			ResourceVersion: 1,
			EventID:         "event-replayed-42",
			OperationID:     "operation-replayed-42",
			ReceiptData:     json.RawMessage(`{"replayed":true}`),
		}, nil
	})
	management, err := NewIntegrationManagementService(fixture.db, inbox)
	if err != nil {
		t.Fatal(err)
	}
	ctx := integrationManagementTestContext(t, fixture.scope, 17)
	replayed, err := management.ReplayDeadLetter(
		ctx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	)
	if err != nil {
		t.Fatalf("replay dead letter: %v", err)
	}
	if replayed.Receipt == nil ||
		replayed.DeadLetter == nil ||
		replayed.DeadLetter.Status != models.DeadLetterStatusResolved ||
		replayed.Message.Status != models.InboxMessageStatusCompleted ||
		replayed.DeadLetter.AttemptCount != 2 {
		t.Fatalf("replay result=%+v", replayed)
	}
	if replayed.DeadLetter.ResolvedByType != models.ActorTypeHuman ||
		replayed.DeadLetter.ResolvedByID != "17" {
		t.Fatalf("replay resolution actor=%+v", replayed.DeadLetter)
	}
	var persistedMessage models.InboxMessage
	if err := fixture.db.First(&persistedMessage, failed.Message.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persistedMessage.MappingVersionID != frozenMappingID ||
		!bytes.Equal(persistedMessage.Payload, originalPayload) {
		t.Fatalf("replay mutated original message=%+v", persistedMessage)
	}

	second, err := management.ReplayDeadLetter(
		ctx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	)
	if err != nil || second == nil || !second.Replayed || second.Receipt == nil {
		t.Fatalf("idempotent replay result=%+v err=%v", second, err)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("replay command calls=%d, want 1", commandCalls.Load())
	}
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 1)
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 1)

	otherCtx := integrationManagementTestContext(t, models.ProjectScope{
		OrganizationID: fixture.scope.OrganizationID + 1,
		ProjectID:      fixture.scope.ProjectID,
	}, 18)
	if _, err := management.ReplayDeadLetter(
		otherCtx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	); !errors.Is(err, ErrIntegrationManagementNotFound) {
		t.Fatalf("cross-project replay error=%v, want not found", err)
	}
}

func TestIntegrationDeadLetterReplayFailureReopensWithoutFakingSuccess(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	inbox := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			return IntegrationDomainCommandResult{}, errors.New("initial failure")
		}),
	)
	failed, err := inbox.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationCommandFailed) {
		t.Fatalf("seed dead letter: result=%+v err=%v", failed, err)
	}
	inbox.commandHandler = IntegrationDomainCommandHandlerFunc(func(
		_ context.Context,
		tx *gorm.DB,
		_ IntegrationDomainCommand,
	) (IntegrationDomainCommandResult, error) {
		if err := tx.Create(&integrationDomainWrite{Message: "must-roll-back"}).Error; err != nil {
			return IntegrationDomainCommandResult{}, err
		}
		return IntegrationDomainCommandResult{}, errors.New("retry still fails")
	})
	management, err := NewIntegrationManagementService(fixture.db, inbox)
	if err != nil {
		t.Fatal(err)
	}
	ctx := integrationManagementTestContext(t, fixture.scope, 23)
	result, err := management.ReplayDeadLetter(
		ctx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	)
	if !errors.Is(err, ErrIntegrationCommandFailed) ||
		result == nil || result.DeadLetter == nil {
		t.Fatalf("failed replay result=%+v err=%v", result, err)
	}
	if result.DeadLetter.Status != models.DeadLetterStatusOpen ||
		result.DeadLetter.AttemptCount != 2 ||
		result.Message.Status != models.InboxMessageStatusDeadLetter {
		t.Fatalf("failed replay faked success: %+v", result)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 0)
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 0)
}

func TestIntegrationDeadLetterReplayPersistsConflictAndRollsBackDomainCommand(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	inbox := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			return IntegrationDomainCommandResult{}, errors.New("initial failure")
		}),
	)
	failed, err := inbox.Receive(context.Background(), fixture.inboundInput())
	if !errors.Is(err, ErrIntegrationCommandFailed) {
		t.Fatalf("seed dead letter: result=%+v err=%v", failed, err)
	}
	existingLink := models.ExternalLink{
		OrganizationID:       fixture.scope.OrganizationID,
		ProjectID:            fixture.scope.ProjectID,
		ConnectionID:         fixture.connection.ID,
		ExternalResourceType: failed.Message.ExternalResourceType,
		ExternalResourceID:   failed.Message.ExternalResourceID,
		InternalResourceType: "ticket",
		InternalResourceID:   "existing-ticket",
		MappingVersionID:     fixture.mapping.ID,
		InternalVersion:      1,
		LastInboxMessageID:   failed.Message.ID,
	}
	if err := fixture.db.Create(&existingLink).Error; err != nil {
		t.Fatal(err)
	}
	var commandCalls atomic.Int32
	inbox.commandHandler = IntegrationDomainCommandHandlerFunc(func(
		_ context.Context,
		tx *gorm.DB,
		_ IntegrationDomainCommand,
	) (IntegrationDomainCommandResult, error) {
		commandCalls.Add(1)
		if err := tx.Create(&integrationDomainWrite{Message: "must-roll-back-conflict"}).Error; err != nil {
			return IntegrationDomainCommandResult{}, err
		}
		return IntegrationDomainCommandResult{
			ResourceType:    "ticket",
			ResourceID:      "different-ticket",
			ResourceVersion: 2,
			EventID:         "event-conflict-replay",
			OperationID:     "operation-conflict-replay",
			ReceiptData:     json.RawMessage(`{"accepted":true}`),
		}, nil
	})
	management, err := NewIntegrationManagementService(fixture.db, inbox)
	if err != nil {
		t.Fatal(err)
	}
	ctx := integrationManagementTestContext(t, fixture.scope, 29)
	result, err := management.ReplayDeadLetter(
		ctx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	)
	if !errors.Is(err, ErrIntegrationConflict) ||
		result == nil || result.Conflict == nil || result.DeadLetter == nil {
		t.Fatalf("conflicting replay result=%+v err=%v", result, err)
	}
	if result.Message.Status != models.InboxMessageStatusConflict ||
		result.DeadLetter.Status != models.DeadLetterStatusResolved ||
		result.Conflict.Status != models.IntegrationConflictStatusOpen {
		t.Fatalf("conflicting replay state=%+v", result)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxReceipt{}, 0)
	assertIntegrationCount(t, fixture.db, &integrationDomainWrite{}, 0)
	assertIntegrationCount(t, fixture.db, &models.IntegrationConflict{}, 1)

	second, err := management.ReplayDeadLetter(
		ctx,
		failed.DeadLetter.PublicID,
		ReplayIntegrationDeadLetterInput{
			ExpectedUpdatedAt: failed.DeadLetter.UpdatedAt,
		},
	)
	if !errors.Is(err, ErrIntegrationConflict) ||
		second == nil || !second.Replayed || second.Conflict == nil {
		t.Fatalf("idempotent conflict replay result=%+v err=%v", second, err)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("conflict replay command calls=%d, want 1", commandCalls.Load())
	}
}

func TestIntegrationConflictResolutionUsesActorAndOptimisticState(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	message := models.InboxMessage{
		OrganizationID:       fixture.scope.OrganizationID,
		ProjectID:            fixture.scope.ProjectID,
		ConnectionID:         fixture.connection.ID,
		MappingVersionID:     fixture.mapping.ID,
		ExternalMessageID:    "conflict-message",
		ExternalResourceType: "case",
		ExternalResourceID:   "EXT-CONFLICT",
		SignedAt:             fixture.now,
		ReceivedAt:           fixture.now,
		ContentType:          "application/json",
		Payload:              []byte(`{"title":"conflict"}`),
		PayloadDigest:        integrationPayloadDigest([]byte(`{"title":"conflict"}`)),
		SignatureDigest:      integrationPayloadDigest([]byte("signature")),
		Status:               models.InboxMessageStatusConflict,
	}
	if err := fixture.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	conflict := models.IntegrationConflict{
		OrganizationID:       fixture.scope.OrganizationID,
		ProjectID:            fixture.scope.ProjectID,
		ConnectionID:         fixture.connection.ID,
		InboxMessageID:       message.ID,
		ConflictKey:          integrationPayloadDigest([]byte("conflict-key")),
		Type:                 models.IntegrationConflictExternalLinkMismatch,
		Status:               models.IntegrationConflictStatusOpen,
		ExternalResourceType: message.ExternalResourceType,
		ExternalResourceID:   message.ExternalResourceID,
		Details:              datatypes.JSON([]byte(`{"requires_resolution":true}`)),
	}
	if err := fixture.db.Create(&conflict).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewIntegrationManagementService(fixture.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return fixture.now.Add(time.Hour) }
	ctx := integrationManagementTestContext(t, fixture.scope, 31)
	resolved, err := service.ResolveConflict(ctx, conflict.PublicID, ResolveIntegrationConflictInput{
		Resolution:        IntegrationConflictResolve,
		ExpectedUpdatedAt: conflict.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("resolve conflict: %v", err)
	}
	if resolved.Status != models.IntegrationConflictStatusResolved ||
		resolved.ResolvedByType != models.ActorTypeHuman ||
		resolved.ResolvedByID != "31" {
		t.Fatalf("resolved conflict=%+v", resolved)
	}
	if _, err := service.ResolveConflict(ctx, conflict.PublicID, ResolveIntegrationConflictInput{
		Resolution:        IntegrationConflictIgnore,
		ExpectedUpdatedAt: conflict.UpdatedAt,
	}); !errors.Is(err, ErrIntegrationManagementConflict) {
		t.Fatalf("stale conflict resolution error=%v, want conflict", err)
	}
	overview, err := service.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Connections != 1 || overview.OpenConflicts != 0 {
		t.Fatalf("integration overview=%+v", overview)
	}
}

func integrationManagementTestContext(
	t *testing.T,
	scope models.ProjectScope,
	userID uint,
) context.Context {
	t.Helper()
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:         scope,
		Actor:         models.HumanActor(userID),
		Source:        SourceProtocolHumanREST,
		TraceID:       "integration-management-test",
		CorrelationID: "integration-management-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
