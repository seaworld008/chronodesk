package services

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultAdminAuditExportLease = 2 * time.Minute
	adminAuditExportQueryBatch   = 1000
)

var adminAuditExportWorkerIDPattern = regexp.MustCompile(
	`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,95}$`,
)

type AdminAuditExportWorker struct {
	service       *AdminAuditExportService
	workerID      string
	leaseDuration time.Duration
	maxRows       int64
}

func NewAdminAuditExportWorker(
	service *AdminAuditExportService,
	workerID string,
) (*AdminAuditExportWorker, error) {
	workerID = strings.TrimSpace(workerID)
	if service == nil || service.db == nil || service.storage == nil {
		return nil, ErrAdminAuditExportUnavailable
	}
	if !adminAuditExportWorkerIDPattern.MatchString(workerID) {
		return nil, errors.New("audit export worker id is invalid")
	}
	return &AdminAuditExportWorker{
		service:       service,
		workerID:      workerID,
		leaseDuration: defaultAdminAuditExportLease,
		maxRows:       MaxAdminAuditExportRows,
	}, nil
}

// ProcessOne claims and processes at most one durable job. The returned bool is
// false when no eligible job exists.
func (worker *AdminAuditExportWorker) ProcessOne(
	ctx context.Context,
) (bool, error) {
	if worker == nil || worker.service == nil {
		return false, ErrAdminAuditExportUnavailable
	}
	job, err := worker.service.claimAdminAuditExport(
		ctx,
		worker.workerID,
		worker.leaseDuration,
	)
	if err != nil || job == nil {
		return false, err
	}
	filter, err := decodeAdminAuditExportFilter(
		job.FilterSnapshot,
		job.FilterHash,
	)
	if err != nil {
		worker.service.markFailed(
			context.WithoutCancel(ctx),
			*job,
			"generation_failed",
		)
		return true, nil
	}
	generated, stored, processErr := worker.generateAndStore(
		ctx,
		*job,
		filter,
	)
	if processErr != nil {
		if ctx.Err() != nil {
			// Shutdown/caller cancellation leaves the job fenced and leased.
			// A later worker can safely reclaim it only after lease expiry.
			return true, ctx.Err()
		}
		code := "generation_failed"
		switch {
		case errors.Is(processErr, errAdminAuditExportLeaseLost):
			code = "lease_lost"
		case errors.Is(processErr, errAdminAuditExportStorage):
			code = "storage_unavailable"
		case errors.Is(processErr, errAdminAuditExportQuery):
			code = "query_failed"
		}
		worker.service.markFailed(
			context.WithoutCancel(ctx),
			*job,
			code,
		)
		return true, nil
	}
	finalized, err := worker.service.finalizeAdminAuditExport(
		ctx,
		*job,
		generated,
		stored,
	)
	if err != nil || !finalized {
		_ = worker.service.storage.Delete(
			context.WithoutCancel(ctx),
			stored.Key,
		)
		if err != nil {
			return true, err
		}
		return true, nil
	}
	return true, nil
}

var (
	errAdminAuditExportLeaseLost = errors.New("audit export lease lost")
	errAdminAuditExportStorage   = errors.New("audit export storage failed")
	errAdminAuditExportQuery     = errors.New("audit export query failed")
)

type adminAuditExportGeneration struct {
	Rows      int64
	Truncated bool
}

type adminAuditExportGenerationResult struct {
	generation adminAuditExportGeneration
	err        error
}

