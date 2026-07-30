package services

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type projectKnowledgeAccessFixture struct {
	db           *gorm.DB
	resolver     *ProjectKnowledgeAccessResolver
	now          time.Time
	organization models.Organization
	otherOrg     models.Organization
	project      models.Project
	otherProject models.Project
	human        models.User
	outsider     models.User
	principal    models.ServicePrincipal
	activeTeam   models.Team
	humanContext context.Context
	principalCtx context.Context
}

func newProjectKnowledgeAccessFixture(
	t *testing.T,
) projectKnowledgeAccessFixture {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.Organization{},
		&models.BusinessUnit{},
		&models.User{},
		&models.ServicePrincipal{},
		&models.Project{},
		&models.ProjectMembership{},
		&models.Team{},
		&models.TeamMembership{},
		&models.ProjectPrincipalGrant{},
	); err != nil {
		t.Fatal(err)
	}

	organization := models.Organization{
		Slug:   "knowledge-main",
		Name:   "Knowledge Main",
		Status: models.OrganizationStatusActive,
	}
	otherOrg := models.Organization{
		Slug:   "knowledge-other",
		Name:   "Knowledge Other",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherOrg).Error; err != nil {
		t.Fatal(err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "KNOWLEDGE",
		Name:           "Knowledge",
		Status:         models.BusinessUnitStatusActive,
	}
	otherUnit := models.BusinessUnit{
		OrganizationID: otherOrg.ID,
		Key:            "OTHER",
		Name:           "Other",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherUnit).Error; err != nil {
		t.Fatal(err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("KNOW"),
		Name:           "Knowledge",
		Status:         models.ProjectStatusActive,
	}
	otherProject := models.Project{
		OrganizationID: otherOrg.ID,
		BusinessUnitID: otherUnit.ID,
		Key:            models.ProjectKey("OTHER"),
		Name:           "Other",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatal(err)
	}

	human := models.User{
		Username:     "knowledge-member",
		Email:        "knowledge-member@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	outsider := models.User{
		Username:     "knowledge-outsider",
		Email:        "knowledge-outsider@example.test",
		PasswordHash: "hash",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&human).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&outsider).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProjectMembership{
		ProjectID: project.ID,
		UserID:    human.ID,
		Role:      models.ProjectRoleManager,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	activeTeam := models.Team{
		ProjectID: project.ID,
		Key:       models.TeamKey("active-team"),
		Name:      "Active Team",
		Status:    models.TeamStatusActive,
	}
	archivedTeam := models.Team{
		ProjectID: project.ID,
		Key:       models.TeamKey("archived-team"),
		Name:      "Archived Team",
		Status:    models.TeamStatusArchived,
	}
	inactiveMembershipTeam := models.Team{
		ProjectID: project.ID,
		Key:       models.TeamKey("inactive-membership"),
		Name:      "Inactive Membership",
		Status:    models.TeamStatusActive,
	}
	foreignTeam := models.Team{
		ProjectID: otherProject.ID,
		Key:       models.TeamKey("foreign-team"),
		Name:      "Foreign Team",
		Status:    models.TeamStatusActive,
	}
	for _, team := range []*models.Team{
		&activeTeam,
		&archivedTeam,
		&inactiveMembershipTeam,
		&foreignTeam,
	} {
		if err := db.Create(team).Error; err != nil {
			t.Fatal(err)
		}
	}
	memberships := []models.TeamMembership{
		{TeamID: activeTeam.ID, UserID: human.ID, Role: models.TeamRoleMember, IsActive: true},
		{TeamID: archivedTeam.ID, UserID: human.ID, Role: models.TeamRoleMember, IsActive: true},
		{TeamID: inactiveMembershipTeam.ID, UserID: human.ID, Role: models.TeamRoleLead, IsActive: false},
		{TeamID: foreignTeam.ID, UserID: human.ID, Role: models.TeamRoleMember, IsActive: true},
		// A Team membership alone must never substitute for Project membership.
		{TeamID: activeTeam.ID, UserID: outsider.ID, Role: models.TeamRoleMember, IsActive: true},
	}
	if err := db.Create(&memberships).Error; err != nil {
		t.Fatal(err)
	}
	// GORM applies the model's default:true tag when a false bool is inserted.
	if err := db.Model(&models.TeamMembership{}).
		Where(
			"team_id = ? AND user_id = ?",
			inactiveMembershipTeam.ID,
			human.ID,
		).
		Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}

	principal := models.ServicePrincipal{
		ID:     "00000000-0000-7000-8000-000000009201",
		Name:   "knowledge-principal",
		Status: models.ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(time.Hour)
	if err := db.Create(&models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(`["tickets:read"]`),
		IsActive:           true,
		ExpiresAt:          &expiry,
	}).Error; err != nil {
		t.Fatal(err)
	}

	humanContext := projectKnowledgeOperationContext(
		t,
		project.Scope(),
		models.HumanActor(human.ID),
	)
	principalCtx := projectKnowledgeOperationContext(
		t,
		project.Scope(),
		models.ServicePrincipalActor(principal.ID),
	)
	resolver, err := NewProjectKnowledgeAccessResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	resolver.now = func() time.Time { return now }
	return projectKnowledgeAccessFixture{
		db:           db,
		resolver:     resolver,
		now:          now,
		organization: organization,
		otherOrg:     otherOrg,
		project:      project,
		otherProject: otherProject,
		human:        human,
		outsider:     outsider,
		principal:    principal,
		activeTeam:   activeTeam,
		humanContext: humanContext,
		principalCtx: principalCtx,
	}
}

