package services

import (
	"context"
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func projectStatusPointer(
	status models.ProjectStatus,
) *models.ProjectStatus {
	return &status
}

func TestProjectServiceListPlatformProjectsIsBoundedFilteredAndStable(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	organization, unit, first, administrator :=
		seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update("platform_role", models.PlatformRolePlatformAdmin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Project{}).
		Where("id = ?", first.ID).
		Updates(map[string]any{
			"name":        "Same name",
			"description": "search target first",
		}).Error; err != nil {
		t.Fatal(err)
	}
	second := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "SECOND",
		Name:           "Same name",
		Description:    "search target second",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	archived := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "ARCHIVED",
		Name:           "Same name",
		Description:    "search target archived",
		Status:         models.ProjectStatusArchived,
	}
	if err := db.Create(&archived).Error; err != nil {
		t.Fatal(err)
	}

	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListPlatformProjectPage(
		context.Background(),
		administrator.ID,
		PlatformProjectListRequest{
			Page:                 1,
			PageSize:             1,
			Search:               "target",
			Status:               projectStatusPointer(models.ProjectStatusActive),
			BusinessUnitPublicID: unit.PublicID,
			OrderBy:              "name",
			Order:                "asc",
		},
	)
	if err != nil {
		t.Fatalf("ListPlatformProjects(): %v", err)
	}
	if page.Total != 2 ||
		page.Page != 1 ||
		page.PageSize != 1 ||
		page.TotalPages != 2 ||
		len(page.Items) != 1 {
		t.Fatalf("platform project page = %+v", page)
	}
	if page.Items[0].PublicID != first.PublicID {
		t.Fatalf(
			"stable first project = %q, want lower persisted id %q",
			page.Items[0].PublicID,
			first.PublicID,
		)
	}
	if page.Items[0].BusinessUnit.PublicID != unit.PublicID ||
		page.Items[0].BusinessUnit.Name != unit.Name {
		t.Fatalf(
			"business unit projection = %+v",
			page.Items[0].BusinessUnit,
		)
	}
}

func TestProjectServiceListPlatformProjectsRejectsInvalidBoundsAndFilters(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, _, administrator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Update("platform_role", models.PlatformRolePlatformAdmin).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	invalidStatus := models.ProjectStatus("unknown")
	for _, test := range []struct {
		name    string
		request PlatformProjectListRequest
	}{
		{
			name:    "zero page",
			request: PlatformProjectListRequest{Page: 0, PageSize: 25},
		},
		{
			name:    "negative page",
			request: PlatformProjectListRequest{Page: -1, PageSize: 25},
		},
		{
			name:    "zero page size",
			request: PlatformProjectListRequest{Page: 1, PageSize: 0},
		},
		{
			name:    "page size over maximum",
			request: PlatformProjectListRequest{Page: 1, PageSize: 101},
		},
		{
			name: "unknown status",
			request: PlatformProjectListRequest{
				Page: 1, PageSize: 25, Status: &invalidStatus,
			},
		},
		{
			name: "unknown order field",
			request: PlatformProjectListRequest{
				Page: 1, PageSize: 25, OrderBy: "organization_id",
			},
		},
		{
			name: "unknown order",
			request: PlatformProjectListRequest{
				Page: 1, PageSize: 25, OrderBy: "name", Order: "sideways",
			},
		},
		{
			name: "invalid business unit public id",
			request: PlatformProjectListRequest{
				Page: 1, PageSize: 25, BusinessUnitPublicID: "1",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ListPlatformProjectPage(
				context.Background(),
				administrator.ID,
				test.request,
			); !errors.Is(err, ErrProjectGovernanceQuery) {
				t.Fatalf(
					"ListPlatformProjects() error = %v, want invalid query",
					err,
				)
			}
		})
	}
}

func TestProjectServiceCreationContextUsesPublicOrganizationAndRemoteUsers(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	organization, unit, _, administrator :=
		seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", administrator.ID).
		Updates(map[string]any{
			"platform_role": models.PlatformRolePlatformAdmin,
			"display_name":  "Creator",
		}).Error; err != nil {
		t.Fatal(err)
	}
	candidate := models.User{
		Username:     "remote-candidate",
		Email:        "remote-candidate@example.test",
		DisplayName:  "Remote Candidate",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&candidate).Error; err != nil {
		t.Fatal(err)
	}
	inactive := models.User{
		Username:     "remote-inactive",
		Email:        "remote-inactive@example.test",
		DisplayName:  "Remote Inactive",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusInactive,
	}
	if err := db.Create(&inactive).Error; err != nil {
		t.Fatal(err)
	}
	archivedUnit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "HISTORICAL",
		Name:           "Historical Unit",
		Description:    "Archived governance dimension",
		Status:         models.BusinessUnitStatusArchived,
	}
	if err := db.Create(&archivedUnit).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}

	options, err := service.GetProjectCreationContext(
		context.Background(),
		administrator.ID,
		ProjectCreationContextRequest{
			Users: ProjectUserSearchRequest{
				Page:     1,
				PageSize: 25,
				Search:   "remote",
			},
			BusinessUnits: PlatformBusinessUnitSearchRequest{
				Page:     1,
				PageSize: 25,
			},
		},
	)
	if err != nil {
		t.Fatalf("GetProjectCreationContext(): %v", err)
	}
	if options.Organization.PublicID != organization.PublicID ||
		options.Organization.Name != organization.Name {
		t.Fatalf("organization option = %+v", options.Organization)
	}
	if options.BusinessUnits.Total != 1 ||
		len(options.BusinessUnits.Items) != 1 ||
		options.BusinessUnits.Items[0].PublicID != unit.PublicID {
		t.Fatalf("business unit options = %+v", options.BusinessUnits)
	}
	if options.Users.Total != 1 ||
		len(options.Users.Items) != 1 ||
		options.Users.Items[0].ID != candidate.ID {
		t.Fatalf("remote active user options = %+v", options.Users)
	}
	if options.Creator.ID != administrator.ID {
		t.Fatalf("creator option = %+v", options.Creator)
	}

	governanceUnits, err := service.ListPlatformProjectBusinessUnits(
		context.Background(),
		administrator.ID,
		PlatformBusinessUnitSearchRequest{
			Page:     1,
			PageSize: 25,
			Search:   "historical",
		},
	)
	if err != nil {
		t.Fatalf("ListPlatformProjectBusinessUnits(): %v", err)
	}
	if governanceUnits.Total != 1 ||
		len(governanceUnits.Items) != 1 ||
		governanceUnits.Items[0].PublicID != archivedUnit.PublicID {
		t.Fatalf(
			"historical governance business units = %+v",
			governanceUnits,
		)
	}
}

