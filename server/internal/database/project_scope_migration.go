package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	DefaultOrganizationSlug = "default"
	DefaultBusinessUnitKey  = "DEFAULT"
	DefaultProjectKey       = models.ProjectKey("DEFAULT")
	DefaultQueueKey         = models.QueueKey("default")
)

const projectScopeBackfillBatchSize = 100

const (
	projectScopeCutoverCheckpointKey      = "20260728_project_scope_v2_cutover"
	projectScopeCutoverCheckpointVersion  = uint(1)
	projectScopeCutoverCheckpointChecksum = "091b47c5791b7bf186967700649312ddf93512cee6804c36d4d053ffc52e3da0"
	legacyTicketPublicIDPrefix            = "legacy-"
)

type projectScopeRequiredTable struct {
	model any
	name  string
}

type ProjectScopeMembershipWriter func(
	context.Context,
	*gorm.DB,
	models.User,
	models.ProjectScope,
	models.ProjectRole,
) error

// PrepareLegacyProjectScopeColumns gives pre-project PostgreSQL tables a
// canonical zero-scope sentinel before GORM parses the new NOT NULL model
// contract. The one-time cutover later replaces every zero scope with trusted
// project IDs. A prior failed attempt may leave all-null legacy columns; those
// are normalized back to the same zero sentinel for retry. Existing project
// control rows or any non-zero scope still fail closed for operator audit.
func PrepareLegacyProjectScopeColumns(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New(
			"project scope preparation requires schema migration checkpoints",
		)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		completed, err := lockAndReadProjectScopeCutoverMarker(tx)
		if err != nil {
			return err
		}
		if completed {
			return nil
		}
		if err := rejectUncheckpointedProjectControlState(tx); err != nil {
			return err
		}
		for _, tableName := range requiredProjectOwnedTableNames {
			if !tx.Migrator().HasTable(tableName) {
				continue
			}
			quotedTable, err := quoteProjectRLSIdentifier(tableName)
			if err != nil {
				return err
			}
			if err := tx.Exec(fmt.Sprintf(`
				ALTER TABLE %s
					ADD COLUMN IF NOT EXISTS organization_id BIGINT NOT NULL DEFAULT 0,
					ADD COLUMN IF NOT EXISTS project_id BIGINT NOT NULL DEFAULT 0
			`, quotedTable)).Error; err != nil {
				return fmt.Errorf(
					"prepare legacy project scope columns on %s: %w",
					tableName,
					err,
				)
			}
			var preScopedRows int64
			if err := tx.Table(tableName).
				Where(
					"COALESCE(organization_id, 0) <> 0 OR COALESCE(project_id, 0) <> 0",
				).
				Count(&preScopedRows).Error; err != nil {
				return fmt.Errorf(
					"inspect uncheckpointed project rows on %s: %w",
					tableName,
					err,
				)
			}
			if preScopedRows != 0 {
				return fmt.Errorf(
					"project scope cutover checkpoint is missing while %s contains %d pre-scoped row(s); audit the database before retrying",
					tableName,
					preScopedRows,
				)
			}
			if err := tx.Table(tableName).
				Where("organization_id IS NULL OR project_id IS NULL").
				Updates(map[string]any{
					"organization_id": 0,
					"project_id":      0,
				}).Error; err != nil {
				return fmt.Errorf(
					"stage retryable legacy project scope on %s: %w",
					tableName,
					err,
				)
			}
			if err := tx.Exec(fmt.Sprintf(`
				ALTER TABLE %s
					ALTER COLUMN organization_id SET NOT NULL,
					ALTER COLUMN project_id SET NOT NULL,
					ALTER COLUMN organization_id DROP DEFAULT,
					ALTER COLUMN project_id DROP DEFAULT
			`, quotedTable)).Error; err != nil {
				return fmt.Errorf(
					"remove legacy project scope defaults on %s: %w",
					tableName,
					err,
				)
			}
		}
		return prepareLegacyTicketContractColumns(tx)
	})
}

func rejectUncheckpointedProjectControlState(tx *gorm.DB) error {
	for _, tableName := range []string{
		"organizations",
		"business_units",
		"projects",
		"project_memberships",
		"teams",
		"team_memberships",
		"queues",
		"project_principal_grants",
	} {
		if !tx.Migrator().HasTable(tableName) {
			continue
		}
		var rows int64
		if err := tx.Table(tableName).Count(&rows).Error; err != nil {
			return fmt.Errorf(
				"inspect uncheckpointed project control table %s: %w",
				tableName,
				err,
			)
		}
		if rows != 0 {
			return fmt.Errorf(
				"project scope cutover checkpoint is missing while %s contains %d project control row(s); audit the database before retrying",
				tableName,
				rows,
			)
		}
	}
	return nil
}

