package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type fakeS3AttachmentObjectClient struct {
	headBucket func(
		context.Context,
		*s3.HeadBucketInput,
		...func(*s3.Options),
	) (*s3.HeadBucketOutput, error)
	getBucketVersioning func(
		context.Context,
		*s3.GetBucketVersioningInput,
		...func(*s3.Options),
	) (*s3.GetBucketVersioningOutput, error)
	headObject func(
		context.Context,
		*s3.HeadObjectInput,
		...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
	listObjectVersions func(
		context.Context,
		*s3.ListObjectVersionsInput,
		...func(*s3.Options),
	) (*s3.ListObjectVersionsOutput, error)
	getObject func(
		context.Context,
		*s3.GetObjectInput,
		...func(*s3.Options),
	) (*s3.GetObjectOutput, error)
	deleteObject func(
		context.Context,
		*s3.DeleteObjectInput,
		...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
}

func (client *fakeS3AttachmentObjectClient) HeadObject(
	ctx context.Context,
	input *s3.HeadObjectInput,
	options ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	if client.headObject == nil {
		return nil, errors.New("unexpected HeadObject")
	}
	return client.headObject(ctx, input, options...)
}

func (client *fakeS3AttachmentObjectClient) ListObjectVersions(
	ctx context.Context,
	input *s3.ListObjectVersionsInput,
	options ...func(*s3.Options),
) (*s3.ListObjectVersionsOutput, error) {
	if client.listObjectVersions == nil {
		return nil, errors.New("unexpected ListObjectVersions")
	}
	return client.listObjectVersions(ctx, input, options...)
}

func (client *fakeS3AttachmentObjectClient) GetBucketVersioning(
	ctx context.Context,
	input *s3.GetBucketVersioningInput,
	options ...func(*s3.Options),
) (*s3.GetBucketVersioningOutput, error) {
	if client.getBucketVersioning == nil {
		return &s3.GetBucketVersioningOutput{}, nil
	}
	return client.getBucketVersioning(ctx, input, options...)
}

func (client *fakeS3AttachmentObjectClient) HeadBucket(
	ctx context.Context,
	input *s3.HeadBucketInput,
	options ...func(*s3.Options),
) (*s3.HeadBucketOutput, error) {
	if client.headBucket == nil {
		return &s3.HeadBucketOutput{}, nil
	}
	return client.headBucket(ctx, input, options...)
}

func (client *fakeS3AttachmentObjectClient) GetObject(
	ctx context.Context,
	input *s3.GetObjectInput,
	options ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	if client.getObject == nil {
		return nil, errors.New("unexpected GetObject")
	}
	return client.getObject(ctx, input, options...)
}

func (client *fakeS3AttachmentObjectClient) DeleteObject(
	ctx context.Context,
	input *s3.DeleteObjectInput,
	options ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	if client.deleteObject == nil {
		return nil, errors.New("unexpected DeleteObject")
	}
	return client.deleteObject(ctx, input, options...)
}

type fakeS3AttachmentUploader struct {
	upload func(
		context.Context,
		*s3.PutObjectInput,
		...func(*manager.Uploader),
	) (*manager.UploadOutput, error)
}

func (uploader *fakeS3AttachmentUploader) Upload(
	ctx context.Context,
	input *s3.PutObjectInput,
	options ...func(*manager.Uploader),
) (*manager.UploadOutput, error) {
	if uploader.upload == nil {
		return nil, errors.New("unexpected upload")
	}
	return uploader.upload(ctx, input, options...)
}

func validS3AttachmentConfig() S3AttachmentStorageConfig {
	return S3AttachmentStorageConfig{
		StoreID: "s3-default",
		Region:  "us-east-1",
		Bucket:  "chronodesk-attachments",
		Prefix:  "private/attachments",
	}
}

func normalizedS3AttachmentConfigForTest(
	t *testing.T,
	config S3AttachmentStorageConfig,
) normalizedS3AttachmentStorageConfig {
	t.Helper()
	normalized, err := normalizeS3AttachmentStorageConfig(config)
	if err != nil {
		t.Fatalf("normalize S3 attachment config: %v", err)
	}
	return normalized
}

func newTestS3AttachmentStorage(
	t *testing.T,
	config S3AttachmentStorageConfig,
	client *fakeS3AttachmentObjectClient,
	uploader s3AttachmentUploader,
) *S3AttachmentStorage {
	t.Helper()
	storage, err := newS3AttachmentStorageWithClients(
		context.Background(),
		normalizedS3AttachmentConfigForTest(t, config),
		client,
		uploader,
	)
	if err != nil {
		t.Fatalf("construct test S3 attachment storage: %v", err)
	}
	return storage
}

func TestNormalizeS3AttachmentStorageConfigDefaultsAndCompatibility(t *testing.T) {
	config := validS3AttachmentConfig()
	config.Prefix = "/private/attachments/"
	config.Endpoint = "https://minio.example.test/base/"
	config.UsePathStyle = true
	config.AccessKeyID = "attachment-writer"
	config.SecretAccessKey = "dedicated-secret"
	config.ServerSideEncryption = "AES256"

	normalized, err := normalizeS3AttachmentStorageConfig(config)
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if normalized.prefix != "private/attachments" ||
		normalized.endpoint != "https://minio.example.test/base" ||
		!normalized.usePathStyle ||
		normalized.encryption != S3AttachmentSSEAES256 ||
		normalized.uploadPartSize != defaultS3AttachmentUploadPartSize ||
		normalized.uploadConcurrency !=
			defaultS3AttachmentUploadConcurrency {
		t.Fatalf("unexpected normalized config: %+v", normalized)
	}
}

func TestNormalizeS3AttachmentStorageConfigRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*S3AttachmentStorageConfig)
	}{
		{
			name: "missing region",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.Region = ""
			},
		},
		{
			name: "invalid bucket",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.Bucket = "bucket/name"
			},
		},
		{
			name: "escaping prefix",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.Prefix = "../outside"
			},
		},
		{
			name: "endpoint credentials",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.Endpoint =
					"https://user:password@minio.example.test"
			},
		},
		{
			name: "insecure endpoint without opt in",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.Endpoint = "http://127.0.0.1:9000"
			},
		},
		{
			name: "partial static credentials",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.AccessKeyID = "access-only"
			},
		},
		{
			name: "session token without static credentials",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.SessionToken = "session-only"
			},
		},
		{
			name: "KMS key with bucket default encryption",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.KMSKeyID = "kms-key"
			},
		},
		{
			name: "unsupported encryption",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.ServerSideEncryption = "SSE-C"
			},
		},
		{
			name: "part below S3 minimum",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.UploadPartSize =
					manager.MinUploadPartSize - 1
			},
		},
		{
			name: "excessive concurrency",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.UploadConcurrency =
					maxS3AttachmentUploadConcurrency + 1
			},
		},
		{
			name: "excessive aggregate multipart buffering",
			mutate: func(config *S3AttachmentStorageConfig) {
				config.UploadPartSize =
					maxS3AttachmentUploadPartSize
				config.UploadConcurrency = 4
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validS3AttachmentConfig()
			test.mutate(&config)
			if _, err := normalizeS3AttachmentStorageConfig(
				config,
			); err == nil {
				t.Fatal("expected invalid S3 configuration")
			}
		})
	}
}

