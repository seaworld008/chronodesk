package agentplatform

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gongdan-system/internal/agentauth"
	"gongdan-system/internal/models"
	"gongdan-system/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	agentReadOnlyConfigKey  = "agent.global_read_only"
	agentEmergencyConfigKey = "agent.emergency_stop"

	adminIdempotencyRetention  = 24 * time.Hour
	adminReplaySchema          = "chronodesk.admin-replay.v1"
	adminVersionKeyPrefix      = "agent.resource_version."
	adminRequestBodyContextKey = "chronodesk.admin.canonical_request_body"
	adminMaximumRequestBytes   = 1 << 20
)

type RuntimeControl struct {
	native    *services.AgentNativeService
	db        *gorm.DB
	readOnly  atomic.Bool
	emergency atomic.Bool
}

func NewRuntimeControl(native *services.AgentNativeService, readOnly bool, databases ...*gorm.DB) *RuntimeControl {
	control := &RuntimeControl{native: native}
	if len(databases) > 0 {
		control.db = databases[0]
	}
	control.SetReadOnly(readOnly)
	_ = control.Refresh(context.Background())
	return control
}

func (c *RuntimeControl) SetReadOnly(enabled bool) {
	c.readOnly.Store(enabled)
	if c.native != nil {
		c.native.SetGlobalReadOnly(enabled)
	}
}

func (c *RuntimeControl) ReadOnly() bool {
	return c != nil && c.readOnly.Load()
}

func (c *RuntimeControl) SetEmergencyStop(enabled bool) {
	c.emergency.Store(enabled)
	if c.native != nil {
		c.native.SetGlobalEmergencyStop(enabled)
	}
}

func (c *RuntimeControl) EmergencyStop() bool {
	return c != nil && c.emergency.Load()
}

func (c *RuntimeControl) PersistReadOnly(ctx context.Context, enabled bool, updatedBy uint) error {
	if err := c.persistOnDB(ctx, c.db, agentReadOnlyConfigKey, enabled, updatedBy); err != nil {
		return err
	}
	c.SetReadOnly(enabled)
	return nil
}

func (c *RuntimeControl) PersistEmergencyStop(ctx context.Context, enabled bool, updatedBy uint) error {
	if err := c.persistOnDB(ctx, c.db, agentEmergencyConfigKey, enabled, updatedBy); err != nil {
		return err
	}
	c.SetEmergencyStop(enabled)
	return nil
}

func (c *RuntimeControl) persistTx(
	ctx context.Context,
	tx *gorm.DB,
	key string,
	enabled bool,
	updatedBy uint,
) error {
	return c.persistOnDB(ctx, tx, key, enabled, updatedBy)
}

func (c *RuntimeControl) persistOnDB(
	ctx context.Context,
	db *gorm.DB,
	key string,
	enabled bool,
	updatedBy uint,
) error {
	if c == nil || db == nil {
		return nil
	}
	value := strconv.FormatBool(enabled)
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	row := models.SystemConfig{
		Key: key, Value: value, ValueType: "bool",
		Description: "Agent-native runtime safety control",
		Category:    "security", Group: "agent", IsActive: true,
		DefaultValue: "false", UpdatedBy: userID, Version: 1,
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"value":       value,
			"value_type":  "bool",
			"is_active":   true,
			"updated_by":  userID,
			"version":     gorm.Expr("system_configs.version + 1"),
			"updated_at":  time.Now().UTC(),
			"description": row.Description,
			"category":    row.Category,
			"group":       row.Group,
		}),
	}).Create(&row).Error
}

func (c *RuntimeControl) Refresh(ctx context.Context) error {
	if c == nil || c.db == nil {
		return nil
	}
	var rows []models.SystemConfig
	if err := c.db.WithContext(ctx).
		Where("key IN ? AND is_active = ?", []string{agentReadOnlyConfigKey, agentEmergencyConfigKey}, true).
		Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		enabled := rows[i].GetBoolValue()
		switch rows[i].Key {
		case agentReadOnlyConfigKey:
			c.SetReadOnly(enabled)
		case agentEmergencyConfigKey:
			c.SetEmergencyStop(enabled)
		}
	}
	return nil
}

func (c *RuntimeControl) Run(ctx context.Context, interval time.Duration) {
	if c == nil || c.db == nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.Refresh(ctx)
		}
	}
}

type CredentialStore struct {
	native *services.AgentNativeService
}

func NewCredentialStore(native *services.AgentNativeService) *CredentialStore {
	return &CredentialStore{native: native}
}

func (s *CredentialStore) AuthenticateClient(
	ctx context.Context,
	clientID string,
	clientSecret string,
) (*agentauth.Principal, error) {
	if s == nil || s.native == nil {
		return nil, services.ErrInvalidCredential
	}
	principal, credential, err := s.native.ValidateCredentialToken(ctx, clientSecret)
	if err != nil {
		return nil, err
	}
	if clientID != principal.ID {
		return nil, services.ErrInvalidCredential
	}
	return &agentauth.Principal{
		ID:           principal.ID,
		CredentialID: credential.ID,
		ClientID:     principal.ID,
		Name:         principal.Name,
		Scopes:       principal.ScopeList(),
		Active:       principal.Status == models.ServicePrincipalStatusActive && !principal.EmergencyDisabled,
		ExpiresAt:    principal.ExpiresAt,
	}, nil
}

// ValidateCredentialToken already updates usage timestamps atomically.
func (s *CredentialStore) TouchCredential(context.Context, string, time.Time) error {
	return nil
}

func (s *CredentialStore) ValidateAccessContext(
	ctx context.Context,
	principalID string,
	credentialID string,
) error {
	if s == nil || s.native == nil {
		return services.ErrInvalidCredential
	}
	return s.native.ValidateCredentialReference(ctx, principalID, credentialID)
}

type AdminHandler struct {
	db                  *gorm.DB
	native              *services.AgentNativeService
	control             *RuntimeControl
	credentialTTL       time.Duration
	compatibilityUserID uint
	replayCipher        cipher.AEAD
}

func NewAdminHandler(
	db *gorm.DB,
	native *services.AgentNativeService,
	control *RuntimeControl,
	credentialTTL time.Duration,
	compatibilityUserID uint,
	replayEncryptionKey ...[]byte,
) *AdminHandler {
	if credentialTTL <= 0 {
		credentialTTL = 90 * 24 * time.Hour
	}
	handler := &AdminHandler{
		db:                  db,
		native:              native,
		control:             control,
		credentialTTL:       credentialTTL,
		compatibilityUserID: compatibilityUserID,
	}
	if len(replayEncryptionKey) > 0 && len(replayEncryptionKey[0]) > 0 {
		handler.replayCipher = newAdminReplayCipher(replayEncryptionKey[0])
	}
	return handler
}

