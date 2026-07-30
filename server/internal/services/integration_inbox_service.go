package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrIntegrationInvalidInput        = errors.New("invalid integration inbox input")
	ErrIntegrationProjectNotFound     = errors.New("integration project not found")
	ErrIntegrationProjectInactive     = errors.New("integration project is not active")
	ErrIntegrationConnectionNotFound  = errors.New("integration connection not found")
	ErrIntegrationConnectionInactive  = errors.New("integration connection is not active")
	ErrIntegrationConnectorInactive   = errors.New("integration connector is not active")
	ErrIntegrationMappingNotFound     = errors.New("integration mapping not found")
	ErrIntegrationMappingNotPublished = errors.New("integration mapping is not published")
	ErrIntegrationReplayWindow        = errors.New("integration message is outside the replay window")
	ErrIntegrationSignatureRejected   = errors.New("integration signature verification failed")
	ErrIntegrationConflict            = errors.New("integration conflict requires resolution")
	ErrIntegrationMessageInProgress   = errors.New("integration message is already being processed")
	ErrIntegrationMessageDeadLettered = errors.New("integration message is in the dead-letter queue")
	ErrIntegrationCommandFailed       = errors.New("integration domain command failed")
	ErrIntegrationDryRunUnavailable   = errors.New("integration mapping dry-run is unavailable")
	ErrIntegrationDeadLetterNotFound  = errors.New("integration dead letter not found")
	ErrIntegrationDeadLetterState     = errors.New("integration dead letter state conflict")
)

var (
	errRollbackIntegrationConflict = errors.New("rollback integration conflict")
	errRollbackIntegrationCommand  = errors.New("rollback integration command")
)

const (
	defaultIntegrationReplayWindow = 5 * time.Minute
	defaultIntegrationFutureSkew   = 30 * time.Second
	defaultIntegrationMaxPayload   = int64(2 << 20)
)

type IntegrationSignatureVerification struct {
	Connection           *models.Connection
	Connector            *models.ConnectorDefinition
	ProjectKey           string
	MappingPublicID      string
	SignedAt             time.Time
	Signature            string
	MessageID            string
	ExternalResourceType string
	ExternalResourceID   string
	ContentType          string
	Body                 []byte
}

// IntegrationSignatureVerifier is implemented by a connector-specific
// cryptographic Adapter. The service never interprets external signature text
// and never persists the raw credential or signature.
type IntegrationSignatureVerifier interface {
	Verify(context.Context, IntegrationSignatureVerification) error
}

type IntegrationSignatureVerifierFunc func(
	context.Context,
	IntegrationSignatureVerification,
) error

func (function IntegrationSignatureVerifierFunc) Verify(
	ctx context.Context,
	input IntegrationSignatureVerification,
) error {
	return function(ctx, input)
}

type IntegrationDomainCommand struct {
	Operation            OperationContext
	Connection           *models.Connection
	Connector            *models.ConnectorDefinition
	Mapping              *models.MappingVersion
	InboxMessageID       uint
	ExternalMessageID    string
	ExternalResourceType string
	ExternalResourceID   string
	Payload              []byte
	PayloadDigest        string
}

type IntegrationDomainCommandResult struct {
	Status          models.InboxReceiptStatus
	ResourceType    string
	ResourceID      string
	ResourceVersion uint64
	EventID         string
	OperationID     string
	ExternalVersion string
	ReceiptData     json.RawMessage
}

// IntegrationDomainCommandHandler must invoke the same project-scoped domain
// command Interface used by human and Agent Adapters. The supplied transaction
// is a savepoint inside the Inbox transaction; using another database handle
// would break atomicity and is unsupported.
type IntegrationDomainCommandHandler interface {
	Execute(
		context.Context,
		*gorm.DB,
		IntegrationDomainCommand,
	) (IntegrationDomainCommandResult, error)
}

type IntegrationDomainCommandHandlerFunc func(
	context.Context,
	*gorm.DB,
	IntegrationDomainCommand,
) (IntegrationDomainCommandResult, error)

func (function IntegrationDomainCommandHandlerFunc) Execute(
	ctx context.Context,
	tx *gorm.DB,
	command IntegrationDomainCommand,
) (IntegrationDomainCommandResult, error) {
	return function(ctx, tx, command)
}

type IntegrationMappingDryRunRequest struct {
	Connection *models.Connection
	Connector  *models.ConnectorDefinition
	Mapping    *models.MappingVersion
	Payload    []byte
}

type IntegrationMappingDryRunResult struct {
	MappingVersionID uint            `json:"mapping_version_id"`
	PayloadDigest    string          `json:"payload_digest"`
	TargetCommand    string          `json:"target_command"`
	Preview          json.RawMessage `json:"preview"`
	Warnings         []string        `json:"warnings,omitempty"`
}

type IntegrationMappingDryRunner interface {
	DryRun(
		context.Context,
		IntegrationMappingDryRunRequest,
	) (IntegrationMappingDryRunResult, error)
}

type IntegrationMappingDryRunnerFunc func(
	context.Context,
	IntegrationMappingDryRunRequest,
) (IntegrationMappingDryRunResult, error)

func (function IntegrationMappingDryRunnerFunc) DryRun(
	ctx context.Context,
	request IntegrationMappingDryRunRequest,
) (IntegrationMappingDryRunResult, error) {
	return function(ctx, request)
}

type IntegrationInboxServiceOptions struct {
	DB                  *gorm.DB
	SignatureVerifier   IntegrationSignatureVerifier
	CommandHandler      IntegrationDomainCommandHandler
	DryRunner           IntegrationMappingDryRunner
	Now                 func() time.Time
	DefaultReplayWindow time.Duration
	AllowedFutureSkew   time.Duration
	MaxPayloadBytes     int64
}

type IntegrationInboxService struct {
	db                  *gorm.DB
	signatureVerifier   IntegrationSignatureVerifier
	commandHandler      IntegrationDomainCommandHandler
	dryRunner           IntegrationMappingDryRunner
	now                 func() time.Time
	defaultReplayWindow time.Duration
	allowedFutureSkew   time.Duration
	maxPayloadBytes     int64
}

