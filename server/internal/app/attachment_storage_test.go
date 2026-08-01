package app

import (
	"context"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func TestBuildAttachmentStoresDefaultsToLocal(t *testing.T) {
	root := t.TempDir()
	storage, staging, err := buildAttachmentStores(
		context.Background(),
		config.AgentConfig{
			AttachmentStorageBackend: "local",
			AttachmentDir:            root,
			AttachmentStagingDir:     root + "/unused-staging",
		},
	)
	if err != nil {
		t.Fatalf("buildAttachmentStores(): %v", err)
	}
	if servicesStorageType(storage) != "local" {
		t.Fatalf("storage type = %q, want local", servicesStorageType(storage))
	}
	router, ok := storage.(*services.AttachmentStorageRouter)
	if !ok ||
		router.PrimaryAttachmentStoreID() != "local-default" ||
		staging == nil {
		t.Fatalf(
			"local backend must use the stable local-default store",
		)
	}
}

func servicesStorageType(storage services.AttachmentStorage) string {
	type storageTyper interface {
		AttachmentStorageType() string
	}
	if typed, ok := storage.(storageTyper); ok {
		return typed.AttachmentStorageType()
	}
	return ""
}

func TestKnowledgeStorageBucketDoesNotExposeLocalPath(t *testing.T) {
	if got := knowledgeStorageBucket(config.AgentConfig{
		AttachmentStorageBackend: "local",
		AttachmentDir:            "/private/operator/path",
	}); got != "chronodesk-managed" {
		t.Fatalf("local logical bucket = %q", got)
	}
	if got := knowledgeStorageBucket(config.AgentConfig{
		AttachmentStorageBackend: "s3",
		AttachmentS3Bucket:       "private-chronodesk",
	}); got != "private-chronodesk" {
		t.Fatalf("s3 bucket = %q", got)
	}
}
