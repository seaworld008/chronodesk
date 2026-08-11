package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/a2a"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/mcp"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	websocketPkg "github.com/seaworld008/chronodesk/server/internal/websocket"

	"gorm.io/gorm"
)

// MCPResourcePublisher bridges durable domain events to live MCP resource
// subscriptions. Direct API writes may also call PublishTicket for low-latency
// delivery; the Outbox remains the recovery path after a process crash.
type MCPResourcePublisher struct {
	Server *mcp.Server
	DB     *gorm.DB
}

type SLAEscalationConsumer interface {
	ExecuteDomainEvent(context.Context, services.CloudEventEnvelope) error
}

type AuthEmailOutboxConsumer interface {
	DeliverAuthEmailOutbox(
		context.Context,
		*models.OutboxDelivery,
		services.CloudEventEnvelope,
	) error
}

type AttachmentUploadOutboxConsumer interface {
	ExecuteAttachmentUploadOutbox(context.Context, uint) error
	ExecuteAttachmentStagingCleanupOutbox(context.Context, uint) error
}

type WebSocketAccessRevoker interface {
	RevokeProjectMembership(models.ProjectScope, uint) error
	RevokeUser(uint) error
	RevokeProject(models.ProjectScope) error
}

func (p *MCPResourcePublisher) PublishTicket(
	projectKey string,
	ticketID uint,
) {
	projectKey = strings.TrimSpace(projectKey)
	if p == nil || p.Server == nil || ticketID == 0 ||
		models.ValidateProjectKey(projectKey) != nil {
		return
	}
	baseURI := "ticket://projects/" + projectKey
	p.Server.Publish(mcp.ResourceEvent{
		URI: fmt.Sprintf("%s/tickets/%d", baseURI, ticketID),
	})
	p.Server.Publish(mcp.ResourceEvent{
		URI: fmt.Sprintf("%s/tickets/%d/history", baseURI, ticketID),
	})
	p.Server.Publish(mcp.ResourceEvent{URI: "ticket://capabilities"})
}

func (p *MCPResourcePublisher) PublishQueue(
	projectKey string,
	queue string,
) {
	projectKey = strings.TrimSpace(projectKey)
	if p == nil || p.Server == nil ||
		models.ValidateProjectKey(projectKey) != nil ||
		!mcpQueuePattern.MatchString(queue) {
		return
	}
	p.Server.Publish(mcp.ResourceEvent{
		URI: "ticket://projects/" + projectKey + "/queues/" + queue,
	})
}

// NativeOutboxDeliverer owns the side effects executed after a domain
// transaction commits. It deliberately has no generic URL-fetch operation.
type NativeOutboxDeliverer struct {
	db                *gorm.DB
	notifications     *services.NotificationService
	publisher         *MCPResourcePublisher
	automation        *services.AutomationService
	slaEscalation     SLAEscalationConsumer
	attachments       services.AttachmentStorage
	attachmentUploads AttachmentUploadOutboxConsumer
	knowledge         *services.KnowledgeService
	authEmails        AuthEmailOutboxConsumer
	accessRevocations WebSocketAccessRevoker
	secretStore       security.Protector
	resolver          *net.Resolver
	a2aPushClient     a2aPushClientFactory
}

func (*NativeOutboxDeliverer) OwnsWebhookDispatchStartBoundary() bool {
	return true
}

type a2aPushClientFactory func(
	context.Context,
	*url.URL,
	*net.Resolver,
	time.Duration,
) (*http.Client, error)

// NativeOutboxDelivererOptions is the complete, immutable dependency graph for
// durable side-effect delivery. Optional consumers may be nil when that
// destination type is not enabled, and Deliver will return a stable error if a
// delivery is nevertheless routed to an unavailable consumer.
type NativeOutboxDelivererOptions struct {
	DB                *gorm.DB
	Notifications     *services.NotificationService
	Publisher         *MCPResourcePublisher
	Automation        *services.AutomationService
	SLAEscalation     SLAEscalationConsumer
	AttachmentStorage services.AttachmentStorage
	AttachmentUploads AttachmentUploadOutboxConsumer
	Knowledge         *services.KnowledgeService
	AuthEmails        AuthEmailOutboxConsumer
	AccessRevocations WebSocketAccessRevoker
	SecretProtector   security.Protector
	Resolver          *net.Resolver
}

