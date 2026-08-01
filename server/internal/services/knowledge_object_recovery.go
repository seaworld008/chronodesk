package services

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	knowledgeObjectRecoveryGrace        = 15 * time.Minute
	knowledgeObjectRecoveryLease        = 2 * time.Minute
	knowledgeObjectRecoveryTimeout      = 30 * time.Second
	knowledgeObjectRecoveryDefaultBatch = 25
	knowledgeObjectRecoveryMaxBatch     = 100
	knowledgeObjectRecoveryMaxVersions  = 100
	knowledgeObjectRecoveryWorkerActor  = "knowledge-object-cleanup-worker"
)

var (
	ErrKnowledgeObjectCleanupDeferred = errors.New(
		"knowledge object cleanup was deferred for durable retry",
	)
	errKnowledgeObjectCleanupContinues = errors.New(
		"knowledge object cleanup has more exact versions",
	)
	knowledgeObjectRecoveryWorkerIDPattern = regexp.MustCompile(
		`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,95}$`,
	)
)

func authoredKnowledgeObjectKey(
	scope models.ProjectScope,
	articleID string,
	versionID string,
) string {
	return fmt.Sprintf(
		"knowledge/%d/%s/%s.md",
		scope.ProjectID,
		articleID,
		versionID,
	)
}

func (service *KnowledgeService) registerAuthoredObjectWriteIntent(
	ctx context.Context,
	operation OperationContext,
	articleID string,
	versionID string,
	markdown string,
) (*models.KnowledgeObjectWriteIntent, error) {
	if service == nil || service.db == nil || service.storage == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if err := operation.Validate(); err != nil {
		return nil, err
	}
	storeID := attachmentStorageStoreID(service.storage)
	provider := attachmentStorageType(service.storage)
	if !validAttachmentStoreID(storeID) ||
		strings.TrimSpace(provider) == "" {
		return nil, ErrAttachmentStorageMissing
	}
	now := service.now().UTC()
	intent := &models.KnowledgeObjectWriteIntent{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		ArticleID:      articleID,
		VersionID:      versionID,
		ObjectProvider: provider,
		ObjectStoreID:  storeID,
		ObjectKey: authoredKnowledgeObjectKey(
			operation.Scope,
			articleID,
			versionID,
		),
		SizeBytes:     int64(len(markdown)),
		ContentHash:   authoredMarkdownHash(markdown),
		CreatedByType: operation.Actor.Type,
		CreatedByID:   operation.Actor.ID,
		NextAttemptAt: now.Add(knowledgeObjectRecoveryGrace),
	}
	if err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			return service.db.WithContext(scopedContext).
				Create(intent).Error
		},
	); err != nil {
		return nil, fmt.Errorf(
			"register authored knowledge object recovery intent: %w",
			err,
		)
	}
	return intent, nil
}

