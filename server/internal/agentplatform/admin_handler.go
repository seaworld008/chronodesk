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
	"sync"
	"sync/atomic"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/middleware"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/services"

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

var (
	ErrRuntimeControlPatchRequired = errors.New(
		"at least one runtime safety control is required",
	)
	ErrRuntimeControlHumanActorRequired = errors.New(
		"runtime safety controls require a human actor",
	)
)

func runtimeControlHumanActorID(actor models.ActorRef) (uint, error) {
	if err := actor.Validate(); err != nil ||
		actor.Type != models.ActorTypeHuman {
		return 0, ErrRuntimeControlHumanActorRequired
	}
	actorID, err := safeconv.ParsePositiveUint(actor.ID)
	if err != nil {
		return 0, ErrRuntimeControlHumanActorRequired
	}
	return actorID, nil
}

// RuntimeControlSnapshot is the authoritative platform-wide Agent safety
// state. Version is shared by both switches so one strong ETag protects the
// complete resource while each boolean can still be patched independently.
type RuntimeControlSnapshot struct {
	GlobalReadOnly bool      `json:"global_read_only"`
	EmergencyStop  bool      `json:"emergency_stop"`
	Version        uint64    `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RuntimeControlPatch preserves omission separately from false. Transports
// must reject a patch with neither field present.
type RuntimeControlPatch struct {
	GlobalReadOnly *bool
	EmergencyStop  *bool
}

// RuntimeControlVersionConflict carries the current durable version so the
// HTTP adapter can return a fresh ETag without disclosing persistence details.
type RuntimeControlVersionConflict struct {
	Expected uint64
	Current  uint64
}

func (e *RuntimeControlVersionConflict) Error() string {
	return fmt.Sprintf(
		"runtime safety control version conflict: expected %d, current %d",
		e.Expected,
		e.Current,
	)
}

func (e *RuntimeControlVersionConflict) CurrentVersion() uint64 {
	if e == nil {
		return 0
	}
	return e.Current
}

type RuntimeControl struct {
	native           *services.AgentNativeService
	db               *gorm.DB
	fallbackReadOnly bool
	mu               sync.Mutex
	readOnly         atomic.Bool
	emergency        atomic.Bool
	healthy          atomic.Bool
}

func NewRuntimeControl(
	ctx context.Context,
	native *services.AgentNativeService,
	db *gorm.DB,
	readOnly bool,
) (*RuntimeControl, error) {
	if ctx == nil {
		return nil, errors.New("runtime control context is required")
	}
	if native == nil {
		return nil, errors.New("runtime control Agent-native service is required")
	}
	if db == nil {
		return nil, errors.New("runtime control database is required")
	}
	control := &RuntimeControl{
		native:           native,
		db:               db,
		fallbackReadOnly: readOnly,
	}
	control.setReadOnly(readOnly)
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return control.ensureRuntimeControlRowsTx(ctx, tx, 0)
	}); err != nil {
		return nil, fmt.Errorf(
			"bootstrap persisted Agent safety controls: %w",
			err,
		)
	}
	if err := control.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("load persisted Agent safety controls: %w", err)
	}
	return control, nil
}

func (c *RuntimeControl) setReadOnly(enabled bool) {
	c.readOnly.Store(enabled)
	if c.native != nil {
		c.native.SetGlobalReadOnly(enabled)
	}
}

func (c *RuntimeControl) ReadOnly() bool {
	return c != nil && c.readOnly.Load()
}

func (c *RuntimeControl) setEmergencyStop(enabled bool) {
	c.emergency.Store(enabled)
	if c.native != nil {
		c.native.SetGlobalEmergencyStop(enabled)
	}
}

func (c *RuntimeControl) EmergencyStop() bool {
	return c != nil && c.emergency.Load()
}

func (c *RuntimeControl) Healthy() bool {
	return c != nil && c.healthy.Load()
}

// Snapshot reads the durable platform resource. It never serves atomics as an
// authoritative management response because another process may have already
// committed a newer value that the refresh loop has not observed yet.
func (c *RuntimeControl) Snapshot(
	ctx context.Context,
) (RuntimeControlSnapshot, error) {
	if c == nil || c.db == nil {
		return RuntimeControlSnapshot{},
			errors.New("runtime control persistence is unavailable")
	}
	if ctx == nil {
		return RuntimeControlSnapshot{},
			errors.New("runtime control context is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var rows []models.SystemConfig
	if err := c.db.WithContext(ctx).
		Where(
			"key IN ? AND is_active = ?",
			[]string{agentReadOnlyConfigKey, agentEmergencyConfigKey},
			true,
		).
		Find(&rows).Error; err != nil {
		c.failClosed()
		return RuntimeControlSnapshot{}, err
	}
	snapshot, err := c.snapshotFromRows(rows)
	if err != nil {
		c.failClosed()
		return RuntimeControlSnapshot{}, err
	}
	return snapshot, nil
}

// ReadPlatformControls exposes a cycle-free primitive contract to the Human
// HTTP adapter while Snapshot remains the typed in-package API.
func (c *RuntimeControl) ReadPlatformControls(
	ctx context.Context,
) (bool, bool, uint64, time.Time, error) {
	snapshot, err := c.Snapshot(ctx)
	return snapshot.GlobalReadOnly,
		snapshot.EmergencyStop,
		snapshot.Version,
		snapshot.UpdatedAt,
		err
}

// UpdateCAS performs one platform-wide compare-and-swap. The emergency-stop
// row is the aggregate version anchor and both protected rows are advanced to
// the same version in one transaction. No generic SystemConfig path can write
// either row.
func (c *RuntimeControl) UpdateCAS(
	ctx context.Context,
	expectedVersion uint64,
	patch RuntimeControlPatch,
	actor models.ActorRef,
) (RuntimeControlSnapshot, error) {
	if c == nil || c.db == nil {
		return RuntimeControlSnapshot{},
			errors.New("runtime control persistence is unavailable")
	}
	if ctx == nil {
		return RuntimeControlSnapshot{},
			errors.New("runtime control context is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if expectedVersion == 0 {
		return RuntimeControlSnapshot{},
			&RuntimeControlVersionConflict{Expected: 0, Current: 1}
	}
	if patch.GlobalReadOnly == nil && patch.EmergencyStop == nil {
		return RuntimeControlSnapshot{}, ErrRuntimeControlPatchRequired
	}
	updatedBy, err := runtimeControlHumanActorID(actor)
	if err != nil {
		return RuntimeControlSnapshot{},
			ErrRuntimeControlHumanActorRequired
	}

	var snapshot RuntimeControlSnapshot
	err = c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var emergencyRow models.SystemConfig
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key = ?", agentEmergencyConfigKey).
			First(&emergencyRow).Error; err != nil {
			return err
		}
		var readOnlyRow models.SystemConfig
		if err := tx.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("key = ?", agentReadOnlyConfigKey).
			First(&readOnlyRow).Error; err != nil {
			return err
		}
		current, err := c.snapshotFromRows(
			[]models.SystemConfig{readOnlyRow, emergencyRow},
		)
		if err != nil {
			return err
		}
		currentVersion := current.Version
		if currentVersion != expectedVersion {
			return &RuntimeControlVersionConflict{
				Expected: expectedVersion,
				Current:  currentVersion,
			}
		}

		nextReadOnly := current.GlobalReadOnly
		if patch.GlobalReadOnly != nil {
			nextReadOnly = *patch.GlobalReadOnly
		}
		nextEmergency := current.EmergencyStop
		if patch.EmergencyStop != nil {
			nextEmergency = *patch.EmergencyStop
		}
		nextVersion := currentVersion + 1
		now := time.Now().UTC()
		anchor := tx.WithContext(ctx).
			Model(&models.SystemConfig{}).
			Where(
				"id = ? AND version = ?",
				emergencyRow.ID,
				emergencyRow.Version,
			).
			Updates(map[string]any{
				"value":      strconv.FormatBool(nextEmergency),
				"value_type": "bool",
				"is_active":  true,
				"updated_by": updatedBy,
				"version":    nextVersion,
				"updated_at": now,
			})
		if anchor.Error != nil {
			return anchor.Error
		}
		if anchor.RowsAffected != 1 {
			var current models.SystemConfig
			if err := tx.WithContext(ctx).
				Where("key = ?", agentEmergencyConfigKey).
				First(&current).Error; err != nil {
				return err
			}
			return &RuntimeControlVersionConflict{
				Expected: expectedVersion,
				Current:  positiveRuntimeControlVersion(current.Version),
			}
		}
		readOnlyUpdate := tx.WithContext(ctx).
			Model(&models.SystemConfig{}).
			Where("id = ?", readOnlyRow.ID).
			Updates(map[string]any{
				"value":      strconv.FormatBool(nextReadOnly),
				"value_type": "bool",
				"is_active":  true,
				"updated_by": updatedBy,
				"version":    nextVersion,
				"updated_at": now,
			})
		if readOnlyUpdate.Error != nil {
			return readOnlyUpdate.Error
		}
		if readOnlyUpdate.RowsAffected != 1 {
			return errors.New("runtime read-only control was not updated")
		}
		snapshot = RuntimeControlSnapshot{
			GlobalReadOnly: nextReadOnly,
			EmergencyStop:  nextEmergency,
			Version:        nextVersion,
			UpdatedAt:      now,
		}
		return nil
	})
	if err != nil {
		var conflict *RuntimeControlVersionConflict
		if !errors.As(err, &conflict) &&
			!errors.Is(err, ErrRuntimeControlPatchRequired) &&
			!errors.Is(err, ErrRuntimeControlHumanActorRequired) {
			c.failClosed()
		}
		return RuntimeControlSnapshot{}, err
	}
	c.setReadOnly(snapshot.GlobalReadOnly)
	c.setEmergencyStop(snapshot.EmergencyStop)
	c.healthy.Store(true)
	return snapshot, nil
}

// CompareAndSwapPlatformControls exposes the dedicated platform adapter
// contract without coupling the handlers package back to agentplatform types.
func (c *RuntimeControl) CompareAndSwapPlatformControls(
	ctx context.Context,
	expectedVersion uint64,
	globalReadOnly *bool,
	emergencyStop *bool,
	actor models.ActorRef,
) (bool, bool, uint64, time.Time, error) {
	snapshot, err := c.UpdateCAS(
		ctx,
		expectedVersion,
		RuntimeControlPatch{
			GlobalReadOnly: globalReadOnly,
			EmergencyStop:  emergencyStop,
		},
		actor,
	)
	return snapshot.GlobalReadOnly,
		snapshot.EmergencyStop,
		snapshot.Version,
		snapshot.UpdatedAt,
		err
}

func (c *RuntimeControl) ensureRuntimeControlRowsTx(
	ctx context.Context,
	tx *gorm.DB,
	updatedBy uint,
) error {
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	rows := []models.SystemConfig{
		{
			Key: agentReadOnlyConfigKey, Value: strconv.FormatBool(c.fallbackReadOnly),
			ValueType: "bool", Description: "Agent-native runtime safety control",
			Category: "security", Group: "agent", IsActive: true,
			DefaultValue: strconv.FormatBool(c.fallbackReadOnly),
			UpdatedBy:    userID, Version: 1,
		},
		{
			Key: agentEmergencyConfigKey, Value: "false",
			ValueType: "bool", Description: "Agent-native runtime safety control",
			Category: "security", Group: "agent", IsActive: true,
			DefaultValue: "false", UpdatedBy: userID, Version: 1,
		},
	}
	for i := range rows {
		if err := tx.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&rows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func (c *RuntimeControl) snapshotFromRows(
	rows []models.SystemConfig,
) (RuntimeControlSnapshot, error) {
	if len(rows) != 2 {
		return RuntimeControlSnapshot{},
			errors.New("runtime safety controls are incomplete")
	}
	snapshot := RuntimeControlSnapshot{}
	seenReadOnly := false
	seenEmergency := false
	var readOnlyVersion uint64
	var emergencyVersion uint64
	for i := range rows {
		row := rows[i]
		if !row.IsActive || row.ValueType != "bool" {
			return RuntimeControlSnapshot{},
				errors.New("runtime safety control metadata is invalid")
		}
		if row.Version < 1 {
			return RuntimeControlSnapshot{},
				errors.New("runtime safety control version is invalid")
		}
		enabled, err := parseRuntimeControlBool(row.Value)
		if err != nil {
			return RuntimeControlSnapshot{}, err
		}
		switch row.Key {
		case agentReadOnlyConfigKey:
			if seenReadOnly {
				return RuntimeControlSnapshot{},
					errors.New("runtime read-only control is duplicated")
			}
			seenReadOnly = true
			snapshot.GlobalReadOnly = enabled
			readOnlyVersion = positiveRuntimeControlVersion(row.Version)
			if row.UpdatedAt.After(snapshot.UpdatedAt) {
				snapshot.UpdatedAt = row.UpdatedAt
			}
		case agentEmergencyConfigKey:
			if seenEmergency {
				return RuntimeControlSnapshot{},
					errors.New("runtime emergency control is duplicated")
			}
			seenEmergency = true
			snapshot.EmergencyStop = enabled
			emergencyVersion = positiveRuntimeControlVersion(row.Version)
			if row.UpdatedAt.After(snapshot.UpdatedAt) {
				snapshot.UpdatedAt = row.UpdatedAt
			}
		default:
			return RuntimeControlSnapshot{},
				errors.New("unexpected runtime safety control key")
		}
	}
	if !seenReadOnly || !seenEmergency {
		return RuntimeControlSnapshot{},
			errors.New("runtime safety controls are incomplete")
	}
	if readOnlyVersion != emergencyVersion {
		return RuntimeControlSnapshot{},
			errors.New("runtime safety control versions are inconsistent")
	}
	snapshot.Version = emergencyVersion
	return snapshot, nil
}

func positiveRuntimeControlVersion(version int) uint64 {
	if version < 1 {
		return 1
	}
	return uint64(version)
}

func parseRuntimeControlBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("runtime safety control value is invalid")
	}
}

func (c *RuntimeControl) failClosed() {
	c.healthy.Store(false)
	c.setEmergencyStop(true)
}

func (c *RuntimeControl) Refresh(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errors.New("runtime control persistence is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var rows []models.SystemConfig
	if err := c.db.WithContext(ctx).
		Where("key IN ? AND is_active = ?", []string{agentReadOnlyConfigKey, agentEmergencyConfigKey}, true).
		Find(&rows).Error; err != nil {
		// Persistence is authoritative for the emergency switch. A stale open
		// value is unsafe, so any refresh failure immediately stops Agent writes.
		c.failClosed()
		return err
	}
	snapshot, err := c.snapshotFromRows(rows)
	if err != nil {
		c.failClosed()
		return err
	}
	c.setReadOnly(snapshot.GlobalReadOnly)
	c.setEmergencyStop(snapshot.EmergencyStop)
	c.healthy.Store(true)
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
	native   *services.AgentNativeService
	projects *services.ProjectService
}

func NewCredentialStore(
	native *services.AgentNativeService,
	projects *services.ProjectService,
) *CredentialStore {
	return &CredentialStore{native: native, projects: projects}
}

func (s *CredentialStore) AuthenticateClient(
	ctx context.Context,
	clientID string,
	clientSecret string,
	projectKey string,
) (*agentauth.Principal, error) {
	if s == nil || s.native == nil || s.projects == nil {
		return nil, services.ErrInvalidCredential
	}
	principal, credential, err := s.native.ValidateCredentialToken(ctx, clientSecret)
	if err != nil {
		return nil, err
	}
	if clientID != principal.ID {
		return nil, services.ErrInvalidCredential
	}
	projectAccess, err := s.projects.ResolvePrincipalProject(
		ctx,
		projectKey,
		principal.ID,
	)
	if err != nil {
		return nil, err
	}
	return &agentauth.Principal{
		ID:           principal.ID,
		CredentialID: credential.ID,
		ClientID:     principal.ID,
		Name:         principal.Name,
		Scopes:       intersectAgentScopes(principal.ScopeList(), projectAccess.Scopes),
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
	projectKey string,
	scopes []string,
) error {
	if s == nil || s.native == nil || s.projects == nil {
		return services.ErrInvalidCredential
	}
	if err := s.native.ValidateCredentialReference(ctx, principalID, credentialID); err != nil {
		return err
	}
	_, err := s.projects.ResolvePrincipalProject(ctx, projectKey, principalID, scopes...)
	return err
}

func intersectAgentScopes(globalScopes, projectScopes []string) []string {
	projectSet := make(map[string]struct{}, len(projectScopes))
	for _, scope := range projectScopes {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			projectSet[scope] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(globalScopes))
	intersection := make([]string, 0, len(globalScopes))
	for _, scope := range globalScopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, granted := projectSet[scope]; !granted {
			continue
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		intersection = append(intersection, scope)
	}
	return intersection
}

type AdminHandler struct {
	db            *gorm.DB
	native        *services.AgentNativeService
	control       *RuntimeControl
	lists         *AdminListService
	credentialTTL time.Duration
	replayCipher  cipher.AEAD
}

func NewAdminHandler(
	db *gorm.DB,
	native *services.AgentNativeService,
	control *RuntimeControl,
	credentialTTL time.Duration,
	replayEncryptionKey ...[]byte,
) *AdminHandler {
	if credentialTTL <= 0 {
		credentialTTL = 90 * 24 * time.Hour
	}
	handler := &AdminHandler{
		db:            db,
		native:        native,
		control:       control,
		credentialTTL: credentialTTL,
	}
	if len(replayEncryptionKey) > 0 && len(replayEncryptionKey[0]) > 0 {
		handler.replayCipher = newAdminReplayCipher(replayEncryptionKey[0])
	}
	return handler
}

func (h *AdminHandler) ConfigureListService(service *AdminListService) error {
	if h == nil || service == nil || service.db == nil {
		return errors.New("administrator list service is required")
	}
	if h.db != service.db {
		return errors.New("administrator list service database does not match handler")
	}
	h.lists = service
	return nil
}

func (h *AdminHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.Use(h.requireAdminCommandHeaders)
	group.GET("/agent-control/overview", h.OverviewMetrics)
	// Global read-only and emergency-stop are platform resources persisted in
	// SystemConfig. They are intentionally read-only from this project route:
	// a project-scoped command must never emit an unscoped 0/0 DomainEvent or
	// disguise a platform-wide mutation as project-local.
	group.GET("/service-principals", h.ListPrincipalsPage)
	group.POST("/service-principals", h.CreateServicePrincipal)
	group.PUT("/service-principals/:id/status", h.SetServicePrincipalStatus)
	group.POST("/service-principals/:id/credentials/rotate", h.RotateCredential)
	group.DELETE("/service-principals/:id/credentials/:credential_id", h.RevokeCredential)
	group.GET("/service-principals/:id/policies", h.ListPoliciesPage)
	group.POST("/service-principals/:id/policies", h.CreatePolicy)
	group.DELETE("/service-principals/:id/policies/:policy_id", h.DisablePolicy)
	group.GET("/leases", h.ListLeasesPage)
	group.POST("/leases/:id/force-release", h.ForceReleaseLease)
	group.GET("/attachments", h.ListAttachmentScansPage)
	group.POST("/attachments/:id/scan", h.MarkAttachmentScan)
	group.GET("/events", h.ListDomainEventsPage)
	group.GET("/outbox", h.ListOutboxPage)
	group.POST("/outbox/:id/replay", h.ReplayOutbox)
	group.POST(
		"/webhooks/:webhookID/emergency-revoke",
		h.EmergencyRevokeWebhook,
	)
	group.GET("/policy-decisions", h.ListPolicyDecisionsPage)
}

func (h *AdminHandler) requireProjectScope(
	c *gin.Context,
) (models.ProjectScope, bool) {
	if h == nil || h.db == nil {
		WriteProblem(
			c,
			http.StatusServiceUnavailable,
			ProblemServiceUnavailable,
			"Agent administrator project scope is unavailable",
			true,
		)
		return models.ProjectScope{}, false
	}
	operation, err := services.OperationContextFromContext(
		c.Request.Context(),
	)
	expectedActor := models.HumanActor(c.GetUint("user_id"))
	if err != nil ||
		operation.Actor != expectedActor ||
		operation.Source != services.SourceProtocolHumanREST {
		WriteProblem(
			c,
			http.StatusForbidden,
			ProblemPolicyDenied,
			"Trusted project scope is required",
			false,
		)
		return models.ProjectScope{}, false
	}
	scope := operation.Scope
	projectKey := strings.TrimSpace(c.Param("projectKey"))
	var matching int64
	if models.ValidateProjectKey(projectKey) != nil {
		WriteProblem(
			c,
			http.StatusNotFound,
			ProblemNotFound,
			"Project administrator resource was not found",
			false,
		)
		return models.ProjectScope{}, false
	}
	if err := h.db.WithContext(c.Request.Context()).
		Model(&models.Project{}).
		Where(
			"id = ? AND organization_id = ? AND key = ? AND status = ?",
			scope.ProjectID,
			scope.OrganizationID,
			projectKey,
			models.ProjectStatusActive,
		).
		Count(&matching).Error; err != nil {
		WriteProblem(
			c,
			http.StatusInternalServerError,
			ProblemInternal,
			"Failed to validate administrator project scope",
			true,
		)
		return models.ProjectScope{}, false
	}
	if matching != 1 {
		WriteProblem(
			c,
			http.StatusNotFound,
			ProblemNotFound,
			"Project administrator resource was not found",
			false,
		)
		return models.ProjectScope{}, false
	}
	return scope, true
}

func scopedAdminPrincipalQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Model(&models.ServicePrincipal{}).
		Select("service_principals.*").
		Joins(
			"JOIN project_principal_grants ON project_principal_grants.service_principal_id = service_principals.id",
		).
		Joins(
			"JOIN projects ON projects.id = project_principal_grants.project_id",
		).
		Where(
			"project_principal_grants.project_id = ? AND projects.organization_id = ? AND project_principal_grants.is_active = ?",
			scope.ProjectID,
			scope.OrganizationID,
			true,
		).
		Where(
			"project_principal_grants.expires_at IS NULL OR project_principal_grants.expires_at > ?",
			time.Now().UTC(),
		)
}

func requireScopedAdminPrincipal(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	principalID string,
) (*models.ServicePrincipal, error) {
	principalID = strings.TrimSpace(principalID)
	if principalID == "" {
		return nil, services.ErrPrincipalNotFound
	}
	var principal models.ServicePrincipal
	err := scopedAdminPrincipalQuery(db.WithContext(ctx), scope).
		Where("service_principals.id = ?", principalID).
		First(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, services.ErrPrincipalNotFound
	}
	if err != nil {
		return nil, err
	}
	return &principal, nil
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
		if _, err := httpcontract.ParseIfMatch(rawIfMatch); err != nil {
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
	h.OverviewMetrics(c)
}

func (h *AdminHandler) CreateServicePrincipal(c *gin.Context) {
	var request struct {
		Name             string     `json:"name" binding:"required,min=3,max=100"`
		Description      string     `json:"description" binding:"max=500"`
		Scopes           []string   `json:"scopes" binding:"required,min=1,unique"`
		RateLimit        int        `json:"rate_limit" binding:"omitempty,min=1,max=10000"`
		ConcurrencyLimit int        `json:"concurrency_limit" binding:"omitempty,min=1,max=100"`
		ExpiresAt        *time.Time `json:"expires_at"`
	}
	if err := bindAdminJSON(c, &request); err != nil {
		WriteProblem(c, http.StatusBadRequest, ProblemInvalidRequest, err.Error(), false)
		return
	}

	userID := c.GetUint("user_id")
	projectKey := strings.TrimSpace(c.Param("projectKey"))
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:                http.StatusCreated,
			ContainsOneTimeSecret: true,
			Request:               request,
		},
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			principal, err := h.native.CreateServicePrincipal(txCtx, services.CreateServicePrincipalInput{
				Name:               request.Name,
				Description:        request.Description,
				Scopes:             request.Scopes,
				RateLimitPerMinute: request.RateLimit,
				ConcurrentLimit:    request.ConcurrencyLimit,
				ExpiresAt:          request.ExpiresAt,
				CreatedByID:        &userID,
			})
			if err != nil {
				return adminMutationResult{}, err
			}
			projectService, err := services.NewProjectService(tx)
			if err != nil {
				return adminMutationResult{}, err
			}
			projectAccess, err := projectService.GrantPrincipalProject(
				txCtx,
				projectKey,
				principal.ID,
				models.ProjectRoleAgent,
				principal.ScopeList(),
				principal.ExpiresAt,
			)
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
					"project_key":   projectAccess.Project.Key,
				},
				EventName:     "service_principal.created",
				Subject:       "service-principal/" + principal.ID,
				ResourceID:    principal.ID,
				Scope:         projectAccess.Scope,
				ChangedFields: []string{"service_principal", "project_grant", "credentials"},
				PublicValues: gin.H{
					"status":               principal.Status,
					"scopes":               principal.ScopeList(),
					"project_key":          projectAccess.Project.Key,
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
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			if _, err := requireScopedAdminPrincipal(
				txCtx,
				tx,
				scope,
				principalID,
			); err != nil {
				return adminMutationResult{}, err
			}
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
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			if _, err := requireScopedAdminPrincipal(
				txCtx,
				tx,
				scope,
				c.Param("id"),
			); err != nil {
				return adminMutationResult{}, err
			}
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
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			existing, err := requireScopedAdminPrincipal(
				txCtx,
				tx,
				scope,
				c.Param("id"),
			)
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
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			if _, err := requireScopedAdminPrincipal(
				txCtx,
				tx,
				scope,
				c.Param("id"),
			); err != nil {
				return adminMutationResult{}, err
			}
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
	h.ListPoliciesPage(c)
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
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			if _, err := requireScopedAdminPrincipal(
				txCtx,
				tx,
				scope,
				c.Param("id"),
			); err != nil {
				return adminMutationResult{}, err
			}
			if _, err := h.native.SetAgentPolicyActive(
				txCtx,
				c.Param("id"),
				c.Param("policy_id"),
				false,
			); err != nil {
				return adminMutationResult{}, err
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
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			var delivery models.OutboxDelivery
			if err := tx.WithContext(txCtx).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					c.Param("id"),
					scope.OrganizationID,
					scope.ProjectID,
				).
				First(&delivery).Error; err != nil {
				return adminMutationResult{}, err
			}
			replay, err := h.native.ReplayOutboxCommand(
				txCtx,
				delivery.ID,
			)
			if err != nil {
				return adminMutationResult{}, err
			}
			if replay.Disposition == services.OutboxReplayExpired {
				if replay.Materialized {
					return adminMutationResult{
						AfterCommitError: services.ErrOutboxReplayExpired,
					}, nil
				}
				return adminMutationResult{},
					services.ErrOutboxReplayExpired
			}
			return adminMutationResult{
				Data:          gin.H{"replayed": true},
				EventName:     "outbox.replayed",
				Subject:       "outbox/" + c.Param("id"),
				ResourceID:    c.Param("id"),
				Scope:         scope,
				ChangedFields: []string{"status", "attempts", "next_attempt_at", "locked_at", "last_error", "delivered_at"},
				PublicValues:  gin.H{"status": models.OutboxDeliveryPending},
			}, nil
		},
	)
}

func (h *AdminHandler) EmergencyRevokeWebhook(c *gin.Context) {
	webhookID, err := strconv.ParseUint(
		strings.TrimSpace(c.Param("webhookID")),
		10,
		32,
	)
	if err != nil || webhookID == 0 {
		WriteProblem(
			c,
			http.StatusBadRequest,
			ProblemInvalidRequest,
			"Webhook ID must be a positive integer",
			false,
		)
		return
	}
	configID := uint(webhookID)
	subject := services.WebhookAdminSubject(configID)
	h.executeAdminMutation(
		c,
		adminMutationOptions{
			Status:              http.StatusOK,
			PreconditionSubject: subject,
			Request: gin.H{
				"webhook_id": configID,
			},
		},
		func(
			txCtx context.Context,
			_ *gorm.DB,
		) (adminMutationResult, error) {
			revoke, err := h.native.EmergencyRevokeWebhook(
				txCtx,
				configID,
			)
			if err != nil {
				return adminMutationResult{}, err
			}
			return adminMutationResult{
				Data:       revoke,
				EventName:  "webhook.emergency_revoked",
				Subject:    subject,
				ResourceID: strconv.FormatUint(webhookID, 10),
				ChangedFields: []string{
					"status",
					"delivery_status",
					"snapshot_credentials",
				},
				PublicValues: gin.H{
					"config_id":               revoke.ConfigID,
					"status":                  revoke.Status,
					"expired_deliveries":      revoke.ExpiredDeliveries,
					"in_flight_deliveries":    revoke.InFlightDeliveries,
					"shredded_snapshots":      revoke.ShreddedSnapshots,
					"credential_shred_reason": revoke.CredentialShredReason,
				},
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
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			var lease models.TicketLease
			if err := tx.WithContext(txCtx).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					leaseID,
					scope.OrganizationID,
					scope.ProjectID,
				).
				First(&lease).Error; err != nil {
				return adminMutationResult{}, err
			}
			reason := fmt.Sprintf("force released by administrator %d", c.GetUint("user_id"))
			now := time.Now().UTC()
			release := tx.WithContext(txCtx).
				Model(&models.TicketLease{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND released_at IS NULL",
					leaseID,
					scope.OrganizationID,
					scope.ProjectID,
				).
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
				Scope:         scope,
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
		func(txCtx context.Context, tx *gorm.DB) (adminMutationResult, error) {
			scope, err := services.RequireProjectScope(txCtx)
			if err != nil {
				return adminMutationResult{}, err
			}
			var attachment models.TicketAttachment
			if err := tx.WithContext(txCtx).
				Select("id", "organization_id", "project_id").
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					attachmentID,
					scope.OrganizationID,
					scope.ProjectID,
				).
				First(&attachment).Error; err != nil {
				return adminMutationResult{}, err
			}
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
				Scope:         scope,
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
	Data             any
	EventName        string
	Subject          string
	ResourceID       string
	Scope            models.ProjectScope
	ChangedFields    []string
	PublicValues     map[string]any
	AfterCommit      func()
	AfterCommitError error
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
	projectScope, ok := h.requireProjectScope(c)
	if !ok {
		return
	}
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
		expectedVersion, err = httpcontract.ParseIfMatch(rawIfMatch)
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
					projectScope,
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
			if result.AfterCommitError != nil {
				return nil
			}
			if strings.TrimSpace(result.Subject) == "" || strings.TrimSpace(result.ResourceID) == "" {
				return fmt.Errorf("%w: administrator mutation returned no resource identity", errAdminEventPersistence)
			}
			if result.Scope.IsZero() {
				result.Scope = projectScope
			}
			if result.Scope != projectScope {
				return fmt.Errorf(
					"%w: administrator mutation scope does not match trusted project",
					errAdminEventPersistence,
				)
			}
			resourceVersion := parentVersion
			parentETag := ""
			if options.PreconditionSubject == "" || options.PreconditionSubject != result.Subject {
				resourceVersion, err = h.initializeAdminResourceVersionTx(
					txCtx,
					tx,
					projectScope,
					result.Subject,
					c.GetUint("user_id"),
				)
				if err != nil {
					return err
				}
				if options.PreconditionSubject != "" {
					parentETag = httpcontract.FormatETag(parentVersion)
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
				httpcontract.FormatETag(receipt.ResourceVersion),
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
			c.Header("ETag", httpcontract.FormatETag(versionConflict.Current))
		} else if options.PreconditionSubject != "" && expectedVersion > 0 {
			c.Header("ETag", httpcontract.FormatETag(expectedVersion))
		}
		h.writeNativeError(c, err)
		return
	}
	if result.AfterCommitError != nil {
		_ = h.native.FailIdempotency(
			c.Request.Context(),
			reservation.Record.ID,
			services.AgentNativeErrorCode(result.AfterCommitError),
		)
		if err := queueAdminNativeErrorAfterProjectCommit(
			c,
			result.AfterCommitError,
			httpcontract.FormatETag(parentVersion),
		); err == nil {
			return
		}
		if middleware.ProjectAfterCommitQueueInstalled(c) {
			WriteProblem(
				c,
				http.StatusInternalServerError,
				ProblemInternal,
				"Failed to queue committed administrator outcome",
				true,
			)
			return
		}
		h.writeNativeError(c, result.AfterCommitError)
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
	if err := result.Scope.Validate(); err != nil {
		return Receipt{}, fmt.Errorf(
			"%w: administrator project scope is required: %v",
			errAdminEventPersistence,
			err,
		)
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
	eventContext, err := services.WithOperationContext(
		ctx,
		services.OperationContext{
			Scope:         result.Scope,
			Actor:         models.HumanActor(adminUserID),
			Source:        services.SourceProtocolHumanREST,
			TraceID:       requestID,
			CorrelationID: requestID,
		},
	)
	if err != nil {
		return Receipt{}, fmt.Errorf(
			"%w: bind administrator project scope: %v",
			errAdminEventPersistence,
			err,
		)
	}
	event, err := h.native.AppendDomainEventTx(
		eventContext,
		tx,
		services.DomainEventInput{
			Type:            "io.chronodesk.admin." + result.EventName + ".v1",
			Subject:         result.Subject,
			Data:            eventData,
			Scope:           result.Scope,
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
	scope models.ProjectScope,
	subject string,
	updatedBy uint,
) (uint64, error) {
	row := adminResourceVersionRow(scope, subject, 1, updatedBy)
	create := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row)
	if create.Error != nil {
		return 0, fmt.Errorf("%w: initialize administrator resource version: %v", errAdminEventPersistence, create.Error)
	}
	if create.RowsAffected == 1 {
		return 1, nil
	}
	current, err := h.currentAdminResourceVersion(ctx, tx, scope, subject)
	if err != nil {
		return 0, err
	}
	return 0, &adminVersionConflictError{Expected: 0, Current: current}
}

func (h *AdminHandler) compareAndSwapAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	subject string,
	expected uint64,
	updatedBy uint,
) (uint64, error) {
	if expected == 0 {
		return 0, &adminVersionConflictError{Expected: expected, Current: 1}
	}
	if err := h.ensureAdminResourceVersionTx(
		ctx,
		tx,
		scope,
		subject,
		updatedBy,
	); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	update := tx.WithContext(ctx).
		Model(&models.SystemConfig{}).
		Where(
			"key = ? AND version = ?",
			adminResourceVersionKey(scope, subject),
			expected,
		).
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
	current, err := h.currentAdminResourceVersion(ctx, tx, scope, subject)
	if err != nil {
		return 0, err
	}
	return 0, &adminVersionConflictError{Expected: expected, Current: current}
}

func (h *AdminHandler) ensureAdminResourceVersionTx(
	ctx context.Context,
	tx *gorm.DB,
	scope models.ProjectScope,
	subject string,
	updatedBy uint,
) error {
	var eventVersion uint64
	if err := tx.WithContext(ctx).
		Model(&models.DomainEvent{}).
		Select("COALESCE(MAX(resource_version), 0)").
		Where(
			"organization_id = ? AND project_id = ? AND subject = ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			subject,
			"io.chronodesk.admin.%",
		).
		Scan(&eventVersion).Error; err != nil {
		return fmt.Errorf("%w: load administrator event version: %v", errAdminEventPersistence, err)
	}
	if eventVersion == 0 {
		eventVersion = 1
	}
	row := adminResourceVersionRow(scope, subject, eventVersion, updatedBy)
	if err := tx.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error; err != nil {
		return fmt.Errorf("%w: initialize administrator resource version: %v", errAdminEventPersistence, err)
	}
	return nil
}

func adminResourceVersionRow(
	scope models.ProjectScope,
	subject string,
	version uint64,
	updatedBy uint,
) models.SystemConfig {
	var userID *uint
	if updatedBy > 0 {
		userID = &updatedBy
	}
	return models.SystemConfig{
		Key:          adminResourceVersionKey(scope, subject),
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

func adminResourceVersionKey(
	scope models.ProjectScope,
	subject string,
) string {
	scopeSubject := strconv.FormatUint(uint64(scope.OrganizationID), 10) +
		"/" +
		strconv.FormatUint(uint64(scope.ProjectID), 10) +
		"/" +
		strings.TrimSpace(subject)
	sum := sha256.Sum256([]byte(scopeSubject))
	return adminVersionKeyPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

func (h *AdminHandler) currentAdminResourceVersion(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	subject string,
) (uint64, error) {
	var row models.SystemConfig
	err := db.WithContext(ctx).
		Select("version").
		First(&row, "key = ?", adminResourceVersionKey(scope, subject)).Error
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
		Where(
			"organization_id = ? AND project_id = ? AND subject = ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			subject,
			"io.chronodesk.admin.%",
		).
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
	scope models.ProjectScope,
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
		key := adminResourceVersionKey(scope, subject)
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
		Where(
			"organization_id = ? AND project_id = ? AND subject IN ? AND type LIKE ?",
			scope.OrganizationID,
			scope.ProjectID,
			missing,
			"io.chronodesk.admin.%",
		).
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
	status, code, detail, retryable := adminNativeProblem(err)
	WriteProblem(c, status, code, detail, retryable)
}

func adminNativeProblem(
	err error,
) (int, string, string, bool) {
	code := services.AgentNativeErrorCode(err)
	status := http.StatusBadRequest
	retryable := false
	switch {
	case errors.Is(err, services.ErrPrincipalNotFound),
		errors.Is(err, services.ErrProjectNotFound),
		errors.Is(err, services.ErrWebhookConfigNotFound),
		errors.Is(err, gorm.ErrRecordNotFound):
		status, code = http.StatusNotFound, ProblemNotFound
	case errors.Is(err, services.ErrProjectAccessDenied):
		status, code = http.StatusForbidden, ProblemPolicyDenied
	case errors.Is(err, services.ErrProjectInactive):
		status, code = http.StatusForbidden, ProblemPolicyDenied
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
		err = errors.New("agent execution protection is temporarily unavailable")
	case errors.Is(err, services.ErrVersionConflict):
		status, code = http.StatusConflict, ProblemVersionConflict
	case errors.Is(err, services.ErrLeaseConflict), errors.Is(err, services.ErrLeaseExpired), errors.Is(err, services.ErrLeaseNotOwned):
		status, code = http.StatusConflict, ProblemLeaseConflict
	case errors.Is(err, services.ErrOutboxReplayConflict):
		status, code = http.StatusConflict, ProblemOutboxConflict
	case errors.Is(err, services.ErrOutboxReplayExpired):
		status, code = http.StatusConflict, ProblemOutboxExpired
	case errors.Is(err, services.ErrIdempotencyConflict), errors.Is(err, services.ErrIdempotencyInProgress):
		status, code = http.StatusConflict, ProblemIdempotencyConflict
	}
	return status, code, err.Error(), retryable
}

func queueAdminNativeErrorAfterProjectCommit(
	c *gin.Context,
	err error,
	etag string,
) error {
	status, code, detail, retryable := adminNativeProblem(err)
	body, marshalErr := json.Marshal(Problem{
		Type:      "https://chronodesk.local/problems/" + code,
		Title:     strings.ReplaceAll(code, "_", " "),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: RequestID(c),
		Retryable: retryable,
	})
	if marshalErr != nil {
		return marshalErr
	}
	header := make(http.Header)
	if strings.TrimSpace(etag) != "" {
		header.Set("ETag", etag)
	}
	return middleware.QueueProjectAfterCommitResponse(
		c,
		middleware.ProjectAfterCommitResponse{
			Status:      status,
			ContentType: "application/problem+json",
			Header:      header,
			Body:        body,
		},
	)
}