func TestNormalizeS3AttachmentStorageConfigAllowsExplicitHTTPMinIO(
	t *testing.T,
) {
	config := validS3AttachmentConfig()
	config.Endpoint = "http://127.0.0.1:9000"
	config.AllowInsecureEndpoint = true
	config.UsePathStyle = true
	config.AccessKeyID = "minio-access"
	config.SecretAccessKey = "minio-secret"
	if _, err := normalizeS3AttachmentStorageConfig(config); err != nil {
		t.Fatalf("explicit development MinIO endpoint rejected: %v", err)
	}
}

func TestNewS3AttachmentStorageWithClientsVerifiesBucket(t *testing.T) {
	headCalls := 0
	client := &fakeS3AttachmentObjectClient{
		headBucket: func(
			_ context.Context,
			input *s3.HeadBucketInput,
			_ ...func(*s3.Options),
		) (*s3.HeadBucketOutput, error) {
			headCalls++
			if aws.ToString(input.Bucket) !=
				"chronodesk-attachments" {
				t.Fatalf(
					"HeadBucket bucket = %q",
					aws.ToString(input.Bucket),
				)
			}
			return &s3.HeadBucketOutput{}, nil
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		client,
		&fakeS3AttachmentUploader{},
	)
	if headCalls != 1 || storage.AttachmentStorageType() != "s3" {
		t.Fatalf(
			"head calls = %d, storage type = %q",
			headCalls,
			storage.AttachmentStorageType(),
		)
	}
}

func TestNewS3AttachmentStorageWithClientsFailsClosedOnHeadBucket(
	t *testing.T,
) {
	headErr := errors.New("bucket unavailable")
	client := &fakeS3AttachmentObjectClient{
		headBucket: func(
			context.Context,
			*s3.HeadBucketInput,
			...func(*s3.Options),
		) (*s3.HeadBucketOutput, error) {
			return nil, headErr
		},
	}
	_, err := newS3AttachmentStorageWithClients(
		context.Background(),
		normalizedS3AttachmentConfigForTest(
			t,
			validS3AttachmentConfig(),
		),
		client,
		&fakeS3AttachmentUploader{},
	)
	if !errors.Is(err, headErr) {
		t.Fatalf("HeadBucket error = %v, want %v", err, headErr)
	}
}

func TestS3AttachmentStoragePutStreamsHashesAndKeepsLogicalKey(
	t *testing.T,
) {
	config := validS3AttachmentConfig()
	config.ServerSideEncryption = S3AttachmentSSEKMS
	config.KMSKeyID = "arn:aws:kms:us-east-1:123456789012:key/test"
	config.UploadPartSize = manager.MinUploadPartSize
	config.UploadConcurrency = 3

	var uploaded []byte
	var uploadOptions manager.Uploader
	uploader := &fakeS3AttachmentUploader{
		upload: func(
			_ context.Context,
			input *s3.PutObjectInput,
			options ...func(*manager.Uploader),
		) (*manager.UploadOutput, error) {
			for _, option := range options {
				option(&uploadOptions)
			}
			if aws.ToString(input.Bucket) !=
				"chronodesk-attachments" {
				t.Fatalf(
					"upload bucket = %q",
					aws.ToString(input.Bucket),
				)
			}
			if aws.ToString(input.Key) !=
				"private/attachments/tickets/42/report.txt" {
				t.Fatalf(
					"upload object key = %q",
					aws.ToString(input.Key),
				)
			}
			if aws.ToString(input.ContentType) !=
				"text/plain; charset=utf-8" {
				t.Fatalf(
					"detected content type = %q",
					aws.ToString(input.ContentType),
				)
			}
			if input.ServerSideEncryption !=
				s3types.ServerSideEncryptionAwsKms ||
				aws.ToString(input.SSEKMSKeyId) != config.KMSKeyID {
				t.Fatalf(
					"unexpected encryption input: %+v",
					input,
				)
			}
			var err error
			uploaded, err = io.ReadAll(input.Body)
			return &manager.UploadOutput{
				Key:       input.Key,
				VersionID: aws.String("version-17"),
			}, err
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		config,
		&fakeS3AttachmentObjectClient{},
		uploader,
	)
	content := []byte("hello from ChronoDesk")
	stored, err := storage.Put(
		context.Background(),
		"tickets/42/report.txt",
		bytes.NewReader(content),
		int64(len(content)),
	)
	if err != nil {
		t.Fatalf("put S3 attachment: %v", err)
	}
	if !bytes.Equal(uploaded, content) {
		t.Fatalf("uploaded content = %q", uploaded)
	}
	if stored.Key != "tickets/42/report.txt" ||
		stored.Size != int64(len(content)) ||
		stored.SHA256 !=
			"4eb5198a4d708dce4ab97e3a276cbfba33cede17785ede644a8ffa68adfac6f0" ||
		stored.DetectedContentType !=
			"text/plain; charset=utf-8" ||
		stored.StoreID != "s3-default" ||
		stored.StorageType != "s3" ||
		stored.VersionID != "version-17" {
		t.Fatalf("stored attachment = %+v", stored)
	}
	if uploadOptions.PartSize != manager.MinUploadPartSize ||
		uploadOptions.Concurrency != 3 ||
		uploadOptions.LeavePartsOnError ||
		uploadOptions.MaxUploadParts != manager.MaxUploadParts ||
		uploadOptions.DisableValidateParts {
		t.Fatalf("unsafe uploader options: %+v", uploadOptions)
	}
}

func TestS3AttachmentStorageVersionedObjectRequiresAndUsesExactVersion(
	t *testing.T,
) {
	var gotGetVersion string
	var gotDeleteVersion string
	client := &fakeS3AttachmentObjectClient{
		getBucketVersioning: func(
			context.Context,
			*s3.GetBucketVersioningInput,
			...func(*s3.Options),
		) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{
				Status: s3types.BucketVersioningStatusEnabled,
			}, nil
		},
		getObject: func(
			_ context.Context,
			input *s3.GetObjectInput,
			_ ...func(*s3.Options),
		) (*s3.GetObjectOutput, error) {
			gotGetVersion = aws.ToString(input.VersionId)
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("stored")),
			}, nil
		},
		deleteObject: func(
			_ context.Context,
			input *s3.DeleteObjectInput,
			_ ...func(*s3.Options),
		) (*s3.DeleteObjectOutput, error) {
			gotDeleteVersion = aws.ToString(input.VersionId)
			return &s3.DeleteObjectOutput{}, nil
		},
	}
	config := validS3AttachmentConfig()
	config.VersioningMode = S3AttachmentVersioningRequired
	storage := newTestS3AttachmentStorage(
		t,
		config,
		client,
		&fakeS3AttachmentUploader{},
	)
	if _, err := storage.OpenVersion(
		context.Background(),
		"tickets/42/report.txt",
		"",
	); !errors.Is(err, ErrAttachmentVersionIDRequired) {
		t.Fatalf("versionless exact Open error = %v", err)
	}
	if err := storage.Delete(
		context.Background(),
		"tickets/42/report.txt",
	); !errors.Is(err, ErrAttachmentVersionIDRequired) {
		t.Fatalf("versionless Delete error = %v", err)
	}
	reader, err := storage.OpenVersion(
		context.Background(),
		"tickets/42/report.txt",
		"version-17",
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if err := storage.DeleteVersion(
		context.Background(),
		"tickets/42/report.txt",
		"version-17",
	); err != nil {
		t.Fatal(err)
	}
	if gotGetVersion != "version-17" ||
		gotDeleteVersion != "version-17" {
		t.Fatalf(
			"exact versions get=%q delete=%q",
			gotGetVersion,
			gotDeleteVersion,
		)
	}
}