func (service *KnowledgeService) recordAuthoredObjectWriteReceipt(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
	stored *StoredAttachmentObject,
) error {
	if stored == nil ||
		stored.Key != intent.ObjectKey ||
		stored.StoreID != intent.ObjectStoreID ||
		stored.StorageType != intent.ObjectProvider ||
		stored.Size != intent.SizeBytes ||
		!strings.EqualFold(stored.SHA256, intent.ContentHash) {
		return errors.New(
			"authored knowledge object receipt does not match recovery intent",
		)
	}
	recoveryContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		knowledgeObjectRecoveryTimeout,
	)
	defer cancel()
	return scopeddb.WithProjectScopeContextTransaction(
		recoveryContext,
		service.db,
		scope,
		func(scopedContext context.Context) error {
			result := service.db.WithContext(scopedContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND object_store_id = ? AND object_key = ? AND receipt_recorded = ?",
					intent.ID,
					scope.OrganizationID,
					scope.ProjectID,
					intent.ObjectStoreID,
					intent.ObjectKey,
					false,
				).
				Updates(map[string]any{
					"object_version_id": stored.VersionID,
					"receipt_recorded":  true,
					"updated_at":        service.now().UTC(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New(
					"authored knowledge object recovery intent is unavailable",
				)
			}
			return nil
		},
	)
}

func takeAuthoredObjectWriteIntentTx(
	tx *gorm.DB,
	operation OperationContext,
	intentID string,
	stored *StoredAttachmentObject,
	now time.Time,
) error {
	if tx == nil || stored == nil || strings.TrimSpace(intentID) == "" {
		return errors.New(
			"authored knowledge object recovery intent is required",
		)
	}
	var intent models.KnowledgeObjectWriteIntent
	if err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			intentID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(&intent).Error; err != nil {
		return fmt.Errorf(
			"lock authored knowledge object recovery intent: %w",
			err,
		)
	}
	if !intent.ReceiptRecorded ||
		intent.ObjectProvider != stored.StorageType ||
		intent.ObjectStoreID != stored.StoreID ||
		intent.ObjectKey != stored.Key ||
		intent.ObjectVersionID != stored.VersionID ||
		intent.SizeBytes != stored.Size ||
		!strings.EqualFold(intent.ContentHash, stored.SHA256) {
		return errors.New(
			"authored knowledge object recovery receipt changed",
		)
	}
	if intent.LeaseExpiresAt != nil &&
		intent.LeaseExpiresAt.After(now) {
		return errors.New(
			"authored knowledge object recovery intent is already claimed",
		)
	}
	result := tx.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		intent.ID,
		operation.Scope.OrganizationID,
		operation.Scope.ProjectID,
	).Delete(&models.KnowledgeObjectWriteIntent{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New(
			"authored knowledge object recovery intent was not taken",
		)
	}
	return nil
}

func (service *KnowledgeService) deferAndCleanupAuthoredObject(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
	stored *StoredAttachmentObject,
	cause error,
) error {
	recoveryContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		knowledgeObjectRecoveryTimeout,
	)
	defer cancel()
	now := service.now().UTC()
	// Make the row immediately eligible before crossing storage. A crash during
	// synchronous cleanup therefore still leaves a durable retry.
	_ = scopeddb.WithProjectScopeContextTransaction(
		recoveryContext,
		service.db,
		scope,
		func(scopedContext context.Context) error {
			return service.db.WithContext(scopedContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					intent.ID,
					scope.OrganizationID,
					scope.ProjectID,
				).
				Updates(map[string]any{
					"next_attempt_at": now,
					"failure_code":    "",
					"updated_at":      now,
				}).Error
		},
	)
	reference := AttachmentStoredReference{
		StorageType: intent.ObjectProvider,
		StoreID:     intent.ObjectStoreID,
		Key:         intent.ObjectKey,
	}
	if stored != nil {
		reference.VersionID = stored.VersionID
	}
	if reference.VersionID == "" {
		versioned, versioningErr :=
			attachmentStoredObjectVersioningEnabled(
				service.storage,
				reference,
			)
		if versioningErr != nil || versioned {
			// A receipt-less versioned write must be recovered by the worker,
			// which enumerates every exact generation under the intent-owned
			// UUID key. Resolving only the current generation could strand an
			// older committed retry.
			return errors.Join(
				cause,
				ErrKnowledgeObjectCleanupDeferred,
			)
		}
		resolved, err := resolveAttachmentStoredObjectVersion(
			recoveryContext,
			service.storage,
			reference,
		)
		if err != nil {
			if errors.Is(err, ErrAttachmentStoredObjectNotFound) {
				if clearErr := service.clearAuthoredObjectIntent(
					recoveryContext,
					scope,
					intent.ID,
				); clearErr == nil {
					return cause
				}
			}
			return errors.Join(
				cause,
				ErrKnowledgeObjectCleanupDeferred,
			)
		}
		reference = resolved
	}
	if err := deleteAttachmentStoredObject(
		recoveryContext,
		service.storage,
		reference,
	); err != nil {
		return errors.Join(
			cause,
			ErrKnowledgeObjectCleanupDeferred,
		)
	}
	if err := service.clearAuthoredObjectIntent(
		recoveryContext,
		scope,
		intent.ID,
	); err != nil {
		// The exact delete is idempotent. Retaining the row is safer than
		// pretending recovery state committed.
		return errors.Join(
			cause,
			ErrKnowledgeObjectCleanupDeferred,
		)
	}
	return cause
}

