package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultAttachmentDownloadConcurrency         = 8
	defaultAttachmentDownloadPerActorConcurrency = 2
	maxAttachmentDownloadConcurrency             = 64
	maxAttachmentDownloadPerActorConcurrency     = 8
	attachmentDownloadCleanupTimeout             = 5 * time.Second
)

type attachmentDownloadActorSlots struct {
	slots      chan struct{}
	references int
}

type PreparedAttachmentReplayAuthorization struct {
	DecisionID string
	Check      PolicyCheckInput
}

type AttachmentReplayFinalizationInput struct {
	TicketID      uint
	Record        *models.IdempotencyRecord
	Authorization PreparedAttachmentReplayAuthorization
}

type AttachmentReplayFinalizationResult struct {
	Attachment *models.TicketAttachment
}

// PrepareAttachmentReplayAuthorization persists the canonical, exact
// attachment policy check for an Agent REST idempotency replay. File name,
// content type and request digest are retained so conditional policies and the
// execution guard see the same command fingerprint as the original request.
func (s *AgentNativeService) PrepareAttachmentReplayAuthorization(
	ctx context.Context,
	input NativeAttachmentInput,
	tokenScopes []string,
) (*PreparedAttachmentReplayAuthorization, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("Agent service is required")
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, errors.New(
			"attachment replay authorization requires a context outside a project transaction",
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal ||
		operation.Source != SourceProtocolAgentREST ||
		input.TicketID == 0 ||
		input.Actor != operation.Actor ||
		input.CredentialID != operation.CredentialID ||
		(input.SourceProtocol != "" &&
			input.SourceProtocol != string(operation.Source)) {
		return nil, ErrInvalidActor
	}
	if err := validateAgentTokenScope(
		tokenScopes,
		models.ScopeAttachmentsWrite,
	); err != nil {
		return nil, err
	}
	safeName, err := SafeAttachmentName(input.OriginalName)
	if err != nil {
		return nil, err
	}
	check := attachmentUploadPolicyCheck(operation, input, safeName)
	decision, err := s.CheckActionInShortProjectTransactions(
		ctx,
		check,
	)
	if err != nil {
		return nil, err
	}
	if decision == nil {
		return nil, errors.New(
			"attachment replay policy decision is unavailable",
		)
	}
	return &PreparedAttachmentReplayAuthorization{
		DecisionID: decision.ID,
		Check:      check,
	}, nil
}

// FinalizeAttachmentReplayInShortProjectTransaction is the final
// output-before-authorization boundary for Agent REST attachment replays. It
// atomically revalidates every revocable authorization fact, consumes the
// exact prepared PolicyDecision (including PolicyEpoch), and optionally loads
// a legacy attachment. Snapshot and fallback output are forbidden until this
// transaction commits.
func (s *AgentNativeService) FinalizeAttachmentReplayInShortProjectTransaction(
	ctx context.Context,
	input AttachmentReplayFinalizationInput,
) (*AttachmentReplayFinalizationResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("Agent service is required")
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, errors.New(
			"attachment replay finalization requires a context outside a project transaction",
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	record := input.Record
	authorization := input.Authorization
	if operation.Actor.Type != models.ActorTypeServicePrincipal ||
		operation.Source != SourceProtocolAgentREST ||
		input.TicketID == 0 ||
		record == nil ||
		strings.TrimSpace(authorization.DecisionID) == "" {
		return nil, ErrInvalidActor
	}
	if record.OrganizationID != operation.Scope.OrganizationID ||
		record.ProjectID != operation.Scope.ProjectID ||
		record.ActorType != operation.Actor.Type ||
		record.ActorID != operation.Actor.ID ||
		record.Operation != "ticket.attachment.create" ||
		record.State != models.IdempotencyStateCompleted {
		return nil, ErrIdempotencyConflict
	}
	expectedCheck := authorization.Check
	if expectedCheck.ServicePrincipalID != operation.Actor.ID ||
		expectedCheck.CredentialID != operation.CredentialID ||
		expectedCheck.Scope != models.ScopeAttachmentsWrite ||
		expectedCheck.Action != "ticket.attachment.create" ||
		expectedCheck.ResourceType != "ticket" ||
		expectedCheck.ResourceID != strconv.FormatUint(
			uint64(input.TicketID),
			10,
		) ||
		!expectedCheck.IsWrite ||
		expectedCheck.SourceProtocol != string(operation.Source) {
		return nil, ErrPolicyDenied
	}

	result := &AttachmentReplayFinalizationResult{}
	err = s.RunProjectOperation(
		ctx,
		func(scopedContext context.Context) error {
			if _, revalidateErr :=
				s.RevalidatePrincipalProjectOperation(
					scopedContext,
					models.ScopeAttachmentsWrite,
				); revalidateErr != nil {
				return revalidateErr
			}
			decision, loadDecisionErr := s.loadMatchingPolicyDecision(
				scopedContext,
				authorization.DecisionID,
				operation.Actor,
				expectedCheck,
				true,
			)
			if loadDecisionErr != nil {
				return loadDecisionErr
			}
			if !decision.Allowed {
				return ErrPolicyDenied
			}
			if len(record.ResourceSnapshot) > 0 {
				return nil
			}

			resourceID, parseErr := strconv.ParseUint(
				strings.TrimSpace(record.ResourceID),
				10,
				32,
			)
			if parseErr != nil || resourceID == 0 {
				return nil
			}
			var attachment models.TicketAttachment
			loadErr := s.dbForContext(scopedContext).
				Where(
					"id = ? AND ticket_id = ? AND organization_id = ? AND project_id = ?",
					uint(resourceID),
					input.TicketID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
				).
				Take(&attachment).Error
			if errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return nil
			}
			if loadErr != nil {
				return loadErr
			}
			result.Attachment = &attachment
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *AgentNativeService) PrepareAttachmentUploadAuthorization(
	ctx context.Context,
	input *NativeAttachmentInput,
) error {
	if s == nil || input == nil {
		return ErrInvalidAttachment
	}
	operation, err := commandOperationContext(ctx, input.Actor)
	if err != nil {
		return err
	}
	if input.Actor.Type != models.ActorTypeServicePrincipal {
		return nil
	}
	safeName, err := SafeAttachmentName(input.OriginalName)
	if err != nil {
		return err
	}
	primary := attachmentUploadPolicyCheck(
		operation,
		*input,
		safeName,
	)
	if strings.TrimSpace(input.PolicyDecisionID) == "" {
		decision, checkErr :=
			s.CheckActionInShortProjectTransactions(
				ctx,
				primary,
			)
		if decision != nil {
			input.PolicyDecisionID = decision.ID
		}
		if checkErr != nil {
			return checkErr
		}
	} else if err := s.validateAttachmentPolicyDecisionOutsideTransaction(
		ctx,
		input.PolicyDecisionID,
		input.Actor,
		primary,
		true,
	); err != nil {
		return err
	}

	external := externalNotificationPolicyCheck(
		input.Actor,
		input.CredentialID,
		primary.ResourceID,
		input.RequestDigest,
		string(operation.Source),
	)
	if strings.TrimSpace(
		input.ExternalNotificationPolicyDecisionID,
	) == "" {
		decision, checkErr :=
			s.CheckActionInShortProjectTransactions(
				ctx,
				external,
			)
		if decision != nil {
			input.ExternalNotificationPolicyDecisionID =
				decision.ID
		}
		if checkErr != nil &&
			!errors.Is(checkErr, ErrPolicyDenied) &&
			!errors.Is(checkErr, ErrAutomationLoop) {
			return checkErr
		}
		if decision == nil {
			return errors.New(
				"attachment external notification decision is unavailable",
			)
		}
	} else if err := s.validateAttachmentPolicyDecisionOutsideTransaction(
		ctx,
		input.ExternalNotificationPolicyDecisionID,
		input.Actor,
		external,
		false,
	); err != nil {
		return err
	}
	return nil
}

func attachmentUploadPolicyCheck(
	operation OperationContext,
	input NativeAttachmentInput,
	safeName string,
) PolicyCheckInput {
	return PolicyCheckInput{
		ServicePrincipalID: input.Actor.ID,
		CredentialID:       input.CredentialID,
		Scope:              models.ScopeAttachmentsWrite,
		Action:             "ticket.attachment.create",
		ResourceType:       "ticket",
		ResourceID: strconv.FormatUint(
			uint64(input.TicketID),
			10,
		),
		IsWrite:        true,
		RequestDigest:  input.RequestDigest,
		SourceProtocol: string(operation.Source),
		Context: map[string]any{
			"file_name": safeName,
			"content_type": strings.TrimSpace(
				input.ContentType,
			),
		},
	}
}

func (s *AgentNativeService) validateAttachmentPolicyDecisionOutsideTransaction(
	ctx context.Context,
	decisionID string,
	actor models.ActorRef,
	input PolicyCheckInput,
	mustAllow bool,
) error {
	if scopeddb.HasTransaction(ctx) {
		return ErrExternalIOInsideProjectTransaction
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			decision, loadErr := s.loadMatchingPolicyDecision(
				scopedContext,
				decisionID,
				actor,
				input,
				true,
			)
			if loadErr != nil {
				return loadErr
			}
			if mustAllow && !decision.Allowed {
				return ErrPolicyDenied
			}
			return nil
		},
	)
}

func (s *AgentNativeService) openAttachmentWithRevalidation(
	ctx context.Context,
	expectedTicketID uint,
	attachmentID uint,
) (*models.TicketAttachment, io.ReadCloser, error) {
	if s == nil ||
		s.db == nil ||
		s.attachmentStorage == nil ||
		s.attachmentStaging == nil {
		return nil, nil, ErrAttachmentStorageMissing
	}
	if attachmentID == 0 {
		return nil, nil, ErrInvalidAttachment
	}
	if err := requireExternalIOOutsideProjectTransaction(
		ctx,
		"attachment download",
	); err != nil {
		return nil, nil, err
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	var (
		attachment    models.TicketAttachment
		initialAccess *ProjectAccess
	)
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			var revalidateErr error
			initialAccess, revalidateErr =
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
					models.ScopeAttachmentsRead,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			return s.loadAndAuthorizeAttachmentDownload(
				scopedContext,
				initialAccess,
				operation,
				expectedTicketID,
				attachmentID,
				&attachment,
				"SHARE",
			)
		},
	)
	if err != nil {
		return nil, nil, err
	}
	releaseDownload, err := s.acquireAttachmentDownload(
		ctx,
		operation.Actor,
	)
	if err != nil {
		return nil, nil, err
	}
	releaseDownloadOnFailure := true
	defer func() {
		if releaseDownloadOnFailure {
			releaseDownload()
		}
	}()

	var source io.ReadCloser
	if routed, ok := s.attachmentStorage.(ReferencedAttachmentStorage); ok {
		source, err = routed.OpenStoredObject(
			ctx,
			AttachmentStoredReference{
				StorageType: attachment.StorageType,
				StoreID:     attachment.StorageStoreID,
				Key:         attachment.StoragePath,
				VersionID:   attachment.StorageVersionID,
			},
		)
	} else if routed, ok := s.attachmentStorage.(TypedAttachmentStorage); ok {
		source, err = routed.OpenStored(
			ctx,
			attachment.StorageType,
			attachment.StoragePath,
		)
	} else {
		source, err = s.attachmentStorage.Open(
			ctx,
			attachment.StoragePath,
		)
	}
	if err != nil {
		return nil, nil, err
	}
	maxBytes := s.attachmentMaxBytes
	if maxBytes <= 0 || maxBytes > attachment.FileSize {
		maxBytes = attachment.FileSize
	}
	stagingKey := ".staging/download-" + newNativeID() + ".spool"
	staged, stageErr := s.attachmentStaging.Stage(
		ctx,
		stagingKey,
		source,
		maxBytes,
	)
	sourceCloseErr := source.Close()
	if stageErr != nil || sourceCloseErr != nil {
		cleanupErr := cleanupAttachmentDownloadStaging(
			ctx,
			s.attachmentStaging,
			stagingKey,
			nil,
		)
		return nil, nil, errors.Join(
			stageErr,
			sourceCloseErr,
			cleanupErr,
		)
	}
	if staged == nil ||
		staged.Key != stagingKey ||
		staged.Size != attachment.FileSize ||
		!strings.EqualFold(staged.SHA256, attachment.Hash) {
		cleanupErr := cleanupAttachmentDownloadStaging(
			ctx,
			s.attachmentStaging,
			stagingKey,
			nil,
		)
		return nil, nil, errors.Join(
			ErrInvalidAttachment,
			cleanupErr,
		)
	}
	stagedReader, err := s.attachmentStaging.OpenStaged(
		ctx,
		stagingKey,
	)
	if err != nil {
		cleanupErr := cleanupAttachmentDownloadStaging(
			ctx,
			s.attachmentStaging,
			stagingKey,
			nil,
		)
		return nil, nil, errors.Join(err, cleanupErr)
	}

	var finalized models.TicketAttachment
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			currentAccess, revalidateErr :=
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
					models.ScopeAttachmentsRead,
				)
			if revalidateErr != nil {
				return revalidateErr
			}
			if !initialAccess.AuthorizationSnapshot.Matches(
				currentAccess.AuthorizationSnapshot,
			) {
				return ErrProjectAccessDenied
			}
			if revalidateErr =
				s.loadAndAuthorizeAttachmentDownload(
					scopedContext,
					currentAccess,
					operation,
					expectedTicketID,
					attachmentID,
					&finalized,
					"UPDATE",
				); revalidateErr != nil {
				return revalidateErr
			}
			if finalized.StoragePath != attachment.StoragePath ||
				finalized.StorageType != attachment.StorageType ||
				finalized.StorageStoreID !=
					attachment.StorageStoreID ||
				finalized.StorageVersionID !=
					attachment.StorageVersionID ||
				finalized.FileSize != attachment.FileSize ||
				!strings.EqualFold(
					finalized.Hash,
					attachment.Hash,
				) ||
				!finalized.UpdatedAt.Equal(attachment.UpdatedAt) {
				return ErrProjectAccessDenied
			}
			update := s.db.WithContext(scopedContext).
				Model(&models.TicketAttachment{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND download_count = ?",
					finalized.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					finalized.DownloadCount,
				).
				UpdateColumn(
					"download_count",
					gorm.Expr("download_count + 1"),
				)
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrProjectAccessDenied
			}
			finalized.DownloadCount++
			return nil
		},
	)
	if err != nil {
		cleanupErr := cleanupAttachmentDownloadStaging(
			ctx,
			s.attachmentStaging,
			stagingKey,
			stagedReader,
		)
		return nil, nil, errors.Join(err, cleanupErr)
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := cleanupAttachmentDownloadStaging(
			ctx,
			s.attachmentStaging,
			stagingKey,
			stagedReader,
		)
		return nil, nil, errors.Join(err, cleanupErr)
	}
	downloadReader := newAttachmentDownloadReader(
		ctx,
		stagedReader,
		s.attachmentStaging,
		stagingKey,
		releaseDownload,
	)
	releaseDownloadOnFailure = false
	return &finalized, downloadReader, nil
}