func (worker *AdminAuditExportWorker) generateAndStore(
	ctx context.Context,
	job models.AdminAuditExportJob,
	filter *AdminAuditFilter,
) (
	adminAuditExportGeneration,
	*StoredAttachmentObject,
	error,
) {
	attemptID, err := uuid.NewV7()
	if err != nil {
		return adminAuditExportGeneration{}, nil,
			fmt.Errorf("%w: object id", errAdminAuditExportStorage)
	}
	objectKey := fmt.Sprintf(
		"audit-exports/%s/attempt-%d-%s.csv",
		job.PublicID,
		job.FencingToken,
		attemptID.String(),
	)
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatDone := make(chan struct{})
	heartbeatError := make(chan error, 1)
	var heartbeatOnce sync.Once
	stopHeartbeat := func() {
		heartbeatOnce.Do(func() { close(heartbeatDone) })
	}
	defer stopHeartbeat()
	go worker.heartbeat(
		workContext,
		job,
		heartbeatDone,
		heartbeatError,
		cancel,
	)

	reader, writer := io.Pipe()
	generationResult := make(chan adminAuditExportGenerationResult, 1)
	go func() {
		generation, generationErr := worker.streamCSV(
			workContext,
			job,
			filter,
			writer,
		)
		_ = writer.CloseWithError(generationErr)
		generationResult <- adminAuditExportGenerationResult{
			generation: generation,
			err:        generationErr,
		}
	}()
	stored, storageErr := worker.service.storage.Put(
		workContext,
		objectKey,
		reader,
		maxAdminAuditExportBytes,
	)
	if storageErr != nil {
		cancel()
		_ = reader.CloseWithError(storageErr)
	}
	generated := <-generationResult
	stopHeartbeat()
	cancel()
	var leaseErr error
	select {
	case leaseErr = <-heartbeatError:
	default:
	}
	if leaseErr != nil {
		if stored != nil {
			_ = worker.service.storage.Delete(
				context.WithoutCancel(ctx),
				stored.Key,
			)
		}
		return adminAuditExportGeneration{}, nil, leaseErr
	}
	if generated.err != nil {
		if stored != nil {
			_ = worker.service.storage.Delete(
				context.WithoutCancel(ctx),
				stored.Key,
			)
		}
		return adminAuditExportGeneration{}, nil, generated.err
	}
	if storageErr != nil || stored == nil {
		return adminAuditExportGeneration{}, nil,
			fmt.Errorf("%w: put", errAdminAuditExportStorage)
	}
	if stored.Key != objectKey ||
		stored.Size < 0 ||
		len(stored.SHA256) != 64 {
		_ = worker.service.storage.Delete(
			context.WithoutCancel(ctx),
			stored.Key,
		)
		return adminAuditExportGeneration{}, nil,
			fmt.Errorf("%w: invalid storage receipt", errAdminAuditExportStorage)
	}
	return generated.generation, stored, nil
}

func (worker *AdminAuditExportWorker) heartbeat(
	ctx context.Context,
	job models.AdminAuditExportJob,
	done <-chan struct{},
	result chan<- error,
	cancel context.CancelFunc,
) {
	interval := worker.leaseDuration / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := worker.service.renewAdminAuditExportLease(
				ctx,
				job,
				worker.leaseDuration,
			)
			if err == nil && ok {
				continue
			}
			if err == nil {
				err = errAdminAuditExportLeaseLost
			} else {
				err = fmt.Errorf("%w: heartbeat", errAdminAuditExportLeaseLost)
			}
			select {
			case result <- err:
			default:
			}
			cancel()
			return
		}
	}
}

func (worker *AdminAuditExportWorker) streamCSV(
	ctx context.Context,
	job models.AdminAuditExportJob,
	filter *AdminAuditFilter,
	destination io.Writer,
) (adminAuditExportGeneration, error) {
	csvWriter := csv.NewWriter(destination)
	if err := csvWriter.Write([]string{
		"time",
		"actor",
		"platform_role",
		"action",
		"http_method",
		"resource_path",
		"result",
		"status_code",
		"latency_ms",
		"masked_client_ip",
		"resource_type",
		"resource_public_id",
		"request_id",
		"trace_id",
		"correlation_id",
	}); err != nil {
		return adminAuditExportGeneration{}, fmt.Errorf(
			"%w: header",
			errAdminAuditExportStorage,
		)
	}
	cursorTime := job.AnchorCreatedAt
	cursorID := job.AnchorID
	generation := adminAuditExportGeneration{}
	for {
		if err := ctx.Err(); err != nil {
			return generation, ctx.Err()
		}
		var rows []models.AdminAuditLog
		query := worker.service.audit.filteredQuery(ctx, filter).
			Where(
				"(created_at < ? OR (created_at = ? AND id < ?))",
				cursorTime,
				cursorTime,
				cursorID,
			).
			Order("created_at DESC").
			Order("id DESC").
			Limit(adminAuditExportQueryBatch)
		if err := query.Find(&rows).Error; err != nil {
			return generation, fmt.Errorf(
				"%w: batch",
				errAdminAuditExportQuery,
			)
		}
		if len(rows) == 0 {
			break
		}
		for index := range rows {
			if generation.Rows >= worker.maxRows {
				generation.Truncated = true
				break
			}
			if err := csvWriter.Write(
				adminAuditExportCSVRow(&rows[index]),
			); err != nil {
				return generation, fmt.Errorf(
					"%w: row",
					errAdminAuditExportStorage,
				)
			}
			generation.Rows++
		}
		if generation.Truncated {
			break
		}
		last := rows[len(rows)-1]
		cursorTime = last.CreatedAt
		cursorID = last.ID
		if len(rows) < adminAuditExportQueryBatch {
			break
		}
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return generation, fmt.Errorf(
			"%w: flush",
			errAdminAuditExportStorage,
		)
	}
	return generation, nil
}

