package database

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	categoryScopeCutoverCheckpointKey      = "20260801_category_project_scope_v1"
	categoryScopeCutoverCheckpointVersion  = uint(1)
	categoryScopeCutoverCheckpointChecksum = "ab3449444a89bca4d6c09b927aece888731bd87295daf96723832cab2aa54517"

	categoryProjectSlugIndex = "idx_categories_project_slug"
	categoryScopeIDIndex     = "idx_categories_scope_id"
	categoryScopeListIndex   = "idx_categories_scope_status_sort"
	categoryScopeParentIndex = "idx_categories_scope_parent"
	categoryScopeTypeIndex   = "idx_categories_scope_type"
	projectScopeIDIndex      = "idx_projects_scope_id"
)

type categoryMigrationTicket struct {
	ID             uint  `gorm:"column:id"`
	OrganizationID uint  `gorm:"column:organization_id"`
	ProjectID      uint  `gorm:"column:project_id"`
	CategoryID     *uint `gorm:"column:category_id"`
	SubcategoryID  *uint `gorm:"column:subcategory_id"`
}

type categoryScopeKey struct {
	OrganizationID uint
	ProjectID      uint
}

type categoryScopeRowKey struct {
	CategoryID uint
	Scope      categoryScopeKey
}

// PrepareCategoryScopeColumns stages a zero sentinel before the canonical
// Category model is parsed. It never infers a project. The dedicated cutover
// replaces every sentinel from Ticket evidence or explicit operator mappings
// before removing the temporary defaults.
func PrepareCategoryScopeColumns(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if err := db.AutoMigrate(&models.CategoryScopeMigrationMapping{}); err != nil {
		return fmt.Errorf("prepare category scope operator mappings: %w", err)
	}
	if !db.Migrator().HasTable(&models.Category{}) {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		switch tx.Dialector.Name() {
		case "postgres":
			if err := tx.Exec(`
				ALTER TABLE categories
					ADD COLUMN IF NOT EXISTS organization_id BIGINT NOT NULL DEFAULT 0,
					ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0
			`).Error; err != nil {
				return fmt.Errorf("stage legacy category scope columns: %w", err)
			}
			if err := tx.Exec(`
				UPDATE categories
				SET
					organization_id = COALESCE(organization_id, 0),
					project_id = COALESCE(project_id, 0)
				WHERE organization_id IS NULL OR project_id IS NULL
			`).Error; err != nil {
				return fmt.Errorf("normalize retryable category scope sentinels: %w", err)
			}
			if err := tx.Exec(`
				ALTER TABLE categories
					ALTER COLUMN organization_id SET NOT NULL,
					ALTER COLUMN project_id SET NOT NULL
			`).Error; err != nil {
				return fmt.Errorf("stabilize category scope sentinels: %w", err)
			}
		case "sqlite":
			for _, column := range []string{"organization_id", "project_id"} {
				hasColumn, err := hasExactDatabaseColumn(tx, "categories", column)
				if err != nil {
					return err
				}
				if hasColumn {
					continue
				}
				if err := tx.Exec(
					"ALTER TABLE categories ADD COLUMN " + column +
						" INTEGER NOT NULL DEFAULT 0",
				).Error; err != nil {
					return fmt.Errorf(
						"stage SQLite legacy category scope column %s: %w",
						column,
						err,
					)
				}
			}
		default:
			return fmt.Errorf(
				"unsupported category scope migration dialect %q",
				tx.Dialector.Name(),
			)
		}
		return nil
	})
}

