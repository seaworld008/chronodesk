package database

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresSLAOrderIndex = "idx_tickets_scope_sla_created_id"

func TestPostgresTicketPaginationIndexesSupportScopedQueries(t *testing.T) {
	dsn := os.Getenv("CHRONODESK_POSTGRES_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set CHRONODESK_POSTGRES_MIGRATION_TEST_DSN for the PostgreSQL pagination-index test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open PostgreSQL migration database: %v", err)
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		t.Fatalf("begin PostgreSQL fixture: %v", transaction.Error)
	}
	defer transaction.Rollback()

	schemaName := fmt.Sprintf("ticket_pagination_%d", time.Now().UnixNano())
	if err := transaction.Exec(`CREATE SCHEMA "` + schemaName + `"`).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	if err := transaction.Exec(`SET LOCAL search_path TO "` + schemaName + `"`).Error; err != nil {
		t.Fatalf("select isolated schema: %v", err)
	}
	if err := RunMigrations(transaction); err != nil {
		t.Fatalf("run PostgreSQL migrations: %v", err)
	}

	var project models.Project
	if err := transaction.
		Where("key = ?", DefaultProjectKey).
		First(&project).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	var queue models.Queue
	if err := transaction.
		Where("project_id = ? AND is_default = ?", project.ID, true).
		First(&queue).Error; err != nil {
		t.Fatalf("load default queue: %v", err)
	}
	otherProject := models.Project{
		OrganizationID: project.OrganizationID,
		BusinessUnitID: project.BusinessUnitID,
		Key:            models.ProjectKey("PAGINATION"),
		Name:           "Pagination isolation",
		Status:         models.ProjectStatusActive,
	}
	if err := transaction.Create(&otherProject).Error; err != nil {
		t.Fatalf("create isolation project: %v", err)
	}
	if err := transaction.Exec(`
		SELECT
			set_config('chronodesk.organization_id', ?, true),
			set_config('chronodesk.project_id', ?, true),
			set_config('chronodesk.project_ids', ?, true)
	`,
		strconv.FormatUint(uint64(project.OrganizationID), 10),
		strconv.FormatUint(uint64(project.ID), 10),
		fmt.Sprintf("%d,%d", project.ID, otherProject.ID),
	).Error; err != nil {
		t.Fatalf("set PostgreSQL project scope: %v", err)
	}
	otherQueue := models.Queue{
		ProjectID: otherProject.ID,
		Key:       DefaultQueueKey,
		Name:      "Default",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := transaction.Create(&otherQueue).Error; err != nil {
		t.Fatalf("create isolation queue: %v", err)
	}

	seedActiveSQL := `
		INSERT INTO tickets (
			public_id, organization_id, project_id, queue_id,
			request_type_version_id, workflow_version_id, ticket_number,
			title, description, type, priority, status, source,
			version, trust_level, sla_breached, created_at, updated_at
		)
		SELECT
			? || LPAD(series::text, 32, '0'), ?, ?, ?, '', '',
			? || LPAD(series::text, 3, '0'),
			'SLA pagination fixture', 'SLA pagination fixture',
			'request', 'normal',
			(ARRAY['open', 'in_progress', 'pending'])[((series - 1) % 3) + 1],
			'web', 1, 'untrusted', TRUE,
			TIMESTAMPTZ '2026-01-01 00:00:00+00' + series * INTERVAL '1 minute',
			TIMESTAMPTZ '2026-01-01 00:00:00+00' + series * INTERVAL '1 minute'
		FROM generate_series(1, ?) AS series
	`
	if err := transaction.Exec(
		seedActiveSQL,
		"p1a-", project.OrganizationID, project.ID, queue.ID, "P1-A-", 180,
	).Error; err != nil {
		t.Fatalf("seed primary-project active SLA tickets: %v", err)
	}
	if err := transaction.Exec(
		seedActiveSQL,
		"p2a-", project.OrganizationID, otherProject.ID, otherQueue.ID, "P2-A-", 60,
	).Error; err != nil {
		t.Fatalf("seed cross-project active SLA tickets: %v", err)
	}
	if err := transaction.Exec(`
		INSERT INTO tickets (
			public_id, organization_id, project_id, queue_id,
			request_type_version_id, workflow_version_id, ticket_number,
			title, description, type, priority, status, source,
			version, trust_level, sla_breached, created_at, updated_at
		)
		SELECT
			'p1r-' || LPAD(series::text, 32, '0'), ?, ?, ?, '', '',
			'P1-R-' || LPAD(series::text, 3, '0'),
			'Resolved SLA fixture', 'Resolved SLA fixture',
			'request', 'normal', 'resolved', 'web', 1, 'untrusted', TRUE,
			TIMESTAMPTZ '2026-02-01 00:00:00+00' + series * INTERVAL '1 minute',
			TIMESTAMPTZ '2026-02-01 00:00:00+00' + series * INTERVAL '1 minute'
		FROM generate_series(1, 30) AS series
	`, project.OrganizationID, project.ID, queue.ID).Error; err != nil {
		t.Fatalf("seed inactive-status SLA tickets: %v", err)
	}
	if err := transaction.Exec(`
		INSERT INTO tickets (
			public_id, organization_id, project_id, queue_id,
			request_type_version_id, workflow_version_id, ticket_number,
			title, description, type, priority, status, source,
			version, trust_level, sla_breached, created_at, updated_at
		)
		SELECT
			'p1u-' || LPAD(series::text, 32, '0'), ?, ?, ?, '', '',
			'P1-U-' || LPAD(series::text, 3, '0'),
			'Unbreached SLA fixture', 'Unbreached SLA fixture',
			'request', 'normal', 'open', 'web', 1, 'untrusted', FALSE,
			TIMESTAMPTZ '2026-03-01 00:00:00+00' + series * INTERVAL '1 minute',
			TIMESTAMPTZ '2026-03-01 00:00:00+00' + series * INTERVAL '1 minute'
		FROM generate_series(1, 30) AS series
	`, project.OrganizationID, project.ID, queue.ID).Error; err != nil {
		t.Fatalf("seed unbreached SLA tickets: %v", err)
	}
	if err := transaction.Exec("ANALYZE tickets").Error; err != nil {
		t.Fatalf("analyze ticket fixtures: %v", err)
	}

	var indexCount int64
	if err := transaction.Raw(
		`SELECT COUNT(*) FROM pg_indexes
		 WHERE schemaname = ? AND indexname = ?`,
		schemaName,
		postgresSLAOrderIndex,
	).Scan(&indexCount).Error; err != nil {
		t.Fatalf("inspect index %s: %v", postgresSLAOrderIndex, err)
	}
	if indexCount != 1 {
		t.Fatalf("PostgreSQL index %s count=%d", postgresSLAOrderIndex, indexCount)
	}

	activeStatuses := []models.TicketStatus{
		models.TicketStatusOpen,
		models.TicketStatusInProgress,
		models.TicketStatusPending,
	}
	var dryRunPage []*models.Ticket
	productionQuery := transaction.Session(&gorm.Session{DryRun: true}).
		Model(&models.Ticket{}).
		Where(
			"tickets.organization_id = ? AND tickets.project_id = ?",
			project.OrganizationID,
			project.ID,
		).
		Where(
			"tickets.sla_breached = ? AND tickets.status IN ?",
			true,
			activeStatuses,
		).
		Preload("CreatedBy").
		Preload("AssignedTo").
		Preload("Category").
		Order("tickets.created_at ASC, tickets.id ASC").
		Offset(25).
		Limit(25).
		Find(&dryRunPage)
	if productionQuery.Error != nil {
		t.Fatalf("build production-shape SLA query: %v", productionQuery.Error)
	}
	productionSQL := productionQuery.Statement.SQL.String()
	if len(productionQuery.Statement.Vars) != 6 {
		t.Fatalf(
			"production-shape SLA bind count=%d, want 6; sql=%s",
			len(productionQuery.Statement.Vars),
			productionSQL,
		)
	}
	if !strings.Contains(productionSQL, "sla_breached = $3") ||
		!strings.Contains(productionSQL, "status IN ($4,$5,$6)") {
		t.Fatalf("unexpected production-shape SLA SQL: %s", productionSQL)
	}

	if err := transaction.Exec("SET LOCAL plan_cache_mode = force_generic_plan").Error; err != nil {
		t.Fatalf("force PostgreSQL generic plan: %v", err)
	}
	for _, setting := range []string{
		"SET LOCAL enable_seqscan = off",
		"SET LOCAL enable_bitmapscan = off",
	} {
		if err := transaction.Exec(setting).Error; err != nil {
			t.Fatalf("configure ordered-index plan: %v", err)
		}
	}
	const preparedStatement = "chronodesk_ticket_sla_page"
	if err := transaction.Exec(
		"PREPARE " + preparedStatement +
			" (bigint, bigint, boolean, varchar, varchar, varchar) AS " +
			productionSQL,
	).Error; err != nil {
		t.Fatalf("prepare production-shape SLA query: %v", err)
	}
	executeSQL := fmt.Sprintf(
		"EXECUTE %s(%d, %d, TRUE, 'open', 'in_progress', 'pending')",
		preparedStatement,
		project.OrganizationID,
		project.ID,
	)
	var secondPage []models.Ticket
	if err := transaction.Raw(executeSQL).Scan(&secondPage).Error; err != nil {
		t.Fatalf("execute prepared generic-plan SLA page: %v", err)
	}
	if len(secondPage) != 25 {
		t.Fatalf("second SLA page length=%d, want 25", len(secondPage))
	}
	for index, ticket := range secondPage {
		wantNumber := fmt.Sprintf("P1-A-%03d", index+26)
		if ticket.TicketNumber != wantNumber {
			t.Fatalf("second page ticket %d=%q, want %q", index, ticket.TicketNumber, wantNumber)
		}
		if ticket.ProjectID != project.ID {
			t.Fatalf("second page leaked project %d into project %d", ticket.ProjectID, project.ID)
		}
		if ticket.Status != models.TicketStatusOpen &&
			ticket.Status != models.TicketStatusInProgress &&
			ticket.Status != models.TicketStatusPending {
			t.Fatalf("second page included inactive status %q", ticket.Status)
		}
	}
	var total int64
	if err := transaction.Raw(`
		SELECT COUNT(*)
		FROM tickets
		WHERE organization_id = ? AND project_id = ?
		  AND sla_breached = TRUE
		  AND status IN ('open', 'in_progress', 'pending')
	`, project.OrganizationID, project.ID).Scan(&total).Error; err != nil {
		t.Fatalf("count scoped SLA tickets: %v", err)
	}
	if total != 180 {
		t.Fatalf("scoped SLA total=%d, want 180", total)
	}

	var planRows []struct {
		Plan string `gorm:"column:QUERY PLAN"`
	}
	if err := transaction.Raw(
		"EXPLAIN (COSTS OFF) " + executeSQL,
	).Scan(&planRows).Error; err != nil {
		t.Fatalf("explain generic plan with %s: %v", postgresSLAOrderIndex, err)
	}
	plan := ""
	for _, row := range planRows {
		plan += row.Plan + "\n"
	}
	if !strings.Contains(plan, postgresSLAOrderIndex) {
		t.Fatalf("query plan does not use %s:\n%s", postgresSLAOrderIndex, plan)
	}
	if !strings.Contains(plan, "$1") || !strings.Contains(plan, "$3") {
		t.Fatalf("query plan is not a parameterized generic plan:\n%s", plan)
	}
	if strings.Contains(plan, "Sort") {
		t.Fatalf("SLA pagination plan has an extra Sort:\n%s", plan)
	}
}
