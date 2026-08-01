package database

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openCategoryScopeMigrationDB(
	t *testing.T,
	name string,
) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(
			"file:category-scope-"+name+"?mode=memory&cache=shared",
		),
		&gorm.Config{
			DisableForeignKeyConstraintWhenMigrating: true,
		},
	)
	if err != nil {
		t.Fatalf("open category scope database: %v", err)
	}
	if err := db.AutoMigrate(
		&models.SchemaMigrationCheckpoint{},
		&models.Organization{},
		&models.BusinessUnit{},
		&models.Project{},
		&models.Category{},
		&models.CategoryScopeMigrationMapping{},
		&models.Ticket{},
	); err != nil {
		t.Fatalf("migrate category scope fixtures: %v", err)
	}
	return db
}

func createCategoryScopeProjects(
	t *testing.T,
	db *gorm.DB,
	count int,
) []models.Project {
	t.Helper()
	organization := models.Organization{
		PublicID: "00000000-0000-7000-8000-000000000001",
		Slug:     "category-scope",
		Name:     "Category Scope",
		Status:   models.OrganizationStatusActive,
	}
	if err := db.Create(&organization).Error; err != nil {
		t.Fatalf("create category scope organization: %v", err)
	}
	unit := models.BusinessUnit{
		PublicID:       "00000000-0000-7000-8000-000000000002",
		OrganizationID: organization.ID,
		Key:            "CATEGORY",
		Name:           "Category",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := db.Create(&unit).Error; err != nil {
		t.Fatalf("create category scope business unit: %v", err)
	}
	projects := make([]models.Project, 0, count)
	for index := 0; index < count; index++ {
		project := models.Project{
			PublicID: fmt.Sprintf(
				"00000000-0000-7000-8000-%012d",
				index+10,
			),
			OrganizationID: organization.ID,
			BusinessUnitID: unit.ID,
			Key:            models.ProjectKey(fmt.Sprintf("CAT%d", index+1)),
			Name:           fmt.Sprintf("Category %d", index+1),
			Status:         models.ProjectStatusActive,
		}
		if err := db.Create(&project).Error; err != nil {
			t.Fatalf("create category scope project %d: %v", index, err)
		}
		projects = append(projects, project)
	}
	return projects
}

func createCategoryMigrationTicket(
	t *testing.T,
	db *gorm.DB,
	project models.Project,
	number string,
	categoryID uint,
	subcategoryID uint,
) models.Ticket {
	t.Helper()
	category := categoryID
	subcategory := subcategoryID
	publicID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("create Ticket UUIDv7: %v", err)
	}
	ticket := models.Ticket{
		PublicID:             publicID.String(),
		OrganizationID:       project.OrganizationID,
		ProjectID:            project.ID,
		QueueID:              project.ID,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000000101",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000000201",
		TicketNumber:         number,
		Title:                number,
		Description:          "category scope migration",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelUntrusted,
		CreatedByActorType:   models.ActorTypeSystem,
		CreatedByActorID:     "category-scope-test",
		CategoryID:           &category,
		SubcategoryID:        &subcategory,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create category migration Ticket: %v", err)
	}
	return ticket
}

