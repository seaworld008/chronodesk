package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var adminListTestCursorKey = []byte(
	"chronodesk-admin-list-test-cursor-key-20260731",
)

func newAdminListTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared&_busy_timeout=5000",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	tableOnly := db.Session(&gorm.Session{NewDB: true})
	tableOnly.Config.IgnoreRelationshipsWhenMigrating = true
	if err := tableOnly.AutoMigrate(
		&models.Project{},
		&models.User{},
		&models.SystemConfig{},
		&models.ServicePrincipal{},
		&models.ProjectPrincipalGrant{},
		&models.AgentPolicy{},
		&models.PolicyDecision{},
		&models.Ticket{},
		&models.TicketLease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.TicketAttachment{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func adminListTestProject(
	t *testing.T,
	db *gorm.DB,
	id uint,
	organizationID uint,
	key string,
) models.Project {
	t.Helper()
	project := models.Project{
		ID:             id,
		PublicID:       fmt.Sprintf("00000000-0000-7000-8000-%012d", id),
		OrganizationID: organizationID,
		BusinessUnitID: 1,
		Key:            models.ProjectKey(key),
		Name:           key,
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	return project
}

func adminListTestContext(
	t *testing.T,
	scope models.ProjectScope,
) context.Context {
	t.Helper()
	ctx, err := services.WithOperationContext(
		context.Background(),
		services.OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(1),
			Source: services.SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestAdminPrincipalPageUsesStableEqualTimeOrderingAndRealTotal(
	t *testing.T,
) {
	db := newAdminListTestDB(t)
	project := adminListTestProject(t, db, 101, 11, "LISTA")
	createdAt := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	for i := 1; i <= 150; i++ {
		id := fmt.Sprintf("00000000-0000-7000-8000-%012d", i)
		principal := models.ServicePrincipal{
			ID:          id,
			CreatedAt:   createdAt,
			UpdatedAt:   createdAt,
			Name:        fmt.Sprintf("Agent List %03d", i),
			Status:      models.ServicePrincipalStatusActive,
			Scopes:      datatypes.JSON([]byte(`["tickets:read"]`)),
			PolicyEpoch: 1,
		}
		grant := models.ProjectPrincipalGrant{
			ProjectID:          project.ID,
			ServicePrincipalID: id,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON([]byte(`["tickets:read"]`)),
			IsActive:           true,
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
		}
		if err := db.Create(&principal).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}

	service, err := NewAdminListService(db, adminListTestCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ListPrincipals(
		adminListTestContext(t, project.Scope()),
		project.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListPrincipals(
		adminListTestContext(t, project.Scope()),
		project.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 150 || first.TotalPages != 2 ||
		len(first.Items) != 100 || len(second.Items) != 50 {
		t.Fatalf("unexpected principal pages: first=%+v second=%+v", first, second)
	}
	seen := make(map[string]struct{}, 150)
	ordered := append(first.Items, second.Items...)
	for index, item := range ordered {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("duplicate principal %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8000-%012d",
			150-index,
		)
		if item.ID != wantID {
			t.Fatalf("principal[%d]=%q, want %q", index, item.ID, wantID)
		}
		if item.Grant.ProjectID != project.ID ||
			len(item.Grant.Scopes) != 1 ||
			item.Grant.Scopes[0] != models.ScopeTicketsRead {
			t.Fatalf("principal grant projection is incomplete: %+v", item.Grant)
		}
	}
	if len(seen) != 150 {
		t.Fatalf("unique principal count=%d, want 150", len(seen))
	}
}

func TestAdminOverviewUsesScopedServerAggregates(t *testing.T) {
	db := newAdminListTestDB(t)
	projectA := adminListTestProject(t, db, 141, 14, "OVERVIEWA")
	projectB := adminListTestProject(t, db, 142, 14, "OVERVIEWB")
	now := time.Date(2026, 7, 31, 8, 15, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)
	for i, fixture := range []struct {
		projectID uint
		status    models.ServicePrincipalStatus
		active    bool
		expiresAt *time.Time
	}{
		{
			projectID: projectA.ID,
			status:    models.ServicePrincipalStatusActive,
			active:    true,
		},
		{
			projectID: projectA.ID,
			status:    models.ServicePrincipalStatusInactive,
			active:    true,
		},
		{
			projectID: projectA.ID,
			status:    models.ServicePrincipalStatusActive,
			active:    true,
			expiresAt: &expiredAt,
		},
		{
			projectID: projectB.ID,
			status:    models.ServicePrincipalStatusActive,
			active:    true,
		},
	} {
		principalID := fmt.Sprintf(
			"00000000-0000-7000-8050-%012d",
			i+1,
		)
		if err := db.Create(&models.ServicePrincipal{
			ID:          principalID,
			CreatedAt:   now,
			UpdatedAt:   now,
			Name:        fmt.Sprintf("Overview Agent %d", i+1),
			Status:      fixture.status,
			Scopes:      datatypes.JSON([]byte(`["tickets:read"]`)),
			PolicyEpoch: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&models.ProjectPrincipalGrant{
			ProjectID:          fixture.projectID,
			ServicePrincipalID: principalID,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON([]byte(`["tickets:read"]`)),
			IsActive:           fixture.active,
			ExpiresAt:          fixture.expiresAt,
			CreatedAt:          now,
			UpdatedAt:          now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	service, err := NewAdminListService(db, adminListTestCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	metrics, err := service.Overview(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.PrincipalCount != 3 || metrics.ActivePrincipalCount != 1 {
		t.Fatalf("unexpected scoped principal metrics: %+v", metrics)
	}
}

func TestAdminOutboxProjectsExpiredTimestampsWithoutInternals(t *testing.T) {
	db := newAdminListTestDB(t)
	project := adminListTestProject(t, db, 145, 14, "OUTBOXSAFE")
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Minute)
	expiredAt := now
	event := models.DomainEvent{
		ID:              "00000000-0000-7000-8000-000000001451",
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test:admin-outbox-safe",
		Type:            "io.chronodesk.test.admin-outbox-safe.v1",
		Subject:         "outbox/safe",
		Time:            now.Add(-time.Hour),
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"safe":true}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "admin-outbox-safe",
		ResourceVersion: 1,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-7000-8000-000000001452",
		CreatedAt:       now.Add(-time.Hour),
		UpdatedAt:       now,
		OrganizationID:  project.OrganizationID,
		ProjectID:       project.ID,
		EventID:         event.ID,
		DestinationType: "webhook",
		DestinationID:   "snapshot:00000000-0000-7000-8000-000000001453",
		Status:          models.OutboxDeliveryExpired,
		Attempts:        2,
		MaxAttempts:     8,
		NextAttemptAt:   expiresAt,
		LockedBy:        "private-worker-generation",
		LastError:       "Authorization: Bearer private-credential",
		ExpiresAt:       &expiresAt,
		ExpiredAt:       &expiredAt,
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewAdminListService(db, adminListTestCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListOutbox(
		adminListTestContext(t, project.Scope()),
		project.Scope(),
		AdminPageQuery{Page: 1, PageSize: 25},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("outbox items=%d, want 1", len(page.Items))
	}
	item := page.Items[0]
	if item.Status != models.OutboxDeliveryExpired ||
		item.ExpiresAt == nil ||
		!item.ExpiresAt.Equal(expiresAt) ||
		item.ExpiredAt == nil ||
		!item.ExpiredAt.Equal(expiredAt) {
		t.Fatalf("expired projection is incomplete: %+v", item)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"destination_id",
		"snapshot:",
		"locked_by",
		"lock_token",
		"private-worker-generation",
		"private-credential",
		"credential",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf(
				"safe Outbox projection leaked %q: %s",
				forbidden,
				encoded,
			)
		}
	}
}

func TestAdminLeasePageUsesStableEqualTimeOrderingWithoutNPlusOne(
	t *testing.T,
) {
	db := newAdminListTestDB(t)
	project := adminListTestProject(t, db, 151, 15, "LEASEA")
	createdAt := time.Date(2026, 7, 31, 8, 30, 0, 0, time.UTC)
	expiresAt := createdAt.Add(time.Hour)
	tickets := make([]models.Ticket, 0, 150)
	leases := make([]models.TicketLease, 0, 150)
	for i := 1; i <= 150; i++ {
		tickets = append(tickets, models.Ticket{
			ID:                   uint(i),
			PublicID:             fmt.Sprintf("00000000-0000-7000-8100-%012d", i),
			CreatedAt:            createdAt,
			UpdatedAt:            createdAt,
			OrganizationID:       project.OrganizationID,
			ProjectID:            project.ID,
			QueueID:              1,
			RequestTypeVersionID: "00000000-0000-7000-8200-000000000001",
			WorkflowVersionID:    "00000000-0000-7000-8300-000000000001",
			TicketNumber:         fmt.Sprintf("LEASE-%03d", i),
			Title:                fmt.Sprintf("Lease fixture %03d", i),
			Description:          "Administrator lease list fixture",
			Type:                 models.TicketTypeRequest,
			Priority:             models.TicketPriorityNormal,
			Status:               models.TicketStatusOpen,
			Source:               models.TicketSourceAgent,
			Version:              1,
			TrustLevel:           models.TicketTrustLevelVerified,
			CreatedByActorType:   models.ActorTypeSystem,
			CreatedByActorID:     "admin-list-test",
		})
		leases = append(leases, models.TicketLease{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8400-%012d",
				i,
			),
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
			OrganizationID:  project.OrganizationID,
			ProjectID:       project.ID,
			TicketID:        uint(i),
			HolderActorType: models.ActorTypeSystem,
			HolderActorID:   "admin-list-test",
			TicketVersion:   1,
			ExpiresAt:       expiresAt,
			LastHeartbeatAt: createdAt,
		})
	}
	if err := db.CreateInBatches(&tickets, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&leases, 100).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewAdminListService(db, adminListTestCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return createdAt }
	var queryCount atomic.Int64
	callbackName := "admin-list-query-count-" +
		strings.ReplaceAll(t.Name(), "/", "-")
	if err := db.Callback().Query().
		Before("gorm:query").
		Register(callbackName, func(*gorm.DB) {
			queryCount.Add(1)
		}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
	})

	first, err := service.ListLeases(
		adminListTestContext(t, project.Scope()),
		project.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstQueryCount := queryCount.Load()
	if firstQueryCount > 4 {
		t.Fatalf(
			"100-row lease page executed %d queries, want at most 4 batched queries",
			firstQueryCount,
		)
	}
	queryCount.Store(0)
	second, err := service.ListLeases(
		adminListTestContext(t, project.Scope()),
		project.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondQueryCount := queryCount.Load(); secondQueryCount > 4 {
		t.Fatalf(
			"50-row lease page executed %d queries, want at most 4 batched queries",
			secondQueryCount,
		)
	}
	if first.Total != 150 || first.TotalPages != 2 ||
		len(first.Items) != 100 || len(second.Items) != 50 {
		t.Fatalf("unexpected lease pages: first=%+v second=%+v", first, second)
	}
	seen := make(map[string]struct{}, 150)
	for index, item := range append(first.Items, second.Items...) {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("duplicate lease %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8400-%012d",
			index+1,
		)
		if item.ID != wantID {
			t.Fatalf("lease[%d]=%q, want %q", index, item.ID, wantID)
		}
	}
	if len(seen) != 150 {
		t.Fatalf("unique lease count=%d, want 150", len(seen))
	}
}

func TestAdminEventCursorBindsScopeLimitKindAndRejectsTampering(
	t *testing.T,
) {
	db := newAdminListTestDB(t)
	projectA := adminListTestProject(t, db, 201, 21, "EVENTA")
	projectB := adminListTestProject(t, db, 202, 21, "EVENTB")
	createdAt := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	for i := 1; i <= 151; i++ {
		event := models.DomainEvent{
			ID:              fmt.Sprintf("00000000-0000-7000-8000-%012d", i),
			CreatedAt:       createdAt,
			OrganizationID:  projectA.OrganizationID,
			ProjectID:       projectA.ID,
			SpecVersion:     "1.0",
			Source:          "/chronodesk/test",
			Type:            "io.chronodesk.test.v1",
			Time:            createdAt,
			DataContentType: "application/json",
			Data:            datatypes.JSON([]byte(`{}`)),
			ActorType:       models.ActorTypeSystem,
			ActorID:         "admin-list-test",
			ResourceVersion: 1,
		}
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&models.DomainEvent{
		ID:              "00000000-0000-7000-9000-000000000001",
		CreatedAt:       createdAt,
		OrganizationID:  projectB.OrganizationID,
		ProjectID:       projectB.ID,
		SpecVersion:     "1.0",
		Source:          "/chronodesk/test",
		Type:            "io.chronodesk.foreign.v1",
		Time:            createdAt,
		DataContentType: "application/json",
		Data:            datatypes.JSON([]byte(`{}`)),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "foreign",
		ResourceVersion: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewAdminListService(db, adminListTestCursorKey)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("unexpected first cursor page: %+v", first)
	}
	second, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100, Cursor: first.NextCursor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 51 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("unexpected second cursor page: %+v", second)
	}
	seen := make(map[string]struct{}, 151)
	for _, item := range append(first.Items, second.Items...) {
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("duplicate event %q", item.ID)
		}
		if item.Type == "io.chronodesk.foreign.v1" {
			t.Fatal("cross-project event leaked into page")
		}
		seen[item.ID] = struct{}{}
	}
	if len(seen) != 151 {
		t.Fatalf("unique event count=%d, want 151", len(seen))
	}

	tampered := first.NextCursor[:len(first.NextCursor)-1] + "A"
	for name, test := range map[string]struct {
		scope models.ProjectScope
		query AdminCursorQuery
	}{
		"tampered": {
			scope: projectA.Scope(),
			query: AdminCursorQuery{Limit: 100, Cursor: tampered},
		},
		"cross-project": {
			scope: projectB.Scope(),
			query: AdminCursorQuery{Limit: 100, Cursor: first.NextCursor},
		},
		"changed-limit": {
			scope: projectA.Scope(),
			query: AdminCursorQuery{Limit: 25, Cursor: first.NextCursor},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ListDomainEvents(
				adminListTestContext(t, test.scope),
				test.scope,
				test.query,
			); !errors.Is(err, ErrInvalidAdminListCursor) {
				t.Fatalf("error=%v, want ErrInvalidAdminListCursor", err)
			}
		})
	}
	if _, err := service.ListPolicyDecisions(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100, Cursor: first.NextCursor},
	); !errors.Is(err, ErrInvalidAdminListCursor) {
		t.Fatalf("event cursor reused for decisions error=%v", err)
	}
}

func TestParseAdminListQueriesIsStrict(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     string
		kind    adminListQueryKind
		wantErr bool
	}{
		{name: "page defaults", raw: "", kind: adminPageListQuery},
		{name: "page maximum", raw: "page=1&page_size=100", kind: adminPageListQuery},
		{name: "page sort", raw: "sort_by=created_at&sort_order=desc", kind: adminPageListQuery},
		{name: "cursor defaults", raw: "", kind: adminCursorListQuery},
		{name: "cursor maximum", raw: "limit=100", kind: adminCursorListQuery},
		{name: "zero page", raw: "page=0", kind: adminPageListQuery, wantErr: true},
		{name: "negative page size", raw: "page_size=-1", kind: adminPageListQuery, wantErr: true},
		{name: "non integer", raw: "limit=twenty-five", kind: adminCursorListQuery, wantErr: true},
		{name: "zero limit", raw: "limit=0", kind: adminCursorListQuery, wantErr: true},
		{name: "limit 101", raw: "limit=101", kind: adminCursorListQuery, wantErr: true},
		{name: "duplicate", raw: "limit=25&limit=50", kind: adminCursorListQuery, wantErr: true},
		{name: "unknown", raw: "limit=25&sort=created_at", kind: adminCursorListQuery, wantErr: true},
		{name: "page cursor mix", raw: "page=1&cursor=opaque", kind: adminPageListQuery, wantErr: true},
		{name: "invalid sort order", raw: "sort_by=created_at&sort_order=sideways", kind: adminPageListQuery, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query, err := parseAdminListQuery(test.raw, test.kind)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseAdminListQuery(%q) unexpectedly succeeded", test.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAdminListQuery(%q): %v", test.raw, err)
			}
			if test.kind == adminPageListQuery &&
				(query.Page != 1 || query.PageSize != 25) &&
				test.raw == "" {
				t.Fatalf("page defaults=%+v", query)
			}
			if test.kind == adminCursorListQuery &&
				query.Limit != 25 &&
				test.raw == "" {
				t.Fatalf("cursor defaults=%+v", query)
			}
		})
	}
}

func TestParsePositiveAdminIntegerUsesNativeIntWidth(t *testing.T) {
	maximum := strconv.Itoa(math.MaxInt)
	got, err := parsePositiveAdminInteger(maximum, math.MaxInt)
	if err != nil {
		t.Fatalf("parsePositiveAdminInteger(%q) error = %v", maximum, err)
	}
	if got != math.MaxInt {
		t.Fatalf(
			"parsePositiveAdminInteger(%q) = %d, want %d",
			maximum,
			got,
			math.MaxInt,
		)
	}

	overflow := "9223372036854775808"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatInt(int64(math.MaxInt32)+1, 10)
	}
	if _, err := parsePositiveAdminInteger(overflow, math.MaxInt); !errors.Is(
		err,
		ErrInvalidAdminListQuery,
	) {
		t.Fatalf(
			"parsePositiveAdminInteger(%q) error = %v, want %v",
			overflow,
			err,
			ErrInvalidAdminListQuery,
		)
	}
}

func TestAdminOutboxDestinationProjectionKeepsAttachmentUploadUsable(t *testing.T) {
	if got := adminOutboxDestinationType(
		services.AttachmentUploadOutboxDestination,
	); got != "attachment_upload" {
		t.Fatalf("attachment upload destination type=%q", got)
	}
	if got := adminOutboxDestinationLabel(
		services.AttachmentUploadOutboxDestination,
	); got != "附件入库" {
		t.Fatalf("attachment upload destination label=%q", got)
	}
	if got := adminOutboxDestinationType("private-callback-token"); got != "other" {
		t.Fatalf("unknown destination type=%q, want other", got)
	}
}
