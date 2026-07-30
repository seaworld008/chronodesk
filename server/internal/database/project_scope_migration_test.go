package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateProjectScopeBackfillsTicketBoundaryAndQueues(t *testing.T) {
	db := openProjectScopeMigrationDB(t, "tickets")
	if err := db.AutoMigrate(&models.Ticket{}); err != nil {
		t.Fatal(err)
	}
	firstCreated := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	tickets := []models.Ticket{
		{
			TicketNumber: "LEGACY-2",
			Title:        "Database",
			Description:  "database queue",
			Type:         models.TicketTypeIncident,
			Priority:     models.TicketPriorityHigh,
			Status:       models.TicketStatusOpen,
			Source:       models.TicketSourceWeb,
			Version:      1,
			CreatedAt:    firstCreated,
			CustomFields: datatypes.NewJSONType(map[string]any{
				"queue": "Database Support",
				"asset": "db-1",
			}),
		},
		{
			TicketNumber: "LEGACY-1",
			Title:        "General",
			Description:  "default queue",
			Type:         models.TicketTypeRequest,
			Priority:     models.TicketPriorityNormal,
			Status:       models.TicketStatusOpen,
			Source:       models.TicketSourceWeb,
			Version:      1,
			CreatedAt:    firstCreated.Add(time.Minute),
			CustomFields: datatypes.NewJSONType(map[string]any{
				"asset": "app-1",
			}),
		},
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Ticket{}).
		Where("id IN ?", []uint{tickets[0].ID, tickets[1].ID}).
		UpdateColumn("public_id", "").Error; err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateProjectScope(
			db,
			testProjectScopeMembershipWriter,
		); err != nil {
			t.Fatalf("migration attempt %d: %v", attempt, err)
		}
	}

	organization, _, project, defaultQueue := loadDefaultProjectHierarchy(t, db)
	var migrated []models.Ticket
	if err := db.Order("created_at ASC, id ASC").Find(&migrated).Error; err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 2 {
		t.Fatalf("ticket count = %d", len(migrated))
	}
	if migrated[0].TicketNumber != "DEFAULT-1" ||
		migrated[1].TicketNumber != "DEFAULT-2" ||
		project.TicketSequence != 2 {
		t.Fatalf("ticket sequence backfill = %+v, project=%+v", migrated, project)
	}
	for _, ticket := range migrated {
		parsed, err := uuid.Parse(ticket.PublicID)
		if err != nil || parsed.Version() != 7 {
			t.Fatalf("ticket public id = %q: %v", ticket.PublicID, err)
		}
		if ticket.OrganizationID != organization.ID ||
			ticket.ProjectID != project.ID ||
			ticket.QueueID == 0 ||
			ticket.RequestTypeVersionID == "" ||
			ticket.WorkflowVersionID != bootstrapWorkflow {
			t.Fatalf("ticket project scope not backfilled: %+v", ticket)
		}
		if _, exists := ticket.CustomFields.Data()["queue"]; exists {
			t.Fatalf("legacy queue projection remains: %+v", ticket.CustomFields.Data())
		}
	}
	if migrated[1].QueueID != defaultQueue.ID {
		t.Fatalf(
			"default queue id = %d, want %d",
			migrated[1].QueueID,
			defaultQueue.ID,
		)
	}
	var migratedQueue models.Queue
	if err := db.Where(
		"project_id = ? AND key = ?",
		project.ID,
		models.QueueKey("database-support"),
	).First(&migratedQueue).Error; err != nil {
		t.Fatal(err)
	}
	if migrated[0].QueueID != migratedQueue.ID {
		t.Fatalf("migrated queue id = %d, want %d", migrated[0].QueueID, migratedQueue.ID)
	}
}

