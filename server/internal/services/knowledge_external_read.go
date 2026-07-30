package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
)

type knowledgeAuthorizationEpoch struct {
	ProjectID            uint
	ProjectUpdatedAtNano int64
	UserID               uint
	UserUpdatedAtNano    int64
	MembershipID         uint
	MembershipVersion    uint64
	MembershipUpdatedAt  int64
	Role                 models.ProjectRole
	SubjectsDigest       [sha256.Size]byte
	PolicyDigest         [sha256.Size]byte
}

type knowledgeSearchSnapshot struct {
	epoch    knowledgeAuthorizationEpoch
	subjects []models.KnowledgeACLSubject
	policy   models.ProjectModelPolicy
	provider ModelProvider
}

func (service *KnowledgeService) captureKnowledgeSearchSnapshot(
	ctx context.Context,
	operation OperationContext,
) (knowledgeSearchSnapshot, error) {
	var snapshot knowledgeSearchSnapshot
	if service == nil || service.db == nil || service.projects == nil {
		return snapshot, errors.New(
			"knowledge authorization snapshot is unavailable",
		)
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return snapshot, ErrProjectKnowledgeAccessDenied
	}
	userID, err := parseKnowledgeHumanID(operation.Actor.ID)
	if err != nil {
		return snapshot, ErrProjectKnowledgeAccessDenied
	}

	reusable, err := scopeddb.CanReuseProjectScopeTransaction(
		ctx,
		operation.Scope,
	)
	if err != nil {
		return snapshot, err
	}
	capture := func(scopedContext context.Context) error {
		var captureErr error
		snapshot, captureErr =
			service.captureKnowledgeSearchSnapshotInTransaction(
				scopedContext,
				operation,
				userID,
			)
		return captureErr
	}
	if reusable {
		err = capture(ctx)
	} else {
		err = scopeddb.WithProjectScopeContextTransaction(
			ctx,
			service.db,
			operation.Scope,
			capture,
		)
	}
	return snapshot, err
}

func (service *KnowledgeService) captureKnowledgeSearchSnapshotInTransaction(
	ctx context.Context,
	operation OperationContext,
	userID uint,
) (knowledgeSearchSnapshot, error) {
	access, err := service.projects.RevalidateHumanProjectAccess(
		ctx,
		operation.Scope,
		userID,
	)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	var identity struct {
		ID        uint
		UpdatedAt time.Time
	}
	if err := service.db.WithContext(ctx).
		Table("users").
		Select("id, updated_at").
		Where("id = ?", userID).
		Take(&identity).Error; err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"load knowledge authorization identity: %w",
			err,
		)
	}
	var membership models.ProjectMembership
	if err := service.db.WithContext(ctx).
		Select("id", "updated_at", "version", "role").
		Where(
			"project_id = ? AND user_id = ? AND is_active = ?",
			operation.Scope.ProjectID,
			userID,
			true,
		).
		Take(&membership).Error; err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"load knowledge authorization membership epoch: %w",
			err,
		)
	}
	subjects, err := service.resolveKnowledgeSubjects(ctx, operation)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	policy, provider, err := service.resolveKnowledgeModelPolicy(
		ctx,
		operation.Scope,
	)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	subjectPayload, err := json.Marshal(subjects)
	if err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"encode knowledge authorization subjects: %w",
			err,
		)
	}
	policyPayload, err := json.Marshal(policy)
	if err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"encode knowledge authorization model policy: %w",
			err,
		)
	}
	return knowledgeSearchSnapshot{
		epoch: knowledgeAuthorizationEpoch{
			ProjectID:            access.Project.ID,
			ProjectUpdatedAtNano: access.Project.UpdatedAt.UTC().UnixNano(),
			UserID:               identity.ID,
			UserUpdatedAtNano:    identity.UpdatedAt.UTC().UnixNano(),
			MembershipID:         membership.ID,
			MembershipVersion:    membership.Version,
			MembershipUpdatedAt:  membership.UpdatedAt.UTC().UnixNano(),
			Role:                 access.Role,
			SubjectsDigest:       sha256.Sum256(subjectPayload),
			PolicyDigest:         sha256.Sum256(policyPayload),
		},
		subjects: append(
			[]models.KnowledgeACLSubject(nil),
			subjects...,
		),
		policy:   policy,
		provider: provider,
	}, nil
}

