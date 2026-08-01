package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxAuthoredMarkdownBytes = 128 << 10
	MaxAuthoredSourceLinks   = 20
	MaxAuthoredSections      = 100
	MaxAuthoredChunks        = 100

	knowledgeAuthoredChunkBytes = 4096
	knowledgeReadScope          = "knowledge:read"
	knowledgeWriteScope         = "knowledge:write"
	knowledgeAuthoredParser     = "markdown-authored-v1"
)

// KnowledgeIdempotencyCompleter is deliberately narrower than
// AgentNativeService. Machine adapters reserve an idempotency record before
// crossing object storage; the knowledge transaction completes that exact
// record atomically with the article, version and DomainEvent.
type KnowledgeIdempotencyCompleter interface {
	CompleteIdempotencyTxWithTTL(
		ctx context.Context,
		tx *gorm.DB,
		recordID string,
		responseCode int,
		response any,
		resourceID string,
		eventID string,
		completionTTL time.Duration,
	) error
}

type CreateAuthoredArticleInput struct {
	Key                              string        `json:"key"`
	Title                            string        `json:"title"`
	Summary                          string        `json:"summary,omitempty"`
	Markdown                         string        `json:"markdown"`
	GrantProjectAccess               bool          `json:"grant_project_access"`
	SourceTicketID                   uint          `json:"source_ticket_id,omitempty"`
	SourceAttachmentIDs              []uint        `json:"source_attachment_ids,omitempty"`
	PolicyDecisionID                 string        `json:"-"`
	SourceTicketPolicyDecisionID     string        `json:"-"`
	SourceAttachmentPolicyDecisionID string        `json:"-"`
	IdempotencyRecordID              string        `json:"-"`
	IdempotencyCompletionTTL         time.Duration `json:"-"`
}

type CreateAuthoredVersionInput struct {
	Title                            string        `json:"title"`
	Markdown                         string        `json:"markdown"`
	SourceTicketID                   uint          `json:"source_ticket_id,omitempty"`
	SourceAttachmentIDs              []uint        `json:"source_attachment_ids,omitempty"`
	PolicyDecisionID                 string        `json:"-"`
	SourceTicketPolicyDecisionID     string        `json:"-"`
	SourceAttachmentPolicyDecisionID string        `json:"-"`
	IdempotencyRecordID              string        `json:"-"`
	IdempotencyCompletionTTL         time.Duration `json:"-"`
}

type AuthoredKnowledgeResult struct {
	Article  models.KnowledgeArticle        `json:"article"`
	Version  models.KnowledgeArticleVersion `json:"version"`
	Sources  []KnowledgeSourceView          `json:"sources"`
	Document *KnowledgeArticleDocument      `json:"document"`
	Event    *models.DomainEvent            `json:"event,omitempty"`
	Receipt  OperationReceipt               `json:"receipt"`
}

