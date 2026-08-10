package services

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestMarkOutboxFailedNeverPersistsCallbackURLQueryOrCredential(t *testing.T) {
	db := openAgentNativeTestDB(t)
	service := NewAgentNativeService(db, AgentNativeOptions{})
	createOutboxResilienceEvent(t, service, 1)
	workerCtx := testProjectOperationContext(
		t,
		db,
		models.SystemActor(outboxSystemActorID),
	)
	claimed, err := service.ClaimPendingOutbox(
		workerCtx,
		"scrub-worker",
		1,
		time.Minute,
	)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim delivery: count=%d err=%v", len(claimed), err)
	}
	claim, err := OutboxClaimRefFromDelivery(claimed[0])
	if err != nil {
		t.Fatal(err)
	}
	const (
		queryToken = "query-token-must-not-persist"
		bearer     = "bearer-credential-must-not-persist"
	)
	deliveryErr := fmt.Errorf(
		`callback https://hooks.example.test/a2a?access_token=%s failed Authorization: Bearer %s`,
		queryToken,
		bearer,
	)
	if err := service.MarkOutboxFailed(
		workerCtx,
		claim,
		deliveryErr,
	); err != nil {
		t.Fatal(err)
	}
	var delivery models.OutboxDelivery
	if err := db.First(&delivery, "id = ?", claimed[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"https://hooks.example.test",
		queryToken,
		bearer,
		"access_token=",
	} {
		if strings.Contains(delivery.LastError, forbidden) {
			t.Fatalf("Outbox last_error leaked %q: %q", forbidden, delivery.LastError)
		}
	}
	if !strings.Contains(delivery.LastError, "[URL 已隐藏]") {
		t.Fatalf("scrubbed diagnostic lost safe classification: %q", delivery.LastError)
	}
}

func TestOutboxURLNetworkErrorsUseFixedSafeClassification(t *testing.T) {
	networkErr := fmt.Errorf("A2A push failed: %w", &url.Error{
		Op:  "Post",
		URL: "https://push.example.test/callback?token=must-not-return",
		Err: errors.New("dial tcp failed"),
	})
	if got := scrubOutboxFailure(networkErr); got != "出站投递失败" {
		t.Fatalf("scrubOutboxFailure()=%q", got)
	}
}

func TestOutboxRelativeCallbackQueryIsScrubbed(t *testing.T) {
	const token = "relative-query-token"
	got := ScrubOutboxFailureText("callback /push/a2a?token=" + token + " failed")
	if strings.Contains(got, token) || strings.Contains(got, "token=") {
		t.Fatalf("relative callback query leaked: %q", got)
	}
	if !strings.Contains(got, "/push/a2a?[查询参数已隐藏]") {
		t.Fatalf("relative callback diagnostic=%q", got)
	}
}

func TestOutboxCredentialScrubCoversQuotedJSONAndAuthorizationSchemes(
	t *testing.T,
) {
	const (
		jsonToken       = "quoted-json-token-must-not-return"
		jsonBasic       = "json-basic-credential-must-not-return"
		standaloneBasic = "standalone-basic-must-not-return"
		standaloneToken = "standalone-bearer-must-not-return"
	)
	got := ScrubOutboxFailureText(
		`provider rejected {"access_token":"` + jsonToken +
			`","Authorization":"Basic ` + jsonBasic +
			`"} Basic ` + standaloneBasic +
			` Bearer ` + standaloneToken,
	)
	for _, forbidden := range []string{
		jsonToken,
		jsonBasic,
		standaloneBasic,
		standaloneToken,
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("credential scrub leaked %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "[凭据已隐藏]") {
		t.Fatalf("credential scrub lost safe marker: %q", got)
	}
}

func TestOutboxErrorScrubIsRuneBounded(t *testing.T) {
	message := strings.Repeat("工", maxOutboxFailureRunes+10)
	scrubbed := scrubOutboxFailure(errors.New(message))
	if got := len([]rune(scrubbed)); got != maxOutboxFailureRunes {
		t.Fatalf("scrubbed runes=%d", got)
	}
}