func TestProjectServiceCreateProjectUsesOnlyExplicitInitialAdministrators(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, unit, _, creator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", creator.ID).
		Update("platform_role", models.PlatformRolePlatformAdmin).Error; err != nil {
		t.Fatal(err)
	}
	explicitAdministrator := models.User{
		Username:     "explicit-project-admin",
		Email:        "explicit-project-admin@example.test",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&explicitAdministrator).Error; err != nil {
		t.Fatal(err)
	}
	events := &projectEventAppenderStub{}
	service, err := NewProjectService(db, events)
	if err != nil {
		t.Fatal(err)
	}

	project, err := service.CreateProject(
		context.Background(),
		CreateProjectInput{
			ActorUserID:             creator.ID,
			BusinessUnitPublicID:    unit.PublicID,
			Key:                     "EXPLICIT",
			Name:                    "Explicit membership",
			InitialAdministratorIDs: []uint{explicitAdministrator.ID},
			DefaultQueueKey:         "service-desk",
			DefaultQueueName:        "服务台",
		},
	)
	if err != nil {
		t.Fatalf("CreateProject(): %v", err)
	}
	var memberships []models.ProjectMembership
	if err := db.Where("project_id = ?", project.ID).
		Order("user_id ASC").
		Find(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 1 ||
		memberships[0].UserID != explicitAdministrator.ID ||
		memberships[0].Role != models.ProjectRoleAdmin {
		t.Fatalf("explicit initial memberships = %+v", memberships)
	}
	var creatorMembershipCount int64
	if err := db.Model(&models.ProjectMembership{}).
		Where("project_id = ? AND user_id = ?", project.ID, creator.ID).
		Count(&creatorMembershipCount).Error; err != nil {
		t.Fatal(err)
	}
	if creatorMembershipCount != 0 {
		t.Fatalf(
			"creator received %d implicit project memberships",
			creatorMembershipCount,
		)
	}
	if events.input.Actor != models.HumanActor(creator.ID) ||
		events.operation.Actor != models.HumanActor(creator.ID) {
		t.Fatalf(
			"project event actor = input=%+v operation=%+v",
			events.input.Actor,
			events.operation.Actor,
		)
	}
}

func TestProjectServiceCreateProjectRequiresAnExplicitAdministrator(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, unit, _, creator := seedProjectAccessFixture(t, db)
	if err := db.Model(&models.User{}).
		Where("id = ?", creator.ID).
		Update("platform_role", models.PlatformRolePlatformAdmin).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewProjectService(db, &projectEventAppenderStub{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.CreateProject(
		context.Background(),
		CreateProjectInput{
			ActorUserID:          creator.ID,
			BusinessUnitPublicID: unit.PublicID,
			Key:                  "NOADMIN",
			Name:                 "No administrator",
		},
	); !errors.Is(err, ErrInitialProjectAdministrator) {
		t.Fatalf(
			"CreateProject() error = %v, want explicit administrator",
			err,
		)
	}
	var projectCount int64
	if err := db.Model(&models.Project{}).
		Where("key = ?", "NOADMIN").
		Count(&projectCount).Error; err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 {
		t.Fatalf("invalid creation persisted %d project(s)", projectCount)
	}
}

func TestProjectServiceSearchMembershipCandidatesIsBoundedAndActiveOnly(
	t *testing.T,
) {
	db := newProjectServiceTestDB(t)
	_, _, project, requester := seedProjectAccessFixture(t, db)
	candidate := models.User{
		Username:     "membership-candidate",
		Email:        "membership-candidate@example.test",
		DisplayName:  "Membership Candidate",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&candidate).Error; err != nil {
		t.Fatal(err)
	}
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  project.Scope(),
			Actor:  models.HumanActor(requester.ID),
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

	users, err := service.SearchHumanMembershipCandidates(
		operationContext,
		project.Scope(),
		ProjectUserSearchRequest{
			Page:     1,
			PageSize: 25,
			Search:   "candidate",
		},
	)
	if err != nil {
		t.Fatalf("SearchHumanMembershipCandidates(): %v", err)
	}
	if users.Total != 1 ||
		len(users.Items) != 1 ||
		users.Items[0].ID != candidate.ID {
		t.Fatalf("membership candidates = %+v", users)
	}
}