func (h *AdminHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.Use(h.requireAdminCommandHeaders)
	group.GET("/agent-control/overview", h.Overview)
	group.PUT("/agent-control/read-only", h.SetReadOnly)
	group.PUT("/agent-control/emergency-stop", h.SetEmergencyStop)
	group.POST("/service-principals", h.CreateServicePrincipal)
	group.PUT("/service-principals/:id/status", h.SetServicePrincipalStatus)
	group.POST("/service-principals/:id/credentials/rotate", h.RotateCredential)
	group.DELETE("/service-principals/:id/credentials/:credential_id", h.RevokeCredential)
	group.GET("/service-principals/:id/policies", h.ListPolicies)
	group.POST("/service-principals/:id/policies", h.CreatePolicy)
	group.DELETE("/service-principals/:id/policies/:policy_id", h.DisablePolicy)
	group.POST("/leases/:id/force-release", h.ForceReleaseLease)
	group.POST("/attachments/:id/scan", h.MarkAttachmentScan)
	group.POST("/outbox/:id/replay", h.ReplayOutbox)
}

func (h *AdminHandler) requireAdminCommandHeaders(c *gin.Context) {
	if c.Request.Method == http.MethodGet ||
		c.Request.Method == http.MethodHead ||
		c.Request.Method == http.MethodOptions {
		c.Next()
		return
	}
	if _, ok := RequireIdempotencyKey(c); !ok {
		c.Abort()
		return
	}
	isTopLevelCreate := c.Request.Method == http.MethodPost &&
		strings.HasSuffix(c.FullPath(), "/service-principals")
	if !isTopLevelCreate {
		rawIfMatch := strings.TrimSpace(c.GetHeader("If-Match"))
		if rawIfMatch == "" {
			WriteProblem(
				c,
				http.StatusPreconditionRequired,
				"precondition_required",
				"If-Match is required for this administrator command",
				false,
			)
			c.Abort()
			return
		}
		if _, err := ParseIfMatch(rawIfMatch); err != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
			c.Abort()
			return
		}
	}
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, adminMaximumRequestBytes+1))
	if err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Failed to read administrator command body", false)
		c.Abort()
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
	if len(rawBody) > adminMaximumRequestBytes {
		WriteProblem(c, http.StatusRequestEntityTooLarge, ProblemInvalidRequest, "Administrator command body is too large", false)
		c.Abort()
		return
	}
	c.Set(adminRequestBodyContextKey, canonicalAdminRequestBody(rawBody))
	c.Next()
}

func canonicalAdminRequestBody(rawBody []byte) []byte {
	trimmed := bytes.TrimSpace(rawBody)
	if len(trimmed) == 0 {
		return []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return append([]byte(nil), trimmed...)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return append([]byte(nil), trimmed...)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return append([]byte(nil), trimmed...)
	}
	return canonical
}

func bindAdminJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return binding.Validator.ValidateStruct(target)
}

