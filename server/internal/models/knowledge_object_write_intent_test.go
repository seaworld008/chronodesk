package models

import (
	"strings"
	"testing"
	"time"
)

func TestKnowledgeObjectWriteIntentGeneratesPrivateUUIDv7AndValidates(
	t *testing.T,
) {
	intent := KnowledgeObjectWriteIntent{
		OrganizationID: 1,
		ProjectID:      2,
		ArticleID:      "019fbd64-6d73-7c5a-96e2-4df5a67f0144",
		VersionID:      "019fbd64-6d73-7436-927d-f28a739fe979",
		ObjectProvider: "s3",
		ObjectStoreID:  "s3-primary",
		ObjectKey:      "knowledge/2/article/version.md",
		SizeBytes:      8,
		ContentHash:    strings.Repeat("a", 64),
		CreatedByType:  ActorTypeHuman,
		CreatedByID:    "42",
		NextAttemptAt:  time.Now().UTC().Add(time.Minute),
	}
	if err := intent.BeforeCreate(nil); err != nil {
		t.Fatal(err)
	}
	if intent.ID == "" ||
		intent.TableName() != "knowledge_object_write_intents" {
		t.Fatalf("knowledge object write intent = %+v", intent)
	}
}

func TestKnowledgeObjectWriteIntentRejectsInvalidRecoveryIdentity(
	t *testing.T,
) {
	base := KnowledgeObjectWriteIntent{
		ID:             "019fbd64-6d73-7c5a-96e2-4df5a67f0144",
		OrganizationID: 1,
		ProjectID:      2,
		ArticleID:      "019fbd64-6d73-7436-927d-f28a739fe979",
		VersionID:      "019fbd64-6d73-76c9-b79a-b06aac8481d7",
		ObjectProvider: "local",
		ObjectStoreID:  "local-default",
		ObjectKey:      "knowledge/2/article/version.md",
		SizeBytes:      8,
		ContentHash:    strings.Repeat("b", 64),
		CreatedByType:  ActorTypeSystem,
		CreatedByID:    "test",
		NextAttemptAt:  time.Now().UTC(),
	}
	for name, mutate := range map[string]func(*KnowledgeObjectWriteIntent){
		"store": func(intent *KnowledgeObjectWriteIntent) {
			intent.ObjectStoreID = "../bucket"
		},
		"key": func(intent *KnowledgeObjectWriteIntent) {
			intent.ObjectKey = "unsafe\x00key"
		},
		"hash": func(intent *KnowledgeObjectWriteIntent) {
			intent.ContentHash = "not-a-hash"
		},
		"actor": func(intent *KnowledgeObjectWriteIntent) {
			intent.CreatedByID = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid intent accepted: %+v", candidate)
			}
		})
	}
}
