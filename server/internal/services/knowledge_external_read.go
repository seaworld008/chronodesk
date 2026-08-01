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
	ActorType            models.ActorType
	ProjectID            uint
	ProjectUpdatedAtNano int64
	UserID               uint
	UserUpdatedAtNano    int64
	MembershipID         uint
	MembershipVersion    uint64
	MembershipUpdatedAt  int64
	Role                 models.ProjectRole
	PrincipalID          string
	PrincipalUpdatedAt   int64
	PrincipalPolicyEpoch uint64
	GrantID              uint
	GrantUpdatedAt       int64
	GrantRole            models.ProjectRole
	GrantScopesDigest    [sha256.Size]byte
	CredentialID         string
	CredentialUpdatedAt  int64
	SubjectsDigest       [sha256.Size]byte
	PolicyDigest         [sha256.Size]byte
}

type knowledgeSearchSnapshot struct {
	epoch    knowledgeAuthorizationEpoch
	subjects []models.KnowledgeACLSubject
	policy   models.ProjectModelPolicy
	provider ModelProvider
}

func (service *KnowledgeService) resolveKnowledgeSearchModelSnapshot(
	ctx context.Context,
	scope models.ProjectScope,
) (
	models.ProjectModelPolicy,
	ModelProvider,
	[]byte,
	error,
) {
	policy, provider, err := service.resolveKnowledgeModelPolicy(ctx, scope)
	if errors.Is(err, ErrKnowledgeModelPolicyUnavailable) {
		// OpenSearch lexical retrieval is the safe, useful baseline. No
		// document content leaves the deployment when a model policy/provider
		// is absent or not currently approved.
		return models.ProjectModelPolicy{},
			nil,
			[]byte(`{"mode":"lexical"}`),
			nil
	}
	if err != nil {
		return models.ProjectModelPolicy{}, nil, nil, err
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return models.ProjectModelPolicy{},
			nil,
			nil,
			fmt.Errorf(
				"encode knowledge authorization model policy: %w",
				err,
			)
	}
	return policy, provider, payload, nil
}

func (service *KnowledgeService) captureKnowledgeSearchSnapshot(
	ctx context.Context,
	operation OperationContext,
	policyDecisionID string,
) (knowledgeSearchSnapshot, error) {
	var snapshot knowledgeSearchSnapshot
	if service == nil || service.db == nil || service.projects == nil {
		return snapshot, errors.New(
			"knowledge authorization snapshot is unavailable",
		)
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
				policyDecisionID,
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
	policyDecisionID string,
) (knowledgeSearchSnapshot, error) {
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := parseKnowledgeHumanID(operation.Actor.ID)
		if err != nil {
			return knowledgeSearchSnapshot{}, ErrProjectKnowledgeAccessDenied
		}
		return service.captureHumanKnowledgeSearchSnapshot(
			ctx,
			operation,
			userID,
		)
	case models.ActorTypeServicePrincipal:
		return service.capturePrincipalKnowledgeSearchSnapshot(
			ctx,
			operation,
			policyDecisionID,
		)
	default:
		return knowledgeSearchSnapshot{}, ErrProjectKnowledgeAccessDenied
	}
}

