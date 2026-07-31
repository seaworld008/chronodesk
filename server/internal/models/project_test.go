package models

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestProjectKeyValidation(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{name: "single letter", key: "A", valid: true},
		{name: "canonical", key: "OPS", valid: true},
		{name: "digits underscore hyphen", key: "OPS_2026-1", valid: true},
		{name: "empty", key: "", valid: false},
		{name: "lowercase", key: "ops", valid: false},
		{name: "leading digit", key: "1OPS", valid: false},
		{name: "dot", key: "OPS.PROD", valid: false},
		{name: "whitespace", key: " OPS", valid: false},
		{name: "unicode", key: "项目", valid: false},
		{name: "too long", key: "P" + strings.Repeat("1", ProjectKeyMaxLength), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := ProjectKey(test.key)
			if got := key.IsValid(); got != test.valid {
				t.Fatalf("ProjectKey(%q).IsValid() = %t, want %t", test.key, got, test.valid)
			}
			err := ValidateProjectKey(test.key)
			if test.valid && err != nil {
				t.Fatalf("ValidateProjectKey(%q): %v", test.key, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ValidateProjectKey(%q) unexpectedly succeeded", test.key)
			}
		})
	}
}

func TestQueueKeyValidation(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{name: "word", key: "default", valid: true},
		{name: "leading digit", key: "1st-line", valid: true},
		{name: "routing punctuation", key: "a.b_c-1", valid: true},
		{name: "empty", key: "", valid: false},
		{name: "uppercase", key: "Default", valid: false},
		{name: "leading hyphen", key: "-default", valid: false},
		{name: "colon", key: "ops:critical", valid: false},
		{name: "slash", key: "ops/critical", valid: false},
		{name: "whitespace", key: "ops critical", valid: false},
		{name: "too long", key: "q" + strings.Repeat("1", QueueKeyMaxLength), valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := QueueKey(test.key)
			if got := key.IsValid(); got != test.valid {
				t.Fatalf("QueueKey(%q).IsValid() = %t, want %t", test.key, got, test.valid)
			}
			err := ValidateQueueKey(test.key)
			if test.valid && err != nil {
				t.Fatalf("ValidateQueueKey(%q): %v", test.key, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ValidateQueueKey(%q) unexpectedly succeeded", test.key)
			}
		})
	}
}

func TestTeamKeyValidation(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{name: "word", key: "platform", valid: true},
		{name: "routing punctuation", key: "level-2.ops_team", valid: true},
		{name: "empty", key: "", valid: false},
		{name: "uppercase", key: "Platform", valid: false},
		{name: "leading punctuation", key: ".platform", valid: false},
		{name: "slash", key: "ops/platform", valid: false},
		{name: "whitespace", key: "ops platform", valid: false},
		{name: "too long", key: "t" + strings.Repeat("1", TeamKeyMaxLength), valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := TeamKey(test.key)
			if got := key.IsValid(); got != test.valid {
				t.Fatalf("TeamKey(%q).IsValid() = %t, want %t", test.key, got, test.valid)
			}
			err := ValidateTeamKey(test.key)
			if test.valid && err != nil {
				t.Fatalf("ValidateTeamKey(%q): %v", test.key, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("ValidateTeamKey(%q) unexpectedly succeeded", test.key)
			}
		})
	}
}