func (s *AgentNativeService) acquireAttachmentDownload(
	ctx context.Context,
	actor models.ActorRef,
) (func(), error) {
	if s == nil ||
		s.attachmentDownloadSlots == nil ||
		s.attachmentDownloadPerActor <= 0 {
		return nil, ErrAttachmentStorageMissing
	}
	if err := actor.Validate(); err != nil {
		return nil, ErrInvalidActor
	}
	actorKey := string(actor.Type) + "\x00" + actor.ID
	s.attachmentDownloadActorsMu.Lock()
	actorSlots := s.attachmentDownloadActors[actorKey]
	if actorSlots == nil {
		actorSlots = &attachmentDownloadActorSlots{
			slots: make(
				chan struct{},
				s.attachmentDownloadPerActor,
			),
		}
		s.attachmentDownloadActors[actorKey] = actorSlots
	}
	actorSlots.references++
	s.attachmentDownloadActorsMu.Unlock()

	releaseActorReference := func() {
		s.attachmentDownloadActorsMu.Lock()
		actorSlots.references--
		if actorSlots.references == 0 &&
			s.attachmentDownloadActors[actorKey] == actorSlots {
			delete(s.attachmentDownloadActors, actorKey)
		}
		s.attachmentDownloadActorsMu.Unlock()
	}
	select {
	case actorSlots.slots <- struct{}{}:
	case <-ctx.Done():
		releaseActorReference()
		return nil, ctx.Err()
	}
	select {
	case s.attachmentDownloadSlots <- struct{}{}:
	case <-ctx.Done():
		<-actorSlots.slots
		releaseActorReference()
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-s.attachmentDownloadSlots
			<-actorSlots.slots
			releaseActorReference()
		})
	}, nil
}