func NewIntegrationInboxService(
	options IntegrationInboxServiceOptions,
) (*IntegrationInboxService, error) {
	if options.DB == nil {
		return nil, errors.New("integration inbox database is required")
	}
	if options.SignatureVerifier == nil {
		return nil, errors.New("integration signature verifier is required")
	}
	if options.CommandHandler == nil {
		return nil, errors.New("integration domain command handler is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DefaultReplayWindow <= 0 {
		options.DefaultReplayWindow = defaultIntegrationReplayWindow
	}
	if options.DefaultReplayWindow < 30*time.Second ||
		options.DefaultReplayWindow > 24*time.Hour {
		return nil, errors.New("integration replay window must be between 30 seconds and 24 hours")
	}
	if options.AllowedFutureSkew < 0 {
		return nil, errors.New("integration future clock skew cannot be negative")
	}
	if options.AllowedFutureSkew == 0 {
		options.AllowedFutureSkew = defaultIntegrationFutureSkew
	}
	if options.AllowedFutureSkew > 5*time.Minute {
		return nil, errors.New("integration future clock skew cannot exceed 5 minutes")
	}
	if options.MaxPayloadBytes <= 0 {
		options.MaxPayloadBytes = defaultIntegrationMaxPayload
	}
	return &IntegrationInboxService{
		db:                  options.DB,
		signatureVerifier:   options.SignatureVerifier,
		commandHandler:      options.CommandHandler,
		dryRunner:           options.DryRunner,
		now:                 options.Now,
		defaultReplayWindow: options.DefaultReplayWindow,
		allowedFutureSkew:   options.AllowedFutureSkew,
		maxPayloadBytes:     options.MaxPayloadBytes,
	}, nil
}

type IntegrationInboundInput struct {
	Scope                models.ProjectScope
	ConnectionID         uint
	MappingVersionID     uint
	ExternalMessageID    string
	ExternalResourceType string
	ExternalResourceID   string
	SignedAt             time.Time
	Signature            string
	ContentType          string
	Body                 []byte
	// TrustedTraceID and TrustedCorrelationID are server-generated Adapter
	// context. External headers and payload fields must not populate them.
	TrustedTraceID       string
	TrustedCorrelationID string
}

type IntegrationInboundResult struct {
	Message    *models.InboxMessage        `json:"message,omitempty"`
	Receipt    *models.InboxReceipt        `json:"receipt,omitempty"`
	Link       *models.ExternalLink        `json:"link,omitempty"`
	Conflict   *models.IntegrationConflict `json:"conflict,omitempty"`
	DeadLetter *models.DeadLetter          `json:"dead_letter,omitempty"`
	Replayed   bool                        `json:"replayed"`
}

type IntegrationDeadLetterReplayInput struct {
	DeadLetterID      uint
	ExpectedUpdatedAt time.Time
}

type integrationTarget struct {
	project    models.Project
	connection models.Connection
	connector  models.ConnectorDefinition
	mapping    models.MappingVersion
}

type integrationConflictDraft struct {
	conflictType                 models.IntegrationConflictType
	messageID                    uint
	externalResourceType         string
	externalResourceID           string
	existingPayloadDigest        string
	incomingPayloadDigest        string
	existingInternalResourceType string
	existingInternalResourceID   string
	incomingInternalResourceType string
	incomingInternalResourceID   string
}

func (service *IntegrationInboxService) Receive(
	ctx context.Context,
	input IntegrationInboundInput,
) (*IntegrationInboundResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrIntegrationInvalidInput)
	}
	if err := service.validateInboundInput(input); err != nil {
		return nil, err
	}
	input.ContentType = normalizedIntegrationContentType(input.ContentType)
	var target *integrationTarget
	err := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		input.Scope,
		func(scopedContext context.Context) error {
			var loadErr error
			target, loadErr = service.loadIntegrationTarget(
				scopedContext,
				input,
				true,
			)
			return loadErr
		},
	)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	replayWindow := service.defaultReplayWindow
	if target.connection.ReplayWindowSeconds > 0 {
		replayWindow = time.Duration(target.connection.ReplayWindowSeconds) * time.Second
	} else if target.connector.DefaultReplayWindowSeconds > 0 {
		replayWindow = time.Duration(target.connector.DefaultReplayWindowSeconds) * time.Second
	}
	signedAt := input.SignedAt.UTC()
	if signedAt.Before(now.Add(-replayWindow)) ||
		signedAt.After(now.Add(service.allowedFutureSkew)) {
		return nil, ErrIntegrationReplayWindow
	}
	body := append([]byte(nil), input.Body...)
	if err := service.signatureVerifier.Verify(
		ctx,
		IntegrationSignatureVerification{
			Connection:           &target.connection,
			Connector:            &target.connector,
			ProjectKey:           string(target.project.Key),
			MappingPublicID:      target.mapping.PublicID,
			SignedAt:             signedAt,
			Signature:            input.Signature,
			MessageID:            input.ExternalMessageID,
			ExternalResourceType: input.ExternalResourceType,
			ExternalResourceID:   input.ExternalResourceID,
			ContentType:          input.ContentType,
			Body:                 append([]byte(nil), body...),
		},
	); err != nil {
		return nil, ErrIntegrationSignatureRejected
	}

	payloadHash := sha256.Sum256(body)
	payloadDigest := hex.EncodeToString(payloadHash[:])
	signatureHash := sha256.Sum256([]byte(input.Signature))
	signatureDigest := hex.EncodeToString(signatureHash[:])
	result := &IntegrationInboundResult{}
	var outcomeErr error

	transactionErr := scopeddb.WithProjectScopeContextTransaction(
		ctx,
		service.db,
		input.Scope,
		func(scopedContext context.Context) error {
			return transactionForContext(
				scopedContext,
				service.db,
				func(tx *gorm.DB) error {
					existing, found, err := findInboxMessageTx(
						tx,
						input.Scope.ProjectID,
						input.ConnectionID,
						input.ExternalMessageID,
					)
					if err != nil {
						return err
					}
					if found {
						existingResult, existingErr, err := service.existingInboxResultTx(
							tx,
							target,
							existing,
							input,
							payloadDigest,
						)
						if err != nil {
							return err
						}
						*result = *existingResult
						outcomeErr = existingErr
						return nil
					}

					message := &models.InboxMessage{
						OrganizationID:       input.Scope.OrganizationID,
						ProjectID:            input.Scope.ProjectID,
						ConnectionID:         input.ConnectionID,
						MappingVersionID:     input.MappingVersionID,
						ExternalMessageID:    input.ExternalMessageID,
						ExternalResourceType: input.ExternalResourceType,
						ExternalResourceID:   input.ExternalResourceID,
						SignedAt:             signedAt,
						ReceivedAt:           now,
						ContentType:          input.ContentType,
						Payload:              body,
						PayloadDigest:        payloadDigest,
						SignatureDigest:      signatureDigest,
						Status:               models.InboxMessageStatusProcessing,
					}
					create := tx.Clauses(clause.OnConflict{
						Columns: []clause.Column{
							{Name: "project_id"},
							{Name: "connection_id"},
							{Name: "external_message_id"},
						},
						DoNothing: true,
					}).Create(message)
					if create.Error != nil {
						return fmt.Errorf("create integration inbox message: %w", create.Error)
					}
					if create.RowsAffected == 0 {
						concurrent, found, err := findInboxMessageTx(
							tx,
							input.Scope.ProjectID,
							input.ConnectionID,
							input.ExternalMessageID,
						)
						if err != nil {
							return err
						}
						if !found {
							return errors.New("concurrent integration inbox message disappeared")
						}
						existingResult, existingErr, err := service.existingInboxResultTx(
							tx,
							target,
							concurrent,
							input,
							payloadDigest,
						)
						if err != nil {
							return err
						}
						*result = *existingResult
						outcomeErr = existingErr
						return nil
					}
					result.Message = message

					var commandFailure error
					var conflictDraft *integrationConflictDraft
					var receipt *models.InboxReceipt
					var link *models.ExternalLink
					nestedErr := tx.Transaction(func(commandTx *gorm.DB) error {
						commandResult, err := service.commandHandler.Execute(
							ctx,
							commandTx,
							IntegrationDomainCommand{
								Operation: OperationContext{
									Scope:        input.Scope,
									Actor:        target.connection.Actor(),
									Source:       SourceProtocolConnector,
									CredentialID: target.connection.ActorCredentialID,
									TraceID: boundedIntegrationControlValue(
										input.TrustedTraceID,
										128,
									),
									CorrelationID: boundedIntegrationControlValue(
										input.TrustedCorrelationID,
										128,
									),
								},
								Connection:           &target.connection,
								Connector:            &target.connector,
								Mapping:              &target.mapping,
								InboxMessageID:       message.ID,
								ExternalMessageID:    input.ExternalMessageID,
								ExternalResourceType: input.ExternalResourceType,
								ExternalResourceID:   input.ExternalResourceID,
								Payload:              append([]byte(nil), body...),
								PayloadDigest:        payloadDigest,
							},
						)
						if err != nil {
							commandFailure = err
							return errRollbackIntegrationCommand
						}
						if err := validateIntegrationCommandResult(commandResult); err != nil {
							commandFailure = err
							return errRollbackIntegrationCommand
						}
						link, conflictDraft, err = reconcileExternalLinkTx(
							commandTx,
							target,
							message,
							input,
							commandResult,
						)
						if err != nil {
							return err
						}
						if conflictDraft != nil {
							return errRollbackIntegrationConflict
						}
						receiptStatus := commandResult.Status
						if receiptStatus == "" {
							receiptStatus = models.InboxReceiptStatusApplied
						}
						receiptData := commandResult.ReceiptData
						if len(receiptData) == 0 {
							receiptData = json.RawMessage(`{}`)
						}
						receipt = &models.InboxReceipt{
							OrganizationID:  input.Scope.OrganizationID,
							ProjectID:       input.Scope.ProjectID,
							ConnectionID:    input.ConnectionID,
							InboxMessageID:  message.ID,
							Status:          receiptStatus,
							ResourceType:    commandResult.ResourceType,
							ResourceID:      commandResult.ResourceID,
							ResourceVersion: commandResult.ResourceVersion,
							EventID:         boundedIntegrationControlValue(commandResult.EventID, 64),
							OperationID:     boundedIntegrationControlValue(commandResult.OperationID, 64),
							Result:          datatypes.JSON(append([]byte(nil), receiptData...)),
							ActorType:       target.connection.ActorType,
							ActorID:         target.connection.ActorID,
							ProcessedAt:     now,
						}
						if err := commandTx.Create(receipt).Error; err != nil {
							return fmt.Errorf("create integration inbox receipt: %w", err)
						}
						updated := commandTx.Model(&models.InboxMessage{}).
							Where("id = ? AND status = ?", message.ID, models.InboxMessageStatusProcessing).
							Updates(map[string]any{
								"status":       models.InboxMessageStatusCompleted,
								"processed_at": now,
							})
						if updated.Error != nil {
							return fmt.Errorf("complete integration inbox message: %w", updated.Error)
						}
						if updated.RowsAffected != 1 {
							return ErrIntegrationMessageInProgress
						}
						message.Status = models.InboxMessageStatusCompleted
						message.ProcessedAt = &now
						return nil
					})

					switch {
					case nestedErr == nil:
						result.Receipt = receipt
						result.Link = link
						return nil
					case errors.Is(nestedErr, errRollbackIntegrationConflict):
						conflict, err := service.persistConflictTx(
							tx,
							target,
							conflictDraft,
						)
						if err != nil {
							return err
						}
						if err := tx.Model(&models.InboxMessage{}).
							Where("id = ?", message.ID).
							Updates(map[string]any{
								"status":       models.InboxMessageStatusConflict,
								"processed_at": now,
							}).Error; err != nil {
							return fmt.Errorf("mark integration inbox conflict: %w", err)
						}
						message.Status = models.InboxMessageStatusConflict
						message.ProcessedAt = &now
						result.Conflict = conflict
						outcomeErr = ErrIntegrationConflict
						return nil
					case errors.Is(nestedErr, errRollbackIntegrationCommand):
						letter := &models.DeadLetter{
							OrganizationID: input.Scope.OrganizationID,
							ProjectID:      input.Scope.ProjectID,
							ConnectionID:   input.ConnectionID,
							InboxMessageID: message.ID,
							Status:         models.DeadLetterStatusOpen,
							ReasonCode:     "domain_command_failed",
							ErrorSummary:   sanitizeIntegrationError(commandFailure),
							PayloadDigest:  payloadDigest,
							AttemptCount:   1,
						}
						if err := tx.Create(letter).Error; err != nil {
							return fmt.Errorf("create integration dead letter: %w", err)
						}
						if err := tx.Model(&models.InboxMessage{}).
							Where("id = ?", message.ID).
							Updates(map[string]any{
								"status":       models.InboxMessageStatusDeadLetter,
								"processed_at": now,
							}).Error; err != nil {
							return fmt.Errorf("mark integration inbox dead letter: %w", err)
						}
						message.Status = models.InboxMessageStatusDeadLetter
						message.ProcessedAt = &now
						result.DeadLetter = letter
						outcomeErr = ErrIntegrationCommandFailed
						return nil
					default:
						return nestedErr
					}
				},
			)
		},
	)
	if transactionErr != nil {
		return nil, transactionErr
	}
	return result, outcomeErr
}