func (h *AdminHandler) Overview(c *gin.Context) {
	var principals []models.ServicePrincipal
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at DESC").
		Find(&principals).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load service principals", true)
		return
	}

	now := time.Now().UTC()
	var leases []models.TicketLease
	if err := h.db.WithContext(c.Request.Context()).
		Where("released_at IS NULL AND expires_at > ?", now).
		Order("expires_at ASC").
		Limit(100).
		Find(&leases).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load ticket leases", true)
		return
	}

	var events []models.DomainEvent
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at DESC").
		Limit(100).
		Find(&events).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load domain events", true)
		return
	}

	var deliveries []models.OutboxDelivery
	if err := h.db.WithContext(c.Request.Context()).
		Order("updated_at DESC").
		Limit(100).
		Find(&deliveries).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load Outbox deliveries", true)
		return
	}
	var attachments []models.TicketAttachment
	if err := h.db.WithContext(c.Request.Context()).
		Order("updated_at DESC").
		Limit(100).
		Find(&attachments).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load attachment scan state", true)
		return
	}
	var decisions []models.PolicyDecision
	if err := h.db.WithContext(c.Request.Context()).
		Order("created_at DESC").
		Limit(100).
		Find(&decisions).Error; err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load policy decisions", true)
		return
	}

	versionSubjects := []string{"agent-control/read-only", "agent-control/emergency-stop"}
	for i := range principals {
		versionSubjects = append(versionSubjects, "service-principal/"+principals[i].ID)
	}
	for i := range leases {
		versionSubjects = append(versionSubjects, "lease/"+leases[i].ID)
	}
	for i := range deliveries {
		versionSubjects = append(versionSubjects, "outbox/"+deliveries[i].ID)
	}
	for i := range attachments {
		versionSubjects = append(
			versionSubjects,
			"attachment/"+strconv.FormatUint(uint64(attachments[i].ID), 10),
		)
	}
	resourceVersions, err := h.adminResourceVersions(
		c.Request.Context(),
		h.db,
		versionSubjects,
	)
	if err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load administrator resource versions", true)
		return
	}

	principalRows := make([]gin.H, 0, len(principals))
	for i := range principals {
		principal := &principals[i]
		principalRows = append(principalRows, gin.H{
			"id":                 principal.ID,
			"client_id":          principal.ID,
			"name":               principal.Name,
			"description":        principal.Description,
			"status":             principal.Status,
			"scopes":             principal.ScopeList(),
			"rate_limit":         principal.RateLimitPerMinute,
			"concurrency_limit":  principal.ConcurrentLimit,
			"last_used_at":       principal.LastUsedAt,
			"expires_at":         principal.ExpiresAt,
			"created_at":         principal.CreatedAt,
			"read_only":          principal.ReadOnly,
			"emergency_disabled": principal.EmergencyDisabled,
			"resource_version":   resourceVersions["service-principal/"+principal.ID],
		})
	}

	leaseRows := make([]gin.H, 0, len(leases))
	for i := range leases {
		lease := &leases[i]
		var ticket models.Ticket
		_ = h.db.WithContext(c.Request.Context()).Select("id", "ticket_number").
			First(&ticket, lease.TicketID).Error
		principalName := lease.HolderActorID
		if lease.HolderActorType == models.ActorTypeServicePrincipal {
			var principal models.ServicePrincipal
			if h.db.WithContext(c.Request.Context()).Select("name").
				First(&principal, "id = ?", lease.HolderActorID).Error == nil {
				principalName = principal.Name
			}
		}
		leaseRows = append(leaseRows, gin.H{
			"id":               lease.ID,
			"ticket_id":        lease.TicketID,
			"ticket_number":    ticket.TicketNumber,
			"principal_name":   principalName,
			"acquired_at":      lease.CreatedAt,
			"expires_at":       lease.ExpiresAt,
			"ticket_version":   lease.TicketVersion,
			"resource_version": resourceVersions["lease/"+lease.ID],
		})
	}

	eventRows := make([]gin.H, 0, len(events))
	for i := range events {
		event := &events[i]
		eventRows = append(eventRows, gin.H{
			"id":               event.ID,
			"type":             event.Type,
			"subject":          event.Subject,
			"actor_type":       event.ActorType,
			"actor_id":         event.ActorID,
			"resource_version": event.ResourceVersion,
			"time":             event.Time,
		})
	}

	outboxRows := make([]gin.H, 0, len(deliveries))
	for i := range deliveries {
		delivery := &deliveries[i]
		outboxRows = append(outboxRows, gin.H{
			"id":               delivery.ID,
			"event_id":         delivery.EventID,
			"destination":      delivery.DestinationType + ":" + delivery.DestinationID,
			"status":           delivery.Status,
			"attempts":         delivery.Attempts,
			"next_attempt_at":  delivery.NextAttemptAt,
			"last_error":       delivery.LastError,
			"updated_at":       delivery.UpdatedAt,
			"resource_version": resourceVersions["outbox/"+delivery.ID],
		})
	}
	attachmentRows := make([]gin.H, 0, len(attachments))
	for i := range attachments {
		attachment := &attachments[i]
		subject := "attachment/" + strconv.FormatUint(uint64(attachment.ID), 10)
		attachmentRows = append(attachmentRows, gin.H{
			"id":               attachment.ID,
			"ticket_id":        attachment.TicketID,
			"original_name":    attachment.OriginalName,
			"mime_type":        attachment.MimeType,
			"file_size":        attachment.FileSize,
			"virus_scan":       attachment.VirusScan,
			"scan_details":     attachment.ScanDetails,
			"scanned_at":       attachment.ScannedAt,
			"updated_at":       attachment.UpdatedAt,
			"resource_version": resourceVersions[subject],
		})
	}
	decisionRows := make([]gin.H, 0, len(decisions))
	for i := range decisions {
		decision := &decisions[i]
		decisionRows = append(decisionRows, gin.H{
			"id":                decision.ID,
			"created_at":        decision.CreatedAt,
			"actor_type":        decision.ActorType,
			"actor_id":          decision.ActorID,
			"credential_id":     decision.CredentialID,
			"scope":             decision.Scope,
			"action":            decision.Action,
			"resource_type":     decision.ResourceType,
			"resource_id":       decision.ResourceID,
			"allowed":           decision.Allowed,
			"reason_code":       decision.ReasonCode,
			"matched_policy_id": decision.MatchedPolicyID,
			"source_protocol":   decision.SourceProtocol,
			"request_digest":    decision.RequestDigest,
		})
	}

	WriteData(c, http.StatusOK, gin.H{
		"global_read_only":         h.control != nil && h.control.ReadOnly(),
		"global_read_only_version": resourceVersions["agent-control/read-only"],
		"emergency_stop":           h.control != nil && h.control.EmergencyStop(),
		"emergency_stop_version":   resourceVersions["agent-control/emergency-stop"],
		"principals":               principalRows,
		"leases":                   leaseRows,
		"events":                   eventRows,
		"outbox":                   outboxRows,
		"attachments":              attachmentRows,
		"policy_decisions":         decisionRows,
	}, Meta{})
}

