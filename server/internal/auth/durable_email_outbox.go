package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"github.com/seaworld008/chronodesk/server/internal/security"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AuthEmailOutboxRepository interface {
	QueueEmailVerification(context.Context, *EmailVerification, string) error
	QueuePasswordReset(context.Context, *PasswordReset) error
	VerifyEmailAndQueueWelcome(context.Context, string, time.Time) (uint, error)
}

// GormAuthEmailOutboxRepository owns authentication writes whose email intent
// must commit atomically with business state. It writes the shared
// DomainEvent/Outbox tables and never performs SMTP.
type GormAuthEmailOutboxRepository struct {
	db          *gorm.DB
	protector   security.Protector
	scope       models.ProjectScope
	eventSource string
}

func NewGormAuthEmailOutboxRepository(
	db *gorm.DB,
	protector security.Protector,
	scope models.ProjectScope,
	eventSource string,
) (*GormAuthEmailOutboxRepository, error) {
	if db == nil {
		return nil, errors.New("authentication email Outbox database is required")
	}
	if protector == nil {
		return nil, security.ErrKeyringUnavailable
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf(
			"authentication email Outbox project scope: %w",
			err,
		)
	}
	eventSource = strings.TrimSpace(eventSource)
	if eventSource == "" {
		eventSource = "urn:chronodesk:auth"
	}
	return &GormAuthEmailOutboxRepository{
		db:          db,
		protector:   protector,
		scope:       scope,
		eventSource: eventSource,
	}, nil
}

// withProjectTransaction binds the server-resolved DEFAULT project to both
// PostgreSQL RLS and the immutable authentication SystemActor provenance.
// Authentication state and its email intent therefore commit or roll back as
// one project-scoped transaction.
func (r *GormAuthEmailOutboxRepository) withProjectTransaction(
	ctx context.Context,
	actor models.ActorRef,
	fn func(context.Context, *gorm.DB) error,
) error {
	if r == nil || r.db == nil || fn == nil {
		return errors.New(
			"authentication email Outbox project transaction is unavailable",
		)
	}
	traceID := ""
	correlationID := ""
	existing, existingErr := services.OperationContextFromContext(ctx)
	if scopeddb.HasTransaction(ctx) &&
		(existingErr != nil || existing.Scope != r.scope) {
		return errors.New(
			"authentication email Outbox transaction project scope mismatch",
		)
	}
	if existingErr == nil {
		traceID = existing.TraceID
		correlationID = existing.CorrelationID
	}
	operationCtx, err := services.WithOperationContext(
		ctx,
		services.OperationContext{
			Scope:         r.scope,
			Actor:         actor,
			Source:        services.SourceProtocolWorker,
			TraceID:       traceID,
			CorrelationID: correlationID,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"bind authentication email Outbox operation: %w",
			err,
		)
	}
	if scopeddb.HasTransaction(operationCtx) {
		return fn(operationCtx, r.db.WithContext(operationCtx))
	}
	return scopeddb.WithProjectScopeContextTransaction(
		operationCtx,
		r.db,
		r.scope,
		func(scopedCtx context.Context) error {
			return fn(scopedCtx, r.db.WithContext(scopedCtx))
		},
	)
}