func (service *IntegrationInboxService) DryRun(
	ctx context.Context,
	input IntegrationInboundInput,
) (IntegrationMappingDryRunResult, error) {
	if service.dryRunner == nil {
		return IntegrationMappingDryRunResult{}, ErrIntegrationDryRunUnavailable
	}
	if ctx == nil {
		return IntegrationMappingDryRunResult{}, fmt.Errorf(
			"%w: context is required",
			ErrIntegrationInvalidInput,
		)
	}
	if err := validateIntegrationScopeAndPayload(input, service.maxPayloadBytes); err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	target, err := service.loadIntegrationTarget(ctx, input, false)
	if err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	result, err := service.dryRunner.DryRun(
		ctx,
		IntegrationMappingDryRunRequest{
			Connection: &target.connection,
			Connector:  &target.connector,
			Mapping:    &target.mapping,
			Payload:    append([]byte(nil), input.Body...),
		},
	)
	if err != nil {
		return IntegrationMappingDryRunResult{}, err
	}
	digest := sha256.Sum256(input.Body)
	result.MappingVersionID = target.mapping.ID
	result.PayloadDigest = hex.EncodeToString(digest[:])
	result.TargetCommand = target.mapping.TargetCommand
	return result, nil
}

// ReplayDeadLetter retries the original InboxMessage through the same shared
// domain command seam used by Receive. The stored payload and frozen
// MappingVersionID are never replaced. A status transition to resolved is
// committed only together with a durable Receipt (or a persisted conflict).
func (service *IntegrationInboxService) ReplayDeadLetter(
	ctx context.Context,
	input IntegrationDeadLetterReplayInput,
) (*IntegrationInboundResult, error) {
	if ctx == nil || input.DeadLetterID == 0 || input.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf(
			"%w: dead-letter id and expected_updated_at are required",
			ErrIntegrationInvalidInput,
		)
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Source != SourceProtocolHumanREST ||
		operation.Actor.Type != models.ActorTypeHuman {
		return nil, fmt.Errorf(
			"%w: trusted human project operation is required",
			ErrIntegrationInvalidInput,
		)
	}
	now := service.now().UTC()
	result := &IntegrationInboundResult{}
	var outcomeErr error
	transactionErr := transactionForContext(ctx, service.db, func(tx *gorm.DB) error {
		var letter models.DeadLetter
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				input.DeadLetterID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
			).
			First(&letter).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIntegrationDeadLetterNotFound
		}
		if err != nil {
			return fmt.Errorf("load integration dead letter: %w", err)
		}
		var message models.InboxMessage
		err = tx.Where(
			"id = ? AND organization_id = ? AND project_id = ? AND connection_id = ?",
			letter.InboxMessageID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			letter.ConnectionID,
		).First(&message).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIntegrationDeadLetterState
		}
		if err != nil {
			return fmt.Errorf("load dead-letter inbox message: %w", err)
		}
		result.Message = &message
		result.DeadLetter = &letter

		switch letter.Status {
		case models.DeadLetterStatusResolved:
			if letter.ResolvedAt == nil ||
				(models.ActorRef{
					Type: letter.ResolvedByType,
					ID:   letter.ResolvedByID,
				}).Validate() != nil {
				return ErrIntegrationDeadLetterState
			}
			return service.loadResolvedDeadLetterResultTx(
				tx,
				operation.Scope,
				&letter,
				&message,
				result,
				&outcomeErr,
			)
		case models.DeadLetterStatusRequeued:
			return ErrIntegrationMessageInProgress
		case models.DeadLetterStatusOpen:
		default:
			return ErrIntegrationDeadLetterState
		}
		if message.Status != models.InboxMessageStatusDeadLetter ||
			message.PayloadDigest != letter.PayloadDigest ||
			integrationPayloadDigest(message.Payload) != message.PayloadDigest {
			return ErrIntegrationDeadLetterState
		}
		target, err := loadDeadLetterReplayTargetTx(
			tx,
			operation.Scope,
			&message,
		)
		if err != nil {
			return err
		}

		claimedAt := now
		if !claimedAt.After(letter.UpdatedAt) {
			claimedAt = letter.UpdatedAt.UTC().Add(time.Nanosecond)
		}
		claimed := tx.Model(&models.DeadLetter{}).
			Where(
				"id = ? AND organization_id = ? AND project_id = ? AND status = ? AND updated_at = ?",
				letter.ID,
				operation.Scope.OrganizationID,
				operation.Scope.ProjectID,
				models.DeadLetterStatusOpen,
				input.ExpectedUpdatedAt,
			).
			UpdateColumns(map[string]any{
				"status":          models.DeadLetterStatusRequeued,
				"attempt_count":   letter.AttemptCount + 1,
				"next_attempt_at": nil,
				"updated_at":      claimedAt,
			})
		if claimed.Error != nil {
			return fmt.Errorf("claim integration dead letter: %w", claimed.Error)
		}
		if claimed.RowsAffected != 1 {
			return ErrIntegrationDeadLetterState
		}
		letter.Status = models.DeadLetterStatusRequeued
		letter.AttemptCount++
		letter.UpdatedAt = claimedAt

		var commandFailure error
		var conflictDraft *integrationConflictDraft
		var receipt *models.InboxReceipt
		var link *models.ExternalLink
		nestedErr := tx.Transaction(func(commandTx *gorm.DB) error {
			commandResult, err := service.commandHandler.Execute(
				ctx,
				commandTx,
				IntegrationDomainCommand{
					Operation: OperationContext{
						Scope:         operation.Scope,
						Actor:         target.connection.Actor(),
						Source:        SourceProtocolConnector,
						CredentialID:  target.connection.ActorCredentialID,
						TraceID:       boundedIntegrationControlValue(operation.TraceID, 128),
						CorrelationID: boundedIntegrationControlValue(operation.CorrelationID, 128),
					},
					Connection:           &target.connection,
					Connector:            &target.connector,
					Mapping:              &target.mapping,
					InboxMessageID:       message.ID,
					ExternalMessageID:    message.ExternalMessageID,
					ExternalResourceType: message.ExternalResourceType,
					ExternalResourceID:   message.ExternalResourceID,
					Payload:              append([]byte(nil), message.Payload...),
					PayloadDigest:        message.PayloadDigest,
				},
			)
			if err != nil {
				commandFailure = err
				return errRollbackIntegrationCommand
			}
			if err := validateIntegrationCommandResult(commandResult); err != nil {
				commandFailure = err
				return errRollbackIntegrationCommand
			}
			link, conflictDraft, err = reconcileDeadLetterExternalLinkTx(
				commandTx,
				target,
				&message,
				commandResult,
				true,
			)
			if err != nil {
				return err
			}
			if conflictDraft != nil {
				return errRollbackIntegrationConflict
			}
			receiptStatus := commandResult.Status
			if receiptStatus == "" {
				receiptStatus = models.InboxReceiptStatusApplied
			}
			receiptData := commandResult.ReceiptData
			if len(receiptData) == 0 {
				receiptData = json.RawMessage(`{}`)
			}
			receipt = &models.InboxReceipt{
				OrganizationID:  operation.Scope.OrganizationID,
				ProjectID:       operation.Scope.ProjectID,
				ConnectionID:    message.ConnectionID,
				InboxMessageID:  message.ID,
				Status:          receiptStatus,
				ResourceType:    commandResult.ResourceType,
				ResourceID:      commandResult.ResourceID,
				ResourceVersion: commandResult.ResourceVersion,
				EventID:         boundedIntegrationControlValue(commandResult.EventID, 64),
				OperationID:     boundedIntegrationControlValue(commandResult.OperationID, 64),
				Result:          datatypes.JSON(append([]byte(nil), receiptData...)),
				ActorType:       target.connection.ActorType,
				ActorID:         target.connection.ActorID,
				ProcessedAt:     now,
			}
			if err := commandTx.Create(receipt).Error; err != nil {
				return fmt.Errorf("create dead-letter replay receipt: %w", err)
			}
			completed := commandTx.Model(&models.InboxMessage{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
					message.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					models.InboxMessageStatusDeadLetter,
				).
				UpdateColumns(map[string]any{
					"status":       models.InboxMessageStatusCompleted,
					"processed_at": now,
					"updated_at":   now,
				})
			if completed.Error != nil {
				return fmt.Errorf("complete replayed inbox message: %w", completed.Error)
			}
			if completed.RowsAffected != 1 {
				return ErrIntegrationDeadLetterState
			}
			return nil
		})

		switch {
		case nestedErr == nil:
			if err := resolveDeadLetterTx(
				tx,
				operation,
				&letter,
				now,
			); err != nil {
				return err
			}
			message.Status = models.InboxMessageStatusCompleted
			message.ProcessedAt = &now
			result.Message = &message
			result.Receipt = receipt
			result.Link = link
			result.DeadLetter = &letter
			return nil
		case errors.Is(nestedErr, errRollbackIntegrationConflict):
			conflict, err := persistDeadLetterConflictTx(
				tx,
				target,
				conflictDraft,
			)
			if err != nil {
				return err
			}
			conflicted := tx.Model(&models.InboxMessage{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
					message.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					models.InboxMessageStatusDeadLetter,
				).
				UpdateColumns(map[string]any{
					"status":       models.InboxMessageStatusConflict,
					"processed_at": now,
					"updated_at":   now,
				})
			if conflicted.Error != nil {
				return fmt.Errorf("mark replayed inbox conflict: %w", conflicted.Error)
			}
			if conflicted.RowsAffected != 1 {
				return ErrIntegrationDeadLetterState
			}
			if err := resolveDeadLetterTx(tx, operation, &letter, now); err != nil {
				return err
			}
			message.Status = models.InboxMessageStatusConflict
			message.ProcessedAt = &now
			result.Message = &message
			result.Conflict = conflict
			result.DeadLetter = &letter
			outcomeErr = ErrIntegrationConflict
			return nil
		case errors.Is(nestedErr, errRollbackIntegrationCommand):
			reopenedAt := now
			if !reopenedAt.After(letter.UpdatedAt) {
				reopenedAt = letter.UpdatedAt.UTC().Add(time.Nanosecond)
			}
			reopened := tx.Model(&models.DeadLetter{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
					letter.ID,
					operation.Scope.OrganizationID,
					operation.Scope.ProjectID,
					models.DeadLetterStatusRequeued,
				).
				UpdateColumns(map[string]any{
					"status":          models.DeadLetterStatusOpen,
					"reason_code":     "domain_command_failed",
					"error_summary":   sanitizeIntegrationError(commandFailure),
					"next_attempt_at": nil,
					"updated_at":      reopenedAt,
				})
			if reopened.Error != nil {
				return fmt.Errorf("reopen integration dead letter: %w", reopened.Error)
			}
			if reopened.RowsAffected != 1 {
				return ErrIntegrationDeadLetterState
			}
			letter.Status = models.DeadLetterStatusOpen
			letter.ErrorSummary = sanitizeIntegrationError(commandFailure)
			letter.UpdatedAt = reopenedAt
			result.DeadLetter = &letter
			outcomeErr = ErrIntegrationCommandFailed
			return nil
		default:
			return nestedErr
		}
	})
	if transactionErr != nil {
		return nil, transactionErr
	}
	return result, outcomeErr
}

func loadDeadLetterReplayTargetTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	message *models.InboxMessage,
) (*integrationTarget, error) {
	if tx == nil || message == nil {
		return nil, ErrIntegrationDeadLetterState
	}
	target := &integrationTarget{}
	err := tx.Where(
		"id = ? AND organization_id = ?",
		scope.ProjectID,
		scope.OrganizationID,
	).First(&target.project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationProjectNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.project.Status != models.ProjectStatusActive {
		return nil, ErrIntegrationProjectInactive
	}
	err = tx.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		message.ConnectionID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&target.connection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.connection.Status != models.ConnectionStatusActive {
		return nil, ErrIntegrationConnectionInactive
	}
	err = tx.Where(
		"id = ? AND organization_id = ? AND project_id = ?",
		target.connection.ConnectorDefinitionID,
		scope.OrganizationID,
		scope.ProjectID,
	).First(&target.connector).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.connector.Status != models.ConnectorDefinitionStatusActive ||
		(target.connector.Direction != models.ConnectorDirectionInbound &&
			target.connector.Direction != models.ConnectorDirectionBidirectional) {
		return nil, ErrIntegrationConnectorInactive
	}
	err = tx.Where(
		"id = ? AND organization_id = ? AND project_id = ? AND connection_id = ?",
		message.MappingVersionID,
		scope.OrganizationID,
		scope.ProjectID,
		message.ConnectionID,
	).First(&target.mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationMappingNotFound
	}
	if err != nil {
		return nil, err
	}
	if target.mapping.Status != models.MappingVersionStatusPublished &&
		target.mapping.Status != models.MappingVersionStatusRetired {
		return nil, ErrIntegrationMappingNotPublished
	}
	if integrationPayloadDigest(target.mapping.Definition) !=
		target.mapping.DefinitionDigest {
		return nil, ErrIntegrationDeadLetterState
	}
	if err := validateIntegrationTargetCommand(target.mapping.TargetCommand); err != nil {
		return nil, err
	}
	return target, nil
}

func reconcileDeadLetterExternalLinkTx(
	tx *gorm.DB,
	target *integrationTarget,
	message *models.InboxMessage,
	command IntegrationDomainCommandResult,
	allowCreate bool,
) (*models.ExternalLink, *integrationConflictDraft, error) {
	if tx == nil || target == nil || message == nil {
		return nil, nil, ErrIntegrationDeadLetterState
	}
	scope := target.project.Scope()
	var external models.ExternalLink
	err := tx.Where(
		"organization_id = ? AND project_id = ? AND connection_id = ? AND external_resource_type = ? AND external_resource_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
		message.ConnectionID,
		message.ExternalResourceType,
		message.ExternalResourceID,
	).First(&external).Error
	if err == nil {
		if external.InternalResourceType == command.ResourceType &&
			external.InternalResourceID == command.ResourceID {
			return &external, nil, nil
		}
		return nil, &integrationConflictDraft{
			conflictType:                 models.IntegrationConflictExternalLinkMismatch,
			messageID:                    message.ID,
			externalResourceType:         message.ExternalResourceType,
			externalResourceID:           message.ExternalResourceID,
			existingInternalResourceType: external.InternalResourceType,
			existingInternalResourceID:   external.InternalResourceID,
			incomingInternalResourceType: command.ResourceType,
			incomingInternalResourceID:   command.ResourceID,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}

	var internal models.ExternalLink
	err = tx.Where(
		"organization_id = ? AND project_id = ? AND connection_id = ? AND internal_resource_type = ? AND internal_resource_id = ?",
		scope.OrganizationID,
		scope.ProjectID,
		message.ConnectionID,
		command.ResourceType,
		command.ResourceID,
	).First(&internal).Error
	if err == nil {
		return nil, &integrationConflictDraft{
			conflictType:                 models.IntegrationConflictInternalLinkCollision,
			messageID:                    message.ID,
			externalResourceType:         message.ExternalResourceType,
			externalResourceID:           message.ExternalResourceID,
			existingInternalResourceType: internal.InternalResourceType,
			existingInternalResourceID:   internal.InternalResourceID,
			incomingInternalResourceType: command.ResourceType,
			incomingInternalResourceID:   command.ResourceID,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	if !allowCreate {
		return nil, nil, ErrIntegrationDeadLetterState
	}

	link := &models.ExternalLink{
		OrganizationID:       scope.OrganizationID,
		ProjectID:            scope.ProjectID,
		ConnectionID:         message.ConnectionID,
		ExternalResourceType: message.ExternalResourceType,
		ExternalResourceID:   message.ExternalResourceID,
		InternalResourceType: command.ResourceType,
		InternalResourceID:   command.ResourceID,
		MappingVersionID:     message.MappingVersionID,
		ExternalVersion:      boundedIntegrationControlValue(command.ExternalVersion, 128),
		InternalVersion:      command.ResourceVersion,
		LastInboxMessageID:   message.ID,
	}
	create := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(link)
	if create.Error != nil {
		return nil, nil, fmt.Errorf("create replay external link: %w", create.Error)
	}
	if create.RowsAffected == 1 {
		return link, nil, nil
	}
	return reconcileDeadLetterExternalLinkTx(
		tx,
		target,
		message,
		command,
		false,
	)
}

func persistDeadLetterConflictTx(
	tx *gorm.DB,
	target *integrationTarget,
	draft *integrationConflictDraft,
) (*models.IntegrationConflict, error) {
	if tx == nil || target == nil || draft == nil {
		return nil, ErrIntegrationDeadLetterState
	}
	keyMaterial := strings.Join([]string{
		string(draft.conflictType),
		fmt.Sprintf("%d", draft.messageID),
		draft.externalResourceType,
		draft.externalResourceID,
		draft.existingPayloadDigest,
		draft.incomingPayloadDigest,
		draft.existingInternalResourceType,
		draft.existingInternalResourceID,
		draft.incomingInternalResourceType,
		draft.incomingInternalResourceID,
	}, "\x00")
	keyHash := sha256.Sum256([]byte(keyMaterial))
	conflictKey := hex.EncodeToString(keyHash[:])
	details, err := json.Marshal(map[string]any{
		"mapping_version_id":  target.mapping.ID,
		"message_id":          draft.messageID,
		"requires_resolution": true,
	})
	if err != nil {
		return nil, err
	}
	scope := target.project.Scope()
	conflict := &models.IntegrationConflict{
		OrganizationID:               scope.OrganizationID,
		ProjectID:                    scope.ProjectID,
		ConnectionID:                 target.connection.ID,
		InboxMessageID:               draft.messageID,
		ConflictKey:                  conflictKey,
		Type:                         draft.conflictType,
		Status:                       models.IntegrationConflictStatusOpen,
		ExternalResourceType:         draft.externalResourceType,
		ExternalResourceID:           draft.externalResourceID,
		ExistingPayloadDigest:        draft.existingPayloadDigest,
		IncomingPayloadDigest:        draft.incomingPayloadDigest,
		ExistingInternalResourceType: draft.existingInternalResourceType,
		ExistingInternalResourceID:   draft.existingInternalResourceID,
		IncomingInternalResourceType: draft.incomingInternalResourceType,
		IncomingInternalResourceID:   draft.incomingInternalResourceID,
		Details:                      datatypes.JSON(details),
	}
	create := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "project_id"},
			{Name: "connection_id"},
			{Name: "conflict_key"},
		},
		DoNothing: true,
	}).Create(conflict)
	if create.Error != nil {
		return nil, fmt.Errorf("create replay integration conflict: %w", create.Error)
	}
	if create.RowsAffected == 0 {
		if err := tx.Where(
			"organization_id = ? AND project_id = ? AND connection_id = ? AND conflict_key = ?",
			scope.OrganizationID,
			scope.ProjectID,
			target.connection.ID,
			conflictKey,
		).First(conflict).Error; err != nil {
			return nil, fmt.Errorf("load replay integration conflict: %w", err)
		}
	}
	return conflict, nil
}

