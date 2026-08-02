package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type authoredMemoryStorage struct {
	mu       sync.Mutex
	objects  map[string][]byte
	deleted  []string
	putErr   error
	afterPut func()
}

type authoredNamedMemoryStorage struct {
	*authoredMemoryStorage
	storageType string
	storeID     string
}

type authoredVersionedMemoryStorage struct {
	*authoredNamedMemoryStorage
	versionID       string
	openedVersions  []string
	deletedVersions []string
}

func (storage *authoredNamedMemoryStorage) AttachmentStorageType() string {
	return storage.storageType
}

func (storage *authoredNamedMemoryStorage) AttachmentStoreID() string {
	if strings.TrimSpace(storage.storeID) != "" {
		return storage.storeID
	}
	return storage.storageType + "-default"
}

func (storage *authoredVersionedMemoryStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	stored, err := storage.authoredMemoryStorage.Put(
		ctx,
		key,
		reader,
		maxBytes,
	)
	if err != nil {
		return nil, err
	}
	stored.StorageType = storage.AttachmentStorageType()
	stored.StoreID = storage.AttachmentStoreID()
	stored.VersionID = storage.versionID
	return stored, nil
}

func (storage *authoredVersionedMemoryStorage) OpenVersion(
	ctx context.Context,
	key string,
	versionID string,
) (io.ReadCloser, error) {
	storage.mu.Lock()
	storage.openedVersions = append(
		storage.openedVersions,
		versionID,
	)
	storage.mu.Unlock()
	if versionID != storage.versionID {
		return nil, errors.New("unexpected object version")
	}
	return storage.authoredMemoryStorage.Open(ctx, key)
}

func (storage *authoredVersionedMemoryStorage) DeleteVersion(
	ctx context.Context,
	key string,
	versionID string,
) error {
	storage.mu.Lock()
	storage.deletedVersions = append(
		storage.deletedVersions,
		versionID,
	)
	storage.mu.Unlock()
	if versionID != storage.versionID {
		return errors.New("unexpected object version")
	}
	return storage.authoredMemoryStorage.Delete(ctx, key)
}

func newAuthoredMemoryStorage() *authoredMemoryStorage {
	return &authoredMemoryStorage{objects: make(map[string][]byte)}
}

func (*authoredMemoryStorage) AttachmentStorageType() string {
	return "managed"
}

func (*authoredMemoryStorage) AttachmentStoreID() string {
	return "memory-default"
}

func (storage *authoredMemoryStorage) Put(
	_ context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if storage.putErr != nil {
		return nil, storage.putErr
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, errors.New("too large")
	}
	digest := sha256.Sum256(payload)
	storage.mu.Lock()
	storage.objects[key] = bytes.Clone(payload)
	storage.mu.Unlock()
	if storage.afterPut != nil {
		storage.afterPut()
	}
	return &StoredAttachmentObject{
		Key:                 key,
		Size:                int64(len(payload)),
		SHA256:              hex.EncodeToString(digest[:]),
		DetectedContentType: http.DetectContentType(payload),
	}, nil
}

func (storage *authoredMemoryStorage) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	payload, exists := storage.objects[key]
	if !exists {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(payload))), nil
}

func (storage *authoredMemoryStorage) Delete(
	_ context.Context,
	key string,
) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	delete(storage.objects, key)
	storage.deleted = append(storage.deleted, key)
	return nil
}

func TestCreateAuthoredArticlePersistsAtomicDraftAndDocument(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	ticket, attachment := fixture.source(t)
	idempotencyID := newNativeID()
	if err := fixture.db.Create(&models.IdempotencyRecord{
		ID:                       idempotencyID,
		OrganizationID:           fixture.scope.OrganizationID,
		ProjectID:                fixture.scope.ProjectID,
		ActorType:                models.ActorTypeHuman,
		ActorID:                  "42",
		Operation:                "knowledge.article.create",
		Key:                      "authored-test",
		RequestHash:              strings.Repeat("b", 64),
		State:                    models.IdempotencyStateProcessing,
		ExpiresAt:                time.Now().Add(time.Minute),
		CompletionTTLNanoseconds: time.Hour.Nanoseconds(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	markdown := "# 恢复步骤\n\n先检查备份。\n\n## 验证\n\n执行只读检查。\n"
	result, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                      "database-recovery",
			Title:                    "数据库恢复",
			Summary:                  "值班手册",
			Markdown:                 markdown,
			GrantProjectAccess:       true,
			SourceTicketID:           ticket.ID,
			SourceAttachmentIDs:      []uint{attachment.ID},
			IdempotencyRecordID:      idempotencyID,
			IdempotencyCompletionTTL: time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("create authored article: %v", err)
	}
	if result.Article.ID == "" ||
		result.Version.ArticleID != result.Article.ID ||
		result.Version.Status != models.KnowledgeVersionDraft ||
		result.Version.VirusScan != models.VirusScanClean ||
		result.Version.ObjectKey != fmt.Sprintf(
			"knowledge/%d/%s/%s.md",
			fixture.scope.ProjectID,
			result.Article.ID,
			result.Version.ID,
		) {
		t.Fatalf("unexpected authored result: %+v", result)
	}
	if result.Document == nil ||
		result.Document.Markdown != markdown ||
		len(result.Document.Sections) != 2 ||
		len(result.Sources) != 1 ||
		result.Sources[0].AttachmentHash != attachment.Hash {
		t.Fatalf("unexpected authored document: %+v", result.Document)
	}
	if result.Receipt.ResourceID != result.Article.ID ||
		result.Receipt.ResourceVersion != 1 ||
		result.Receipt.EventID == "" {
		t.Fatalf("unexpected authored receipt: %+v", result.Receipt)
	}
	var ingestion models.KnowledgeIngestionTask
	if err := fixture.db.Where(
		"version_id = ?",
		result.Version.ID,
	).First(&ingestion).Error; err != nil {
		t.Fatal(err)
	}
	if ingestion.Status != models.KnowledgeIngestionCompleted {
		t.Fatalf("ingestion status = %q", ingestion.Status)
	}
	var chunks []models.KnowledgeChunk
	if err := fixture.db.Where(
		"version_id = ?",
		result.Version.ID,
	).Order("ordinal ASC").Find(&chunks).Error; err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 ||
		chunks[0].SectionPath != "恢复步骤" ||
		chunks[1].SectionPath != "恢复步骤 / 验证" {
		t.Fatalf("unexpected authored chunks: %+v", chunks)
	}
	var event models.DomainEvent
	if err := fixture.db.First(&event, "id = ?", result.Event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(event.Data, []byte("先检查备份")) ||
		!bytes.Contains(event.Data, []byte(result.Version.ContentHash)) {
		t.Fatalf("event leaked body or omitted hash: %s", event.Data)
	}
	var idempotency models.IdempotencyRecord
	if err := fixture.db.First(&idempotency, "id = ?", idempotencyID).Error; err != nil {
		t.Fatal(err)
	}
	if idempotency.State != models.IdempotencyStateCompleted ||
		idempotency.ResponseCode != http.StatusCreated ||
		idempotency.ResourceID != result.Article.ID ||
		idempotency.EventID != event.ID {
		t.Fatalf("idempotency was not completed atomically: %+v", idempotency)
	}
	var completed AuthoredKnowledgeIdempotencyReceipt
	if err := json.Unmarshal(idempotency.ResponseBody, &completed); err != nil {
		t.Fatalf("decode authored idempotency receipt: %v", err)
	}
	if completed.OperationID != result.Receipt.OperationID ||
		completed.ResourceID != result.Article.ID ||
		completed.ArticleID != result.Article.ID ||
		completed.VersionID != result.Version.ID ||
		completed.ContentHash != result.Version.ContentHash {
		t.Fatalf(
			"authored idempotency receipt is not version-bound: %+v",
			completed,
		)
	}

	document, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		result.Article.ID,
		GetArticleDocumentInput{VersionID: result.Version.ID},
	)
	if err != nil {
		t.Fatalf("get authored document: %v", err)
	}
	if document.Markdown != markdown ||
		document.Version.ContentHash != result.Version.ContentHash ||
		len(document.Sources) != 1 {
		t.Fatalf("unexpected loaded document: %+v", document)
	}
}

func TestKnowledgeSourceAuthorizationsAreBoundedAndCanonical(
	t *testing.T,
) {
	decisionID := newNativeID()
	maximum := make(
		[]KnowledgeSourceAuthorization,
		0,
		MaxAuthoredSourceLinks,
	)
	for index := 0; index < MaxAuthoredSourceLinks; index++ {
		maximum = append(maximum, KnowledgeSourceAuthorization{
			SourceTicketID:         uint(index + 1),
			TicketPolicyDecisionID: decisionID,
		})
	}
	tooMany := append(
		append([]KnowledgeSourceAuthorization(nil), maximum...),
		KnowledgeSourceAuthorization{
			SourceTicketID:         uint(MaxAuthoredSourceLinks + 1),
			TicketPolicyDecisionID: decisionID,
		},
	)
	for _, testCase := range []struct {
		name           string
		authorizations []KnowledgeSourceAuthorization
		wantError      bool
	}{
		{name: "empty"},
		{
			name:           "maximum",
			authorizations: maximum,
		},
		{
			name:           "too many",
			authorizations: tooMany,
			wantError:      true,
		},
		{
			name: "duplicate ticket",
			authorizations: []KnowledgeSourceAuthorization{
				{
					SourceTicketID:         1,
					TicketPolicyDecisionID: decisionID,
				},
				{
					SourceTicketID:         1,
					TicketPolicyDecisionID: decisionID,
				},
			},
			wantError: true,
		},
		{
			name: "invalid decision",
			authorizations: []KnowledgeSourceAuthorization{{
				SourceTicketID:         1,
				TicketPolicyDecisionID: "not-a-uuid",
			}},
			wantError: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateKnowledgeSourceAuthorizations(
				testCase.authorizations,
			)
			if (err != nil) != testCase.wantError {
				t.Fatalf(
					"validation error = %v, wantError=%t",
					err,
					testCase.wantError,
				)
			}
		})
	}
}

func TestCreateAuthoredArticleCleansObjectWhenTransactionFails(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	first, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "duplicate-key",
			Title:    "首个版本",
			Markdown: "# 正文\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "duplicate-key",
			Title:    "重复版本",
			Markdown: "# 不应保留\n",
		},
	)
	if err == nil {
		t.Fatal("duplicate article key unexpectedly succeeded")
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 1 ||
		!bytes.Equal(
			fixture.storage.objects[first.Version.ObjectKey],
			[]byte("# 正文\n"),
		) ||
		len(fixture.storage.deleted) == 0 {
		t.Fatalf(
			"failed transaction left storage state objects=%v deleted=%v",
			fixture.storage.objects,
			fixture.storage.deleted,
		)
	}
}

