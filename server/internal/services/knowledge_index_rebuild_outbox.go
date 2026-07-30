package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const KnowledgeIndexRebuildOutboxDestination = "knowledge_index_rebuild"

// ExecuteIndexRebuildOutbox performs the external model/OpenSearch work for a
// committed rebuild intent. It is called only by the trusted Outbox worker and
// deliberately keeps claim/snapshot/finalize database transactions separate
// from external I/O.
func (service *KnowledgeService) ExecuteIndexRebuildOutbox(
	ctx context.Context,
	stateID string,
	generation uint64,
) error {
	if service == nil || service.db == nil || service.searchIndex == nil {
		return ErrKnowledgeIndexUnavailable
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"knowledge index rebuild worker",
	); err != nil {
		return err
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolWorker ||
		operation.Actor.Type != models.ActorTypeSystem {
		return ErrKnowledgeWorkerRequired
	}
	stateID = strings.TrimSpace(stateID)
	if stateID == "" || generation == 0 {
		return ErrKnowledgeIngestionState
	}

	alreadyComplete := false
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			var state models.KnowledgeIndexState
			if err := knowledgeScopedQuery(
				service.db.WithContext(scopedContext),
				operation.Scope,
			).Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", stateID).
				Take(&state).Error; err != nil {
				return knowledgeLookupError(err)
			}
			if state.Generation >= generation {
				alreadyComplete = true
				return nil
			}
			if state.DesiredGeneration < generation {
				return ErrKnowledgeIngestionState
			}
			now := service.now().UTC()
			state.Status = models.KnowledgeIndexBuilding
			state.StartedAt = &now
			state.CompletedAt = nil
			state.FailureDetail = ""
			return service.db.WithContext(scopedContext).Save(&state).Error
		},
	)
	if err != nil || alreadyComplete {
		return err
	}

	var (
		documents    []HybridIndexDocument
		sourceDigest string
		policy       models.ProjectModelPolicy
		provider     ModelProvider
	)
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			var snapshotErr error
			documents, sourceDigest, snapshotErr =
				service.loadKnowledgeIndexDocuments(
					scopedContext,
					operation.Scope,
				)
			if snapshotErr != nil || len(documents) == 0 {
				return snapshotErr
			}
			policy, provider, snapshotErr =
				service.resolveKnowledgeModelPolicy(
					scopedContext,
					operation.Scope,
				)
			return snapshotErr
		},
	)
	if err == nil && len(documents) > 0 {
		if boundaryErr := requireExternalIOOutsideProjectTransaction(
			ctx,
			"knowledge index embedding",
		); boundaryErr != nil {
			err = boundaryErr
		} else {
			documents, err = embedKnowledgeIndexDocuments(
				ctx,
				operation.Scope,
				documents,
				policy,
				provider,
			)
		}
	}
	if err == nil {
		if boundaryErr := requireExternalIOOutsideProjectTransaction(
			ctx,
			"knowledge project index replacement",
		); boundaryErr != nil {
			err = boundaryErr
		} else {
			err = service.searchIndex.ReplaceProject(
				ctx,
				HybridIndexReplacement{
					OrganizationID: operation.Scope.OrganizationID,
					ProjectID:      operation.Scope.ProjectID,
					Generation:     generation,
					SourceDigest:   sourceDigest,
					Documents:      documents,
				},
			)
		}
	}
	if err != nil {
		if persistErr := service.persistKnowledgeIndexRebuildFailure(
			ctx,
			operation.Scope,
			stateID,
			generation,
		); persistErr != nil {
			return fmt.Errorf(
				"knowledge index rebuild failed (%v), persist failure: %w",
				err,
				persistErr,
			)
		}
		return fmt.Errorf("replace project knowledge index: %w", err)
	}

	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			var state models.KnowledgeIndexState
			if err := knowledgeScopedQuery(
				service.db.WithContext(scopedContext),
				operation.Scope,
			).Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", stateID).
				Take(&state).Error; err != nil {
				return knowledgeLookupError(err)
			}
			if state.Generation >= generation {
				return nil
			}
			if state.DesiredGeneration < generation {
				return ErrKnowledgeIngestionState
			}
			now := service.now().UTC()
			state.Generation = generation
			state.SourceDigest = sourceDigest
			state.DocumentCount = len(documents)
			state.FailureDetail = ""
			state.CompletedAt = &now
			if state.DesiredGeneration > generation {
				state.Status = models.KnowledgeIndexRebuildRequested
			} else {
				state.Status = models.KnowledgeIndexReady
			}
			return service.db.WithContext(scopedContext).Save(&state).Error
		},
	)
}

func (service *KnowledgeService) persistKnowledgeIndexRebuildFailure(
	ctx context.Context,
	scope models.ProjectScope,
	stateID string,
	generation uint64,
) error {
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		scope,
		func(scopedContext context.Context) error {
			var state models.KnowledgeIndexState
			err := knowledgeScopedQuery(
				service.db.WithContext(scopedContext),
				scope,
			).Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ?", stateID).
				Take(&state).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeNotFound
			}
			if err != nil {
				return err
			}
			if state.Generation >= generation {
				return nil
			}
			now := time.Now().UTC()
			state.Status = models.KnowledgeIndexFailed
			state.FailureDetail = "知识索引重建失败"
			state.CompletedAt = &now
			return service.db.WithContext(scopedContext).Save(&state).Error
		},
	)
}