func (d *NativeOutboxDeliverer) withProjectTransaction(
	ctx context.Context,
	scope models.ProjectScope,
	run func(*gorm.DB) error,
) error {
	if d == nil || d.db == nil || run == nil {
		return errors.New("outbox project transaction is unavailable")
	}
	if scopeddb.HasTransaction(ctx) {
		return scopeddb.TransactionForContext(ctx, d.db, run)
	}
	return scopeddb.WithProjectScopeTransaction(ctx, d.db, scope, run)
}

func (d *NativeOutboxDeliverer) withProjectContext(
	ctx context.Context,
	scope models.ProjectScope,
	run func(context.Context) error,
) error {
	if d == nil || d.db == nil || run == nil {
		return errors.New("outbox project operation is unavailable")
	}
	if scopeddb.HasTransaction(ctx) {
		return run(ctx)
	}
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		d.db,
		scope,
		run,
	)
}

func NewNativeOutboxDeliverer(options NativeOutboxDelivererOptions) (*NativeOutboxDeliverer, error) {
	if options.DB == nil {
		return nil, errors.New("outbox deliverer database is required")
	}
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &NativeOutboxDeliverer{
		db:                options.DB,
		notifications:     options.Notifications,
		publisher:         options.Publisher,
		automation:        options.Automation,
		slaEscalation:     options.SLAEscalation,
		attachments:       options.AttachmentStorage,
		attachmentUploads: options.AttachmentUploads,
		knowledge:         options.Knowledge,
		authEmails:        options.AuthEmails,
		accessRevocations: options.AccessRevocations,
		secretStore:       options.SecretProtector,
		resolver:          resolver,
		a2aPushClient:     security.NewPinnedHTTPSClient,
	}, nil
}

func (d *NativeOutboxDeliverer) Deliver(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if err := validateOutboxDeliveryOperation(ctx, delivery, event); err != nil {
		if delivery != nil &&
			delivery.DestinationType == "webhook" {
			return services.ErrWebhookOutboxAttemptRejected
		}
		return err
	}
	switch delivery.DestinationType {
	case "event_stream":
		handled, err := d.deliverWebSocketAccessRevocation(event)
		if err != nil || handled {
			return err
		}
		projectKey, err := d.projectKeyForEvent(ctx, event)
		if err != nil {
			return err
		}
		if ticketID := ticketIDFromCloudEvent(event); ticketID > 0 {
			d.publisher.PublishTicket(projectKey, ticketID)
		}
		for _, queue := range queueNamesFromCloudEvent(event) {
			d.publisher.PublishQueue(projectKey, queue)
		}
		return nil
	case "webhook":
		return d.deliverWebhook(ctx, delivery, event)
	case services.NotificationOutboxDestination:
		if d.notifications == nil {
			return errors.New("in-app notification service is unavailable")
		}
		var (
			notification *models.Notification
			created      bool
		)
		err := d.withProjectContext(
			ctx,
			outboxDeliveryScope(delivery),
			func(scopedContext context.Context) error {
				var deliveryErr error
				notification, created, deliveryErr =
					d.notifications.DeliverTicketNotificationOutbox(
						scopedContext,
						event,
						delivery.DestinationID,
					)
				return deliveryErr
			},
		)
		if err != nil {
			return err
		}
		// The database row above is the authoritative notification. WebSocket
		// delivery is a best-effort latency optimization and is deliberately
		// skipped on an idempotent replay to avoid duplicate live pushes.
		if created {
			websocketPkg.NotificationCreatedHook(ctx, notification)
		}
		return nil
	case "a2a_push":
		return d.deliverA2APush(ctx, delivery, event)
	case "automation":
		if d.automation == nil {
			return errors.New("automation service is unavailable")
		}
		return d.withProjectContext(
			ctx,
			outboxDeliveryScope(delivery),
			func(scopedContext context.Context) error {
				return d.automation.ExecuteDomainEvent(
					scopedContext,
					event,
				)
			},
		)
	case services.SLAEscalationOutboxDestination:
		if d.slaEscalation == nil {
			return errors.New("SLA escalation consumer is unavailable")
		}
		return d.withProjectContext(
			ctx,
			outboxDeliveryScope(delivery),
			func(scopedContext context.Context) error {
				return d.slaEscalation.ExecuteDomainEvent(
					scopedContext,
					event,
				)
			},
		)
	case services.AttachmentCleanupOutboxDestination:
		return d.deliverAttachmentCleanup(ctx, delivery, event)
	case services.AttachmentUploadOutboxDestination:
		if d.attachmentUploads == nil {
			return errors.New(
				"attachment upload consumer is unavailable",
			)
		}
		attachmentID, err := safeconv.ParsePositiveUint(
			delivery.DestinationID,
		)
		if err != nil {
			return errors.New(
				"attachment upload destination is invalid",
			)
		}
		return d.attachmentUploads.ExecuteAttachmentUploadOutbox(
			ctx,
			attachmentID,
		)
	case services.AttachmentStagingCleanupOutboxDestination:
		if d.attachmentUploads == nil {
			return errors.New(
				"attachment staging cleanup consumer is unavailable",
			)
		}
		attachmentID, err := safeconv.ParsePositiveUint(
			delivery.DestinationID,
		)
		if err != nil {
			return errors.New(
				"attachment staging cleanup destination is invalid",
			)
		}
		return d.attachmentUploads.ExecuteAttachmentStagingCleanupOutbox(
			ctx,
			attachmentID,
		)
	case services.KnowledgeIndexRebuildOutboxDestination:
		if d.knowledge == nil {
			return errors.New(
				"knowledge index rebuild consumer is unavailable",
			)
		}
		stateID, generation, err :=
			parseKnowledgeIndexRebuildDestination(
				delivery.DestinationID,
			)
		if err != nil {
			return err
		}
		return d.knowledge.ExecuteIndexRebuildOutbox(
			ctx,
			stateID,
			generation,
		)
	case services.EmailOutboxDestination:
		return d.deliverEmail(ctx, delivery, event)
	default:
		return fmt.Errorf("unsupported outbox destination type %q", delivery.DestinationType)
	}
}