func (service *KnowledgeService) finalizeKnowledgeSearch(
	ctx context.Context,
	operation OperationContext,
	expected knowledgeAuthorizationEpoch,
	hits []HybridSearchHit,
	citations []models.KnowledgeCitation,
) error {
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			// Authorization must be checked in the same final transaction that
			// validates live document ACL/version state and writes citations.
			finalSnapshot, snapshotErr :=
				service.captureKnowledgeSearchSnapshotInTransaction(
					scopedContext,
					operation,
					expected.UserID,
				)
			if snapshotErr != nil {
				return snapshotErr
			}
			if finalSnapshot.epoch != expected {
				return ErrProjectAccessDenied
			}
			if len(hits) == 0 {
				return nil
			}
			if err := service.validateKnowledgeSearchHits(
				scopedContext,
				operation.Scope,
				finalSnapshot.subjects,
				hits,
			); err != nil {
				return err
			}
			if len(citations) == 0 {
				return nil
			}
			if err := service.db.WithContext(scopedContext).
				Create(&citations).Error; err != nil {
				return fmt.Errorf(
					"persist knowledge citations: %w",
					err,
				)
			}
			return nil
		},
	)
}

func (service *KnowledgeService) validateKnowledgeSearchHits(
	ctx context.Context,
	scope models.ProjectScope,
	subjects []models.KnowledgeACLSubject,
	hits []HybridSearchHit,
) error {
	chunkIDs := make([]string, 0, len(hits))
	articleIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		chunkIDs = append(chunkIDs, hit.ChunkID)
		articleIDs = append(articleIDs, hit.ArticleID)
	}
	type liveChunk struct {
		ID              string
		ArticleID       string
		VersionID       string
		DocumentVersion uint64
		ContentHash     string
	}
	var rows []liveChunk
	if err := service.db.WithContext(ctx).
		Table("knowledge_chunks AS chunks").
		Select(
			"chunks.id, chunks.article_id, chunks.version_id, versions.version AS document_version, chunks.content_hash",
		).
		Joins(
			"JOIN knowledge_article_versions AS versions ON versions.id = chunks.version_id AND versions.article_id = chunks.article_id",
		).
		Joins(
			"JOIN knowledge_articles AS articles ON articles.id = chunks.article_id",
		).
		Where(
			"chunks.organization_id = ? AND chunks.project_id = ? AND chunks.id IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			chunkIDs,
		).
		Where(
			"versions.organization_id = ? AND versions.project_id = ? AND versions.status = ? AND versions.virus_scan = ?",
			scope.OrganizationID,
			scope.ProjectID,
			models.KnowledgeVersionPublished,
			models.VirusScanClean,
		).
		Where(
			"articles.organization_id = ? AND articles.project_id = ? AND articles.status = ?",
			scope.OrganizationID,
			scope.ProjectID,
			models.KnowledgeArticleActive,
		).
		Find(&rows).Error; err != nil {
		return fmt.Errorf("revalidate knowledge search documents: %w", err)
	}
	liveByID := make(map[string]liveChunk, len(rows))
	for _, row := range rows {
		liveByID[row.ID] = row
	}
	for _, hit := range hits {
		row, ok := liveByID[hit.ChunkID]
		if !ok ||
			row.ArticleID != hit.ArticleID ||
			row.VersionID != hit.VersionID ||
			row.DocumentVersion != hit.DocumentVersion ||
			row.ContentHash != hit.ContentHash {
			return ErrProjectKnowledgeAccessDenied
		}
	}

	subjectPredicates := make([]string, 0, len(subjects))
	subjectArguments := make([]any, 0, len(subjects)*2)
	for _, subject := range subjects {
		subjectPredicates = append(
			subjectPredicates,
			"(subject_type = ? AND subject_id = ?)",
		)
		subjectArguments = append(
			subjectArguments,
			subject.Type,
			subject.ID,
		)
	}
	var aclRows []struct {
		ArticleID string
	}
	if err := service.db.WithContext(ctx).
		Table("knowledge_article_acl").
		Select("DISTINCT article_id AS article_id").
		Where(
			"organization_id = ? AND project_id = ? AND article_id IN ? AND permission IN ?",
			scope.OrganizationID,
			scope.ProjectID,
			articleIDs,
			[]models.KnowledgeACLPermission{
				models.KnowledgeACLRead,
				models.KnowledgeACLManage,
			},
		).
		Where(
			"("+strings.Join(subjectPredicates, " OR ")+")",
			subjectArguments...,
		).
		Find(&aclRows).Error; err != nil {
		return fmt.Errorf("revalidate knowledge search ACL: %w", err)
	}
	allowed := make(map[string]struct{}, len(aclRows))
	for _, row := range aclRows {
		allowed[strings.TrimSpace(row.ArticleID)] = struct{}{}
	}
	for _, hit := range hits {
		if _, ok := allowed[hit.ArticleID]; !ok {
			return ErrProjectKnowledgeAccessDenied
		}
	}
	return nil
}
