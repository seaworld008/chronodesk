package services

import (
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidActor              = errors.New("invalid actor")
	ErrInvalidScope              = errors.New("invalid agent scope")
	ErrPrincipalNotFound         = errors.New("service principal not found")
	ErrPrincipalDisabled         = errors.New("service principal disabled")
	ErrPrincipalExpired          = errors.New("service principal expired")
	ErrInvalidCredential         = errors.New("invalid agent credential")
	ErrCredentialExpired         = errors.New("agent credential expired")
	ErrPolicyDenied              = errors.New("agent policy denied")
	ErrGlobalEmergencyStop       = errors.New("agent operations are stopped")
	ErrReadOnlyMode              = errors.New("agent is in read-only mode")
	ErrRateLimited               = errors.New("agent rate limit exceeded")
	ErrConcurrencyLimit          = errors.New("agent concurrency limit exceeded")
	ErrExecutionGuardUnavailable = errors.New("agent execution guard is unavailable")
	ErrAutomationLoop            = errors.New("agent automation loop detected")
	ErrIdempotencyConflict       = errors.New("idempotency key request conflict")
	ErrIdempotencyInProgress     = errors.New("idempotent operation in progress")
	ErrCommandScopeMismatch      = errors.New("command fields do not match required scope")
	ErrVersionConflict           = errors.New("ticket version conflict")
	ErrLeaseConflict             = errors.New("ticket lease conflict")
	ErrLeaseExpired              = errors.New("ticket lease expired")
	ErrLeaseNotOwned             = errors.New("ticket lease is not owned by actor")
	ErrAttachmentStorageMissing  = errors.New("attachment storage is not configured")
	ErrAttachmentTooLarge        = errors.New("attachment exceeds size limit")
	ErrAttachmentNotClean        = errors.New("attachment is not cleared for download")
	ErrAttachmentUnavailable     = errors.New("attachment is unavailable")
	ErrInvalidAttachment         = errors.New("invalid attachment")
	ErrInvalidAttachmentName     = errors.New("invalid attachment name")
	ErrInvalidComment            = errors.New("invalid comment")
	ErrNestedCommentReply        = errors.New("nested comment replies are not supported")
	ErrInvalidAttachmentCleanup  = errors.New("invalid attachment cleanup destination")
	ErrOutboxLockLost            = errors.New("outbox delivery lock lost")
	ErrOutboxReplayConflict      = errors.New("outbox delivery is not replayable")
)

const (
	// AttachmentCleanupOutboxDestination routes a committed ticket deletion to
	// the configured AttachmentStorage. The destination identifier contains an
	// attachment ID and a hash of its storage key, never a path or URL.
	AttachmentCleanupOutboxDestination = "attachment_cleanup"
	// AttachmentUploadOutboxDestination copies a committed inbound staging
	// object into managed attachment storage. Its destination ID is only the
	// numeric Attachment record ID; staging and storage paths remain private.
	AttachmentUploadOutboxDestination = "attachment_upload"
	// AttachmentStagingCleanupOutboxDestination is registered before inbound
	// bytes are staged. If the process dies before the final attachment
	// transaction commits, its delayed worker can claim the durable intent,
	// delete the staged object and remove the placeholder row.
	AttachmentStagingCleanupOutboxDestination = "attachment_staging_cleanup"
	// AttachmentCleanupObjectsDataField is persisted only for Outbox recovery.
	// CloudEventFromModel removes it from every externally serialized envelope.
	AttachmentCleanupObjectsDataField = "_attachment_cleanup_objects"
	attachmentCleanupPrefix           = "attachment:"
	externalNotificationAction        = "external.notification.send"
	maxNativeCommentRunes             = 10000
	maxNativeCommentBytes             = maxNativeCommentRunes * utf8.UTFMax
	defaultIdempotencyRetentionTTL    = 24 * time.Hour
	defaultIdempotencyProcessingLease = 2 * time.Minute
	idempotencyFailureCleanupTimeout  = 5 * time.Second
	webhookConfiguredDestinationID    = "configured"
	webhookSnapshotDestinationPrefix  = "snapshot:"
)

// AgentNativeErrorCode turns exported sentinel errors into stable API codes.
func AgentNativeErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidTicketTags),
		errors.Is(err, ErrInvalidAgentContext),
		errors.Is(err, ErrTicketCategoryScope),
		errors.Is(err, ErrInvalidTicketCategorySelection):
		return "invalid_request"
	case errors.Is(err, ErrInvalidAssignee):
		return "invalid_assignee"
	case errors.Is(err, ErrAssigneeNotFound):
		return "assignee_not_found"
	case errors.Is(err, ErrAssigneePolicyDenied):
		return "assignee_policy_denied"
	case errors.Is(err, ErrInvalidActor):
		return "invalid_actor"
	case errors.Is(err, ErrInvalidScope):
		return "invalid_scope"
	case errors.Is(err, ErrPrincipalNotFound):
		return "principal_not_found"
	case errors.Is(err, ErrPrincipalDisabled):
		return "principal_disabled"
	case errors.Is(err, ErrPrincipalExpired):
		return "principal_expired"
	case errors.Is(err, ErrInvalidCredential):
		return "invalid_credential"
	case errors.Is(err, ErrCredentialExpired):
		return "credential_expired"
	case errors.Is(err, ErrPolicyDenied):
		return "policy_denied"
	case errors.Is(err, ErrProjectAccessDenied):
		return "policy_denied"
	case errors.Is(err, ErrGlobalEmergencyStop):
		return "agent_emergency_stop"
	case errors.Is(err, ErrReadOnlyMode):
		return "read_only"
	case errors.Is(err, ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrConcurrencyLimit):
		return "concurrency_limit"
	case errors.Is(err, ErrExecutionGuardUnavailable):
		return "execution_guard_unavailable"
	case errors.Is(err, ErrAutomationLoop):
		return "automation_loop"
	case errors.Is(err, ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, ErrIdempotencyInProgress):
		return "idempotency_in_progress"
	case errors.Is(err, ErrCommandScopeMismatch):
		return "command_scope_mismatch"
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict"
	case errors.Is(err, ErrLeaseConflict):
		return "lease_conflict"
	case errors.Is(err, ErrLeaseExpired):
		return "lease_expired"
	case errors.Is(err, ErrLeaseNotOwned):
		return "lease_not_owned"
	case errors.Is(err, ErrAttachmentTooLarge):
		return "attachment_too_large"
	case errors.Is(err, ErrAttachmentNotClean):
		return "attachment_not_clean"
	case errors.Is(err, ErrInvalidAttachment):
		return "invalid_attachment"
	case errors.Is(err, ErrInvalidAttachmentName):
		return "invalid_attachment_name"
	case errors.Is(err, ErrNestedCommentReply):
		return "nested_comment_reply"
	case errors.Is(err, ErrInvalidComment):
		return "invalid_comment"
	case errors.Is(err, ErrOutboxReplayConflict):
		return "outbox_replay_conflict"
	case errors.Is(err, ErrTicketConfigurationUnavailable):
		return "ticket_configuration_unavailable"
	case errors.Is(err, ErrTicketRequestTypeAmbiguous):
		return "request_type_version_required"
	case errors.Is(err, ErrTicketFormValidation):
		return "ticket_form_validation_failed"
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "not_found"
	default:
		return "internal_error"
	}
}

type OutboxTarget struct {
	Type        string
	ID          string
	MaxAttempts int
}

type AttachmentCleanupObject struct {
	AttachmentID uint   `json:"attachment_id"`
	TicketID     uint   `json:"ticket_id"`
	StorageType  string `json:"storage_type,omitempty"`
	StoreID      string `json:"store_id,omitempty"`
	StoragePath  string `json:"storage_path"`
	VersionID    string `json:"version_id,omitempty"`
}

// NewAttachmentCleanupOutboxTarget binds a cleanup delivery to the exact
// storage key observed inside the ticket deletion transaction. The key itself
// is deliberately not persisted in the public CloudEvent or destination ID.
func NewAttachmentCleanupOutboxTarget(
	attachmentID uint,
	storagePath string,
) (OutboxTarget, error) {
	if attachmentID == 0 || strings.TrimSpace(storagePath) == "" {
		return OutboxTarget{}, ErrInvalidAttachmentCleanup
	}
	sum := sha256.Sum256([]byte(storagePath))
	return OutboxTarget{
		Type: AttachmentCleanupOutboxDestination,
		ID: fmt.Sprintf(
			"%s%d:%s",
			attachmentCleanupPrefix,
			attachmentID,
			hex.EncodeToString(sum[:]),
		),
		MaxAttempts: 8,
	}, nil
}

func NewAttachmentCleanupOutboxTargetForObject(
	object AttachmentCleanupObject,
) (OutboxTarget, error) {
	if object.AttachmentID == 0 ||
		strings.TrimSpace(object.StoragePath) == "" ||
		!validAttachmentStoreID(object.StoreID) ||
		strings.TrimSpace(object.StorageType) == "" {
		return OutboxTarget{}, ErrInvalidAttachmentCleanup
	}
	sum := attachmentCleanupReferenceDigest(
		object.StorageType,
		object.StoreID,
		object.StoragePath,
		object.VersionID,
	)
	return OutboxTarget{
		Type: AttachmentCleanupOutboxDestination,
		ID: fmt.Sprintf(
			"%s%d:%s",
			attachmentCleanupPrefix,
			object.AttachmentID,
			hex.EncodeToString(sum[:]),
		),
		MaxAttempts: 8,
	}, nil
}

// ValidateAttachmentCleanupDestination verifies that a delivery still refers
// to the exact attachment ID and storage key captured by the deletion
// transaction.
func ValidateAttachmentCleanupDestination(
	destinationID string,
	storagePath string,
) (uint, error) {
	attachmentID, expectedHash, err := parseAttachmentCleanupDestination(destinationID)
	if err != nil {
		return 0, ErrInvalidAttachmentCleanup
	}
	sum := sha256.Sum256([]byte(storagePath))
	if expectedHash != hex.EncodeToString(sum[:]) {
		return 0, ErrInvalidAttachmentCleanup
	}
	return attachmentID, nil
}

func validateAttachmentCleanupObjectDestination(
	destinationID string,
	object AttachmentCleanupObject,
) (uint, error) {
	attachmentID, expectedHash, err := parseAttachmentCleanupDestination(
		destinationID,
	)
	if err != nil {
		return 0, ErrInvalidAttachmentCleanup
	}
	if object.StoreID == "" && object.VersionID == "" {
		return ValidateAttachmentCleanupDestination(
			destinationID,
			object.StoragePath,
		)
	}
	sum := attachmentCleanupReferenceDigest(
		object.StorageType,
		object.StoreID,
		object.StoragePath,
		object.VersionID,
	)
	if expectedHash != hex.EncodeToString(sum[:]) {
		return 0, ErrInvalidAttachmentCleanup
	}
	return attachmentID, nil
}

func attachmentCleanupReferenceDigest(
	storageType string,
	storeID string,
	storagePath string,
	versionID string,
) [sha256.Size]byte {
	return sha256.Sum256([]byte(
		strings.ToLower(strings.TrimSpace(storageType)) + "\x00" +
			normalizeAttachmentStoreID(storeID) + "\x00" +
			storagePath + "\x00" +
			versionID,
	))
}

func parseAttachmentCleanupDestination(destinationID string) (uint, string, error) {
	parts := strings.Split(destinationID, ":")
	if len(parts) != 3 || parts[0]+":" != attachmentCleanupPrefix {
		return 0, "", ErrInvalidAttachmentCleanup
	}
	attachmentID, err := safeconv.ParsePositiveUint(parts[1])
	if err != nil || len(parts[2]) != sha256.Size*2 {
		return 0, "", ErrInvalidAttachmentCleanup
	}
	if _, err := hex.DecodeString(parts[2]); err != nil {
		return 0, "", ErrInvalidAttachmentCleanup
	}
	return attachmentID, parts[2], nil
}

type AgentNativeOptions struct {
	CredentialPepper           []byte
	EventSource                string
	DefaultOutboxTargets       []OutboxTarget
	DefaultCredentialTTL       time.Duration
	MaxCredentialTTL           time.Duration
	DefaultLeaseTTL            time.Duration
	MaxLeaseTTL                time.Duration
	IdempotencyProcessingLease time.Duration
	AttachmentStorage          AttachmentStorage
	AttachmentStaging          AttachmentStagingStore
	AttachmentMaxBytes         int64
	// AttachmentDownloadConcurrency bounds all attachment downloads handled
	// by this process. A slot remains held until the returned reader is closed.
	AttachmentDownloadConcurrency int
	// AttachmentDownloadPerActorConcurrency prevents one Human, Agent, or
	// system actor from consuming the complete process-wide download budget.
	AttachmentDownloadPerActorConcurrency int
	OutboxLockTTL                         time.Duration
	OutboxDeliveryTimeout                 time.Duration
	OutboxDeliveryConcurrency             int
	LoopThreshold                         int
	LoopWindow                            time.Duration
	ExecutionGuard                        AgentExecutionGuard
	ExecutionLeaseTTL                     time.Duration
	AuditLedger                           *AuditLedgerService
	// RequireDistributedExecutionGuard prevents a production deployment from
	// silently falling back to process-local enforcement when Redis is absent.
	RequireDistributedExecutionGuard bool
	Now                              func() time.Time
}

// AgentNativeService owns identity, policy, event, outbox, idempotency, lease,
// comment and attachment invariants shared by REST, MCP and A2A adapters.
type AgentNativeService struct {
	db                         *gorm.DB
	sla                        *SLAService
	slaProjectionEnabled       bool
	credentialPepper           []byte
	eventSource                string
	defaultOutboxTargets       []OutboxTarget
	defaultCredentialTTL       time.Duration
	maxCredentialTTL           time.Duration
	defaultLeaseTTL            time.Duration
	maxLeaseTTL                time.Duration
	idempotencyProcessingLease time.Duration
	attachmentStorage          AttachmentStorage
	attachmentStaging          AttachmentStagingStore
	attachmentMaxBytes         int64
	attachmentDownloadSlots    chan struct{}
	attachmentDownloadPerActor int
	attachmentDownloadActorsMu sync.Mutex
	attachmentDownloadActors   map[string]*attachmentDownloadActorSlots
	outboxLockTTL              time.Duration
	outboxDeliveryTimeout      time.Duration
	outboxDeliverySlots        chan struct{}
	outboxProjectCursor        atomic.Uint64
	loopThreshold              int
	loopWindow                 time.Duration
	executionGuard             AgentExecutionGuard
	executionLeaseTTL          time.Duration
	auditLedger                *AuditLedgerService
	requireDistributedGuard    bool
	now                        func() time.Time

	globalEmergencyStop atomic.Bool
	globalReadOnly      atomic.Bool
}

type agentNativeTransactionContextKey struct{}

// InTransaction exposes one transaction boundary to protocol adapters without
// leaking the service's root DB handle. Service methods that call dbForContext
// automatically participate in this transaction.
func (s *AgentNativeService) InTransaction(
	ctx context.Context,
	fn func(context.Context, *gorm.DB) error,
) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	if existing, ok := ctx.Value(agentNativeTransactionContextKey{}).(*gorm.DB); ok && existing != nil {
		return fn(ctx, existing.WithContext(ctx))
	}
	return transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, agentNativeTransactionContextKey{}, tx)
		return fn(txCtx, tx.WithContext(txCtx))
	})
}

func (s *AgentNativeService) dbForContext(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(agentNativeTransactionContextKey{}).(*gorm.DB); ok && tx != nil {
		return tx.WithContext(ctx)
	}
	return s.db.WithContext(ctx)
}

func (s *AgentNativeService) projectTicketSLAOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticket *models.Ticket,
	now time.Time,
) (slaProjection, *TicketSLAStatus, error) {
	if s.sla == nil || db == nil || !s.slaProjectionEnabled {
		return slaProjection{
			DueDate:  ticket.SLADueDate,
			Breached: ticket.SLABreached,
		}, nil, nil
	}
	return s.sla.projectionForTicketOnDB(ctx, db, ticket, now)
}

func NewAgentNativeService(db *gorm.DB, provided ...AgentNativeOptions) *AgentNativeService {
	options := AgentNativeOptions{}
	if len(provided) > 0 {
		options = provided[0]
	}
	if options.EventSource == "" {
		options.EventSource = "urn:chronodesk:server"
	}
	if len(options.DefaultOutboxTargets) == 0 {
		options.DefaultOutboxTargets = []OutboxTarget{{Type: "event_stream", ID: "default", MaxAttempts: 8}}
	}
	if options.DefaultCredentialTTL <= 0 {
		options.DefaultCredentialTTL = 90 * 24 * time.Hour
	}
	if options.MaxCredentialTTL <= 0 {
		options.MaxCredentialTTL = 365 * 24 * time.Hour
	}
	if options.DefaultLeaseTTL <= 0 {
		options.DefaultLeaseTTL = 2 * time.Minute
	}
	if options.MaxLeaseTTL <= 0 {
		options.MaxLeaseTTL = 15 * time.Minute
	}
	if options.IdempotencyProcessingLease <= 0 {
		options.IdempotencyProcessingLease = defaultIdempotencyProcessingLease
	}
	if options.AttachmentMaxBytes <= 0 {
		options.AttachmentMaxBytes = 25 << 20
	}
	if options.AttachmentStaging == nil {
		if staging, ok := options.AttachmentStorage.(AttachmentStagingStore); ok {
			options.AttachmentStaging = staging
		}
	}
	if options.AttachmentDownloadConcurrency <= 0 {
		options.AttachmentDownloadConcurrency =
			defaultAttachmentDownloadConcurrency
	}
	if options.AttachmentDownloadConcurrency >
		maxAttachmentDownloadConcurrency {
		options.AttachmentDownloadConcurrency =
			maxAttachmentDownloadConcurrency
	}
	if options.AttachmentDownloadPerActorConcurrency <= 0 {
		options.AttachmentDownloadPerActorConcurrency =
			defaultAttachmentDownloadPerActorConcurrency
	}
	if options.AttachmentDownloadPerActorConcurrency >
		maxAttachmentDownloadPerActorConcurrency {
		options.AttachmentDownloadPerActorConcurrency =
			maxAttachmentDownloadPerActorConcurrency
	}
	if options.AttachmentDownloadPerActorConcurrency >
		options.AttachmentDownloadConcurrency {
		options.AttachmentDownloadPerActorConcurrency =
			options.AttachmentDownloadConcurrency
	}
	if options.OutboxLockTTL <= 0 {
		options.OutboxLockTTL = 2 * time.Minute
	}
	if options.OutboxDeliveryTimeout <= 0 {
		options.OutboxDeliveryTimeout = 30 * time.Second
	}
	if options.OutboxDeliveryTimeout >= options.OutboxLockTTL {
		options.OutboxDeliveryTimeout = options.OutboxLockTTL / 2
	}
	if options.OutboxDeliveryConcurrency <= 0 {
		options.OutboxDeliveryConcurrency = 8
	}
	if options.LoopThreshold <= 0 {
		options.LoopThreshold = 20
	}
	if options.LoopWindow <= 0 {
		options.LoopWindow = time.Minute
	}
	if options.ExecutionLeaseTTL <= 0 {
		options.ExecutionLeaseTTL = defaultAgentExecutionLeaseTTL
	}
	if options.ExecutionGuard == nil && !options.RequireDistributedExecutionGuard {
		options.ExecutionGuard = NewInMemoryAgentExecutionGuardForTesting()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	slaProjectionEnabled := db != nil && db.Migrator().HasTable(&models.SLAConfig{})

	return &AgentNativeService{
		db:                         db,
		sla:                        newSLAServiceWithClock(db, options.Now),
		slaProjectionEnabled:       slaProjectionEnabled,
		credentialPepper:           append([]byte(nil), options.CredentialPepper...),
		eventSource:                options.EventSource,
		defaultOutboxTargets:       append([]OutboxTarget(nil), options.DefaultOutboxTargets...),
		defaultCredentialTTL:       options.DefaultCredentialTTL,
		maxCredentialTTL:           options.MaxCredentialTTL,
		defaultLeaseTTL:            options.DefaultLeaseTTL,
		maxLeaseTTL:                options.MaxLeaseTTL,
		idempotencyProcessingLease: options.IdempotencyProcessingLease,
		attachmentStorage:          options.AttachmentStorage,
		attachmentStaging:          options.AttachmentStaging,
		attachmentMaxBytes:         options.AttachmentMaxBytes,
		attachmentDownloadSlots: make(
			chan struct{},
			options.AttachmentDownloadConcurrency,
		),
		attachmentDownloadPerActor: options.AttachmentDownloadPerActorConcurrency,
		attachmentDownloadActors: make(
			map[string]*attachmentDownloadActorSlots,
		),
		outboxLockTTL:           options.OutboxLockTTL,
		outboxDeliveryTimeout:   options.OutboxDeliveryTimeout,
		outboxDeliverySlots:     make(chan struct{}, options.OutboxDeliveryConcurrency),
		loopThreshold:           options.LoopThreshold,
		loopWindow:              options.LoopWindow,
		executionGuard:          options.ExecutionGuard,
		executionLeaseTTL:       options.ExecutionLeaseTTL,
		auditLedger:             options.AuditLedger,
		requireDistributedGuard: options.RequireDistributedExecutionGuard,
		now:                     options.Now,
	}
}

func (s *AgentNativeService) SetGlobalEmergencyStop(enabled bool) {
	s.globalEmergencyStop.Store(enabled)
}

func (s *AgentNativeService) SetGlobalReadOnly(enabled bool) {
	s.globalReadOnly.Store(enabled)
}

type CreateServicePrincipalInput struct {
	Name               string
	Description        string
	Scopes             []string
	RateLimitPerMinute int
	ConcurrentLimit    int
	ExpiresAt          *time.Time
	ReadOnly           bool
	CreatedByID        *uint
}

func (s *AgentNativeService) CreateServicePrincipal(ctx context.Context, input CreateServicePrincipalInput) (*models.ServicePrincipal, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("service principal name is required")
	}
	scopes, err := normalizeAgentScopes(input.Scopes)
	if err != nil {
		return nil, err
	}
	scopeJSON, err := json.Marshal(scopes)
	if err != nil {
		return nil, fmt.Errorf("encode scopes: %w", err)
	}
	if input.RateLimitPerMinute <= 0 {
		input.RateLimitPerMinute = 60
	}
	if input.ConcurrentLimit <= 0 {
		input.ConcurrentLimit = 4
	}
	principal := &models.ServicePrincipal{
		ID:                 newNativeID(),
		Name:               name,
		Description:        strings.TrimSpace(input.Description),
		Status:             models.ServicePrincipalStatusActive,
		Scopes:             datatypes.JSON(scopeJSON),
		RateLimitPerMinute: input.RateLimitPerMinute,
		ConcurrentLimit:    input.ConcurrentLimit,
		ExpiresAt:          input.ExpiresAt,
		ReadOnly:           input.ReadOnly,
		CreatedByID:        input.CreatedByID,
	}
	if err := s.dbForContext(ctx).Create(principal).Error; err != nil {
		return nil, fmt.Errorf("create service principal: %w", err)
	}
	return principal, nil
}

func (s *AgentNativeService) GetServicePrincipal(ctx context.Context, principalID string) (*models.ServicePrincipal, error) {
	var principal models.ServicePrincipal
	if err := s.dbForContext(ctx).First(&principal, "id = ?", principalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPrincipalNotFound
		}
		return nil, fmt.Errorf("get service principal: %w", err)
	}
	return &principal, nil
}

func (s *AgentNativeService) SetServicePrincipalControls(
	ctx context.Context,
	principalID string,
	status models.ServicePrincipalStatus,
	readOnly bool,
	emergencyDisabled bool,
) (*models.ServicePrincipal, error) {
	if status != models.ServicePrincipalStatusActive &&
		status != models.ServicePrincipalStatusInactive &&
		status != models.ServicePrincipalStatusRevoked {
		return nil, fmt.Errorf("invalid service principal status %q", status)
	}
	result := s.dbForContext(ctx).Model(&models.ServicePrincipal{}).
		Where("id = ?", principalID).
		Updates(map[string]any{
			"status":             status,
			"read_only":          readOnly,
			"emergency_disabled": emergencyDisabled,
			"updated_at":         s.now(),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("update service principal controls: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, ErrPrincipalNotFound
	}
	return s.GetServicePrincipal(ctx, principalID)
}

type IssuedAgentCredential struct {
	Credential *models.AgentCredential `json:"credential"`
	Secret     string                  `json:"secret"`
	Token      string                  `json:"token"`
}

func (s *AgentNativeService) IssueCredential(
	ctx context.Context,
	principalID string,
	name string,
	ttl time.Duration,
) (*IssuedAgentCredential, error) {
	principal, err := s.getUsablePrincipal(ctx, principalID)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = s.defaultCredentialTTL
	}
	if ttl < time.Minute || ttl > s.maxCredentialTTL {
		return nil, fmt.Errorf("credential ttl must be between 1m and %s", s.maxCredentialTTL)
	}
	secretBytes := make([]byte, 32)
	if _, err := cryptorand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("generate credential: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	credential := &models.AgentCredential{
		ID:                 newNativeID(),
		ServicePrincipalID: principal.ID,
		Name:               strings.TrimSpace(name),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          s.now().Add(ttl),
	}
	if credential.Name == "" {
		credential.Name = "default"
	}
	credential.SecretHash = s.hashCredential(credential.ID, secret)
	if err := s.dbForContext(ctx).Create(credential).Error; err != nil {
		return nil, fmt.Errorf("create agent credential: %w", err)
	}
	return &IssuedAgentCredential{
		Credential: credential,
		Secret:     secret,
		Token:      credential.ID + "." + secret,
	}, nil
}

// RotateCredential creates the replacement and revokes every prior active
// credential in one transaction, so a partial rotation cannot leave an
// unexpected old credential usable.
func (s *AgentNativeService) RotateCredential(
	ctx context.Context,
	principalID string,
	name string,
	ttl time.Duration,
	actor models.ActorRef,
) (*IssuedAgentCredential, error) {
	if err := actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	principal, err := s.getUsablePrincipal(ctx, principalID)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = s.defaultCredentialTTL
	}
	if ttl < time.Minute || ttl > s.maxCredentialTTL {
		return nil, fmt.Errorf("credential ttl must be between 1m and %s", s.maxCredentialTTL)
	}
	secretBytes := make([]byte, 32)
	if _, err := cryptorand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("generate credential: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	credential := &models.AgentCredential{
		ID:                 newNativeID(),
		ServicePrincipalID: principal.ID,
		Name:               strings.TrimSpace(name),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          s.now().Add(ttl),
	}
	if credential.Name == "" {
		credential.Name = "rotated"
	}
	credential.SecretHash = s.hashCredential(credential.ID, secret)
	now := s.now()
	err = s.InTransaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		if err := tx.Create(credential).Error; err != nil {
			return err
		}
		return tx.Model(&models.AgentCredential{}).
			Where(
				"service_principal_id = ? AND id <> ? AND status = ?",
				principal.ID,
				credential.ID,
				models.AgentCredentialStatusActive,
			).
			Updates(map[string]any{
				"status":                models.AgentCredentialStatusRevoked,
				"revoked_at":            now,
				"revoked_by_actor_type": actor.Type,
				"revoked_by_actor_id":   actor.ID,
				"updated_at":            now,
			}).Error
	})
	if err != nil {
		return nil, fmt.Errorf("rotate agent credential: %w", err)
	}
	return &IssuedAgentCredential{
		Credential: credential,
		Secret:     secret,
		Token:      credential.ID + "." + secret,
	}, nil
}

