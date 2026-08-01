package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/services"
	"gorm.io/gorm"
)

func openPostgresCategoryScopeFixture(
	t *testing.T,
	name string,
) *gorm.DB {
	t.Helper()
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"category_"+name,
	)
	if err := db.AutoMigrate(
		&models.SchemaMigrationCheckpoint{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Category{},
		&models.CategoryScopeMigrationMapping{},
		&models.Ticket{},
	); err != nil {
		t.Fatalf("migrate PostgreSQL category fixture: %v", err)
	}
	creator := models.User{
		Username:     "category-migration-owner",
		Email:        "category-migration-owner@example.com",
		PasswordHash: "not-a-login-fixture",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&creator).Error; err != nil {
		t.Fatalf("create PostgreSQL category fixture owner: %v", err)
	}
	if creator.ID != 1 {
		t.Fatalf("category fixture owner ID=%d, want 1", creator.ID)
	}
	return db
}

func TestPostgresCategoryScopeCutoverClonesAndResumes(
	t *testing.T,
) {
	db := openPostgresCategoryScopeFixture(t, "clone")
	projects := createCategoryScopeProjects(t, db, 2)
	parent := models.Category{
		Name:      "PostgreSQL Parent",
		Slug:      "postgres-parent",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		CreatedBy: 1,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create PostgreSQL legacy parent: %v", err)
	}
	child := models.Category{
		Name:      "PostgreSQL Child",
		Slug:      "postgres-child",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		ParentID:  &parent.ID,
		CreatedBy: 1,
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create PostgreSQL legacy child: %v", err)
	}
	tickets := []models.Ticket{
		createCategoryMigrationTicket(
			t,
			db,
			projects[0],
			"CAT1-1",
			parent.ID,
			child.ID,
		),
		createCategoryMigrationTicket(
			t,
			db,
			projects[1],
			"CAT2-1",
			parent.ID,
			child.ID,
		),
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("migrate PostgreSQL category project scope: %v", err)
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("idempotent PostgreSQL category migration: %v", err)
	}
	if err := ValidateCategoryScopeContract(db); err != nil {
		t.Fatalf("validate PostgreSQL category contract: %v", err)
	}

	var secondTicket models.Ticket
	if err := db.First(&secondTicket, tickets[1].ID).Error; err != nil {
		t.Fatalf("load rewritten PostgreSQL Ticket: %v", err)
	}
	if secondTicket.CategoryID == nil ||
		*secondTicket.CategoryID == parent.ID ||
		secondTicket.SubcategoryID == nil ||
		*secondTicket.SubcategoryID == child.ID {
		t.Fatalf(
			"second-project Ticket was not cloned and rewritten: %+v",
			secondTicket,
		)
	}
	if err := db.Exec(
		"DROP INDEX " + quoteStaticProjectRLSIdentifier(categoryScopeListIndex),
	).Error; err != nil {
		t.Fatalf("drop PostgreSQL category resume index: %v", err)
	}
	if err := db.Exec(
		"CREATE INDEX " +
			quoteStaticProjectRLSIdentifier(categoryScopeListIndex) +
			" ON categories(project_id, organization_id, id)",
	).Error; err != nil {
		t.Fatalf("create drifted PostgreSQL category index: %v", err)
	}
	if err := ValidateCategoryScopeContract(db); err == nil ||
		!strings.Contains(err.Error(), categoryScopeListIndex) {
		t.Fatalf("runtime gate accepted drifted category index: %v", err)
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("resume PostgreSQL category contract: %v", err)
	}
	if err := ValidateCategoryScopeContract(db); err != nil {
		t.Fatalf("validate resumed PostgreSQL category contract: %v", err)
	}

	if err := db.Model(&models.Ticket{}).
		Where("id = ?", tickets[0].ID).
		Update("category_id", secondTicket.CategoryID).Error; err == nil {
		t.Fatal("PostgreSQL composite FK accepted a cross-project category")
	}
	if err := db.Model(&models.Category{}).
		Where("id = ?", child.ID).
		Update("parent_id", secondTicket.CategoryID).Error; err == nil {
		t.Fatal("PostgreSQL composite FK accepted a cross-project parent")
	}
}

