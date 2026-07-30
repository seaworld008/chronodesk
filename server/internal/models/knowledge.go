package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	knowledgeKeyPattern  = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	knowledgeHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var ErrPublishedKnowledgeVersionImmutable = errors.New(
	"published knowledge version is immutable",
)

type KnowledgeArticleStatus string

const (
	KnowledgeArticleActive   KnowledgeArticleStatus = "active"
	KnowledgeArticleArchived KnowledgeArticleStatus = "archived"
)

func (status KnowledgeArticleStatus) IsValid() bool {
	return status == KnowledgeArticleActive ||
		status == KnowledgeArticleArchived
}

type KnowledgeArticle struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_article_project_key,priority:1"`
	ProjectID      uint                   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_article_project_key,priority:2"`
	Key            string                 `json:"key" gorm:"size:64;not null;uniqueIndex:idx_knowledge_article_project_key,priority:3"`
	Title          string                 `json:"title" gorm:"size:240;not null"`
	Summary        string                 `json:"summary" gorm:"size:1000"`
	Status         KnowledgeArticleStatus `json:"status" gorm:"size:20;not null;default:'active';index"`
	CurrentVersion *string                `json:"current_version_id,omitempty" gorm:"column:current_version_id;size:36;index"`
	Revision       uint64                 `json:"revision" gorm:"not null;default:1"`
	CreatedByType  ActorType              `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string                 `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	UpdatedByType  ActorType              `json:"updated_by_type" gorm:"size:32;not null"`
	UpdatedByID    string                 `json:"updated_by_id" gorm:"size:128;not null"`
}

func (KnowledgeArticle) TableName() string {
	return "knowledge_articles"
}

func (article *KnowledgeArticle) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&article.ID); err != nil {
		return err
	}
	if article.Status == "" {
		article.Status = KnowledgeArticleActive
	}
	if article.Revision == 0 {
		article.Revision = 1
	}
	return article.Validate()
}

func (article *KnowledgeArticle) BeforeUpdate(_ *gorm.DB) error {
	return article.Validate()
}

