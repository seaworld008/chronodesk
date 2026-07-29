package a2a

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/security"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGormPushCredentialsEncryptedAcrossRestart(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:a2a-secrets?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	key := bytes.Repeat([]byte{0x7a}, 32)
	ring, err := security.NewKeyring("a2a-test", map[string][]byte{"a2a-test": key})
	if err != nil {
		t.Fatal(err)
	}
	store := NewGormStoreWithProtector(db, ring)
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{
		ID: "task-secret", ContextID: "context-secret",
		Status:        TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		StatusHistory: []TaskStatus{{State: TaskStateSubmitted, Timestamp: now}},
		CreatedAt:     now, LastModified: now, Version: 1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	want := PushNotificationConfig{
		ID: "push-secret", TaskID: task.ID, URL: "https://push.example.test",
		Token: "notification-token",
		Authentication: &AuthenticationInfo{
			Scheme: "Bearer", Credentials: "authorization-credential",
		},
		CreatedAt: now,
	}
	if err := store.CreatePushConfig(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	var stored models.AgentPushNotificationConfig
	if err := db.First(&stored, "id = ?", want.ID).Error; err != nil {
		t.Fatal(err)
	}
	databaseValue := stored.Token + "\n" + string(stored.Authentication)
	if strings.Contains(databaseValue, want.Token) ||
		strings.Contains(databaseValue, want.Authentication.Credentials) ||
		!security.IsEnvelope(stored.Token) {
		t.Fatalf("push credentials were not encrypted at rest: %s", databaseValue)
	}

	restartedRing, err := security.NewKeyring("a2a-test", map[string][]byte{
		"a2a-test": append([]byte(nil), key...),
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewGormStoreWithProtector(db, restartedRing)
	got, err := restarted.GetPushConfig(context.Background(), task.ID, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != want.Token || got.Authentication == nil ||
		got.Authentication.Credentials != want.Authentication.Credentials {
		t.Fatalf("reloaded push config=%+v", got)
	}

	wrong, err := security.NewKeyring("a2a-test", map[string][]byte{
		"a2a-test": bytes.Repeat([]byte{0x7b}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewGormStoreWithProtector(db, wrong).
		GetPushConfig(context.Background(), task.ID, want.ID); !errors.Is(err, security.ErrAuthentication) {
		t.Fatalf("wrong key error=%v", err)
	}
}

func TestGormPushStoreRejectsLegacyPlaintext(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:a2a-legacy-secret?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := NewGormStoreWithProtector(db, nil)
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{
		ID: "task-legacy-secret", ContextID: "context-legacy-secret",
		Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: now},
		CreatedAt: now, LastModified: now, Version: 1,
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	row := models.AgentPushNotificationConfig{
		ID: "push-legacy-secret", TaskID: task.ID,
		URL: "https://push.example.test", Token: "plaintext-token",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPushConfig(context.Background(), task.ID, row.ID); !errors.Is(err, security.ErrKeyringUnavailable) &&
		!errors.Is(err, security.ErrPlaintextSecret) {
		t.Fatalf("legacy plaintext error=%v", err)
	}
}
