package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	defaultS3AttachmentUploadPartSize    int64 = 8 << 20
	defaultS3AttachmentUploadConcurrency       = 2
	maxS3AttachmentUploadPartSize        int64 = 128 << 20
	maxS3AttachmentUploadConcurrency           = 16
	maxS3AttachmentUploadBufferBytes     int64 = 512 << 20
	maxS3AttachmentObjectKeyBytes              = 1024
)

const (
	S3AttachmentSSEBucketDefault   = "bucket-default"
	S3AttachmentSSEAES256          = "aes256"
	S3AttachmentSSEKMS             = "aws:kms"
	S3AttachmentVersioningAuto     = "auto"
	S3AttachmentVersioningRequired = "required"
	S3AttachmentVersioningDisabled = "disabled"
)

var ErrAttachmentVersionIDRequired = errors.New(
	"S3 attachment version ID is required",
)

// S3AttachmentStorageConfig configures a private S3-compatible attachment
// store. Endpoint is optional for AWS S3 and may identify MinIO or another
// operator-controlled S3 API. Object URLs and presigned URLs are intentionally
// not part of this contract.
type S3AttachmentStorageConfig struct {
	StoreID               string
	Region                string
	Bucket                string
	Prefix                string
	Endpoint              string
	UsePathStyle          bool
	AllowInsecureEndpoint bool

	// AccessKeyID and SecretAccessKey must either both be set or both be empty.
	// Empty values use the AWS SDK default credential chain.
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	ServerSideEncryption string
	KMSKeyID             string
	VersioningMode       string

	// UploadPartSize and UploadConcurrency bound multipart buffering. Zero uses
	// conservative defaults of 8 MiB and two workers.
	UploadPartSize    int64
	UploadConcurrency int
}

type s3AttachmentObjectClient interface {
	HeadBucket(
		context.Context,
		*s3.HeadBucketInput,
		...func(*s3.Options),
	) (*s3.HeadBucketOutput, error)
	GetBucketVersioning(
		context.Context,
		*s3.GetBucketVersioningInput,
		...func(*s3.Options),
	) (*s3.GetBucketVersioningOutput, error)
	HeadObject(
		context.Context,
		*s3.HeadObjectInput,
		...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
	ListObjectVersions(
		context.Context,
		*s3.ListObjectVersionsInput,
		...func(*s3.Options),
	) (*s3.ListObjectVersionsOutput, error)
	GetObject(
		context.Context,
		*s3.GetObjectInput,
		...func(*s3.Options),
	) (*s3.GetObjectOutput, error)
	DeleteObject(
		context.Context,
		*s3.DeleteObjectInput,
		...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
}

type s3AttachmentUploader interface {
	Upload(
		context.Context,
		*s3.PutObjectInput,
		...func(*manager.Uploader),
	) (*manager.UploadOutput, error)
}

type normalizedS3AttachmentStorageConfig struct {
	storeID               string
	region                string
	bucket                string
	prefix                string
	endpoint              string
	usePathStyle          bool
	allowInsecureEndpoint bool
	accessKeyID           string
	secretAccessKey       string
	sessionToken          string
	encryption            string
	kmsKeyID              string
	versioningMode        string
	uploadPartSize        int64
	uploadConcurrency     int
}

// S3AttachmentStorage implements AttachmentStorage over AWS S3, MinIO, and
// S3-compatible APIs. It stores only logical keys in ChronoDesk; bucket,
// endpoint, credentials, and configured prefixes remain private.
type S3AttachmentStorage struct {
	client            s3AttachmentObjectClient
	uploader          s3AttachmentUploader
	bucket            string
	prefix            string
	storeID           string
	versioned         bool
	encryption        string
	kmsKeyID          string
	uploadPartSize    int64
	uploadConcurrency int
}

var _ AttachmentStorage = (*S3AttachmentStorage)(nil)

// NewS3AttachmentStorage constructs the SDK client and verifies bucket access
// with HeadBucket before returning a usable store.
func NewS3AttachmentStorage(
	ctx context.Context,
	config S3AttachmentStorageConfig,
) (*S3AttachmentStorage, error) {
	if ctx == nil {
		return nil, fmt.Errorf("S3 attachment storage context is required")
	}
	normalized, err := normalizeS3AttachmentStorageConfig(config)
	if err != nil {
		return nil, err
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(normalized.region),
	}
	if normalized.accessKeyID != "" {
		loadOptions = append(
			loadOptions,
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(
					normalized.accessKeyID,
					normalized.secretAccessKey,
					normalized.sessionToken,
				),
			),
		)
	}
	sdkConfig, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load S3 attachment SDK configuration: %w", err)
	}
	client := s3.NewFromConfig(sdkConfig, func(options *s3.Options) {
		options.UsePathStyle = normalized.usePathStyle
		if normalized.endpoint != "" {
			options.BaseEndpoint = aws.String(normalized.endpoint)
		}
	})
	uploader := manager.NewUploader(client)
	return newS3AttachmentStorageWithClients(
		ctx,
		normalized,
		client,
		uploader,
	)
}

