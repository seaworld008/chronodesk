package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKnowledgeNotFound               = errors.New("knowledge resource not found")
	ErrKnowledgeIngestionState         = errors.New("knowledge ingestion state conflict")
	ErrKnowledgeVirusScanRequired      = errors.New("clean virus scan required before parsing")
	ErrKnowledgeIndexUnavailable       = errors.New("knowledge search index is unavailable")
	ErrKnowledgeIndexBoundaryViolation = errors.New("knowledge index returned an out-of-scope result")
	ErrKnowledgeIndexResponseInvalid   = errors.New("knowledge search index response is invalid")
	ErrKnowledgeModelPolicyDenied      = errors.New("knowledge model policy denied the operation")
	ErrKnowledgeModelPolicyUnavailable = errors.New("knowledge model policy or provider is unavailable")
	ErrKnowledgeModelResponseInvalid   = errors.New("knowledge model provider response is invalid")
	ErrKnowledgeWorkerRequired         = errors.New("trusted knowledge worker operation is required")
)

const (
	knowledgeIndexRebuildDefaultBatchSize = 500
	knowledgeIndexRebuildMaxBatchSize     = 500
)

type KnowledgeAccessResolver interface {
	ResolveKnowledgeSubjects(
		ctx context.Context,
		scope models.ProjectScope,
		actor models.ActorRef,
	) ([]models.KnowledgeACLSubject, error)
}

type HybridSearchFilter struct {
	OrganizationID uint                         `json:"organization_id"`
	ProjectID      uint                         `json:"project_id"`
	ACLSubjects    []models.KnowledgeACLSubject `json:"acl_subjects"`
	PublishedOnly  bool                         `json:"published_only"`
	VirusScan      models.VirusScanStatus       `json:"virus_scan"`
}

func (filter HybridSearchFilter) Validate() error {
	if err := (models.ProjectScope{
		OrganizationID: filter.OrganizationID,
		ProjectID:      filter.ProjectID,
	}).Validate(); err != nil {
		return err
	}
	if len(filter.ACLSubjects) == 0 {
		return errors.New("hybrid search requires ACL subjects")
	}
	for _, subject := range filter.ACLSubjects {
		if err := subject.Validate(); err != nil {
			return err
		}
	}
	if !filter.PublishedOnly {
		return errors.New("knowledge search must require published versions")
	}
	if filter.VirusScan != models.VirusScanClean {
		return errors.New("knowledge search must require clean virus scan status")
	}
	return nil
}

type HybridSearchRequest struct {
	Query          string             `json:"query"`
	QueryEmbedding []float32          `json:"query_embedding"`
	Limit          int                `json:"limit"`
	Filter         HybridSearchFilter `json:"filter"`
}

type HybridSearchHit struct {
	OrganizationID  uint    `json:"organization_id"`
	ProjectID       uint    `json:"project_id"`
	ArticleID       string  `json:"article_id"`
	VersionID       string  `json:"version_id"`
	DocumentVersion uint64  `json:"document_version"`
	ChunkID         string  `json:"chunk_id"`
	PageNumber      *int    `json:"page_number,omitempty"`
	Snippet         string  `json:"snippet"`
	ContentHash     string  `json:"content_hash"`
	Score           float64 `json:"score"`
	TokenCount      int     `json:"token_count"`
}

type HybridIndexDocument struct {
	OrganizationID  uint                         `json:"organization_id"`
	ProjectID       uint                         `json:"project_id"`
	ArticleID       string                       `json:"article_id"`
	VersionID       string                       `json:"version_id"`
	DocumentVersion uint64                       `json:"document_version"`
	ChunkID         string                       `json:"chunk_id"`
	PageNumber      *int                         `json:"page_number,omitempty"`
	Content         string                       `json:"content"`
	Embedding       []float32                    `json:"embedding"`
	Snippet         string                       `json:"snippet"`
	ContentHash     string                       `json:"content_hash"`
	TokenCount      int                          `json:"token_count"`
	ACLSubjects     []models.KnowledgeACLSubject `json:"acl_subjects"`
}

type HybridIndexReplacement struct {
	OrganizationID uint                  `json:"organization_id"`
	ProjectID      uint                  `json:"project_id"`
	Generation     uint64                `json:"generation"`
	SourceDigest   string                `json:"source_digest"`
	Documents      []HybridIndexDocument `json:"documents"`
}

// HybridIndexBatchSource returns one bounded, strictly chunk-ID-ordered batch.
// An empty batch marks EOF. Implementations must not retain a returned batch
// after the next call, so callers can release document bodies and embeddings
// as soon as the backend has accepted them.
type HybridIndexBatchSource func(
	ctx context.Context,
) ([]HybridIndexDocument, error)

// HybridSearchIndex requires tenancy and ACL filters in the backend request.
// Implementations must apply Filter while retrieving candidates.
type HybridSearchIndex interface {
	Search(
		ctx context.Context,
		request HybridSearchRequest,
	) ([]HybridSearchHit, error)
	ReplaceProject(
		ctx context.Context,
		replacement HybridIndexReplacement,
	) error
}

// HybridSearchIndexBatchReplacer is the fail-closed rebuild contract. The
// Outbox worker requires this capability instead of falling back to an
// unbounded in-memory replacement.
type HybridSearchIndexBatchReplacer interface {
	ReplaceProjectBatches(
		ctx context.Context,
		replacement HybridIndexReplacement,
		source HybridIndexBatchSource,
	) error
}

type ModelProviderDescriptor struct {
	Key        string `json:"key"`
	IsExternal bool   `json:"is_external"`
}

type ModelUsage struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}

type ModelCallLimits struct {
	MonthlyTokenBudget      int64 `json:"monthly_token_budget"`
	MonthlyCostBudgetMicros int64 `json:"monthly_cost_budget_micros"`
	RequestsPerMinute       int   `json:"requests_per_minute"`
	TokensPerMinute         int   `json:"tokens_per_minute"`
}

