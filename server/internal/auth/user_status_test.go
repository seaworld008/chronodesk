package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestValidateUserAccessState(t *testing.T) {
	now := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	tests := []struct {
		name    string
		user    *User
		wantErr error
	}{
		{
			name: "active account is allowed",
			user: &User{Status: StatusActive},
		},
		{
			name:    "inactive account is denied",
			user:    &User{Status: StatusInactive},
			wantErr: ErrAccountInactive,
		},
		{
			name:    "suspended account is denied",
			user:    &User{Status: StatusSuspended},
			wantErr: ErrAccountSuspended,
		},
		{
			name:    "deleted account is denied",
			user:    &User{Status: StatusDeleted},
			wantErr: ErrAccountDeleted,
		},
		{
			name:    "unknown persisted state fails closed",
			user:    &User{Status: UserStatus("unknown")},
			wantErr: ErrInvalidAccountState,
		},
		{
			name:    "future lock denies an active account",
			user:    &User{Status: StatusActive, LockedUntil: &future},
			wantErr: ErrAccountLocked,
		},
		{
			name: "expired lock automatically permits an active account",
			user: &User{Status: StatusActive, LockedUntil: &past},
		},
		{
			name:    "missing account fails closed",
			user:    nil,
			wantErr: ErrUserNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateUserAccessState(test.user, now)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validateUserAccessState() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestUserLockStateDependsOnlyOnLockedUntil(t *testing.T) {
	now := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)

	tests := []struct {
		name string
		user *User
		want bool
	}{
		{
			name: "active account with future deadline is locked",
			user: &User{Status: StatusActive, LockedUntil: &future},
			want: true,
		},
		{
			name: "suspended status does not erase a lock deadline",
			user: &User{Status: StatusSuspended, LockedUntil: &future},
			want: true,
		},
		{
			name: "historical locked string without deadline is not a lock",
			user: &User{Status: UserStatus("locked")},
		},
		{
			name: "expired deadline is unlocked",
			user: &User{Status: StatusActive, LockedUntil: &past},
		},
		{
			name: "missing deadline is unlocked",
			user: &User{Status: StatusDeleted},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.user.isLockedAt(now); got != test.want {
				t.Fatalf("isLockedAt() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestGormUserRepositoryPreservesDomainUserStatuses(t *testing.T) {
	db := openUserStatusTestDB(t)
	repository := NewGormUserRepository(db).(*GormUserRepository)

	statuses := []UserStatus{
		StatusActive,
		StatusInactive,
		StatusSuspended,
		StatusDeleted,
	}
	for index, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			user := &User{
				Username:     fmt.Sprintf("status-user-%d", index),
				Email:        fmt.Sprintf("status-user-%d@example.test", index),
				PasswordHash: "hash",
				PlatformRole: PlatformRoleMember,
				Status:       status,
			}
			if err := repository.Create(context.Background(), user); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			loaded, err := repository.GetByID(context.Background(), user.ID)
			if err != nil {
				t.Fatalf("GetByID() error = %v", err)
			}
			if loaded.Status != status {
				t.Fatalf("GetByID() status = %q, want %q", loaded.Status, status)
			}
		})
	}
}

func TestGormUserRepositoryRejectsUnknownUserStatus(t *testing.T) {
	db := openUserStatusTestDB(t)
	repository := NewGormUserRepository(db).(*GormUserRepository)

	authUser := &User{
		Username:     "invalid-status-auth",
		Email:        "invalid-status-auth@example.test",
		PasswordHash: "hash",
		PlatformRole: PlatformRoleMember,
		Status:       UserStatus("locked"),
	}
	if err := repository.Create(context.Background(), authUser); !errors.Is(err, ErrInvalidAccountState) {
		t.Fatalf("Create() error = %v, want %v", err, ErrInvalidAccountState)
	}

	persisted := &models.User{
		Username:     "invalid-status-persisted",
		Email:        "invalid-status-persisted@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatus("locked"),
	}
	if err := db.Create(persisted).Error; err != nil {
		t.Fatalf("seed unknown status: %v", err)
	}
	if _, err := repository.GetByID(context.Background(), persisted.ID); !errors.Is(err, ErrInvalidAccountState) {
		t.Fatalf("GetByID() error = %v, want %v", err, ErrInvalidAccountState)
	}
}

func TestResetFailedLoginClearsExpiredLockState(t *testing.T) {
	db := openUserStatusTestDB(t)
	repository := NewGormUserRepository(db).(*GormUserRepository)
	expired := time.Now().Add(-time.Minute)
	expiredUser := &models.User{
		Username:      "expired-lock",
		Email:         "expired-lock@example.test",
		PasswordHash:  "hash",
		PlatformRole:  models.PlatformRoleMember,
		Status:        models.UserStatusActive,
		LoginAttempts: 4,
		LockedUntil:   &expired,
	}
	if err := db.Create(expiredUser).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := repository.ResetFailedLogin(context.Background(), expiredUser.ID); err != nil {
		t.Fatalf("ResetFailedLogin() error = %v", err)
	}

	var stored models.User
	if err := db.First(&stored, expiredUser.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.LoginAttempts != 0 || stored.LockedUntil != nil {
		t.Fatalf(
			"login lock state = attempts %d, deadline %v; want zero and nil",
			stored.LoginAttempts,
			stored.LockedUntil,
		)
	}

	future := time.Now().Add(time.Minute)
	lockedUser := &models.User{
		Username:      "active-lock",
		Email:         "active-lock@example.test",
		PasswordHash:  "hash",
		PlatformRole:  models.PlatformRoleMember,
		Status:        models.UserStatusActive,
		LoginAttempts: 4,
		LockedUntil:   &future,
	}
	if err := db.Create(lockedUser).Error; err != nil {
		t.Fatalf("seed actively locked user: %v", err)
	}
	if err := repository.ResetFailedLogin(context.Background(), lockedUser.ID); err != nil {
		t.Fatalf("ResetFailedLogin() active lock error = %v", err)
	}
	stored = models.User{}
	if err := db.First(&stored, lockedUser.ID).Error; err != nil {
		t.Fatalf("reload actively locked user: %v", err)
	}
	if stored.LoginAttempts != 0 || stored.LockedUntil == nil {
		t.Fatalf(
			"active login lock state = attempts %d, deadline %v; want zero and retained deadline",
			stored.LoginAttempts,
			stored.LockedUntil,
		)
	}
}

func openUserStatusTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:auth-user-status-%d?mode=memory&cache=shared",
		time.Now().UnixNano(),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db
}