func prepareLegacyTicketContractColumns(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&models.Ticket{}) {
		return nil
	}
	if !tx.Migrator().HasColumn(&models.Ticket{}, "public_id") {
		if err := tx.Exec(`
			ALTER TABLE tickets
				ADD COLUMN public_id VARCHAR(36)
		`).Error; err != nil {
			return fmt.Errorf("add legacy Ticket public ID: %w", err)
		}
	}
	if err := tx.Exec(`
		ALTER TABLE tickets
			ADD COLUMN IF NOT EXISTS queue_id BIGINT NOT NULL DEFAULT 0,
			ADD COLUMN IF NOT EXISTS request_type_version_id VARCHAR(36) NOT NULL DEFAULT '',
			ADD COLUMN IF NOT EXISTS workflow_version_id VARCHAR(36) NOT NULL DEFAULT ''
	`).Error; err != nil {
		return fmt.Errorf("prepare legacy Ticket project columns: %w", err)
	}
	if err := tx.Exec(`
		UPDATE tickets
		SET
			public_id = CASE
				WHEN public_id IS NULL OR BTRIM(public_id) = ''
				THEN ? || LPAD(id::text, 29, '0')
				ELSE public_id
			END,
			queue_id = COALESCE(queue_id, 0),
			request_type_version_id = COALESCE(request_type_version_id, ''),
			workflow_version_id = COALESCE(workflow_version_id, '')
		WHERE
			COALESCE(organization_id, 0) = 0
			AND COALESCE(project_id, 0) = 0
	`, legacyTicketPublicIDPrefix).Error; err != nil {
		return fmt.Errorf("stage retryable legacy Ticket contract: %w", err)
	}
	if err := tx.Exec(`
		ALTER TABLE tickets
			ALTER COLUMN public_id SET NOT NULL,
			ALTER COLUMN queue_id SET NOT NULL,
			ALTER COLUMN request_type_version_id SET NOT NULL,
			ALTER COLUMN workflow_version_id SET NOT NULL,
			ALTER COLUMN queue_id DROP DEFAULT,
			ALTER COLUMN request_type_version_id DROP DEFAULT,
			ALTER COLUMN workflow_version_id DROP DEFAULT
	`).Error; err != nil {
		return fmt.Errorf("remove legacy Ticket project defaults: %w", err)
	}
	return nil
}

var projectScopeRequiredTables = [...]projectScopeRequiredTable{
	{model: &models.Organization{}, name: "organizations"},
	{model: &models.BusinessUnit{}, name: "business_units"},
	{model: &models.Project{}, name: "projects"},
	{model: &models.ProjectMembership{}, name: "project_memberships"},
	{model: &models.Team{}, name: "teams"},
	{model: &models.TeamMembership{}, name: "team_memberships"},
	{model: &models.Queue{}, name: "queues"},
	{model: &models.ProjectPrincipalGrant{}, name: "project_principal_grants"},
	{model: &models.User{}, name: "users"},
	{model: &models.ServicePrincipal{}, name: "service_principals"},
	{model: &models.SchemaMigrationCheckpoint{}, name: "schema_migration_checkpoints"},
}