func (d *NativeOutboxDeliverer) DeliverAttempt(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) services.OutboxAttemptResult {
	if err := validateOutboxDeliveryOperation(
		ctx,
		delivery,
		event,
	); err != nil {
		if delivery != nil &&
			delivery.DestinationType == "webhook" {
			return services.OutboxKnownFailure(
				services.ErrWebhookOutboxAttemptRejected,
			)
		}
		return services.OutboxKnownFailure(err)
	}
	if delivery.DestinationType == "webhook" {
		return d.deliverWebhookAttempt(ctx, delivery, event)
	}
	err := d.Deliver(ctx, delivery, event)
	if contextErr := ctx.Err(); contextErr != nil {
		return services.OutboxUncertain(contextErr)
	}
	if err == nil {
		return services.OutboxKnownSuccess(time.Now().UTC())
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return services.OutboxUncertain(err)
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return services.OutboxUncertain(err)
	}
	return services.OutboxKnownFailure(err)
}

func (d *NativeOutboxDeliverer) deliverWebSocketAccessRevocation(
	event services.CloudEventEnvelope,
) (bool, error) {
	scope := models.ProjectScope{
		OrganizationID: event.OrganizationID,
		ProjectID:      event.ProjectID,
	}
	switch event.Type {
	case services.ProjectMembershipDeactivatedEventType:
		if d == nil || d.accessRevocations == nil {
			return true, errors.New(
				"WebSocket membership revocation consumer is unavailable",
			)
		}
		var data struct {
			UserID uint `json:"user_id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil ||
			data.UserID == 0 {
			return true, errors.New(
				"WebSocket membership revocation event is invalid",
			)
		}
		return true, d.accessRevocations.RevokeProjectMembership(
			scope,
			data.UserID,
		)
	case services.UserAccessRevokedEventType:
		if d == nil || d.accessRevocations == nil {
			return true, errors.New(
				"WebSocket user revocation consumer is unavailable",
			)
		}
		var data struct {
			UserID uint `json:"user_id"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil ||
			data.UserID == 0 {
			return true, errors.New(
				"WebSocket user revocation event is invalid",
			)
		}
		return true, d.accessRevocations.RevokeUser(data.UserID)
	case services.ProjectAccessRevokedEventType:
		if d == nil || d.accessRevocations == nil {
			return true, errors.New(
				"WebSocket project revocation consumer is unavailable",
			)
		}
		return true, d.accessRevocations.RevokeProject(scope)
	default:
		return false, nil
	}
}

func parseKnowledgeIndexRebuildDestination(
	destinationID string,
) (string, uint64, error) {
	stateID, generationText, ok := strings.Cut(
		strings.TrimSpace(destinationID),
		":",
	)
	if !ok || strings.TrimSpace(stateID) == "" {
		return "", 0, errors.New(
			"knowledge index rebuild destination is invalid",
		)
	}
	generation, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || generation == 0 {
		return "", 0, errors.New(
			"knowledge index rebuild generation is invalid",
		)
	}
	return stateID, generation, nil
}

// validateOutboxDeliveryOperation prevents adapters and tests from bypassing
// the same trusted worker boundary used by ProcessOutboxBatch. Side effects
// execute outside a database transaction; consumers that need persistence
// open their own bounded project transaction.
func validateOutboxDeliveryOperation(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if delivery == nil {
		return errors.New("outbox delivery is required")
	}
	if scopeddb.HasTransaction(ctx) {
		return errors.New("outbox side effects cannot run inside a database transaction")
	}
	operation, err := services.OperationContextFromContext(ctx)
	if err != nil {
		return errors.New("outbox delivery requires a trusted worker context")
	}
	if operation.Source != services.SourceProtocolWorker ||
		operation.Actor.Type != models.ActorTypeSystem {
		return errors.New("outbox delivery requires a system worker actor")
	}
	scope := outboxDeliveryScope(delivery)
	if err := scope.Validate(); err != nil || operation.Scope != scope {
		return errors.New("outbox delivery project scope mismatch")
	}
	if strings.TrimSpace(delivery.EventID) == "" ||
		strings.TrimSpace(event.ID) == "" ||
		delivery.EventID != event.ID {
		return errors.New("outbox delivery event reference mismatch")
	}
	if event.OrganizationID != scope.OrganizationID ||
		event.ProjectID != scope.ProjectID {
		return errors.New("outbox event project scope mismatch")
	}
	return nil
}

func outboxDeliveryScope(
	delivery *models.OutboxDelivery,
) models.ProjectScope {
	if delivery == nil {
		return models.ProjectScope{}
	}
	return models.ProjectScope{
		OrganizationID: delivery.OrganizationID,
		ProjectID:      delivery.ProjectID,
	}
}

func (d *NativeOutboxDeliverer) projectKeyForEvent(
	ctx context.Context,
	event services.CloudEventEnvelope,
) (string, error) {
	if d == nil || d.db == nil ||
		event.OrganizationID == 0 || event.ProjectID == 0 {
		return "", errors.New("outbox event project scope is required")
	}
	var project models.Project
	err := d.db.WithContext(ctx).
		Select("id", "organization_id", "key", "status").
		Where(
			"id = ? AND organization_id = ? AND status = ?",
			event.ProjectID,
			event.OrganizationID,
			models.ProjectStatusActive,
		).
		First(&project).Error
	projectKey := string(project.Key)
	if err != nil || models.ValidateProjectKey(projectKey) != nil {
		return "", errors.New("outbox event project is unavailable")
	}
	return projectKey, nil
}

func (d *NativeOutboxDeliverer) deliverEmail(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	switch {
	case strings.HasPrefix(
		delivery.DestinationID,
		services.NotificationEmailDestinationPrefix,
	):
		if d.notifications == nil {
			return errors.New("email notification service is unavailable")
		}
		return d.notifications.DeliverEmailNotificationOutbox(
			ctx,
			event,
			delivery.DestinationID,
		)
	case strings.HasPrefix(
		delivery.DestinationID,
		services.AuthVerificationEmailDestinationPrefix,
	), strings.HasPrefix(
		delivery.DestinationID,
		services.AuthPasswordResetEmailDestinationPrefix,
	), strings.HasPrefix(
		delivery.DestinationID,
		services.AuthWelcomeEmailDestinationPrefix,
	):
		if d.authEmails == nil {
			return errors.New("authentication email service is unavailable")
		}
		return d.authEmails.DeliverAuthEmailOutbox(ctx, delivery, event)
	default:
		return fmt.Errorf(
			"unsupported email Outbox destination %q",
			delivery.DestinationID,
		)
	}
}

func (d *NativeOutboxDeliverer) deliverAttachmentCleanup(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if d.attachments == nil {
		return errors.New("attachment storage is unavailable")
	}
	if event.Type != eventcontract.TicketDeletedEventType {
		return fmt.Errorf(
			"attachment cleanup requires a ticket deletion event, got %q",
			event.Type,
		)
	}
	var data struct {
		TicketID uint `json:"ticket_id"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil || data.TicketID == 0 {
		return errors.New("attachment cleanup event is missing ticket_id")
	}

	storageReference, err := services.AttachmentCleanupStorageReference(
		event,
		delivery.DestinationID,
	)
	if err != nil {
		return err
	}
	if routed, ok := d.attachments.(services.ReferencedAttachmentStorage); ok {
		err = routed.DeleteStoredObject(
			ctx,
			services.AttachmentStoredReference{
				StorageType: storageReference.StorageType,
				StoreID:     storageReference.StoreID,
				Key:         storageReference.StoragePath,
				VersionID:   storageReference.VersionID,
			},
		)
	} else if routed, ok := d.attachments.(services.TypedAttachmentStorage); ok {
		err = routed.DeleteStored(
			ctx,
			storageReference.StorageType,
			storageReference.StoragePath,
		)
	} else {
		err = d.attachments.Delete(
			ctx,
			storageReference.StoragePath,
		)
	}
	if err != nil {
		return fmt.Errorf("delete attachment object: %w", err)
	}
	return nil
}

func queueNamesFromCloudEvent(event services.CloudEventEnvelope) []string {
	var data struct {
		OldQueue string `json:"old_queue"`
		NewQueue string `json:"new_queue"`
	}
	if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil {
		return nil
	}
	seen := make(map[string]struct{}, 2)
	result := make([]string, 0, 2)
	for _, queue := range []string{data.OldQueue, data.NewQueue} {
		queue = strings.TrimSpace(queue)
		if !mcpQueuePattern.MatchString(queue) {
			continue
		}
		if _, exists := seen[queue]; exists {
			continue
		}
		seen[queue] = struct{}{}
		result = append(result, queue)
	}
	return result
}

const webhookSnapshotPrefix = models.WebhookDeliverySnapshotDestinationPrefix

func (d *NativeOutboxDeliverer) deliverWebhook(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	return d.deliverWebhookAttempt(ctx, delivery, event).Err
}

func (d *NativeOutboxDeliverer) deliverWebhookAttempt(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) services.OutboxAttemptResult {
	if d.notifications == nil {
		return services.OutboxKnownFailure(
			errors.New("webhook notification service is unavailable"),
		)
	}
	_, err := parseWebhookSnapshotDestinationID(
		delivery.DestinationID,
	)
	if err != nil {
		return services.OutboxKnownFailure(
			services.ErrWebhookOutboxAttemptRejected,
		)
	}
	claimRef, err := services.OutboxClaimRefFromDelivery(delivery)
	if err != nil || delivery.ExpiresAt == nil {
		return services.OutboxKnownFailure(
			services.ErrWebhookOutboxAttemptRejected,
		)
	}
	effectiveDeadline, ok := ctx.Deadline()
	if !ok {
		return services.OutboxKnownFailure(
			services.ErrWebhookOutboxAttemptRejected,
		)
	}
	credentialExpiresAt := delivery.ExpiresAt.UTC()
	if credentialExpiresAt.Before(effectiveDeadline) {
		effectiveDeadline = credentialExpiresAt
	}
	return d.notifications.SendWebhookSnapshotOutboxAttemptResult(
		ctx,
		services.WebhookOutboxAttemptClaim{
			DeliveryID: delivery.ID,
			EventID:    event.ID,
			Scope: models.ProjectScope{
				OrganizationID: event.OrganizationID,
				ProjectID:      event.ProjectID,
			},
			WorkerID:            claimRef.WorkerID,
			LockToken:           claimRef.LockToken,
			LockedAt:            claimRef.LockedAt,
			AttemptGeneration:   claimRef.Attempts,
			SnapshotDestination: delivery.DestinationID,
			EffectiveDeadline:   effectiveDeadline.UTC(),
			CredentialExpiresAt: credentialExpiresAt,
		},
		&event,
	)
}

func notificationEventFromCloudEvent(
	event services.CloudEventEnvelope,
) *services.NotificationEvent {
	var payload map[string]any
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["event_id"] = event.ID
	payload["cloud_event"] = event
	ticketID := ticketIDFromCloudEvent(event)
	transitionStatus := models.TicketStatus("")
	if event.Type == eventcontract.TicketTransitionedEventType {
		transitionStatus = webhookTransitionStatus(event)
	}
	title := "ChronoDesk ticket event"
	description := event.Type
	if event.Type == eventcontract.AutomationNotificationRequestedEventType {
		var requested struct {
			Notification struct {
				Title   string `json:"title"`
				Content string `json:"content"`
			} `json:"notification"`
		}
		if json.Unmarshal(event.Data, &requested) == nil {
			if value := strings.TrimSpace(requested.Notification.Title); value != "" {
				title = value
			}
			if value := strings.TrimSpace(requested.Notification.Content); value != "" {
				description = value
			}
		}
	}
	return &services.NotificationEvent{
		Type:             models.WebhookEventType(event.Type),
		TransitionStatus: transitionStatus,
		ResourceID:       ticketID,
		ResourceType:     "ticket",
		Title:            title,
		Description:      description,
		Data:             payload,
		Metadata: map[string]string{
			"event_id":       event.ID,
			"trace_id":       event.TraceID,
			"correlation_id": event.CorrelationID,
			"specversion":    event.SpecVersion,
		},
		Timestamp: event.Time,
	}
}

func parseWebhookSnapshotDestinationID(
	destinationID string,
) (string, error) {
	return models.ParseWebhookDeliverySnapshotDestinationID(destinationID)
}

func (d *NativeOutboxDeliverer) deliverA2APush(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	snapshotID, err := parseA2APushSnapshotDestinationID(
		delivery.DestinationID,
	)
	if err != nil {
		return err
	}
	var data struct {
		TaskID         string `json:"a2a_task_id"`
		PushSnapshotID string `json:"push_snapshot_id"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return fmt.Errorf("decode A2A push event: %w", err)
	}
	if data.TaskID == "" ||
		data.PushSnapshotID == "" ||
		data.PushSnapshotID != snapshotID {
		return errors.New("A2A push event is incomplete")
	}

	var snapshot models.A2APushDeliverySnapshot
	scope := outboxDeliveryScope(delivery)
	if err := d.withProjectTransaction(
		ctx,
		scope,
		func(tx *gorm.DB) error {
			return tx.Where(
				"id = ? AND event_id = ? AND task_id = ? AND organization_id = ? AND project_id = ?",
				snapshotID,
				event.ID,
				data.TaskID,
				scope.OrganizationID,
				scope.ProjectID,
			).First(&snapshot).Error
		},
	); err != nil {
		return fmt.Errorf("load A2A push delivery snapshot: %w", err)
	}

	request, err := newA2APushSnapshotRequest(
		ctx,
		snapshot.CallbackURL,
		json.RawMessage(snapshot.RequestBody),
		snapshot.EventID,
		snapshot.ContentType,
		snapshot.ProtocolVersion,
	)
	if err != nil {
		return errors.New("A2A Push 回调请求无效")
	}
	token, err := security.RevealOptional(
		d.secretStore,
		snapshot.TokenCiphertext,
		a2aPushSnapshotSecretAAD(snapshot, "token"),
	)
	if err != nil {
		return fmt.Errorf("decrypt A2A push token: %w", err)
	}
	if strings.ContainsAny(token, "\r\n") {
		return errors.New("A2A push token contains invalid characters")
	}
	if token != "" {
		request.Header.Set("X-A2A-Notification-Token", token)
	}
	if snapshot.AuthenticationCiphertext != "" {
		plaintext, err := security.RevealOptional(
			d.secretStore,
			snapshot.AuthenticationCiphertext,
			a2aPushSnapshotSecretAAD(
				snapshot,
				"authentication",
			),
		)
		if err != nil {
			return fmt.Errorf("decrypt A2A push authentication: %w", err)
		}
		var authentication a2a.AuthenticationInfo
		if err := json.Unmarshal([]byte(plaintext), &authentication); err != nil {
			return fmt.Errorf("decode A2A push authentication: %w", err)
		}
		if strings.ContainsAny(authentication.Scheme+authentication.Credentials, "\r\n") {
			return errors.New("A2A push authentication contains invalid characters")
		}
		if authentication.Scheme != "" && authentication.Credentials != "" {
			request.Header.Set("Authorization", authentication.Scheme+" "+authentication.Credentials)
		}
	}

	if d.a2aPushClient == nil {
		return errors.New("A2A Push 回调地址不可用")
	}
	client, err := d.a2aPushClient(
		ctx,
		request.URL,
		d.resolver,
		20*time.Second,
	)
	if err != nil {
		return errors.New("A2A Push 回调地址不可用")
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("A2A Push 网络投递失败")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("A2A Push 上游返回 HTTP %d", response.StatusCode)
	}
	return nil
}

func parseA2APushSnapshotDestinationID(
	destinationID string,
) (string, error) {
	if !strings.HasPrefix(
		destinationID,
		a2aPushSnapshotDestinationPrefix,
	) {
		return "", errors.New(
			"A2A push Outbox destination snapshot is invalid",
		)
	}
	value := strings.TrimPrefix(
		destinationID,
		a2aPushSnapshotDestinationPrefix,
	)
	parsed, err := uuid.Parse(value)
	if err != nil ||
		parsed.String() != value ||
		parsed.Version() != 7 {
		return "", errors.New(
			"A2A push Outbox destination snapshot is invalid",
		)
	}
	return value, nil
}

func newA2APushRequest(
	ctx context.Context,
	target string,
	payload json.RawMessage,
	eventID string,
) (*http.Request, error) {
	return newA2APushSnapshotRequest(
		ctx,
		target,
		payload,
		eventID,
		"application/a2a+json",
		a2a.ProtocolVersion,
	)
}

func newA2APushSnapshotRequest(
	ctx context.Context,
	target string,
	payload json.RawMessage,
	eventID string,
	contentType string,
	protocolVersion string,
) (*http.Request, error) {
	if contentType != "application/a2a+json" ||
		protocolVersion != a2a.ProtocolVersion ||
		!json.Valid(payload) {
		return nil, errors.New("invalid A2A push request snapshot")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		target,
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return nil, errors.New("invalid A2A push callback URL")
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("A2A-Version", protocolVersion)
	request.Header.Set("X-CloudEvents-ID", eventID)
	return request, nil
}

func ticketIDFromCloudEvent(event services.CloudEventEnvelope) uint {
	var data struct {
		TicketID uint `json:"ticket_id"`
	}
	if len(event.Data) > 0 && json.Unmarshal(event.Data, &data) == nil && data.TicketID > 0 {
		return data.TicketID
	}
	const prefix = "ticket/"
	if strings.HasPrefix(event.Subject, prefix) {
		value, err := safeconv.ParsePositiveUint(strings.TrimPrefix(event.Subject, prefix))
		if err == nil {
			return value
		}
	}
	return 0
}

func webhookTransitionStatus(event services.CloudEventEnvelope) models.TicketStatus {
	var data struct {
		Status    models.TicketStatus `json:"status"`
		NewStatus models.TicketStatus `json:"new_status"`
	}
	if len(event.Data) == 0 || json.Unmarshal(event.Data, &data) != nil {
		return ""
	}
	if data.NewStatus != "" {
		return data.NewStatus
	}
	return data.Status
}