func (article KnowledgeArticle) Validate() error {
	if err := validateKnowledgeScope(
		article.OrganizationID,
		article.ProjectID,
	); err != nil {
		return err
	}
	if !knowledgeKeyPattern.MatchString(article.Key) {
		return fmt.Errorf("knowledge article key %q is invalid", article.Key)
	}
	if strings.TrimSpace(article.Title) == "" {
		return errors.New("knowledge article title is required")
	}
	if !article.Status.IsValid() {
		return fmt.Errorf("knowledge article status %q is invalid", article.Status)
	}
	if article.Revision == 0 {
		return errors.New("knowledge article revision must be positive")
	}
	if err := (ActorRef{
		Type: article.CreatedByType,
		ID:   article.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge article creator is invalid: %w", err)
	}
	if err := (ActorRef{
		Type: article.UpdatedByType,
		ID:   article.UpdatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge article updater is invalid: %w", err)
	}
	return nil
}

type KnowledgeVersionStatus string

const (
	KnowledgeVersionDraft       KnowledgeVersionStatus = "draft"
	KnowledgeVersionPublished   KnowledgeVersionStatus = "published"
	KnowledgeVersionSuperseded  KnowledgeVersionStatus = "superseded"
	KnowledgeVersionQuarantined KnowledgeVersionStatus = "quarantined"
)

func (status KnowledgeVersionStatus) IsValid() bool {
	switch status {
	case KnowledgeVersionDraft,
		KnowledgeVersionPublished,
		KnowledgeVersionSuperseded,
		KnowledgeVersionQuarantined:
		return true
	default:
		return false
	}
}

// KnowledgeObjectReference is control metadata for an object already stored in
// an object store. It deliberately has no URL or raw byte/content field.
type KnowledgeObjectReference struct {
	Provider    string `json:"provider"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	VersionID   string `json:"version_id,omitempty"`
	FileName    string `json:"file_name"`
	MimeType    string `json:"mime_type"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentHash string `json:"content_hash"`
}

func (reference KnowledgeObjectReference) Validate() error {
	if strings.TrimSpace(reference.Provider) == "" ||
		strings.TrimSpace(reference.Bucket) == "" ||
		strings.TrimSpace(reference.Key) == "" {
		return errors.New("knowledge source requires object provider, bucket and key")
	}
	if strings.Contains(reference.Key, "\x00") {
		return errors.New("knowledge object key is invalid")
	}
	if strings.TrimSpace(reference.FileName) == "" ||
		strings.TrimSpace(reference.MimeType) == "" {
		return errors.New("knowledge source requires file name and MIME type")
	}
	if reference.SizeBytes <= 0 {
		return errors.New("knowledge source size must be positive")
	}
	if !knowledgeHashPattern.MatchString(reference.ContentHash) {
		return errors.New("knowledge source content hash must be SHA-256")
	}
	return nil
}

type KnowledgeArticleVersion struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_version_article_number,priority:1"`
	ProjectID      uint                   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_version_article_number,priority:2"`
	ArticleID      string                 `json:"article_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_version_article_number,priority:3"`
	Version        uint64                 `json:"version" gorm:"not null;uniqueIndex:idx_knowledge_version_article_number,priority:4"`
	Status         KnowledgeVersionStatus `json:"status" gorm:"size:20;not null;default:'draft';index"`
	Title          string                 `json:"title" gorm:"size:240;not null"`

	ObjectProvider   string `json:"object_provider" gorm:"size:64;not null"`
	ObjectBucket     string `json:"object_bucket" gorm:"size:255;not null"`
	ObjectKey        string `json:"object_key" gorm:"size:1000;not null"`
	ObjectVersionID  string `json:"object_version_id,omitempty" gorm:"size:255"`
	OriginalFileName string `json:"original_file_name" gorm:"size:255;not null"`
	MimeType         string `json:"mime_type" gorm:"size:160;not null"`
	SizeBytes        int64  `json:"size_bytes" gorm:"not null"`
	ContentHash      string `json:"content_hash" gorm:"size:64;not null;index"`

	VirusScan  VirusScanStatus `json:"virus_scan" gorm:"size:20;not null;default:'pending';index"`
	ScanDetail string          `json:"scan_detail,omitempty" gorm:"size:1000"`
	ScannedAt  *time.Time      `json:"scanned_at,omitempty"`
	PageCount  int             `json:"page_count,omitempty" gorm:"not null;default:0"`

	CreatedByType ActorType  `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID   string     `json:"created_by_id" gorm:"size:128;not null;<-:create"`
	PublishedAt   *time.Time `json:"published_at,omitempty" gorm:"index"`
}

func (KnowledgeArticleVersion) TableName() string {
	return "knowledge_article_versions"
}

func (version *KnowledgeArticleVersion) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&version.ID); err != nil {
		return err
	}
	if version.Status == "" {
		version.Status = KnowledgeVersionDraft
	}
	if version.VirusScan == "" {
		version.VirusScan = VirusScanPending
	}
	return version.Validate()
}

func (version *KnowledgeArticleVersion) BeforeUpdate(_ *gorm.DB) error {
	if version.Status == KnowledgeVersionPublished ||
		version.Status == KnowledgeVersionSuperseded {
		return ErrPublishedKnowledgeVersionImmutable
	}
	return version.Validate()
}

func (version KnowledgeArticleVersion) ObjectReference() KnowledgeObjectReference {
	return KnowledgeObjectReference{
		Provider:    version.ObjectProvider,
		Bucket:      version.ObjectBucket,
		Key:         version.ObjectKey,
		VersionID:   version.ObjectVersionID,
		FileName:    version.OriginalFileName,
		MimeType:    version.MimeType,
		SizeBytes:   version.SizeBytes,
		ContentHash: version.ContentHash,
	}
}

func (version KnowledgeArticleVersion) CanParse() bool {
	return version.Status == KnowledgeVersionDraft &&
		version.VirusScan == VirusScanClean
}

func (version KnowledgeArticleVersion) Validate() error {
	if err := validateKnowledgeScope(
		version.OrganizationID,
		version.ProjectID,
	); err != nil {
		return err
	}
	if strings.TrimSpace(version.ArticleID) == "" || version.Version == 0 {
		return errors.New("knowledge version requires article and version number")
	}
	if !version.Status.IsValid() {
		return fmt.Errorf("knowledge version status %q is invalid", version.Status)
	}
	if strings.TrimSpace(version.Title) == "" {
		return errors.New("knowledge version title is required")
	}
	if err := version.ObjectReference().Validate(); err != nil {
		return err
	}
	if !isKnowledgeVirusScanStatus(version.VirusScan) {
		return fmt.Errorf("knowledge virus scan status %q is invalid", version.VirusScan)
	}
	if version.VirusScan != VirusScanPending && version.ScannedAt == nil {
		return errors.New("terminal knowledge virus scan requires scanned_at")
	}
	if version.PageCount < 0 {
		return errors.New("knowledge page count cannot be negative")
	}
	if err := (ActorRef{
		Type: version.CreatedByType,
		ID:   version.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge version creator is invalid: %w", err)
	}
	return nil
}

type KnowledgeACLSubjectType string

const (
	KnowledgeACLHuman            KnowledgeACLSubjectType = "human"
	KnowledgeACLServicePrincipal KnowledgeACLSubjectType = "service_principal"
	KnowledgeACLTeam             KnowledgeACLSubjectType = "team"
	KnowledgeACLProjectRole      KnowledgeACLSubjectType = "project_role"
	KnowledgeACLAllProject       KnowledgeACLSubjectType = "all_project"
)

func (subjectType KnowledgeACLSubjectType) IsValid() bool {
	switch subjectType {
	case KnowledgeACLHuman,
		KnowledgeACLServicePrincipal,
		KnowledgeACLTeam,
		KnowledgeACLProjectRole,
		KnowledgeACLAllProject:
		return true
	default:
		return false
	}
}

type KnowledgeACLPermission string

const (
	KnowledgeACLRead   KnowledgeACLPermission = "read"
	KnowledgeACLManage KnowledgeACLPermission = "manage"
)

func (permission KnowledgeACLPermission) IsValid() bool {
	return permission == KnowledgeACLRead ||
		permission == KnowledgeACLManage
}

type KnowledgeACLSubject struct {
	Type KnowledgeACLSubjectType `json:"type"`
	ID   string                  `json:"id"`
}

func (subject KnowledgeACLSubject) Validate() error {
	if !subject.Type.IsValid() {
		return fmt.Errorf("knowledge ACL subject type %q is invalid", subject.Type)
	}
	if subject.Type == KnowledgeACLAllProject {
		if subject.ID != "*" {
			return errors.New("all-project ACL subject id must be *")
		}
		return nil
	}
	if strings.TrimSpace(subject.ID) == "" {
		return errors.New("knowledge ACL subject id is required")
	}
	return nil
}

type KnowledgeArticleACL struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                    `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_acl_subject,priority:1"`
	ProjectID      uint                    `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_acl_subject,priority:2"`
	ArticleID      string                  `json:"article_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_acl_subject,priority:3"`
	SubjectType    KnowledgeACLSubjectType `json:"subject_type" gorm:"size:32;not null;index;uniqueIndex:idx_knowledge_acl_subject,priority:4"`
	SubjectID      string                  `json:"subject_id" gorm:"size:128;not null;index;uniqueIndex:idx_knowledge_acl_subject,priority:5"`
	Permission     KnowledgeACLPermission  `json:"permission" gorm:"size:20;not null;uniqueIndex:idx_knowledge_acl_subject,priority:6"`
	GrantedByType  ActorType               `json:"granted_by_type" gorm:"size:32;not null;<-:create"`
	GrantedByID    string                  `json:"granted_by_id" gorm:"size:128;not null;<-:create"`
}

func (KnowledgeArticleACL) TableName() string {
	return "knowledge_article_acl"
}

func (acl *KnowledgeArticleACL) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&acl.ID); err != nil {
		return err
	}
	return acl.Validate()
}