func newS3AttachmentStorageWithClients(
	ctx context.Context,
	config normalizedS3AttachmentStorageConfig,
	client s3AttachmentObjectClient,
	uploader s3AttachmentUploader,
) (*S3AttachmentStorage, error) {
	if ctx == nil {
		return nil, fmt.Errorf("S3 attachment storage context is required")
	}
	if client == nil || uploader == nil {
		return nil, fmt.Errorf("S3 attachment client and uploader are required")
	}
	if _, err := client.HeadBucket(
		ctx,
		&s3.HeadBucketInput{Bucket: aws.String(config.bucket)},
	); err != nil {
		return nil, fmt.Errorf("verify S3 attachment bucket access: %w", err)
	}
	versioning, err := client.GetBucketVersioning(
		ctx,
		&s3.GetBucketVersioningInput{Bucket: aws.String(config.bucket)},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"verify S3 attachment bucket versioning: %w",
			err,
		)
	}
	versioned := versioning != nil &&
		(versioning.Status == s3types.BucketVersioningStatusEnabled ||
			versioning.Status == s3types.BucketVersioningStatusSuspended)
	if versioning != nil &&
		versioning.Status != "" &&
		!versioned {
		return nil, fmt.Errorf(
			"unsupported S3 attachment bucket versioning status %q",
			versioning.Status,
		)
	}
	switch config.versioningMode {
	case S3AttachmentVersioningRequired:
		if !versioned {
			return nil, fmt.Errorf(
				"S3 attachment bucket versioning is required",
			)
		}
	case S3AttachmentVersioningDisabled:
		if versioned {
			return nil, fmt.Errorf(
				"S3 attachment bucket versioning must be disabled",
			)
		}
	}
	return &S3AttachmentStorage{
		client:            client,
		uploader:          uploader,
		bucket:            config.bucket,
		prefix:            config.prefix,
		storeID:           config.storeID,
		versioned:         versioned,
		encryption:        config.encryption,
		kmsKeyID:          config.kmsKeyID,
		uploadPartSize:    config.uploadPartSize,
		uploadConcurrency: config.uploadConcurrency,
	}, nil
}

