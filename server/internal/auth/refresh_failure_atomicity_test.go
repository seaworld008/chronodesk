package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

type refreshLookupFailureRepository struct {
	TokenRepository
	err error
}

func (repository *refreshLookupFailureRepository) GetRefreshTokenForRotation(
	context.Context,
	string,
) (*RefreshToken, error) {
	return nil, repository.err
}

type refreshUserFailureRepository struct {
	UserRepository
	err error
}

func (repository *refreshUserFailureRepository) GetByID(
	context.Context,
	uint,
) (*User, error) {
	return nil, repository.err
}

type refreshProfileFailureRepository struct {
	ProfileRepository
	err error
}

func (repository *refreshProfileFailureRepository) GetByUserID(
	context.Context,
	uint,
) (*UserProfile, error) {
	return nil, repository.err
}

func assertRefreshAuthorityRemainsActive(
	t *testing.T,
	repository *GormTokenRepository,
	refreshToken string,
) {
	t.Helper()
	if _, err := repository.GetRefreshToken(
		context.Background(),
		refreshToken,
	); err != nil {
		t.Fatalf("refresh authority is no longer active: %v", err)
	}
}

func TestRefreshLookupFailuresPreserveDependencyErrorsAndCurrentToken(
	t *testing.T,
) {
	for _, test := range []struct {
		name         string
		configure    func(*AuthService, error)
		sentinel     error
		wantSentinel bool
	}{
		{
			name: "refresh authority sentinel remains unauthorized",
			configure: func(service *AuthService, err error) {
				service.tokenRepo = &refreshLookupFailureRepository{
					TokenRepository: service.tokenRepo,
					err:             fmt.Errorf("wrapped authority lookup: %w", err),
				}
			},
			sentinel:     ErrInvalidToken,
			wantSentinel: true,
		},
		{
			name: "refresh authority storage failure remains unavailable",
			configure: func(service *AuthService, err error) {
				service.tokenRepo = &refreshLookupFailureRepository{
					TokenRepository: service.tokenRepo,
					err:             fmt.Errorf("wrapped authority lookup: %w", err),
				}
			},
			sentinel: errors.New("refresh authority storage unavailable"),
		},
		{
			name: "user sentinel remains unauthorized",
			configure: func(service *AuthService, err error) {
				service.userRepo = &refreshUserFailureRepository{
					UserRepository: service.userRepo,
					err:            fmt.Errorf("wrapped user lookup: %w", err),
				}
			},
			sentinel:     ErrUserNotFound,
			wantSentinel: true,
		},
		{
			name: "user storage failure remains unavailable",
			configure: func(service *AuthService, err error) {
				service.userRepo = &refreshUserFailureRepository{
					UserRepository: service.userRepo,
					err:            fmt.Errorf("wrapped user lookup: %w", err),
				}
			},
			sentinel: errors.New("user storage unavailable"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, service, repository, oldRefresh := seedRefreshService(t)
			test.configure(service, test.sentinel)

			_, err := service.RefreshToken(
				context.Background(),
				&RefreshTokenRequest{RefreshToken: oldRefresh},
				"127.0.0.1",
				"refresh storage failure test",
			)
			if err == nil {
				t.Fatal("refresh unexpectedly succeeded")
			}
			if test.wantSentinel {
				if !errors.Is(err, test.sentinel) {
					t.Fatalf("refresh error = %v, want %v", err, test.sentinel)
				}
			} else {
				if errors.Is(err, ErrInvalidToken) ||
					errors.Is(err, ErrUserNotFound) {
					t.Fatalf("storage failure was disguised as an authentication sentinel: %v", err)
				}
				if !errors.Is(err, test.sentinel) {
					t.Fatalf("refresh error = %v, want wrapped storage failure", err)
				}
				status, _, _ := refreshFailureHTTPResponse(err)
				if status != 503 {
					t.Fatalf("refresh HTTP status = %d, want 503", status)
				}
			}
			assertRefreshAuthorityRemainsActive(t, repository, oldRefresh)
		})
	}
}

func TestRefreshProfileFailureLeavesCurrentTokenActive(t *testing.T) {
	db, service, repository, oldRefresh := seedRefreshService(t)
	injected := errors.New("profile storage unavailable")
	service.profileRepo = &refreshProfileFailureRepository{
		ProfileRepository: service.profileRepo,
		err:               injected,
	}

	_, err := service.RefreshToken(
		context.Background(),
		&RefreshTokenRequest{RefreshToken: oldRefresh},
		"127.0.0.1",
		"refresh profile failure test",
	)
	if !errors.Is(err, injected) {
		t.Fatalf("refresh error = %v, want wrapped profile failure", err)
	}
	assertRefreshAuthorityRemainsActive(t, repository, oldRefresh)
	var tokenCount int64
	if err := db.Model(&RefreshToken{}).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count refresh authorities: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("refresh authority rows = %d, want 1", tokenCount)
	}
}

func TestRefreshRotationRollsBackWhenLoginHistoryUpdateFails(t *testing.T) {
	db, service, repository, oldRefresh := seedRefreshService(t)
	if err := db.Exec(`
		CREATE TRIGGER reject_refresh_session_audit
		BEFORE UPDATE ON login_histories
		BEGIN
			SELECT RAISE(FAIL, 'injected refresh session audit failure');
		END
	`).Error; err != nil {
		t.Fatalf("create login-history failure trigger: %v", err)
	}

	if _, err := service.RefreshToken(
		context.Background(),
		&RefreshTokenRequest{RefreshToken: oldRefresh},
		"127.0.0.2",
		"refresh history failure test",
	); err == nil {
		t.Fatal("refresh unexpectedly succeeded when login-history update failed")
	}
	assertRefreshAuthorityRemainsActive(t, repository, oldRefresh)

	var (
		tokenCount  int64
		activeCount int64
		history     models.LoginHistory
	)
	if err := db.Model(&RefreshToken{}).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count refresh authorities: %v", err)
	}
	if err := db.Model(&RefreshToken{}).
		Where("revoked = ?", false).
		Count(&activeCount).Error; err != nil {
		t.Fatalf("count active refresh authorities: %v", err)
	}
	if err := db.Where("session_id = ?", "atomic-refresh-session").
		First(&history).Error; err != nil {
		t.Fatalf("load login history: %v", err)
	}
	if tokenCount != 1 || activeCount != 1 {
		t.Fatalf(
			"refresh authority total/active = %d/%d, want 1/1",
			tokenCount,
			activeCount,
		)
	}
	if history.IPAddress == "127.0.0.2" ||
		history.UserAgent == "refresh history failure test" {
		t.Fatalf("failed rotation partially updated login history: %+v", history)
	}
}