func (acl KnowledgeArticleACL) Subject() KnowledgeACLSubject {
	return KnowledgeACLSubject{Type: acl.SubjectType, ID: acl.SubjectID}
}

func (acl KnowledgeArticleACL) Validate() error {
	if err := validateKnowledgeScope(acl.OrganizationID, acl.ProjectID); err != nil {
		return err
	}
	if strings.TrimSpace(acl.ArticleID) == "" {
		return errors.New("knowledge ACL article id is required")
	}
	if err := acl.Subject().Validate(); err != nil {
		return err
	}
	if !acl.Permission.IsValid() {
		return fmt.Errorf("knowledge ACL permission %q is invalid", acl.Permission)
	}
	if err := (ActorRef{
		Type: acl.GrantedByType,
		ID:   acl.GrantedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge ACL grant actor is invalid: %w", err)
	}
	return nil
}

type KnowledgeIngestionStatus string

const (
	KnowledgeIngestionQueued      KnowledgeIngestionStatus = "queued"
	KnowledgeIngestionParsing     KnowledgeIngestionStatus = "parsing"
	KnowledgeIngestionIndexing    KnowledgeIngestionStatus = "indexing"
	KnowledgeIngestionCompleted   KnowledgeIngestionStatus = "completed"
	KnowledgeIngestionQuarantined KnowledgeIngestionStatus = "quarantined"
	KnowledgeIngestionFailed      KnowledgeIngestionStatus = "failed"
)

func (status KnowledgeIngestionStatus) IsValid() bool {
	switch status {
	case KnowledgeIngestionQueued,
		KnowledgeIngestionParsing,
		KnowledgeIngestionIndexing,
		KnowledgeIngestionCompleted,
		KnowledgeIngestionQuarantined,
		KnowledgeIngestionFailed:
		return true
	default:
		return false
	}
}

type KnowledgeIngestionTask struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                     `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_ingestion_attempt,priority:1"`
	ProjectID      uint                     `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_ingestion_attempt,priority:2"`
	ArticleID      string                   `json:"article_id" gorm:"size:36;not null;index"`
	VersionID      string                   `json:"version_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_ingestion_attempt,priority:3"`
	Attempt        uint                     `json:"attempt" gorm:"not null;uniqueIndex:idx_knowledge_ingestion_attempt,priority:4"`
	Status         KnowledgeIngestionStatus `json:"status" gorm:"size:24;not null;default:'queued';index"`
	ParserKey      string                   `json:"parser_key" gorm:"size:64;not null"`
	FailureCode    string                   `json:"failure_code,omitempty" gorm:"size:64"`
	FailureDetail  string                   `json:"failure_detail,omitempty" gorm:"size:1000"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	CompletedAt    *time.Time               `json:"completed_at,omitempty"`
	CreatedByType  ActorType                `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID    string                   `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (KnowledgeIngestionTask) TableName() string {
	return "knowledge_ingestion_tasks"
}

func (task *KnowledgeIngestionTask) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&task.ID); err != nil {
		return err
	}
	if task.Status == "" {
		task.Status = KnowledgeIngestionQueued
	}
	if task.Attempt == 0 {
		task.Attempt = 1
	}
	return task.Validate()
}

func (task *KnowledgeIngestionTask) BeforeUpdate(_ *gorm.DB) error {
	return task.Validate()
}

func (task KnowledgeIngestionTask) Validate() error {
	if err := validateKnowledgeScope(task.OrganizationID, task.ProjectID); err != nil {
		return err
	}
	if strings.TrimSpace(task.ArticleID) == "" ||
		strings.TrimSpace(task.VersionID) == "" ||
		task.Attempt == 0 {
		return errors.New("knowledge ingestion requires article, version and attempt")
	}
	if !task.Status.IsValid() {
		return fmt.Errorf("knowledge ingestion status %q is invalid", task.Status)
	}
	if strings.TrimSpace(task.ParserKey) == "" {
		return errors.New("knowledge ingestion parser key is required")
	}
	if err := (ActorRef{
		Type: task.CreatedByType,
		ID:   task.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge ingestion creator is invalid: %w", err)
	}
	return nil
}

type KnowledgeChunk struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID  uint   `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_chunk_ordinal,priority:1"`
	ProjectID       uint   `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_chunk_ordinal,priority:2"`
	ArticleID       string `json:"article_id" gorm:"size:36;not null;index"`
	VersionID       string `json:"version_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_chunk_ordinal,priority:3"`
	IngestionTaskID string `json:"ingestion_task_id" gorm:"size:36;not null;index"`
	Ordinal         uint   `json:"ordinal" gorm:"not null;uniqueIndex:idx_knowledge_chunk_ordinal,priority:4"`
	PageNumber      *int   `json:"page_number,omitempty" gorm:"index"`
	SectionPath     string `json:"section_path,omitempty" gorm:"size:500"`
	Content         string `json:"content" gorm:"type:text;not null"`
	Snippet         string `json:"snippet" gorm:"size:1000;not null"`
	ContentHash     string `json:"content_hash" gorm:"size:64;not null;index"`
	TokenCount      int    `json:"token_count" gorm:"not null;default:0"`
}