func normalizeS3AttachmentStorageConfig(
	config S3AttachmentStorageConfig,
) (normalizedS3AttachmentStorageConfig, error) {
	result := normalizedS3AttachmentStorageConfig{
		storeID:               normalizeAttachmentStoreID(config.StoreID),
		region:                strings.TrimSpace(config.Region),
		bucket:                strings.TrimSpace(config.Bucket),
		endpoint:              strings.TrimSpace(config.Endpoint),
		usePathStyle:          config.UsePathStyle,
		allowInsecureEndpoint: config.AllowInsecureEndpoint,
		accessKeyID:           strings.TrimSpace(config.AccessKeyID),
		secretAccessKey:       config.SecretAccessKey,
		sessionToken:          config.SessionToken,
		encryption:            strings.ToLower(strings.TrimSpace(config.ServerSideEncryption)),
		kmsKeyID:              strings.TrimSpace(config.KMSKeyID),
		versioningMode:        strings.ToLower(strings.TrimSpace(config.VersioningMode)),
		uploadPartSize:        config.UploadPartSize,
		uploadConcurrency:     config.UploadConcurrency,
	}
	if !validAttachmentStoreID(result.storeID) {
		return result, fmt.Errorf("S3 attachment store_id is invalid")
	}
	if result.region == "" {
		return result, fmt.Errorf("S3 attachment region is required")
	}
	if err := validateS3BucketName(result.bucket); err != nil {
		return result, err
	}
	prefix, err := normalizeS3AttachmentPrefix(config.Prefix)
	if err != nil {
		return result, err
	}
	result.prefix = prefix
	if result.endpoint != "" {
		endpoint, endpointErr := validateS3AttachmentEndpoint(
			result.endpoint,
			result.allowInsecureEndpoint,
		)
		if endpointErr != nil {
			return result, endpointErr
		}
		result.endpoint = endpoint
	}

	hasAccessKey := result.accessKeyID != ""
	hasSecretKey := strings.TrimSpace(result.secretAccessKey) != ""
	if hasAccessKey != hasSecretKey {
		return result, fmt.Errorf(
			"S3 attachment static credentials require both access key ID and secret access key",
		)
	}
	if !hasAccessKey && strings.TrimSpace(result.sessionToken) != "" {
		return result, fmt.Errorf(
			"S3 attachment session token requires static access key credentials",
		)
	}

	if result.encryption == "" {
		result.encryption = S3AttachmentSSEBucketDefault
	}
	switch result.encryption {
	case S3AttachmentSSEBucketDefault, S3AttachmentSSEAES256:
		if result.kmsKeyID != "" {
			return result, fmt.Errorf(
				"S3 attachment KMS key requires aws:kms server-side encryption",
			)
		}
	case S3AttachmentSSEKMS:
	default:
		return result, fmt.Errorf(
			"unsupported S3 attachment server-side encryption %q",
			result.encryption,
		)
	}
	if result.versioningMode == "" {
		result.versioningMode = S3AttachmentVersioningAuto
	}
	switch result.versioningMode {
	case S3AttachmentVersioningAuto,
		S3AttachmentVersioningRequired,
		S3AttachmentVersioningDisabled:
	default:
		return result, fmt.Errorf(
			"unsupported S3 attachment versioning mode %q",
			result.versioningMode,
		)
	}

	if result.uploadPartSize == 0 {
		result.uploadPartSize = defaultS3AttachmentUploadPartSize
	}
	if result.uploadPartSize < manager.MinUploadPartSize ||
		result.uploadPartSize > maxS3AttachmentUploadPartSize {
		return result, fmt.Errorf(
			"S3 attachment upload part size must be between %d and %d bytes",
			manager.MinUploadPartSize,
			maxS3AttachmentUploadPartSize,
		)
	}
	if result.uploadConcurrency == 0 {
		result.uploadConcurrency = defaultS3AttachmentUploadConcurrency
	}
	if result.uploadConcurrency < 1 ||
		result.uploadConcurrency > maxS3AttachmentUploadConcurrency {
		return result, fmt.Errorf(
			"S3 attachment upload concurrency must be between 1 and %d",
			maxS3AttachmentUploadConcurrency,
		)
	}
	// The manager keeps at most Concurrency+1 part buffers for a streaming
	// reader. Enforce a hard aggregate ceiling even for operator overrides.
	if result.uploadPartSize >
		maxS3AttachmentUploadBufferBytes/
			int64(result.uploadConcurrency+1) {
		return result, fmt.Errorf(
			"S3 attachment multipart buffering must not exceed %d bytes",
			maxS3AttachmentUploadBufferBytes,
		)
	}
	return result, nil
}

