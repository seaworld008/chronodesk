package services

import (
	"context"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestSLAProjectionUnifiesBackgroundRESTAndDashboards(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.IdempotencyRecord{},
	); err != nil {
		t.Fatalf("migrate SLA projection schema: %v", err)
	}

	admin := models.User{
		Username:     "sla-projection-admin",
		Email:        "sla-projection-admin@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("create SLA projection actor: %v", err)
	}
	config := models.SLAConfig{
		Name:            "projection authority",
		IsActive:        true,
		IsDefault:       true,
		ResponseTime:    60,
		ResolutionTime:  120,
		ExcludeWeekends: true,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatalf("create SLA projection config: %v", err)
	}

	now := time.Now()
	legacyDeadline := now.Add(-24 * time.Hour)
	breached := models.Ticket{
		TicketNumber: "SLA-PROJECTION-BREACHED",
		Title:        "Calculated breach",
		Description:  "The scheduler must materialize this breach",
		Type:         models.TicketTypeIncident,
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		Version:      1,
		CreatedAt:    now.AddDate(0, 0, -30),
		CreatedByID:  &admin.ID,
		AssignedToID: &admin.ID,
	}
	notBreached := models.Ticket{
		TicketNumber: "SLA-PROJECTION-CURRENT",
		Title:        "Legacy deadline is not authoritative",
		Description:  "The config-derived deadline is still in the future",
		Type:         models.TicketTypeIncident,
		Priority:     models.TicketPriorityHigh,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		Version:      1,
		CreatedAt:    now,
		CreatedByID:  &admin.ID,
		AssignedToID: &admin.ID,
		SLADueDate:   &legacyDeadline,
	}
	if err := db.Create(&[]*models.Ticket{&breached, &notBreached}).Error; err != nil {
		t.Fatalf("create SLA projection tickets: %v", err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(admin.ID))

	ticketService := newTicketServiceForTest(t, db)
	before, beforeTotal, err := ticketService.GetSLABreachedTickets(
		ctx,
		admin.ID,
		string(models.ProjectRoleAdmin),
	)
	if err != nil {
		t.Fatalf("query pre-scan SLA projection: %v", err)
	}
	if beforeTotal != 0 || len(before) != 0 {
		t.Fatalf(
			"REST must not derive breach from a stale deadline: total=%d tickets=%d",
			beforeTotal,
			len(before),
		)
	}

	native := NewAgentNativeService(db)
	escalation := NewEscalationService(db)
	escalation.SetAgentNativeService(native)
	if err := escalation.CheckSLAViolations(context.Background()); err != nil {
		t.Fatalf("refresh SLA projections: %v", err)
	}

	var persistedBreached models.Ticket
	if err := db.First(&persistedBreached, breached.ID).Error; err != nil {
		t.Fatalf("reload breached ticket: %v", err)
	}
	if !persistedBreached.SLABreached || persistedBreached.SLADueDate == nil {
		t.Fatalf("breached projection was not materialized: %+v", persistedBreached)
	}
	automation := NewAutomationService(db)
	selectedConfig, err := automation.GetSLAConfigForTicket(
		ctx,
		&persistedBreached,
	)
	if err != nil {
		t.Fatalf("automation config selection: %v", err)
	}
	_, expectedDeadline, err := automation.CalculateSLADeadlines(
		ctx,
		&persistedBreached,
		selectedConfig,
	)
	if err != nil {
		t.Fatalf("automation deadline calculation: %v", err)
	}
	if !persistedBreached.SLADueDate.Equal(expectedDeadline) {
		t.Fatalf(
			"background projection deadline = %v, automation deadline = %v",
			persistedBreached.SLADueDate,
			expectedDeadline,
		)
	}

	var persistedCurrent models.Ticket
	if err := db.First(&persistedCurrent, notBreached.ID).Error; err != nil {
		t.Fatalf("reload current ticket: %v", err)
	}
	if persistedCurrent.SLABreached || persistedCurrent.SLADueDate == nil {
		t.Fatalf("current projection was not refreshed: %+v", persistedCurrent)
	}
	if persistedCurrent.SLADueDate.Equal(legacyDeadline) {
		t.Fatal("legacy deadline survived authoritative projection refresh")
	}

	tickets, total, err := ticketService.GetSLABreachedTickets(
		ctx,
		admin.ID,
		string(models.ProjectRoleAdmin),
	)
	if err != nil {
		t.Fatalf("query refreshed REST SLA projection: %v", err)
	}
	if total != 1 || len(tickets) != 1 || tickets[0].ID != breached.ID {
		t.Fatalf("REST SLA projection mismatch: total=%d tickets=%+v", total, tickets)
	}

	stats, err := ticketService.GetTicketStatistics(
		ctx,
		admin.ID,
		string(models.ProjectRoleAdmin),
	)
	if err != nil {
		t.Fatalf("query ticket dashboard projection: %v", err)
	}
	if stats.SLABreached != 1 {
		t.Fatalf("ticket dashboard SLA breaches = %d, want 1", stats.SLABreached)
	}

	dashboard, err := escalation.GetSLADashboard(ctx)
	if err != nil {
		t.Fatalf("query SLA dashboard projection: %v", err)
	}
	currentViolations, ok := dashboard["current_violations"].(int64)
	if !ok || currentViolations != 1 {
		t.Fatalf(
			"SLA dashboard current violations = %#v, want int64(1)",
			dashboard["current_violations"],
		)
	}
}

func TestTicketMutationRefreshesSLAProjectionInDomainTransaction(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketHistory{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
	); err != nil {
		t.Fatalf("migrate SLA mutation schema: %v", err)
	}

	actor := models.User{
		Username:     "sla-mutation-actor",
		Email:        "sla-mutation-actor@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&actor).Error; err != nil {
		t.Fatalf("create SLA mutation actor: %v", err)
	}
	normalConfig := models.SLAConfig{
		Name:            "normal SLA",
		IsActive:        true,
		IsDefault:       true,
		ResponseTime:    30,
		ResolutionTime:  240,
		ExcludeWeekends: true,
	}
	highPriority := string(models.TicketPriorityHigh)
	highConfig := models.SLAConfig{
		Name:            "high priority SLA",
		IsActive:        true,
		Priority:        &highPriority,
		ResponseTime:    15,
		ResolutionTime:  60,
		ExcludeWeekends: true,
	}
	if err := db.Create(&[]*models.SLAConfig{&normalConfig, &highConfig}).Error; err != nil {
		t.Fatalf("create SLA mutation configs: %v", err)
	}

	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 3, 10, 0, 0, 0, location)
	native := NewAgentNativeService(db, AgentNativeOptions{Now: func() time.Time { return now }})
	ctx := testProjectOperationContext(t, db, models.HumanActor(actor.ID))
	created, err := native.CreateNativeTicket(ctx, NativeTicketCreateInput{
		Request: models.TicketCreateRequest{
			Title:       "SLA mutation projection",
			Description: "Priority change must refresh the deadline",
			Type:        models.TicketTypeIncident,
			Priority:    models.TicketPriorityNormal,
			Source:      models.TicketSourceWeb,
		},
		Actor:          models.HumanActor(actor.ID),
		SourceProtocol: "rest-human",
		TrustLevel:     models.TicketTrustLevelUntrusted,
	})
	if err != nil {
		t.Fatalf("create SLA mutation ticket: %v", err)
	}
	initialDeadline := time.Date(2026, time.August, 3, 14, 0, 0, 0, location)
	if created.Ticket.SLADueDate == nil || !created.Ticket.SLADueDate.Equal(initialDeadline) {
		t.Fatalf(
			"initial SLA projection = %v, want %v",
			created.Ticket.SLADueDate,
			initialDeadline,
		)
	}

	updated, err := native.UpdateTicketVersion(ctx, VersionedTicketUpdateInput{
		TicketID:        created.Ticket.ID,
		ExpectedVersion: created.Ticket.Version,
		Actor:           models.HumanActor(actor.ID),
		Action:          "ticket.update",
		SourceProtocol:  "rest-human",
		Changes: map[string]any{
			"priority": models.TicketPriorityHigh,
		},
	})
	if err != nil {
		t.Fatalf("update SLA mutation ticket: %v", err)
	}
	highDeadline := time.Date(2026, time.August, 3, 11, 0, 0, 0, location)
	if updated.Ticket.SLADueDate == nil || !updated.Ticket.SLADueDate.Equal(highDeadline) {
		t.Fatalf(
			"updated SLA projection = %v, want %v",
			updated.Ticket.SLADueDate,
			highDeadline,
		)
	}
	if !fieldsContain(updated.Receipt.ChangedFields, "sla_due_date") {
		t.Fatalf(
			"SLA projection change missing from atomic receipt: %v",
			updated.Receipt.ChangedFields,
		)
	}
}