func (service *KnowledgeService) clearAuthoredObjectIntent(
	ctx context.Context,
	scope models.ProjectScope,
	intentID string,
) error {
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		scope,
		func(scopedContext context.Context) error {
			return service.db.WithContext(scopedContext).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					intentID,
					scope.OrganizationID,
					scope.ProjectID,
				).
				Delete(&models.KnowledgeObjectWriteIntent{}).Error
		},
	)
}

type KnowledgeObjectCleanupWorker struct {
	db            *gorm.DB
	storage       AttachmentStorage
	workerID      string
	leaseDuration time.Duration
	now           func() time.Time
}

type KnowledgeObjectCleanupResult struct {
	Claimed   int
	Cleaned   int
	Continued int
	Failed    int
}

func NewKnowledgeObjectCleanupWorker(
	db *gorm.DB,
	storage AttachmentStorage,
	workerID string,
) (*KnowledgeObjectCleanupWorker, error) {
	workerID = strings.TrimSpace(workerID)
	if db == nil || storage == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if !knowledgeObjectRecoveryWorkerIDPattern.MatchString(workerID) {
		return nil, errors.New(
			"knowledge object cleanup worker id is invalid",
		)
	}
	return &KnowledgeObjectCleanupWorker{
		db:            db,
		storage:       storage,
		workerID:      workerID,
		leaseDuration: knowledgeObjectRecoveryLease,
		now:           time.Now,
	}, nil
}

func (worker *KnowledgeObjectCleanupWorker) ProcessBatch(
	ctx context.Context,
	limit int,
) (KnowledgeObjectCleanupResult, error) {
	result := KnowledgeObjectCleanupResult{}
	if worker == nil || worker.db == nil || worker.storage == nil {
		return result, ErrAttachmentStorageMissing
	}
	if limit == 0 {
		limit = knowledgeObjectRecoveryDefaultBatch
	}
	if limit < 1 || limit > knowledgeObjectRecoveryMaxBatch {
		return result, errors.New(
			"knowledge object cleanup limit must be 1 to 100",
		)
	}
	projects, err := knowledgeObjectCleanupProjects(
		ctx,
		worker.db,
	)
	if err != nil {
		return result, err
	}
	remaining := limit
	var processErrors []error
	for _, scope := range projects {
		if remaining == 0 {
			break
		}
		projectResult, projectErr := worker.ProcessProject(
			ctx,
			scope,
			remaining,
		)
		result.Claimed += projectResult.Claimed
		result.Cleaned += projectResult.Cleaned
		result.Continued += projectResult.Continued
		result.Failed += projectResult.Failed
		remaining -= projectResult.Claimed
		if projectErr != nil {
			processErrors = append(
				processErrors,
				fmt.Errorf(
					"clean project %d knowledge objects: %w",
					scope.ProjectID,
					projectErr,
				),
			)
		}
	}
	return result, errors.Join(processErrors...)
}

