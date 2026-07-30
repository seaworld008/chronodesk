package database

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

type projectScopeRequiredTable struct {
	model any
	name  string
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
}

// MigrateProjectScope performs the one-time destructive scope upgrade and
// default authorization backfill for databases created before projects
// existed. The previously unscoped installation becomes one explicit
// Organization / BusinessUnit / Project, and every existing identity receives
// an equivalent default-project grant.
//
// Although this is a one-time upgrade, it is safe to rerun. Composite conflict
// targets preserve any role, scope or activation changes made after the
// initial backfill.
func MigrateProjectScope(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
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
		if err := backfillDefaultProjectMemberships(tx, project.ID); err != nil {
			return err
		}
		return backfillDefaultProjectPrincipalGrants(tx, project.ID)
	})
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
	for _, tableName := range requiredProjectOwnedTableNames {
		quotedTable, err := quoteProjectRLSIdentifier(tableName)
		if err != nil {
			return err
		}
		if err := tx.Exec(fmt.Sprintf(
			"ALTER TABLE %s ALTER COLUMN organization_id SET NOT NULL, ALTER COLUMN project_id SET NOT NULL",
			quotedTable,
		)).Error; err != nil {
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

	var tickets []models.Ticket
	if err := tx.Unscoped().
		Order("created_at ASC, id ASC").
		Find(&tickets).Error; err != nil {
		return fmt.Errorf("load tickets for project backfill: %w", err)
	}
	queueByKey := map[models.QueueKey]*models.Queue{
		defaultQueue.Key: defaultQueue,
	}
	sequence := uint64(0)
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
		if publicID == "" {
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
		if err := tx.Unscoped().Model(&models.Ticket{}).
			Where("id = ?", ticket.ID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("backfill ticket %d project scope: %w", ticket.ID, err)
		}
	}
	if err := tx.Model(&models.Project{}).
		Where("id = ?", project.ID).
		UpdateColumn("ticket_sequence", sequence).Error; err != nil {
		return fmt.Errorf("update default project ticket sequence: %w", err)
	}
	project.TicketSequence = sequence
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(`
			ALTER TABLE tickets
				ALTER COLUMN public_id SET NOT NULL,
				ALTER COLUMN organization_id SET NOT NULL,
				ALTER COLUMN project_id SET NOT NULL,
				ALTER COLUMN queue_id SET NOT NULL,
				ALTER COLUMN request_type_version_id SET NOT NULL,
				ALTER COLUMN workflow_version_id SET NOT NULL
		`).Error; err != nil {
			return fmt.Errorf("enforce ticket project scope columns: %w", err)
		}
	}
	return nil
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
		result := tx.Unscoped().
			Model(scopedModel).
			Where("organization_id = 0 OR project_id = 0").
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

func backfillDefaultProjectMemberships(tx *gorm.DB, projectID uint) error {
	var users []models.User
	if err := tx.Unscoped().
		Select("id", "role").
		Order("id ASC").
		Find(&users).Error; err != nil {
		return fmt.Errorf("load users for default project membership: %w", err)
	}
	memberships := make([]models.ProjectMembership, 0, len(users))
	for i := range users {
		role, err := defaultProjectRoleForUser(users[i].Role)
		if err != nil {
			return fmt.Errorf("user %d: %w", users[i].ID, err)
		}
		memberships = append(memberships, models.ProjectMembership{
			ProjectID: projectID,
			UserID:    users[i].ID,
			Role:      role,
			IsActive:  true,
		})
	}
	if len(memberships) == 0 {
		return nil
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "project_id"},
			{Name: "user_id"},
		},
		DoNothing: true,
	}).CreateInBatches(memberships, projectScopeBackfillBatchSize).Error; err != nil {
		return fmt.Errorf("backfill default project memberships: %w", err)
	}
	return nil
}

func defaultProjectRoleForUser(role models.UserRole) (models.ProjectRole, error) {
	switch role {
	case models.RoleAdmin:
		return models.ProjectRoleAdmin, nil
	case models.RoleSupervisor:
		return models.ProjectRoleManager, nil
	case models.RoleAgent:
		return models.ProjectRoleAgent, nil
	case models.RoleCustomer:
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