func (service *IntegrationInboxService) loadResolvedDeadLetterResultTx(
	tx *gorm.DB,
	scope models.ProjectScope,
	letter *models.DeadLetter,
	message *models.InboxMessage,
	result *IntegrationInboundResult,
	outcomeErr *error,
) error {
	var receipt models.InboxReceipt
	err := tx.Where(
		"inbox_message_id = ? AND organization_id = ? AND project_id = ? AND connection_id = ?",
		message.ID,
		scope.OrganizationID,
		scope.ProjectID,
		message.ConnectionID,
	).First(&receipt).Error
	if err == nil {
		if message.Status != models.InboxMessageStatusCompleted {
			return ErrIntegrationDeadLetterState
		}
		result.Receipt = &receipt
		result.Replayed = true
		var link models.ExternalLink
		linkErr := tx.Where(
			"organization_id = ? AND project_id = ? AND connection_id = ? AND external_resource_type = ? AND external_resource_id = ?",
			scope.OrganizationID,
			scope.ProjectID,
			message.ConnectionID,
			message.ExternalResourceType,
			message.ExternalResourceID,
		).First(&link).Error
		if linkErr == nil {
			result.Link = &link
		} else if !errors.Is(linkErr, gorm.ErrRecordNotFound) {
			return linkErr
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var conflict models.IntegrationConflict
	err = tx.Where(
		"inbox_message_id = ? AND organization_id = ? AND project_id = ? AND connection_id = ?",
		message.ID,
		scope.OrganizationID,
		scope.ProjectID,
		message.ConnectionID,
	).Order("id DESC").First(&conflict).Error
	if err == nil {
		if message.Status != models.InboxMessageStatusConflict {
			return ErrIntegrationDeadLetterState
		}
		result.Conflict = &conflict
		result.Replayed = true
		*outcomeErr = ErrIntegrationConflict
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrIntegrationDeadLetterState
	}
	_ = letter
	return err
}

func resolveDeadLetterTx(
	tx *gorm.DB,
	operation OperationContext,
	letter *models.DeadLetter,
	resolvedAt time.Time,
) error {
	if tx == nil || letter == nil {
		return ErrIntegrationDeadLetterState
	}
	if !resolvedAt.After(letter.UpdatedAt) {
		resolvedAt = letter.UpdatedAt.UTC().Add(time.Nanosecond)
	}
	resolved := tx.Model(&models.DeadLetter{}).
		Where(
			"id = ? AND organization_id = ? AND project_id = ? AND status = ?",
			letter.ID,
			operation.Scope.OrganizationID,
			operation.Scope.ProjectID,
			models.DeadLetterStatusRequeued,
		).
		UpdateColumns(map[string]any{
			"status":           models.DeadLetterStatusResolved,
			"resolved_at":      resolvedAt,
			"resolved_by_type": operation.Actor.Type,
			"resolved_by_id":   operation.Actor.ID,
			"updated_at":       resolvedAt,
		})
	if resolved.Error != nil {
		return fmt.Errorf("resolve integration dead letter: %w", resolved.Error)
	}
	if resolved.RowsAffected != 1 {
		return ErrIntegrationDeadLetterState
	}
	letter.Status = models.DeadLetterStatusResolved
	letter.ResolvedAt = &resolvedAt
	letter.ResolvedByType = operation.Actor.Type
	letter.ResolvedByID = operation.Actor.ID
	letter.UpdatedAt = resolvedAt
	return nil
}

func integrationPayloadDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (service *IntegrationInboxService) validateInboundInput(
	input IntegrationInboundInput,
) error {
	if err := validateIntegrationScopeAndPayload(input, service.maxPayloadBytes); err != nil {
		return err
	}
	if strings.TrimSpace(input.ExternalMessageID) == "" ||
		len(input.ExternalMessageID) > 191 {
		return fmt.Errorf("%w: invalid external message id", ErrIntegrationInvalidInput)
	}
	if strings.TrimSpace(input.ExternalResourceType) == "" ||
		len(input.ExternalResourceType) > 64 ||
		strings.TrimSpace(input.ExternalResourceID) == "" ||
		len(input.ExternalResourceID) > 191 {
		return fmt.Errorf("%w: invalid external resource identity", ErrIntegrationInvalidInput)
	}
	if input.SignedAt.IsZero() {
		return fmt.Errorf("%w: signed timestamp is required", ErrIntegrationInvalidInput)
	}
	if strings.TrimSpace(input.Signature) == "" || len(input.Signature) > 4096 {
		return fmt.Errorf("%w: signature is required", ErrIntegrationInvalidInput)
	}
	if len(input.ContentType) > 128 {
		return fmt.Errorf("%w: content type is too long", ErrIntegrationInvalidInput)
	}
	return nil
}

func validateIntegrationScopeAndPayload(
	input IntegrationInboundInput,
	maxPayloadBytes int64,
) error {
	if err := input.Scope.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrIntegrationInvalidInput, err)
	}
	if input.ConnectionID == 0 || input.MappingVersionID == 0 {
		return fmt.Errorf(
			"%w: connection and mapping are required",
			ErrIntegrationInvalidInput,
		)
	}
	if len(input.Body) == 0 || int64(len(input.Body)) > maxPayloadBytes {
		return fmt.Errorf("%w: payload size is invalid", ErrIntegrationInvalidInput)
	}
	return nil
}