func (s *AgentNativeService) ValidateCredentialToken(
	ctx context.Context,
	token string,
) (*models.ServicePrincipal, *models.AgentCredential, error) {
	credentialID, secret, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || credentialID == "" || secret == "" {
		return nil, nil, ErrInvalidCredential
	}
	return s.ValidateCredential(ctx, credentialID, secret)
}

func (s *AgentNativeService) ValidateCredential(
	ctx context.Context,
	credentialID string,
	secret string,
) (*models.ServicePrincipal, *models.AgentCredential, error) {
	var credential models.AgentCredential
	if err := s.db.WithContext(ctx).First(&credential, "id = ?", credentialID).Error; err != nil {
		return nil, nil, ErrInvalidCredential
	}
	expected := s.hashCredential(credential.ID, secret)
	if !hmac.Equal([]byte(expected), []byte(credential.SecretHash)) {
		return nil, nil, ErrInvalidCredential
	}
	now := s.now()
	if credential.Status != models.AgentCredentialStatusActive || credential.RevokedAt != nil {
		return nil, nil, ErrInvalidCredential
	}
	if !credential.ExpiresAt.After(now) {
		return nil, nil, ErrCredentialExpired
	}
	principal, err := s.getUsablePrincipal(ctx, credential.ServicePrincipalID)
	if err != nil {
		return nil, nil, err
	}
	if err := transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		if err := tx.Model(&models.AgentCredential{}).
			Where("id = ?", credential.ID).
			Update("last_used_at", now).Error; err != nil {
			return err
		}
		return tx.Model(&models.ServicePrincipal{}).
			Where("id = ?", principal.ID).
			Update("last_used_at", now).Error
	}); err != nil {
		return nil, nil, fmt.Errorf("update credential usage: %w", err)
	}
	credential.LastUsedAt = &now
	principal.LastUsedAt = &now
	return principal, &credential, nil
}

// ValidateCredentialReference revalidates the credential embedded in a
// short-lived access token without requiring the original client secret. This
// makes revocation and emergency controls effective immediately.
func (s *AgentNativeService) ValidateCredentialReference(
	ctx context.Context,
	principalID string,
	credentialID string,
) error {
	if _, err := s.getUsablePrincipal(ctx, principalID); err != nil {
		return err
	}
	var credential models.AgentCredential
	if err := s.dbForContext(ctx).
		Where("id = ? AND service_principal_id = ?", credentialID, principalID).
		First(&credential).Error; err != nil {
		return ErrInvalidCredential
	}
	if credential.Status != models.AgentCredentialStatusActive ||
		credential.RevokedAt != nil ||
		!credential.ExpiresAt.After(s.now()) {
		return ErrInvalidCredential
	}
	return nil
}

func (s *AgentNativeService) RevokeCredential(ctx context.Context, credentialID string, actor models.ActorRef) error {
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	now := s.now()
	result := s.dbForContext(ctx).Model(&models.AgentCredential{}).
		Where("id = ? AND status = ?", credentialID, models.AgentCredentialStatusActive).
		Updates(map[string]any{
			"status":                models.AgentCredentialStatusRevoked,
			"revoked_at":            now,
			"revoked_by_actor_type": actor.Type,
			"revoked_by_actor_id":   actor.ID,
			"updated_at":            now,
		})
	if result.Error != nil {
		return fmt.Errorf("revoke credential: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrInvalidCredential
	}
	return nil
}

func (s *AgentNativeService) getUsablePrincipal(ctx context.Context, principalID string) (*models.ServicePrincipal, error) {
	principal, err := s.GetServicePrincipal(ctx, principalID)
	if err != nil {
		return nil, err
	}
	if principal.Status != models.ServicePrincipalStatusActive || principal.EmergencyDisabled {
		return nil, ErrPrincipalDisabled
	}
	if principal.ExpiresAt != nil && !principal.ExpiresAt.After(s.now()) {
		return nil, ErrPrincipalExpired
	}
	return principal, nil
}

func (s *AgentNativeService) hashCredential(credentialID, secret string) string {
	if len(s.credentialPepper) == 0 {
		sum := sha256.Sum256([]byte(credentialID + ":" + secret))
		return hex.EncodeToString(sum[:])
	}
	mac := hmac.New(sha256.New, s.credentialPepper)
	_, _ = mac.Write([]byte(credentialID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeAgentScopes(scopes []string) ([]string, error) {
	supported := make(map[string]struct{}, len(models.SupportedAgentScopes))
	for _, scope := range models.SupportedAgentScopes {
		supported[scope] = struct{}{}
	}
	seen := make(map[string]struct{}, len(scopes))
	result := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		scope := strings.TrimSpace(raw)
		if _, ok := supported[scope]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrInvalidScope, scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	return result, nil
}

type CreateAgentPolicyInput struct {
	ServicePrincipalID string
	Name               string
	Effect             models.AgentPolicyEffect
	Scope              string
	Action             string
	ResourceType       string
	ResourceID         string
	Conditions         map[string]any
	Priority           int
	ExpiresAt          *time.Time
}

func (s *AgentNativeService) CreateAgentPolicy(ctx context.Context, input CreateAgentPolicyInput) (*models.AgentPolicy, error) {
	if input.Effect != models.AgentPolicyEffectAllow && input.Effect != models.AgentPolicyEffectDeny {
		return nil, fmt.Errorf("invalid policy effect %q", input.Effect)
	}
	if input.Scope != "*" {
		if _, err := normalizeAgentScopes([]string{input.Scope}); err != nil {
			return nil, err
		}
	}
	conditions, err := json.Marshal(input.Conditions)
	if err != nil {
		return nil, fmt.Errorf("encode policy conditions: %w", err)
	}
	policy := &models.AgentPolicy{
		ID:                 newNativeID(),
		ServicePrincipalID: input.ServicePrincipalID,
		Name:               strings.TrimSpace(input.Name),
		Effect:             input.Effect,
		Scope:              input.Scope,
		Action:             strings.TrimSpace(input.Action),
		ResourceType:       strings.TrimSpace(input.ResourceType),
		ResourceID:         strings.TrimSpace(input.ResourceID),
		Conditions:         datatypes.JSON(conditions),
		Priority:           input.Priority,
		IsActive:           true,
		ExpiresAt:          input.ExpiresAt,
	}
	if policy.Name == "" {
		policy.Name = string(policy.Effect) + " " + policy.Scope
	}
	err = s.InTransaction(
		ctx,
		func(txCtx context.Context, tx *gorm.DB) error {
			var principal models.ServicePrincipal
			if lockErr := tx.WithContext(txCtx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", input.ServicePrincipalID).
				Take(&principal).Error; lockErr != nil {
				if errors.Is(lockErr, gorm.ErrRecordNotFound) {
					return ErrPrincipalNotFound
				}
				return lockErr
			}
			if createErr := tx.WithContext(txCtx).
				Create(policy).Error; createErr != nil {
				return createErr
			}
			currentEpoch := principal.PolicyEpoch
			if currentEpoch == 0 {
				currentEpoch = 1
			}
			update := tx.WithContext(txCtx).
				Model(&models.ServicePrincipal{}).
				Where(
					"id = ? AND policy_epoch = ?",
					principal.ID,
					principal.PolicyEpoch,
				).
				Updates(map[string]any{
					"policy_epoch": currentEpoch + 1,
					"updated_at":   s.now().UTC(),
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrPolicyDenied
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create agent policy: %w", err)
	}
	return policy, nil
}

func (s *AgentNativeService) SetAgentPolicyActive(
	ctx context.Context,
	principalID string,
	policyID string,
	active bool,
) (*models.AgentPolicy, error) {
	principalID = strings.TrimSpace(principalID)
	policyID = strings.TrimSpace(policyID)
	if s == nil || s.db == nil ||
		principalID == "" ||
		policyID == "" {
		return nil, ErrPrincipalNotFound
	}
	var policy models.AgentPolicy
	err := s.InTransaction(
		ctx,
		func(txCtx context.Context, tx *gorm.DB) error {
			var principal models.ServicePrincipal
			if lockErr := tx.WithContext(txCtx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", principalID).
				Take(&principal).Error; lockErr != nil {
				if errors.Is(lockErr, gorm.ErrRecordNotFound) {
					return ErrPrincipalNotFound
				}
				return lockErr
			}
			if lockErr := tx.WithContext(txCtx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND service_principal_id = ?",
					policyID,
					principalID,
				).
				Take(&policy).Error; lockErr != nil {
				return lockErr
			}
			if policy.IsActive == active {
				return nil
			}
			now := s.now().UTC()
			updatePolicy := tx.WithContext(txCtx).
				Model(&models.AgentPolicy{}).
				Where(
					"id = ? AND service_principal_id = ? AND is_active = ?",
					policy.ID,
					principalID,
					policy.IsActive,
				).
				Updates(map[string]any{
					"is_active":  active,
					"updated_at": now,
				})
			if updatePolicy.Error != nil {
				return updatePolicy.Error
			}
			if updatePolicy.RowsAffected != 1 {
				return ErrPolicyDenied
			}
			currentEpoch := principal.PolicyEpoch
			if currentEpoch == 0 {
				currentEpoch = 1
			}
			updatePrincipal := tx.WithContext(txCtx).
				Model(&models.ServicePrincipal{}).
				Where(
					"id = ? AND policy_epoch = ?",
					principal.ID,
					principal.PolicyEpoch,
				).
				Updates(map[string]any{
					"policy_epoch": currentEpoch + 1,
					"updated_at":   now,
				})
			if updatePrincipal.Error != nil {
				return updatePrincipal.Error
			}
			if updatePrincipal.RowsAffected != 1 {
				return ErrPolicyDenied
			}
			policy.IsActive = active
			policy.UpdatedAt = now
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

type PolicyCheckInput struct {
	ServicePrincipalID string
	CredentialID       string
	Scope              string
	Action             string
	ResourceType       string
	ResourceID         string
	IsWrite            bool
	IsRisky            bool
	RequestDigest      string
	SourceProtocol     string
	Context            map[string]any
}

type preparedPolicyCheck struct {
	input         PolicyCheckInput
	decision      models.PolicyDecision
	guardRequired bool
}

// PolicyDecisionAuthorization binds one already-persisted PolicyDecision to
// the exact action it authorized. Protocol adapters use this only after the
// execution guard and PolicyDecision have completed outside the later business
// transaction.
type PolicyDecisionAuthorization struct {
	Input      PolicyCheckInput
	DecisionID string
}

type policyDecisionAuthorizationContextKey struct{}

type policyDecisionAuthorizationContext struct {
	service *AgentNativeService
	byKey   map[string]string
}

// RequirePolicyDecisionAuthorizations installs an empty fail-closed binding.
// A protocol adapter uses it before entering a business transaction so any
// unexpected guarded action is rejected instead of invoking the execution
// guard from inside that transaction.
func (s *AgentNativeService) RequirePolicyDecisionAuthorizations(
	ctx context.Context,
) (context.Context, error) {
	if s == nil || ctx == nil {
		return nil, errors.New("policy decision authorization context is required")
	}
	return context.WithValue(
		ctx,
		policyDecisionAuthorizationContextKey{},
		policyDecisionAuthorizationContext{
			service: s,
			byKey:   map[string]string{},
		},
	), nil
}

// WithPolicyDecisionAuthorizations installs validated, request-local
// references to PolicyDecisions. CheckAction revalidates the referenced
// decision and current principal controls before reusing it, so callers cannot
// turn a stale or mismatched decision into authority.
func (s *AgentNativeService) WithPolicyDecisionAuthorizations(
	ctx context.Context,
	authorizations ...PolicyDecisionAuthorization,
) (context.Context, error) {
	if s == nil || ctx == nil || len(authorizations) == 0 {
		return nil, errors.New("policy decision authorizations are required")
	}
	binding := policyDecisionAuthorizationContext{
		service: s,
		byKey:   make(map[string]string, len(authorizations)),
	}
	for _, authorization := range authorizations {
		decisionID := strings.TrimSpace(authorization.DecisionID)
		if decisionID == "" {
			return nil, errors.New("policy decision authorization requires a decision id")
		}
		key, err := policyCheckAuthorizationKey(authorization.Input)
		if err != nil {
			return nil, err
		}
		if existing := binding.byKey[key]; existing != "" && existing != decisionID {
			return nil, errors.New("policy action has conflicting decision authorizations")
		}
		binding.byKey[key] = decisionID
	}
	return context.WithValue(
		ctx,
		policyDecisionAuthorizationContextKey{},
		binding,
	), nil
}

func (s *AgentNativeService) CheckAction(ctx context.Context, input PolicyCheckInput) (*models.PolicyDecision, error) {
	if binding, ok := ctx.Value(
		policyDecisionAuthorizationContextKey{},
	).(policyDecisionAuthorizationContext); ok {
		if binding.service != s {
			return nil, errors.New("policy decision authorization belongs to another service")
		}
		key, err := policyCheckAuthorizationKey(input)
		if err != nil {
			return nil, err
		}
		decisionID := binding.byKey[key]
		if decisionID == "" {
			return nil, errors.New(
				"business action is missing its prepared policy decision",
			)
		}
		decision, err := s.loadMatchingPolicyDecision(
			ctx,
			decisionID,
			models.ServicePrincipalActor(input.ServicePrincipalID),
			input,
			true,
		)
		if err != nil {
			return decision, err
		}
		return decision, policyDecisionOutcome(decision)
	}
	prepared, err := s.preparePolicyCheck(ctx, input)
	if err != nil {
		return nil, err
	}
	s.applyPolicyExecutionGuard(ctx, prepared)
	decision, err := s.persistPreparedPolicyCheck(ctx, prepared)
	if err != nil {
		return nil, err
	}
	return decision, policyDecisionOutcome(decision)
}

// CheckActionInShortProjectTransactions separates database policy work from
// the distributed execution guard. The policy snapshot and final immutable
// PolicyDecision each use a short trusted project transaction; Redis or another
// AgentExecutionGuard is invoked only after the snapshot transaction closes.
func (s *AgentNativeService) CheckActionInShortProjectTransactions(
	ctx context.Context,
	input PolicyCheckInput,
) (*models.PolicyDecision, error) {
	if s == nil {
		return nil, errors.New("Agent service is required")
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, errors.New(
			"short policy check requires a context outside a project transaction",
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor !=
		models.ServicePrincipalActor(input.ServicePrincipalID) ||
		strings.TrimSpace(input.CredentialID) == "" ||
		operation.CredentialID != input.CredentialID ||
		(input.SourceProtocol != "" &&
			string(operation.Source) != input.SourceProtocol) {
		return nil, errors.New(
			"policy check does not match trusted operation context",
		)
	}

	var (
		prepared      *preparedPolicyCheck
		initialAccess *ProjectAccess
	)
	if err := s.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			var revalidateErr error
			initialAccess, revalidateErr =
				s.RevalidatePrincipalProjectOperation(
					scopedContext,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			var prepareErr error
			prepared, prepareErr = s.preparePolicyCheck(
				scopedContext,
				input,
			)
			if prepareErr == nil &&
				!projectAccessHasScope(initialAccess, input.Scope) {
				denyPreparedPolicyForMissingGrantScope(prepared)
			}
			return prepareErr
		},
	); err != nil {
		return nil, err
	}

	s.applyPolicyExecutionGuard(ctx, prepared)

	var (
		decision   *models.PolicyDecision
		outcomeErr error
	)
	transactionErr := s.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			currentAccess, revalidateErr :=
				s.RevalidatePrincipalProjectOperation(
					scopedContext,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if initialAccess == nil ||
				!initialAccess.AuthorizationSnapshot.Matches(
					currentAccess.AuthorizationSnapshot,
				) {
				return ErrProjectAccessDenied
			}
			if !projectAccessHasScope(currentAccess, input.Scope) {
				denyPreparedPolicyForMissingGrantScope(prepared)
			}
			var persistErr error
			decision, persistErr = s.persistPreparedPolicyCheck(
				scopedContext,
				prepared,
			)
			if persistErr != nil {
				return persistErr
			}
			outcomeErr = policyDecisionOutcome(decision)
			// Denials are durable audit outcomes. Commit their decision and
			// return the domain error only after the short transaction closes.
			return nil
		},
	)
	if transactionErr != nil {
		return nil, transactionErr
	}
	return decision, outcomeErr
}

func projectAccessHasScope(access *ProjectAccess, required string) bool {
	if access == nil || strings.TrimSpace(required) == "" {
		return false
	}
	for _, scope := range access.Scopes {
		if scope == required {
			return true
		}
	}
	return false
}

func denyPreparedPolicyForMissingGrantScope(
	prepared *preparedPolicyCheck,
) {
	if prepared == nil {
		return
	}
	prepared.decision.Allowed = false
	prepared.decision.ReasonCode = "scope_not_granted"
	prepared.decision.MatchedPolicyID = ""
	prepared.guardRequired = false
}

func (s *AgentNativeService) preparePolicyCheck(
	ctx context.Context,
	input PolicyCheckInput,
) (*preparedPolicyCheck, error) {
	db := s.dbForContext(ctx)
	loopGuarded := input.IsWrite && input.RequestDigest != "" && s.loopThreshold > 0
	principal, principalErr := s.getUsablePrincipal(ctx, input.ServicePrincipalID)
	var credentialErr error
	if principalErr == nil && input.CredentialID != "" {
		credentialErr = s.ValidateCredentialReference(
			ctx,
			input.ServicePrincipalID,
			input.CredentialID,
		)
	}
	allowed := true
	reason := "scope_allowed"
	var matchedPolicyID string

	switch {
	case s.globalEmergencyStop.Load():
		allowed, reason = false, "global_emergency_stop"
	case principalErr != nil:
		allowed, reason = false, AgentNativeErrorCode(principalErr)
	case credentialErr != nil:
		allowed, reason = false, "invalid_credential"
	case s.globalReadOnly.Load() && input.IsWrite:
		allowed, reason = false, "global_read_only"
	case principal.ReadOnly && input.IsWrite:
		allowed, reason = false, "principal_read_only"
	case !principal.HasScope(input.Scope):
		allowed, reason = false, "scope_not_granted"
	}

	var policies []models.AgentPolicy
	if principalErr == nil {
		if err := db.
			Where("service_principal_id = ? AND is_active = ?", input.ServicePrincipalID, true).
			Where("expires_at IS NULL OR expires_at > ?", s.now()).
			Order("priority DESC, created_at ASC").
			Find(&policies).Error; err != nil {
			return nil, fmt.Errorf("load agent policies: %w", err)
		}
	}

	explicitAllow := false
	for i := range policies {
		policy := &policies[i]
		if !policyMatches(policy, input) {
			continue
		}
		if policy.Effect == models.AgentPolicyEffectDeny {
			allowed = false
			reason = "explicit_deny"
			matchedPolicyID = policy.ID
			break
		}
		if policy.Effect == models.AgentPolicyEffectAllow && !explicitAllow {
			explicitAllow = true
			matchedPolicyID = policy.ID
		}
	}
	if allowed && input.IsRisky && !explicitAllow {
		allowed = false
		reason = "explicit_allow_required"
		matchedPolicyID = ""
	} else if allowed && explicitAllow {
		reason = "explicit_allow"
	}
	contextJSON, err := json.Marshal(input.Context)
	if err != nil {
		return nil, fmt.Errorf("encode policy context: %w", err)
	}
	return &preparedPolicyCheck{
		input: input,
		decision: models.PolicyDecision{
			ServicePrincipalID: input.ServicePrincipalID,
			CredentialID:       input.CredentialID,
			ActorType:          models.ActorTypeServicePrincipal,
			ActorID:            input.ServicePrincipalID,
			Scope:              input.Scope,
			Action:             input.Action,
			ResourceType:       input.ResourceType,
			ResourceID:         input.ResourceID,
			IsWrite:            input.IsWrite,
			IsRisky:            input.IsRisky,
			Allowed:            allowed,
			ReasonCode:         reason,
			MatchedPolicyID:    matchedPolicyID,
			PolicyEpoch:        principal.PolicyEpoch,
			RequestDigest:      input.RequestDigest,
			SourceProtocol:     input.SourceProtocol,
			Context:            datatypes.JSON(contextJSON),
		},
		guardRequired: allowed && loopGuarded,
	}, nil
}

func (s *AgentNativeService) applyPolicyExecutionGuard(
	ctx context.Context,
	prepared *preparedPolicyCheck,
) {
	if prepared == nil || !prepared.guardRequired {
		return
	}
	if !s.executionGuardReady() {
		prepared.decision.Allowed = false
		prepared.decision.ReasonCode = "execution_guard_unavailable"
		return
	}
	loopDetected, guardErr := s.executionGuard.RecordLoop(
		ctx,
		AgentLoopGuardRequest{
			Fingerprint:       agentLoopFingerprint(prepared.input),
			Threshold:         s.loopThreshold,
			Window:            s.loopWindow,
			ObservedAtForTest: s.now(),
		},
	)
	switch {
	case guardErr != nil:
		prepared.decision.Allowed = false
		prepared.decision.ReasonCode = "execution_guard_unavailable"
	case loopDetected:
		prepared.decision.Allowed = false
		prepared.decision.ReasonCode = "automation_loop"
	}
}

func (s *AgentNativeService) persistPreparedPolicyCheck(
	ctx context.Context,
	prepared *preparedPolicyCheck,
) (*models.PolicyDecision, error) {
	if prepared == nil {
		return nil, errors.New("prepared policy check is required")
	}
	decision := prepared.decision
	decision.ID = newNativeID()
	decision.CreatedAt = s.now()
	if operationContext, contextErr := OperationContextFromContext(ctx); contextErr == nil &&
		operationContext.Actor ==
			models.ServicePrincipalActor(prepared.input.ServicePrincipalID) {
		decision.OrganizationID = operationContext.Scope.OrganizationID
		decision.ProjectID = operationContext.Scope.ProjectID
	}
	if err := s.dbForContext(ctx).Create(&decision).Error; err != nil {
		return nil, fmt.Errorf("persist policy decision: %w", err)
	}
	return &decision, nil
}

func policyDecisionOutcome(decision *models.PolicyDecision) error {
	if decision == nil || decision.Allowed {
		return nil
	}
	switch decision.ReasonCode {
	case "global_emergency_stop":
		return ErrGlobalEmergencyStop
	case "global_read_only", "principal_read_only":
		return ErrReadOnlyMode
	case "principal_disabled":
		return ErrPrincipalDisabled
	case "principal_expired":
		return ErrPrincipalExpired
	case "invalid_credential":
		return ErrInvalidCredential
	case "automation_loop":
		return ErrAutomationLoop
	case "execution_guard_unavailable":
		return ErrExecutionGuardUnavailable
	default:
		return fmt.Errorf("%w: %s", ErrPolicyDenied, decision.ReasonCode)
	}
}

func policyCheckAuthorizationKey(input PolicyCheckInput) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode policy decision authorization: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func agentLoopFingerprint(input PolicyCheckInput) string {
	encoded, _ := json.Marshal([]string{
		input.ServicePrincipalID,
		input.Action,
		input.ResourceType,
		input.ResourceID,
		input.RequestDigest,
	})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// externalNotificationsAllowed is deliberately separate from the business
// command authorization. The principal needs events:subscribe and a matching
// explicit allow policy for external.notification.send; otherwise the command
// still succeeds but its event remains inside ChronoDesk.
func (s *AgentNativeService) externalNotificationsAllowed(
	ctx context.Context,
	actor models.ActorRef,
	credentialID string,
	resourceID string,
	requestDigest string,
	sourceProtocol string,
) (bool, error) {
	if actor.Type != models.ActorTypeServicePrincipal {
		return true, nil
	}
	_, err := s.CheckAction(
		ctx,
		externalNotificationPolicyCheck(
			actor,
			credentialID,
			resourceID,
			requestDigest,
			sourceProtocol,
		),
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrPolicyDenied) || errors.Is(err, ErrAutomationLoop) {
		return false, nil
	}
	return false, err
}

func (s *AgentNativeService) validatePolicyDecision(
	ctx context.Context,
	decisionID string,
	actor models.ActorRef,
	input PolicyCheckInput,
) error {
	decision, err := s.loadMatchingPolicyDecision(
		ctx,
		decisionID,
		actor,
		input,
		false,
	)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf(
			"%w: policy decision does not authorize this action",
			ErrPolicyDenied,
		)
	}
	return nil
}

func (s *AgentNativeService) loadMatchingPolicyDecision(
	ctx context.Context,
	decisionID string,
	actor models.ActorRef,
	input PolicyCheckInput,
	exact bool,
) (*models.PolicyDecision, error) {
	if s.globalEmergencyStop.Load() {
		return nil, ErrGlobalEmergencyStop
	}
	principal, err := s.getUsablePrincipal(ctx, input.ServicePrincipalID)
	if err != nil {
		return nil, err
	}
	if input.CredentialID != "" {
		if err := s.ValidateCredentialReference(ctx, input.ServicePrincipalID, input.CredentialID); err != nil {
			return nil, err
		}
	}
	if input.IsWrite && (s.globalReadOnly.Load() || principal.ReadOnly) {
		return nil, ErrReadOnlyMode
	}
	var decision models.PolicyDecision
	if err := s.dbForContext(ctx).
		First(&decision, "id = ?", decisionID).Error; err != nil {
		return nil, fmt.Errorf(
			"%w: policy decision not found",
			ErrPolicyDenied,
		)
	}
	if actor.Type != models.ActorTypeServicePrincipal ||
		decision.ActorType != actor.Type ||
		decision.ActorID != actor.ID ||
		decision.ServicePrincipalID != input.ServicePrincipalID ||
		decision.Scope != input.Scope ||
		decision.Action != input.Action ||
		decision.ResourceType != input.ResourceType ||
		decision.IsWrite != input.IsWrite ||
		(exact && (decision.ResourceID != input.ResourceID ||
			decision.IsRisky != input.IsRisky ||
			decision.CredentialID != input.CredentialID ||
			decision.RequestDigest != input.RequestDigest ||
			decision.SourceProtocol != input.SourceProtocol ||
			!policyDecisionContextEqual(decision.Context, input.Context))) ||
		(!exact && ((input.IsRisky && !decision.IsRisky) ||
			(input.ResourceID != "" && decision.ResourceID != input.ResourceID) ||
			(input.CredentialID != "" && decision.CredentialID != input.CredentialID) ||
			(input.RequestDigest != "" && decision.RequestDigest != input.RequestDigest) ||
			(input.SourceProtocol != "" && decision.SourceProtocol != input.SourceProtocol))) {
		return nil, fmt.Errorf(
			"%w: policy decision does not authorize this action",
			ErrPolicyDenied,
		)
	}
	if operation, operationErr := OperationContextFromContext(ctx); operationErr == nil &&
		(decision.OrganizationID != operation.Scope.OrganizationID ||
			decision.ProjectID != operation.Scope.ProjectID) {
		return nil, fmt.Errorf(
			"%w: policy decision project scope does not match",
			ErrPolicyDenied,
		)
	}
	if s.now().Sub(decision.CreatedAt) > 5*time.Minute {
		return nil, fmt.Errorf(
			"%w: policy decision expired",
			ErrPolicyDenied,
		)
	}
	if decision.PolicyEpoch == 0 ||
		principal.PolicyEpoch != decision.PolicyEpoch {
		return nil, fmt.Errorf(
			"%w: policy set changed after authorization",
			ErrPolicyDenied,
		)
	}
	if decision.Allowed &&
		decision.IsWrite &&
		!scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: allowed write decision requires a live project transaction",
			ErrPolicyDenied,
		)
	}
	// A denied decision is a conservative outcome, not authority. In
	// particular, optional external notifications deliberately persist a
	// scope_not_granted denial when the principal lacks events:subscribe.
	// Requiring that absent scope before loading the denial would incorrectly
	// fail the primary business command. Allowed decisions still fail closed
	// if the principal has since lost the scope.
	if decision.Allowed && !principal.HasScope(input.Scope) {
		return nil, fmt.Errorf(
			"%w: scope is no longer granted",
			ErrPolicyDenied,
		)
	}
	return &decision, nil
}

func policyDecisionContextEqual(
	persisted datatypes.JSON,
	input map[string]any,
) bool {
	expected, err := json.Marshal(input)
	if err != nil {
		return false
	}
	var persistedValue any
	if err := json.Unmarshal(persisted, &persistedValue); err != nil {
		return false
	}
	var expectedValue any
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		return false
	}
	return reflect.DeepEqual(persistedValue, expectedValue)
}

func policyMatches(policy *models.AgentPolicy, input PolicyCheckInput) bool {
	if policy.Scope != "*" && policy.Scope != input.Scope {
		return false
	}
	if policy.Action != "" && policy.Action != "*" && policy.Action != input.Action {
		return false
	}
	if policy.ResourceType != "" && policy.ResourceType != "*" && policy.ResourceType != input.ResourceType {
		return false
	}
	if policy.ResourceID != "" && policy.ResourceID != "*" && policy.ResourceID != input.ResourceID {
		return false
	}
	if len(policy.Conditions) == 0 || string(policy.Conditions) == "null" || string(policy.Conditions) == "{}" {
		return true
	}
	var conditions map[string]any
	if err := json.Unmarshal(policy.Conditions, &conditions); err != nil {
		return false
	}
	for key, expected := range conditions {
		actual, ok := input.Context[key]
		if !ok || fmt.Sprint(actual) != fmt.Sprint(expected) {
			return false
		}
	}
	return true
}

// AcquireAgentExecution enforces the principal's shared one-minute rate limit
// and expiring in-flight concurrency lease. Production sets
// RequireDistributedExecutionGuard and injects RedisAgentExecutionGuard, so a
// Redis outage fails closed instead of silently creating one bucket per
// process. The returned release function is safe to call more than once.
func (s *AgentNativeService) AcquireAgentExecution(ctx context.Context, principalID string) (func(), error) {
	_, release, err := s.AcquireAgentExecutionContext(
		ctx,
		principalID,
	)
	return release, err
}

// AcquireAgentExecutionContext returns the context that every admitted
// command must use. Distributed permits are renewed while the command is
// active; a lost renewal cancels this context so external I/O and database
// work fail closed instead of continuing after the Redis concurrency lease
// expires.
func (s *AgentNativeService) AcquireAgentExecutionContext(
	ctx context.Context,
	principalID string,
) (context.Context, func(), error) {
	principal, err := s.getUsablePrincipal(ctx, principalID)
	if err != nil {
		return nil, nil, err
	}
	if s.globalEmergencyStop.Load() {
		return nil, nil, ErrGlobalEmergencyStop
	}
	if !s.executionGuardReady() {
		return nil, nil, ErrExecutionGuardUnavailable
	}
	return s.acquireAgentExecutionSubjectContext(
		ctx,
		principalID,
		principal.RateLimitPerMinute,
		principal.ConcurrentLimit,
		s.executionLeaseTTL,
	)
}

// AcquireAgentStream reserves distributed, concurrency-only capacity for a
// long-lived machine-protocol stream. Global, service-principal and credential
// limits share the same Redis execution guard as Agent commands, but use
// isolated subject keys so an open SSE connection does not consume the
// command execution slot used by its asynchronous backend work.
func (s *AgentNativeService) AcquireAgentStream(
	ctx context.Context,
	streamType string,
	principalID string,
	credentialID string,
	globalLimit int,
) (func(), error) {
	streamType = strings.TrimSpace(streamType)
	principalID = strings.TrimSpace(principalID)
	credentialID = strings.TrimSpace(credentialID)
	if streamType == "" || principalID == "" || credentialID == "" || globalLimit <= 0 {
		return nil, ErrInvalidCredential
	}
	if err := s.ValidateCredentialReference(ctx, principalID, credentialID); err != nil {
		return nil, err
	}
	principal, err := s.getUsablePrincipal(ctx, principalID)
	if err != nil {
		return nil, err
	}
	if s.globalEmergencyStop.Load() {
		return nil, ErrGlobalEmergencyStop
	}
	if !s.executionGuardReady() {
		return nil, ErrExecutionGuardUnavailable
	}

	ttl := s.executionLeaseTTL
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < time.Second {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, context.DeadlineExceeded
		}
		// OAuth middleware cancels the stream at token expiry. Keep the Redis
		// lease marginally longer so it never expires while the socket remains
		// admitted.
		ttl = remaining + time.Second
	}

	type streamDimension struct {
		subject string
		limit   int
	}
	dimensions := []streamDimension{
		{subject: "stream:" + streamType + ":global", limit: globalLimit},
		{subject: "stream:" + streamType + ":principal:" + principalID, limit: principal.ConcurrentLimit},
		{subject: "stream:" + streamType + ":credential:" + principalID + ":" + credentialID, limit: principal.ConcurrentLimit},
	}
	releases := make([]func(), 0, len(dimensions))
	releaseAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	for _, dimension := range dimensions {
		release, acquireErr := s.acquireAgentExecutionSubject(
			ctx,
			dimension.subject,
			0,
			dimension.limit,
			ttl,
		)
		if acquireErr != nil {
			releaseAll()
			return nil, acquireErr
		}
		releases = append(releases, release)
	}

	var once sync.Once
	return func() {
		once.Do(releaseAll)
	}, nil
}

func (s *AgentNativeService) acquireAgentExecutionSubject(
	ctx context.Context,
	subjectID string,
	rateLimit int,
	concurrencyLimit int,
	concurrencyTTL time.Duration,
) (func(), error) {
	permit, err := s.executionGuard.Acquire(ctx, AgentExecutionGuardRequest{
		SubjectID:         subjectID,
		RateLimit:         rateLimit,
		ConcurrencyLimit:  concurrencyLimit,
		ConcurrencyTTL:    concurrencyTTL,
		ObservedAtForTest: s.now(),
	})
	if err != nil {
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			// Request contexts are commonly cancelled before deferred cleanup
			// runs. Give Redis a short independent cleanup window; a failed
			// release remains bounded by the concurrency lease TTL.
			releaseCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				3*time.Second,
			)
			defer cancel()
			_ = s.executionGuard.Release(releaseCtx, permit)
		})
	}, nil
}

func (s *AgentNativeService) acquireAgentExecutionSubjectContext(
	ctx context.Context,
	subjectID string,
	rateLimit int,
	concurrencyLimit int,
	concurrencyTTL time.Duration,
) (context.Context, func(), error) {
	permit, err := s.executionGuard.Acquire(
		ctx,
		AgentExecutionGuardRequest{
			SubjectID:         subjectID,
			RateLimit:         rateLimit,
			ConcurrencyLimit:  concurrencyLimit,
			ConcurrencyTTL:    concurrencyTTL,
			ObservedAtForTest: s.now(),
		},
	)
	if err != nil {
		return nil, nil, err
	}
	leaseContext, cancelLease := context.WithCancelCause(ctx)
	stopRenewal := make(chan struct{})
	renewer, canRenew := s.executionGuard.(AgentExecutionPermitRenewer)
	if s.requireDistributedGuard && !canRenew {
		_ = s.executionGuard.Release(ctx, permit)
		cancelLease(ErrExecutionGuardUnavailable)
		return nil, nil, ErrExecutionGuardUnavailable
	}
	if canRenew {
		interval := concurrencyTTL / 3
		if interval < 250*time.Millisecond {
			interval = 250 * time.Millisecond
		}
		if interval > 30*time.Second {
			interval = 30 * time.Second
		}
		go func() {
			timer := time.NewTicker(interval)
			defer timer.Stop()
			for {
				select {
				case <-stopRenewal:
					return
				case <-leaseContext.Done():
					return
				case <-timer.C:
					timeout := concurrencyTTL / 4
					if timeout < 100*time.Millisecond {
						timeout = 100 * time.Millisecond
					}
					if timeout > 3*time.Second {
						timeout = 3 * time.Second
					}
					renewContext, cancelRenew :=
						context.WithTimeout(
							context.WithoutCancel(ctx),
							timeout,
						)
					renewErr := renewer.Renew(
						renewContext,
						AgentExecutionRenewRequest{
							Permit:            permit,
							ConcurrencyTTL:    concurrencyTTL,
							ObservedAtForTest: s.now(),
						},
					)
					cancelRenew()
					if renewErr != nil {
						cancelLease(fmt.Errorf(
							"%w: %v",
							ErrExecutionGuardUnavailable,
							renewErr,
						))
						return
					}
				}
			}
		}()
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			close(stopRenewal)
			cancelLease(context.Canceled)
			releaseCtx, cancelRelease := context.WithTimeout(
				context.WithoutCancel(ctx),
				3*time.Second,
			)
			defer cancelRelease()
			_ = s.executionGuard.Release(
				releaseCtx,
				permit,
			)
		})
	}
	return leaseContext, release, nil
}

// ValidateExecutionGuardConfiguration is a startup gate for production
// wiring. It intentionally does not probe Redis; database.New already performs
// that connectivity check before constructing the guard.
func (s *AgentNativeService) ValidateExecutionGuardConfiguration() error {
	if !s.executionGuardReady() {
		return ErrExecutionGuardUnavailable
	}
	return nil
}

func (s *AgentNativeService) executionGuardReady() bool {
	return s.executionGuard != nil &&
		(!s.requireDistributedGuard || s.executionGuard.IsDistributed())
}

type IdempotencyReservation struct {
	Record   *models.IdempotencyRecord
	Replayed bool
}

func (s *AgentNativeService) ReserveIdempotency(
	ctx context.Context,
	actor models.ActorRef,
	operation string,
	key string,
	requestBody []byte,
	ttl time.Duration,
) (*IdempotencyReservation, error) {
	boundTransaction, hasAgentNativeTransaction :=
		ctx.Value(agentNativeTransactionContextKey{}).(*gorm.DB)
	hasAgentNativeTransaction =
		hasAgentNativeTransaction && boundTransaction != nil
	if !scopeddb.HasTransaction(ctx) &&
		!hasAgentNativeTransaction {
		if operationContext, contextErr :=
			OperationContextFromContext(ctx); contextErr == nil &&
			operationContext.Actor == actor &&
			!operationContext.Scope.IsZero() {
			var reservation *IdempotencyReservation
			err := runProjectOperation(
				ctx,
				s.db,
				func(scopedContext context.Context) error {
					var reserveErr error
					reservation, reserveErr =
						s.reserveIdempotencyScoped(
							scopedContext,
							actor,
							operation,
							key,
							requestBody,
							ttl,
						)
					return reserveErr
				},
			)
			return reservation, err
		}
	}
	return s.reserveIdempotencyScoped(
		ctx,
		actor,
		operation,
		key,
		requestBody,
		ttl,
	)
}

func (s *AgentNativeService) reserveIdempotencyScoped(
	ctx context.Context,
	actor models.ActorRef,
	operation string,
	key string,
	requestBody []byte,
	ttl time.Duration,
) (*IdempotencyReservation, error) {
	db := s.dbForContext(ctx)
	if err := actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	operation = strings.TrimSpace(operation)
	key = strings.TrimSpace(key)
	if operation == "" || key == "" || len(key) > 255 {
		return nil, fmt.Errorf("operation and idempotency key are required")
	}
	if ttl <= 0 {
		ttl = defaultIdempotencyRetentionTTL
	}
	requestHash := sha256.Sum256(requestBody)
	hashText := hex.EncodeToString(requestHash[:])
	now := s.now()
	processingLease := s.idempotencyProcessingLease
	if ttl < processingLease {
		processingLease = ttl
	}
	record := &models.IdempotencyRecord{
		ID:                       newNativeID(),
		ActorType:                actor.Type,
		ActorID:                  actor.ID,
		Operation:                operation,
		Key:                      key,
		RequestHash:              hashText,
		State:                    models.IdempotencyStateProcessing,
		ExpiresAt:                now.Add(processingLease),
		CompletionTTLNanoseconds: ttl.Nanoseconds(),
	}
	if operationContext, contextErr := OperationContextFromContext(ctx); contextErr == nil && operationContext.Actor == actor {
		record.OrganizationID = operationContext.Scope.OrganizationID
		record.ProjectID = operationContext.Scope.ProjectID
	}
	createResult := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "organization_id"},
			{Name: "project_id"},
			{Name: "actor_type"},
			{Name: "actor_id"},
			{Name: "operation"},
			{Name: "key"},
		},
		DoNothing: true,
	}).Create(record)
	if createResult.Error != nil {
		return nil, fmt.Errorf("reserve idempotency key: %w", createResult.Error)
	}
	if createResult.RowsAffected == 1 {
		return &IdempotencyReservation{Record: record}, nil
	}
	if createResult.RowsAffected != 0 {
		return nil, fmt.Errorf(
			"reserve idempotency key: unexpected rows affected: %d",
			createResult.RowsAffected,
		)
	}

	var existing models.IdempotencyRecord
	if err := db.
		Where(
			"organization_id = ? AND project_id = ? AND actor_type = ? AND actor_id = ? AND operation = ? AND key = ?",
			record.OrganizationID,
			record.ProjectID,
			actor.Type,
			actor.ID,
			operation,
			key,
		).
		First(&existing).Error; err != nil {
		return nil, fmt.Errorf("load idempotency key: %w", err)
	}
	processingLeaseExpired := existing.State == models.IdempotencyStateProcessing &&
		(!existing.ExpiresAt.After(now) ||
			!existing.UpdatedAt.Add(s.idempotencyProcessingLease).After(now))
	recordActive := (existing.State == models.IdempotencyStateCompleted && existing.ExpiresAt.After(now)) ||
		(existing.State == models.IdempotencyStateProcessing && !processingLeaseExpired)
	if recordActive {
		if existing.RequestHash != hashText {
			return nil, ErrIdempotencyConflict
		}
		switch existing.State {
		case models.IdempotencyStateCompleted:
			return &IdempotencyReservation{Record: &existing, Replayed: true}, nil
		case models.IdempotencyStateProcessing:
			return nil, ErrIdempotencyInProgress
		}
	}

	replacementID := newNativeID()
	processingLeaseCutoff := now.Add(-s.idempotencyProcessingLease)
	result := db.Model(&models.IdempotencyRecord{}).
		Where(
			`id = ? AND (
				state = ?
				OR (state = ? AND (expires_at <= ? OR updated_at <= ?))
				OR (state = ? AND expires_at <= ?)
			)`,
			existing.ID,
			models.IdempotencyStateFailed,
			models.IdempotencyStateProcessing,
			now,
			processingLeaseCutoff,
			models.IdempotencyStateCompleted,
			now,
		).
		Updates(map[string]any{
			"id":                         replacementID,
			"request_hash":               hashText,
			"state":                      models.IdempotencyStateProcessing,
			"response_code":              0,
			"response_body":              nil,
			"resource_snapshot":          nil,
			"resource_id":                "",
			"event_id":                   "",
			"last_error_code":            "",
			"expires_at":                 now.Add(processingLease),
			"completion_ttl_nanoseconds": ttl.Nanoseconds(),
			"completed_at":               nil,
			"updated_at":                 now,
		})
	if result.Error != nil {
		return nil, fmt.Errorf("refresh idempotency reservation: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, ErrIdempotencyInProgress
	}
	var refreshed models.IdempotencyRecord
	if err := db.First(&refreshed, "id = ?", replacementID).Error; err != nil {
		return nil, fmt.Errorf("reload idempotency reservation: %w", err)
	}
	return &IdempotencyReservation{Record: &refreshed}, nil
}

