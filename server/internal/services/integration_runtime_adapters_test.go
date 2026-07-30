package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestIntegrationHMACSHA256VerifierCurrentPreviousAndFailure(t *testing.T) {
	current := []byte("current-integration-verification-key-32")
	previous := []byte("previous-integration-verification-key")
	var resolvedReference string
	verifier, err := NewIntegrationHMACSHA256Verifier(
		IntegrationVerificationKeyResolverFunc(func(
			_ context.Context,
			reference string,
		) (IntegrationVerificationKeySet, error) {
			resolvedReference = reference
			return IntegrationVerificationKeySet{
				Current:  append([]byte(nil), current...),
				Previous: append([]byte(nil), previous...),
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	signedAt := time.Date(2026, time.July, 30, 9, 10, 11, 0, time.UTC)
	body := []byte(`{"event":"created"}`)
	base := IntegrationSignatureVerification{
		Connection: &models.Connection{
			PublicID:           "018f0f77-ec00-7000-8000-000000000101",
			VerificationKeyRef: "secret/integrations/acme",
		},
		Connector: &models.ConnectorDefinition{
			SignatureScheme: IntegrationHMACSHA256SignatureScheme,
		},
		ProjectKey:           "OPS",
		MappingPublicID:      "018f0f77-ec00-7000-8000-000000000102",
		MessageID:            "event-created-1",
		ExternalResourceType: "case",
		ExternalResourceID:   "case-42",
		ContentType:          "application/json",
		SignedAt:             signedAt,
		Body:                 body,
	}
	for name, key := range map[string][]byte{
		"current":  current,
		"previous": previous,
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Signature = signIntegrationRuntimeMessage(key, input)
			if err := verifier.Verify(context.Background(), input); err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
	if resolvedReference != "secret/integrations/acme" {
		t.Fatalf("resolved reference = %q", resolvedReference)
	}

	for name, signature := range map[string]string{
		"wrong digest": "v1=" + string(make([]byte, 64)),
		"uppercase":    "v1=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"extra version": "v2=" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Signature = signature
			if err := verifier.Verify(context.Background(), input); !errors.Is(
				err,
				ErrIntegrationSignatureRejected,
			) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestIntegrationHMACSHA256VerifierFailsClosedOnKeyOrScheme(t *testing.T) {
	verifier, err := NewIntegrationHMACSHA256Verifier(
		IntegrationVerificationKeyResolverFunc(func(
			context.Context,
			string,
		) (IntegrationVerificationKeySet, error) {
			return IntegrationVerificationKeySet{
				Current: []byte("short"),
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	signedAt := time.Now().UTC().Truncate(time.Second)
	input := IntegrationSignatureVerification{
		Connection: &models.Connection{
			PublicID:           "018f0f77-ec00-7000-8000-000000000103",
			VerificationKeyRef: "secret/key",
		},
		Connector: &models.ConnectorDefinition{
			SignatureScheme: IntegrationHMACSHA256SignatureScheme,
		},
		ProjectKey:           "OPS",
		MappingPublicID:      "018f0f77-ec00-7000-8000-000000000104",
		MessageID:            "message-1",
		ExternalResourceType: "case",
		ExternalResourceID:   "case-1",
		ContentType:          "application/json",
		SignedAt:             signedAt,
		Body:                 []byte(`{}`),
	}
	input.Signature = signIntegrationRuntimeMessage(make([]byte, 32), input)
	if err := verifier.Verify(context.Background(), input); !errors.Is(
		err,
		ErrIntegrationVerificationKeyUnavailable,
	) {
		t.Fatalf("short key error = %v", err)
	}
	input.Connector.SignatureScheme = "arbitrary"
	if err := verifier.Verify(context.Background(), input); !errors.Is(
		err,
		ErrIntegrationSignatureRejected,
	) {
		t.Fatalf("wrong scheme error = %v", err)
	}
}

func TestIntegrationInboxWithHMACRejectsBadSignatureAndDeduplicatesReplay(
	t *testing.T,
) {
	fixture := newIntegrationInboxFixture(t)
	fixture.definition.SignatureScheme = IntegrationHMACSHA256SignatureScheme
	if err := fixture.db.Model(&models.ConnectorDefinition{}).
		Where("id = ?", fixture.definition.ID).
		Update("signature_scheme", fixture.definition.SignatureScheme).Error; err != nil {
		t.Fatal(err)
	}
	key := []byte("inbox-runtime-verification-key-material")
	verifier, err := NewIntegrationHMACSHA256Verifier(
		IntegrationVerificationKeyResolverFunc(func(
			context.Context,
			string,
		) (IntegrationVerificationKeySet, error) {
			return IntegrationVerificationKeySet{Current: key}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var commandCalls atomic.Int32
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		verifier,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			commandCalls.Add(1)
			return IntegrationDomainCommandResult{
				Status:          models.InboxReceiptStatusApplied,
				ResourceType:    "ticket",
				ResourceID:      "018f0f77-ec00-7000-8000-000000000001",
				ResourceVersion: 1,
				EventID:         "event-hmac-replay",
				OperationID:     "operation-hmac-replay",
				ReceiptData:     json.RawMessage(`{"accepted":true}`),
			}, nil
		}),
	)
	input := fixture.inboundInput()
	input.Signature = signIntegrationRuntimeMessage(
		key,
		integrationSignatureVerificationForFixture(fixture, input),
	)
	first, err := service.Receive(context.Background(), input)
	if err != nil || first == nil || first.Replayed {
		t.Fatalf("first Receive() result=%+v err=%v", first, err)
	}
	replayed, err := service.Receive(context.Background(), input)
	if err != nil || replayed == nil || !replayed.Replayed {
		t.Fatalf("replay Receive() result=%+v err=%v", replayed, err)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("command calls = %d, want 1", commandCalls.Load())
	}

	tamperedMessageID := input
	tamperedMessageID.ExternalMessageID = "copied-signature-new-id"
	if result, err := service.Receive(
		context.Background(),
		tamperedMessageID,
	); result != nil || !errors.Is(err, ErrIntegrationSignatureRejected) {
		t.Fatalf(
			"tampered message id with copied signature result=%+v err=%v",
			result,
			err,
		)
	}
	if commandCalls.Load() != 1 {
		t.Fatalf("tampered replay executed domain command %d times", commandCalls.Load())
	}

	rejected := input
	rejected.ExternalMessageID = "bad-signature-message"
	rejected.Signature = "v1=" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if result, err := service.Receive(context.Background(), rejected); result != nil ||
		!errors.Is(err, ErrIntegrationSignatureRejected) {
		t.Fatalf("bad signature result=%+v err=%v", result, err)
	}
	assertIntegrationCount(t, fixture.db, &models.InboxMessage{}, 1)
}

func TestResolvePublicInboundTargetRejectsCrossProjectOpaqueIDs(t *testing.T) {
	fixture := newIntegrationInboxFixture(t)
	service := newIntegrationInboxServiceForTest(
		t,
		fixture,
		acceptIntegrationSignature,
		IntegrationDomainCommandHandlerFunc(func(
			context.Context,
			*gorm.DB,
			IntegrationDomainCommand,
		) (IntegrationDomainCommandResult, error) {
			return IntegrationDomainCommandResult{}, errors.New("unused")
		}),
	)
	target, err := service.ResolvePublicInboundTarget(
		context.Background(),
		string(fixture.project.Key),
		fixture.connection.PublicID,
		fixture.mapping.PublicID,
	)
	if err != nil ||
		target.Scope != fixture.scope ||
		target.ConnectionID != fixture.connection.ID ||
		target.MappingVersionID != fixture.mapping.ID {
		t.Fatalf("ResolvePublicInboundTarget() target=%+v err=%v", target, err)
	}
	other := models.Project{
		OrganizationID: fixture.project.OrganizationID,
		BusinessUnitID: fixture.project.BusinessUnitID,
		Key:            "OTHER",
		Name:           "Other Project",
		Status:         models.ProjectStatusActive,
	}
	if err := fixture.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if target, err := service.ResolvePublicInboundTarget(
		context.Background(),
		string(other.Key),
		fixture.connection.PublicID,
		fixture.mapping.PublicID,
	); target.ConnectionID != 0 ||
		!errors.Is(err, ErrIntegrationConnectionNotFound) {
		t.Fatalf("cross-project target=%+v err=%v", target, err)
	}
}

func TestDeclarativeIntegrationRuntimeDryRunIsTypedAndScriptFree(t *testing.T) {
	fixture := newIntegrationRuntimeFixture(t)
	definition := json.RawMessage(`{
		"version":1,
		"fields":{
			"title":{"pointer":"/case/summary","type":"string","required":true},
			"description":{"value":"Imported through connector","type":"string"},
			"priority":{"pointer":"/case/priority","type":"string"},
			"tags":{"pointer":"/case/tags","type":"array"}
		}
	}`)
	mapping := fixture.createMapping(t, "dry-run", 1, "ticket.create", definition)
	result, err := fixture.runtime.DryRun(
		context.Background(),
		IntegrationMappingDryRunRequest{
			Connection: &fixture.connection,
			Connector:  &fixture.definition,
			Mapping:    &mapping,
			Payload: []byte(
				`{"case":{"summary":"Printer unavailable","priority":"high","tags":["office"]}}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var preview map[string]any
	if err := json.Unmarshal(result.Preview, &preview); err != nil {
		t.Fatal(err)
	}
	if preview["title"] != "Printer unavailable" ||
		preview["description"] != "Imported through connector" ||
		preview["priority"] != "high" {
		t.Fatalf("preview = %#v", preview)
	}

	for name, invalid := range map[string]json.RawMessage{
		"json path is not accepted": json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"$.title","type":"string","required":true},
				"description":{"value":"description","type":"string"}
			}
		}`),
		"unknown executable field": json.RawMessage(`{
			"version":1,
			"script":"return payload",
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"value":"description","type":"string"}
			}
		}`),
		"type mismatch": json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"object","required":true},
				"description":{"value":"description","type":"string"}
			}
		}`),
		"duplicate keys": json.RawMessage(`{
			"version":1,
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"value":"description","type":"string"}
			}
		}`),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := mapping
			candidate.Definition = datatypes.JSON(invalid)
			digest := sha256.Sum256(invalid)
			candidate.DefinitionDigest = hex.EncodeToString(digest[:])
			if _, err := fixture.runtime.DryRun(
				context.Background(),
				IntegrationMappingDryRunRequest{
					Connection: &fixture.connection,
					Connector:  &fixture.definition,
					Mapping:    &candidate,
					Payload:    []byte(`{"title":"unsafe"}`),
				},
			); !errors.Is(err, ErrIntegrationRuntimeInvalidMapping) {
				t.Fatalf("DryRun() error = %v", err)
			}
		})
	}
}

func TestDeclarativeIntegrationRuntimeTicketCreateUsesOuterTransaction(
	t *testing.T,
) {
	fixture := newIntegrationRuntimeFixture(t)
	mapping := fixture.createMapping(
		t,
		"ticket-create",
		1,
		"ticket.create",
		json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"pointer":"/description","type":"string","required":true},
				"type":{"value":"incident","type":"string"},
				"priority":{"value":"urgent","type":"string"}
			}
		}`),
	)
	body := []byte(
		`{"title":"External outage","description":"Untrusted details"}`,
	)
	command := fixture.command(mapping, body)
	rollback := errors.New("force outer rollback")
	err := fixture.db.Transaction(func(tx *gorm.DB) error {
		result, err := fixture.runtime.Execute(context.Background(), tx, command)
		if err != nil {
			return err
		}
		if result.ResourceType != "ticket" ||
			!canonicalIntegrationUUID(result.ResourceID) ||
			result.ResourceVersion != 1 ||
			result.EventID == "" ||
			result.OperationID == "" {
			t.Fatalf("runtime result = %+v", result)
		}
		assertCountOnDB(t, tx, &models.Ticket{}, 1)
		assertCountOnDB(t, tx, &models.TicketHistory{}, 1)
		assertCountOnDB(t, tx, &models.DomainEvent{}, 1)
		assertCountOnDB(t, tx, &models.OutboxDelivery{}, 1)
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction error = %v", err)
	}
	assertCountOnDB(t, fixture.db, &models.Ticket{}, 0)
	assertCountOnDB(t, fixture.db, &models.TicketHistory{}, 0)
	assertCountOnDB(t, fixture.db, &models.DomainEvent{}, 0)
	assertCountOnDB(t, fixture.db, &models.OutboxDelivery{}, 0)
	var project models.Project
	if err := fixture.db.First(&project, fixture.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if project.TicketSequence != 0 {
		t.Fatalf("ticket sequence committed outside Inbox transaction: %d", project.TicketSequence)
	}
}

func TestDeclarativeIntegrationRuntimeTicketCommentUsesOuterTransaction(
	t *testing.T,
) {
	fixture := newIntegrationRuntimeFixture(t)
	createMapping := fixture.createMapping(
		t,
		"create-before-comment",
		1,
		"ticket.create",
		json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"pointer":"/description","type":"string","required":true}
			}
		}`),
	)
	createBody := []byte(`{"title":"Comment target","description":"External ticket"}`)
	if err := fixture.db.Transaction(func(tx *gorm.DB) error {
		_, err := fixture.runtime.Execute(
			context.Background(),
			tx,
			fixture.command(createMapping, createBody),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var ticket models.Ticket
	if err := fixture.db.First(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	commentMapping := fixture.createMapping(
		t,
		"ticket-comment",
		1,
		"ticket.comment.create",
		json.RawMessage(`{
			"version":1,
			"fields":{
				"ticket_public_id":{"pointer":"/ticket","type":"string","required":true},
				"expected_version":{"pointer":"/version","type":"integer","required":true},
				"content":{"pointer":"/comment","type":"string","required":true},
				"comment_type":{"value":"public","type":"string"}
			}
		}`),
	)
	commentBody := []byte(fmt.Sprintf(
		`{"ticket":%q,"version":1,"comment":"Technician dispatched"}`,
		ticket.PublicID,
	))
	if !canonicalIntegrationUUID(ticket.PublicID) {
		t.Fatalf("created ticket public id is not canonical: %q", ticket.PublicID)
	}
	if _, err := fixture.runtime.DryRun(
		context.Background(),
		IntegrationMappingDryRunRequest{
			Connection: &fixture.connection,
			Connector:  &fixture.definition,
			Mapping:    &commentMapping,
			Payload:    commentBody,
		},
	); err != nil {
		t.Fatalf("comment mapping dry-run: %v", err)
	}
	beforeEvents := countOnDB(t, fixture.db, &models.DomainEvent{})
	beforeOutbox := countOnDB(t, fixture.db, &models.OutboxDelivery{})
	rollback := errors.New("force comment rollback")
	err := fixture.db.Transaction(func(tx *gorm.DB) error {
		result, err := fixture.runtime.Execute(
			context.Background(),
			tx,
			fixture.command(commentMapping, commentBody),
		)
		if err != nil {
			return err
		}
		if result.ResourceType != "ticket_comment" ||
			result.ResourceVersion != 2 ||
			result.EventID == "" {
			t.Fatalf("comment result = %+v", result)
		}
		assertCountOnDB(t, tx, &models.TicketComment{}, 1)
		if got := countOnDB(t, tx, &models.DomainEvent{}); got != beforeEvents+1 {
			t.Fatalf("events inside tx = %d, want %d", got, beforeEvents+1)
		}
		if got := countOnDB(t, tx, &models.OutboxDelivery{}); got != beforeOutbox+1 {
			t.Fatalf("outbox inside tx = %d, want %d", got, beforeOutbox+1)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction error = %v", err)
	}
	assertCountOnDB(t, fixture.db, &models.TicketComment{}, 0)
	if got := countOnDB(t, fixture.db, &models.DomainEvent{}); got != beforeEvents {
		t.Fatalf("events after rollback = %d, want %d", got, beforeEvents)
	}
	if got := countOnDB(t, fixture.db, &models.OutboxDelivery{}); got != beforeOutbox {
		t.Fatalf("outbox after rollback = %d, want %d", got, beforeOutbox)
	}
	if err := fixture.db.First(&ticket, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ticket.Version != 1 || ticket.CommentCount != 0 {
		t.Fatalf("ticket changed outside outer transaction: %+v", ticket)
	}
}

func TestDeclarativeIntegrationRuntimeServicePrincipalRollbackIsAtomic(
	t *testing.T,
) {
	fixture := newIntegrationRuntimeFixture(t)
	native := fixture.runtime.native
	principal, err := native.CreateServicePrincipal(
		context.Background(),
		CreateServicePrincipalInput{
			Name: "integration-principal",
			Scopes: []string{
				models.ScopeTicketsCreate,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := native.IssueCredential(
		context.Background(),
		principal.ID,
		"integration",
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	grantScopes, err := json.Marshal([]string{models.ScopeTicketsCreate})
	if err != nil {
		t.Fatal(err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          fixture.scope.ProjectID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(grantScopes),
		IsActive:           true,
	}
	if err := fixture.db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.Connection{}).
		Where("id = ?", fixture.connection.ID).
		Updates(map[string]any{
			"actor_type":          models.ActorTypeServicePrincipal,
			"actor_id":            principal.ID,
			"actor_credential_id": credential.Credential.ID,
		}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.connection.ActorType = models.ActorTypeServicePrincipal
	fixture.connection.ActorID = principal.ID
	fixture.connection.ActorCredentialID = credential.Credential.ID
	mapping := fixture.createMapping(
		t,
		"service-principal-create",
		1,
		"ticket.create",
		json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"pointer":"/description","type":"string","required":true}
			}
		}`),
	)
	body := []byte(`{"title":"SP ticket","description":"must roll back atomically"}`)
	rollback := errors.New("force service-principal rollback")
	err = fixture.db.Transaction(func(tx *gorm.DB) error {
		result, err := fixture.runtime.Execute(
			context.Background(),
			tx,
			fixture.command(mapping, body),
		)
		if err != nil {
			return err
		}
		if result.ResourceType != "ticket" {
			t.Fatalf("runtime result = %+v", result)
		}
		assertCountOnDB(t, tx, &models.Ticket{}, 1)
		assertCountOnDB(t, tx, &models.TicketHistory{}, 1)
		assertCountOnDB(t, tx, &models.DomainEvent{}, 1)
		assertCountOnDB(t, tx, &models.OutboxDelivery{}, 1)
		// ticket.create and the independently governed external notification
		// side effect each produce an immutable policy decision.
		assertCountOnDB(t, tx, &models.PolicyDecision{}, 2)
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction error = %v", err)
	}
	assertCountOnDB(t, fixture.db, &models.Ticket{}, 0)
	assertCountOnDB(t, fixture.db, &models.TicketHistory{}, 0)
	assertCountOnDB(t, fixture.db, &models.DomainEvent{}, 0)
	assertCountOnDB(t, fixture.db, &models.OutboxDelivery{}, 0)
	assertCountOnDB(t, fixture.db, &models.PolicyDecision{}, 0)

	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where("id = ?", grant.ID).
		Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.runtime.Execute(
		context.Background(),
		fixture.db,
		fixture.command(mapping, body),
	); result.ResourceID != "" ||
		!errors.Is(err, ErrIntegrationRuntimeScopeMismatch) {
		t.Fatalf("revoked grant result=%+v err=%v", result, err)
	}
	assertCountOnDB(t, fixture.db, &models.Ticket{}, 0)
	assertCountOnDB(t, fixture.db, &models.PolicyDecision{}, 0)
}

func TestAgentNativeReserveIdempotencyUsesBoundTransaction(t *testing.T) {
	fixture := newIntegrationRuntimeFixture(t)
	actor := fixture.connection.Actor()
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.scope,
			Actor:  actor,
			Source: SourceProtocolConnector,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("force idempotency rollback")
	err = fixture.db.Transaction(func(tx *gorm.DB) error {
		txContext := context.WithValue(
			operationContext,
			agentNativeTransactionContextKey{},
			tx,
		)
		reservation, err := fixture.runtime.native.ReserveIdempotency(
			txContext,
			actor,
			"ticket.create",
			"idempotency-runtime-test",
			[]byte(`{"safe":true}`),
			time.Hour,
		)
		if err != nil || reservation == nil || reservation.Record == nil {
			t.Fatalf("ReserveIdempotency() reservation=%+v err=%v", reservation, err)
		}
		assertCountOnDB(t, tx, &models.IdempotencyRecord{}, 1)
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("outer transaction error = %v", err)
	}
	assertCountOnDB(t, fixture.db, &models.IdempotencyRecord{}, 0)
}

func TestDeclarativeIntegrationRuntimeUnknownCommandFailsClosed(t *testing.T) {
	fixture := newIntegrationRuntimeFixture(t)
	mapping := fixture.createMapping(
		t,
		"unsupported-update",
		1,
		"ticket.update",
		json.RawMessage(`{"version":1,"fields":{"title":{"pointer":"/title","type":"string"}}}`),
	)
	body := []byte(`{"title":"must not execute"}`)
	result, err := fixture.runtime.Execute(
		context.Background(),
		fixture.db,
		fixture.command(mapping, body),
	)
	if result.ResourceID != "" ||
		!errors.Is(err, ErrIntegrationRuntimeUnsupportedCommand) {
		t.Fatalf("Execute() result=%+v err=%v", result, err)
	}
	assertCountOnDB(t, fixture.db, &models.Ticket{}, 0)
}

func TestDeclarativeIntegrationRuntimeRejectsSecurityFieldsInCustomData(
	t *testing.T,
) {
	fixture := newIntegrationRuntimeFixture(t)
	mapping := fixture.createMapping(
		t,
		"security-fields",
		1,
		"ticket.create",
		json.RawMessage(`{
			"version":1,
			"fields":{
				"title":{"pointer":"/title","type":"string","required":true},
				"description":{"pointer":"/description","type":"string","required":true},
				"custom_fields":{"pointer":"/fields","type":"object"}
			}
		}`),
	)
	body := []byte(
		`{"title":"Unsafe","description":"Unsafe","fields":{"nested":{"project_id":999}}}`,
	)
	if _, err := fixture.runtime.DryRun(
		context.Background(),
		IntegrationMappingDryRunRequest{
			Connection: &fixture.connection,
			Connector:  &fixture.definition,
			Mapping:    &mapping,
			Payload:    body,
		},
	); !errors.Is(err, ErrIntegrationRuntimeInvalidMapping) {
		t.Fatalf("DryRun() error = %v", err)
	}
}

type integrationRuntimeFixture struct {
	db         *gorm.DB
	project    models.Project
	scope      models.ProjectScope
	definition models.ConnectorDefinition
	connection models.Connection
	runtime    *DeclarativeIntegrationRuntime
}

func newIntegrationRuntimeFixture(t *testing.T) *integrationRuntimeFixture {
	t.Helper()
	db := openAgentNativeTestDB(t)
	systemActor := models.SystemActor("connector-runtime-test")
	workerContext := testProjectOperationContext(t, db, systemActor)
	operation, err := OperationContextFromContext(workerContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.ProjectPrincipalGrant{},
		&models.ConnectorDefinition{},
		&models.Connection{},
		&models.MappingVersion{},
	); err != nil {
		t.Fatal(err)
	}
	var project models.Project
	if err := db.First(&project, operation.Scope.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	definition := models.ConnectorDefinition{
		OrganizationID:             operation.Scope.OrganizationID,
		ProjectID:                  operation.Scope.ProjectID,
		Key:                        "runtime-webhook",
		Name:                       "Runtime Webhook",
		Kind:                       "webhook",
		Direction:                  models.ConnectorDirectionInbound,
		Status:                     models.ConnectorDefinitionStatusActive,
		SignatureScheme:            IntegrationHMACSHA256SignatureScheme,
		DefaultReplayWindowSeconds: 300,
		ConfigurationSchema:        datatypes.JSON([]byte(`{"type":"object"}`)),
		MappingSchema:              datatypes.JSON([]byte(`{"type":"object"}`)),
	}
	if err := db.Create(&definition).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.Connection{
		OrganizationID:        operation.Scope.OrganizationID,
		ProjectID:             operation.Scope.ProjectID,
		ConnectorDefinitionID: definition.ID,
		Key:                   "runtime-primary",
		Name:                  "Runtime Primary",
		Status:                models.ConnectionStatusActive,
		VerificationKeyRef:    "secret/runtime",
		ReplayWindowSeconds:   300,
		ActorType:             systemActor.Type,
		ActorID:               systemActor.ID,
	}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	runtime, err := NewDeclarativeIntegrationRuntime(NewAgentNativeService(db))
	if err != nil {
		t.Fatal(err)
	}
	return &integrationRuntimeFixture{
		db:         db,
		project:    project,
		scope:      operation.Scope,
		definition: definition,
		connection: connection,
		runtime:    runtime,
	}
}

func (fixture *integrationRuntimeFixture) createMapping(
	t *testing.T,
	key string,
	version uint,
	command string,
	definition json.RawMessage,
) models.MappingVersion {
	t.Helper()
	mapping := models.MappingVersion{
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		ConnectionID:   fixture.connection.ID,
		Key:            key,
		Version:        version,
		Status:         models.MappingVersionStatusDraft,
		TargetCommand:  command,
		Definition:     datatypes.JSON(definition),
	}
	if err := mapping.Publish(
		models.SystemActor("integration-runtime-admin"),
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	return mapping
}

func (fixture *integrationRuntimeFixture) command(
	mapping models.MappingVersion,
	body []byte,
) IntegrationDomainCommand {
	digest := sha256.Sum256(body)
	return IntegrationDomainCommand{
		Operation: OperationContext{
			Scope:         fixture.scope,
			Actor:         fixture.connection.Actor(),
			Source:        SourceProtocolConnector,
			CredentialID:  fixture.connection.ActorCredentialID,
			TraceID:       "trace-runtime-test",
			CorrelationID: "correlation-runtime-test",
		},
		Connection:           &fixture.connection,
		Connector:            &fixture.definition,
		Mapping:              &mapping,
		ExternalMessageID:    "runtime-message",
		ExternalResourceType: "case",
		ExternalResourceID:   "external-case",
		Payload:              body,
		PayloadDigest:        hex.EncodeToString(digest[:]),
	}
}

func signIntegrationRuntimeMessage(
	key []byte,
	input IntegrationSignatureVerification,
) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(integrationHMACSigningPayload(input))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func integrationSignatureVerificationForFixture(
	fixture *integrationInboxFixture,
	input IntegrationInboundInput,
) IntegrationSignatureVerification {
	return IntegrationSignatureVerification{
		Connection:           &fixture.connection,
		Connector:            &fixture.definition,
		ProjectKey:           string(fixture.project.Key),
		MappingPublicID:      fixture.mapping.PublicID,
		SignedAt:             input.SignedAt,
		MessageID:            input.ExternalMessageID,
		ExternalResourceType: input.ExternalResourceType,
		ExternalResourceID:   input.ExternalResourceID,
		ContentType:          normalizedIntegrationContentType(input.ContentType),
		Body:                 input.Body,
	}
}

func assertCountOnDB(t *testing.T, db *gorm.DB, model any, want int64) {
	t.Helper()
	if got := countOnDB(t, db, model); got != want {
		t.Fatalf("%T count = %d, want %d", model, got, want)
	}
}

func countOnDB(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
