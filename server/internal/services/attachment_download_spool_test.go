package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestAttachmentDownloadLimiterBoundsGlobalAndActorConcurrency(
	t *testing.T,
) {
	service := NewAgentNativeService(nil, AgentNativeOptions{
		AttachmentDownloadConcurrency:         2,
		AttachmentDownloadPerActorConcurrency: 1,
	})
	firstActor := models.HumanActor(1)
	secondActor := models.HumanActor(2)
	thirdActor := models.HumanActor(3)

	releaseFirst, err := service.acquireAttachmentDownload(
		context.Background(),
		firstActor,
	)
	if err != nil {
		t.Fatalf("acquire first actor: %v", err)
	}
	defer releaseFirst()

	perActorContext, cancelPerActor := context.WithTimeout(
		context.Background(),
		25*time.Millisecond,
	)
	defer cancelPerActor()
	if _, err := service.acquireAttachmentDownload(
		perActorContext,
		firstActor,
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"second same-actor acquisition error = %v, want deadline",
			err,
		)
	}

	releaseSecond, err := service.acquireAttachmentDownload(
		context.Background(),
		secondActor,
	)
	if err != nil {
		t.Fatalf("acquire second actor: %v", err)
	}
	defer releaseSecond()

	globalContext, cancelGlobal := context.WithTimeout(
		context.Background(),
		25*time.Millisecond,
	)
	defer cancelGlobal()
	if _, err := service.acquireAttachmentDownload(
		globalContext,
		thirdActor,
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"global-budget acquisition error = %v, want deadline",
			err,
		)
	}

	releaseFirst()
	releaseSecond()
	releaseFirst()
	releaseSecond()

	releaseAfterCleanup, err := service.acquireAttachmentDownload(
		context.Background(),
		thirdActor,
	)
	if err != nil {
		t.Fatalf("acquire after releases: %v", err)
	}
	releaseAfterCleanup()

	service.attachmentDownloadActorsMu.Lock()
	activeActors := len(service.attachmentDownloadActors)
	service.attachmentDownloadActorsMu.Unlock()
	if activeActors != 0 {
		t.Fatalf(
			"download actor limiter retained %d inactive actors",
			activeActors,
		)
	}
}

func TestAttachmentDownloadReaderCancellationCleansSpoolAndReleasesSlot(
	t *testing.T,
) {
	staging := newTrackingAttachmentStagingStore()
	key := ".staging/download-cancel.spool"
	staging.objects[key] = []byte("cancelled download")
	ctx, cancel := context.WithCancel(context.Background())
	var releases atomic.Int32
	reader := newAttachmentDownloadReader(
		ctx,
		io.NopCloser(bytes.NewReader(staging.objects[key])),
		staging,
		key,
		func() {
			releases.Add(1)
		},
	)

	cancel()
	select {
	case deletedKey := <-staging.deleted:
		if deletedKey != key {
			t.Fatalf("deleted staging key = %q, want %q", deletedKey, key)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled download did not clean its spool")
	}
	if releases.Load() != 1 {
		t.Fatalf("download release count = %d, want 1", releases.Load())
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if releases.Load() != 1 {
		t.Fatalf(
			"idempotent close release count = %d, want 1",
			releases.Load(),
		)
	}
}

func TestAttachmentDownloadReaderErrorCleansSpool(
	t *testing.T,
) {
	staging := newTrackingAttachmentStagingStore()
	key := ".staging/download-read-error.spool"
	staging.objects[key] = []byte("failed download")
	var releases atomic.Int32
	readFailure := errors.New("staged reader failed")
	failingReader := &attachmentDownloadFailingReader{
		err: readFailure,
	}
	reader := newAttachmentDownloadReader(
		context.Background(),
		failingReader,
		staging,
		key,
		func() {
			releases.Add(1)
		},
	)

	if _, err := reader.Read(make([]byte, 32)); !errors.Is(
		err,
		readFailure,
	) {
		t.Fatalf("read failure = %v, want staged reader failure", err)
	}
	select {
	case deletedKey := <-staging.deleted:
		if deletedKey != key {
			t.Fatalf("deleted staging key = %q, want %q", deletedKey, key)
		}
	case <-time.After(time.Second):
		t.Fatal("failed download did not clean its spool")
	}
	if releases.Load() != 1 {
		t.Fatalf("download release count = %d, want 1", releases.Load())
	}
	if !failingReader.closed.Load() {
		t.Fatal("failed staged reader was not closed")
	}
}

func TestOpenAttachmentStreamsLargeObjectThroughPrivateSpool(
	t *testing.T,
) {
	const objectSize = int64(24 << 20)
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "bounded-download")
	actor := models.SystemActor("bounded-download-test")
	ctx := testProjectOperationContext(t, db, actor)
	ticket := seedNativeTicket(
		t,
		db,
		user.ID,
		"ATTACHMENT-BOUNDED-DOWNLOAD",
	)
	contentHash := repeatedByteSHA256('x', objectSize)
	source := &boundedPatternAttachmentStorage{
		key:  "tickets/large-object.bin",
		size: objectSize,
		fill: 'x',
	}
	stagingRoot := t.TempDir()
	staging, err := NewLocalAttachmentStorage(stagingRoot)
	if err != nil {
		t.Fatalf("create attachment staging: %v", err)
	}
	attachment := seedCleanDownloadAttachment(
		t,
		db,
		ticket,
		actor,
		source.key,
		"test",
		objectSize,
		contentHash,
	)
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage:                     source,
		AttachmentStaging:                     staging,
		AttachmentMaxBytes:                    objectSize,
		AttachmentDownloadConcurrency:         1,
		AttachmentDownloadPerActorConcurrency: 1,
	})

	opened, reader, err := service.OpenTicketAttachment(
		ctx,
		ticket.ID,
		attachment.ID,
	)
	if err != nil {
		t.Fatalf("open large attachment: %v", err)
	}
	if opened.ID != attachment.ID {
		t.Fatalf("opened attachment ID = %d, want %d", opened.ID, attachment.ID)
	}
	hash := sha256.New()
	count, copyErr := io.CopyBuffer(
		hash,
		reader,
		make([]byte, 32*1024),
	)
	closeErr := reader.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf(
			"stream large attachment: copy=%v close=%v",
			copyErr,
			closeErr,
		)
	}
	if count != objectSize ||
		hex.EncodeToString(hash.Sum(nil)) != contentHash {
		t.Fatalf(
			"large attachment output size/hash = %d/%x",
			count,
			hash.Sum(nil),
		)
	}
	if largest := source.maxReadBuffer.Load(); largest > 32*1024 {
		t.Fatalf(
			"source read buffer = %d bytes, want at most 32768",
			largest,
		)
	}
	assertAttachmentDownloadStagingEmpty(t, stagingRoot)
}