// MigrateCategoryProjectScope is the one-time fail-closed category cutover.
// A legacy category tree is cloned per proven ProjectScope and every Ticket
// reference is rewritten in the same transaction.
func MigrateCategoryProjectScope(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, required := range []any{
		&models.SchemaMigrationCheckpoint{},
		&models.Category{},
		&models.CategoryScopeMigrationMapping{},
		&models.Project{},
		&models.Ticket{},
	} {
		if !db.Migrator().HasTable(required) {
			return fmt.Errorf(
				"category project scope migration requires %s",
				indirectModelName(required),
			)
		}
	}
	if err := PrepareCategoryScopeColumns(db); err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		completed, err := lockAndReadCategoryScopeMarker(tx)
		if err != nil {
			return err
		}
		if completed {
			// Healthy PostgreSQL deployments avoid taking repeat DDL locks on
			// categories/tickets. Catalog drift is repairable and therefore
			// falls through to the exact contract installer.
			if tx.Dialector.Name() == "postgres" {
				if err := validatePostgresCategoryScopeCatalog(tx); err == nil {
					// A completed deployment may already FORCE RLS. The owner
					// connection deliberately has no fabricated ProjectScope,
					// so post-checkpoint readiness is catalog-only.
					return nil
				}
				if err := installCategoryScopeContract(tx); err != nil {
					return err
				}
				return validatePostgresCategoryScopeCatalog(tx)
			}
			if err := installCategoryScopeContract(tx); err != nil {
				return err
			}
			return validateCategoryScopeData(tx)
		}

		var categories []models.Category
		if err := tx.Unscoped().
			Order("id ASC").
			Find(&categories).Error; err != nil {
			return fmt.Errorf("load categories for project scope cutover: %w", err)
		}
		var projects []models.Project
		if err := tx.Order("organization_id ASC, id ASC").
			Find(&projects).Error; err != nil {
			return fmt.Errorf("load projects for category scope cutover: %w", err)
		}
		projectScopes := make(map[uint]categoryScopeKey, len(projects))
		for _, project := range projects {
			if project.ID == 0 || project.OrganizationID == 0 {
				return errors.New("category scope cutover found an invalid Project")
			}
			projectScopes[project.ID] = categoryScopeKey{
				OrganizationID: project.OrganizationID,
				ProjectID:      project.ID,
			}
		}
		if len(categories) > 0 && len(projectScopes) == 0 {
			return errors.New(
				"category scope cutover requires at least one persisted Project",
			)
		}

		categoryByID := make(map[uint]*models.Category, len(categories))
		parent := make(map[uint]uint, len(categories))
		for index := range categories {
			category := &categories[index]
			categoryByID[category.ID] = category
			parent[category.ID] = category.ID
			if (category.OrganizationID == 0) != (category.ProjectID == 0) {
				return fmt.Errorf(
					"category scope cutover found partially scoped category %d",
					category.ID,
				)
			}
			if category.ProjectID != 0 {
				scope, exists := projectScopes[category.ProjectID]
				if !exists || scope.OrganizationID != category.OrganizationID {
					return fmt.Errorf(
						"category %d references an invalid project scope",
						category.ID,
					)
				}
			}
			if category.ParentID != nil {
				if *category.ParentID == category.ID {
					return fmt.Errorf(
						"category %d cannot be its own parent",
						category.ID,
					)
				}
				if _, exists := categoryByID[*category.ParentID]; !exists {
					// Parent rows may appear later in ID order.
					continue
				}
			}
		}
		for _, category := range categories {
			if category.ParentID == nil {
				continue
			}
			if _, exists := categoryByID[*category.ParentID]; !exists {
				return fmt.Errorf(
					"category %d references missing parent %d",
					category.ID,
					*category.ParentID,
				)
			}
			categoryUnion(parent, category.ID, *category.ParentID)
		}
		if err := rejectCategoryParentCycles(categoryByID); err != nil {
			return err
		}

		var tickets []categoryMigrationTicket
		if err := tx.Unscoped().
			Table("tickets").
			Select(
				"id",
				"organization_id",
				"project_id",
				"category_id",
				"subcategory_id",
			).
			Order("id ASC").
			Scan(&tickets).Error; err != nil {
			return fmt.Errorf("load Ticket category references: %w", err)
		}
		componentScopes := make(map[uint]map[categoryScopeKey]struct{})
		addScope := func(categoryID uint, scope categoryScopeKey) error {
			if _, exists := categoryByID[categoryID]; !exists {
				return fmt.Errorf(
					"category scope cutover references missing category %d",
					categoryID,
				)
			}
			root := categoryFind(parent, categoryID)
			if componentScopes[root] == nil {
				componentScopes[root] = make(map[categoryScopeKey]struct{})
			}
			componentScopes[root][scope] = struct{}{}
			return nil
		}
		for _, category := range categories {
			if category.ProjectID == 0 {
				continue
			}
			if err := addScope(category.ID, categoryScopeKey{
				OrganizationID: category.OrganizationID,
				ProjectID:      category.ProjectID,
			}); err != nil {
				return err
			}
		}
		for _, ticket := range tickets {
			scope, exists := projectScopes[ticket.ProjectID]
			if ticket.ProjectID == 0 ||
				ticket.OrganizationID == 0 ||
				!exists ||
				scope.OrganizationID != ticket.OrganizationID {
				return fmt.Errorf(
					"Ticket %d has no trustworthy project scope for category migration",
					ticket.ID,
				)
			}
			for _, categoryID := range []*uint{
				ticket.CategoryID,
				ticket.SubcategoryID,
			} {
				if categoryID == nil {
					continue
				}
				if err := addScope(*categoryID, scope); err != nil {
					return fmt.Errorf("Ticket %d: %w", ticket.ID, err)
				}
			}
		}

		var mappings []models.CategoryScopeMigrationMapping
		if err := tx.Order("category_id ASC, project_id ASC").
			Find(&mappings).Error; err != nil {
			return fmt.Errorf("load category scope operator mappings: %w", err)
		}
		for _, mapping := range mappings {
			scope, exists := projectScopes[mapping.ProjectID]
			if !exists || scope.OrganizationID != mapping.OrganizationID {
				return fmt.Errorf(
					"category scope operator mapping for category %d targets invalid project %d/%d",
					mapping.CategoryID,
					mapping.OrganizationID,
					mapping.ProjectID,
				)
			}
			if err := addScope(mapping.CategoryID, scope); err != nil {
				return fmt.Errorf("invalid category scope operator mapping: %w", err)
			}
		}

		componentMembers := make(map[uint][]uint)
		for _, category := range categories {
			root := categoryFind(parent, category.ID)
			componentMembers[root] = append(
				componentMembers[root],
				category.ID,
			)
		}
		if len(projectScopes) == 1 {
			var onlyScope categoryScopeKey
			for _, scope := range projectScopes {
				onlyScope = scope
			}
			for root := range componentMembers {
				if len(componentScopes[root]) == 0 {
					componentScopes[root] = map[categoryScopeKey]struct{}{
						onlyScope: {},
					}
				}
			}
		}
		var ambiguous []string
		for root, members := range componentMembers {
			if len(componentScopes[root]) != 0 {
				continue
			}
			sort.Slice(members, func(i, j int) bool {
				return members[i] < members[j]
			})
			for _, categoryID := range members {
				category := categoryByID[categoryID]
				ambiguous = append(
					ambiguous,
					fmt.Sprintf("%d:%s", category.ID, category.Slug),
				)
			}
		}
		if len(ambiguous) > 0 {
			return fmt.Errorf(
				"category project scope is ambiguous for [%s]; insert explicit rows into category_scope_migration_mappings(category_id, organization_id, project_id) and retry",
				strings.Join(ambiguous, ", "),
			)
		}

		if err := dropLegacyCategoryUniqueness(tx); err != nil {
			return err
		}
		if len(categories) > 0 {
			if err := tx.Unscoped().
				Table("categories").
				Where("parent_id IS NOT NULL").
				Update("parent_id", nil).Error; err != nil {
				return fmt.Errorf(
					"stage category parents for scoped cloning: %w",
					err,
				)
			}
		}

		scopedCategoryIDs := make(
			map[categoryScopeRowKey]uint,
			len(categories),
		)
		for _, category := range categories {
			root := categoryFind(parent, category.ID)
			scopes := sortedCategoryScopes(componentScopes[root])
			keepScope := scopes[0]
			if category.ProjectID != 0 {
				keepScope = categoryScopeKey{
					OrganizationID: category.OrganizationID,
					ProjectID:      category.ProjectID,
				}
			}
			if err := tx.Unscoped().
				Table("categories").
				Where("id = ?", category.ID).
				Updates(map[string]any{
					"organization_id": keepScope.OrganizationID,
					"project_id":      keepScope.ProjectID,
					"parent_id":       nil,
				}).Error; err != nil {
				return fmt.Errorf(
					"scope legacy category %d: %w",
					category.ID,
					err,
				)
			}
			scopedCategoryIDs[categoryScopeRowKey{
				CategoryID: category.ID,
				Scope:      keepScope,
			}] = category.ID

			for _, scope := range scopes {
				if scope == keepScope {
					continue
				}
				clone := category
				clone.ID = 0
				clone.OrganizationID = scope.OrganizationID
				clone.ProjectID = scope.ProjectID
				clone.ParentID = nil
				clone.Parent = nil
				clone.Children = nil
				clone.Tickets = nil
				if err := tx.Unscoped().
					Omit(clause.Associations).
					Create(&clone).Error; err != nil {
					return fmt.Errorf(
						"clone category %d into project %d: %w",
						category.ID,
						scope.ProjectID,
						err,
					)
				}
				scopedCategoryIDs[categoryScopeRowKey{
					CategoryID: category.ID,
					Scope:      scope,
				}] = clone.ID
			}
		}

		for _, category := range categories {
			if category.ParentID == nil {
				continue
			}
			root := categoryFind(parent, category.ID)
			for _, scope := range sortedCategoryScopes(componentScopes[root]) {
				categoryID := scopedCategoryIDs[categoryScopeRowKey{
					CategoryID: category.ID,
					Scope:      scope,
				}]
				parentID := scopedCategoryIDs[categoryScopeRowKey{
					CategoryID: *category.ParentID,
					Scope:      scope,
				}]
				if categoryID == 0 || parentID == 0 {
					return fmt.Errorf(
						"category %d parent mapping is incomplete for project %d",
						category.ID,
						scope.ProjectID,
					)
				}
				if err := tx.Unscoped().
					Table("categories").
					Where("id = ?", categoryID).
					Update("parent_id", parentID).Error; err != nil {
					return fmt.Errorf(
						"restore scoped parent for category %d: %w",
						categoryID,
						err,
					)
				}
			}
		}
		if err := rebuildCategoryHierarchyProjection(
			tx,
			categories,
			parent,
			componentScopes,
			scopedCategoryIDs,
		); err != nil {
			return err
		}

		for _, ticket := range tickets {
			scope := categoryScopeKey{
				OrganizationID: ticket.OrganizationID,
				ProjectID:      ticket.ProjectID,
			}
			updates := make(map[string]any, 2)
			for column, original := range map[string]*uint{
				"category_id":    ticket.CategoryID,
				"subcategory_id": ticket.SubcategoryID,
			} {
				if original == nil {
					continue
				}
				mappedID := scopedCategoryIDs[categoryScopeRowKey{
					CategoryID: *original,
					Scope:      scope,
				}]
				if mappedID == 0 {
					return fmt.Errorf(
						"Ticket %d category %d has no mapping for project %d",
						ticket.ID,
						*original,
						ticket.ProjectID,
					)
				}
				updates[column] = mappedID
			}
			if len(updates) == 0 {
				continue
			}
			result := tx.Unscoped().
				Table("tickets").
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					ticket.ID,
					ticket.OrganizationID,
					ticket.ProjectID,
				).
				Updates(updates)
			if result.Error != nil {
				return fmt.Errorf(
					"rewrite Ticket %d category references: %w",
					ticket.ID,
					result.Error,
				)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf(
					"rewrite Ticket %d category references changed %d rows",
					ticket.ID,
					result.RowsAffected,
				)
			}
		}
		if err := refreshCategoryCounters(tx); err != nil {
			return err
		}

		if err := installCategoryScopeContract(tx); err != nil {
			return err
		}
		if err := validateCategoryScopeData(tx); err != nil {
			return err
		}
		if err := tx.Create(&models.SchemaMigrationCheckpoint{
			Key:         categoryScopeCutoverCheckpointKey,
			Version:     categoryScopeCutoverCheckpointVersion,
			Checksum:    categoryScopeCutoverCheckpointChecksum,
			CompletedAt: time.Now().UTC(),
		}).Error; err != nil {
			return fmt.Errorf(
				"record category project scope cutover completion: %w",
				err,
			)
		}
		return nil
	})
}

