package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	webhookOutboxCleanupOperationTimeout = 5 * time.Second
	// Legacy succeeded rows are historical repair input. Scan a fixed page
	// even when the mutation limit is one so already-canonical terminal rows
	// cannot hide one repair indefinitely. The page is a global raw-row
	// budget shared across all projects in one cleanup invocation.
	webhookOutboxLegacyScanPageSize = 200
)

func (s *AgentNativeService) ExpireWebhookDeliveriesBatch(
	ctx context.Context,
	limit int,
) (WebhookOutboxCleanupResult, error) {
	result := WebhookOutboxCleanupResult{}
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
	if len(projects) == 0 {
		return result, nil
	}
	now := s.now().UTC()
	lockCutoff := now.Add(-s.outboxLockTTL)
	projectBudget := minInt(len(projects), limit)
	start := int(
		(s.outboxCleanupProjectCursor.Add(uint64(projectBudget)) -
			uint64(projectBudget)) %
			uint64(len(projects)),
	)
	queues := make(map[string][]webhookOutboxCleanupCandidate, len(projects))
	loaded := make(map[string]bool, len(projects))
	var cleanupErrors []error

	for result.Attempted < limit {
		progressed := false
		for offset := 0; offset < projectBudget &&
			result.Attempted < limit; offset++ {
			project := projects[(start+offset)%len(projects)]
			candidateLimit := partitionWebhookOutboxCleanupBudget(
				limit,
				projectBudget,
				offset,
			)
			legacyScanLimit := partitionWebhookOutboxCleanupBudget(
				webhookOutboxLegacyScanPageSize,
				projectBudget,
				offset,
			)
			key := fmt.Sprintf(
				"%d/%d",
				project.Scope.OrganizationID,
				project.Scope.ProjectID,
			)
			if !loaded[key] {
				cursor := s.webhookOutboxCleanupCursor(key)
				listCtx, cancelList := context.WithTimeout(
					ctx,
					webhookOutboxCleanupOperationTimeout,
				)
				candidates, listErr := s.listWebhookOutboxCleanupCandidates(
					listCtx,
					project,
					candidateLimit,
					legacyScanLimit,
					now,
					lockCutoff,
					cursor,
				)
				cancelList()
				loaded[key] = true
				if listErr != nil {
					cleanupErrors = append(
						cleanupErrors,
						ErrWebhookOutboxLifecycleInvariant,
					)
					continue
				}
				queues[key] = candidates
			}
			if len(queues[key]) == 0 {
				continue
			}
			candidate := queues[key][0]
			queues[key] = queues[key][1:]
			result.Attempted++
			progressed = true
			candidateCtx, cancelCandidate := context.WithTimeout(
				ctx,
				webhookOutboxCleanupOperationTimeout,
			)
			outcome, candidateErr :=
				s.processWebhookOutboxCleanupCandidate(
					candidateCtx,
					project,
					candidate,
					now,
					lockCutoff,
				)
			cancelCandidate()
			if candidateErr != nil {
				if errors.Is(
					candidateErr,
					errWebhookOutboxCleanupCandidateLost,
				) {
					// SKIP LOCKED and concurrent CAS loss are bounded scan
					// outcomes, not reasons to pin this class cursor forever.
					// Advance past the stable key now; the normal empty-page
					// wrap will revisit it after the competing lock releases.
					s.advanceWebhookOutboxCleanupCursor(key, candidate)
					continue
				}
				s.advanceWebhookOutboxCleanupCursor(key, candidate)
				result.Malformed++
				cleanupErrors = append(
					cleanupErrors,
					ErrWebhookOutboxLifecycleInvariant,
				)
				continue
			}
			s.advanceWebhookOutboxCleanupCursor(key, candidate)
			switch outcome {
			case webhookOutboxCleanupExpired:
				result.Expired++
			case webhookOutboxCleanupOverlapCleared:
				result.OverlapCleared++
			case webhookOutboxCleanupLegacySucceededShredded:
				result.LegacySucceededShredded++
			}
		}
		if !progressed {
			break
		}
	}
	return result, errors.Join(cleanupErrors...)
}