type attachmentDownloadReader struct {
	ctx     context.Context
	reader  io.ReadCloser
	staging AttachmentStagingStore
	key     string
	release func()

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newAttachmentDownloadReader(
	ctx context.Context,
	reader io.ReadCloser,
	staging AttachmentStagingStore,
	key string,
	release func(),
) *attachmentDownloadReader {
	download := &attachmentDownloadReader{
		ctx:     ctx,
		reader:  reader,
		staging: staging,
		key:     key,
		release: release,
		closed:  make(chan struct{}),
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = download.Close()
		case <-download.closed:
		}
	}()
	return download
}

func (reader *attachmentDownloadReader) Read(
	buffer []byte,
) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, errors.Join(err, reader.Close())
	}
	count, readErr := reader.reader.Read(buffer)
	if readErr == nil {
		return count, nil
	}
	closeErr := reader.Close()
	if errors.Is(readErr, io.EOF) && closeErr == nil {
		return count, io.EOF
	}
	return count, errors.Join(readErr, closeErr)
}

func (reader *attachmentDownloadReader) Close() error {
	reader.closeOnce.Do(func() {
		close(reader.closed)
		reader.closeErr = cleanupAttachmentDownloadStaging(
			reader.ctx,
			reader.staging,
			reader.key,
			reader.reader,
		)
		if reader.release != nil {
			reader.release()
		}
	})
	return reader.closeErr
}