func (service *IntegrationInboxService) loadIntegrationTarget(
	ctx context.Context,
	input IntegrationInboundInput,
	requirePublished bool,
) (*integrationTarget, error) {
	target := &integrationTarget{}
	err := service.db.WithContext(ctx).
		Where(
			"id = ? AND organization_id = ?",
			input.Scope.ProjectID,
			input.Scope.OrganizationID,
		).
		First(&target.project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load integration project: %w", err)
	}
	if target.project.Status != models.ProjectStatusActive {
		return nil, ErrIntegrationProjectInactive
	}

	err = service.db.WithContext(ctx).
		Where("id = ? AND project_id = ?", input.ConnectionID, input.Scope.ProjectID).
		First(&target.connection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load integration connection: %w", err)
	}
	if target.connection.Status != models.ConnectionStatusActive {
		return nil, ErrIntegrationConnectionInactive
	}
	if err := (OperationContext{
		Scope:        input.Scope,
		Actor:        target.connection.Actor(),
		Source:       SourceProtocolConnector,
		CredentialID: target.connection.ActorCredentialID,
	}).Validate(); err != nil {
		return nil, ErrIntegrationConnectionInactive
	}

	err = service.db.WithContext(ctx).
		Where(
			"id = ? AND project_id = ?",
			target.connection.ConnectorDefinitionID,
			input.Scope.ProjectID,
		).
		First(&target.connector).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load connector definition: %w", err)
	}
	if target.connector.Status != models.ConnectorDefinitionStatusActive ||
		(target.connector.Direction != models.ConnectorDirectionInbound &&
			target.connector.Direction != models.ConnectorDirectionBidirectional) {
		return nil, ErrIntegrationConnectorInactive
	}

	err = service.db.WithContext(ctx).
		Where(
			"id = ? AND project_id = ? AND connection_id = ?",
			input.MappingVersionID,
			input.Scope.ProjectID,
			input.ConnectionID,
		).
		First(&target.mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntegrationMappingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load integration mapping: %w", err)
	}
	if requirePublished &&
		target.mapping.Status != models.MappingVersionStatusPublished {
		return nil, ErrIntegrationMappingNotPublished
	}
	if err := validateIntegrationTargetCommand(target.mapping.TargetCommand); err != nil {
		return nil, err
	}
	return target, nil
}

