package agentplatform

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/seaworld008/chronodesk/server/internal/agentauth"
	"github.com/seaworld008/chronodesk/server/internal/httpcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAgentRESTAttachmentReplayUnderNonOwnerPostgresForceRLS(
	t *testing.T,
) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL attachment replay FORCE RLS evidence",
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
				"PostgreSQL attachment replay test requires a loopback target",
			)
		}
	}

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	schemaName := "chronodesk_attachment_replay_" + suffix
	roleName := "chronodesk_attachment_runtime_" + suffix
	rolePassword := "ChronoDeskAttachment" + suffix + "!"
	quotedSchema := quoteAttachmentReplayPostgresIdentifier(schemaName)
	quotedRole := quoteAttachmentReplayPostgresIdentifier(roleName)
	silentConfig := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	admin, err := gorm.Open(postgres.Open(rawDSN), silentConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL integration administrator: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var adminScopedSQL, runtimeSQL *sql.DB
	roleCreated := false
	schemaCreated := false
	t.Cleanup(func() {
		if runtimeSQL != nil {
			_ = runtimeSQL.Close()
		}
		if adminScopedSQL != nil {
			_ = adminScopedSQL.Close()
		}
		if schemaCreated {
			_ = admin.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		if roleCreated {
			_ = admin.Exec("DROP ROLE IF EXISTS " + quotedRole).Error
		}
		_ = adminSQL.Close()
	})
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL attachment replay schema: %v", err)
	}
	schemaCreated = true

	adminScopedURL := *parsed
	adminQuery := adminScopedURL.Query()
	adminQuery.Set("search_path", schemaName)
	adminScopedURL.RawQuery = adminQuery.Encode()
	adminScoped, err := gorm.Open(
		postgres.Open(adminScopedURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open schema-scoped PostgreSQL administrator: %v", err)
	}
	adminScopedDB, err := adminScoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	adminScopedSQL = adminScopedDB
	tableOnly := adminScoped.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.Project{},
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.ProjectPrincipalGrant{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.Ticket{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL attachment replay schema: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	project := models.Project{
		ID:             2201,
		PublicID:       "00000000-0000-7000-8000-000000002201",
		CreatedAt:      now,
		UpdatedAt:      now,
		OrganizationID: 1101,
		BusinessUnitID: 1,
		Key:            "ATTACH",
		Name:           "Attachment Replay",
		Status:         models.ProjectStatusActive,
	}
	principal := models.ServicePrincipal{
		ID:          "00000000-0000-7000-8000-000000003001",
		CreatedAt:   now,
		UpdatedAt:   now,
		Name:        "PostgreSQL Attachment Replay Agent",
		Status:      models.ServicePrincipalStatusActive,
		Scopes:      datatypes.JSON([]byte(`["attachments:write"]`)),
		PolicyEpoch: 1,
	}
	credential := models.AgentCredential{
		ID:                 "00000000-0000-7000-8000-000000003002",
		CreatedAt:          now,
		UpdatedAt:          now,
		ServicePrincipalID: principal.ID,
		Name:               "PostgreSQL replay credential",
		SecretHash:         strings.Repeat("a", 64),
		Status:             models.AgentCredentialStatusActive,
		ExpiresAt:          now.Add(time.Hour),
	}
	grant := models.ProjectPrincipalGrant{
		ID:                 3301,
		CreatedAt:          now,
		UpdatedAt:          now,
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON([]byte(`["attachments:write"]`)),
		IsActive:           true,
	}
	ticket := models.Ticket{
		ID:                   4401,
		PublicID:             "00000000-0000-7000-8000-000000004401",
		CreatedAt:            now,
		UpdatedAt:            now,
		OrganizationID:       project.OrganizationID,
		ProjectID:            project.ID,
		QueueID:              1,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000004402",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000004403",
		TicketNumber:         "ATTACH-1",
		Title:                "PostgreSQL attachment replay",
		Description:          "FORCE RLS attachment replay fixture",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelVerified,
		CreatedByActorType:   models.ActorTypeServicePrincipal,
		CreatedByActorID:     principal.ID,
	}
	lease := models.TicketLease{
		ID:              "00000000-0000-7000-8000-000000004404",
		CreatedAt:       now,
		UpdatedAt:       now,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		TicketID:        ticket.ID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   principal.ID,
		TicketVersion:   ticket.Version,
		ExpiresAt:       now.Add(time.Hour),
		LastHeartbeatAt: now,
	}
	for _, fixture := range []struct {
		name  string
		value any
	}{
		{name: "project", value: &project},
		{name: "principal", value: &principal},
		{name: "credential", value: &credential},
		{name: "grant", value: &grant},
		{name: "ticket", value: &ticket},
		{name: "lease", value: &lease},
	} {
		if err := adminScoped.Create(fixture.value).Error; err != nil {
			t.Fatalf(
				"seed PostgreSQL attachment replay %s: %v",
				fixture.name,
				err,
			)
		}
	}

	for _, tableName := range []string{
		"policy_decisions",
		"idempotency_records",
		"ticket_attachments",
		"ticket_histories",
		"ticket_leases",
		"tickets",
		"domain_events",
		"outbox_deliveries",
	} {
		quotedTable := quoteAttachmentReplayPostgresIdentifier(tableName)
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" ENABLE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := adminScoped.Exec(
			"ALTER TABLE " + quotedTable +
				" FORCE ROW LEVEL SECURITY",
		).Error; err != nil {
			t.Fatal(err)
		}
		predicate := `(organization_id = NULLIF(current_setting(` +
			`'chronodesk.organization_id', true), '')::bigint AND ` +
			`project_id = NULLIF(current_setting(` +
			`'chronodesk.project_id', true), '')::bigint)`
		if err := adminScoped.Exec(
			"CREATE POLICY chronodesk_project_scope ON " + quotedTable +
				" FOR ALL TO PUBLIC USING " + predicate +
				" WITH CHECK " + predicate,
		).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := admin.Exec(
		"CREATE ROLE " + quotedRole +
			" LOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS PASSWORD " +
			quoteAttachmentReplayPostgresLiteral(rolePassword),
	).Error; err != nil {
		t.Fatalf("create PostgreSQL attachment runtime role: %v", err)
	}
	roleCreated = true
	if err := adminScoped.Exec(
		"GRANT USAGE ON SCHEMA " + quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := adminScoped.Exec(
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA " +
			quotedSchema + " TO " + quotedRole,
	).Error; err != nil {
		t.Fatal(err)
	}

	runtimeURL := adminScopedURL
	runtimeURL.User = url.UserPassword(roleName, rolePassword)
	runtimeDB, err := gorm.Open(
		postgres.Open(runtimeURL.String()),
		silentConfig,
	)
	if err != nil {
		t.Fatalf("open non-owner PostgreSQL attachment runtime: %v", err)
	}
	runtimeDBSQL, err := runtimeDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL = runtimeDBSQL

	var runtimeUser string
	if err := runtimeDB.Raw("SELECT current_user").Scan(&runtimeUser).Error; err != nil {
		t.Fatal(err)
	}
	if runtimeUser != roleName {
		t.Fatalf("runtime role=%q, want %q", runtimeUser, roleName)
	}
	var unscopedAttachments int64
	if err := runtimeDB.Model(&models.TicketAttachment{}).
		Count(&unscopedAttachments).Error; err != nil {
		t.Fatal(err)
	}
	if unscopedAttachments != 0 {
		t.Fatalf(
			"FORCE RLS exposed %d unscoped attachments",
			unscopedAttachments,
		)
	}

	storage, err := services.NewLocalAttachmentStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	native := services.NewAgentNativeService(
		runtimeDB,
		services.AgentNativeOptions{
			AttachmentStorage:  storage,
			AttachmentStaging:  storage,
			AttachmentMaxBytes: 1 << 20,
		},
	)
	handler := NewAPIHandler(
		runtimeDB,
		native,
		nil,
		1<<20,
		nil,
	)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(agentauth.ContextPrincipalID, principal.ID)
		c.Set(agentauth.ContextCredentialID, credential.ID)
		c.Set(
			agentauth.ContextScopes,
			[]string{models.ScopeAttachmentsWrite},
		)
		c.Next()
	})
	router.POST(
		"/projects/:projectKey/tickets/:id/attachments",
		handler.bindExternalProjectContext(),
		handler.StoreAttachment,
	)

	requestAttachment := func() *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		partHeader := make(textproto.MIMEHeader)
		partHeader.Set(
			"Content-Disposition",
			`form-data; name="file"; filename="force-rls-canary.txt"`,
		)
		partHeader.Set("Content-Type", "text/plain")
		part, partErr := writer.CreatePart(partHeader)
		if partErr != nil {
			t.Fatal(partErr)
		}
		if _, partErr = part.Write([]byte("force-rls-canary-body")); partErr != nil {
			t.Fatal(partErr)
		}
		if partErr = writer.WriteField(
			"description",
			"PostgreSQL FORCE RLS canary",
		); partErr != nil {
			t.Fatal(partErr)
		}
		if partErr = writer.Close(); partErr != nil {
			t.Fatal(partErr)
		}

		request := httptest.NewRequest(
			http.MethodPost,
			fmt.Sprintf(
				"/projects/%s/tickets/%d/attachments",
				project.Key,
				ticket.ID,
			),
			&body,
		)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("If-Match", httpcontract.FormatETag(1))
		request.Header.Set("X-Ticket-Lease", lease.ID)
		request.Header.Set(
			"Idempotency-Key",
			"postgres-attachment-replay-key",
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	first := requestAttachment()
	if first.Code != http.StatusAccepted ||
		!strings.Contains(first.Body.String(), "force-rls-canary.txt") {
		t.Fatalf(
			"first attachment status=%d body=%s",
			first.Code,
			first.Body.String(),
		)
	}
	var record models.IdempotencyRecord
	if err := adminScoped.Where(
		"organization_id = ? AND project_id = ? AND actor_id = ? AND operation = ? AND key = ?",
		project.OrganizationID,
		project.ID,
		principal.ID,
		"ticket.attachment.create",
		"postgres-attachment-replay-key",
	).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.State != models.IdempotencyStateCompleted ||
		len(record.ResourceSnapshot) == 0 {
		t.Fatalf("first request did not persist completed snapshot: %+v", record)
	}
	if err := adminScoped.Model(&models.IdempotencyRecord{}).
		Where("id = ?", record.ID).
		Update("resource_snapshot", nil).Error; err != nil {
		t.Fatalf("convert completed record to legacy fallback fixture: %v", err)
	}

	var decisionsBefore int64
	if err := adminScoped.Model(&models.PolicyDecision{}).
		Where(
			"organization_id = ? AND project_id = ? AND action = ?",
			project.OrganizationID,
			project.ID,
			"ticket.attachment.create",
		).
		Count(&decisionsBefore).Error; err != nil {
		t.Fatal(err)
	}
	replay := requestAttachment()
	if replay.Code != http.StatusAccepted ||
		!strings.Contains(
			replay.Body.String(),
			"force-rls-canary.txt",
		) {
		t.Fatalf(
			"legacy replay status=%d body=%s",
			replay.Code,
			replay.Body.String(),
		)
	}
	var decisionsAfter int64
	if err := adminScoped.Model(&models.PolicyDecision{}).
		Where(
			"organization_id = ? AND project_id = ? AND action = ?",
			project.OrganizationID,
			project.ID,
			"ticket.attachment.create",
		).
		Count(&decisionsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if decisionsAfter != decisionsBefore+1 {
		t.Fatalf(
			"replay decisions %d -> %d, want exactly one persisted decision",
			decisionsBefore,
			decisionsAfter,
		)
	}

	var replayDecision models.PolicyDecision
	if err := adminScoped.Where(
		"organization_id = ? AND project_id = ? AND action = ?",
		project.OrganizationID,
		project.ID,
		"ticket.attachment.create",
	).Order("created_at DESC").Take(&replayDecision).Error; err != nil {
		t.Fatal(err)
	}
	var replayDecisionContext map[string]any
	if err := json.Unmarshal(
		replayDecision.Context,
		&replayDecisionContext,
	); err != nil {
		t.Fatalf("decode replay decision context: %v", err)
	}
	if !replayDecision.Allowed ||
		replayDecision.SourceProtocol !=
			string(services.SourceProtocolAgentREST) ||
		replayDecision.RequestDigest == "" ||
		replayDecisionContext["file_name"] !=
			"force-rls-canary.txt" ||
		replayDecisionContext["content_type"] != "text/plain" {
		t.Fatalf(
			"replay decision did not preserve exact attachment policy input: %+v",
			replayDecision,
		)
	}

	if err := adminScoped.Model(&models.ProjectPrincipalGrant{}).
		Where("id = ?", grant.ID).
		Update("is_active", false).Error; err != nil {
		t.Fatalf("revoke PostgreSQL attachment Grant: %v", err)
	}
	denied := requestAttachment()
	if denied.Code != http.StatusForbidden {
		t.Fatalf(
			"revoked replay status=%d body=%s, want 403",
			denied.Code,
			denied.Body.String(),
		)
	}
	if body := denied.Body.String(); strings.Contains(
		body,
		"force-rls-canary",
	) {
		t.Fatalf("revoked PostgreSQL replay leaked canary: %s", body)
	}
}

func quoteAttachmentReplayPostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteAttachmentReplayPostgresLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
