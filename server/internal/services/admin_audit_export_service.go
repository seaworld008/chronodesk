package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxAdminAuditExportRows  = int64(100000)
	MaxAdminAuditExportRange = 30 * 24 * time.Hour
	AdminAuditExportTTL      = 24 * time.Hour
	maxAdminAuditExportBytes = int64(256 << 20)
)

var (
	ErrAdminAuditExportUnavailable = errors.New(
		"admin audit export service is unavailable",
	)
	ErrAdminAuditExportUnauthorized = errors.New(
		"admin audit export is not authorized",
	)
	ErrAdminAuditExportInvalidRange = errors.New(
		"admin audit export time range is invalid",
	)
	ErrAdminAuditExportInvalidAnchor = errors.New(
		"admin audit export anchor is invalid",
	)
	ErrAdminAuditExportNotFound = errors.New(
		"admin audit export was not found",
	)
	ErrAdminAuditExportPending = errors.New(
		"admin audit export is not ready",
	)
	ErrAdminAuditExportFailed = errors.New(
		"admin audit export failed",
	)
	ErrAdminAuditExportExpired = errors.New(
		"admin audit export expired",
	)
)

// AuditExportStorage is a narrow, server-owned object storage boundary. It has
// no URL fetch or redirect method, so export bytes can only be written,
// streamed and deleted by trusted server code.
type AuditExportStorage interface {
	Put(
		context.Context,
		string,
		io.Reader,
		int64,
	) (*StoredAttachmentObject, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

type adminAuditExportFilterSnapshot struct {
	UserID       *uint               `json:"user_id,omitempty"`
	Actor        string              `json:"actor,omitempty"`
	PlatformRole models.PlatformRole `json:"platform_role,omitempty"`
	Action       string              `json:"action,omitempty"`
	Method       string              `json:"method,omitempty"`
	Path         string              `json:"path,omitempty"`
	Status       *int                `json:"status,omitempty"`
	Result       string              `json:"result,omitempty"`
	Keyword      string              `json:"keyword,omitempty"`
	StartTime    string              `json:"start_time"`
	EndTime      string              `json:"end_time"`
}

func (snapshot adminAuditExportFilterSnapshot) Filter() (
	*AdminAuditFilter,
	error,
) {
	start, err := time.Parse(time.RFC3339Nano, snapshot.StartTime)
	if err != nil {
		return nil, ErrAdminAuditExportInvalidRange
	}
	end, err := time.Parse(time.RFC3339Nano, snapshot.EndTime)
	if err != nil {
		return nil, ErrAdminAuditExportInvalidRange
	}
	return &AdminAuditFilter{
		UserID:       snapshot.UserID,
		Actor:        snapshot.Actor,
		PlatformRole: snapshot.PlatformRole,
		Action:       snapshot.Action,
		Method:       snapshot.Method,
		Path:         snapshot.Path,
		Status:       snapshot.Status,
		Result:       snapshot.Result,
		Keyword:      snapshot.Keyword,
		StartTime:    &start,
		EndTime:      &end,
	}, nil
}

// AdminAuditExportView is the only job projection published to browsers.
// Filter snapshots, object keys, lease owners and fencing tokens stay private.
type AdminAuditExportView struct {
	PublicID    string                       `json:"public_id"`
	State       models.AdminAuditExportState `json:"state"`
	RequestedAt time.Time                    `json:"requested_at"`
	StartedAt   *time.Time                   `json:"started_at,omitempty"`
	CompletedAt *time.Time                   `json:"completed_at,omitempty"`
	ExpiresAt   *time.Time                   `json:"expires_at,omitempty"`
	RowCount    int64                        `json:"row_count"`
	Truncated   bool                         `json:"truncated"`
	SHA256      string                       `json:"sha256,omitempty"`
	SizeBytes   int64                        `json:"size_bytes"`
	FailureCode string                       `json:"failure_code,omitempty"`
}

type AdminAuditExportDownload struct {
	Reader   io.ReadCloser
	Filename string
	Size     int64
	SHA256   string
}

type AdminAuditExportService struct {
	db      *gorm.DB
	storage AuditExportStorage
	audit   *AdminAuditService
	now     func() time.Time
}

func NewAdminAuditExportService(
	db *gorm.DB,
	storage AuditExportStorage,
	audit *AdminAuditService,
) (*AdminAuditExportService, error) {
	if db == nil || storage == nil || audit == nil {
		return nil, ErrAdminAuditExportUnavailable
	}
	return &AdminAuditExportService{
		db:      db,
		storage: storage,
		audit:   audit,
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

func (service *AdminAuditExportService) Create(
	ctx context.Context,
	requesterUserID uint,
	requesterRole models.PlatformRole,
	filter *AdminAuditFilter,
	anchorAuditID uint,
) (*AdminAuditExportView, error) {
	if service == nil || service.db == nil || service.audit == nil {
		return nil, ErrAdminAuditExportUnavailable
	}
	if requesterUserID == 0 || !adminAuditExportRoleAllowed(requesterRole) {
		return nil, ErrAdminAuditExportUnauthorized
	}
	snapshot, canonical, hash, err := canonicalAdminAuditExportFilter(filter)
	if err != nil {
		return nil, err
	}
	var job models.AdminAuditExportJob
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var anchor models.AdminAuditLog
		if anchorAuditID == 0 {
			return ErrAdminAuditExportInvalidAnchor
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			First(&anchor, anchorAuditID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAdminAuditExportInvalidAnchor
			}
			return err
		}
		if anchor.UserID == nil || *anchor.UserID != requesterUserID ||
			anchor.ActorType != models.ActorTypeHuman ||
			anchor.ActorID != strconv.FormatUint(
				uint64(requesterUserID),
				10,
			) ||
			anchor.ActionCode != "platform.audit_export.create" {
			return ErrAdminAuditExportInvalidAnchor
		}
		job = models.AdminAuditExportJob{
			RequesterUserID: requesterUserID,
			RequesterRole:   requesterRole,
			FilterSnapshot:  canonical,
			FilterHash:      hash,
			StartTime:       mustParseAuditExportTime(snapshot.StartTime),
			EndTime:         mustParseAuditExportTime(snapshot.EndTime),
			AnchorCreatedAt: anchor.CreatedAt,
			AnchorID:        anchor.ID,
			State:           models.AdminAuditExportQueued,
			RequestedAt:     service.now(),
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return adminAuditExportView(job), nil
}

func (service *AdminAuditExportService) Get(
	ctx context.Context,
	requesterUserID uint,
	publicID string,
) (*AdminAuditExportView, error) {
	job, err := service.ownerJob(ctx, requesterUserID, publicID)
	if err != nil {
		return nil, err
	}
	service.expireViewIfNeeded(ctx, &job)
	return adminAuditExportView(job), nil
}

func (service *AdminAuditExportService) Open(
	ctx context.Context,
	requesterUserID uint,
	publicID string,
) (*AdminAuditExportDownload, error) {
	job, err := service.ownerJob(ctx, requesterUserID, publicID)
	if err != nil {
		return nil, err
	}
	if service.jobExpired(job) {
		service.expireViewIfNeeded(ctx, &job)
		return nil, ErrAdminAuditExportExpired
	}
	switch job.State {
	case models.AdminAuditExportCompleted:
	case models.AdminAuditExportFailed:
		return nil, ErrAdminAuditExportFailed
	case models.AdminAuditExportExpired:
		return nil, ErrAdminAuditExportExpired
	default:
		return nil, ErrAdminAuditExportPending
	}
	if strings.TrimSpace(job.ObjectKey) == "" ||
		job.SizeBytes < 0 ||
		len(job.SHA256) != sha256.Size*2 {
		return nil, ErrAdminAuditExportFailed
	}
	reader, err := service.storage.Open(ctx, job.ObjectKey)
	if err != nil {
		return nil, ErrAdminAuditExportFailed
	}
	return &AdminAuditExportDownload{
		Reader:   reader,
		Filename: "chronodesk-audit-" + job.PublicID + ".csv",
		Size:     job.SizeBytes,
		SHA256:   job.SHA256,
	}, nil
}

func (service *AdminAuditExportService) ownerJob(
	ctx context.Context,
	requesterUserID uint,
	publicID string,
) (models.AdminAuditExportJob, error) {
	if service == nil || service.db == nil || requesterUserID == 0 ||
		!validAuditExportPublicID(publicID) {
		return models.AdminAuditExportJob{}, ErrAdminAuditExportNotFound
	}
	var job models.AdminAuditExportJob
	if err := service.db.WithContext(ctx).
		Where(
			"public_id = ? AND requester_user_id = ?",
			publicID,
			requesterUserID,
		).
		First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AdminAuditExportJob{}, ErrAdminAuditExportNotFound
		}
		return models.AdminAuditExportJob{}, err
	}
	return job, nil
}

func (service *AdminAuditExportService) expireViewIfNeeded(
	ctx context.Context,
	job *models.AdminAuditExportJob,
) {
	if job == nil || !service.jobExpired(*job) ||
		job.State == models.AdminAuditExportExpired {
		return
	}
	result := service.db.WithContext(ctx).
		Model(&models.AdminAuditExportJob{}).
		Where(
			"id = ? AND state = ?",
			job.ID,
			models.AdminAuditExportCompleted,
		).
		Update("state", models.AdminAuditExportExpired)
	if result.Error == nil && result.RowsAffected == 1 {
		job.State = models.AdminAuditExportExpired
	}
}

func (service *AdminAuditExportService) jobExpired(
	job models.AdminAuditExportJob,
) bool {
	return job.ExpiresAt != nil && !service.now().Before(*job.ExpiresAt)
}

func adminAuditExportView(
	job models.AdminAuditExportJob,
) *AdminAuditExportView {
	return &AdminAuditExportView{
		PublicID:    job.PublicID,
		State:       job.State,
		RequestedAt: job.RequestedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		ExpiresAt:   job.ExpiresAt,
		RowCount:    job.RowCount,
		Truncated:   job.Truncated,
		SHA256:      job.SHA256,
		SizeBytes:   job.SizeBytes,
		FailureCode: safeAuditExportFailureCode(job.FailureCode),
	}
}

func adminAuditExportRoleAllowed(role models.PlatformRole) bool {
	return role == models.PlatformRolePlatformAdmin ||
		role == models.PlatformRoleSecurityAuditor
}

func canonicalAdminAuditExportFilter(
	filter *AdminAuditFilter,
) (
	adminAuditExportFilterSnapshot,
	string,
	string,
	error,
) {
	if filter == nil || filter.StartTime == nil || filter.EndTime == nil {
		return adminAuditExportFilterSnapshot{}, "", "",
			ErrAdminAuditExportInvalidRange
	}
	start := filter.StartTime.UTC()
	end := filter.EndTime.UTC()
	if start.After(end) || end.Sub(start) > MaxAdminAuditExportRange {
		return adminAuditExportFilterSnapshot{}, "", "",
			ErrAdminAuditExportInvalidRange
	}
	if filter.PlatformRole != "" && !filter.PlatformRole.IsValid() {
		return adminAuditExportFilterSnapshot{}, "", "",
			errors.New("audit export platform role is invalid")
	}
	if filter.Page != 0 || filter.Limit != 0 || filter.Cursor != "" ||
		filter.TimePreset != "" {
		return adminAuditExportFilterSnapshot{}, "", "",
			errors.New("audit export pagination or preset is invalid")
	}
	snapshot := adminAuditExportFilterSnapshot{
		UserID:       filter.UserID,
		Actor:        strings.TrimSpace(filter.Actor),
		PlatformRole: filter.PlatformRole,
		Action:       strings.TrimSpace(filter.Action),
		Method:       strings.ToUpper(strings.TrimSpace(filter.Method)),
		Path:         strings.TrimSpace(filter.Path),
		Status:       filter.Status,
		Result:       strings.TrimSpace(filter.Result),
		Keyword:      strings.TrimSpace(filter.Keyword),
		StartTime:    start.Format(time.RFC3339Nano),
		EndTime:      end.Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return adminAuditExportFilterSnapshot{}, "", "", err
	}
	sum := sha256.Sum256(payload)
	return snapshot, string(payload), hex.EncodeToString(sum[:]), nil
}

func decodeAdminAuditExportFilter(
	payload string,
	hash string,
) (*AdminAuditFilter, error) {
	sum := sha256.Sum256([]byte(payload))
	if !strings.EqualFold(hex.EncodeToString(sum[:]), hash) {
		return nil, errors.New("audit export filter snapshot is corrupt")
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	var snapshot adminAuditExportFilterSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, errors.New("audit export filter snapshot is corrupt")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("audit export filter snapshot is corrupt")
	}
	return snapshot.Filter()
}

func mustParseAuditExportTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func validAuditExportPublicID(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	parsed, err := uuid.Parse(value)
	return err == nil &&
		parsed.Version() == 7 &&
		parsed.Variant() == uuid.RFC4122 &&
		parsed.String() == value
}

func safeAuditExportFailureCode(code string) string {
	switch strings.TrimSpace(code) {
	case "storage_unavailable",
		"query_failed",
		"generation_failed",
		"lease_lost":
		return strings.TrimSpace(code)
	case "":
		return ""
	default:
		return "generation_failed"
	}
}

func (service *AdminAuditExportService) markFailed(
	ctx context.Context,
	job models.AdminAuditExportJob,
	failureCode string,
) {
	_ = service.db.WithContext(ctx).
		Model(&models.AdminAuditExportJob{}).
		Where(
			"id = ? AND state = ? AND fencing_token = ? AND lease_owner = ?",
			job.ID,
			models.AdminAuditExportProcessing,
			job.FencingToken,
			job.LeaseOwner,
		).
		Updates(map[string]any{
			"state":            models.AdminAuditExportFailed,
			"failure_code":     safeAuditExportFailureCode(failureCode),
			"lease_owner":      "",
			"lease_expires_at": nil,
		}).Error
}

func (service *AdminAuditExportService) recordCleanupAudit(
	ctx context.Context,
	job models.AdminAuditExportJob,
	result string,
) {
	_ = service.audit.Record(ctx, &AdminAuditRecord{
		Actor:            models.SystemActor("audit-export-cleaner"),
		Username:         "audit-export-cleaner",
		Action:           "清理过期审计导出",
		ActionCode:       "platform.audit_export.cleanup",
		ResourceType:     "audit_export",
		ResourcePublicID: job.PublicID,
		Method:           "SYSTEM",
		Path:             "/internal/audit-exports/cleanup",
		StatusCode:       200,
		Result:           result,
	})
}

func (service *AdminAuditExportService) CleanupExpired(
	ctx context.Context,
	limit int,
) (int, error) {
	if service == nil || service.db == nil || service.storage == nil {
		return 0, ErrAdminAuditExportUnavailable
	}
	if limit < 1 || limit > 100 {
		return 0, errors.New("audit export cleanup limit must be 1 to 100")
	}
	now := service.now()
	var jobs []models.AdminAuditExportJob
	if err := service.db.WithContext(ctx).
		Where(
			"expires_at IS NOT NULL AND expires_at <= ? AND state IN ?",
			now,
			[]models.AdminAuditExportState{
				models.AdminAuditExportCompleted,
				models.AdminAuditExportExpired,
			},
		).
		Order("expires_at ASC, id ASC").
		Limit(limit).
		Find(&jobs).Error; err != nil {
		return 0, err
	}
	cleaned := 0
	var cleanupErrors []error
	for _, job := range jobs {
		if job.State == models.AdminAuditExportCompleted {
			result := service.db.WithContext(ctx).
				Model(&models.AdminAuditExportJob{}).
				Where(
					"id = ? AND state = ? AND expires_at <= ?",
					job.ID,
					models.AdminAuditExportCompleted,
					now,
				).
				Update("state", models.AdminAuditExportExpired)
			if result.Error != nil {
				cleanupErrors = append(cleanupErrors, result.Error)
				continue
			}
			if result.RowsAffected != 1 {
				continue
			}
		}
		if strings.TrimSpace(job.ObjectKey) != "" {
			if err := service.storage.Delete(ctx, job.ObjectKey); err != nil {
				cleanupErrors = append(
					cleanupErrors,
					fmt.Errorf("delete expired audit export: %w", err),
				)
				service.recordCleanupAudit(ctx, job, "error")
				continue
			}
		}
		if err := service.db.WithContext(ctx).
			Model(&models.AdminAuditExportJob{}).
			Where(
				"id = ? AND state = ?",
				job.ID,
				models.AdminAuditExportExpired,
			).
			Updates(map[string]any{
				"object_key": "",
			}).Error; err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		cleaned++
		service.recordCleanupAudit(ctx, job, "success")
	}
	return cleaned, errors.Join(cleanupErrors...)
}
