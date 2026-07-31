package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func openAgentNativeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.Category{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookDeliverySnapshot{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatalf("failed to migrate agent-native schema: %v", err)
	}
	return db
}

func assertTicketHistoryEventLink(
	t *testing.T,
	history *models.TicketHistory,
	event *models.DomainEvent,
) {
	t.Helper()
	if history.EventID == nil ||
		event == nil ||
		*history.EventID != event.ID ||
		history.ResourceVersion != event.ResourceVersion ||
		history.Provenance != models.TicketHistoryProvenanceDomainEvent {
		t.Fatalf("history is not linked to its persisted domain event: history=%+v event=%+v", history, event)
	}
}

func seedActorUser(t *testing.T, db *gorm.DB, suffix string) models.User {
	t.Helper()
	user := models.User{
		Username:     "compat-" + suffix,
		Email:        "compat-" + suffix + "@example.com",
		PasswordHash: "not-a-real-password",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed actor user: %v", err)
	}
	return user
}

func ensureAttachmentTestAuthorization(
	t *testing.T,
	db *gorm.DB,
	ctx context.Context,
	actor models.ActorRef,
) {
	t.Helper()
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	switch actor.Type {
	case models.ActorTypeHuman:
		userID, err := humanActorUserID(actor)
		if err != nil {
			t.Fatal(err)
		}
		ensureTestHumanProjectRole(
			t,
			db,
			ctx,
			userID,
			models.ProjectRoleAdmin,
		)
	case models.ActorTypeServicePrincipal:
		var principal models.ServicePrincipal
		if err := db.Where("id = ?", actor.ID).
			Take(&principal).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.ProjectPrincipalGrant{
			ProjectID:          operation.Scope.ProjectID,
			ServicePrincipalID: actor.ID,
			Role:               models.ProjectRoleAgent,
			Scopes:             principal.Scopes,
			IsActive:           true,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.AgentCredential{
			ID:                 operation.CredentialID,
			ServicePrincipalID: actor.ID,
			Name:               "attachment-test",
			SecretHash:         strings.Repeat("a", 64),
			Status:             models.AgentCredentialStatusActive,
			ExpiresAt:          time.Now().Add(time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	case models.ActorTypeSystem:
	default:
		t.Fatalf("unsupported attachment test actor: %+v", actor)
	}
}

func createNativePrincipal(
	t *testing.T,
	service *AgentNativeService,
	userID uint,
	name string,
	scopes ...string,
) *models.ServicePrincipal {
	t.Helper()
	principal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:   name,
		Scopes: scopes,
	})
	if err != nil {
		t.Fatalf("failed to create service principal: %v", err)
	}
	return principal
}

func seedNativeTicket(t *testing.T, db *gorm.DB, userID uint, number string) models.Ticket {
	t.Helper()
	ticket := models.Ticket{
		TicketNumber:       number,
		Title:              "Original title",
		Description:        "Untrusted customer text",
		Type:               models.TicketTypeRequest,
		Priority:           models.TicketPriorityNormal,
		Status:             models.TicketStatusOpen,
		Source:             models.TicketSourceAgent,
		Version:            1,
		TrustLevel:         models.TicketTrustLevelUntrusted,
		CreatedByID:        &userID,
		CreatedByActorType: models.ActorTypeHuman,
		CreatedByActorID:   models.HumanActor(userID).ID,
	}
	if db.Migrator().HasTable(&models.Project{}) {
		var project models.Project
		if err := db.Where("key = ?", models.ProjectKey("TEST")).
			First(&project).Error; err == nil {
			var queue models.Queue
			if err := db.Where(
				"project_id = ? AND key = ?",
				project.ID,
				models.QueueKey("default"),
			).First(&queue).Error; err != nil {
				t.Fatalf("load test project queue: %v", err)
			}
			ticket.OrganizationID = project.OrganizationID
			ticket.ProjectID = project.ID
			ticket.QueueID = queue.ID
			ticket.RequestTypeVersionID = defaultRequestTypeRequestVersionID
			ticket.WorkflowVersionID = defaultWorkflowVersionID
		}
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to seed ticket: %v", err)
	}
	return ticket
}

func TestCreateCommentRejectsReplyToReplyBeforeMutation(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "single-level-reply")
	ticket := seedNativeTicket(t, db, user.ID, "SINGLE-LEVEL-REPLY")
	actor := models.HumanActor(user.ID)
	ctx := testProjectOperationContext(t, db, actor)
	root := models.TicketComment{
		TicketID: ticket.ID, UserID: &user.ID,
		ActorType: actor.Type, ActorID: actor.ID,
		Content: "root", ContentType: "text", Type: models.CommentTypePublic,
	}
	if err := db.Create(&root).Error; err != nil {
		t.Fatal(err)
	}
	reply := root
	reply.ID = 0
	reply.Content = "reply"
	reply.ParentID = &root.ID
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	beforeComments := int64(0)
	if err := db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&beforeComments).Error; err != nil {
		t.Fatal(err)
	}

	service := NewAgentNativeService(db)
	_, err := service.CreateComment(ctx, NativeCommentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: ticket.Version,
		Actor:           actor,
		SourceProtocol:  "test",
		Content:         "must not become unreachable",
		ContentType:     "text",
		Type:            models.CommentTypePublic,
		ParentID:        &reply.ID,
	})
	if !errors.Is(err, ErrNestedCommentReply) ||
		AgentNativeErrorCode(err) != "nested_comment_reply" {
		t.Fatalf("nested reply error=%v code=%q", err, AgentNativeErrorCode(err))
	}
	var afterComments int64
	if err := db.Model(&models.TicketComment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&afterComments).Error; err != nil {
		t.Fatal(err)
	}
	var persisted models.Ticket
	if err := db.First(&persisted, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterComments != beforeComments || persisted.Version != ticket.Version {
		t.Fatalf(
			"nested reply mutated state: comments=%d->%d version=%d->%d",
			beforeComments,
			afterComments,
			ticket.Version,
			persisted.Version,
		)
	}
}

func TestAgentNativeCredentialHashValidationAndRevocation(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "credential")
	service := NewAgentNativeService(db, AgentNativeOptions{CredentialPepper: []byte("test-pepper")})
	principal := createNativePrincipal(t, service, user.ID, "credential-agent", models.ScopeTicketsRead)

	issued, err := service.IssueCredential(context.Background(), principal.ID, "integration", 5*time.Minute)
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	if issued.Secret == "" || issued.Token == "" {
		t.Fatal("expected one-time secret and token")
	}
	if issued.Credential.SecretHash == issued.Secret || strings.Contains(issued.Credential.SecretHash, issued.Secret) {
		t.Fatal("credential secret must not be stored in plaintext")
	}
	if _, _, err := service.ValidateCredentialToken(context.Background(), issued.Credential.ID+".wrong"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected invalid credential, got %v", err)
	}
	validatedPrincipal, validatedCredential, err := service.ValidateCredentialToken(context.Background(), issued.Token)
	if err != nil {
		t.Fatalf("validate credential: %v", err)
	}
	if validatedPrincipal.ID != principal.ID || validatedCredential.ID != issued.Credential.ID {
		t.Fatal("validated identity does not match issued credential")
	}

	rotated, err := service.RotateCredential(
		context.Background(),
		principal.ID,
		"rotated",
		5*time.Minute,
		models.HumanActor(user.ID),
	)
	if err != nil {
		t.Fatalf("rotate credential: %v", err)
	}
	if _, _, err := service.ValidateCredentialToken(context.Background(), issued.Token); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("rotated old credential must be rejected, got %v", err)
	}
	issued = rotated
	if err := service.RevokeCredential(context.Background(), issued.Credential.ID, models.HumanActor(user.ID)); err != nil {
		t.Fatalf("revoke credential: %v", err)
	}
	if _, _, err := service.ValidateCredentialToken(context.Background(), issued.Token); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("expected revoked credential to be rejected, got %v", err)
	}
	decision, err := service.CheckAction(context.Background(), PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		CredentialID:       issued.Credential.ID,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		ResourceID:         "1",
		SourceProtocol:     "test",
	})
	if !errors.Is(err, ErrInvalidCredential) ||
		decision == nil ||
		decision.ReasonCode != "invalid_credential" {
		t.Fatalf("revoked credential policy check = decision=%+v err=%v", decision, err)
	}
}

func TestAgentNativePolicyScopeDenyAndRiskyExplicitAllow(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "policy")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"policy-agent",
		models.ScopeTicketsUpdate,
		models.ScopeTicketsTransition,
	)

	ordinaryCheck := PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              models.ScopeTicketsUpdate,
		Action:             "ticket.update",
		ResourceType:       "ticket",
		ResourceID:         "1",
		IsWrite:            true,
	}
	ordinaryDecision, err := service.CheckAction(context.Background(), ordinaryCheck)
	if err != nil {
		t.Fatalf("ordinary granted scope should be allowed: %v", err)
	}
	riskyReuse := ordinaryCheck
	riskyReuse.IsRisky = true
	if err := service.validatePolicyDecision(
		context.Background(),
		ordinaryDecision.ID,
		models.ServicePrincipalActor(principal.ID),
		riskyReuse,
	); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("a non-risky decision must not authorize a risky action, got %v", err)
	}
	service.SetGlobalReadOnly(true)
	if err := service.validatePolicyDecision(
		context.Background(),
		ordinaryDecision.ID,
		models.ServicePrincipalActor(principal.ID),
		ordinaryCheck,
	); !errors.Is(err, ErrReadOnlyMode) {
		t.Fatalf("reused decisions must respect the live read-only switch, got %v", err)
	}
	service.SetGlobalReadOnly(false)
	if _, err := service.CheckAction(context.Background(), PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              models.ScopeTicketsTransition,
		Action:             "ticket.transition",
		ResourceType:       "ticket",
		ResourceID:         "1",
		IsWrite:            true,
		IsRisky:            true,
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("risky action without explicit allow must be denied, got %v", err)
	}
	if _, err := service.CreateAgentPolicy(context.Background(), CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "allow transition",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeTicketsTransition,
		Action:             "ticket.transition",
		ResourceType:       "ticket",
	}); err != nil {
		t.Fatalf("create allow policy: %v", err)
	}
	if _, err := service.CheckAction(context.Background(), PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              models.ScopeTicketsTransition,
		Action:             "ticket.transition",
		ResourceType:       "ticket",
		ResourceID:         "1",
		IsWrite:            true,
		IsRisky:            true,
	}); err != nil {
		t.Fatalf("explicitly allowed risky action should succeed: %v", err)
	}
	if _, err := service.CreateAgentPolicy(context.Background(), CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "deny ticket 42",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsUpdate,
		Action:             "ticket.update",
		ResourceType:       "ticket",
		ResourceID:         "42",
		Priority:           100,
	}); err != nil {
		t.Fatalf("create deny policy: %v", err)
	}
	decision, err := service.CheckAction(context.Background(), PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              models.ScopeTicketsUpdate,
		Action:             "ticket.update",
		ResourceType:       "ticket",
		ResourceID:         "42",
		IsWrite:            true,
	})
	if !errors.Is(err, ErrPolicyDenied) || decision == nil || decision.ReasonCode != "explicit_deny" {
		t.Fatalf("expected persisted explicit deny, decision=%+v err=%v", decision, err)
	}
	var count int64
	if err := db.Model(&models.PolicyDecision{}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("expected four persisted decisions, count=%d err=%v", count, err)
	}
}

