package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

type recoveryVersionStorage struct {
	mu             sync.Mutex
	storeID        string
	nextVersion    int
	versions       map[string]map[string][]byte
	order          map[string][]string
	deleted        []string
	deleteFailures int
}

func newRecoveryVersionStorage(storeID string) *recoveryVersionStorage {
	return &recoveryVersionStorage{
		storeID:  storeID,
		versions: make(map[string]map[string][]byte),
		order:    make(map[string][]string),
	}
}

func (*recoveryVersionStorage) AttachmentStorageType() string {
	return "s3"
}

func (storage *recoveryVersionStorage) AttachmentStoreID() string {
	return storage.storeID
}

func (*recoveryVersionStorage) ObjectVersioningEnabled() bool {
	return true
}

func (storage *recoveryVersionStorage) Put(
	_ context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, ErrAttachmentTooLarge
	}
	digest := sha256.Sum256(payload)
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.nextVersion++
	versionID := fmt.Sprintf("version-%d", storage.nextVersion)
	if storage.versions[key] == nil {
		storage.versions[key] = make(map[string][]byte)
	}
	storage.versions[key][versionID] = bytes.Clone(payload)
	storage.order[key] = append(
		[]string{versionID},
		storage.order[key]...,
	)
	return &StoredAttachmentObject{
		Key:                 key,
		Size:                int64(len(payload)),
		SHA256:              hex.EncodeToString(digest[:]),
		DetectedContentType: http.DetectContentType(payload),
		StorageType:         "s3",
		StoreID:             storage.storeID,
		VersionID:           versionID,
	}, nil
}

func (storage *recoveryVersionStorage) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if len(storage.order[key]) == 0 {
		return nil, ErrAttachmentStoredObjectNotFound
	}
	return storage.openLocked(key, storage.order[key][0])
}

func (storage *recoveryVersionStorage) OpenVersion(
	_ context.Context,
	key string,
	versionID string,
) (io.ReadCloser, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return storage.openLocked(key, versionID)
}

func (storage *recoveryVersionStorage) openLocked(
	key string,
	versionID string,
) (io.ReadCloser, error) {
	payload, exists := storage.versions[key][versionID]
	if !exists {
		return nil, ErrAttachmentStoredObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(payload))), nil
}

func (storage *recoveryVersionStorage) Delete(
	ctx context.Context,
	key string,
) error {
	versionID, err := storage.CurrentVersion(ctx, key)
	if err != nil {
		if errors.Is(err, ErrAttachmentStoredObjectNotFound) {
			return nil
		}
		return err
	}
	return storage.DeleteVersion(ctx, key, versionID)
}

func (storage *recoveryVersionStorage) DeleteVersion(
	_ context.Context,
	key string,
	versionID string,
) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if storage.deleteFailures > 0 {
		storage.deleteFailures--
		return errors.New(
			"private endpoint credential should never be persisted",
		)
	}
	storage.deleted = append(storage.deleted, versionID)
	delete(storage.versions[key], versionID)
	filtered := storage.order[key][:0]
	for _, candidate := range storage.order[key] {
		if candidate != versionID {
			filtered = append(filtered, candidate)
		}
	}
	storage.order[key] = filtered
	return nil
}

func (storage *recoveryVersionStorage) CurrentVersion(
	_ context.Context,
	key string,
) (string, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	if len(storage.order[key]) == 0 {
		return "", ErrAttachmentStoredObjectNotFound
	}
	return storage.order[key][0], nil
}

func (storage *recoveryVersionStorage) ListObjectVersions(
	_ context.Context,
	key string,
	limit int,
) ([]string, bool, error) {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	hasMore := limit < len(storage.order[key])
	if limit > len(storage.order[key]) {
		limit = len(storage.order[key])
	}
	return append([]string(nil), storage.order[key][:limit]...),
		hasMore,
		nil
}

func (storage *recoveryVersionStorage) versionIDs(key string) []string {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	result := append([]string(nil), storage.order[key]...)
	sort.Strings(result)
	return result
}