type ModelGenerateRequest struct {
	Scope           models.ProjectScope `json:"scope"`
	Model           string              `json:"model"`
	Prompt          string              `json:"prompt"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	Limits          ModelCallLimits     `json:"limits"`
}

type ModelGenerateResponse struct {
	Text  string     `json:"text"`
	Usage ModelUsage `json:"usage"`
}

type ModelEmbedRequest struct {
	Scope  models.ProjectScope `json:"scope"`
	Model  string              `json:"model"`
	Inputs []string            `json:"inputs"`
	Limits ModelCallLimits     `json:"limits"`
}

type ModelEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Usage      ModelUsage  `json:"usage"`
}

type ModelRerankCandidate struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type ModelRerankRequest struct {
	Scope      models.ProjectScope    `json:"scope"`
	Model      string                 `json:"model"`
	Query      string                 `json:"query"`
	Candidates []ModelRerankCandidate `json:"candidates"`
	Limit      int                    `json:"limit"`
	Limits     ModelCallLimits        `json:"limits"`
}

type ModelRerankItem struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type ModelRerankResponse struct {
	Items []ModelRerankItem `json:"items"`
	Usage ModelUsage        `json:"usage"`
}

// ModelProvider is protocol-neutral; HTTP SDKs and local runtimes implement
// this interface without leaking transport details into the domain service.
type ModelProvider interface {
	Descriptor() ModelProviderDescriptor
	Generate(
		ctx context.Context,
		request ModelGenerateRequest,
	) (ModelGenerateResponse, error)
	Embed(
		ctx context.Context,
		request ModelEmbedRequest,
	) (ModelEmbedResponse, error)
	Rerank(
		ctx context.Context,
		request ModelRerankRequest,
	) (ModelRerankResponse, error)
}

type KnowledgeServiceDependencies struct {
	SearchIndex          HybridSearchIndex
	AccessResolver       KnowledgeAccessResolver
	ModelProviders       map[string]ModelProvider
	ProjectAuthorization *ProjectService
	Events               projectDomainEventAppender
	AttachmentStorage    AttachmentStorage
	StorageBucket        string
	IdempotencyCompleter KnowledgeIdempotencyCompleter
	// IndexRebuildBatchSize is injectable for deterministic tests. Runtime
	// composition should leave it zero to use the bounded default.
	IndexRebuildBatchSize int
}

type KnowledgeService struct {
	db             *gorm.DB
	searchIndex    HybridSearchIndex
	accessResolver KnowledgeAccessResolver
	modelProviders map[string]ModelProvider
	projects       *ProjectService
	events         projectDomainEventAppender
	storage        AttachmentStorage
	storageBucket  string
	idempotency    KnowledgeIdempotencyCompleter
	indexBatchSize int
	now            func() time.Time
}

func NewKnowledgeService(
	db *gorm.DB,
	dependencies KnowledgeServiceDependencies,
) (*KnowledgeService, error) {
	if db == nil {
		return nil, errors.New("knowledge database is required")
	}
	if dependencies.Events == nil {
		return nil, errors.New("knowledge domain event pipeline is required")
	}
	indexBatchSize := dependencies.IndexRebuildBatchSize
	if indexBatchSize == 0 {
		indexBatchSize = knowledgeIndexRebuildDefaultBatchSize
	}
	if indexBatchSize < 1 ||
		indexBatchSize > knowledgeIndexRebuildMaxBatchSize {
		return nil, errors.New("knowledge index rebuild batch size is invalid")
	}
	providers := make(map[string]ModelProvider, len(dependencies.ModelProviders))
	for key, provider := range dependencies.ModelProviders {
		key = strings.TrimSpace(key)
		if key == "" || provider == nil {
			return nil, errors.New("knowledge model provider registration is invalid")
		}
		descriptor := provider.Descriptor()
		if strings.TrimSpace(descriptor.Key) != key {
			return nil, errors.New("knowledge model provider key mismatch")
		}
		providers[key] = provider
	}
	projects := dependencies.ProjectAuthorization
	if projects == nil {
		var err error
		projects, err = NewProjectService(db)
		if err != nil {
			return nil, fmt.Errorf(
				"initialize knowledge project authorization: %w",
				err,
			)
		}
	}
	return &KnowledgeService{
		db:             db,
		searchIndex:    dependencies.SearchIndex,
		accessResolver: dependencies.AccessResolver,
		modelProviders: providers,
		projects:       projects,
		events:         dependencies.Events,
		storage:        dependencies.AttachmentStorage,
		storageBucket:  strings.TrimSpace(dependencies.StorageBucket),
		idempotency:    dependencies.IdempotencyCompleter,
		indexBatchSize: indexBatchSize,
		now:            time.Now,
	}, nil
}

type CreateKnowledgeArticleInput struct {
	Key                string
	Title              string
	Summary            string
	GrantProjectAccess bool
}

type KnowledgeArticleListFilter struct {
	Status           models.KnowledgeArticleStatus
	Query            string
	ManageAll        bool
	ManagedByActor   bool
	PolicyDecisionID string
}

func (service *KnowledgeService) ListArticles(
	ctx context.Context,
	filter KnowledgeArticleListFilter,
	request DirectoryPageRequest,
) (*DirectoryPage[models.KnowledgeArticle], error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if validateDirectoryPageRequest(
		request,
		map[string]struct{}{
			"created_at": {},
			"updated_at": {},
			"key":        {},
			"title":      {},
			"status":     {},
		},
	) != nil ||
		(filter.Status != "" && !filter.Status.IsValid()) ||
		(filter.ManageAll && filter.ManagedByActor) {
		return nil, ErrDirectoryListQuery
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if len([]rune(filter.Query)) > 200 {
		return nil, ErrDirectoryListQuery
	}
	page := &DirectoryPage[models.KnowledgeArticle]{
		Items:    make([]models.KnowledgeArticle, 0),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			tx := service.db.WithContext(scopedContext)
			subjects, err := service.revalidateKnowledgeListAccess(
				scopedContext,
				operation,
				filter.ManageAll,
				filter.ManagedByActor,
				filter.PolicyDecisionID,
			)
			if err != nil {
				return err
			}
			query := knowledgeScopedQuery(
				tx.Model(&models.KnowledgeArticle{}),
				operation.Scope,
			)
			switch {
			case filter.ManageAll:
			case filter.ManagedByActor:
				query = applyKnowledgeArticleHumanManageVisibility(
					query,
					operation.Scope,
					operation.Actor.ID,
				)
			default:
				query = applyKnowledgeArticleReadVisibility(
					query,
					operation.Scope,
					subjects,
				)
			}
			if filter.Status != "" {
				query = query.Where("status = ?", filter.Status)
			}
			if filter.Query != "" {
				like := "%" + escapeKnowledgeDirectoryLike(
					strings.ToLower(filter.Query),
				) + "%"
				query = query.Where(
					"(lower(key) LIKE ? ESCAPE '\\' OR lower(title) LIKE ? ESCAPE '\\' OR lower(summary) LIKE ? ESCAPE '\\')",
					like,
					like,
					like,
				)
			}
			if err := query.Count(&page.Total).Error; err != nil {
				return fmt.Errorf("count knowledge articles: %w", err)
			}
			if err := query.
				Order(knowledgeArticleDirectoryOrder(
					request,
					filter.ManagedByActor,
				)).
				Offset(directoryPageOffset(request)).
				Limit(request.PageSize).
				Find(&page.Items).Error; err != nil {
				return fmt.Errorf("list knowledge articles: %w", err)
			}
			if (filter.ManageAll || filter.ManagedByActor) &&
				len(page.Items) > 0 {
				if err := hydrateKnowledgeDraftActivity(
					tx,
					operation.Scope,
					page.Items,
				); err != nil {
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	page.TotalPages = directoryTotalPages(page.Total, request.PageSize)
	return page, nil
}

type KnowledgeVersionListFilter struct {
	Status    models.KnowledgeVersionStatus
	VirusScan models.VirusScanStatus
}

func (service *KnowledgeService) ListArticleVersions(
	ctx context.Context,
	articleID string,
	filter KnowledgeVersionListFilter,
	request DirectoryPageRequest,
) (*DirectoryPage[models.KnowledgeArticleVersion], error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	articleID = strings.TrimSpace(articleID)
	if articleID == "" ||
		validateDirectoryPageRequest(
			request,
			map[string]struct{}{
				"created_at": {},
				"updated_at": {},
				"version":    {},
				"status":     {},
			},
		) != nil ||
		(filter.Status != "" && !filter.Status.IsValid()) ||
		(filter.VirusScan != "" &&
			!knowledgeVirusScanStatusIsValid(filter.VirusScan)) {
		return nil, ErrDirectoryListQuery
	}
	page := &DirectoryPage[models.KnowledgeArticleVersion]{
		Items:    make([]models.KnowledgeArticleVersion, 0, request.PageSize),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			if _, err := service.revalidateKnowledgeListAccess(
				scopedContext,
				operation,
				true,
				false,
				"",
			); err != nil {
				return err
			}
			tx := service.db.WithContext(scopedContext)
			var articleCount int64
			if err := knowledgeScopedQuery(
				tx.Model(&models.KnowledgeArticle{}),
				operation.Scope,
			).Where("id = ?", articleID).
				Count(&articleCount).Error; err != nil {
				return fmt.Errorf("verify knowledge article: %w", err)
			}
			if articleCount != 1 {
				return ErrKnowledgeNotFound
			}
			query := knowledgeScopedQuery(
				tx.Model(&models.KnowledgeArticleVersion{}),
				operation.Scope,
			).Where("article_id = ?", articleID)
			if filter.Status != "" {
				query = query.Where("status = ?", filter.Status)
			}
			if filter.VirusScan != "" {
				query = query.Where("virus_scan = ?", filter.VirusScan)
			}
			if err := query.Count(&page.Total).Error; err != nil {
				return fmt.Errorf(
					"count knowledge article versions: %w",
					err,
				)
			}
			if err := query.
				Order(knowledgeVersionDirectoryOrder(request)).
				Offset(directoryPageOffset(request)).
				Limit(request.PageSize).
				Find(&page.Items).Error; err != nil {
				return fmt.Errorf(
					"list knowledge article versions: %w",
					err,
				)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	page.TotalPages = directoryTotalPages(page.Total, request.PageSize)
	return page, nil
}

type KnowledgeIngestionListFilter struct {
	Status    models.KnowledgeIngestionStatus
	VersionID string
}

func (service *KnowledgeService) ListIngestions(
	ctx context.Context,
	filter KnowledgeIngestionListFilter,
	request DirectoryPageRequest,
) (*DirectoryPage[models.KnowledgeIngestionTask], error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	filter.VersionID = strings.TrimSpace(filter.VersionID)
	if validateDirectoryPageRequest(
		request,
		map[string]struct{}{
			"created_at": {},
			"updated_at": {},
			"attempt":    {},
			"status":     {},
		},
	) != nil ||
		(filter.Status != "" && !filter.Status.IsValid()) ||
		len(filter.VersionID) > 64 {
		return nil, ErrDirectoryListQuery
	}
	page := &DirectoryPage[models.KnowledgeIngestionTask]{
		Items:    make([]models.KnowledgeIngestionTask, 0, request.PageSize),
		Page:     request.Page,
		PageSize: request.PageSize,
	}
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			if _, err := service.revalidateKnowledgeListAccess(
				scopedContext,
				operation,
				true,
				false,
				"",
			); err != nil {
				return err
			}
			query := knowledgeScopedQuery(
				service.db.WithContext(scopedContext).
					Model(&models.KnowledgeIngestionTask{}),
				operation.Scope,
			)
			if filter.Status != "" {
				query = query.Where("status = ?", filter.Status)
			}
			if filter.VersionID != "" {
				query = query.Where("version_id = ?", filter.VersionID)
			}
			if err := query.Count(&page.Total).Error; err != nil {
				return fmt.Errorf("count knowledge ingestions: %w", err)
			}
			if err := query.
				Order(knowledgeIngestionDirectoryOrder(request)).
				Offset(directoryPageOffset(request)).
				Limit(request.PageSize).
				Find(&page.Items).Error; err != nil {
				return fmt.Errorf("list knowledge ingestions: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	page.TotalPages = directoryTotalPages(page.Total, request.PageSize)
	return page, nil
}

func knowledgeArticleDirectoryOrder(
	request DirectoryPageRequest,
	managedByActor bool,
) string {
	if managedByActor && request.SortBy == "updated_at" {
		direction := "ASC"
		if request.SortOrder == "desc" {
			direction = "DESC"
		}
		return `COALESCE((
			SELECT MAX(latest_draft.created_at)
			FROM knowledge_article_versions AS latest_draft
			WHERE latest_draft.organization_id =
			      knowledge_articles.organization_id
			  AND latest_draft.project_id = knowledge_articles.project_id
			  AND latest_draft.article_id = knowledge_articles.id
			  AND latest_draft.status = 'draft'
		), knowledge_articles.updated_at) ` + direction +
			", knowledge_articles.id " + direction
	}
	return knowledgeDirectoryOrder(request, map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"key":        "key",
		"title":      "title",
		"status":     "status",
	})
}

type knowledgeDraftActivityRow struct {
	ArticleID          string    `gorm:"column:article_id"`
	LatestDraftAt      time.Time `gorm:"column:latest_draft_at"`
	LatestDraftVersion uint64    `gorm:"column:latest_draft_version"`
}

func hydrateKnowledgeDraftActivity(
	tx *gorm.DB,
	scope models.ProjectScope,
	articles []models.KnowledgeArticle,
) error {
	if tx == nil || len(articles) == 0 {
		return nil
	}
	articleIDs := make([]string, 0, len(articles))
	for index := range articles {
		articleIDs = append(articleIDs, articles[index].ID)
	}
	rows := make([]knowledgeDraftActivityRow, 0, len(articles))
	rankedDrafts := knowledgeScopedQuery(
		tx.Model(&models.KnowledgeArticleVersion{}),
		scope,
	).Select(
		"article_id, created_at, version, ROW_NUMBER() OVER (PARTITION BY article_id ORDER BY created_at DESC, id DESC) AS draft_rank",
	).
		Where(
			"article_id IN ? AND status = ?",
			articleIDs,
			models.KnowledgeVersionDraft,
		)
	if err := tx.Table("(?) AS ranked_drafts", rankedDrafts).
		Select(
			"article_id, created_at AS latest_draft_at, version AS latest_draft_version",
		).
		Where("draft_rank = 1").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"load knowledge draft activity projection: %w",
			err,
		)
	}
	byArticleID := make(
		map[string]knowledgeDraftActivityRow,
		len(rows),
	)
	for _, row := range rows {
		byArticleID[row.ArticleID] = row
	}
	for index := range articles {
		row, ok := byArticleID[articles[index].ID]
		if !ok {
			continue
		}
		latestAt := row.LatestDraftAt
		latestVersion := row.LatestDraftVersion
		articles[index].HasUnpublishedDraft = true
		articles[index].LatestDraftAt = &latestAt
		articles[index].LatestDraftVersion = &latestVersion
	}
	return nil
}

func knowledgeVersionDirectoryOrder(request DirectoryPageRequest) string {
	return knowledgeDirectoryOrder(request, map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"version":    "version",
		"status":     "status",
	})
}

func knowledgeIngestionDirectoryOrder(request DirectoryPageRequest) string {
	return knowledgeDirectoryOrder(request, map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"attempt":    "attempt",
		"status":     "status",
	})
}

func knowledgeDirectoryOrder(
	request DirectoryPageRequest,
	columns map[string]string,
) string {
	direction := "ASC"
	if request.SortOrder == "desc" {
		direction = "DESC"
	}
	return columns[request.SortBy] + " " + direction + ", id " + direction
}

func escapeKnowledgeDirectoryLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func knowledgeVirusScanStatusIsValid(status models.VirusScanStatus) bool {
	switch status {
	case models.VirusScanPending,
		models.VirusScanClean,
		models.VirusScanInfected,
		models.VirusScanError:
		return true
	default:
		return false
	}
}

func (service *KnowledgeService) CreateArticle(
	ctx context.Context,
	input CreateKnowledgeArticleInput,
) (*models.KnowledgeArticle, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	article := models.KnowledgeArticle{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		Key:            strings.TrimSpace(input.Key),
		Title:          strings.TrimSpace(input.Title),
		Summary:        strings.TrimSpace(input.Summary),
		Status:         models.KnowledgeArticleActive,
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
		UpdatedByType:  operation.Actor.Type,
		UpdatedByID:    operation.Actor.ID,
	}
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := tx.Create(&article).Error; err != nil {
			return fmt.Errorf("create knowledge article: %w", err)
		}
		if !input.GrantProjectAccess {
			return nil
		}
		acl := models.KnowledgeArticleACL{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ArticleID:      article.ID,
			SubjectType:    models.KnowledgeACLAllProject,
			SubjectID:      "*",
			Permission:     models.KnowledgeACLRead,
			GrantedByType:  operation.Actor.Type,
			GrantedByID:    operation.Actor.ID,
		}
		if err := tx.Create(&acl).Error; err != nil {
			return fmt.Errorf("grant project knowledge access: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &article, nil
}

type CreateKnowledgeVersionInput struct {
	Title  string
	Source models.KnowledgeObjectReference
}

func (service *KnowledgeService) CreateVersion(
	ctx context.Context,
	articleID string,
	input CreateKnowledgeVersionInput,
) (*models.KnowledgeArticleVersion, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	var created models.KnowledgeArticleVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var article models.KnowledgeArticle
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Where("id = ? AND status = ?", articleID, models.KnowledgeArticleActive).
			First(&article).Error; err != nil {
			return knowledgeLookupError(err)
		}
		var maximum struct {
			Version uint64
		}
		if err := knowledgeScopedQuery(
			tx.Model(&models.KnowledgeArticleVersion{}),
			operation.Scope,
		).Select("COALESCE(MAX(version), 0) AS version").
			Where("article_id = ?", article.ID).
			Scan(&maximum).Error; err != nil {
			return fmt.Errorf("select next knowledge version: %w", err)
		}
		created = models.KnowledgeArticleVersion{
			OrganizationID:   operation.Scope.OrganizationID,
			ProjectID:        operation.Scope.ProjectID,
			ArticleID:        article.ID,
			Version:          maximum.Version + 1,
			Status:           models.KnowledgeVersionDraft,
			Title:            strings.TrimSpace(input.Title),
			ObjectProvider:   strings.TrimSpace(input.Source.Provider),
			ObjectBucket:     strings.TrimSpace(input.Source.Bucket),
			ObjectKey:        strings.TrimSpace(input.Source.Key),
			ObjectVersionID:  strings.TrimSpace(input.Source.VersionID),
			OriginalFileName: strings.TrimSpace(input.Source.FileName),
			MimeType:         strings.TrimSpace(input.Source.MimeType),
			SizeBytes:        input.Source.SizeBytes,
			ContentHash:      strings.ToLower(strings.TrimSpace(input.Source.ContentHash)),
			VirusScan:        models.VirusScanPending,
			CreatedByType:    operation.Actor.Type,
			CreatedByID:      operation.Actor.ID,
		}
		if err := tx.Create(&created).Error; err != nil {
			return fmt.Errorf("create knowledge version: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (service *KnowledgeService) GrantArticleAccess(
	ctx context.Context,
	articleID string,
	subject models.KnowledgeACLSubject,
	permission models.KnowledgeACLPermission,
) (*models.KnowledgeArticleACL, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeHuman ||
		subject.Validate() != nil ||
		!permission.IsValid() {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	userID, err := parseKnowledgeHumanID(operation.Actor.ID)
	if err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	var acl models.KnowledgeArticleACL
	err = runProjectOperation(ctx, service.db, func(scopedContext context.Context) error {
		tx := service.db.WithContext(scopedContext)
		access, revalidateErr := service.projects.RevalidateHumanProjectAccess(
			scopedContext,
			operation.Scope,
			userID,
		)
		if revalidateErr != nil {
			return revalidateErr
		}
		if access.Role != models.ProjectRoleAdmin &&
			access.Role != models.ProjectRoleManager {
			return ErrProjectKnowledgeAccessDenied
		}
		if err := validateKnowledgeACLSubjectTx(
			tx,
			operation.Scope,
			subject,
		); err != nil {
			return err
		}
		var article models.KnowledgeArticle
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ?", articleID).
			First(&article).Error; err != nil {
			return knowledgeLookupError(err)
		}
		acl = models.KnowledgeArticleACL{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ArticleID:      article.ID,
			SubjectType:    subject.Type,
			SubjectID:      strings.TrimSpace(subject.ID),
			Permission:     permission,
			GrantedByType:  operation.Actor.Type,
			GrantedByID:    operation.Actor.ID,
		}
		if err := tx.Create(&acl).Error; err != nil {
			return fmt.Errorf("grant knowledge ACL: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &acl, nil
}

func validateKnowledgeACLSubjectTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	subject models.KnowledgeACLSubject,
) error {
	if tx == nil || subject.Validate() != nil {
		return ErrProjectKnowledgeAccessDenied
	}
	switch subject.Type {
	case models.KnowledgeACLAllProject:
		return nil
	case models.KnowledgeACLProjectRole:
		if !models.ProjectRole(subject.ID).IsValid() {
			return ErrProjectKnowledgeAccessDenied
		}
		return nil
	case models.KnowledgeACLHuman:
		userID, err := strconv.ParseUint(
			strings.TrimSpace(subject.ID),
			10,
			strconv.IntSize,
		)
		if err != nil || userID == 0 {
			return ErrProjectKnowledgeAccessDenied
		}
		var count int64
		if err := tx.Table("project_memberships AS memberships").
			Joins(
				"JOIN users AS users ON users.id = memberships.user_id",
			).
			Where(
				"memberships.project_id = ? AND memberships.user_id = ? AND memberships.is_active = ?",
				scope.ProjectID,
				uint(userID),
				true,
			).
			Where(
				"users.status = ? AND users.deleted_at IS NULL",
				models.UserStatusActive,
			).
			Count(&count).Error; err != nil {
			return fmt.Errorf("validate knowledge Human ACL subject: %w", err)
		}
		if count != 1 {
			return ErrProjectKnowledgeAccessDenied
		}
	case models.KnowledgeACLServicePrincipal:
		var count int64
		if err := tx.Table("project_principal_grants AS grants").
			Joins(
				"JOIN service_principals AS principals ON principals.id = grants.service_principal_id",
			).
			Where(
				"grants.project_id = ? AND grants.service_principal_id = ? AND grants.is_active = ?",
				scope.ProjectID,
				strings.TrimSpace(subject.ID),
				true,
			).
			Where(
				"principals.status = ? AND principals.deleted_at IS NULL",
				models.ServicePrincipalStatusActive,
			).
			Count(&count).Error; err != nil {
			return fmt.Errorf(
				"validate knowledge Service Principal ACL subject: %w",
				err,
			)
		}
		if count != 1 {
			return ErrProjectKnowledgeAccessDenied
		}
	case models.KnowledgeACLTeam:
		var count int64
		if err := tx.Model(&models.Team{}).
			Where(
				"project_id = ? AND public_id = ? AND status = ?",
				scope.ProjectID,
				strings.TrimSpace(subject.ID),
				models.TeamStatusActive,
			).
			Count(&count).Error; err != nil {
			return fmt.Errorf("validate knowledge Team ACL subject: %w", err)
		}
		if count != 1 {
			return ErrProjectKnowledgeAccessDenied
		}
	default:
		return ErrProjectKnowledgeAccessDenied
	}
	return nil
}

func (service *KnowledgeService) QueueIngestion(
	ctx context.Context,
	versionID string,
	parserKey string,
) (*models.KnowledgeIngestionTask, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	var task models.KnowledgeIngestionTask
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var version models.KnowledgeArticleVersion
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Where("id = ? AND status = ?", versionID, models.KnowledgeVersionDraft).
			First(&version).Error; err != nil {
			return knowledgeLookupError(err)
		}
		var maximum struct {
			Attempt uint
		}
		if err := knowledgeScopedQuery(
			tx.Model(&models.KnowledgeIngestionTask{}),
			operation.Scope,
		).Select("COALESCE(MAX(attempt), 0) AS attempt").
			Where("version_id = ?", version.ID).
			Scan(&maximum).Error; err != nil {
			return fmt.Errorf("select knowledge ingestion attempt: %w", err)
		}
		task = models.KnowledgeIngestionTask{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ArticleID:      version.ArticleID,
			VersionID:      version.ID,
			Attempt:        maximum.Attempt + 1,
			Status:         models.KnowledgeIngestionQueued,
			ParserKey:      strings.TrimSpace(parserKey),
			CreatedByType:  operation.Actor.Type,
			CreatedByID:    operation.Actor.ID,
		}
		if err := tx.Create(&task).Error; err != nil {
			return fmt.Errorf("queue knowledge ingestion: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (service *KnowledgeService) MarkVersionVirusScan(
	ctx context.Context,
	versionID string,
	status models.VirusScanStatus,
	detail string,
) (*models.KnowledgeArticleVersion, error) {
	operation, err := knowledgeWorkerOperation(ctx)
	if err != nil {
		return nil, err
	}
	switch status {
	case models.VirusScanClean,
		models.VirusScanInfected,
		models.VirusScanError:
	default:
		return nil, errors.New("knowledge virus scan result must be terminal")
	}
	var version models.KnowledgeArticleVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", versionID).
			First(&version).Error; err != nil {
			return knowledgeLookupError(err)
		}
		if version.Status != models.KnowledgeVersionDraft ||
			version.VirusScan != models.VirusScanPending {
			return ErrKnowledgeIngestionState
		}
		now := service.now().UTC()
		version.VirusScan = status
		version.ScanDetail = strings.TrimSpace(detail)
		version.ScannedAt = &now
		if status == models.VirusScanInfected {
			version.Status = models.KnowledgeVersionQuarantined
		}
		if err := tx.Save(&version).Error; err != nil {
			return fmt.Errorf("persist knowledge virus scan: %w", err)
		}
		if status != models.VirusScanClean {
			if err := knowledgeScopedQuery(
				tx.Model(&models.KnowledgeIngestionTask{}),
				operation.Scope,
			).Where(
				"version_id = ? AND status = ?",
				version.ID,
				models.KnowledgeIngestionQueued,
			).UpdateColumns(map[string]any{
				"status":         models.KnowledgeIngestionQuarantined,
				"failure_code":   "virus_scan_not_clean",
				"failure_detail": "文档未通过病毒扫描",
				"completed_at":   now,
				"updated_at":     now,
			}).Error; err != nil {
				return fmt.Errorf("quarantine knowledge ingestion: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &version, nil
}

func (service *KnowledgeService) StartParsing(
	ctx context.Context,
	taskID string,
) (*models.KnowledgeIngestionTask, error) {
	operation, err := knowledgeWorkerOperation(ctx)
	if err != nil {
		return nil, err
	}
	var task models.KnowledgeIngestionTask
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", taskID).
			First(&task).Error; err != nil {
			return knowledgeLookupError(err)
		}
		if task.Status != models.KnowledgeIngestionQueued {
			return ErrKnowledgeIngestionState
		}
		var version models.KnowledgeArticleVersion
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Where("id = ? AND article_id = ?", task.VersionID, task.ArticleID).
			First(&version).Error; err != nil {
			return knowledgeLookupError(err)
		}
		if !version.CanParse() {
			return ErrKnowledgeVirusScanRequired
		}
		now := service.now().UTC()
		task.Status = models.KnowledgeIngestionParsing
		task.StartedAt = &now
		if err := tx.Save(&task).Error; err != nil {
			return fmt.Errorf("start knowledge parsing: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &task, nil
}

type KnowledgeChunkInput struct {
	PageNumber  *int
	SectionPath string
	Content     string
	Snippet     string
	TokenCount  int
}

func (service *KnowledgeService) StoreChunks(
	ctx context.Context,
	taskID string,
	inputs []KnowledgeChunkInput,
) ([]models.KnowledgeChunk, error) {
	operation, err := knowledgeWorkerOperation(ctx)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 || len(inputs) > 1000 {
		return nil, errors.New("knowledge ingestion requires between 1 and 1000 chunks")
	}
	chunks := make([]models.KnowledgeChunk, 0, len(inputs))
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var task models.KnowledgeIngestionTask
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", taskID).
			First(&task).Error; err != nil {
			return knowledgeLookupError(err)
		}
		if task.Status != models.KnowledgeIngestionParsing {
			return ErrKnowledgeIngestionState
		}
		var version models.KnowledgeArticleVersion
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Where("id = ? AND article_id = ?", task.VersionID, task.ArticleID).
			First(&version).Error; err != nil {
			return knowledgeLookupError(err)
		}
		if !version.CanParse() {
			return ErrKnowledgeVirusScanRequired
		}
		for index, input := range inputs {
			content := strings.TrimSpace(input.Content)
			snippet := strings.TrimSpace(input.Snippet)
			if len(snippet) > 1000 {
				return errors.New("knowledge chunk snippet exceeds maximum length")
			}
			chunk := models.KnowledgeChunk{
				OrganizationID:  operation.Scope.OrganizationID,
				ProjectID:       operation.Scope.ProjectID,
				ArticleID:       task.ArticleID,
				VersionID:       task.VersionID,
				IngestionTaskID: task.ID,
				Ordinal:         uint(index),
				PageNumber:      input.PageNumber,
				SectionPath:     strings.TrimSpace(input.SectionPath),
				Content:         content,
				Snippet:         snippet,
				TokenCount:      input.TokenCount,
			}
			if err := tx.Create(&chunk).Error; err != nil {
				return fmt.Errorf("persist knowledge chunk %d: %w", index, err)
			}
			chunks = append(chunks, chunk)
		}
		task.Status = models.KnowledgeIngestionIndexing
		if err := tx.Save(&task).Error; err != nil {
			return fmt.Errorf("advance knowledge ingestion to indexing: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func (service *KnowledgeService) CompleteIngestion(
	ctx context.Context,
	taskID string,
) (*models.KnowledgeIngestionTask, error) {
	operation, err := knowledgeWorkerOperation(ctx)
	if err != nil {
		return nil, err
	}
	var task models.KnowledgeIngestionTask
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", taskID).First(&task).Error; err != nil {
		return nil, knowledgeLookupError(err)
	}
	if task.Status != models.KnowledgeIngestionIndexing {
		return nil, ErrKnowledgeIngestionState
	}
	now := service.now().UTC()
	task.Status = models.KnowledgeIngestionCompleted
	task.CompletedAt = &now
	if err := service.db.WithContext(ctx).Save(&task).Error; err != nil {
		return nil, fmt.Errorf("complete knowledge ingestion: %w", err)
	}
	return &task, nil
}

func (service *KnowledgeService) PublishVersion(
	ctx context.Context,
	versionID string,
) (*models.KnowledgeArticleVersion, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	userID, err := parseKnowledgeHumanID(operation.Actor.ID)
	if err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	var published models.KnowledgeArticleVersion
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			return transactionForContext(
				scopedContext,
				service.db,
				func(tx *gorm.DB) error {
					access, revalidateErr :=
						service.projects.RevalidateHumanProjectAccess(
							scopedContext,
							operation.Scope,
							userID,
						)
					if revalidateErr != nil {
						return revalidateErr
					}
					if access.Role != models.ProjectRoleAdmin &&
						access.Role != models.ProjectRoleManager {
						return ErrProjectKnowledgeAccessDenied
					}
					var target struct {
						ArticleID string
					}
					if err := knowledgeScopedQuery(
						tx.Model(&models.KnowledgeArticleVersion{}),
						operation.Scope,
					).Select("article_id").
						Where("id = ?", versionID).
						Take(&target).Error; err != nil {
						return knowledgeLookupError(err)
					}
					var article models.KnowledgeArticle
					if err := knowledgeScopedQuery(tx, operation.Scope).
						Clauses(clause.Locking{Strength: "UPDATE"}).
						Where(
							"id = ? AND status = ?",
							target.ArticleID,
							models.KnowledgeArticleActive,
						).
						First(&article).Error; err != nil {
						return knowledgeLookupError(err)
					}
					if err := knowledgeScopedQuery(tx, operation.Scope).
						Clauses(clause.Locking{Strength: "UPDATE"}).
						Where(
							"id = ? AND article_id = ?",
							versionID,
							article.ID,
						).
						First(&published).Error; err != nil {
						return knowledgeLookupError(err)
					}
					if published.Status != models.KnowledgeVersionDraft ||
						published.VirusScan != models.VirusScanClean {
						return ErrKnowledgeIngestionState
					}
					var completed int64
					if err := knowledgeScopedQuery(
						tx.Model(&models.KnowledgeIngestionTask{}),
						operation.Scope,
					).Where(
						"version_id = ? AND status = ?",
						published.ID,
						models.KnowledgeIngestionCompleted,
					).Count(&completed).Error; err != nil {
						return fmt.Errorf("check completed knowledge ingestion: %w", err)
					}
					if completed == 0 {
						return ErrKnowledgeIngestionState
					}
					now := service.now().UTC()
					projectReadACL := models.KnowledgeArticleACL{
						OrganizationID: operation.Scope.OrganizationID,
						ProjectID:      operation.Scope.ProjectID,
						ArticleID:      published.ArticleID,
						SubjectType:    models.KnowledgeACLAllProject,
						SubjectID:      "*",
						Permission:     models.KnowledgeACLRead,
						GrantedByType:  operation.Actor.Type,
						GrantedByID:    operation.Actor.ID,
					}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
						Create(&projectReadACL).Error; err != nil {
						return fmt.Errorf(
							"grant published knowledge project access: %w",
							err,
						)
					}
					if err := knowledgeScopedQuery(
						tx.Model(&models.KnowledgeArticleVersion{}),
						operation.Scope,
					).Where(
						"article_id = ? AND status = ? AND id <> ?",
						published.ArticleID,
						models.KnowledgeVersionPublished,
						published.ID,
					).UpdateColumns(map[string]any{
						"status":     models.KnowledgeVersionSuperseded,
						"updated_at": now,
					}).Error; err != nil {
						return fmt.Errorf("supersede knowledge version: %w", err)
					}
					result := knowledgeScopedQuery(
						tx.Model(&models.KnowledgeArticleVersion{}),
						operation.Scope,
					).Where(
						"id = ? AND status = ?",
						published.ID,
						models.KnowledgeVersionDraft,
					).UpdateColumns(map[string]any{
						"status":       models.KnowledgeVersionPublished,
						"published_at": now,
						"updated_at":   now,
					})
					if result.Error != nil {
						return fmt.Errorf("publish knowledge version: %w", result.Error)
					}
					if result.RowsAffected != 1 {
						return ErrKnowledgeIngestionState
					}
					articleUpdate := knowledgeScopedQuery(
						tx.Model(&models.KnowledgeArticle{}),
						operation.Scope,
					).Where("id = ?", published.ArticleID).
						UpdateColumns(map[string]any{
							"current_version_id": published.ID,
							"revision":           gorm.Expr("revision + 1"),
							"updated_by_type":    operation.Actor.Type,
							"updated_by_id":      operation.Actor.ID,
							"updated_at":         now,
						})
					if articleUpdate.Error != nil {
						return fmt.Errorf(
							"activate knowledge version: %w",
							articleUpdate.Error,
						)
					}
					if articleUpdate.RowsAffected != 1 {
						return ErrKnowledgeIngestionState
					}
					if _, err := service.events.AppendDomainEventTx(
						scopedContext,
						tx,
						DomainEventInput{
							Type: eventcontract.
								KnowledgeVersionPublishedEventType,
							Subject: fmt.Sprintf(
								"knowledge/articles/%s/versions/%s",
								published.ArticleID,
								published.ID,
							),
							Actor:           operation.Actor,
							Scope:           operation.Scope,
							TraceID:         operation.TraceID,
							CorrelationID:   operation.CorrelationID,
							ResourceVersion: published.Version,
							Data: map[string]any{
								"article_id":       published.ArticleID,
								"version_id":       published.ID,
								"document_version": published.Version,
								"audience":         "project",
							},
						},
						nil,
					); err != nil {
						return err
					}
					if _, err := service.requestKnowledgeIndexRebuildTx(
						scopedContext,
						tx,
						operation,
						operation.Scope,
						"knowledge",
					); err != nil {
						return err
					}
					published.Status = models.KnowledgeVersionPublished
					published.PublishedAt = &now
					return nil
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	return &published, nil
}

type ProjectModelPolicyInput struct {
	PolicyKey               string
	ProviderKey             string
	GenerateModel           string
	EmbeddingModel          string
	RerankModel             string
	DataEgress              models.ModelDataEgressMode
	RedactionRules          []models.ModelRedactionRule
	ProviderAllowlist       []string
	ModelAllowlist          []string
	MonthlyTokenBudget      int64
	MonthlyCostBudgetMicros int64
	RequestsPerMinute       int
	TokensPerMinute         int
}

func (service *KnowledgeService) SetProjectModelPolicy(
	ctx context.Context,
	input ProjectModelPolicyInput,
) (*models.ProjectModelPolicy, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return nil, ErrKnowledgeModelPolicyDenied
	}
	userID, err := parseKnowledgeHumanID(operation.Actor.ID)
	if err != nil {
		return nil, ErrKnowledgeModelPolicyDenied
	}
	providerKey := strings.TrimSpace(input.ProviderKey)
	provider, registered := service.modelProviders[providerKey]
	if !registered || provider == nil ||
		provider.Descriptor().Key != providerKey {
		return nil, ErrKnowledgeModelPolicyDenied
	}
	redactions, err := json.Marshal(input.RedactionRules)
	if err != nil {
		return nil, err
	}
	providers, err := json.Marshal(input.ProviderAllowlist)
	if err != nil {
		return nil, err
	}
	allowedModels, err := json.Marshal(input.ModelAllowlist)
	if err != nil {
		return nil, err
	}
	policyKey := strings.TrimSpace(input.PolicyKey)
	if policyKey == "" {
		policyKey = "knowledge"
	}
	desired := models.ProjectModelPolicy{
		OrganizationID:          operation.Scope.OrganizationID,
		ProjectID:               operation.Scope.ProjectID,
		PolicyKey:               policyKey,
		IsActive:                true,
		ProviderKey:             providerKey,
		GenerateModel:           strings.TrimSpace(input.GenerateModel),
		EmbeddingModel:          strings.TrimSpace(input.EmbeddingModel),
		RerankModel:             strings.TrimSpace(input.RerankModel),
		DataEgress:              input.DataEgress,
		RedactionRules:          datatypes.JSON(redactions),
		ProviderAllowlist:       datatypes.JSON(providers),
		ModelAllowlist:          datatypes.JSON(allowedModels),
		MonthlyTokenBudget:      input.MonthlyTokenBudget,
		MonthlyCostBudgetMicros: input.MonthlyCostBudgetMicros,
		RequestsPerMinute:       input.RequestsPerMinute,
		TokensPerMinute:         input.TokensPerMinute,
		CreatedByType:           operation.Actor.Type,
		CreatedByID:             operation.Actor.ID,
	}
	if err := desired.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKnowledgeModelPolicyDenied, err)
	}
	var policy models.ProjectModelPolicy
	err = runProjectOperation(
		ctx,
		service.db,
		func(scopedContext context.Context) error {
			return transactionForContext(
				scopedContext,
				service.db,
				func(tx *gorm.DB) error {
					access, err :=
						service.projects.RevalidateHumanProjectAccess(
							scopedContext,
							operation.Scope,
							userID,
						)
					if err != nil {
						return err
					}
					if access.Role != models.ProjectRoleAdmin &&
						access.Role != models.ProjectRoleManager {
						return ErrKnowledgeModelPolicyDenied
					}
					var previousDigest string
					var previousEgress models.ModelDataEgressMode
					action := "created"
					query := knowledgeScopedQuery(tx, operation.Scope).
						Clauses(clause.Locking{Strength: "UPDATE"}).
						Where("policy_key = ?", policyKey).
						First(&policy)
					switch {
					case query.Error == nil:
						action = "updated"
						previousDigest = knowledgeModelPolicyAuditDigest(policy)
						previousEgress = policy.DataEgress
						update := knowledgeScopedQuery(
							tx.Model(&policy),
							operation.Scope,
						).Where(
							"id = ? AND policy_key = ?",
							policy.ID,
							policyKey,
						).Updates(map[string]any{
							"provider_key":               desired.ProviderKey,
							"generate_model":             desired.GenerateModel,
							"embedding_model":            desired.EmbeddingModel,
							"rerank_model":               desired.RerankModel,
							"data_egress":                desired.DataEgress,
							"redaction_rules":            desired.RedactionRules,
							"provider_allowlist":         desired.ProviderAllowlist,
							"model_allowlist":            desired.ModelAllowlist,
							"monthly_token_budget":       desired.MonthlyTokenBudget,
							"monthly_cost_budget_micros": desired.MonthlyCostBudgetMicros,
							"requests_per_minute":        desired.RequestsPerMinute,
							"tokens_per_minute":          desired.TokensPerMinute,
							"is_active":                  true,
						})
						if update.Error != nil {
							return fmt.Errorf(
								"update project model policy: %w",
								update.Error,
							)
						}
						if update.RowsAffected != 1 {
							return ErrKnowledgeNotFound
						}
						if err := knowledgeScopedQuery(tx, operation.Scope).
							Where("id = ?", policy.ID).
							First(&policy).Error; err != nil {
							return fmt.Errorf(
								"reload project model policy: %w",
								err,
							)
						}
					case !errors.Is(query.Error, gorm.ErrRecordNotFound):
						return fmt.Errorf(
							"load project model policy: %w",
							query.Error,
						)
					default:
						policy = desired
						if err := tx.Create(&policy).Error; err != nil {
							return fmt.Errorf(
								"create project model policy: %w",
								err,
							)
						}
					}
					resourceVersion := uint64(policy.UpdatedAt.UTC().UnixNano())
					_, err = service.events.AppendDomainEventTx(
						scopedContext,
						tx,
						DomainEventInput{
							Type: "io.chronodesk.knowledge.model-policy." +
								action + ".v1",
							Subject:         "knowledge/model-policies/" + policy.ID,
							Actor:           operation.Actor,
							Scope:           operation.Scope,
							TraceID:         operation.TraceID,
							CorrelationID:   operation.CorrelationID,
							ResourceVersion: resourceVersion,
							Data: map[string]any{
								"policy_key":      policy.PolicyKey,
								"provider_key":    policy.ProviderKey,
								"previous_digest": previousDigest,
								"current_digest": knowledgeModelPolicyAuditDigest(
									policy,
								),
								"previous_data_egress": previousEgress,
								"current_data_egress":  policy.DataEgress,
							},
						},
						nil,
					)
					if err != nil {
						return fmt.Errorf(
							"append knowledge model policy event: %w",
							err,
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
	return &policy, nil
}

type KnowledgeSearchInput struct {
	Query            string `json:"query"`
	Limit            int    `json:"limit"`
	PolicyDecisionID string `json:"-"`
}

type KnowledgeSearchResult struct {
	SearchID string                     `json:"search_id"`
	Items    []models.KnowledgeCitation `json:"items"`
}

func (service *KnowledgeService) Search(
	ctx context.Context,
	input KnowledgeSearchInput,
) (*KnowledgeSearchResult, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(input.Query)
	if query == "" || len([]rune(query)) > 2000 {
		return nil, errors.New("knowledge search query is invalid")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return nil, errors.New("knowledge search limit must be between 1 and 50")
	}
	if service.searchIndex == nil {
		return nil, ErrKnowledgeIndexUnavailable
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"knowledge search orchestration",
	); err != nil {
		return nil, err
	}
	snapshot, err := service.captureKnowledgeSearchSnapshot(
		ctx,
		operation,
		input.PolicyDecisionID,
	)
	if err != nil {
		return nil, err
	}
	subjects := snapshot.subjects
	policy := snapshot.policy
	provider := snapshot.provider
	modelQuery := query
	var queryEmbedding []float32
	hybrid := provider != nil
	limits := ModelCallLimits{}
	candidateLimit := limit
	if hybrid {
		modelQuery, err = prepareKnowledgeModelContent(
			query,
			policy,
			provider.Descriptor(),
		)
		if err != nil {
			return nil, err
		}
		limits = modelLimitsFromPolicy(policy)
		if err := requireExternalIOOutsideProjectTransaction(
			ctx,
			"knowledge query embedding",
		); err != nil {
			return nil, err
		}
		embedding, embedErr := provider.Embed(ctx, ModelEmbedRequest{
			Scope:  operation.Scope,
			Model:  policy.EmbeddingModel,
			Inputs: []string{modelQuery},
			Limits: limits,
		})
		if embedErr != nil {
			return nil, fmt.Errorf("embed knowledge query: %w", embedErr)
		}
		if len(embedding.Embeddings) != 1 ||
			len(embedding.Embeddings[0]) == 0 {
			return nil, ErrKnowledgeModelResponseInvalid
		}
		queryEmbedding = append(
			[]float32(nil),
			embedding.Embeddings[0]...,
		)
		candidateLimit = limit * 4
		if candidateLimit > 100 {
			candidateLimit = 100
		}
	}
	filter := HybridSearchFilter{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		ACLSubjects:    subjects,
		PublishedOnly:  true,
		VirusScan:      models.VirusScanClean,
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"knowledge index search",
	); err != nil {
		return nil, err
	}
	hits, err := service.searchIndex.Search(ctx, HybridSearchRequest{
		Query:          modelQuery,
		QueryEmbedding: queryEmbedding,
		Limit:          candidateLimit,
		Filter:         filter,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrKnowledgeIndexUnavailable),
			errors.Is(err, ErrKnowledgeIndexBoundaryViolation),
			errors.Is(err, ErrKnowledgeIndexResponseInvalid):
			return nil, fmt.Errorf("knowledge index search: %w", err)
		default:
			return nil, fmt.Errorf(
				"knowledge index search: %w: %v",
				ErrKnowledgeIndexResponseInvalid,
				err,
			)
		}
	}
	if len(hits) == 0 {
		searchID, err := newKnowledgeSearchID()
		if err != nil {
			return nil, err
		}
		if err := service.finalizeKnowledgeSearch(
			ctx,
			operation,
			snapshot.epoch,
			input.PolicyDecisionID,
			nil,
			nil,
		); err != nil {
			return nil, err
		}
		return &KnowledgeSearchResult{
			SearchID: searchID,
			Items:    []models.KnowledgeCitation{},
		}, nil
	}
	indexChunkIDs := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		if err := validateKnowledgeSearchHit(operation.Scope, hit); err != nil {
			return nil, err
		}
		if _, duplicate := indexChunkIDs[hit.ChunkID]; duplicate {
			return nil, ErrKnowledgeIndexResponseInvalid
		}
		indexChunkIDs[hit.ChunkID] = struct{}{}
	}
	hits, err = service.prevalidateKnowledgeSearchHits(
		ctx,
		operation,
		snapshot.epoch,
		input.PolicyDecisionID,
		hits,
	)
	if err != nil {
		return nil, err
	}
	if !hybrid {
		if len(hits) > limit {
			hits = hits[:limit]
		}
		searchID, err := newKnowledgeSearchID()
		if err != nil {
			return nil, err
		}
		citations := make(
			[]models.KnowledgeCitation,
			0,
			len(hits),
		)
		seen := make(map[string]struct{}, len(hits))
		for index, hit := range hits {
			if err := validateKnowledgeSearchHit(
				operation.Scope,
				hit,
			); err != nil {
				return nil, err
			}
			if _, duplicate := seen[hit.ChunkID]; duplicate {
				return nil, ErrKnowledgeIndexResponseInvalid
			}
			seen[hit.ChunkID] = struct{}{}
			citations = append(citations, models.KnowledgeCitation{
				OrganizationID:  operation.Scope.OrganizationID,
				ProjectID:       operation.Scope.ProjectID,
				SearchID:        searchID,
				ArticleID:       hit.ArticleID,
				VersionID:       hit.VersionID,
				DocumentVersion: hit.DocumentVersion,
				ChunkID:         hit.ChunkID,
				PageNumber:      hit.PageNumber,
				Snippet:         hit.Snippet,
				ContentHash:     hit.ContentHash,
				Rank:            index + 1,
				Score:           hit.Score,
				CreatedByType:   operation.Actor.Type,
				CreatedByID:     operation.Actor.ID,
			})
		}
		if err := service.finalizeKnowledgeSearch(
			ctx,
			operation,
			snapshot.epoch,
			input.PolicyDecisionID,
			hits,
			citations,
		); err != nil {
			return nil, err
		}
		return &KnowledgeSearchResult{
			SearchID: searchID,
			Items:    citations,
		}, nil
	}
	candidates := make([]ModelRerankCandidate, 0, len(hits))
	hitsByID := make(map[string]HybridSearchHit, len(hits))
	for _, hit := range hits {
		if err := validateKnowledgeSearchHit(operation.Scope, hit); err != nil {
			return nil, err
		}
		if _, duplicate := hitsByID[hit.ChunkID]; duplicate {
			return nil, ErrKnowledgeIndexResponseInvalid
		}
		content, err := prepareKnowledgeModelContent(
			hit.Snippet,
			policy,
			provider.Descriptor(),
		)
		if err != nil {
			return nil, err
		}
		hitsByID[hit.ChunkID] = hit
		candidates = append(candidates, ModelRerankCandidate{
			ID:      hit.ChunkID,
			Content: content,
		})
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"knowledge result rerank",
	); err != nil {
		return nil, err
	}
	reranked, err := provider.Rerank(ctx, ModelRerankRequest{
		Scope:      operation.Scope,
		Model:      policy.RerankModel,
		Query:      modelQuery,
		Candidates: candidates,
		Limit:      limit,
		Limits:     limits,
	})
	if err != nil {
		return nil, fmt.Errorf("rerank knowledge results: %w", err)
	}
	if len(reranked.Items) == 0 || len(reranked.Items) > limit {
		return nil, ErrKnowledgeModelResponseInvalid
	}
	searchID, err := newKnowledgeSearchID()
	if err != nil {
		return nil, err
	}
	citations := make([]models.KnowledgeCitation, 0, len(reranked.Items))
	selectedHits := make([]HybridSearchHit, 0, len(reranked.Items))
	seen := make(map[string]struct{}, len(reranked.Items))
	for index, item := range reranked.Items {
		hit, exists := hitsByID[item.ID]
		if !exists {
			return nil, ErrKnowledgeModelResponseInvalid
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, ErrKnowledgeModelResponseInvalid
		}
		seen[item.ID] = struct{}{}
		selectedHits = append(selectedHits, hit)
		citations = append(citations, models.KnowledgeCitation{
			OrganizationID:  operation.Scope.OrganizationID,
			ProjectID:       operation.Scope.ProjectID,
			SearchID:        searchID,
			ArticleID:       hit.ArticleID,
			VersionID:       hit.VersionID,
			DocumentVersion: hit.DocumentVersion,
			ChunkID:         hit.ChunkID,
			PageNumber:      hit.PageNumber,
			Snippet:         hit.Snippet,
			ContentHash:     hit.ContentHash,
			Rank:            index + 1,
			Score:           item.Score,
			CreatedByType:   operation.Actor.Type,
			CreatedByID:     operation.Actor.ID,
		})
	}
	if err := service.finalizeKnowledgeSearch(
		ctx,
		operation,
		snapshot.epoch,
		input.PolicyDecisionID,
		selectedHits,
		citations,
	); err != nil {
		return nil, err
	}
	return &KnowledgeSearchResult{
		SearchID: searchID,
		Items:    citations,
	}, nil
}

func (service *KnowledgeService) RecordFeedback(
	ctx context.Context,
	citationID string,
	rating models.KnowledgeFeedbackRating,
	comment string,
) (*models.KnowledgeFeedback, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	var citation models.KnowledgeCitation
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", citationID).First(&citation).Error; err != nil {
		return nil, knowledgeLookupError(err)
	}
	feedback := models.KnowledgeFeedback{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		CitationID:     citation.ID,
		Rating:         rating,
		Comment:        strings.TrimSpace(comment),
		ActorType:      operation.Actor.Type,
		ActorID:        operation.Actor.ID,
	}
	if err := service.db.WithContext(ctx).Create(&feedback).Error; err != nil {
		return nil, fmt.Errorf("create knowledge feedback: %w", err)
	}
	return &feedback, nil
}

func (service *KnowledgeService) RebuildIndex(
	ctx context.Context,
) (*models.KnowledgeIndexState, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if service.searchIndex == nil {
		return nil, ErrKnowledgeIndexUnavailable
	}
	if service.events == nil || service.projects == nil {
		return nil, errors.New(
			"knowledge index rebuild event pipeline is unavailable",
		)
	}
	if operation.Actor.Type != models.ActorTypeHuman {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	userID, err := parseKnowledgeHumanID(operation.Actor.ID)
	if err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	var state models.KnowledgeIndexState
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			access, revalidateErr :=
				service.projects.RevalidateHumanProjectAccess(
					scopedContext,
					operation.Scope,
					userID,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if access.Role != models.ProjectRoleAdmin &&
				access.Role != models.ProjectRoleManager {
				return ErrProjectKnowledgeAccessDenied
			}
			return transactionForContext(
				scopedContext,
				service.db,
				func(tx *gorm.DB) error {
					var requestErr error
					state, requestErr =
						service.requestKnowledgeIndexRebuildTx(
							scopedContext,
							tx,
							operation,
							operation.Scope,
							"knowledge",
						)
					return requestErr
				},
			)
		},
	)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (service *KnowledgeService) GetIndexState(
	ctx context.Context,
) (*models.KnowledgeIndexState, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	var state models.KnowledgeIndexState
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("index_name = ?", "knowledge").
		Take(&state).Error; err != nil {
		return nil, knowledgeLookupError(err)
	}
	return &state, nil
}

func embedKnowledgeIndexDocuments(
	ctx context.Context,
	scope models.ProjectScope,
	documents []HybridIndexDocument,
	policy models.ProjectModelPolicy,
	provider ModelProvider,
) ([]HybridIndexDocument, error) {
	if provider == nil {
		return nil, ErrKnowledgeModelPolicyDenied
	}
	const batchSize = 32
	limits := modelLimitsFromPolicy(policy)
	embeddingDimension := 0
	for offset := 0; offset < len(documents); offset += batchSize {
		end := offset + batchSize
		if end > len(documents) {
			end = len(documents)
		}
		inputs := make([]string, 0, end-offset)
		for index := offset; index < end; index++ {
			content, err := prepareKnowledgeModelContent(
				documents[index].Content,
				policy,
				provider.Descriptor(),
			)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, content)
		}
		response, err := provider.Embed(ctx, ModelEmbedRequest{
			Scope:  scope,
			Model:  policy.EmbeddingModel,
			Inputs: inputs,
			Limits: limits,
		})
		if err != nil {
			return nil, fmt.Errorf("embed knowledge index batch: %w", err)
		}
		if len(response.Embeddings) != len(inputs) {
			return nil, ErrKnowledgeModelResponseInvalid
		}
		for index, embedding := range response.Embeddings {
			if len(embedding) == 0 {
				return nil, ErrKnowledgeModelResponseInvalid
			}
			if embeddingDimension == 0 {
				embeddingDimension = len(embedding)
			}
			if len(embedding) != embeddingDimension {
				return nil, ErrKnowledgeModelResponseInvalid
			}
			documentIndex := offset + index
			documents[documentIndex].Embedding = append(
				[]float32(nil),
				embedding...,
			)
		}
	}
	return documents, nil
}

func (service *KnowledgeService) loadKnowledgeIndexDocumentBatch(
	ctx context.Context,
	scope models.ProjectScope,
	afterChunkID string,
	limit int,
) ([]HybridIndexDocument, string, bool, error) {
	if limit < 1 || limit > knowledgeIndexRebuildMaxBatchSize {
		return nil, "", false, errors.New(
			"knowledge index rebuild batch size is invalid",
		)
	}
	type indexedChunk struct {
		models.KnowledgeChunk
		DocumentVersion uint64 `gorm:"column:document_version"`
	}
	var rows []indexedChunk
	query := service.db.WithContext(ctx).
		Table("knowledge_chunks AS chunks").
		Select("chunks.*, versions.version AS document_version").
		Joins(
			"JOIN knowledge_article_versions AS versions "+
				"ON versions.id = chunks.version_id "+
				"AND versions.organization_id = chunks.organization_id "+
				"AND versions.project_id = chunks.project_id",
		).
		Joins(
			"JOIN knowledge_articles AS articles "+
				"ON articles.id = versions.article_id "+
				"AND articles.organization_id = versions.organization_id "+
				"AND articles.project_id = versions.project_id "+
				"AND articles.current_version_id = versions.id",
		).
		Where(
			"chunks.organization_id = ? AND chunks.project_id = ? "+
				"AND articles.status = ? "+
				"AND versions.status = ? AND versions.virus_scan = ?",
			scope.OrganizationID,
			scope.ProjectID,
			models.KnowledgeArticleActive,
			models.KnowledgeVersionPublished,
			models.VirusScanClean,
		)
	if afterChunkID = strings.TrimSpace(afterChunkID); afterChunkID != "" {
		query = query.Where("chunks.id > ?", afterChunkID)
	}
	err := query.
		Order("chunks.id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, "", false, fmt.Errorf(
			"load knowledge index chunk batch: %w",
			err,
		)
	}
	exhausted := len(rows) < limit
	nextChunkID := afterChunkID
	if len(rows) > 0 {
		nextChunkID = rows[len(rows)-1].ID
	}
	articleIDs := make([]string, 0)
	seenArticles := make(map[string]struct{})
	for _, row := range rows {
		if _, exists := seenArticles[row.ArticleID]; !exists {
			seenArticles[row.ArticleID] = struct{}{}
			articleIDs = append(articleIDs, row.ArticleID)
		}
	}
	var aclRows []models.KnowledgeArticleACL
	if len(articleIDs) > 0 {
		if err := knowledgeScopedQuery(
			service.db.WithContext(ctx),
			scope,
		).Where(
			"article_id IN ? AND permission IN ?",
			articleIDs,
			[]models.KnowledgeACLPermission{
				models.KnowledgeACLRead,
				models.KnowledgeACLManage,
			},
		).Order("article_id ASC, subject_type ASC, subject_id ASC").
			Find(&aclRows).Error; err != nil {
			return nil, "", false, fmt.Errorf(
				"load knowledge index ACL batch: %w",
				err,
			)
		}
	}
	aclByArticle := make(map[string][]models.KnowledgeACLSubject)
	for _, acl := range aclRows {
		aclByArticle[acl.ArticleID] = append(
			aclByArticle[acl.ArticleID],
			acl.Subject(),
		)
	}
	documents := make([]HybridIndexDocument, 0, len(rows))
	for _, row := range rows {
		subjects := aclByArticle[row.ArticleID]
		if len(subjects) == 0 {
			continue
		}
		documents = append(documents, HybridIndexDocument{
			OrganizationID:  scope.OrganizationID,
			ProjectID:       scope.ProjectID,
			ArticleID:       row.ArticleID,
			VersionID:       row.VersionID,
			DocumentVersion: row.DocumentVersion,
			ChunkID:         row.ID,
			PageNumber:      row.PageNumber,
			Content:         row.Content,
			Snippet:         row.Snippet,
			ContentHash:     row.ContentHash,
			TokenCount:      row.TokenCount,
			ACLSubjects:     append([]models.KnowledgeACLSubject(nil), subjects...),
		})
	}
	return documents, nextChunkID, exhausted, nil
}

func (service *KnowledgeService) resolveKnowledgeSubjects(
	ctx context.Context,
	operation OperationContext,
) ([]models.KnowledgeACLSubject, error) {
	subjects := []models.KnowledgeACLSubject{
		{Type: models.KnowledgeACLAllProject, ID: "*"},
	}
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		subjects = append(subjects, models.KnowledgeACLSubject{
			Type: models.KnowledgeACLHuman,
			ID:   operation.Actor.ID,
		})
	case models.ActorTypeServicePrincipal:
		subjects = append(subjects, models.KnowledgeACLSubject{
			Type: models.KnowledgeACLServicePrincipal,
			ID:   operation.Actor.ID,
		})
	}
	if service.accessResolver != nil {
		resolved, err := service.accessResolver.ResolveKnowledgeSubjects(
			ctx,
			operation.Scope,
			operation.Actor,
		)
		if err != nil {
			return nil, fmt.Errorf("resolve knowledge ACL subjects: %w", err)
		}
		subjects = append(subjects, resolved...)
	}
	unique := make(map[string]models.KnowledgeACLSubject, len(subjects))
	for _, subject := range subjects {
		if err := subject.Validate(); err != nil {
			return nil, err
		}
		unique[string(subject.Type)+"\x00"+subject.ID] = subject
	}
	result := make([]models.KnowledgeACLSubject, 0, len(unique))
	for _, subject := range unique {
		result = append(result, subject)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (service *KnowledgeService) resolveKnowledgeModelPolicy(
	ctx context.Context,
	scope models.ProjectScope,
) (models.ProjectModelPolicy, ModelProvider, error) {
	var policy models.ProjectModelPolicy
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		scope,
	).Where(
		"policy_key = ? AND is_active = ?",
		"knowledge",
		true,
	).First(&policy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return policy, nil, ErrKnowledgeModelPolicyUnavailable
		}
		return policy, nil, fmt.Errorf("load knowledge model policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return policy, nil, fmt.Errorf("%w: %v", ErrKnowledgeModelPolicyDenied, err)
	}
	provider, exists := service.modelProviders[policy.ProviderKey]
	if !exists || provider == nil {
		return policy, nil, ErrKnowledgeModelPolicyUnavailable
	}
	descriptor := provider.Descriptor()
	if descriptor.Key != policy.ProviderKey {
		return policy, nil, ErrKnowledgeModelPolicyDenied
	}
	if descriptor.IsExternal &&
		policy.DataEgress == models.ModelDataEgressDenied {
		return policy, nil, ErrKnowledgeModelPolicyDenied
	}
	return policy, provider, nil
}

func prepareKnowledgeModelContent(
	content string,
	policy models.ProjectModelPolicy,
	descriptor ModelProviderDescriptor,
) (string, error) {
	if !descriptor.IsExternal {
		return content, nil
	}
	switch policy.DataEgress {
	case models.ModelDataEgressAllowed:
		return content, nil
	case models.ModelDataEgressRedacted:
		rules, err := policy.Redactions()
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrKnowledgeModelPolicyDenied, err)
		}
		result := content
		for _, rule := range rules {
			result = strings.ReplaceAll(result, rule.Literal, rule.Replacement)
		}
		return result, nil
	default:
		return "", ErrKnowledgeModelPolicyDenied
	}
}

func modelLimitsFromPolicy(
	policy models.ProjectModelPolicy,
) ModelCallLimits {
	return ModelCallLimits{
		MonthlyTokenBudget:      policy.MonthlyTokenBudget,
		MonthlyCostBudgetMicros: policy.MonthlyCostBudgetMicros,
		RequestsPerMinute:       policy.RequestsPerMinute,
		TokensPerMinute:         policy.TokensPerMinute,
	}
}

func knowledgeModelPolicyAuditDigest(
	policy models.ProjectModelPolicy,
) string {
	payload, _ := json.Marshal(struct {
		PolicyKey               string
		ProviderKey             string
		GenerateModel           string
		EmbeddingModel          string
		RerankModel             string
		DataEgress              models.ModelDataEgressMode
		RedactionRules          json.RawMessage
		ProviderAllowlist       json.RawMessage
		ModelAllowlist          json.RawMessage
		MonthlyTokenBudget      int64
		MonthlyCostBudgetMicros int64
		RequestsPerMinute       int
		TokensPerMinute         int
		IsActive                bool
	}{
		PolicyKey:               policy.PolicyKey,
		ProviderKey:             policy.ProviderKey,
		GenerateModel:           policy.GenerateModel,
		EmbeddingModel:          policy.EmbeddingModel,
		RerankModel:             policy.RerankModel,
		DataEgress:              policy.DataEgress,
		RedactionRules:          json.RawMessage(policy.RedactionRules),
		ProviderAllowlist:       json.RawMessage(policy.ProviderAllowlist),
		ModelAllowlist:          json.RawMessage(policy.ModelAllowlist),
		MonthlyTokenBudget:      policy.MonthlyTokenBudget,
		MonthlyCostBudgetMicros: policy.MonthlyCostBudgetMicros,
		RequestsPerMinute:       policy.RequestsPerMinute,
		TokensPerMinute:         policy.TokensPerMinute,
		IsActive:                policy.IsActive,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateKnowledgeSearchHit(
	scope models.ProjectScope,
	hit HybridSearchHit,
) error {
	if hit.OrganizationID != scope.OrganizationID ||
		hit.ProjectID != scope.ProjectID {
		return ErrKnowledgeIndexBoundaryViolation
	}
	if strings.TrimSpace(hit.ArticleID) == "" ||
		strings.TrimSpace(hit.VersionID) == "" ||
		strings.TrimSpace(hit.ChunkID) == "" ||
		hit.DocumentVersion == 0 ||
		strings.TrimSpace(hit.Snippet) == "" ||
		len(hit.ContentHash) != sha256.Size*2 {
		return ErrKnowledgeIndexResponseInvalid
	}
	if _, err := hex.DecodeString(hit.ContentHash); err != nil {
		return ErrKnowledgeIndexResponseInvalid
	}
	if hit.PageNumber != nil && *hit.PageNumber <= 0 {
		return ErrKnowledgeIndexResponseInvalid
	}
	return nil
}

func (service *KnowledgeService) requestKnowledgeIndexRebuildTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	scope models.ProjectScope,
	indexName string,
) (models.KnowledgeIndexState, error) {
	var state models.KnowledgeIndexState
	err := knowledgeScopedQuery(tx, scope).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("index_name = ?", indexName).
		First(&state).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		state = models.KnowledgeIndexState{
			OrganizationID:    scope.OrganizationID,
			ProjectID:         scope.ProjectID,
			IndexName:         indexName,
			Generation:        0,
			DesiredGeneration: 1,
			Status:            models.KnowledgeIndexRebuildRequested,
		}
		if err := tx.Create(&state).Error; err != nil {
			return models.KnowledgeIndexState{}, fmt.Errorf(
				"create knowledge index rebuild request: %w",
				err,
			)
		}
	case err != nil:
		return models.KnowledgeIndexState{}, fmt.Errorf(
			"load knowledge index rebuild state: %w",
			err,
		)
	default:
		if state.DesiredGeneration <= state.Generation {
			state.DesiredGeneration = state.Generation + 1
		} else {
			state.DesiredGeneration++
		}
		state.Status = models.KnowledgeIndexRebuildRequested
		state.CompletedAt = nil
		state.FailureDetail = ""
		if err := tx.Save(&state).Error; err != nil {
			return models.KnowledgeIndexState{}, fmt.Errorf(
				"request knowledge index rebuild: %w",
				err,
			)
		}
	}
	_, err = service.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type: eventcontract.KnowledgeIndexRebuildRequestedEventType,
			Subject: fmt.Sprintf(
				"knowledge/index-rebuild/%s",
				state.ID,
			),
			Actor:           operation.Actor,
			Scope:           scope,
			TraceID:         operation.TraceID,
			CorrelationID:   operation.CorrelationID,
			ResourceVersion: state.DesiredGeneration,
			Data: map[string]any{
				"state_id":   state.ID,
				"generation": state.DesiredGeneration,
			},
		},
		[]OutboxTarget{{
			Type: KnowledgeIndexRebuildOutboxDestination,
			ID: fmt.Sprintf(
				"%s:%d",
				state.ID,
				state.DesiredGeneration,
			),
			MaxAttempts: 8,
		}},
	)
	if err != nil {
		return models.KnowledgeIndexState{}, err
	}
	return state, nil
}

func knowledgeSubjectsDigest(subjects []models.KnowledgeACLSubject) string {
	values := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		values = append(values, string(subject.Type)+":"+subject.ID)
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}

func knowledgeOperation(ctx context.Context) (OperationContext, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return OperationContext{}, err
	}
	if err := operation.Scope.Validate(); err != nil {
		return OperationContext{}, err
	}
	return operation, nil
}

func knowledgeWorkerOperation(ctx context.Context) (OperationContext, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return OperationContext{}, err
	}
	if operation.Source != SourceProtocolWorker ||
		operation.Actor.Type != models.ActorTypeSystem {
		return OperationContext{}, ErrKnowledgeWorkerRequired
	}
	return operation, nil
}

func knowledgeScopedQuery(
	db *gorm.DB,
	scope models.ProjectScope,
) *gorm.DB {
	return db.Where(
		"organization_id = ? AND project_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
	)
}

func knowledgeLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrKnowledgeNotFound
	}
	return err
}

func newKnowledgeSearchID() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate knowledge search id: %w", err)
	}
	return value.String(), nil
}