func partitionWebhookOutboxCleanupBudget(
	total int,
	parts int,
	index int,
) int {
	if total <= 0 || parts <= 0 || index < 0 || index >= parts {
		return 0
	}
	share := total / parts
	if index < total%parts {
		share++
	}
	return share
}

type webhookOutboxCleanupCandidateKind uint8

const (
	webhookOutboxCleanupCandidateExpire webhookOutboxCleanupCandidateKind = iota + 1
	webhookOutboxCleanupCandidateLegacySucceeded
	webhookOutboxCleanupCandidateOverlap
	webhookOutboxCleanupCandidateKindCount
)

type webhookOutboxCleanupCandidate struct {
	kind          webhookOutboxCleanupCandidateKind
	destinationID string
	stableID      string
	deliveryID    string
	snapshotID    string
	sortAt        time.Time
	status        models.OutboxDeliveryStatus
}

type webhookOutboxCleanupCursor struct {
	destinationID string
	stableID      string
	sortAt        time.Time
	status        models.OutboxDeliveryStatus
}

func (cursor webhookOutboxCleanupCursor) isZero() bool {
	return cursor.stableID == ""
}

type webhookOutboxCleanupProjectCursor struct {
	nextKind  webhookOutboxCleanupCandidateKind
	positions [webhookOutboxCleanupCandidateKindCount]webhookOutboxCleanupCursor
}

func (cursor webhookOutboxCleanupProjectCursor) position(
	kind webhookOutboxCleanupCandidateKind,
) webhookOutboxCleanupCursor {
	if kind == 0 || kind >= webhookOutboxCleanupCandidateKindCount {
		return webhookOutboxCleanupCursor{}
	}
	return cursor.positions[kind]
}

func (cursor webhookOutboxCleanupProjectCursor) firstKind() webhookOutboxCleanupCandidateKind {
	if cursor.nextKind == 0 ||
		cursor.nextKind >= webhookOutboxCleanupCandidateKindCount {
		return webhookOutboxCleanupCandidateExpire
	}
	return cursor.nextKind
}

func nextWebhookOutboxCleanupCandidateKind(
	kind webhookOutboxCleanupCandidateKind,
) webhookOutboxCleanupCandidateKind {
	next := kind + 1
	if next >= webhookOutboxCleanupCandidateKindCount {
		return webhookOutboxCleanupCandidateExpire
	}
	return next
}

func (s *AgentNativeService) webhookOutboxCleanupCursor(
	projectKey string,
) webhookOutboxCleanupProjectCursor {
	s.outboxCleanupCursorMu.Lock()
	defer s.outboxCleanupCursorMu.Unlock()
	if s.outboxCleanupCursors == nil {
		return webhookOutboxCleanupProjectCursor{}
	}
	return s.outboxCleanupCursors[projectKey]
}

func (s *AgentNativeService) advanceWebhookOutboxCleanupCursor(
	projectKey string,
	candidate webhookOutboxCleanupCandidate,
) {
	s.outboxCleanupCursorMu.Lock()
	defer s.outboxCleanupCursorMu.Unlock()
	if s.outboxCleanupCursors == nil {
		s.outboxCleanupCursors = make(
			map[string]webhookOutboxCleanupProjectCursor,
			1,
		)
	}
	cursor := s.outboxCleanupCursors[projectKey]
	cursor.positions[candidate.kind] = webhookOutboxCleanupCursor{
		destinationID: candidate.destinationID,
		stableID:      candidate.stableID,
		sortAt:        candidate.sortAt,
		status:        candidate.status,
	}
	cursor.nextKind = nextWebhookOutboxCleanupCandidateKind(candidate.kind)
	s.outboxCleanupCursors[projectKey] = cursor
}

