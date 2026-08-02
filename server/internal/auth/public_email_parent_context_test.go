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

type publicEmailParentContextUserRepository struct {
	UserRepository
	waitForContext bool
	err            error
}

func (repository *publicEmailParentContextUserRepository) GetByEmail(
	ctx context.Context,
	_ string,
) (*User, error) {
	if repository.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if repository.err != nil {
		return nil, repository.err
	}
	return nil, ErrUserNotFound
}

type publicEmailParentContextEndpoint struct {
	name   string
	path   string
	invoke func(*AuthHandler, HTTPContext)
}

func publicEmailParentContextEndpoints() []publicEmailParentContextEndpoint {
	return []publicEmailParentContextEndpoint{
		{
			name: "forgot password",
			path: "/api/auth/forgot-password",
			invoke: func(handler *AuthHandler, request HTTPContext) {
				handler.ForgotPassword(request)
			},
		},
		{
			name: "resend verification",
			path: "/api/auth/resend-verification",
			invoke: func(handler *AuthHandler, request HTTPContext) {
				handler.ResendVerification(request)
			},
		},
	}
}

func TestPublicEmailHandlersAbortWhenParentContextAndServiceShareTermination(
	t *testing.T,
) {
	terminations := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		wantStatus int
	}{
		{
			name: "canceled",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			wantStatus: statusClientClosedRequest,
		},
		{
			name: "deadline exceeded",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(
					context.Background(),
					time.Now().Add(-time.Second),
				)
			},
			wantStatus: http.StatusRequestTimeout,
		},
	}

	for _, endpoint := range publicEmailParentContextEndpoints() {
		endpoint := endpoint
		t.Run(endpoint.name, func(t *testing.T) {
			for _, termination := range terminations {
				termination := termination
				t.Run(termination.name, func(t *testing.T) {
					logger := &requestContextLogger{}
					handler := newPublicEmailParentContextHandler(
						&publicEmailParentContextUserRepository{
							waitForContext: true,
						},
						logger,
					)
					parentContext, cancel := termination.newContext()
					defer cancel()

					response, aborted := executePublicEmailParentContextRequest(
						t,
						handler,
						endpoint,
						parentContext,
					)

					if response.Code != termination.wantStatus {
						t.Fatalf(
							"status = %d, want %d; body=%q",
							response.Code,
							termination.wantStatus,
							response.Body.String(),
						)
					}
					if response.Body.Len() != 0 {
						t.Fatalf(
							"terminated parent request wrote success JSON: %q",
							response.Body.String(),
						)
					}
					if !aborted {
						t.Fatal("terminated parent request was not aborted")
					}
					if !logger.containsLevel("debug") ||
						logger.containsLevel("error") {
						t.Fatalf(
							"parent termination log levels = %+v",
							logger.entries,
						)
					}
				})
			}
		})
	}
}

func TestPublicEmailHandlersKeepDerivedTimeoutEnumerationSafe(t *testing.T) {
	for _, endpoint := range publicEmailParentContextEndpoints() {
		endpoint := endpoint
		t.Run(endpoint.name, func(t *testing.T) {
			unknownResponse := executeUnknownPublicEmailParentContextRequest(
				t,
				endpoint,
			)
			logger := &requestContextLogger{}
			handler := newPublicEmailParentContextHandler(
				&publicEmailParentContextUserRepository{
					waitForContext: true,
				},
				logger,
			)
			// Exercise the same child deadline created by the production handler
			// without making the unit test wait for its full default timeout.
			handler.requestTimeout = 5 * time.Millisecond

			response, aborted := executePublicEmailParentContextRequest(
				t,
				handler,
				endpoint,
				context.Background(),
			)

			assertPublicEmailParentContextMatchesUnknown(
				t,
				response,
				unknownResponse,
			)
			if aborted {
				t.Fatal("handler-derived timeout aborted the live parent request")
			}
			if !logger.containsLevel("error") ||
				logger.containsLevel("debug") {
				t.Fatalf(
					"handler-derived timeout log levels = %+v",
					logger.entries,
				)
			}
		})
	}
}