func projectKnowledgeOperationContext(
	t *testing.T,
	scope models.ProjectScope,
	actor models.ActorRef,
) context.Context {
	t.Helper()
	source := SourceProtocolHumanREST
	credentialID := ""
	if actor.Type == models.ActorTypeServicePrincipal {
		source = SourceProtocolAgentREST
		credentialID = "knowledge-test-credential"
	}
	if actor.Type == models.ActorTypeSystem {
		source = SourceProtocolWorker
	}
	ctx, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:        scope,
			Actor:        actor,
			Source:       source,
			CredentialID: credentialID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestProjectKnowledgeAccessResolverHumanRoleAndTeamACL(t *testing.T) {
	fixture := newProjectKnowledgeAccessFixture(t)
	subjects, err := fixture.resolver.ResolveKnowledgeSubjects(
		fixture.humanContext,
		fixture.project.Scope(),
		models.HumanActor(fixture.human.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	expected := []models.KnowledgeACLSubject{
		{
			Type: models.KnowledgeACLProjectRole,
			ID:   string(models.ProjectRoleManager),
		},
		{
			Type: models.KnowledgeACLTeam,
			ID:   fixture.activeTeam.PublicID,
		},
	}
	if len(subjects) != len(expected) {
		t.Fatalf("subjects = %+v", subjects)
	}
	for index := range expected {
		if subjects[index] != expected[index] {
			t.Fatalf("subjects[%d] = %+v, want %+v", index, subjects[index], expected[index])
		}
	}
}

func TestProjectKnowledgeAccessResolverPrincipalGrant(t *testing.T) {
	fixture := newProjectKnowledgeAccessFixture(t)
	subjects, err := fixture.resolver.ResolveKnowledgeSubjects(
		fixture.principalCtx,
		fixture.project.Scope(),
		models.ServicePrincipalActor(fixture.principal.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(subjects) != 1 ||
		subjects[0] != (models.KnowledgeACLSubject{
			Type: models.KnowledgeACLProjectRole,
			ID:   string(models.ProjectRoleAgent),
		}) {
		t.Fatalf("principal subjects = %+v", subjects)
	}
}

func TestParseKnowledgeHumanIDHonorsPlatformUintRange(t *testing.T) {
	maxUint := uint64(^uint(0))
	overflow := "18446744073709551616"
	if strconv.IntSize == 32 {
		overflow = strconv.FormatUint(maxUint+1, 10)
	}

	tests := []struct {
		name    string
		value   string
		want    uint
		wantErr bool
	}{
		{
			name:  "ordinary positive identifier",
			value: "42",
			want:  42,
		},
		{
			name:  "platform maximum identifier",
			value: strconv.FormatUint(maxUint, 10),
			want:  ^uint(0),
		},
		{
			name:    "zero is rejected",
			value:   "0",
			wantErr: true,
		},
		{
			name:    "platform overflow is rejected before conversion",
			value:   overflow,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseKnowledgeHumanID(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf(
						"parseKnowledgeHumanID(%q) = %d, want error",
						test.value,
						got,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf(
					"parseKnowledgeHumanID(%q) error = %v",
					test.value,
					err,
				)
			}
			if got != test.want {
				t.Fatalf(
					"parseKnowledgeHumanID(%q) = %d, want %d",
					test.value,
					got,
					test.want,
				)
			}
		})
	}
}

func TestProjectKnowledgeAccessResolverFailsClosed(t *testing.T) {
	t.Run("missing trusted context", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			context.Background(),
			fixture.project.Scope(),
			models.HumanActor(fixture.human.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("scope and Actor must match context", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			fixture.humanContext,
			fixture.otherProject.Scope(),
			models.HumanActor(fixture.human.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("scope error = %v", err)
		}
		_, err = fixture.resolver.ResolveKnowledgeSubjects(
			fixture.humanContext,
			fixture.project.Scope(),
			models.HumanActor(fixture.outsider.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("Actor error = %v", err)
		}
	})

	t.Run("organization and project pair is validated", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		mismatchedScope := models.ProjectScope{
			OrganizationID: fixture.otherOrg.ID,
			ProjectID:      fixture.project.ID,
		}
		ctx := projectKnowledgeOperationContext(
			t,
			mismatchedScope,
			models.HumanActor(fixture.human.ID),
		)
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			ctx,
			mismatchedScope,
			models.HumanActor(fixture.human.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("Team membership cannot replace Project membership", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		actor := models.HumanActor(fixture.outsider.ID)
		ctx := projectKnowledgeOperationContext(t, fixture.project.Scope(), actor)
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			ctx,
			fixture.project.Scope(),
			actor,
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("inactive human membership", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		if err := fixture.db.Model(&models.ProjectMembership{}).
			Where("project_id = ? AND user_id = ?", fixture.project.ID, fixture.human.ID).
			Update("is_active", false).Error; err != nil {
			t.Fatal(err)
		}
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			fixture.humanContext,
			fixture.project.Scope(),
			models.HumanActor(fixture.human.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("expired or inactive principal grant", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
			Where(
				"project_id = ? AND service_principal_id = ?",
				fixture.project.ID,
				fixture.principal.ID,
			).
			Update("expires_at", fixture.now).Error; err != nil {
			t.Fatal(err)
		}
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			fixture.principalCtx,
			fixture.project.Scope(),
			models.ServicePrincipalActor(fixture.principal.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("expired error = %v", err)
		}
		if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
			Where(
				"project_id = ? AND service_principal_id = ?",
				fixture.project.ID,
				fixture.principal.ID,
			).
			Updates(map[string]any{
				"expires_at": nil,
				"is_active":  false,
			}).Error; err != nil {
			t.Fatal(err)
		}
		_, err = fixture.resolver.ResolveKnowledgeSubjects(
			fixture.principalCtx,
			fixture.project.Scope(),
			models.ServicePrincipalActor(fixture.principal.ID),
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("inactive error = %v", err)
		}
	})

	t.Run("system Actor", func(t *testing.T) {
		fixture := newProjectKnowledgeAccessFixture(t)
		actor := models.SystemActor("knowledge-worker")
		ctx := projectKnowledgeOperationContext(t, fixture.project.Scope(), actor)
		_, err := fixture.resolver.ResolveKnowledgeSubjects(
			ctx,
			fixture.project.Scope(),
			actor,
		)
		if !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
			t.Fatalf("error = %v", err)
		}
	})
}