func TestKnowledgeContributorCanSubmitPrivateDraftWithoutPublishingAuthority(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		501,
		models.ProjectRoleRequester,
	)
	result, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "member-created-draft",
			Title:    "普通成员沉淀的排障说明",
			Markdown: "# 现象\n\n由项目成员提交，等待复核。\n",
		},
	)
	if err != nil {
		t.Fatalf("contributor create draft: %v", err)
	}
	if result.Version.Status != models.KnowledgeVersionDraft ||
		result.Article.CurrentVersion != nil {
		t.Fatalf("contributor result was not an unpublished draft: %+v", result)
	}
	var creatorGrant models.KnowledgeArticleACL
	if err := fixture.db.Where(
		"article_id = ? AND subject_type = ? AND subject_id = ?",
		result.Article.ID,
		models.KnowledgeACLHuman,
		"501",
	).First(&creatorGrant).Error; err != nil {
		t.Fatal(err)
	}
	if creatorGrant.Permission != models.KnowledgeACLManage {
		t.Fatalf("creator ACL = %+v", creatorGrant)
	}
	var projectReadCount int64
	if err := fixture.db.Model(&models.KnowledgeArticleACL{}).
		Where(
			"article_id = ? AND subject_type = ?",
			result.Article.ID,
			models.KnowledgeACLAllProject,
		).
		Count(&projectReadCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectReadCount != 0 {
		t.Fatalf("contributor draft granted %d project-wide ACLs", projectReadCount)
	}
	if _, err := fixture.service.GrantArticleAccess(
		contributorContext,
		result.Article.ID,
		models.KnowledgeACLSubject{
			Type: models.KnowledgeACLAllProject,
			ID:   "*",
		},
		models.KnowledgeACLRead,
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("contributor domain ACL grant error = %v", err)
	}
	document, err := fixture.service.GetArticleDocument(
		contributorContext,
		result.Article.ID,
		GetArticleDocumentInput{VersionID: result.Version.ID},
	)
	if err != nil {
		t.Fatalf("contributor read own draft: %v", err)
	}
	if document.Markdown != "# 现象\n\n由项目成员提交，等待复核。\n" {
		t.Fatalf("own draft body = %q", document.Markdown)
	}
	directoryRequest := DirectoryPageRequest{
		Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
	}
	if _, err := fixture.service.ListArticleVersions(
		contributorContext,
		result.Article.ID,
		KnowledgeVersionListFilter{},
		directoryRequest,
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("contributor version directory error = %v", err)
	}
	if _, err := fixture.service.ListIngestions(
		contributorContext,
		KnowledgeIngestionListFilter{},
		directoryRequest,
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("contributor ingestion directory error = %v", err)
	}
	defaultPage, err := fixture.service.ListArticles(
		contributorContext,
		KnowledgeArticleListFilter{},
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "updated_at", SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if defaultPage.Total != 0 {
		t.Fatalf("unpublished contributor draft leaked into browse: %+v", defaultPage)
	}
	minePage, err := fixture.service.ListArticles(
		contributorContext,
		KnowledgeArticleListFilter{ManagedByActor: true},
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "updated_at", SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if minePage.Total != 1 ||
		len(minePage.Items) != 1 ||
		minePage.Items[0].ID != result.Article.ID {
		t.Fatalf("contributor personal draft page = %+v", minePage)
	}

	if _, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:                "member-public-draft",
			Title:              "不应自行授权",
			Markdown:           "# body\n",
			GrantProjectAccess: true,
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("contributor project ACL request error = %v", err)
	}
	if _, err := fixture.service.PublishVersion(
		contributorContext,
		result.Version.ID,
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("contributor publish error = %v", err)
	}
	if _, err := fixture.service.PublishVersion(
		fixture.ctx,
		result.Version.ID,
	); err != nil {
		t.Fatalf("manager publish contributor draft: %v", err)
	}
	var projectReadACLCount int64
	if err := fixture.db.Model(&models.KnowledgeArticleACL{}).
		Where(
			"article_id = ? AND subject_type = ? AND subject_id = ? AND permission = ?",
			result.Article.ID,
			models.KnowledgeACLAllProject,
			"*",
			models.KnowledgeACLRead,
		).
		Count(&projectReadACLCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectReadACLCount != 1 {
		t.Fatalf(
			"published contributor article project read ACL count = %d",
			projectReadACLCount,
		)
	}
	var publicationEvent models.DomainEvent
	if err := fixture.db.Where(
		"type = ? AND subject = ?",
		eventcontract.KnowledgeVersionPublishedEventType,
		fmt.Sprintf(
			"knowledge/articles/%s/versions/%s",
			result.Article.ID,
			result.Version.ID,
		),
	).Take(&publicationEvent).Error; err != nil {
		t.Fatalf("load knowledge publication event: %v", err)
	}
	var publicationData map[string]any
	if err := json.Unmarshal(
		publicationEvent.Data,
		&publicationData,
	); err != nil {
		t.Fatalf("decode knowledge publication event: %v", err)
	}
	if publicationData["article_id"] != result.Article.ID ||
		publicationData["version_id"] != result.Version.ID ||
		publicationData["audience"] != "project" {
		t.Fatalf(
			"knowledge publication event data = %+v",
			publicationData,
		)
	}
	readerContext := fixture.humanContext(
		t,
		599,
		models.ProjectRoleObserver,
	)
	readerPage, err := fixture.service.ListArticles(
		readerContext,
		KnowledgeArticleListFilter{},
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "updated_at", SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatalf("project reader list published contribution: %v", err)
	}
	if readerPage.Total != 1 ||
		len(readerPage.Items) != 1 ||
		readerPage.Items[0].ID != result.Article.ID {
		t.Fatalf("published contribution reader page = %+v", readerPage)
	}
	readerDocument, err := fixture.service.GetArticleDocument(
		readerContext,
		result.Article.ID,
		GetArticleDocumentInput{},
	)
	if err != nil {
		t.Fatalf("project reader open published contribution: %v", err)
	}
	if readerDocument.Version.ID != result.Version.ID {
		t.Fatalf(
			"reader document version = %s, want %s",
			readerDocument.Version.ID,
			result.Version.ID,
		)
	}
	revised, err := fixture.service.CreateAuthoredVersion(
		contributorContext,
		result.Article.ID,
		CreateAuthoredVersionInput{
			Title:    "复核前补充",
			Markdown: "# 现象\n\n补充后的处理步骤。\n",
		},
	)
	if err != nil {
		t.Fatalf("contributor revise own draft: %v", err)
	}
	if revised.Version.Version != 2 ||
		revised.Version.Status != models.KnowledgeVersionDraft {
		t.Fatalf("contributor revision = %+v", revised.Version)
	}
	publishedDocument, err := fixture.service.GetArticleDocument(
		contributorContext,
		result.Article.ID,
		GetArticleDocumentInput{},
	)
	if err != nil {
		t.Fatalf("read canonical published version: %v", err)
	}
	if publishedDocument.Version.ID != result.Version.ID {
		t.Fatalf(
			"default document version = %s, want published %s",
			publishedDocument.Version.ID,
			result.Version.ID,
		)
	}
	latestDraft, err := fixture.service.GetArticleDocument(
		contributorContext,
		result.Article.ID,
		GetArticleDocumentInput{PreferLatestDraft: true},
	)
	if err != nil {
		t.Fatalf("read preferred latest draft: %v", err)
	}
	if latestDraft.Version.ID != revised.Version.ID ||
		latestDraft.Markdown != "# 现象\n\n补充后的处理步骤。\n" {
		t.Fatalf("preferred latest draft = %+v", latestDraft.Version)
	}
}

type failingKnowledgePublicationEventAppender struct{}

func (failingKnowledgePublicationEventAppender) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	input DomainEventInput,
	_ []OutboxTarget,
) (*models.DomainEvent, error) {
	return nil, fmt.Errorf("reject event %s", input.Type)
}

func TestKnowledgePublicationEventFailureRollsBackVersionAndAudience(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "publication-event-rollback",
			Title:    "发布事件失败回滚",
			Markdown: "# body\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.events = failingKnowledgePublicationEventAppender{}
	if _, err := fixture.service.PublishVersion(
		fixture.ctx,
		created.Version.ID,
	); err == nil ||
		!strings.Contains(err.Error(), "reject event") {
		t.Fatalf("publication event failure = %v", err)
	}
	var version models.KnowledgeArticleVersion
	if err := fixture.db.First(
		&version,
		"id = ?",
		created.Version.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if version.Status != models.KnowledgeVersionDraft ||
		version.PublishedAt != nil {
		t.Fatalf("failed publication mutated version: %+v", version)
	}
	var article models.KnowledgeArticle
	if err := fixture.db.First(
		&article,
		"id = ?",
		created.Article.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if article.CurrentVersion != nil {
		t.Fatalf(
			"failed publication activated article version: %+v",
			article.CurrentVersion,
		)
	}
	var projectACLs int64
	if err := fixture.db.Model(&models.KnowledgeArticleACL{}).
		Where(
			"article_id = ? AND subject_type = ?",
			created.Article.ID,
			models.KnowledgeACLAllProject,
		).
		Count(&projectACLs).Error; err != nil {
		t.Fatal(err)
	}
	if projectACLs != 0 {
		t.Fatalf(
			"failed publication persisted %d project ACLs",
			projectACLs,
		)
	}
	var indexStates int64
	if err := fixture.db.Model(&models.KnowledgeIndexState{}).
		Count(&indexStates).Error; err != nil {
		t.Fatal(err)
	}
	if indexStates != 0 {
		t.Fatalf(
			"failed publication persisted %d index rebuilds",
			indexStates,
		)
	}
}

func TestKnowledgeManagedViewOrdersByLatestDraftActivityWithoutTouchingArticle(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		601,
		models.ProjectRoleAgent,
	)
	first, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "older-article-newer-draft",
			Title:    "旧文章的新草稿",
			Markdown: "# first\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "newer-article-older-draft",
			Title:    "新文章的旧草稿",
			Markdown: "# second\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstArticleTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	secondArticleTime := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	firstDraftTime := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	secondDraftTime := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	for _, update := range []struct {
		model any
		id    string
		field string
		value time.Time
	}{
		{
			model: &models.KnowledgeArticle{},
			id:    first.Article.ID, field: "updated_at", value: firstArticleTime,
		},
		{
			model: &models.KnowledgeArticle{},
			id:    second.Article.ID, field: "updated_at", value: secondArticleTime,
		},
		{
			model: &models.KnowledgeArticleVersion{},
			id:    first.Version.ID, field: "created_at", value: firstDraftTime,
		},
		{
			model: &models.KnowledgeArticleVersion{},
			id:    second.Version.ID, field: "created_at", value: secondDraftTime,
		},
	} {
		if err := fixture.db.Model(update.model).
			Where("id = ?", update.id).
			UpdateColumn(update.field, update.value).Error; err != nil {
			t.Fatal(err)
		}
	}
	page, err := fixture.service.ListArticles(
		contributorContext,
		KnowledgeArticleListFilter{ManagedByActor: true},
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "updated_at", SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 ||
		page.Items[0].ID != first.Article.ID ||
		page.Items[1].ID != second.Article.ID {
		t.Fatalf("managed knowledge activity order = %+v", page.Items)
	}
	if !page.Items[0].HasUnpublishedDraft ||
		page.Items[0].LatestDraftAt == nil ||
		!page.Items[0].LatestDraftAt.Equal(firstDraftTime) ||
		page.Items[0].LatestDraftVersion == nil ||
		*page.Items[0].LatestDraftVersion != 1 {
		t.Fatalf(
			"latest draft activity projection = %+v",
			page.Items[0],
		)
	}
	var persistedFirst models.KnowledgeArticle
	if err := fixture.db.First(
		&persistedFirst,
		"id = ?",
		first.Article.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if !persistedFirst.UpdatedAt.Equal(firstArticleTime) {
		t.Fatalf(
			"managed ordering leaked draft activity into article updated_at: %s",
			persistedFirst.UpdatedAt,
		)
	}
}

func TestKnowledgeContributorGrantIsRevalidatedAfterObjectUpload(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		502,
		models.ProjectRoleAgent,
	)
	fixture.storage.afterPut = func() {
		if err := fixture.db.Model(&models.ProjectMembership{}).
			Where(
				"project_id = ? AND user_id = ?",
				fixture.scope.ProjectID,
				uint(502),
			).
			Updates(map[string]any{
				"knowledge_contributor": false,
				"version":               gorm.Expr("version + 1"),
			}).Error; err != nil {
			panic(err)
		}
	}
	_, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "revoked-during-upload",
			Title:    "上传期间撤销",
			Markdown: "# body\n",
		},
	)
	if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("revoked contributor error = %v", err)
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 0 ||
		len(fixture.storage.deleted) != 1 {
		t.Fatalf(
			"revoked contributor left object state objects=%v deleted=%v",
			fixture.storage.objects,
			fixture.storage.deleted,
		)
	}
	var articleCount int64
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Where("key = ?", "revoked-during-upload").
		Count(&articleCount).Error; err != nil {
		t.Fatal(err)
	}
	if articleCount != 0 {
		t.Fatalf("revoked contributor persisted %d articles", articleCount)
	}
}

