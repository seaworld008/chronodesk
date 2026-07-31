package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestA2APushDeliverySnapshotIsUUIDv7AppendOnlyAndLogSafe(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:a2a-push-snapshot-model?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&A2APushDeliverySnapshot{}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewA2APushDeliverySnapshot(
		ProjectScope{OrganizationID: 11, ProjectID: 22},
		"019fb4a6-0000-7000-8000-000000000001",
		"task-a2a-snapshot",
		"push-config-a2a-snapshot",
		time.Now().UTC(),
		"https://old.example.test/a2a?token=callback-secret",
		[]byte(`{"statusUpdate":{"taskId":"task-a2a-snapshot"}}`),
		"application/a2a+json",
		"1.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.TokenCiphertext = "ciphertext-token"
	snapshot.AuthenticationCiphertext = "ciphertext-authentication"
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(snapshot.ID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("snapshot id %q is not UUIDv7: %v", snapshot.ID, err)
	}
	serialized, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		snapshot.CallbackURL,
		string(snapshot.RequestBody),
		snapshot.TokenCiphertext,
		snapshot.AuthenticationCiphertext,
		"callback-secret",
	} {
		if strings.Contains(string(serialized), forbidden) {
			t.Fatalf(
				"snapshot JSON leaked protected delivery data: %s",
				serialized,
			)
		}
	}

	if err := db.Model(snapshot).Update(
		"callback_url",
		"https://changed.example.test/a2a",
	).Error; err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("snapshot update error = %v", err)
	}
	if err := db.Delete(snapshot).Error; err == nil ||
		!strings.Contains(err.Error(), "immutable") {
		t.Fatalf("snapshot delete error = %v", err)
	}
	var retained A2APushDeliverySnapshot
	if err := db.First(&retained, "id = ?", snapshot.ID).Error; err != nil {
		t.Fatal(err)
	}
	if retained.CallbackURL != snapshot.CallbackURL ||
		string(retained.RequestBody) != string(snapshot.RequestBody) {
		t.Fatalf("immutable snapshot changed: %+v", retained)
	}
}
