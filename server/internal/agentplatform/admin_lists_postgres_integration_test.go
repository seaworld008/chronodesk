package agentplatform

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAdminListsPostgresStablePagesAndBoundCursors(t *testing.T) {
	db := openAdminListsPostgresIntegrationDB(t)
	createdAt := time.Date(2026, 7, 31, 10, 0, 0, 123000, time.UTC)
	projectA := models.Project{
		ID:             5101,
		PublicID:       "00000000-0000-7000-8500-000000005101",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: 510,
		BusinessUnitID: 1,
		Key:            "PGLISTA",
		Name:           "PostgreSQL Agent List A",
		Status:         models.ProjectStatusActive,
	}
	projectB := models.Project{
		ID:             5102,
		PublicID:       "00000000-0000-7000-8500-000000005102",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: projectA.OrganizationID,
		BusinessUnitID: 1,
		Key:            "PGLISTB",
		Name:           "PostgreSQL Agent List B",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&[]models.Project{projectA, projectB}).Error; err != nil {
		t.Fatalf("seed PostgreSQL list projects: %v", err)
	}

	principals := make([]models.ServicePrincipal, 0, 150)
	grants := make([]models.ProjectPrincipalGrant, 0, 150)
	for i := 1; i <= 150; i++ {
		principalID := fmt.Sprintf(
			"00000000-0000-7000-8600-%012d",
			i,
		)
		principals = append(principals, models.ServicePrincipal{
			ID:          principalID,
			CreatedAt:   createdAt,
			UpdatedAt:   createdAt,
			Name:        fmt.Sprintf("PostgreSQL Agent List %03d", i),
			Status:      models.ServicePrincipalStatusActive,
			Scopes:      datatypes.JSON([]byte(`["tickets:read"]`)),
			PolicyEpoch: 1,
		})
		grants = append(grants, models.ProjectPrincipalGrant{
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
			ProjectID:          projectA.ID,
			ServicePrincipalID: principalID,
			Role:               models.ProjectRoleAgent,
			Scopes:             datatypes.JSON([]byte(`["tickets:read"]`)),
			IsActive:           true,
		})
	}
	if err := db.CreateInBatches(&principals, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL principals: %v", err)
	}
	if err := db.CreateInBatches(&grants, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL principal grants: %v", err)
	}

	events := make([]models.DomainEvent, 0, 152)
	for i := 1; i <= 151; i++ {
		events = append(events, models.DomainEvent{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8700-%012d",
				i,
			),
			CreatedAt:       createdAt,
			OrganizationID:  projectA.OrganizationID,
			ProjectID:       projectA.ID,
			SpecVersion:     "1.0",
			Source:          "/chronodesk/postgres/admin-list-test",
			Type:            "io.chronodesk.test.admin-list.v1",
			Time:            createdAt,
			DataContentType: "application/json",
			Data:            datatypes.JSON([]byte(`{}`)),
			ActorType:       models.ActorTypeSystem,
			ActorID:         "admin-list-postgres-test",
			ResourceVersion: 1,
		})
	}
	events = append(events, models.DomainEvent{
		ID:              "00000000-0000-7000-8800-000000000001",
		CreatedAt:       createdAt,
		OrganizationID:  projectB.OrganizationID,
		ProjectID:       projectB.ID,
		SpecVersion:     "1.0",
		Source:          "/chronodesk/postgres/admin-list-test",
		Type:            "io.chronodesk.test.admin-list.foreign.v1",
		Time:            createdAt,
		DataContentType: "application/json",
		Data:            datatypes.JSON([]byte(`{}`)),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "admin-list-postgres-test",
		ResourceVersion: 1,
	})
	if err := db.CreateInBatches(&events, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL events: %v", err)
	}

	foreignPrincipal := models.ServicePrincipal{
		ID:          "00000000-0000-7000-8601-000000000001",
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
		Name:        "PostgreSQL Foreign Agent",
		Status:      models.ServicePrincipalStatusActive,
		Scopes:      datatypes.JSON([]byte(`["tickets:read"]`)),
		PolicyEpoch: 1,
	}
	if err := db.Create(&foreignPrincipal).Error; err != nil {
		t.Fatalf("seed PostgreSQL foreign principal: %v", err)
	}
	if err := db.Create(&models.ProjectPrincipalGrant{
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
		ProjectID:          projectB.ID,
		ServicePrincipalID: foreignPrincipal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON([]byte(`["tickets:read"]`)),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatalf("seed PostgreSQL foreign principal grant: %v", err)
	}

	policies := make([]models.AgentPolicy, 0, 151)
	for i := 1; i <= 150; i++ {
		policies = append(policies, models.AgentPolicy{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8900-%012d",
				i,
			),
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
			ServicePrincipalID: principals[0].ID,
			Name:               fmt.Sprintf("PostgreSQL Policy %03d", i),
			Effect:             models.AgentPolicyEffectAllow,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			Priority:           100,
			IsActive:           true,
		})
	}
	policies = append(policies, models.AgentPolicy{
		ID:                 "00000000-0000-7000-8901-000000000001",
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
		ServicePrincipalID: foreignPrincipal.ID,
		Name:               "PostgreSQL Foreign Policy",
		Effect:             models.AgentPolicyEffectDeny,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		Priority:           100,
		IsActive:           true,
	})
	if err := db.CreateInBatches(&policies, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL policies: %v", err)
	}

	tickets := make([]models.Ticket, 0, 151)
	leases := make([]models.TicketLease, 0, 151)
	attachments := make([]models.TicketAttachment, 0, 151)
	expiresAt := createdAt.Add(time.Hour)
	for i := 1; i <= 150; i++ {
		ticketID := uint(6000 + i)
		tickets = append(tickets, models.Ticket{
			ID:                   ticketID,
			PublicID:             fmt.Sprintf("00000000-0000-7000-8a00-%012d", i),
			CreatedAt:            createdAt,
			UpdatedAt:            createdAt,
			OrganizationID:       projectA.OrganizationID,
			ProjectID:            projectA.ID,
			QueueID:              1,
			RequestTypeVersionID: "00000000-0000-7000-8a10-000000000001",
			WorkflowVersionID:    "00000000-0000-7000-8a20-000000000001",
			TicketNumber:         fmt.Sprintf("PGLIST-%03d", i),
			Title:                fmt.Sprintf("PostgreSQL list ticket %03d", i),
			Description:          "PostgreSQL administrator list evidence",
			Type:                 models.TicketTypeRequest,
			Priority:             models.TicketPriorityNormal,
			Status:               models.TicketStatusOpen,
			Source:               models.TicketSourceAgent,
			Version:              1,
			TrustLevel:           models.TicketTrustLevelVerified,
			CreatedByActorType:   models.ActorTypeSystem,
			CreatedByActorID:     "admin-list-postgres-test",
		})
		leases = append(leases, models.TicketLease{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8b00-%012d",
				i,
			),
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
			OrganizationID:  projectA.OrganizationID,
			ProjectID:       projectA.ID,
			TicketID:        ticketID,
			HolderActorType: models.ActorTypeServicePrincipal,
			HolderActorID:   principals[0].ID,
			TicketVersion:   1,
			ExpiresAt:       expiresAt,
			LastHeartbeatAt: createdAt,
		})
		attachments = append(attachments, models.TicketAttachment{
			ID:             uint(i),
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
			OrganizationID: projectA.OrganizationID,
			ProjectID:      projectA.ID,
			TicketID:       ticketID,
			ActorType:      models.ActorTypeSystem,
			ActorID:        "admin-list-postgres-test",
			FileName:       fmt.Sprintf("postgres-list-%03d.txt", i),
			OriginalName:   fmt.Sprintf("postgres-list-%03d.txt", i),
			FileSize:       int64(i),
			MimeType:       "text/plain",
			StoragePath:    fmt.Sprintf("postgres-list/%03d.txt", i),
			VirusScan:      models.VirusScanPending,
		})
	}
	foreignTicketID := uint(6999)
	tickets = append(tickets, models.Ticket{
		ID:                   foreignTicketID,
		PublicID:             "00000000-0000-7000-8a01-000000000001",
		CreatedAt:            createdAt,
		UpdatedAt:            createdAt,
		OrganizationID:       projectB.OrganizationID,
		ProjectID:            projectB.ID,
		QueueID:              1,
		RequestTypeVersionID: "00000000-0000-7000-8a10-000000000001",
		WorkflowVersionID:    "00000000-0000-7000-8a20-000000000001",
		TicketNumber:         "PGLIST-FOREIGN",
		Title:                "PostgreSQL foreign list ticket",
		Description:          "Must remain project scoped",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAgent,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelVerified,
		CreatedByActorType:   models.ActorTypeSystem,
		CreatedByActorID:     "admin-list-postgres-test",
	})
	leases = append(leases, models.TicketLease{
		ID:              "00000000-0000-7000-8b01-000000000001",
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
		OrganizationID:  projectB.OrganizationID,
		ProjectID:       projectB.ID,
		TicketID:        foreignTicketID,
		HolderActorType: models.ActorTypeServicePrincipal,
		HolderActorID:   foreignPrincipal.ID,
		TicketVersion:   1,
		ExpiresAt:       expiresAt,
		LastHeartbeatAt: createdAt,
	})
	attachments = append(attachments, models.TicketAttachment{
		ID:             10001,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
		OrganizationID: projectB.OrganizationID,
		ProjectID:      projectB.ID,
		TicketID:       foreignTicketID,
		ActorType:      models.ActorTypeSystem,
		ActorID:        "admin-list-postgres-test",
		FileName:       "postgres-list-foreign.txt",
		OriginalName:   "postgres-list-foreign.txt",
		FileSize:       1,
		MimeType:       "text/plain",
		StoragePath:    "postgres-list/foreign.txt",
		VirusScan:      models.VirusScanPending,
	})
	if err := db.CreateInBatches(&tickets, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL tickets: %v", err)
	}
	if err := db.CreateInBatches(&leases, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL leases: %v", err)
	}
	if err := db.CreateInBatches(&attachments, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL attachments: %v", err)
	}

	deliveries := make([]models.OutboxDelivery, 0, 151)
	for i := 1; i <= 150; i++ {
		deliveries = append(deliveries, models.OutboxDelivery{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8c00-%012d",
				i,
			),
			CreatedAt:       createdAt,
			UpdatedAt:       createdAt,
			OrganizationID:  projectA.OrganizationID,
			ProjectID:       projectA.ID,
			EventID:         events[0].ID,
			DestinationType: "event_stream",
			DestinationID:   fmt.Sprintf("postgres-list-%03d", i),
			Status:          models.OutboxDeliveryFailed,
			MaxAttempts:     8,
			NextAttemptAt:   createdAt,
		})
	}
	deliveries = append(deliveries, models.OutboxDelivery{
		ID:              "00000000-0000-7000-8c01-000000000001",
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
		OrganizationID:  projectB.OrganizationID,
		ProjectID:       projectB.ID,
		EventID:         events[len(events)-1].ID,
		DestinationType: "event_stream",
		DestinationID:   "postgres-list-foreign",
		Status:          models.OutboxDeliveryFailed,
		MaxAttempts:     8,
		NextAttemptAt:   createdAt,
	})
	if err := db.CreateInBatches(&deliveries, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL Outbox deliveries: %v", err)
	}

	decisions := make([]models.PolicyDecision, 0, 152)
	for i := 1; i <= 151; i++ {
		decisions = append(decisions, models.PolicyDecision{
			ID: fmt.Sprintf(
				"00000000-0000-7000-8d00-%012d",
				i,
			),
			CreatedAt:          createdAt,
			OrganizationID:     projectA.OrganizationID,
			ProjectID:          projectA.ID,
			ServicePrincipalID: principals[0].ID,
			ActorType:          models.ActorTypeServicePrincipal,
			ActorID:            principals[0].ID,
			Scope:              models.ScopeTicketsRead,
			Action:             "ticket.read",
			ResourceType:       "ticket",
			Allowed:            true,
			ReasonCode:         "scope_allowed",
			PolicyEpoch:        1,
			SourceProtocol:     "agent_rest",
			Context:            datatypes.JSON([]byte(`{}`)),
		})
	}
	decisions = append(decisions, models.PolicyDecision{
		ID:                 "00000000-0000-7000-8d01-000000000001",
		CreatedAt:          createdAt,
		OrganizationID:     projectB.OrganizationID,
		ProjectID:          projectB.ID,
		ServicePrincipalID: foreignPrincipal.ID,
		ActorType:          models.ActorTypeServicePrincipal,
		ActorID:            foreignPrincipal.ID,
		Scope:              models.ScopeTicketsRead,
		Action:             "ticket.read",
		ResourceType:       "ticket",
		Allowed:            true,
		ReasonCode:         "scope_allowed",
		PolicyEpoch:        1,
		SourceProtocol:     "agent_rest",
		Context:            datatypes.JSON([]byte(`{}`)),
	})
	if err := db.CreateInBatches(&decisions, 100).Error; err != nil {
		t.Fatalf("seed PostgreSQL policy decisions: %v", err)
	}

	service, err := NewAdminListService(
		db,
		[]byte("admin-list-postgres-stable-cursor-key-20260731"),
	)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return createdAt }
	firstPrincipals, err := service.ListPrincipals(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondPrincipals, err := service.ListPrincipals(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstPrincipals.Total != 150 ||
		firstPrincipals.TotalPages != 2 ||
		len(firstPrincipals.Items) != 100 ||
		len(secondPrincipals.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL principal pages: first=%+v second=%+v",
			firstPrincipals,
			secondPrincipals,
		)
	}
	principalIDs := make(map[string]struct{}, 150)
	for index, principal := range append(
		firstPrincipals.Items,
		secondPrincipals.Items...,
	) {
		if _, duplicate := principalIDs[principal.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL principal %q", principal.ID)
		}
		principalIDs[principal.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8600-%012d",
			150-index,
		)
		if principal.ID != wantID {
			t.Fatalf(
				"PostgreSQL principal[%d]=%q, want stable %q",
				index,
				principal.ID,
				wantID,
			)
		}
	}
	if len(principalIDs) != 150 {
		t.Fatalf(
			"PostgreSQL principal unique count=%d, want 150",
			len(principalIDs),
		)
	}

	firstPolicies, err := service.ListPolicies(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		principals[0].ID,
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondPolicies, err := service.ListPolicies(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		principals[0].ID,
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstPolicies.Total != 150 ||
		firstPolicies.TotalPages != 2 ||
		len(firstPolicies.Items) != 100 ||
		len(secondPolicies.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL policy pages: first=%+v second=%+v",
			firstPolicies,
			secondPolicies,
		)
	}
	policyIDs := make(map[string]struct{}, 150)
	for index, policy := range append(
		firstPolicies.Items,
		secondPolicies.Items...,
	) {
		if _, duplicate := policyIDs[policy.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL policy %q", policy.ID)
		}
		policyIDs[policy.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8900-%012d",
			150-index,
		)
		if policy.ID != wantID {
			t.Fatalf(
				"PostgreSQL policy[%d]=%q, want stable %q",
				index,
				policy.ID,
				wantID,
			)
		}
	}
	if len(policyIDs) != 150 {
		t.Fatalf(
			"PostgreSQL policy unique count=%d, want 150",
			len(policyIDs),
		)
	}

	firstLeases, err := service.ListLeases(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondLeases, err := service.ListLeases(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstLeases.Total != 150 ||
		firstLeases.TotalPages != 2 ||
		len(firstLeases.Items) != 100 ||
		len(secondLeases.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL lease pages: first=%+v second=%+v",
			firstLeases,
			secondLeases,
		)
	}
	leaseIDs := make(map[string]struct{}, 150)
	for index, lease := range append(
		firstLeases.Items,
		secondLeases.Items...,
	) {
		if _, duplicate := leaseIDs[lease.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL lease %q", lease.ID)
		}
		leaseIDs[lease.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8b00-%012d",
			index+1,
		)
		if lease.ID != wantID {
			t.Fatalf(
				"PostgreSQL lease[%d]=%q, want stable %q",
				index,
				lease.ID,
				wantID,
			)
		}
		if lease.TicketNumber == "PGLIST-FOREIGN" {
			t.Fatal("cross-project PostgreSQL lease leaked")
		}
	}
	if len(leaseIDs) != 150 {
		t.Fatalf(
			"PostgreSQL lease unique count=%d, want 150",
			len(leaseIDs),
		)
	}

	firstAttachments, err := service.ListAttachments(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondAttachments, err := service.ListAttachments(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstAttachments.Total != 150 ||
		firstAttachments.TotalPages != 2 ||
		len(firstAttachments.Items) != 100 ||
		len(secondAttachments.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL attachment pages: first=%+v second=%+v",
			firstAttachments,
			secondAttachments,
		)
	}
	attachmentIDs := make(map[uint]struct{}, 150)
	for index, attachment := range append(
		firstAttachments.Items,
		secondAttachments.Items...,
	) {
		if _, duplicate := attachmentIDs[attachment.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL attachment %d", attachment.ID)
		}
		attachmentIDs[attachment.ID] = struct{}{}
		wantID := uint(150 - index)
		if attachment.ID != wantID {
			t.Fatalf(
				"PostgreSQL attachment[%d]=%d, want stable %d",
				index,
				attachment.ID,
				wantID,
			)
		}
		if attachment.OriginalName == "postgres-list-foreign.txt" {
			t.Fatal("cross-project PostgreSQL attachment leaked")
		}
	}
	if len(attachmentIDs) != 150 {
		t.Fatalf(
			"PostgreSQL attachment unique count=%d, want 150",
			len(attachmentIDs),
		)
	}

	firstOutbox, err := service.ListOutbox(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 1, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondOutbox, err := service.ListOutbox(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminPageQuery{Page: 2, PageSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstOutbox.Total != 150 ||
		firstOutbox.TotalPages != 2 ||
		len(firstOutbox.Items) != 100 ||
		len(secondOutbox.Items) != 50 {
		t.Fatalf(
			"unexpected PostgreSQL Outbox pages: first=%+v second=%+v",
			firstOutbox,
			secondOutbox,
		)
	}
	outboxIDs := make(map[string]struct{}, 150)
	for index, delivery := range append(
		firstOutbox.Items,
		secondOutbox.Items...,
	) {
		if _, duplicate := outboxIDs[delivery.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL Outbox delivery %q", delivery.ID)
		}
		outboxIDs[delivery.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8c00-%012d",
			150-index,
		)
		if delivery.ID != wantID {
			t.Fatalf(
				"PostgreSQL Outbox[%d]=%q, want stable %q",
				index,
				delivery.ID,
				wantID,
			)
		}
	}
	if len(outboxIDs) != 150 {
		t.Fatalf(
			"PostgreSQL Outbox unique count=%d, want 150",
			len(outboxIDs),
		)
	}

	firstEvents, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := service.ListDomainEvents(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{
			Limit:  100,
			Cursor: firstEvents.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstEvents.Items) != 100 ||
		!firstEvents.HasMore ||
		len(secondEvents.Items) != 51 ||
		secondEvents.HasMore {
		t.Fatalf(
			"unexpected PostgreSQL event pages: first=%+v second=%+v",
			firstEvents,
			secondEvents,
		)
	}
	eventIDs := make(map[string]struct{}, 151)
	for index, event := range append(firstEvents.Items, secondEvents.Items...) {
		if event.Type == "io.chronodesk.test.admin-list.foreign.v1" {
			t.Fatal("cross-project PostgreSQL event leaked")
		}
		if _, duplicate := eventIDs[event.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL event %q", event.ID)
		}
		eventIDs[event.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8700-%012d",
			151-index,
		)
		if event.ID != wantID {
			t.Fatalf(
				"PostgreSQL event[%d]=%q, want stable %q",
				index,
				event.ID,
				wantID,
			)
		}
	}
	if len(eventIDs) != 151 {
		t.Fatalf(
			"PostgreSQL event unique count=%d, want 151",
			len(eventIDs),
		)
	}

	firstDecisions, err := service.ListPolicyDecisions(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{Limit: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondDecisions, err := service.ListPolicyDecisions(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{
			Limit:  100,
			Cursor: firstDecisions.NextCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstDecisions.Items) != 100 ||
		!firstDecisions.HasMore ||
		len(secondDecisions.Items) != 51 ||
		secondDecisions.HasMore {
		t.Fatalf(
			"unexpected PostgreSQL policy-decision pages: first=%+v second=%+v",
			firstDecisions,
			secondDecisions,
		)
	}
	decisionIDs := make(map[string]struct{}, 151)
	for index, decision := range append(
		firstDecisions.Items,
		secondDecisions.Items...,
	) {
		if _, duplicate := decisionIDs[decision.ID]; duplicate {
			t.Fatalf("duplicate PostgreSQL policy decision %q", decision.ID)
		}
		decisionIDs[decision.ID] = struct{}{}
		wantID := fmt.Sprintf(
			"00000000-0000-7000-8d00-%012d",
			151-index,
		)
		if decision.ID != wantID {
			t.Fatalf(
				"PostgreSQL policy decision[%d]=%q, want stable %q",
				index,
				decision.ID,
				wantID,
			)
		}
		if decision.ActorID == foreignPrincipal.ID {
			t.Fatal("cross-project PostgreSQL policy decision leaked")
		}
	}
	if len(decisionIDs) != 151 {
		t.Fatalf(
			"PostgreSQL policy-decision unique count=%d, want 151",
			len(decisionIDs),
		)
	}

	tampered := firstEvents.NextCursor
	replacement := byte('A')
	if tampered[len(tampered)-1] == replacement {
		replacement = 'B'
	}
	tampered = tampered[:len(tampered)-1] + string(replacement)
	for name, test := range map[string]struct {
		scope  models.ProjectScope
		cursor string
	}{
		"tamper": {
			scope:  projectA.Scope(),
			cursor: tampered,
		},
		"cross-project": {
			scope:  projectB.Scope(),
			cursor: firstEvents.NextCursor,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ListDomainEvents(
				adminListTestContext(t, test.scope),
				test.scope,
				AdminCursorQuery{Limit: 100, Cursor: test.cursor},
			); !errors.Is(err, ErrInvalidAdminListCursor) {
				t.Fatalf(
					"PostgreSQL cursor rejection error=%v, want ErrInvalidAdminListCursor",
					err,
				)
			}
		})
	}
	if _, err := service.ListPolicyDecisions(
		adminListTestContext(t, projectB.Scope()),
		projectB.Scope(),
		AdminCursorQuery{
			Limit:  100,
			Cursor: firstDecisions.NextCursor,
		},
	); !errors.Is(err, ErrInvalidAdminListCursor) {
		t.Fatalf(
			"PostgreSQL cross-project decision cursor error=%v, want ErrInvalidAdminListCursor",
			err,
		)
	}
	if _, err := service.ListPolicyDecisions(
		adminListTestContext(t, projectA.Scope()),
		projectA.Scope(),
		AdminCursorQuery{
			Limit:  100,
			Cursor: firstEvents.NextCursor,
		},
	); !errors.Is(err, ErrInvalidAdminListCursor) {
		t.Fatalf(
			"PostgreSQL event cursor reused for decisions error=%v, want ErrInvalidAdminListCursor",
			err,
		)
	}
}

func openAdminListsPostgresIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("CHRONODESK_POSTGRES_INTEGRATION") != "1" {
		t.Skip(
			"set CHRONODESK_POSTGRES_INTEGRATION=1 for PostgreSQL administrator list evidence",
		)
	}
	rawDSN := strings.TrimSpace(
		os.Getenv("CHRONODESK_POSTGRES_INTEGRATION_DSN"),
	)
	if rawDSN == "" {
		t.Fatal("CHRONODESK_POSTGRES_INTEGRATION_DSN is required")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil || parsed.Hostname() == "" {
		t.Fatal("parse PostgreSQL integration DSN: invalid URL")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatal(
				"PostgreSQL administrator list test requires a loopback target",
			)
		}
	}

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	schemaName := "chronodesk_admin_lists_" + suffix
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	config := &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	}
	adminDB, err := gorm.Open(postgres.Open(rawDSN), config)
	if err != nil {
		t.Fatalf("open PostgreSQL administrator list database: %v", err)
	}
	adminSQL, err := adminDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	schemaCreated := false
	var runtimeSQL interface{ Close() error }
	t.Cleanup(func() {
		if runtimeSQL != nil {
			_ = runtimeSQL.Close()
		}
		if schemaCreated {
			_ = adminDB.Exec(
				"DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE",
			).Error
		}
		_ = adminSQL.Close()
	})
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL administrator list schema: %v", err)
	}
	schemaCreated = true

	runtimeURL := *parsed
	query := runtimeURL.Query()
	query.Set("search_path", schemaName)
	query.Set("application_name", "chronodesk-admin-list-"+suffix)
	query.Set("connect_timeout", "3")
	runtimeURL.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(runtimeURL.String()), config)
	if err != nil {
		t.Fatalf("open scoped PostgreSQL administrator list database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	runtimeSQL = sqlDB
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

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
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL administrator list schema: %v", err)
	}
	return db
}