func TestKnowledgeContributorSourceMustRemainHumanVisible(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		503,
		models.ProjectRoleRequester,
	)
	ticket, attachment := fixture.source(t)
	_, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:            "unrelated-ticket-source",
			Title:          "不相关工单",
			Markdown:       "# body\n",
			SourceTicketID: ticket.ID,
		},
	)
	if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("unrelated requester source error = %v", err)
	}

	contributorID := uint(503)
	if err := fixture.db.Model(&models.Ticket{}).
		Where("id = ?", ticket.ID).
		Update("created_by_id", contributorID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:                 "internal-attachment-source",
			Title:               "内部附件",
			Markdown:            "# body\n",
			SourceTicketID:      ticket.ID,
			SourceAttachmentIDs: []uint{attachment.ID},
		},
	)
	if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("requester internal attachment source error = %v", err)
	}
	if err := fixture.db.Model(&models.TicketAttachment{}).
		Where("id = ?", attachment.ID).
		Update("is_public", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:                 "public-attachment-source",
			Title:               "公开附件",
			Markdown:            "# body\n",
			SourceTicketID:      ticket.ID,
			SourceAttachmentIDs: []uint{attachment.ID},
		},
	); err != nil {
		t.Fatalf("requester visible attachment source: %v", err)
	}
}