func TestOpenAttachmentFinalAuthorizationFailureCleansSpool(
	t *testing.T,
) {
	db := openAgentNativeTestDB(t)
	user := seedActorUser(t, db, "download-revoked")
	actor := models.HumanActor(user.ID)
	ctx := testProjectOperationContext(t, db, actor)
	ensureAttachmentTestAuthorization(t, db, ctx, actor)
	ticket := seedNativeTicket(
		t,
		db,
		user.ID,
		"ATTACHMENT-DOWNLOAD-REVOKED",
	)
	source, err := NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatalf("create source storage: %v", err)
	}
	content := []byte("authorization changes after external read")
	stored, err := source.Put(
		context.Background(),
		"tickets/revoked.txt",
		bytes.NewReader(content),
		int64(len(content)),
	)
	if err != nil {
		t.Fatalf("store source attachment: %v", err)
	}
	attachment := seedCleanDownloadAttachment(
		t,
		db,
		ticket,
		actor,
		stored.Key,
		source.AttachmentStorageType(),
		stored.Size,
		stored.SHA256,
	)
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stagingRoot := t.TempDir()
	localStaging, err := NewLocalAttachmentStorage(stagingRoot)
	if err != nil {
		t.Fatalf("create download staging: %v", err)
	}
	staging := &attachmentStagingHook{
		AttachmentStagingStore: localStaging,
		afterStage: func() error {
			result := db.Model(&models.ProjectMembership{}).
				Where(
					"project_id = ? AND user_id = ?",
					operation.Scope.ProjectID,
					user.ID,
				).
				Updates(map[string]any{
					"is_active": false,
					"version":   gorm.Expr("version + 1"),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("membership revocation did not update one row")
			}
			return nil
		},
	}
	service := NewAgentNativeService(db, AgentNativeOptions{
		AttachmentStorage: source,
		AttachmentStaging: staging,
	})

	_, reader, err := service.OpenTicketAttachment(
		ctx,
		ticket.ID,
		attachment.ID,
	)
	if reader != nil {
		_ = reader.Close()
		t.Fatal("authorization failure returned a reader")
	}
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"authorization revocation error = %v, want project access denied",
			err,
		)
	}
	assertAttachmentDownloadStagingEmpty(t, stagingRoot)
}

type trackingAttachmentStagingStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	deleted chan string
}

func newTrackingAttachmentStagingStore() *trackingAttachmentStagingStore {
	return &trackingAttachmentStagingStore{
		objects: make(map[string][]byte),
		deleted: make(chan string, 4),
	}
}