func TestAgentNativeAutomationLoopDetection(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "loop")
	now := time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{
		LoopThreshold: 2,
		LoopWindow:    time.Minute,
		Now:           func() time.Time { return now },
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"loop-agent",
		models.ScopeTicketsUpdate,
	)
	check := PolicyCheckInput{
		ServicePrincipalID: principal.ID,
		Scope:              models.ScopeTicketsUpdate,
		Action:             "ticket.update",
		ResourceType:       "ticket",
		ResourceID:         "42",
		IsWrite:            true,
		RequestDigest:      "same-command-digest",
		SourceProtocol:     "test",
	}

	for attempt := 1; attempt <= 2; attempt++ {
		decision, err := service.CheckAction(context.Background(), check)
		if err != nil || decision == nil || !decision.Allowed {
			t.Fatalf("attempt %d should be allowed: decision=%+v err=%v", attempt, decision, err)
		}
	}
	decision, err := service.CheckAction(context.Background(), check)
	if !errors.Is(err, ErrAutomationLoop) ||
		decision == nil ||
		decision.Allowed ||
		decision.ReasonCode != "automation_loop" {
		t.Fatalf("third repeated command should trip loop breaker: decision=%+v err=%v", decision, err)
	}

	now = now.Add(2 * time.Minute)
	decision, err = service.CheckAction(context.Background(), check)
	if err != nil || decision == nil || !decision.Allowed {
		t.Fatalf("expired loop window should allow command again: decision=%+v err=%v", decision, err)
	}
}

func TestAgentNativeExecutionRateAndConcurrencyLimits(t *testing.T) {
	db := openAgentNativeTestDB(t)
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{Now: func() time.Time { return now }})
	concurrentPrincipal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "concurrency-agent",
		Scopes:             []string{models.ScopeTicketsRead},
		RateLimitPerMinute: 10,
		ConcurrentLimit:    1,
	})
	if err != nil {
		t.Fatalf("create concurrency principal: %v", err)
	}
	release, err := service.AcquireAgentExecution(context.Background(), concurrentPrincipal.ID)
	if err != nil {
		t.Fatalf("acquire execution slot: %v", err)
	}
	if _, err := service.AcquireAgentExecution(context.Background(), concurrentPrincipal.ID); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("expected concurrency rejection, got %v", err)
	}
	release()
	if releaseAgain, err := service.AcquireAgentExecution(context.Background(), concurrentPrincipal.ID); err != nil {
		t.Fatalf("released concurrency slot should be reusable: %v", err)
	} else {
		releaseAgain()
	}

	ratePrincipal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "rate-agent",
		Scopes:             []string{models.ScopeTicketsRead},
		RateLimitPerMinute: 1,
		ConcurrentLimit:    5,
	})
	if err != nil {
		t.Fatalf("create rate principal: %v", err)
	}
	rateRelease, err := service.AcquireAgentExecution(context.Background(), ratePrincipal.ID)
	if err != nil {
		t.Fatalf("acquire rate-limited execution: %v", err)
	}
	rateRelease()
	if _, err := service.AcquireAgentExecution(context.Background(), ratePrincipal.ID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected per-minute rate rejection, got %v", err)
	}
	now = now.Add(time.Minute)
	if nextWindowRelease, err := service.AcquireAgentExecution(context.Background(), ratePrincipal.ID); err != nil {
		t.Fatalf("next rate window should allow execution: %v", err)
	} else {
		nextWindowRelease()
	}
}

func TestAgentNativeExecutionConcurrencyPersistsAcrossRateWindows(t *testing.T) {
	db := openAgentNativeTestDB(t)
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{Now: func() time.Time { return now }})
	principal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "cross-window-concurrency-agent",
		Scopes:             []string{models.ScopeTicketsRead},
		RateLimitPerMinute: 10,
		ConcurrentLimit:    1,
	})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	releaseFirst, err := service.AcquireAgentExecution(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("acquire first execution: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := service.AcquireAgentExecution(context.Background(), principal.ID); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("long-running execution must remain counted after the rate window rolls over, got %v", err)
	}

	releaseFirst()
	releaseSecond, err := service.AcquireAgentExecution(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("released slot should be reusable in the new rate window: %v", err)
	}
	releaseFirst()
	if _, err := service.AcquireAgentExecution(context.Background(), principal.ID); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("repeated old release must not decrement the active execution, got %v", err)
	}
	releaseSecond()

	guard := service.executionGuard.(*InMemoryAgentExecutionGuard)
	guard.mu.Lock()
	_, retained := guard.inFlight[principal.ID]
	guard.mu.Unlock()
	if retained {
		t.Fatal("zero in-flight state must be removed")
	}
}

func TestAgentNativeExecutionReleaseIsConcurrentAndIdempotent(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	principal, err := service.CreateServicePrincipal(context.Background(), CreateServicePrincipalInput{
		Name:               "concurrent-release-agent",
		Scopes:             []string{models.ScopeTicketsRead},
		RateLimitPerMinute: 100,
		ConcurrentLimit:    1,
	})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}

	release, err := service.AcquireAgentExecution(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("acquire execution: %v", err)
	}
	var releases sync.WaitGroup
	for range 64 {
		releases.Add(1)
		go func() {
			defer releases.Done()
			release()
		}()
	}
	releases.Wait()

	nextRelease, err := service.AcquireAgentExecution(context.Background(), principal.ID)
	if err != nil {
		t.Fatalf("concurrent repeated release must free exactly one slot: %v", err)
	}
	defer nextRelease()
	if _, err := service.AcquireAgentExecution(context.Background(), principal.ID); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("active replacement execution must remain counted, got %v", err)
	}
}

func TestAgentNativeExecutionPrunesExpiredRateWindows(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "rate-window-cleanup")
	now := time.Date(2026, 7, 28, 13, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{Now: func() time.Time { return now }})
	first := createNativePrincipal(t, service, user.ID, "stale-rate-window", models.ScopeTicketsRead)
	second := createNativePrincipal(t, service, user.ID, "active-rate-window", models.ScopeTicketsRead)

	firstRelease, err := service.AcquireAgentExecution(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("acquire first principal: %v", err)
	}
	firstRelease()
	now = now.Add(time.Minute)
	secondRelease, err := service.AcquireAgentExecution(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("acquire second principal: %v", err)
	}
	defer secondRelease()

	guard := service.executionGuard.(*InMemoryAgentExecutionGuard)
	guard.mu.Lock()
	_, staleRetained := guard.rateWindows[first.ID]
	_, activeRetained := guard.rateWindows[second.ID]
	guard.mu.Unlock()
	if staleRetained {
		t.Fatal("expired rate window must be pruned")
	}
	if !activeRetained {
		t.Fatal("active rate window must be retained")
	}
}

func TestTicketChangeAuthorizationContractRejectsScopeConfusion(t *testing.T) {
	t.Run("ordinary update defaults safely", func(t *testing.T) {
		scope, action, risky, err := ticketChangeAuthorizationContract([]string{"title"}, "", "")
		if err != nil || scope != models.ScopeTicketsUpdate || action != "ticket.update" || risky {
			t.Fatalf("unexpected ordinary update contract: scope=%s action=%s risky=%v err=%v", scope, action, risky, err)
		}
	})
	t.Run("transition requires explicit transition scope", func(t *testing.T) {
		if _, _, _, err := ticketChangeAuthorizationContract([]string{"status"}, "", ""); !errors.Is(err, ErrCommandScopeMismatch) {
			t.Fatalf("missing transition scope must be rejected, got %v", err)
		}
		scope, action, risky, err := ticketChangeAuthorizationContract(
			[]string{"status"},
			models.ScopeTicketsTransition,
			"ticket.transition",
		)
		if err != nil || scope != models.ScopeTicketsTransition || action != "ticket.transition" || !risky {
			t.Fatalf("unexpected transition contract: scope=%s action=%s risky=%v err=%v", scope, action, risky, err)
		}
	})
	t.Run("assignment requires explicit assign scope", func(t *testing.T) {
		scope, action, risky, err := ticketChangeAuthorizationContract(
			[]string{"assigned_to_actor_type", "assigned_to_actor_id"},
			models.ScopeTicketsAssign,
			"ticket.assign",
		)
		if err != nil || scope != models.ScopeTicketsAssign || action != "ticket.assign" || !risky {
			t.Fatalf("unexpected assignment contract: scope=%s action=%s risky=%v err=%v", scope, action, risky, err)
		}
	})
	t.Run("permission categories cannot be mixed", func(t *testing.T) {
		if _, _, _, err := ticketChangeAuthorizationContract(
			[]string{"status", "title"},
			models.ScopeTicketsTransition,
			"ticket.transition",
		); !errors.Is(err, ErrCommandScopeMismatch) {
			t.Fatalf("mixed transition/update command must be rejected, got %v", err)
		}
		if _, _, _, err := ticketChangeAuthorizationContract(
			[]string{"status", "assigned_to_id"},
			models.ScopeTicketsTransition,
			"ticket.transition",
		); !errors.Is(err, ErrCommandScopeMismatch) {
			t.Fatalf("mixed transition/assignment command must be rejected, got %v", err)
		}
	})
	t.Run("escalation is an explicit privileged ordinary-field command", func(t *testing.T) {
		scope, action, risky, err := ticketChangeAuthorizationContract(
			[]string{"is_escalated", "priority"},
			models.ScopeTicketsTransition,
			"ticket.escalate",
		)
		if err != nil || scope != models.ScopeTicketsTransition || action != "ticket.escalate" || !risky {
			t.Fatalf("unexpected escalation contract: scope=%s action=%s risky=%v err=%v", scope, action, risky, err)
		}
		if _, _, _, err := ticketChangeAuthorizationContract(
			[]string{"title"},
			models.ScopeTicketsTransition,
			"ticket.escalate",
		); !errors.Is(err, ErrCommandScopeMismatch) {
			t.Fatalf("escalation scope must not authorize arbitrary updates, got %v", err)
		}
	})
}