func (worker *KnowledgeObjectCleanupWorker) ProcessProject(
	ctx context.Context,
	scope models.ProjectScope,
	limit int,
) (KnowledgeObjectCleanupResult, error) {
	result := KnowledgeObjectCleanupResult{}
	if worker == nil || worker.db == nil || worker.storage == nil {
		return result, ErrAttachmentStorageMissing
	}
	if err := scope.Validate(); err != nil {
		return result, err
	}
	if limit < 1 || limit > knowledgeObjectRecoveryMaxBatch {
		return result, errors.New(
			"knowledge object cleanup limit must be 1 to 100",
		)
	}
	claimed, err := worker.claimProjectIntents(ctx, scope, limit)
	if err != nil {
		return result, err
	}
	result.Claimed = len(claimed)
	var cleanupErrors []error
	for _, intent := range claimed {
		if err := worker.cleanupClaimedIntent(
			ctx,
			scope,
			intent,
		); err != nil {
			if errors.Is(
				err,
				errKnowledgeObjectCleanupContinues,
			) {
				result.Continued++
				continue
			}
			result.Failed++
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		result.Cleaned++
	}
	return result, errors.Join(cleanupErrors...)
}

func (worker *KnowledgeObjectCleanupWorker) claimProjectIntents(
	ctx context.Context,
	scope models.ProjectScope,
	limit int,
) ([]models.KnowledgeObjectWriteIntent, error) {
	actor := models.SystemActor(knowledgeObjectRecoveryWorkerActor)
	var claimed []models.KnowledgeObjectWriteIntent
	err := runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		actor,
		"",
		"",
		func(projectContext context.Context) error {
			now := worker.now().UTC()
			query := worker.db.WithContext(projectContext).
				Where(
					"organization_id = ? AND project_id = ? AND next_attempt_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)",
					scope.OrganizationID,
					scope.ProjectID,
					now,
					now,
				).
				Order("next_attempt_at ASC, id ASC").
				Limit(limit)
			if worker.db.Dialector.Name() == "postgres" {
				query = query.Clauses(clause.Locking{
					Strength: "UPDATE",
					Options:  "SKIP LOCKED",
				})
			}
			var candidates []models.KnowledgeObjectWriteIntent
			if err := query.Find(&candidates).Error; err != nil {
				return err
			}
			leaseExpiry := now.Add(worker.leaseDuration)
			for index := range candidates {
				candidate := candidates[index]
				result := worker.db.WithContext(projectContext).
					Model(&models.KnowledgeObjectWriteIntent{}).
					Where(
						"id = ? AND organization_id = ? AND project_id = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)",
						candidate.ID,
						scope.OrganizationID,
						scope.ProjectID,
						now,
					).
					Updates(map[string]any{
						"lease_owner":      worker.workerID,
						"lease_expires_at": leaseExpiry,
						"fencing_token": gorm.Expr(
							"fencing_token + 1",
						),
						"attempts": gorm.Expr(
							"attempts + 1",
						),
						"failure_code": "",
						"updated_at":   now,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					continue
				}
				candidate.LeaseOwner = worker.workerID
				candidate.LeaseExpiresAt = &leaseExpiry
				candidate.FencingToken++
				candidate.Attempts++
				candidate.FailureCode = ""
				claimed = append(claimed, candidate)
			}
			return nil
		},
	)
	return claimed, err
}

func (worker *KnowledgeObjectCleanupWorker) cleanupClaimedIntent(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
) error {
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		knowledgeObjectRecoveryTimeout,
	)
	defer cancel()
	reference := AttachmentStoredReference{
		StorageType: intent.ObjectProvider,
		StoreID:     intent.ObjectStoreID,
		Key:         intent.ObjectKey,
		VersionID:   intent.ObjectVersionID,
	}
	if !intent.ReceiptRecorded {
		return worker.cleanupUnrecordedWrite(
			cleanupContext,
			scope,
			intent,
			reference,
		)
	}
	if err := deleteAttachmentStoredObject(
		cleanupContext,
		worker.storage,
		reference,
	); err != nil {
		worker.failClaim(
			cleanupContext,
			scope,
			intent,
			"storage_unavailable",
		)
		return errors.New(
			"knowledge object cleanup storage operation failed",
		)
	}
	return worker.completeClaim(cleanupContext, scope, intent)
}

