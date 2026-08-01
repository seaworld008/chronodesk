package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var attachmentStoreIDPattern = regexp.MustCompile(
	`^[a-z][a-z0-9-]{0,62}$`,
)

var ErrAttachmentStoredObjectNotFound = errors.New(
	"stored attachment object was not found",
)

// AttachmentStoredReference is private control data. It identifies one exact
// immutable object generation without exposing provider topology to protocol
// responses.
type AttachmentStoredReference struct {
	StorageType string
	StoreID     string
	Key         string
	VersionID   string
}

// TypedAttachmentStorage preserves access to objects written by an earlier
// configured backend while new objects are written to the current primary
// backend. Provider URLs and credentials remain entirely behind this boundary.
type TypedAttachmentStorage interface {
	AttachmentStorage
	OpenStored(
		ctx context.Context,
		storageType string,
		key string,
	) (io.ReadCloser, error)
	DeleteStored(
		ctx context.Context,
		storageType string,
		key string,
	) error
}

// ReferencedAttachmentStorage is the generation-safe storage seam used by
// attachment upload, download, and cleanup. New records always use StoreID;
// legacy storage_type aliases are accepted only when they resolve to exactly
// one registered store.
type ReferencedAttachmentStorage interface {
	AttachmentStorage
	PrimaryAttachmentStoreID() string
	PutStored(
		ctx context.Context,
		storeID string,
		key string,
		reader io.Reader,
		maxBytes int64,
	) (*StoredAttachmentObject, error)
	OpenStoredObject(
		ctx context.Context,
		reference AttachmentStoredReference,
	) (io.ReadCloser, error)
	DeleteStoredObject(
		ctx context.Context,
		reference AttachmentStoredReference,
	) error
}

type VersionedAttachmentStorage interface {
	AttachmentStorage
	OpenVersion(
		ctx context.Context,
		key string,
		versionID string,
	) (io.ReadCloser, error)
	DeleteVersion(
		ctx context.Context,
		key string,
		versionID string,
	) error
}

// CurrentVersionAttachmentStorage closes the recovery gap where a versioned
// object store accepted a write but the process terminated before its
// provider-generated version ID was persisted. Keys are immutable UUID-based
// knowledge/attachment keys, so resolving the current generation is safe only
// for the private orphan-recovery path.
type CurrentVersionAttachmentStorage interface {
	CurrentVersion(
		ctx context.Context,
		key string,
	) (string, error)
}

type AttachmentStorageVersioning interface {
	ObjectVersioningEnabled() bool
}

// ObjectVersionListStorage is restricted to durable orphan recovery. The
// caller owns a UUID-derived key through a PostgreSQL intent and therefore may
// enumerate every provider generation for that one exact logical key. Returned
// identifiers must be deleted one by one through DeleteVersion.
type ObjectVersionListStorage interface {
	ListObjectVersions(
		ctx context.Context,
		key string,
		limit int,
	) ([]string, bool, error)
}

type AttachmentStorageRegistration struct {
	StoreID     string
	StorageType string
	Storage     AttachmentStorage
}

// AttachmentStorageRouter writes through one primary backend and routes reads
// and cleanup by the immutable store_id persisted with each attachment.
type AttachmentStorageRouter struct {
	primary     AttachmentStorage
	primaryType string
	primaryID   string
	backends    map[string]AttachmentStorageRegistration
	byType      map[string][]string
}

func NewAttachmentStorageRouter(
	primary AttachmentStorage,
	aliases map[string]AttachmentStorage,
) (*AttachmentStorageRouter, error) {
	if primary == nil {
		return nil, ErrAttachmentStorageMissing
	}
	primaryType := attachmentStorageType(primary)
	if primaryType == "managed" {
		return nil, fmt.Errorf(
			"primary attachment storage must declare its backend type",
		)
	}
	registrations := []AttachmentStorageRegistration{{
		StoreID:     primaryType,
		StorageType: primaryType,
		Storage:     primary,
	}}
	for alias, storage := range aliases {
		registrations = append(
			registrations,
			AttachmentStorageRegistration{
				StoreID:     strings.ToLower(strings.TrimSpace(alias)),
				StorageType: strings.ToLower(strings.TrimSpace(alias)),
				Storage:     storage,
			},
		)
	}
	return NewAttachmentStorageRouterWithRegistry(
		primaryType,
		registrations,
	)
}