func TestProjectEntitiesGenerateUUIDv7PublicIDs(t *testing.T) {
	db := openProjectModelDB(t, "public-ids")
	organization := createProjectTestOrganization(t, db, "public-ids")
	unit := createProjectTestBusinessUnit(t, db, organization.ID, "OPS")
	project := createProjectTestProject(t, db, organization.ID, unit.ID, "OPS")
	if project.TicketSequence != 0 {
		t.Fatalf("new project ticket sequence = %d, want 0", project.TicketSequence)
	}
	if !db.Migrator().HasColumn(&Project{}, "ticket_sequence") {
		t.Fatal("projects.ticket_sequence column was not migrated")
	}
	if err := db.Model(&project).UpdateColumn(
		"ticket_sequence",
		gorm.Expr("ticket_sequence + ?", 1),
	).Error; err != nil {
		t.Fatalf("atomically increment ticket sequence: %v", err)
	}
	if err := db.First(&project, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if project.TicketSequence != 1 {
		t.Fatalf("incremented ticket sequence = %d, want 1", project.TicketSequence)
	}
	team := Team{ProjectID: project.ID, Key: "platform", Name: "Platform"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	queue := Queue{
		ProjectID: project.ID,
		TeamID:    &team.ID,
		Key:       "default",
		Name:      "Default",
	}
	if err := db.Create(&queue).Error; err != nil {
		t.Fatal(err)
	}

	publicIDs := map[string]string{
		"organization":  organization.PublicID,
		"business unit": unit.PublicID,
		"project":       project.PublicID,
		"team":          team.PublicID,
		"queue":         queue.PublicID,
	}
	seen := make(map[string]struct{}, len(publicIDs))
	for entity, publicID := range publicIDs {
		parsed, err := uuid.Parse(publicID)
		if err != nil {
			t.Fatalf("%s public id %q is invalid: %v", entity, publicID, err)
		}
		if parsed.Version() != 7 {
			t.Fatalf("%s public id version = %d, want 7", entity, parsed.Version())
		}
		if _, duplicate := seen[publicID]; duplicate {
			t.Fatalf("duplicate public id %q", publicID)
		}
		seen[publicID] = struct{}{}
	}

	invalid := Organization{
		PublicID: "not-a-uuid",
		Slug:     "invalid-public-id",
		Name:     "Invalid",
	}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("invalid supplied public id was accepted")
	}
	duplicate := Organization{
		PublicID: organization.PublicID,
		Slug:     "duplicate-public-id",
		Name:     "Duplicate",
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate organization public id was accepted")
	}
}

func TestProjectAndQueueHooksRejectInvalidKeys(t *testing.T) {
	db := openProjectModelDB(t, "key-hooks")
	organization := createProjectTestOrganization(t, db, "key-hooks")
	unit := createProjectTestBusinessUnit(t, db, organization.ID, "OPS")

	invalidProject := Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            "lowercase",
		Name:           "Invalid",
	}
	if err := db.Create(&invalidProject).Error; err == nil {
		t.Fatal("invalid project key was accepted")
	}

	project := createProjectTestProject(t, db, organization.ID, unit.ID, "OPS")
	invalidQueue := Queue{
		ProjectID: project.ID,
		Key:       "Uppercase",
		Name:      "Invalid",
	}
	if err := db.Create(&invalidQueue).Error; err == nil {
		t.Fatal("invalid queue key was accepted")
	}

	project.Key = "still-lowercase"
	if err := db.Save(&project).Error; err == nil {
		t.Fatal("invalid project key update was accepted")
	}
	project.Key = "OPS"
	if err := db.Model(&project).Update("key", "map-lowercase").Error; err == nil {
		t.Fatal("invalid project key column update was accepted")
	}
	queue := Queue{ProjectID: project.ID, Key: "default", Name: "Default"}
	if err := db.Create(&queue).Error; err != nil {
		t.Fatal(err)
	}
	queue.Key = "Not-Lowercase"
	if err := db.Save(&queue).Error; err == nil {
		t.Fatal("invalid queue key update was accepted")
	}
	queue.Key = "default"
	if err := db.Model(&queue).Update("key", "Map-Uppercase").Error; err == nil {
		t.Fatal("invalid queue key column update was accepted")
	}
	invalidTeam := Team{ProjectID: project.ID, Key: "Not-Lowercase", Name: "Invalid"}
	if err := db.Create(&invalidTeam).Error; err == nil {
		t.Fatal("invalid team key was accepted")
	}
	team := Team{ProjectID: project.ID, Key: "platform", Name: "Platform"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&team).Update("key", "Map-Uppercase").Error; err == nil {
		t.Fatal("invalid team key column update was accepted")
	}
}