func cleanupAttachmentDownloadStaging(
	ctx context.Context,
	staging AttachmentStagingStore,
	key string,
	reader io.ReadCloser,
) error {
	var closeErr error
	if reader != nil {
		closeErr = reader.Close()
	}
	if staging == nil || strings.TrimSpace(key) == "" {
		return closeErr
	}
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		attachmentDownloadCleanupTimeout,
	)
	defer cancel()
	return errors.Join(
		closeErr,
		staging.DeleteStaged(cleanupContext, key),
	)
}

func (s *AgentNativeService) loadAndAuthorizeAttachmentDownload(
	ctx context.Context,
	access *ProjectAccess,
	operation OperationContext,
	expectedTicketID uint,
	attachmentID uint,
	destination *models.TicketAttachment,
	lockStrength string,
) error {
	if destination == nil {
		return ErrInvalidAttachment
	}
	query := s.db.WithContext(ctx)
	if strings.TrimSpace(lockStrength) != "" {
		query = query.Clauses(
			clause.Locking{Strength: lockStrength},
		)
	}
	if err := query.
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			attachmentID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(destination).Error; err != nil {
		return err
	}
	if expectedTicketID != 0 &&
		destination.TicketID != expectedTicketID {
		return gorm.ErrRecordNotFound
	}
	if destination.StorageType == "staging" ||
		destination.VirusScan != models.VirusScanClean {
		return fmt.Errorf(
			"%w: %s",
			ErrAttachmentNotClean,
			destination.VirusScan,
		)
	}
	var ticket models.Ticket
	if err := s.db.WithContext(ctx).
		Select(
			"id",
			"organization_id",
			"project_id",
			"created_by_id",
			"assigned_to_id",
		).
		Where(
			"id = ? AND organization_id = ? AND project_id = ?",
			destination.TicketID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
		).
		Take(&ticket).Error; err != nil {
		return err
	}
	switch operation.Actor.Type {
	case models.ActorTypeHuman:
		if err := authorizeHumanAttachmentTicket(
			access,
			operation,
			ticket,
			false,
			destination.IsPublic,
		); err != nil {
			return err
		}
		if access.Role == models.ProjectRoleRequester &&
			!destination.IsPublic {
			return ErrProjectAccessDenied
		}
	case models.ActorTypeServicePrincipal:
		_, err := s.CheckAction(ctx, PolicyCheckInput{
			ServicePrincipalID: operation.Actor.ID,
			CredentialID:       operation.CredentialID,
			Scope:              models.ScopeAttachmentsRead,
			Action:             "ticket.attachment.read",
			ResourceType:       "ticket",
			ResourceID: strconv.FormatUint(
				uint64(ticket.ID),
				10,
			),
			SourceProtocol: string(operation.Source),
		})
		if err != nil {
			return err
		}
	case models.ActorTypeSystem:
	default:
		return ErrInvalidActor
	}
	return nil
}

