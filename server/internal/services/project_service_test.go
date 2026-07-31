package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/eventcontract"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type projectEventAppenderStub struct {
	input     DomainEventInput
	operation OperationContext
	err       error
	calls     int
}

func (stub *projectEventAppenderStub) AppendDomainEventTx(
	ctx context.Context,
	_ *gorm.DB,
	input DomainEventInput,
	_ []OutboxTarget,
) (*models.DomainEvent, error) {
	stub.calls++
	stub.input = input
	if operation, err := OperationContextFromContext(ctx); err == nil {
		stub.operation = operation
	}
	if stub.err != nil {
		return nil, stub.err
	}
	return &models.DomainEvent{ID: "project-event"}, nil
}

func newProjectServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close Project service test database: %v", closeErr)
		}
	})
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Team{},
		&models.Queue{},
		&models.ProjectPrincipalGrant{},
		&models.RequestTypeVersion{},
		&models.WorkflowVersion{},
		&models.ConfigurationRelease{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.AuditChainHead{},
		&models.AuditLedgerEntry{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedProjectAccessFixture(
	t *testing.T,
	db *gorm.DB,
) (models.Organization, models.BusinessUnit, models.Project, models.User) {
	t.Helper()
	organization := models.Organization{
		Slug:   "example",
		Name:   "Example",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "member",
		Email:        "member@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return organization, unit, project, user
}

func TestProjectDirectoryPagesAreBoundedStableAndScopeChecked(t *testing.T) {
	db := newProjectServiceTestDB(t)
	organization, unit, project, actor := seedProjectAccessFixture(t, db)
	otherProject := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "OTHER",
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	users := make([]models.User, 0, 152)
	for index := 0; index < 152; index++ {
		users = append(users, models.User{
			Username:     fmt.Sprintf("directory-%03d", index),
			Email:        fmt.Sprintf("directory-%03d@example.test", index),
			PlatformRole: models.PlatformRoleMember,
			Status:       models.UserStatusActive,
		})
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	memberships := make([]models.ProjectMembership, 0, 152)
	for index := 0; index < 151; index++ {
		memberships = append(memberships, models.ProjectMembership{
			ProjectID: project.ID,
			UserID:    users[index].ID,
			Role:      models.ProjectRoleObserver,
			IsActive:  true,
		})
	}
	memberships = append(memberships, models.ProjectMembership{
		ProjectID: otherProject.ID,
		UserID:    users[151].ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
	})
	if err := db.Create(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	queues := make([]models.Queue, 0, 152)
	for index := 0; index < 151; index++ {
		queues = append(queues, models.Queue{
			ProjectID: project.ID,
			Key:       models.QueueKey(fmt.Sprintf("queue-%03d", index)),
			Name:      fmt.Sprintf("Queue %03d", index),
			Status:    models.QueueStatusActive,
		})
	}
	queues = append(queues, models.Queue{
		ProjectID: otherProject.ID,
		Key:       "other-queue",
		Name:      "Other Queue",
		Status:    models.QueueStatusActive,
	})
	if err := db.Create(&queues).Error; err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(actor.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	request := DirectoryPageRequest{
		Page:      1,
		PageSize:  100,
		SortBy:    "user_id",
		SortOrder: "asc",
	}
	firstMemberships, err := service.ListHumanMembershipPage(
		ctx,
		project.Scope(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Page = 2
	secondMemberships, err := service.ListHumanMembershipPage(
		ctx,
		project.Scope(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertDirectoryPageIDs(
		t,
		firstMemberships.Total,
		firstMemberships.TotalPages,
		membershipViewIDs(firstMemberships.Items),
		membershipViewIDs(secondMemberships.Items),
	)

	queueRequest := DirectoryPageRequest{
		Page:      1,
		PageSize:  100,
		SortBy:    "name",
		SortOrder: "asc",
	}
	firstQueues, err := service.ListQueuePage(
		ctx,
		project.Scope(),
		queueRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	queueRequest.Page = 2
	secondQueues, err := service.ListQueuePage(
		ctx,
		project.Scope(),
		queueRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertDirectoryPageIDs(
		t,
		firstQueues.Total,
		firstQueues.TotalPages,
		queueIDs(firstQueues.Items),
		queueIDs(secondQueues.Items),
	)

	invalid := DirectoryPageRequest{}
	if _, err := service.ListHumanMembershipPage(
		context.Background(),
		project.Scope(),
		invalid,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("membership authorization-before-pagination error = %v", err)
	}
	if _, err := service.ListQueuePage(
		context.Background(),
		project.Scope(),
		invalid,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("queue authorization-before-pagination error = %v", err)
	}
}

func membershipViewIDs(items []ProjectMembershipView) []uint {
	result := make([]uint, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func queueIDs(items []models.Queue) []uint {
	result := make([]uint, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func assertDirectoryPageIDs(
	t *testing.T,
	total int64,
	totalPages int,
	first []uint,
	second []uint,
) {
	t.Helper()
	if total != 151 || totalPages != 2 ||
		len(first) != 100 || len(second) != 51 {
		t.Fatalf(
			"unexpected page sizes: total=%d pages=%d first=%d second=%d",
			total,
			totalPages,
			len(first),
			len(second),
		)
	}
	seen := make(map[uint]struct{}, 151)
	for _, id := range append(first, second...) {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("directory row %d appears on multiple pages", id)
		}
		seen[id] = struct{}{}
	}
}

func TestProjectServiceCreateProjectPersistsEventOutboxAndAudit(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, unit, _, administrator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	ledger, err := NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		AuditLedger: ledger,
		DefaultOutboxTargets: []OutboxTarget{
			{Type: "event_stream", ID: "default", MaxAttempts: 8},
		},
	})
	service, err := NewProjectService(db, native)
	if err != nil {
		t.Fatal(err)
	}

	project, err := service.CreateProject(context.Background(), CreateProjectInput{
		ActorUserID:             administrator.ID,
		BusinessUnitPublicID:    unit.PublicID,
		Key:                     "NEW",
		Name:                    "New Project",
		InitialAdministratorIDs: []uint{administrator.ID},
	})
	if err != nil {
		t.Fatalf("CreateProject(): %v", err)
	}

	var event models.DomainEvent
	if err := db.Where(
		"type = ?",
		eventcontract.ProjectCreatedEventType,
	).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.OrganizationID != project.OrganizationID ||
		event.ProjectID != project.ID ||
		event.ActorType != models.ActorTypeHuman ||
		event.ActorID != models.HumanActor(administrator.ID).ID ||
		event.Subject != "project/"+
			strconv.FormatUint(uint64(project.ID), 10) ||
		event.ResourceVersion != 1 {
		t.Fatalf("project event identity = %+v", event)
	}
	var release models.ConfigurationRelease
	if err := db.Where(
		"organization_id = ? AND project_id = ?",
		project.OrganizationID,
		project.ID,
	).First(&release).Error; err != nil {
		t.Fatal(err)
	}
	if event.ConfigurationVersion != release.ID {
		t.Fatalf(
			"event configuration version = %q, want %q",
			event.ConfigurationVersion,
			release.ID,
		)
	}
	var data struct {
		OrganizationID              uint              `json:"organization_id"`
		ProjectID                   uint              `json:"project_id"`
		ProjectKey                  models.ProjectKey `json:"project_key"`
		CreatorUserID               string            `json:"creator_user_id"`
		InitialProjectAdminUserIDs  []uint            `json:"initial_project_admin_user_ids"`
		DefaultQueueID              uint              `json:"default_queue_id"`
		ConfigurationReleaseID      string            `json:"configuration_release_id"`
		ConfigurationReleaseVersion uint64            `json:"configuration_release_version"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.OrganizationID != project.OrganizationID ||
		data.ProjectID != project.ID ||
		data.ProjectKey != project.Key ||
		data.CreatorUserID != models.HumanActor(administrator.ID).ID ||
		!slices.Equal(
			data.InitialProjectAdminUserIDs,
			[]uint{administrator.ID},
		) ||
		data.DefaultQueueID == 0 ||
		data.ConfigurationReleaseID != release.ID ||
		data.ConfigurationReleaseVersion != release.Version {
		t.Fatalf("project event data = %+v", data)
	}
	var delivery models.OutboxDelivery
	if err := db.Where("event_id = ?", event.ID).First(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.OrganizationID != project.OrganizationID ||
		delivery.ProjectID != project.ID ||
		delivery.DestinationType != "event_stream" {
		t.Fatalf("project event outbox = %+v", delivery)
	}
	var audit models.AuditLedgerEntry
	if err := db.Where("domain_event_id = ?", event.ID).First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.ActorType != models.ActorTypeHuman ||
		audit.ActorID != models.HumanActor(administrator.ID).ID ||
		audit.ConfigurationVersion != release.ID ||
		audit.EventType != eventcontract.ProjectCreatedEventType {
		t.Fatalf("project event audit = %+v", audit)
	}
}

func TestProjectServiceArchiveProjectPersistsRevocationOutboxAndAudit(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	ledger, err := NewAuditLedgerService(db)
	if err != nil {
		t.Fatal(err)
	}
	native := NewAgentNativeService(db, AgentNativeOptions{
		AuditLedger: ledger,
	})
	service, err := NewProjectService(db, native)
	if err != nil {
		t.Fatal(err)
	}
	actor := models.HumanActor(administrator.ID)

	archived, err := service.ArchiveProject(
		context.Background(),
		project.PublicID,
		actor,
	)
	if err != nil {
		t.Fatalf("ArchiveProject(): %v", err)
	}
	if archived.ID != project.ID ||
		archived.Status != models.ProjectStatusArchived {
		t.Fatalf("archived project = %+v", archived)
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusArchived {
		t.Fatalf("persisted project status = %q", persisted.Status)
	}
	var events []models.DomainEvent
	if err := db.Where(
		"type = ? AND subject = ?",
		ProjectAccessRevokedEventType,
		"project/"+strconv.FormatUint(uint64(project.ID), 10),
	).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("project access-revoked events = %d, want 1", len(events))
	}
	event := events[0]
	if event.OrganizationID != project.OrganizationID ||
		event.ProjectID != project.ID ||
		(models.ActorRef{
			Type: event.ActorType,
			ID:   event.ActorID,
		}) != actor ||
		event.ResourceVersion != 2 {
		t.Fatalf("project access-revoked event = %+v", event)
	}
	var delivery models.OutboxDelivery
	if err := db.Where("event_id = ?", event.ID).
		Take(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.DestinationType != "event_stream" ||
		delivery.DestinationID !=
			projectAccessRevocationOutboxDestinationID ||
		delivery.Status != models.OutboxDeliveryPending {
		t.Fatalf("project access-revoked delivery = %+v", delivery)
	}
	var audit models.AuditLedgerEntry
	if err := db.Where("domain_event_id = ?", event.ID).
		Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.EventType != ProjectAccessRevokedEventType ||
		audit.Actor() != actor ||
		audit.ResourceVersion != 2 {
		t.Fatalf("project access-revoked audit = %+v", audit)
	}

	replayed, err := service.ArchiveProject(
		context.Background(),
		project.PublicID,
		actor,
	)
	if err != nil {
		t.Fatalf("idempotent ArchiveProject(): %v", err)
	}
	if replayed.Status != models.ProjectStatusArchived {
		t.Fatalf("idempotent archived project = %+v", replayed)
	}
	var eventCount int64
	if err := db.Model(&models.DomainEvent{}).
		Where("type = ?", ProjectAccessRevokedEventType).
		Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("idempotent archive events = %d, want 1", eventCount)
	}
}

func TestProjectServiceArchiveProjectRollsBackWhenEventAppendFails(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	appendFailure := errors.New("revocation outbox unavailable")
	service, err := NewProjectService(
		db,
		&projectEventAppenderStub{err: appendFailure},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(administrator.ID),
	)
	if !errors.Is(err, appendFailure) {
		t.Fatalf("ArchiveProject() error = %v, want %v", err, appendFailure)
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusActive {
		t.Fatalf(
			"failed revocation append committed project status %q",
			persisted.Status,
		)
	}
}

func TestProjectServiceArchiveProjectRejectsNonCanonicalUUIDv7(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	service, err := NewProjectService(db, &projectEventAppenderStub{})
	if err != nil {
		t.Fatal(err)
	}
	for _, publicID := range []string{
		"",
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-7000-0000-000000000001",
		" 00000000-0000-7000-8000-000000000001",
		"00000000-0000-7000-8000-00000000000A",
	} {
		if _, err := service.ArchiveProject(
			context.Background(),
			publicID,
			models.HumanActor(1),
		); !errors.Is(err, ErrProjectPublicID) {
			t.Errorf(
				"ArchiveProject(%q) error = %v, want invalid public id",
				publicID,
				err,
			)
		}
	}
}

func TestProjectServiceArchiveProjectRevalidatesPlatformAdministrator(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, member := seedProjectAccessFixture(t, db)
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(member.ID),
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"ArchiveProject() error = %v, want platform administrator denial",
			err,
		)
	}
	if events.calls != 0 {
		t.Fatalf("denied archive appended %d event(s)", events.calls)
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusActive {
		t.Fatalf(
			"denied archive committed project status %q",
			persisted.Status,
		)
	}
}

func TestProjectServiceArchiveProjectPreservesDefaultControlPlaneEnvelope(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Exec(
		"UPDATE projects SET key = ? WHERE id = ?",
		models.ProjectKey("DEFAULT"),
		project.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	project.Key = models.ProjectKey("DEFAULT")
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.ArchiveProject(
		context.Background(),
		project.PublicID,
		models.HumanActor(administrator.ID),
	); !errors.Is(err, ErrDefaultProjectArchive) {
		t.Fatalf(
			"ArchiveProject(DEFAULT) error = %v, want %v",
			err,
			ErrDefaultProjectArchive,
		)
	}
	var persisted models.Project
	if err := db.First(&persisted, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != models.ProjectStatusActive {
		t.Fatalf(
			"DEFAULT project status = %q, want active",
			persisted.Status,
		)
	}
	if events.calls != 0 {
		t.Fatalf("DEFAULT archive appended %d event(s)", events.calls)
	}
	adminUsers, err := NewAdminUserServiceWithAccessRevocationOutbox(
		db,
		events,
	)
	if err != nil {
		t.Fatalf(
			"initialize admin-user access Outbox after denied archive: %v",
			err,
		)
	}
	if adminUsers.eventScope != project.Scope() {
		t.Fatalf(
			"admin-user event scope = %+v, want %+v",
			adminUsers.eventScope,
			project.Scope(),
		)
	}
}

func TestProjectServiceCreateProjectRequiresEventWriterAndRollsBackOnFailure(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	organization, unit, _, administrator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update(
			"platform_role",
			models.PlatformRolePlatformAdmin,
		).Error; err != nil {
		t.Fatal(err)
	}
	input := CreateProjectInput{
		ActorUserID:             administrator.ID,
		BusinessUnitPublicID:    unit.PublicID,
		Key:                     "FAIL",
		Name:                    "Rollback Project",
		InitialAdministratorIDs: []uint{administrator.ID},
	}
	withoutEvents, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutEvents.CreateProject(
		context.Background(),
		input,
	); !errors.Is(err, ErrProjectEventWriter) {
		t.Fatalf("missing event writer error = %v", err)
	}

	eventFailure := errors.New("project event persistence failed")
	events := &projectEventAppenderStub{err: eventFailure}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateProject(context.Background(), input)
	if !errors.Is(err, eventFailure) {
		t.Fatalf("CreateProject() error = %v, want event failure", err)
	}
	if events.calls != 1 ||
		events.input.Type != eventcontract.ProjectCreatedEventType ||
		events.input.Scope.IsZero() ||
		events.input.Scope != events.operation.Scope ||
		events.input.Actor != models.HumanActor(administrator.ID) ||
		events.input.Actor != events.operation.Actor {
		t.Fatalf(
			"failed project event context/input = operation=%+v input=%+v",
			events.operation,
			events.input,
		)
	}
	var projectCount int64
	if err := db.Model(&models.Project{}).
		Where("organization_id = ? AND key = ?", organization.ID, input.Key).
		Count(&projectCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 {
		t.Fatalf("event failure persisted %d projects", projectCount)
	}
	for model, name := range map[any]string{
		&models.ProjectMembership{}:    "memberships",
		&models.Queue{}:                "queues",
		&models.ConfigurationRelease{}: "configuration releases",
	} {
		var count int64
		if err := db.Model(model).
			Where("project_id = ?", events.input.Scope.ProjectID).
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("event failure persisted %d %s", count, name)
		}
	}
}

func TestProjectServiceCreateProjectRevalidatesPlatformAdministrator(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	organization, unit, _, member := seedProjectAccessFixture(t, db)
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.CreateProject(
		context.Background(),
		CreateProjectInput{
			ActorUserID:             member.ID,
			BusinessUnitPublicID:    unit.PublicID,
			Key:                     "DENIED",
			Name:                    "Denied Project",
			InitialAdministratorIDs: []uint{member.ID},
		},
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"CreateProject() error = %v, want platform administrator denial",
			err,
		)
	}
	if events.calls != 0 {
		t.Fatalf("denied project creation appended %d event(s)", events.calls)
	}
	var projectCount int64
	if err := db.Model(&models.Project{}).
		Where(
			"organization_id = ? AND key = ?",
			organization.ID,
			"DENIED",
		).
		Count(&projectCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 {
		t.Fatalf("denied project creation persisted %d project(s)", projectCount)
	}
}

func TestProjectServiceListPlatformProjectsDiscoversAllTargetsWithoutMembership(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	organization, unit, activeProject, member :=
		seedProjectAccessFixture(t, db)
	archivedProject := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "ARCHIVE",
		Name:           "Archived Governance Target",
		Description:    "Retained for platform governance",
		Status:         models.ProjectStatusArchived,
	}
	if err := db.Create(&archivedProject).Error; err != nil {
		t.Fatal(err)
	}
	administrator := models.User{
		Username:     "inventory-platform-admin",
		Email:        "inventory-platform-admin@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: activeProject.ID,
		UserID:    member.ID,
		Role:      models.ProjectRoleObserver,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := service.ListPlatformProjects(
		context.Background(),
		administrator.ID,
	)
	if err != nil {
		t.Fatalf("ListPlatformProjects(): %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("platform project inventory = %+v, want two targets", projects)
	}
	if projects[0].PublicID != activeProject.PublicID ||
		projects[1].PublicID != archivedProject.PublicID {
		t.Fatalf(
			"platform project inventory order/content = %+v",
			projects,
		)
	}
	var administratorMemberships int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("user_id = ?", administrator.ID).
		Count(&administratorMemberships).Error; err != nil {
		t.Fatal(err)
	}
	if administratorMemberships != 0 {
		t.Fatalf(
			"test platform administrator has %d Memberships, want none",
			administratorMemberships,
		)
	}

	payload, err := json.Marshal(projects)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(payload, &items); err != nil {
		t.Fatal(err)
	}
	for index, item := range items {
		for _, required := range []string{
			"public_id",
			"key",
			"name",
			"description",
			"status",
		} {
			if _, ok := item[required]; !ok {
				t.Errorf(
					"project %d is missing %q: %s",
					index,
					required,
					payload,
				)
			}
		}
		for _, forbidden := range []string{
			"id",
			"project_id",
			"organization_id",
			"business_unit_id",
			"scope",
			"project_role",
			"role",
		} {
			if _, ok := item[forbidden]; ok {
				t.Errorf(
					"project %d exposes %q: %s",
					index,
					forbidden,
					payload,
				)
			}
		}
	}
}

func TestProjectServiceListPlatformProjectsRevalidatesExactActivePlatformRole(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, _, _ = seedProjectAccessFixture(t, db)
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	for index, test := range []struct {
		name   string
		role   models.PlatformRole
		status models.UserStatus
	}{
		{
			name:   "member",
			role:   models.PlatformRoleMember,
			status: models.UserStatusActive,
		},
		{
			name:   "security auditor",
			role:   models.PlatformRoleSecurityAuditor,
			status: models.UserStatusActive,
		},
		{
			name:   "emergency operator",
			role:   models.PlatformRoleEmergencyOperator,
			status: models.UserStatusActive,
		},
		{
			name:   "inactive platform administrator",
			role:   models.PlatformRolePlatformAdmin,
			status: models.UserStatusInactive,
		},
		{
			name:   "suspended platform administrator",
			role:   models.PlatformRolePlatformAdmin,
			status: models.UserStatusSuspended,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := models.User{
				Username:     "denied-platform-project-list-" + strconv.Itoa(index),
				Email:        "denied-platform-project-list-" + strconv.Itoa(index) + "@example.test",
				PasswordHash: "hash",
				PlatformRole: test.role,
				Status:       test.status,
			}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := service.ListPlatformProjects(
				context.Background(),
				user.ID,
			); !errors.Is(err, ErrProjectAccessDenied) {
				t.Fatalf(
					"ListPlatformProjects() error = %v, want access denied",
					err,
				)
			}
		})
	}
}

func TestProjectServiceListPlatformProjectsIgnoresLegacyRoleColumn(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, _, _ = seedProjectAccessFixture(t, db)
	if err := db.Exec(
		"ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'customer'",
	).Error; err != nil {
		t.Fatal(err)
	}
	administrator := models.User{
		Username:     "platform-role-authority",
		Email:        "platform-role-authority@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	legacyAdministrator := models.User{
		Username:     "legacy-role-is-not-authority",
		Email:        "legacy-role-is-not-authority@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&administrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyAdministrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET role = ? WHERE id = ?",
		"customer",
		administrator.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET role = ? WHERE id = ?",
		"admin",
		legacyAdministrator.ID,
	).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListPlatformProjects(
		context.Background(),
		administrator.ID,
	); err != nil {
		t.Fatalf("platform_role administrator denied by legacy role: %v", err)
	}
	if _, err := service.ListPlatformProjects(
		context.Background(),
		legacyAdministrator.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"legacy role granted platform inventory: %v",
			err,
		)
	}
}

func TestProjectServiceGrantPrincipalProjectCreatesExplicitBoundedGrant(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, _ := seedProjectAccessFixture(t, db)
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000091",
		Name:   "grant-target",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read","tasks:manage"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	access, err := service.GrantPrincipalProject(
		context.Background(),
		string(project.Key),
		principal.ID,
		models.ProjectRoleAgent,
		[]string{models.ScopeTasksManage, models.ScopeTicketsRead},
		&expiresAt,
	)
	if err != nil {
		t.Fatalf("grant principal project: %v", err)
	}
	if access.Scope != project.Scope() ||
		access.Role != models.ProjectRoleAgent ||
		len(access.Scopes) != 2 {
		t.Fatalf("unexpected project access: %+v", access)
	}

	var grant models.ProjectPrincipalGrant
	if err := db.Where(
		"project_id = ? AND service_principal_id = ?",
		project.ID,
		principal.ID,
	).First(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if !grant.IsActive ||
		grant.Role != models.ProjectRoleAgent ||
		!grant.HasScope(models.ScopeTicketsRead) ||
		!grant.HasScope(models.ScopeTasksManage) ||
		grant.ExpiresAt == nil ||
		!grant.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("persisted grant is incomplete: %+v", grant)
	}
}

func TestProjectServiceGrantPrincipalProjectFailsClosedForAmbiguousKey(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, _, _ = seedProjectAccessFixture(t, db)
	secondOrganization := models.Organization{
		Slug:   "second",
		Name:   "Second",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&secondOrganization).Error; err != nil {
		t.Fatal(err)
	}
	secondUnit := models.BusinessUnit{
		OrganizationID: secondOrganization.ID,
		Key:            "OPS",
		Name:           "Second Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&secondUnit).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Project{
		OrganizationID: secondOrganization.ID,
		BusinessUnitID: secondUnit.ID,
		Key:            "OPS",
		Name:           "Ambiguous Operations",
		Status:         models.ProjectStatusActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000092",
		Name:   "ambiguous-grant-target",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.GrantPrincipalProject(
		context.Background(),
		"OPS",
		principal.ID,
		models.ProjectRoleAgent,
		[]string{models.ScopeTicketsRead},
		nil,
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("ambiguous key error = %v, want project access denied", err)
	}
	var grantCount int64
	if err := db.Model(&models.ProjectPrincipalGrant{}).
		Where("service_principal_id = ?", principal.ID).
		Count(&grantCount).Error; err != nil {
		t.Fatal(err)
	}
	if grantCount != 0 {
		t.Fatalf("ambiguous project key persisted %d grants", grantCount)
	}
}

func TestProjectServiceHumanAndPrincipalIsolation(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, member := seedProjectAccessFixture(t, db)
	outsider := models.User{
		Username:     "outsider",
		Email:        "outsider@example.test",
		PlatformRole: models.PlatformRolePlatformAdmin,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&outsider).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    member.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000001",
		Name:   "project-agent",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             []byte(`["tickets:read"]`),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveHumanProject(
		context.Background(),
		"OPS",
		member.ID,
	); err != nil {
		t.Fatalf("member project resolution failed: %v", err)
	}
	if _, err := service.ResolveHumanProject(
		context.Background(),
		"OPS",
		outsider.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("outsider error = %v", err)
	}
	if _, err := service.ResolvePrincipalProject(
		context.Background(),
		"OPS",
		principal.ID,
		"tickets:read",
	); err != nil {
		t.Fatalf("principal project resolution failed: %v", err)
	}
	if _, err := service.ResolvePrincipalProject(
		context.Background(),
		"OPS",
		principal.ID,
		"tickets:update",
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("scope escalation error = %v", err)
	}
}

func TestProjectServiceRevalidatesHumanAccessInsideProjectTransaction(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, member := seedProjectAccessFixture(t, db)
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    member.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.RevalidateHumanProjectAccess(
		context.Background(),
		project.Scope(),
		member.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("unscoped revalidation error = %v, want access denied", err)
	}

	var current *ProjectAccess
	if err := scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			var revalidateErr error
			current, revalidateErr = service.RevalidateHumanProjectAccess(
				ctx,
				project.Scope(),
				member.ID,
			)
			return revalidateErr
		},
	); err != nil {
		t.Fatalf("active human revalidation: %v", err)
	}
	if current == nil ||
		current.Scope != project.Scope() ||
		current.Role != models.ProjectRoleManager {
		t.Fatalf("active human access = %+v", current)
	}
	humanSnapshot := current.AuthorizationSnapshot
	if humanSnapshot.Scope != project.Scope() ||
		humanSnapshot.ActorType != models.ActorTypeHuman ||
		humanSnapshot.ProjectUpdatedAt.IsZero() ||
		humanSnapshot.UserID != member.ID ||
		humanSnapshot.UserUpdatedAt.IsZero() ||
		humanSnapshot.MembershipID != membership.ID ||
		humanSnapshot.MembershipVersion != membership.Version ||
		humanSnapshot.MembershipUpdatedAt.IsZero() ||
		humanSnapshot.MembershipRole != models.ProjectRoleManager ||
		!humanSnapshot.Matches(humanSnapshot) {
		t.Fatalf("active Human authorization snapshot = %+v", humanSnapshot)
	}

	if err := db.Model(&membership).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	businessExecuted := false
	err = scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			if _, revalidateErr := service.RevalidateHumanProjectAccess(
				ctx,
				project.Scope(),
				member.ID,
			); revalidateErr != nil {
				return revalidateErr
			}
			businessExecuted = true
			return nil
		},
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("revoked human revalidation error = %v, want access denied", err)
	}
	if businessExecuted {
		t.Fatal("revoked human authorization executed the business callback")
	}
}

func TestProjectServiceRevalidatesPrincipalGrantInsideProjectTransaction(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, _ := seedProjectAccessFixture(t, db)
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000093",
		Name:   "transaction-revalidation",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read","tasks:manage"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             []byte(`["tickets:read","tasks:manage"]`),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	var current *ProjectAccess
	if err := scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			var revalidateErr error
			current, revalidateErr = service.RevalidatePrincipalProjectAccess(
				ctx,
				project.Scope(),
				principal.ID,
				models.ScopeTicketsRead,
			)
			return revalidateErr
		},
	); err != nil {
		t.Fatalf("active principal revalidation: %v", err)
	}
	if current == nil ||
		current.Scope != project.Scope() ||
		current.Role != models.ProjectRoleAgent ||
		len(current.Scopes) != 2 {
		t.Fatalf("active principal access = %+v", current)
	}
	principalSnapshot := current.AuthorizationSnapshot
	if principalSnapshot.Scope != project.Scope() ||
		principalSnapshot.ActorType != models.ActorTypeServicePrincipal ||
		principalSnapshot.ProjectUpdatedAt.IsZero() ||
		principalSnapshot.PrincipalID != principal.ID ||
		principalSnapshot.PrincipalUpdatedAt.IsZero() ||
		principalSnapshot.GrantID != grant.ID ||
		principalSnapshot.GrantUpdatedAt.IsZero() ||
		principalSnapshot.GrantRole != models.ProjectRoleAgent ||
		len(principalSnapshot.GrantScopes) != 2 ||
		principalSnapshot.CredentialID != "" ||
		!principalSnapshot.Matches(principalSnapshot) {
		t.Fatalf(
			"active Principal authorization snapshot = %+v",
			principalSnapshot,
		)
	}

	if err := db.Model(&grant).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	businessExecuted := false
	err = scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			if _, revalidateErr := service.RevalidatePrincipalProjectAccess(
				ctx,
				project.Scope(),
				principal.ID,
				models.ScopeTicketsRead,
			); revalidateErr != nil {
				return revalidateErr
			}
			businessExecuted = true
			return nil
		},
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("revoked principal revalidation error = %v, want access denied", err)
	}
	if businessExecuted {
		t.Fatal("revoked principal grant executed the business callback")
	}
}

func TestProjectServicePlatformRolesNeverGrantProjectAccess(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, _ := seedProjectAccessFixture(t, db)
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	for index, platformRole := range []models.PlatformRole{
		models.PlatformRolePlatformAdmin,
		models.PlatformRoleSecurityAuditor,
		models.PlatformRoleEmergencyOperator,
		models.PlatformRoleMember,
	} {
		user := models.User{
			Username:     "platform-only-" + strconv.Itoa(index),
			Email:        "platform-only-" + strconv.Itoa(index) + "@example.test",
			PasswordHash: "hash",
			PlatformRole: platformRole,
			Status:       models.UserStatusActive,
		}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}

		projects, err := service.ListHumanProjects(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("list projects for %q: %v", platformRole, err)
		}
		if len(projects) != 0 {
			t.Fatalf("platform role %q received projects: %+v", platformRole, projects)
		}
		if _, err := service.ResolveHumanProject(
			context.Background(),
			string(project.Key),
			user.ID,
		); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("platform role %q resolve error = %v", platformRole, err)
		}
	}
}

func TestProjectAccessUsesExplicitProjectRoleJSONField(t *testing.T) {
	payload, err := json.Marshal(ProjectAccess{
		Project: models.Project{
			ID:             3,
			PublicID:       "019fb344-fa16-7e13-9c5b-08eb95478098",
			OrganizationID: 5,
			BusinessUnitID: 7,
			Key:            "OPS",
			Name:           "Operations",
			Status:         models.ProjectStatusActive,
			TicketSequence: 99,
			Organization: models.Organization{
				ID:   5,
				Name: "must-not-leak",
			},
			BusinessUnit: models.BusinessUnit{
				ID:   7,
				Name: "must-not-leak",
			},
		},
		Role: models.ProjectRoleManager,
		AuthorizationSnapshot: AuthorizationSnapshot{
			Scope:             models.ProjectScope{OrganizationID: 5, ProjectID: 3},
			ActorType:         models.ActorTypeHuman,
			MembershipID:      11,
			MembershipVersion: 7,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["project_role"]; !exists {
		t.Fatalf("ProjectAccess JSON is missing project_role: %s", payload)
	}
	if _, exists := fields["role"]; exists {
		t.Fatalf("ProjectAccess JSON retained ambiguous role: %s", payload)
	}
	if _, exists := fields["authorization_snapshot"]; exists {
		t.Fatalf("ProjectAccess JSON leaked authorization snapshot: %s", payload)
	}
	var projectEnvelope struct {
		Project map[string]json.RawMessage `json:"project"`
	}
	if err := json.Unmarshal(payload, &projectEnvelope); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"organization",
		"business_unit",
		"ticket_sequence",
	} {
		if _, exists := projectEnvelope.Project[forbidden]; exists {
			t.Fatalf(
				"AuthorizedProject JSON exposed %q: %s",
				forbidden,
				payload,
			)
		}
	}
}

func TestAuthorizationSnapshotMatchesExactLiveAuthorizationState(
	t *testing.T,
) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	snapshot := AuthorizationSnapshot{
		Scope:               models.ProjectScope{OrganizationID: 2, ProjectID: 3},
		ActorType:           models.ActorTypeServicePrincipal,
		ProjectUpdatedAt:    now,
		PrincipalID:         "principal",
		PrincipalUpdatedAt:  now.Add(time.Second),
		GrantID:             5,
		GrantUpdatedAt:      now.Add(2 * time.Second),
		GrantRole:           models.ProjectRoleAgent,
		GrantScopes:         []string{"tickets:read", "tasks:manage"},
		CredentialID:        "credential",
		CredentialUpdatedAt: now.Add(3 * time.Second),
	}
	same := snapshot
	same.GrantScopes = append([]string(nil), snapshot.GrantScopes...)
	if !snapshot.Matches(same) {
		t.Fatalf("identical authorization snapshots did not match")
	}
	if (AuthorizationSnapshot{}).Matches(AuthorizationSnapshot{}) {
		t.Fatal("empty authorization snapshots matched")
	}

	changedGrant := same
	changedGrant.GrantUpdatedAt = changedGrant.GrantUpdatedAt.Add(time.Nanosecond)
	if snapshot.Matches(changedGrant) {
		t.Fatal("changed Grant authorization snapshot matched")
	}
	changedScopes := same
	changedScopes.GrantScopes = []string{"tickets:read"}
	if snapshot.Matches(changedScopes) {
		t.Fatal("changed Grant scope snapshot matched")
	}
	changedCredential := same
	changedCredential.CredentialID = "rotated-credential"
	if snapshot.Matches(changedCredential) {
		t.Fatal("changed credential authorization snapshot matched")
	}
}

func TestProjectServicePrincipalRevalidationSamplesNowAfterGrantLock(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, _ := seedProjectAccessFixture(t, db)
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000094",
		Name:   "clock-order-principal",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             []byte(`["tickets:read"]`),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}

	queried := map[string]bool{}
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:principal-clock-after-locks",
		func(query *gorm.DB) {
			queried[query.Statement.Table] = true
		},
	); err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	nowCalls := 0
	service.now = func() time.Time {
		nowCalls++
		for _, table := range []string{
			"projects",
			"service_principals",
			"project_principal_grants",
		} {
			if !queried[table] {
				t.Fatalf("clock sampled before %s lock query completed", table)
			}
		}
		return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	}

	err = scopeddb.WithProjectScopeContextTransaction(
		context.Background(),
		db,
		project.Scope(),
		func(ctx context.Context) error {
			_, revalidateErr := service.RevalidatePrincipalProjectAccess(
				ctx,
				project.Scope(),
				principal.ID,
				models.ScopeTicketsRead,
			)
			return revalidateErr
		},
	)
	if err != nil {
		t.Fatalf("revalidate Principal with ordered clock: %v", err)
	}
	if nowCalls != 1 {
		t.Fatalf("authorization clock sampled %d times, want 1", nowCalls)
	}
}

func TestProjectServiceMembershipAdministrationUsesStableSubjectLockOrder(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, target := seedProjectAccessFixture(t, db)
	requester := models.User{
		Username:     "later-project-administrator",
		Email:        "later-project-administrator@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&requester).Error; err != nil {
		t.Fatal(err)
	}
	if requester.ID <= target.ID {
		t.Fatalf(
			"test requires reverse caller order: requester=%d target=%d",
			requester.ID,
			target.ID,
		)
	}
	for _, membership := range []models.ProjectMembership{
		{
			ProjectID: project.ID,
			UserID:    target.ID,
			Role:      models.ProjectRoleAdmin,
			IsActive:  true,
			Version:   1,
		},
		{
			ProjectID: project.ID,
			UserID:    requester.ID,
			Role:      models.ProjectRoleAdmin,
			IsActive:  true,
			Version:   1,
		},
	} {
		if err := db.Create(&membership).Error; err != nil {
			t.Fatal(err)
		}
	}

	type lockQuery struct {
		table    string
		strength string
		sql      string
		vars     []any
	}
	var locks []lockQuery
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:membership-administration-lock-order",
		func(query *gorm.DB) {
			lockClause, exists := query.Statement.Clauses["FOR"]
			if !exists {
				return
			}
			locking, ok := lockClause.Expression.(clause.Locking)
			if !ok {
				return
			}
			locks = append(locks, lockQuery{
				table:    query.Statement.Table,
				strength: locking.Strength,
				sql: query.Dialector.Explain(
					query.Statement.SQL.String(),
					query.Statement.Vars...,
				),
				vars: append([]any(nil), query.Statement.Vars...),
			})
		},
	); err != nil {
		t.Fatal(err)
	}

	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.HumanActor(requester.ID),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		project.Scope(),
		func(scopedContext context.Context) error {
			_, deactivateErr := service.DeactivateHumanMembership(
				scopedContext,
				project.Scope(),
				target.ID,
			)
			return deactivateErr
		},
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"preauthorized membership command error = %v, want access denied",
			err,
		)
	}
	if events.calls != 0 {
		t.Fatalf(
			"preauthorized membership command appended %d events",
			events.calls,
		)
	}
	revoked, err := service.DeactivateHumanMembership(
		ctx,
		project.Scope(),
		target.ID,
	)
	if err != nil {
		t.Fatalf("deactivate membership with stable locks: %v", err)
	}
	if revoked.IsActive || events.calls != 1 {
		t.Fatalf("deactivation result=%+v events=%d", revoked, events.calls)
	}

	if len(locks) < 3 {
		t.Fatalf("locking queries = %+v, want Project, Users, Memberships", locks)
	}
	wantTables := []string{"projects", "users", "project_memberships"}
	wantStrengths := []string{"UPDATE", "SHARE", "UPDATE"}
	for index := range wantTables {
		if locks[index].table != wantTables[index] ||
			locks[index].strength != wantStrengths[index] {
			t.Fatalf(
				"lock %d = %+v, want table=%s strength=%s",
				index,
				locks[index],
				wantTables[index],
				wantStrengths[index],
			)
		}
	}
	if !strings.Contains(
		strings.ToUpper(locks[1].sql),
		"ORDER BY ID ASC",
	) || !strings.Contains(
		strings.ToUpper(locks[2].sql),
		"ORDER BY ID ASC",
	) {
		t.Fatalf("subject locks are not explicitly ordered: %+v", locks)
	}
	if len(locks[1].vars) < 2 ||
		locks[1].vars[0] != target.ID ||
		locks[1].vars[1] != requester.ID {
		t.Fatalf(
			"User lock IDs = %+v, want sorted [%d %d]",
			locks[1].vars,
			target.ID,
			requester.ID,
		)
	}

	reverseContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(target.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeactivateHumanMembership(
		reverseContext,
		project.Scope(),
		requester.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("revoked administrator reverse mutation error = %v", err)
	}
	if events.calls != 1 {
		t.Fatalf("denied reverse mutation appended %d events, want 1", events.calls)
	}
}

func TestProjectServiceTicketCreateCommandOwnsStableAuthorizationTransaction(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, requester := seedProjectAccessFixture(t, db)
	membership := models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    requester.ID,
		Role:      models.ProjectRoleRequester,
		IsActive:  true,
		Version:   2,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.HumanActor(requester.ID),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}

	commandCalls := 0
	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		project.Scope(),
		func(scopedContext context.Context) error {
			_, commandErr := service.RunHumanTicketCreateDatabaseCommand(
				scopedContext,
				project.Scope(),
				requester.ID,
				func(context.Context, *gorm.DB, *ProjectAccess) error {
					commandCalls++
					return nil
				},
			)
			return commandErr
		},
	)
	if !errors.Is(err, ErrTicketCreateAccessDenied) {
		t.Fatalf(
			"preauthorized ticket command error = %v, want access denied",
			err,
		)
	}
	if commandCalls != 0 {
		t.Fatalf("preauthorized ticket command ran %d callbacks", commandCalls)
	}

	type lockQuery struct {
		table    string
		strength string
	}
	var locks []lockQuery
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:ticket-create-command-lock-order",
		func(query *gorm.DB) {
			lockClause, exists := query.Statement.Clauses["FOR"]
			if !exists {
				return
			}
			locking, ok := lockClause.Expression.(clause.Locking)
			if !ok {
				return
			}
			locks = append(locks, lockQuery{
				table:    query.Statement.Table,
				strength: locking.Strength,
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	var ticketNumber string
	access, err := service.RunHumanTicketCreateDatabaseCommand(
		ctx,
		project.Scope(),
		requester.ID,
		func(
			commandContext context.Context,
			tx *gorm.DB,
			lockedAccess *ProjectAccess,
		) error {
			commandCalls++
			if lockedAccess == nil ||
				lockedAccess.Role != models.ProjectRoleRequester {
				return errors.New("ticket command did not receive requester access")
			}
			var allocateErr error
			ticketNumber, allocateErr = service.AllocateTicketIdentityTx(
				commandContext,
				tx,
				project.Scope(),
			)
			return allocateErr
		},
	)
	if err != nil {
		t.Fatalf("run ticket create database command: %v", err)
	}
	if commandCalls != 1 ||
		ticketNumber != "OPS-1" ||
		access == nil ||
		access.AuthorizationSnapshot.MembershipID != membership.ID {
		t.Fatalf(
			"ticket command calls=%d number=%q access=%+v",
			commandCalls,
			ticketNumber,
			access,
		)
	}
	if len(locks) < 4 {
		t.Fatalf("ticket command locks = %+v", locks)
	}
	want := []lockQuery{
		{table: "projects", strength: "UPDATE"},
		{table: "users", strength: "SHARE"},
		{table: "project_memberships", strength: "SHARE"},
		{table: "projects", strength: "UPDATE"},
	}
	for index := range want {
		if locks[index] != want[index] {
			t.Fatalf(
				"ticket command lock %d = %+v, want %+v",
				index,
				locks[index],
				want[index],
			)
		}
	}
}

func TestProjectServiceMembershipGrantAndRevocationAreScopedAndAudited(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    administrator.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	target := models.User{
		Username:     "membership-target",
		Email:        "membership-target@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.HumanActor(administrator.ID),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		project.Scope(),
		func(scopedContext context.Context) error {
			_, upsertErr := service.UpsertHumanMembership(
				scopedContext,
				project.Scope(),
				UpsertProjectMembershipInput{
					UserID: target.ID,
					Role:   models.ProjectRoleAgent,
				},
			)
			return upsertErr
		},
	)
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf(
			"preauthorized membership upsert error = %v, want access denied",
			err,
		)
	}
	if events.calls != 0 {
		t.Fatalf("preauthorized membership upsert appended %d events", events.calls)
	}
	granted, err := service.UpsertHumanMembership(
		ctx,
		project.Scope(),
		UpsertProjectMembershipInput{
			UserID: target.ID,
			Role:   models.ProjectRoleAgent,
		},
	)
	if err != nil {
		t.Fatalf("grant membership: %v", err)
	}
	if !granted.IsActive ||
		granted.Role != models.ProjectRoleAgent ||
		granted.UserID != target.ID ||
		granted.Version != 1 {
		t.Fatalf("unexpected membership grant: %+v", granted)
	}
	if events.calls != 1 ||
		events.input.Scope != project.Scope() ||
		events.input.Actor != models.HumanActor(administrator.ID) ||
		events.input.Type != "io.chronodesk.project.membership.upserted.v1" {
		t.Fatalf("unexpected membership event: %+v", events.input)
	}

	revoked, err := service.DeactivateHumanMembership(
		ctx,
		project.Scope(),
		target.ID,
	)
	if err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	if revoked.IsActive || revoked.Version != 2 {
		t.Fatalf("unexpected revoked membership: %+v", revoked)
	}
	if events.calls != 2 ||
		events.input.Type != "io.chronodesk.project.membership.deactivated.v1" {
		t.Fatalf("unexpected revocation event: %+v", events.input)
	}
}

func TestProjectServiceMembershipMutationRollsBackWhenEventFails(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    administrator.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	target := models.User{
		Username:     "rollback-target",
		Email:        "rollback-target@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	eventFailure := errors.New("event persistence failed")
	service, err := NewProjectService(
		db,
		&projectEventAppenderStub{err: eventFailure},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.HumanActor(administrator.ID),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpsertHumanMembership(
		ctx,
		project.Scope(),
		UpsertProjectMembershipInput{
			UserID: target.ID,
			Role:   models.ProjectRoleRequester,
		},
	)
	if !errors.Is(err, eventFailure) {
		t.Fatalf("membership error = %v, want event failure", err)
	}
	var count int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, target.ID).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("event failure persisted %d memberships", count)
	}
}

func TestProjectServiceEnsureMembershipIsIdempotentAndRejectsConflict(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.SystemActor("bootstrap-test"),
		Source: SourceProtocolWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := UpsertProjectMembershipInput{
		UserID: administrator.ID,
		Role:   models.ProjectRoleAdmin,
	}

	first, err := service.EnsureHumanMembership(ctx, project.Scope(), input)
	if err != nil {
		t.Fatalf("create required membership: %v", err)
	}
	second, err := service.EnsureHumanMembership(ctx, project.Scope(), input)
	if err != nil {
		t.Fatalf("repeat required membership: %v", err)
	}
	if first.ID == 0 || second.ID != first.ID ||
		second.Version != first.Version || events.calls != 1 {
		t.Fatalf(
			"ensure was not idempotent: first=%+v second=%+v events=%d",
			first,
			second,
			events.calls,
		)
	}

	_, err = service.EnsureHumanMembership(
		ctx,
		project.Scope(),
		UpsertProjectMembershipInput{
			UserID: administrator.ID,
			Role:   models.ProjectRoleManager,
		},
	)
	if !errors.Is(err, ErrProjectMembershipConflict) {
		t.Fatalf("conflicting grant error = %v, want membership conflict", err)
	}
	var persisted models.ProjectMembership
	if err := db.First(&persisted, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Role != models.ProjectRoleAdmin ||
		!persisted.IsActive ||
		persisted.Version != first.Version ||
		events.calls != 1 {
		t.Fatalf(
			"conflicting ensure mutated grant: membership=%+v events=%d",
			persisted,
			events.calls,
		)
	}
}

func TestProjectServiceProtectsLastActiveProjectAdministrator(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, administrator := seedProjectAccessFixture(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    administrator.ID,
		Role:      models.ProjectRoleAdmin,
		IsActive:  true,
		Version:   1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db, &projectEventAppenderStub{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithOperationContext(context.Background(), OperationContext{
		Scope:  project.Scope(),
		Actor:  models.HumanActor(administrator.ID),
		Source: SourceProtocolHumanREST,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeactivateHumanMembership(
		ctx,
		project.Scope(),
		administrator.ID,
	); !errors.Is(err, ErrLastProjectAdministrator) {
		t.Fatalf("last administrator revocation error = %v", err)
	}
}

func TestProjectServiceGrantExpiryAndAtomicSequence(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, _ := seedProjectAccessFixture(t, db)
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	}

	for index, want := range []string{"OPS-1", "OPS-2"} {
		var got string
		if err := db.Transaction(func(tx *gorm.DB) error {
			var allocateErr error
			got, allocateErr = service.AllocateTicketIdentityTx(
				context.Background(),
				tx,
				project.Scope(),
			)
			return allocateErr
		}); err != nil {
			t.Fatalf("allocate %d: %v", index, err)
		}
		if got != want {
			t.Fatalf("ticket number %d = %q, want %q", index, got, want)
		}
	}

	expiredAt := service.now().Add(-time.Minute)
	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000000002",
		Name:   "expired-agent",
		Status: models.ServicePrincipalStatusActive,
		Scopes: []byte(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             []byte(`["tickets:read"]`),
		IsActive:           true,
		ExpiresAt:          &expiredAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolvePrincipalProject(
		context.Background(),
		"OPS",
		principal.ID,
		"tickets:read",
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("expired grant error = %v", err)
	}
}

func TestProjectServiceRejectsOrganizationImplicitProjectKey(t *testing.T) {
	db := newProjectServiceTestDB(t)
	_, _, project, member := seedProjectAccessFixture(t, db)
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    member.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	otherOrganization := models.Organization{
		Slug:   "other",
		Name:   "Other",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&otherOrganization).Error; err != nil {
		t.Fatal(err)
	}
	otherUnit := models.BusinessUnit{
		OrganizationID: otherOrganization.ID,
		Key:            "OPS",
		Name:           "Other Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&otherUnit).Error; err != nil {
		t.Fatal(err)
	}
	otherProject := models.Project{
		OrganizationID: otherOrganization.ID,
		BusinessUnitID: otherUnit.ID,
		Key:            "OPS",
		Name:           "Other Operations",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: otherProject.ID,
		UserID:    member.ID,
		Role:      models.ProjectRoleAgent,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveHumanProject(
		context.Background(),
		"OPS",
		member.ID,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("ambiguous organization-local key error = %v", err)
	}
}