func (h *AdminHandler) CreateServicePrincipal(c *gin.Context) {
	var request struct {
		Name                string     `json:"name" binding:"required,min=3,max=100"`
		Description         string     `json:"description" binding:"max=500"`
		Scopes              []string   `json:"scopes" binding:"required,min=1,unique"`
		RateLimit           int        `json:"rate_limit" binding:"omitempty,min=1,max=10000"`
		ConcurrencyLimit    int        `json:"concurrency_limit" binding:"omitempty,min=1,max=100"`
		ExpiresAt           *time.Time `json:"expires_at"`
		CompatibilityUserID *uint      `json:"compatibility_user_id"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}

	userID := c.GetUint("user_id")
	compatibilityUserID := request.CompatibilityUserID
	if compatibilityUserID == nil && h.compatibilityUserID > 0 {
		value := h.compatibilityUserID
		compatibilityUserID = &value
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:                http.StatusCreated,
			ContainsOneTimeSecret: true,
			Request:               request,
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			principal, err := h.native.CreateServicePrincipal(txCtx, services.CreateServicePrincipalInput{
				Name:                request.Name,
				Description:         request.Description,
				Scopes:              request.Scopes,
				RateLimitPerMinute:  request.RateLimit,
				ConcurrentLimit:     request.ConcurrencyLimit,
				ExpiresAt:           request.ExpiresAt,
				CompatibilityUserID: compatibilityUserID,
				CreatedByID:         &userID,
			})
			if err != nil {
				return adminMutationResult{}, err
			}
			issued, err := h.native.IssueCredential(txCtx, principal.ID, "initial", h.credentialTTL)
			if err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data: gin.H{
					"client_id":     principal.ID,
					"client_secret": issued.Token,
					"expires_at":    issued.Credential.ExpiresAt,
				},
				EventName:     "service_principal.created",
				Subject:       "service-principal/" + principal.ID,
				ResourceID:    principal.ID,
				ChangedFields: []string{"service_principal", "credentials"},
				PublicValues: gin.H{
					"status":               principal.Status,
					"scopes":               principal.ScopeList(),
					"rate_limit":           principal.RateLimitPerMinute,
					"concurrency_limit":    principal.ConcurrentLimit,
					"credential_id":        issued.Credential.ID,
					"credential_expires":   issued.Credential.ExpiresAt,
					"principal_expires_at": principal.ExpiresAt,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) RotateCredential(c *gin.Context) {
	principalID := strings.TrimSpace(c.Param("id"))
	if principalID == "" {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Service principal ID is required", false)
		return
	}

	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:                http.StatusOK,
			ContainsOneTimeSecret: true,
			PreconditionSubject:   "service-principal/" + principalID,
			Request:               struct{}{},
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			issued, err := h.native.RotateCredential(
				txCtx,
				principalID,
				"rotated",
				h.credentialTTL,
				models.HumanActor(c.GetUint("user_id")),
			)
			if err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data: gin.H{
					"client_id":     principalID,
					"client_secret": issued.Token,
					"expires_at":    issued.Credential.ExpiresAt,
				},
				EventName:     "service_principal.credential.rotated",
				Subject:       "service-principal/" + principalID,
				ResourceID:    principalID,
				ChangedFields: []string{"credentials"},
				PublicValues: gin.H{
					"credential_id":      issued.Credential.ID,
					"credential_expires": issued.Credential.ExpiresAt,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) RevokeCredential(c *gin.Context) {
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "service-principal/" + c.Param("id"),
			Request: gin.H{
				"credential_id": c.Param("credential_id"),
			},
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			var credential models.AgentCredential
			if err := tx.WithContext(txCtx).
				Where("id = ? AND service_principal_id = ?", c.Param("credential_id"), c.Param("id")).
				First(&credential).Error; err != nil {
				return adminMutationResult{}, err
			}
			if err := h.native.RevokeCredential(
				txCtx,
				credential.ID,
				models.HumanActor(c.GetUint("user_id")),
			); err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          gin.H{"revoked": true},
				EventName:     "service_principal.credential.revoked",
				Subject:       "service-principal/" + credential.ServicePrincipalID,
				ResourceID:    credential.ServicePrincipalID,
				ChangedFields: []string{"credentials"},
				PublicValues:  gin.H{"credential_id": credential.ID},
			}, nil
		},
	)
}

func (h *AdminHandler) SetServicePrincipalStatus(c *gin.Context) {
	var request struct {
		Status            *models.ServicePrincipalStatus `json:"status"`
		ReadOnly          *bool                          `json:"read_only"`
		EmergencyDisabled *bool                          `json:"emergency_disabled"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	changedFields := make([]string, 0, 3)
	if request.Status != nil {
		changedFields = append(changedFields, "status")
	}
	if request.ReadOnly != nil {
		changedFields = append(changedFields, "read_only")
	}
	if request.EmergencyDisabled != nil {
		changedFields = append(changedFields, "emergency_disabled")
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "service-principal/" + c.Param("id"),
			Request:             request,
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			existing, err := h.native.GetServicePrincipal(txCtx, c.Param("id"))
			if err != nil {
				return adminMutationResult{}, err
			}
			status := existing.Status
			if request.Status != nil {
				status = *request.Status
			}
			readOnly := existing.ReadOnly
			if request.ReadOnly != nil {
				readOnly = *request.ReadOnly
			}
			emergencyDisabled := existing.EmergencyDisabled
			if request.EmergencyDisabled != nil {
				emergencyDisabled = *request.EmergencyDisabled
			}
			principal, err := h.native.SetServicePrincipalControls(
				txCtx,
				c.Param("id"),
				status,
				readOnly,
				emergencyDisabled,
			)
			if err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          principal,
				EventName:     "service_principal.controls.updated",
				Subject:       "service-principal/" + principal.ID,
				ResourceID:    principal.ID,
				ChangedFields: changedFields,
				PublicValues: gin.H{
					"status":             principal.Status,
					"read_only":          principal.ReadOnly,
					"emergency_disabled": principal.EmergencyDisabled,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) CreatePolicy(c *gin.Context) {
	var request struct {
		Name         string                   `json:"name"`
		Effect       models.AgentPolicyEffect `json:"effect" binding:"required"`
		Scope        string                   `json:"scope" binding:"required"`
		Action       string                   `json:"action"`
		ResourceType string                   `json:"resource_type"`
		ResourceID   string                   `json:"resource_id"`
		Conditions   map[string]any           `json:"conditions"`
		Priority     int                      `json:"priority"`
		ExpiresAt    *time.Time               `json:"expires_at"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusCreated,
			PreconditionSubject: "service-principal/" + c.Param("id"),
			Request:             request,
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			policy, err := h.native.CreateAgentPolicy(txCtx, services.CreateAgentPolicyInput{
				ServicePrincipalID: c.Param("id"),
				Name:               request.Name,
				Effect:             request.Effect,
				Scope:              request.Scope,
				Action:             request.Action,
				ResourceType:       request.ResourceType,
				ResourceID:         request.ResourceID,
				Conditions:         request.Conditions,
				Priority:           request.Priority,
				ExpiresAt:          request.ExpiresAt,
			})
			if err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          policy,
				EventName:     "service_principal.policy.created",
				Subject:       "service-principal/" + policy.ServicePrincipalID + "/policy/" + policy.ID,
				ResourceID:    policy.ID,
				ChangedFields: []string{"policy"},
				PublicValues: gin.H{
					"service_principal_id": policy.ServicePrincipalID,
					"effect":               policy.Effect,
					"scope":                policy.Scope,
					"action":               policy.Action,
					"resource_type":        policy.ResourceType,
					"resource_id":          policy.ResourceID,
					"priority":             policy.Priority,
					"expires_at":           policy.ExpiresAt,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) ListPolicies(c *gin.Context) {
	var policies []models.AgentPolicy
	if err := h.db.WithContext(c.Request.Context()).
		Where("service_principal_id = ?", c.Param("id")).
		Order("priority DESC, created_at DESC").
		Find(&policies).Error; err != nil {
		h.writeNativeError(c, err)
		return
	}
	subjects := make([]string, 0, len(policies))
	for i := range policies {
		subjects = append(
			subjects,
			"service-principal/"+policies[i].ServicePrincipalID+"/policy/"+policies[i].ID,
		)
	}
	versions, err := h.adminResourceVersions(c.Request.Context(), h.db, subjects)
	if err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to load policy versions", true)
		return
	}
	rows := make([]gin.H, 0, len(policies))
	for i := range policies {
		policy := &policies[i]
		subject := "service-principal/" + policy.ServicePrincipalID + "/policy/" + policy.ID
		rows = append(rows, gin.H{
			"id":                   policy.ID,
			"created_at":           policy.CreatedAt,
			"updated_at":           policy.UpdatedAt,
			"service_principal_id": policy.ServicePrincipalID,
			"name":                 policy.Name,
			"effect":               policy.Effect,
			"scope":                policy.Scope,
			"action":               policy.Action,
			"resource_type":        policy.ResourceType,
			"resource_id":          policy.ResourceID,
			"conditions":           policy.Conditions,
			"priority":             policy.Priority,
			"is_active":            policy.IsActive,
			"expires_at":           policy.ExpiresAt,
			"resource_version":     versions[subject],
		})
	}
	WriteData(c, http.StatusOK, rows, Meta{})
}

func (h *AdminHandler) DisablePolicy(c *gin.Context) {
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "service-principal/" + c.Param("id") + "/policy/" + c.Param("policy_id"),
			Request: gin.H{
				"policy_id": c.Param("policy_id"),
			},
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			update := tx.WithContext(txCtx).
				Model(&models.AgentPolicy{}).
				Where("id = ? AND service_principal_id = ?", c.Param("policy_id"), c.Param("id")).
				Updates(map[string]any{"is_active": false, "updated_at": time.Now().UTC()})
			if update.Error != nil {
				return adminMutationResult{}, update.Error
			}
			if update.RowsAffected == 0 {
				return adminMutationResult{}, gorm.ErrRecordNotFound
			}
			return adminMutationResult{
				Data:          gin.H{"disabled": true},
				EventName:     "service_principal.policy.disabled",
				Subject:       "service-principal/" + c.Param("id") + "/policy/" + c.Param("policy_id"),
				ResourceID:    c.Param("policy_id"),
				ChangedFields: []string{"is_active"},
				PublicValues: gin.H{
					"service_principal_id": c.Param("id"),
					"is_active":            false,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) SetReadOnly(c *gin.Context) {
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	if h.control == nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Agent runtime control is unavailable", true)
		return
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "agent-control/read-only",
			Request:             request,
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			if err := h.control.persistTx(
				txCtx,
				tx,
				agentReadOnlyConfigKey,
				request.Enabled,
				c.GetUint("user_id"),
			); err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          gin.H{"enabled": request.Enabled},
				EventName:     "agent_control.read_only.updated",
				Subject:       "agent-control/read-only",
				ResourceID:    agentReadOnlyConfigKey,
				ChangedFields: []string{"enabled"},
				PublicValues:  gin.H{"enabled": request.Enabled},
				AfterCommit: func() {
					h.control.SetReadOnly(request.Enabled)
				},
			}, nil
		},
	)
}

func (h *AdminHandler) SetEmergencyStop(c *gin.Context) {
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	if h.control == nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Agent runtime control is unavailable", true)
		return
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "agent-control/emergency-stop",
			Request:             request,
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			if err := h.control.persistTx(
				txCtx,
				tx,
				agentEmergencyConfigKey,
				request.Enabled,
				c.GetUint("user_id"),
			); err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          gin.H{"enabled": request.Enabled},
				EventName:     "agent_control.emergency_stop.updated",
				Subject:       "agent-control/emergency-stop",
				ResourceID:    agentEmergencyConfigKey,
				ChangedFields: []string{"enabled"},
				PublicValues:  gin.H{"enabled": request.Enabled},
				AfterCommit: func() {
					h.control.SetEmergencyStop(request.Enabled)
				},
			}, nil
		},
	)
}

func (h *AdminHandler) ReplayOutbox(c *gin.Context) {
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusAccepted,
			PreconditionSubject: "outbox/" + c.Param("id"),
			Request: gin.H{
				"delivery_id": c.Param("id"),
			},
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			if err := h.native.ReplayOutbox(txCtx, c.Param("id")); err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:          gin.H{"replayed": true},
				EventName:     "outbox.replayed",
				Subject:       "outbox/" + c.Param("id"),
				ResourceID:    c.Param("id"),
				ChangedFields: []string{"status", "attempts", "next_attempt_at", "locked_at", "last_error", "delivered_at"},
				PublicValues:  gin.H{"status": models.OutboxDeliveryPending},
			}, nil
		},
	)
}

func (h *AdminHandler) ForceReleaseLease(c *gin.Context) {
	leaseID := strings.TrimSpace(c.Param("id"))
	if leaseID == "" {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Ticket lease ID is required", false)
		return
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: "lease/" + leaseID,
			Request: gin.H{
				"lease_id": leaseID,
			},
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			var lease models.TicketLease
			if err := tx.WithContext(txCtx).First(&lease, "id = ?", leaseID).Error; err != nil {
				return adminMutationResult{}, err
			}
			reason := fmt.Sprintf("force released by administrator %d", c.GetUint("user_id"))
			now := time.Now().UTC()
			release := tx.WithContext(txCtx).
				Model(&models.TicketLease{}).
				Where("id = ? AND released_at IS NULL", leaseID).
				Updates(map[string]any{
					"released_at":    now,
					"release_reason": reason,
					"updated_at":     now,
				})
			if release.Error != nil {
				return adminMutationResult{}, release.Error
			}
			if release.RowsAffected == 0 {
				return adminMutationResult{}, services.ErrLeaseExpired
			}
			lease.ReleasedAt = &now
			lease.ReleaseReason = reason
			return adminMutationResult{
				Data:          &lease,
				EventName:     "ticket.lease.force_released",
				Subject:       "lease/" + leaseID,
				ResourceID:    leaseID,
				ChangedFields: []string{"released_at", "release_reason"},
				PublicValues: gin.H{
					"ticket_id":      lease.TicketID,
					"release_reason": reason,
				},
			}, nil
		},
	)
}

func (h *AdminHandler) MarkAttachmentScan(c *gin.Context) {
	attachmentID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || attachmentID == 0 {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Invalid attachment ID", false)
		return
	}
	var request struct {
		Status  models.VirusScanStatus `json:"status" binding:"required"`
		Details string                 `json:"details" binding:"max=4000"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: fmt.Sprintf("attachment/%d", attachmentID),
			Request:             request,
		},
		func(txCtx context.Context, _ *gorm.DB) (adminMutationResult, error) {
			if err := h.native.MarkAttachmentScan(
				txCtx,
				uint(attachmentID),
				request.Status,
				request.Details,
			); err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data: gin.H{
					"attachment_id": attachmentID,
					"status":        request.Status,
				},
				EventName:     "attachment.scan.recorded",
				Subject:       fmt.Sprintf("attachment/%d", attachmentID),
				ResourceID:    strconv.FormatUint(attachmentID, 10),
				ChangedFields: []string{"virus_scan", "scan_details", "scanned_at"},
				PublicValues:  gin.H{"status": request.Status},
			}, nil
		},
	)
}

func setOneTimeSecretResponseHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

var errAdminEventPersistence = errors.New("persist administrator event")

type adminMutationResult struct {
	Data          any
	EventName     string
	Subject       string
	ResourceID    string
	ChangedFields []string
	PublicValues  map[string]any
	AfterCommit   func()
}

type adminMutationOptions struct {
	Status                int
	ContainsOneTimeSecret bool
	PreconditionSubject   string
	Request               any
}

type adminReplayRecord struct {
	Schema     string `json:"schema"`
	Body       string `json:"body"`
	Nonce      string `json:"nonce,omitempty"`
	Encrypted  bool   `json:"encrypted"`
	ETag       string `json:"etag"`
	ParentETag string `json:"parent_etag,omitempty"`
	NoStore    bool   `json:"no_store,omitempty"`
}

type adminVersionConflictError struct {
	Expected uint64
	Current  uint64
}

func (e *adminVersionConflictError) Error() string {
	return fmt.Sprintf(
		"%s: expected %d, actual %d",
		services.ErrVersionConflict,
		e.Expected,
		e.Current,
	)
}

func (e *adminVersionConflictError) Unwrap() error {
	return services.ErrVersionConflict
}

func newAdminReplayCipher(key []byte) cipher.AEAD {
	material := make([]byte, 0, len(key)+48)
	material = append(material, []byte("chronodesk/admin-idempotency-replay/v1")...)
	material = append(material, 0)
	material = append(material, key...)
	sum := sha256.Sum256(material)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		panic("create administrator replay cipher: " + err.Error())
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("create administrator replay AEAD: " + err.Error())
	}
	return aead
}