func TestAgentNativeRejectsServicePrincipalProvenanceEscalation(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "provenance")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"provenance-agent",
		models.ScopeTicketsUpdate,
	)
	ticket := seedNativeTicket(t, db, user.ID, "AI-PROVENANCE-001")
	ctx := testProjectOperationContext(
		t,
		db,
		models.ServicePrincipalActor(principal.ID),
	)

	for field, value := range map[string]any{
		"source":      models.TicketSourceAPI,
		"trust_level": models.TicketTrustLevelSystem,
	} {
		_, err := service.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
			TicketID:        ticket.ID,
			ExpectedVersion: ticket.Version,
			Actor:           models.ServicePrincipalActor(principal.ID),
			RequiredScope:   models.ScopeTicketsUpdate,
			Action:          "ticket.update",
			Changes:         map[string]any{field: value},
		})
		if !errors.Is(err, ErrCommandScopeMismatch) {
			t.Fatalf("service principal changing %s should be rejected, got %v", field, err)
		}
	}
}

func TestAgentNativeIntakeCannotSmuggleAssignmentOrTransition(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "intake-scope")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"intake-agent",
		models.ScopeTicketsCreate,
	)
	ctx := testProjectOperationContext(
		t,
		db,
		models.ServicePrincipalActor(principal.ID),
	)
	closed := models.TicketStatusClosed
	for name, mutate := range map[string]func(*NativeTicketCreateInput){
		"transition": func(input *NativeTicketCreateInput) {
			input.Request.Status = &closed
		},
		"assignment": func(input *NativeTicketCreateInput) {
			assignee := models.HumanActor(user.ID)
			input.AssignedActor = &assignee
			input.Request.AssignedToID = &user.ID
		},
		"trusted provenance": func(input *NativeTicketCreateInput) {
			input.TrustLevel = models.TicketTrustLevelSystem
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := NativeTicketCreateInput{
				Request: models.TicketCreateRequest{
					Title: "Scoped intake", Description: "untrusted",
					Type: models.TicketTypeRequest, Priority: models.TicketPriorityNormal,
					Source: models.TicketSourceAgent,
				},
				Actor:      models.ServicePrincipalActor(principal.ID),
				TrustLevel: models.TicketTrustLevelUntrusted,
			}
			mutate(&input)
			if _, err := service.CreateNativeTicket(ctx, input); !errors.Is(err, ErrCommandScopeMismatch) {
				t.Fatalf("smuggled %s should be rejected, got %v", name, err)
			}
		})
	}
}

func TestAgentNativeIdempotencyReserveCompleteReplayAndConflict(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	actor := models.SystemActor("idempotency-test")
	body := []byte(`{"title":"same"}`)

	reservation, err := service.ReserveIdempotency(context.Background(), actor, "ticket.create", "key-1", body, time.Hour)
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}
	if _, err := service.ReserveIdempotency(context.Background(), actor, "ticket.create", "key-1", body, time.Hour); !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("expected in-progress duplicate, got %v", err)
	}
	receipt := OperationReceipt{OperationID: "op-1", ResourceID: "7", ResourceVersion: 1, EventID: "event-1"}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return service.CompleteIdempotencyTx(context.Background(), tx, reservation.Record.ID, 201, receipt, "7", "event-1")
	}); err != nil {
		t.Fatalf("complete idempotency: %v", err)
	}
	replayed, err := service.ReserveIdempotency(context.Background(), actor, "ticket.create", "key-1", body, time.Hour)
	if err != nil {
		t.Fatalf("replay idempotency: %v", err)
	}
	if !replayed.Replayed || replayed.Record.State != models.IdempotencyStateCompleted {
		t.Fatalf("expected completed replay, got %+v", replayed)
	}
	var decoded OperationReceipt
	if err := json.Unmarshal(replayed.Record.ResponseBody, &decoded); err != nil || decoded.ResourceID != "7" {
		t.Fatalf("unexpected replay body: %+v err=%v", decoded, err)
	}
	if _, err := service.ReserveIdempotency(
		context.Background(),
		actor,
		"ticket.create",
		"key-1",
		[]byte(`{"title":"different"}`),
		time.Hour,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected request hash conflict, got %v", err)
	}
}

func TestUniqueConstraintDetectionHandlesTranslatedGORMError(t *testing.T) {
	if !isUniqueConstraintError(gorm.ErrDuplicatedKey) {
		t.Fatal("translated GORM duplicate-key error must enter idempotent replay path")
	}
	if isUniqueConstraintError(errors.New("duplicated key not allowed")) {
		t.Fatal("untyped duplicate-key text must not bypass constraint verification")
	}
	if isUniqueConstraintError(errors.New("database unavailable")) {
		t.Fatal("non-unique database error must not be treated as an idempotent replay")
	}
}

func TestAgentNativeIdempotencyProcessingLeaseRejectsThenAtomicallyRecovers(t *testing.T) {
	db := openAgentNativeTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	const processingLease = 2 * time.Minute
	const completedRetention = 24 * time.Hour
	service := NewAgentNativeService(db, AgentNativeOptions{
		IdempotencyProcessingLease: processingLease,
		Now:                        func() time.Time { return now },
	})
	actor := models.SystemActor("idempotency-lease-test")
	body := []byte(`{"title":"recover after crash"}`)

	crashed, err := service.ReserveIdempotency(
		context.Background(),
		actor,
		"ticket.create",
		"lease-recovery-key",
		body,
		completedRetention,
	)
	if err != nil {
		t.Fatalf("reserve crashed operation: %v", err)
	}
	if want := now.Add(processingLease); !crashed.Record.ExpiresAt.Equal(want) {
		t.Fatalf("processing expiry=%s, want short lease ending %s", crashed.Record.ExpiresAt, want)
	}
	if crashed.Record.CompletionTTLNanoseconds != completedRetention.Nanoseconds() {
		t.Fatalf(
			"completion retention=%s, want %s",
			time.Duration(crashed.Record.CompletionTTLNanoseconds),
			completedRetention,
		)
	}
	// Preserve recovery for processing rows written before the lease/retention
	// split, whose expires_at may still contain the full 24-hour retention.
	if err := db.Model(&models.IdempotencyRecord{}).
		Where("id = ?", crashed.Record.ID).
		UpdateColumns(map[string]any{
			"expires_at": now.Add(completedRetention),
			"updated_at": now,
		}).Error; err != nil {
		t.Fatalf("simulate legacy 24-hour processing lock: %v", err)
	}

	now = now.Add(processingLease - time.Second)
	if _, err := service.ReserveIdempotency(
		context.Background(),
		actor,
		"ticket.create",
		"lease-recovery-key",
		body,
		completedRetention,
	); !errors.Is(err, ErrIdempotencyInProgress) {
		t.Fatalf("unexpired processing lease should reject duplicate, got %v", err)
	}

	// Simulate the first process disappearing without calling FailIdempotency.
	// Once the short lease expires, concurrent recovery attempts must have one
	// winner, and the replacement ID fences the crashed worker from completing.
	now = now.Add(2 * time.Second)
	type reserveResult struct {
		reservation *IdempotencyReservation
		err         error
	}
	start := make(chan struct{})
	results := make(chan reserveResult, 2)
	for range 2 {
		go func() {
			<-start
			reservation, reserveErr := service.ReserveIdempotency(
				context.Background(),
				actor,
				"ticket.create",
				"lease-recovery-key",
				body,
				completedRetention,
			)
			results <- reserveResult{reservation: reservation, err: reserveErr}
		}()
	}
	close(start)

	var winner *IdempotencyReservation
	inProgress := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			if winner != nil {
				t.Fatal("more than one concurrent recovery acquired the processing lease")
			}
			winner = result.reservation
		case errors.Is(result.err, ErrIdempotencyInProgress):
			inProgress++
		default:
			t.Fatalf("unexpected concurrent recovery error: %v", result.err)
		}
	}
	if winner == nil || inProgress != 1 {
		t.Fatalf("atomic recovery winner=%+v in_progress=%d", winner, inProgress)
	}
	if winner.Record.ID == crashed.Record.ID {
		t.Fatal("takeover must rotate the record ID to fence the crashed worker")
	}
	if want := now.Add(processingLease); !winner.Record.ExpiresAt.Equal(want) {
		t.Fatalf("recovered processing expiry=%s, want %s", winner.Record.ExpiresAt, want)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return service.CompleteIdempotencyTx(
			context.Background(),
			tx,
			crashed.Record.ID,
			http.StatusCreated,
			OperationReceipt{OperationID: "stale"},
			"",
			"",
		)
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("crashed lease holder should be fenced after takeover, got %v", err)
	}
	var currentOwner models.IdempotencyRecord
	if err := db.First(&currentOwner, "id = ?", winner.Record.ID).Error; err != nil {
		t.Fatalf("load current processing owner after stale completion: %v", err)
	}
	if currentOwner.State != models.IdempotencyStateProcessing {
		t.Fatalf("stale completion overwrote the current owner: %+v", currentOwner)
	}
}