// AuthoredKnowledgeIdempotencyReceipt keeps the public operation receipt
// shape compatible while binding a completed write to one immutable document
// version. Replays must use all three reference fields together and verify the
// content hash after the normal live authorization and object-integrity checks.
type AuthoredKnowledgeIdempotencyReceipt struct {
	OperationReceipt
	ArticleID   string `json:"article_id,omitempty"`
	VersionID   string `json:"version_id,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type GetArticleDocumentInput struct {
	VersionID            string                             `json:"version_id,omitempty"`
	PreferLatestDraft    bool                               `json:"-"`
	PolicyDecisionID     string                             `json:"-"`
	Authorization        KnowledgeDocumentAuthorizationMode `json:"-"`
	SourceAuthorizations []KnowledgeSourceAuthorization     `json:"-"`
}

type KnowledgeDocumentAuthorizationMode string

const (
	KnowledgeDocumentRead                  KnowledgeDocumentAuthorizationMode = ""
	KnowledgeDocumentAuthoredArticleReplay KnowledgeDocumentAuthorizationMode = "authored_article_replay"
	KnowledgeDocumentAuthoredVersionReplay KnowledgeDocumentAuthorizationMode = "authored_version_replay"
)

type KnowledgeDocumentSection struct {
	Ordinal      uint   `json:"ordinal"`
	Heading      string `json:"heading,omitempty"`
	HeadingLevel int    `json:"heading_level"`
	SectionPath  string `json:"section_path,omitempty"`
	Markdown     string `json:"markdown"`
	ContentHash  string `json:"content_hash"`
}

type KnowledgeSourceVisibility string

const (
	KnowledgeSourceFull        KnowledgeSourceVisibility = "full"
	KnowledgeSourceRestricted  KnowledgeSourceVisibility = "restricted"
	KnowledgeSourceUnavailable KnowledgeSourceVisibility = "unavailable"
)

type KnowledgeSourceKind string

const (
	KnowledgeSourceTicket     KnowledgeSourceKind = "ticket"
	KnowledgeSourceAttachment KnowledgeSourceKind = "attachment"
)

// KnowledgeSourceView is the bounded public projection of an immutable
// KnowledgeSourceLink. Snapshot identifiers and untrusted display values are
// populated only after the current caller is authorized against the live
// Ticket and, when present, the live Attachment.
type KnowledgeSourceView struct {
	Ordinal            uint                      `json:"ordinal"`
	Kind               KnowledgeSourceKind       `json:"kind"`
	Visibility         KnowledgeSourceVisibility `json:"visibility"`
	ReferenceLabel     string                    `json:"reference_label"`
	SourceTicketID     *uint                     `json:"source_ticket_id,omitempty"`
	TicketNumber       string                    `json:"ticket_number,omitempty"`
	TicketTitle        string                    `json:"ticket_title,omitempty"`
	SourceAttachmentID *uint                     `json:"source_attachment_id,omitempty"`
	AttachmentName     string                    `json:"attachment_name,omitempty"`
	AttachmentHash     string                    `json:"attachment_hash,omitempty"`
}

// KnowledgeSourceAuthorization binds a Service Principal's freshly persisted
// PolicyDecisions to one live source Ticket. Protocol adapters may provide at
// most one entry per Ticket after the knowledge document itself is authorized.
type KnowledgeSourceAuthorization struct {
	SourceTicketID             uint   `json:"-"`
	TicketPolicyDecisionID     string `json:"-"`
	AttachmentPolicyDecisionID string `json:"-"`
}

type KnowledgeSourceAuthorizationTarget struct {
	SourceTicketID uint `json:"-"`
	HasAttachment  bool `json:"-"`
}

type KnowledgeArticleDocument struct {
	Article  models.KnowledgeArticle        `json:"article"`
	Version  models.KnowledgeArticleVersion `json:"version"`
	Markdown string                         `json:"markdown"`
	Sections []KnowledgeDocumentSection     `json:"sections"`
	Sources  []KnowledgeSourceView          `json:"sources"`

	sourceLinks []models.KnowledgeSourceLink
}

// SourceAuthorizationTargets returns only the bounded, current document's
// internal source identifiers so a machine Adapter can prepare per-Ticket
// PolicyDecisions. These identifiers are never part of a response DTO.
func (document *KnowledgeArticleDocument) SourceAuthorizationTargets() []KnowledgeSourceAuthorizationTarget {
	if document == nil || len(document.sourceLinks) == 0 {
		return []KnowledgeSourceAuthorizationTarget{}
	}
	byTicket := make(map[uint]bool, len(document.sourceLinks))
	for _, source := range document.sourceLinks {
		byTicket[source.SourceTicketID] =
			byTicket[source.SourceTicketID] ||
				source.SourceAttachmentID != nil
	}
	targets := make([]KnowledgeSourceAuthorizationTarget, 0, len(byTicket))
	for ticketID, hasAttachment := range byTicket {
		targets = append(targets, KnowledgeSourceAuthorizationTarget{
			SourceTicketID: ticketID,
			HasAttachment:  hasAttachment,
		})
	}
	sort.Slice(targets, func(left, right int) bool {
		return targets[left].SourceTicketID <
			targets[right].SourceTicketID
	})
	return targets
}

type authoredCreateOptions struct {
	createdArticle                   bool
	grantProjectAccess               bool
	articleID                        string
	versionID                        string
	articleKey                       string
	title                            string
	markdown                         string
	sourceTicketID                   uint
	sourceAttachmentIDs              []uint
	policyDecisionID                 string
	sourceTicketPolicyDecisionID     string
	sourceAttachmentPolicyDecisionID string
	idempotencyRecordID              string
	idempotencyOperation             string
	idempotencyCompletionTTL         time.Duration
}

type authoredDocumentSnapshot struct {
	Article models.KnowledgeArticle
	Version models.KnowledgeArticleVersion
	Sources []models.KnowledgeSourceLink
	Views   []KnowledgeSourceView
}

func (service *KnowledgeService) CreateAuthoredArticle(
	ctx context.Context,
	input CreateAuthoredArticleInput,
) (*AuthoredKnowledgeResult, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.validateAuthoredDependencies(
		input.IdempotencyRecordID,
	); err != nil {
		return nil, err
	}
	articleID, err := newAuthoredKnowledgeID()
	if err != nil {
		return nil, err
	}
	versionID, err := newAuthoredKnowledgeID()
	if err != nil {
		return nil, err
	}
	article := models.KnowledgeArticle{
		ID:             articleID,
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		Key:            strings.TrimSpace(input.Key),
		Title:          strings.TrimSpace(input.Title),
		Summary:        strings.TrimSpace(input.Summary),
		Status:         models.KnowledgeArticleActive,
		Revision:       1,
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
		UpdatedByType:  operation.Actor.Type,
		UpdatedByID:    operation.Actor.ID,
	}
	if err := article.Validate(); err != nil {
		return nil, err
	}
	options := authoredCreateOptions{
		createdArticle:                   true,
		grantProjectAccess:               input.GrantProjectAccess,
		articleID:                        articleID,
		versionID:                        versionID,
		articleKey:                       article.Key,
		title:                            article.Title,
		markdown:                         input.Markdown,
		sourceTicketID:                   input.SourceTicketID,
		sourceAttachmentIDs:              input.SourceAttachmentIDs,
		policyDecisionID:                 input.PolicyDecisionID,
		sourceTicketPolicyDecisionID:     input.SourceTicketPolicyDecisionID,
		sourceAttachmentPolicyDecisionID: input.SourceAttachmentPolicyDecisionID,
		idempotencyRecordID:              input.IdempotencyRecordID,
		idempotencyOperation:             "knowledge.article.create",
		idempotencyCompletionTTL:         input.IdempotencyCompletionTTL,
	}
	if err := validateAuthoredCreateOptions(options); err != nil {
		return nil, err
	}
	if err := service.preflightAuthoredWrite(ctx, operation, options); err != nil {
		return nil, err
	}

	intent, err := service.registerAuthoredObjectWriteIntent(
		ctx,
		operation,
		articleID,
		versionID,
		input.Markdown,
	)
	if err != nil {
		return nil, err
	}
	stored, objectKey, err := service.putAuthoredMarkdown(
		ctx,
		*intent,
		input.Markdown,
	)
	if err != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			err,
		)
	}
	if err := service.recordAuthoredObjectWriteReceipt(
		ctx,
		operation.Scope,
		*intent,
		stored,
	); err != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			err,
		)
	}
	now := service.now().UTC()
	version, task, chunks, err := service.prepareAuthoredVersionRecords(
		operation,
		options,
		1,
		stored,
		objectKey,
		now,
	)
	if err != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			err,
		)
	}

	result := &AuthoredKnowledgeResult{}
	transactionErr := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			tx := service.db.WithContext(scopedContext)
			access, err := service.revalidateAuthoredWrite(
				scopedContext,
				operation,
				options,
			)
			if err != nil {
				return err
			}
			if err := tx.Create(&article).Error; err != nil {
				return fmt.Errorf("create authored knowledge article: %w", err)
			}
			if err := tx.Create(&version).Error; err != nil {
				return fmt.Errorf("create authored knowledge version: %w", err)
			}
			sources, err := service.createAuthoredSourcesTx(
				scopedContext,
				tx,
				operation,
				options,
				access,
			)
			if err != nil {
				return err
			}
			if err := tx.Create(&task).Error; err != nil {
				return fmt.Errorf("create authored knowledge ingestion: %w", err)
			}
			if err := tx.Create(&chunks).Error; err != nil {
				return fmt.Errorf("create authored knowledge chunks: %w", err)
			}
			if err := service.grantAuthoredArticleAccessTx(
				tx,
				operation,
				article.ID,
				input.GrantProjectAccess,
			); err != nil {
				return err
			}
			event, receipt, err := service.appendAuthoredDraftEventTx(
				scopedContext,
				tx,
				operation,
				article,
				version,
				len(sources),
				options,
				true,
			)
			if err != nil {
				return err
			}
			if err := takeAuthoredObjectWriteIntentTx(
				tx,
				operation,
				intent.ID,
				stored,
				now,
			); err != nil {
				return err
			}
			sourceViews := fullKnowledgeSourceViews(sources)
			result = &AuthoredKnowledgeResult{
				Article: article,
				Version: version,
				Sources: sourceViews,
				Document: &KnowledgeArticleDocument{
					Article:  article,
					Version:  version,
					Markdown: options.markdown,
					Sections: parseAuthoredMarkdownSections(options.markdown),
					Sources:  sourceViews,
					sourceLinks: append(
						[]models.KnowledgeSourceLink(nil),
						sources...,
					),
				},
				Event:   event,
				Receipt: receipt,
			}
			return nil
		},
	)
	if transactionErr != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			transactionErr,
		)
	}
	return result, nil
}

func (service *KnowledgeService) CreateAuthoredVersion(
	ctx context.Context,
	articleID string,
	input CreateAuthoredVersionInput,
) (*AuthoredKnowledgeResult, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.validateAuthoredDependencies(
		input.IdempotencyRecordID,
	); err != nil {
		return nil, err
	}
	articleID = strings.TrimSpace(articleID)
	if !isCanonicalAuthoredKnowledgeID(articleID) {
		return nil, errors.New("knowledge article id must be a canonical UUID")
	}
	versionID, err := newAuthoredKnowledgeID()
	if err != nil {
		return nil, err
	}
	options := authoredCreateOptions{
		createdArticle:                   false,
		articleID:                        articleID,
		versionID:                        versionID,
		title:                            strings.TrimSpace(input.Title),
		markdown:                         input.Markdown,
		sourceTicketID:                   input.SourceTicketID,
		sourceAttachmentIDs:              input.SourceAttachmentIDs,
		policyDecisionID:                 input.PolicyDecisionID,
		sourceTicketPolicyDecisionID:     input.SourceTicketPolicyDecisionID,
		sourceAttachmentPolicyDecisionID: input.SourceAttachmentPolicyDecisionID,
		idempotencyRecordID:              input.IdempotencyRecordID,
		idempotencyOperation:             "knowledge.article.draft.create",
		idempotencyCompletionTTL:         input.IdempotencyCompletionTTL,
	}
	if err := validateAuthoredCreateOptions(options); err != nil {
		return nil, err
	}
	var preflightArticle models.KnowledgeArticle
	if err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			if _, err := service.revalidateAuthoredWrite(
				scopedContext,
				operation,
				options,
			); err != nil {
				return err
			}
			err := knowledgeScopedQuery(
				service.db.WithContext(scopedContext),
				operation.Scope,
			).Where(
				"id = ? AND status = ?",
				articleID,
				models.KnowledgeArticleActive,
			).First(&preflightArticle).Error
			return knowledgeLookupError(err)
		},
	); err != nil {
		return nil, err
	}
	options.articleKey = preflightArticle.Key

	intent, err := service.registerAuthoredObjectWriteIntent(
		ctx,
		operation,
		articleID,
		versionID,
		input.Markdown,
	)
	if err != nil {
		return nil, err
	}
	stored, objectKey, err := service.putAuthoredMarkdown(
		ctx,
		*intent,
		input.Markdown,
	)
	if err != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			err,
		)
	}
	if err := service.recordAuthoredObjectWriteReceipt(
		ctx,
		operation.Scope,
		*intent,
		stored,
	); err != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			err,
		)
	}
	now := service.now().UTC()
	result := &AuthoredKnowledgeResult{}
	transactionErr := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			tx := service.db.WithContext(scopedContext)
			access, err := service.revalidateAuthoredWrite(
				scopedContext,
				operation,
				options,
			)
			if err != nil {
				return err
			}
			var article models.KnowledgeArticle
			if err := knowledgeScopedQuery(tx, operation.Scope).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND status = ?",
					articleID,
					models.KnowledgeArticleActive,
				).
				First(&article).Error; err != nil {
				return knowledgeLookupError(err)
			}
			var maximum struct{ Version uint64 }
			if err := knowledgeScopedQuery(
				tx.Model(&models.KnowledgeArticleVersion{}),
				operation.Scope,
			).Select("COALESCE(MAX(version), 0) AS version").
				Where("article_id = ?", article.ID).
				Scan(&maximum).Error; err != nil {
				return fmt.Errorf("select authored knowledge version: %w", err)
			}
			version, task, chunks, err :=
				service.prepareAuthoredVersionRecords(
					operation,
					options,
					maximum.Version+1,
					stored,
					objectKey,
					now,
				)
			if err != nil {
				return err
			}
			if err := tx.Create(&version).Error; err != nil {
				return fmt.Errorf("create authored knowledge version: %w", err)
			}
			sources, err := service.createAuthoredSourcesTx(
				scopedContext,
				tx,
				operation,
				options,
				access,
			)
			if err != nil {
				return err
			}
			if err := tx.Create(&task).Error; err != nil {
				return fmt.Errorf("create authored knowledge ingestion: %w", err)
			}
			if err := tx.Create(&chunks).Error; err != nil {
				return fmt.Errorf("create authored knowledge chunks: %w", err)
			}
			event, receipt, err := service.appendAuthoredDraftEventTx(
				scopedContext,
				tx,
				operation,
				article,
				version,
				len(sources),
				options,
				false,
			)
			if err != nil {
				return err
			}
			if err := takeAuthoredObjectWriteIntentTx(
				tx,
				operation,
				intent.ID,
				stored,
				now,
			); err != nil {
				return err
			}
			sourceViews := fullKnowledgeSourceViews(sources)
			result = &AuthoredKnowledgeResult{
				Article: article,
				Version: version,
				Sources: sourceViews,
				Document: &KnowledgeArticleDocument{
					Article:  article,
					Version:  version,
					Markdown: options.markdown,
					Sections: parseAuthoredMarkdownSections(options.markdown),
					Sources:  sourceViews,
					sourceLinks: append(
						[]models.KnowledgeSourceLink(nil),
						sources...,
					),
				},
				Event:   event,
				Receipt: receipt,
			}
			return nil
		},
	)
	if transactionErr != nil {
		return nil, service.deferAndCleanupAuthoredObject(
			ctx,
			operation.Scope,
			*intent,
			stored,
			transactionErr,
		)
	}
	return result, nil
}

func (service *KnowledgeService) GetArticleDocument(
	ctx context.Context,
	articleID string,
	input GetArticleDocumentInput,
) (*KnowledgeArticleDocument, error) {
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.validateAuthoredDependencies(""); err != nil {
		return nil, err
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"authored knowledge document read",
	); err != nil {
		return nil, err
	}
	articleID = strings.TrimSpace(articleID)
	input.VersionID = strings.TrimSpace(input.VersionID)
	if !isCanonicalAuthoredKnowledgeID(articleID) ||
		(input.VersionID != "" &&
			!isCanonicalAuthoredKnowledgeID(input.VersionID)) ||
		(input.VersionID != "" && input.PreferLatestDraft) {
		return nil, errors.New("knowledge document id must be a canonical UUID")
	}
	if err := validateKnowledgeSourceAuthorizations(
		input.SourceAuthorizations,
	); err != nil {
		return nil, err
	}

	initial, err := service.captureAuthoredDocument(
		ctx,
		operation,
		articleID,
		input,
	)
	if err != nil {
		return nil, err
	}
	markdown, err := service.readAuthoredMarkdown(ctx, initial.Version)
	if err != nil {
		return nil, err
	}
	final, err := service.captureAuthoredDocument(
		ctx,
		operation,
		articleID,
		GetArticleDocumentInput{
			VersionID:        initial.Version.ID,
			PolicyDecisionID: input.PolicyDecisionID,
			Authorization:    input.Authorization,
			SourceAuthorizations: append(
				[]KnowledgeSourceAuthorization(nil),
				input.SourceAuthorizations...,
			),
		},
	)
	if err != nil {
		return nil, err
	}
	if !sameAuthoredDocumentSnapshot(initial, final) {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	return &KnowledgeArticleDocument{
		Article:  final.Article,
		Version:  final.Version,
		Markdown: markdown,
		Sections: parseAuthoredMarkdownSections(markdown),
		Sources:  final.Views,
		sourceLinks: append(
			[]models.KnowledgeSourceLink(nil),
			final.Sources...,
		),
	}, nil
}

func (service *KnowledgeService) captureAuthoredDocument(
	ctx context.Context,
	operation OperationContext,
	articleID string,
	input GetArticleDocumentInput,
) (authoredDocumentSnapshot, error) {
	var snapshot authoredDocumentSnapshot
	err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			tx := service.db.WithContext(scopedContext)
			if err := knowledgeScopedQuery(tx, operation.Scope).
				Where(
					"id = ? AND status = ?",
					articleID,
					models.KnowledgeArticleActive,
				).
				First(&snapshot.Article).Error; err != nil {
				return knowledgeLookupError(err)
			}
			query := knowledgeScopedQuery(
				tx,
				operation.Scope,
			).Where("article_id = ?", snapshot.Article.ID)
			if input.VersionID != "" {
				query = query.Where("id = ?", input.VersionID)
			} else if input.PreferLatestDraft {
				if snapshot.Article.CurrentVersion != nil {
					query = query.Where(
						"status = ? OR id = ?",
						models.KnowledgeVersionDraft,
						*snapshot.Article.CurrentVersion,
					)
				} else {
					query = query.Where(
						"status = ?",
						models.KnowledgeVersionDraft,
					)
				}
				query = query.
					Order(clause.Expr{
						SQL: "CASE WHEN status = ? THEN 0 ELSE 1 END",
						Vars: []any{
							models.KnowledgeVersionDraft,
						},
						WithoutParentheses: true,
					}).
					Order("version DESC, id DESC")
			} else if snapshot.Article.CurrentVersion != nil {
				query = query.Where("id = ?", *snapshot.Article.CurrentVersion)
			} else {
				query = query.
					Where("status = ?", models.KnowledgeVersionDraft).
					Order("version DESC, id DESC")
			}
			if err := query.First(&snapshot.Version).Error; err != nil {
				return knowledgeLookupError(err)
			}
			if snapshot.Version.VirusScan != models.VirusScanClean {
				return ErrProjectKnowledgeAccessDenied
			}
			if err := service.authorizeKnowledgeDocumentTx(
				scopedContext,
				operation,
				snapshot.Article.ID,
				snapshot.Version.Status,
				input,
			); err != nil {
				return err
			}
			if err := knowledgeScopedQuery(
				tx,
				operation.Scope,
			).Where(
				"article_id = ? AND version_id = ?",
				snapshot.Article.ID,
				snapshot.Version.ID,
			).Order("ordinal ASC, id ASC").
				Find(&snapshot.Sources).Error; err != nil {
				return fmt.Errorf("load authored knowledge sources: %w", err)
			}
			if snapshot.Sources == nil {
				snapshot.Sources = []models.KnowledgeSourceLink{}
			}
			views, err := service.projectKnowledgeSourceViewsTx(
				scopedContext,
				operation,
				snapshot.Sources,
				input.SourceAuthorizations,
			)
			if err != nil {
				return err
			}
			snapshot.Views = views
			return nil
		},
	)
	return snapshot, err
}

func (service *KnowledgeService) authorizeKnowledgeDocumentTx(
	ctx context.Context,
	operation OperationContext,
	articleID string,
	status models.KnowledgeVersionStatus,
	input GetArticleDocumentInput,
) error {
	manager := false
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := parseKnowledgeHumanID(operation.Actor.ID)
		if err != nil {
			return ErrProjectKnowledgeAccessDenied
		}
		access, err := service.projects.RevalidateHumanProjectAccess(
			ctx,
			operation.Scope,
			userID,
		)
		if err != nil {
			return err
		}
		manager = access.Role == models.ProjectRoleAdmin ||
			access.Role == models.ProjectRoleManager
	case models.ActorTypeServicePrincipal:
		access, err := service.projects.RevalidatePrincipalProjectAccess(
			ctx,
			operation.Scope,
			operation.Actor.ID,
		)
		if err != nil {
			return err
		}
		expectedScope := knowledgeReadScope
		expectedAction := "knowledge.article.read"
		expectedResourceID := articleID
		expectedWrite := false
		switch input.Authorization {
		case KnowledgeDocumentRead:
		case KnowledgeDocumentAuthoredArticleReplay:
			expectedScope = knowledgeWriteScope
			expectedAction = "knowledge.article.draft.create"
			expectedResourceID = "*"
			expectedWrite = true
		case KnowledgeDocumentAuthoredVersionReplay:
			expectedScope = knowledgeWriteScope
			expectedAction = "knowledge.article.draft.create"
			expectedWrite = true
		default:
			return ErrProjectKnowledgeAccessDenied
		}
		if !projectAccessHasScope(access, expectedScope) {
			return ErrProjectKnowledgeAccessDenied
		}
		if err := service.validateAuthoredPolicyDecisionTx(
			ctx,
			operation,
			input.PolicyDecisionID,
			expectedScope,
			expectedAction,
			"knowledge_article",
			expectedResourceID,
			expectedWrite,
		); err != nil {
			return err
		}
	default:
		return ErrProjectKnowledgeAccessDenied
	}
	if manager {
		return nil
	}
	subjects, err := service.resolveKnowledgeSubjects(ctx, operation)
	if err != nil {
		return err
	}
	permissions := []models.KnowledgeACLPermission{
		models.KnowledgeACLManage,
	}
	if status == models.KnowledgeVersionPublished ||
		status == models.KnowledgeVersionSuperseded {
		permissions = append(permissions, models.KnowledgeACLRead)
	}
	predicates := make([]string, 0, len(subjects))
	arguments := make([]any, 0, len(subjects)*2)
	for _, subject := range subjects {
		predicates = append(
			predicates,
			"(subject_type = ? AND subject_id = ?)",
		)
		arguments = append(arguments, subject.Type, subject.ID)
	}
	var count int64
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx).
			Model(&models.KnowledgeArticleACL{}),
		operation.Scope,
	).Where(
		"article_id = ? AND permission IN ?",
		articleID,
		permissions,
	).Where(
		"("+strings.Join(predicates, " OR ")+")",
		arguments...,
	).Count(&count).Error; err != nil {
		return fmt.Errorf("authorize knowledge document ACL: %w", err)
	}
	if count == 0 {
		return ErrProjectKnowledgeAccessDenied
	}
	return nil
}

func validateKnowledgeSourceAuthorizations(
	authorizations []KnowledgeSourceAuthorization,
) error {
	if len(authorizations) > MaxAuthoredSourceLinks {
		return fmt.Errorf(
			"knowledge source authorizations cannot exceed %d",
			MaxAuthoredSourceLinks,
		)
	}
	seen := make(map[uint]struct{}, len(authorizations))
	for _, authorization := range authorizations {
		if authorization.SourceTicketID == 0 {
			return errors.New(
				"knowledge source authorization ticket is invalid",
			)
		}
		if _, exists := seen[authorization.SourceTicketID]; exists {
			return errors.New(
				"knowledge source authorization tickets must be unique",
			)
		}
		seen[authorization.SourceTicketID] = struct{}{}
		for name, value := range map[string]string{
			"ticket":     authorization.TicketPolicyDecisionID,
			"attachment": authorization.AttachmentPolicyDecisionID,
		} {
			value = strings.TrimSpace(value)
			if name == "ticket" && value == "" {
				return errors.New(
					"knowledge source ticket authorization is required",
				)
			}
			if value == "" {
				continue
			}
			parsed, err := uuid.Parse(value)
			if err != nil || parsed.String() != value {
				return fmt.Errorf(
					"knowledge source %s authorization is invalid",
					name,
				)
			}
		}
	}
	return nil
}

func (service *KnowledgeService) projectKnowledgeSourceViewsTx(
	ctx context.Context,
	operation OperationContext,
	sources []models.KnowledgeSourceLink,
	authorizations []KnowledgeSourceAuthorization,
) ([]KnowledgeSourceView, error) {
	if len(sources) == 0 {
		return []KnowledgeSourceView{}, nil
	}
	if len(sources) > MaxAuthoredSourceLinks {
		return nil, errors.New("authored knowledge source set is invalid")
	}

	ticketIDs := make([]uint, 0, len(sources))
	attachmentIDs := make([]uint, 0, len(sources))
	seenTickets := make(map[uint]struct{}, len(sources))
	for _, source := range sources {
		if source.SourceTicketID == 0 {
			return nil, errors.New(
				"authored knowledge source ticket is invalid",
			)
		}
		if _, exists := seenTickets[source.SourceTicketID]; !exists {
			seenTickets[source.SourceTicketID] = struct{}{}
			ticketIDs = append(ticketIDs, source.SourceTicketID)
		}
		if source.SourceAttachmentID != nil {
			attachmentIDs = append(
				attachmentIDs,
				*source.SourceAttachmentID,
			)
		}
	}

	var tickets []models.Ticket
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx).Model(&models.Ticket{}),
		operation.Scope,
	).Select(
		"id",
		"organization_id",
		"project_id",
		"created_by_id",
		"assigned_to_id",
	).Where("id IN ?", ticketIDs).
		Find(&tickets).Error; err != nil {
		return nil, fmt.Errorf(
			"load live knowledge source tickets: %w",
			err,
		)
	}
	ticketsByID := make(map[uint]models.Ticket, len(tickets))
	for _, ticket := range tickets {
		ticketsByID[ticket.ID] = ticket
	}

	attachmentsByID := make(map[uint]models.TicketAttachment)
	if len(attachmentIDs) > 0 {
		var attachments []models.TicketAttachment
		if err := knowledgeScopedQuery(
			service.db.WithContext(ctx).
				Model(&models.TicketAttachment{}),
			operation.Scope,
		).Select(
			"id",
			"organization_id",
			"project_id",
			"ticket_id",
			"is_public",
			"virus_scan",
			"hash",
		).Where("id IN ?", attachmentIDs).
			Find(&attachments).Error; err != nil {
			return nil, fmt.Errorf(
				"load live knowledge source attachments: %w",
				err,
			)
		}
		attachmentsByID = make(
			map[uint]models.TicketAttachment,
			len(attachments),
		)
		for _, attachment := range attachments {
			attachmentsByID[attachment.ID] = attachment
		}
	}

	var access *ProjectAccess
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := parseKnowledgeHumanID(operation.Actor.ID)
		if err != nil {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		var accessErr error
		access, accessErr = service.projects.RevalidateHumanProjectAccess(
			ctx,
			operation.Scope,
			userID,
		)
		if accessErr != nil {
			return nil, accessErr
		}
	case models.ActorTypeServicePrincipal:
		var accessErr error
		access, accessErr =
			service.projects.RevalidatePrincipalProjectAccess(
				ctx,
				operation.Scope,
				operation.Actor.ID,
			)
		if accessErr != nil {
			return nil, accessErr
		}
	default:
		return nil, ErrProjectKnowledgeAccessDenied
	}

	authorizationByTicket := make(
		map[uint]KnowledgeSourceAuthorization,
		len(authorizations),
	)
	for _, authorization := range authorizations {
		authorizationByTicket[authorization.SourceTicketID] =
			authorization
	}
	ticketDecisionAllowed := make(map[uint]bool, len(seenTickets))
	attachmentDecisionAllowed := make(map[uint]bool, len(seenTickets))

	views := make([]KnowledgeSourceView, 0, len(sources))
	for _, source := range sources {
		ticket, ticketAvailable := ticketsByID[source.SourceTicketID]
		if !ticketAvailable {
			views = append(
				views,
				unavailableKnowledgeSourceView(source),
			)
			continue
		}

		var attachment models.TicketAttachment
		attachmentAvailable := true
		if source.SourceAttachmentID != nil {
			var exists bool
			attachment, exists =
				attachmentsByID[*source.SourceAttachmentID]
			attachmentAvailable = exists &&
				attachment.TicketID == ticket.ID &&
				attachment.VirusScan == models.VirusScanClean &&
				strings.EqualFold(
					strings.TrimSpace(attachment.Hash),
					source.AttachmentHash,
				)
		}
		if !attachmentAvailable {
			views = append(
				views,
				unavailableKnowledgeSourceView(source),
			)
			continue
		}

		ticketAllowed := false
		attachmentAllowed := source.SourceAttachmentID == nil
		switch operation.Actor.Type {
		case models.ActorTypeHuman:
			ticketAllowed =
				authorizeHumanAttachmentTicket(
					access,
					operation,
					ticket,
					false,
					true,
				) == nil
			if source.SourceAttachmentID != nil && ticketAllowed {
				attachmentAllowed =
					authorizeHumanAttachmentTicket(
						access,
						operation,
						ticket,
						false,
						attachment.IsPublic,
					) == nil &&
						(access.Role != models.ProjectRoleRequester ||
							attachment.IsPublic)
			}
		case models.ActorTypeServicePrincipal:
			authorization, exists :=
				authorizationByTicket[source.SourceTicketID]
			if !exists ||
				!projectAccessHasScope(
					access,
					models.ScopeTicketsRead,
				) {
				break
			}
			ticketAllowed, exists =
				ticketDecisionAllowed[source.SourceTicketID]
			if !exists {
				ticketAllowed =
					service.validateAuthoredPolicyDecisionTx(
						ctx,
						operation,
						authorization.TicketPolicyDecisionID,
						models.ScopeTicketsRead,
						"ticket.read",
						"ticket",
						strconv.FormatUint(
							uint64(source.SourceTicketID),
							10,
						),
						false,
					) == nil
				ticketDecisionAllowed[source.SourceTicketID] =
					ticketAllowed
			}
			if source.SourceAttachmentID != nil &&
				ticketAllowed &&
				projectAccessHasScope(
					access,
					models.ScopeAttachmentsRead,
				) {
				attachmentAllowed, exists =
					attachmentDecisionAllowed[source.SourceTicketID]
				if !exists {
					attachmentAllowed =
						service.validateAuthoredPolicyDecisionTx(
							ctx,
							operation,
							authorization.
								AttachmentPolicyDecisionID,
							models.ScopeAttachmentsRead,
							"ticket.attachment.read",
							"ticket",
							strconv.FormatUint(
								uint64(source.SourceTicketID),
								10,
							),
							false,
						) == nil
					attachmentDecisionAllowed[source.SourceTicketID] =
						attachmentAllowed
				}
			}
		}

		if !ticketAllowed || !attachmentAllowed {
			views = append(
				views,
				restrictedKnowledgeSourceView(source),
			)
			continue
		}
		views = append(views, fullKnowledgeSourceView(source))
	}
	return views, nil
}

func fullKnowledgeSourceViews(
	sources []models.KnowledgeSourceLink,
) []KnowledgeSourceView {
	views := make([]KnowledgeSourceView, 0, len(sources))
	for _, source := range sources {
		views = append(views, fullKnowledgeSourceView(source))
	}
	return views
}

func fullKnowledgeSourceView(
	source models.KnowledgeSourceLink,
) KnowledgeSourceView {
	ticketID := source.SourceTicketID
	view := KnowledgeSourceView{
		Ordinal:        source.Ordinal,
		Kind:           KnowledgeSourceTicket,
		Visibility:     KnowledgeSourceFull,
		ReferenceLabel: "工单 " + source.TicketNumber,
		SourceTicketID: &ticketID,
		TicketNumber:   source.TicketNumber,
		TicketTitle:    source.TicketTitle,
	}
	if source.SourceAttachmentID != nil {
		attachmentID := *source.SourceAttachmentID
		view.Kind = KnowledgeSourceAttachment
		view.ReferenceLabel = "附件 " + source.AttachmentName
		view.SourceAttachmentID = &attachmentID
		view.AttachmentName = source.AttachmentName
		view.AttachmentHash = source.AttachmentHash
	}
	return view
}

func restrictedKnowledgeSourceView(
	source models.KnowledgeSourceLink,
) KnowledgeSourceView {
	view := KnowledgeSourceView{
		Ordinal:        source.Ordinal,
		Kind:           KnowledgeSourceTicket,
		Visibility:     KnowledgeSourceRestricted,
		ReferenceLabel: "受限工单来源",
	}
	if source.SourceAttachmentID != nil {
		view.Kind = KnowledgeSourceAttachment
		view.ReferenceLabel = "受限附件来源"
	}
	return view
}

func unavailableKnowledgeSourceView(
	source models.KnowledgeSourceLink,
) KnowledgeSourceView {
	view := KnowledgeSourceView{
		Ordinal:        source.Ordinal,
		Kind:           KnowledgeSourceTicket,
		Visibility:     KnowledgeSourceUnavailable,
		ReferenceLabel: "工单来源已不可用",
	}
	if source.SourceAttachmentID != nil {
		view.Kind = KnowledgeSourceAttachment
		view.ReferenceLabel = "附件来源已不可用"
	}
	return view
}

func (service *KnowledgeService) readAuthoredMarkdown(
	ctx context.Context,
	version models.KnowledgeArticleVersion,
) (string, error) {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"authored knowledge object read",
	); err != nil {
		return "", err
	}
	if version.SizeBytes < 1 ||
		version.SizeBytes > MaxAuthoredMarkdownBytes {
		return "", errors.New("authored knowledge object size is invalid")
	}
	mediaType, _, err := mime.ParseMediaType(version.MimeType)
	if err != nil || !strings.EqualFold(mediaType, "text/plain") {
		return "", errors.New("authored knowledge object MIME type is invalid")
	}
	expectedKey := fmt.Sprintf(
		"knowledge/%d/%s/%s.md",
		version.ProjectID,
		version.ArticleID,
		version.ID,
	)
	if version.ObjectKey != expectedKey {
		return "", errors.New("authored knowledge object key is invalid")
	}
	var reader io.ReadCloser
	err = nil
	reference := AttachmentStoredReference{
		StorageType: version.ObjectProvider,
		StoreID:     version.ObjectStoreID,
		Key:         version.ObjectKey,
		VersionID:   version.ObjectVersionID,
	}
	if routed, ok := service.storage.(ReferencedAttachmentStorage); ok {
		reader, err = routed.OpenStoredObject(
			ctx,
			reference,
		)
	} else if version.ObjectStoreID != "" {
		reader, err = openAttachmentStoredObject(
			ctx,
			service.storage,
			reference,
		)
	} else if typed, ok := service.storage.(TypedAttachmentStorage); ok {
		reader, err = typed.OpenStored(
			ctx,
			version.ObjectProvider,
			version.ObjectKey,
		)
	} else {
		currentProvider := attachmentStorageType(service.storage)
		if version.ObjectProvider != currentProvider {
			return "", fmt.Errorf(
				"authored knowledge object provider %q is unavailable",
				version.ObjectProvider,
			)
		}
		reader, err = service.storage.Open(ctx, version.ObjectKey)
	}
	if err != nil {
		return "", fmt.Errorf(
			"open authored knowledge object provider: %w",
			err,
		)
	}
	payload, readErr := io.ReadAll(io.LimitReader(
		reader,
		int64(MaxAuthoredMarkdownBytes)+1,
	))
	closeErr := reader.Close()
	if readErr != nil {
		return "", fmt.Errorf("read authored knowledge object: %w", readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close authored knowledge object: %w", closeErr)
	}
	if len(payload) > MaxAuthoredMarkdownBytes ||
		int64(len(payload)) != version.SizeBytes ||
		!utf8.Valid(payload) {
		return "", errors.New("authored knowledge object integrity is invalid")
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != version.ContentHash {
		return "", errors.New("authored knowledge object hash is invalid")
	}
	return string(payload), nil
}

func sameAuthoredDocumentSnapshot(
	left authoredDocumentSnapshot,
	right authoredDocumentSnapshot,
) bool {
	if left.Article.ID != right.Article.ID ||
		left.Article.Revision != right.Article.Revision ||
		left.Article.Status != right.Article.Status ||
		!sameOptionalString(
			left.Article.CurrentVersion,
			right.Article.CurrentVersion,
		) ||
		left.Version.ID != right.Version.ID ||
		left.Version.Status != right.Version.Status ||
		left.Version.Version != right.Version.Version ||
		left.Version.Title != right.Version.Title ||
		left.Version.VirusScan != right.Version.VirusScan ||
		left.Version.ObjectProvider != right.Version.ObjectProvider ||
		left.Version.ObjectBucket != right.Version.ObjectBucket ||
		left.Version.ObjectKey != right.Version.ObjectKey ||
		left.Version.ObjectStoreID != right.Version.ObjectStoreID ||
		left.Version.ObjectVersionID != right.Version.ObjectVersionID ||
		left.Version.MimeType != right.Version.MimeType ||
		left.Version.SizeBytes != right.Version.SizeBytes ||
		left.Version.ContentHash != right.Version.ContentHash ||
		len(left.Sources) != len(right.Sources) {
		return false
	}
	for index := range left.Sources {
		a := left.Sources[index]
		b := right.Sources[index]
		if a.ID != b.ID ||
			a.VersionID != b.VersionID ||
			a.Ordinal != b.Ordinal ||
			a.SourceTicketID != b.SourceTicketID ||
			!sameOptionalUint(
				a.SourceAttachmentID,
				b.SourceAttachmentID,
			) ||
			a.TicketNumber != b.TicketNumber ||
			a.TicketTitle != b.TicketTitle ||
			a.AttachmentName != b.AttachmentName ||
			a.AttachmentHash != b.AttachmentHash ||
			a.CreatedByType != b.CreatedByType ||
			a.CreatedByID != b.CreatedByID {
			return false
		}
	}
	return true
}

func sameOptionalString(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalUint(left *uint, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (service *KnowledgeService) revalidateKnowledgeListAccess(
	ctx context.Context,
	operation OperationContext,
	manageAll bool,
	managedByActor bool,
	policyDecisionID string,
) ([]models.KnowledgeACLSubject, error) {
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := parseKnowledgeHumanID(operation.Actor.ID)
		if err != nil {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		access, err := service.projects.RevalidateHumanProjectAccess(
			ctx,
			operation.Scope,
			userID,
		)
		if err != nil {
			return nil, err
		}
		if manageAll &&
			access.Role != models.ProjectRoleAdmin &&
			access.Role != models.ProjectRoleManager {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		if managedByActor && !access.CanCreateKnowledgeDrafts {
			return nil, ErrProjectKnowledgeAccessDenied
		}
	case models.ActorTypeServicePrincipal:
		if manageAll || managedByActor {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		if _, err := service.projects.RevalidatePrincipalProjectAccess(
			ctx,
			operation.Scope,
			operation.Actor.ID,
			knowledgeReadScope,
		); err != nil {
			return nil, err
		}
		if err := service.validateAuthoredPolicyDecisionTx(
			ctx,
			operation,
			policyDecisionID,
			knowledgeReadScope,
			"knowledge.article.list",
			"knowledge_article",
			"*",
			false,
		); err != nil {
			return nil, err
		}
	default:
		return nil, ErrProjectKnowledgeAccessDenied
	}
	if manageAll || managedByActor {
		return nil, nil
	}
	return service.resolveKnowledgeSubjects(ctx, operation)
}

func applyKnowledgeArticleHumanManageVisibility(
	query *gorm.DB,
	scope models.ProjectScope,
	humanID string,
) *gorm.DB {
	return query.Where(`
		EXISTS (
			SELECT 1
			FROM knowledge_article_acl AS creator_acl
			WHERE creator_acl.article_id = knowledge_articles.id
			  AND creator_acl.organization_id = ?
			  AND creator_acl.project_id = ?
			  AND creator_acl.subject_type = ?
			  AND creator_acl.subject_id = ?
			  AND creator_acl.permission = ?
		)
	`,
		scope.OrganizationID,
		scope.ProjectID,
		models.KnowledgeACLHuman,
		humanID,
		models.KnowledgeACLManage,
	)
}

func applyKnowledgeArticleReadVisibility(
	query *gorm.DB,
	scope models.ProjectScope,
	subjects []models.KnowledgeACLSubject,
) *gorm.DB {
	predicates := make([]string, 0, len(subjects))
	arguments := make([]any, 0, 10+len(subjects)*2)
	arguments = append(
		arguments,
		scope.OrganizationID,
		scope.ProjectID,
		models.KnowledgeVersionPublished,
		models.VirusScanClean,
		scope.OrganizationID,
		scope.ProjectID,
		models.KnowledgeACLRead,
		models.KnowledgeACLManage,
	)
	for _, subject := range subjects {
		predicates = append(
			predicates,
			"(acl.subject_type = ? AND acl.subject_id = ?)",
		)
		arguments = append(arguments, subject.Type, subject.ID)
	}
	return query.Where(`
		EXISTS (
			SELECT 1
			FROM knowledge_article_versions AS readable_version
			WHERE readable_version.id = knowledge_articles.current_version_id
			  AND readable_version.article_id = knowledge_articles.id
			  AND readable_version.organization_id = ?
			  AND readable_version.project_id = ?
			  AND readable_version.status = ?
			  AND readable_version.virus_scan = ?
		)
		AND EXISTS (
			SELECT 1
			FROM knowledge_article_acl AS acl
			WHERE acl.article_id = knowledge_articles.id
			  AND acl.organization_id = ?
			  AND acl.project_id = ?
			  AND acl.permission IN (?, ?)
			  AND (`+strings.Join(predicates, " OR ")+`)
		)
	`, arguments...)
}

func (service *KnowledgeService) validateAuthoredDependencies(
	idempotencyRecordID string,
) error {
	if service == nil || service.db == nil || service.projects == nil ||
		service.events == nil || service.storage == nil ||
		strings.TrimSpace(service.storageBucket) == "" {
		return errors.New("authored knowledge storage pipeline is unavailable")
	}
	if len(service.storageBucket) > 255 ||
		strings.ContainsRune(service.storageBucket, '\x00') {
		return errors.New("authored knowledge storage descriptor is invalid")
	}
	if strings.TrimSpace(idempotencyRecordID) != "" &&
		service.idempotency == nil {
		return errors.New(
			"authored knowledge idempotency completion is unavailable",
		)
	}
	return nil
}

func validateAuthoredCreateOptions(options authoredCreateOptions) error {
	if !isCanonicalAuthoredKnowledgeID(options.articleID) ||
		!isCanonicalAuthoredKnowledgeID(options.versionID) {
		return errors.New("authored knowledge ids must be canonical UUIDs")
	}
	if strings.TrimSpace(options.title) == "" ||
		len([]rune(options.title)) > 240 {
		return errors.New("authored knowledge title is invalid")
	}
	if !utf8.ValidString(options.markdown) ||
		len(options.markdown) < 1 ||
		len(options.markdown) > MaxAuthoredMarkdownBytes ||
		strings.TrimSpace(options.markdown) == "" {
		return fmt.Errorf(
			"authored knowledge Markdown must be UTF-8 text between 1 and %d bytes",
			MaxAuthoredMarkdownBytes,
		)
	}
	if options.sourceTicketID == 0 &&
		len(options.sourceAttachmentIDs) > 0 {
		return errors.New(
			"authored knowledge attachments require a source ticket",
		)
	}
	if len(options.sourceAttachmentIDs) > MaxAuthoredSourceLinks {
		return fmt.Errorf(
			"authored knowledge sources cannot exceed %d",
			MaxAuthoredSourceLinks,
		)
	}
	seen := make(map[uint]struct{}, len(options.sourceAttachmentIDs))
	for _, attachmentID := range options.sourceAttachmentIDs {
		if attachmentID == 0 {
			return errors.New("authored knowledge attachment id is invalid")
		}
		if _, exists := seen[attachmentID]; exists {
			return errors.New(
				"authored knowledge attachment ids contain duplicates",
			)
		}
		seen[attachmentID] = struct{}{}
	}
	sections := parseAuthoredMarkdownSections(options.markdown)
	if len(sections) == 0 || len(sections) > MaxAuthoredSections {
		return fmt.Errorf(
			"authored knowledge Markdown must produce between 1 and %d sections",
			MaxAuthoredSections,
		)
	}
	chunkCount := 0
	for _, section := range sections {
		chunkCount += len(splitAuthoredMarkdown(
			section.Markdown,
			knowledgeAuthoredChunkBytes,
		))
	}
	if chunkCount < 1 || chunkCount > MaxAuthoredChunks {
		return fmt.Errorf(
			"authored knowledge Markdown must produce between 1 and %d chunks",
			MaxAuthoredChunks,
		)
	}
	return nil
}

func newAuthoredKnowledgeID() (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate authored knowledge UUIDv7: %w", err)
	}
	return value.String(), nil
}

func isCanonicalAuthoredKnowledgeID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func (service *KnowledgeService) preflightAuthoredWrite(
	ctx context.Context,
	operation OperationContext,
	options authoredCreateOptions,
) error {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"authored knowledge object upload",
	); err != nil {
		return err
	}
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			access, err := service.revalidateAuthoredWrite(
				scopedContext,
				operation,
				options,
			)
			if err != nil {
				return err
			}
			_, err = service.loadAuthorizedAuthoredSourcesTx(
				scopedContext,
				service.db.WithContext(scopedContext),
				operation,
				options,
				access,
			)
			return err
		},
	)
}

func (service *KnowledgeService) revalidateAuthoredWrite(
	ctx context.Context,
	operation OperationContext,
	options authoredCreateOptions,
) (*ProjectAccess, error) {
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		userID, err := parseKnowledgeHumanID(operation.Actor.ID)
		if err != nil {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		access, err := service.projects.RevalidateHumanProjectAccess(
			ctx,
			operation.Scope,
			userID,
		)
		if err != nil {
			return nil, err
		}
		if access.Role != models.ProjectRoleAdmin &&
			access.Role != models.ProjectRoleManager {
			if !access.CanCreateKnowledgeDrafts ||
				options.grantProjectAccess {
				return nil, ErrProjectKnowledgeAccessDenied
			}
			if !options.createdArticle {
				if err := service.requireAuthoredArticleManageACLTx(
					ctx,
					operation,
					options.articleID,
				); err != nil {
					return nil, err
				}
			}
		}
		return access, nil
	case models.ActorTypeServicePrincipal:
		if options.grantProjectAccess {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		required := []string{knowledgeWriteScope}
		if options.sourceTicketID != 0 {
			required = append(required, models.ScopeTicketsRead)
		}
		if len(options.sourceAttachmentIDs) != 0 {
			required = append(required, models.ScopeAttachmentsRead)
		}
		access, err := service.projects.RevalidatePrincipalProjectAccess(
			ctx,
			operation.Scope,
			operation.Actor.ID,
			required...,
		)
		if err != nil {
			return nil, err
		}
		resourceID := options.articleID
		if options.createdArticle {
			resourceID = "*"
		}
		if err := service.validateAuthoredPolicyDecisionTx(
			ctx,
			operation,
			options.policyDecisionID,
			knowledgeWriteScope,
			"knowledge.article.draft.create",
			"knowledge_article",
			resourceID,
			true,
		); err != nil {
			return nil, err
		}
		if !options.createdArticle {
			if err := service.requireAuthoredArticleManageACLTx(
				ctx,
				operation,
				options.articleID,
			); err != nil {
				return nil, err
			}
		}
		if options.sourceTicketID != 0 {
			if err := service.validateAuthoredPolicyDecisionTx(
				ctx,
				operation,
				options.sourceTicketPolicyDecisionID,
				models.ScopeTicketsRead,
				"ticket.read",
				"ticket",
				strconv.FormatUint(uint64(options.sourceTicketID), 10),
				false,
			); err != nil {
				return nil, err
			}
		}
		if len(options.sourceAttachmentIDs) != 0 {
			if err := service.validateAuthoredPolicyDecisionTx(
				ctx,
				operation,
				options.sourceAttachmentPolicyDecisionID,
				models.ScopeAttachmentsRead,
				"ticket.attachment.read",
				"ticket",
				strconv.FormatUint(uint64(options.sourceTicketID), 10),
				false,
			); err != nil {
				return nil, err
			}
		}
		return access, nil
	default:
		return nil, ErrProjectKnowledgeAccessDenied
	}
}

func (service *KnowledgeService) requireAuthoredArticleManageACLTx(
	ctx context.Context,
	operation OperationContext,
	articleID string,
) error {
	subjects, err := service.resolveKnowledgeSubjects(ctx, operation)
	if err != nil {
		return err
	}
	predicates := make([]string, 0, len(subjects))
	arguments := make([]any, 0, len(subjects)*2)
	for _, subject := range subjects {
		predicates = append(
			predicates,
			"(subject_type = ? AND subject_id = ?)",
		)
		arguments = append(arguments, subject.Type, subject.ID)
	}
	var count int64
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx).
			Model(&models.KnowledgeArticleACL{}),
		operation.Scope,
	).Where(
		"article_id = ? AND permission = ?",
		articleID,
		models.KnowledgeACLManage,
	).Where(
		"("+strings.Join(predicates, " OR ")+")",
		arguments...,
	).Count(&count).Error; err != nil {
		return fmt.Errorf("authorize authored knowledge article manage ACL: %w", err)
	}
	if count == 0 {
		return ErrProjectKnowledgeAccessDenied
	}
	return nil
}

func (service *KnowledgeService) validateAuthoredPolicyDecisionTx(
	ctx context.Context,
	operation OperationContext,
	decisionID string,
	scope string,
	action string,
	resourceType string,
	resourceID string,
	expectedWrite bool,
) error {
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" {
		return fmt.Errorf(
			"%w: authored source requires a prepared policy decision",
			ErrPolicyDenied,
		)
	}
	var decision models.PolicyDecision
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"id = ? AND actor_type = ? AND actor_id = ? AND credential_id = ?",
		decisionID,
		operation.Actor.Type,
		operation.Actor.ID,
		operation.CredentialID,
	).First(&decision).Error; err != nil {
		return fmt.Errorf("%w: authored source decision is unavailable", ErrPolicyDenied)
	}
	if !decision.Allowed ||
		decision.Scope != scope ||
		decision.Action != action ||
		decision.ResourceType != resourceType ||
		decision.ResourceID != resourceID ||
		decision.IsWrite != expectedWrite {
		return fmt.Errorf(
			"%w: authored source policy decision does not match",
			ErrPolicyDenied,
		)
	}
	var principal models.ServicePrincipal
	if err := service.db.WithContext(ctx).
		Select("id", "policy_epoch").
		Where("id = ?", operation.Actor.ID).
		First(&principal).Error; err != nil {
		return fmt.Errorf("%w: authored source principal is unavailable", ErrPolicyDenied)
	}
	if decision.PolicyEpoch == 0 ||
		decision.PolicyEpoch != principal.PolicyEpoch {
		return fmt.Errorf(
			"%w: authored source policy epoch changed",
			ErrPolicyDenied,
		)
	}
	return nil
}

func (service *KnowledgeService) putAuthoredMarkdown(
	ctx context.Context,
	intent models.KnowledgeObjectWriteIntent,
	markdown string,
) (*StoredAttachmentObject, string, error) {
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"authored knowledge object upload",
	); err != nil {
		return nil, "", err
	}
	objectKey := intent.ObjectKey
	targetStoreID := intent.ObjectStoreID
	targetStorageType := intent.ObjectProvider
	stored, err := putAttachmentInStore(
		ctx,
		service.storage,
		targetStoreID,
		objectKey,
		bytes.NewReader([]byte(markdown)),
		MaxAuthoredMarkdownBytes,
	)
	if err != nil {
		return nil, objectKey,
			fmt.Errorf("store authored knowledge Markdown: %w", err)
	}
	if stored == nil ||
		stored.Key != objectKey ||
		stored.Size != int64(len(markdown)) ||
		!strings.EqualFold(
			stored.SHA256,
			authoredMarkdownHash(markdown),
		) {
		return stored, objectKey,
			errors.New("authored knowledge storage result is invalid")
	}
	if stored.StoreID != targetStoreID ||
		stored.StorageType != targetStorageType {
		return stored, objectKey,
			errors.New("authored knowledge storage identity is invalid")
	}
	mediaType, _, err := mime.ParseMediaType(stored.DetectedContentType)
	if err != nil || !strings.EqualFold(mediaType, "text/plain") {
		return stored, objectKey,
			errors.New(
				"authored knowledge storage did not detect text/plain",
			)
	}
	return stored, objectKey, nil
}

func authoredMarkdownHash(markdown string) string {
	digest := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(digest[:])
}

func (service *KnowledgeService) prepareAuthoredVersionRecords(
	operation OperationContext,
	options authoredCreateOptions,
	documentVersion uint64,
	stored *StoredAttachmentObject,
	objectKey string,
	now time.Time,
) (
	models.KnowledgeArticleVersion,
	models.KnowledgeIngestionTask,
	[]models.KnowledgeChunk,
	error,
) {
	if stored == nil || documentVersion == 0 {
		return models.KnowledgeArticleVersion{},
			models.KnowledgeIngestionTask{},
			nil,
			errors.New("authored knowledge storage result is required")
	}
	version := models.KnowledgeArticleVersion{
		ID:               options.versionID,
		OrganizationID:   operation.Scope.OrganizationID,
		ProjectID:        operation.Scope.ProjectID,
		ArticleID:        options.articleID,
		Version:          documentVersion,
		Status:           models.KnowledgeVersionDraft,
		Title:            strings.TrimSpace(options.title),
		ObjectProvider:   stored.StorageType,
		ObjectBucket:     service.storageBucket,
		ObjectKey:        objectKey,
		ObjectStoreID:    stored.StoreID,
		ObjectVersionID:  stored.VersionID,
		OriginalFileName: options.versionID + ".md",
		MimeType:         "text/plain; charset=utf-8",
		SizeBytes:        stored.Size,
		ContentHash:      strings.ToLower(stored.SHA256),
		VirusScan:        models.VirusScanClean,
		ScanDetail:       "authored UTF-8 plaintext",
		ScannedAt:        &now,
		PageCount:        1,
		CreatedByType:    operation.Actor.Type,
		CreatedByID:      operation.Actor.ID,
	}
	if err := version.Validate(); err != nil {
		return version, models.KnowledgeIngestionTask{}, nil, err
	}
	taskID, err := newAuthoredKnowledgeID()
	if err != nil {
		return version, models.KnowledgeIngestionTask{}, nil, err
	}
	task := models.KnowledgeIngestionTask{
		ID:             taskID,
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		ArticleID:      options.articleID,
		VersionID:      options.versionID,
		Attempt:        1,
		Status:         models.KnowledgeIngestionCompleted,
		ParserKey:      knowledgeAuthoredParser,
		StartedAt:      &now,
		CompletedAt:    &now,
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
	}
	if err := task.Validate(); err != nil {
		return version, task, nil, err
	}
	chunks, err := authoredKnowledgeChunks(
		operation,
		options.articleID,
		options.versionID,
		task.ID,
		options.markdown,
	)
	if err != nil {
		return version, task, nil, err
	}
	return version, task, chunks, nil
}

func authoredKnowledgeChunks(
	operation OperationContext,
	articleID string,
	versionID string,
	taskID string,
	markdown string,
) ([]models.KnowledgeChunk, error) {
	sections := parseAuthoredMarkdownSections(markdown)
	chunks := make([]models.KnowledgeChunk, 0, len(sections))
	for _, section := range sections {
		for _, part := range splitAuthoredMarkdown(
			section.Markdown,
			knowledgeAuthoredChunkBytes,
		) {
			chunkID, err := newAuthoredKnowledgeID()
			if err != nil {
				return nil, err
			}
			content := strings.TrimSpace(part)
			if content == "" {
				continue
			}
			chunk := models.KnowledgeChunk{
				ID:              chunkID,
				OrganizationID:  operation.Scope.OrganizationID,
				ProjectID:       operation.Scope.ProjectID,
				ArticleID:       articleID,
				VersionID:       versionID,
				IngestionTaskID: taskID,
				Ordinal:         uint(len(chunks)),
				SectionPath:     section.SectionPath,
				Content:         content,
				Snippet:         truncateAuthoredUTF8(content, 1000),
				TokenCount:      (utf8.RuneCountInString(content) + 3) / 4,
			}
			if err := chunk.BeforeCreate(nil); err != nil {
				return nil, err
			}
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) == 0 || len(chunks) > MaxAuthoredChunks {
		return nil, errors.New(
			"authored knowledge Markdown produced an invalid chunk count",
		)
	}
	return chunks, nil
}

func parseAuthoredMarkdownSections(
	markdown string,
) []KnowledgeDocumentSection {
	type sectionBuilder struct {
		heading string
		level   int
		path    string
		body    strings.Builder
	}
	builders := make([]*sectionBuilder, 0, 8)
	var current *sectionBuilder
	headings := make([]string, 6)
	lines := strings.SplitAfter(markdown, "\n")
	var fenceMarker byte
	fenceLength := 0
	for _, line := range lines {
		if fenceMarker != 0 {
			if current == nil {
				current = &sectionBuilder{}
				builders = append(builders, current)
			}
			current.body.WriteString(line)
			if authoredMarkdownFenceClose(
				line,
				fenceMarker,
				fenceLength,
			) {
				fenceMarker = 0
				fenceLength = 0
			}
			continue
		}
		if marker, length, ok := authoredMarkdownFenceOpen(line); ok {
			if current == nil {
				current = &sectionBuilder{}
				builders = append(builders, current)
			}
			current.body.WriteString(line)
			fenceMarker = marker
			fenceLength = length
			continue
		}
		heading, level, ok := authoredMarkdownHeading(line)
		if ok {
			for index := level - 1; index < len(headings); index++ {
				headings[index] = ""
			}
			headings[level-1] = heading
			pathParts := make([]string, 0, level)
			for index := 0; index < level; index++ {
				if headings[index] != "" {
					pathParts = append(pathParts, headings[index])
				}
			}
			current = &sectionBuilder{
				heading: heading,
				level:   level,
				path:    strings.Join(pathParts, " / "),
			}
			builders = append(builders, current)
		}
		if current == nil {
			current = &sectionBuilder{}
			builders = append(builders, current)
		}
		current.body.WriteString(line)
	}
	sections := make([]KnowledgeDocumentSection, 0, len(builders))
	for _, builder := range builders {
		content := builder.body.String()
		if strings.TrimSpace(content) == "" {
			continue
		}
		sections = append(sections, KnowledgeDocumentSection{
			Ordinal:      uint(len(sections)),
			Heading:      builder.heading,
			HeadingLevel: builder.level,
			SectionPath:  builder.path,
			Markdown:     content,
			ContentHash:  authoredMarkdownHash(content),
		})
	}
	return sections
}

func authoredMarkdownFenceOpen(line string) (byte, int, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	leading := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if leading > 3 {
		return 0, 0, false
	}
	candidate := trimmed[leading:]
	if len(candidate) < 3 ||
		(candidate[0] != '`' && candidate[0] != '~') {
		return 0, 0, false
	}
	marker := candidate[0]
	length := 0
	for length < len(candidate) && candidate[length] == marker {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	return marker, length, true
}