func TestKnowledgeObjectRecoveryClosesPostPutPreReceiptCrashAcrossAllS3Versions(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	oldStore := newRecoveryVersionStorage("s3-2025")
	fixture.service.storage = oldStore
	operation, err := knowledgeOperation(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	articleID := newNativeID()
	versionID := newNativeID()
	markdown := "# interrupted\n"
	intent, err := fixture.service.registerAuthoredObjectWriteIntent(
		fixture.ctx,
		operation,
		articleID,
		versionID,
		markdown,
	)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := fixture.service.putAuthoredMarkdown(
		fixture.ctx,
		*intent,
		markdown,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an ambiguous transport retry that committed a second provider
	// generation before either VersionID reached PostgreSQL.
	second, err := oldStore.Put(
		fixture.ctx,
		intent.ObjectKey,
		bytes.NewBufferString(markdown),
		MaxAuthoredMarkdownBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.VersionID == second.VersionID {
		t.Fatal("test storage reused an S3 generation")
	}
	makeKnowledgeObjectIntentDue(t, fixture.db, intent.ID)

	newStore := newRecoveryVersionStorage("s3-2026")
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
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		router,
		"restart-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Claimed != 1 ||
		result.Cleaned != 1 ||
		result.Failed != 0 ||
		len(oldStore.versionIDs(intent.ObjectKey)) != 0 ||
		len(newStore.deleted) != 0 {
		t.Fatalf(
			"post-crash recovery result=%+v old=%v oldDeleted=%v newDeleted=%v",
			result,
			oldStore.versionIDs(intent.ObjectKey),
			oldStore.deleted,
			newStore.deleted,
		)
	}
	assertKnowledgeObjectIntentMissing(t, fixture.db, intent.ID)
}

func TestKnowledgeObjectRecoveryDeletesOnlyRecordedS3Generation(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	store := newRecoveryVersionStorage("s3-exact")
	fixture.service.storage = store
	operation, err := knowledgeOperation(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := fixture.service.registerAuthoredObjectWriteIntent(
		fixture.ctx,
		operation,
		newNativeID(),
		newNativeID(),
		"# exact\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	recorded, _, err := fixture.service.putAuthoredMarkdown(
		fixture.ctx,
		*intent,
		"# exact\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.recordAuthoredObjectWriteReceipt(
		fixture.ctx,
		fixture.scope,
		*intent,
		recorded,
	); err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.Put(
		fixture.ctx,
		intent.ObjectKey,
		bytes.NewBufferString("# later generation\n"),
		MaxAuthoredMarkdownBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	makeKnowledgeObjectIntentDue(t, fixture.db, intent.ID)
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		store,
		"exact-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	remaining := store.versionIDs(intent.ObjectKey)
	if result.Cleaned != 1 ||
		len(remaining) != 1 ||
		remaining[0] != unrelated.VersionID {
		t.Fatalf(
			"exact recovery result=%+v recorded=%s unrelated=%s remaining=%v deleted=%v",
			result,
			recorded.VersionID,
			unrelated.VersionID,
			remaining,
			store.deleted,
		)
	}
}

func TestKnowledgeObjectRecoveryContinuesAcrossBoundedS3VersionPages(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	store := newRecoveryVersionStorage("s3-many")
	fixture.service.storage = store
	operation, err := knowledgeOperation(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := fixture.service.registerAuthoredObjectWriteIntent(
		fixture.ctx,
		operation,
		newNativeID(),
		newNativeID(),
		"# repeated\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	for range knowledgeObjectRecoveryMaxVersions + 1 {
		if _, err := store.Put(
			fixture.ctx,
			intent.ObjectKey,
			bytes.NewBufferString("# repeated\n"),
			MaxAuthoredMarkdownBytes,
		); err != nil {
			t.Fatal(err)
		}
	}
	makeKnowledgeObjectIntentDue(t, fixture.db, intent.ID)
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		store,
		"paged-version-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		1,
	)
	if err != nil ||
		first.Continued != 1 ||
		first.Cleaned != 0 ||
		len(store.versionIDs(intent.ObjectKey)) != 1 {
		t.Fatalf(
			"first bounded version page result=%+v err=%v remaining=%d",
			first,
			err,
			len(store.versionIDs(intent.ObjectKey)),
		)
	}
	second, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		1,
	)
	if err != nil ||
		second.Cleaned != 1 ||
		len(store.versionIDs(intent.ObjectKey)) != 0 {
		t.Fatalf(
			"second bounded version page result=%+v err=%v remaining=%d",
			second,
			err,
			len(store.versionIDs(intent.ObjectKey)),
		)
	}
	assertKnowledgeObjectIntentMissing(t, fixture.db, intent.ID)
}

func TestKnowledgeObjectRecoveryRetriesSanitizedFailureAfterRestart(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	store := newRecoveryVersionStorage("s3-retry")
	store.deleteFailures = 2
	fixture.service.storage = store
	existing := models.KnowledgeArticle{
		ID:             newNativeID(),
		OrganizationID: fixture.scope.OrganizationID,
		ProjectID:      fixture.scope.ProjectID,
		Key:            "duplicate-recovery",
		Title:          "Existing",
		Status:         models.KnowledgeArticleActive,
		Revision:       1,
		CreatedByType:  models.ActorTypeHuman,
		CreatedByID:    "42",
		UpdatedByType:  models.ActorTypeHuman,
		UpdatedByID:    "42",
	}
	if err := fixture.db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}
	_, createErr := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      existing.Key,
			Title:    "Duplicate",
			Markdown: "# durable cleanup\n",
		},
	)
	if !errors.Is(
		createErr,
		ErrKnowledgeObjectCleanupDeferred,
	) {
		t.Fatalf("transaction failure cleanup error = %v", createErr)
	}
	var intent models.KnowledgeObjectWriteIntent
	if err := fixture.db.First(&intent).Error; err != nil {
		t.Fatalf("durable cleanup intent missing: %v", err)
	}
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		store,
		"first-retry-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		10,
	)
	if err == nil || failed.Failed != 1 {
		t.Fatalf("first cleanup result=%+v err=%v", failed, err)
	}
	if err := fixture.db.First(&intent, "id = ?", intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intent.FailureCode != "storage_unavailable" ||
		intent.LeaseOwner != "" ||
		intent.LeaseExpiresAt != nil {
		t.Fatalf("persisted recovery state = %+v", intent)
	}
	if bytes.Contains(
		[]byte(intent.FailureCode),
		[]byte("credential"),
	) {
		t.Fatalf("sensitive storage failure was persisted: %+v", intent)
	}
	makeKnowledgeObjectIntentDue(t, fixture.db, intent.ID)
	restarted, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		store,
		"restarted-retry-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.ProcessProject(
		context.Background(),
		fixture.scope,
		10,
	)
	if err != nil ||
		recovered.Cleaned != 1 ||
		len(store.versionIDs(intent.ObjectKey)) != 0 {
		t.Fatalf(
			"restarted cleanup result=%+v err=%v remaining=%v",
			recovered,
			err,
			store.versionIDs(intent.ObjectKey),
		)
	}
	assertKnowledgeObjectIntentMissing(t, fixture.db, intent.ID)
}