func (store *trackingAttachmentStagingStore) Stage(
	context.Context,
	string,
	io.Reader,
	int64,
) (*StoredAttachmentObject, error) {
	return nil, errors.New("tracking staging store does not stage objects")
}

func (store *trackingAttachmentStagingStore) OpenStaged(
	_ context.Context,
	key string,
) (io.ReadCloser, error) {
	store.mu.Lock()
	content, ok := store.objects[key]
	store.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), content...))), nil
}

func (store *trackingAttachmentStagingStore) DeleteStaged(
	_ context.Context,
	key string,
) error {
	store.mu.Lock()
	delete(store.objects, key)
	store.mu.Unlock()
	store.deleted <- key
	return nil
}

type boundedPatternAttachmentStorage struct {
	key           string
	size          int64
	fill          byte
	maxReadBuffer atomic.Int64
}

func (storage *boundedPatternAttachmentStorage) Put(
	context.Context,
	string,
	io.Reader,
	int64,
) (*StoredAttachmentObject, error) {
	return nil, errors.New("bounded pattern storage is read-only")
}

func (storage *boundedPatternAttachmentStorage) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key != storage.key {
		return nil, os.ErrNotExist
	}
	return &boundedPatternReader{
		remaining: storage.size,
		fill:      storage.fill,
		maxBuffer: &storage.maxReadBuffer,
	}, nil
}

func (storage *boundedPatternAttachmentStorage) Delete(
	context.Context,
	string,
) error {
	return errors.New("bounded pattern storage is read-only")
}

type boundedPatternReader struct {
	remaining int64
	fill      byte
	maxBuffer *atomic.Int64
	closed    atomic.Bool
}

func (reader *boundedPatternReader) Read(buffer []byte) (int, error) {
	if reader.closed.Load() {
		return 0, os.ErrClosed
	}
	for {
		previous := reader.maxBuffer.Load()
		if int64(len(buffer)) <= previous ||
			reader.maxBuffer.CompareAndSwap(
				previous,
				int64(len(buffer)),
			) {
			break
		}
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := len(buffer)
	if int64(count) > reader.remaining {
		count = int(reader.remaining)
	}
	for index := range buffer[:count] {
		buffer[index] = reader.fill
	}
	reader.remaining -= int64(count)
	return count, nil
}

func (reader *boundedPatternReader) Close() error {
	reader.closed.Store(true)
	return nil
}

type attachmentDownloadFailingReader struct {
	err    error
	closed atomic.Bool
}

func (reader *attachmentDownloadFailingReader) Read(
	[]byte,
) (int, error) {
	return 0, reader.err
}

func (reader *attachmentDownloadFailingReader) Close() error {
	reader.closed.Store(true)
	return nil
}

type attachmentStagingHook struct {
	AttachmentStagingStore
	afterStage func() error
}

func (staging *attachmentStagingHook) Stage(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	staged, err := staging.AttachmentStagingStore.Stage(
		ctx,
		key,
		reader,
		maxBytes,
	)
	if err != nil {
		return nil, err
	}
	if staging.afterStage != nil {
		if err := staging.afterStage(); err != nil {
			return nil, err
		}
	}
	return staged, nil
}

func repeatedByteSHA256(fill byte, size int64) string {
	hash := sha256.New()
	buffer := bytes.Repeat([]byte{fill}, 32*1024)
	for remaining := size; remaining > 0; {
		count := int64(len(buffer))
		if count > remaining {
			count = remaining
		}
		_, _ = hash.Write(buffer[:count])
		remaining -= count
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func seedCleanDownloadAttachment(
	t *testing.T,
	db *gorm.DB,
	ticket models.Ticket,
	actor models.ActorRef,
	storagePath string,
	storageType string,
	size int64,
	hash string,
) models.TicketAttachment {
	t.Helper()
	attachment := models.TicketAttachment{
		OrganizationID: ticket.OrganizationID,
		ProjectID:      ticket.ProjectID,
		TicketID:       ticket.ID,
		ActorType:      actor.Type,
		ActorID:        actor.ID,
		FileName:       filepath.Base(storagePath),
		OriginalName:   "download.bin",
		FileSize:       size,
		MimeType:       "application/octet-stream",
		FileType:       models.AttachmentTypeOther,
		Extension:      ".bin",
		StoragePath:    storagePath,
		StorageType:    storageType,
		Hash:           hash,
		VirusScan:      models.VirusScanClean,
		IsPublic:       true,
	}
	if err := db.Create(&attachment).Error; err != nil {
		t.Fatalf("seed clean attachment: %v", err)
	}
	return attachment
}

func assertAttachmentDownloadStagingEmpty(
	t *testing.T,
	root string,
) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".staging"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read attachment staging directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 0 {
		t.Fatalf("attachment download spools were not cleaned: %v", names)
	}
}