func TestAgentNativeIdempotencyCompletionExtendsCallerRetentionAndReplays(t *testing.T) {
	db := openAgentNativeTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	const completedRetention = 24 * time.Hour
	service := NewAgentNativeService(db, AgentNativeOptions{
		IdempotencyProcessingLease: 2 * time.Minute,
		Now:                        func() time.Time { return now },
	})
	actor := models.SystemActor("idempotency-retention-test")
	body := []byte(`{"title":"retain completion"}`)

	reservation, err := service.ReserveIdempotency(
		context.Background(),
		actor,
		"ticket.create",
		"completed-retention-key",
		body,
		completedRetention,
	)
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}
	now = now.Add(time.Minute)
	receipt := OperationReceipt{OperationID: "retained-operation", ResourceID: "42"}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return service.CompleteIdempotencyTx(
			context.Background(),
			tx,
			reservation.Record.ID,
			http.StatusCreated,
			receipt,
			"42",
			"",
		)
	}); err != nil {
		t.Fatalf("complete idempotency: %v", err)
	}

	var completed models.IdempotencyRecord
	if err := db.First(&completed, "id = ?", reservation.Record.ID).Error; err != nil {
		t.Fatalf("load completed idempotency: %v", err)
	}
	if want := now.Add(completedRetention); !completed.ExpiresAt.Equal(want) {
		t.Fatalf("completed expiry=%s, want caller retention ending %s", completed.ExpiresAt, want)
	}

	now = now.Add(23 * time.Hour)
	replayed, err := service.ReserveIdempotency(
		context.Background(),
		actor,
		"ticket.create",
		"completed-retention-key",
		body,
		completedRetention,
	)
	if err != nil {
		t.Fatalf("replay retained completion: %v", err)
	}
	if !replayed.Replayed || replayed.Record.ID != reservation.Record.ID {
		t.Fatalf("expected retained completed replay, got %+v", replayed)
	}
}