func (s *AgentNativeService) CompleteIdempotencyTx(
	ctx context.Context,
	tx *gorm.DB,
	recordID string,
	responseCode int,
	response any,
	resourceID string,
	eventID string,
) error {
	return s.completeIdempotencyTx(
		ctx,
		tx,
		recordID,
		responseCode,
		response,
		resourceID,
		eventID,
		0,
	)
}

func (s *AgentNativeService) CompleteIdempotencyTxWithTTL(
	ctx context.Context,
	tx *gorm.DB,
	recordID string,
	responseCode int,
	response any,
	resourceID string,
	eventID string,
	completionTTL time.Duration,
) error {
	return s.completeIdempotencyTx(
		ctx,
		tx,
		recordID,
		responseCode,
		response,
		resourceID,
		eventID,
		completionTTL,
	)
}

func (s *AgentNativeService) completeIdempotencyTx(
	ctx context.Context,
	tx *gorm.DB,
	recordID string,
	responseCode int,
	response any,
	resourceID string,
	eventID string,
	completionTTL time.Duration,
) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	body, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	now := s.now()
	if completionTTL <= 0 {
		var record models.IdempotencyRecord
		if err := tx.WithContext(ctx).
			Select("completion_ttl_nanoseconds").
			Where("id = ? AND state = ?", recordID, models.IdempotencyStateProcessing).
			First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrIdempotencyConflict
			}
			return fmt.Errorf("load idempotency retention: %w", err)
		}
		completionTTL = time.Duration(record.CompletionTTLNanoseconds)
		if completionTTL <= 0 {
			completionTTL = defaultIdempotencyRetentionTTL
		}
	}
	updates := map[string]any{
		"state":         models.IdempotencyStateCompleted,
		"response_code": responseCode,
		"response_body": datatypes.JSON(body),
		"resource_id":   resourceID,
		"event_id":      eventID,
		"completed_at":  now,
		"updated_at":    now,
		"expires_at":    now.Add(completionTTL),
	}
	result := tx.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where("id = ? AND state = ?", recordID, models.IdempotencyStateProcessing).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("complete idempotency key: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrIdempotencyConflict
	}
	return nil
}

func (s *AgentNativeService) storeIdempotencySnapshotTx(
	ctx context.Context,
	tx *gorm.DB,
	recordID string,
	snapshot any,
) error {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode idempotent resource snapshot: %w", err)
	}
	result := tx.WithContext(ctx).Model(&models.IdempotencyRecord{}).
		Where("id = ? AND state = ?", recordID, models.IdempotencyStateCompleted).
		Update("resource_snapshot", datatypes.JSON(body))
	if result.Error != nil {
		return fmt.Errorf("store idempotent resource snapshot: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrIdempotencyConflict
	}
	return nil
}

func (s *AgentNativeService) FailIdempotency(ctx context.Context, recordID string, code string) error {
	if !scopeddb.HasTransaction(ctx) {
		if operation, contextErr :=
			OperationContextFromContext(ctx); contextErr == nil &&
			!operation.Scope.IsZero() {
			return runProjectOperation(
				ctx,
				s.db,
				func(scopedContext context.Context) error {
					return s.failIdempotencyScoped(
						scopedContext,
						recordID,
						code,
					)
				},
			)
		}
	}
	return s.failIdempotencyScoped(ctx, recordID, code)
}

func (s *AgentNativeService) failIdempotencyScoped(
	ctx context.Context,
	recordID string,
	code string,
) error {
	now := s.now()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), idempotencyFailureCleanupTimeout)
	defer cancel()
	result := s.dbForContext(cleanupCtx).Model(&models.IdempotencyRecord{}).
		Where("id = ? AND state = ?", recordID, models.IdempotencyStateProcessing).
		Updates(map[string]any{
			"state":           models.IdempotencyStateFailed,
			"last_error_code": code,
			"completed_at":    now,
			"updated_at":      now,
		})
	if result.Error != nil {
		return fmt.Errorf("fail idempotency key: %w", result.Error)
	}
	return nil
}