func (s *AgentNativeService) captureAttachmentAuthorization(
	ctx context.Context,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	if s == nil || s.db == nil || scopeddb.HasTransaction(ctx) {
		return nil, fmt.Errorf(
			"%w: attachment authorization capture",
			ErrExternalIOInsideProjectTransaction,
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var access *ProjectAccess
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		s.db,
		operation.Scope,
		func(scopedContext context.Context) error {
			var revalidateErr error
			access, revalidateErr =
				s.revalidateAttachmentAuthorizationInTransaction(
					scopedContext,
					requiredScopes...,
				)
			return revalidateErr
		},
	)
	if err != nil {
		return nil, err
	}
	return access, nil
}

func (s *AgentNativeService) revalidateAttachmentAuthorizationInTransaction(
	ctx context.Context,
	requiredScopes ...string,
) (*ProjectAccess, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if operation.Actor.Type != models.ActorTypeSystem {
		return RevalidateOperationProjectAuthorization(
			ctx,
			s.db,
			s,
			requiredScopes...,
		)
	}
	if operation.Source != SourceProtocolWorker {
		return nil, ErrInvalidActor
	}
	if err := requireExactProjectAuthorizationTransaction(
		ctx,
		operation.Scope,
	); err != nil {
		return nil, err
	}
	var project models.Project
	err = s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(
			"id = ? AND organization_id = ? AND status = ?",
			operation.Scope.ProjectID,
			operation.Scope.OrganizationID,
			models.ProjectStatusActive,
		).
		Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectAccessDenied
	}
	if err != nil {
		return nil, err
	}
	return &ProjectAccess{
		Project: project,
		Scope:   operation.Scope,
		AuthorizationSnapshot: AuthorizationSnapshot{
			Scope:            operation.Scope,
			ActorType:        models.ActorTypeSystem,
			ProjectUpdatedAt: project.UpdatedAt,
		},
	}, nil
}