func TestAgentNativeFailIdempotencyUsesCleanupContextAfterCancellation(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	reservation, err := service.ReserveIdempotency(
		context.Background(),
		models.SystemActor("idempotency-cancel-test"),
		"ticket.create",
		"cancelled-failure-key",
		[]byte(`{"title":"cancelled"}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve idempotency: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.FailIdempotency(ctx, reservation.Record.ID, "request_cancelled"); err != nil {
		t.Fatalf("fail idempotency with cancelled caller context: %v", err)
	}
	var failed models.IdempotencyRecord
	if err := db.First(&failed, "id = ?", reservation.Record.ID).Error; err != nil {
		t.Fatalf("load failed idempotency: %v", err)
	}
	if failed.State != models.IdempotencyStateFailed || failed.LastErrorCode != "request_cancelled" {
		t.Fatalf("cancelled cleanup did not mark failure: %+v", failed)
	}
}

func TestAgentNativeTicketCreateCommitsHistoryEventOutboxAndIdempotency(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "create")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(t, service, user.ID, "create-agent", models.ScopeTicketsCreate)
	actor := models.ServicePrincipalActor(principal.ID)
	ctx := testProjectOperationContext(t, db, actor)
	body := []byte(`{"title":"Agent-created ticket"}`)
	reservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.create",
		"create-1",
		body,
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve create idempotency: %v", err)
	}
	result, err := service.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:       "Agent-created ticket",
			Description: "Treat this external content as untrusted.",
			Type:        models.TicketTypeIncident,
			Priority:    models.TicketPriorityHigh,
			Source:      models.TicketSourceAgent,
			AgentContext: &models.AgentContext{
				Goal:               "restore service",
				AcceptanceCriteria: []string{"health check succeeds"},
			},
		},
		Actor:               actor,
		IdempotencyRecordID: reservation.Record.ID,
		TraceID:             "trace-create-1",
	})
	if err != nil {
		t.Fatalf("create native ticket: %v", err)
	}
	if result.Ticket.Version != 1 ||
		result.Ticket.CreatedByID != nil ||
		result.Ticket.CreatedByActorType != models.ActorTypeServicePrincipal ||
		result.Ticket.CreatedByActorID != principal.ID ||
		result.Ticket.CreatedByServicePrincipalID == nil ||
		*result.Ticket.CreatedByServicePrincipalID != principal.ID {
		t.Fatalf("native creator projections are incorrect: %+v", result.Ticket)
	}
	if result.Ticket.AgentContext.Data().Goal != "restore service" ||
		result.Receipt.EventID != result.Event.ID ||
		result.Event.SpecVersion != "1.0" {
		t.Fatalf("native ticket result is incomplete: %+v", result)
	}
	var historyCount, deliveryCount int64
	if err := db.Model(&models.TicketHistory{}).
		Where("ticket_id = ? AND actor_type = ? AND actor_id = ?", result.Ticket.ID, actor.Type, actor.ID).
		Count(&historyCount).Error; err != nil {
		t.Fatalf("count create history: %v", err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Where("event_id = ?", result.Event.ID).
		Count(&deliveryCount).Error; err != nil {
		t.Fatalf("count create outbox: %v", err)
	}
	if historyCount != 1 || deliveryCount != 1 {
		t.Fatalf("history/event/outbox must commit together: history=%d delivery=%d", historyCount, deliveryCount)
	}
	var createHistory models.TicketHistory
	if err := db.Where("ticket_id = ?", result.Ticket.ID).First(&createHistory).Error; err != nil {
		t.Fatalf("load create history: %v", err)
	}
	assertTicketHistoryEventLink(t, &createHistory, result.Event)
	replayed, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.create",
		"create-1",
		body,
		time.Hour,
	)
	if err != nil || !replayed.Replayed || replayed.Record.EventID != result.Event.ID {
		t.Fatalf("create command should replay original receipt: replay=%+v err=%v", replayed, err)
	}
}

func TestAgentNativeEventOutboxRollbackRetryAndRecovery(t *testing.T) {
	db := openAgentNativeTestDB(t)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{
		Now:                  func() time.Time { return now },
		DefaultOutboxTargets: []OutboxTarget{{Type: "event_stream", ID: "primary", MaxAttempts: 3}},
	})
	ctx := testProjectOperationContext(
		t,
		db,
		models.SystemActor("test"),
	)
	rollbackErr := errors.New("force rollback")
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := service.AppendDomainEventTx(ctx, tx, DomainEventInput{
			Type:            "io.chronodesk.ticket.test.v1",
			Subject:         "ticket/1",
			Actor:           models.SystemActor("test"),
			ResourceVersion: 1,
			Data:            map[string]any{"ticket_id": 1},
		}, nil); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback error, got %v", err)
	}
	var eventCount, deliveryCount int64
	_ = db.Model(&models.DomainEvent{}).Count(&eventCount).Error
	_ = db.Model(&models.OutboxDelivery{}).Count(&deliveryCount).Error
	if eventCount != 0 || deliveryCount != 0 {
		t.Fatalf("event and outbox must roll back together: events=%d deliveries=%d", eventCount, deliveryCount)
	}

	event, err := service.createDomainEvent(t, ctx, DomainEventInput{
		Type:            "io.chronodesk.ticket.test.v1",
		Subject:         "ticket/1",
		Actor:           models.SystemActor("test"),
		ResourceVersion: 1,
		Data:            map[string]any{"ticket_id": 1},
	}, nil)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	attempts := 0
	deliverer := OutboxDeliverFunc(func(_ context.Context, _ *models.OutboxDelivery, envelope CloudEventEnvelope) error {
		attempts++
		if envelope.SpecVersion != "1.0" || envelope.ID != event.ID {
			t.Fatalf("invalid CloudEvents envelope: %+v", envelope)
		}
		if attempts == 1 {
			return errors.New("temporary destination failure")
		}
		return nil
	})
	first, err := service.ProcessOutboxBatch(context.Background(), "worker-1", 10, deliverer)
	if err != nil {
		t.Fatalf("process failed batch: %v", err)
	}
	if first.Claimed != 1 || first.Failed != 1 {
		t.Fatalf("unexpected first batch result: %+v", first)
	}

	now = now.Add(3 * time.Second)
	second, err := service.ProcessOutboxBatch(context.Background(), "worker-2", 10, deliverer)
	if err != nil {
		t.Fatalf("process recovered batch: %v", err)
	}
	if second.Delivered != 1 || attempts != 2 {
		t.Fatalf("expected recovered delivery, result=%+v attempts=%d", second, attempts)
	}
	var persisted models.DomainEvent
	if err := db.First(&persisted, "id = ?", event.ID).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	if persisted.PublishedAt == nil {
		t.Fatal("event should be marked published after all targets succeed")
	}
}

func TestAppendDomainEventPersistsAuditLedgerInSameTransaction(t *testing.T) {
	db := openAgentNativeTestDB(t)
	ledger, err := NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AuditLedger: ledger,
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "event_stream", ID: "default", MaxAttempts: 3},
		},
	})
	scope := models.ProjectScope{OrganizationID: 4, ProjectID: 40}
	actor := models.HumanActor(42)
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:         scope,
		Actor:         actor,
		Source:        SourceProtocolHumanREST,
		TraceID:       "trace-audit-1",
		CorrelationID: "correlation-audit-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var event *models.DomainEvent
	if err := db.Transaction(func(tx *gorm.DB) error {
		var appendErr error
		event, appendErr = service.AppendDomainEventTx(
			ctx,
			tx,
			DomainEventInput{
				Type:            "io.chronodesk.ticket.audited.v1",
				Subject:         "ticket/77",
				Actor:           actor,
				ResourceVersion: 8,
				Scope:           scope,
				Data:            map[string]any{"ticket_id": 77},
			},
			nil,
		)
		return appendErr
	}); err != nil {
		t.Fatal(err)
	}

	var entry models.AuditLedgerEntry
	if err := db.Where(
		"organization_id = ? AND project_id = ? AND domain_event_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
		event.ID,
	).First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.Sequence != 1 ||
		entry.EventType != event.Type ||
		entry.ResourceVersion != event.ResourceVersion ||
		entry.Actor() != actor ||
		entry.TraceID != "trace-audit-1" ||
		entry.CorrelationID != "correlation-audit-1" {
		t.Fatalf("unexpected audit ledger entry: %+v", entry)
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("invalid audit ledger entry: %v", err)
	}
}

func TestServicePrincipalWebhookFanoutRequiresSeparateExplicitPolicy(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "external-notification")
	service := NewAgentNativeService(db, AgentNativeOptions{
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "event_stream", ID: "default"},
			{Type: "webhook", ID: "configured"},
		},
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"external-notification-agent",
		models.ScopeTicketsCreate,
	)
	ctx := testProjectOperationContext(
		t,
		db,
		models.ServicePrincipalActor(principal.ID),
	)
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config := models.WebhookConfig{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "agent-native-policy-snapshot",
		Provider:       models.WebhookProviderCustom,
		WebhookURL:     "https://webhook.example.test/events",
		Status:         models.WebhookStatusActive,
		EnabledEventsObj: []models.WebhookEventType{
			models.WebhookEventTicketCreated,
		},
		RetryCount: 2,
		CreatedBy:  user.ID,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	create := func(title, digest string) *NativeTicketCreateResult {
		t.Helper()
		result, err := service.CreateNativeTicket(ctx, NativeTicketCreateInput{
			Request: models.TicketCreateRequest{
				Title: title, Description: "untrusted", Type: models.TicketTypeRequest,
				Priority: models.TicketPriorityNormal, Source: models.TicketSourceAgent,
			},
			Actor:          models.ServicePrincipalActor(principal.ID),
			SourceProtocol: "test",
			RequestDigest:  digest,
			TrustLevel:     models.TicketTrustLevelUntrusted,
		})
		if err != nil {
			t.Fatalf("create ticket %q: %v", title, err)
		}
		return result
	}

	withoutPolicy := create("internal event only", "external-notification-denied")
	var webhookCount int64
	if err := db.Model(&models.OutboxDelivery{}).
		Where("event_id = ? AND destination_type = ?", withoutPolicy.Event.ID, "webhook").
		Count(&webhookCount).Error; err != nil {
		t.Fatal(err)
	}
	if webhookCount != 0 {
		t.Fatalf("service-principal event created %d webhook deliveries without explicit policy", webhookCount)
	}

	scopes, _ := json.Marshal([]string{models.ScopeTicketsCreate, models.ScopeEventsSubscribe})
	if err := db.Model(&models.ServicePrincipal{}).
		Where("id = ?", principal.ID).
		Update("scopes", datatypes.JSON(scopes)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAgentPolicy(context.Background(), CreateAgentPolicyInput{
		ServicePrincipalID: principal.ID,
		Name:               "allow external ticket notifications",
		Effect:             models.AgentPolicyEffectAllow,
		Scope:              models.ScopeEventsSubscribe,
		Action:             externalNotificationAction,
		ResourceType:       "ticket",
	}); err != nil {
		t.Fatal(err)
	}

	withPolicy := create("explicit external event", "external-notification-allowed")
	if err := db.Model(&models.OutboxDelivery{}).
		Where("event_id = ? AND destination_type = ?", withPolicy.Event.ID, "webhook").
		Count(&webhookCount).Error; err != nil {
		t.Fatal(err)
	}
	if webhookCount != 1 {
		t.Fatalf("explicit external policy created %d webhook deliveries, want 1", webhookCount)
	}
	var snapshot models.WebhookDeliverySnapshot
	if err := db.Where(
		"event_id = ? AND config_id = ?",
		withPolicy.Event.ID,
		config.ID,
	).First(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		withPolicy.Event.ID,
		"webhook",
	).First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.DestinationID != webhookSnapshotDestinationPrefix+snapshot.ID ||
		delivery.MaxAttempts != config.RetryCount+1 {
		t.Fatalf("webhook delivery did not bind immutable snapshot: %+v", delivery)
	}
}

func TestCloudEventWireUsesCompliantScalarExtensions(t *testing.T) {
	event := &models.DomainEvent{
		ID:              "event-wire-contract",
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test",
		Type:            "io.chronodesk.ticket.updated.v1",
		Subject:         "ticket/42",
		Time:            time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
		DataContentType: "application/json",
		DataSchema:      "urn:chronodesk:schema:domain-event-data:v1",
		Data: datatypes.JSON(`{
			"ticket_id":42,
			"actor":{"type":"human","id":"spoofed"},
			"_attachment_cleanup_objects":[{"attachment_id":1,"storage_path":"private"}]
		}`),
		TraceID:         "trace-1",
		CorrelationID:   "correlation-1",
		CausationID:     "causation-1",
		ActorType:       models.ActorTypeServicePrincipal,
		ActorID:         "principal-1",
		ResourceVersion: 8,
	}
	envelope := CloudEventFromModel(event)
	wire, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"trace_id",
		"correlation_id",
		"causation_id",
		"actor",
		"resource_version",
	} {
		if _, exists := fields[invalid]; exists {
			t.Fatalf("invalid CloudEvents context attribute %q leaked to wire: %s", invalid, wire)
		}
	}
	for _, required := range []string{
		"traceid",
		"correlationid",
		"causationid",
		"actortype",
		"actorid",
		"resourceversion",
	} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("required CloudEvents extension %q missing: %s", required, wire)
		}
	}
	var publicData struct {
		Actor models.ActorRef `json:"actor"`
	}
	if err := json.Unmarshal(envelope.Data, &publicData); err != nil {
		t.Fatal(err)
	}
	if publicData.Actor != models.ServicePrincipalActor("principal-1") {
		t.Fatalf("public event data contains non-authoritative actor: %+v", publicData.Actor)
	}
	if strings.Contains(string(envelope.Data), AttachmentCleanupObjectsDataField) ||
		!strings.Contains(string(envelope.InternalData), AttachmentCleanupObjectsDataField) {
		t.Fatal("CloudEvent cleanup manifest sanitization changed")
	}
}

func TestProcessOutboxBatchStartsIndependentDeliveriesConcurrently(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db)
	_, err := service.createDomainEvent(t, context.Background(), DomainEventInput{
		Type:            "io.chronodesk.ticket.test.v1",
		Subject:         "ticket/1",
		Actor:           models.SystemActor("test"),
		ResourceVersion: 1,
		Data:            map[string]any{"ticket_id": 1},
	}, []OutboxTarget{
		{Type: "webhook", ID: "slow", MaxAttempts: 2},
		{Type: "webhook", ID: "fast", MaxAttempts: 2},
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	fastStarted := make(chan struct{})
	deliverer := OutboxDeliverFunc(func(
		_ context.Context,
		delivery *models.OutboxDelivery,
		_ CloudEventEnvelope,
	) error {
		switch delivery.DestinationID {
		case "slow":
			close(slowStarted)
			<-releaseSlow
		case "fast":
			close(fastStarted)
		}
		return nil
	})
	type batchResult struct {
		result OutboxBatchResult
		err    error
	}
	done := make(chan batchResult, 1)
	go func() {
		result, processErr := service.ProcessOutboxBatch(context.Background(), "worker-concurrent", 10, deliverer)
		done <- batchResult{result: result, err: processErr}
	}()

	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow delivery did not start")
	}
	fastStartedBeforeRelease := false
	select {
	case <-fastStarted:
		fastStartedBeforeRelease = true
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseSlow)
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("process batch: %v", outcome.err)
	}
	if !fastStartedBeforeRelease {
		t.Fatal("slow Outbox target blocked an independent delivery")
	}
	if outcome.result.Delivered != 2 {
		t.Fatalf("unexpected batch result: %+v", outcome.result)
	}
}

func TestAgentNativeLeaseVersionConflictAndIdempotentUpdate(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "lease")
	service := NewAgentNativeService(db)
	first := createNativePrincipal(
		t,
		service,
		user.ID,
		"lease-agent-a",
		models.ScopeTicketsUpdate,
		models.ScopeTicketsTransition,
	)
	second := createNativePrincipal(t, service, user.ID, "lease-agent-b", models.ScopeTicketsUpdate)
	ticket := seedNativeTicket(t, db, user.ID, "AI-LEASE-001")
	firstActor := models.ServicePrincipalActor(first.ID)
	secondActor := models.ServicePrincipalActor(second.ID)
	firstCtx := testProjectOperationContext(t, db, firstActor)
	secondCtx := testProjectOperationContext(t, db, secondActor)

	lease, err := service.claimTicketLease(firstCtx, ticket.ID, firstActor, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim first lease: %v", err)
	}
	if _, err := service.claimTicketLease(secondCtx, ticket.ID, secondActor, 1, time.Minute); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("expected competing lease conflict, got %v", err)
	}
	reservation, err := service.ReserveIdempotency(
		firstCtx,
		firstActor,
		"ticket.update",
		"update-1",
		[]byte(`{"title":"Updated"}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve update idempotency: %v", err)
	}
	updated, err := service.UpdateTicketVersion(firstCtx, VersionedTicketUpdateInput{
		TicketID:            ticket.ID,
		ExpectedVersion:     1,
		LeaseID:             lease.ID,
		Actor:               firstActor,
		Changes:             map[string]any{"title": "Updated"},
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		t.Fatalf("versioned update: %v", err)
	}
	if updated.Ticket.Version != 2 || updated.Ticket.Title != "Updated" || updated.Receipt.ResourceVersion != 2 {
		t.Fatalf("unexpected updated ticket: %+v", updated)
	}
	var history models.TicketHistory
	if err := db.Where("ticket_id = ? AND action = ?", ticket.ID, models.HistoryActionUpdate).
		Order("id DESC").
		First(&history).Error; err != nil {
		t.Fatalf("load update history: %v", err)
	}
	var details struct {
		Changes map[string]struct {
			Old any `json:"old"`
			New any `json:"new"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(history.Details), &details); err != nil {
		t.Fatalf("decode history change set: %v", err)
	}
	if details.Changes["title"].Old != "Original title" || details.Changes["title"].New != "Updated" {
		t.Fatalf("history must persist the change set: %+v", details.Changes)
	}
	assertTicketHistoryEventLink(t, &history, updated.Event)
	if _, err := service.UpdateTicketVersion(firstCtx, VersionedTicketUpdateInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 2,
		LeaseID:         lease.ID,
		Actor:           firstActor,
		RequiredScope:   models.ScopeTicketsTransition,
		Action:          "ticket.transition",
		Changes:         map[string]any{"status": models.TicketStatusInProgress},
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("transition scope without explicit allow must still be denied, got %v", err)
	}
	if _, err := service.UpdateTicketVersion(firstCtx, VersionedTicketUpdateInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 1,
		LeaseID:         lease.ID,
		Actor:           firstActor,
		Changes:         map[string]any{"title": "Stale"},
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected stale version conflict, got %v", err)
	}
	if _, err := service.heartbeatTicketLease(firstCtx, lease.ID, firstActor, 2, time.Minute); err != nil {
		t.Fatalf("heartbeat updated lease: %v", err)
	}
	if err := service.releaseTicketLease(firstCtx, lease.ID, firstActor, "done"); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if _, err := service.claimTicketLease(secondCtx, ticket.ID, secondActor, 2, time.Minute); err != nil {
		t.Fatalf("second actor should claim released lease: %v", err)
	}
	replayed, err := service.ReserveIdempotency(
		firstCtx,
		firstActor,
		"ticket.update",
		"update-1",
		[]byte(`{"title":"Updated"}`),
		time.Hour,
	)
	if err != nil || !replayed.Replayed {
		t.Fatalf("expected completed update replay, replay=%+v err=%v", replayed, err)
	}
}

func TestAgentNativeLeaseCommandsAreAtomicWithEventOutboxAndIdempotency(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "lease-command")
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)
	service := NewAgentNativeService(db, AgentNativeOptions{Now: func() time.Time { return now }})
	principal := createNativePrincipal(t, service, user.ID, "lease-command-agent", models.ScopeTasksManage)
	actor := models.ServicePrincipalActor(principal.ID)
	ticket := seedNativeTicket(t, db, user.ID, "AI-LEASE-COMMAND-001")
	ctx := testProjectOperationContext(t, db, actor)

	claimReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.claim",
		"claim-command-1",
		[]byte(`{"ttl_seconds":60}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve claim command: %v", err)
	}
	claimed, err := service.ClaimTicketLeaseCommand(ctx, ClaimTicketLeaseCommandInput{
		TicketID:            ticket.ID,
		Actor:               actor,
		ExpectedVersion:     1,
		TTL:                 time.Minute,
		IdempotencyRecordID: claimReservation.Record.ID,
		TraceID:             "trace-claim",
	})
	if err != nil {
		t.Fatalf("claim lease command: %v", err)
	}
	if claimed.Event.Type != "io.chronodesk.ticket.lease.claimed.v1" ||
		claimed.Receipt.EventID != claimed.Event.ID ||
		claimed.Receipt.PolicyDecisionID == "" {
		t.Fatalf("claim command result is incomplete: %+v", claimed)
	}
	var claimRecord models.IdempotencyRecord
	if err := db.First(&claimRecord, "id = ?", claimReservation.Record.ID).Error; err != nil {
		t.Fatalf("load claim idempotency: %v", err)
	}
	if claimRecord.State != models.IdempotencyStateCompleted ||
		claimRecord.EventID != claimed.Event.ID ||
		len(claimRecord.ResourceSnapshot) == 0 {
		t.Fatalf("claim idempotency was not completed atomically: %+v", claimRecord)
	}

	now = now.Add(10 * time.Second)
	failedHeartbeatReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.lease.heartbeat",
		"heartbeat-command-failed",
		[]byte(claimed.Lease.ID),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve failed heartbeat: %v", err)
	}
	originalExpiry := claimed.Lease.ExpiresAt
	if _, err := service.HeartbeatTicketLeaseCommand(ctx, HeartbeatTicketLeaseCommandInput{
		LeaseID:             claimed.Lease.ID,
		Actor:               actor,
		ExpectedVersion:     1,
		TTL:                 2 * time.Minute,
		IdempotencyRecordID: failedHeartbeatReservation.Record.ID,
		OutboxTargets:       []OutboxTarget{{Type: "webhook"}},
	}); err == nil {
		t.Fatal("invalid outbox target should fail heartbeat command")
	}
	var rolledBackLease models.TicketLease
	if err := db.First(&rolledBackLease, "id = ?", claimed.Lease.ID).Error; err != nil {
		t.Fatalf("reload lease after failed heartbeat: %v", err)
	}
	if !rolledBackLease.ExpiresAt.Equal(originalExpiry) {
		t.Fatalf("heartbeat must roll back with outbox failure: before=%s after=%s", originalExpiry, rolledBackLease.ExpiresAt)
	}
	_ = service.FailIdempotency(ctx, failedHeartbeatReservation.Record.ID, "test_failure")

	heartbeatReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.lease.heartbeat",
		"heartbeat-command-1",
		[]byte(claimed.Lease.ID),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve heartbeat command: %v", err)
	}
	heartbeat, err := service.HeartbeatTicketLeaseCommand(ctx, HeartbeatTicketLeaseCommandInput{
		LeaseID:             claimed.Lease.ID,
		Actor:               actor,
		ExpectedVersion:     1,
		TTL:                 2 * time.Minute,
		IdempotencyRecordID: heartbeatReservation.Record.ID,
	})
	if err != nil {
		t.Fatalf("heartbeat lease command: %v", err)
	}
	if !heartbeat.Lease.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("heartbeat expiry was not committed: %+v", heartbeat.Lease)
	}

	failedReleaseReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.lease.release",
		"release-command-failed",
		[]byte(claimed.Lease.ID),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve failed release: %v", err)
	}
	if _, err := service.ReleaseTicketLeaseCommand(ctx, ReleaseTicketLeaseCommandInput{
		LeaseID:             claimed.Lease.ID,
		Actor:               actor,
		Reason:              "done",
		IdempotencyRecordID: failedReleaseReservation.Record.ID,
		OutboxTargets:       []OutboxTarget{{Type: "webhook"}},
	}); err == nil {
		t.Fatal("invalid outbox target should fail release command")
	}
	if err := db.First(&rolledBackLease, "id = ?", claimed.Lease.ID).Error; err != nil {
		t.Fatalf("reload lease after failed release: %v", err)
	}
	if rolledBackLease.ReleasedAt != nil {
		t.Fatalf("release must roll back with outbox failure: %+v", rolledBackLease)
	}
	_ = service.FailIdempotency(ctx, failedReleaseReservation.Record.ID, "test_failure")

	releaseReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.lease.release",
		"release-command-1",
		[]byte(claimed.Lease.ID),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve release command: %v", err)
	}
	released, err := service.ReleaseTicketLeaseCommand(ctx, ReleaseTicketLeaseCommandInput{
		LeaseID:             claimed.Lease.ID,
		Actor:               actor,
		Reason:              "completed",
		IdempotencyRecordID: releaseReservation.Record.ID,
	})
	if err != nil {
		t.Fatalf("release lease command: %v", err)
	}
	if released.Lease.ReleasedAt == nil || released.Event.Type != "io.chronodesk.ticket.lease.released.v1" {
		t.Fatalf("release command did not commit lease and event: %+v", released)
	}

	secondTicket := seedNativeTicket(t, db, user.ID, "AI-LEASE-COMMAND-002")
	failedClaimReservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.claim",
		"claim-command-failed",
		[]byte(`{"ttl_seconds":60}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve failed claim: %v", err)
	}
	if _, err := service.ClaimTicketLeaseCommand(ctx, ClaimTicketLeaseCommandInput{
		TicketID:            secondTicket.ID,
		Actor:               actor,
		ExpectedVersion:     1,
		TTL:                 time.Minute,
		IdempotencyRecordID: failedClaimReservation.Record.ID,
		OutboxTargets:       []OutboxTarget{{Type: "webhook"}},
	}); err == nil {
		t.Fatal("invalid outbox target should fail claim command")
	}
	var leaseCount int64
	if err := db.Model(&models.TicketLease{}).Where("ticket_id = ?", secondTicket.ID).Count(&leaseCount).Error; err != nil {
		t.Fatalf("count rolled back claim lease: %v", err)
	}
	if leaseCount != 0 {
		t.Fatalf("claim must roll back with outbox failure, leases=%d", leaseCount)
	}
}

func TestAgentNativeCommentActorEventAndIdempotency(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "comment")
	service := NewAgentNativeService(db)
	principal := createNativePrincipal(t, service, user.ID, "comment-agent", models.ScopeCommentsWrite)
	ticket := seedNativeTicket(t, db, user.ID, "AI-COMMENT-001")
	actor := models.ServicePrincipalActor(principal.ID)
	ctx := testProjectOperationContext(t, db, actor)
	lease, err := service.claimTicketLease(ctx, ticket.ID, actor, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim comment lease: %v", err)
	}
	reservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.comment.create",
		"comment-1",
		[]byte(`{"content":"investigating"}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve comment idempotency: %v", err)
	}
	result, err := service.CreateComment(ctx, NativeCommentInput{
		TicketID:            ticket.ID,
		ExpectedVersion:     1,
		LeaseID:             lease.ID,
		Actor:               actor,
		Content:             "Investigating the customer report.",
		ContentType:         "markdown",
		Type:                models.CommentTypeInternal,
		Reason:              "Initial triage",
		EvidenceRefs:        []string{"ticket://tickets/1"},
		IdempotencyRecordID: reservation.Record.ID,
	})
	if err != nil {
		t.Fatalf("create native comment: %v", err)
	}
	if result.Comment.Actor() != actor || result.Receipt.ResourceVersion != 2 {
		t.Fatalf("unexpected comment result: %+v", result)
	}
	var history models.TicketHistory
	if err := db.Where("comment_id = ?", result.Comment.ID).First(&history).Error; err != nil {
		t.Fatalf("load comment history: %v", err)
	}
	if history.Actor() != actor || history.IsVisible {
		t.Fatalf("internal Agent comment history actor/visibility incorrect: %+v", history)
	}
	assertTicketHistoryEventLink(t, &history, result.Event)
	var record models.IdempotencyRecord
	if err := db.First(&record, "id = ?", reservation.Record.ID).Error; err != nil {
		t.Fatalf("load idempotency record: %v", err)
	}
	if record.State != models.IdempotencyStateCompleted ||
		record.EventID != result.Event.ID ||
		len(record.ResourceSnapshot) == 0 {
		t.Fatalf("comment command did not atomically complete idempotency: %+v", record)
	}
}