type categoryHierarchyProjection struct {
	Level int
	Path  string
}

func rebuildCategoryHierarchyProjection(
	tx *gorm.DB,
	categories []models.Category,
	componentParent map[uint]uint,
	componentScopes map[uint]map[categoryScopeKey]struct{},
	scopedCategoryIDs map[categoryScopeRowKey]uint,
) error {
	categoryByID := make(map[uint]models.Category, len(categories))
	for _, category := range categories {
		categoryByID[category.ID] = category
	}
	memo := make(
		map[categoryScopeRowKey]categoryHierarchyProjection,
		len(scopedCategoryIDs),
	)
	var resolve func(
		uint,
		categoryScopeKey,
	) (categoryHierarchyProjection, error)
	resolve = func(
		originalID uint,
		scope categoryScopeKey,
	) (categoryHierarchyProjection, error) {
		key := categoryScopeRowKey{
			CategoryID: originalID,
			Scope:      scope,
		}
		if projection, exists := memo[key]; exists {
			return projection, nil
		}
		scopedID := scopedCategoryIDs[key]
		if scopedID == 0 {
			return categoryHierarchyProjection{}, fmt.Errorf(
				"category %d has no hierarchy projection for project %d",
				originalID,
				scope.ProjectID,
			)
		}
		category, exists := categoryByID[originalID]
		if !exists {
			return categoryHierarchyProjection{}, fmt.Errorf(
				"category hierarchy references missing category %d",
				originalID,
			)
		}
		projection := categoryHierarchyProjection{
			Level: 0,
			Path:  fmt.Sprintf("/%d", scopedID),
		}
		if category.ParentID != nil {
			parentProjection, err := resolve(*category.ParentID, scope)
			if err != nil {
				return categoryHierarchyProjection{}, err
			}
			projection.Level = parentProjection.Level + 1
			projection.Path = parentProjection.Path +
				fmt.Sprintf("/%d", scopedID)
		}
		memo[key] = projection
		if err := tx.Unscoped().
			Table("categories").
			Where(
				"id = ? AND organization_id = ? AND project_id = ?",
				scopedID,
				scope.OrganizationID,
				scope.ProjectID,
			).
			Updates(map[string]any{
				"level": projection.Level,
				"path":  projection.Path,
			}).Error; err != nil {
			return categoryHierarchyProjection{}, fmt.Errorf(
				"update hierarchy projection for category %d: %w",
				scopedID,
				err,
			)
		}
		return projection, nil
	}
	for _, category := range categories {
		root := categoryFind(componentParent, category.ID)
		for _, scope := range sortedCategoryScopes(componentScopes[root]) {
			if _, err := resolve(category.ID, scope); err != nil {
				return err
			}
		}
	}
	return nil
}