func (h *AdminHandler) executeAdminMutation(
	c *gin.Context,
	options adminMutationOptions,
	mutate func(context.Context, *gorm.DB) (adminMutationResult, error),
) {
	if h.native == nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Agent-native service is unavailable", true)
		return
	}
	if options.Status == 0 {
		options.Status = http.StatusOK
	}
	if options.ContainsOneTimeSecret && h.replayCipher == nil {
		WriteProblem(
			c,
			http.StatusServiceUnavailable,
			ProblemInternal,
			"Administrator credential replay encryption is not configured",
			false,
		)
		return
	}
	idempotencyKey, ok := RequireIdempotencyKey(c)
	if !ok {
		return
	}
	var expectedVersion uint64
	if options.PreconditionSubject != "" {
		rawIfMatch := strings.TrimSpace(c.GetHeader("If-Match"))
		if rawIfMatch == "" {
			WriteProblem(
				c,
				http.StatusPreconditionRequired,
				"precondition_required",
				"If-Match is required for this administrator command",
				false,
			)
			return
		}
		var err error
		expectedVersion, err = ParseIfMatch(rawIfMatch)
		if err != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
			return
		}
	}
	requestBody, _ := c.Get(adminRequestBodyContextKey)
	canonicalBody, ok := requestBody.([]byte)
	if !ok {
		var err error
		canonicalBody, err = json.Marshal(options.Request)
		if err != nil {
			WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, "Failed to canonicalize request", false)
			return
		}
	}
	operationPath := c.FullPath()
	if operationPath == "" {
		operationPath = c.Request.URL.Path
	}
	operation := "admin:" + c.Request.Method + ":" + operationPath
	fingerprint := commandFingerprint(
		c.Request.Method,
		c.Request.URL.Path,
		expectedVersion,
		"",
		canonicalBody,
	)
	reservation, err := h.native.ReserveIdempotency(
		c.Request.Context(),
		models.HumanActor(c.GetUint("user_id")),
		operation,
		idempotencyKey,
		fingerprint,
		adminIdempotencyRetention,
	)
	if err != nil {
		h.writeNativeError(c, err)
		return
	}
	if reservation.Replayed {
		h.writeAdminReplay(c, reservation.Record)
		return
	}

	var result adminMutationResult
	var receipt Receipt
	var replay adminReplayRecord
	var responseBody []byte
	var parentVersion uint64
	err = h.native.InTransaction(
		c.Request.Context(),
		func(txCtx context.Context, tx *gorm.DB) error {
			if options.PreconditionSubject != "" {
				var casErr error
				parentVersion, casErr = h.compareAndSwapAdminResourceVersionTx(
					txCtx,
					tx,
					options.PreconditionSubject,
					expectedVersion,
					c.GetUint("user_id"),
				)
				if casErr != nil {
					return casErr
				}
			}
			var err error
			result, err = mutate(txCtx, tx)
			if err != nil {
				return err
			}
			if strings.TrimSpace(result.Subject) == "" || strings.TrimSpace(result.ResourceID) == "" {
				return fmt.Errorf("%w: administrator mutation returned no resource identity", errAdminEventPersistence)
			}
			resourceVersion := parentVersion
			parentETag := ""
			if options.PreconditionSubject == "" || options.PreconditionSubject != result.Subject {
				resourceVersion, err = h.initializeAdminResourceVersionTx(
					txCtx,
					tx,
					result.Subject,
					c.GetUint("user_id"),
				)
				if err != nil {
					return err
				}
				if options.PreconditionSubject != "" {
					parentETag = FormatETag(parentVersion)
				}
			}
			receipt, err = h.appendAdminMutationTx(
				txCtx,
				tx,
				c.GetUint("user_id"),
				RequestID(c),
				result,
				resourceVersion,
			)
			if err != nil {
				return err
			}
			envelope := Envelope{
				Data:    result.Data,
				Meta:    Meta{RequestID: RequestID(c)},
				Receipt: &receipt,
			}
			responseBody, err = json.Marshal(envelope)
			if err != nil {
				return fmt.Errorf("encode administrator response: %w", err)
			}
			replay, err = h.encodeAdminReplay(
				reservation.Record.ID,
				responseBody,
				FormatETag(receipt.ResourceVersion),
				parentETag,
				options.ContainsOneTimeSecret,
			)
			if err != nil {
				return err
			}
			return h.native.CompleteIdempotencyTx(
				txCtx,
				tx,
				reservation.Record.ID,
				options.Status,
				replay,
				receipt.ResourceID,
				receipt.EventID,
			)
		},
	)
	if err != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(err),
		)
		if errors.Is(err, errAdminEventPersistence) {
			WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Failed to record administrator domain event", true)
			return
		}
		var versionConflict *adminVersionConflictError
		if errors.As(err, &versionConflict) && versionConflict.Current > 0 {
			c.Header("ETag", FormatETag(versionConflict.Current))
		} else if options.PreconditionSubject != "" && expectedVersion > 0 {
			c.Header("ETag", FormatETag(expectedVersion))
		}
		h.writeNativeError(c, err)
		return
	}
	if result.AfterCommit != nil {
		result.AfterCommit()
	}
	if options.ContainsOneTimeSecret {
		setOneTimeSecretResponseHeaders(c)
	}
	c.Header("ETag", replay.ETag)
	if replay.ParentETag != "" {
		c.Header("X-Parent-ETag", replay.ParentETag)
	}
	c.Data(options.Status, "application/json; charset=utf-8", responseBody)
}

