package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type projectEventAppenderStub struct {
	input DomainEventInput
	err   error
	calls int
}

func (stub *projectEventAppenderStub) AppendDomainEventTx(
	_ context.Context,
	_ *gorm.DB,
	input DomainEventInput,
	_ []OutboxTarget,
) (*models.DomainEvent, error) {
	stub.calls++
	stub.input = input
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
		Username: "member",
		Email:    "member@example.test",
		Role:     models.RoleAgent,
		Status:   models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return organization, unit, project, user
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
		Username: "outsider",
		Email:    "outsider@example.test",
		Role:     models.RoleAgent,
		Status:   models.UserStatusActive,
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
		false,
	); err != nil {
		t.Fatalf("member project resolution failed: %v", err)
	}
	if _, err := service.ResolveHumanProject(
		context.Background(),
		"OPS",
		outsider.ID,
		false,
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
		Username: "membership-target",
		Email:    "membership-target@example.test",
		Role:     models.RoleAgent,
		Status:   models.UserStatusActive,
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
	target := models.User{
		Username: "rollback-target",
		Email:    "rollback-target@example.test",
		Role:     models.RoleCustomer,
		Status:   models.UserStatusActive,
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
		false,
	); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("ambiguous organization-local key error = %v", err)
	}
}