func (KnowledgeChunk) TableName() string {
	return "knowledge_chunks"
}

func (chunk *KnowledgeChunk) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&chunk.ID); err != nil {
		return err
	}
	if chunk.ContentHash == "" {
		digest := sha256.Sum256([]byte(chunk.Content))
		chunk.ContentHash = hex.EncodeToString(digest[:])
	}
	return chunk.Validate()
}

func (chunk *KnowledgeChunk) BeforeUpdate(_ *gorm.DB) error {
	return errors.New("knowledge chunks are immutable; rebuild the version instead")
}

func (chunk KnowledgeChunk) Validate() error {
	if err := validateKnowledgeScope(chunk.OrganizationID, chunk.ProjectID); err != nil {
		return err
	}
	if strings.TrimSpace(chunk.ArticleID) == "" ||
		strings.TrimSpace(chunk.VersionID) == "" ||
		strings.TrimSpace(chunk.IngestionTaskID) == "" {
		return errors.New("knowledge chunk requires article, version and ingestion task")
	}
	if strings.TrimSpace(chunk.Content) == "" ||
		strings.TrimSpace(chunk.Snippet) == "" {
		return errors.New("knowledge chunk requires content and snippet")
	}
	if !knowledgeHashPattern.MatchString(chunk.ContentHash) {
		return errors.New("knowledge chunk content hash must be SHA-256")
	}
	if chunk.PageNumber != nil && *chunk.PageNumber <= 0 {
		return errors.New("knowledge chunk page number must be positive")
	}
	if chunk.TokenCount < 0 {
		return errors.New("knowledge chunk token count cannot be negative")
	}
	return nil
}