func (h *AdminHandler) appendAdminMutationTx(
	ctx context.Context,
	tx *gorm.DB,
	adminUserID uint,
	requestID string,
	result adminMutationResult,
	resourceVersion uint64,
) (Receipt, error) {
	changedFields := result.ChangedFields
	if len(changedFields) == 0 {
		changedFields = []string{"resource"}
	} else {
		changedFields = append([]string(nil), changedFields...)
	}
	if resourceVersion == 0 {
		return Receipt{}, fmt.Errorf("%w: resource version is required", errAdminEventPersistence)
	}
	eventData := gin.H{
		"request_id":     requestID,
		"subject":        result.Subject,
		"resource_id":    result.ResourceID,
		"changed_fields": changedFields,
	}
	if values := safeAdminEventValues(result.PublicValues); len(values) > 0 {
		eventData["values"] = values
	}
	event, err := h.native.AppendDomainEventTx(
		ctx,
		tx,
		services.DomainEventInput{
			Type:            "io.chronodesk.admin." + result.EventName + ".v1",
			Subject:         result.Subject,
			Data:            eventData,
			TraceID:         requestID,
			CorrelationID:   requestID,
			Actor:           models.HumanActor(adminUserID),
			ResourceVersion: resourceVersion,
		},
		nil,
	)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", errAdminEventPersistence, err)
	}
	return Receipt{
		OperationID:     "admin-op-" + event.ID,
		ResourceID:      result.ResourceID,
		ResourceVersion: event.ResourceVersion,
		EventID:         event.ID,
		ChangedFields:   changedFields,
	}, nil
}

