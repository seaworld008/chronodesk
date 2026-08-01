package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/services"
)

func buildAttachmentStores(
	ctx context.Context,
	cfg config.AgentConfig,
) (
	services.AttachmentStorage,
	services.AttachmentStagingStore,
	error,
) {
	localStorage, err := services.NewLocalAttachmentStorage(
		cfg.AttachmentDir,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"initialize local attachment storage: %w",
			err,
		)
	}
	backend := strings.ToLower(
		strings.TrimSpace(cfg.AttachmentStorageBackend),
	)
	if backend == "" {
		backend = "local"
	}
	localStoreID := strings.ToLower(
		strings.TrimSpace(cfg.AttachmentLocalStoreID),
	)
	if localStoreID == "" {
		localStoreID = "local-default"
	}
	s3StoreID := strings.ToLower(
		strings.TrimSpace(cfg.AttachmentS3StoreID),
	)
	if s3StoreID == "" {
		s3StoreID = "s3-default"
	}

	var stagingStorage *services.LocalAttachmentStorage
	if backend == "local" {
		stagingStorage = localStorage
	} else {
		stagingStorage, err = services.NewLocalAttachmentStorage(
			cfg.AttachmentStagingDir,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"initialize attachment staging storage: %w",
				err,
			)
		}
	}

	s3Requested := backend == "s3" ||
		strings.TrimSpace(cfg.AttachmentS3Bucket) != ""
	registrations := []services.AttachmentStorageRegistration{{
		StoreID:     localStoreID,
		StorageType: "local",
		Storage:     localStorage,
	}}
	primaryStoreID := localStoreID
	startupContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if s3Requested {
		s3Storage, storageErr := services.NewS3AttachmentStorage(
			startupContext,
			services.S3AttachmentStorageConfig{
				StoreID:               s3StoreID,
				Endpoint:              cfg.AttachmentS3Endpoint,
				Region:                cfg.AttachmentS3Region,
				Bucket:                cfg.AttachmentS3Bucket,
				Prefix:                cfg.AttachmentS3Prefix,
				UsePathStyle:          cfg.AttachmentS3UsePathStyle,
				AllowInsecureEndpoint: cfg.AttachmentS3AllowInsecure,
				AccessKeyID:           cfg.AttachmentS3AccessKeyID,
				SecretAccessKey:       cfg.AttachmentS3SecretAccessKey,
				SessionToken:          cfg.AttachmentS3SessionToken,
				ServerSideEncryption:  cfg.AttachmentS3SSE,
				KMSKeyID:              cfg.AttachmentS3KMSKeyID,
				VersioningMode:        cfg.AttachmentS3VersioningMode,
			},
		)
		if storageErr != nil {
			return nil, nil, fmt.Errorf(
				"initialize S3 attachment storage: %w",
				storageErr,
			)
		}
		registrations = append(
			registrations,
			services.AttachmentStorageRegistration{
				StoreID:     s3StoreID,
				StorageType: "s3",
				Storage:     s3Storage,
			},
		)
		if backend == "s3" {
			primaryStoreID = s3StoreID
		}
	}
	for _, historical := range cfg.AttachmentS3HistoricalStores {
		historicalStorage, storageErr :=
			services.NewS3AttachmentStorage(
				startupContext,
				services.S3AttachmentStorageConfig{
					StoreID:               historical.StoreID,
					Endpoint:              historical.Endpoint,
					Region:                historical.Region,
					Bucket:                historical.Bucket,
					Prefix:                historical.Prefix,
					UsePathStyle:          historical.UsePathStyle,
					AllowInsecureEndpoint: historical.AllowInsecureEndpoint,
					AccessKeyID:           historical.AccessKeyID,
					SecretAccessKey:       historical.SecretAccessKey,
					SessionToken:          historical.SessionToken,
					ServerSideEncryption:  historical.ServerSideEncryption,
					KMSKeyID:              historical.KMSKeyID,
					VersioningMode:        historical.VersioningMode,
				},
			)
		if storageErr != nil {
			return nil, nil, fmt.Errorf(
				"initialize historical S3 attachment store %q: %w",
				historical.StoreID,
				storageErr,
			)
		}
		registrations = append(
			registrations,
			services.AttachmentStorageRegistration{
				StoreID:     historical.StoreID,
				StorageType: "s3",
				Storage:     historicalStorage,
			},
		)
	}
	router, err := services.NewAttachmentStorageRouterWithRegistry(
		primaryStoreID,
		registrations,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"initialize attachment storage router: %w",
			err,
		)
	}
	return router, stagingStorage, nil
}

// knowledgeStorageBucket is descriptive control metadata only; object access
// always flows through AttachmentStorage and never through a client-visible
// bucket URL. Local deployments use a stable logical bucket name so stored
// version metadata does not expose a host filesystem path.
func knowledgeStorageBucket(cfg config.AgentConfig) string {
	if strings.EqualFold(
		strings.TrimSpace(cfg.AttachmentStorageBackend),
		"s3",
	) {
		return strings.TrimSpace(cfg.AttachmentS3Bucket)
	}
	return "chronodesk-managed"
}