type KnowledgeCitation struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	OrganizationID  uint      `json:"organization_id" gorm:"not null;index"`
	ProjectID       uint      `json:"project_id" gorm:"not null;index"`
	SearchID        string    `json:"search_id" gorm:"size:36;not null;index"`
	ArticleID       string    `json:"article_id" gorm:"size:36;not null;index"`
	VersionID       string    `json:"version_id" gorm:"size:36;not null;index"`
	DocumentVersion uint64    `json:"document_version" gorm:"not null"`
	ChunkID         string    `json:"chunk_id" gorm:"size:36;not null;index"`
	PageNumber      *int      `json:"page_number,omitempty"`
	Snippet         string    `json:"snippet" gorm:"size:1000;not null"`
	ContentHash     string    `json:"content_hash" gorm:"size:64;not null;index"`
	Rank            int       `json:"rank" gorm:"not null"`
	Score           float64   `json:"score" gorm:"not null"`
	CreatedByType   ActorType `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID     string    `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (KnowledgeCitation) TableName() string {
	return "knowledge_citations"
}

func (citation *KnowledgeCitation) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&citation.ID); err != nil {
		return err
	}
	return citation.Validate()
}

func (citation KnowledgeCitation) Validate() error {
	if err := validateKnowledgeScope(
		citation.OrganizationID,
		citation.ProjectID,
	); err != nil {
		return err
	}
	if strings.TrimSpace(citation.SearchID) == "" ||
		strings.TrimSpace(citation.ArticleID) == "" ||
		strings.TrimSpace(citation.VersionID) == "" ||
		strings.TrimSpace(citation.ChunkID) == "" ||
		citation.DocumentVersion == 0 {
		return errors.New("knowledge citation requires search and document version references")
	}
	if citation.PageNumber != nil && *citation.PageNumber <= 0 {
		return errors.New("knowledge citation page number must be positive")
	}
	if strings.TrimSpace(citation.Snippet) == "" {
		return errors.New("knowledge citation snippet is required")
	}
	if !knowledgeHashPattern.MatchString(citation.ContentHash) {
		return errors.New("knowledge citation content hash must be SHA-256")
	}
	if citation.Rank <= 0 {
		return errors.New("knowledge citation rank must be positive")
	}
	if err := (ActorRef{
		Type: citation.CreatedByType,
		ID:   citation.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge citation actor is invalid: %w", err)
	}
	return nil
}

type KnowledgeFeedbackRating string

const (
	KnowledgeFeedbackHelpful   KnowledgeFeedbackRating = "helpful"
	KnowledgeFeedbackUnhelpful KnowledgeFeedbackRating = "unhelpful"
	KnowledgeFeedbackIncorrect KnowledgeFeedbackRating = "incorrect"
	KnowledgeFeedbackOutdated  KnowledgeFeedbackRating = "outdated"
)

func (rating KnowledgeFeedbackRating) IsValid() bool {
	switch rating {
	case KnowledgeFeedbackHelpful,
		KnowledgeFeedbackUnhelpful,
		KnowledgeFeedbackIncorrect,
		KnowledgeFeedbackOutdated:
		return true
	default:
		return false
	}
}

type KnowledgeFeedback struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`

	OrganizationID uint                    `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_feedback_actor,priority:1"`
	ProjectID      uint                    `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_feedback_actor,priority:2"`
	CitationID     string                  `json:"citation_id" gorm:"size:36;not null;index;uniqueIndex:idx_knowledge_feedback_actor,priority:3"`
	Rating         KnowledgeFeedbackRating `json:"rating" gorm:"size:20;not null"`
	Comment        string                  `json:"comment,omitempty" gorm:"size:1000"`
	ActorType      ActorType               `json:"actor_type" gorm:"size:32;not null;uniqueIndex:idx_knowledge_feedback_actor,priority:4"`
	ActorID        string                  `json:"actor_id" gorm:"size:128;not null;uniqueIndex:idx_knowledge_feedback_actor,priority:5"`
}