func (h *AdminHandler) initializeAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	subject string,
	updatedBy uint,
) (uint64, error) {
	row := adminResourceVersionRow(subject, 1, updatedBy)
	create := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row)
	if create.Error != nil {
		return 0, fmt.Errorf("%w: initialize administrator resource version: %v", errAdminEventPersistence, create.Error)
	}
	if create.RowsAffected == 1 {
		return 1, nil
	}
	current, err := h.currentAdminResourceVersion(ctx, tx, subject)
	if err != nil {
		return 0, err
	}
	return 0, &adminVersionConflictError{Expected: 0, Current: current}
}

func (h *AdminHandler) compareAndSwapAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	subject string,
	expected uint64,
	updatedBy uint,
) (uint64, error) {
	if expected == 0 {
		return 0, &adminVersionConflictError{Expected: expected, Current: 1}
	}
	if err := h.ensureAdminResourceVersionTx(ctx, tx, subject, updatedBy); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	update := tx.WithContext(ctx).
		Model(&models.SystemConfig{}).
		Where("key = ? AND version = ?", adminResourceVersionKey(subject), expected).
		Updates(map[string]any{
			"version":    gorm.Expr("version + 1"),
			"updated_at": now,
			"updated_by": userID,
		})
	if update.Error != nil {
		return 0, fmt.Errorf("%w: compare administrator resource version: %v", errAdminEventPersistence, update.Error)
	}
	if update.RowsAffected == 1 {
		return expected + 1, nil
	}
	current, err := h.currentAdminResourceVersion(ctx, tx, subject)
	if err != nil {
		return 0, err
	}
	return 0, &adminVersionConflictError{Expected: expected, Current: current}
}

func (h *AdminHandler) ensureAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	subject string,
	updatedBy uint,
) error {
	var eventVersion uint64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("COALESCE(MAX(resource_version), 0)").
		Where("subject = ? AND type LIKE ?", subject, "io.chronodesk.admin.%").
		Scan(&eventVersion).Error; err != nil {
		return fmt.Errorf("%w: load administrator event version: %v", errAdminEventPersistence, err)
	}
	if eventVersion == 0 {
		eventVersion = 1
	}
	row := adminResourceVersionRow(subject, eventVersion, updatedBy)
	if err := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error; err != nil {
		return fmt.Errorf("%w: initialize administrator resource version: %v", errAdminEventPersistence, err)
	}
	return nil
}

func adminResourceVersionRow(subject string, version uint64, updatedBy uint) models.SystemConfig {
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	return models.SystemConfig{
		Key:          adminResourceVersionKey(subject),
		Value:        subject,
		ValueType:    "string",
		Description:  "Administrator command resource version",
		Category:     "security",
		Group:        "agent-resource-version",
		IsActive:     true,
		DefaultValue: subject,
		UpdatedBy:    userID,
		Version:      int(version),
	}
}

func adminResourceVersionKey(subject string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(subject)))
	return adminVersionKeyPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

