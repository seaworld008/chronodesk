package models

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgePersistentModelsCarryOrganizationAndProjectScope(
	t *testing.T,
) {
	models := []any{
		KnowledgeArticle{},
		KnowledgeArticleVersion{},
		KnowledgeArticleACL{},
		KnowledgeIngestionTask{},
		KnowledgeChunk{},
		KnowledgeCitation{},
		KnowledgeFeedback{},
		KnowledgeIndexState{},
		ProjectModelPolicy{},
	}
	for _, model := range models {
		modelType := reflect.TypeOf(model)
		if _, exists := modelType.FieldByName("OrganizationID"); !exists {
			t.Errorf("%s has no OrganizationID", modelType.Name())
		}
		if _, exists := modelType.FieldByName("ProjectID"); !exists {
			t.Errorf("%s has no ProjectID", modelType.Name())
		}
	}
}

func TestKnowledgeVersionStoresOnlyObjectReferenceForOriginalFile(
	t *testing.T,
) {
	versionType := reflect.TypeOf(KnowledgeArticleVersion{})
	forbidden := []string{
		"RawContent",
		"Content",
		"Body",
		"Data",
		"Bytes",
		"Blob",
		"URL",
		"StorageURL",
	}
	for _, name := range forbidden {
		if _, exists := versionType.FieldByName(name); exists {
			t.Errorf("KnowledgeArticleVersion must not persist %s", name)
		}
	}
	version := validKnowledgeVersion()
	encoded, err := json.Marshal(version)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"object_provider",
		"object_bucket",
		"object_key",
		"object_version_id",
		"content_hash",
	} {
		if _, exists := fields[required]; !exists {
			t.Errorf("serialized version is missing %q", required)
		}
	}
}

func TestKnowledgeVersionRequiresCleanScanBeforeParsingAndIsImmutable(
	t *testing.T,
) {
	version := validKnowledgeVersion()
	if version.CanParse() {
		t.Fatal("pending virus scan was allowed to parse")
	}
	now := time.Now().UTC()
	version.VirusScan = VirusScanClean
	version.ScannedAt = &now
	if !version.CanParse() {
		t.Fatal("clean draft was not allowed to parse")
	}

	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&KnowledgeArticleVersion{}); err != nil {
		t.Fatal(err)
	}
	version.Status = KnowledgeVersionPublished
	version.PublishedAt = &now
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(version.ID)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("knowledge id = %q, version = %v, err = %v", version.ID, parsed.Version(), err)
	}
	version.Title = "被篡改"
	if err := db.Save(&version).Error; !errors.Is(
		err,
		ErrPublishedKnowledgeVersionImmutable,
	) {
		t.Fatalf("published version update error = %v", err)
	}
}

func TestKnowledgeCitationRequiresVersionPageSnippetAndHash(
	t *testing.T,
) {
	page := 3
	citation := KnowledgeCitation{
		OrganizationID:  1,
		ProjectID:       2,
		SearchID:        uuid.Must(uuid.NewV7()).String(),
		ArticleID:       uuid.Must(uuid.NewV7()).String(),
		VersionID:       uuid.Must(uuid.NewV7()).String(),
		DocumentVersion: 4,
		ChunkID:         uuid.Must(uuid.NewV7()).String(),
		PageNumber:      &page,
		Snippet:         "恢复服务前先确认数据库健康状态。",
		ContentHash:     strings.Repeat("a", 64),
		Rank:            1,
		Score:           0.91,
		CreatedByType:   ActorTypeHuman,
		CreatedByID:     "7",
	}
	if err := citation.Validate(); err != nil {
		t.Fatalf("valid citation rejected: %v", err)
	}
	citation.DocumentVersion = 0
	if err := citation.Validate(); err == nil {
		t.Fatal("citation without document version was accepted")
	}
	citation.DocumentVersion = 4
	citation.ContentHash = "not-a-hash"
	if err := citation.Validate(); err == nil {
		t.Fatal("citation without content hash was accepted")
	}
}

func TestProjectModelPolicyValidatesEgressAllowlistBudgetAndLimits(
	t *testing.T,
) {
	policy := validKnowledgeModelPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}

	policy.ProviderAllowlist = datatypes.JSON(`["different-provider"]`)
	if err := policy.Validate(); err == nil {
		t.Fatal("non-allowlisted provider was accepted")
	}
	policy = validKnowledgeModelPolicy()
	policy.DataEgress = ModelDataEgressRedacted
	policy.RedactionRules = datatypes.JSON(`[]`)
	if err := policy.Validate(); err == nil {
		t.Fatal("redacted egress without rules was accepted")
	}
	policy = validKnowledgeModelPolicy()
	policy.MonthlyTokenBudget = -1
	if err := policy.Validate(); err == nil {
		t.Fatal("negative model budget was accepted")
	}
}

func validKnowledgeVersion() KnowledgeArticleVersion {
	return KnowledgeArticleVersion{
		OrganizationID:   1,
		ProjectID:        2,
		ArticleID:        uuid.Must(uuid.NewV7()).String(),
		Version:          1,
		Status:           KnowledgeVersionDraft,
		Title:            "数据库恢复手册",
		ObjectProvider:   "s3",
		ObjectBucket:     "knowledge",
		ObjectKey:        "projects/2/database-recovery.pdf",
		ObjectVersionID:  "object-version-1",
		OriginalFileName: "database-recovery.pdf",
		MimeType:         "application/pdf",
		SizeBytes:        1024,
		ContentHash:      strings.Repeat("a", 64),
		VirusScan:        VirusScanPending,
		CreatedByType:    ActorTypeHuman,
		CreatedByID:      "7",
	}
}

func validKnowledgeModelPolicy() ProjectModelPolicy {
	return ProjectModelPolicy{
		OrganizationID:          1,
		ProjectID:               2,
		PolicyKey:               "knowledge",
		IsActive:                true,
		ProviderKey:             "approved-provider",
		GenerateModel:           "generate-v1",
		EmbeddingModel:          "embed-v1",
		RerankModel:             "rerank-v1",
		DataEgress:              ModelDataEgressAllowed,
		RedactionRules:          datatypes.JSON(`[]`),
		ProviderAllowlist:       datatypes.JSON(`["approved-provider"]`),
		ModelAllowlist:          datatypes.JSON(`["generate-v1","embed-v1","rerank-v1"]`),
		MonthlyTokenBudget:      100000,
		MonthlyCostBudgetMicros: 500000,
		RequestsPerMinute:       60,
		TokensPerMinute:         20000,
		CreatedByType:           ActorTypeHuman,
		CreatedByID:             "7",
	}
}