func (service *KnowledgeService) captureHumanKnowledgeSearchSnapshot(
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
	policy, provider, policyPayload, err :=
		service.resolveKnowledgeSearchModelSnapshot(
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
	return knowledgeSearchSnapshot{
		epoch: knowledgeAuthorizationEpoch{
			ActorType:            models.ActorTypeHuman,
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

func (service *KnowledgeService) capturePrincipalKnowledgeSearchSnapshot(
	ctx context.Context,
	operation OperationContext,
	policyDecisionID string,
) (knowledgeSearchSnapshot, error) {
	if strings.TrimSpace(operation.CredentialID) == "" {
		return knowledgeSearchSnapshot{}, ErrProjectKnowledgeAccessDenied
	}
	access, err := service.projects.RevalidatePrincipalProjectAccess(
		ctx,
		operation.Scope,
		operation.Actor.ID,
		knowledgeReadScope,
	)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	if err := service.validateAuthoredPolicyDecisionTx(
		ctx,
		operation,
		policyDecisionID,
		knowledgeReadScope,
		"knowledge.search",
		"knowledge",
		"*",
		false,
	); err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	var principal models.ServicePrincipal
	if err := service.db.WithContext(ctx).
		Select("id", "updated_at", "policy_epoch").
		Where("id = ?", operation.Actor.ID).
		Take(&principal).Error; err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"load knowledge principal epoch: %w",
			err,
		)
	}
	var credential models.AgentCredential
	if err := service.db.WithContext(ctx).
		Select(
			"id",
			"updated_at",
			"status",
			"expires_at",
			"revoked_at",
		).
		Where(
			"id = ? AND service_principal_id = ?",
			operation.CredentialID,
			operation.Actor.ID,
		).
		Take(&credential).Error; err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"load knowledge credential epoch: %w",
			err,
		)
	}
	if credential.Status != models.AgentCredentialStatusActive ||
		credential.RevokedAt != nil ||
		!credential.ExpiresAt.After(service.now().UTC()) {
		return knowledgeSearchSnapshot{}, ErrProjectKnowledgeAccessDenied
	}
	subjects, err := service.resolveKnowledgeSubjects(ctx, operation)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	policy, provider, policyPayload, err :=
		service.resolveKnowledgeSearchModelSnapshot(
			ctx,
			operation.Scope,
		)
	if err != nil {
		return knowledgeSearchSnapshot{}, err
	}
	subjectPayload, err := json.Marshal(subjects)
	if err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"encode knowledge principal subjects: %w",
			err,
		)
	}
	grantScopesPayload, err := json.Marshal(access.Scopes)
	if err != nil {
		return knowledgeSearchSnapshot{}, fmt.Errorf(
			"encode knowledge principal grant scopes: %w",
			err,
		)
	}
	authorization := access.AuthorizationSnapshot
	return knowledgeSearchSnapshot{
		epoch: knowledgeAuthorizationEpoch{
			ActorType:            models.ActorTypeServicePrincipal,
			ProjectID:            access.Project.ID,
			ProjectUpdatedAtNano: access.Project.UpdatedAt.UTC().UnixNano(),
			PrincipalID:          principal.ID,
			PrincipalUpdatedAt:   principal.UpdatedAt.UTC().UnixNano(),
			PrincipalPolicyEpoch: principal.PolicyEpoch,
			GrantID:              authorization.GrantID,
			GrantUpdatedAt:       authorization.GrantUpdatedAt.UTC().UnixNano(),
			GrantRole:            authorization.GrantRole,
			GrantScopesDigest:    sha256.Sum256(grantScopesPayload),
			CredentialID:         credential.ID,
			CredentialUpdatedAt:  credential.UpdatedAt.UTC().UnixNano(),
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
	policyDecisionID string,
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
					policyDecisionID,
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
			authoritativeHits, displayByChunk, err :=
				service.validateKnowledgeSearchHits(
					scopedContext,
					operation.Scope,
					finalSnapshot.subjects,
					hits,
				)
			if err != nil {
				return err
			}
			if len(citations) == 0 {
				return nil
			}
			for index := range citations {
				display, ok := displayByChunk[citations[index].ChunkID]
				if !ok {
					return ErrProjectKnowledgeAccessDenied
				}
				authoritative, ok := authoritativeHits[citations[index].ChunkID]
				if !ok {
					return ErrProjectKnowledgeAccessDenied
				}
				citations[index].ArticleKey = display.ArticleKey
				citations[index].ArticleTitle = display.ArticleTitle
				citations[index].SectionPath = display.SectionPath
				citations[index].PageNumber = authoritative.PageNumber
				citations[index].Snippet = authoritative.Snippet
				citations[index].ContentHash = authoritative.ContentHash
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

// prevalidateKnowledgeSearchHits converts an OpenSearch projection back into
// PostgreSQL-authoritative snippets before any result text is sent to a model
// provider. The final write transaction repeats the authorization and
// document checks to close the subsequent TOCTOU window.
func (service *KnowledgeService) prevalidateKnowledgeSearchHits(
	ctx context.Context,
	operation OperationContext,
	expected knowledgeAuthorizationEpoch,
	policyDecisionID string,
	hits []HybridSearchHit,
) ([]HybridSearchHit, error) {
	validated := make([]HybridSearchHit, 0, len(hits))
	err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			snapshot, err :=
				service.captureKnowledgeSearchSnapshotInTransaction(
					scopedContext,
					operation,
					policyDecisionID,
				)
			if err != nil {
				return err
			}
			if snapshot.epoch != expected {
				return ErrProjectAccessDenied
			}
			authoritativeByChunk, _, err :=
				service.validateKnowledgeSearchHits(
					scopedContext,
					operation.Scope,
					snapshot.subjects,
					hits,
				)
			if err != nil {
				return err
			}
			for _, hit := range hits {
				authoritative, ok := authoritativeByChunk[hit.ChunkID]
				if !ok {
					return ErrProjectKnowledgeAccessDenied
				}
				validated = append(validated, authoritative)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return validated, nil
}

func (service *KnowledgeService) validateKnowledgeSearchHits(
	ctx context.Context,
	scope models.ProjectScope,
	subjects []models.KnowledgeACLSubject,
	hits []HybridSearchHit,
) (
	map[string]HybridSearchHit,
	map[string]knowledgeCitationDisplay,
	error,
) {
	chunkIDs := make([]string, 0, len(hits))
	articleIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		chunkIDs = append(chunkIDs, hit.ChunkID)
		articleIDs = append(articleIDs, hit.ArticleID)
	}
	type liveChunk struct {
		ID              string
		ArticleID       string
		ArticleKey      string
		ArticleTitle    string
		VersionID       string
		DocumentVersion uint64
		PageNumber      *int
		SectionPath     string
		Snippet         string
		ContentHash     string
		TokenCount      int
	}
	var rows []liveChunk
	if err := service.db.WithContext(ctx).
		Table("knowledge_chunks AS chunks").
		Select(
			"chunks.id, chunks.article_id, articles.key AS article_key, articles.title AS article_title, chunks.version_id, versions.version AS document_version, chunks.page_number, chunks.section_path, chunks.snippet, chunks.content_hash, chunks.token_count",
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
		return nil, nil, fmt.Errorf(
			"revalidate knowledge search documents: %w",
			err,
		)
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
			return nil, nil, ErrProjectKnowledgeAccessDenied
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
		return nil, nil, fmt.Errorf(
			"revalidate knowledge search ACL: %w",
			err,
		)
	}
	allowed := make(map[string]struct{}, len(aclRows))
	for _, row := range aclRows {
		allowed[strings.TrimSpace(row.ArticleID)] = struct{}{}
	}
	for _, hit := range hits {
		if _, ok := allowed[hit.ArticleID]; !ok {
			return nil, nil, ErrProjectKnowledgeAccessDenied
		}
	}
	authoritativeHits := make(map[string]HybridSearchHit, len(hits))
	displayByChunk := make(
		map[string]knowledgeCitationDisplay,
		len(liveByID),
	)
	for _, hit := range hits {
		row := liveByID[hit.ChunkID]
		hit.PageNumber = row.PageNumber
		hit.Snippet = row.Snippet
		hit.ContentHash = row.ContentHash
		hit.TokenCount = row.TokenCount
		authoritativeHits[hit.ChunkID] = hit
		displayByChunk[hit.ChunkID] = knowledgeCitationDisplay{
			ArticleKey:   row.ArticleKey,
			ArticleTitle: row.ArticleTitle,
			SectionPath:  row.SectionPath,
		}
	}
	return authoritativeHits, displayByChunk, nil
}

type knowledgeCitationDisplay struct {
	ArticleKey   string
	ArticleTitle string
	SectionPath  string
}