func TestSuccessfulAuthoredWriteTransfersObjectAndLeavesNothingForSweeper(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	created, err := fixture.service.CreateAuthoredArticle(
		fixture.ctx,
		CreateAuthoredArticleInput{
			Key:      "successful-transfer",
			Title:    "Successful transfer",
			Markdown: "# owned by PostgreSQL\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var intents int64
	if err := fixture.db.Model(
		&models.KnowledgeObjectWriteIntent{},
	).Count(&intents).Error; err != nil {
		t.Fatal(err)
	}
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		fixture.storage,
		"success-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		10,
	)
	if err != nil ||
		intents != 0 ||
		result.Claimed != 0 {
		t.Fatalf(
			"successful transfer intents=%d result=%+v err=%v",
			intents,
			result,
			err,
		)
	}
	fixture.storage.mu.Lock()
	_, exists := fixture.storage.objects[created.Version.ObjectKey]
	deleted := append([]string(nil), fixture.storage.deleted...)
	fixture.storage.mu.Unlock()
	if !exists || len(deleted) != 0 {
		t.Fatalf(
			"business-owned object exists=%v deleted=%v",
			exists,
			deleted,
		)
	}
}

func TestKnowledgeObjectCleanupClaimIsBoundedAndFenced(t *testing.T) {
	fixture := newAuthoredKnowledgeFixture(t)
	now := time.Now().UTC()
	for index := 0; index < 2; index++ {
		intent := models.KnowledgeObjectWriteIntent{
			OrganizationID: fixture.scope.OrganizationID,
			ProjectID:      fixture.scope.ProjectID,
			ArticleID:      newNativeID(),
			VersionID:      newNativeID(),
			ObjectProvider: "managed",
			ObjectStoreID:  "memory-default",
			ObjectKey: fmt.Sprintf(
				"knowledge/%d/%s/%d.md",
				fixture.scope.ProjectID,
				newNativeID(),
				index,
			),
			SizeBytes:     1,
			ContentHash:   fmt.Sprintf("%064x", index+1),
			CreatedByType: models.ActorTypeHuman,
			CreatedByID:   "42",
			NextAttemptAt: now.Add(-time.Minute),
		}
		if err := fixture.db.Create(&intent).Error; err != nil {
			t.Fatal(err)
		}
	}
	worker, err := NewKnowledgeObjectCleanupWorker(
		fixture.db,
		fixture.storage,
		"bounded-worker",
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := worker.claimProjectIntents(
		context.Background(),
		fixture.scope,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 ||
		claimed[0].FencingToken != 1 ||
		claimed[0].Attempts != 1 ||
		claimed[0].LeaseOwner != "bounded-worker" ||
		claimed[0].LeaseExpiresAt == nil {
		t.Fatalf("bounded fenced claim = %+v", claimed)
	}
	second, err := worker.claimProjectIntents(
		context.Background(),
		fixture.scope,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID == claimed[0].ID {
		t.Fatalf("second bounded claim = %+v first=%+v", second, claimed)
	}
	if _, err := worker.ProcessProject(
		context.Background(),
		fixture.scope,
		knowledgeObjectRecoveryMaxBatch+1,
	); err == nil {
		t.Fatal("cleanup accepted an unbounded batch")
	}
}

func makeKnowledgeObjectIntentDue(
	t *testing.T,
	db *gorm.DB,
	intentID string,
) {
	t.Helper()
	if err := db.Model(
		&models.KnowledgeObjectWriteIntent{},
	).Where("id = ?", intentID).Updates(map[string]any{
		"next_attempt_at":  time.Now().UTC().Add(-time.Minute),
		"lease_owner":      "",
		"lease_expires_at": nil,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func assertKnowledgeObjectIntentMissing(
	t *testing.T,
	db *gorm.DB,
	intentID string,
) {
	t.Helper()
	var count int64
	if err := db.Model(
		&models.KnowledgeObjectWriteIntent{},
	).Where("id = ?", intentID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf(
			"knowledge object recovery intent %s still exists",
			intentID,
		)
	}
}
