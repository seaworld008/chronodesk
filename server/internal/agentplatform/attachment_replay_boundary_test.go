package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type attachmentReplayBoundaryFixture struct {
	db         *gorm.DB
	native     *services.AgentNativeService
	context    context.Context
	project    models.Project
	principal  models.ServicePrincipal
	credential models.AgentCredential
	grant      models.ProjectPrincipalGrant
	ticket     models.Ticket
	attachment models.TicketAttachment
	record     models.IdempotencyRecord
}

func newAttachmentReplayBoundaryFixture(
	t *testing.T,
) attachmentReplayBoundaryFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{DisableForeignKeyConstraintWhenMigrating: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Project{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.ProjectPrincipalGrant{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	project := models.Project{
		ID:             22,
		OrganizationID: 11,
		BusinessUnitID: 1,
		Key:            "TEST",
		Name:           "Attachment replay",
		Status:         models.ProjectStatusActive,
	}
	principal := models.ServicePrincipal{
		ID:          "attachment-replay-agent",
		Name:        "Attachment Replay Agent",
		Status:      models.ServicePrincipalStatusActive,
		Scopes:      datatypes.JSON([]byte(`["attachments:write"]`)),
		PolicyEpoch: 1,
	}
	credential := models.AgentCredential{
		ID:                 "attachment-replay-credential",
		ServicePrincipalID: principal.ID,
		Name:               "test",
		SecretHash:         "test-only-hash",
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          now.Add(time.Hour),
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON([]byte(`["attachments:write"]`)),
		IsActive:           true,
	}
	for _, value := range []any{
		&project,
		&principal,
		&credential,
		&grant,
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("seed %T: %v", value, err)
		}
	}

	ticket := models.Ticket{
		OrganizationID:       project.OrganizationID,
		ProjectID:            project.ID,
		QueueID:              33,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000000001",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000000002",
		TicketNumber:         "TEST-42",
		Title:                "Attachment replay",
		Description:          "FORCE RLS regression fixture",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              2,
		TrustLevel:           models.TicketTrustLevelVerified,
		CreatedByActorType:   models.ActorTypeServicePrincipal,
		CreatedByActorID:     principal.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:           ticket.ID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            principal.ID,
		ServicePrincipalID: &principal.ID,
		FileName:           "stored.txt",
		OriginalName:       "force-rls-canary.txt",
		FileSize:           5,
		MimeType:           "text/plain",
		FileType:           models.AttachmentTypeDocument,
		StoragePath:        "attachments/stored.txt",
		StorageType:        "local",
		Hash:               "test-hash",
		VirusScan:          models.VirusScanPending,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}

	responseBody, err := json.Marshal(Receipt{
		OperationID:     "attachment-operation",
		ResourceID:      fmt.Sprint(attachment.ID),
		ResourceVersion: ticket.Version,
		EventID:         "attachment-event",
	})
	if err != nil {
		t.Fatal(err)
	}
	record := models.IdempotencyRecord{
		ID:             "attachment-replay-record",
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		ActorType:      models.ActorTypeServicePrincipal,
		ActorID:        principal.ID,
		Operation:      "ticket.attachment.create",
		Key:            "attachment-replay-key",
		RequestHash:    strings.Repeat("a", 64),
		State:          models.IdempotencyStateCompleted,
		ResponseCode:   http.StatusAccepted,
		ResponseBody:   datatypes.JSON(responseBody),
		ResourceID:     fmt.Sprint(attachment.ID),
		ExpiresAt:      now.Add(time.Hour),
	}
	operationContext, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:        project.Scope(),
			Actor:        models.ServicePrincipalActor(principal.ID),
			Source:       services.SourceProtocolAgentREST,
			CredentialID: credential.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return attachmentReplayBoundaryFixture{
		db:         db,
		native:     services.NewAgentNativeService(db),
		context:    operationContext,
		project:    project,
		principal:  principal,
		credential: credential,
		grant:      grant,
		ticket:     ticket,
		attachment: attachment,
		record:     record,
	}
}

func (fixture attachmentReplayBoundaryFixture) authorize(
	t *testing.T,
) (
	*services.PreparedAttachmentReplayAuthorization,
	*services.AttachmentReplayFinalizationResult,
	error,
) {
	t.Helper()
	authorization, err :=
		fixture.native.PrepareAttachmentReplayAuthorization(
			fixture.context,
			services.NativeAttachmentInput{
				TicketID:       fixture.ticket.ID,
				Actor:          models.ServicePrincipalActor(fixture.principal.ID),
				CredentialID:   fixture.credential.ID,
				SourceProtocol: string(services.SourceProtocolAgentREST),
				RequestDigest:  strings.Repeat("b", 64),
				OriginalName:   "force-rls-canary.txt",
				ContentType:    "text/plain",
			},
			[]string{models.ScopeAttachmentsWrite},
		)
	if err != nil {
		return nil, nil, err
	}
	replay, err :=
		fixture.native.
			FinalizeAttachmentReplayInShortProjectTransaction(
				fixture.context,
				services.AttachmentReplayFinalizationInput{
					TicketID:      fixture.ticket.ID,
					Record:        &fixture.record,
					Authorization: *authorization,
				},
			)
	return authorization, replay, err
}

func TestAttachmentReplayAuthorizationAndFallbackUseShortProjectTransactions(
	t *testing.T,
) {
	fixture := newAttachmentReplayBoundaryFixture(t)
	var (
		unscopedDecisionWrite  atomic.Bool
		unscopedAttachmentRead atomic.Bool
	)
	if err := fixture.db.Callback().Create().Before("gorm:create").
		Register("test:reject_unscoped_replay_decision", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table ==
					(models.PolicyDecision{}).TableName() &&
				!scopeddb.HasTransaction(tx.Statement.Context) {
				unscopedDecisionWrite.Store(true)
				tx.AddError(errors.New(
					"policy decision escaped the short project transaction",
				))
			}
		}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Callback().Query().Before("gorm:query").
		Register("test:reject_unscoped_replay_attachment", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil &&
				tx.Statement.Schema.Table ==
					(models.TicketAttachment{}).TableName() &&
				!scopeddb.HasTransaction(tx.Statement.Context) {
				unscopedAttachmentRead.Store(true)
				tx.AddError(errors.New(
					"attachment replay read escaped the short project transaction",
				))
			}
		}); err != nil {
		t.Fatal(err)
	}

	authorization, replay, err := fixture.authorize(t)
	if err != nil {
		t.Fatal(err)
	}
	if unscopedDecisionWrite.Load() {
		t.Error("attachment replay wrote PolicyDecision through the base DB")
	}
	if unscopedAttachmentRead.Load() {
		t.Error("attachment replay loaded TicketAttachment through the base DB")
	}
	if replay == nil ||
		replay.Attachment == nil ||
		replay.Attachment.ID != fixture.attachment.ID {
		t.Fatalf("unexpected authorized replay: %+v", replay)
	}
	if authorization == nil || authorization.DecisionID == "" {
		t.Fatalf("unexpected replay authorization: %+v", authorization)
	}

	var decision models.PolicyDecision
	if err := fixture.db.First(
		&decision,
		"id = ?",
		authorization.DecisionID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed ||
		decision.SourceProtocol !=
			string(services.SourceProtocolAgentREST) ||
		decision.RequestDigest != strings.Repeat("b", 64) ||
		!strings.Contains(
			string(decision.Context),
			`"file_name":"force-rls-canary.txt"`,
		) ||
		!strings.Contains(
			string(decision.Context),
			`"content_type":"text/plain"`,
		) {
		t.Fatalf("unexpected replay decision: %+v", decision)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/projects/TEST/tickets/42/attachments",
		nil,
	).WithContext(fixture.context)
	handler := &APIHandler{}
	handler.writeReplayedAttachment(
		c,
		&fixture.record,
		replay.Attachment,
	)
	if recorder.Code != http.StatusAccepted ||
		!strings.Contains(
			recorder.Body.String(),
			"force-rls-canary.txt",
		) {
		t.Fatalf(
			"legacy attachment replay failed: status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
}

func TestAttachmentReplayFinalBarrierSuppressesSnapshotAfterGrantRevocation(
	t *testing.T,
) {
	fixture := newAttachmentReplayBoundaryFixture(t)
	snapshot, err := json.Marshal(fixture.attachment)
	if err != nil {
		t.Fatal(err)
	}
	fixture.attachment.OriginalName = "database-canary.txt"
	fixture.record.ResourceSnapshot = datatypes.JSON(snapshot)

	if err := fixture.db.Exec(`
		CREATE TRIGGER revoke_grant_after_replay_decision
		AFTER INSERT ON policy_decisions
		WHEN NEW.action = 'ticket.attachment.create'
		BEGIN
			UPDATE project_principal_grants
			SET is_active = 0
			WHERE project_id = 22
				AND service_principal_id = 'attachment-replay-agent';
		END
	`).Error; err != nil {
		t.Fatalf("create Grant revocation barrier: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/projects/TEST/tickets/42/attachments",
		nil,
	).WithContext(fixture.context)
	handler := &APIHandler{}
	_, replay, replayErr := fixture.authorize(t)
	if replayErr != nil {
		handler.writeNativeError(c, replayErr)
	} else {
		handler.writeReplayedAttachment(
			c,
			&fixture.record,
			replay.Attachment,
		)
	}

	var active bool
	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Select("is_active").
		Where("id = ?", fixture.grant.ID).
		Scan(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("Grant revocation barrier did not run")
	}
	if !errors.Is(replayErr, services.ErrProjectAccessDenied) {
		t.Fatalf("replay error=%v, want ErrProjectAccessDenied", replayErr)
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf(
			"status=%d body=%s, want 403",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if body := recorder.Body.String(); strings.Contains(
		body,
		"force-rls-canary.txt",
	) || strings.Contains(body, "database-canary.txt") {
		t.Fatalf("revoked replay leaked attachment snapshot: %s", body)
	}

	var decisions int64
	if err := fixture.db.Model(&models.PolicyDecision{}).
		Where(
			"action = ? AND resource_id = ?",
			"ticket.attachment.create",
			fmt.Sprint(fixture.ticket.ID),
		).
		Count(&decisions).Error; err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("persisted replay decisions=%d, want 1", decisions)
	}
}

func TestAttachmentReplayFinalBarrierRejectsFilenamePolicyEpochChange(
	t *testing.T,
) {
	fixture := newAttachmentReplayBoundaryFixture(t)
	snapshot, err := json.Marshal(fixture.attachment)
	if err != nil {
		t.Fatal(err)
	}
	fixture.record.ResourceSnapshot = datatypes.JSON(snapshot)

	if err := fixture.db.Exec(`
		CREATE TRIGGER add_filename_deny_after_replay_decision
		AFTER INSERT ON policy_decisions
		WHEN NEW.action = 'ticket.attachment.create'
		BEGIN
			INSERT INTO agent_policies (
				id,
				created_at,
				updated_at,
				service_principal_id,
				name,
				effect,
				scope,
				action,
				resource_type,
				resource_id,
				conditions,
				priority,
				is_active
			) VALUES (
				'filename-deny-barrier',
				CURRENT_TIMESTAMP,
				CURRENT_TIMESTAMP,
				'attachment-replay-agent',
				'Deny canary filename',
				'deny',
				'attachments:write',
				'ticket.attachment.create',
				'ticket',
				'',
				'{"file_name":"force-rls-canary.txt"}',
				100,
				1
			);
			UPDATE service_principals
			SET policy_epoch = policy_epoch + 1
			WHERE id = 'attachment-replay-agent';
		END
	`).Error; err != nil {
		t.Fatalf("create filename-policy barrier: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/projects/TEST/tickets/42/attachments",
		nil,
	).WithContext(fixture.context)
	handler := &APIHandler{}
	_, replay, replayErr := fixture.authorize(t)
	if replayErr != nil {
		handler.writeNativeError(c, replayErr)
	} else {
		handler.writeReplayedAttachment(
			c,
			&fixture.record,
			replay.Attachment,
		)
	}

	if !errors.Is(replayErr, services.ErrPolicyDenied) {
		t.Fatalf("replay error=%v, want ErrPolicyDenied", replayErr)
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf(
			"status=%d body=%s, want 403",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if body := recorder.Body.String(); strings.Contains(
		body,
		"force-rls-canary.txt",
	) {
		t.Fatalf("policy-epoch barrier leaked attachment snapshot: %s", body)
	}

	var principal models.ServicePrincipal
	if err := fixture.db.First(
		&principal,
		"id = ?",
		fixture.principal.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if principal.PolicyEpoch == fixture.principal.PolicyEpoch {
		t.Fatal("filename-policy barrier did not bump PolicyEpoch")
	}
	var denyPolicy models.AgentPolicy
	if err := fixture.db.First(
		&denyPolicy,
		"id = ?",
		"filename-deny-barrier",
	).Error; err != nil {
		t.Fatal(err)
	}
	if denyPolicy.Effect != models.AgentPolicyEffectDeny ||
		!strings.Contains(
			string(denyPolicy.Conditions),
			"force-rls-canary.txt",
		) {
		t.Fatalf("unexpected filename deny policy: %+v", denyPolicy)
	}
}