func authorizeHumanAttachmentTicket(
	access *ProjectAccess,
	operation OperationContext,
	ticket models.Ticket,
	write bool,
	isPublic bool,
) error {
	if access == nil ||
		operation.Actor.Type != models.ActorTypeHuman ||
		ticket.OrganizationID != access.Scope.OrganizationID ||
		ticket.ProjectID != access.Scope.ProjectID {
		return ErrProjectAccessDenied
	}
	userID, err := strconv.ParseUint(
		operation.Actor.ID,
		10,
		strconv.IntSize,
	)
	if err != nil || userID == 0 {
		return ErrProjectAccessDenied
	}
	currentUserID := uint(userID)
	switch access.Role {
	case models.ProjectRoleAdmin, models.ProjectRoleManager:
		return nil
	case models.ProjectRoleObserver:
		if !write {
			return nil
		}
	case models.ProjectRoleAgent:
		if !write ||
			ticket.AssignedToID == nil ||
			*ticket.AssignedToID == currentUserID {
			return nil
		}
	case models.ProjectRoleRequester:
		if ticket.CreatedByID != nil &&
			*ticket.CreatedByID == currentUserID &&
			(!write || isPublic) {
			return nil
		}
	}
	return ErrProjectAccessDenied
}

func attachmentStorageType(storage AttachmentStorage) string {
	if typed, ok := storage.(attachmentStorageTyper); ok {
		switch typed.AttachmentStorageType() {
		case "local", "s3", "gcs", "azure":
			return typed.AttachmentStorageType()
		}
	}
	return "managed"
}
