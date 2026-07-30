package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
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
	ErrKnowledgeModelPolicyDenied      = errors.New("knowledge model policy denied the operation")
	ErrKnowledgeModelResponseInvalid   = errors.New("knowledge model provider response is invalid")
	ErrKnowledgeWorkerRequired         = errors.New("trusted knowledge worker operation is required")
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
	SearchIndex    HybridSearchIndex
	AccessResolver KnowledgeAccessResolver
	ModelProviders map[string]ModelProvider
}

type KnowledgeService struct {
	db             *gorm.DB
	searchIndex    HybridSearchIndex
	accessResolver KnowledgeAccessResolver
	modelProviders map[string]ModelProvider
	now            func() time.Time
}

func NewKnowledgeService(
	db *gorm.DB,
	dependencies KnowledgeServiceDependencies,
) (*KnowledgeService, error) {
	if db == nil {
		return nil, errors.New("knowledge database is required")
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
	return &KnowledgeService{
		db:             db,
		searchIndex:    dependencies.SearchIndex,
		accessResolver: dependencies.AccessResolver,
		modelProviders: providers,
		now:            time.Now,
	}, nil
}

type CreateKnowledgeArticleInput struct {
	Key                string
	Title              string
	Summary            string
	GrantProjectAccess bool
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
	var acl models.KnowledgeArticleACL
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var article models.KnowledgeArticle
		if err := knowledgeScopedQuery(tx, operation.Scope).
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
	var published models.KnowledgeArticleVersion
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		if err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", versionID).
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
		if err := knowledgeScopedQuery(
			tx.Model(&models.KnowledgeArticle{}),
			operation.Scope,
		).Where("id = ?", published.ArticleID).
			UpdateColumns(map[string]any{
				"current_version_id": published.ID,
				"revision":           gorm.Expr("revision + 1"),
				"updated_by_type":    operation.Actor.Type,
				"updated_by_id":      operation.Actor.ID,
				"updated_at":         now,
			}).Error; err != nil {
			return fmt.Errorf("activate knowledge version: %w", err)
		}
		if err := requestKnowledgeIndexRebuildTx(
			tx,
			operation.Scope,
			"knowledge",
		); err != nil {
			return err
		}
		published.Status = models.KnowledgeVersionPublished
		published.PublishedAt = &now
		return nil
	})
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
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		query := knowledgeScopedQuery(tx, operation.Scope).
			Where("policy_key = ?", policyKey).
			First(&policy)
		switch {
		case query.Error == nil:
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
				return fmt.Errorf("reload project model policy: %w", err)
			}
			return nil
		case !errors.Is(query.Error, gorm.ErrRecordNotFound):
			return fmt.Errorf("load project model policy: %w", query.Error)
		}
		policy = desired
		if err := tx.Create(&policy).Error; err != nil {
			return fmt.Errorf("create project model policy: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

type KnowledgeSearchInput struct {
	Query string
	Limit int
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
	subjects, err := service.resolveKnowledgeSubjects(ctx, operation)
	if err != nil {
		return nil, err
	}
	policy, provider, err := service.resolveKnowledgeModelPolicy(
		ctx,
		operation.Scope,
	)
	if err != nil {
		return nil, err
	}
	modelQuery, err := prepareKnowledgeModelContent(
		query,
		policy,
		provider.Descriptor(),
	)
	if err != nil {
		return nil, err
	}
	limits := modelLimitsFromPolicy(policy)
	embedding, err := provider.Embed(ctx, ModelEmbedRequest{
		Scope:  operation.Scope,
		Model:  policy.EmbeddingModel,
		Inputs: []string{modelQuery},
		Limits: limits,
	})
	if err != nil {
		return nil, fmt.Errorf("embed knowledge query: %w", err)
	}
	if len(embedding.Embeddings) != 1 ||
		len(embedding.Embeddings[0]) == 0 {
		return nil, ErrKnowledgeModelResponseInvalid
	}
	candidateLimit := limit * 4
	if candidateLimit > 100 {
		candidateLimit = 100
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
	hits, err := service.searchIndex.Search(ctx, HybridSearchRequest{
		Query:          modelQuery,
		QueryEmbedding: embedding.Embeddings[0],
		Limit:          candidateLimit,
		Filter:         filter,
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid knowledge search: %w", err)
	}
	if len(hits) == 0 {
		searchID, err := newKnowledgeSearchID()
		if err != nil {
			return nil, err
		}
		return &KnowledgeSearchResult{
			SearchID: searchID,
			Items:    []models.KnowledgeCitation{},
		}, nil
	}
	candidates := make([]ModelRerankCandidate, 0, len(hits))
	hitsByID := make(map[string]HybridSearchHit, len(hits))
	for _, hit := range hits {
		if err := validateKnowledgeSearchHit(operation.Scope, hit); err != nil {
			return nil, err
		}
		if _, duplicate := hitsByID[hit.ChunkID]; duplicate {
			return nil, ErrKnowledgeModelResponseInvalid
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
	if err := service.db.WithContext(ctx).Create(&citations).Error; err != nil {
		return nil, fmt.Errorf("persist knowledge citations: %w", err)
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
	var state models.KnowledgeIndexState
	err = transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		err := knowledgeScopedQuery(tx, operation.Scope).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("index_name = ?", "knowledge").
			First(&state).Error
		switch {
		case err == nil:
			if state.DesiredGeneration <= state.Generation {
				state.DesiredGeneration = state.Generation + 1
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			state = models.KnowledgeIndexState{
				OrganizationID:    operation.Scope.OrganizationID,
				ProjectID:         operation.Scope.ProjectID,
				IndexName:         "knowledge",
				Generation:        0,
				DesiredGeneration: 1,
				Status:            models.KnowledgeIndexRebuildRequested,
			}
			if err := tx.Create(&state).Error; err != nil {
				return fmt.Errorf("create knowledge index state: %w", err)
			}
		default:
			return fmt.Errorf("load knowledge index state: %w", err)
		}
		now := service.now().UTC()
		state.Status = models.KnowledgeIndexBuilding
		state.StartedAt = &now
		state.CompletedAt = nil
		state.FailureDetail = ""
		if err := tx.Save(&state).Error; err != nil {
			return fmt.Errorf("start knowledge index rebuild: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	documents, sourceDigest, err := service.loadKnowledgeIndexDocuments(
		ctx,
		operation.Scope,
	)
	if err == nil && len(documents) > 0 {
		var policy models.ProjectModelPolicy
		var provider ModelProvider
		policy, provider, err = service.resolveKnowledgeModelPolicy(
			ctx,
			operation.Scope,
		)
		if err == nil {
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
		err = service.searchIndex.ReplaceProject(ctx, HybridIndexReplacement{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			Generation:     state.DesiredGeneration,
			SourceDigest:   sourceDigest,
			Documents:      documents,
		})
	}
	now := service.now().UTC()
	if err != nil {
		failure := knowledgeScopedQuery(
			service.db.WithContext(ctx).Model(&models.KnowledgeIndexState{}),
			operation.Scope,
		).Where("id = ?", state.ID).UpdateColumns(map[string]any{
			"status":         models.KnowledgeIndexFailed,
			"failure_detail": "知识索引重建失败",
			"completed_at":   now,
			"updated_at":     now,
		})
		if failure.Error != nil {
			return nil, fmt.Errorf(
				"knowledge index rebuild failed (%v), persist failure: %w",
				err,
				failure.Error,
			)
		}
		return nil, fmt.Errorf("replace project knowledge index: %w", err)
	}
	result := knowledgeScopedQuery(
		service.db.WithContext(ctx).Model(&models.KnowledgeIndexState{}),
		operation.Scope,
	).Where(
		"id = ? AND status = ?",
		state.ID,
		models.KnowledgeIndexBuilding,
	).UpdateColumns(map[string]any{
		"generation":     state.DesiredGeneration,
		"status":         models.KnowledgeIndexReady,
		"source_digest":  sourceDigest,
		"document_count": len(documents),
		"failure_detail": "",
		"completed_at":   now,
		"updated_at":     now,
	})
	if result.Error != nil {
		return nil, fmt.Errorf("complete knowledge index rebuild: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrKnowledgeIngestionState
	}
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where("id = ?", state.ID).First(&state).Error; err != nil {
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

func (service *KnowledgeService) loadKnowledgeIndexDocuments(
	ctx context.Context,
	scope models.ProjectScope,
) ([]HybridIndexDocument, string, error) {
	type indexedChunk struct {
		models.KnowledgeChunk
		DocumentVersion uint64 `gorm:"column:document_version"`
	}
	var rows []indexedChunk
	err := service.db.WithContext(ctx).
		Table("knowledge_chunks AS chunks").
		Select("chunks.*, versions.version AS document_version").
		Joins(
			"JOIN knowledge_article_versions AS versions "+
				"ON versions.id = chunks.version_id "+
				"AND versions.organization_id = chunks.organization_id "+
				"AND versions.project_id = chunks.project_id",
		).
		Where(
			"chunks.organization_id = ? AND chunks.project_id = ? "+
				"AND versions.status = ? AND versions.virus_scan = ?",
			scope.OrganizationID,
			scope.ProjectID,
			models.KnowledgeVersionPublished,
			models.VirusScanClean,
		).
		Order("chunks.article_id ASC, versions.version ASC, chunks.ordinal ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, "", fmt.Errorf("load knowledge index chunks: %w", err)
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
			return nil, "", fmt.Errorf("load knowledge index ACL: %w", err)
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
	digestInput := make([]string, 0, len(rows))
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
		digestInput = append(
			digestInput,
			row.ID+":"+row.ContentHash+":"+knowledgeSubjectsDigest(subjects),
		)
	}
	digest := sha256.Sum256([]byte(strings.Join(digestInput, "\n")))
	return documents, hex.EncodeToString(digest[:]), nil
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
			return policy, nil, ErrKnowledgeModelPolicyDenied
		}
		return policy, nil, fmt.Errorf("load knowledge model policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return policy, nil, fmt.Errorf("%w: %v", ErrKnowledgeModelPolicyDenied, err)
	}
	provider, exists := service.modelProviders[policy.ProviderKey]
	if !exists || provider == nil {
		return policy, nil, ErrKnowledgeModelPolicyDenied
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
		return ErrKnowledgeModelResponseInvalid
	}
	if _, err := hex.DecodeString(hit.ContentHash); err != nil {
		return ErrKnowledgeModelResponseInvalid
	}
	if hit.PageNumber != nil && *hit.PageNumber <= 0 {
		return ErrKnowledgeModelResponseInvalid
	}
	return nil
}

func requestKnowledgeIndexRebuildTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	indexName string,
) error {
	var state models.KnowledgeIndexState
	err := knowledgeScopedQuery(tx, scope).
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
			return fmt.Errorf("create knowledge index rebuild request: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("load knowledge index rebuild state: %w", err)
	}
	desired := state.DesiredGeneration
	if desired <= state.Generation {
		desired = state.Generation + 1
	}
	if err := knowledgeScopedQuery(
		tx.Model(&models.KnowledgeIndexState{}),
		scope,
	).Where("id = ?", state.ID).UpdateColumns(map[string]any{
		"desired_generation": desired,
		"status":             models.KnowledgeIndexRebuildRequested,
		"failure_detail":     "",
		"updated_at":         time.Now().UTC(),
	}).Error; err != nil {
		return fmt.Errorf("request knowledge index rebuild: %w", err)
	}
	return nil
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