func TestProjectSchemaEnforcesScopedCompositeUniqueness(t *testing.T) {
	db := openProjectModelDB(t, "composite-uniqueness")
	organizationA := createProjectTestOrganization(t, db, "org-a")
	organizationB := createProjectTestOrganization(t, db, "org-b")
	unitA := createProjectTestBusinessUnit(t, db, organizationA.ID, "OPS")
	unitB := createProjectTestBusinessUnit(t, db, organizationB.ID, "OPS")

	if err := db.Create(&BusinessUnit{
		OrganizationID: organizationA.ID,
		Key:            unitA.Key,
		Name:           "Duplicate",
	}).Error; err == nil {
		t.Fatal("duplicate business-unit key in one organization was accepted")
	}

	projectA := createProjectTestProject(t, db, organizationA.ID, unitA.ID, "OPS")
	projectB := createProjectTestProject(t, db, organizationB.ID, unitB.ID, "OPS")
	projectC := createProjectTestProject(t, db, organizationA.ID, unitA.ID, "OTHER")
	if err := db.Create(&Project{
		OrganizationID: organizationA.ID,
		BusinessUnitID: unitA.ID,
		Key:            projectA.Key,
		Name:           "Duplicate",
	}).Error; err == nil {
		t.Fatal("duplicate project key in one organization was accepted")
	}

	queueA := Queue{ProjectID: projectA.ID, Key: "default", Name: "Default"}
	queueC := Queue{ProjectID: projectC.ID, Key: "default", Name: "Default"}
	if err := db.Create(&queueA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&queueC).Error; err != nil {
		t.Fatalf("same queue key in another project was rejected: %v", err)
	}
	if err := db.Create(&Queue{
		ProjectID: projectA.ID,
		Key:       queueA.Key,
		Name:      "Duplicate",
	}).Error; err == nil {
		t.Fatal("duplicate queue key in one project was accepted")
	}

	user := User{
		Username:     "project-member",
		Email:        "project-member@example.test",
		PasswordHash: "hash",
		PlatformRole: PlatformRoleMember,
		Status:       UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	membership := ProjectMembership{
		ProjectID: projectA.ID,
		UserID:    user.ID,
		Role:      ProjectRoleAgent,
		IsActive:  true,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProjectMembership{
		ProjectID: projectA.ID,
		UserID:    user.ID,
		Role:      ProjectRoleObserver,
		IsActive:  true,
	}).Error; err == nil {
		t.Fatal("duplicate project membership was accepted")
	}
	if err := db.Create(&ProjectMembership{
		ProjectID: projectB.ID,
		UserID:    user.ID,
		Role:      ProjectRoleRequester,
		IsActive:  true,
	}).Error; err != nil {
		t.Fatalf("same user membership in another project was rejected: %v", err)
	}

	teamA := Team{ProjectID: projectA.ID, Key: "platform", Name: "Platform"}
	teamC := Team{ProjectID: projectC.ID, Key: "platform", Name: "Platform"}
	if err := db.Create(&teamA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&teamC).Error; err != nil {
		t.Fatalf("same team key in another project was rejected: %v", err)
	}
	if err := db.Create(&Team{
		ProjectID: projectA.ID,
		Key:       teamA.Key,
		Name:      "Duplicate",
	}).Error; err == nil {
		t.Fatal("duplicate team key in one project was accepted")
	}
	if err := db.Create(&TeamMembership{
		TeamID:   teamA.ID,
		UserID:   user.ID,
		Role:     TeamRoleMember,
		IsActive: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TeamMembership{
		TeamID:   teamA.ID,
		UserID:   user.ID,
		Role:     TeamRoleLead,
		IsActive: true,
	}).Error; err == nil {
		t.Fatal("duplicate team membership was accepted")
	}
	if err := db.Create(&TeamMembership{
		TeamID:   teamC.ID,
		UserID:   user.ID,
		Role:     TeamRoleMember,
		IsActive: true,
	}).Error; err != nil {
		t.Fatalf("same user membership in another team was rejected: %v", err)
	}

	principal := ServicePrincipal{
		ID:     "00000000-0000-4000-8000-000000000001",
		Name:   "project-principal",
		Status: ServicePrincipalStatusActive,
		Scopes: datatypes.JSON(`["tickets:read"]`),
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	grant := ProjectPrincipalGrant{
		ProjectID:          projectA.ID,
		ServicePrincipalID: principal.ID,
		Role:               ProjectRoleAgent,
		Scopes:             datatypes.JSON(`["tickets:read"]`),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ProjectPrincipalGrant{
		ProjectID:          projectA.ID,
		ServicePrincipalID: principal.ID,
		Role:               ProjectRoleObserver,
		Scopes:             datatypes.JSON(`[]`),
		IsActive:           true,
	}).Error; err == nil {
		t.Fatal("duplicate project principal grant was accepted")
	}
	if err := db.Create(&ProjectPrincipalGrant{
		ProjectID:          projectB.ID,
		ServicePrincipalID: principal.ID,
		Role:               ProjectRoleObserver,
		Scopes:             datatypes.JSON(`["tickets:read"]`),
		IsActive:           true,
	}).Error; err != nil {
		t.Fatalf("same principal grant in another project was rejected: %v", err)
	}
}

func TestProjectScopeValidation(t *testing.T) {
	tests := []struct {
		name  string
		scope ProjectScope
		valid bool
	}{
		{name: "complete", scope: ProjectScope{OrganizationID: 1, ProjectID: 2}, valid: true},
		{name: "missing organization", scope: ProjectScope{ProjectID: 2}, valid: false},
		{name: "missing project", scope: ProjectScope{OrganizationID: 1}, valid: false},
		{name: "empty", scope: ProjectScope{}, valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.scope.Validate()
			if test.valid && err != nil {
				t.Fatalf("ProjectScope.Validate(): %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("ProjectScope.Validate() unexpectedly succeeded")
			}
		})
	}
	if !(ProjectScope{}).IsZero() {
		t.Fatal("empty scope was not recognized")
	}
}

func TestProjectRolesUseStableClosedValues(t *testing.T) {
	valid := []ProjectRole{
		ProjectRoleAdmin,
		ProjectRoleManager,
		ProjectRoleAgent,
		ProjectRoleRequester,
		ProjectRoleObserver,
	}
	want := []string{
		"project_admin",
		"manager",
		"agent",
		"requester",
		"observer",
	}
	for index, role := range valid {
		if string(role) != want[index] || !role.IsValid() {
			t.Errorf("project role %d = %q valid=%t, want %q", index, role, role.IsValid(), want[index])
		}
	}
	for _, role := range []ProjectRole{"", "owner", "admin", "member", "viewer"} {
		if role.IsValid() {
			t.Errorf("legacy or empty project role %q was accepted", role)
		}
	}
}

func openProjectModelDB(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf(
			"file:%s-%s?mode=memory&cache=shared",
			strings.ReplaceAll(t.Name(), "/", "_"),
			suffix,
		)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&User{},
		&ServicePrincipal{},
		&Organization{},
		&BusinessUnit{},
		&Project{},
		&ProjectMembership{},
		&Team{},
		&TeamMembership{},
		&Queue{},
		&ProjectPrincipalGrant{},
	); err != nil {
		t.Fatalf("migrate project model schema: %v", err)
	}
	return db
}

func createProjectTestOrganization(
	t *testing.T,
	db *gorm.DB,
	slug string,
) Organization {
	t.Helper()
	organization := Organization{Slug: slug, Name: "Organization " + slug}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatalf("create organization %s: %v", slug, err)
	}
	return organization
}

func createProjectTestBusinessUnit(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	key string,
) BusinessUnit {
	t.Helper()
	unit := BusinessUnit{
		OrganizationID: organizationID,
		Key:            key,
		Name:           "Business Unit " + key,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatalf("create business unit %s: %v", key, err)
	}
	return unit
}

func createProjectTestProject(
	t *testing.T,
	db *gorm.DB,
	organizationID uint,
	businessUnitID uint,
	key ProjectKey,
) Project {
	t.Helper()
	project := Project{
		OrganizationID: organizationID,
		BusinessUnitID: businessUnitID,
		Key:            key,
		Name:           "Project " + string(key),
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("create project %s: %v", key, err)
	}
	return project
}