func TestS3AttachmentStorageRequiredVersioningFailsStartup(
	t *testing.T,
) {
	config := validS3AttachmentConfig()
	config.VersioningMode = S3AttachmentVersioningRequired
	_, err := newS3AttachmentStorageWithClients(
		context.Background(),
		normalizedS3AttachmentConfigForTest(t, config),
		&fakeS3AttachmentObjectClient{},
		&fakeS3AttachmentUploader{},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "versioning is required") {
		t.Fatalf("required versioning startup error = %v", err)
	}
}

func TestS3AttachmentStorageVersionedPutFailsClosedWithoutVersionID(
	t *testing.T,
) {
	client := &fakeS3AttachmentObjectClient{
		getBucketVersioning: func(
			context.Context,
			*s3.GetBucketVersioningInput,
			...func(*s3.Options),
		) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{
				Status: s3types.BucketVersioningStatusEnabled,
			}, nil
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		client,
		&fakeS3AttachmentUploader{
			upload: func(
				_ context.Context,
				input *s3.PutObjectInput,
				_ ...func(*manager.Uploader),
			) (*manager.UploadOutput, error) {
				_, err := io.Copy(io.Discard, input.Body)
				return &manager.UploadOutput{}, err
			},
		},
	)
	if _, err := storage.Put(
		context.Background(),
		"tickets/42/report.txt",
		strings.NewReader("content"),
		100,
	); !errors.Is(err, ErrAttachmentVersionIDRequired) {
		t.Fatalf("missing upload VersionID error = %v", err)
	}
}