func NewAttachmentStorageRouterWithRegistry(
	primaryStoreID string,
	registrations []AttachmentStorageRegistration,
) (*AttachmentStorageRouter, error) {
	primaryStoreID = normalizeAttachmentStoreID(primaryStoreID)
	if !validAttachmentStoreID(primaryStoreID) {
		return nil, fmt.Errorf("primary attachment store_id is invalid")
	}
	backends := make(
		map[string]AttachmentStorageRegistration,
		len(registrations),
	)
	byType := make(map[string][]string)
	for _, registration := range registrations {
		registration.StoreID = normalizeAttachmentStoreID(
			registration.StoreID,
		)
		registration.StorageType = strings.ToLower(
			strings.TrimSpace(registration.StorageType),
		)
		if !validAttachmentStoreID(registration.StoreID) ||
			registration.StorageType == "" ||
			registration.Storage == nil {
			return nil, fmt.Errorf(
				"attachment storage registrations require a valid store_id, type, and backend",
			)
		}
		if _, ok := backends[registration.StoreID]; ok {
			return nil, fmt.Errorf(
				"attachment store_id %q is already registered",
				registration.StoreID,
			)
		}
		backends[registration.StoreID] = registration
		byType[registration.StorageType] = append(
			byType[registration.StorageType],
			registration.StoreID,
		)
	}
	primary, ok := backends[primaryStoreID]
	if !ok {
		return nil, fmt.Errorf(
			"primary attachment store_id %q is not registered",
			primaryStoreID,
		)
	}
	return &AttachmentStorageRouter{
		primary:     primary.Storage,
		primaryType: primary.StorageType,
		primaryID:   primary.StoreID,
		backends:    backends,
		byType:      byType,
	}, nil
}

func (r *AttachmentStorageRouter) AttachmentStorageType() string {
	if r == nil {
		return "managed"
	}
	return r.primaryType
}

func (r *AttachmentStorageRouter) PrimaryAttachmentStoreID() string {
	if r == nil {
		return ""
	}
	return r.primaryID
}

func (r *AttachmentStorageRouter) Put(
	ctx context.Context,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if r == nil {
		return nil, ErrAttachmentStorageMissing
	}
	return r.PutStored(ctx, r.primaryID, key, reader, maxBytes)
}

func (r *AttachmentStorageRouter) PutStored(
	ctx context.Context,
	storeID string,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	registration, err := r.registration(storeID, "")
	if err != nil {
		return nil, err
	}
	stored, err := registration.Storage.Put(
		ctx,
		key,
		reader,
		maxBytes,
	)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, ErrInvalidAttachment
	}
	stored.StoreID = registration.StoreID
	stored.StorageType = registration.StorageType
	return stored, nil
}

func (r *AttachmentStorageRouter) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	if r == nil || r.primary == nil {
		return nil, ErrAttachmentStorageMissing
	}
	return r.primary.Open(ctx, key)
}

func (r *AttachmentStorageRouter) Delete(
	ctx context.Context,
	key string,
) error {
	if r == nil {
		return ErrAttachmentStorageMissing
	}
	return r.DeleteStoredObject(ctx, AttachmentStoredReference{
		StorageType: r.primaryType,
		StoreID:     r.primaryID,
		Key:         key,
	})
}

func (r *AttachmentStorageRouter) OpenStored(
	ctx context.Context,
	storageType string,
	key string,
) (io.ReadCloser, error) {
	registration, err := r.registration("", storageType)
	if err != nil {
		return nil, err
	}
	return registration.Storage.Open(ctx, key)
}

func (r *AttachmentStorageRouter) DeleteStored(
	ctx context.Context,
	storageType string,
	key string,
) error {
	return r.DeleteStoredObject(ctx, AttachmentStoredReference{
		StorageType: storageType,
		Key:         key,
	})
}