func TestCategoryScopeMigrationClonesTreesAndRewritesTickets(
	t *testing.T,
) {
	db := openCategoryScopeMigrationDB(t, "clone")
	projects := createCategoryScopeProjects(t, db, 2)
	parent := models.Category{
		Name:      "Legacy Parent",
		Slug:      "legacy-parent",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		CreatedBy: 1,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create legacy parent: %v", err)
	}
	child := models.Category{
		Name:      "Legacy Child",
		Slug:      "legacy-child",
		Type:      models.CategoryTypeSupport,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		ParentID:  &parent.ID,
		CreatedBy: 1,
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create legacy child: %v", err)
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
		t.Fatalf("migrate category project scope: %v", err)
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("idempotent category project scope migration: %v", err)
	}
	if err := ValidateCategoryScopeContract(db); err != nil {
		t.Fatalf("validate category scope contract: %v", err)
	}

	var categories []models.Category
	if err := db.Unscoped().
		Order("project_id ASC, parent_id ASC, id ASC").
		Find(&categories).Error; err != nil {
		t.Fatalf("load scoped categories: %v", err)
	}
	if len(categories) != 4 {
		t.Fatalf("scoped category count=%d, want 4", len(categories))
	}
	for index := range tickets {
		var ticket models.Ticket
		if err := db.First(&ticket, tickets[index].ID).Error; err != nil {
			t.Fatalf("load rewritten Ticket: %v", err)
		}
		if ticket.CategoryID == nil || ticket.SubcategoryID == nil {
			t.Fatalf("Ticket %d lost category selection", ticket.ID)
		}
		var category, subcategory models.Category
		if err := db.First(&category, *ticket.CategoryID).Error; err != nil {
			t.Fatalf("load rewritten category: %v", err)
		}
		if err := db.First(&subcategory, *ticket.SubcategoryID).Error; err != nil {
			t.Fatalf("load rewritten subcategory: %v", err)
		}
		if category.OrganizationID != ticket.OrganizationID ||
			category.ProjectID != ticket.ProjectID ||
			subcategory.OrganizationID != ticket.OrganizationID ||
			subcategory.ProjectID != ticket.ProjectID ||
			subcategory.ParentID == nil ||
			*subcategory.ParentID != category.ID ||
			category.Level != 0 ||
			category.Path != fmt.Sprintf("/%d", category.ID) ||
			category.TicketCount != 1 ||
			category.ActiveTicketCount != 1 ||
			category.ChildrenCount != 1 ||
			subcategory.Level != 1 ||
			subcategory.Path != fmt.Sprintf(
				"/%d/%d",
				category.ID,
				subcategory.ID,
			) ||
			subcategory.TicketCount != 0 ||
			subcategory.ActiveTicketCount != 0 ||
			subcategory.ChildrenCount != 0 {
			t.Fatalf(
				"Ticket %d has invalid scoped hierarchy: ticket=%+v category=%+v subcategory=%+v",
				ticket.ID,
				ticket,
				category,
				subcategory,
			)
		}
	}
}

func TestPrepareCategoryScopeColumnsStagesOnlyZeroSentinels(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:category-scope-prepare?mode=memory&cache=shared",
		),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open legacy category database: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatalf("create legacy categories: %v", err)
	}
	if err := db.Exec(`
		INSERT INTO categories (name, slug)
		VALUES ('Legacy', 'legacy')
	`).Error; err != nil {
		t.Fatalf("seed legacy category: %v", err)
	}
	if err := PrepareCategoryScopeColumns(db); err != nil {
		t.Fatalf("prepare legacy category scope columns: %v", err)
	}
	var row struct {
		OrganizationID uint `gorm:"column:organization_id"`
		ProjectID      uint `gorm:"column:project_id"`
	}
	if err := db.Table("categories").First(&row).Error; err != nil {
		t.Fatalf("load prepared legacy category: %v", err)
	}
	if row.OrganizationID != 0 || row.ProjectID != 0 {
		t.Fatalf(
			"preparation inferred category scope %d/%d",
			row.OrganizationID,
			row.ProjectID,
		)
	}
}