func refreshCategoryCounters(tx *gorm.DB) error {
	if err := tx.Exec(`
		UPDATE categories
		SET
			children_count = (
				SELECT COUNT(*)
				FROM categories AS child
				WHERE child.organization_id = categories.organization_id
				  AND child.project_id = categories.project_id
				  AND child.parent_id = categories.id
				  AND child.deleted_at IS NULL
			),
			ticket_count = (
				SELECT COUNT(*)
				FROM tickets AS ticket
				WHERE ticket.organization_id = categories.organization_id
				  AND ticket.project_id = categories.project_id
				  AND ticket.category_id = categories.id
				  AND ticket.deleted_at IS NULL
			),
			active_ticket_count = (
				SELECT COUNT(*)
				FROM tickets AS ticket
				WHERE ticket.organization_id = categories.organization_id
				  AND ticket.project_id = categories.project_id
				  AND ticket.category_id = categories.id
				  AND ticket.deleted_at IS NULL
				  AND ticket.status IN ('open', 'in_progress', 'pending')
			)
	`).Error; err != nil {
		return fmt.Errorf("refresh scoped category counters: %w", err)
	}
	return nil
}

func indirectModelName(model any) string {
	switch model.(type) {
	case *models.SchemaMigrationCheckpoint:
		return "schema_migration_checkpoints table"
	case *models.Category:
		return "categories table"
	case *models.CategoryScopeMigrationMapping:
		return "category_scope_migration_mappings table"
	case *models.Project:
		return "projects table"
	case *models.Ticket:
		return "tickets table"
	default:
		return "required table"
	}
}