func (r *AttachmentStorageRouter) OpenStoredObject(
	ctx context.Context,
	reference AttachmentStoredReference,
) (io.ReadCloser, error) {
	registration, err := r.registration(
		reference.StoreID,
		reference.StorageType,
	)
	if err != nil {
		return nil, err
	}
	if versioned, ok := registration.Storage.(VersionedAttachmentStorage); ok {
		return versioned.OpenVersion(
			ctx,
			reference.Key,
			reference.VersionID,
		)
	}
	if reference.VersionID != "" {
		return nil, fmt.Errorf(
			"%w: store %q does not support object versions",
			ErrAttachmentStorageMissing,
			registration.StoreID,
		)
	}
	return registration.Storage.Open(ctx, reference.Key)
}

func (r *AttachmentStorageRouter) DeleteStoredObject(
	ctx context.Context,
	reference AttachmentStoredReference,
) error {
	registration, err := r.registration(
		reference.StoreID,
		reference.StorageType,
	)
	if err != nil {
		return err
	}
	if versioned, ok := registration.Storage.(VersionedAttachmentStorage); ok {
		return versioned.DeleteVersion(
			ctx,
			reference.Key,
			reference.VersionID,
		)
	}
	if reference.VersionID != "" {
		return fmt.Errorf(
			"%w: store %q does not support object versions",
			ErrAttachmentStorageMissing,
			registration.StoreID,
		)
	}
	return registration.Storage.Delete(ctx, reference.Key)
}

// ResolveStoredObjectVersion returns a deletion-safe exact reference. It never
// falls back to the current primary store: StoreID remains the durable routing
// identity across bucket, prefix, provider and credential rotation.
func (r *AttachmentStorageRouter) ResolveStoredObjectVersion(
	ctx context.Context,
	reference AttachmentStoredReference,
) (AttachmentStoredReference, error) {
	registration, err := r.registration(
		reference.StoreID,
		reference.StorageType,
	)
	if err != nil {
		return AttachmentStoredReference{}, err
	}
	reference.StoreID = registration.StoreID
	reference.StorageType = registration.StorageType
	if reference.VersionID != "" {
		return reference, nil
	}
	if _, versioned := registration.Storage.(VersionedAttachmentStorage); !versioned {
		return reference, nil
	}
	if capability, ok := registration.Storage.(AttachmentStorageVersioning); ok &&
		!capability.ObjectVersioningEnabled() {
		return reference, nil
	}
	resolver, ok := registration.Storage.(CurrentVersionAttachmentStorage)
	if !ok {
		return AttachmentStoredReference{}, fmt.Errorf(
			"%w: store %q cannot resolve an interrupted object generation",
			ErrAttachmentStorageMissing,
			registration.StoreID,
		)
	}
	versionID, err := resolver.CurrentVersion(ctx, reference.Key)
	if err != nil {
		return AttachmentStoredReference{}, err
	}
	if strings.TrimSpace(versionID) == "" {
		return AttachmentStoredReference{}, ErrAttachmentStoredObjectNotFound
	}
	reference.VersionID = versionID
	return reference, nil
}

func (r *AttachmentStorageRouter) ListStoredObjectVersions(
	ctx context.Context,
	reference AttachmentStoredReference,
	limit int,
) ([]string, bool, error) {
	registration, err := r.registration(
		reference.StoreID,
		reference.StorageType,
	)
	if err != nil {
		return nil, false, err
	}
	if capability, ok := registration.Storage.(AttachmentStorageVersioning); ok &&
		!capability.ObjectVersioningEnabled() {
		return []string{}, false, nil
	}
	lister, ok := registration.Storage.(ObjectVersionListStorage)
	if !ok {
		return nil, false, fmt.Errorf(
			"%w: store %q cannot enumerate interrupted object generations",
			ErrAttachmentStorageMissing,
			registration.StoreID,
		)
	}
	return lister.ListObjectVersions(ctx, reference.Key, limit)
}

func (r *AttachmentStorageRouter) StoredObjectVersioningEnabled(
	reference AttachmentStoredReference,
) (bool, error) {
	registration, err := r.registration(
		reference.StoreID,
		reference.StorageType,
	)
	if err != nil {
		return false, err
	}
	if capability, ok := registration.Storage.(AttachmentStorageVersioning); ok {
		return capability.ObjectVersioningEnabled(), nil
	}
	_, versioned := registration.Storage.(VersionedAttachmentStorage)
	return versioned, nil
}

