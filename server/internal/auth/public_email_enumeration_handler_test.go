package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type publicEmailEnumerationUserRepository struct {
	UserRepository
	user      *User
	lookupErr error
}

func (repository *publicEmailEnumerationUserRepository) GetByEmail(
	_ context.Context,
	email string,
) (*User, error) {
	if repository.lookupErr != nil {
		return nil, repository.lookupErr
	}
	if repository.user == nil || repository.user.Email != email {
		return nil, ErrUserNotFound
	}
	copied := *repository.user
	return &copied, nil
}

type publicEmailEnumerationOutboxRepository struct {
	AuthEmailOutboxRepository
	err error
}

func (repository *publicEmailEnumerationOutboxRepository) QueuePasswordReset(
	context.Context,
	*PasswordReset,
) error {
	return repository.err
}

func (repository *publicEmailEnumerationOutboxRepository) QueueEmailVerification(
	context.Context,
	*EmailVerification,
	string,
) error {
	return repository.err
}

func TestPublicEmailDependencyFailuresMatchUnknownAccountResponses(t *testing.T) {
	injected := errors.New("injected authentication email dependency failure")
	for _, test := range []struct {
		name   string
		path   string
		handle func(*AuthHandler, HTTPContext)
	}{
		{
			name: "forgot password",
			path: "/api/auth/forgot-password",
			handle: func(handler *AuthHandler, context HTTPContext) {
				handler.ForgotPassword(context)
			},
		},
		{
			name: "resend verification",
			path: "/api/auth/resend-verification",
			handle: func(handler *AuthHandler, context HTTPContext) {
				handler.ResendVerification(context)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, failure := range []struct {
				name          string
				userLookupErr error
				outboxErr     error
			}{
				{name: "account repository unavailable", userLookupErr: injected},
				{name: "durable email dependency unavailable", outboxErr: injected},
			} {
				t.Run(failure.name, func(t *testing.T) {
					const knownEmail = "known-public-email@example.test"
					userRepository := &publicEmailEnumerationUserRepository{
						user: &User{
							ID:            42,
							Email:         knownEmail,
							EmailVerified: false,
						},
					}
					outboxRepository := &publicEmailEnumerationOutboxRepository{
						err: failure.outboxErr,
					}
					logger := &requestContextLogger{}
					service := &AuthService{
						userRepo:        userRepository,
						emailOutboxRepo: outboxRepository,
						config: &AuthConfig{
							EmailVerificationExpire: time.Hour,
							PasswordResetExpire:     time.Hour,
						},
					}
					handler := NewAuthHandler(service, logger)

					unknown := executePublicEmailRequest(
						t,
						handler,
						test.path,
						"unknown-public-email@example.test",
						test.handle,
					)
					userRepository.lookupErr = failure.userLookupErr
					failed := executePublicEmailRequest(
						t,
						handler,
						test.path,
						knownEmail,
						test.handle,
					)

					if unknown.Code != http.StatusOK ||
						failed.Code != unknown.Code ||
						failed.Body.String() != unknown.Body.String() {
						t.Fatalf(
							"unknown/failure responses differ: %d %q / %d %q",
							unknown.Code,
							unknown.Body.String(),
							failed.Code,
							failed.Body.String(),
						)
					}
					if !logger.containsLevel("error") {
						t.Fatal("dependency failure was not recorded internally")
					}
				})
			}
		})
	}
}

func executePublicEmailRequest(
	t *testing.T,
	handler *AuthHandler,
	path,
	email string,
	handle func(*AuthHandler, HTTPContext),
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(path, func(context *gin.Context) {
		handle(handler, NewGinHTTPContext(context))
	})
	request := httptest.NewRequest(
		http.MethodPost,
		path,
		bytes.NewBufferString(`{"email":"`+email+`"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