// cleanupUnrecordedWrite owns the UUID-derived key, not just the current
// provider generation. S3 can commit PutObject and return a VersionID one CPU
// instruction before process termination; enumerating and exact-deleting every
// generation under this never-published key closes that narrow dual-write
// window without creating a delete marker.
func (worker *KnowledgeObjectCleanupWorker) cleanupUnrecordedWrite(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
	reference AttachmentStoredReference,
) error {
	versioned, err := attachmentStoredObjectVersioningEnabled(
		worker.storage,
		reference,
	)
	if err != nil {
		worker.failClaim(
			ctx,
			scope,
			intent,
			"identity_unavailable",
		)
		return errors.New(
			"knowledge object cleanup storage identity unavailable",
		)
	}
	if !versioned {
		reference.VersionID = ""
		if err := deleteAttachmentStoredObject(
			ctx,
			worker.storage,
			reference,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"storage_unavailable",
			)
			return errors.New(
				"knowledge object cleanup storage operation failed",
			)
		}
		return worker.completeClaim(ctx, scope, intent)
	}

	processed := 0
	if reference.VersionID != "" {
		if err := deleteAttachmentStoredObject(
			ctx,
			worker.storage,
			reference,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"storage_unavailable",
			)
			return errors.New(
				"knowledge object cleanup storage operation failed",
			)
		}
		if err := worker.clearRecoveredVersion(
			ctx,
			scope,
			intent,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"database_unavailable",
			)
			return errors.New(
				"knowledge object cleanup receipt persistence failed",
			)
		}
		intent.ObjectVersionID = ""
		processed++
	}
	versions, hasMore, err := listAttachmentStoredObjectVersions(
		ctx,
		worker.storage,
		reference,
		knowledgeObjectRecoveryMaxVersions-processed,
	)
	if err != nil {
		worker.failClaim(
			ctx,
			scope,
			intent,
			"identity_unavailable",
		)
		return errors.New(
			"knowledge object cleanup version enumeration failed",
		)
	}
	for _, versionID := range versions {
		if err := worker.recordRecoveredVersion(
			ctx,
			scope,
			intent,
			versionID,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"database_unavailable",
			)
			return errors.New(
				"knowledge object cleanup receipt persistence failed",
			)
		}
		reference.VersionID = versionID
		if err := deleteAttachmentStoredObject(
			ctx,
			worker.storage,
			reference,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"storage_unavailable",
			)
			return errors.New(
				"knowledge object cleanup storage operation failed",
			)
		}
		if err := worker.clearRecoveredVersion(
			ctx,
			scope,
			intent,
		); err != nil {
			worker.failClaim(
				ctx,
				scope,
				intent,
				"database_unavailable",
			)
			return errors.New(
				"knowledge object cleanup receipt persistence failed",
			)
		}
		intent.ObjectVersionID = ""
		processed++
	}
	if hasMore || processed >= knowledgeObjectRecoveryMaxVersions {
		return worker.continueClaim(ctx, scope, intent)
	}
	return worker.completeClaim(ctx, scope, intent)
}