func (KnowledgeFeedback) TableName() string {
	return "knowledge_feedback"
}

func (feedback *KnowledgeFeedback) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&feedback.ID); err != nil {
		return err
	}
	return feedback.Validate()
}

func (feedback KnowledgeFeedback) Validate() error {
	if err := validateKnowledgeScope(
		feedback.OrganizationID,
		feedback.ProjectID,
	); err != nil {
		return err
	}
	if strings.TrimSpace(feedback.CitationID) == "" {
		return errors.New("knowledge feedback citation id is required")
	}
	if !feedback.Rating.IsValid() {
		return fmt.Errorf("knowledge feedback rating %q is invalid", feedback.Rating)
	}
	if err := (ActorRef{
		Type: feedback.ActorType,
		ID:   feedback.ActorID,
	}).Validate(); err != nil {
		return fmt.Errorf("knowledge feedback actor is invalid: %w", err)
	}
	return nil
}

type KnowledgeIndexStatus string

const (
	KnowledgeIndexRebuildRequested KnowledgeIndexStatus = "rebuild_requested"
	KnowledgeIndexBuilding         KnowledgeIndexStatus = "building"
	KnowledgeIndexReady            KnowledgeIndexStatus = "ready"
	KnowledgeIndexFailed           KnowledgeIndexStatus = "failed"
)

func (status KnowledgeIndexStatus) IsValid() bool {
	switch status {
	case KnowledgeIndexRebuildRequested,
		KnowledgeIndexBuilding,
		KnowledgeIndexReady,
		KnowledgeIndexFailed:
		return true
	default:
		return false
	}
}

type KnowledgeIndexState struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID    uint                 `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_knowledge_index_project_name,priority:1"`
	ProjectID         uint                 `json:"project_id" gorm:"not null;index;uniqueIndex:idx_knowledge_index_project_name,priority:2"`
	IndexName         string               `json:"index_name" gorm:"size:64;not null;uniqueIndex:idx_knowledge_index_project_name,priority:3"`
	Generation        uint64               `json:"generation" gorm:"not null;default:0"`
	DesiredGeneration uint64               `json:"desired_generation" gorm:"not null;default:1"`
	Status            KnowledgeIndexStatus `json:"status" gorm:"size:24;not null;default:'rebuild_requested';index"`
	SourceDigest      string               `json:"source_digest,omitempty" gorm:"size:64"`
	DocumentCount     int                  `json:"document_count" gorm:"not null;default:0"`
	FailureDetail     string               `json:"failure_detail,omitempty" gorm:"size:1000"`
	StartedAt         *time.Time           `json:"started_at,omitempty"`
	CompletedAt       *time.Time           `json:"completed_at,omitempty"`
}

func (KnowledgeIndexState) TableName() string {
	return "knowledge_index_states"
}

func (state *KnowledgeIndexState) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&state.ID); err != nil {
		return err
	}
	if state.IndexName == "" {
		state.IndexName = "knowledge"
	}
	if state.Status == "" {
		state.Status = KnowledgeIndexRebuildRequested
	}
	if state.DesiredGeneration == 0 {
		state.DesiredGeneration = state.Generation + 1
	}
	return state.Validate()
}

