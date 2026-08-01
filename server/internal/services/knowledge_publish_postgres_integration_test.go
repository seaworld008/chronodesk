package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type postgresKnowledgePublishEventAppender struct{}

func (postgresKnowledgePublishEventAppender) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	_ DomainEventInput,
	_ []OutboxTarget,
) (*models.DomainEvent, error) {
	return &models.DomainEvent{ID: uuid.Must(uuid.NewV7()).String()}, nil
}

func TestPostgresConcurrentKnowledgePublicationLeavesOneCanonicalVersion(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL knowledge publication evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse PostgreSQL integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"knowledge publication integration test requires a loopback PostgreSQL target",
			)
		}
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL knowledge administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	schemaName := fmt.Sprintf(
		"chronodesk_knowledge_publish_%d",
		time.Now().UnixNano(),
	)
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create knowledge publication schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec(
			"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
		).Error; cleanupErr != nil {
			t.Errorf("drop knowledge publication schema: %v", cleanupErr)
		}
	})
	scopedURL := *parsed
	query := scopedURL.Query()
	query.Set("search_path", schemaName)
	scopedURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(scopedURL.String()), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open schema-scoped knowledge database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(6)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.User{},
		&models.ProjectMembership{},
		&models.KnowledgeArticle{},
		&models.KnowledgeArticleVersion{},
		&models.KnowledgeArticleACL{},
		&models.KnowledgeIngestionTask{},
		&models.KnowledgeIndexState{},
	); err != nil {
		t.Fatalf("migrate knowledge publication fixture: %v", err)
	}
	organization := models.Organization{
		Slug:   "knowledge-publish",
		Name:   "Knowledge Publish",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "KNOWLEDGE",
		Name:           "Knowledge",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "KNOWLEDGE",
		Name:           "Knowledge",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	manager := models.User{
		Username:     "knowledge-publish-manager",
		Email:        "knowledge-publish-manager@example.test",
		PasswordHash: "test-only",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&manager).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    manager.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	article := models.KnowledgeArticle{
		OrganizationID: project.OrganizationID,
		ProjectID:      project.ID,
		Key:            "concurrent-publish",
		Title:          "Concurrent publish",
		Status:         models.KnowledgeArticleActive,
		Revision:       1,
		CreatedByType:  models.ActorTypeHuman,
		CreatedByID:    fmt.Sprint(manager.ID),
		UpdatedByType:  models.ActorTypeHuman,
		UpdatedByID:    fmt.Sprint(manager.ID),
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	versions := []models.KnowledgeArticleVersion{
		postgresKnowledgeDraftVersion(article, manager.ID, 1, now),
		postgresKnowledgeDraftVersion(article, manager.ID, 2, now),
	}
	if err := db.Create(&versions).Error; err != nil {
		t.Fatal(err)
	}
	for index := range versions {
		task := models.KnowledgeIngestionTask{
			OrganizationID: project.OrganizationID,
			ProjectID:      project.ID,
			ArticleID:      article.ID,
			VersionID:      versions[index].ID,
			Attempt:        1,
			Status:         models.KnowledgeIngestionCompleted,
			ParserKey:      "postgres-test",
			CompletedAt:    &now,
			CreatedByType:  models.ActorTypeHuman,
			CreatedByID:    fmt.Sprint(manager.ID),
		}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	projectService, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeService(
		db,
		KnowledgeServiceDependencies{
			ProjectAuthorization: projectService,
			Events:               postgresKnowledgePublishEventAppender{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(manager.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, len(versions))
	var wait sync.WaitGroup
	for index := range versions {
		versionID := versions[index].ID
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, publishErr := service.PublishVersion(
				operationContext,
				versionID,
			)
			results <- publishErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for publishErr := range results {
		if publishErr != nil {
			t.Fatalf("concurrent knowledge publication: %v", publishErr)
		}
	}
	var published []models.KnowledgeArticleVersion
	if err := db.Where(
		"article_id = ? AND status = ?",
		article.ID,
		models.KnowledgeVersionPublished,
	).Find(&published).Error; err != nil {
		t.Fatal(err)
	}
	if len(published) != 1 {
		t.Fatalf("published versions = %+v, want exactly one", published)
	}
	if err := db.First(&article, "id = ?", article.ID).Error; err != nil {
		t.Fatal(err)
	}
	if article.CurrentVersion == nil ||
		*article.CurrentVersion != published[0].ID {
		t.Fatalf(
			"article current version = %v, published = %s",
			article.CurrentVersion,
			published[0].ID,
		)
	}
}

func postgresKnowledgeDraftVersion(
	article models.KnowledgeArticle,
	managerID uint,
	version uint64,
	now time.Time,
) models.KnowledgeArticleVersion {
	return models.KnowledgeArticleVersion{
		OrganizationID:   article.OrganizationID,
		ProjectID:        article.ProjectID,
		ArticleID:        article.ID,
		Version:          version,
		Status:           models.KnowledgeVersionDraft,
		Title:            fmt.Sprintf("Draft %d", version),
		ObjectProvider:   "local",
		ObjectBucket:     "postgres-test",
		ObjectKey:        fmt.Sprintf("knowledge/%s/%d.md", article.ID, version),
		OriginalFileName: fmt.Sprintf("%d.md", version),
		MimeType:         "text/plain",
		SizeBytes:        1,
		ContentHash:      strings.Repeat(fmt.Sprint(version), 64),
		VirusScan:        models.VirusScanClean,
		ScannedAt:        &now,
		PageCount:        1,
		CreatedByType:    models.ActorTypeHuman,
		CreatedByID:      fmt.Sprint(managerID),
	}
}
