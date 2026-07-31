package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type requestContextUserRepository struct {
	UserRepository
	user             *User
	err              error
	returnContextErr bool
}

func (r *requestContextUserRepository) GetByID(ctx context.Context, _ uint) (*User, error) {
	if r.returnContextErr {
		return nil, ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.user, nil
}

type requestContextTokenRepository struct {
	TokenRepository
	active           bool
	err              error
	returnContextErr bool
}

func (r *requestContextTokenRepository) IsSessionActive(
	ctx context.Context,
	_ uint,
	_ string,
) (bool, error) {
	if r.returnContextErr {
		return false, ctx.Err()
	}
	return r.active, r.err
}

type requestContextLogEntry struct {
	level   string
	message string
}

type requestContextLogger struct {
	entries []requestContextLogEntry
}

func (l *requestContextLogger) append(level, message string) {
	l.entries = append(l.entries, requestContextLogEntry{
		level:   level,
		message: message,
	})
}

func (l *requestContextLogger) Info(message string, _ ...interface{}) {
	l.append("info", message)
}

func (l *requestContextLogger) Error(message string, _ ...interface{}) {
	l.append("error", message)
}

func (l *requestContextLogger) Warn(message string, _ ...interface{}) {
	l.append("warn", message)
}

func (l *requestContextLogger) Debug(message string, _ ...interface{}) {
	l.append("debug", message)
}

func (l *requestContextLogger) containsLevel(level string) bool {
	for _, entry := range l.entries {
		if entry.level == level {
			return true
		}
	}
	return false
}

func requestContextAuthHandler(
	t *testing.T,
	userRepository UserRepository,
	tokenRepository TokenRepository,
	logger Logger,
) (*AuthHandler, string) {
	t.Helper()
	manager := mustTestJWTManager(t, time.Hour, time.Hour)
	accessToken, _, err := manager.GenerateTokenPair(
		42,
		PlatformRolePlatformAdmin,
		"request-context-session",
	)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}
	handler := NewAuthHandler(&AuthService{
		userRepo:   userRepository,
		tokenRepo:  tokenRepository,
		jwtManager: manager,
	}, logger)
	return handler, accessToken
}

func executeRequestContextAuth(
	t *testing.T,
	handler *AuthHandler,
	accessToken string,
	method string,
	requestContext context.Context,
) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	downstreamCalled := false
	router.Handle(method, "/protected", func(c *gin.Context) {
		handler.RequireAuth(NewGinHTTPContext(c))
		if c.IsAborted() {
			return
		}
		downstreamCalled = true
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	request := httptest.NewRequest(method, "/protected", nil).
		WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response, downstreamCalled
}

func TestRequireAuthDoesNotWriteJSONAfterReadRequestContextEnds(t *testing.T) {
	activeUser := &User{
		ID:           42,
		PlatformRole: PlatformRolePlatformAdmin,
		Status:       StatusActive,
	}
	tests := []struct {
		name            string
		requestContext  func() (context.Context, context.CancelFunc)
		userRepository  UserRepository
		tokenRepository TokenRepository
		wantStatus      int
	}{
		{
			name: "principal lookup observes client cancellation",
			requestContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			userRepository: &requestContextUserRepository{
				returnContextErr: true,
			},
			tokenRepository: &requestContextTokenRepository{active: true},
			wantStatus:      statusClientClosedRequest,
		},
		{
			name: "session lookup observes request deadline",
			requestContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(
					context.Background(),
					time.Now().Add(-time.Second),
				)
			},
			userRepository: &requestContextUserRepository{user: activeUser},
			tokenRepository: &requestContextTokenRepository{
				returnContextErr: true,
			},
			wantStatus: http.StatusRequestTimeout,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := &requestContextLogger{}
			handler, accessToken := requestContextAuthHandler(
				t,
				test.userRepository,
				test.tokenRepository,
				logger,
			)
			requestContext, cancel := test.requestContext()
			defer cancel()

			response, downstreamCalled := executeRequestContextAuth(
				t,
				handler,
				accessToken,
				http.MethodGet,
				requestContext,
			)
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%q",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			if response.Body.Len() != 0 {
				t.Fatalf("canceled request wrote JSON body: %q", response.Body.String())
			}
			if downstreamCalled {
				t.Fatal("canceled request reached downstream handler")
			}
			if !logger.containsLevel("debug") || logger.containsLevel("error") {
				t.Fatalf("cancellation log levels = %+v", logger.entries)
			}
		})
	}
}

func TestRequireAuthKeepsRepositoryFailuresFailClosed(t *testing.T) {
	activeUser := &User{
		ID:           42,
		PlatformRole: PlatformRolePlatformAdmin,
		Status:       StatusActive,
	}
	tests := []struct {
		name            string
		userRepository  UserRepository
		tokenRepository TokenRepository
	}{
		{
			name: "principal repository failure",
			userRepository: &requestContextUserRepository{
				err: errors.New("principal database unavailable"),
			},
			tokenRepository: &requestContextTokenRepository{active: true},
		},
		{
			name:           "session repository failure",
			userRepository: &requestContextUserRepository{user: activeUser},
			tokenRepository: &requestContextTokenRepository{
				err: errors.New("session database unavailable"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := &requestContextLogger{}
			handler, accessToken := requestContextAuthHandler(
				t,
				test.userRepository,
				test.tokenRepository,
				logger,
			)
			response, downstreamCalled := executeRequestContextAuth(
				t,
				handler,
				accessToken,
				http.MethodGet,
				context.Background(),
			)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf(
					"status = %d, want 503; body=%q",
					response.Code,
					response.Body.String(),
				)
			}
			var problem ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if problem.Error != "authentication_unavailable" {
				t.Fatalf("error = %q, want authentication_unavailable", problem.Error)
			}
			if downstreamCalled {
				t.Fatal("repository failure reached downstream handler")
			}
			if !logger.containsLevel("error") {
				t.Fatalf("repository failure log levels = %+v", logger.entries)
			}
		})
	}
}

func TestRequireAuthDoesNotSuppressCanceledWriteRequest(t *testing.T) {
	logger := &requestContextLogger{}
	handler, accessToken := requestContextAuthHandler(
		t,
		&requestContextUserRepository{returnContextErr: true},
		&requestContextTokenRepository{active: true},
		logger,
	)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()

	response, downstreamCalled := executeRequestContextAuth(
		t,
		handler,
		accessToken,
		http.MethodPost,
		requestContext,
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%q", response.Code, response.Body.String())
	}
	if response.Body.Len() == 0 {
		t.Fatal("write request repository failure did not return fail-closed JSON")
	}
	if downstreamCalled {
		t.Fatal("failed write request reached downstream handler")
	}
	if !logger.containsLevel("error") {
		t.Fatalf("write request log levels = %+v", logger.entries)
	}
}

func TestCanceledAuthRequestDoesNotOverwriteStartedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil).
		WithContext(requestContext)
	response := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(response)
	ginContext.Request = request
	ginContext.JSON(http.StatusInternalServerError, gin.H{"error": "original"})
	originalBody := response.Body.String()

	handler := NewAuthHandler(nil, &requestContextLogger{})
	if !handler.abortTerminatedReadRequest(
		NewGinHTTPContext(ginContext),
		context.Canceled,
	) {
		t.Fatal("canceled request was not recognized")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want original 500", response.Code)
	}
	if response.Body.String() != originalBody {
		t.Fatalf(
			"started response changed from %q to %q",
			originalBody,
			response.Body.String(),
		)
	}
}