func TestS3AttachmentStoragePutRejectsOversizeBeforeUploadWhenSampleExceedsLimit(
	t *testing.T,
) {
	uploadCalls := 0
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		&fakeS3AttachmentObjectClient{},
		&fakeS3AttachmentUploader{
			upload: func(
				context.Context,
				*s3.PutObjectInput,
				...func(*manager.Uploader),
			) (*manager.UploadOutput, error) {
				uploadCalls++
				return &manager.UploadOutput{}, nil
			},
		},
	)
	_, err := storage.Put(
		context.Background(),
		"tickets/42/oversized.txt",
		strings.NewReader("1234"),
		3,
	)
	if !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("oversized Put error = %v", err)
	}
	if uploadCalls != 0 {
		t.Fatalf("oversized sample started %d uploads", uploadCalls)
	}
}

func TestS3AttachmentStorageMultipartFailureAbortsUpload(t *testing.T) {
	config := validS3AttachmentConfig()
	config.UploadPartSize = manager.MinUploadPartSize
	config.UploadConcurrency = 1
	multipart := &failingS3MultipartUploadClient{
		partError: errors.New("injected second-part failure"),
	}
	storage := newTestS3AttachmentStorage(
		t,
		config,
		&fakeS3AttachmentObjectClient{},
		manager.NewUploader(multipart),
	)
	content := bytes.Repeat(
		[]byte("x"),
		int(manager.MinUploadPartSize)+1,
	)
	_, err := storage.Put(
		context.Background(),
		"tickets/42/multipart.bin",
		bytes.NewReader(content),
		int64(len(content)),
	)
	if !errors.Is(err, multipart.partError) {
		t.Fatalf("multipart Put error = %v", err)
	}
	multipart.mu.Lock()
	defer multipart.mu.Unlock()
	if multipart.createCalls != 1 ||
		multipart.abortCalls != 1 ||
		multipart.completeCalls != 0 {
		t.Fatalf(
			"multipart lifecycle create=%d abort=%d complete=%d",
			multipart.createCalls,
			multipart.abortCalls,
			multipart.completeCalls,
		)
	}
}