func TestKnowledgeSourceViewsRevalidateHumanTicketAndAttachmentVisibility(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	ticket, attachment := fixture.source(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                 "source-human-visibility",
			Title:               "来源权限",
			Markdown:            "# 处理方法\n",
			GrantProjectAccess:  true,
			SourceTicketID:      ticket.ID,
			SourceAttachmentIDs: []uint{attachment.ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PublishVersion(
		fixture.ctx,
		created.Version.ID,
	); err != nil {
		t.Fatal(err)
	}

	type humanCase struct {
		name       string
		userID     uint
		role       models.ProjectRole
		visibility KnowledgeSourceVisibility
	}
	for _, testCase := range []humanCase{
		{
			name:       "project admin",
			userID:     101,
			role:       models.ProjectRoleAdmin,
			visibility: KnowledgeSourceFull,
		},
		{
			name:       "manager",
			userID:     102,
			role:       models.ProjectRoleManager,
			visibility: KnowledgeSourceFull,
		},
		{
			name:       "human agent",
			userID:     103,
			role:       models.ProjectRoleAgent,
			visibility: KnowledgeSourceFull,
		},
		{
			name:       "observer",
			userID:     104,
			role:       models.ProjectRoleObserver,
			visibility: KnowledgeSourceRestricted,
		},
		{
			name:       "unrelated requester",
			userID:     105,
			role:       models.ProjectRoleRequester,
			visibility: KnowledgeSourceRestricted,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := fixture.humanContext(
				t,
				testCase.userID,
				testCase.role,
			)
			document, getErr := fixture.service.GetArticleDocument(
				ctx,
				created.Article.ID,
				GetArticleDocumentInput{},
			)
			if getErr != nil {
				t.Fatalf("get document: %v", getErr)
			}
			assertKnowledgeSourceVisibility(
				t,
				document.Sources,
				testCase.visibility,
				ticket,
				attachment,
			)
		})
	}

	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.scope.ProjectID,
			uint(42),
		).
		Updates(map[string]any{
			"role":    models.ProjectRoleRequester,
			"version": gorm.Expr("version + 1"),
		}).Error; err != nil {
		t.Fatal(err)
	}
	ownerContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.scope,
			Actor:  models.HumanActor(42),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	internal, err := fixture.service.GetArticleDocument(
		ownerContext,
		created.Article.ID,
		GetArticleDocumentInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		internal.Sources,
		KnowledgeSourceRestricted,
		ticket,
		attachment,
	)

	if err := fixture.db.Model(&models.TicketAttachment{}).
		Where("id = ?", attachment.ID).
		Update("is_public", true).Error; err != nil {
		t.Fatal(err)
	}
	public, err := fixture.service.GetArticleDocument(
		ownerContext,
		created.Article.ID,
		GetArticleDocumentInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		public.Sources,
		KnowledgeSourceFull,
		ticket,
		attachment,
	)

	if err := fixture.db.Delete(
		&models.TicketAttachment{},
		attachment.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	unavailable, err := fixture.service.GetArticleDocument(
		ownerContext,
		created.Article.ID,
		GetArticleDocumentInput{},
	)
	if err != nil {
		t.Fatalf("deleted source broke article read: %v", err)
	}
	assertKnowledgeSourceVisibility(
		t,
		unavailable.Sources,
		KnowledgeSourceUnavailable,
		ticket,
		attachment,
	)
}

func TestKnowledgeSourceViewsRequireLivePrincipalScopesGrantAndDecisions(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	ticket, attachment := fixture.source(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                 "source-principal-visibility",
			Title:               "Agent 来源权限",
			Markdown:            "# 处理方法\n",
			GrantProjectAccess:  true,
			SourceTicketID:      ticket.ID,
			SourceAttachmentIDs: []uint{attachment.ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PublishVersion(
		fixture.ctx,
		created.Version.ID,
	); err != nil {
		t.Fatal(err)
	}

	principal := fixture.principal(
		t,
		"source-reader",
		knowledgeReadScope,
		models.ScopeTicketsRead,
		models.ScopeAttachmentsRead,
	)
	knowledgeDecision := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.article.read",
		"knowledge_article",
		created.Article.ID,
		false,
		1,
	)
	ticketDecision := fixture.policyDecision(
		t,
		principal,
		models.ScopeTicketsRead,
		"ticket.read",
		"ticket",
		strconv.FormatUint(uint64(ticket.ID), 10),
		false,
		1,
	)
	attachmentDecision := fixture.policyDecision(
		t,
		principal,
		models.ScopeAttachmentsRead,
		"ticket.attachment.read",
		"ticket",
		strconv.FormatUint(uint64(ticket.ID), 10),
		false,
		1,
	)

	restricted, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			PolicyDecisionID: knowledgeDecision.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		restricted.Sources,
		KnowledgeSourceRestricted,
		ticket,
		attachment,
	)

	ticketOnly, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			PolicyDecisionID: knowledgeDecision.ID,
			SourceAuthorizations: []KnowledgeSourceAuthorization{{
				SourceTicketID:         ticket.ID,
				TicketPolicyDecisionID: ticketDecision.ID,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		ticketOnly.Sources,
		KnowledgeSourceRestricted,
		ticket,
		attachment,
	)

	full, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			PolicyDecisionID: knowledgeDecision.ID,
			SourceAuthorizations: []KnowledgeSourceAuthorization{{
				SourceTicketID:             ticket.ID,
				TicketPolicyDecisionID:     ticketDecision.ID,
				AttachmentPolicyDecisionID: attachmentDecision.ID,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		full.Sources,
		KnowledgeSourceFull,
		ticket,
		attachment,
	)

	grantScopes, err := json.Marshal([]string{
		knowledgeReadScope,
		models.ScopeTicketsRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.scope.ProjectID,
			principal.principal.ID,
		).
		Update("scopes", datatypes.JSON(grantScopes)).Error; err != nil {
		t.Fatal(err)
	}
	revokedAttachment, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			PolicyDecisionID: knowledgeDecision.ID,
			SourceAuthorizations: []KnowledgeSourceAuthorization{{
				SourceTicketID:             ticket.ID,
				TicketPolicyDecisionID:     ticketDecision.ID,
				AttachmentPolicyDecisionID: attachmentDecision.ID,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		revokedAttachment.Sources,
		KnowledgeSourceRestricted,
		ticket,
		attachment,
	)

	restoredGrantScopes, err := json.Marshal([]string{
		knowledgeReadScope,
		models.ScopeTicketsRead,
		models.ScopeAttachmentsRead,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.scope.ProjectID,
			principal.principal.ID,
		).
		Update("scopes", datatypes.JSON(restoredGrantScopes)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.ServicePrincipal{}).
		Where("id = ?", principal.principal.ID).
		Update("policy_epoch", 2).Error; err != nil {
		t.Fatal(err)
	}
	currentKnowledgeDecision := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.article.read",
		"knowledge_article",
		created.Article.ID,
		false,
		2,
	)
	staleSourceDecisions, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			PolicyDecisionID: currentKnowledgeDecision.ID,
			SourceAuthorizations: []KnowledgeSourceAuthorization{{
				SourceTicketID:             ticket.ID,
				TicketPolicyDecisionID:     ticketDecision.ID,
				AttachmentPolicyDecisionID: attachmentDecision.ID,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertKnowledgeSourceVisibility(
		t,
		staleSourceDecisions.Sources,
		KnowledgeSourceRestricted,
		ticket,
		attachment,
	)
}

func TestCreateAuthoredVersionUsesNextNumberAndVersionReceipt(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	first, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "versioned-runbook",
			Title:    "值班手册",
			Markdown: "# v1\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateAuthoredVersion(
		fixture.ctx,
		first.Article.ID,
		CreateAuthoredVersionInput{
			Title:    "值班手册 v2",
			Markdown: "# v2\n\n更新后的步骤。\n",
		},
	)
	if err != nil {
		t.Fatalf("create authored version: %v", err)
	}
	if second.Version.Version != 2 ||
		second.Receipt.ResourceID != second.Version.ID ||
		second.Article.ID != first.Article.ID ||
		second.Document == nil ||
		second.Document.Markdown != "# v2\n\n更新后的步骤。\n" {
		t.Fatalf("unexpected second authored version: %+v", second)
	}
}

func TestAuthoredMethodsFailClosedWithoutManagedStorage(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	service, err := NewKnowledgeService(
		fixture.db,
		KnowledgeServiceDependencies{
			ProjectAuthorization: mustProjectServiceForAuthoredTest(
				t,
				fixture.db,
			),
			Events: NewAgentNativeService(fixture.db),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "missing-storage",
			Title:    "缺少存储",
			Markdown: "# body\n",
		},
	); err == nil || !strings.Contains(err.Error(), "storage pipeline") {
		t.Fatalf("missing storage error = %v", err)
	}
}

func TestCreateAuthoredArticleRejectsMoreThanOneHundredSectionsBeforePut(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	var markdown strings.Builder
	for index := 0; index <= MaxAuthoredSections; index++ {
		fmt.Fprintf(&markdown, "# Section %d\nbody\n", index)
	}
	_, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "too-many-sections",
			Title:    "过多章节",
			Markdown: markdown.String(),
		},
	)
	if err == nil || !strings.Contains(err.Error(), "100 sections") {
		t.Fatalf("section limit error = %v", err)
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 0 {
		t.Fatalf("invalid Markdown reached storage: %v", fixture.storage.objects)
	}
}

func TestCreateAuthoredArticleRevalidatesSourceTicketAndAttachment(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, authoredKnowledgeFixture, models.Ticket, models.TicketAttachment)
	}{
		{
			name: "attachment is not clean",
			mutate: func(
				t *testing.T,
				fixture authoredKnowledgeFixture,
				_ models.Ticket,
				attachment models.TicketAttachment,
			) {
				if err := fixture.db.Model(&models.TicketAttachment{}).
					Where("id = ?", attachment.ID).
					UpdateColumn("virus_scan", models.VirusScanPending).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "attachment hash is absent",
			mutate: func(
				t *testing.T,
				fixture authoredKnowledgeFixture,
				_ models.Ticket,
				attachment models.TicketAttachment,
			) {
				if err := fixture.db.Model(&models.TicketAttachment{}).
					Where("id = ?", attachment.ID).
					UpdateColumn("hash", "").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "attachment belongs to another ticket",
			mutate: func(
				t *testing.T,
				fixture authoredKnowledgeFixture,
				_ models.Ticket,
				attachment models.TicketAttachment,
			) {
				other := queryTestTicket(
					fixture.scope.OrganizationID,
					fixture.scope.ProjectID,
					"OPS-8",
					models.HumanActor(42),
				)
				other.QueueID = fixture.scope.ProjectID
				if err := fixture.db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
				if err := fixture.db.Model(&models.TicketAttachment{}).
					Where("id = ?", attachment.ID).
					UpdateColumn("ticket_id", other.ID).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "ticket moved outside project scope",
			mutate: func(
				t *testing.T,
				fixture authoredKnowledgeFixture,
				ticket models.Ticket,
				_ models.TicketAttachment,
			) {
				if err := fixture.db.Model(&models.Ticket{}).
					Where("id = ?", ticket.ID).
					UpdateColumn("project_id", fixture.scope.ProjectID+1).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAuthoredKnowledgeFixture(t)
			ticket, attachment := fixture.source(t)
			test.mutate(t, fixture, ticket, attachment)
			_, err := fixture.service.CreateAuthoredArticle(
				fixture.ctx,
				CreateAuthoredArticleInput{
					Key:                 "invalid-source",
					Title:               "Invalid source",
					Markdown:            "# body\n",
					SourceTicketID:      ticket.ID,
					SourceAttachmentIDs: []uint{attachment.ID},
				},
			)
			if err == nil {
				t.Fatal("invalid source unexpectedly succeeded")
			}
			fixture.storage.mu.Lock()
			defer fixture.storage.mu.Unlock()
			if len(fixture.storage.objects) != 0 {
				t.Fatalf(
					"invalid source left an object: %v",
					fixture.storage.objects,
				)
			}
		})
	}
}

func TestAuthoredMarkdownSectionsIgnoreHeadingsInsideFences(t *testing.T) {
	markdown := "# 真实章节\n\n```markdown\n# 代码里的井号\n```\n\n## 子章节\n正文\n"
	sections := parseAuthoredMarkdownSections(markdown)
	if len(sections) != 2 ||
		sections[0].Heading != "真实章节" ||
		sections[1].Heading != "子章节" ||
		!strings.Contains(sections[0].Markdown, "# 代码里的井号") {
		t.Fatalf("fenced heading split sections: %+v", sections)
	}
}

func TestGetArticleDocumentRoutesHistoricalStorageProvider(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	local := &authoredNamedMemoryStorage{
		authoredMemoryStorage: newAuthoredMemoryStorage(),
		storageType:           "local",
	}
	fixture.service.storage = local
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "historical-storage",
			Title:    "历史存储",
			Markdown: "# local object\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version.ObjectProvider != "local" {
		t.Fatalf("object provider = %q", created.Version.ObjectProvider)
	}
	s3 := &authoredNamedMemoryStorage{
		authoredMemoryStorage: newAuthoredMemoryStorage(),
		storageType:           "s3",
	}
	router, err := NewAttachmentStorageRouterWithRegistry(
		"s3-default",
		[]AttachmentStorageRegistration{
			{
				StoreID:     "s3-default",
				StorageType: "s3",
				Storage:     s3,
			},
			{
				StoreID:     "local-default",
				StorageType: "local",
				Storage:     local,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.storage = router
	document, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		created.Article.ID,
		GetArticleDocumentInput{VersionID: created.Version.ID},
	)
	if err != nil {
		t.Fatalf("read historical local document through S3 primary: %v", err)
	}
	if document.Markdown != "# local object\n" {
		t.Fatalf("historical document = %q", document.Markdown)
	}
}

func TestAuthoredKnowledgePersistsAndReadsExactStoreGeneration(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	oldStore := &authoredVersionedMemoryStorage{
		authoredNamedMemoryStorage: &authoredNamedMemoryStorage{
			authoredMemoryStorage: newAuthoredMemoryStorage(),
			storageType:           "s3",
			storeID:               "s3-2025",
		},
		versionID: "version-2025",
	}
	fixture.service.storage = oldStore
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "store-generation",
			Title:    "存储代际",
			Markdown: "# exact generation\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version.ObjectProvider != "s3" ||
		created.Version.ObjectStoreID != "s3-2025" ||
		created.Version.ObjectVersionID != "version-2025" {
		t.Fatalf(
			"authored storage identity = %+v",
			created.Version,
		)
	}
	newStore := &authoredVersionedMemoryStorage{
		authoredNamedMemoryStorage: &authoredNamedMemoryStorage{
			authoredMemoryStorage: newAuthoredMemoryStorage(),
			storageType:           "s3",
			storeID:               "s3-2026",
		},
		versionID: "version-2026",
	}
	router, err := NewAttachmentStorageRouterWithRegistry(
		"s3-2026",
		[]AttachmentStorageRegistration{
			{
				StoreID:     "s3-2026",
				StorageType: "s3",
				Storage:     newStore,
			},
			{
				StoreID:     "s3-2025",
				StorageType: "s3",
				Storage:     oldStore,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.storage = router
	document, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		created.Article.ID,
		GetArticleDocumentInput{
			VersionID: created.Version.ID,
		},
	)
	if err != nil {
		t.Fatalf("read exact historical generation: %v", err)
	}
	if document.Markdown != "# exact generation\n" ||
		len(oldStore.openedVersions) != 1 ||
		oldStore.openedVersions[0] != "version-2025" ||
		len(newStore.openedVersions) != 0 {
		t.Fatalf(
			"generation routing old=%v new=%v document=%q",
			oldStore.openedVersions,
			newStore.openedVersions,
			document.Markdown,
		)
	}
	if err := fixture.db.Model(
		&models.KnowledgeArticleVersion{},
	).Where("id = ?", created.Version.ID).
		UpdateColumn("object_store_id", "").Error; err != nil {
		t.Fatal(err)
	}
	legacyRouter, err := NewAttachmentStorageRouterWithRegistry(
		"s3-2025",
		[]AttachmentStorageRegistration{{
			StoreID:     "s3-2025",
			StorageType: "s3",
			Storage:     oldStore,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	oldStore.openedVersions = nil
	fixture.service.storage = legacyRouter
	if _, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		created.Article.ID,
		GetArticleDocumentInput{VersionID: created.Version.ID},
	); err != nil {
		t.Fatalf("read unique legacy provider: %v", err)
	}
	if len(oldStore.openedVersions) != 1 ||
		oldStore.openedVersions[0] != "version-2025" {
		t.Fatalf(
			"legacy provider did not use exact version: %v",
			oldStore.openedVersions,
		)
	}
}

func TestLegacyAuthoredKnowledgeFailsClosedAcrossAmbiguousS3Stores(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	oldStore := &authoredNamedMemoryStorage{
		authoredMemoryStorage: newAuthoredMemoryStorage(),
		storageType:           "s3",
		storeID:               "s3-2025",
	}
	fixture.service.storage = oldStore
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "legacy-ambiguous",
			Title:    "旧引用",
			Markdown: "# legacy\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(
		&models.KnowledgeArticleVersion{},
	).Where("id = ?", created.Version.ID).
		UpdateColumn("object_store_id", "").Error; err != nil {
		t.Fatal(err)
	}
	newStore := &authoredNamedMemoryStorage{
		authoredMemoryStorage: newAuthoredMemoryStorage(),
		storageType:           "s3",
		storeID:               "s3-2026",
	}
	router, err := NewAttachmentStorageRouterWithRegistry(
		"s3-2026",
		[]AttachmentStorageRegistration{
			{
				StoreID:     "s3-2026",
				StorageType: "s3",
				Storage:     newStore,
			},
			{
				StoreID:     "s3-2025",
				StorageType: "s3",
				Storage:     oldStore,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.storage = router
	if _, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		created.Article.ID,
		GetArticleDocumentInput{VersionID: created.Version.ID},
	); !errors.Is(err, ErrAttachmentStorageMissing) {
		t.Fatalf("ambiguous legacy object error = %v", err)
	}
}

func TestAuthoredKnowledgeRevocationCleansExactHistoricalVersion(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		504,
		models.ProjectRoleAgent,
	)
	oldStore := &authoredVersionedMemoryStorage{
		authoredNamedMemoryStorage: &authoredNamedMemoryStorage{
			authoredMemoryStorage: newAuthoredMemoryStorage(),
			storageType:           "s3",
			storeID:               "s3-2025",
		},
		versionID: "version-revoked",
	}
	newStore := &authoredVersionedMemoryStorage{
		authoredNamedMemoryStorage: &authoredNamedMemoryStorage{
			authoredMemoryStorage: newAuthoredMemoryStorage(),
			storageType:           "s3",
			storeID:               "s3-2026",
		},
		versionID: "version-new",
	}
	oldRouter, err := NewAttachmentStorageRouterWithRegistry(
		"s3-2025",
		[]AttachmentStorageRegistration{{
			StoreID:     "s3-2025",
			StorageType: "s3",
			Storage:     oldStore,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	rotatedRouter, err := NewAttachmentStorageRouterWithRegistry(
		"s3-2026",
		[]AttachmentStorageRegistration{
			{
				StoreID:     "s3-2026",
				StorageType: "s3",
				Storage:     newStore,
			},
			{
				StoreID:     "s3-2025",
				StorageType: "s3",
				Storage:     oldStore,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.storage = oldRouter
	oldStore.afterPut = func() {
		fixture.service.storage = rotatedRouter
		if err := fixture.db.Model(&models.ProjectMembership{}).
			Where(
				"project_id = ? AND user_id = ?",
				fixture.scope.ProjectID,
				uint(504),
			).
			Updates(map[string]any{
				"knowledge_contributor": false,
				"version":               gorm.Expr("version + 1"),
			}).Error; err != nil {
			panic(err)
		}
	}
	_, err = fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "revoked-generation",
			Title:    "撤销后清理",
			Markdown: "# cleanup\n",
		},
	)
	if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("revoked write error = %v", err)
	}
	if len(oldStore.deletedVersions) != 1 ||
		oldStore.deletedVersions[0] != "version-revoked" ||
		len(newStore.deletedVersions) != 0 ||
		len(oldStore.objects) != 0 {
		t.Fatalf(
			"exact cleanup old=%v new=%v objects=%v",
			oldStore.deletedVersions,
			newStore.deletedVersions,
			oldStore.objects,
		)
	}
}

func TestGetArticleDocumentRejectsProviderMismatchWithoutRouter(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "provider-mismatch",
			Title:    "Provider mismatch",
			Markdown: "# body\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.KnowledgeArticleVersion{}).
		Where("id = ?", created.Version.ID).
		UpdateColumn("object_provider", "local").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.GetArticleDocument(
		fixture.ctx,
		created.Article.ID,
		GetArticleDocumentInput{VersionID: created.Version.ID},
	); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("provider mismatch error = %v", err)
	}
}

func TestPublishVersionRejectsServicePrincipalBeforeMutation(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	principalContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        fixture.scope,
			Actor:        models.ServicePrincipalActor(newNativeID()),
			Source:       SourceProtocolAgentREST,
			CredentialID: newNativeID(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PublishVersion(
		principalContext,
		newNativeID(),
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("service principal publish error = %v", err)
	}
}

func TestCreateAuthoredArticleRejectsServicePrincipalProjectWideACL(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	principal := fixture.principal(
		t,
		"draft-only-writer",
		knowledgeWriteScope,
	)
	decision := fixture.policyDecision(
		t,
		principal,
		knowledgeWriteScope,
		"knowledge.article.draft.create",
		"knowledge_article",
		"*",
		true,
		1,
	)
	if _, err := fixture.service.CreateAuthoredArticle(
		principal.context,
		CreateAuthoredArticleInput{
			Key:                "agent-project-acl",
			Title:              "Agent cannot grant project ACL",
			Markdown:           "# draft\n",
			GrantProjectAccess: true,
			PolicyDecisionID:   decision.ID,
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("service principal project ACL error = %v", err)
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 0 {
		t.Fatalf(
			"denied Agent ACL request uploaded objects: %v",
			fixture.storage.objects,
		)
	}
}

func TestCreateAuthoredVersionRequiresServicePrincipalManageACL(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	article, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "human-owned",
			Title:    "Human owned",
			Markdown: "# body\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	principalID := newNativeID()
	credentialID := newNativeID()
	if err := fixture.db.Create(&models.ServicePrincipal{
		ID:          principalID,
		Name:        "knowledge-writer",
		Status:      models.ServicePrincipalStatusActive,
		Scopes:      datatypes.JSON([]byte(`["knowledge:write"]`)),
		PolicyEpoch: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          fixture.scope.ProjectID,
		ServicePrincipalID: principalID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON([]byte(`["knowledge:write"]`)),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.AgentCredential{
		ID:                 credentialID,
		ServicePrincipalID: principalID,
		Name:               "knowledge writer",
		SecretHash:         strings.Repeat("c", 64),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	decisionID := newNativeID()
	if err := fixture.db.Create(&models.PolicyDecision{
		ID:                 decisionID,
		OrganizationID:     fixture.scope.OrganizationID,
		ProjectID:          fixture.scope.ProjectID,
		ServicePrincipalID: principalID,
		CredentialID:       credentialID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            principalID,
		Scope:              knowledgeWriteScope,
		Action:             "knowledge.article.draft.create",
		ResourceType:       "knowledge_article",
		ResourceID:         article.Article.ID,
		IsWrite:            true,
		Allowed:            true,
		ReasonCode:         "allow",
		PolicyEpoch:        1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	principalContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        fixture.scope,
			Actor:        models.ServicePrincipalActor(principalID),
			Source:       SourceProtocolAgentREST,
			CredentialID: credentialID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CreateAuthoredVersion(
		principalContext,
		article.Article.ID,
		CreateAuthoredVersionInput{
			Title:            "unauthorized version",
			Markdown:         "# must not upload\n",
			PolicyDecisionID: decisionID,
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("foreign article manage error = %v", err)
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 1 {
		t.Fatalf("denied principal uploaded an object: %v", fixture.storage.objects)
	}
}

func TestCreateAuthoredArticleRejectsSourcePolicyEpochChangeAfterUpload(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	ticket, attachment := fixture.source(t)
	principal := fixture.principal(
		t,
		"source-epoch-writer",
		knowledgeWriteScope,
		models.ScopeTicketsRead,
		models.ScopeAttachmentsRead,
	)
	writeDecision := fixture.policyDecision(
		t,
		principal,
		knowledgeWriteScope,
		"knowledge.article.draft.create",
		"knowledge_article",
		"*",
		true,
		1,
	)
	ticketDecision := fixture.policyDecision(
		t,
		principal,
		models.ScopeTicketsRead,
		"ticket.read",
		"ticket",
		fmt.Sprint(ticket.ID),
		false,
		1,
	)
	attachmentDecision := fixture.policyDecision(
		t,
		principal,
		models.ScopeAttachmentsRead,
		"ticket.attachment.read",
		"ticket",
		fmt.Sprint(ticket.ID),
		false,
		1,
	)
	fixture.storage.afterPut = func() {
		if err := fixture.db.Model(&models.ServicePrincipal{}).
			Where("id = ?", principal.principal.ID).
			UpdateColumn("policy_epoch", 2).Error; err != nil {
			panic(err)
		}
	}
	_, err := fixture.service.CreateAuthoredArticle(
		principal.context,
		CreateAuthoredArticleInput{
			Key:                              "source-epoch",
			Title:                            "Source epoch",
			Markdown:                         "# body\n",
			SourceTicketID:                   ticket.ID,
			SourceAttachmentIDs:              []uint{attachment.ID},
			PolicyDecisionID:                 writeDecision.ID,
			SourceTicketPolicyDecisionID:     ticketDecision.ID,
			SourceAttachmentPolicyDecisionID: attachmentDecision.ID,
		},
	)
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("policy epoch change error = %v", err)
	}
	fixture.storage.mu.Lock()
	defer fixture.storage.mu.Unlock()
	if len(fixture.storage.objects) != 0 {
		t.Fatalf("policy epoch failure left object: %v", fixture.storage.objects)
	}
	var count int64
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Where("key = ?", "source-epoch").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("policy epoch failure persisted %d articles", count)
	}
}

func TestKnowledgeArticleListRequiresPublishedACLOrHumanManageView(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	publicDraft, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                "public-draft",
			Title:              "公开草稿",
			Markdown:           "# public\n",
			GrantProjectAccess: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	privatePublished, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "private-published",
			Title:    "私有已发布",
			Markdown: "# private\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publicPublished, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                "public-published",
			Title:              "公开已发布",
			Markdown:           "# published\n",
			GrantProjectAccess: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*AuthoredKnowledgeResult{
		privatePublished,
		publicPublished,
	} {
		if err := fixture.db.Model(&models.KnowledgeArticleVersion{}).
			Where("id = ?", result.Version.ID).
			UpdateColumns(map[string]any{
				"status": models.KnowledgeVersionPublished,
			}).Error; err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.Model(&models.KnowledgeArticle{}).
			Where("id = ?", result.Article.ID).
			UpdateColumn("current_version_id", result.Version.ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	reader := models.User{
		ID:           43,
		Username:     "knowledge-reader",
		Email:        "knowledge-reader@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := fixture.db.Create(&reader).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ProjectMembership{
		ProjectID: fixture.scope.ProjectID,
		UserID:    reader.ID,
		Role:      models.ProjectRoleObserver,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	readerContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.scope,
			Actor:  models.HumanActor(reader.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	page, err := fixture.service.ListArticles(
		readerContext,
		KnowledgeArticleListFilter{},
		DirectoryPageRequest{
			Page: 1, PageSize: 20, SortBy: "updated_at", SortOrder: "desc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 ||
		len(page.Items) != 1 ||
		page.Items[0].ID != publicPublished.Article.ID {
		t.Fatalf(
			"reader saw draft/private article: total=%d items=%+v draft=%s",
			page.Total,
			page.Items,
			publicDraft.Article.ID,
		)
	}
	if _, err := fixture.service.ListArticles(
		readerContext,
		KnowledgeArticleListFilter{ManageAll: true},
		DirectoryPageRequest{
			Page: 1, PageSize: 20, SortBy: "updated_at", SortOrder: "desc",
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("observer manage view error = %v", err)
	}
}

func TestMachineKnowledgeReadsRequireExactLivePolicyDecision(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:                "machine-readable",
			Title:              "Machine readable",
			Markdown:           "# body\n",
			GrantProjectAccess: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.KnowledgeArticleVersion{}).
		Where("id = ?", created.Version.ID).
		UpdateColumn("status", models.KnowledgeVersionPublished).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.KnowledgeArticle{}).
		Where("id = ?", created.Article.ID).
		UpdateColumn("current_version_id", created.Version.ID).Error; err != nil {
		t.Fatal(err)
	}
	principal := fixture.principal(
		t,
		"knowledge-reader-policy",
		knowledgeReadScope,
	)
	wrongList := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.article.read",
		"knowledge_article",
		"*",
		false,
		1,
	)
	listDecision := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.article.list",
		"knowledge_article",
		"*",
		false,
		1,
	)
	readDecision := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.article.read",
		"knowledge_article",
		created.Article.ID,
		false,
		1,
	)
	request := DirectoryPageRequest{
		Page: 1, PageSize: 20, SortBy: "updated_at", SortOrder: "desc",
	}
	for name, decisionID := range map[string]string{
		"missing":    "",
		"mismatched": wrongList.ID,
	} {
		t.Run("list "+name, func(t *testing.T) {
			_, err := fixture.service.ListArticles(
				principal.context,
				KnowledgeArticleListFilter{
					PolicyDecisionID: decisionID,
				},
				request,
			)
			if !errors.Is(err, ErrPolicyDenied) {
				t.Fatalf("list %s decision error = %v", name, err)
			}
		})
	}
	page, err := fixture.service.ListArticles(
		principal.context,
		KnowledgeArticleListFilter{PolicyDecisionID: listDecision.ID},
		request,
	)
	if err != nil || page.Total != 1 {
		t.Fatalf("authorized machine list page=%+v error=%v", page, err)
	}
	if _, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{VersionID: created.Version.ID},
	); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("missing document decision error = %v", err)
	}
	document, err := fixture.service.GetArticleDocument(
		principal.context,
		created.Article.ID,
		GetArticleDocumentInput{
			VersionID:        created.Version.ID,
			PolicyDecisionID: readDecision.ID,
		},
	)
	if err != nil || document.Markdown != "# body\n" {
		t.Fatalf("authorized document=%+v error=%v", document, err)
	}
	fixture.service.searchIndex = &knowledgeServiceTestIndex{}
	if _, err := fixture.service.Search(
		principal.context,
		KnowledgeSearchInput{Query: "body"},
	); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("missing search decision error = %v", err)
	}
	if err := fixture.db.Model(&models.ServicePrincipal{}).
		Where("id = ?", principal.principal.ID).
		UpdateColumn("policy_epoch", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ListArticles(
		principal.context,
		KnowledgeArticleListFilter{PolicyDecisionID: listDecision.ID},
		request,
	); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("stale list decision error = %v", err)
	}
}

func TestMachineKnowledgeSearchFinalizationRevalidatesPrincipalEpoch(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	fixture.service.modelProviders = map[string]ModelProvider{
		"approved-external": &knowledgeServiceTestProvider{
			descriptor: ModelProviderDescriptor{
				Key:        "approved-external",
				IsExternal: false,
			},
		},
	}
	setKnowledgeServiceTestPolicy(
		t,
		fixture.service,
		fixture.ctx,
		models.ModelDataEgressDenied,
	)
	principal := fixture.principal(
		t,
		"search-epoch-reader",
		knowledgeReadScope,
	)
	decision := fixture.policyDecision(
		t,
		principal,
		knowledgeReadScope,
		"knowledge.search",
		"knowledge",
		"*",
		false,
		1,
	)
	operation, err := OperationContextFromContext(principal.context)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.service.captureKnowledgeSearchSnapshot(
		principal.context,
		operation,
		decision.ID,
	)
	if err != nil {
		t.Fatalf("capture principal search snapshot: %v", err)
	}
	if snapshot.epoch.ActorType != models.ActorTypeServicePrincipal ||
		snapshot.epoch.PrincipalID != principal.principal.ID ||
		snapshot.epoch.CredentialID != principal.credential.ID {
		t.Fatalf("principal epoch is incomplete: %+v", snapshot.epoch)
	}
	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.scope.ProjectID,
			principal.principal.ID,
		).
		UpdateColumn("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.finalizeKnowledgeSearch(
		principal.context,
		operation,
		snapshot.epoch,
		decision.ID,
		nil,
		nil,
	); err == nil {
		t.Fatal("revoked principal search snapshot finalized")
	}
}

func TestKnowledgeManageListAndPublishReuseBoundProjectTransaction(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	result, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "nested-transaction",
			Title:    "事务复用",
			Markdown: "# body\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	err = scopeddb.WithProjectScopeContextTransaction(
		fixture.ctx,
		fixture.db,
		fixture.scope,
		func(scopedContext context.Context) error {
			page, listErr := fixture.service.ListArticles(
				scopedContext,
				KnowledgeArticleListFilter{ManageAll: true},
				DirectoryPageRequest{
					Page:      1,
					PageSize:  20,
					SortBy:    "updated_at",
					SortOrder: "desc",
				},
			)
			if listErr != nil {
				return listErr
			}
			if page.Total != 1 {
				return fmt.Errorf("manage list total = %d", page.Total)
			}
			_, publishErr := fixture.service.PublishVersion(
				scopedContext,
				result.Version.ID,
			)
			return publishErr
		},
	)
	if err != nil {
		t.Fatalf("reuse scoped transaction: %v", err)
	}
}

func TestKnowledgeEventWritesUseActualAuditLedgerTransaction(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	ledger, err := NewAuditLedgerService(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(
		fixture.db,
		AgentNativeOptions{AuditLedger: ledger},
	)
	fixture.service.events = native
	fixture.service.idempotency = native
	fixture.service.searchIndex = &knowledgeServiceTestIndex{}
	fixture.service.modelProviders = map[string]ModelProvider{
		"approved-local": &knowledgeServiceTestProvider{
			descriptor: ModelProviderDescriptor{
				Key:        "approved-local",
				IsExternal: false,
			},
		},
	}

	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "audited-knowledge-write",
			Title:    "审计事务知识写入",
			Markdown: "# 初始版本\n",
		},
	)
	if err != nil {
		t.Fatalf("create audited authored article: %v", err)
	}
	revised, err := fixture.service.CreateAuthoredVersion(
		fixture.ctx,
		created.Article.ID,
		CreateAuthoredVersionInput{
			Title:    "审计事务知识写入第二版",
			Markdown: "# 第二版本\n",
		},
	)
	if err != nil {
		t.Fatalf("create audited authored version: %v", err)
	}
	if _, err := fixture.service.PublishVersion(
		fixture.ctx,
		revised.Version.ID,
	); err != nil {
		t.Fatalf("publish audited authored version: %v", err)
	}
	if _, err := fixture.service.SetProjectModelPolicy(
		fixture.ctx,
		ProjectModelPolicyInput{
			PolicyKey:         "knowledge",
			ProviderKey:       "approved-local",
			GenerateModel:     "generate-v1",
			EmbeddingModel:    "embed-v1",
			RerankModel:       "rerank-v1",
			DataEgress:        models.ModelDataEgressDenied,
			ProviderAllowlist: []string{"approved-local"},
			ModelAllowlist: []string{
				"generate-v1",
				"embed-v1",
				"rerank-v1",
			},
		},
	); err != nil {
		t.Fatalf("set audited model policy: %v", err)
	}
	if _, err := fixture.service.RebuildIndex(fixture.ctx); err != nil {
		t.Fatalf("request audited index rebuild: %v", err)
	}

	verification, err := ledger.Verify(fixture.ctx)
	if err != nil {
		t.Fatalf("verify knowledge audit ledger: %v", err)
	}
	if !verification.Valid || verification.HeadSequence != 6 {
		t.Fatalf(
			"knowledge audit ledger verification = %+v, want six valid entries",
			verification,
		)
	}
}

type authoredKnowledgeFixture struct {
	db      *gorm.DB
	scope   models.ProjectScope
	ctx     context.Context
	storage *authoredMemoryStorage
	service *KnowledgeService
}

func (fixture authoredKnowledgeFixture) humanContext(
	t *testing.T,
	userID uint,
	role models.ProjectRole,
) context.Context {
	t.Helper()
	user := models.User{
		ID:           userID,
		Username:     fmt.Sprintf("knowledge-source-user-%d", userID),
		Email:        fmt.Sprintf("knowledge-source-user-%d@example.test", userID),
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := fixture.db.Where("id = ?", userID).
		FirstOrCreate(&user).Error; err != nil {
		t.Fatal(err)
	}
	var membership models.ProjectMembership
	query := fixture.db.Where(
		"project_id = ? AND user_id = ?",
		fixture.scope.ProjectID,
		userID,
	).First(&membership)
	switch {
	case errors.Is(query.Error, gorm.ErrRecordNotFound):
		membership = models.ProjectMembership{
			ProjectID: fixture.scope.ProjectID,
			UserID:    userID,
			Role:      role,
			IsActive:  true,
			Version:   1,
		}
		if err := fixture.db.Create(&membership).Error; err != nil {
			t.Fatal(err)
		}
	case query.Error != nil:
		t.Fatal(query.Error)
	default:
		if err := fixture.db.Model(&membership).Updates(map[string]any{
			"role":      role,
			"is_active": true,
			"version":   gorm.Expr("version + 1"),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  fixture.scope,
			Actor:  models.HumanActor(userID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (fixture authoredKnowledgeFixture) contributorContext(
	t *testing.T,
	userID uint,
	role models.ProjectRole,
) context.Context {
	t.Helper()
	ctx := fixture.humanContext(t, userID, role)
	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.scope.ProjectID,
			userID,
		).
		Updates(map[string]any{
			"knowledge_contributor": true,
			"version":               gorm.Expr("version + 1"),
		}).Error; err != nil {
		t.Fatal(err)
	}
	return ctx
}

func assertKnowledgeSourceVisibility(
	t *testing.T,
	sources []KnowledgeSourceView,
	visibility KnowledgeSourceVisibility,
	ticket models.Ticket,
	attachment models.TicketAttachment,
) {
	t.Helper()
	if len(sources) != 1 {
		t.Fatalf("source count = %d, want 1: %+v", len(sources), sources)
	}
	source := sources[0]
	if source.Visibility != visibility ||
		source.Kind != KnowledgeSourceAttachment ||
		strings.TrimSpace(source.ReferenceLabel) == "" {
		t.Fatalf(
			"source projection = %+v, want visibility %q",
			source,
			visibility,
		)
	}
	if visibility == KnowledgeSourceFull {
		if source.SourceTicketID == nil ||
			*source.SourceTicketID != ticket.ID ||
			source.TicketNumber != ticket.TicketNumber ||
			source.TicketTitle != ticket.Title ||
			source.SourceAttachmentID == nil ||
			*source.SourceAttachmentID != attachment.ID ||
			source.AttachmentName != attachment.OriginalName ||
			source.AttachmentHash != attachment.Hash {
			t.Fatalf("full source projection is incomplete: %+v", source)
		}
		return
	}
	if source.SourceTicketID != nil ||
		source.SourceAttachmentID != nil ||
		source.TicketNumber != "" ||
		source.TicketTitle != "" ||
		source.AttachmentName != "" ||
		source.AttachmentHash != "" {
		t.Fatalf(
			"%s source leaked protected fields: %+v",
			visibility,
			source,
		)
	}
}

func newAuthoredKnowledgeFixture(t *testing.T) authoredKnowledgeFixture {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Team{},
		&models.TeamMembership{},
		&models.Queue{},
		&models.User{},
		&models.ProjectMembership{},
		&models.ServicePrincipal{},
		&models.ProjectPrincipalGrant{},
		&models.AgentCredential{},
		&models.PolicyDecision{},
		&models.Ticket{},
		&models.TicketAttachment{},
		&models.KnowledgeArticle{},
		&models.KnowledgeArticleVersion{},
		&models.KnowledgeObjectWriteIntent{},
		&models.KnowledgeArticleACL{},
		&models.KnowledgeSourceLink{},
		&models.KnowledgeIngestionTask{},
		&models.KnowledgeChunk{},
		&models.KnowledgeCitation{},
		&models.KnowledgeIndexState{},
		&models.ProjectModelPolicy{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	seedKnowledgeSearchAuthorization(t, db, scope)
	if err := db.Create(&models.Queue{
		ID:        scope.ProjectID,
		ProjectID: scope.ProjectID,
		Key:       "knowledge",
		Name:      "Knowledge",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := knowledgeServiceTestContext(t, scope)
	storage := newAuthoredMemoryStorage()
	resolver, err := NewProjectKnowledgeAccessResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db)
	service, err := NewKnowledgeService(
		db,
		KnowledgeServiceDependencies{
			AccessResolver:       resolver,
			ProjectAuthorization: mustProjectServiceForAuthoredTest(t, db),
			Events:               native,
			AttachmentStorage:    storage,
			StorageBucket:        "knowledge-private",
			IdempotencyCompleter: native,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return authoredKnowledgeFixture{
		db: db, scope: scope, ctx: ctx, storage: storage, service: service,
	}
}

func mustProjectServiceForAuthoredTest(
	t *testing.T,
	db *gorm.DB,
) *ProjectService {
	t.Helper()
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (fixture authoredKnowledgeFixture) source(
	t *testing.T,
) (models.Ticket, models.TicketAttachment) {
	t.Helper()
	ticket := queryTestTicket(
		fixture.scope.OrganizationID,
		fixture.scope.ProjectID,
		"OPS-7",
		models.HumanActor(42),
	)
	ticket.QueueID = fixture.scope.ProjectID
	creatorID := uint(42)
	ticket.CreatedByID = &creatorID
	if err := fixture.db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	attachment := models.TicketAttachment{
		TicketID:     ticket.ID,
		ActorType:    models.ActorTypeHuman,
		ActorID:      "42",
		FileName:     "evidence.txt",
		OriginalName: "evidence.txt",
		FileSize:     8,
		MimeType:     "text/plain",
		StoragePath:  "private/evidence.txt",
		Hash:         strings.Repeat("a", 64),
		VirusScan:    models.VirusScanClean,
	}
	if err := fixture.db.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	return ticket, attachment
}

type authoredPrincipalFixture struct {
	principal  models.ServicePrincipal
	credential models.AgentCredential
	context    context.Context
}

func (fixture authoredKnowledgeFixture) principal(
	t *testing.T,
	name string,
	scopes ...string,
) authoredPrincipalFixture {
	t.Helper()
	scopePayload, err := json.Marshal(scopes)
	if err != nil {
		t.Fatal(err)
	}
	principal := models.ServicePrincipal{
		ID:          newNativeID(),
		Name:        name,
		Status:      models.ServicePrincipalStatusActive,
		Scopes:      datatypes.JSON(scopePayload),
		PolicyEpoch: 1,
	}
	if err := fixture.db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          fixture.scope.ProjectID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(scopePayload),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.AgentCredential{
		ID:                 newNativeID(),
		ServicePrincipalID: principal.ID,
		Name:               name,
		SecretHash:         strings.Repeat("d", 64),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          time.Now().Add(time.Hour),
	}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        fixture.scope,
			Actor:        models.ServicePrincipalActor(principal.ID),
			Source:       SourceProtocolAgentREST,
			CredentialID: credential.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return authoredPrincipalFixture{
		principal: principal, credential: credential, context: operationContext,
	}
}

func (fixture authoredKnowledgeFixture) policyDecision(
	t *testing.T,
	principal authoredPrincipalFixture,
	scope string,
	action string,
	resourceType string,
	resourceID string,
	isWrite bool,
	policyEpoch uint64,
) models.PolicyDecision {
	t.Helper()
	decision := models.PolicyDecision{
		ID:                 newNativeID(),
		OrganizationID:     fixture.scope.OrganizationID,
		ProjectID:          fixture.scope.ProjectID,
		ServicePrincipalID: principal.principal.ID,
		CredentialID:       principal.credential.ID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            principal.principal.ID,
		Scope:              scope,
		Action:             action,
		ResourceType:       resourceType,
		ResourceID:         resourceID,
		IsWrite:            isWrite,
		Allowed:            true,
		ReasonCode:         "allow",
		PolicyEpoch:        policyEpoch,
	}
	if err := fixture.db.Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	return decision
}