func (r *AttachmentStorageRouter) registration(
	storeID string,
	storageType string,
) (AttachmentStorageRegistration, error) {
	if r == nil || r.primary == nil {
		return AttachmentStorageRegistration{},
			ErrAttachmentStorageMissing
	}
	storeID = normalizeAttachmentStoreID(storeID)
	storageType = strings.ToLower(strings.TrimSpace(storageType))
	if storeID != "" {
		registration, ok := r.backends[storeID]
		if !ok {
			return AttachmentStorageRegistration{}, fmt.Errorf(
				"%w: attachment store_id %q is not configured",
				ErrAttachmentStorageMissing,
				storeID,
			)
		}
		if storageType != "" &&
			storageType != "managed" &&
			registration.StorageType != storageType {
			return AttachmentStorageRegistration{},
				ErrInvalidAttachment
		}
		return registration, nil
	}
	if storageType == "" || storageType == "managed" {
		return AttachmentStorageRegistration{}, fmt.Errorf(
			"%w: legacy attachment storage reference has no backend",
			ErrAttachmentStorageMissing,
		)
	}
	candidates := r.byType[storageType]
	if len(candidates) != 1 {
		return AttachmentStorageRegistration{}, fmt.Errorf(
			"%w: legacy attachment backend %q resolves to %d stores",
			ErrAttachmentStorageMissing,
			storageType,
			len(candidates),
		)
	}
	return r.backends[candidates[0]], nil
}

func normalizeAttachmentStoreID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validAttachmentStoreID(value string) bool {
	return attachmentStoreIDPattern.MatchString(value)
}

func attachmentStorageStoreID(storage AttachmentStorage) string {
	if storage == nil {
		return ""
	}
	if routed, ok := storage.(ReferencedAttachmentStorage); ok {
		return normalizeAttachmentStoreID(
			routed.PrimaryAttachmentStoreID(),
		)
	}
	type storeIdentifier interface {
		AttachmentStoreID() string
	}
	if identified, ok := storage.(storeIdentifier); ok {
		if storeID := normalizeAttachmentStoreID(
			identified.AttachmentStoreID(),
		); validAttachmentStoreID(storeID) {
			return storeID
		}
	}
	storageType := attachmentStorageType(storage)
	if storageType == "managed" {
		return ""
	}
	return storageType + "-default"
}

func putAttachmentInStore(
	ctx context.Context,
	storage AttachmentStorage,
	storeID string,
	key string,
	reader io.Reader,
	maxBytes int64,
) (*StoredAttachmentObject, error) {
	if storage == nil {
		return nil, ErrAttachmentStorageMissing
	}
	storeID = normalizeAttachmentStoreID(storeID)
	if !validAttachmentStoreID(storeID) {
		return nil, ErrInvalidAttachment
	}
	if routed, ok := storage.(ReferencedAttachmentStorage); ok {
		return routed.PutStored(
			ctx,
			storeID,
			key,
			reader,
			maxBytes,
		)
	}
	if attachmentStorageStoreID(storage) != storeID {
		return nil, fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			storeID,
		)
	}
	stored, err := storage.Put(ctx, key, reader, maxBytes)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, ErrInvalidAttachment
	}
	stored.StoreID = storeID
	stored.StorageType = attachmentStorageType(storage)
	return stored, nil
}