func authoredMarkdownFenceClose(
	line string,
	marker byte,
	minimumLength int,
) bool {
	trimmed := strings.TrimRight(line, "\r\n")
	leading := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if leading > 3 {
		return false
	}
	candidate := trimmed[leading:]
	length := 0
	for length < len(candidate) && candidate[length] == marker {
		length++
	}
	return length >= minimumLength &&
		strings.TrimSpace(candidate[length:]) == ""
}

func authoredMarkdownHeading(line string) (string, int, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	leading := len(trimmed) - len(strings.TrimLeft(trimmed, " "))
	if leading > 3 {
		return "", 0, false
	}
	candidate := trimmed[leading:]
	level := 0
	for level < len(candidate) && level < 6 && candidate[level] == '#' {
		level++
	}
	if level == 0 ||
		(level < len(candidate) &&
			candidate[level] != ' ' &&
			candidate[level] != '\t') {
		return "", 0, false
	}
	heading := strings.TrimSpace(candidate[level:])
	heading = strings.TrimSpace(strings.TrimRight(heading, "#"))
	return heading, level, true
}

func splitAuthoredMarkdown(value string, maximum int) []string {
	if len(value) <= maximum {
		return []string{value}
	}
	parts := make([]string, 0, len(value)/maximum+1)
	for len(value) > maximum {
		cut := maximum
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		if newline := strings.LastIndexByte(value[:cut], '\n'); newline >= maximum/2 {
			cut = newline + 1
		}
		parts = append(parts, value[:cut])
		value = value[cut:]
	}
	if value != "" {
		parts = append(parts, value)
	}
	return parts
}

func truncateAuthoredUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	cut := maximum
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

type authoredSourceSnapshot struct {
	ticket      models.Ticket
	attachments []models.TicketAttachment
}

func (service *KnowledgeService) loadAuthorizedAuthoredSourcesTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	options authoredCreateOptions,
	access *ProjectAccess,
) (authoredSourceSnapshot, error) {
	if options.sourceTicketID == 0 {
		return authoredSourceSnapshot{
			attachments: []models.TicketAttachment{},
		}, nil
	}
	var ticket models.Ticket
	if err := knowledgeScopedQuery(tx, operation.Scope).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("id = ?", options.sourceTicketID).
		First(&ticket).Error; err != nil {
		return authoredSourceSnapshot{}, fmt.Errorf(
			"load authored knowledge source ticket: %w",
			knowledgeLookupError(err),
		)
	}
	if operation.Actor.Type == models.ActorTypeHuman {
		if err := authorizeHumanAttachmentTicket(
			access,
			operation,
			ticket,
			false,
			true,
		); err != nil {
			return authoredSourceSnapshot{},
				ErrProjectKnowledgeAccessDenied
		}
	}
	if len(options.sourceAttachmentIDs) == 0 {
		return authoredSourceSnapshot{
			ticket:      ticket,
			attachments: []models.TicketAttachment{},
		}, nil
	}

	var attachments []models.TicketAttachment
	if err := knowledgeScopedQuery(
		tx.Model(&models.TicketAttachment{}),
		operation.Scope,
	).Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id IN ? AND ticket_id = ? AND virus_scan = ? AND deleted_at IS NULL",
			options.sourceAttachmentIDs,
			ticket.ID,
			models.VirusScanClean,
		).
		Find(&attachments).Error; err != nil {
		return authoredSourceSnapshot{}, fmt.Errorf(
			"load authored knowledge source attachments: %w",
			err,
		)
	}
	if len(attachments) != len(options.sourceAttachmentIDs) {
		return authoredSourceSnapshot{}, errors.New(
			"authored knowledge source attachment is unavailable or not clean",
		)
	}
	byID := make(map[uint]models.TicketAttachment, len(attachments))
	for _, attachment := range attachments {
		if operation.Actor.Type == models.ActorTypeHuman &&
			access != nil &&
			access.Role == models.ProjectRoleRequester &&
			!attachment.IsPublic {
			return authoredSourceSnapshot{},
				ErrProjectKnowledgeAccessDenied
		}
		if !isAuthoredSHA256(attachment.Hash) {
			return authoredSourceSnapshot{}, errors.New(
				"authored knowledge source attachment lacks a verified hash",
			)
		}
		byID[attachment.ID] = attachment
	}
	ordered := make(
		[]models.TicketAttachment,
		0,
		len(options.sourceAttachmentIDs),
	)
	for _, attachmentID := range options.sourceAttachmentIDs {
		attachment, exists := byID[attachmentID]
		if !exists {
			return authoredSourceSnapshot{}, errors.New(
				"authored knowledge source attachment changed during validation",
			)
		}
		ordered = append(ordered, attachment)
	}
	return authoredSourceSnapshot{
		ticket:      ticket,
		attachments: ordered,
	}, nil
}

