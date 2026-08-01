package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type routerTestStorage struct {
	storageType string
	objects     map[string][]byte
	deletes     []string
}

func (s *routerTestStorage) AttachmentStorageType() string {
	return s.storageType
}

func (s *routerTestStorage) Put(
	_ context.Context,
	key string,
	reader io.Reader,
	_ int64,
) (*StoredAttachmentObject, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if s.objects == nil {
		s.objects = make(map[string][]byte)
	}
	s.objects[key] = content
	return &StoredAttachmentObject{
		Key:  key,
		Size: int64(len(content)),
	}, nil
}

func (s *routerTestStorage) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}

func (s *routerTestStorage) Delete(
	_ context.Context,
	key string,
) error {
	s.deletes = append(s.deletes, key)
	delete(s.objects, key)
	return nil
}

func TestAttachmentStorageRouterWritesPrimaryAndReadsPersistedBackend(
	t *testing.T,
) {
	local := &routerTestStorage{
		storageType: "local",
		objects: map[string][]byte{
			"tickets/1/legacy.txt": []byte("legacy"),
		},
	}
	s3 := &routerTestStorage{
		storageType: "s3",
		objects:     make(map[string][]byte),
	}
	router, err := NewAttachmentStorageRouter(
		s3,
		map[string]AttachmentStorage{"local": local},
	)
	if err != nil {
		t.Fatalf("NewAttachmentStorageRouter(): %v", err)
	}
	stored, err := router.Put(
		context.Background(),
		"tickets/1/current.txt",
		bytes.NewBufferString("current"),
		100,
	)
	if err != nil || stored.Key != "tickets/1/current.txt" {
		t.Fatalf("Put() = %+v, %v", stored, err)
	}
	if string(s3.objects["tickets/1/current.txt"]) != "current" {
		t.Fatalf("primary object was not written to S3")
	}
	reader, err := router.OpenStored(
		context.Background(),
		"local",
		"tickets/1/legacy.txt",
	)
	if err != nil {
		t.Fatalf("OpenStored(local): %v", err)
	}
	content, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(content) != "legacy" {
		t.Fatalf("legacy content = %q, %v", content, err)
	}
}

func TestAttachmentStorageRouterLegacyCleanupWithoutBackendFailsClosed(
	t *testing.T,
) {
	local := &routerTestStorage{
		storageType: "local",
		objects: map[string][]byte{
			"tickets/1/object": []byte("local"),
		},
	}
	s3 := &routerTestStorage{
		storageType: "s3",
		objects: map[string][]byte{
			"tickets/1/object": []byte("s3"),
		},
	}
	router, err := NewAttachmentStorageRouter(
		s3,
		map[string]AttachmentStorage{
			"local":   local,
			"staging": local,
		},
	)
	if err != nil {
		t.Fatalf("NewAttachmentStorageRouter(): %v", err)
	}
	if err := router.DeleteStored(
		context.Background(),
		"",
		"tickets/1/object",
	); !errors.Is(err, ErrAttachmentStorageMissing) {
		t.Fatalf("DeleteStored(legacy) error = %v", err)
	}
	if len(local.deletes) != 0 || len(s3.deletes) != 0 {
		t.Fatalf(
			"failed-closed cleanup deleted local=%v s3=%v",
			local.deletes,
			s3.deletes,
		)
	}
}

func TestAttachmentStorageRouterRoutesExactHistoricalStoreAndRejectsAmbiguousAlias(
	t *testing.T,
) {
	current := &routerTestStorage{
		storageType: "s3",
		objects:     map[string][]byte{},
	}
	historical := &routerTestStorage{
		storageType: "s3",
		objects: map[string][]byte{
			"tickets/1/old": []byte("old"),
		},
	}
	router, err := NewAttachmentStorageRouterWithRegistry(
		"s3-current",
		[]AttachmentStorageRegistration{
			{
				StoreID:     "s3-current",
				StorageType: "s3",
				Storage:     current,
			},
			{
				StoreID:     "s3-2025",
				StorageType: "s3",
				Storage:     historical,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := router.OpenStoredObject(
		context.Background(),
		AttachmentStoredReference{
			StorageType: "s3",
			StoreID:     "s3-2025",
			Key:         "tickets/1/old",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || string(content) != "old" {
		t.Fatalf("historical read = %q, %v", content, readErr)
	}
	if _, err := router.OpenStored(
		context.Background(),
		"s3",
		"tickets/1/old",
	); !errors.Is(err, ErrAttachmentStorageMissing) {
		t.Fatalf("ambiguous legacy alias error = %v", err)
	}
}

func TestAttachmentUploadIntentFreezesStoreAcrossPrimaryRotation(
	t *testing.T,
) {
	oldStore := &routerTestStorage{
		storageType: "s3",
		objects:     map[string][]byte{},
	}
	newStore := &routerTestStorage{
		storageType: "s3",
		objects:     map[string][]byte{},
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
	intent, err := newAttachmentUploadMigrationIntent(
		models.TicketAttachment{
			ID:          9,
			TicketID:    3,
			FileName:    "object.bin",
			FileSize:    7,
			Hash:        strings.Repeat("a", 64),
			StorageType: "staging",
			StoragePath: ".staging/object.bin",
		},
		oldRouter,
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
	stored, err := putAttachmentInStore(
		context.Background(),
		rotatedRouter,
		intent.TargetStoreID,
		intent.FinalKey,
		strings.NewReader("content"),
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.StoreID != "s3-2025" ||
		string(oldStore.objects[intent.FinalKey]) != "content" ||
		len(newStore.objects) != 0 {
		t.Fatalf(
			"replay escaped frozen store: stored=%+v old=%v new=%v",
			stored,
			oldStore.objects,
			newStore.objects,
		)
	}
}

func TestAttachmentStorageRouterFailsClosedForUnknownBackend(
	t *testing.T,
) {
	local := &routerTestStorage{storageType: "local"}
	router, err := NewAttachmentStorageRouter(local, nil)
	if err != nil {
		t.Fatalf("NewAttachmentStorageRouter(): %v", err)
	}
	if _, err := router.OpenStored(
		context.Background(),
		"s3",
		"tickets/1/missing",
	); err == nil {
		t.Fatal("unknown persisted backend must fail closed")
	}
}