func TestMigrateProjectScopeBackfillsLegacyChildEventAndDeliveryRows(t *testing.T) {
	db := openProjectScopeMigrationDB(t, "project-owned-rows")
	if err := db.AutoMigrate(
		&models.Ticket{},
		&models.TicketComment{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.AutomationRule{},
		&models.AutomationLog{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		TicketNumber: "LEGACY-CHILD-1",
		Title:        "Legacy child",
		Description:  "scope descendants",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		Version:      1,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comment := models.TicketComment{
		TicketID:    ticket.ID,
		ActorType:   models.ActorTypeSystem,
		ActorID:     "legacy-import",
		Content:     "legacy",
		ContentType: "text",
		Type:        models.CommentTypeSystem,
	}
	if err := db.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	event := models.DomainEvent{
		ID:              "00000000-0000-4000-8000-000000000301",
		SpecVersion:     "1.0",
		Source:          "urn:chronodesk:test",
		Type:            "io.chronodesk.ticket.created.v1",
		Subject:         fmt.Sprintf("ticket/%d", ticket.ID),
		Time:            time.Now().UTC(),
		DataContentType: "application/json",
		Data:            datatypes.JSON(`{"ticket_id":1}`),
		ActorType:       models.ActorTypeSystem,
		ActorID:         "legacy-import",
		ResourceVersion: 1,
	}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	delivery := models.OutboxDelivery{
		ID:              "00000000-0000-4000-8000-000000000302",
		EventID:         event.ID,
		DestinationType: "event_stream",
		DestinationID:   "default",
		Status:          models.OutboxDeliveryPending,
		MaxAttempts:     8,
		NextAttemptAt:   time.Now().UTC(),
	}
	if err := db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	webhook := models.WebhookConfig{
		Name:       "legacy",
		Provider:   models.WebhookProviderCustom,
		WebhookURL: "https://example.test/hook",
		Status:     models.WebhookStatusActive,
	}
	if err := db.Create(&webhook).Error; err != nil {
		t.Fatal(err)
	}
	webhookLog := models.WebhookLog{
		ConfigID:  webhook.ID,
		EventType: models.WebhookEventTicketCreated,
		Status:    "success",
	}
	if err := db.Create(&webhookLog).Error; err != nil {
		t.Fatal(err)
	}
	rule := models.AutomationRule{
		Name:         "legacy automation",
		RuleType:     "assignment",
		TriggerEvent: "io.chronodesk.ticket.created.v1",
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	automationLog := models.AutomationLog{
		RuleID:       rule.ID,
		TicketID:     ticket.ID,
		TriggerEvent: rule.TriggerEvent,
		ExecutedAt:   time.Now().UTC(),
		Success:      true,
	}
	if err := db.Create(&automationLog).Error; err != nil {
		t.Fatal(err)
	}
	sla := models.SLAConfig{
		Name:           "legacy SLA",
		IsActive:       true,
		ResponseTime:   60,
		ResolutionTime: 120,
	}
	if err := db.Create(&sla).Error; err != nil {
		t.Fatal(err)
	}
	template := models.TicketTemplate{
		Name:     "legacy template",
		Category: "legacy",
		IsActive: true,
	}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	reply := models.QuickReply{
		Name:     "legacy reply",
		Content:  "legacy",
		IsPublic: true,
	}
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateProjectScope(
		db,
		testProjectScopeMembershipWriter,
	); err != nil {
		t.Fatal(err)
	}
	organization, _, project, _ := loadDefaultProjectHierarchy(t, db)
	for _, assertion := range []struct {
		model any
		id    any
	}{
		{model: &models.TicketComment{}, id: comment.ID},
		{model: &models.DomainEvent{}, id: event.ID},
		{model: &models.OutboxDelivery{}, id: delivery.ID},
		{model: &models.WebhookConfig{}, id: webhook.ID},
		{model: &models.WebhookLog{}, id: webhookLog.ID},
		{model: &models.AutomationRule{}, id: rule.ID},
		{model: &models.AutomationLog{}, id: automationLog.ID},
		{model: &models.SLAConfig{}, id: sla.ID},
		{model: &models.TicketTemplate{}, id: template.ID},
		{model: &models.QuickReply{}, id: reply.ID},
	} {
		var count int64
		if err := db.Model(assertion.model).
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				assertion.id,
				organization.ID,
				project.ID,
			).
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%T was not backfilled into the default project", assertion.model)
		}
	}
}

func TestMigrateProjectScopeBackfillsDefaultAuthorizationIdempotently(t *testing.T) {
	db := openProjectScopeMigrationDB(t, "idempotent")
	users := []models.User{
		projectScopeMigrationUser("admin", models.RoleAdmin),
		projectScopeMigrationUser("supervisor", models.RoleSupervisor),
		projectScopeMigrationUser("agent", models.RoleAgent),
		projectScopeMigrationUser("customer", models.RoleCustomer),
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	principals := []models.ServicePrincipal{
		projectScopeMigrationPrincipal(
			"00000000-0000-4000-8000-000000000101",
			"reader",
			`["tickets:read","attachments:read"]`,
		),
		projectScopeMigrationPrincipal(
			"00000000-0000-4000-8000-000000000102",
			"writer",
			`["tickets:read","tickets:update","comments:write"]`,
		),
	}
	if err := db.Create(&principals).Error; err != nil {
		t.Fatalf("seed service principals: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if err := MigrateProjectScope(
			db,
			testProjectScopeMembershipWriter,
		); err != nil {
			t.Fatalf("project scope migration attempt %d: %v", attempt, err)
		}
	}

	organization, unit, project, queue := loadDefaultProjectHierarchy(t, db)
	for entity, publicID := range map[string]string{
		"organization":  organization.PublicID,
		"business unit": unit.PublicID,
		"project":       project.PublicID,
		"queue":         queue.PublicID,
	} {
		parsed, err := uuid.Parse(publicID)
		if err != nil || parsed.Version() != 7 {
			t.Fatalf("%s public id = %q, want UUIDv7: %v", entity, publicID, err)
		}
	}
	if unit.OrganizationID != organization.ID ||
		project.OrganizationID != organization.ID ||
		project.BusinessUnitID != unit.ID ||
		project.TicketSequence != 0 ||
		queue.ProjectID != project.ID ||
		!queue.IsDefault {
		t.Fatalf(
			"default hierarchy is inconsistent: org=%+v unit=%+v project=%+v queue=%+v",
			organization,
			unit,
			project,
			queue,
		)
	}

	wantRoles := map[uint]models.ProjectRole{
		users[0].ID: models.ProjectRoleAdmin,
		users[1].ID: models.ProjectRoleManager,
		users[2].ID: models.ProjectRoleAgent,
		users[3].ID: models.ProjectRoleRequester,
	}
	var memberships []models.ProjectMembership
	if err := db.Where("project_id = ?", project.ID).
		Order("user_id ASC").
		Find(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if len(memberships) != len(users) {
		t.Fatalf("membership count = %d, want %d", len(memberships), len(users))
	}
	for _, membership := range memberships {
		if membership.Role != wantRoles[membership.UserID] || !membership.IsActive {
			t.Errorf("unexpected membership: %+v", membership)
		}
	}

	var grants []models.ProjectPrincipalGrant
	if err := db.Where("project_id = ?", project.ID).
		Order("service_principal_id ASC").
		Find(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if len(grants) != len(principals) {
		t.Fatalf("grant count = %d, want %d", len(grants), len(principals))
	}
	for index := range grants {
		gotScopes, err := grants[index].ScopeList()
		if err != nil {
			t.Fatal(err)
		}
		wantScopes := principals[index].ScopeList()
		if grants[index].Role != models.ProjectRoleAgent ||
			!grants[index].IsActive ||
			!reflect.DeepEqual(gotScopes, wantScopes) {
			t.Errorf(
				"grant %q = role %q active=%t scopes=%v, want agent/true/%v",
				grants[index].ServicePrincipalID,
				grants[index].Role,
				grants[index].IsActive,
				gotScopes,
				wantScopes,
			)
		}
	}

	// A rerun must not reinterpret identities created after the one-time
	// cutover or undo explicit project-local authorization changes.
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, users[0].ID).
		Updates(map[string]any{
			"role":      models.ProjectRoleObserver,
			"is_active": false,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			project.ID,
			principals[0].ID,
		).
		Updates(map[string]any{
			"scopes":    datatypes.JSON(`["comments:write"]`),
			"is_active": false,
		}).Error; err != nil {
		t.Fatal(err)
	}
	newUser := projectScopeMigrationUser("late-agent", models.RoleAgent)
	if err := db.Create(&newUser).Error; err != nil {
		t.Fatal(err)
	}
	newPrincipal := projectScopeMigrationPrincipal(
		"00000000-0000-4000-8000-000000000103",
		"late-principal",
		`["events:subscribe"]`,
	)
	if err := db.Create(&newPrincipal).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateProjectScope(
		db,
		testProjectScopeMembershipWriter,
	); err != nil {
		t.Fatalf("project scope migration after new identities: %v", err)
	}

	var preservedMembership models.ProjectMembership
	if err := db.Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		users[0].ID,
	).First(&preservedMembership).Error; err != nil {
		t.Fatal(err)
	}
	if preservedMembership.Role != models.ProjectRoleObserver || preservedMembership.IsActive {
		t.Fatalf("explicit membership change was overwritten: %+v", preservedMembership)
	}
	var preservedGrant models.ProjectPrincipalGrant
	if err := db.Where(
		"project_id = ? AND service_principal_id = ?",
		project.ID,
		principals[0].ID,
	).First(&preservedGrant).Error; err != nil {
		t.Fatal(err)
	}
	preservedScopes, err := preservedGrant.ScopeList()
	if err != nil {
		t.Fatal(err)
	}
	if preservedGrant.IsActive ||
		!reflect.DeepEqual(preservedScopes, []string{"comments:write"}) {
		t.Fatalf("explicit principal grant change was overwritten: %+v", preservedGrant)
	}

	var lateMembershipCount int64
	if err := db.Model(&models.ProjectMembership{}).Where(
		"project_id = ? AND user_id = ?",
		project.ID,
		newUser.ID,
	).Count(&lateMembershipCount).Error; err != nil {
		t.Fatal(err)
	}
	if lateMembershipCount != 0 {
		t.Fatalf("late user received %d implicit DEFAULT memberships", lateMembershipCount)
	}
	var lateGrantCount int64
	if err := db.Model(&models.ProjectPrincipalGrant{}).Where(
		"project_id = ? AND service_principal_id = ?",
		project.ID,
		newPrincipal.ID,
	).Count(&lateGrantCount).Error; err != nil {
		t.Fatal(err)
	}
	if lateGrantCount != 0 {
		t.Fatalf("late principal received %d implicit DEFAULT grants", lateGrantCount)
	}

	assertProjectScopeRowCount(t, db, &models.Organization{}, 1)
	assertProjectScopeRowCount(t, db, &models.BusinessUnit{}, 1)
	assertProjectScopeRowCount(t, db, &models.Project{}, 1)
	assertProjectScopeRowCount(t, db, &models.Queue{}, 1)
	assertProjectScopeRowCount(t, db, &models.ProjectMembership{}, 4)
	assertProjectScopeRowCount(t, db, &models.ProjectPrincipalGrant{}, 2)
	assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 1)
}

func TestMigrateProjectScopeCheckpointProtectsLiveMultiProjectData(
	t *testing.T,
) {
	db := openProjectScopeMigrationDB(t, "checkpoint-protects-live-data")
	if err := db.AutoMigrate(&models.Ticket{}); err != nil {
		t.Fatal(err)
	}
	if err := MigrateProjectScope(
		db,
		testProjectScopeMembershipWriter,
	); err != nil {
		t.Fatalf("initial project scope cutover: %v", err)
	}
	organization, _, defaultProject, _ := loadDefaultProjectHierarchy(t, db)
	if err := db.Model(&models.Project{}).
		Where("id = ?", defaultProject.ID).
		UpdateColumn("ticket_sequence", 41).Error; err != nil {
		t.Fatal(err)
	}

	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OTHER",
		Name:           "其他业务线",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	otherProject := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("OTHER"),
		Name:           "其他项目",
		Status:         models.ProjectStatusActive,
		TicketSequence: 7,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	otherQueue := models.Queue{
		ProjectID: otherProject.ID,
		Key:       models.QueueKey("other"),
		Name:      "其他队列",
		Status:    models.QueueStatusActive,
		IsDefault: true,
	}
	if err := db.Create(&otherQueue).Error; err != nil {
		t.Fatal(err)
	}
	otherTicket := models.Ticket{
		OrganizationID:       organization.ID,
		ProjectID:            otherProject.ID,
		QueueID:              otherQueue.ID,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000009001",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000009002",
		TicketNumber:         "OTHER-7",
		Title:                "保持项目边界",
		Description:          "迁移重跑不能重写",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceAPI,
		Version:              3,
		CreatedByActorType:   models.ActorTypeSystem,
		CreatedByActorID:     "checkpoint-regression",
	}
	if err := db.Create(&otherTicket).Error; err != nil {
		t.Fatal(err)
	}
	originalPublicID := otherTicket.PublicID

	lateUser := projectScopeMigrationUser("other-only-user", models.RoleAgent)
	if err := db.Create(&lateUser).Error; err != nil {
		t.Fatal(err)
	}
	latePrincipal := projectScopeMigrationPrincipal(
		"00000000-0000-4000-8000-000000009003",
		"other-only-principal",
		`["tickets:read"]`,
	)
	if err := db.Create(&latePrincipal).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateProjectScope(
		db,
		testProjectScopeMembershipWriter,
	); err != nil {
		t.Fatalf("repeat project scope migration: %v", err)
	}

	var preserved models.Ticket
	if err := db.Unscoped().First(&preserved, otherTicket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.OrganizationID != organization.ID ||
		preserved.ProjectID != otherProject.ID ||
		preserved.QueueID != otherQueue.ID ||
		preserved.PublicID != originalPublicID ||
		preserved.TicketNumber != "OTHER-7" ||
		preserved.RequestTypeVersionID != otherTicket.RequestTypeVersionID ||
		preserved.WorkflowVersionID != otherTicket.WorkflowVersionID ||
		preserved.Version != 3 {
		t.Fatalf("repeat cutover rewrote scoped ticket: %+v", preserved)
	}
	var reloadedDefault models.Project
	if err := db.First(&reloadedDefault, defaultProject.ID).Error; err != nil {
		t.Fatal(err)
	}
	var reloadedOther models.Project
	if err := db.First(&reloadedOther, otherProject.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedDefault.TicketSequence != 41 ||
		reloadedOther.TicketSequence != 7 {
		t.Fatalf(
			"repeat cutover changed project sequences: DEFAULT=%d OTHER=%d",
			reloadedDefault.TicketSequence,
			reloadedOther.TicketSequence,
		)
	}
	var implicitMemberships int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", defaultProject.ID, lateUser.ID).
		Count(&implicitMemberships).Error; err != nil {
		t.Fatal(err)
	}
	var implicitGrants int64
	if err := db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			defaultProject.ID,
			latePrincipal.ID,
		).
		Count(&implicitGrants).Error; err != nil {
		t.Fatal(err)
	}
	if implicitMemberships != 0 || implicitGrants != 0 {
		t.Fatalf(
			"repeat cutover granted DEFAULT access: memberships=%d grants=%d",
			implicitMemberships,
			implicitGrants,
		)
	}
	assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 1)
}

func TestMigrateProjectScopeRejectsMismatchedCheckpoint(t *testing.T) {
	db := openProjectScopeMigrationDB(t, "mismatched-checkpoint")
	if err := db.Create(&models.SchemaMigrationCheckpoint{
		Key:         projectScopeCutoverCheckpointKey,
		Version:     projectScopeCutoverCheckpointVersion,
		Checksum:    strings.Repeat("0", 64),
		CompletedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	err := MigrateProjectScope(db, testProjectScopeMembershipWriter)
	if err == nil || !strings.Contains(err.Error(), "version or checksum") {
		t.Fatalf("mismatched checkpoint error = %v", err)
	}
	assertProjectScopeRowCount(t, db, &models.Organization{}, 0)
}

func TestMigrateProjectScopeWritesCheckpointOnlyAfterAtomicBackfill(
	t *testing.T,
) {
	db := openProjectScopeMigrationDB(t, "checkpoint-after-backfill")
	users := []models.User{
		projectScopeMigrationUser("first-legacy-user", models.RoleAdmin),
		projectScopeMigrationUser("second-legacy-user", models.RoleAgent),
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected membership writer failure")
	calls := 0
	failingWriter := func(
		_ context.Context,
		tx *gorm.DB,
		user models.User,
		scope models.ProjectScope,
		role models.ProjectRole,
	) error {
		calls++
		if calls == 2 {
			return injected
		}
		return testProjectScopeMembershipWriter(
			context.Background(),
			tx,
			user,
			scope,
			role,
		)
	}
	err := MigrateProjectScope(db, failingWriter)
	if !errors.Is(err, injected) {
		t.Fatalf("cutover error = %v, want injected failure", err)
	}
	assertProjectScopeRowCount(t, db, &models.Organization{}, 0)
	assertProjectScopeRowCount(t, db, &models.ProjectMembership{}, 0)
	assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 0)

	if err := MigrateProjectScope(
		db,
		testProjectScopeMembershipWriter,
	); err != nil {
		t.Fatalf("retry project scope cutover: %v", err)
	}
	assertProjectScopeRowCount(t, db, &models.Organization{}, 1)
	assertProjectScopeRowCount(t, db, &models.ProjectMembership{}, 2)
	assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 1)
}

func TestMigrateProjectScopeFailsClosedAndRollsBack(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *gorm.DB)
		want string
	}{
		{
			name: "unsupported human role",
			seed: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`
					INSERT INTO users (
						username, email, password_hash, role, status
					) VALUES (
						'legacy-role',
						'legacy-role@example.test',
						'hash',
						'legacy',
						'active'
					)
				`).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
					t.Fatal(err)
				}
			},
			want: "unsupported human role",
		},
		{
			name: "malformed principal scopes",
			seed: func(t *testing.T, db *gorm.DB) {
				t.Helper()
				principal := projectScopeMigrationPrincipal(
					"00000000-0000-4000-8000-000000000201",
					"malformed",
					`{"not":"an array"}`,
				)
				if err := db.Create(&principal).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: "decode scopes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openProjectScopeMigrationDB(t, test.name)
			test.seed(t, db)
			err := MigrateProjectScope(
				db,
				testProjectScopeMembershipWriter,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("migration error = %v, want %q", err, test.want)
			}
			assertProjectScopeRowCount(t, db, &models.Organization{}, 0)
			assertProjectScopeRowCount(t, db, &models.BusinessUnit{}, 0)
			assertProjectScopeRowCount(t, db, &models.Project{}, 0)
			assertProjectScopeRowCount(t, db, &models.Queue{}, 0)
			assertProjectScopeRowCount(t, db, &models.ProjectMembership{}, 0)
			assertProjectScopeRowCount(t, db, &models.ProjectPrincipalGrant{}, 0)
			assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 0)
		})
	}
}