func (service *IntegrationInboxService) existingInboxResultTx(
	tx *gorm.DB,
	target *integrationTarget,
	message *models.InboxMessage,
	input IntegrationInboundInput,
	incomingDigest string,
) (*IntegrationInboundResult, error, error) {
	result := &IntegrationInboundResult{Message: message}
	if message.PayloadDigest != incomingDigest ||
		message.MappingVersionID != input.MappingVersionID ||
		message.ExternalResourceType != input.ExternalResourceType ||
		message.ExternalResourceID != input.ExternalResourceID ||
		message.ContentType != input.ContentType {
		conflict, err := service.persistConflictTx(
			tx,
			target,
			&integrationConflictDraft{
				conflictType:          models.IntegrationConflictMessageIdentityReuse,
				messageID:             message.ID,
				externalResourceType:  input.ExternalResourceType,
				externalResourceID:    input.ExternalResourceID,
				existingPayloadDigest: message.PayloadDigest,
				incomingPayloadDigest: incomingDigest,
			},
		)
		if err != nil {
			return nil, nil, err
		}
		result.Conflict = conflict
		return result, ErrIntegrationConflict, nil
	}

	var receipt models.InboxReceipt
	err := tx.Where("inbox_message_id = ?", message.ID).First(&receipt).Error
	if err == nil {
		result.Receipt = &receipt
		result.Replayed = true
		var link models.ExternalLink
		linkErr := tx.Where(
			"project_id = ? AND connection_id = ? AND external_resource_type = ? AND external_resource_id = ?",
			message.ProjectID,
			message.ConnectionID,
			message.ExternalResourceType,
			message.ExternalResourceID,
		).First(&link).Error
		if linkErr == nil {
			result.Link = &link
		} else if !errors.Is(linkErr, gorm.ErrRecordNotFound) {
			return nil, nil, linkErr
		}
		return result, nil, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}

	switch message.Status {
	case models.InboxMessageStatusConflict:
		var conflict models.IntegrationConflict
		err := tx.Where("inbox_message_id = ?", message.ID).
			Order("id DESC").
			First(&conflict).Error
		if err == nil {
			result.Conflict = &conflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		return result, ErrIntegrationConflict, nil
	case models.InboxMessageStatusDeadLetter:
		var letter models.DeadLetter
		err := tx.Where("inbox_message_id = ?", message.ID).First(&letter).Error
		if err == nil {
			result.DeadLetter = &letter
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
		return result, ErrIntegrationMessageDeadLettered, nil
	default:
		return result, ErrIntegrationMessageInProgress, nil
	}
}

func (service *IntegrationInboxService) persistConflictTx(
	tx *gorm.DB,
	target *integrationTarget,
	draft *integrationConflictDraft,
) (*models.IntegrationConflict, error) {
	if draft == nil {
		return nil, errors.New("integration conflict details are required")
	}
	keyMaterial := strings.Join([]string{
		string(draft.conflictType),
		fmt.Sprintf("%d", draft.messageID),
		draft.externalResourceType,
		draft.externalResourceID,
		draft.existingPayloadDigest,
		draft.incomingPayloadDigest,
		draft.existingInternalResourceType,
		draft.existingInternalResourceID,
		draft.incomingInternalResourceType,
		draft.incomingInternalResourceID,
	}, "\x00")
	keyHash := sha256.Sum256([]byte(keyMaterial))
	conflictKey := hex.EncodeToString(keyHash[:])
	details, err := json.Marshal(map[string]any{
		"mapping_version_id":  target.mapping.ID,
		"message_id":          draft.messageID,
		"requires_resolution": true,
	})
	if err != nil {
		return nil, err
	}
	conflict := &models.IntegrationConflict{
		OrganizationID:               target.project.OrganizationID,
		ProjectID:                    target.project.ID,
		ConnectionID:                 target.connection.ID,
		InboxMessageID:               draft.messageID,
		ConflictKey:                  conflictKey,
		Type:                         draft.conflictType,
		Status:                       models.IntegrationConflictStatusOpen,
		ExternalResourceType:         draft.externalResourceType,
		ExternalResourceID:           draft.externalResourceID,
		ExistingPayloadDigest:        draft.existingPayloadDigest,
		IncomingPayloadDigest:        draft.incomingPayloadDigest,
		ExistingInternalResourceType: draft.existingInternalResourceType,
		ExistingInternalResourceID:   draft.existingInternalResourceID,
		IncomingInternalResourceType: draft.incomingInternalResourceType,
		IncomingInternalResourceID:   draft.incomingInternalResourceID,
		Details:                      datatypes.JSON(details),
	}
	create := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "project_id"},
			{Name: "connection_id"},
			{Name: "conflict_key"},
		},
		DoNothing: true,
	}).Create(conflict)
	if create.Error != nil {
		return nil, fmt.Errorf("create integration conflict: %w", create.Error)
	}
	if create.RowsAffected == 0 {
		if err := tx.Where(
			"project_id = ? AND connection_id = ? AND conflict_key = ?",
			target.project.ID,
			target.connection.ID,
			conflictKey,
		).First(conflict).Error; err != nil {
			return nil, fmt.Errorf("load integration conflict: %w", err)
		}
	}
	return conflict, nil
}