func (r *GormAuthEmailOutboxRepository) CommitRegistration(
	ctx context.Context,
	command *RegistrationCommit,
) (*RegistrationCommitResult, error) {
	if err := validateRegistrationCommit(command); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, ErrAtomicRegistrationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scopeddb.HasTransaction(ctx) {
		return nil, ErrAtomicRegistrationUnavailable
	}

	modelUser := authUserToModel(command.User, command.Profile)
	modelProfile := authProfileToModel(command.Profile)
	var storedVerification *EmailVerification
	var builtSession *RegistrationSession
	var storedSession *preparedLoginSessionRows
	err := r.withProjectTransaction(
		ctx,
		models.SystemActor("auth-registration"),
		func(txCtx context.Context, tx *gorm.DB) error {
			if err := lockAndMatchEmailVerificationPolicyTx(
				tx,
				command.ExpectedEmailPolicy,
			); err != nil {
				return err
			}
			if err := createRegistrationUserTx(tx, &modelUser); err != nil {
				return err
			}
			modelProfile.UserID = modelUser.ID
			if err := tx.Create(&modelProfile).Error; err != nil {
				return err
			}

			if command.Verification != nil {
				verificationCopy := *command.Verification
				verificationCopy.UserID = modelUser.ID
				if verificationCopy.Email == "" {
					verificationCopy.Email = modelUser.Email
				}
				if verificationCopy.Email != modelUser.Email {
					return errors.New("email verification recipient does not match user")
				}
				if err := r.createEmailVerificationTx(
					txCtx,
					tx,
					&verificationCopy,
				); err != nil {
					return err
				}
				storedVerification = &verificationCopy
				_, err := services.AppendEmailOutboxTx(txCtx, tx, services.EmailOutboxEventInput{
					Scope:   r.scope,
					Source:  r.eventSource,
					Type:    eventcontract.EmailVerificationRequestedEventType,
					Subject: fmt.Sprintf("user/%d", modelUser.ID),
					Actor:   models.SystemActor("auth-registration"),
					Data: services.EmailIntentReference{
						UserID:         modelUser.ID,
						VerificationID: verificationCopy.ID,
						RequestReason:  "registration",
					},
					DestinationID: services.AuthVerificationEmailDestinationPrefix +
						strconv.FormatUint(uint64(verificationCopy.ID), 10),
				})
				return err
			}

			session, err := command.BuildSession(
				modelUser.ID,
				*command.SessionIssuedAt,
			)
			if err != nil {
				return err
			}
			if session == nil ||
				strings.TrimSpace(session.AccessToken) == "" ||
				strings.TrimSpace(session.RefreshToken) == "" ||
				strings.TrimSpace(session.SessionID) == "" ||
				session.RefreshAuthority == nil ||
				session.RefreshAuthority.Token != session.RefreshToken ||
				session.RefreshAuthority.SessionID != session.SessionID {
				return ErrInvalidToken
			}
			if err := validateRegistrationSession(
				command.User,
				session,
				*command.SessionIssuedAt,
			); err != nil {
				return err
			}
			prepared, err := prepareLoginSessionRows(
				modelUser.ID,
				*command.SessionIssuedAt,
				session.RefreshAuthority,
				session.LoginHistory,
				session.SuccessfulAttempt,
			)
			if err != nil {
				return err
			}
			if err := createLoginSessionRowsTx(tx, prepared); err != nil {
				return err
			}
			builtSession = session
			storedSession = prepared

			_, err = services.AppendEmailOutboxTx(txCtx, tx, services.EmailOutboxEventInput{
				Scope:   r.scope,
				Source:  r.eventSource,
				Type:    eventcontract.UserRegisteredEventType,
				Subject: fmt.Sprintf("user/%d", modelUser.ID),
				Actor:   models.SystemActor("auth-registration"),
				Data: services.EmailIntentReference{
					UserID: modelUser.ID,
				},
				DestinationID: services.AuthWelcomeEmailDestinationPrefix +
					strconv.FormatUint(uint64(modelUser.ID), 10),
			})
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	copyCreatedAuthUser(command.User, &modelUser)
	copyCreatedAuthProfile(command.Profile, &modelProfile)
	if command.Verification != nil && storedVerification != nil {
		command.Verification.ID = storedVerification.ID
		command.Verification.UserID = storedVerification.UserID
		command.Verification.CreatedAt = storedVerification.CreatedAt
		command.Verification.UpdatedAt = storedVerification.UpdatedAt
	}
	if builtSession != nil && storedSession != nil {
		copyPreparedLoginSessionRowIDs(
			builtSession.RefreshAuthority,
			builtSession.LoginHistory,
			builtSession.SuccessfulAttempt,
			storedSession,
		)
	}
	return &RegistrationCommitResult{Session: builtSession}, nil
}

func validateRegistrationCommit(command *RegistrationCommit) error {
	if command == nil ||
		command.User == nil ||
		command.Profile == nil ||
		command.ExpectedEmailPolicy == nil ||
		command.CommittedAt.IsZero() {
		return ErrAtomicRegistrationUnavailable
	}
	user := command.User
	policy := command.ExpectedEmailPolicy
	if !user.PlatformRole.IsValid() || !isValidUserStatus(user.Status) {
		return ErrInvalidAccountState
	}
	if user.ID != 0 || command.Profile.ID != 0 ||
		command.Profile.UserID != 0 {
		return ErrInvalidToken
	}
	if user.EmailVerified == policy.Enabled ||
		(command.Verification != nil) != policy.Enabled {
		return ErrEmailVerificationPolicyChanged
	}
	if user.PasswordChangedAt == nil ||
		user.PasswordChangedAt.After(command.CommittedAt) {
		return ErrInvalidToken
	}
	if policy.Enabled {
		if command.BuildSession != nil ||
			command.SessionIssuedAt != nil ||
			user.EmailVerifiedAt != nil ||
			user.LastLoginAt != nil ||
			strings.TrimSpace(command.Verification.Token) == "" {
			return ErrEmailVerificationPolicyChanged
		}
		return nil
	}
	if command.BuildSession == nil ||
		command.SessionIssuedAt == nil ||
		command.SessionIssuedAt.IsZero() ||
		!command.SessionIssuedAt.Equal(command.CommittedAt) ||
		user.EmailVerifiedAt == nil ||
		user.LastLoginAt == nil ||
		!user.PasswordChangedAt.Before(*command.SessionIssuedAt) ||
		!user.EmailVerifiedAt.Equal(*command.SessionIssuedAt) ||
		!user.LastLoginAt.Equal(*command.SessionIssuedAt) {
		return ErrEmailVerificationPolicyChanged
	}
	return nil
}

var errRegistrationUserInsertConflict = errors.New(
	"registration user insert conflicted outside identity keys",
)

func createRegistrationUserTx(tx *gorm.DB, user *models.User) error {
	if tx == nil || user == nil || user.ID != 0 {
		return ErrInvalidToken
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(user)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if result.RowsAffected != 0 {
		return errRegistrationUserInsertConflict
	}
	var identity models.User
	identityResult := tx.Unscoped().
		Select("id").
		Where("email = ? OR username = ?", user.Email, user.Username).
		Limit(1).
		Find(&identity)
	if identityResult.Error != nil {
		return identityResult.Error
	}
	if identityResult.RowsAffected == 1 {
		return ErrUserExists
	}
	return errRegistrationUserInsertConflict
}

func validateRegistrationSession(
	user *User,
	session *RegistrationSession,
	issuedAt time.Time,
) error {
	if user == nil ||
		user.ID != 0 ||
		user.LastLoginAt == nil ||
		!user.LastLoginAt.Equal(issuedAt) ||
		user.PasswordChangedAt == nil ||
		!user.PasswordChangedAt.Before(issuedAt) ||
		session == nil ||
		session.RefreshAuthority == nil ||
		session.LoginHistory == nil ||
		session.SuccessfulAttempt == nil ||
		strings.TrimSpace(session.AccessToken) == "" ||
		strings.TrimSpace(session.AccessToken) != session.AccessToken ||
		strings.TrimSpace(session.RefreshToken) == "" ||
		strings.TrimSpace(session.RefreshToken) != session.RefreshToken ||
		session.AccessToken == session.RefreshToken ||
		strings.TrimSpace(session.SessionID) == "" ||
		strings.TrimSpace(session.SessionID) != session.SessionID {
		return ErrInvalidToken
	}
	refresh := session.RefreshAuthority
	history := session.LoginHistory
	attempt := session.SuccessfulAttempt
	if refresh.ID != 0 ||
		!isZeroRegistrationUserAssociation(refresh.User) ||
		refresh.Token != session.RefreshToken ||
		refresh.SessionID != session.SessionID ||
		refresh.Revoked ||
		refresh.RevokedAt != nil ||
		refresh.RotatedAt != nil ||
		strings.TrimSpace(refresh.ReplacedByToken) != "" ||
		!refresh.CreatedAt.Equal(issuedAt) {
		return ErrInvalidToken
	}
	if history.ID != 0 ||
		!isZeroRegistrationUserAssociation(history.User) ||
		history.Username != user.Username ||
		history.Email != user.Email ||
		history.IPAddress != refresh.IPAddress ||
		history.UserAgent != refresh.UserAgent ||
		history.SessionID != session.SessionID ||
		history.LoginMethod != models.LoginMethodPassword ||
		history.LoginStatus != models.LoginStatusSuccess ||
		!history.IsActive ||
		history.LogoutTime != nil ||
		history.SessionDuration != nil ||
		strings.TrimSpace(history.FailureReason) != "" ||
		!history.LoginTime.Equal(issuedAt) ||
		history.LastActivityAt == nil ||
		!history.LastActivityAt.Equal(issuedAt) {
		return ErrInvalidToken
	}
	if attempt.ID != 0 ||
		attempt.User != nil ||
		attempt.Email != user.Email ||
		attempt.IPAddress != refresh.IPAddress ||
		attempt.UserAgent != refresh.UserAgent ||
		!attempt.Success ||
		strings.TrimSpace(attempt.FailReason) != "" ||
		!attempt.CreatedAt.Equal(issuedAt) {
		return ErrInvalidToken
	}
	return nil
}

func isZeroRegistrationUserAssociation(user models.User) bool {
	return reflect.ValueOf(user).IsZero()
}

func (r *GormAuthEmailOutboxRepository) QueueEmailVerification(
	ctx context.Context,
	verification *EmailVerification,
	reason string,
) error {
	if verification == nil ||
		verification.UserID == 0 ||
		strings.TrimSpace(verification.Token) == "" {
		return ErrInvalidToken
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "resend"
	}
	stored := *verification
	err := r.withProjectTransaction(
		ctx,
		models.SystemActor("auth-email-verification"),
		func(txCtx context.Context, tx *gorm.DB) error {
			if err := validateAuthEmailRecipientTx(
				tx,
				stored.UserID,
				stored.Email,
			); err != nil {
				return err
			}
			if err := r.createEmailVerificationTx(txCtx, tx, &stored); err != nil {
				return err
			}
			_, err := services.AppendEmailOutboxTx(txCtx, tx, services.EmailOutboxEventInput{
				Scope:   r.scope,
				Source:  r.eventSource,
				Type:    eventcontract.EmailVerificationRequestedEventType,
				Subject: fmt.Sprintf("user/%d", stored.UserID),
				Actor:   models.SystemActor("auth-email-verification"),
				Data: services.EmailIntentReference{
					UserID:         stored.UserID,
					VerificationID: stored.ID,
					RequestReason:  reason,
				},
				DestinationID: services.AuthVerificationEmailDestinationPrefix +
					strconv.FormatUint(uint64(stored.ID), 10),
			})
			return err
		},
	)
	if err != nil {
		return err
	}
	verification.ID = stored.ID
	verification.CreatedAt = stored.CreatedAt
	verification.UpdatedAt = stored.UpdatedAt
	return nil
}

func (r *GormAuthEmailOutboxRepository) QueuePasswordReset(
	ctx context.Context,
	reset *PasswordReset,
) error {
	if reset == nil ||
		reset.UserID == 0 ||
		strings.TrimSpace(reset.Token) == "" {
		return ErrInvalidToken
	}
	stored := *reset
	err := r.withProjectTransaction(
		ctx,
		models.SystemActor("auth-password-reset"),
		func(txCtx context.Context, tx *gorm.DB) error {
			if err := validateAuthEmailRecipientTx(
				tx,
				stored.UserID,
				stored.Email,
			); err != nil {
				return err
			}
			if err := r.createPasswordResetTx(txCtx, tx, &stored); err != nil {
				return err
			}
			_, err := services.AppendEmailOutboxTx(txCtx, tx, services.EmailOutboxEventInput{
				Scope:   r.scope,
				Source:  r.eventSource,
				Type:    eventcontract.PasswordResetRequestedEventType,
				Subject: fmt.Sprintf("user/%d", stored.UserID),
				Actor:   models.SystemActor("auth-password-reset"),
				Data: services.EmailIntentReference{
					UserID:          stored.UserID,
					PasswordResetID: stored.ID,
					RequestReason:   "forgot-password",
				},
				DestinationID: services.AuthPasswordResetEmailDestinationPrefix +
					strconv.FormatUint(uint64(stored.ID), 10),
			})
			return err
		},
	)
	if err != nil {
		return err
	}
	reset.ID = stored.ID
	reset.CreatedAt = stored.CreatedAt
	reset.UpdatedAt = stored.UpdatedAt
	return nil
}

func validateAuthEmailRecipientTx(
	tx *gorm.DB,
	userID uint,
	email string,
) error {
	// Issuance and consumption lock the same account row. A reset or
	// verification request therefore linearizes wholly before or after a
	// successful one-time credential consumption; it cannot insert an
	// unaccounted credential in the middle of account-wide invalidation.
	return lockAuthCredentialUserEmail(tx, userID, email)
}

func (r *GormAuthEmailOutboxRepository) VerifyEmailAndQueueWelcome(
	ctx context.Context,
	token string,
	verifiedAt time.Time,
) (uint, error) {
	digest := bearerTokenDigest("email-verification", token)
	var userID uint
	err := r.withProjectTransaction(
		ctx,
		models.SystemActor("auth-email-verification"),
		func(txCtx context.Context, tx *gorm.DB) error {
			var verification EmailVerification
			if err := tx.Where(
				"token = ? AND used = ? AND expires_at > ?",
				digest,
				false,
				verifiedAt,
			).Take(&verification).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrInvalidToken
				}
				return err
			}
			if err := lockAuthCredentialUserEmail(
				tx,
				verification.UserID,
				verification.Email,
			); err != nil {
				return err
			}
			if err := tx.Where(
				"id = ? AND token = ? AND used = ? AND expires_at > ?",
				verification.ID,
				digest,
				false,
				verifiedAt,
			).Take(&verification).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrInvalidToken
				}
				return err
			}
			result := tx.Model(&EmailVerification{}).
				Where(
					"id = ? AND used = ? AND expires_at > ?",
					verification.ID,
					false,
					verifiedAt,
				).
				Updates(map[string]any{
					"used":            true,
					"used_at":         &verifiedAt,
					"delivery_secret": "",
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrInvalidToken
			}

			result = tx.Model(&models.User{}).
				Where(
					"id = ? AND email = ?",
					verification.UserID,
					verification.Email,
				).
				Updates(map[string]any{
					"email_verified":    true,
					"email_verified_at": &verifiedAt,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrUserNotFound
			}
			userID = verification.UserID
			_, err := services.AppendEmailOutboxTx(txCtx, tx, services.EmailOutboxEventInput{
				Scope:   r.scope,
				Source:  r.eventSource,
				Type:    eventcontract.EmailVerifiedEventType,
				Subject: fmt.Sprintf("user/%d", userID),
				Actor:   models.SystemActor("auth-email-verification"),
				Data: services.EmailIntentReference{
					UserID: userID,
				},
				DestinationID: services.AuthWelcomeEmailDestinationPrefix +
					strconv.FormatUint(uint64(userID), 10),
			})
			return err
		},
	)
	return userID, err
}

func (r *GormAuthEmailOutboxRepository) createEmailVerificationTx(
	ctx context.Context,
	tx *gorm.DB,
	verification *EmailVerification,
) error {
	plaintext := verification.Token
	verification.Token = bearerTokenDigest("email-verification", plaintext)
	verification.DeliverySecret = ""
	if err := tx.WithContext(ctx).Create(verification).Error; err != nil {
		verification.Token = plaintext
		return err
	}
	envelope, err := security.ProtectOptional(
		r.protector,
		plaintext,
		emailVerificationDeliverySecretAAD(verification.ID),
	)
	if err != nil {
		verification.Token = plaintext
		return fmt.Errorf("protect email verification delivery secret: %w", err)
	}
	if err := tx.WithContext(ctx).Model(&EmailVerification{}).
		Where("id = ?", verification.ID).
		UpdateColumn("delivery_secret", envelope).Error; err != nil {
		verification.Token = plaintext
		return err
	}
	verification.Token = plaintext
	verification.DeliverySecret = envelope
	return nil
}

func (r *GormAuthEmailOutboxRepository) createPasswordResetTx(
	ctx context.Context,
	tx *gorm.DB,
	reset *PasswordReset,
) error {
	plaintext := reset.Token
	reset.Token = bearerTokenDigest("password-reset", plaintext)
	reset.DeliverySecret = ""
	if err := tx.WithContext(ctx).Create(reset).Error; err != nil {
		reset.Token = plaintext
		return err
	}
	envelope, err := security.ProtectOptional(
		r.protector,
		plaintext,
		passwordResetDeliverySecretAAD(reset.ID),
	)
	if err != nil {
		reset.Token = plaintext
		return fmt.Errorf("protect password reset delivery secret: %w", err)
	}
	if err := tx.WithContext(ctx).Model(&PasswordReset{}).
		Where("id = ?", reset.ID).
		UpdateColumn("delivery_secret", envelope).Error; err != nil {
		reset.Token = plaintext
		return err
	}
	reset.Token = plaintext
	reset.DeliverySecret = envelope
	return nil
}

func authUserToModel(user *User, profile *UserProfile) models.User {
	return models.User{
		Username:         user.Username,
		Email:            user.Email,
		PasswordHash:     user.PasswordHash,
		FirstName:        profile.FirstName,
		LastName:         profile.LastName,
		DisplayName:      profile.DisplayName,
		PlatformRole:     models.PlatformRole(user.PlatformRole),
		Status:           user.Status,
		EmailVerified:    user.EmailVerified,
		EmailVerifiedAt:  user.EmailVerifiedAt,
		LastLoginAt:      user.LastLoginAt,
		LoginAttempts:    user.FailedLoginCount,
		LockedUntil:      user.LockedUntil,
		PasswordResetAt:  user.PasswordChangedAt,
		Department:       profile.Department,
		JobTitle:         profile.Position,
		TwoFactorEnabled: false,
		TwoFactorSecret:  "",
		BackupCodes:      "",
	}
}

func authProfileToModel(profile *UserProfile) models.UserProfile {
	timezone := profile.Timezone
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	language := profile.Language
	if language == "" {
		language = "zh-CN"
	}
	return models.UserProfile{
		Avatar:   profile.Avatar,
		Phone:    profile.Phone,
		Timezone: timezone,
		Language: language,
	}
}

func copyCreatedAuthUser(user *User, modelUser *models.User) {
	user.ID = modelUser.ID
	user.CreatedAt = modelUser.CreatedAt
	user.UpdatedAt = modelUser.UpdatedAt
}

func copyCreatedAuthProfile(profile *UserProfile, modelProfile *models.UserProfile) {
	profile.ID = modelProfile.ID
	profile.UserID = modelProfile.UserID
	profile.CreatedAt = modelProfile.CreatedAt
	profile.UpdatedAt = modelProfile.UpdatedAt
}

func emailVerificationDeliverySecretAAD(id uint) []byte {
	return security.FieldAAD(
		"email_verifications",
		strconv.FormatUint(uint64(id), 10),
		"delivery_secret",
	)
}

func passwordResetDeliverySecretAAD(id uint) []byte {
	return security.FieldAAD(
		"password_resets",
		strconv.FormatUint(uint64(id), 10),
		"delivery_secret",
	)
}

// AuthEmailOutboxConsumer performs one bounded authentication email attempt.
// Successful attempts persist an intent-specific receipt before the shared
// worker acknowledges the Outbox row, making crash recovery replay-safe.
type AuthEmailOutboxConsumer struct {
	db        *gorm.DB
	protector security.Protector
	sender    EmailService
}

func NewAuthEmailOutboxConsumer(
	db *gorm.DB,
	protector security.Protector,
	sender EmailService,
) (*AuthEmailOutboxConsumer, error) {
	if db == nil {
		return nil, errors.New("authentication email consumer database is required")
	}
	if protector == nil {
		return nil, security.ErrKeyringUnavailable
	}
	if sender == nil {
		return nil, errors.New("authentication email sender is required")
	}
	return &AuthEmailOutboxConsumer{
		db:        db,
		protector: protector,
		sender:    sender,
	}, nil
}

func (c *AuthEmailOutboxConsumer) DeliverAuthEmailOutbox(
	ctx context.Context,
	delivery *models.OutboxDelivery,
	event services.CloudEventEnvelope,
) error {
	if delivery == nil {
		return errors.New("authentication email Outbox delivery is required")
	}
	var reference services.EmailIntentReference
	if err := json.Unmarshal(event.Data, &reference); err != nil {
		return errors.New("authentication email event reference is invalid")
	}

	switch {
	case strings.HasPrefix(
		delivery.DestinationID,
		services.AuthVerificationEmailDestinationPrefix,
	):
		return c.deliverVerification(ctx, delivery.DestinationID, event, reference)
	case strings.HasPrefix(
		delivery.DestinationID,
		services.AuthPasswordResetEmailDestinationPrefix,
	):
		return c.deliverPasswordReset(ctx, delivery.DestinationID, event, reference)
	case strings.HasPrefix(
		delivery.DestinationID,
		services.AuthWelcomeEmailDestinationPrefix,
	):
		return c.deliverWelcome(ctx, delivery.DestinationID, event, reference)
	default:
		return fmt.Errorf(
			"unsupported authentication email Outbox destination %q",
			delivery.DestinationID,
		)
	}
}

func (c *AuthEmailOutboxConsumer) deliverVerification(
	ctx context.Context,
	destination string,
	event services.CloudEventEnvelope,
	reference services.EmailIntentReference,
) error {
	id, err := parseEmailDestinationID(
		destination,
		services.AuthVerificationEmailDestinationPrefix,
	)
	if err != nil ||
		event.Type != eventcontract.EmailVerificationRequestedEventType ||
		reference.VerificationID != id ||
		reference.UserID == 0 {
		return errors.New("email verification Outbox reference is inconsistent")
	}
	var verification EmailVerification
	if err := c.db.WithContext(ctx).First(&verification, id).Error; err != nil {
		return fmt.Errorf("load email verification intent: %w", err)
	}
	if verification.UserID != reference.UserID {
		return errors.New("email verification Outbox user reference is inconsistent")
	}
	if verification.EmailDeliveredAt != nil ||
		verification.Used ||
		!verification.ExpiresAt.After(time.Now()) {
		return nil
	}
	token, err := security.RevealOptional(
		c.protector,
		verification.DeliverySecret,
		emailVerificationDeliverySecretAAD(verification.ID),
	)
	if err != nil || token == "" {
		return errors.New("email verification delivery secret is unavailable")
	}
	if err := c.sender.SendVerificationEmail(ctx, verification.Email, token); err != nil {
		return fmt.Errorf("send verification email Outbox attempt: %w", err)
	}
	deliveredAt := time.Now().UTC()
	return c.db.WithContext(ctx).Model(&EmailVerification{}).
		Where("id = ? AND email_delivered_at IS NULL", verification.ID).
		Updates(map[string]any{
			"email_delivered_at": &deliveredAt,
			"delivery_secret":    "",
		}).Error
}

func (c *AuthEmailOutboxConsumer) deliverPasswordReset(
	ctx context.Context,
	destination string,
	event services.CloudEventEnvelope,
	reference services.EmailIntentReference,
) error {
	id, err := parseEmailDestinationID(
		destination,
		services.AuthPasswordResetEmailDestinationPrefix,
	)
	if err != nil ||
		event.Type != eventcontract.PasswordResetRequestedEventType ||
		reference.PasswordResetID != id ||
		reference.UserID == 0 {
		return errors.New("password reset email Outbox reference is inconsistent")
	}
	var reset PasswordReset
	if err := c.db.WithContext(ctx).First(&reset, id).Error; err != nil {
		return fmt.Errorf("load password reset email intent: %w", err)
	}
	if reset.UserID != reference.UserID {
		return errors.New("password reset email Outbox user reference is inconsistent")
	}
	if reset.EmailDeliveredAt != nil || reset.Used || !reset.ExpiresAt.After(time.Now()) {
		return nil
	}
	token, err := security.RevealOptional(
		c.protector,
		reset.DeliverySecret,
		passwordResetDeliverySecretAAD(reset.ID),
	)
	if err != nil || token == "" {
		return errors.New("password reset delivery secret is unavailable")
	}
	if err := c.sender.SendPasswordResetEmail(ctx, reset.Email, token); err != nil {
		return fmt.Errorf("send password reset email Outbox attempt: %w", err)
	}
	deliveredAt := time.Now().UTC()
	return c.db.WithContext(ctx).Model(&PasswordReset{}).
		Where("id = ? AND email_delivered_at IS NULL", reset.ID).
		Updates(map[string]any{
			"email_delivered_at": &deliveredAt,
			"delivery_secret":    "",
		}).Error
}

func (c *AuthEmailOutboxConsumer) deliverWelcome(
	ctx context.Context,
	destination string,
	event services.CloudEventEnvelope,
	reference services.EmailIntentReference,
) error {
	id, err := parseEmailDestinationID(
		destination,
		services.AuthWelcomeEmailDestinationPrefix,
	)
	if err != nil ||
		(event.Type != eventcontract.UserRegisteredEventType &&
			event.Type != eventcontract.EmailVerifiedEventType) ||
		reference.UserID != id {
		return errors.New("welcome email Outbox reference is inconsistent")
	}
	var user models.User
	if err := c.db.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("load welcome email user: %w", err)
	}
	if user.WelcomeEmailDeliveredAt != nil || user.Status == models.UserStatusDeleted {
		return nil
	}
	if err := c.sender.SendWelcomeEmail(ctx, user.Email, user.Username); err != nil {
		return fmt.Errorf("send welcome email Outbox attempt: %w", err)
	}
	deliveredAt := time.Now().UTC()
	return c.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ? AND welcome_email_delivered_at IS NULL", user.ID).
		Update("welcome_email_delivered_at", &deliveredAt).Error
}

func parseEmailDestinationID(destination, prefix string) (uint, error) {
	raw, found := strings.CutPrefix(destination, prefix)
	if !found || raw == "" || strings.Contains(raw, ":") {
		return 0, errors.New("invalid authentication email Outbox destination")
	}
	value, err := safeconv.ParsePositiveUint(raw)
	if err != nil {
		return 0, errors.New("invalid authentication email Outbox record ID")
	}
	return value, nil
}