// FinalizeRevokedActorIdempotency closes a processing reservation after the
// caller's live project Grant has been revoked. It runs as a narrowly scoped
// system finalizer and can update only the exact project, actor and record that
// created the reservation; it never restores business authorization.
func (s *AgentNativeService) FinalizeRevokedActorIdempotency(
	ctx context.Context,
	scope models.ProjectScope,
	actor models.ActorRef,
	recordID string,
	code string,
) error {
	if s == nil || s.db == nil {
		return errors.New("idempotency finalizer is unavailable")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := actor.Validate(); err != nil ||
		actor.Type != models.ActorTypeServicePrincipal {
		return ErrInvalidActor
	}
	recordID = strings.TrimSpace(recordID)
	code = strings.TrimSpace(code)
	if recordID == "" || code == "" {
		return errors.New("idempotency finalizer record and code are required")
	}
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		idempotencyFailureCleanupTimeout,
	)
	defer cancel()
	systemContext, err := WithOperationContext(
		cleanupCtx,
		OperationContext{
			Scope:  scope,
			Actor:  models.SystemActor("agent-idempotency-finalizer"),
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		return err
	}
	now := s.now()
	return runProjectOperation(
		systemContext,
		s.db,
		func(scopedContext context.Context) error {
			result := s.dbForContext(scopedContext).
				Model(&models.IdempotencyRecord{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND actor_type = ? AND actor_id = ? AND state = ?",
					recordID,
					scope.OrganizationID,
					scope.ProjectID,
					actor.Type,
					actor.ID,
					models.IdempotencyStateProcessing,
				).
				Updates(map[string]any{
					"state":           models.IdempotencyStateFailed,
					"last_error_code": code,
					"completed_at":    now,
					"updated_at":      now,
				})
			if result.Error != nil {
				return fmt.Errorf(
					"finalize revoked actor idempotency: %w",
					result.Error,
				)
			}
			return nil
		},
	)
}

type DomainEventInput struct {
	ID                   string
	Source               string
	Type                 string
	Subject              string
	Time                 time.Time
	DataSchema           string
	Data                 any
	TraceID              string
	CorrelationID        string
	CausationID          string
	Actor                models.ActorRef
	ResourceVersion      uint64
	Scope                models.ProjectScope
	ConfigurationVersion string
	PolicyDecisionID     string
	// Service-principal events may fan out to external webhooks only after a
	// separate risky policy check explicitly allows that side effect.
	AllowExternalNotifications bool
}

type CloudEventEnvelope struct {
	SpecVersion          string           `json:"specversion"`
	ID                   string           `json:"id"`
	Source               string           `json:"source"`
	Type                 string           `json:"type"`
	Subject              string           `json:"subject,omitempty"`
	Time                 time.Time        `json:"time"`
	DataContentType      string           `json:"datacontenttype"`
	DataSchema           string           `json:"dataschema,omitempty"`
	TraceID              string           `json:"traceid,omitempty"`
	CorrelationID        string           `json:"correlationid,omitempty"`
	CausationID          string           `json:"causationid,omitempty"`
	ActorType            models.ActorType `json:"actortype"`
	ActorID              string           `json:"actorid"`
	ResourceVersion      uint64           `json:"resourceversion"`
	OrganizationID       uint             `json:"organizationid,omitempty"`
	ProjectID            uint             `json:"projectid,omitempty"`
	ConfigurationVersion string           `json:"configurationversion,omitempty"`
	PolicyDecisionID     string           `json:"policydecisionid,omitempty"`
	Data                 json.RawMessage  `json:"data"`
	// InternalData contains the persisted event payload before private Outbox
	// fields are removed. It is never serialized to REST, webhook, MCP or A2A.
	InternalData json.RawMessage `json:"-"`
}

func CloudEventFromModel(event *models.DomainEvent) CloudEventEnvelope {
	internalData := append(json.RawMessage(nil), event.Data...)
	return CloudEventEnvelope{
		SpecVersion:          event.SpecVersion,
		ID:                   event.ID,
		Source:               event.Source,
		Type:                 event.Type,
		Subject:              event.Subject,
		Time:                 event.Time,
		DataContentType:      event.DataContentType,
		DataSchema:           event.DataSchema,
		TraceID:              event.TraceID,
		CorrelationID:        event.CorrelationID,
		CausationID:          event.CausationID,
		ActorType:            event.ActorType,
		ActorID:              event.ActorID,
		ResourceVersion:      event.ResourceVersion,
		OrganizationID:       event.OrganizationID,
		ProjectID:            event.ProjectID,
		ConfigurationVersion: event.ConfigurationVersion,
		PolicyDecisionID:     event.PolicyDecisionID,
		Data: publicCloudEventData(
			internalData,
			models.ActorRef{Type: event.ActorType, ID: event.ActorID},
		),
		InternalData: internalData,
	}
}

func publicCloudEventData(raw json.RawMessage, actor models.ActorRef) json.RawMessage {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil {
		return append(json.RawMessage(nil), raw...)
	}
	delete(object, AttachmentCleanupObjectsDataField)
	delete(object, AttachmentUploadMigrationDataField)
	actorData, err := json.Marshal(actor)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	// Actor is authoritative event provenance. Keeping the rich representation
	// in data avoids the object-valued context attribute forbidden by the
	// CloudEvents type system.
	object["actor"] = actorData
	sanitized, err := json.Marshal(object)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return sanitized
}

// AttachmentCleanupStoragePath resolves one cleanup delivery solely from the
// durable internal event manifest. It never reads or returns provider URLs.
func AttachmentCleanupStoragePath(
	event CloudEventEnvelope,
	destinationID string,
) (string, error) {
	reference, err := AttachmentCleanupStorageReference(
		event,
		destinationID,
	)
	if err != nil {
		return "", err
	}
	return reference.StoragePath, nil
}

type AttachmentCleanupReference struct {
	StorageType string
	StoreID     string
	StoragePath string
	VersionID   string
}

// AttachmentCleanupStorageReference resolves the private storage backend and
// logical key captured by the deletion transaction. Older manifests omit the
// backend; a TypedAttachmentStorage may then delete that immutable key from
// every configured migration backend.
func AttachmentCleanupStorageReference(
	event CloudEventEnvelope,
	destinationID string,
) (AttachmentCleanupReference, error) {
	attachmentID, _, err := parseAttachmentCleanupDestination(destinationID)
	if err != nil {
		return AttachmentCleanupReference{}, err
	}
	raw := event.InternalData
	if len(raw) == 0 {
		raw = event.Data
	}
	var publicData struct {
		TicketID uint `json:"ticket_id"`
	}
	if err := json.Unmarshal(event.Data, &publicData); err != nil ||
		publicData.TicketID == 0 {
		return AttachmentCleanupReference{}, ErrInvalidAttachmentCleanup
	}
	var data struct {
		TicketID uint                      `json:"ticket_id"`
		Objects  []AttachmentCleanupObject `json:"_attachment_cleanup_objects"`
	}
	if err := json.Unmarshal(raw, &data); err != nil ||
		data.TicketID == 0 ||
		data.TicketID != publicData.TicketID {
		return AttachmentCleanupReference{}, ErrInvalidAttachmentCleanup
	}
	var reference AttachmentCleanupReference
	matches := 0
	for _, object := range data.Objects {
		if object.AttachmentID != attachmentID {
			continue
		}
		matches++
		if matches > 1 || object.TicketID != data.TicketID {
			return AttachmentCleanupReference{}, ErrInvalidAttachmentCleanup
		}
		reference = AttachmentCleanupReference{
			StorageType: object.StorageType,
			StoreID:     object.StoreID,
			StoragePath: object.StoragePath,
			VersionID:   object.VersionID,
		}
	}
	if matches != 1 || reference.StoragePath == "" {
		return AttachmentCleanupReference{}, ErrInvalidAttachmentCleanup
	}
	if _, err := validateAttachmentCleanupObjectDestination(
		destinationID,
		AttachmentCleanupObject{
			AttachmentID: attachmentID,
			TicketID:     data.TicketID,
			StorageType:  reference.StorageType,
			StoreID:      reference.StoreID,
			StoragePath:  reference.StoragePath,
			VersionID:    reference.VersionID,
		},
	); err != nil {
		return AttachmentCleanupReference{}, err
	}
	return reference, nil
}

func (s *AgentNativeService) AppendDomainEventTx(
	ctx context.Context,
	tx *gorm.DB,
	input DomainEventInput,
	targets []OutboxTarget,
) (*models.DomainEvent, error) {
	if tx == nil {
		return nil, fmt.Errorf("transaction is required")
	}
	if strings.TrimSpace(input.Type) == "" {
		return nil, fmt.Errorf("domain event type is required")
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if input.Scope.IsZero() {
		if operationContext, contextErr := OperationContextFromContext(ctx); contextErr == nil && operationContext.Actor == input.Actor {
			input.Scope = operationContext.Scope
			if input.TraceID == "" {
				input.TraceID = operationContext.TraceID
			}
			if input.CorrelationID == "" {
				input.CorrelationID = operationContext.CorrelationID
			}
		}
	} else if err := input.Scope.Validate(); err != nil {
		return nil, fmt.Errorf("invalid domain event project scope: %w", err)
	}
	if input.Scope.IsZero() {
		return nil, fmt.Errorf(
			"%w: domain events require an explicit project scope",
			ErrInvalidScope,
		)
	}
	payload, err := json.Marshal(input.Data)
	if err != nil {
		return nil, fmt.Errorf("encode domain event data: %w", err)
	}
	if len(payload) == 0 || string(payload) == "null" {
		payload = []byte("{}")
	}
	if input.ID == "" {
		input.ID = newNativeID()
	}
	if input.Source == "" {
		input.Source = s.eventSource
	}
	if input.DataSchema == "" {
		input.DataSchema = "urn:chronodesk:schema:domain-event-data:v1"
	}
	if input.Time.IsZero() {
		input.Time = s.now().UTC()
	} else {
		input.Time = input.Time.UTC()
	}
	event := &models.DomainEvent{
		ID:                   input.ID,
		OrganizationID:       input.Scope.OrganizationID,
		ProjectID:            input.Scope.ProjectID,
		ConfigurationVersion: strings.TrimSpace(input.ConfigurationVersion),
		PolicyDecisionID:     strings.TrimSpace(input.PolicyDecisionID),
		SpecVersion:          "1.0",
		Source:               input.Source,
		Type:                 input.Type,
		Subject:              input.Subject,
		Time:                 input.Time,
		DataContentType:      "application/json",
		DataSchema:           input.DataSchema,
		Data:                 datatypes.JSON(payload),
		TraceID:              input.TraceID,
		CorrelationID:        input.CorrelationID,
		CausationID:          input.CausationID,
		ActorType:            input.Actor.Type,
		ActorID:              input.Actor.ID,
		ResourceVersion:      input.ResourceVersion,
	}
	if event.ResourceVersion == 0 {
		event.ResourceVersion = 1
	}
	if err := tx.WithContext(ctx).Create(event).Error; err != nil {
		return nil, fmt.Errorf("create domain event: %w", err)
	}
	if s.auditLedger != nil && !input.Scope.IsZero() {
		operation, err := OperationContextFromContext(ctx)
		if err != nil ||
			operation.Scope != input.Scope ||
			operation.Actor != input.Actor {
			return nil, fmt.Errorf(
				"append domain event audit ledger entry: operation context does not match event scope and actor",
			)
		}
		payloadDigest := sha256.Sum256(payload)
		if _, err := s.auditLedger.AppendTx(
			ctx,
			tx,
			AuditLedgerAppendInput{
				PayloadDigest:        hex.EncodeToString(payloadDigest[:]),
				EventType:            event.Type,
				ResourceType:         "domain_event",
				ResourceID:           event.ID,
				ResourceVersion:      event.ResourceVersion,
				Outcome:              models.AuditLedgerOutcomeSucceeded,
				DomainEventID:        event.ID,
				ConfigurationVersion: event.ConfigurationVersion,
				PolicyVersion:        event.PolicyDecisionID,
				OccurredAt:           event.Time,
			},
		); err != nil {
			return nil, fmt.Errorf(
				"append domain event audit ledger entry: %w",
				err,
			)
		}
	}
	if len(targets) == 0 {
		targets = s.defaultOutboxTargets
	}
	normalizedTargets := make([]OutboxTarget, len(targets))
	for index, target := range targets {
		if strings.TrimSpace(target.Type) == "" || strings.TrimSpace(target.ID) == "" {
			return nil, fmt.Errorf("outbox target type and id are required")
		}
		if target.MaxAttempts <= 0 {
			target.MaxAttempts = 8
		}
		normalizedTargets[index] = target
	}
	targets = normalizedTargets
	if input.Actor.Type == models.ActorTypeServicePrincipal && !input.AllowExternalNotifications {
		filtered := make([]OutboxTarget, 0, len(targets))
		for _, target := range targets {
			if target.Type != "webhook" {
				filtered = append(filtered, target)
			}
		}
		targets = filtered
	}
	targets, err = s.freezeWebhookOutboxTargetsTx(
		ctx,
		tx,
		event,
		targets,
	)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for _, target := range targets {
		delivery := &models.OutboxDelivery{
			ID:              newNativeID(),
			OrganizationID:  event.OrganizationID,
			ProjectID:       event.ProjectID,
			EventID:         event.ID,
			DestinationType: target.Type,
			DestinationID:   target.ID,
			Status:          models.OutboxDeliveryPending,
			MaxAttempts:     target.MaxAttempts,
			NextAttemptAt:   now,
		}
		if err := tx.WithContext(ctx).Create(delivery).Error; err != nil {
			return nil, fmt.Errorf("create outbox delivery: %w", err)
		}
		event.Deliveries = append(event.Deliveries, *delivery)
	}
	return event, nil
}

// freezeWebhookOutboxTargetsTx resolves active matching subscriptions and
// copies every delivery-affecting field into immutable snapshots before the
// business transaction commits. Outbox workers never fan out against mutable
// WebhookConfig rows.
func (s *AgentNativeService) freezeWebhookOutboxTargetsTx(
	ctx context.Context,
	tx *gorm.DB,
	event *models.DomainEvent,
	targets []OutboxTarget,
) ([]OutboxTarget, error) {
	if tx == nil || event == nil {
		return nil, errors.New("webhook snapshot transaction and event are required")
	}
	requiresSnapshot := false
	for _, target := range targets {
		if target.Type == "webhook" &&
			target.ID == webhookConfiguredDestinationID {
			requiresSnapshot = true
			break
		}
	}
	if !requiresSnapshot {
		return targets, nil
	}

	eventType := models.WebhookEventType(strings.TrimSpace(event.Type))
	var transitionStatus models.TicketStatus
	if eventType == models.WebhookEventTicketTransitioned {
		var data struct {
			Status    models.TicketStatus `json:"status"`
			NewStatus models.TicketStatus `json:"new_status"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return nil, fmt.Errorf("decode webhook transition status: %w", err)
		}
		transitionStatus = data.NewStatus
		if transitionStatus == "" {
			transitionStatus = data.Status
		}
	}

	var configs []models.WebhookConfig
	if eventcontract.IsWebhookDeliveryEventType(event.Type) {
		if err := tx.WithContext(ctx).
			Where(
				"organization_id = ? AND project_id = ? AND status = ?",
				event.OrganizationID,
				event.ProjectID,
				models.WebhookStatusActive,
			).
			Order("id ASC").
			Find(&configs).Error; err != nil {
			return nil, fmt.Errorf(
				"freeze webhook subscriptions for domain event: %w",
				err,
			)
		}
	}

	frozen := make([]OutboxTarget, 0, len(targets)+len(configs))
	for _, target := range targets {
		if target.Type != "webhook" ||
			target.ID != webhookConfiguredDestinationID {
			frozen = append(frozen, target)
			continue
		}
		for _, config := range configs {
			if !config.MatchesEvent(eventType, transitionStatus) {
				continue
			}
			snapshot, err := models.NewWebhookDeliverySnapshot(
				config,
				event.ID,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"freeze webhook configuration %d: %w",
					config.ID,
					err,
				)
			}
			if err := tx.WithContext(ctx).Create(snapshot).Error; err != nil {
				return nil, fmt.Errorf(
					"create webhook delivery snapshot: %w",
					err,
				)
			}
			maxAttempts := config.RetryCount + 1
			if maxAttempts < 1 {
				maxAttempts = 1
			}
			if maxAttempts > 11 {
				maxAttempts = 11
			}
			frozen = append(frozen, OutboxTarget{
				Type:        "webhook",
				ID:          webhookSnapshotDestinationPrefix + snapshot.ID,
				MaxAttempts: maxAttempts,
			})
		}
	}
	return frozen, nil
}

// AppendDomainEventWithAdditionalTargetsTx preserves every configured default
// destination and adds operation-specific destinations in the same business
// transaction. Callers use this for side effects such as attachment cleanup
// that must not replace event_stream, webhook, or automation delivery.
func (s *AgentNativeService) AppendDomainEventWithAdditionalTargetsTx(
	ctx context.Context,
	tx *gorm.DB,
	input DomainEventInput,
	additionalTargets []OutboxTarget,
) (*models.DomainEvent, error) {
	targets := make([]OutboxTarget, 0, len(s.defaultOutboxTargets)+len(additionalTargets))
	seen := make(map[string]struct{}, cap(targets))
	appendUnique := func(target OutboxTarget) {
		key := strings.TrimSpace(target.Type) + "\x00" + strings.TrimSpace(target.ID)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	for _, target := range s.defaultOutboxTargets {
		appendUnique(target)
	}
	for _, target := range additionalTargets {
		appendUnique(target)
	}
	return s.AppendDomainEventTx(ctx, tx, input, targets)
}

func (s *AgentNativeService) ClaimPendingOutbox(
	ctx context.Context,
	workerID string,
	limit int,
	lockTTL time.Duration,
) ([]*models.OutboxDelivery, error) {
	return s.claimPendingOutbox(
		ctx,
		workerID,
		limit,
		lockTTL,
		archivedProjectOutboxDestinations(),
		archivedProjectOutboxEventTypes(),
	)
}

func (s *AgentNativeService) claimPendingOutbox(
	ctx context.Context,
	workerID string,
	limit int,
	lockTTL time.Duration,
	archivedAllowedDestinations []string,
	archivedAllowedEventTypes []string,
) ([]*models.OutboxDelivery, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if lockTTL <= 0 {
		lockTTL = 2 * time.Minute
	}
	now := s.now()
	lockCutoff := now.Add(-lockTTL)
	var claimed []*models.OutboxDelivery
	operation, err := requireOutboxWorkerOperation(ctx)
	if err != nil {
		return nil, err
	}
	applyAllowlist := func(query *gorm.DB) *gorm.DB {
		if len(archivedAllowedDestinations) == 0 &&
			len(archivedAllowedEventTypes) == 0 {
			return query
		}
		const allowedEventExists = `
			destination_type = ?
			AND EXISTS (
				SELECT 1
				FROM domain_events AS allowed_event
				WHERE allowed_event.id = outbox_deliveries.event_id
				  AND allowed_event.organization_id =
					outbox_deliveries.organization_id
				  AND allowed_event.project_id =
					outbox_deliveries.project_id
				  AND allowed_event.type IN ?
			)
		`
		if len(archivedAllowedDestinations) == 0 {
			return query.Where(
				"("+allowedEventExists+")",
				"event_stream",
				archivedAllowedEventTypes,
			)
		}
		if len(archivedAllowedEventTypes) == 0 {
			return query.Where(
				"destination_type IN ?",
				archivedAllowedDestinations,
			)
		}
		return query.Where(
			"(destination_type IN ? OR ("+allowedEventExists+"))",
			archivedAllowedDestinations,
			"event_stream",
			archivedAllowedEventTypes,
		)
	}
	err = runSystemProjectOperation(
		ctx,
		s.db,
		operation.Scope,
		operation.Actor,
		operation.TraceID,
		operation.CorrelationID,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					var project models.Project
					if err := tx.
						Select("id", "organization_id", "status").
						Clauses(clause.Locking{Strength: "SHARE"}).
						Where(
							"id = ? AND organization_id = ?",
							operation.Scope.ProjectID,
							operation.Scope.OrganizationID,
						).
						Take(&project).Error; err != nil {
						return fmt.Errorf(
							"lock outbox project status: %w",
							err,
						)
					}
					if project.Status != models.ProjectStatusActive &&
						project.Status != models.ProjectStatusArchived {
						return ErrProjectInactive
					}
					applyCurrentProjectPolicy := func(
						query *gorm.DB,
					) *gorm.DB {
						if project.Status !=
							models.ProjectStatusArchived {
							return query
						}
						return applyAllowlist(query)
					}
					var candidates []models.OutboxDelivery
					candidateQuery := tx.Where(
						"organization_id = ? AND project_id = ? AND ((status IN ? AND next_attempt_at <= ?) OR (status = ? AND locked_at < ?))",
						operation.Scope.OrganizationID,
						operation.Scope.ProjectID,
						[]models.OutboxDeliveryStatus{
							models.OutboxDeliveryPending,
							models.OutboxDeliveryFailed,
						},
						now,
						models.OutboxDeliveryProcessing,
						lockCutoff,
					)
					candidateQuery =
						applyCurrentProjectPolicy(candidateQuery)
					if err := candidateQuery.
						Order("next_attempt_at ASC, created_at ASC").
						Limit(limit).
						Find(&candidates).Error; err != nil {
						return err
					}
					for index := range candidates {
						candidate := &candidates[index]
						updateQuery := tx.Model(
							&models.OutboxDelivery{},
						).Where(
							"id = ? AND organization_id = ? AND project_id = ? AND ((status IN ? AND next_attempt_at <= ?) OR (status = ? AND locked_at < ?))",
							candidate.ID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
							[]models.OutboxDeliveryStatus{
								models.OutboxDeliveryPending,
								models.OutboxDeliveryFailed,
							},
							now,
							models.OutboxDeliveryProcessing,
							lockCutoff,
						)
						updateQuery =
							applyCurrentProjectPolicy(updateQuery)
						result := updateQuery.
							Updates(map[string]any{
								"status":     models.OutboxDeliveryProcessing,
								"attempts":   gorm.Expr("attempts + 1"),
								"locked_at":  now,
								"locked_by":  workerID,
								"updated_at": now,
							})
						if result.Error != nil {
							return result.Error
						}
						if result.RowsAffected == 0 {
							continue
						}
						var delivery models.OutboxDelivery
						if err := tx.Preload("Event").Where(
							"id = ? AND organization_id = ? AND project_id = ?",
							candidate.ID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
						).First(&delivery).Error; err != nil {
							return err
						}
						if delivery.Event == nil ||
							delivery.Event.OrganizationID !=
								operation.Scope.OrganizationID ||
							delivery.Event.ProjectID !=
								operation.Scope.ProjectID {
							return errors.New(
								"outbox delivery event project scope mismatch",
							)
						}
						claimed = append(claimed, &delivery)
					}
					return nil
				},
			)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("claim outbox deliveries: %w", err)
	}
	return claimed, nil
}

func (s *AgentNativeService) MarkOutboxDelivered(ctx context.Context, deliveryID, workerID string) error {
	operation, err := requireOutboxWorkerOperation(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	return runSystemProjectOperation(
		ctx,
		s.db,
		operation.Scope,
		operation.Actor,
		operation.TraceID,
		operation.CorrelationID,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					result := tx.Model(&models.OutboxDelivery{}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ? AND status = ? AND locked_by = ?",
							deliveryID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
							models.OutboxDeliveryProcessing,
							workerID,
						).
						Updates(map[string]any{
							"status":       models.OutboxDeliverySucceeded,
							"delivered_at": now,
							"locked_at":    nil,
							"locked_by":    "",
							"last_error":   "",
							"updated_at":   now,
						})
					if result.Error != nil {
						return fmt.Errorf(
							"mark outbox delivered: %w",
							result.Error,
						)
					}
					if result.RowsAffected == 0 {
						return ErrOutboxLockLost
					}
					var delivery models.OutboxDelivery
					if err := tx.Where(
						"id = ? AND organization_id = ? AND project_id = ?",
						deliveryID,
						operation.Scope.OrganizationID,
						operation.Scope.ProjectID,
					).First(&delivery).Error; err != nil {
						return err
					}
					var remaining int64
					if err := tx.Model(&models.OutboxDelivery{}).
						Where(
							"event_id = ? AND organization_id = ? AND project_id = ? AND status <> ?",
							delivery.EventID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
							models.OutboxDeliverySucceeded,
						).
						Count(&remaining).Error; err != nil {
						return err
					}
					if remaining == 0 {
						if err := tx.Model(&models.DomainEvent{}).
							Where(
								"id = ? AND organization_id = ? AND project_id = ?",
								delivery.EventID,
								operation.Scope.OrganizationID,
								operation.Scope.ProjectID,
							).
							Update("published_at", now).Error; err != nil {
							return err
						}
					}
					return nil
				},
			)
		},
	)
}

func (s *AgentNativeService) MarkOutboxFailed(ctx context.Context, deliveryID, workerID string, deliveryErr error) error {
	operation, err := requireOutboxWorkerOperation(ctx)
	if err != nil {
		return err
	}
	message := ""
	if deliveryErr != nil {
		message = scrubOutboxFailure(deliveryErr)
	}
	return runSystemProjectOperation(
		ctx,
		s.db,
		operation.Scope,
		operation.Actor,
		operation.TraceID,
		operation.CorrelationID,
		func(projectCtx context.Context) error {
			return transactionForContext(
				projectCtx,
				s.db,
				func(tx *gorm.DB) error {
					var delivery models.OutboxDelivery
					load := tx.Clauses(
						clause.Locking{Strength: "UPDATE"},
					).Where(
						"id = ? AND organization_id = ? AND project_id = ? AND status = ? AND locked_by = ?",
						deliveryID,
						operation.Scope.OrganizationID,
						operation.Scope.ProjectID,
						models.OutboxDeliveryProcessing,
						workerID,
					).First(&delivery)
					if errors.Is(load.Error, gorm.ErrRecordNotFound) {
						return ErrOutboxLockLost
					}
					if load.Error != nil {
						return fmt.Errorf(
							"lock failed outbox delivery: %w",
							load.Error,
						)
					}
					status := models.OutboxDeliveryFailed
					if delivery.Attempts >= delivery.MaxAttempts {
						status = models.OutboxDeliveryDead
					}
					backoff := time.Second * time.Duration(
						1<<minInt(delivery.Attempts, 10),
					)
					if backoff > time.Hour {
						backoff = time.Hour
					}
					now := s.now()
					result := tx.Model(&models.OutboxDelivery{}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ? AND status = ? AND locked_by = ?",
							deliveryID,
							operation.Scope.OrganizationID,
							operation.Scope.ProjectID,
							models.OutboxDeliveryProcessing,
							workerID,
						).
						Updates(map[string]any{
							"status":          status,
							"next_attempt_at": now.Add(backoff),
							"locked_at":       nil,
							"locked_by":       "",
							"last_error":      message,
							"updated_at":      now,
						})
					if result.Error != nil {
						return fmt.Errorf(
							"mark outbox failed: %w",
							result.Error,
						)
					}
					if result.RowsAffected == 0 {
						return ErrOutboxLockLost
					}
					return nil
				},
			)
		},
	)
}

func (s *AgentNativeService) ReplayOutbox(ctx context.Context, deliveryID string) error {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	return s.InTransaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		var delivery models.OutboxDelivery
		if err := tx.Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			deliveryID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).First(&delivery).Error; err != nil {
			return err
		}
		if delivery.Status != models.OutboxDeliveryFailed &&
			delivery.Status != models.OutboxDeliveryDead {
			return ErrOutboxReplayConflict
		}
		result := tx.Model(&models.OutboxDelivery{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND status IN ?",
				deliveryID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
				[]models.OutboxDeliveryStatus{
					models.OutboxDeliveryFailed,
					models.OutboxDeliveryDead,
				},
			).
			Updates(map[string]any{
				"status":          models.OutboxDeliveryPending,
				"attempts":        0,
				"next_attempt_at": now,
				"locked_at":       nil,
				"locked_by":       "",
				"last_error":      "",
				"delivered_at":    nil,
				"updated_at":      now,
			})
		if result.Error != nil {
			return fmt.Errorf("replay outbox delivery: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// A worker may claim a failed delivery after the read above. The
			// status predicate is the linearization point: never clear a lock
			// or requeue a delivery after another actor won that race.
			return ErrOutboxReplayConflict
		}
		if err := tx.Model(&models.DomainEvent{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				delivery.EventID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).
			Update("published_at", nil).Error; err != nil {
			return fmt.Errorf("reset replayed event publication state: %w", err)
		}
		return nil
	})
}

type OutboxDeliverer interface {
	// Deliver may be called concurrently for independent delivery records.
	// Implementations must not retain mutable per-attempt state without
	// synchronization.
	Deliver(ctx context.Context, delivery *models.OutboxDelivery, event CloudEventEnvelope) error
}

type OutboxDeliverFunc func(ctx context.Context, delivery *models.OutboxDelivery, event CloudEventEnvelope) error

func (f OutboxDeliverFunc) Deliver(ctx context.Context, delivery *models.OutboxDelivery, event CloudEventEnvelope) error {
	return f(ctx, delivery, event)
}

type OutboxBatchResult struct {
	Claimed   int
	Delivered int
	Failed    int
	Dead      int
}

type outboxWorkerProject struct {
	Scope models.ProjectScope
	Key   models.ProjectKey
}

func archivedProjectOutboxDestinations() []string {
	return []string{
		AttachmentStagingCleanupOutboxDestination,
		AttachmentCleanupOutboxDestination,
		AttachmentUploadOutboxDestination,
	}
}

func archivedProjectOutboxEventTypes() []string {
	return []string{
		ProjectMembershipDeactivatedEventType,
		UserAccessRevokedEventType,
		ProjectAccessRevokedEventType,
	}
}

func outboxWorkerProjects(
	ctx context.Context,
	db *gorm.DB,
	actor models.ActorRef,
) ([]outboxWorkerProject, error) {
	if ctx == nil || db == nil ||
		actor != models.SystemActor(outboxSystemActorID) {
		return nil, ErrSystemWorkerContext
	}
	query := db.WithContext(ctx).
		Select("id", "organization_id", "key", "status").
		Where(
			"status IN ?",
			[]models.ProjectStatus{
				models.ProjectStatusActive,
				models.ProjectStatusArchived,
			},
		)
	if operation, err := OperationContextFromContext(ctx); err == nil {
		if operation.Actor != actor ||
			operation.Source != SourceProtocolWorker {
			return nil, ErrSystemWorkerContext
		}
		query = query.Where(
			"id = ? AND organization_id = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
		)
	}
	var projects []models.Project
	if err := query.
		Order("organization_id ASC, id ASC").
		Limit(maxSystemWorkerProjects + 1).
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf(
			"list outbox worker projects: %w",
			err,
		)
	}
	if len(projects) > maxSystemWorkerProjects {
		return nil, ErrSystemWorkerProjectLimit
	}
	result := make([]outboxWorkerProject, 0, len(projects))
	for index := range projects {
		scope := projects[index].Scope()
		if err := scope.Validate(); err != nil {
			return nil, err
		}
		result = append(result, outboxWorkerProject{
			Scope: scope,
			Key:   projects[index].Key,
		})
	}
	return result, nil
}

func (s *AgentNativeService) ProcessOutboxBatch(
	ctx context.Context,
	workerID string,
	limit int,
	deliverer OutboxDeliverer,
) (OutboxBatchResult, error) {
	result := OutboxBatchResult{}
	if deliverer == nil {
		return result, fmt.Errorf("outbox deliverer is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	actor := models.SystemActor(outboxSystemActorID)
	projects, err := outboxWorkerProjects(ctx, s.db, actor)
	if err != nil {
		return result, err
	}
	deliveries := make([]*models.OutboxDelivery, 0, limit)
	if len(projects) > 0 {
		start := int(
			(s.outboxProjectCursor.Add(1) - 1) % uint64(len(projects)),
		)
		for offset := 0; offset < len(projects) &&
			len(deliveries) < limit; offset++ {
			project := projects[(start+offset)%len(projects)]
			traceID := fmt.Sprintf(
				"outbox:%s:%d:%d",
				workerID,
				project.Scope.ProjectID,
				s.now().UnixNano(),
			)
			projectCtx, contextErr := EnsureSystemProjectOperationContext(
				ctx,
				project.Scope,
				actor,
				traceID,
				traceID,
			)
			if contextErr != nil {
				return result, contextErr
			}
			var claimed []*models.OutboxDelivery
			claimed, claimErr := s.claimPendingOutbox(
				projectCtx,
				workerID,
				limit-len(deliveries),
				s.outboxLockTTL,
				archivedProjectOutboxDestinations(),
				archivedProjectOutboxEventTypes(),
			)
			if claimErr != nil {
				return result, fmt.Errorf(
					"claim project %s outbox: %w",
					project.Key,
					claimErr,
				)
			}
			deliveries = append(deliveries, claimed...)
		}
	}
	result.Claimed = len(deliveries)
	if len(deliveries) == 0 {
		return result, nil
	}

	var batchErrors []error
	type deliveryAttempt struct {
		delivery *models.OutboxDelivery
		err      error
	}
	type deliveryJob struct {
		delivery *models.OutboxDelivery
		deadline time.Time
	}

	workerCount := cap(s.outboxDeliverySlots)
	if workerCount > len(deliveries) {
		workerCount = len(deliveries)
	}
	if workerCount < 1 {
		workerCount = 1
	}
	jobs := make(chan deliveryJob, len(deliveries))
	attempts := make(chan deliveryAttempt, len(deliveries))
	for _, delivery := range deliveries {
		jobs <- deliveryJob{
			delivery: delivery,
			// Every claimed delivery receives the same full bounded window.
			// A non-cooperative black-hole attempt therefore cannot make queued
			// work extend the batch by another timeout per item.
			deadline: time.Now().Add(s.outboxDeliveryTimeout),
		}
	}
	close(jobs)

	for range workerCount {
		go func() {
			for job := range jobs {
				deliveryCtx, contextErr :=
					outboxDeliveryOperationContext(ctx, job.delivery)
				if contextErr != nil {
					attempts <- deliveryAttempt{
						delivery: job.delivery,
						err:      contextErr,
					}
					continue
				}
				attempts <- deliveryAttempt{
					delivery: job.delivery,
					err: s.performOutboxDeliveryAttempt(
						deliveryCtx,
						job.deadline,
						deliverer,
						job.delivery,
					),
				}
			}
		}()
	}

	for range deliveries {
		attempt := <-attempts
		finalizeBase, contextErr := outboxWorkerOperationContext(
			context.WithoutCancel(ctx),
			models.ProjectScope{
				OrganizationID: attempt.delivery.OrganizationID,
				ProjectID:      attempt.delivery.ProjectID,
			},
			attempt.delivery,
		)
		if contextErr != nil {
			batchErrors = append(batchErrors, contextErr)
			continue
		}
		finalizeCtx, cancelFinalize := context.WithTimeout(
			finalizeBase,
			5*time.Second,
		)
		if attempt.err == nil {
			markErr := s.MarkOutboxDelivered(
				finalizeCtx,
				attempt.delivery.ID,
				workerID,
			)
			cancelFinalize()
			if markErr != nil {
				batchErrors = append(batchErrors, markErr)
				continue
			}
			result.Delivered++
			continue
		}
		markErr := s.MarkOutboxFailed(
			finalizeCtx,
			attempt.delivery.ID,
			workerID,
			attempt.err,
		)
		cancelFinalize()
		if markErr != nil {
			batchErrors = append(batchErrors, markErr)
			continue
		}
		result.Failed++
		if attempt.delivery.Attempts >= attempt.delivery.MaxAttempts {
			result.Dead++
		}
	}
	return result, errors.Join(batchErrors...)
}

func requireOutboxWorkerOperation(
	ctx context.Context,
) (OperationContext, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, fmt.Errorf(
			"%w: outbox operation requires trusted context",
			ErrSystemWorkerContext,
		)
	}
	if operation.Actor != models.SystemActor(outboxSystemActorID) ||
		operation.Source != SourceProtocolWorker {
		return OperationContext{}, fmt.Errorf(
			"%w: outbox operation actor mismatch",
			ErrSystemWorkerContext,
		)
	}
	return operation, nil
}

func outboxWorkerOperationContext(
	ctx context.Context,
	scope models.ProjectScope,
	delivery *models.OutboxDelivery,
) (context.Context, error) {
	traceID := ""
	correlationID := ""
	if delivery != nil {
		traceID = delivery.ID
		if delivery.Event != nil {
			if strings.TrimSpace(delivery.Event.TraceID) != "" {
				traceID = delivery.Event.TraceID
			}
			correlationID = delivery.Event.CorrelationID
		}
	}
	if correlationID == "" {
		correlationID = traceID
	}
	return WithOperationContext(ctx, OperationContext{
		Scope:         scope,
		Actor:         models.SystemActor(outboxSystemActorID),
		Source:        SourceProtocolWorker,
		TraceID:       traceID,
		CorrelationID: correlationID,
	})
}

// outboxDeliveryOperationContext creates trusted per-destination provenance
// without opening a database transaction. Deliverers may perform network,
// model or file I/O; any database prepare/finalize phase they need must open
// its own short project-scoped transaction.
func outboxDeliveryOperationContext(
	ctx context.Context,
	delivery *models.OutboxDelivery,
) (context.Context, error) {
	if delivery == nil ||
		delivery.OrganizationID == 0 ||
		delivery.ProjectID == 0 ||
		delivery.Event == nil ||
		delivery.Event.OrganizationID != delivery.OrganizationID ||
		delivery.Event.ProjectID != delivery.ProjectID {
		return nil, errors.New("outbox delivery has invalid project scope")
	}
	actorID := outboxSystemActorID
	switch delivery.DestinationType {
	case "automation":
		actorID = automationActorID
	case SLAEscalationOutboxDestination:
		actorID = slaMonitorActorID
	}
	traceID := delivery.Event.TraceID
	if traceID == "" {
		traceID = delivery.ID
	}
	correlationID := delivery.Event.CorrelationID
	if correlationID == "" {
		correlationID = traceID
	}
	return WithOperationContext(ctx, OperationContext{
		Scope: models.ProjectScope{
			OrganizationID: delivery.OrganizationID,
			ProjectID:      delivery.ProjectID,
		},
		Actor:         models.SystemActor(actorID),
		Source:        SourceProtocolWorker,
		TraceID:       traceID,
		CorrelationID: correlationID,
	})
}

func (s *AgentNativeService) performOutboxDeliveryAttempt(
	parent context.Context,
	deadline time.Time,
	deliverer OutboxDeliverer,
	delivery *models.OutboxDelivery,
) error {
	if delivery == nil {
		return errors.New("outbox delivery is missing")
	}
	if delivery.Event == nil {
		return fmt.Errorf("domain event %s is missing", delivery.EventID)
	}
	attemptCtx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if err := attemptCtx.Err(); err != nil {
		return err
	}

	select {
	case s.outboxDeliverySlots <- struct{}{}:
	case <-attemptCtx.Done():
		return fmt.Errorf("outbox delivery slot unavailable: %w", attemptCtx.Err())
	}

	outcome := make(chan error, 1)
	go func() {
		defer func() {
			<-s.outboxDeliverySlots
			if recover() != nil {
				// Panic payloads can contain sensitive adapter state and must
				// not be persisted in Outbox error details.
				outcome <- errors.New("outbox deliverer panicked")
			}
		}()
		outcome <- deliverer.Deliver(
			attemptCtx,
			delivery,
			CloudEventFromModel(delivery.Event),
		)
	}()

	select {
	case err := <-outcome:
		return err
	case <-attemptCtx.Done():
		// The downstream outcome is unknown near a timeout. SMTP/webhook
		// protocols do not provide exactly-once semantics; the bounded slot is
		// released only if the adapter eventually returns.
		return fmt.Errorf("outbox delivery attempt timed out: %w", attemptCtx.Err())
	}
}

func (s *AgentNativeService) normalizeLeaseTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		ttl = s.defaultLeaseTTL
	}
	if ttl < 10*time.Second || ttl > s.maxLeaseTTL {
		return 0, fmt.Errorf("lease ttl must be between 10s and %s", s.maxLeaseTTL)
	}
	return ttl, nil
}

func commandProjectScope(ctx context.Context) (models.ProjectScope, error) {
	scope, err := RequireProjectScope(ctx)
	if err != nil {
		return models.ProjectScope{}, fmt.Errorf(
			"project-scoped command requires trusted operation context: %w",
			err,
		)
	}
	return scope, nil
}

func commandOperationContext(
	ctx context.Context,
	actor models.ActorRef,
) (OperationContext, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, fmt.Errorf(
			"project-scoped command requires trusted operation context: %w",
			err,
		)
	}
	if operation.Actor != actor {
		return OperationContext{}, fmt.Errorf(
			"%w: command actor does not match trusted operation context",
			ErrInvalidActor,
		)
	}
	return operation, nil
}

func (s *AgentNativeService) claimTicketLeaseOnDB(
	ctx context.Context,
	db *gorm.DB,
	ticketID uint,
	actor models.ActorRef,
	expectedVersion uint64,
	ttl time.Duration,
) (*models.TicketLease, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	ttl, err = s.normalizeLeaseTTL(ttl)
	if err != nil {
		return nil, err
	}
	if expectedVersion == 0 {
		return nil, fmt.Errorf("%w: expected version is required", ErrVersionConflict)
	}
	var claimed *models.TicketLease
	err = transactionForContext(ctx, db, func(tx *gorm.DB) error {
		ticket, err := lockTicketForLeaseTx(
			tx.WithContext(ctx),
			projectScope,
			ticketID,
		)
		if err != nil {
			return err
		}
		if ticket.Version != expectedVersion {
			return fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, expectedVersion, ticket.Version)
		}

		var lease models.TicketLease
		leaseErr := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"organization_id = ? AND project_id = ? AND ticket_id = ?",
				projectScope.OrganizationID,
				projectScope.ProjectID,
				ticketID,
			).
			First(&lease).Error
		// Lease expiry must be evaluated only after the final revocable row has
		// been locked. Otherwise a waiter can revive a lease that expired while
		// another transaction held the row.
		now := s.now()
		if errors.Is(leaseErr, gorm.ErrRecordNotFound) {
			lease = models.TicketLease{
				ID:              newNativeID(),
				OrganizationID:  projectScope.OrganizationID,
				ProjectID:       projectScope.ProjectID,
				TicketID:        ticketID,
				HolderActorType: actor.Type,
				HolderActorID:   actor.ID,
				TicketVersion:   ticket.Version,
				ExpiresAt:       now.Add(ttl),
				LastHeartbeatAt: now,
			}
			if err := tx.Create(&lease).Error; err != nil {
				if isUniqueConstraintError(err) {
					return ErrLeaseConflict
				}
				return err
			}
			claimed = &lease
			return nil
		}
		if leaseErr != nil {
			return leaseErr
		}

		if lease.IsActive(now) {
			if lease.HolderActorType != actor.Type || lease.HolderActorID != actor.ID {
				return fmt.Errorf(
					"%w: held by %s/%s until %s",
					ErrLeaseConflict,
					lease.HolderActorType,
					lease.HolderActorID,
					lease.ExpiresAt.Format(time.RFC3339),
				)
			}
			// A retry by the same holder is idempotent. Refreshing version here
			// also lets the holder explicitly recover after a human edit.
			result := tx.Model(&models.TicketLease{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND released_at IS NULL AND expires_at > ?",
					lease.ID,
					projectScope.OrganizationID,
					projectScope.ProjectID,
					now,
				).
				Updates(map[string]any{
					"ticket_version":    ticket.Version,
					"expires_at":        now.Add(ttl),
					"last_heartbeat_at": now,
					"updated_at":        now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrLeaseConflict
			}
			lease.TicketVersion = ticket.Version
			lease.ExpiresAt = now.Add(ttl)
			lease.LastHeartbeatAt = now
			claimed = &lease
			return nil
		}

		newID := newNativeID()
		result := tx.Model(&models.TicketLease{}).
			Where(
				"organization_id = ? AND project_id = ? AND ticket_id = ? AND (released_at IS NOT NULL OR expires_at <= ?)",
				projectScope.OrganizationID,
				projectScope.ProjectID,
				ticketID,
				now,
			).
			Updates(map[string]any{
				"id":                newID,
				"holder_actor_type": actor.Type,
				"holder_actor_id":   actor.ID,
				"ticket_version":    ticket.Version,
				"expires_at":        now.Add(ttl),
				"last_heartbeat_at": now,
				"released_at":       nil,
				"release_reason":    "",
				"created_at":        now,
				"updated_at":        now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrLeaseConflict
		}
		lease.ID = newID
		lease.HolderActorType = actor.Type
		lease.HolderActorID = actor.ID
		lease.TicketVersion = ticket.Version
		lease.ExpiresAt = now.Add(ttl)
		lease.LastHeartbeatAt = now
		lease.ReleasedAt = nil
		lease.ReleaseReason = ""
		claimed = &lease
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *AgentNativeService) heartbeatTicketLeaseOnDB(
	ctx context.Context,
	db *gorm.DB,
	leaseID string,
	actor models.ActorRef,
	expectedVersion uint64,
	ttl time.Duration,
) (*models.TicketLease, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	ttl, err = s.normalizeLeaseTTL(ttl)
	if err != nil {
		return nil, err
	}
	ticketID, err := ticketIDForLease(
		ctx,
		db,
		projectScope,
		leaseID,
	)
	if err != nil {
		return nil, err
	}
	var lease models.TicketLease
	err = transactionForContext(ctx, db, func(tx *gorm.DB) error {
		ticket, lockedLease, lockErr := lockTicketLeaseTx(
			tx.WithContext(ctx),
			projectScope,
			ticketID,
			leaseID,
		)
		if lockErr != nil {
			return lockErr
		}
		lease = *lockedLease
		// Sample the clock after Ticket and Lease are both locked so a lease
		// cannot be revived after expiring behind either lock.
		now := s.now()
		if lease.HolderActorType != actor.Type || lease.HolderActorID != actor.ID {
			return ErrLeaseNotOwned
		}
		if !lease.IsActive(now) {
			return ErrLeaseExpired
		}
		if expectedVersion == 0 || ticket.Version != expectedVersion || lease.TicketVersion != expectedVersion {
			return fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, expectedVersion, ticket.Version)
		}
		result := tx.Model(&models.TicketLease{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND released_at IS NULL AND expires_at > ?",
				leaseID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
				now,
			).
			Updates(map[string]any{
				"expires_at":        now.Add(ttl),
				"last_heartbeat_at": now,
				"updated_at":        now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrLeaseExpired
		}
		lease.ExpiresAt = now.Add(ttl)
		lease.LastHeartbeatAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

func (s *AgentNativeService) releaseTicketLeaseOnDB(
	ctx context.Context,
	db *gorm.DB,
	leaseID string,
	actor models.ActorRef,
	reason string,
) (*models.TicketLease, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	ticketID, err := ticketIDForLease(
		ctx,
		db,
		projectScope,
		leaseID,
	)
	if err != nil {
		return nil, err
	}
	var released models.TicketLease
	err = transactionForContext(ctx, db, func(tx *gorm.DB) error {
		_, lease, lockErr := lockTicketLeaseTx(
			tx.WithContext(ctx),
			projectScope,
			ticketID,
			leaseID,
		)
		if lockErr != nil {
			return lockErr
		}
		released = *lease
		if actor.Type != models.ActorTypeSystem &&
			(lease.HolderActorType != actor.Type || lease.HolderActorID != actor.ID) {
			return ErrLeaseNotOwned
		}
		if lease.ReleasedAt != nil {
			return ErrLeaseExpired
		}
		now := s.now()
		releaseReason := truncateText(strings.TrimSpace(reason), 255)
		result := tx.WithContext(ctx).
			Model(&models.TicketLease{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND released_at IS NULL",
				leaseID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
			).
			Updates(map[string]any{
				"released_at":    now,
				"release_reason": releaseReason,
				"updated_at":     now,
			})
		if result.Error != nil {
			return fmt.Errorf("release ticket lease: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrLeaseExpired
		}
		released.ReleasedAt = &now
		released.ReleaseReason = releaseReason
		released.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &released, nil
}

type ClaimTicketLeaseCommandInput struct {
	TicketID                             uint
	Actor                                models.ActorRef
	ExpectedVersion                      uint64
	TTL                                  time.Duration
	CredentialID                         string
	PolicyDecisionID                     string
	ExternalNotificationPolicyDecisionID string
	SourceProtocol                       string
	RequestDigest                        string
	IdempotencyRecordID                  string
	TraceID                              string
	CorrelationID                        string
	CausationID                          string
	OutboxTargets                        []OutboxTarget
}

type HeartbeatTicketLeaseCommandInput struct {
	LeaseID             string
	Actor               models.ActorRef
	ExpectedVersion     uint64
	TTL                 time.Duration
	CredentialID        string
	PolicyDecisionID    string
	SourceProtocol      string
	RequestDigest       string
	IdempotencyRecordID string
	TraceID             string
	CorrelationID       string
	CausationID         string
	OutboxTargets       []OutboxTarget
}

type ReleaseTicketLeaseCommandInput struct {
	LeaseID             string
	Actor               models.ActorRef
	Reason              string
	CredentialID        string
	PolicyDecisionID    string
	SourceProtocol      string
	RequestDigest       string
	IdempotencyRecordID string
	TraceID             string
	CorrelationID       string
	CausationID         string
	OutboxTargets       []OutboxTarget
}

type TicketLeaseCommandResult struct {
	Lease   *models.TicketLease `json:"lease"`
	Event   *models.DomainEvent `json:"event"`
	Receipt OperationReceipt    `json:"receipt"`
}

func (s *AgentNativeService) ClaimTicketLeaseCommand(
	ctx context.Context,
	input ClaimTicketLeaseCommandInput,
) (*TicketLeaseCommandResult, error) {
	policyDecisionID, err := s.authorizeLeaseCommand(
		ctx,
		input.Actor,
		input.CredentialID,
		input.PolicyDecisionID,
		"ticket.claim",
		input.TicketID,
		input.SourceProtocol,
		input.RequestDigest,
	)
	if err != nil {
		return nil, err
	}
	var lease *models.TicketLease
	var event *models.DomainEvent
	var receipt OperationReceipt
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		var claimErr error
		lease, claimErr = s.claimTicketLeaseOnDB(
			ctx,
			tx,
			input.TicketID,
			input.Actor,
			input.ExpectedVersion,
			input.TTL,
		)
		if claimErr != nil {
			return claimErr
		}
		event, claimErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:            "io.chronodesk.ticket.lease.claimed.v1",
			Subject:         fmt.Sprintf("ticket/%d", lease.TicketID),
			Actor:           input.Actor,
			ResourceVersion: lease.TicketVersion,
			TraceID:         input.TraceID,
			CorrelationID:   input.CorrelationID,
			CausationID:     input.CausationID,
			Data: map[string]any{
				"ticket_id":          lease.TicketID,
				"lease_id":           lease.ID,
				"expires_at":         lease.ExpiresAt,
				"policy_decision_id": policyDecisionID,
			},
		}, input.OutboxTargets)
		if claimErr != nil {
			return claimErr
		}
		receipt = leaseOperationReceipt(lease, event, policyDecisionID)
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusOK,
				receipt,
				receipt.ResourceID,
				event.ID,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				leaseSnapshot(lease),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &TicketLeaseCommandResult{Lease: lease, Event: event, Receipt: receipt}, nil
}

func (s *AgentNativeService) HeartbeatTicketLeaseCommand(
	ctx context.Context,
	input HeartbeatTicketLeaseCommandInput,
) (*TicketLeaseCommandResult, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var existing models.TicketLease
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		input.LeaseID,
		projectScope.OrganizationID,
		projectScope.ProjectID,
	).First(&existing).Error; err != nil {
		return nil, err
	}
	policyDecisionID, err := s.authorizeLeaseCommand(
		ctx,
		input.Actor,
		input.CredentialID,
		input.PolicyDecisionID,
		"ticket.lease.heartbeat",
		existing.TicketID,
		input.SourceProtocol,
		input.RequestDigest,
	)
	if err != nil {
		return nil, err
	}
	var lease *models.TicketLease
	var event *models.DomainEvent
	var receipt OperationReceipt
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		var heartbeatErr error
		lease, heartbeatErr = s.heartbeatTicketLeaseOnDB(
			ctx,
			tx,
			input.LeaseID,
			input.Actor,
			input.ExpectedVersion,
			input.TTL,
		)
		if heartbeatErr != nil {
			return heartbeatErr
		}
		event, heartbeatErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:            "io.chronodesk.ticket.lease.heartbeat.v1",
			Subject:         fmt.Sprintf("ticket/%d", lease.TicketID),
			Actor:           input.Actor,
			ResourceVersion: lease.TicketVersion,
			TraceID:         input.TraceID,
			CorrelationID:   input.CorrelationID,
			CausationID:     input.CausationID,
			Data: map[string]any{
				"ticket_id":          lease.TicketID,
				"lease_id":           lease.ID,
				"expires_at":         lease.ExpiresAt,
				"policy_decision_id": policyDecisionID,
			},
		}, input.OutboxTargets)
		if heartbeatErr != nil {
			return heartbeatErr
		}
		receipt = leaseOperationReceipt(lease, event, policyDecisionID)
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusOK,
				receipt,
				receipt.ResourceID,
				event.ID,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				leaseSnapshot(lease),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &TicketLeaseCommandResult{Lease: lease, Event: event, Receipt: receipt}, nil
}

func (s *AgentNativeService) ReleaseTicketLeaseCommand(
	ctx context.Context,
	input ReleaseTicketLeaseCommandInput,
) (*TicketLeaseCommandResult, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	var existing models.TicketLease
	if err := s.db.WithContext(ctx).Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		input.LeaseID,
		projectScope.OrganizationID,
		projectScope.ProjectID,
	).First(&existing).Error; err != nil {
		return nil, err
	}
	policyDecisionID, err := s.authorizeLeaseCommand(
		ctx,
		input.Actor,
		input.CredentialID,
		input.PolicyDecisionID,
		"ticket.lease.release",
		existing.TicketID,
		input.SourceProtocol,
		input.RequestDigest,
	)
	if err != nil {
		return nil, err
	}
	var event *models.DomainEvent
	var receipt OperationReceipt
	var lease *models.TicketLease
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		var releaseErr error
		lease, releaseErr = s.releaseTicketLeaseOnDB(
			ctx,
			tx,
			input.LeaseID,
			input.Actor,
			input.Reason,
		)
		if releaseErr != nil {
			return releaseErr
		}
		var appendErr error
		event, appendErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:            "io.chronodesk.ticket.lease.released.v1",
			Subject:         fmt.Sprintf("ticket/%d", lease.TicketID),
			Actor:           input.Actor,
			ResourceVersion: lease.TicketVersion,
			TraceID:         input.TraceID,
			CorrelationID:   input.CorrelationID,
			CausationID:     input.CausationID,
			Data: map[string]any{
				"ticket_id":          lease.TicketID,
				"lease_id":           lease.ID,
				"release_reason":     lease.ReleaseReason,
				"policy_decision_id": policyDecisionID,
			},
		}, input.OutboxTargets)
		if appendErr != nil {
			return appendErr
		}
		receipt = leaseOperationReceipt(lease, event, policyDecisionID)
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusOK,
				receipt,
				receipt.ResourceID,
				event.ID,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				leaseSnapshot(lease),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &TicketLeaseCommandResult{Lease: lease, Event: event, Receipt: receipt}, nil
}

func (s *AgentNativeService) authorizeLeaseCommand(
	ctx context.Context,
	actor models.ActorRef,
	credentialID string,
	providedDecisionID string,
	action string,
	ticketID uint,
	sourceProtocol string,
	requestDigest string,
) (string, error) {
	if err := actor.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if actor.Type != models.ActorTypeServicePrincipal {
		return "", nil
	}
	check := PolicyCheckInput{}
	if kind, canonical := nativeLeaseCommandKind(action); canonical {
		var planErr error
		check, _, planErr = nativeCommandPrimaryPolicyCheck(
			NativeCommandAuthorizationInput{
				Kind:           kind,
				Actor:          actor,
				CredentialID:   credentialID,
				TicketID:       ticketID,
				RequestDigest:  requestDigest,
				SourceProtocol: sourceProtocol,
			},
		)
		if planErr != nil {
			return "", planErr
		}
	} else {
		check = PolicyCheckInput{
			ServicePrincipalID: actor.ID,
			CredentialID:       credentialID,
			Scope:              models.ScopeTasksManage,
			Action:             action,
			ResourceType:       "ticket",
			ResourceID:         strconv.FormatUint(uint64(ticketID), 10),
			IsWrite:            true,
			RequestDigest:      requestDigest,
			SourceProtocol:     sourceProtocol,
		}
	}
	if providedDecisionID != "" {
		if err := s.validatePolicyDecision(ctx, providedDecisionID, actor, check); err != nil {
			return "", err
		}
		return providedDecisionID, nil
	}
	decision, err := s.CheckAction(ctx, check)
	if err != nil {
		return "", err
	}
	return decision.ID, nil
}

func leaseOperationReceipt(
	lease *models.TicketLease,
	event *models.DomainEvent,
	policyDecisionID string,
) OperationReceipt {
	return OperationReceipt{
		OperationID:      newNativeID(),
		ResourceID:       strconv.FormatUint(uint64(lease.TicketID), 10),
		ResourceVersion:  lease.TicketVersion,
		EventID:          event.ID,
		ChangedFields:    []string{"lease"},
		PolicyDecisionID: policyDecisionID,
	}
}

func leaseSnapshot(lease *models.TicketLease) map[string]any {
	return map[string]any{
		"lease_id":       lease.ID,
		"ticket_id":      lease.TicketID,
		"expires_at":     lease.ExpiresAt,
		"ticket_version": lease.TicketVersion,
		"released_at":    lease.ReleasedAt,
	}
}

func ticketIDForLease(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	leaseID string,
) (uint, error) {
	if db == nil {
		return 0, errors.New("ticket lease database is required")
	}
	var identity struct {
		TicketID uint
	}
	if err := db.WithContext(ctx).
		Model(&models.TicketLease{}).
		Select("ticket_id").
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			leaseID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&identity).Error; err != nil {
		return 0, err
	}
	return identity.TicketID, nil
}

func lockTicketForLeaseTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
) (*models.Ticket, error) {
	if tx == nil {
		return nil, errors.New("ticket lease transaction is required")
	}
	var ticket models.Ticket
	if err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Select(
			"id",
			"version",
			"organization_id",
			"project_id",
			"created_by_id",
			"assigned_to_id",
		).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&ticket).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

// lockTicketLeaseTx is the one row-lock order for every lease lifecycle and
// lease-consuming command: Ticket first, then Lease. Callers may perform
// non-locking identity reads before this helper, but no mutation or time-based
// decision is allowed until both rows are locked.
func lockTicketLeaseTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	ticketID uint,
	leaseID string,
) (*models.Ticket, *models.TicketLease, error) {
	ticket, err := lockTicketForLeaseTx(tx, scope, ticketID)
	if err != nil {
		return nil, nil, err
	}
	var lease models.TicketLease
	if err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ?",
			leaseID,
			ticketID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		First(&lease).Error; err != nil {
		return nil, nil, err
	}
	return ticket, &lease, nil
}

func (s *AgentNativeService) validateTicketLeaseTx(
	tx *gorm.DB,
	leaseID string,
	ticketID uint,
	actor models.ActorRef,
	expectedVersion uint64,
) (*models.TicketLease, error) {
	if tx == nil {
		return nil, errors.New("ticket lease transaction is required")
	}
	scope, err := commandProjectScope(tx.Statement.Context)
	if err != nil {
		return nil, err
	}
	ticket, lease, err := lockTicketLeaseTx(
		tx,
		scope,
		ticketID,
		leaseID,
	)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLeaseConflict
		}
		return nil, err
	}
	// The final revocable row is now locked. Sampling earlier lets an operation
	// queued behind a Lease lock use a stale pre-expiry timestamp.
	now := s.now()
	if lease.HolderActorType != actor.Type || lease.HolderActorID != actor.ID {
		return nil, ErrLeaseNotOwned
	}
	if !lease.IsActive(now) {
		return nil, ErrLeaseExpired
	}
	if expectedVersion == 0 ||
		ticket.Version != expectedVersion ||
		lease.TicketVersion != expectedVersion {
		return nil, fmt.Errorf("%w: lease version %d, expected %d", ErrVersionConflict, lease.TicketVersion, expectedVersion)
	}
	return lease, nil
}

type OperationReceipt struct {
	OperationID      string   `json:"operation_id"`
	ResourceID       string   `json:"resource_id"`
	ResourceVersion  uint64   `json:"resource_version"`
	EventID          string   `json:"event_id"`
	ChangedFields    []string `json:"changed_fields"`
	PolicyDecisionID string   `json:"policy_decision_id,omitempty"`
}

func linkTicketHistoryToDomainEvent(
	history *models.TicketHistory,
	event *models.DomainEvent,
) error {
	if history == nil || event == nil || event.ID == "" || event.ResourceVersion == 0 {
		return fmt.Errorf("ticket history requires a persisted domain event")
	}
	expectedSubject := fmt.Sprintf("ticket/%d", history.TicketID)
	if event.Subject != expectedSubject {
		return fmt.Errorf(
			"ticket history event subject mismatch: expected %q, got %q",
			expectedSubject,
			event.Subject,
		)
	}
	actor := history.Actor()
	if event.ActorType != actor.Type || event.ActorID != actor.ID {
		return fmt.Errorf(
			"ticket history event actor mismatch: expected %s/%s, got %s/%s",
			actor.Type,
			actor.ID,
			event.ActorType,
			event.ActorID,
		)
	}
	eventID := event.ID
	history.EventID = &eventID
	history.ResourceVersion = event.ResourceVersion
	history.Provenance = models.TicketHistoryProvenanceDomainEvent
	return nil
}

type NativeTicketCreateInput struct {
	Request              models.TicketCreateRequest
	TicketNumber         string
	QueueID              uint
	RequestTypeVersionID string
	WorkflowVersionID    string
	Actor                models.ActorRef
	AssignedActor        *models.ActorRef
	CredentialID         string
	PolicyDecisionID     string
	SourceProtocol       string
	RequestDigest        string
	TrustLevel           models.TicketTrustLevel
	TraceID              string
	CorrelationID        string
	IdempotencyRecordID  string
	OutboxTargets        []OutboxTarget
}

type NativeTicketCreateResult struct {
	Ticket  *models.Ticket      `json:"ticket"`
	Event   *models.DomainEvent `json:"event"`
	Receipt OperationReceipt    `json:"receipt"`
}

// CreateNativeTicket is the transaction-safe Ticket intake command shared by
// human and machine protocol Adapters.
func (s *AgentNativeService) CreateNativeTicket(
	ctx context.Context,
	input NativeTicketCreateInput,
) (*NativeTicketCreateResult, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if input.Actor != operation.Actor {
		return nil, fmt.Errorf(
			"%w: command actor does not match trusted operation context",
			ErrInvalidActor,
		)
	}
	if strings.TrimSpace(input.Request.Title) == "" || strings.TrimSpace(input.Request.Description) == "" {
		return nil, fmt.Errorf("ticket title and description are required")
	}
	if !input.Request.Type.IsValid() || !input.Request.Priority.IsValid() {
		return nil, fmt.Errorf("invalid ticket type or priority")
	}
	if input.Request.Source == "" {
		input.Request.Source = models.TicketSourceAgent
	} else if !input.Request.Source.IsValid() {
		return nil, fmt.Errorf("invalid ticket source %q", input.Request.Source)
	}
	status := models.TicketStatusOpen
	if input.Request.Status != nil {
		status = *input.Request.Status
	}
	if !status.IsValid() {
		return nil, fmt.Errorf("invalid ticket status %q", status)
	}
	normalizedTags, err := normalizeTicketTags(input.Request.Tags)
	if err != nil {
		return nil, err
	}
	input.Request.Tags = normalizedTags
	if err := validateAgentContext(input.Request.AgentContext); err != nil {
		return nil, err
	}
	if input.TrustLevel == "" {
		input.TrustLevel = models.TicketTrustLevelUntrusted
	} else if !validTrustLevel(input.TrustLevel) {
		return nil, fmt.Errorf("invalid ticket trust level %q", input.TrustLevel)
	}
	assignedActor := input.AssignedActor
	if assignedActor == nil && input.Request.AssignedToID != nil {
		human := models.HumanActor(*input.Request.AssignedToID)
		assignedActor = &human
	}
	if assignedActor != nil &&
		input.Request.AssignedToID != nil &&
		(assignedActor.Type != models.ActorTypeHuman ||
			assignedActor.ID != models.HumanActor(*input.Request.AssignedToID).ID) {
		return nil, fmt.Errorf("%w: assigned actor conflicts with assigned_to_id", ErrInvalidAssignee)
	}
	if input.Actor.Type == models.ActorTypeServicePrincipal {
		if status != models.TicketStatusOpen ||
			assignedActor != nil ||
			input.Request.AssignedToID != nil {
			return nil, fmt.Errorf(
				"%w: Agent intake cannot assign or transition a ticket; use a separately authorized command",
				ErrCommandScopeMismatch,
			)
		}
		if input.Request.Source != models.TicketSourceAgent ||
			input.TrustLevel != models.TicketTrustLevelUntrusted {
			return nil, fmt.Errorf(
				"%w: Agent intake provenance is server-controlled",
				ErrCommandScopeMismatch,
			)
		}
	}
	assignmentChanges, err := s.ResolveTicketAssignmentChanges(ctx, assignedActor)
	if err != nil {
		return nil, err
	}

	policyDecisionID := input.PolicyDecisionID
	if input.Actor.Type == models.ActorTypeServicePrincipal {
		check, _, planErr := nativeCommandPrimaryPolicyCheck(
			NativeCommandAuthorizationInput{
				Kind:           NativeCommandTicketCreate,
				Actor:          input.Actor,
				CredentialID:   input.CredentialID,
				RequestDigest:  input.RequestDigest,
				SourceProtocol: input.SourceProtocol,
			},
		)
		if planErr != nil {
			return nil, planErr
		}
		if policyDecisionID != "" {
			if err := s.validatePolicyDecision(ctx, policyDecisionID, input.Actor, check); err != nil {
				return nil, err
			}
		} else {
			decision, err := s.CheckAction(ctx, check)
			if decision != nil {
				policyDecisionID = decision.ID
			}
			if err != nil {
				return nil, err
			}
		}
	}

	allowExternalNotifications, err := s.externalNotificationsAllowed(
		ctx,
		input.Actor,
		input.CredentialID,
		"",
		input.RequestDigest,
		input.SourceProtocol,
	)
	if err != nil {
		return nil, err
	}
	humanUserID, err := s.humanUserProjection(ctx, input.Actor)
	if err != nil {
		return nil, err
	}
	now := s.now()
	ticket := &models.Ticket{
		OrganizationID:       operation.Scope.OrganizationID,
		ProjectID:            operation.Scope.ProjectID,
		QueueID:              input.QueueID,
		RequestTypeVersionID: strings.TrimSpace(input.RequestTypeVersionID),
		WorkflowVersionID:    strings.TrimSpace(input.WorkflowVersionID),
		Title:                strings.TrimSpace(input.Request.Title),
		Description:          input.Request.Description,
		Type:                 input.Request.Type,
		Priority:             input.Request.Priority,
		Status:               status,
		Source:               input.Request.Source,
		Version:              1,
		TrustLevel:           input.TrustLevel,
		CreatedByID:          humanUserID,
		CreatedByActorType:   input.Actor.Type,
		CreatedByActorID:     input.Actor.ID,
		CategoryID:           input.Request.CategoryID,
		SubcategoryID:        input.Request.SubcategoryID,
		Tags:                 datatypes.JSONSlice[string](input.Request.Tags),
		DueDate:              input.Request.DueDate,
		CustomerEmail:        input.Request.CustomerEmail,
		CustomerPhone:        input.Request.CustomerPhone,
		CustomerName:         input.Request.CustomerName,
	}
	if input.Request.CustomFields != nil {
		ticket.CustomFields = datatypes.NewJSONType(input.Request.CustomFields.ToMap())
	}
	if input.Request.AgentContext != nil {
		ticket.AgentContext = datatypes.NewJSONType(*input.Request.AgentContext)
	}
	setTicketActorFields(ticket, input.Actor, nil)
	applyAssignmentChangesToTicket(ticket, assignmentChanges)
	applyTicketStatusTimestamps(ticket, status, now)

	var event *models.DomainEvent
	result := &NativeTicketCreateResult{Ticket: ticket}
	err = transactionForContext(ctx, s.dbForContext(ctx), func(tx *gorm.DB) error {
		ticket.CreatedAt = now
		ticket.UpdatedAt = now
		projectService, projectErr := NewProjectService(tx)
		if projectErr != nil {
			return projectErr
		}
		ticketNumber, projectErr := projectService.AllocateTicketIdentityTx(
			ctx,
			tx,
			operation.Scope,
		)
		if projectErr != nil {
			return fmt.Errorf("allocate project ticket number: %w", projectErr)
		}
		ticket.TicketNumber = ticketNumber
		queueQuery := tx.WithContext(ctx).Where(
			"project_id = ? AND status = ?",
			operation.Scope.ProjectID,
			models.QueueStatusActive,
		)
		if ticket.QueueID != 0 {
			queueQuery = queueQuery.Where("id = ?", ticket.QueueID)
		} else {
			queueQuery = queueQuery.Where("is_default = ?", true)
		}
		var queue models.Queue
		if queueErr := queueQuery.Order("id ASC").First(&queue).Error; queueErr != nil {
			return fmt.Errorf("resolve project queue: %w", queueErr)
		}
		ticket.QueueID = queue.ID
		configuration, configurationErr := resolveTicketConfigurationTx(
			ctx,
			tx,
			operation.Scope,
			&input.Request,
			ticket.RequestTypeVersionID,
			ticket.WorkflowVersionID,
		)
		if configurationErr != nil {
			return configurationErr
		}
		if err := validateTicketCategorySelectionTx(
			ctx,
			tx,
			operation.Scope,
			ticket.CategoryID,
			ticket.SubcategoryID,
		); err != nil {
			return err
		}
		initialStatus, configurationErr := configuration.InitialStatus()
		if configurationErr != nil {
			return fmt.Errorf(
				"resolve workflow initial state: %w",
				configurationErr,
			)
		}
		if ticket.Status != initialStatus {
			return fmt.Errorf(
				"ticket initial status %q does not match workflow lifecycle %q",
				ticket.Status,
				initialStatus,
			)
		}
		ticket.RequestTypeVersionID = configuration.RequestType.ID
		ticket.WorkflowVersionID = configuration.Workflow.ID
		projection, _, projectionErr := s.projectTicketSLAOnDB(ctx, tx, ticket, now)
		if projectionErr != nil {
			return fmt.Errorf("initialize ticket SLA projection: %w", projectionErr)
		}
		applySLAProjection(ticket, projection)
		if err := tx.Create(ticket).Error; err != nil {
			return fmt.Errorf("create native ticket: %w", err)
		}
		history := &models.TicketHistory{
			TicketID:           ticket.ID,
			UserID:             humanUserID,
			ActorType:          input.Actor.Type,
			ActorID:            input.Actor.ID,
			Action:             models.HistoryActionCreate,
			Description:        "工单已创建",
			IsVisible:          true,
			IsSystem:           input.Actor.Type == models.ActorTypeSystem,
			IsAutomated:        input.Actor.Type != models.ActorTypeHuman,
			IsImportant:        true,
			ServicePrincipalID: actorServicePrincipalID(input.Actor),
			Metadata:           policyMetadata(policyDecisionID),
		}
		var appendErr error
		event, appendErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:                       eventcontract.TicketCreatedEventType,
			Subject:                    fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:                      input.Actor,
			ResourceVersion:            ticket.Version,
			AllowExternalNotifications: allowExternalNotifications,
			TraceID:                    input.TraceID,
			CorrelationID:              input.CorrelationID,
			Scope:                      operation.Scope,
			ConfigurationVersion:       configuration.Release.ID,
			PolicyDecisionID:           policyDecisionID,
			DataSchema:                 "https://chronodesk.local/schemas/events/ticket-created.v1.json",
			Data: map[string]any{
				"organization_id":         ticket.OrganizationID,
				"project_id":              ticket.ProjectID,
				"queue_id":                ticket.QueueID,
				"ticket_id":               ticket.ID,
				"ticket_number":           ticket.TicketNumber,
				"title":                   ticket.Title,
				"status":                  ticket.Status,
				"priority":                ticket.Priority,
				"source":                  ticket.Source,
				"trust_level":             ticket.TrustLevel,
				"request_type_version_id": ticket.RequestTypeVersionID,
				"workflow_version_id":     ticket.WorkflowVersionID,
			},
		}, input.OutboxTargets)
		if appendErr != nil {
			return appendErr
		}
		if err := linkTicketHistoryToDomainEvent(history, event); err != nil {
			return err
		}
		if err := tx.Create(history).Error; err != nil {
			return fmt.Errorf("create ticket history: %w", err)
		}
		result.Receipt = OperationReceipt{
			OperationID:      newNativeID(),
			ResourceID:       strconv.FormatUint(uint64(ticket.ID), 10),
			ResourceVersion:  ticket.Version,
			EventID:          event.ID,
			ChangedFields:    []string{"ticket"},
			PolicyDecisionID: policyDecisionID,
		}
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusCreated,
				result.Receipt,
				result.Receipt.ResourceID,
				event.ID,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				ticket.ToResponse(),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result.Event = event
	return result, nil
}

type VersionedTicketUpdateInput struct {
	TicketID                 uint
	ExpectedVersion          uint64
	LeaseID                  string
	Actor                    models.ActorRef
	CredentialID             string
	PolicyDecisionID         string
	RequiredScope            string
	Action                   string
	SourceProtocol           string
	RequestDigest            string
	Changes                  map[string]any
	EventType                string
	EventData                any
	TraceID                  string
	CorrelationID            string
	CausationID              string
	IsRisky                  bool
	IdempotencyRecordID      string
	IdempotencyCompletionTTL time.Duration
	OutboxTargets            []OutboxTarget

	assignmentResolved            bool
	authorizationContractOverride bool
	historyRecords                []ticketHistorySpec
	slaProjectionAlreadyResolved  bool
}

type VersionedTicketUpdateResult struct {
	Ticket  *models.Ticket      `json:"ticket"`
	Event   *models.DomainEvent `json:"event"`
	Receipt OperationReceipt    `json:"receipt"`
}

func (s *AgentNativeService) UpdateTicketVersion(
	ctx context.Context,
	input VersionedTicketUpdateInput,
) (*VersionedTicketUpdateResult, error) {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	if input.ExpectedVersion == 0 {
		return nil, fmt.Errorf("%w: expected version is required", ErrVersionConflict)
	}
	if len(input.Changes) == 0 {
		return nil, fmt.Errorf("ticket changes are required")
	}
	changes, fields, err := sanitizeTicketChanges(input.Changes)
	if err != nil {
		return nil, err
	}
	if input.Actor.Type == models.ActorTypeServicePrincipal &&
		(fieldsContain(fields, "source") || fieldsContain(fields, "trust_level")) {
		return nil, fmt.Errorf(
			"%w: source and trust_level are server-controlled provenance fields",
			ErrCommandScopeMismatch,
		)
	}
	if input.Actor.Type == models.ActorTypeServicePrincipal &&
		assignmentFieldsPresent(fields) &&
		!input.assignmentResolved {
		return nil, fmt.Errorf(
			"%w: Agent assignment changes require AssignTicket or EscalateTicket",
			ErrCommandScopeMismatch,
		)
	}
	if input.Actor.Type != models.ActorTypeSystem &&
		(fieldsContain(fields, "sla_breached") || fieldsContain(fields, "sla_due_date")) {
		return nil, fmt.Errorf(
			"%w: SLA state is controlled by trusted system workflows",
			ErrCommandScopeMismatch,
		)
	}
	var (
		scope         string
		action        string
		categoryRisky bool
	)
	if input.Actor.Type == models.ActorTypeServicePrincipal &&
		!input.authorizationContractOverride {
		scope, action, categoryRisky, err = ticketChangeAuthorizationContract(
			fields,
			input.RequiredScope,
			input.Action,
		)
		if err != nil {
			return nil, err
		}
	} else {
		// Human commands may intentionally update several Ticket fields in one
		// form submission. Trusted system workflows and typed multi-scope
		// commands also arrive here only after their domain authorization has
		// completed.
		scope = input.RequiredScope
		action = input.Action
	}
	isRisky := input.IsRisky || categoryRisky
	policyDecisionID := input.PolicyDecisionID
	if input.Actor.Type == models.ActorTypeServicePrincipal {
		kind, canonical := nativeMutationCommandKind(scope, action)
		if !canonical {
			return nil, fmt.Errorf(
				"%w: unsupported Agent mutation policy contract %s/%s",
				ErrCommandScopeMismatch,
				scope,
				action,
			)
		}
		check, _, planErr := nativeCommandPrimaryPolicyCheck(
			NativeCommandAuthorizationInput{
				Kind:           kind,
				Actor:          input.Actor,
				CredentialID:   input.CredentialID,
				TicketID:       input.TicketID,
				RequestDigest:  input.RequestDigest,
				SourceProtocol: input.SourceProtocol,
			},
		)
		if planErr != nil {
			return nil, planErr
		}
		if check.Scope != scope ||
			check.Action != action ||
			check.IsRisky != isRisky {
			return nil, fmt.Errorf(
				"%w: Agent mutation policy contract drift",
				ErrCommandScopeMismatch,
			)
		}
		if policyDecisionID != "" {
			if err := s.validatePolicyDecision(ctx, policyDecisionID, input.Actor, check); err != nil {
				return nil, err
			}
		} else {
			decision, checkErr := s.CheckAction(ctx, check)
			if decision != nil {
				policyDecisionID = decision.ID
			}
			if checkErr != nil {
				return nil, checkErr
			}
		}
	}

	allowExternalNotifications, err := s.externalNotificationsAllowed(
		ctx,
		input.Actor,
		input.CredentialID,
		strconv.FormatUint(uint64(input.TicketID), 10),
		input.RequestDigest,
		input.SourceProtocol,
	)
	if err != nil {
		return nil, err
	}
	var ticket models.Ticket
	var event *models.DomainEvent
	var receipt OperationReceipt
	err = transactionForContext(ctx, s.db, func(tx *gorm.DB) error {
		if err := tx.Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			input.TicketID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).First(&ticket).Error; err != nil {
			return err
		}
		beforeTicket := ticket
		if ticket.Version != input.ExpectedVersion {
			return fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, input.ExpectedVersion, ticket.Version)
		}
		if input.Actor.Type == models.ActorTypeServicePrincipal && input.LeaseID == "" {
			return fmt.Errorf("%w: service principal updates require a lease", ErrLeaseConflict)
		}
		if input.LeaseID != "" {
			if _, err := s.validateTicketLeaseTx(tx, input.LeaseID, input.TicketID, input.Actor, input.ExpectedVersion); err != nil {
				return err
			}
		}
		if assignmentFieldsPresent(fields) {
			if err := validateCanonicalAssignmentChangesTx(ctx, tx, changes); err != nil {
				return err
			}
		}
		if err := validateTicketChangeSemantics(
			ctx,
			tx,
			projectScope,
			&ticket,
			changes,
			input.Actor,
		); err != nil {
			return err
		}
		if rawStatus, ok := changes["status"]; ok {
			nextStatus := models.TicketStatus(fmt.Sprint(rawStatus))
			applyTicketStatusTimestamps(&ticket, nextStatus, s.now())
			changes["resolved_at"] = ticket.ResolvedAt
			changes["closed_at"] = ticket.ClosedAt
		}
		changeSet := make(map[string]any, len(fields))
		for _, field := range fields {
			changeSet[field] = map[string]any{
				"old": ticketFieldValue(&ticket, field),
				"new": changes[field],
			}
		}
		changes["version"] = input.ExpectedVersion + 1
		changes["updated_at"] = s.now()
		update := tx.Model(&models.Ticket{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
				input.TicketID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
				input.ExpectedVersion,
			).
			Updates(changes)
		if update.Error != nil {
			return fmt.Errorf("update ticket version: %w", update.Error)
		}
		if update.RowsAffected != 1 {
			return ErrVersionConflict
		}
		if err := tx.Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			input.TicketID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).First(&ticket).Error; err != nil {
			return err
		}
		derivedSLAChanges := map[string]any{}
		if !input.slaProjectionAlreadyResolved {
			projection, _, projectionErr := s.projectTicketSLAOnDB(
				ctx,
				tx,
				&ticket,
				s.now(),
			)
			if projectionErr != nil {
				return fmt.Errorf("refresh ticket SLA projection: %w", projectionErr)
			}
			derivedSLAChanges = slaProjectionChanges(&ticket, projection)
			if len(derivedSLAChanges) > 0 {
				if err := tx.Model(&models.Ticket{}).
					Where(
						"id = ? AND organization_id = ? AND project_id = ?",
						ticket.ID,
						projectScope.OrganizationID,
						projectScope.ProjectID,
					).
					Updates(derivedSLAChanges).Error; err != nil {
					return fmt.Errorf("persist ticket SLA projection: %w", err)
				}
				applySLAProjection(&ticket, projection)
				for _, field := range slaProjectionFieldNames(derivedSLAChanges) {
					if !fieldsContain(fields, field) {
						fields = append(fields, field)
					}
					changeSet[field] = map[string]any{
						"old": ticketFieldValue(&beforeTicket, field),
						"new": ticketFieldValue(&ticket, field),
					}
				}
				sort.Strings(fields)
			}
		}
		humanUserID, projectionErr := s.humanUserProjectionTx(tx, input.Actor)
		if projectionErr != nil {
			return projectionErr
		}
		details, _ := json.Marshal(map[string]any{
			"changed_fields":     fields,
			"changes":            changeSet,
			"policy_decision_id": policyDecisionID,
		})
		histories := ticketHistoriesForUpdate(
			input,
			&beforeTicket,
			&ticket,
			fields,
			string(details),
			humanUserID,
			policyDecisionID,
		)
		eventType := input.EventType
		if eventType == "" {
			eventType = eventcontract.TicketUpdatedEventType
		}
		eventData := input.EventData
		var eventDataMap map[string]any
		if eventData == nil {
			eventDataMap = map[string]any{
				"ticket_id":      ticket.ID,
				"changed_fields": fields,
				"status":         ticket.Status,
				"priority":       ticket.Priority,
			}
			eventData = eventDataMap
		}
		if len(derivedSLAChanges) > 0 {
			if eventDataMap == nil {
				var normalizeErr error
				eventDataMap, normalizeErr = normalizeTicketEventDataObject(eventData)
				if normalizeErr != nil {
					return normalizeErr
				}
			}
			eventDataMap["ticket_id"] = ticket.ID
			eventDataMap["changed_fields"] = fields
			eventDataMap["sla_breached"] = ticket.SLABreached
			eventDataMap["sla_due_date"] = ticket.SLADueDate
			eventData = eventDataMap
		}
		notificationTargets := ticketUpdateNotificationTargets(
			&beforeTicket,
			&ticket,
			input.Actor,
			fields,
		)
		if len(notificationTargets) > 0 {
			if eventDataMap == nil {
				var normalizeErr error
				eventDataMap, normalizeErr = normalizeTicketEventDataObject(eventData)
				if normalizeErr != nil {
					return normalizeErr
				}
			}
			eventDataMap["ticket_id"] = ticket.ID
			addTicketNotificationEventSnapshot(eventDataMap, &ticket)
			if beforeTicket.Status != ticket.Status {
				eventDataMap["old_status"] = beforeTicket.Status
				eventDataMap["new_status"] = ticket.Status
			}
			if ticket.AssignedToID != nil {
				eventDataMap["assigned_to_id"] = *ticket.AssignedToID
			}
			eventData = eventDataMap
		}
		additionalTargets := make(
			[]OutboxTarget,
			0,
			len(input.OutboxTargets)+len(notificationTargets),
		)
		additionalTargets = append(additionalTargets, input.OutboxTargets...)
		additionalTargets = append(additionalTargets, notificationTargets...)
		var appendErr error
		event, appendErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:                       eventType,
			Subject:                    fmt.Sprintf("ticket/%d", ticket.ID),
			Actor:                      input.Actor,
			ResourceVersion:            ticket.Version,
			AllowExternalNotifications: allowExternalNotifications,
			TraceID:                    input.TraceID,
			CorrelationID:              input.CorrelationID,
			CausationID:                input.CausationID,
			Data:                       eventData,
		}, additionalTargets)
		if appendErr != nil {
			return appendErr
		}
		for _, history := range histories {
			if err := linkTicketHistoryToDomainEvent(history, event); err != nil {
				return err
			}
			if err := tx.Create(history).Error; err != nil {
				return err
			}
		}
		if input.LeaseID != "" {
			if err := tx.Model(&models.TicketLease{}).
				Where("id = ?", input.LeaseID).
				Update("ticket_version", ticket.Version).Error; err != nil {
				return err
			}
		}
		receipt = OperationReceipt{
			OperationID:      newNativeID(),
			ResourceID:       strconv.FormatUint(uint64(ticket.ID), 10),
			ResourceVersion:  ticket.Version,
			EventID:          event.ID,
			ChangedFields:    fields,
			PolicyDecisionID: policyDecisionID,
		}
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTxWithTTL(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusOK,
				receipt,
				receipt.ResourceID,
				event.ID,
				input.IdempotencyCompletionTTL,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				ticket.ToResponse(),
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &VersionedTicketUpdateResult{
		Ticket:  &ticket,
		Event:   event,
		Receipt: receipt,
	}, nil
}

type NativeCommentInput struct {
	TicketID                 uint
	ExpectedVersion          uint64
	LeaseID                  string
	Actor                    models.ActorRef
	CredentialID             string
	PolicyDecisionID         string
	SourceProtocol           string
	RequestDigest            string
	Content                  string
	ContentType              string
	Type                     models.CommentType
	ParentID                 *uint
	TimeSpent                *int
	BillableTime             *int
	WorkType                 string
	Reason                   string
	EvidenceRefs             []string
	InputSources             []string
	TraceID                  string
	CorrelationID            string
	CausationID              string
	AutomationRuleID         uint
	AutomationActionIndex    *int
	IdempotencyRecordID      string
	IdempotencyCompletionTTL time.Duration
	OutboxTargets            []OutboxTarget
}

type NativeCommentResult struct {
	Comment *models.TicketComment `json:"comment"`
	Event   *models.DomainEvent   `json:"event"`
	Receipt OperationReceipt      `json:"receipt"`
}

func (s *AgentNativeService) CreateComment(ctx context.Context, input NativeCommentInput) (*NativeCommentResult, error) {
	operation, err := commandOperationContext(ctx, input.Actor)
	if err != nil {
		return nil, err
	}
	projectScope := operation.Scope
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	if input.Actor.Type == models.ActorTypeServicePrincipal && input.LeaseID == "" {
		return nil, fmt.Errorf("%w: service principal comments require a ticket lease", ErrLeaseConflict)
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" ||
		utf8.RuneCountInString(input.Content) > maxNativeCommentRunes ||
		len(input.Content) > maxNativeCommentBytes {
		return nil, fmt.Errorf(
			"%w: content must be between 1 and %d characters",
			ErrInvalidComment,
			maxNativeCommentRunes,
		)
	}
	if input.ContentType == "" {
		input.ContentType = "text"
	}
	if input.ContentType != "text" && input.ContentType != "markdown" {
		return nil, fmt.Errorf("%w: content type must be text or markdown", ErrInvalidComment)
	}
	if input.Type != models.CommentTypePublic && input.Type != models.CommentTypeInternal && input.Type != models.CommentTypeSystem {
		return nil, fmt.Errorf("%w: unsupported comment type", ErrInvalidComment)
	}
	if input.Type == models.CommentTypeSystem && input.Actor.Type != models.ActorTypeSystem {
		return nil, fmt.Errorf("%w: only system actors can create system comments", ErrPolicyDenied)
	}
	if input.ExpectedVersion == 0 {
		return nil, fmt.Errorf("%w: expected version is required", ErrVersionConflict)
	}

	policyDecisionID := input.PolicyDecisionID
	if input.Actor.Type == models.ActorTypeServicePrincipal {
		check, _, planErr := nativeCommandPrimaryPolicyCheck(
			NativeCommandAuthorizationInput{
				Kind:           NativeCommandCommentCreate,
				Actor:          input.Actor,
				CredentialID:   input.CredentialID,
				TicketID:       input.TicketID,
				RequestDigest:  input.RequestDigest,
				SourceProtocol: input.SourceProtocol,
			},
		)
		if planErr != nil {
			return nil, planErr
		}
		if policyDecisionID != "" {
			if err := s.validatePolicyDecision(ctx, policyDecisionID, input.Actor, check); err != nil {
				return nil, err
			}
		} else {
			decision, err := s.CheckAction(ctx, check)
			if decision != nil {
				policyDecisionID = decision.ID
			}
			if err != nil {
				return nil, err
			}
		}
	}
	allowExternalNotifications, err := s.externalNotificationsAllowed(
		ctx,
		input.Actor,
		input.CredentialID,
		strconv.FormatUint(uint64(input.TicketID), 10),
		input.RequestDigest,
		input.SourceProtocol,
	)
	if err != nil {
		return nil, err
	}
	humanUserID, err := s.humanUserProjection(ctx, input.Actor)
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(map[string]any{
		"reason":             truncateText(input.Reason, 1000),
		"evidence_refs":      input.EvidenceRefs,
		"input_sources":      input.InputSources,
		"policy_decision_id": policyDecisionID,
		"untrusted_content":  true,
	})
	comment := &models.TicketComment{
		TicketID:           input.TicketID,
		UserID:             humanUserID,
		ActorType:          input.Actor.Type,
		ActorID:            input.Actor.ID,
		ServicePrincipalID: actorServicePrincipalID(input.Actor),
		Content:            input.Content,
		ContentType:        input.ContentType,
		Type:               input.Type,
		ParentID:           input.ParentID,
		Metadata:           string(metadata),
		TimeSpent:          input.TimeSpent,
		BillableTime:       input.BillableTime,
		WorkType:           truncateText(input.WorkType, 50),
	}
	var event *models.DomainEvent
	var resourceVersion uint64
	var receipt OperationReceipt
	err = transactionForContext(ctx, s.dbForContext(ctx), func(tx *gorm.DB) error {
		var ticket models.Ticket
		if err := tx.Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			input.TicketID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).First(&ticket).Error; err != nil {
			return err
		}
		if ticket.Version != input.ExpectedVersion {
			return fmt.Errorf("%w: expected %d, actual %d", ErrVersionConflict, input.ExpectedVersion, ticket.Version)
		}
		if input.LeaseID != "" {
			if _, err := s.validateTicketLeaseTx(tx, input.LeaseID, input.TicketID, input.Actor, input.ExpectedVersion); err != nil {
				return err
			}
		}
		if input.ParentID != nil {
			var parent models.TicketComment
			if err := tx.
				Select("id", "parent_id").
				Where(
					"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ? AND is_deleted = ?",
					*input.ParentID,
					input.TicketID,
					projectScope.OrganizationID,
					projectScope.ProjectID,
					false,
				).
				First(&parent).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: parent comment not found", ErrInvalidComment)
			} else if err != nil {
				return err
			}
			if parent.ParentID != nil {
				return fmt.Errorf("%w: %w", ErrInvalidComment, ErrNestedCommentReply)
			}
		}
		if err := tx.Create(comment).Error; err != nil {
			return fmt.Errorf("create comment: %w", err)
		}
		if input.ParentID != nil {
			result := tx.Model(&models.TicketComment{}).
				Where(
					"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ?",
					*input.ParentID,
					input.TicketID,
					projectScope.OrganizationID,
					projectScope.ProjectID,
				).
				Update("reply_count", gorm.Expr("reply_count + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%w: parent comment not found", ErrInvalidComment)
			}
		}
		resourceVersion = input.ExpectedVersion + 1
		ticketUpdates := map[string]any{
			"version":       resourceVersion,
			"comment_count": gorm.Expr("comment_count + 1"),
			"updated_at":    s.now(),
		}
		if input.Type == models.CommentTypePublic && input.Actor.Type != models.ActorTypeHuman {
			ticketUpdates["first_reply_at"] = gorm.Expr("COALESCE(first_reply_at, ?)", s.now())
		}
		projectionChanges := map[string]any{}
		if input.Type != models.CommentTypeSystem {
			projection, _, projectionErr := s.projectTicketSLAOnDB(
				ctx,
				tx,
				&ticket,
				s.now(),
			)
			if projectionErr != nil {
				return fmt.Errorf("refresh ticket SLA projection after comment: %w", projectionErr)
			}
			projectionChanges = slaProjectionChanges(&ticket, projection)
			for field, value := range projectionChanges {
				ticketUpdates[field] = value
			}
		}
		update := tx.Model(&models.Ticket{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
				input.TicketID,
				projectScope.OrganizationID,
				projectScope.ProjectID,
				input.ExpectedVersion,
			).
			Updates(ticketUpdates)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrVersionConflict
		}
		history := &models.TicketHistory{
			TicketID:           input.TicketID,
			UserID:             humanUserID,
			ActorType:          input.Actor.Type,
			ActorID:            input.Actor.ID,
			ServicePrincipalID: actorServicePrincipalID(input.Actor),
			Action:             models.HistoryActionComment,
			Description:        "添加了评论",
			CommentID:          &comment.ID,
			IsVisible:          input.Type == models.CommentTypePublic,
			IsSystem:           input.Actor.Type == models.ActorTypeSystem,
			IsAutomated:        input.Actor.Type != models.ActorTypeHuman,
			Metadata:           policyMetadata(policyDecisionID),
		}
		var appendErr error
		eventData := map[string]any{
			"ticket_id":         input.TicketID,
			"comment_id":        comment.ID,
			"comment_type":      comment.Type,
			"content_untrusted": true,
		}
		if input.AutomationRuleID > 0 {
			eventData["automation_rule_id"] = input.AutomationRuleID
		}
		if input.AutomationActionIndex != nil {
			eventData["automation_action_index"] = *input.AutomationActionIndex
		}
		event, appendErr = s.AppendDomainEventWithAdditionalTargetsTx(ctx, tx, DomainEventInput{
			Type:                       eventcontract.TicketCommentCreatedEventType,
			Subject:                    fmt.Sprintf("ticket/%d", input.TicketID),
			Actor:                      input.Actor,
			ResourceVersion:            resourceVersion,
			AllowExternalNotifications: allowExternalNotifications,
			TraceID:                    input.TraceID,
			CorrelationID:              input.CorrelationID,
			CausationID:                input.CausationID,
			Data:                       eventData,
		}, input.OutboxTargets)
		if appendErr != nil {
			return appendErr
		}
		if err := linkTicketHistoryToDomainEvent(history, event); err != nil {
			return err
		}
		if err := tx.Create(history).Error; err != nil {
			return err
		}
		if input.Type != models.CommentTypePublic {
			if err := tx.Model(history).UpdateColumn("is_visible", false).Error; err != nil {
				return err
			}
		}
		if input.LeaseID != "" {
			if err := tx.Model(&models.TicketLease{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					input.LeaseID,
					projectScope.OrganizationID,
					projectScope.ProjectID,
				).
				Update("ticket_version", resourceVersion).Error; err != nil {
				return err
			}
		}
		changedFields := []string{"comments", "comment_count"}
		changedFields = append(changedFields, slaProjectionFieldNames(projectionChanges)...)
		receipt = OperationReceipt{
			OperationID:      newNativeID(),
			ResourceID:       strconv.FormatUint(uint64(comment.ID), 10),
			ResourceVersion:  resourceVersion,
			EventID:          event.ID,
			ChangedFields:    changedFields,
			PolicyDecisionID: policyDecisionID,
		}
		if input.IdempotencyRecordID != "" {
			if err := s.CompleteIdempotencyTxWithTTL(
				ctx,
				tx,
				input.IdempotencyRecordID,
				http.StatusCreated,
				receipt,
				receipt.ResourceID,
				event.ID,
				input.IdempotencyCompletionTTL,
			); err != nil {
				return err
			}
			if err := s.storeIdempotencySnapshotTx(
				ctx,
				tx,
				input.IdempotencyRecordID,
				comment,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &NativeCommentResult{
		Comment: comment,
		Event:   event,
		Receipt: receipt,
	}, nil
}

// AttachmentStorage only accepts bytes supplied by the authenticated caller.
// It intentionally has no URL-fetch method.
type AttachmentStorage interface {
	Put(ctx context.Context, key string, reader io.Reader, maxBytes int64) (*StoredAttachmentObject, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// AttachmentStagingStore durably buffers an authenticated inbound body before
// the business transaction persists its upload intent. Only the Outbox worker
// may copy a staged object into AttachmentStorage after that transaction
// commits.
type AttachmentStagingStore interface {
	Stage(
		ctx context.Context,
		key string,
		reader io.Reader,
		maxBytes int64,
	) (*StoredAttachmentObject, error)
	OpenStaged(ctx context.Context, key string) (io.ReadCloser, error)
	DeleteStaged(ctx context.Context, key string) error
}

type attachmentStorageTyper interface {
	AttachmentStorageType() string
}

type StoredAttachmentObject struct {
	Key                 string
	Size                int64
	SHA256              string
	DetectedContentType string
	StorageType         string
	StoreID             string
	VersionID           string
}

type LocalAttachmentStorage struct {
	root string
}

// NewLocalAttachmentStorage configures an operator-owned storage root. Child
// access uses os.Root for every operation, so untrusted object keys cannot
// escape through traversal or symlinks. The configured root tree and its
// ancestors remain an operator boundary: they must not be mutable by an
// untrusted same-UID process or redirected with mounts while the service runs.
func NewLocalAttachmentStorage(root string) (*LocalAttachmentStorage, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("attachment root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve attachment root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create attachment root: %w", err)
	}
	storageRoot, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open attachment root: %w", err)
	}
	if err := storageRoot.Close(); err != nil {
		return nil, fmt.Errorf("close attachment root: %w", err)
	}
	return &LocalAttachmentStorage{root: absolute}, nil
}

func (s *LocalAttachmentStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"local attachment write",
	); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("attachment reader is required")
	}
	if maxBytes <= 0 {
		return nil, ErrAttachmentTooLarge
	}
	normalizedKey, err := normalizeLocalAttachmentKey(key)
	if err != nil {
		return nil, err
	}
	storageRoot, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = storageRoot.Close()
	}()
	relativePath := filepath.FromSlash(normalizedKey)
	parentsExist, err := verifyLocalAttachmentParents(
		storageRoot,
		normalizedKey,
		true,
	)
	if err != nil {
		return nil, err
	}
	parentPath := filepath.Dir(relativePath)
	if !parentsExist && parentPath != "." {
		if err := storageRoot.MkdirAll(parentPath, 0o700); err != nil {
			return nil, fmt.Errorf("create attachment directory: %w", err)
		}
	}
	if _, err := verifyLocalAttachmentParents(
		storageRoot,
		normalizedKey,
		false,
	); err != nil {
		return nil, fmt.Errorf("create attachment directory: %w", err)
	}
	// The partial path is derived from the durable target key. A hard process
	// crash can therefore be recovered by the staging-intent sweeper or by a
	// retry of the same outbox delivery; random .upload-* names would be
	// undiscoverable after restart.
	tempPath := relativePath + ".partial"
	if err := removeLocalAttachmentRegularFileIfExists(
		storageRoot,
		tempPath,
	); err != nil {
		return nil, fmt.Errorf(
			"remove stale attachment partial: %w",
			err,
		)
	}
	temp, err := storageRoot.OpenFile(
		tempPath,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("create attachment temp file: %w", err)
	}
	defer func() {
		_ = temp.Close()
		_ = storageRoot.Remove(tempPath)
	}()

	hash := sha256.New()
	var size int64
	sample := make([]byte, 0, 512)
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > maxBytes {
				return nil, ErrAttachmentTooLarge
			}
			if len(sample) < 512 {
				remaining := 512 - len(sample)
				if remaining > n {
					remaining = n
				}
				sample = append(sample, buffer[:remaining]...)
			}
			if _, err := hash.Write(buffer[:n]); err != nil {
				return nil, err
			}
			if _, err := temp.Write(buffer[:n]); err != nil {
				return nil, fmt.Errorf("write attachment: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read attachment: %w", readErr)
		}
	}
	if err := temp.Sync(); err != nil {
		return nil, fmt.Errorf("sync attachment: %w", err)
	}
	if err := temp.Close(); err != nil {
		return nil, err
	}
	if _, err := inspectLocalAttachmentRegularFile(
		storageRoot,
		relativePath,
		true,
	); err != nil {
		return nil, err
	}
	if err := storageRoot.Rename(tempPath, relativePath); err != nil {
		return nil, fmt.Errorf("commit attachment: %w", err)
	}
	return &StoredAttachmentObject{
		Key:                 normalizedKey,
		Size:                size,
		SHA256:              hex.EncodeToString(hash.Sum(nil)),
		DetectedContentType: http.DetectContentType(sample),
	}, nil
}

func (s *LocalAttachmentStorage) AttachmentStorageType() string {
	return "local"
}

func (s *LocalAttachmentStorage) Stage(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if !validAttachmentStagingKey(key) {
		return nil, ErrInvalidAttachmentName
	}
	return s.Put(ctx, key, reader, maxBytes)
}

func (s *LocalAttachmentStorage) OpenStaged(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	if !validAttachmentStagingKey(key) {
		return nil, ErrInvalidAttachmentName
	}
	return s.Open(ctx, key)
}

func (s *LocalAttachmentStorage) DeleteStaged(
	ctx context.Context,
	key string,
) error {
	if !validAttachmentStagingKey(key) {
		return ErrInvalidAttachmentName
	}
	return s.Delete(ctx, key)
}

func (s *LocalAttachmentStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"local attachment read",
	); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalizedKey, err := normalizeLocalAttachmentKey(key)
	if err != nil {
		return nil, err
	}
	storageRoot, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = storageRoot.Close()
	}()
	if _, err := verifyLocalAttachmentParents(
		storageRoot,
		normalizedKey,
		false,
	); err != nil {
		return nil, err
	}
	relativePath := filepath.FromSlash(normalizedKey)
	if _, err := inspectLocalAttachmentRegularFile(
		storageRoot,
		relativePath,
		false,
	); err != nil {
		return nil, err
	}
	// Non-blocking open prevents a same-UID filesystem race from replacing the
	// inspected regular file with a FIFO and hanging a request. Regular files
	// ignore O_NONBLOCK; the descriptor is checked again before it is returned.
	file, err := storageRoot.OpenFile(
		relativePath,
		os.O_RDONLY|syscall.O_NONBLOCK,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open attachment: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat opened attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrInvalidAttachmentName
	}
	return file, nil
}

func (s *LocalAttachmentStorage) Delete(ctx context.Context, key string) error {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"local attachment delete",
	); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	normalizedKey, err := normalizeLocalAttachmentKey(key)
	if err != nil {
		return err
	}
	storageRoot, err := s.openRoot()
	if err != nil {
		return err
	}
	defer func() {
		_ = storageRoot.Close()
	}()
	parentsExist, err := verifyLocalAttachmentParents(
		storageRoot,
		normalizedKey,
		true,
	)
	if err != nil {
		return err
	}
	if !parentsExist {
		return nil
	}
	relativePath := filepath.FromSlash(normalizedKey)
	tempPath := relativePath + ".partial"
	objectExists, err := inspectLocalAttachmentRegularFile(
		storageRoot,
		relativePath,
		true,
	)
	if err != nil {
		return err
	}
	partialExists, err := inspectLocalAttachmentRegularFile(
		storageRoot,
		tempPath,
		true,
	)
	if err != nil {
		return err
	}
	if objectExists {
		if err := storageRoot.Remove(relativePath); err != nil {
			return fmt.Errorf("delete attachment: %w", err)
		}
	}
	if partialExists {
		if err := storageRoot.Remove(tempPath); err != nil {
			return fmt.Errorf("delete attachment partial: %w", err)
		}
	}
	return nil
}

func (s *LocalAttachmentStorage) openRoot() (*os.Root, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return nil, ErrAttachmentStorageMissing
	}
	storageRoot, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("open attachment root: %w", err)
	}
	return storageRoot, nil
}

func normalizeLocalAttachmentKey(key string) (string, error) {
	if key == "" || key != strings.TrimSpace(key) ||
		strings.ContainsRune(key, '\x00') ||
		strings.ContainsRune(key, '\\') ||
		strings.HasPrefix(key, "/") ||
		filepath.IsAbs(filepath.FromSlash(key)) ||
		hasWindowsAttachmentVolumePrefix(key) {
		return "", ErrInvalidAttachmentName
	}
	components := strings.Split(key, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", ErrInvalidAttachmentName
		}
	}
	normalized := strings.Join(components, "/")
	if path.Clean(normalized) != normalized {
		return "", ErrInvalidAttachmentName
	}
	return normalized, nil
}

func hasWindowsAttachmentVolumePrefix(key string) bool {
	if len(key) < 2 || key[1] != ':' {
		return false
	}
	first := key[0]
	return first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z'
}

// verifyLocalAttachmentParents rejects every existing symlink component. The
// subsequent os.Root operation remains the containment boundary if a
// service-local filesystem race occurs after this check.
func verifyLocalAttachmentParents(
	storageRoot *os.Root,
	normalizedKey string,
	allowMissing bool,
) (bool, error) {
	components := strings.Split(normalizedKey, "/")
	for index := 1; index < len(components); index++ {
		parentPath := filepath.FromSlash(
			strings.Join(components[:index], "/"),
		)
		info, err := storageRoot.Lstat(parentPath)
		if errors.Is(err, os.ErrNotExist) && allowMissing {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf(
				"inspect attachment directory: %w",
				err,
			)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, ErrInvalidAttachmentName
		}
	}
	return true, nil
}

func inspectLocalAttachmentRegularFile(
	storageRoot *os.Root,
	relativePath string,
	allowMissing bool,
) (bool, error) {
	info, err := storageRoot.Lstat(relativePath)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect attachment object: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, ErrInvalidAttachmentName
	}
	return true, nil
}

func removeLocalAttachmentRegularFileIfExists(
	storageRoot *os.Root,
	relativePath string,
) error {
	exists, err := inspectLocalAttachmentRegularFile(
		storageRoot,
		relativePath,
		true,
	)
	if err != nil || !exists {
		return err
	}
	return storageRoot.Remove(relativePath)
}

func validAttachmentStagingKey(key string) bool {
	key = filepath.ToSlash(strings.TrimSpace(key))
	if !strings.HasPrefix(key, ".staging/") {
		return false
	}
	name := strings.TrimPrefix(key, ".staging/")
	return name != "" &&
		name == filepath.Base(name) &&
		!strings.ContainsAny(name, `/\\`) &&
		!strings.Contains(name, "..")
}

type NativeAttachmentInput struct {
	TicketID                             uint
	CommentID                            *uint
	ExpectedVersion                      uint64
	LeaseID                              string
	Actor                                models.ActorRef
	CredentialID                         string
	PolicyDecisionID                     string
	ExternalNotificationPolicyDecisionID string
	SourceProtocol                       string
	RequestDigest                        string
	OriginalName                         string
	ContentType                          string
	FileType                             models.AttachmentType
	Description                          string
	IsPublic                             bool
	Reader                               io.Reader
	TraceID                              string
	CorrelationID                        string
	IdempotencyRecordID                  string
	OutboxTargets                        []OutboxTarget
}

type NativeAttachmentResult struct {
	Attachment *models.TicketAttachment `json:"attachment"`
	Event      *models.DomainEvent      `json:"event"`
	Receipt    OperationReceipt         `json:"receipt"`
}

func (s *AgentNativeService) StoreAttachment(
	ctx context.Context,
	input NativeAttachmentInput,
) (*NativeAttachmentResult, error) {
	if s == nil {
		return nil, ErrAttachmentStorageMissing
	}
	operation, err := commandOperationContext(ctx, input.Actor)
	if err != nil {
		return nil, err
	}
	projectScope := operation.Scope
	if input.Actor.Type == models.ActorTypeServicePrincipal {
		if strings.TrimSpace(input.CredentialID) == "" {
			input.CredentialID = operation.CredentialID
		} else if input.CredentialID != operation.CredentialID {
			return nil, ErrInvalidCredential
		}
	}
	if s.attachmentStorage == nil || s.attachmentStaging == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if err := input.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidActor, err)
	}
	input.LeaseID = strings.TrimSpace(input.LeaseID)
	if input.Actor.Type == models.ActorTypeServicePrincipal && input.LeaseID == "" {
		return nil, fmt.Errorf("%w: service principal attachments require a ticket lease", ErrLeaseConflict)
	}
	if input.ExpectedVersion == 0 {
		return nil, fmt.Errorf("%w: expected version is required", ErrVersionConflict)
	}
	safeName, err := SafeAttachmentName(input.OriginalName)
	if err != nil {
		return nil, err
	}
	if err := s.PrepareAttachmentUploadAuthorization(
		ctx,
		&input,
	); err != nil {
		return nil, err
	}
	initialAccess, err := s.captureAttachmentAuthorization(
		ctx,
		models.ScopeAttachmentsWrite,
	)
	if err != nil {
		return nil, err
	}
	policyCheck := attachmentUploadPolicyCheck(
		operation,
		input,
		safeName,
	)
	policyDecisionID := strings.TrimSpace(input.PolicyDecisionID)
	extension := safeAttachmentExtension(safeName)
	storageName := newNativeID() + extension
	stagingKey := ".staging/" + storageName
	attachment := &models.TicketAttachment{
		TicketID:           input.TicketID,
		CommentID:          input.CommentID,
		ActorType:          input.Actor.Type,
		ActorID:            input.Actor.ID,
		ServicePrincipalID: actorServicePrincipalID(input.Actor),
		FileName:           storageName,
		OriginalName:       safeName,
		FileType:           models.AttachmentTypeOther,
		Extension:          extension,
		StoragePath:        stagingKey,
		StorageType:        attachmentStagingIntentStorageType,
		IsPublic:           input.IsPublic,
		VirusScan:          models.VirusScanPending,
		Description:        truncateText(input.Description, 4000),
	}
	if err := s.registerAttachmentStagingIntent(
		ctx,
		operation,
		input,
		initialAccess,
		policyCheck,
		attachment,
	); err != nil {
		return nil, err
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"attachment inbound staging",
	); err != nil {
		return nil, err
	}
	staged, err := s.attachmentStaging.Stage(
		ctx,
		stagingKey,
		input.Reader,
		s.attachmentMaxBytes,
	)
	if err != nil {
		return nil, err
	}
	removeOnFailure := true
	defer func() {
		if removeOnFailure {
			_ = s.attachmentStaging.DeleteStaged(
				context.Background(),
				staged.Key,
			)
		}
	}()
	if staged.Size == 0 {
		return nil, ErrInvalidAttachment
	}
	contentType := "application/octet-stream"
	if parsedType, _, parseErr := mime.ParseMediaType(
		strings.TrimSpace(staged.DetectedContentType),
	); parseErr == nil && parsedType != "" {
		contentType = strings.ToLower(parsedType)
	}
	fileType := attachmentTypeForMIME(contentType)
	attachment.FileSize = staged.Size
	attachment.MimeType = contentType
	attachment.FileType = fileType
	attachment.StoragePath = staged.Key
	attachment.StorageType = "staging"
	attachment.Hash = staged.SHA256
	uploadMigration, err := newAttachmentUploadMigrationIntent(
		*attachment,
		s.attachmentStorage,
	)
	if err != nil {
		return nil, err
	}
	var event *models.DomainEvent
	var resourceVersion uint64
	var receipt OperationReceipt
	var allowExternalNotifications bool
	var humanUserID *uint
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		projectScope,
		func(scopedContext context.Context) error {
			currentAccess, revalidateErr :=
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
					models.ScopeAttachmentsWrite,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if !initialAccess.AuthorizationSnapshot.Matches(
				currentAccess.AuthorizationSnapshot,
			) {
				return ErrProjectAccessDenied
			}
			allowExternalNotifications, revalidateErr =
				s.validateAttachmentPolicyDecisionsInTransaction(
					scopedContext,
					operation,
					input,
					policyCheck,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			humanUserID, revalidateErr = s.humanUserProjection(
				scopedContext,
				input.Actor,
			)
			if revalidateErr != nil {
				return revalidateErr
			}
			if (attachment.UploadedBy == nil) !=
				(humanUserID == nil) ||
				(attachment.UploadedBy != nil &&
					humanUserID != nil &&
					*attachment.UploadedBy != *humanUserID) {
				return ErrProjectAccessDenied
			}
			return transactionForContext(
				scopedContext,
				s.db,
				func(tx *gorm.DB) error {
					if _, err := s.validateAttachmentCommandStateTx(
						scopedContext,
						tx,
						currentAccess,
						operation,
						input,
					); err != nil {
						return err
					}
					var persistedIntent models.TicketAttachment
					if err := tx.
						Clauses(clause.Locking{
							Strength: "UPDATE",
						}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ? AND storage_type = ?",
							attachment.ID,
							projectScope.OrganizationID,
							projectScope.ProjectID,
							attachmentStagingIntentStorageType,
						).
						Take(&persistedIntent).Error; err != nil {
						return fmt.Errorf(
							"lock attachment staging intent: %w",
							err,
						)
					}
					if persistedIntent.TicketID !=
						attachment.TicketID ||
						persistedIntent.ActorType !=
							attachment.ActorType ||
						persistedIntent.ActorID !=
							attachment.ActorID ||
						persistedIntent.FileName !=
							attachment.FileName ||
						persistedIntent.StoragePath !=
							attachment.StoragePath {
						return ErrInvalidAttachment
					}
					now := s.now().UTC()
					updateIntent := tx.Model(
						&models.TicketAttachment{},
					).Where(
						"id = ? AND storage_type = ?",
						attachment.ID,
						attachmentStagingIntentStorageType,
					).Updates(map[string]any{
						"file_size":    attachment.FileSize,
						"mime_type":    attachment.MimeType,
						"file_type":    attachment.FileType,
						"storage_type": attachment.StorageType,
						"hash":         attachment.Hash,
						"updated_at":   now,
					})
					if updateIntent.Error != nil {
						return updateIntent.Error
					}
					if updateIntent.RowsAffected != 1 {
						return ErrInvalidAttachment
					}
					attachment.UpdatedAt = now
					if err := completeAttachmentStagingCleanupIntentTx(
						tx,
						attachment.ID,
						now,
					); err != nil {
						return err
					}
					resourceVersion = input.ExpectedVersion + 1
					update := tx.Model(&models.Ticket{}).
						Where(
							"id = ? AND organization_id = ? AND project_id = ? AND version = ?",
							input.TicketID,
							projectScope.OrganizationID,
							projectScope.ProjectID,
							input.ExpectedVersion,
						).
						Updates(map[string]any{
							"version":    resourceVersion,
							"updated_at": now,
						})
					if update.Error != nil {
						return update.Error
					}
					if update.RowsAffected != 1 {
						return ErrVersionConflict
					}
					history := &models.TicketHistory{
						TicketID:           input.TicketID,
						UserID:             humanUserID,
						ActorType:          input.Actor.Type,
						ActorID:            input.Actor.ID,
						ServicePrincipalID: actorServicePrincipalID(input.Actor),
						Action:             models.HistoryActionAttachment,
						Description:        "添加了附件",
						AttachmentID:       &attachment.ID,
						IsVisible:          input.IsPublic,
						IsSystem:           input.Actor.Type == models.ActorTypeSystem,
						IsAutomated:        input.Actor.Type != models.ActorTypeHuman,
						Metadata:           policyMetadata(policyDecisionID),
					}
					var appendErr error
					uploadTargets := append(
						[]OutboxTarget(nil),
						input.OutboxTargets...,
					)
					uploadTargets = append(uploadTargets, OutboxTarget{
						Type: AttachmentUploadOutboxDestination,
						ID: strconv.FormatUint(
							uint64(attachment.ID),
							10,
						),
						MaxAttempts: 8,
					})
					event, appendErr = s.AppendDomainEventWithAdditionalTargetsTx(scopedContext, tx, DomainEventInput{
						Type:                       "io.chronodesk.ticket.attachment.upload-requested.v1",
						Subject:                    fmt.Sprintf("ticket/%d", input.TicketID),
						Actor:                      input.Actor,
						ResourceVersion:            resourceVersion,
						AllowExternalNotifications: allowExternalNotifications,
						TraceID:                    input.TraceID,
						CorrelationID:              input.CorrelationID,
						Data: map[string]any{
							"ticket_id":                        input.TicketID,
							"attachment_id":                    attachment.ID,
							"file_name":                        safeName,
							"file_size":                        attachment.FileSize,
							"sha256":                           attachment.Hash,
							"virus_scan":                       attachment.VirusScan,
							"storage_state":                    attachment.StorageType,
							"content_untrusted":                true,
							AttachmentUploadMigrationDataField: uploadMigration,
						},
					}, uploadTargets)
					if appendErr != nil {
						return appendErr
					}
					if err := linkTicketHistoryToDomainEvent(history, event); err != nil {
						return err
					}
					if err := tx.Create(history).Error; err != nil {
						return err
					}
					if !input.IsPublic {
						if err := tx.Model(history).UpdateColumn("is_visible", false).Error; err != nil {
							return err
						}
					}
					if input.LeaseID != "" {
						if err := tx.Model(&models.TicketLease{}).
							Where(
								"id = ? AND organization_id = ? AND project_id = ?",
								input.LeaseID,
								projectScope.OrganizationID,
								projectScope.ProjectID,
							).
							Update("ticket_version", resourceVersion).Error; err != nil {
							return err
						}
					}
					receipt = OperationReceipt{
						OperationID:      newNativeID(),
						ResourceID:       strconv.FormatUint(uint64(attachment.ID), 10),
						ResourceVersion:  resourceVersion,
						EventID:          event.ID,
						ChangedFields:    []string{"attachments"},
						PolicyDecisionID: policyDecisionID,
					}
					if input.IdempotencyRecordID != "" {
						if err := s.CompleteIdempotencyTx(
							ctx,
							tx,
							input.IdempotencyRecordID,
							http.StatusAccepted,
							receipt,
							receipt.ResourceID,
							event.ID,
						); err != nil {
							return err
						}
						if err := s.storeIdempotencySnapshotTx(
							ctx,
							tx,
							input.IdempotencyRecordID,
							attachment,
						); err != nil {
							return err
						}
					}
					return nil
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	removeOnFailure = false
	return &NativeAttachmentResult{
		Attachment: attachment,
		Event:      event,
		Receipt:    receipt,
	}, nil
}

func (s *AgentNativeService) OpenAttachment(
	ctx context.Context,
	attachmentID uint,
) (*models.TicketAttachment, io.ReadCloser, error) {
	return s.openAttachmentWithRevalidation(
		ctx,
		0,
		attachmentID,
	)
}

func (s *AgentNativeService) OpenTicketAttachment(
	ctx context.Context,
	ticketID uint,
	attachmentID uint,
) (*models.TicketAttachment, io.ReadCloser, error) {
	if ticketID == 0 {
		return nil, nil, ErrInvalidAttachment
	}
	return s.openAttachmentWithRevalidation(
		ctx,
		ticketID,
		attachmentID,
	)
}

func (s *AgentNativeService) MarkAttachmentScan(
	ctx context.Context,
	attachmentID uint,
	status models.VirusScanStatus,
	details string,
) error {
	projectScope, err := commandProjectScope(ctx)
	if err != nil {
		return err
	}
	if status != models.VirusScanClean && status != models.VirusScanInfected && status != models.VirusScanError {
		return fmt.Errorf("invalid terminal virus scan status %q", status)
	}
	now := s.now()
	result := s.dbForContext(ctx).Model(&models.TicketAttachment{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			attachmentID,
			projectScope.OrganizationID,
			projectScope.ProjectID,
		).
		Updates(map[string]any{
			"virus_scan":   status,
			"scan_details": truncateText(details, 4000),
			"scanned_at":   now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// SafeAttachmentName strips path components and control characters before a
// filename is ever persisted or reflected in a download header.
func SafeAttachmentName(name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return "", ErrInvalidAttachmentName
	}
	var builder strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r), r == '/', r == '\\':
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	result := strings.TrimSpace(builder.String())
	if result == "" {
		return "", ErrInvalidAttachmentName
	}
	runes := []rune(result)
	if len(runes) > 200 {
		result = string(runes[:200])
	}
	return result, nil
}

func safeAttachmentExtension(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	if len(extension) > 20 {
		return ""
	}
	for _, r := range extension {
		if r == '.' || r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return ""
	}
	return extension
}

func attachmentTypeForMIME(contentType string) models.AttachmentType {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return models.AttachmentTypeImage
	case strings.HasPrefix(contentType, "video/"):
		return models.AttachmentTypeVideo
	case strings.HasPrefix(contentType, "audio/"):
		return models.AttachmentTypeAudio
	case contentType == "application/zip" || contentType == "application/x-tar" ||
		contentType == "application/gzip" || contentType == "application/x-7z-compressed":
		return models.AttachmentTypeArchive
	case strings.HasPrefix(contentType, "text/") || strings.HasPrefix(contentType, "application/pdf") ||
		strings.Contains(contentType, "document") || strings.Contains(contentType, "sheet"):
		return models.AttachmentTypeDocument
	default:
		return models.AttachmentTypeOther
	}
}

func (s *AgentNativeService) humanUserProjection(
	ctx context.Context,
	actor models.ActorRef,
) (*uint, error) {
	return s.humanUserProjectionTx(s.db.WithContext(ctx), actor)
}

func (s *AgentNativeService) humanUserProjectionTx(
	tx *gorm.DB,
	actor models.ActorRef,
) (*uint, error) {
	switch actor.Type {
	case models.ActorTypeHuman:
		parsed, err := safeconv.ParsePositiveUint(actor.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: human actor id must be a user id", ErrInvalidActor)
		}
		var count int64
		if err := tx.Unscoped().Model(&models.User{}).
			Where("id = ?", parsed).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("validate human actor projection: %w", err)
		}
		if count != 1 {
			return nil, fmt.Errorf("%w: human actor user %d does not exist", ErrInvalidActor, parsed)
		}
		return &parsed, nil
	case models.ActorTypeServicePrincipal, models.ActorTypeSystem:
		return nil, nil
	default:
		return nil, ErrInvalidActor
	}
}

func setTicketActorFields(ticket *models.Ticket, creator models.ActorRef, assigned *models.ActorRef) {
	ticket.CreatedByActorType = creator.Type
	ticket.CreatedByActorID = creator.ID
	ticket.CreatedByServicePrincipalID = actorServicePrincipalID(creator)
	if assigned == nil {
		return
	}
	ticket.AssignedToActorType = assigned.Type
	ticket.AssignedToActorID = assigned.ID
	ticket.AssignedToServicePrincipalID = actorServicePrincipalID(*assigned)
}

func actorServicePrincipalID(actor models.ActorRef) *string {
	if actor.Type != models.ActorTypeServicePrincipal {
		return nil
	}
	id := actor.ID
	return &id
}

func policyMetadata(policyDecisionID string) string {
	if policyDecisionID == "" {
		return ""
	}
	encoded, _ := json.Marshal(map[string]string{"policy_decision_id": policyDecisionID})
	return string(encoded)
}

var allowedTicketChangeFields = map[string]struct{}{
	"title":                            {},
	"description":                      {},
	"type":                             {},
	"priority":                         {},
	"status":                           {},
	"source":                           {},
	"assigned_to_id":                   {},
	"assigned_to_actor_type":           {},
	"assigned_to_actor_id":             {},
	"assigned_to_service_principal_id": {},
	"category_id":                      {},
	"subcategory_id":                   {},
	"tags":                             {},
	"due_date":                         {},
	"customer_email":                   {},
	"customer_phone":                   {},
	"customer_name":                    {},
	"internal_notes":                   {},
	"rating":                           {},
	"rating_comment":                   {},
	"custom_fields":                    {},
	"is_escalated":                     {},
	"sla_breached":                     {},
	"sla_due_date":                     {},
	"agent_context":                    {},
	"trust_level":                      {},
}

func sanitizeTicketChanges(input map[string]any) (map[string]any, []string, error) {
	changes := make(map[string]any, len(input))
	fields := make([]string, 0, len(input))
	for rawField, value := range input {
		field := strings.ToLower(strings.TrimSpace(rawField))
		if _, ok := allowedTicketChangeFields[field]; !ok {
			return nil, nil, fmt.Errorf("ticket field %q is not writable", rawField)
		}
		switch field {
		case "sla_breached":
			if _, ok := value.(bool); !ok {
				return nil, nil, fmt.Errorf("sla_breached must be a boolean")
			}
		case "sla_due_date":
			switch value.(type) {
			case nil, time.Time, *time.Time:
			default:
				return nil, nil, fmt.Errorf("sla_due_date must be a time value or null")
			}
		case "agent_context":
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, nil, fmt.Errorf(
					"%w: encode agent_context: %v",
					ErrInvalidAgentContext,
					err,
				)
			}
			var contextValue models.AgentContext
			if err := json.Unmarshal(encoded, &contextValue); err != nil {
				return nil, nil, fmt.Errorf(
					"%w: decode agent_context: %v",
					ErrInvalidAgentContext,
					err,
				)
			}
			if err := validateAgentContext(&contextValue); err != nil {
				return nil, nil, err
			}
			value = datatypes.NewJSONType(contextValue)
		case "custom_fields":
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, nil, fmt.Errorf("encode custom_fields: %w", err)
			}
			var customFields map[string]any
			if err := json.Unmarshal(encoded, &customFields); err != nil {
				return nil, nil, fmt.Errorf("decode custom_fields: %w", err)
			}
			value = datatypes.NewJSONType(customFields)
		case "tags":
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, nil, fmt.Errorf("encode tags: %w", err)
			}
			var tags []string
			if err := json.Unmarshal(encoded, &tags); err != nil {
				return nil, nil, fmt.Errorf("decode tags: %w", err)
			}
			normalizedTags, err := normalizeTicketTags(tags)
			if err != nil {
				return nil, nil, err
			}
			value = datatypes.NewJSONSlice([]string(normalizedTags))
		case "category_id", "subcategory_id":
			normalizedCategoryID, err :=
				normalizeTicketCategoryChange(value)
			if err != nil {
				return nil, nil, err
			}
			value = normalizedCategoryID
		}
		changes[field] = value
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return changes, fields, nil
}

func ticketChangeAuthorizationContract(
	fields []string,
	requiredScope string,
	action string,
) (scope string, canonicalAction string, risky bool, err error) {
	hasTransition := false
	hasAssignment := false
	hasOrdinaryUpdate := false
	for _, field := range fields {
		switch field {
		case "status":
			hasTransition = true
		case "assigned_to_id",
			"assigned_to_actor_type",
			"assigned_to_actor_id",
			"assigned_to_service_principal_id":
			hasAssignment = true
		default:
			hasOrdinaryUpdate = true
		}
	}
	categoryCount := 0
	for _, present := range []bool{hasTransition, hasAssignment, hasOrdinaryUpdate} {
		if present {
			categoryCount++
		}
	}
	if categoryCount != 1 {
		return "", "", false, fmt.Errorf(
			"%w: transition, assignment and ordinary update fields cannot be mixed",
			ErrCommandScopeMismatch,
		)
	}

	switch {
	case hasTransition:
		scope, canonicalAction, risky = models.ScopeTicketsTransition, "ticket.transition", true
	case hasAssignment:
		scope, canonicalAction, risky = models.ScopeTicketsAssign, "ticket.assign", true
	default:
		if requiredScope == models.ScopeTicketsTransition && action == "ticket.escalate" &&
			fieldsOnly(fields, "is_escalated", "priority") {
			return models.ScopeTicketsTransition, "ticket.escalate", true, nil
		}
		scope, canonicalAction = models.ScopeTicketsUpdate, "ticket.update"
	}
	if requiredScope == "" && scope == models.ScopeTicketsUpdate {
		requiredScope = scope
	}
	if requiredScope != scope {
		return "", "", false, fmt.Errorf(
			"%w: fields require %s, got %s",
			ErrCommandScopeMismatch,
			scope,
			requiredScope,
		)
	}
	if action == "" {
		action = canonicalAction
	}
	if action != canonicalAction {
		return "", "", false, fmt.Errorf(
			"%w: fields require action %s, got %s",
			ErrCommandScopeMismatch,
			canonicalAction,
			action,
		)
	}
	return scope, canonicalAction, risky, nil
}

func fieldsOnly(fields []string, allowed ...string) bool {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		allowSet[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := allowSet[field]; !ok {
			return false
		}
	}
	return len(fields) > 0
}

func fieldsContain(fields []string, expected string) bool {
	for _, field := range fields {
		if field == expected {
			return true
		}
	}
	return false
}

func assignmentFieldsPresent(fields []string) bool {
	for _, field := range fields {
		switch field {
		case "assigned_to_id",
			"assigned_to_actor_type",
			"assigned_to_actor_id",
			"assigned_to_service_principal_id":
			return true
		}
	}
	return false
}

func ticketFieldValue(ticket *models.Ticket, field string) any {
	switch field {
	case "title":
		return ticket.Title
	case "description":
		return ticket.Description
	case "type":
		return ticket.Type
	case "priority":
		return ticket.Priority
	case "status":
		return ticket.Status
	case "source":
		return ticket.Source
	case "assigned_to_id":
		return ticket.AssignedToID
	case "assigned_to_actor_type":
		return ticket.AssignedToActorType
	case "assigned_to_actor_id":
		return ticket.AssignedToActorID
	case "assigned_to_service_principal_id":
		return ticket.AssignedToServicePrincipalID
	case "category_id":
		return ticket.CategoryID
	case "subcategory_id":
		return ticket.SubcategoryID
	case "tags":
		return ticket.Tags
	case "due_date":
		return ticket.DueDate
	case "customer_email":
		return ticket.CustomerEmail
	case "customer_phone":
		return ticket.CustomerPhone
	case "customer_name":
		return ticket.CustomerName
	case "internal_notes":
		return ticket.InternalNotes
	case "rating":
		return ticket.Rating
	case "rating_comment":
		return ticket.RatingComment
	case "custom_fields":
		return ticket.CustomFields.Data()
	case "is_escalated":
		return ticket.IsEscalated
	case "sla_breached":
		return ticket.SLABreached
	case "sla_due_date":
		return ticket.SLADueDate
	case "agent_context":
		return ticket.AgentContext.Data()
	case "trust_level":
		return ticket.TrustLevel
	default:
		return nil
	}
}

func validateTicketChangeSemantics(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	ticket *models.Ticket,
	changes map[string]any,
	actor models.ActorRef,
) error {
	categoryID := ticket.CategoryID
	subcategoryID := ticket.SubcategoryID
	if value, ok := changes["category_id"]; ok {
		var err error
		categoryID, err = optionalTicketCategoryID(value)
		if err != nil {
			return err
		}
	}
	if value, ok := changes["subcategory_id"]; ok {
		var err error
		subcategoryID, err = optionalTicketCategoryID(value)
		if err != nil {
			return err
		}
	}
	if _, categoryChanged := changes["category_id"]; categoryChanged {
		if err := validateTicketCategorySelectionTx(
			ctx,
			tx,
			scope,
			categoryID,
			subcategoryID,
		); err != nil {
			return err
		}
	} else if _, subcategoryChanged := changes["subcategory_id"]; subcategoryChanged {
		if err := validateTicketCategorySelectionTx(
			ctx,
			tx,
			scope,
			categoryID,
			subcategoryID,
		); err != nil {
			return err
		}
	}
	if value, ok := changes["status"]; ok {
		status := models.TicketStatus(fmt.Sprint(value))
		if err := validateTicketWorkflowTransitionTx(
			ctx,
			tx,
			scope,
			ticket,
			status,
			actor,
		); err != nil {
			return err
		}
	}
	if value, ok := changes["priority"]; ok {
		priority := models.TicketPriority(fmt.Sprint(value))
		if !priority.IsValid() {
			return fmt.Errorf("invalid ticket priority %q", value)
		}
	}
	if value, ok := changes["type"]; ok {
		ticketType := models.TicketType(fmt.Sprint(value))
		if !ticketType.IsValid() {
			return fmt.Errorf("invalid ticket type %q", value)
		}
	}
	if value, ok := changes["source"]; ok {
		source := models.TicketSource(fmt.Sprint(value))
		if !source.IsValid() {
			return fmt.Errorf("invalid ticket source %q", value)
		}
	}
	if value, ok := changes["trust_level"]; ok {
		level := models.TicketTrustLevel(fmt.Sprint(value))
		if !validTrustLevel(level) {
			return fmt.Errorf("invalid ticket trust level %q", value)
		}
	}
	return nil
}

func validTrustLevel(level models.TicketTrustLevel) bool {
	switch level {
	case models.TicketTrustLevelUntrusted,
		models.TicketTrustLevelVerified,
		models.TicketTrustLevelTrusted,
		models.TicketTrustLevelSystem:
		return true
	default:
		return false
	}
}

func historyActionForChanges(fields []string) models.HistoryAction {
	for _, field := range fields {
		switch field {
		case "status":
			return models.HistoryActionStatusChange
		case "priority":
			return models.HistoryActionPriorityChange
		case "assigned_to_id", "assigned_to_actor_id", "assigned_to_service_principal_id":
			return models.HistoryActionAssign
		}
	}
	return models.HistoryActionUpdate
}

func newNativeID() string {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		// crypto/rand failure is unrecoverable for identity generation; mixing a
		// timestamp still avoids returning an empty primary key.
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		copy(value[:], sum[:16])
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}

func newAgentTicketNumber(now time.Time) string {
	id := strings.ReplaceAll(newNativeID(), "-", "")
	return fmt.Sprintf("AI-%s-%s", now.UTC().Format("20060102"), strings.ToUpper(id[:10]))
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	// Production enables GORM's TranslateError option, so PostgreSQL 23505 is
	// normalized to gorm.ErrDuplicatedKey before it reaches the domain layer.
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Code == "23505" &&
			postgresError.ConstraintName == "idx_idempotency_actor_operation_key"
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed: idempotency_records.") &&
		strings.Contains(message, "idempotency_records.actor_type") &&
		strings.Contains(message, "idempotency_records.actor_id") &&
		strings.Contains(message, "idempotency_records.operation") &&
		strings.Contains(message, "idempotency_records.key")
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func truncateText(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