func (s *AgentNativeService) advanceWebhookOutboxCleanupScanCursor(
	projectKey string,
	kind webhookOutboxCleanupCandidateKind,
	position webhookOutboxCleanupCursor,
) {
	if position.isZero() ||
		kind == 0 ||
		kind >= webhookOutboxCleanupCandidateKindCount {
		return
	}
	s.outboxCleanupCursorMu.Lock()
	defer s.outboxCleanupCursorMu.Unlock()
	if s.outboxCleanupCursors == nil {
		s.outboxCleanupCursors = make(
			map[string]webhookOutboxCleanupProjectCursor,
			1,
		)
	}
	cursor := s.outboxCleanupCursors[projectKey]
	current := cursor.positions[kind]
	if current.isZero() ||
		webhookOutboxCleanupCursorLess(kind, current, position) {
		cursor.positions[kind] = position
		s.outboxCleanupCursors[projectKey] = cursor
	}
}

func webhookOutboxCleanupCursorLess(
	kind webhookOutboxCleanupCandidateKind,
	left webhookOutboxCleanupCursor,
	right webhookOutboxCleanupCursor,
) bool {
	switch kind {
	case webhookOutboxCleanupCandidateExpire:
		if left.status != right.status {
			return left.status < right.status
		}
		if !left.sortAt.Equal(right.sortAt) {
			return left.sortAt.Before(right.sortAt)
		}
		if left.destinationID != right.destinationID {
			return left.destinationID < right.destinationID
		}
		return left.stableID < right.stableID
	case webhookOutboxCleanupCandidateOverlap:
		if !left.sortAt.Equal(right.sortAt) {
			return left.sortAt.Before(right.sortAt)
		}
		return left.stableID < right.stableID
	case webhookOutboxCleanupCandidateLegacySucceeded:
		if left.destinationID != right.destinationID {
			return left.destinationID < right.destinationID
		}
		return left.stableID < right.stableID
	default:
		return false
	}
}

func (s *AgentNativeService) resetWebhookOutboxCleanupScanCursor(
	projectKey string,
	kind webhookOutboxCleanupCandidateKind,
	expected webhookOutboxCleanupCursor,
) {
	if expected.isZero() ||
		kind == 0 ||
		kind >= webhookOutboxCleanupCandidateKindCount {
		return
	}
	s.outboxCleanupCursorMu.Lock()
	defer s.outboxCleanupCursorMu.Unlock()
	cursor, exists := s.outboxCleanupCursors[projectKey]
	if !exists {
		return
	}
	current := cursor.positions[kind]
	if current != expected {
		return
	}
	cursor.positions[kind] = webhookOutboxCleanupCursor{}
	s.outboxCleanupCursors[projectKey] = cursor
}

type webhookOutboxCleanupOutcome uint8

const (
	webhookOutboxCleanupNoChange webhookOutboxCleanupOutcome = iota
	webhookOutboxCleanupExpired
	webhookOutboxCleanupOverlapCleared
	webhookOutboxCleanupLegacySucceededShredded
)

var errWebhookOutboxCleanupCandidateLost = errors.New(
	"webhook outbox cleanup candidate changed",
)