func (state *KnowledgeIndexState) BeforeUpdate(_ *gorm.DB) error {
	return state.Validate()
}

func (state KnowledgeIndexState) Validate() error {
	if err := validateKnowledgeScope(state.OrganizationID, state.ProjectID); err != nil {
		return err
	}
	if !knowledgeKeyPattern.MatchString(state.IndexName) {
		return fmt.Errorf("knowledge index name %q is invalid", state.IndexName)
	}
	if !state.Status.IsValid() {
		return fmt.Errorf("knowledge index status %q is invalid", state.Status)
	}
	if state.DesiredGeneration < state.Generation {
		return errors.New("knowledge desired generation cannot precede current generation")
	}
	if state.SourceDigest != "" &&
		!knowledgeHashPattern.MatchString(state.SourceDigest) {
		return errors.New("knowledge index source digest must be SHA-256")
	}
	if state.DocumentCount < 0 {
		return errors.New("knowledge index document count cannot be negative")
	}
	return nil
}

type ModelDataEgressMode string

const (
	ModelDataEgressDenied   ModelDataEgressMode = "denied"
	ModelDataEgressRedacted ModelDataEgressMode = "redacted"
	ModelDataEgressAllowed  ModelDataEgressMode = "allowed"
)

func (mode ModelDataEgressMode) IsValid() bool {
	switch mode {
	case ModelDataEgressDenied,
		ModelDataEgressRedacted,
		ModelDataEgressAllowed:
		return true
	default:
		return false
	}
}

type ModelRedactionRule struct {
	Literal     string `json:"literal"`
	Replacement string `json:"replacement"`
}

func (rule ModelRedactionRule) Validate() error {
	if strings.TrimSpace(rule.Literal) == "" {
		return errors.New("model redaction literal is required")
	}
	if len(rule.Literal) > 256 || len(rule.Replacement) > 256 {
		return errors.New("model redaction rule exceeds maximum length")
	}
	return nil
}

type ProjectModelPolicy struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36;<-:create"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`

	OrganizationID uint                `json:"organization_id" gorm:"not null;index;uniqueIndex:idx_project_model_policy_active,priority:1"`
	ProjectID      uint                `json:"project_id" gorm:"not null;index;uniqueIndex:idx_project_model_policy_active,priority:2"`
	PolicyKey      string              `json:"policy_key" gorm:"size:64;not null;uniqueIndex:idx_project_model_policy_active,priority:3"`
	IsActive       bool                `json:"is_active" gorm:"not null;default:true;index"`
	ProviderKey    string              `json:"provider_key" gorm:"size:64;not null"`
	GenerateModel  string              `json:"generate_model" gorm:"size:160;not null"`
	EmbeddingModel string              `json:"embedding_model" gorm:"size:160;not null"`
	RerankModel    string              `json:"rerank_model" gorm:"size:160;not null"`
	DataEgress     ModelDataEgressMode `json:"data_egress" gorm:"size:20;not null;default:'denied'"`

	RedactionRules    datatypes.JSON `json:"redaction_rules" gorm:"type:jsonb;not null"`
	ProviderAllowlist datatypes.JSON `json:"provider_allowlist" gorm:"type:jsonb;not null"`
	ModelAllowlist    datatypes.JSON `json:"model_allowlist" gorm:"type:jsonb;not null"`

	MonthlyTokenBudget      int64 `json:"monthly_token_budget" gorm:"not null;default:0"`
	MonthlyCostBudgetMicros int64 `json:"monthly_cost_budget_micros" gorm:"not null;default:0"`
	RequestsPerMinute       int   `json:"requests_per_minute" gorm:"not null;default:0"`
	TokensPerMinute         int   `json:"tokens_per_minute" gorm:"not null;default:0"`

	CreatedByType ActorType `json:"created_by_type" gorm:"size:32;not null;<-:create"`
	CreatedByID   string    `json:"created_by_id" gorm:"size:128;not null;<-:create"`
}

func (ProjectModelPolicy) TableName() string {
	return "project_model_policies"
}

func (policy *ProjectModelPolicy) BeforeCreate(_ *gorm.DB) error {
	if err := ensureKnowledgeUUID(&policy.ID); err != nil {
		return err
	}
	if policy.PolicyKey == "" {
		policy.PolicyKey = "knowledge"
	}
	return policy.Validate()
}

