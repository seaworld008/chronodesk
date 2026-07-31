package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

	for _, test := range []struct {
		index string
		query string
	}{
		{
			index: "idx_tickets_scope_due_id",
			query: `SELECT id FROM tickets
				WHERE organization_id = 1 AND project_id = 1
				  AND due_date < NOW()
				ORDER BY due_date ASC, id ASC LIMIT 25`,
		},
		{
			index: "idx_tickets_scope_sla_status_created_id",
			query: `SELECT id FROM tickets
				WHERE organization_id = 1 AND project_id = 1
				  AND sla_breached = TRUE AND status IN ('open', 'in_progress')
				ORDER BY created_at ASC, id ASC LIMIT 25`,
		},
	} {
		var count int64
		if err := transaction.Raw(
			`SELECT COUNT(*) FROM pg_indexes
			 WHERE schemaname = ? AND indexname = ?`,
			schemaName,
			test.index,
		).Scan(&count).Error; err != nil {
			t.Fatalf("inspect index %s: %v", test.index, err)
		}
		if count != 1 {
			t.Fatalf("PostgreSQL index %s count=%d", test.index, count)
		}
		if err := transaction.Exec("SET LOCAL enable_seqscan = off").Error; err != nil {
			t.Fatal(err)
		}
		var planRows []struct {
			Plan string `gorm:"column:QUERY PLAN"`
		}
		if err := transaction.Raw("EXPLAIN " + test.query).Scan(&planRows).Error; err != nil {
			t.Fatalf("explain %s: %v", test.index, err)
		}
		plan := ""
		for _, row := range planRows {
			plan += row.Plan + "\n"
		}
		if !strings.Contains(plan, test.index) {
			t.Fatalf("query plan does not use %s:\n%s", test.index, plan)
		}
	}
}