func TestPublicEmailHandlersRequireMatchingParentAndServiceTermination(
	t *testing.T,
) {
	injectedDependencyError := errors.New("injected public email dependency failure")
	mismatches := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		serviceErr error
	}{
		{
			name: "canceled parent with dependency error",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			serviceErr: injectedDependencyError,
		},
		{
			name: "canceled parent with deadline service error",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			serviceErr: context.DeadlineExceeded,
		},
		{
			name: "deadline parent with canceled service error",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(
					context.Background(),
					time.Now().Add(-time.Second),
				)
			},
			serviceErr: context.Canceled,
		},
	}

	for _, endpoint := range publicEmailParentContextEndpoints() {
		endpoint := endpoint
		t.Run(endpoint.name, func(t *testing.T) {
			unknownResponse := executeUnknownPublicEmailParentContextRequest(
				t,
				endpoint,
			)
			for _, mismatch := range mismatches {
				mismatch := mismatch
				t.Run(mismatch.name, func(t *testing.T) {
					logger := &requestContextLogger{}
					handler := newPublicEmailParentContextHandler(
						&publicEmailParentContextUserRepository{
							err: mismatch.serviceErr,
						},
						logger,
					)
					parentContext, cancel := mismatch.newContext()
					defer cancel()

					response, aborted := executePublicEmailParentContextRequest(
						t,
						handler,
						endpoint,
						parentContext,
					)

					assertPublicEmailParentContextMatchesUnknown(
						t,
						response,
						unknownResponse,
					)
					if aborted {
						t.Fatal(
							"mismatched parent/service errors aborted request",
						)
					}
					if !logger.containsLevel("error") ||
						logger.containsLevel("debug") {
						t.Fatalf(
							"mismatched error log levels = %+v",
							logger.entries,
						)
					}
				})
			}
		})
	}
}

func newPublicEmailParentContextHandler(
	userRepository UserRepository,
	logger Logger,
) *AuthHandler {
	return NewAuthHandler(
		&AuthService{
			userRepo: userRepository,
			config: &AuthConfig{
				EmailVerificationExpire: time.Hour,
				PasswordResetExpire:     time.Hour,
			},
		},
		logger,
	)
}

func executeUnknownPublicEmailParentContextRequest(
	t *testing.T,
	endpoint publicEmailParentContextEndpoint,
) *httptest.ResponseRecorder {
	t.Helper()
	handler := newPublicEmailParentContextHandler(
		&publicEmailParentContextUserRepository{},
		&requestContextLogger{},
	)
	response, aborted := executePublicEmailParentContextRequest(
		t,
		handler,
		endpoint,
		context.Background(),
	)
	if aborted {
		t.Fatal("unknown-account request was aborted")
	}
	return response
}

func executePublicEmailParentContextRequest(
	t *testing.T,
	handler *AuthHandler,
	endpoint publicEmailParentContextEndpoint,
	parentContext context.Context,
) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	aborted := false
	router.POST(endpoint.path, func(ginContext *gin.Context) {
		endpoint.invoke(handler, NewGinHTTPContext(ginContext))
		aborted = ginContext.IsAborted()
	})
	request := httptest.NewRequest(
		http.MethodPost,
		endpoint.path,
		bytes.NewBufferString(`{"email":"public-context@example.test"}`),
	).WithContext(parentContext)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response, aborted
}

func assertPublicEmailParentContextMatchesUnknown(
	t *testing.T,
	response,
	unknown *httptest.ResponseRecorder,
) {
	t.Helper()
	if unknown.Code != http.StatusOK ||
		response.Code != unknown.Code ||
		response.Body.String() != unknown.Body.String() {
		t.Fatalf(
			"public response differs from unknown account: %d %q / %d %q",
			response.Code,
			response.Body.String(),
			unknown.Code,
			unknown.Body.String(),
		)
	}
}