func TestCategoryScopeMigrationFailsClosedForAmbiguousUnreferencedTree(
	t *testing.T,
) {
	db := openCategoryScopeMigrationDB(t, "ambiguous")
	_ = createCategoryScopeProjects(t, db, 2)
	category := models.Category{
		Name:      "Unreferenced",
		Slug:      "unreferenced",
		Type:      models.CategoryTypeGeneral,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		CreatedBy: 1,
	}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("create ambiguous category: %v", err)
	}

	err := MigrateCategoryProjectScope(db)
	if err == nil ||
		!strings.Contains(err.Error(), "category_scope_migration_mappings") {
		t.Fatalf("ambiguous category migration error=%v", err)
	}
	var after models.Category
	if err := db.First(&after, category.ID).Error; err != nil {
		t.Fatalf("load rolled-back category: %v", err)
	}
	if after.OrganizationID != 0 || after.ProjectID != 0 {
		t.Fatalf("ambiguous migration retained inferred scope: %+v", after)
	}
	var checkpoints int64
	if err := db.Model(&models.SchemaMigrationCheckpoint{}).
		Where("key = ?", categoryScopeCutoverCheckpointKey).
		Count(&checkpoints).Error; err != nil {
		t.Fatalf("count category checkpoints: %v", err)
	}
	if checkpoints != 0 {
		t.Fatalf("ambiguous migration retained %d checkpoint(s)", checkpoints)
	}
}

func TestCategoryScopeMigrationUsesExplicitOperatorMapping(
	t *testing.T,
) {
	db := openCategoryScopeMigrationDB(t, "operator-mapping")
	projects := createCategoryScopeProjects(t, db, 2)
	category := models.Category{
		Name:      "Mapped",
		Slug:      "mapped",
		Type:      models.CategoryTypeGeneral,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		CreatedBy: 1,
	}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("create mapped category: %v", err)
	}
	if err := db.Create(&models.CategoryScopeMigrationMapping{
		CategoryID:     category.ID,
		OrganizationID: projects[1].OrganizationID,
		ProjectID:      projects[1].ID,
	}).Error; err != nil {
		t.Fatalf("create category operator mapping: %v", err)
	}
	if err := MigrateCategoryProjectScope(db); err != nil {
		t.Fatalf("migrate operator-mapped category: %v", err)
	}
	var after models.Category
	if err := db.First(&after, category.ID).Error; err != nil {
		t.Fatalf("load operator-mapped category: %v", err)
	}
	if after.OrganizationID != projects[1].OrganizationID ||
		after.ProjectID != projects[1].ID {
		t.Fatalf("operator-mapped category scope=%+v", after)
	}
}

func TestCategoryScopeMigrationRollsBackCheckpointFailure(
	t *testing.T,
) {
	db := openCategoryScopeMigrationDB(t, "rollback")
	project := createCategoryScopeProjects(t, db, 1)[0]
	category := models.Category{
		Name:      "Rollback",
		Slug:      "rollback",
		Type:      models.CategoryTypeGeneral,
		Status:    models.CategoryStatusActive,
		IsPublic:  true,
		CreatedBy: 1,
	}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("create rollback category: %v", err)
	}
	injected := errors.New("injected category checkpoint failure")
	const callbackName = "test:fail-category-scope-checkpoint"
	if err := db.Callback().Create().Before("gorm:create").
		Register(callbackName, func(tx *gorm.DB) {
			checkpoint, ok := tx.Statement.Dest.(*models.SchemaMigrationCheckpoint)
			if ok && checkpoint != nil &&
				checkpoint.Key == categoryScopeCutoverCheckpointKey {
				tx.AddError(injected)
			}
		}); err != nil {
		t.Fatalf("register category checkpoint failure: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Create().Remove(callbackName)
	})

	err := MigrateCategoryProjectScope(db)
	if !errors.Is(err, injected) {
		t.Fatalf("checkpoint failure error=%v, want %v", err, injected)
	}
	var after models.Category
	if err := db.First(&after, category.ID).Error; err != nil {
		t.Fatalf("load rolled-back category: %v", err)
	}
	if after.OrganizationID != 0 || after.ProjectID != 0 {
		t.Fatalf(
			"checkpoint failure retained category scope %d/%d, project was %d/%d",
			after.OrganizationID,
			after.ProjectID,
			project.OrganizationID,
			project.ID,
		)
	}
}