func TestPostgresCategoryScopeCutoverAmbiguityAndRollback(
	t *testing.T,
) {
	t.Run("ambiguous", func(t *testing.T) {
		db := openPostgresCategoryScopeFixture(t, "ambiguous")
		_ = createCategoryScopeProjects(t, db, 2)
		category := models.Category{
			Name:      "PostgreSQL Ambiguous",
			Slug:      "postgres-ambiguous",
			Type:      models.CategoryTypeGeneral,
			Status:    models.CategoryStatusActive,
			IsPublic:  true,
			CreatedBy: 1,
		}
		if err := db.Create(&category).Error; err != nil {
			t.Fatalf("create PostgreSQL ambiguous category: %v", err)
		}
		err := MigrateCategoryProjectScope(db)
		if err == nil ||
			!strings.Contains(
				err.Error(),
				"category_scope_migration_mappings",
			) {
			t.Fatalf("PostgreSQL ambiguity error=%v", err)
		}
	})

	t.Run("checkpoint rollback", func(t *testing.T) {
		db := openPostgresCategoryScopeFixture(t, "rollback")
		_ = createCategoryScopeProjects(t, db, 1)
		category := models.Category{
			Name:      "PostgreSQL Rollback",
			Slug:      "postgres-rollback",
			Type:      models.CategoryTypeGeneral,
			Status:    models.CategoryStatusActive,
			IsPublic:  true,
			CreatedBy: 1,
		}
		if err := db.Create(&category).Error; err != nil {
			t.Fatalf("create PostgreSQL rollback category: %v", err)
		}
		injected := errors.New("injected PostgreSQL category checkpoint failure")
		const callbackName = "test:fail-postgres-category-checkpoint"
		if err := db.Callback().Create().Before("gorm:create").
			Register(callbackName, func(tx *gorm.DB) {
				checkpoint, ok :=
					tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
				if ok && checkpoint != nil &&
					checkpoint.Key == categoryScopeCutoverCheckpointKey {
					tx.AddError(injected)
				}
			}); err != nil {
			t.Fatalf("register PostgreSQL checkpoint failure: %v", err)
		}
		t.Cleanup(func() {
			_ = db.Callback().Create().Remove(callbackName)
		})
		err := MigrateCategoryProjectScope(db)
		if !errors.Is(err, injected) {
			t.Fatalf("PostgreSQL checkpoint failure error=%v", err)
		}
		var after models.Category
		if err := db.First(&after, category.ID).Error; err != nil {
			t.Fatalf("load rolled-back PostgreSQL category: %v", err)
		}
		if after.OrganizationID != 0 || after.ProjectID != 0 {
			t.Fatalf(
				"PostgreSQL checkpoint failure retained scope: %+v",
				after,
			)
		}
	})
}

func TestPostgresCategoryScopeRLSRejectsCrossProjectReadAndWrite(
	t *testing.T,
) {
	db, _, _ := openPostgresMembershipReleaseTestDB(
		t,
		"category_rls",
	)
	if err := RunMigrations(
		db,
		services.EnsureProjectScopeMigrationMembership,
	); err != nil {
		t.Fatalf("migrate PostgreSQL category RLS fixture: %v", err)
	}
	var projects []models.Project
	if err := db.Order("id ASC").Find(&projects).Error; err != nil {
		t.Fatalf("load default project: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("default project count=%d, want 1", len(projects))
	}
	creator := models.User{
		Username:     "category-rls",
		Email:        "category-rls@example.com",
		PasswordHash: "not-a-login-fixture",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&creator).Error; err != nil {
		t.Fatalf("create category RLS creator: %v", err)
	}
	second := models.Project{
		OrganizationID: projects[0].OrganizationID,
		BusinessUnitID: projects[0].BusinessUnitID,
		Key:            "CATRLS",
		Name:           "Category RLS",
		Status:         models.ProjectStatusActive,
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second category RLS project: %v", err)
	}
	categories := []models.Category{
		{
			OrganizationID: projects[0].OrganizationID,
			ProjectID:      projects[0].ID,
			Name:           "RLS First",
			Slug:           "rls-first",
			Type:           models.CategoryTypeGeneral,
			Status:         models.CategoryStatusActive,
			CreatedBy:      creator.ID,
		},
		{
			OrganizationID: second.OrganizationID,
			ProjectID:      second.ID,
			Name:           "RLS Second",
			Slug:           "rls-second",
			Type:           models.CategoryTypeGeneral,
			Status:         models.CategoryStatusActive,
			CreatedBy:      creator.ID,
		},
	}
	if err := db.Create(&categories).Error; err != nil {
		t.Fatalf("create category RLS rows: %v", err)
	}
	if err := EnableProjectRLS(db); err != nil {
		t.Fatalf("enable category FORCE RLS: %v", err)
	}

	firstScope := projects[0].Scope()
	if err := WithProjectScopeTransaction(
		context.Background(),
		db,
		firstScope,
		func(tx *gorm.DB) error {
			var visible []models.Category
			if err := tx.Order("id ASC").Find(&visible).Error; err != nil {
				return err
			}
			if len(visible) != 1 || visible[0].ID != categories[0].ID {
				t.Fatalf(
					"project-one category visibility=%+v",
					visible,
				)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("run category project scope transaction: %v", err)
	}
	crossProject := models.Category{
		OrganizationID: second.OrganizationID,
		ProjectID:      second.ID,
		Name:           "Blocked",
		Slug:           "blocked",
		Type:           models.CategoryTypeGeneral,
		Status:         models.CategoryStatusActive,
		CreatedBy:      creator.ID,
	}
	err := WithProjectScopeTransaction(
		context.Background(),
		db,
		firstScope,
		func(tx *gorm.DB) error {
			return tx.Create(&crossProject).Error
		},
	)
	if err == nil {
		t.Fatal("FORCE RLS accepted a cross-project category write")
	}
	if err := db.Exec(
		"DROP INDEX " +
			quoteStaticProjectRLSIdentifier(categoryScopeListIndex),
	).Error; err != nil {
		t.Fatalf("drop post-FORCE category index: %v", err)
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("repair category contract after FORCE RLS: %v", err)
	}
	if err := ValidateCategoryScopeContract(db); err != nil {
		t.Fatalf("validate post-FORCE category repair: %v", err)
	}
}