func (service *KnowledgeService) createAuthoredSourcesTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	options authoredCreateOptions,
	access *ProjectAccess,
) ([]models.KnowledgeSourceLink, error) {
	snapshot, err := service.loadAuthorizedAuthoredSourcesTx(
		ctx,
		tx,
		operation,
		options,
		access,
	)
	if err != nil {
		return nil, err
	}
	if options.sourceTicketID == 0 {
		return []models.KnowledgeSourceLink{}, nil
	}
	if len(snapshot.attachments) == 0 {
		link, err := newAuthoredSourceLink(
			operation,
			options,
			snapshot.ticket,
			nil,
			0,
		)
		if err != nil {
			return nil, err
		}
		if err := tx.Create(&link).Error; err != nil {
			return nil, fmt.Errorf("create authored knowledge source: %w", err)
		}
		return []models.KnowledgeSourceLink{link}, nil
	}
	links := make(
		[]models.KnowledgeSourceLink,
		0,
		len(snapshot.attachments),
	)
	for ordinal := range snapshot.attachments {
		link, err := newAuthoredSourceLink(
			operation,
			options,
			snapshot.ticket,
			&snapshot.attachments[ordinal],
			uint(ordinal),
		)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := tx.Create(&links).Error; err != nil {
		return nil, fmt.Errorf("create authored knowledge sources: %w", err)
	}
	return links, nil
}

func newAuthoredSourceLink(
	operation OperationContext,
	options authoredCreateOptions,
	ticket models.Ticket,
	attachment *models.TicketAttachment,
	ordinal uint,
) (models.KnowledgeSourceLink, error) {
	linkID, err := newAuthoredKnowledgeID()
	if err != nil {
		return models.KnowledgeSourceLink{}, err
	}
	link := models.KnowledgeSourceLink{
		ID:             linkID,
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		ArticleID:      options.articleID,
		VersionID:      options.versionID,
		Ordinal:        ordinal,
		SourceTicketID: ticket.ID,
		TicketNumber:   ticket.TicketNumber,
		TicketTitle:    ticket.Title,
		CreatedByType:  operation.Actor.Type,
		CreatedByID:    operation.Actor.ID,
	}
	if attachment != nil {
		attachmentID := attachment.ID
		link.SourceAttachmentID = &attachmentID
		link.AttachmentName = strings.TrimSpace(attachment.OriginalName)
		if link.AttachmentName == "" {
			link.AttachmentName = strings.TrimSpace(attachment.FileName)
		}
		link.AttachmentHash = strings.ToLower(strings.TrimSpace(attachment.Hash))
	}
	if err := link.Validate(); err != nil {
		return models.KnowledgeSourceLink{}, err
	}
	return link, nil
}

func isAuthoredSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (service *KnowledgeService) grantAuthoredArticleAccessTx(
	tx *gorm.DB,
	operation OperationContext,
	articleID string,
	grantProjectAccess bool,
) error {
	subjectType := models.KnowledgeACLHuman
	if operation.Actor.Type == models.ActorTypeServicePrincipal {
		subjectType = models.KnowledgeACLServicePrincipal
	}
	grants := []models.KnowledgeArticleACL{{
		OrganizationID: operation.Scope.OrganizationID,
		ProjectID:      operation.Scope.ProjectID,
		ArticleID:      articleID,
		SubjectType:    subjectType,
		SubjectID:      operation.Actor.ID,
		Permission:     models.KnowledgeACLManage,
		GrantedByType:  operation.Actor.Type,
		GrantedByID:    operation.Actor.ID,
	}}
	if grantProjectAccess {
		grants = append(grants, models.KnowledgeArticleACL{
			OrganizationID: operation.Scope.OrganizationID,
			ProjectID:      operation.Scope.ProjectID,
			ArticleID:      articleID,
			SubjectType:    models.KnowledgeACLAllProject,
			SubjectID:      "*",
			Permission:     models.KnowledgeACLRead,
			GrantedByType:  operation.Actor.Type,
			GrantedByID:    operation.Actor.ID,
		})
	}
	if err := tx.Create(&grants).Error; err != nil {
		return fmt.Errorf("create authored knowledge ACL: %w", err)
	}
	return nil
}

func (service *KnowledgeService) appendAuthoredDraftEventTx(
	ctx context.Context,
	tx *gorm.DB,
	operation OperationContext,
	article models.KnowledgeArticle,
	version models.KnowledgeArticleVersion,
	sourceCount int,
	options authoredCreateOptions,
	createdArticle bool,
) (*models.DomainEvent, OperationReceipt, error) {
	event, err := service.events.AppendDomainEventTx(
		ctx,
		tx,
		DomainEventInput{
			Type: eventcontract.KnowledgeDraftCreatedEventType,
			Subject: fmt.Sprintf(
				"knowledge/articles/%s/versions/%s",
				article.ID,
				version.ID,
			),
			Data: map[string]any{
				"article_id":       article.ID,
				"version_id":       version.ID,
				"document_version": version.Version,
				"content_hash":     version.ContentHash,
				"source_count":     sourceCount,
				"created_article":  createdArticle,
			},
			Scope:            operation.Scope,
			TraceID:          operation.TraceID,
			CorrelationID:    operation.CorrelationID,
			Actor:            operation.Actor,
			ResourceVersion:  version.Version,
			PolicyDecisionID: strings.TrimSpace(options.policyDecisionID),
		},
		nil,
	)
	if err != nil {
		return nil, OperationReceipt{}, fmt.Errorf(
			"append authored knowledge draft event: %w",
			err,
		)
	}
	resourceID := version.ID
	changedFields := []string{
		"version",
		"sources",
		"ingestion",
		"chunks",
	}
	if createdArticle {
		resourceID = article.ID
		changedFields = append([]string{"article"}, changedFields...)
	}
	receipt := OperationReceipt{
		OperationID:      newNativeID(),
		ResourceID:       resourceID,
		ResourceVersion:  version.Version,
		EventID:          event.ID,
		ChangedFields:    changedFields,
		PolicyDecisionID: strings.TrimSpace(options.policyDecisionID),
	}
	if strings.TrimSpace(options.idempotencyRecordID) != "" {
		if err := service.validateAuthoredIdempotencyRecordTx(
			tx,
			operation,
			options.idempotencyRecordID,
			options.idempotencyOperation,
		); err != nil {
			return nil, OperationReceipt{}, err
		}
		idempotencyReceipt := AuthoredKnowledgeIdempotencyReceipt{
			OperationReceipt: receipt,
			ArticleID:        article.ID,
			VersionID:        version.ID,
			ContentHash:      version.ContentHash,
		}
		if err := service.idempotency.CompleteIdempotencyTxWithTTL(
			ctx,
			tx,
			strings.TrimSpace(options.idempotencyRecordID),
			http.StatusCreated,
			idempotencyReceipt,
			resourceID,
			event.ID,
			options.idempotencyCompletionTTL,
		); err != nil {
			return nil, OperationReceipt{}, fmt.Errorf(
				"complete authored knowledge idempotency: %w",
				err,
			)
		}
	}
	return event, receipt, nil
}

func (service *KnowledgeService) validateAuthoredIdempotencyRecordTx(
	tx *gorm.DB,
	operation OperationContext,
	recordID string,
	expectedOperation string,
) error {
	var count int64
	if err := knowledgeScopedQuery(
		tx.Model(&models.IdempotencyRecord{}),
		operation.Scope,
	).Where(
		"id = ? AND actor_type = ? AND actor_id = ? AND operation = ? AND state = ?",
		strings.TrimSpace(recordID),
		operation.Actor.Type,
		operation.Actor.ID,
		expectedOperation,
		models.IdempotencyStateProcessing,
	).Count(&count).Error; err != nil {
		return fmt.Errorf("validate authored knowledge idempotency: %w", err)
	}
	if count != 1 {
		return ErrIdempotencyConflict
	}
	return nil
}