func TestAgentNativeServicePrincipalCommentAndAttachmentLeaseEnforcement(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "write-lease")
	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create local storage: %v", err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage: storage,
		AttachmentStaging: storage,
		Now:               func() time.Time { return now },
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"lease-protected-writer",
		models.ScopeCommentsWrite,
		models.ScopeAttachmentsWrite,
	)
	otherPrincipal := createNativePrincipal(t, service, user.ID, "other-lease-holder")
	actor := models.ServicePrincipalActor(principal.ID)
	otherActor := models.ServicePrincipalActor(otherPrincipal.ID)
	actorCtx := testProjectOperationContext(t, db, actor)
	otherActorCtx := testProjectOperationContext(t, db, otherActor)
	ensureAttachmentTestAuthorization(t, db, actorCtx, actor)

	callComment := func(ticketID uint, expectedVersion uint64, leaseID string) error {
		t.Helper()
		_, err := service.CreateComment(actorCtx, NativeCommentInput{
			TicketID:        ticketID,
			ExpectedVersion: expectedVersion,
			LeaseID:         leaseID,
			Actor:           actor,
			Content:         "Lease-protected investigation note.",
			ContentType:     "text",
			Type:            models.CommentTypeInternal,
		})
		return err
	}
	callAttachment := func(ticketID uint, expectedVersion uint64, leaseID string) error {
		t.Helper()
		_, err := service.StoreAttachment(actorCtx, NativeAttachmentInput{
			TicketID:        ticketID,
			ExpectedVersion: expectedVersion,
			LeaseID:         leaseID,
			Actor:           actor,
			OriginalName:    "evidence.txt",
			Reader:          bytes.NewBufferString("evidence"),
		})
		return err
	}

	commentNoLease := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-NONE")
	if err := callComment(commentNoLease.ID, 1, " \t"); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("comment without lease must fail with lease conflict, got %v", err)
	}
	attachmentNoLease := seedNativeTicket(t, db, user.ID, "AI-ATTACH-LEASE-NONE")
	if err := callAttachment(attachmentNoLease.ID, 1, " \t"); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("attachment without lease must fail with lease conflict, got %v", err)
	}

	commentOtherLease := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-OTHER")
	otherCommentLease, err := service.claimTicketLease(
		otherActorCtx,
		commentOtherLease.ID,
		otherActor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim other comment lease: %v", err)
	}
	if err := callComment(commentOtherLease.ID, 1, otherCommentLease.ID); !errors.Is(err, ErrLeaseNotOwned) {
		t.Fatalf("comment with another holder's lease must fail, got %v", err)
	}
	attachmentOtherLease := seedNativeTicket(t, db, user.ID, "AI-ATTACH-LEASE-OTHER")
	otherAttachmentLease, err := service.claimTicketLease(
		otherActorCtx,
		attachmentOtherLease.ID,
		otherActor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim other attachment lease: %v", err)
	}
	if err := callAttachment(attachmentOtherLease.ID, 1, otherAttachmentLease.ID); !errors.Is(err, ErrLeaseNotOwned) {
		t.Fatalf("attachment with another holder's lease must fail, got %v", err)
	}

	commentExpiredLease := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-EXPIRED")
	expiredCommentLease, err := service.claimTicketLease(
		actorCtx,
		commentExpiredLease.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim expiring comment lease: %v", err)
	}
	if err := db.Model(&models.TicketLease{}).
		Where("id = ?", expiredCommentLease.ID).
		Update("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatalf("expire comment lease: %v", err)
	}
	if err := callComment(commentExpiredLease.ID, 1, expiredCommentLease.ID); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("comment with expired lease must fail, got %v", err)
	}
	attachmentExpiredLease := seedNativeTicket(t, db, user.ID, "AI-ATTACH-LEASE-EXPIRED")
	expiredAttachmentLease, err := service.claimTicketLease(
		actorCtx,
		attachmentExpiredLease.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim expiring attachment lease: %v", err)
	}
	if err := db.Model(&models.TicketLease{}).
		Where("id = ?", expiredAttachmentLease.ID).
		Update("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatalf("expire attachment lease: %v", err)
	}
	if err := callAttachment(attachmentExpiredLease.ID, 1, expiredAttachmentLease.ID); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("attachment with expired lease must fail, got %v", err)
	}

	commentLeaseTicket := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-SOURCE")
	crossTicketLease, err := service.claimTicketLease(
		actorCtx,
		commentLeaseTicket.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim cross-ticket lease: %v", err)
	}
	commentOtherTicket := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-TARGET")
	if err := callComment(commentOtherTicket.ID, 1, crossTicketLease.ID); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("comment with another ticket's lease must fail, got %v", err)
	}

	attachmentStaleLease := seedNativeTicket(t, db, user.ID, "AI-ATTACH-LEASE-STALE")
	staleAttachmentLease, err := service.claimTicketLease(
		actorCtx,
		attachmentStaleLease.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim stale attachment lease: %v", err)
	}
	if err := db.Model(&models.Ticket{}).
		Where("id = ? AND version = ?", attachmentStaleLease.ID, 1).
		Update("version", 2).Error; err != nil {
		t.Fatalf("advance ticket without lease: %v", err)
	}
	if err := callAttachment(attachmentStaleLease.ID, 2, staleAttachmentLease.ID); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("attachment with stale lease ticket version must fail, got %v", err)
	}

	commentValidLease := seedNativeTicket(t, db, user.ID, "AI-COMMENT-LEASE-VALID")
	validCommentLease, err := service.claimTicketLease(
		actorCtx,
		commentValidLease.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim valid comment lease: %v", err)
	}
	if err := callComment(commentValidLease.ID, 1, validCommentLease.ID); err != nil {
		t.Fatalf("comment with valid lease: %v", err)
	}
	var refreshedCommentLease models.TicketLease
	if err := db.First(&refreshedCommentLease, "id = ?", validCommentLease.ID).Error; err != nil {
		t.Fatalf("reload comment lease: %v", err)
	}
	if refreshedCommentLease.TicketVersion != 2 {
		t.Fatalf("comment did not advance lease ticket version: %+v", refreshedCommentLease)
	}

	attachmentValidLease := seedNativeTicket(t, db, user.ID, "AI-ATTACH-LEASE-VALID")
	validAttachmentLease, err := service.claimTicketLease(
		actorCtx,
		attachmentValidLease.ID,
		actor,
		1,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("claim valid attachment lease: %v", err)
	}
	if err := callAttachment(attachmentValidLease.ID, 1, validAttachmentLease.ID); err != nil {
		t.Fatalf("attachment with valid lease: %v", err)
	}
	var refreshedAttachmentLease models.TicketLease
	if err := db.First(&refreshedAttachmentLease, "id = ?", validAttachmentLease.ID).Error; err != nil {
		t.Fatalf("reload attachment lease: %v", err)
	}
	if refreshedAttachmentLease.TicketVersion != 2 {
		t.Fatalf("attachment did not advance lease ticket version: %+v", refreshedAttachmentLease)
	}
}