func deleteAttachmentStoredObject(
	ctx context.Context,
	storage AttachmentStorage,
	reference AttachmentStoredReference,
) error {
	if storage == nil {
		return ErrAttachmentStorageMissing
	}
	if routed, ok := storage.(ReferencedAttachmentStorage); ok {
		return routed.DeleteStoredObject(ctx, reference)
	}
	if reference.StoreID != attachmentStorageStoreID(storage) ||
		(reference.StorageType != "" &&
			reference.StorageType != attachmentStorageType(storage)) {
		return fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	if versioned, ok := storage.(VersionedAttachmentStorage); ok {
		return versioned.DeleteVersion(
			ctx,
			reference.Key,
			reference.VersionID,
		)
	}
	if reference.VersionID != "" {
		return ErrInvalidAttachment
	}
	return storage.Delete(ctx, reference.Key)
}

func resolveAttachmentStoredObjectVersion(
	ctx context.Context,
	storage AttachmentStorage,
	reference AttachmentStoredReference,
) (AttachmentStoredReference, error) {
	if storage == nil {
		return AttachmentStoredReference{}, ErrAttachmentStorageMissing
	}
	if routed, ok := storage.(*AttachmentStorageRouter); ok {
		return routed.ResolveStoredObjectVersion(ctx, reference)
	}
	if reference.StoreID != attachmentStorageStoreID(storage) ||
		(reference.StorageType != "" &&
			reference.StorageType != attachmentStorageType(storage)) {
		return AttachmentStoredReference{}, fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	reference.StoreID = attachmentStorageStoreID(storage)
	reference.StorageType = attachmentStorageType(storage)
	if reference.VersionID != "" {
		return reference, nil
	}
	if _, versioned := storage.(VersionedAttachmentStorage); !versioned {
		return reference, nil
	}
	if capability, ok := storage.(AttachmentStorageVersioning); ok &&
		!capability.ObjectVersioningEnabled() {
		return reference, nil
	}
	resolver, ok := storage.(CurrentVersionAttachmentStorage)
	if !ok {
		return AttachmentStoredReference{}, fmt.Errorf(
			"%w: store %q cannot resolve an interrupted object generation",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	versionID, err := resolver.CurrentVersion(ctx, reference.Key)
	if err != nil {
		return AttachmentStoredReference{}, err
	}
	if strings.TrimSpace(versionID) == "" {
		return AttachmentStoredReference{}, ErrAttachmentStoredObjectNotFound
	}
	reference.VersionID = versionID
	return reference, nil
}

func listAttachmentStoredObjectVersions(
	ctx context.Context,
	storage AttachmentStorage,
	reference AttachmentStoredReference,
	limit int,
) ([]string, bool, error) {
	if storage == nil {
		return nil, false, ErrAttachmentStorageMissing
	}
	if routed, ok := storage.(*AttachmentStorageRouter); ok {
		return routed.ListStoredObjectVersions(ctx, reference, limit)
	}
	if reference.StoreID != attachmentStorageStoreID(storage) ||
		(reference.StorageType != "" &&
			reference.StorageType != attachmentStorageType(storage)) {
		return nil, false, fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	if capability, ok := storage.(AttachmentStorageVersioning); ok &&
		!capability.ObjectVersioningEnabled() {
		return []string{}, false, nil
	}
	lister, ok := storage.(ObjectVersionListStorage)
	if !ok {
		return nil, false, fmt.Errorf(
			"%w: store %q cannot enumerate interrupted object generations",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	return lister.ListObjectVersions(ctx, reference.Key, limit)
}

func attachmentStoredObjectVersioningEnabled(
	storage AttachmentStorage,
	reference AttachmentStoredReference,
) (bool, error) {
	if storage == nil {
		return false, ErrAttachmentStorageMissing
	}
	if routed, ok := storage.(*AttachmentStorageRouter); ok {
		return routed.StoredObjectVersioningEnabled(reference)
	}
	if reference.StoreID != attachmentStorageStoreID(storage) ||
		(reference.StorageType != "" &&
			reference.StorageType != attachmentStorageType(storage)) {
		return false, fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	if capability, ok := storage.(AttachmentStorageVersioning); ok {
		return capability.ObjectVersioningEnabled(), nil
	}
	_, versioned := storage.(VersionedAttachmentStorage)
	return versioned, nil
}

func openAttachmentStoredObject(
	ctx context.Context,
	storage AttachmentStorage,
	reference AttachmentStoredReference,
) (io.ReadCloser, error) {
	if storage == nil {
		return nil, ErrAttachmentStorageMissing
	}
	if routed, ok := storage.(ReferencedAttachmentStorage); ok {
		return routed.OpenStoredObject(ctx, reference)
	}
	if reference.StoreID != attachmentStorageStoreID(storage) ||
		(reference.StorageType != "" &&
			reference.StorageType != attachmentStorageType(storage)) {
		return nil, fmt.Errorf(
			"%w: attachment store_id %q is not active",
			ErrAttachmentStorageMissing,
			reference.StoreID,
		)
	}
	if versioned, ok := storage.(VersionedAttachmentStorage); ok {
		return versioned.OpenVersion(
			ctx,
			reference.Key,
			reference.VersionID,
		)
	}
	if reference.VersionID != "" {
		return nil, ErrInvalidAttachment
	}
	return storage.Open(ctx, reference.Key)
}
