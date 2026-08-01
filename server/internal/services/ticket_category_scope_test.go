package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTicketCategorySelectionRequiresTrustedProjectHierarchy(
	t *testing.T,
) {
	db, err := gorm.Open(
		sqlite.Open(
			"file:ticket-category-scope?mode=memory&cache=shared",
		),
		&gorm.Config{DisableForeignKeyConstraintWhenMigrating: true},
	)
	if err != nil {
		t.Fatalf("open category validation database: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Category{},
		&models.Ticket{},
	); err != nil {
		t.Fatalf("migrate category validation schema: %v", err)
	}
	scope := models.ProjectScope{OrganizationID: 1, ProjectID: 10}
	foreignScope := models.ProjectScope{OrganizationID: 1, ProjectID: 11}
	parent := models.Category{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "Primary",
		Slug:           "primary",
		Type:           models.CategoryTypeSupport,
		Status:         models.CategoryStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create primary category: %v", err)
	}
	child := models.Category{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "Child",
		Slug:           "child",
		Type:           models.CategoryTypeSupport,
		Status:         models.CategoryStatusActive,
		ParentID:       &parent.ID,
		CreatedBy:      1,
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child category: %v", err)
	}
	foreign := models.Category{
		OrganizationID: foreignScope.OrganizationID,
		ProjectID:      foreignScope.ProjectID,
		Name:           "Foreign",
		Slug:           "foreign",
		Type:           models.CategoryTypeSupport,
		Status:         models.CategoryStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatalf("create foreign category: %v", err)
	}
	unrelated := models.Category{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "Unrelated",
		Slug:           "unrelated",
		Type:           models.CategoryTypeSupport,
		Status:         models.CategoryStatusActive,
		CreatedBy:      1,
	}
	if err := db.Create(&unrelated).Error; err != nil {
		t.Fatalf("create unrelated category: %v", err)
	}

	if err := validateTicketCategorySelectionTx(
		context.Background(),
		db,
		scope,
		&parent.ID,
		&child.ID,
	); err != nil {
		t.Fatalf("valid category hierarchy rejected: %v", err)
	}
	if err := validateTicketCategorySelectionTx(
		context.Background(),
		db,
		scope,
		&foreign.ID,
		nil,
	); !errors.Is(err, ErrTicketCategoryScope) {
		t.Fatalf("foreign category error=%v", err)
	} else if code := AgentNativeErrorCode(err); code != "invalid_request" {
		t.Fatalf("foreign category error code=%q", code)
	}
	if err := validateTicketCategorySelectionTx(
		context.Background(),
		db,
		scope,
		&parent.ID,
		&foreign.ID,
	); !errors.Is(err, ErrTicketCategoryScope) {
		t.Fatalf("foreign subcategory error=%v", err)
	}
	if err := validateTicketCategorySelectionTx(
		context.Background(),
		db,
		scope,
		&parent.ID,
		&unrelated.ID,
	); !errors.Is(err, ErrInvalidTicketCategorySelection) {
		t.Fatalf("unrelated subcategory error=%v", err)
	}
	if err := validateTicketCategorySelectionTx(
		context.Background(),
		db,
		scope,
		nil,
		&child.ID,
	); !errors.Is(err, ErrInvalidTicketCategorySelection) {
		t.Fatalf("orphan subcategory error=%v", err)
	}

	publicID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	ticket := models.Ticket{
		PublicID:             publicID.String(),
		OrganizationID:       scope.OrganizationID,
		ProjectID:            scope.ProjectID,
		QueueID:              1,
		RequestTypeVersionID: "00000000-0000-7000-8000-000000000101",
		WorkflowVersionID:    "00000000-0000-7000-8000-000000000201",
		TicketNumber:         "CATEGORY-1",
		Title:                "Category validation",
		Description:          "Category validation",
		Type:                 models.TicketTypeRequest,
		Priority:             models.TicketPriorityNormal,
		Status:               models.TicketStatusOpen,
		Source:               models.TicketSourceWeb,
		Version:              1,
		TrustLevel:           models.TicketTrustLevelUntrusted,
		CreatedByActorType:   models.ActorTypeSystem,
		CreatedByActorID:     "category-test",
		CategoryID:           &parent.ID,
		SubcategoryID:        &child.ID,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create category update Ticket: %v", err)
	}
	actor := models.SystemActor("category-test")
	operationContext, err := WithOperationContext(
		context.Background(),
		OperationContext{
			Scope:  scope,
			Actor:  actor,
			Source: SourceProtocolWorker,
		},
	)
	if err != nil {
		t.Fatalf("create category update operation context: %v", err)
	}
	if _, err := NewAgentNativeService(db).UpdateTicketVersion(
		operationContext,
		VersionedTicketUpdateInput{
			TicketID:        ticket.ID,
			ExpectedVersion: ticket.Version,
			Actor:           actor,
			Changes: map[string]any{
				"category_id": foreign.ID,
			},
		},
	); !errors.Is(err, ErrTicketCategoryScope) {
		t.Fatalf("shared native update accepted foreign category: %v", err)
	}
}

func TestTicketCategoryChangeNormalizationRejectsAmbiguousIDs(
	t *testing.T,
) {
	for _, value := range []any{
		0,
		-1,
		1.5,
		1e100,
		float64(1 << 53),
		json.Number("9223372036854775808"),
		"1",
		true,
	} {
		if _, err := normalizeTicketCategoryChange(value); !errors.Is(
			err,
			ErrInvalidTicketCategorySelection,
		) {
			t.Fatalf("category change %T(%v) error=%v", value, value, err)
		}
	}
	for _, value := range []any{
		uint(1),
		int64(2),
		float64(3),
		json.Number("4"),
	} {
		normalized, err := normalizeTicketCategoryChange(value)
		if err != nil {
			t.Fatalf("normalize category change %T(%v): %v", value, value, err)
		}
		if normalized == nil {
			t.Fatalf("normalized category change %T(%v) is nil", value, value)
		}
	}
}