func TestAgentNativeHumanAndSystemCommentAndAttachmentWritesRemainLeaseOptional(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "lease-optional")
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create local storage: %v", err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage: storage,
		AttachmentStaging: storage,
	})
	actors := []struct {
		name  string
		actor models.ActorRef
	}{
		{name: "human", actor: models.HumanActor(user.ID)},
		{name: "system", actor: models.SystemActor("lease-compatibility-test")},
	}
	for _, test := range actors {
		t.Run(test.name, func(t *testing.T) {
			ctx := testProjectOperationContext(t, db, test.actor)
			ensureAttachmentTestAuthorization(
				t,
				db,
				ctx,
				test.actor,
			)
			commentTicket := seedNativeTicket(t, db, user.ID, "LEASE-OPTIONAL-COMMENT-"+strings.ToUpper(test.name))
			if _, err := service.CreateComment(ctx, NativeCommentInput{
				TicketID:        commentTicket.ID,
				ExpectedVersion: 1,
				Actor:           test.actor,
				Content:         "Lease-optional trusted actor note.",
				ContentType:     "text",
				Type:            models.CommentTypeInternal,
			}); err != nil {
				t.Fatalf("%s comment without lease: %v", test.name, err)
			}

			attachmentTicket := seedNativeTicket(t, db, user.ID, "LEASE-OPTIONAL-ATTACH-"+strings.ToUpper(test.name))
			if _, err := service.StoreAttachment(ctx, NativeAttachmentInput{
				TicketID:        attachmentTicket.ID,
				ExpectedVersion: 1,
				Actor:           test.actor,
				OriginalName:    "trusted.txt",
				Reader:          bytes.NewBufferString("trusted"),
			}); err != nil {
				t.Fatalf("%s attachment without lease: %v", test.name, err)
			}
		})
	}
}