func adminAuditExportCSVRow(log *models.AdminAuditLog) []string {
	item := convertAuditLog(log)
	return []string{
		item.CreatedAt.UTC().Format(time.RFC3339Nano),
		safeAdminAuditExportCell(item.Username, 100),
		safeAdminAuditExportCell(string(item.PlatformRole), 30),
		safeAdminAuditExportCell(item.Action, 255),
		safeAdminAuditExportCell(item.Method, 20),
		safeAdminAuditExportCell(item.Path, 512),
		safeAdminAuditExportCell(item.Result, 100),
		strconv.Itoa(item.StatusCode),
		strconv.FormatInt(item.LatencyMs, 10),
		safeAdminAuditExportCell(item.MaskedIP, 64),
		safeAdminAuditExportCell(item.ResourceType, 100),
		safeAdminAuditExportCell(item.ResourcePublicID, 512),
		safeAdminAuditExportCell(log.RequestID, 128),
		safeAdminAuditExportCell(log.TraceID, 128),
		safeAdminAuditExportCell(log.CorrelationID, 255),
	}
}

func safeAdminAuditExportCell(value string, maxRunes int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = redactAuditText(value, maxRunes)
	first, _ := utf8.DecodeRuneInString(value)
	switch first {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func (service *AdminAuditExportService) claimAdminAuditExport(
	ctx context.Context,
	workerID string,
	leaseDuration time.Duration,
) (*models.AdminAuditExportJob, error) {
	if !adminAuditExportWorkerIDPattern.MatchString(workerID) ||
		leaseDuration < time.Second {
		return nil, errors.New("audit export lease configuration is invalid")
	}
	now := service.now()
	var claimed models.AdminAuditExportJob
	err := service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidate models.AdminAuditExportJob
		query := tx.Where(
			"state = ? OR (state = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)",
			models.AdminAuditExportQueued,
			models.AdminAuditExportProcessing,
			now,
		).Order(
			"requested_at ASC, id ASC",
		)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{
				Strength: "UPDATE",
				Options:  "SKIP LOCKED",
			})
		}
		if err := query.First(&candidate).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"state":            models.AdminAuditExportProcessing,
			"lease_owner":      workerID,
			"lease_expires_at": now.Add(leaseDuration),
			"fencing_token":    candidate.FencingToken + 1,
			"attempt":          candidate.Attempt + 1,
			"failure_code":     "",
		}
		if candidate.StartedAt == nil {
			updates["started_at"] = now
		}
		result := tx.Model(&models.AdminAuditExportJob{}).
			Where(
				"id = ? AND fencing_token = ? AND (state = ? OR (state = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
				candidate.ID,
				candidate.FencingToken,
				models.AdminAuditExportQueued,
				models.AdminAuditExportProcessing,
				now,
			).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errAdminAuditExportLeaseLost
		}
		return tx.First(&claimed, candidate.ID).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (service *AdminAuditExportService) renewAdminAuditExportLease(
	ctx context.Context,
	job models.AdminAuditExportJob,
	leaseDuration time.Duration,
) (bool, error) {
	now := service.now()
	result := service.db.WithContext(ctx).
		Model(&models.AdminAuditExportJob{}).
		Where(
			"id = ? AND state = ? AND fencing_token = ? AND lease_owner = ? AND lease_expires_at > ?",
			job.ID,
			models.AdminAuditExportProcessing,
			job.FencingToken,
			job.LeaseOwner,
			now,
		).
		Update("lease_expires_at", now.Add(leaseDuration))
	return result.RowsAffected == 1, result.Error
}

func (service *AdminAuditExportService) finalizeAdminAuditExport(
	ctx context.Context,
	job models.AdminAuditExportJob,
	generation adminAuditExportGeneration,
	stored *StoredAttachmentObject,
) (bool, error) {
	if stored == nil || generation.Rows < 0 ||
		generation.Rows > MaxAdminAuditExportRows {
		return false, errors.New("audit export finalization receipt is invalid")
	}
	now := service.now()
	result := service.db.WithContext(ctx).
		Model(&models.AdminAuditExportJob{}).
		Where(
			"id = ? AND state = ? AND fencing_token = ? AND lease_owner = ? AND lease_expires_at > ?",
			job.ID,
			models.AdminAuditExportProcessing,
			job.FencingToken,
			job.LeaseOwner,
			now,
		).
		Updates(map[string]any{
			"state":            models.AdminAuditExportCompleted,
			"completed_at":     now,
			"expires_at":       now.Add(AdminAuditExportTTL),
			"row_count":        generation.Rows,
			"truncated":        generation.Truncated,
			"object_key":       stored.Key,
			"sha256":           stored.SHA256,
			"size_bytes":       stored.Size,
			"failure_code":     "",
			"lease_owner":      "",
			"lease_expires_at": nil,
		})
	return result.RowsAffected == 1, result.Error
}