func (s *AgentNativeService) listWebhookOutboxCleanupCandidates(
	ctx context.Context,
	project outboxWorkerProject,
	limit int,
	legacyScanLimit int,
	now time.Time,
	lockCutoff time.Time,
	cursor webhookOutboxCleanupProjectCursor,
) ([]webhookOutboxCleanupCandidate, error) {
	var candidates []webhookOutboxCleanupCandidate
	var expiryScanAdvance webhookOutboxCleanupCursor
	var expiryScanReset webhookOutboxCleanupCursor
	var legacyScanAdvance webhookOutboxCleanupCursor
	var legacyScanReset webhookOutboxCleanupCursor
	traceID := fmt.Sprintf(
		"outbox-cleanup-list:%d:%d",
		project.Scope.ProjectID,
		now.UnixNano(),
	)
	projectCtx, err := EnsureSystemProjectOperationContext(
		ctx,
		project.Scope,
		models.SystemActor(outboxSystemActorID),
		traceID,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	err = runSystemProjectOperation(
		projectCtx,
		s.db,
		project.Scope,
		models.SystemActor(outboxSystemActorID),
		traceID,
		traceID,
		func(scopedCtx context.Context) error {
			return transactionForContext(
				scopedCtx,
				s.db,
				func(tx *gorm.DB) error {
					if err := lockWebhookLifecycleProject(
						tx,
						project.Scope,
					); err != nil {
						return err
					}
					var due []models.OutboxDelivery
					expiryCursor := cursor.position(
						webhookOutboxCleanupCandidateExpire,
					)
					dueQuery := buildWebhookExpiryCandidateQuery(
						tx.Model(&models.OutboxDelivery{}),
						project.Scope,
						now,
						lockCutoff,
						expiryCursor,
						limit,
					)
					if err := dueQuery.Find(&due).Error; err != nil {
						return err
					}
					if len(due) == 0 && !expiryCursor.isZero() {
						expiryScanReset = expiryCursor
						if err := buildWebhookExpiryCandidateQuery(
							tx.Model(&models.OutboxDelivery{}),
							project.Scope,
							now,
							lockCutoff,
							webhookOutboxCleanupCursor{},
							limit,
						).Find(&due).Error; err != nil {
							return err
						}
					}
					dueIDs := make([]string, 0, len(due))
					for index := range due {
						dueIDs = append(dueIDs, due[index].ID)
					}
					var eligibleDue []models.OutboxDelivery
					if len(dueIDs) > 0 {
						if err := buildWebhookExpiryEligiblePageQuery(
							tx.Model(&models.OutboxDelivery{}),
							project.Scope,
							now,
							lockCutoff,
							dueIDs,
						).Find(&eligibleDue).Error; err != nil {
							return err
						}
					}
					eligibleDueByID := make(
						map[string]models.OutboxDelivery,
						len(eligibleDue),
					)
					for index := range eligibleDue {
						eligibleDueByID[eligibleDue[index].ID] =
							eligibleDue[index]
					}
					encounteredDueCandidate := false
					for index := range due {
						eligible, exists :=
							eligibleDueByID[due[index].ID]
						if !exists {
							if !encounteredDueCandidate {
								expiryScanAdvance =
									webhookOutboxCleanupCursor{
										destinationID: due[index].
											DestinationID,
										stableID: due[index].ID,
										sortAt: func() time.Time {
											if due[index].ExpiresAt == nil {
												return time.Time{}
											}
											return due[index].
												ExpiresAt.UTC()
										}(),
										status: due[index].Status,
									}
							}
							continue
						}
						encounteredDueCandidate = true
						snapshotID, _ :=
							models.ParseWebhookDeliverySnapshotDestinationID(
								eligible.DestinationID,
							)
						candidates = append(
							candidates,
							webhookOutboxCleanupCandidate{
								kind:          webhookOutboxCleanupCandidateExpire,
								destinationID: eligible.DestinationID,
								stableID:      eligible.ID,
								deliveryID:    eligible.ID,
								snapshotID:    snapshotID,
								sortAt: func() time.Time {
									if eligible.ExpiresAt == nil {
										return time.Time{}
									}
									return eligible.ExpiresAt.UTC()
								}(),
								status: eligible.Status,
							},
						)
					}

					var succeeded []webhookLegacySucceededScanRow
					legacyCursor := cursor.position(
						webhookOutboxCleanupCandidateLegacySucceeded,
					)
					if err := buildWebhookLegacySucceededCandidateQuery(
						tx.Model(&models.OutboxDelivery{}),
						project.Scope,
						legacyCursor,
						minInt(
							webhookOutboxLegacyScanPageSize,
							legacyScanLimit,
						),
					).Find(&succeeded).Error; err != nil {
						return err
					}
					if len(succeeded) == 0 &&
						!legacyCursor.isZero() {
						legacyScanReset = legacyCursor
					}
					encounteredRepair := false
					for index := range succeeded {
						if succeeded[index].SnapshotShredded &&
							!encounteredRepair {
							legacyScanAdvance =
								webhookOutboxCleanupCursor{
									destinationID: succeeded[index].
										DestinationID,
									stableID: succeeded[index].ID,
								}
							continue
						}
						if succeeded[index].SnapshotShredded {
							continue
						}
						encounteredRepair = true
						snapshotID, _ :=
							models.ParseWebhookDeliverySnapshotDestinationID(
								succeeded[index].DestinationID,
							)
						candidates = append(
							candidates,
							webhookOutboxCleanupCandidate{
								kind:          webhookOutboxCleanupCandidateLegacySucceeded,
								destinationID: succeeded[index].DestinationID,
								stableID:      succeeded[index].ID,
								deliveryID:    succeeded[index].ID,
								snapshotID:    snapshotID,
							},
						)
					}

					var overlap []models.WebhookDeliverySnapshot
					overlapCursor := cursor.position(
						webhookOutboxCleanupCandidateOverlap,
					)
					overlapQuery := buildWebhookOverlapCandidateQuery(
						tx.Model(&models.WebhookDeliverySnapshot{}),
						project.Scope,
						now,
						overlapCursor,
						limit,
					)
					if err := overlapQuery.Find(&overlap).Error; err != nil {
						return err
					}
					if len(overlap) == 0 && !overlapCursor.isZero() {
						if err := buildWebhookOverlapCandidateQuery(
							tx.Model(&models.WebhookDeliverySnapshot{}),
							project.Scope,
							now,
							webhookOutboxCleanupCursor{},
							limit,
						).Find(&overlap).Error; err != nil {
							return err
						}
					}
					for index := range overlap {
						sortAt := time.Time{}
						if overlap[index].PreviousSecretExpiresAt != nil {
							sortAt = overlap[index].
								PreviousSecretExpiresAt.UTC()
						}
						candidates = append(
							candidates,
							webhookOutboxCleanupCandidate{
								kind:       webhookOutboxCleanupCandidateOverlap,
								stableID:   overlap[index].ID,
								snapshotID: overlap[index].ID,
								sortAt:     sortAt,
							},
						)
					}
					return nil
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	projectKey := fmt.Sprintf(
		"%d/%d",
		project.Scope.OrganizationID,
		project.Scope.ProjectID,
	)
	if !expiryScanReset.isZero() {
		s.resetWebhookOutboxCleanupScanCursor(
			projectKey,
			webhookOutboxCleanupCandidateExpire,
			expiryScanReset,
		)
	}
	if !expiryScanAdvance.isZero() {
		s.advanceWebhookOutboxCleanupScanCursor(
			projectKey,
			webhookOutboxCleanupCandidateExpire,
			expiryScanAdvance,
		)
	}
	if !legacyScanAdvance.isZero() {
		s.advanceWebhookOutboxCleanupScanCursor(
			projectKey,
			webhookOutboxCleanupCandidateLegacySucceeded,
			legacyScanAdvance,
		)
	}
	if !legacyScanReset.isZero() {
		s.resetWebhookOutboxCleanupScanCursor(
			projectKey,
			webhookOutboxCleanupCandidateLegacySucceeded,
			legacyScanReset,
		)
	}
	var queues [webhookOutboxCleanupCandidateKindCount][]webhookOutboxCleanupCandidate
	for _, candidate := range candidates {
		queues[candidate.kind] = append(
			queues[candidate.kind],
			candidate,
		)
	}
	unique := make(
		[]webhookOutboxCleanupCandidate,
		0,
		minInt(len(candidates), limit),
	)
	seenSnapshots := make(map[string]struct{}, len(candidates))
	startKind := cursor.firstKind()
	for offset := 0; offset <
		int(webhookOutboxCleanupCandidateKindCount)-1 &&
		len(unique) < limit; offset++ {
		kind := webhookOutboxCleanupCandidateKind(
			((int(startKind) - 1 + offset) %
				(int(webhookOutboxCleanupCandidateKindCount) - 1)) + 1,
		)
		for len(queues[kind]) > 0 && len(unique) < limit {
			candidate := queues[kind][0]
			queues[kind] = queues[kind][1:]
			if candidate.snapshotID != "" {
				if _, exists :=
					seenSnapshots[candidate.snapshotID]; exists {
					continue
				}
				seenSnapshots[candidate.snapshotID] = struct{}{}
			}
			unique = append(unique, candidate)
		}
	}
	return unique, nil
}

func (s *AgentNativeService) processWebhookOutboxCleanupCandidate(
	ctx context.Context,
	project outboxWorkerProject,
	candidate webhookOutboxCleanupCandidate,
	now time.Time,
	lockCutoff time.Time,
) (webhookOutboxCleanupOutcome, error) {
	outcome := webhookOutboxCleanupNoChange
	traceID := fmt.Sprintf(
		"outbox-cleanup:%d:%d",
		project.Scope.ProjectID,
		now.UnixNano(),
	)
	projectCtx, err := EnsureSystemProjectOperationContext(
		ctx,
		project.Scope,
		models.SystemActor(outboxSystemActorID),
		traceID,
		traceID,
	)
	if err != nil {
		return outcome, err
	}
	err = runSystemProjectOperation(
		projectCtx,
		s.db,
		project.Scope,
		models.SystemActor(outboxSystemActorID),
		traceID,
		traceID,
		func(scopedCtx context.Context) error {
			return transactionForContext(
				scopedCtx,
				s.db,
				func(tx *gorm.DB) error {
					if err := lockWebhookLifecycleProject(
						tx,
						project.Scope,
					); err != nil {
						return err
					}
					if _, err := lockWebhookConfigForSnapshot(
						tx,
						project.Scope,
						candidate.snapshotID,
					); err != nil {
						return err
					}
					switch candidate.kind {
					case webhookOutboxCleanupCandidateExpire:
						changed, err := expireWebhookOutboxCandidate(
							tx,
							project.Scope,
							candidate,
							now,
							lockCutoff,
						)
						if changed {
							outcome = webhookOutboxCleanupExpired
						}
						return err
					case webhookOutboxCleanupCandidateLegacySucceeded:
						changed, err :=
							shredLegacySucceededWebhookCandidate(
								tx,
								project.Scope,
								candidate,
								now,
							)
						if changed {
							outcome =
								webhookOutboxCleanupLegacySucceededShredded
						}
						return err
					case webhookOutboxCleanupCandidateOverlap:
						changed, err := clearWebhookSnapshotOverlap(
							tx,
							project.Scope,
							candidate,
							now,
						)
						if changed {
							outcome = webhookOutboxCleanupOverlapCleared
						}
						return err
					default:
						return ErrWebhookOutboxLifecycleInvariant
					}
				},
			)
		},
	)
	return outcome, err
}

func expireWebhookOutboxCandidate(
	tx *gorm.DB,
	scope models.ProjectScope,
	candidate webhookOutboxCleanupCandidate,
	now time.Time,
	lockCutoff time.Time,
) (bool, error) {
	var delivery models.OutboxDelivery
	err := tx.Clauses(webhookOutboxCleanupLocking(tx)).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			candidate.deliveryID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&delivery).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	if err != nil {
		return false, err
	}
	if !webhookOutboxDeliveryIsCleanupDue(
		&delivery,
		now,
		lockCutoff,
	) {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	snapshot, err := lockWebhookSnapshotForDelivery(tx, &delivery)
	if err != nil {
		return false, ErrWebhookOutboxLifecycleInvariant
	}
	update := tx.Model(&models.OutboxDelivery{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND destination_type = ? AND destination_id = ? "+
				"AND expires_at = ? AND expires_at <= ? AND status = ?",
			delivery.ID,
			scope.OrganizationID,
			scope.ProjectID,
			"webhook",
			delivery.DestinationID,
			delivery.ExpiresAt.UTC(),
			now,
			delivery.Status,
		)
	if delivery.Status == models.OutboxDeliveryProcessing {
		update = update.Where(
			"locked_at IS NULL OR TRIM(locked_by) = '' OR locked_at < ?",
			lockCutoff,
		)
	}
	result := update.Updates(map[string]any{
		"status":       models.OutboxDeliveryExpired,
		"expired_at":   now,
		"delivered_at": nil,
		"locked_at":    nil,
		"locked_by":    "",
		"lock_token":   nil,
		"last_error":   "webhook delivery credential deadline expired",
		"updated_at":   now,
	})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	if snapshot.CredentialShreddedAt == nil {
		if err := shredWebhookSnapshot(
			tx,
			snapshot,
			models.WebhookCredentialShredReasonExpired,
			now,
		); err != nil {
			return false, err
		}
	}
	if err := clearOutboxEventPublication(
		tx,
		scope,
		delivery.EventID,
	); err != nil {
		return false, err
	}
	return true, nil
}

func webhookOutboxDeliveryIsCleanupDue(
	delivery *models.OutboxDelivery,
	now time.Time,
	lockCutoff time.Time,
) bool {
	if delivery == nil ||
		delivery.DestinationType != "webhook" ||
		delivery.ExpiresAt == nil ||
		delivery.ExpiresAt.After(now) {
		return false
	}
	switch delivery.Status {
	case models.OutboxDeliveryPending,
		models.OutboxDeliveryFailed,
		models.OutboxDeliveryDead:
		return true
	case models.OutboxDeliveryProcessing:
		return delivery.LockedAt == nil ||
			strings.TrimSpace(delivery.LockedBy) == "" ||
			delivery.LockedAt.Before(lockCutoff)
	default:
		return false
	}
}

func shredLegacySucceededWebhookCandidate(
	tx *gorm.DB,
	scope models.ProjectScope,
	candidate webhookOutboxCleanupCandidate,
	now time.Time,
) (bool, error) {
	var delivery models.OutboxDelivery
	err := tx.Clauses(webhookOutboxCleanupLocking(tx)).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND destination_type = ? AND status = ?",
			candidate.deliveryID,
			scope.OrganizationID,
			scope.ProjectID,
			"webhook",
			models.OutboxDeliverySucceeded,
		).
		Take(&delivery).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	if err != nil {
		return false, err
	}
	snapshot, err := lockWebhookSnapshotForDelivery(tx, &delivery)
	if err != nil {
		return false, ErrWebhookOutboxLifecycleInvariant
	}
	if snapshot.CredentialShreddedAt != nil {
		if snapshot.CredentialShredReason != nil &&
			*snapshot.CredentialShredReason ==
				models.WebhookCredentialShredReasonSucceeded &&
			snapshot.Secret == "" &&
			snapshot.PreviousSecret == "" &&
			snapshot.PreviousSecretExpiresAt == nil &&
			snapshot.AccessToken == "" {
			return false, errWebhookOutboxCleanupCandidateLost
		}
		return false, ErrWebhookOutboxLifecycleInvariant
	}
	if err := shredWebhookSnapshot(
		tx,
		snapshot,
		models.WebhookCredentialShredReasonSucceeded,
		now,
	); err != nil {
		return false, err
	}
	return true, nil
}

func clearWebhookSnapshotOverlap(
	tx *gorm.DB,
	scope models.ProjectScope,
	candidate webhookOutboxCleanupCandidate,
	now time.Time,
) (bool, error) {
	var snapshot models.WebhookDeliverySnapshot
	err := tx.Clauses(webhookOutboxCleanupLocking(tx)).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			candidate.snapshotID,
			scope.OrganizationID,
			scope.ProjectID,
		).
		Take(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	if err != nil {
		return false, err
	}
	if snapshot.CredentialShreddedAt != nil ||
		snapshot.PreviousSecretExpiresAt == nil ||
		snapshot.PreviousSecretExpiresAt.After(now) {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	result := tx.Table((models.WebhookDeliverySnapshot{}).TableName()).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? "+
				"AND credential_shredded_at IS NULL "+
				"AND previous_secret_expires_at = ? "+
				"AND previous_secret_expires_at <= ?",
			snapshot.ID,
			scope.OrganizationID,
			scope.ProjectID,
			snapshot.PreviousSecretExpiresAt.UTC(),
			now,
		).
		Updates(map[string]any{
			"previous_secret":            "",
			"previous_secret_expires_at": nil,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, errWebhookOutboxCleanupCandidateLost
	}
	return true, nil
}

func webhookOutboxCleanupLocking(tx *gorm.DB) clause.Locking {
	locking := clause.Locking{Strength: "UPDATE"}
	if tx != nil && tx.Dialector.Name() == "postgres" {
		locking.Options = "SKIP LOCKED"
	}
	return locking
}