func (worker *KnowledgeObjectCleanupWorker) recordRecoveredVersion(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
	versionID string,
) error {
	return runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(knowledgeObjectRecoveryWorkerActor),
		"",
		"",
		func(projectContext context.Context) error {
			result := worker.db.WithContext(projectContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND lease_owner = ? AND fencing_token = ?",
					intent.ID,
					worker.workerID,
					intent.FencingToken,
				).
				Updates(map[string]any{
					"object_version_id": versionID,
					"updated_at":        worker.now().UTC(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New(
					"knowledge object cleanup lease was lost",
				)
			}
			return nil
		},
	)
}

func (worker *KnowledgeObjectCleanupWorker) clearRecoveredVersion(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
) error {
	return runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(knowledgeObjectRecoveryWorkerActor),
		"",
		"",
		func(projectContext context.Context) error {
			result := worker.db.WithContext(projectContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND lease_owner = ? AND fencing_token = ?",
					intent.ID,
					worker.workerID,
					intent.FencingToken,
				).
				Updates(map[string]any{
					"object_version_id": "",
					"updated_at":        worker.now().UTC(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New(
					"knowledge object cleanup lease was lost",
				)
			}
			return nil
		},
	)
}

func (worker *KnowledgeObjectCleanupWorker) continueClaim(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
) error {
	err := runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(knowledgeObjectRecoveryWorkerActor),
		"",
		"",
		func(projectContext context.Context) error {
			now := worker.now().UTC()
			result := worker.db.WithContext(projectContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND lease_owner = ? AND fencing_token = ?",
					intent.ID,
					worker.workerID,
					intent.FencingToken,
				).
				Updates(map[string]any{
					"lease_owner":      "",
					"lease_expires_at": nil,
					"next_attempt_at":  now,
					"failure_code":     "",
					"updated_at":       now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New(
					"knowledge object cleanup lease was lost",
				)
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	return errKnowledgeObjectCleanupContinues
}

func (worker *KnowledgeObjectCleanupWorker) completeClaim(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
) error {
	return runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(knowledgeObjectRecoveryWorkerActor),
		"",
		"",
		func(projectContext context.Context) error {
			result := worker.db.WithContext(projectContext).
				Where(
					"id = ? AND lease_owner = ? AND fencing_token = ?",
					intent.ID,
					worker.workerID,
					intent.FencingToken,
				).
				Delete(&models.KnowledgeObjectWriteIntent{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New(
					"knowledge object cleanup lease was lost",
				)
			}
			return nil
		},
	)
}

func (worker *KnowledgeObjectCleanupWorker) failClaim(
	ctx context.Context,
	scope models.ProjectScope,
	intent models.KnowledgeObjectWriteIntent,
	failureCode string,
) {
	failureCode = safeKnowledgeObjectCleanupFailureCode(failureCode)
	_ = runSystemProjectOperation(
		ctx,
		worker.db,
		scope,
		models.SystemActor(knowledgeObjectRecoveryWorkerActor),
		"",
		"",
		func(projectContext context.Context) error {
			now := worker.now().UTC()
			return worker.db.WithContext(projectContext).
				Model(&models.KnowledgeObjectWriteIntent{}).
				Where(
					"id = ? AND lease_owner = ? AND fencing_token = ?",
					intent.ID,
					worker.workerID,
					intent.FencingToken,
				).
				Updates(map[string]any{
					"lease_owner":      "",
					"lease_expires_at": nil,
					"next_attempt_at": now.Add(
						knowledgeObjectCleanupBackoff(
							intent.Attempts,
						),
					),
					"failure_code": failureCode,
					"updated_at":   now,
				}).Error
		},
	)
}

func knowledgeObjectCleanupBackoff(attempt uint) time.Duration {
	if attempt == 0 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 8 {
		exponent = 8
	}
	delay := time.Second * time.Duration(1<<exponent)
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func safeKnowledgeObjectCleanupFailureCode(value string) string {
	switch strings.TrimSpace(value) {
	case "storage_unavailable",
		"identity_unavailable",
		"database_unavailable":
		return strings.TrimSpace(value)
	default:
		return "storage_unavailable"
	}
}

func knowledgeObjectCleanupProjects(
	ctx context.Context,
	db *gorm.DB,
) ([]models.ProjectScope, error) {
	if ctx == nil || db == nil {
		return nil, ErrSystemWorkerContext
	}
	if operation, err := OperationContextFromContext(ctx); err == nil {
		if operation.Source != SourceProtocolWorker ||
			operation.Actor != models.SystemActor(
				knowledgeObjectRecoveryWorkerActor,
			) {
			return nil, ErrSystemWorkerContext
		}
		return []models.ProjectScope{operation.Scope}, nil
	}
	var projects []models.Project
	if err := db.WithContext(ctx).
		Select("id", "organization_id").
		Where(
			"status IN ?",
			[]models.ProjectStatus{
				models.ProjectStatusActive,
				models.ProjectStatusArchived,
			},
		).
		Order("organization_id ASC, id ASC").
		Limit(maxSystemWorkerProjects + 1).
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf(
			"list knowledge object cleanup projects: %w",
			err,
		)
	}
	if len(projects) > maxSystemWorkerProjects {
		return nil, ErrSystemWorkerProjectLimit
	}
	scopes := make([]models.ProjectScope, 0, len(projects))
	for index := range projects {
		scope := projects[index].Scope()
		if err := scope.Validate(); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}