func TestHumanAgentCannotUploadAttachmentAssignedToAnotherHuman(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	agent := seedActorUser(t, db, "attachment-object-agent")
	other := seedActorUser(t, db, "attachment-object-other")
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage:  storage,
		AttachmentStaging:  storage,
		AttachmentMaxBytes: 1024,
	})
	actor := models.HumanActor(agent.ID)
	ctx := testProjectOperationContext(t, db, actor)
	ensureTestHumanProjectRole(
		t,
		db,
		ctx,
		agent.ID,
		models.ProjectRoleAgent,
	)
	ticket := seedNativeTicket(
		t,
		db,
		other.ID,
		"AI-ATTACH-OBJECT-AUTHORIZATION",
	)
	if err := db.Model(&ticket).
		Update("assigned_to_id", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	var eventsBefore, outboxBefore int64
	if err := db.Model(&models.DomainEvent{}).
		Count(&eventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&outboxBefore).Error; err != nil {
		t.Fatal(err)
	}

	_, err = service.StoreAttachment(ctx, NativeAttachmentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: ticket.Version,
		Actor:           actor,
		OriginalName:    "other-assignee.txt",
		IsPublic:        true,
		Reader:          bytes.NewBufferString("untrusted evidence"),
	})
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"attachment assigned to another human error = %v, want project access denied",
			err,
		)
	}
	var attachments int64
	if err := db.Model(&models.TicketAttachment{}).
		Where("ticket_id = ?", ticket.ID).
		Count(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	if attachments != 0 {
		t.Fatalf(
			"denied attachment persisted %d records",
			attachments,
		)
	}
	var eventsAfter, outboxAfter int64
	if err := db.Model(&models.DomainEvent{}).
		Count(&eventsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.OutboxDelivery{}).
		Count(&outboxAfter).Error; err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore || outboxAfter != outboxBefore {
		t.Fatalf(
			"denied attachment changed events/outbox: events %d->%d outbox %d->%d",
			eventsBefore,
			eventsAfter,
			outboxBefore,
			outboxAfter,
		)
	}
}

func TestAgentNativeLocalAttachmentSecurityHashScanAndLimit(t *testing.T) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "attachment")
	storage, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create local storage: %v", err)
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage:  storage,
		AttachmentStaging:  storage,
		AttachmentMaxBytes: 5,
	})
	principal := createNativePrincipal(
		t,
		service,
		user.ID,
		"attachment-agent",
		models.ScopeAttachmentsWrite,
		models.ScopeAttachmentsRead,
	)
	ticket := seedNativeTicket(t, db, user.ID, "AI-ATTACH-001")
	actor := models.ServicePrincipalActor(principal.ID)
	ctx := testProjectOperationContext(t, db, actor)
	ensureAttachmentTestAuthorization(t, db, ctx, actor)
	lease, err := service.claimTicketLease(ctx, ticket.ID, actor, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim attachment lease: %v", err)
	}
	reservation, err := service.ReserveIdempotency(
		ctx,
		actor,
		"ticket.attachment.create",
		"attachment-1",
		[]byte(`{"name":"report.txt","sha256":"client-unknown"}`),
		time.Hour,
	)
	if err != nil {
		t.Fatalf("reserve attachment idempotency: %v", err)
	}
	attachmentInput := NativeAttachmentInput{
		TicketID:            ticket.ID,
		ExpectedVersion:     1,
		LeaseID:             lease.ID,
		Actor:               actor,
		CredentialID:        "test-credential",
		OriginalName:        "../../report.txt",
		Reader:              bytes.NewBufferString("hello"),
		IdempotencyRecordID: reservation.Record.ID,
	}
	if err := service.PrepareAttachmentUploadAuthorization(
		ctx,
		&attachmentInput,
	); err != nil {
		t.Fatalf("prepare attachment authorization: %v", err)
	}
	result, err := service.StoreAttachment(ctx, attachmentInput)
	if err != nil {
		t.Fatalf("store attachment: %v", err)
	}
	if result.Attachment.OriginalName != "report.txt" ||
		result.Attachment.Hash != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" ||
		result.Attachment.VirusScan != models.VirusScanPending ||
		result.Attachment.StorageType != "staging" {
		t.Fatalf("unexpected stored attachment: %+v", result.Attachment)
	}
	if strings.Contains(result.Attachment.StoragePath, "..") || strings.HasPrefix(result.Attachment.StoragePath, "/") {
		t.Fatalf("storage path must be relative and generated: %s", result.Attachment.StoragePath)
	}
	var completed models.IdempotencyRecord
	if err := db.First(&completed, "id = ?", reservation.Record.ID).Error; err != nil {
		t.Fatalf("load attachment idempotency record: %v", err)
	}
	if completed.State != models.IdempotencyStateCompleted ||
		completed.EventID != result.Event.ID ||
		len(completed.ResourceSnapshot) == 0 {
		t.Fatalf("attachment command did not complete idempotency atomically: %+v", completed)
	}
	var attachmentHistory models.TicketHistory
	if err := db.Where("attachment_id = ?", result.Attachment.ID).First(&attachmentHistory).Error; err != nil {
		t.Fatalf("load attachment history: %v", err)
	}
	assertTicketHistoryEventLink(t, &attachmentHistory, result.Event)
	if _, _, err := service.OpenAttachment(ctx, result.Attachment.ID); !errors.Is(err, ErrAttachmentNotClean) {
		t.Fatalf("pending attachment must not be downloadable, got %v", err)
	}
	var uploadDelivery models.OutboxDelivery
	if err := db.Where(
		"event_id = ? AND destination_type = ?",
		result.Event.ID,
		AttachmentUploadOutboxDestination,
	).Take(&uploadDelivery).Error; err != nil {
		t.Fatalf("load attachment upload outbox: %v", err)
	}
	if uploadDelivery.DestinationID !=
		strconv.FormatUint(uint64(result.Attachment.ID), 10) {
		t.Fatalf("upload destination = %q", uploadDelivery.DestinationID)
	}
	var stagingCleanupDelivery models.OutboxDelivery
	if err := db.Where(
		"destination_type = ? AND destination_id = ?",
		AttachmentStagingCleanupOutboxDestination,
		strconv.FormatUint(uint64(result.Attachment.ID), 10),
	).Take(&stagingCleanupDelivery).Error; err != nil {
		t.Fatalf("load staging cleanup outbox: %v", err)
	}
	if stagingCleanupDelivery.Status !=
		models.OutboxDeliverySucceeded ||
		stagingCleanupDelivery.DeliveredAt == nil {
		t.Fatalf(
			"committed upload did not retire orphan cleanup: %+v",
			stagingCleanupDelivery,
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  operation.Scope,
			Actor:  models.SystemActor(outboxSystemActorID),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	finalPartialPath := filepath.Join(
		storage.root,
		"tickets",
		strconv.FormatUint(uint64(ticket.ID), 10),
		result.Attachment.FileName,
	) + ".partial"
	if err := os.MkdirAll(
		filepath.Dir(finalPartialPath),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		finalPartialPath,
		[]byte("partial final object from crashed worker"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAttachmentUploadOutbox(
		workerCtx,
		result.Attachment.ID,
	); err != nil {
		t.Fatalf("execute attachment upload outbox: %v", err)
	}
	if err := db.First(
		result.Attachment,
		"id = ?",
		result.Attachment.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if result.Attachment.StorageType != "local" ||
		strings.HasPrefix(
			result.Attachment.StoragePath,
			".staging/",
		) {
		t.Fatalf(
			"finalized attachment storage = %+v",
			result.Attachment,
		)
	}
	if _, err := os.Stat(finalPartialPath); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("final upload retry left partial object: %v", err)
	}
	if err := service.MarkAttachmentScan(ctx, result.Attachment.ID, models.VirusScanClean, "scanner ok"); err != nil {
		t.Fatalf("mark attachment clean: %v", err)
	}
	_, reader, err := service.OpenAttachment(ctx, result.Attachment.ID)
	if err != nil {
		t.Fatalf("open clean attachment: %v", err)
	}
	content, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(content) != "hello" {
		t.Fatalf("unexpected attachment content %q err=%v", content, err)
	}

	var before int64
	_ = db.Model(&models.TicketAttachment{}).Count(&before).Error
	if _, err := service.StoreAttachment(ctx, NativeAttachmentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 2,
		LeaseID:         lease.ID,
		Actor:           actor,
		OriginalName:    "empty.txt",
		Reader:          bytes.NewReader(nil),
	}); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("expected empty attachment rejection, got %v", err)
	}
	var afterEmpty int64
	_ = db.Model(&models.TicketAttachment{}).Count(&afterEmpty).Error
	if afterEmpty != before+1 {
		t.Fatalf(
			"empty attachment must leave one durable cleanup intent: before=%d after=%d",
			before,
			afterEmpty,
		)
	}
	var emptyIntent models.TicketAttachment
	if err := db.Where(
		"storage_type = ?",
		attachmentStagingIntentStorageType,
	).Order("id DESC").Take(&emptyIntent).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAttachmentStagingCleanupOutbox(
		workerCtx,
		emptyIntent.ID,
	); err != nil {
		t.Fatalf("cleanup empty attachment intent: %v", err)
	}
	if _, err := service.StoreAttachment(ctx, NativeAttachmentInput{
		TicketID:        ticket.ID,
		ExpectedVersion: 2,
		LeaseID:         lease.ID,
		Actor:           actor,
		OriginalName:    "too-large.txt",
		Reader:          bytes.NewBufferString("123456"),
	}); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("expected attachment size rejection, got %v", err)
	}
	var after int64
	_ = db.Model(&models.TicketAttachment{}).Count(&after).Error
	if after != before+1 {
		t.Fatalf(
			"oversized attachment must leave one durable cleanup intent: before=%d after=%d",
			before,
			after,
		)
	}
	var oversizedIntent models.TicketAttachment
	if err := db.Where(
		"storage_type = ?",
		attachmentStagingIntentStorageType,
	).Order("id DESC").Take(&oversizedIntent).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteAttachmentStagingCleanupOutbox(
		workerCtx,
		oversizedIntent.ID,
	); err != nil {
		t.Fatalf("cleanup oversized attachment intent: %v", err)
	}
	var afterCleanup int64
	_ = db.Model(&models.TicketAttachment{}).Count(&afterCleanup).Error
	if afterCleanup != before {
		t.Fatalf(
			"cleanup worker left attachment intents: before=%d after=%d",
			before,
			afterCleanup,
		)
	}
}