// MigrateProjectScope performs the one-time destructive scope upgrade and
// default authorization backfill for databases created before projects
// existed. The previously unscoped installation becomes one explicit
// Organization / BusinessUnit / Project, and every existing identity receives
// an equivalent default-project grant.
//
// The destructive backfill is guarded by an immutable versioned checkpoint
// committed as its final transactional write. Later schema migrations return
// before reading Ticket or identity data and therefore cannot reinterpret
// live multi-project state.
func MigrateProjectScope(
	db *gorm.DB,
	membershipWriters ...ProjectScopeMembershipWriter,
) error {
	if db == nil {
		return errors.New("database is required")
	}
	if len(membershipWriters) > 1 {
		return errors.New(
			"only one project scope membership writer is supported",
		)
	}
	var membershipWriter ProjectScopeMembershipWriter
	if len(membershipWriters) == 1 {
		membershipWriter = membershipWriters[0]
	}
	if !db.Migrator().HasTable(&models.Organization{}) {
		return nil
	}
	for _, required := range projectScopeRequiredTables {
		if !db.Migrator().HasTable(required.model) {
			return fmt.Errorf(
				"project scope migration requires %s table",
				required.name,
			)
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		completed, err := lockAndReadProjectScopeCutoverMarker(tx)
		if err != nil {
			return err
		}
		if completed {
			// The checkpoint skips only the destructive data/authorization
			// backfill. AutoMigrate still runs on every release, so structural
			// project constraints must be reasserted before the migration can
			// report success.
			if err := enforceTicketProjectColumnsNotNull(tx); err != nil {
				return err
			}
			return enforceProjectOwnedScopeNotNull(tx)
		}
		organization, err := ensureDefaultOrganization(tx)
		if err != nil {
			return err
		}
		unit, err := ensureDefaultBusinessUnit(tx, organization.ID)
		if err != nil {
			return err
		}
		project, err := ensureDefaultProject(tx, organization.ID, unit.ID)
		if err != nil {
			return err
		}
		queue, err := ensureDefaultQueue(tx, project.ID)
		if err != nil {
			return err
		}
		if err := backfillProjectTickets(
			tx,
			organization.ID,
			project,
			queue,
		); err != nil {
			return err
		}
		if err := backfillLegacyProjectOwnedRows(
			tx,
			organization.ID,
			project.ID,
		); err != nil {
			return err
		}
		if err := enforceProjectOwnedScopeNotNull(tx); err != nil {
			return err
		}
		if err := backfillDefaultProjectMemberships(
			tx,
			project.Scope(),
			membershipWriter,
		); err != nil {
			return err
		}
		if err := backfillDefaultProjectPrincipalGrants(tx, project.ID); err != nil {
			return err
		}
		if err := tx.Create(&models.SchemaMigrationCheckpoint{
			Key:         projectScopeCutoverCheckpointKey,
			Version:     projectScopeCutoverCheckpointVersion,
			Checksum:    projectScopeCutoverCheckpointChecksum,
			CompletedAt: time.Now().UTC(),
		}).Error; err != nil {
			return fmt.Errorf("record project scope cutover completion: %w", err)
		}
		return nil
	})
}

