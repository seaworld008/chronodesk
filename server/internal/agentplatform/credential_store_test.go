package agentplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type credentialStoreFixture struct {
	db         *gorm.DB
	store      *CredentialStore
	native     *services.AgentNativeService
	principal  *models.ServicePrincipal
	credential *services.IssuedAgentCredential
	project    models.Project
}

func newCredentialStoreFixture(t *testing.T) *credentialStoreFixture {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.ServicePrincipal{},
		&models.AgentCredential{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.ProjectPrincipalGrant{},
	); err != nil {
		t.Fatalf("migrate credential store schema: %v", err)
	}

	native := services.NewAgentNativeService(
		db,
		services.AgentNativeOptions{CredentialPepper: []byte("credential-store-test-pepper")},
	)
	principal, err := native.CreateServicePrincipal(
		context.Background(),
		services.CreateServicePrincipalInput{
			Name: "project-bound-agent",
			Scopes: []string{
				models.ScopeTicketsRead,
				models.ScopeTicketsUpdate,
				models.ScopeTasksManage,
			},
			RateLimitPerMinute: 60,
			ConcurrentLimit:    4,
		},
	)
	if err != nil {
		t.Fatalf("create service principal: %v", err)
	}
	credential, err := native.IssueCredential(
		context.Background(),
		principal.ID,
		"project-bound",
		time.Hour,
	)
	if err != nil {
		t.Fatalf("issue service principal credential: %v", err)
	}

	organization := models.Organization{
		Slug:   "credential-store",
		Name:   "Credential Store",
		Status: models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatalf("create organization: %v", err)
	}
	unit := models.BusinessUnit{
		OrganizationID: organization.ID,
		Key:            "OPS",
		Name:           "Operations",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatalf("create business unit: %v", err)
	}
	project := models.Project{
		OrganizationID: organization.ID,
		BusinessUnitID: unit.ID,
		Key:            models.ProjectKey("ALPHA"),
		Name:           "Alpha",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&project).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	scopes, err := json.Marshal([]string{
		models.ScopeTicketsRead,
		models.ScopeTasksManage,
		models.ScopeCommentsWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant := models.ProjectPrincipalGrant{
		ProjectID:          project.ID,
		ServicePrincipalID: principal.ID,
		Role:               models.ProjectRoleAgent,
		Scopes:             datatypes.JSON(scopes),
		IsActive:           true,
	}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatalf("create project principal grant: %v", err)
	}
	projects, err := services.NewProjectService(db)
	if err != nil {
		t.Fatal(err)
	}
	return &credentialStoreFixture{
		db:         db,
		store:      NewCredentialStore(native, projects),
		native:     native,
		principal:  principal,
		credential: credential,
		project:    project,
	}
}

func TestCredentialStoreAuthenticatesAgainstProjectGrantAndIntersectsScopes(t *testing.T) {
	fixture := newCredentialStoreFixture(t)
	principal, err := fixture.store.AuthenticateClient(
		context.Background(),
		fixture.principal.ID,
		fixture.credential.Token,
		"ALPHA",
	)
	if err != nil {
		t.Fatalf("AuthenticateClient(): %v", err)
	}
	wantScopes := []string{models.ScopeTicketsRead, models.ScopeTasksManage}
	if fmt.Sprint(principal.Scopes) != fmt.Sprint(wantScopes) {
		t.Fatalf(
			"project-bound scopes = %v, want global/project intersection %v",
			principal.Scopes,
			wantScopes,
		)
	}
}

func TestCredentialStoreRejectsMissingInactiveExpiredAndArchivedProjectGrants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *credentialStoreFixture)
		key    string
	}{
		{name: "missing grant", key: "MISSING"},
		{
			name: "inactive grant",
			key:  "ALPHA",
			mutate: func(t *testing.T, fixture *credentialStoreFixture) {
				if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
					Where(
						"project_id = ? AND service_principal_id = ?",
						fixture.project.ID,
						fixture.principal.ID,
					).
					Update("is_active", false).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "expired grant",
			key:  "ALPHA",
			mutate: func(t *testing.T, fixture *credentialStoreFixture) {
				expiredAt := time.Now().UTC().Add(-time.Minute)
				if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
					Where(
						"project_id = ? AND service_principal_id = ?",
						fixture.project.ID,
						fixture.principal.ID,
					).
					Update("expires_at", expiredAt).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "archived project",
			key:  "ALPHA",
			mutate: func(t *testing.T, fixture *credentialStoreFixture) {
				if err := fixture.db.Model(&models.Project{}).
					Where("id = ?", fixture.project.ID).
					Update("status", models.ProjectStatusArchived).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCredentialStoreFixture(t)
			if test.mutate != nil {
				test.mutate(t, fixture)
			}
			_, err := fixture.store.AuthenticateClient(
				context.Background(),
				fixture.principal.ID,
				fixture.credential.Token,
				test.key,
			)
			if err == nil {
				t.Fatal("AuthenticateClient() accepted an unusable project grant")
			}
		})
	}
}

func TestCredentialStoreRevalidatesProjectAndTokenScopes(t *testing.T) {
	fixture := newCredentialStoreFixture(t)
	ctx := context.Background()
	if err := fixture.store.ValidateAccessContext(
		ctx,
		fixture.principal.ID,
		fixture.credential.Credential.ID,
		"ALPHA",
		[]string{models.ScopeTicketsRead},
	); err != nil {
		t.Fatalf("ValidateAccessContext(valid): %v", err)
	}
	if err := fixture.store.ValidateAccessContext(
		ctx,
		fixture.principal.ID,
		fixture.credential.Credential.ID,
		"ALPHA",
		[]string{models.ScopeTicketsUpdate},
	); !errors.Is(err, services.ErrProjectAccessDenied) {
		t.Fatalf(
			"ValidateAccessContext(ungranted scope) error = %v, want %v",
			err,
			services.ErrProjectAccessDenied,
		)
	}

	if err := fixture.db.Model(&models.ProjectPrincipalGrant{}).
		Where(
			"project_id = ? AND service_principal_id = ?",
			fixture.project.ID,
			fixture.principal.ID,
		).
		Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ValidateAccessContext(
		ctx,
		fixture.principal.ID,
		fixture.credential.Credential.ID,
		"ALPHA",
		[]string{models.ScopeTicketsRead},
	); !errors.Is(err, services.ErrProjectAccessDenied) {
		t.Fatalf(
			"ValidateAccessContext(revoked grant) error = %v, want %v",
			err,
			services.ErrProjectAccessDenied,
		)
	}
}
