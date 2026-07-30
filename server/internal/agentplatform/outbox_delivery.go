package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	db            *gorm.DB
	notifications *services.NotificationService
	publisher     *MCPResourcePublisher
	automation    *services.AutomationService
	slaEscalation SLAEscalationConsumer
	attachments   services.AttachmentStorage
	authEmails    AuthEmailOutboxConsumer
	secretStore   security.Protector
	resolver      *net.Resolver
}

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
	AuthEmails        AuthEmailOutboxConsumer
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
		db:            options.DB,
		notifications: options.Notifications,
		publisher:     options.Publisher,
		automation:    options.Automation,
		slaEscalation: options.SLAEscalation,
		attachments:   options.AttachmentStorage,
		authEmails:    options.AuthEmails,
		secretStore:   options.SecretProtector,
		resolver:      resolver,
	}, nil
}

func (d *NativeOutboxDeliverer) Deliver(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if err := validateOutboxDeliveryOperation(ctx, delivery, event); err != nil {
		return err
	}
	switch delivery.DestinationType {
	case "event_stream":
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
		return d.deliverA2APush(ctx, event)
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
	case services.EmailOutboxDestination:
		return d.deliverEmail(ctx, delivery, event)
	default:
		return fmt.Errorf("unsupported outbox destination type %q", delivery.DestinationType)
	}
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

	storagePath, err := services.AttachmentCleanupStoragePath(
		event,
		delivery.DestinationID,
	)
	if err != nil {
		return err
	}
	if err := d.attachments.Delete(ctx, storagePath); err != nil {
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

const webhookSnapshotPrefix = "snapshot:"

func (d *NativeOutboxDeliverer) deliverWebhook(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if d.notifications == nil {
		return errors.New("webhook notification service is unavailable")
	}
	if !eventcontract.IsWebhookDeliveryEventType(strings.TrimSpace(event.Type)) {
		return nil
	}
	snapshotID, err := parseWebhookSnapshotDestinationID(
		delivery.DestinationID,
	)
	if err != nil {
		return err
	}
	notification := notificationEventFromCloudEvent(event)
	notification.Metadata["delivery_id"] = delivery.ID
	return d.notifications.SendWebhookSnapshotOutboxAttempt(
		ctx,
		models.ProjectScope{
			OrganizationID: event.OrganizationID,
			ProjectID:      event.ProjectID,
		},
		snapshotID,
		notification,
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
	if !strings.HasPrefix(destinationID, webhookSnapshotPrefix) {
		return "", fmt.Errorf(
			"unsupported webhook Outbox destination %q",
			destinationID,
		)
	}
	value := strings.TrimPrefix(destinationID, webhookSnapshotPrefix)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value || parsed.Version() != 7 {
		return "", fmt.Errorf(
			"invalid webhook Outbox snapshot destination %q",
			destinationID,
		)
	}
	return value, nil
}

func (d *NativeOutboxDeliverer) deliverA2APush(
	ctx context.Context,
	event services.CloudEventEnvelope,
) error {
	var data struct {
		TaskID         string          `json:"a2a_task_id"`
		PushConfigID   string          `json:"push_config_id"`
		StreamResponse json.RawMessage `json:"stream_response"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return fmt.Errorf("decode A2A push event: %w", err)
	}
	if data.TaskID == "" || data.PushConfigID == "" || len(data.StreamResponse) == 0 {
		return errors.New("A2A push event is incomplete")
	}

	var row models.AgentPushNotificationConfig
	scope := models.ProjectScope{
		OrganizationID: event.OrganizationID,
		ProjectID:      event.ProjectID,
	}
	if err := d.withProjectTransaction(
		ctx,
		scope,
		func(tx *gorm.DB) error {
			return tx.Where(
				"id = ? AND task_id = ? AND organization_id = ? AND project_id = ?",
				data.PushConfigID,
				data.TaskID,
				scope.OrganizationID,
				scope.ProjectID,
			).First(&row).Error
		},
	); err != nil {
		return fmt.Errorf("load A2A push configuration: %w", err)
	}

	request, err := newA2APushRequest(ctx, row.URL, data.StreamResponse, event.ID)
	if err != nil {
		return errors.New("A2A Push 回调请求无效")
	}
	token, err := security.RevealOptional(
		d.secretStore,
		row.Token,
		security.FieldAAD("agent_push_notification_configs", row.ID, "token"),
	)
	if err != nil {
		return fmt.Errorf("decrypt A2A push token: %w", err)
	}
	if token != "" {
		request.Header.Set("X-A2A-Notification-Token", token)
	}
	if len(row.Authentication) > 0 && string(row.Authentication) != "null" {
		var envelope string
		if err := json.Unmarshal(row.Authentication, &envelope); err != nil {
			return fmt.Errorf("decode A2A push authentication envelope: %w", security.ErrPlaintextSecret)
		}
		plaintext, err := security.RevealOptional(
			d.secretStore,
			envelope,
			security.FieldAAD("agent_push_notification_configs", row.ID, "authentication"),
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

	client, err := security.NewPinnedHTTPSClient(
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

func newA2APushRequest(
	ctx context.Context,
	target string,
	payload json.RawMessage,
	eventID string,
) (*http.Request, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		target,
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return nil, errors.New("invalid A2A push callback URL")
	}
	request.Header.Set("Content-Type", "application/a2a+json")
	request.Header.Set("A2A-Version", a2a.ProtocolVersion)
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