func reconcileExternalLinkTx(
	tx *gorm.DB,
	target *integrationTarget,
	message *models.InboxMessage,
	input IntegrationInboundInput,
	command IntegrationDomainCommandResult,
) (*models.ExternalLink, *integrationConflictDraft, error) {
	return reconcileExternalLinkTxAttempt(
		tx,
		target,
		message,
		input,
		command,
		true,
	)
}

func reconcileExternalLinkTxAttempt(
	tx *gorm.DB,
	target *integrationTarget,
	message *models.InboxMessage,
	input IntegrationInboundInput,
	command IntegrationDomainCommandResult,
	allowCreate bool,
) (*models.ExternalLink, *integrationConflictDraft, error) {
	var external models.ExternalLink
	err := tx.Where(
		"project_id = ? AND connection_id = ? AND external_resource_type = ? AND external_resource_id = ?",
		input.Scope.ProjectID,
		input.ConnectionID,
		input.ExternalResourceType,
		input.ExternalResourceID,
	).First(&external).Error
	if err == nil {
		if external.InternalResourceType == command.ResourceType &&
			external.InternalResourceID == command.ResourceID {
			return &external, nil, nil
		}
		return nil, &integrationConflictDraft{
			conflictType:                 models.IntegrationConflictExternalLinkMismatch,
			messageID:                    message.ID,
			externalResourceType:         input.ExternalResourceType,
			externalResourceID:           input.ExternalResourceID,
			existingInternalResourceType: external.InternalResourceType,
			existingInternalResourceID:   external.InternalResourceID,
			incomingInternalResourceType: command.ResourceType,
			incomingInternalResourceID:   command.ResourceID,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}

	var internal models.ExternalLink
	err = tx.Where(
		"project_id = ? AND connection_id = ? AND internal_resource_type = ? AND internal_resource_id = ?",
		input.Scope.ProjectID,
		input.ConnectionID,
		command.ResourceType,
		command.ResourceID,
	).First(&internal).Error
	if err == nil {
		return nil, &integrationConflictDraft{
			conflictType:                 models.IntegrationConflictInternalLinkCollision,
			messageID:                    message.ID,
			externalResourceType:         input.ExternalResourceType,
			externalResourceID:           input.ExternalResourceID,
			existingInternalResourceType: internal.InternalResourceType,
			existingInternalResourceID:   internal.InternalResourceID,
			incomingInternalResourceType: command.ResourceType,
			incomingInternalResourceID:   command.ResourceID,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	if !allowCreate {
		return nil, nil, errors.New(
			"concurrent external link winner could not be resolved",
		)
	}

	link := &models.ExternalLink{
		OrganizationID:       input.Scope.OrganizationID,
		ProjectID:            input.Scope.ProjectID,
		ConnectionID:         input.ConnectionID,
		ExternalResourceType: input.ExternalResourceType,
		ExternalResourceID:   input.ExternalResourceID,
		InternalResourceType: command.ResourceType,
		InternalResourceID:   command.ResourceID,
		MappingVersionID:     target.mapping.ID,
		ExternalVersion:      boundedIntegrationControlValue(command.ExternalVersion, 128),
		InternalVersion:      command.ResourceVersion,
		LastInboxMessageID:   message.ID,
	}
	create := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(link)
	if create.Error != nil {
		return nil, nil, fmt.Errorf("create integration external link: %w", create.Error)
	}
	if create.RowsAffected == 1 {
		return link, nil, nil
	}
	// A concurrent winner is re-read and compared. Identity disagreement is a
	// persisted conflict; the incoming command savepoint is never allowed to
	// overwrite the winner.
	return reconcileExternalLinkTxAttempt(
		tx,
		target,
		message,
		input,
		command,
		false,
	)
}

func findInboxMessageTx(
	tx *gorm.DB,
	projectID uint,
	connectionID uint,
	externalMessageID string,
) (*models.InboxMessage, bool, error) {
	var message models.InboxMessage
	err := tx.Where(
		"project_id = ? AND connection_id = ? AND external_message_id = ?",
		projectID,
		connectionID,
		externalMessageID,
	).First(&message).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &message, true, nil
}

func validateIntegrationCommandResult(
	result IntegrationDomainCommandResult,
) error {
	if result.Status != "" &&
		result.Status != models.InboxReceiptStatusApplied &&
		result.Status != models.InboxReceiptStatusNoop {
		return errors.New("integration command returned an unsupported receipt status")
	}
	if strings.TrimSpace(result.ResourceType) == "" ||
		len(result.ResourceType) > 64 ||
		strings.TrimSpace(result.ResourceID) == "" ||
		len(result.ResourceID) > 128 {
		return errors.New("integration command returned an invalid resource identity")
	}
	if result.ResourceVersion == 0 {
		return errors.New("integration command returned no resource version")
	}
	status := result.Status
	if status == "" {
		status = models.InboxReceiptStatusApplied
	}
	if strings.TrimSpace(result.OperationID) == "" || len(result.OperationID) > 64 {
		return errors.New("integration command returned no operation id")
	}
	if status == models.InboxReceiptStatusApplied &&
		(strings.TrimSpace(result.EventID) == "" || len(result.EventID) > 64) {
		return errors.New("applied integration command returned no event id")
	}
	if len(result.ReceiptData) > 0 && !json.Valid(result.ReceiptData) {
		return errors.New("integration command returned invalid receipt JSON")
	}
	return nil
}

func sanitizeIntegrationError(err error) string {
	if err == nil {
		return "integration domain command failed"
	}
	value := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, err.Error())
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		value = "integration domain command failed"
	}
	if len(value) > 500 {
		value = truncateIntegrationRunes(value, 500)
	}
	return value
}

func boundedIntegrationControlValue(value string, maximum int) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if len(value) > maximum {
		return truncateIntegrationRunes(value, maximum)
	}
	return value
}

func truncateIntegrationRunes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func normalizedIntegrationContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "application/octet-stream"
	}
	return value
}