func validateS3BucketName(bucket string) error {
	if bucket == "" || len(bucket) > 255 || !utf8.ValidString(bucket) ||
		bucket != strings.TrimSpace(bucket) ||
		strings.ContainsAny(bucket, `/\`) {
		return fmt.Errorf("S3 attachment bucket is invalid")
	}
	for _, character := range bucket {
		if unicode.IsControl(character) {
			return fmt.Errorf("S3 attachment bucket is invalid")
		}
	}
	return nil
}

func normalizeS3AttachmentPrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	normalized, err := normalizeS3AttachmentLogicalKey(prefix)
	if err != nil {
		return "", fmt.Errorf("invalid S3 attachment prefix: %w", err)
	}
	return normalized, nil
}

func validateS3AttachmentEndpoint(
	endpoint string,
	allowInsecure bool,
) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("S3 attachment endpoint must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !allowInsecure {
		return "", fmt.Errorf(
			"S3 attachment HTTP endpoint requires explicit insecure-endpoint opt-in",
		)
	}
	return strings.TrimRight(endpoint, "/"), nil
}

func normalizeS3AttachmentLogicalKey(key string) (string, error) {
	if !utf8.ValidString(key) {
		return "", ErrInvalidAttachmentName
	}
	normalized, err := normalizeLocalAttachmentKey(key)
	if err != nil {
		return "", err
	}
	for _, character := range normalized {
		if unicode.IsControl(character) {
			return "", ErrInvalidAttachmentName
		}
	}
	return normalized, nil
}

func (storage *S3AttachmentStorage) objectKey(
	logicalKey string,
) (string, string, error) {
	normalized, err := normalizeS3AttachmentLogicalKey(logicalKey)
	if err != nil {
		return "", "", err
	}
	objectKey := normalized
	if storage.prefix != "" {
		objectKey = storage.prefix + "/" + normalized
	}
	if len(objectKey) > maxS3AttachmentObjectKeyBytes {
		return "", "", ErrInvalidAttachmentName
	}
	return normalized, objectKey, nil
}

func (storage *S3AttachmentStorage) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if storage == nil || storage.client == nil || storage.uploader == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"S3 attachment write",
	); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("attachment reader is required")
	}
	if maxBytes <= 0 {
		return nil, ErrAttachmentTooLarge
	}
	logicalKey, objectKey, err := storage.objectKey(key)
	if err != nil {
		return nil, err
	}

	tracker := newS3AttachmentUploadReader(ctx, reader, maxBytes)
	sample, err := prefetchS3AttachmentSample(tracker)
	if err != nil {
		if tracker.oversizedValue() {
			return nil, ErrAttachmentTooLarge
		}
		return nil, fmt.Errorf("read S3 attachment sample: %w", err)
	}
	body := io.MultiReader(bytes.NewReader(sample), tracker)
	input := &s3.PutObjectInput{
		Bucket:      aws.String(storage.bucket),
		Key:         aws.String(objectKey),
		Body:        body,
		ContentType: aws.String(http.DetectContentType(sample)),
	}
	switch storage.encryption {
	case S3AttachmentSSEAES256:
		input.ServerSideEncryption =
			s3types.ServerSideEncryptionAes256
	case S3AttachmentSSEKMS:
		input.ServerSideEncryption =
			s3types.ServerSideEncryptionAwsKms
		if storage.kmsKeyID != "" {
			input.SSEKMSKeyId = aws.String(storage.kmsKeyID)
		}
	}
	uploadOutput, uploadErr := storage.uploader.Upload(
		ctx,
		input,
		func(options *manager.Uploader) {
			options.PartSize = storage.uploadPartSize
			options.Concurrency = storage.uploadConcurrency
			options.LeavePartsOnError = false
			options.MaxUploadParts = manager.MaxUploadParts
			options.DisableValidateParts = false
		},
	)
	snapshot := tracker.snapshot()
	if snapshot.oversized {
		return nil, ErrAttachmentTooLarge
	}
	if uploadErr != nil {
		return nil, fmt.Errorf("upload S3 attachment: %w", uploadErr)
	}
	if !snapshot.complete {
		return nil, fmt.Errorf(
			"%w: S3 uploader returned before consuming the attachment",
			ErrInvalidAttachment,
		)
	}
	versionID := ""
	if uploadOutput != nil {
		versionID = aws.ToString(uploadOutput.VersionID)
	}
	if storage.versioned && !validS3AttachmentVersionID(versionID) {
		return nil, fmt.Errorf(
			"%w: store %q did not return a version ID",
			ErrAttachmentVersionIDRequired,
			storage.storeID,
		)
	}
	return &StoredAttachmentObject{
		Key:                 logicalKey,
		Size:                snapshot.size,
		SHA256:              snapshot.sha256,
		DetectedContentType: http.DetectContentType(snapshot.sample),
		StorageType:         "s3",
		StoreID:             storage.storeID,
		VersionID:           versionID,
	}, nil
}

func (storage *S3AttachmentStorage) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	return storage.openVersion(ctx, key, "", false)
}

// CurrentVersion resolves the exact provider generation for an interrupted
// write whose Put receipt could not be durably recorded. It is used only by
// orphan recovery; normal reads always use the version stored with the
// authoritative business record.
func (storage *S3AttachmentStorage) CurrentVersion(
	ctx context.Context,
	key string,
) (string, error) {
	if storage == nil || storage.client == nil {
		return "", ErrAttachmentStorageMissing
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"S3 attachment version resolve",
	); err != nil {
		return "", err
	}
	_, objectKey, err := storage.objectKey(key)
	if err != nil {
		return "", err
	}
	output, err := storage.client.HeadObject(
		ctx,
		&s3.HeadObjectInput{
			Bucket: aws.String(storage.bucket),
			Key:    aws.String(objectKey),
		},
	)
	if err != nil {
		if isS3AttachmentObjectMissing(err) {
			return "", ErrAttachmentStoredObjectNotFound
		}
		return "", fmt.Errorf("resolve S3 attachment version: %w", err)
	}
	if !storage.versioned {
		return "", nil
	}
	versionID := ""
	if output != nil {
		versionID = aws.ToString(output.VersionId)
	}
	if !validS3AttachmentVersionID(versionID) {
		return "", ErrAttachmentVersionIDRequired
	}
	return versionID, nil
}

func (storage *S3AttachmentStorage) ListObjectVersions(
	ctx context.Context,
	key string,
	limit int,
) ([]string, bool, error) {
	if storage == nil || storage.client == nil {
		return nil, false, ErrAttachmentStorageMissing
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"S3 attachment version enumeration",
	); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > knowledgeObjectRecoveryMaxVersions {
		return nil, false, ErrInvalidAttachment
	}
	if !storage.versioned {
		return []string{}, false, nil
	}
	_, objectKey, err := storage.objectKey(key)
	if err != nil {
		return nil, false, err
	}
	output, err := storage.client.ListObjectVersions(
		ctx,
		&s3.ListObjectVersionsInput{
			Bucket:  aws.String(storage.bucket),
			Prefix:  aws.String(objectKey),
			MaxKeys: aws.Int32(int32(limit)),
		},
	)
	if err != nil {
		return nil, false, fmt.Errorf(
			"list S3 attachment versions: %w",
			err,
		)
	}
	if output == nil {
		return []string{}, false, nil
	}
	versions := make([]string, 0, len(output.Versions)+len(output.DeleteMarkers))
	seen := make(map[string]struct{}, cap(versions))
	appendVersion := func(candidateKey string, versionID string) error {
		if candidateKey != objectKey {
			return nil
		}
		if !validS3AttachmentVersionID(versionID) {
			return ErrAttachmentVersionIDRequired
		}
		if _, exists := seen[versionID]; exists {
			return nil
		}
		seen[versionID] = struct{}{}
		versions = append(versions, versionID)
		return nil
	}
	for _, version := range output.Versions {
		if err := appendVersion(
			aws.ToString(version.Key),
			aws.ToString(version.VersionId),
		); err != nil {
			return nil, false, err
		}
	}
	for _, marker := range output.DeleteMarkers {
		if err := appendVersion(
			aws.ToString(marker.Key),
			aws.ToString(marker.VersionId),
		); err != nil {
			return nil, false, err
		}
	}
	return versions, aws.ToBool(output.IsTruncated), nil
}

func (storage *S3AttachmentStorage) OpenVersion(
	ctx context.Context,
	key string,
	versionID string,
) (io.ReadCloser, error) {
	return storage.openVersion(ctx, key, versionID, true)
}

func (storage *S3AttachmentStorage) openVersion(
	ctx context.Context,
	key string,
	versionID string,
	requireExact bool,
) (io.ReadCloser, error) {
	if storage == nil || storage.client == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"S3 attachment read",
	); err != nil {
		return nil, err
	}
	_, objectKey, err := storage.objectKey(key)
	if err != nil {
		return nil, err
	}
	if requireExact &&
		storage.versioned &&
		!validS3AttachmentVersionID(versionID) {
		return nil, ErrAttachmentVersionIDRequired
	}
	if versionID != "" && !validS3AttachmentVersionID(versionID) {
		return nil, ErrInvalidAttachment
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(objectKey),
	}
	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	output, err := storage.client.GetObject(
		ctx,
		input,
	)
	if err != nil {
		return nil, fmt.Errorf("open S3 attachment: %w", err)
	}
	if output == nil || output.Body == nil {
		return nil, fmt.Errorf("open S3 attachment: empty object response")
	}
	return output.Body, nil
}

func (storage *S3AttachmentStorage) Delete(
	ctx context.Context,
	key string,
) error {
	return storage.DeleteVersion(ctx, key, "")
}

func (storage *S3AttachmentStorage) DeleteVersion(
	ctx context.Context,
	key string,
	versionID string,
) error {
	if storage == nil || storage.client == nil {
		return ErrAttachmentStorageMissing
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"S3 attachment delete",
	); err != nil {
		return err
	}
	_, objectKey, err := storage.objectKey(key)
	if err != nil {
		return err
	}
	if storage.versioned && !validS3AttachmentVersionID(versionID) {
		return ErrAttachmentVersionIDRequired
	}
	if versionID != "" && !validS3AttachmentVersionID(versionID) {
		return ErrInvalidAttachment
	}
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(storage.bucket),
		Key:    aws.String(objectKey),
	}
	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}
	if _, err := storage.client.DeleteObject(
		ctx,
		input,
	); err != nil {
		if isS3AttachmentObjectMissing(err) {
			return nil
		}
		return fmt.Errorf("delete S3 attachment: %w", err)
	}
	return nil
}

func isS3AttachmentObjectMissing(err error) bool {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch strings.ToLower(strings.TrimSpace(apiError.ErrorCode())) {
		case "nosuchkey", "nosuchversion", "notfound", "404":
			return true
		}
	}
	var responseError *smithyhttp.ResponseError
	return errors.As(err, &responseError) &&
		responseError.HTTPStatusCode() == http.StatusNotFound
}

func (*S3AttachmentStorage) AttachmentStorageType() string {
	return "s3"
}

func (storage *S3AttachmentStorage) AttachmentStoreID() string {
	if storage == nil {
		return ""
	}
	return storage.storeID
}

func (storage *S3AttachmentStorage) ObjectVersioningEnabled() bool {
	return storage != nil && storage.versioned
}

func validS3AttachmentVersionID(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

type s3AttachmentUploadSnapshot struct {
	size      int64
	sha256    string
	sample    []byte
	complete  bool
	oversized bool
}

type s3AttachmentUploadReader struct {
	mu        sync.Mutex
	ctx       context.Context
	source    io.Reader
	maxBytes  int64
	size      int64
	hash      hash.Hash
	sample    []byte
	complete  bool
	oversized bool
}

func newS3AttachmentUploadReader(
	ctx context.Context,
	source io.Reader,
	maxBytes int64,
) *s3AttachmentUploadReader {
	return &s3AttachmentUploadReader{
		ctx:      ctx,
		source:   source,
		maxBytes: maxBytes,
		hash:     sha256.New(),
		sample:   make([]byte, 0, 512),
	}
}

func (reader *s3AttachmentUploadReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()

	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.complete {
		return 0, io.EOF
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	remaining := reader.maxBytes - reader.size
	if remaining < 0 {
		reader.oversized = true
		return 0, ErrAttachmentTooLarge
	}
	readLength := int64(len(buffer))
	if remaining < readLength {
		readLength = remaining + 1
	}
	count, err := reader.source.Read(buffer[:int(readLength)])
	allowed := count
	if int64(allowed) > remaining {
		allowed = int(remaining)
		reader.oversized = true
	}
	if allowed > 0 {
		reader.size += int64(allowed)
		_, _ = reader.hash.Write(buffer[:allowed])
		if len(reader.sample) < cap(reader.sample) {
			sampleLength := cap(reader.sample) - len(reader.sample)
			if sampleLength > allowed {
				sampleLength = allowed
			}
			reader.sample = append(
				reader.sample,
				buffer[:sampleLength]...,
			)
		}
	}
	if reader.oversized {
		return allowed, ErrAttachmentTooLarge
	}
	if err == io.EOF {
		reader.complete = true
	}
	return allowed, err
}

func (reader *s3AttachmentUploadReader) oversizedValue() bool {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.oversized
}

func (reader *s3AttachmentUploadReader) snapshot() s3AttachmentUploadSnapshot {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return s3AttachmentUploadSnapshot{
		size:      reader.size,
		sha256:    hex.EncodeToString(reader.hash.Sum(nil)),
		sample:    append([]byte(nil), reader.sample...),
		complete:  reader.complete,
		oversized: reader.oversized,
	}
}

func prefetchS3AttachmentSample(
	reader *s3AttachmentUploadReader,
) ([]byte, error) {
	sample := make([]byte, 512)
	count, err := io.ReadFull(reader, sample)
	switch err {
	case nil, io.EOF, io.ErrUnexpectedEOF:
		return sample[:count], nil
	default:
		return nil, err
	}
}