func (h *AdminHandler) currentAdminResourceVersion(
	ctx context.Context,
	db *gorm.DB,
	subject string,
) (uint64, error) {
	var row models.SystemConfig
	err := db.WithContext(ctx).
		Select("version").
		First(&row, "key = ?", adminResourceVersionKey(subject)).Error
	if err == nil {
		if row.Version <= 0 {
			return 1, nil
		}
		return uint64(row.Version), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	var eventVersion uint64
	if err := db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("COALESCE(MAX(resource_version), 0)").
		Where("subject = ? AND type LIKE ?", subject, "io.chronodesk.admin.%").
		Scan(&eventVersion).Error; err != nil {
		return 0, err
	}
	if eventVersion == 0 {
		eventVersion = 1
	}
	return eventVersion, nil
}

func (h *AdminHandler) adminResourceVersions(
	ctx context.Context,
	db *gorm.DB,
	subjects []string,
) (map[string]uint64, error) {
	versions := make(map[string]uint64, len(subjects))
	subjectByKey := make(map[string]string, len(subjects))
	keys := make([]string, 0, len(subjects))
	uniqueSubjects := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		if _, exists := versions[subject]; exists {
			continue
		}
		versions[subject] = 1
		key := adminResourceVersionKey(subject)
		subjectByKey[key] = subject
		keys = append(keys, key)
		uniqueSubjects = append(uniqueSubjects, subject)
	}
	if len(keys) == 0 {
		return versions, nil
	}
	var rows []models.SystemConfig
	if err := db.WithContext(ctx).
		Select("key", "version").
		Where("key IN ?", keys).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	found := make(map[string]struct{}, len(rows))
	for i := range rows {
		subject := subjectByKey[rows[i].Key]
		if subject == "" {
			continue
		}
		found[subject] = struct{}{}
		if rows[i].Version > 0 {
			versions[subject] = uint64(rows[i].Version)
		}
	}
	missing := make([]string, 0, len(uniqueSubjects)-len(found))
	for _, subject := range uniqueSubjects {
		if _, ok := found[subject]; !ok {
			missing = append(missing, subject)
		}
	}
	if len(missing) == 0 {
		return versions, nil
	}
	var eventRows []struct {
		Subject string
		Version uint64
	}
	if err := db.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("subject, MAX(resource_version) AS version").
		Where("subject IN ? AND type LIKE ?", missing, "io.chronodesk.admin.%").
		Group("subject").
		Scan(&eventRows).Error; err != nil {
		return nil, err
	}
	for i := range eventRows {
		if eventRows[i].Version > 0 {
			versions[eventRows[i].Subject] = eventRows[i].Version
		}
	}
	return versions, nil
}

func (h *AdminHandler) encodeAdminReplay(
	recordID string,
	responseBody []byte,
	etag string,
	parentETag string,
	encrypt bool,
) (adminReplayRecord, error) {
	replay := adminReplayRecord{
		Schema:     adminReplaySchema,
		ETag:       etag,
		ParentETag: parentETag,
		Encrypted:  encrypt,
		NoStore:    encrypt,
	}
	payload := responseBody
	if encrypt {
		if h.replayCipher == nil {
			return adminReplayRecord{}, errors.New("administrator replay encryption is unavailable")
		}
		nonce := make([]byte, h.replayCipher.NonceSize())
		if _, err := cryptorand.Read(nonce); err != nil {
			return adminReplayRecord{}, fmt.Errorf("generate administrator replay nonce: %w", err)
		}
		payload = h.replayCipher.Seal(nil, nonce, responseBody, []byte(recordID))
		replay.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	}
	replay.Body = base64.RawURLEncoding.EncodeToString(payload)
	return replay, nil
}

func (h *AdminHandler) writeAdminReplay(c *gin.Context, record *models.IdempotencyRecord) {
	if record == nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Idempotency replay record is unavailable", true)
		return
	}
	var replay adminReplayRecord
	if err := json.Unmarshal(record.ResponseBody, &replay); err != nil ||
		replay.Schema != adminReplaySchema ||
		replay.Body == "" {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Idempotency replay record is invalid", true)
		return
	}
	body, err := base64.RawURLEncoding.DecodeString(replay.Body)
	if err != nil {
		WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Idempotency replay payload is invalid", true)
		return
	}
	if replay.Encrypted {
		if h.replayCipher == nil {
			WriteProblem(c, http.StatusServiceUnavailable, ProblemInternal, "Administrator replay decryption is unavailable", true)
			return
		}
		nonce, decodeErr := base64.RawURLEncoding.DecodeString(replay.Nonce)
		if decodeErr != nil || len(nonce) != h.replayCipher.NonceSize() {
			WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Idempotency replay nonce is invalid", true)
			return
		}
		body, err = h.replayCipher.Open(nil, nonce, body, []byte(record.ID))
		if err != nil {
			WriteProblem(c, http.StatusInternalServerError, ProblemInternal, "Idempotency replay authentication failed", true)
			return
		}
	}
	if replay.NoStore {
		setOneTimeSecretResponseHeaders(c)
	}
	if replay.ETag != "" {
		c.Header("ETag", replay.ETag)
	}
	if replay.ParentETag != "" {
		c.Header("X-Parent-ETag", replay.ParentETag)
	}
	status := record.ResponseCode
	if status < 200 || status > 599 {
		status = http.StatusOK
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

func safeAdminEventValues(values map[string]any) map[string]any {
	safe := make(map[string]any, len(values))
	for key, value := range values {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "client_secret", "secret", "password", "access_token", "refresh_token", "token":
			continue
		default:
			safe[key] = value
		}
	}
	return safe
}

func (h *AdminHandler) writeNativeError(c *gin.Context, err error) {
	code := services.AgentNativeErrorCode(err)
	status := http.StatusBadRequest
	retryable := false
	switch {
	case errors.Is(err, services.ErrPrincipalNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		status, code = http.StatusNotFound, ProblemNotFound
	case errors.Is(err, services.ErrPolicyDenied),
		errors.Is(err, services.ErrReadOnlyMode),
		errors.Is(err, services.ErrGlobalEmergencyStop):
		status, code = http.StatusForbidden, ProblemPolicyDenied
	case errors.Is(err, services.ErrRateLimited),
		errors.Is(err, services.ErrConcurrencyLimit):
		status, code, retryable = http.StatusTooManyRequests, ProblemRateLimited, true
	case errors.Is(err, services.ErrAutomationLoop):
		status, code, retryable = http.StatusTooManyRequests, ProblemAutomationLoop, true
	case errors.Is(err, services.ErrExecutionGuardUnavailable):
		status, code, retryable = http.StatusServiceUnavailable, ProblemServiceUnavailable, true
		err = errors.New("Agent execution protection is temporarily unavailable")
	case errors.Is(err, services.ErrVersionConflict):
		status, code = http.StatusConflict, ProblemVersionConflict
	case errors.Is(err, services.ErrLeaseConflict), errors.Is(err, services.ErrLeaseExpired), errors.Is(err, services.ErrLeaseNotOwned):
		status, code = http.StatusConflict, ProblemLeaseConflict
	case errors.Is(err, services.ErrOutboxReplayConflict):
		status, code = http.StatusConflict, ProblemOutboxConflict
	case errors.Is(err, services.ErrIdempotencyConflict), errors.Is(err, services.ErrIdempotencyInProgress):
		status, code = http.StatusConflict, ProblemIdempotencyConflict
	}
	WriteProblem(c, status, code, err.Error(), retryable)
}