func TestS3AttachmentStorageOpenAndIdempotentDeleteUsePrivatePrefix(
	t *testing.T,
) {
	getCalls := 0
	var deleted []string
	client := &fakeS3AttachmentObjectClient{
		getObject: func(
			_ context.Context,
			input *s3.GetObjectInput,
			_ ...func(*s3.Options),
		) (*s3.GetObjectOutput, error) {
			getCalls++
			if aws.ToString(input.Key) !=
				"private/attachments/tickets/42/report.txt" {
				t.Fatalf(
					"GetObject key = %q",
					aws.ToString(input.Key),
				)
			}
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader("stored")),
			}, nil
		},
		deleteObject: func(
			_ context.Context,
			input *s3.DeleteObjectInput,
			_ ...func(*s3.Options),
		) (*s3.DeleteObjectOutput, error) {
			deleted = append(deleted, aws.ToString(input.Key))
			if len(deleted) == 2 {
				return nil, &smithy.GenericAPIError{
					Code:    "NoSuchKey",
					Message: "already absent",
				}
			}
			return &s3.DeleteObjectOutput{}, nil
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		client,
		&fakeS3AttachmentUploader{},
	)
	reader, err := storage.Open(
		context.Background(),
		"tickets/42/report.txt",
	)
	if err != nil {
		t.Fatalf("open S3 attachment: %v", err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(content) != "stored" {
		t.Fatalf(
			"read S3 attachment content=%q readErr=%v closeErr=%v",
			content,
			readErr,
			closeErr,
		)
	}
	for range 2 {
		if err := storage.Delete(
			context.Background(),
			"tickets/42/report.txt",
		); err != nil {
			t.Fatalf("idempotent delete: %v", err)
		}
	}
	if getCalls != 1 || len(deleted) != 2 {
		t.Fatalf(
			"get calls=%d deleted keys=%v",
			getCalls,
			deleted,
		)
	}
	for _, key := range deleted {
		if key !=
			"private/attachments/tickets/42/report.txt" {
			t.Fatalf("DeleteObject key = %q", key)
		}
	}
}

func TestS3AttachmentStorageEnumeratesOnlyExactRecoveryKeyVersions(
	t *testing.T,
) {
	config := validS3AttachmentConfig()
	config.VersioningMode = S3AttachmentVersioningRequired
	logicalKey := "knowledge/10/article/version.md"
	expectedObjectKey := "private/attachments/" + logicalKey
	client := &fakeS3AttachmentObjectClient{
		getBucketVersioning: func(
			context.Context,
			*s3.GetBucketVersioningInput,
			...func(*s3.Options),
		) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{
				Status: s3types.BucketVersioningStatusEnabled,
			}, nil
		},
		listObjectVersions: func(
			_ context.Context,
			input *s3.ListObjectVersionsInput,
			_ ...func(*s3.Options),
		) (*s3.ListObjectVersionsOutput, error) {
			if aws.ToString(input.Bucket) != config.Bucket ||
				aws.ToString(input.Prefix) != expectedObjectKey ||
				aws.ToInt32(input.MaxKeys) != 3 {
				t.Fatalf(
					"unexpected version-list input: %+v",
					input,
				)
			}
			return &s3.ListObjectVersionsOutput{
				Versions: []s3types.ObjectVersion{
					{
						Key:       aws.String(expectedObjectKey),
						VersionId: aws.String("version-current"),
					},
					{
						Key: aws.String(
							expectedObjectKey + "-other",
						),
						VersionId: aws.String("unowned-version"),
					},
				},
				DeleteMarkers: []s3types.DeleteMarkerEntry{{
					Key:       aws.String(expectedObjectKey),
					VersionId: aws.String("delete-marker"),
				}},
			}, nil
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		config,
		client,
		&fakeS3AttachmentUploader{},
	)
	versions, hasMore, err := storage.ListObjectVersions(
		context.Background(),
		logicalKey,
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasMore ||
		len(versions) != 2 ||
		versions[0] != "version-current" ||
		versions[1] != "delete-marker" {
		t.Fatalf("exact recovery versions = %v", versions)
	}
}

func TestS3AttachmentStorageRejectsInvalidLogicalKeysBeforeIO(
	t *testing.T,
) {
	getCalls := 0
	deleteCalls := 0
	client := &fakeS3AttachmentObjectClient{
		getObject: func(
			context.Context,
			*s3.GetObjectInput,
			...func(*s3.Options),
		) (*s3.GetObjectOutput, error) {
			getCalls++
			return nil, nil
		},
		deleteObject: func(
			context.Context,
			*s3.DeleteObjectInput,
			...func(*s3.Options),
		) (*s3.DeleteObjectOutput, error) {
			deleteCalls++
			return nil, nil
		},
	}
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		client,
		&fakeS3AttachmentUploader{},
	)
	for _, key := range []string{
		"../outside",
		"/absolute",
		"tickets//file",
		`tickets\file`,
		"tickets/\nfile",
	} {
		if _, err := storage.Open(
			context.Background(),
			key,
		); !errors.Is(err, ErrInvalidAttachmentName) {
			t.Fatalf("Open(%q) error = %v", key, err)
		}
		if err := storage.Delete(
			context.Background(),
			key,
		); !errors.Is(err, ErrInvalidAttachmentName) {
			t.Fatalf("Delete(%q) error = %v", key, err)
		}
	}
	if getCalls != 0 || deleteCalls != 0 {
		t.Fatalf(
			"invalid keys reached S3: get=%d delete=%d",
			getCalls,
			deleteCalls,
		)
	}
}

func TestS3AttachmentStoragePutPropagatesReaderAndContextErrors(
	t *testing.T,
) {
	readerErr := errors.New("injected reader failure")
	storage := newTestS3AttachmentStorage(
		t,
		validS3AttachmentConfig(),
		&fakeS3AttachmentObjectClient{},
		&fakeS3AttachmentUploader{
			upload: func(
				_ context.Context,
				input *s3.PutObjectInput,
				_ ...func(*manager.Uploader),
			) (*manager.UploadOutput, error) {
				_, err := io.ReadAll(input.Body)
				return nil, err
			},
		},
	)
	if _, err := storage.Put(
		context.Background(),
		"tickets/42/broken.bin",
		&failingAttachmentReader{err: readerErr},
		1024,
	); !errors.Is(err, readerErr) {
		t.Fatalf("reader error = %v, want %v", err, readerErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := storage.Put(
		ctx,
		"tickets/42/cancelled.bin",
		strings.NewReader("content"),
		1024,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Put error = %v", err)
	}
}

type failingAttachmentReader struct {
	err error
}

func (reader *failingAttachmentReader) Read([]byte) (int, error) {
	return 0, reader.err
}

type failingS3MultipartUploadClient struct {
	mu            sync.Mutex
	partError     error
	createCalls   int
	abortCalls    int
	completeCalls int
}

func (*failingS3MultipartUploadClient) PutObject(
	context.Context,
	*s3.PutObjectInput,
	...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	return nil, errors.New("unexpected single-part PutObject")
}

func (client *failingS3MultipartUploadClient) CreateMultipartUpload(
	_ context.Context,
	_ *s3.CreateMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CreateMultipartUploadOutput, error) {
	client.mu.Lock()
	client.createCalls++
	client.mu.Unlock()
	return &s3.CreateMultipartUploadOutput{
		UploadId: aws.String("upload-id"),
	}, nil
}

func (client *failingS3MultipartUploadClient) UploadPart(
	_ context.Context,
	input *s3.UploadPartInput,
	_ ...func(*s3.Options),
) (*s3.UploadPartOutput, error) {
	if _, err := io.Copy(io.Discard, input.Body); err != nil {
		return nil, err
	}
	if aws.ToInt32(input.PartNumber) == 2 {
		return nil, client.partError
	}
	return &s3.UploadPartOutput{
		ETag: aws.String(
			fmt.Sprintf("etag-%d", aws.ToInt32(input.PartNumber)),
		),
	}, nil
}

func (client *failingS3MultipartUploadClient) CompleteMultipartUpload(
	_ context.Context,
	_ *s3.CompleteMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CompleteMultipartUploadOutput, error) {
	client.mu.Lock()
	client.completeCalls++
	client.mu.Unlock()
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (client *failingS3MultipartUploadClient) AbortMultipartUpload(
	_ context.Context,
	input *s3.AbortMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.AbortMultipartUploadOutput, error) {
	if aws.ToString(input.UploadId) != "upload-id" {
		return nil, fmt.Errorf(
			"unexpected multipart upload ID %q",
			aws.ToString(input.UploadId),
		)
	}
	client.mu.Lock()
	client.abortCalls++
	client.mu.Unlock()
	return &s3.AbortMultipartUploadOutput{}, nil
}