func TestMigrateProjectScopeRequiresAuditedWriterForLegacyUsers(
	t *testing.T,
) {
	db := openProjectScopeMigrationDB(t, "membership-writer-required")
	user := projectScopeMigrationUser("legacy-admin", models.RoleAdmin)
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	err := MigrateProjectScope(db)
	if err == nil || !strings.Contains(
		err.Error(),
		"audited project scope membership writer is required",
	) {
		t.Fatalf("migration error = %v, want audited writer requirement", err)
	}
	assertProjectScopeRowCount(t, db, &models.Organization{}, 0)
	assertProjectScopeRowCount(t, db, &models.BusinessUnit{}, 0)
	assertProjectScopeRowCount(t, db, &models.Project{}, 0)
	assertProjectScopeRowCount(t, db, &models.Queue{}, 0)
	assertProjectScopeRowCount(t, db, &models.ProjectMembership{}, 0)
	assertProjectScopeRowCount(t, db, &models.SchemaMigrationCheckpoint{}, 0)
}

func TestMigrateProjectScopeRequiresCompleteSchema(t *testing.T) {
	if err := MigrateProjectScope(nil); err == nil {
		t.Fatal("nil database was accepted")
	}

	db, err := gorm.Open(
		sqlite.Open("file:project-scope-partial?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Organization{}); err != nil {
		t.Fatal(err)
	}
	err = MigrateProjectScope(db)
	if err == nil || !strings.Contains(err.Error(), "business_units table") {
		t.Fatalf("partial schema error = %v", err)
	}
}

func TestRunMigrationsInstallsDefaultProjectScope(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:run-migrations-project-scope?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations(): %v", err)
	}
	if !db.Migrator().HasColumn(&models.Project{}, "ticket_sequence") {
		t.Fatal("RunMigrations() did not create projects.ticket_sequence")
	}
	_, _, project, _ := loadDefaultProjectHierarchy(t, db)
	if project.TicketSequence != 0 {
		t.Fatalf(
			"default project ticket_sequence = %d, want 0",
			project.TicketSequence,
		)
	}
}