func lockAndReadCategoryScopeMarker(tx *gorm.DB) (bool, error) {
	if tx == nil {
		return false, errors.New(
			"category project scope cutover transaction is required",
		)
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(
			`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
			categoryScopeCutoverCheckpointKey,
		).Error; err != nil {
			return false, fmt.Errorf(
				"lock category project scope cutover: %w",
				err,
			)
		}
	}
	var checkpoint models.SchemaMigrationCheckpoint
	err := tx.Where("key = ?", categoryScopeCutoverCheckpointKey).
		First(&checkpoint).Error
	switch {
	case err == nil:
		if checkpoint.Version != categoryScopeCutoverCheckpointVersion ||
			checkpoint.Checksum != categoryScopeCutoverCheckpointChecksum {
			return false, fmt.Errorf(
				"category project scope checkpoint %q has unexpected version or checksum",
				categoryScopeCutoverCheckpointKey,
			)
		}
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, nil
	default:
		return false, fmt.Errorf(
			"read category project scope checkpoint: %w",
			err,
		)
	}
}

func categoryFind(parent map[uint]uint, value uint) uint {
	root := value
	for parent[root] != root {
		root = parent[root]
	}
	for value != root {
		next := parent[value]
		parent[value] = root
		value = next
	}
	return root
}

func categoryUnion(parent map[uint]uint, left, right uint) {
	leftRoot := categoryFind(parent, left)
	rightRoot := categoryFind(parent, right)
	if leftRoot == rightRoot {
		return
	}
	if leftRoot < rightRoot {
		parent[rightRoot] = leftRoot
		return
	}
	parent[leftRoot] = rightRoot
}

func rejectCategoryParentCycles(
	categoryByID map[uint]*models.Category,
) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[uint]int, len(categoryByID))
	var visit func(uint) error
	visit = func(categoryID uint) error {
		switch state[categoryID] {
		case visiting:
			return fmt.Errorf(
				"category parent hierarchy contains a cycle at category %d",
				categoryID,
			)
		case visited:
			return nil
		}
		state[categoryID] = visiting
		category := categoryByID[categoryID]
		if category != nil && category.ParentID != nil {
			if err := visit(*category.ParentID); err != nil {
				return err
			}
		}
		state[categoryID] = visited
		return nil
	}
	for categoryID := range categoryByID {
		if err := visit(categoryID); err != nil {
			return err
		}
	}
	return nil
}

func sortedCategoryScopes(
	values map[categoryScopeKey]struct{},
) []categoryScopeKey {
	result := make([]categoryScopeKey, 0, len(values))
	for scope := range values {
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].OrganizationID != result[j].OrganizationID {
			return result[i].OrganizationID < result[j].OrganizationID
		}
		return result[i].ProjectID < result[j].ProjectID
	})
	return result
}

func dropLegacyCategoryUniqueness(tx *gorm.DB) error {
	for _, indexName := range []string{
		"idx_categories_name",
		"idx_categories_slug",
	} {
		if err := tx.Exec(
			"DROP INDEX IF EXISTS " +
				quoteStaticProjectRLSIdentifier(indexName),
		).Error; err != nil {
			return fmt.Errorf(
				"drop legacy category index %s: %w",
				indexName,
				err,
			)
		}
	}
	return nil
}

func installCategoryScopeContract(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("category scope contract database is required")
	}
	if err := dropLegacyCategoryUniqueness(tx); err != nil {
		return err
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(`
			ALTER TABLE categories
				ALTER COLUMN organization_id DROP DEFAULT,
				ALTER COLUMN project_id DROP DEFAULT,
				ALTER COLUMN organization_id SET NOT NULL,
				ALTER COLUMN project_id SET NOT NULL
		`).Error; err != nil {
			return fmt.Errorf("finalize category scope columns: %w", err)
		}
		if err := dropPostgresCategoryForeignKeys(tx); err != nil {
			return err
		}
	}
	for _, indexName := range []string{
		categoryProjectSlugIndex,
		categoryScopeIDIndex,
		categoryScopeListIndex,
		categoryScopeParentIndex,
		categoryScopeTypeIndex,
	} {
		if err := tx.Exec(
			"DROP INDEX IF EXISTS " +
				quoteStaticProjectRLSIdentifier(indexName),
		).Error; err != nil {
			return fmt.Errorf(
				"replace category scope index %s: %w",
				indexName,
				err,
			)
		}
	}
	for _, statement := range []string{
		"CREATE UNIQUE INDEX " + categoryProjectSlugIndex +
			" ON categories(organization_id, project_id, slug)",
		"CREATE UNIQUE INDEX " + categoryScopeIDIndex +
			" ON categories(organization_id, project_id, id)",
		"CREATE INDEX " + categoryScopeListIndex +
			" ON categories(organization_id, project_id, status, sort_order, id)",
		"CREATE INDEX " + categoryScopeParentIndex +
			" ON categories(organization_id, project_id, parent_id)",
		"CREATE INDEX " + categoryScopeTypeIndex +
			" ON categories(organization_id, project_id, type, id)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"install category scope index contract: %w",
				err,
			)
		}
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	for _, statement := range []string{
		`ALTER TABLE categories
			DROP CONSTRAINT IF EXISTS chk_categories_parent_not_self`,
		`ALTER TABLE categories
			ADD CONSTRAINT chk_categories_parent_not_self
			CHECK (parent_id IS NULL OR parent_id <> id)`,
		`ALTER TABLE categories
			ADD CONSTRAINT fk_categories_project_scope
			FOREIGN KEY (organization_id, project_id)
			REFERENCES projects(organization_id, id)
			ON UPDATE CASCADE ON DELETE RESTRICT`,
		`ALTER TABLE categories
			ADD CONSTRAINT fk_categories_parent_scope
			FOREIGN KEY (organization_id, project_id, parent_id)
			REFERENCES categories(organization_id, project_id, id)
			ON UPDATE CASCADE ON DELETE RESTRICT`,
		`ALTER TABLE tickets
			ADD CONSTRAINT fk_tickets_category_scope
			FOREIGN KEY (organization_id, project_id, category_id)
			REFERENCES categories(organization_id, project_id, id)
			ON UPDATE CASCADE ON DELETE RESTRICT`,
		`ALTER TABLE tickets
			ADD CONSTRAINT fk_tickets_subcategory_scope
			FOREIGN KEY (organization_id, project_id, subcategory_id)
			REFERENCES categories(organization_id, project_id, id)
			ON UPDATE CASCADE ON DELETE RESTRICT`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"install PostgreSQL category scope constraint: %w",
				err,
			)
		}
	}
	if err := tx.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS " + projectScopeIDIndex +
			" ON projects(organization_id, id)",
	).Error; err != nil {
		return fmt.Errorf(
			"ensure Project scope identity index %s: %w",
			projectScopeIDIndex,
			err,
		)
	}
	return nil
}

func dropPostgresCategoryForeignKeys(tx *gorm.DB) error {
	type constraintRow struct {
		TableName      string `gorm:"column:table_name"`
		ConstraintName string `gorm:"column:constraint_name"`
	}
	var rows []constraintRow
	if err := tx.Raw(`
		SELECT DISTINCT
			table_row.relname AS table_name,
			constraint_row.conname AS constraint_name
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS table_row
		  ON table_row.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		JOIN LATERAL unnest(constraint_row.conkey) AS key_row(attnum)
		  ON TRUE
		JOIN pg_attribute AS column_row
		  ON column_row.attrelid = table_row.oid
		 AND column_row.attnum = key_row.attnum
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND constraint_row.contype = 'f'
		  AND (
			(table_row.relname = 'categories' AND column_row.attname IN (
				'organization_id', 'project_id', 'parent_id'
			))
			OR
			(table_row.relname = 'tickets' AND column_row.attname IN (
				'category_id', 'subcategory_id'
			))
		  )
		ORDER BY table_name, constraint_name
	`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("inspect legacy category foreign keys: %w", err)
	}
	for _, row := range rows {
		if !projectRLSIdentifierPattern.MatchString(row.TableName) ||
			!projectRLSIdentifierPattern.MatchString(row.ConstraintName) {
			return fmt.Errorf(
				"unsafe category foreign-key identifier %q.%q",
				row.TableName,
				row.ConstraintName,
			)
		}
		if err := tx.Exec(
			"ALTER TABLE " +
				quoteStaticProjectRLSIdentifier(row.TableName) +
				" DROP CONSTRAINT " +
				quoteStaticProjectRLSIdentifier(row.ConstraintName),
		).Error; err != nil {
			return fmt.Errorf(
				"drop legacy category foreign key %s.%s: %w",
				row.TableName,
				row.ConstraintName,
				err,
			)
		}
	}
	return nil
}

func validateCategoryScopeData(db *gorm.DB) error {
	var invalidCategories int64
	if err := db.Unscoped().
		Table("categories AS category").
		Joins(
			"LEFT JOIN projects AS project ON project.id = category.project_id AND project.organization_id = category.organization_id",
		).
		Where(
			"category.organization_id = 0 OR category.project_id = 0 OR project.id IS NULL",
		).
		Count(&invalidCategories).Error; err != nil {
		return fmt.Errorf("validate category project ownership: %w", err)
	}
	if invalidCategories != 0 {
		return fmt.Errorf(
			"category scope contract contains %d category row(s) without a valid Project",
			invalidCategories,
		)
	}

	var invalidParents int64
	if err := db.Unscoped().
		Table("categories AS child").
		Joins(
			"LEFT JOIN categories AS parent ON parent.id = child.parent_id AND parent.organization_id = child.organization_id AND parent.project_id = child.project_id",
		).
		Where("child.parent_id IS NOT NULL AND parent.id IS NULL").
		Count(&invalidParents).Error; err != nil {
		return fmt.Errorf("validate category parent scopes: %w", err)
	}
	if invalidParents != 0 {
		return fmt.Errorf(
			"category scope contract contains %d cross-project or missing parent reference(s)",
			invalidParents,
		)
	}

	var invalidTicketCategories int64
	if err := db.Unscoped().
		Table("tickets AS ticket").
		Joins(
			"LEFT JOIN categories AS category ON category.id = ticket.category_id AND category.organization_id = ticket.organization_id AND category.project_id = ticket.project_id",
		).
		Joins(
			"LEFT JOIN categories AS subcategory ON subcategory.id = ticket.subcategory_id AND subcategory.organization_id = ticket.organization_id AND subcategory.project_id = ticket.project_id",
		).
		Where(`
			(ticket.category_id IS NOT NULL AND category.id IS NULL)
			OR (ticket.subcategory_id IS NOT NULL AND subcategory.id IS NULL)
			OR (ticket.subcategory_id IS NOT NULL AND ticket.category_id IS NULL)
			OR (
				ticket.subcategory_id IS NOT NULL
				AND subcategory.parent_id IS DISTINCT FROM ticket.category_id
			)
		`).
		Count(&invalidTicketCategories).Error; err != nil {
		// SQLite has no IS DISTINCT FROM before older embedded versions.
		if db.Dialector.Name() != "sqlite" {
			return fmt.Errorf("validate Ticket category scopes: %w", err)
		}
		if err := db.Unscoped().
			Table("tickets AS ticket").
			Joins(
				"LEFT JOIN categories AS category ON category.id = ticket.category_id AND category.organization_id = ticket.organization_id AND category.project_id = ticket.project_id",
			).
			Joins(
				"LEFT JOIN categories AS subcategory ON subcategory.id = ticket.subcategory_id AND subcategory.organization_id = ticket.organization_id AND subcategory.project_id = ticket.project_id",
			).
			Where(`
				(ticket.category_id IS NOT NULL AND category.id IS NULL)
				OR (ticket.subcategory_id IS NOT NULL AND subcategory.id IS NULL)
				OR (ticket.subcategory_id IS NOT NULL AND ticket.category_id IS NULL)
				OR (
					ticket.subcategory_id IS NOT NULL
					AND (
						subcategory.parent_id IS NULL
						OR subcategory.parent_id <> ticket.category_id
					)
				)
			`).
			Count(&invalidTicketCategories).Error; err != nil {
			return fmt.Errorf("validate SQLite Ticket category scopes: %w", err)
		}
	}
	if invalidTicketCategories != 0 {
		return fmt.Errorf(
			"category scope contract contains %d invalid Ticket category selection(s)",
			invalidTicketCategories,
		)
	}
	return nil
}

// ValidateCategoryScopeContract is the runtime gate for the dedicated
// checkpoint and exact database contract. Generic project RLS validation
// independently verifies the canonical policy and ENABLE/FORCE state.
func ValidateCategoryScopeContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, model := range []any{
		&models.Category{},
		&models.CategoryScopeMigrationMapping{},
		&models.SchemaMigrationCheckpoint{},
	} {
		if !db.Migrator().HasTable(model) {
			return errors.New(
				"category project scope schema is incomplete; run `go run ./cmd/migrate`",
			)
		}
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where("key = ?", categoryScopeCutoverCheckpointKey).
		First(&checkpoint).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New(
			"category project scope cutover is incomplete; run `go run ./cmd/migrate`",
		)
	} else if err != nil {
		return fmt.Errorf("validate category scope checkpoint: %w", err)
	}
	if checkpoint.Version != categoryScopeCutoverCheckpointVersion ||
		checkpoint.Checksum != categoryScopeCutoverCheckpointChecksum {
		return fmt.Errorf(
			"category project scope checkpoint %q has unexpected version or checksum",
			categoryScopeCutoverCheckpointKey,
		)
	}
	if db.Dialector.Name() == "postgres" {
		return validatePostgresCategoryScopeCatalog(db)
	}
	for _, pair := range []struct {
		Table string
		Index string
	}{
		{"categories", categoryProjectSlugIndex},
		{"categories", categoryScopeIDIndex},
		{"categories", categoryScopeListIndex},
		{"categories", categoryScopeParentIndex},
		{"categories", categoryScopeTypeIndex},
		{"projects", projectScopeIDIndex},
	} {
		var count int64
		if err := db.Raw(`
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'index' AND tbl_name = ? AND name = ?
		`, pair.Table, pair.Index).Scan(&count).Error; err != nil {
			return fmt.Errorf("validate SQLite category scope index: %w", err)
		}
		if count != 1 {
			return fmt.Errorf(
				"category scope index %s is missing; run `go run ./cmd/migrate`",
				pair.Index,
			)
		}
	}
	return validateCategoryScopeData(db)
}

func validatePostgresCategoryScopeCatalog(db *gorm.DB) error {
	var columns []struct {
		ColumnName string  `gorm:"column:column_name"`
		IsNullable string  `gorm:"column:is_nullable"`
		Default    *string `gorm:"column:column_default"`
		DataType   string  `gorm:"column:data_type"`
	}
	if err := db.Raw(`
		SELECT column_name, is_nullable, column_default, data_type
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name = 'categories'
		  AND column_name IN ('organization_id', 'project_id')
		ORDER BY column_name
	`).Scan(&columns).Error; err != nil {
		return fmt.Errorf("read PostgreSQL category scope columns: %w", err)
	}
	if len(columns) != 2 {
		return errors.New(
			"categories scope columns are missing; run `go run ./cmd/migrate`",
		)
	}
	for _, column := range columns {
		if column.IsNullable != "NO" ||
			column.Default != nil ||
			column.DataType != "bigint" {
			return fmt.Errorf(
				"categories.%s must be BIGINT NOT NULL without a default",
				column.ColumnName,
			)
		}
	}

	requiredIndexes := map[string]struct {
		Unique  bool
		Columns string
	}{
		categoryProjectSlugIndex: {
			Unique:  true,
			Columns: "organization_id,project_id,slug",
		},
		categoryScopeIDIndex: {
			Unique:  true,
			Columns: "organization_id,project_id,id",
		},
		categoryScopeListIndex: {
			Columns: "organization_id,project_id,status,sort_order,id",
		},
		categoryScopeParentIndex: {
			Columns: "organization_id,project_id,parent_id",
		},
		categoryScopeTypeIndex: {
			Columns: "organization_id,project_id,type,id",
		},
		projectScopeIDIndex: {
			Unique:  true,
			Columns: "organization_id,id",
		},
	}
	indexNames := make([]string, 0, len(requiredIndexes))
	for indexName := range requiredIndexes {
		indexNames = append(indexNames, indexName)
	}
	var indexes []struct {
		Name       string `gorm:"column:index_name"`
		Unique     bool   `gorm:"column:is_unique"`
		Partial    bool   `gorm:"column:is_partial"`
		Expression bool   `gorm:"column:is_expression"`
		Columns    string `gorm:"column:columns"`
	}
	if err := db.Raw(`
		SELECT
			index_row.relname AS index_name,
			index_contract.indisunique AS is_unique,
			(index_contract.indpred IS NOT NULL) AS is_partial,
			(index_contract.indexprs IS NOT NULL) AS is_expression,
			string_agg(column_row.attname, ',' ORDER BY key_row.ordinality)
				AS columns
		FROM pg_index AS index_contract
		JOIN pg_class AS table_row
		  ON table_row.oid = index_contract.indrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		JOIN pg_class AS index_row
		  ON index_row.oid = index_contract.indexrelid
		JOIN LATERAL unnest(index_contract.indkey)
			WITH ORDINALITY AS key_row(attnum, ordinality)
		  ON TRUE
		LEFT JOIN pg_attribute AS column_row
		  ON column_row.attrelid = table_row.oid
		 AND column_row.attnum = key_row.attnum
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND index_row.relname IN ?
		GROUP BY
			index_row.relname,
			index_contract.indisunique,
			index_contract.indpred,
			index_contract.indexprs
	`, indexNames).Scan(&indexes).Error; err != nil {
		return fmt.Errorf("read PostgreSQL category scope indexes: %w", err)
	}
	if len(indexes) != len(requiredIndexes) {
		return errors.New(
			"category project scope indexes are incomplete; run `go run ./cmd/migrate`",
		)
	}
	for _, index := range indexes {
		expected, exists := requiredIndexes[index.Name]
		if !exists ||
			index.Unique != expected.Unique ||
			index.Partial ||
			index.Expression ||
			index.Columns != expected.Columns {
			return fmt.Errorf(
				"category project scope index %s has drifted (unique=%t partial=%t expression=%t columns=%q); run `go run ./cmd/migrate`",
				index.Name,
				index.Unique,
				index.Partial,
				index.Expression,
				index.Columns,
			)
		}
	}

	requiredConstraints := map[string]struct {
		Table    string
		Type     string
		Contains []string
	}{
		"chk_categories_parent_not_self": {
			Table: "categories",
			Type:  "c",
			Contains: []string{
				"parent_id",
				"id",
			},
		},
		"fk_categories_project_scope": {
			Table: "categories",
			Type:  "f",
			Contains: []string{
				"foreign key (organization_id, project_id)",
				"references projects(organization_id, id)",
			},
		},
		"fk_categories_parent_scope": {
			Table: "categories",
			Type:  "f",
			Contains: []string{
				"foreign key (organization_id, project_id, parent_id)",
				"references categories(organization_id, project_id, id)",
			},
		},
		"fk_tickets_category_scope": {
			Table: "tickets",
			Type:  "f",
			Contains: []string{
				"foreign key (organization_id, project_id, category_id)",
				"references categories(organization_id, project_id, id)",
			},
		},
		"fk_tickets_subcategory_scope": {
			Table: "tickets",
			Type:  "f",
			Contains: []string{
				"foreign key (organization_id, project_id, subcategory_id)",
				"references categories(organization_id, project_id, id)",
			},
		},
	}
	constraintNames := make([]string, 0, len(requiredConstraints))
	for constraintName := range requiredConstraints {
		constraintNames = append(constraintNames, constraintName)
	}
	var constraints []struct {
		Name       string `gorm:"column:constraint_name"`
		Table      string `gorm:"column:table_name"`
		Type       string `gorm:"column:constraint_type"`
		Definition string `gorm:"column:definition"`
		Validated  bool   `gorm:"column:is_validated"`
		UpdateType string `gorm:"column:update_type"`
		DeleteType string `gorm:"column:delete_type"`
	}
	if err := db.Raw(`
		SELECT
			constraint_row.conname AS constraint_name,
			table_row.relname AS table_name,
			constraint_row.contype::text AS constraint_type,
			LOWER(pg_get_constraintdef(constraint_row.oid, true)) AS definition,
			constraint_row.convalidated AS is_validated,
			constraint_row.confupdtype::text AS update_type,
			constraint_row.confdeltype::text AS delete_type
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS table_row
		  ON table_row.oid = constraint_row.conrelid
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = constraint_row.connamespace
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND constraint_row.conname IN ?
	`, constraintNames).Scan(&constraints).Error; err != nil {
		return fmt.Errorf(
			"read PostgreSQL category scope constraints: %w",
			err,
		)
	}
	if len(constraints) != len(requiredConstraints) {
		return errors.New(
			"category project scope constraints are incomplete; run `go run ./cmd/migrate`",
		)
	}
	for _, constraint := range constraints {
		expected, exists := requiredConstraints[constraint.Name]
		if !exists ||
			constraint.Table != expected.Table ||
			constraint.Type != expected.Type ||
			!constraint.Validated {
			return fmt.Errorf(
				"category project scope constraint %s has drifted; run `go run ./cmd/migrate`",
				constraint.Name,
			)
		}
		if expected.Type == "f" &&
			(constraint.UpdateType != "c" || constraint.DeleteType != "r") {
			return fmt.Errorf(
				"category project scope constraint %s has unsafe actions; run `go run ./cmd/migrate`",
				constraint.Name,
			)
		}
		for _, fragment := range expected.Contains {
			if !strings.Contains(constraint.Definition, fragment) {
				return fmt.Errorf(
					"category project scope constraint %s has drifted; run `go run ./cmd/migrate`",
					constraint.Name,
				)
			}
		}
	}
	return nil
}