func (policy *ProjectModelPolicy) BeforeUpdate(_ *gorm.DB) error {
	return policy.Validate()
}

func (policy ProjectModelPolicy) Redactions() ([]ModelRedactionRule, error) {
	var rules []ModelRedactionRule
	if err := decodeKnowledgeStrictJSON(policy.RedactionRules, &rules); err != nil {
		return nil, fmt.Errorf("decode model redaction rules: %w", err)
	}
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

func (policy ProjectModelPolicy) AllowedProviders() ([]string, error) {
	return decodeKnowledgeStringSet(policy.ProviderAllowlist, "provider allowlist")
}

func (policy ProjectModelPolicy) AllowedModels() ([]string, error) {
	return decodeKnowledgeStringSet(policy.ModelAllowlist, "model allowlist")
}

func (policy ProjectModelPolicy) Validate() error {
	if err := validateKnowledgeScope(policy.OrganizationID, policy.ProjectID); err != nil {
		return err
	}
	if !knowledgeKeyPattern.MatchString(policy.PolicyKey) {
		return fmt.Errorf("model policy key %q is invalid", policy.PolicyKey)
	}
	if strings.TrimSpace(policy.ProviderKey) == "" ||
		strings.TrimSpace(policy.GenerateModel) == "" ||
		strings.TrimSpace(policy.EmbeddingModel) == "" ||
		strings.TrimSpace(policy.RerankModel) == "" {
		return errors.New("model policy requires provider and generate/embed/rerank models")
	}
	if !policy.DataEgress.IsValid() {
		return fmt.Errorf("model data egress mode %q is invalid", policy.DataEgress)
	}
	if _, err := policy.Redactions(); err != nil {
		return err
	}
	providers, err := policy.AllowedProviders()
	if err != nil {
		return err
	}
	if !containsKnowledgeString(providers, policy.ProviderKey) {
		return errors.New("selected model provider is not allowlisted")
	}
	models, err := policy.AllowedModels()
	if err != nil {
		return err
	}
	for _, model := range []string{
		policy.GenerateModel,
		policy.EmbeddingModel,
		policy.RerankModel,
	} {
		if !containsKnowledgeString(models, model) {
			return fmt.Errorf("selected model %q is not allowlisted", model)
		}
	}
	if policy.MonthlyTokenBudget < 0 ||
		policy.MonthlyCostBudgetMicros < 0 ||
		policy.RequestsPerMinute < 0 ||
		policy.TokensPerMinute < 0 {
		return errors.New("model budgets and limits cannot be negative")
	}
	if policy.DataEgress == ModelDataEgressRedacted {
		rules, err := policy.Redactions()
		if err != nil {
			return err
		}
		if len(rules) == 0 {
			return errors.New("redacted model egress requires redaction rules")
		}
	}
	if err := (ActorRef{
		Type: policy.CreatedByType,
		ID:   policy.CreatedByID,
	}).Validate(); err != nil {
		return fmt.Errorf("model policy creator is invalid: %w", err)
	}
	return nil
}

func ensureKnowledgeUUID(value *string) error {
	if value == nil {
		return errors.New("knowledge id destination is required")
	}
	if strings.TrimSpace(*value) == "" {
		generated, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate knowledge UUIDv7: %w", err)
		}
		*value = generated.String()
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil || parsed.String() != strings.ToLower(*value) {
		return errors.New("knowledge id must be a canonical UUID")
	}
	return nil
}

func validateKnowledgeScope(organizationID uint, projectID uint) error {
	return (ProjectScope{
		OrganizationID: organizationID,
		ProjectID:      projectID,
	}).Validate()
}

func isKnowledgeVirusScanStatus(status VirusScanStatus) bool {
	switch status {
	case VirusScanPending, VirusScanClean, VirusScanInfected, VirusScanError:
		return true
	default:
		return false
	}
}

func decodeKnowledgeStrictJSON(raw []byte, destination any) error {
	if len(raw) == 0 {
		return errors.New("JSON value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func decodeKnowledgeStringSet(raw []byte, name string) ([]string, error) {
	var values []string
	if err := decodeKnowledgeStrictJSON(raw, &values); err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(values) == 0 || len(values) > 128 {
		return nil, fmt.Errorf("%s must contain between 1 and 128 values", name)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 160 {
			return nil, fmt.Errorf("%s contains an invalid value", name)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%s contains duplicate value %q", name, value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func containsKnowledgeString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