func openProjectScopeMigrationDB(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:%s-%s?mode=memory&cache=shared",
			strings.ReplaceAll(t.Name(), "/", "_"),
			strings.ReplaceAll(suffix, " ", "-"),
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.SchemaMigrationCheckpoint{},
		&models.User{},
		&models.ServicePrincipal{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Team{},
		&models.TeamMembership{},
		&models.Queue{},
		&models.ProjectPrincipalGrant{},
	); err != nil {
		t.Fatalf("migrate project scope fixture: %v", err)
	}
	return db
}

func projectScopeMigrationUser(
	name string,
	role models.UserRole,
) models.User {
	return models.User{
		Username:     name,
		Email:        name + "@example.test",
		PasswordHash: "hash",
		Role:         role,
		Status:       models.UserStatusActive,
	}
}

func testProjectScopeMembershipWriter(
	_ context.Context,
	tx *gorm.DB,
	user models.User,
	scope models.ProjectScope,
	role models.ProjectRole,
) error {
	return tx.Create(&models.ProjectMembership{
		ProjectID: scope.ProjectID,
		UserID:    user.ID,
		Role:      role,
		IsActive:  true,
		Version:   1,
	}).Error
}

func projectScopeMigrationPrincipal(
	id string,
	name string,
	scopes string,
) models.ServicePrincipal {
	return models.ServicePrincipal{
		ID:     id,
		Name:   name,
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(scopes),
	}
}

func loadDefaultProjectHierarchy(
	t *testing.T,
	db *gorm.DB,
) (
	models.Organization,
	models.BusinessUnit,
	models.Project,
	models.Queue,
) {
	t.Helper()
	var organization models.Organization
	if err := db.Where("slug = ?", DefaultOrganizationSlug).
		First(&organization).Error; err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	var unit models.BusinessUnit
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		DefaultBusinessUnitKey,
	).First(&unit).Error; err != nil {
		t.Fatalf("load default business unit: %v", err)
	}
	var project models.Project
	if err := db.Where(
		"organization_id = ? AND key = ?",
		organization.ID,
		DefaultProjectKey,
	).First(&project).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	var queue models.Queue
	if err := db.Where(
		"project_id = ? AND key = ?",
		project.ID,
		DefaultQueueKey,
	).First(&queue).Error; err != nil {
		t.Fatalf("load default queue: %v", err)
	}
	return organization, unit, project, queue
}

func assertProjectScopeRowCount(
	t *testing.T,
	db *gorm.DB,
	model any,
	want int64,
) {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%T row count = %d, want %d", model, count, want)
	}
}