func lockAndReadProjectScopeCutoverMarker(tx *gorm.DB) (bool, error) {
	if tx == nil {
		return false, errors.New("project scope cutover transaction is required")
	}
	if tx.Dialector.Name() == "postgres" {
		// Serialize the absent-marker case too. SELECT ... FOR UPDATE cannot
		// lock a row that does not exist, while two concurrent legacy
		// backfills must never run against the same live tables.
		if err := tx.Exec(
			`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
			projectScopeCutoverCheckpointKey,
		).Error; err != nil {
			return false, fmt.Errorf(
				"lock project scope cutover checkpoint: %w",
				err,
			)
		}
	}
	var checkpoint models.SchemaMigrationCheckpoint
	err := tx.Where("key = ?", projectScopeCutoverCheckpointKey).
		First(&checkpoint).Error
	switch {
	case err == nil:
		if checkpoint.Version != projectScopeCutoverCheckpointVersion ||
			checkpoint.Checksum != projectScopeCutoverCheckpointChecksum {
			return false, fmt.Errorf(
				"project scope cutover checkpoint %q has unexpected version or checksum",
				projectScopeCutoverCheckpointKey,
			)
		}
		return true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := rejectProjectScopeCutoverAfterRLS(tx); err != nil {
			return false, err
		}
		return false, nil
	default:
		return false, fmt.Errorf("read project scope cutover checkpoint: %w", err)
	}
}

func rejectProjectScopeCutoverAfterRLS(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	var protectedTables int64
	if err := tx.Raw(`
		SELECT COUNT(*)
		FROM pg_class AS table_row
		JOIN pg_namespace AS namespace_row
		  ON namespace_row.oid = table_row.relnamespace
		WHERE namespace_row.nspname = CURRENT_SCHEMA()
		  AND table_row.relname IN ?
		  AND (table_row.relrowsecurity OR table_row.relforcerowsecurity)
	`, requiredProjectOwnedTableNames).Scan(&protectedTables).Error; err != nil {
		return fmt.Errorf(
			"inspect project RLS before scope cutover: %w",
			err,
		)
	}
	if protectedTables > 0 {
		return errors.New(
			"project scope cutover checkpoint is missing after project RLS was enabled; audit the database before retrying",
		)
	}
	return nil
}

// ValidateProjectScopeCutoverMarker prevents the runtime from accepting
// traffic when structural project tables exist but the destructive legacy
// data/authorization cutover never committed.
func ValidateProjectScopeCutoverMarker(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New(
			"project scope cutover checkpoint table is missing; run `go run ./cmd/migrate`",
		)
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where("key = ?", projectScopeCutoverCheckpointKey).
		First(&checkpoint).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New(
			"project scope cutover is incomplete; run `go run ./cmd/migrate`",
		)
	} else if err != nil {
		return fmt.Errorf("validate project scope cutover checkpoint: %w", err)
	}
	if checkpoint.Version != projectScopeCutoverCheckpointVersion ||
		checkpoint.Checksum != projectScopeCutoverCheckpointChecksum {
		return fmt.Errorf(
			"project scope cutover checkpoint %q has unexpected version or checksum",
			projectScopeCutoverCheckpointKey,
		)
	}
	return nil
}

func enforceProjectOwnedScopeNotNull(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	if err := validateProjectOwnedTableScopes(
		tx,
		requiredProjectOwnedTableNames,
	); err != nil {
		return err
	}
	nullable, err := postgresNullableProjectColumns(
		tx,
		requiredProjectOwnedTableNames,
		[]string{"organization_id", "project_id"},
	)
	if err != nil {
		return err
	}
	for _, tableName := range requiredProjectOwnedTableNames {
		var alterations []string
		for _, columnName := range []string{"organization_id", "project_id"} {
			if !nullable[tableName+"."+columnName] {
				continue
			}
			quotedColumn, err := quoteProjectContractColumn(columnName)
			if err != nil {
				return err
			}
			alterations = append(
				alterations,
				"ALTER COLUMN "+quotedColumn+" SET NOT NULL",
			)
		}
		if len(alterations) == 0 {
			continue
		}
		quotedTable, err := quoteProjectRLSIdentifier(tableName)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			"ALTER TABLE " + quotedTable + " " +
				strings.Join(alterations, ", "),
		).Error; err != nil {
			return fmt.Errorf(
				"enforce project scope columns on %s: %w",
				tableName,
				err,
			)
		}
	}
	return nil
}

func ensureDefaultOrganization(tx *gorm.DB) (*models.Organization, error) {
	candidate := models.Organization{
		Slug:        DefaultOrganizationSlug,
		Name:        "默认组织",
		Description: "项目作用域升级创建的默认组织",
		Status:      models.OrganizationStatusActive,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "slug"}},
		DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("create default organization: %w", err)
	}

	var organization models.Organization
	if err := tx.Where("slug = ?", DefaultOrganizationSlug).
		First(&organization).Error; err != nil {
		return nil, fmt.Errorf("load default organization: %w", err)
	}
	return &organization, nil
}

func ensureDefaultBusinessUnit(
	tx *gorm.DB,
	organizationID uint,
) (*models.BusinessUnit, error) {
	candidate := models.BusinessUnit{
		OrganizationID: organizationID,
		Key:            DefaultBusinessUnitKey,
		Name:           "默认业务线",
		Description:    "项目作用域升级创建的默认业务线",
		Status:         models.BusinessUnitStatusActive,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "organization_id"},
			{Name: "key"},
		},
		DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("create default business unit: %w", err)
	}

	var unit models.BusinessUnit
	if err := tx.
		Where("organization_id = ? AND key = ?", organizationID, DefaultBusinessUnitKey).
		First(&unit).Error; err != nil {
		return nil, fmt.Errorf("load default business unit: %w", err)
	}
	return &unit, nil
}

func ensureDefaultProject(
	tx *gorm.DB,
	organizationID uint,
	businessUnitID uint,
) (*models.Project, error) {
	candidate := models.Project{
		OrganizationID: organizationID,
		BusinessUnitID: businessUnitID,
		Key:            DefaultProjectKey,
		Name:           "默认项目",
		Description:    "项目作用域升级创建的默认项目",
		Status:         models.ProjectStatusActive,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "organization_id"},
			{Name: "key"},
		},
		DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("create default project: %w", err)
	}

	var project models.Project
	if err := tx.
		Where("organization_id = ? AND key = ?", organizationID, DefaultProjectKey).
		First(&project).Error; err != nil {
		return nil, fmt.Errorf("load default project: %w", err)
	}
	if project.BusinessUnitID != businessUnitID {
		return nil, fmt.Errorf(
			"default project belongs to business unit %d, require %d",
			project.BusinessUnitID,
			businessUnitID,
		)
	}
	return &project, nil
}

func ensureDefaultQueue(tx *gorm.DB, projectID uint) (*models.Queue, error) {
	candidate := models.Queue{
		ProjectID:   projectID,
		Key:         DefaultQueueKey,
		Name:        "默认队列",
		Description: "项目作用域升级创建的默认队列",
		Status:      models.QueueStatusActive,
		IsDefault:   true,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "project_id"},
			{Name: "key"},
		},
		DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("create default queue: %w", err)
	}
	var queue models.Queue
	if err := tx.Where(
		"project_id = ? AND key = ?",
		projectID,
		DefaultQueueKey,
	).First(&queue).Error; err != nil {
		return nil, fmt.Errorf("load default queue: %w", err)
	}
	return &queue, nil
}

const (
	bootstrapRequestIncident     = "00000000-0000-7000-8000-000000000101"
	bootstrapRequestRequest      = "00000000-0000-7000-8000-000000000102"
	bootstrapRequestProblem      = "00000000-0000-7000-8000-000000000103"
	bootstrapRequestChange       = "00000000-0000-7000-8000-000000000104"
	bootstrapRequestComplaint    = "00000000-0000-7000-8000-000000000105"
	bootstrapRequestConsultation = "00000000-0000-7000-8000-000000000106"
	bootstrapWorkflow            = "00000000-0000-7000-8000-000000000201"
)

func backfillProjectTickets(
	tx *gorm.DB,
	organizationID uint,
	project *models.Project,
	defaultQueue *models.Queue,
) error {
	if project == nil || defaultQueue == nil {
		return errors.New("project ticket backfill requires project and queue")
	}
	if !tx.Migrator().HasTable(&models.Ticket{}) {
		return nil
	}
	for _, column := range []string{
		"public_id",
		"organization_id",
		"project_id",
		"queue_id",
		"request_type_version_id",
		"workflow_version_id",
	} {
		if !tx.Migrator().HasColumn(&models.Ticket{}, column) {
			return fmt.Errorf("project ticket backfill requires tickets.%s", column)
		}
	}

	var partiallyScoped int64
	if err := tx.Unscoped().Model(&models.Ticket{}).
		Where(
			"(COALESCE(organization_id, 0) = 0 AND COALESCE(project_id, 0) <> 0) OR " +
				"(COALESCE(organization_id, 0) <> 0 AND COALESCE(project_id, 0) = 0)",
		).
		Count(&partiallyScoped).Error; err != nil {
		return fmt.Errorf("validate legacy ticket scope: %w", err)
	}
	if partiallyScoped != 0 {
		return fmt.Errorf(
			"project scope cutover found %d partially scoped tickets",
			partiallyScoped,
		)
	}
	var tickets []models.Ticket
	if err := tx.Unscoped().
		Where(
			"COALESCE(organization_id, 0) = 0 AND COALESCE(project_id, 0) = 0",
		).
		Order("created_at ASC, id ASC").
		Find(&tickets).Error; err != nil {
		return fmt.Errorf("load tickets for project backfill: %w", err)
	}
	queueByKey := map[models.QueueKey]*models.Queue{
		defaultQueue.Key: defaultQueue,
	}
	sequence := project.TicketSequence
	for index := range tickets {
		ticket := &tickets[index]
		queue := defaultQueue
		if ticket.ProjectID == project.ID && ticket.QueueID != 0 {
			var existingQueue models.Queue
			if err := tx.Where(
				"id = ? AND project_id = ?",
				ticket.QueueID,
				project.ID,
			).First(&existingQueue).Error; err == nil {
				queue = &existingQueue
				queueByKey[existingQueue.Key] = &existingQueue
			}
		}
		customFields := ticket.CustomFields.Data()
		if rawQueue, ok := customFields["queue"].(string); ok &&
			strings.TrimSpace(rawQueue) != "" {
			key := normalizedMigratedQueueKey(rawQueue)
			resolved, exists := queueByKey[key]
			if !exists {
				candidate := models.Queue{
					ProjectID:   project.ID,
					Key:         key,
					Name:        strings.TrimSpace(rawQueue),
					Description: "由存量工单 custom_fields.queue 一次性迁移",
					Status:      models.QueueStatusActive,
				}
				if err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{
						{Name: "project_id"},
						{Name: "key"},
					},
					DoNothing: true,
				}).Create(&candidate).Error; err != nil {
					return fmt.Errorf("create migrated queue %q: %w", key, err)
				}
				if err := tx.Where(
					"project_id = ? AND key = ?",
					project.ID,
					key,
				).First(&candidate).Error; err != nil {
					return fmt.Errorf("load migrated queue %q: %w", key, err)
				}
				resolved = &candidate
				queueByKey[key] = resolved
			}
			queue = resolved
			delete(customFields, "queue")
		}
		publicID := strings.TrimSpace(ticket.PublicID)
		if publicID == "" ||
			strings.HasPrefix(publicID, legacyTicketPublicIDPrefix) {
			generated, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("generate ticket UUIDv7: %w", err)
			}
			publicID = generated.String()
		}
		sequence++
		updates := map[string]any{
			"public_id":               publicID,
			"organization_id":         organizationID,
			"project_id":              project.ID,
			"queue_id":                queue.ID,
			"request_type_version_id": bootstrapRequestTypeID(ticket.Type),
			"workflow_version_id":     bootstrapWorkflow,
			"ticket_number":           fmt.Sprintf("%s-%d", project.Key, sequence),
			"custom_fields":           datatypes.NewJSONType(customFields),
		}
		// Use the physical migration table so GORM's runtime `<-:create`
		// protection on PublicID cannot suppress this one-time trusted backfill.
		result := tx.Unscoped().Table("tickets").
			Where(
				"id = ? AND COALESCE(organization_id, 0) = 0 AND COALESCE(project_id, 0) = 0",
				ticket.ID,
			).
			Updates(updates)
		if result.Error != nil {
			return fmt.Errorf(
				"backfill ticket %d project scope: %w",
				ticket.ID,
				result.Error,
			)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf(
				"backfill ticket %d project scope changed %d rows",
				ticket.ID,
				result.RowsAffected,
			)
		}
	}
	result := tx.Model(&models.Project{}).
		Where("id = ?", project.ID).
		UpdateColumn("ticket_sequence", sequence)
	if result.Error != nil {
		return fmt.Errorf(
			"update default project ticket sequence: %w",
			result.Error,
		)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf(
			"update default project ticket sequence changed %d rows",
			result.RowsAffected,
		)
	}
	project.TicketSequence = sequence
	return enforceTicketProjectColumnsNotNull(tx)
}

func enforceTicketProjectColumnsNotNull(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	columns := []string{
		"public_id",
		"organization_id",
		"project_id",
		"queue_id",
		"request_type_version_id",
		"workflow_version_id",
	}
	for _, columnName := range columns {
		if !tx.Migrator().HasColumn(&models.Ticket{}, columnName) {
			return fmt.Errorf(
				"ticket project contract requires tickets.%s",
				columnName,
			)
		}
	}
	nullable, err := postgresNullableProjectColumns(
		tx,
		[]string{"tickets"},
		columns,
	)
	if err != nil {
		return err
	}
	var alterations []string
	for _, columnName := range columns {
		if !nullable["tickets."+columnName] {
			continue
		}
		quotedColumn, err := quoteProjectContractColumn(columnName)
		if err != nil {
			return err
		}
		alterations = append(
			alterations,
			"ALTER COLUMN "+quotedColumn+" SET NOT NULL",
		)
	}
	if len(alterations) == 0 {
		return nil
	}
	if err := tx.Exec(
		"ALTER TABLE tickets " + strings.Join(alterations, ", "),
	).Error; err != nil {
		return fmt.Errorf("enforce ticket project scope columns: %w", err)
	}
	return nil
}

type postgresNullableProjectColumn struct {
	TableName  string `gorm:"column:table_name"`
	ColumnName string `gorm:"column:column_name"`
}

func postgresNullableProjectColumns(
	tx *gorm.DB,
	tableNames []string,
	columnNames []string,
) (map[string]bool, error) {
	var rows []postgresNullableProjectColumn
	if err := tx.Raw(`
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = CURRENT_SCHEMA()
		  AND table_name IN ?
		  AND column_name IN ?
		  AND is_nullable = 'YES'
	`, tableNames, columnNames).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read nullable project columns: %w", err)
	}
	nullable := make(map[string]bool, len(rows))
	for _, row := range rows {
		nullable[row.TableName+"."+row.ColumnName] = true
	}
	return nullable, nil
}

func quoteProjectContractColumn(columnName string) (string, error) {
	switch columnName {
	case "organization_id",
		"project_id",
		"public_id",
		"queue_id",
		"request_type_version_id",
		"workflow_version_id":
		return quoteStaticProjectRLSIdentifier(columnName), nil
	default:
		return "", fmt.Errorf(
			"project contract column %q is not allowlisted",
			columnName,
		)
	}
}

func bootstrapRequestTypeID(ticketType models.TicketType) string {
	switch ticketType {
	case models.TicketTypeIncident:
		return bootstrapRequestIncident
	case models.TicketTypeProblem:
		return bootstrapRequestProblem
	case models.TicketTypeChange:
		return bootstrapRequestChange
	case models.TicketTypeComplaint:
		return bootstrapRequestComplaint
	case models.TicketTypeConsultation:
		return bootstrapRequestConsultation
	default:
		return bootstrapRequestRequest
	}
}

// backfillLegacyProjectOwnedRows runs only for rows that predate explicit
// project columns. It never rewrites a non-zero scope, so rerunning migration
// cannot pull data from a newly created project back into DEFAULT.
func backfillLegacyProjectOwnedRows(
	tx *gorm.DB,
	organizationID uint,
	projectID uint,
) error {
	if organizationID == 0 || projectID == 0 {
		return errors.New("legacy project-owned row backfill requires scope")
	}
	scopedModels := []any{
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
		&models.TicketLease{},
		&models.PolicyDecision{},
		&models.IdempotencyRecord{},
		&models.DomainEvent{},
		&models.OutboxDelivery{},
		&models.Notification{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.AutomationRule{},
		&models.AutomationLog{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
		&models.AgentTask{},
		&models.AgentMessage{},
		&models.AgentArtifact{},
		&models.AgentTaskStatusHistory{},
		&models.AgentTaskEvent{},
		&models.AgentPushNotificationConfig{},
	}
	for _, scopedModel := range scopedModels {
		if !tx.Migrator().HasTable(scopedModel) {
			continue
		}
		if !tx.Migrator().HasColumn(scopedModel, "organization_id") ||
			!tx.Migrator().HasColumn(scopedModel, "project_id") {
			return fmt.Errorf(
				"legacy project-owned table %T is missing explicit scope columns",
				scopedModel,
			)
		}
		var partiallyScoped int64
		if err := tx.Unscoped().
			Model(scopedModel).
			Where(
				"(COALESCE(organization_id, 0) = 0 AND COALESCE(project_id, 0) <> 0) OR " +
					"(COALESCE(organization_id, 0) <> 0 AND COALESCE(project_id, 0) = 0)",
			).
			Count(&partiallyScoped).Error; err != nil {
			return fmt.Errorf(
				"validate legacy project-owned scope for %T: %w",
				scopedModel,
				err,
			)
		}
		if partiallyScoped != 0 {
			return fmt.Errorf(
				"project scope cutover found %d partially scoped rows for %T",
				partiallyScoped,
				scopedModel,
			)
		}
		result := tx.Unscoped().
			Model(scopedModel).
			Where(
				"COALESCE(organization_id, 0) = 0 AND COALESCE(project_id, 0) = 0",
			).
			Updates(map[string]any{
				"organization_id": organizationID,
				"project_id":      projectID,
			})
		if result.Error != nil {
			return fmt.Errorf(
				"backfill legacy project-owned rows for %T: %w",
				scopedModel,
				result.Error,
			)
		}
	}
	return nil
}

func normalizedMigratedQueueKey(raw string) models.QueueKey {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	lastSeparator := false
	for _, character := range normalized {
		valid := character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' ||
			character == '_' ||
			character == '-'
		if valid {
			builder.WriteRune(character)
			lastSeparator = false
			continue
		}
		if !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	candidate := strings.Trim(builder.String(), "-.")
	if len(candidate) > models.QueueKeyMaxLength {
		candidate = strings.Trim(candidate[:models.QueueKeyMaxLength], "-.")
	}
	key := models.QueueKey(candidate)
	if key.Validate() == nil {
		return key
	}
	digest := sha256.Sum256([]byte(raw))
	return models.QueueKey(fmt.Sprintf("migrated-%x", digest[:6]))
}

func backfillDefaultProjectMemberships(
	tx *gorm.DB,
	scope models.ProjectScope,
	writer ProjectScopeMembershipWriter,
) error {
	// A fresh schema already has only platform_role and no users yet. Legacy
	// project duties can be recovered only while the destructive users.role
	// column still exists.
	hasLegacyRole, err := hasExactDatabaseColumn(tx, "users", "role")
	if err != nil {
		return err
	}
	if !hasLegacyRole {
		return nil
	}
	type legacyProjectScopeUser struct {
		ID           uint
		Role         string
		PlatformRole models.PlatformRole
		Status       models.UserStatus
		DeletedAt    gorm.DeletedAt
	}
	var legacyUsers []legacyProjectScopeUser
	if err := tx.Unscoped().
		Select("id", "role", "platform_role", "status", "deleted_at").
		Where("status = ? AND deleted_at IS NULL", models.UserStatusActive).
		Order("id ASC").
		Table("users").
		Find(&legacyUsers).Error; err != nil {
		return fmt.Errorf("load users for default project membership: %w", err)
	}
	if len(legacyUsers) > 0 && writer == nil {
		return errors.New(
			"audited project scope membership writer is required for legacy users",
		)
	}
	migrationContext := tx.Statement.Context
	if migrationContext == nil {
		migrationContext = context.Background()
	}
	for i := range legacyUsers {
		role, err := defaultProjectRoleForLegacyUser(legacyUsers[i].Role)
		if err != nil {
			return fmt.Errorf("user %d: %w", legacyUsers[i].ID, err)
		}
		var existing models.ProjectMembership
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"project_id = ? AND user_id = ?",
				scope.ProjectID,
				legacyUsers[i].ID,
			).
			First(&existing)
		switch {
		case query.Error == nil:
			// Preserve every explicit role or activation change made after the
			// initial migration. Reruns never reinterpret global roles.
			continue
		case !errors.Is(query.Error, gorm.ErrRecordNotFound):
			return fmt.Errorf(
				"load existing default project membership for user %d: %w",
				legacyUsers[i].ID,
				query.Error,
			)
		}
		user := models.User{
			ID:           legacyUsers[i].ID,
			PlatformRole: legacyUsers[i].PlatformRole,
			Status:       legacyUsers[i].Status,
		}
		if !user.PlatformRole.IsValid() {
			user.PlatformRole = models.PlatformRoleMember
		}
		user.DeletedAt = legacyUsers[i].DeletedAt
		if err := writer(
			migrationContext,
			tx,
			user,
			scope,
			role,
		); err != nil {
			return fmt.Errorf(
				"backfill audited default project membership for user %d: %w",
				legacyUsers[i].ID,
				err,
			)
		}
	}
	return nil
}

func defaultProjectRoleForLegacyUser(role string) (models.ProjectRole, error) {
	switch strings.TrimSpace(role) {
	case "admin":
		return models.ProjectRoleAdmin, nil
	case "supervisor":
		return models.ProjectRoleManager, nil
	case "agent":
		return models.ProjectRoleAgent, nil
	case "customer":
		return models.ProjectRoleRequester, nil
	default:
		return "", fmt.Errorf("unsupported human role %q", role)
	}
}

func backfillDefaultProjectPrincipalGrants(tx *gorm.DB, projectID uint) error {
	var principals []models.ServicePrincipal
	if err := tx.Unscoped().
		Select("id", "scopes").
		Order("id ASC").
		Find(&principals).Error; err != nil {
		return fmt.Errorf("load service principals for default project grants: %w", err)
	}
	grants := make([]models.ProjectPrincipalGrant, 0, len(principals))
	for i := range principals {
		scopes, err := normalizedProjectGrantScopes(principals[i].Scopes)
		if err != nil {
			return fmt.Errorf("service principal %q: %w", principals[i].ID, err)
		}
		grants = append(grants, models.ProjectPrincipalGrant{
			ProjectID:          projectID,
			ServicePrincipalID: principals[i].ID,
			Role:               models.ProjectRoleAgent,
			Scopes:             scopes,
			IsActive:           true,
		})
	}
	if len(grants) == 0 {
		return nil
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "project_id"},
			{Name: "service_principal_id"},
		},
		DoNothing: true,
	}).CreateInBatches(grants, projectScopeBackfillBatchSize).Error; err != nil {
		return fmt.Errorf("backfill default project principal grants: %w", err)
	}
	return nil
}

func normalizedProjectGrantScopes(scopes datatypes.JSON) (datatypes.JSON, error) {
	if len(scopes) == 0 || string(scopes) == "null" {
		return datatypes.JSON(`[]`), nil
	}
	var decoded []string
	if err := json.Unmarshal(scopes, &decoded); err != nil {
		return nil, fmt.Errorf("decode scopes: %w", err)
	}
	if decoded == nil {
		decoded = []string{}
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("encode scopes: %w", err)
	}
	return datatypes.JSON(normalized), nil
}
