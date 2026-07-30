package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAgentNativeIdempotencyReplayKeepsPostgresTransactionUsable(t *testing.T) {
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set CHRONODESK_POSTGRES_INTEGRATION=1 for the isolated PostgreSQL idempotency test")
	}
	rawDSN := strings.TrimSpace(os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"))
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse integration DSN: %v", err)
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal("idempotency integration test requires a loopback PostgreSQL target")
		}
	}

	admin, err := gorm.Open(postgres.Open(rawDSN), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open integration PostgreSQL: %v", err)
	}
	schemaName := fmt.Sprintf("chronodesk_idempotency_%d", time.Now().UnixNano())
	quotedSchema := `"` + schemaName + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := admin.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error; cleanupErr != nil {
			t.Errorf("drop isolated schema: %v", cleanupErr)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schemaName)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open isolated PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close isolated PostgreSQL pool: %v", closeErr)
		}
	})
	if err := db.AutoMigrate(&models.IdempotencyRecord{}); err != nil {
		t.Fatalf("migrate isolated idempotency table: %v", err)
	}

	service := NewAgentNativeService(db)
	actor := models.ServicePrincipalActor("agent-rest-replay")
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:        models.ProjectScope{OrganizationID: 11, ProjectID: 22},
		Actor:        actor,
		Source:       SourceProtocolAgentREST,
		CredentialID: "credential-1",
	})
	if err != nil {
		t.Fatalf("build operation context: %v", err)
	}
	body := []byte(`{"title":"same request"}`)

	err = service.InTransaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		reservation, reserveErr := service.ReserveIdempotency(
			txCtx,
			actor,
			"ticket.create",
			"same-key",
			body,
			time.Hour,
		)
		if reserveErr != nil {
			return reserveErr
		}
		receipt := OperationReceipt{
			OperationID:     "operation-1",
			ResourceID:      "ticket-1",
			ResourceVersion: 1,
			EventID:         "event-1",
		}
		return service.CompleteIdempotencyTx(
			txCtx,
			tx,
			reservation.Record.ID,
			201,
			receipt,
			"ticket-1",
			"event-1",
		)
	})
	if err != nil {
		t.Fatalf("commit first request: %v", err)
	}

	var replayed *IdempotencyReservation
	err = service.InTransaction(ctx, func(txCtx context.Context, _ *gorm.DB) error {
		var replayErr error
		replayed, replayErr = service.ReserveIdempotency(
			txCtx,
			actor,
			"ticket.create",
			"same-key",
			body,
			time.Hour,
		)
		return replayErr
	})
	if err != nil {
		t.Fatalf("replay request in PostgreSQL transaction: %v", err)
	}
	if replayed == nil || !replayed.Replayed {
		t.Fatalf("expected completed replay, got %+v", replayed)
	}
	var receipt OperationReceipt
	if err := json.Unmarshal(replayed.Record.ResponseBody, &receipt); err != nil {
		t.Fatalf("decode replayed response: %v", err)
	}
	if receipt.ResourceID != "ticket-1" || receipt.EventID != "event-1" {
		t.Fatalf("unexpected replayed response: %+v", receipt)
	}
}
