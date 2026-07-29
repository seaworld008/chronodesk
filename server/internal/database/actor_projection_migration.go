package database

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/safeconv"
	"gorm.io/gorm"
)

const actorProjectionSystemID = "chronodesk"

type actorProjectionEvidence struct {
	ActorType          models.ActorType
	ActorID            string
	HumanUserID        *uint
	ServicePrincipalID *string
	SystemEvidence     bool
}

type resolvedActorProjection struct {
	ActorType          models.ActorType
	ActorID            string
	HumanUserID        *uint
	ServicePrincipalID *string
}

type actorProjectionCatalog struct {
	users      map[uint]struct{}
	principals map[string]struct{}
}

// MigrateActorProjections closes every business ActorRef and makes the human
// foreign keys true nullable projections. It never interprets a legacy human
// foreign key as evidence of a service principal. Explicit service-principal
// actor fields or a service-principal foreign key are required.
//
// Run this migration after model AutoMigrate and before history-event linking,
// because event-link validation treats ActorRef as authoritative.
func MigrateActorProjections(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	requiredTables := []any{
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.TicketHistory{},
	}
	for _, model := range requiredTables {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("actor projection migration requires table %q", tableNameForActorProjection(model))
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := makeHumanProjectionColumnsNullable(tx); err != nil {
			return err
		}
		catalog, err := loadActorProjectionCatalog(tx)
		if err != nil {
			return err
		}
		if err := backfillTicketActorProjections(tx, catalog); err != nil {
			return err
		}
		if err := backfillCommentActorProjections(tx, catalog); err != nil {
			return err
		}
		if err := backfillAttachmentActorProjections(tx, catalog); err != nil {
			return err
		}
		if err := backfillHistoryActorProjections(tx, catalog); err != nil {
			return err
		}
		if err := validatePersistedActorProjections(tx, catalog); err != nil {
			return err
		}
		if err := dropServicePrincipalHumanProjection(tx); err != nil {
			return err
		}
		if err := installActorProjectionConstraints(tx); err != nil {
			return err
		}
		return nil
	})
}

func tableNameForActorProjection(model any) string {
	switch model.(type) {
	case *models.User:
		return "users"
	case *models.ServicePrincipal:
		return "service_principals"
	case *models.Ticket:
		return "tickets"
	case *models.TicketComment:
		return "ticket_comments"
	case *models.TicketAttachment:
		return "ticket_attachments"
	case *models.TicketHistory:
		return "ticket_histories"
	default:
		return "unknown"
	}
}

func makeHumanProjectionColumnsNullable(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	for _, statement := range []string{
		`ALTER TABLE tickets ALTER COLUMN created_by_id DROP NOT NULL`,
		`ALTER TABLE ticket_comments ALTER COLUMN user_id DROP NOT NULL`,
		`ALTER TABLE ticket_attachments ALTER COLUMN uploaded_by DROP NOT NULL`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("make human actor projection nullable: %w", err)
		}
	}
	return nil
}

func loadActorProjectionCatalog(tx *gorm.DB) (*actorProjectionCatalog, error) {
	var userIDs []uint
	if err := tx.Unscoped().Model(&models.User{}).Pluck("id", &userIDs).Error; err != nil {
		return nil, fmt.Errorf("load human actor catalog: %w", err)
	}
	var principalIDs []string
	if err := tx.Unscoped().Model(&models.ServicePrincipal{}).Pluck("id", &principalIDs).Error; err != nil {
		return nil, fmt.Errorf("load service-principal actor catalog: %w", err)
	}
	catalog := &actorProjectionCatalog{
		users:      make(map[uint]struct{}, len(userIDs)),
		principals: make(map[string]struct{}, len(principalIDs)),
	}
	for _, id := range userIDs {
		catalog.users[id] = struct{}{}
	}
	for _, id := range principalIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			catalog.principals[id] = struct{}{}
		}
	}
	return catalog, nil
}

func backfillTicketActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	var tickets []models.Ticket
	if err := tx.Unscoped().
		Select(
			"id",
			"created_by_id",
			"created_by_actor_type",
			"created_by_actor_id",
			"created_by_service_principal_id",
			"assigned_to_id",
			"assigned_to_actor_type",
			"assigned_to_actor_id",
			"assigned_to_service_principal_id",
		).
		Order("id ASC").
		Find(&tickets).Error; err != nil {
		return fmt.Errorf("load ticket actor projections: %w", err)
	}
	for i := range tickets {
		ticket := &tickets[i]
		creatorEvidence := actorProjectionEvidence{
			ActorType:          ticket.CreatedByActorType,
			ActorID:            ticket.CreatedByActorID,
			HumanUserID:        ticket.CreatedByID,
			ServicePrincipalID: ticket.CreatedByServicePrincipalID,
		}
		creator, err := resolveActorProjection(catalog, creatorEvidence)
		if err != nil {
			return fmt.Errorf("ticket %d creator: %w", ticket.ID, err)
		}
		assignmentEvidence := actorProjectionEvidence{
			ActorType:          ticket.AssignedToActorType,
			ActorID:            ticket.AssignedToActorID,
			HumanUserID:        ticket.AssignedToID,
			ServicePrincipalID: ticket.AssignedToServicePrincipalID,
		}
		assignment, assigned, err := resolveOptionalActorProjection(catalog, assignmentEvidence)
		if err != nil {
			return fmt.Errorf("ticket %d assignee: %w", ticket.ID, err)
		}
		if actorProjectionEqualsEvidence(creator, creatorEvidence) &&
			(!assigned || actorProjectionEqualsEvidence(assignment, assignmentEvidence)) {
			continue
		}
		updates := map[string]any{
			"created_by_actor_type":           creator.ActorType,
			"created_by_actor_id":             creator.ActorID,
			"created_by_id":                   creator.HumanUserID,
			"created_by_service_principal_id": creator.ServicePrincipalID,
		}
		if assigned {
			updates["assigned_to_actor_type"] = assignment.ActorType
			updates["assigned_to_actor_id"] = assignment.ActorID
			updates["assigned_to_id"] = assignment.HumanUserID
			updates["assigned_to_service_principal_id"] = assignment.ServicePrincipalID
		} else {
			updates["assigned_to_actor_type"] = ""
			updates["assigned_to_actor_id"] = ""
			updates["assigned_to_id"] = nil
			updates["assigned_to_service_principal_id"] = nil
		}
		if err := tx.Unscoped().Model(&models.Ticket{}).
			Where("id = ?", ticket.ID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("backfill ticket %d actor projections: %w", ticket.ID, err)
		}
	}
	return nil
}

func backfillCommentActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	var comments []models.TicketComment
	if err := tx.Unscoped().
		Select("id", "user_id", "actor_type", "actor_id", "service_principal_id", "type").
		Order("id ASC").
		Find(&comments).Error; err != nil {
		return fmt.Errorf("load comment actor projections: %w", err)
	}
	for i := range comments {
		comment := &comments[i]
		evidence := actorProjectionEvidence{
			ActorType:          comment.ActorType,
			ActorID:            comment.ActorID,
			HumanUserID:        comment.UserID,
			ServicePrincipalID: comment.ServicePrincipalID,
			SystemEvidence:     comment.Type == models.CommentTypeSystem,
		}
		resolved, err := resolveActorProjection(catalog, evidence)
		if err != nil {
			return fmt.Errorf("ticket comment %d: %w", comment.ID, err)
		}
		if actorProjectionEqualsEvidence(resolved, evidence) {
			continue
		}
		if err := updateActorProjection(
			tx,
			&models.TicketComment{},
			comment.ID,
			"user_id",
			"service_principal_id",
			resolved,
		); err != nil {
			return fmt.Errorf("backfill ticket comment %d actor projection: %w", comment.ID, err)
		}
	}
	return nil
}

func backfillAttachmentActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	var attachments []models.TicketAttachment
	if err := tx.Unscoped().
		Select("id", "uploaded_by", "actor_type", "actor_id", "service_principal_id").
		Order("id ASC").
		Find(&attachments).Error; err != nil {
		return fmt.Errorf("load attachment actor projections: %w", err)
	}
	for i := range attachments {
		attachment := &attachments[i]
		evidence := actorProjectionEvidence{
			ActorType:          attachment.ActorType,
			ActorID:            attachment.ActorID,
			HumanUserID:        attachment.UploadedBy,
			ServicePrincipalID: attachment.ServicePrincipalID,
		}
		resolved, err := resolveActorProjection(catalog, evidence)
		if err != nil {
			return fmt.Errorf("ticket attachment %d: %w", attachment.ID, err)
		}
		if actorProjectionEqualsEvidence(resolved, evidence) {
			continue
		}
		if err := updateActorProjection(
			tx,
			&models.TicketAttachment{},
			attachment.ID,
			"uploaded_by",
			"service_principal_id",
			resolved,
		); err != nil {
			return fmt.Errorf("backfill ticket attachment %d actor projection: %w", attachment.ID, err)
		}
	}
	return nil
}

func backfillHistoryActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	var histories []models.TicketHistory
	if err := tx.
		Select("id", "user_id", "actor_type", "actor_id", "service_principal_id", "action", "is_system").
		Order("id ASC").
		Find(&histories).Error; err != nil {
		return fmt.Errorf("load history actor projections: %w", err)
	}
	for i := range histories {
		history := &histories[i]
		evidence := actorProjectionEvidence{
			ActorType:          history.ActorType,
			ActorID:            history.ActorID,
			HumanUserID:        history.UserID,
			ServicePrincipalID: history.ServicePrincipalID,
			SystemEvidence: history.IsSystem ||
				history.Action == models.HistoryActionSystem,
		}
		resolved, err := resolveActorProjection(catalog, evidence)
		if err != nil {
			return fmt.Errorf("ticket history %d: %w", history.ID, err)
		}
		if actorProjectionEqualsEvidence(resolved, evidence) {
			continue
		}
		if err := updateActorProjection(
			tx,
			&models.TicketHistory{},
			history.ID,
			"user_id",
			"service_principal_id",
			resolved,
		); err != nil {
			return fmt.Errorf("backfill ticket history %d actor projection: %w", history.ID, err)
		}
	}
	return nil
}

func updateActorProjection(
	tx *gorm.DB,
	model any,
	id uint,
	humanColumn string,
	principalColumn string,
	resolved resolvedActorProjection,
) error {
	return tx.Model(model).Where("id = ?", id).Updates(map[string]any{
		"actor_type":    resolved.ActorType,
		"actor_id":      resolved.ActorID,
		humanColumn:     resolved.HumanUserID,
		principalColumn: resolved.ServicePrincipalID,
	}).Error
}

func resolveOptionalActorProjection(
	catalog *actorProjectionCatalog,
	evidence actorProjectionEvidence,
) (resolvedActorProjection, bool, error) {
	if strings.TrimSpace(string(evidence.ActorType)) == "" &&
		strings.TrimSpace(evidence.ActorID) == "" &&
		normalizedUintPointer(evidence.HumanUserID) == nil &&
		normalizedStringPointer(evidence.ServicePrincipalID) == nil {
		return resolvedActorProjection{}, false, nil
	}
	resolved, err := resolveActorProjection(catalog, evidence)
	return resolved, err == nil, err
}

func resolveActorProjection(
	catalog *actorProjectionCatalog,
	evidence actorProjectionEvidence,
) (resolvedActorProjection, error) {
	actorType := models.ActorType(strings.TrimSpace(string(evidence.ActorType)))
	actorID := strings.TrimSpace(evidence.ActorID)
	humanID := normalizedUintPointer(evidence.HumanUserID)
	principalID := normalizedStringPointer(evidence.ServicePrincipalID)

	if evidence.SystemEvidence &&
		(actorType == "" || actorType == models.ActorTypeHuman) &&
		actorID == "" &&
		principalID == nil {
		actorType = models.ActorTypeSystem
		humanID = nil
	} else if evidence.SystemEvidence &&
		actorType == models.ActorTypeHuman {
		return resolvedActorProjection{}, errors.New("system evidence conflicts with a human actor")
	}
	if actorType == "" {
		switch {
		case actorID != "":
			return resolvedActorProjection{}, errors.New("actor_id exists without actor_type")
		case principalID != nil:
			actorType = models.ActorTypeServicePrincipal
			actorID = *principalID
		case humanID != nil:
			actorType = models.ActorTypeHuman
			actorID = strconv.FormatUint(uint64(*humanID), 10)
		case evidence.SystemEvidence:
			actorType = models.ActorTypeSystem
		default:
			return resolvedActorProjection{}, errors.New("actor identity has no provable source")
		}
	}

	switch actorType {
	case models.ActorTypeHuman:
		if principalID != nil {
			return resolvedActorProjection{}, errors.New("human actor carries a service-principal projection")
		}
		if humanID == nil {
			return resolvedActorProjection{}, errors.New("human actor has no user projection")
		}
		if _, exists := catalog.users[*humanID]; !exists {
			return resolvedActorProjection{}, fmt.Errorf("human user %d does not exist", *humanID)
		}
		expectedActorID := strconv.FormatUint(uint64(*humanID), 10)
		if actorID == "" {
			actorID = expectedActorID
		}
		parsedActorID, err := safeconv.ParsePositiveUint(actorID)
		if err != nil || parsedActorID != *humanID {
			return resolvedActorProjection{}, fmt.Errorf(
				"human actor_id %q conflicts with user projection %d",
				actorID,
				*humanID,
			)
		}
		return resolvedActorProjection{
			ActorType:   models.ActorTypeHuman,
			ActorID:     expectedActorID,
			HumanUserID: humanID,
		}, nil

	case models.ActorTypeServicePrincipal:
		if actorID == "" && principalID != nil {
			actorID = *principalID
		}
		if principalID == nil && actorID != "" {
			principalID = &actorID
		}
		if actorID == "" || principalID == nil || actorID != *principalID {
			return resolvedActorProjection{}, errors.New("service-principal actor fields conflict")
		}
		if _, exists := catalog.principals[actorID]; !exists {
			return resolvedActorProjection{}, fmt.Errorf("service principal %q does not exist", actorID)
		}
		return resolvedActorProjection{
			ActorType:          models.ActorTypeServicePrincipal,
			ActorID:            actorID,
			ServicePrincipalID: principalID,
		}, nil

	case models.ActorTypeSystem:
		if actorID == "" {
			actorID = actorProjectionSystemID
		}
		return resolvedActorProjection{
			ActorType: models.ActorTypeSystem,
			ActorID:   actorID,
		}, nil

	default:
		return resolvedActorProjection{}, fmt.Errorf("unsupported actor_type %q", actorType)
	}
}

func normalizedUintPointer(value *uint) *uint {
	if value == nil || *value == 0 {
		return nil
	}
	result := *value
	return &result
}

func normalizedStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := strings.TrimSpace(*value)
	if result == "" {
		return nil
	}
	return &result
}

func validatePersistedActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	if err := validateTicketActorProjections(tx, catalog); err != nil {
		return err
	}
	validations := []struct {
		name string
		load func() ([]actorProjectionEvidence, error)
	}{
		{
			name: "ticket_comments",
			load: func() ([]actorProjectionEvidence, error) {
				var rows []models.TicketComment
				err := tx.Unscoped().
					Select("user_id", "actor_type", "actor_id", "service_principal_id", "type").
					Find(&rows).Error
				result := make([]actorProjectionEvidence, 0, len(rows))
				for i := range rows {
					result = append(result, actorProjectionEvidence{
						ActorType: rows[i].ActorType, ActorID: rows[i].ActorID,
						HumanUserID: rows[i].UserID, ServicePrincipalID: rows[i].ServicePrincipalID,
						SystemEvidence: rows[i].Type == models.CommentTypeSystem,
					})
				}
				return result, err
			},
		},
		{
			name: "ticket_attachments",
			load: func() ([]actorProjectionEvidence, error) {
				var rows []models.TicketAttachment
				err := tx.Unscoped().
					Select("uploaded_by", "actor_type", "actor_id", "service_principal_id").
					Find(&rows).Error
				result := make([]actorProjectionEvidence, 0, len(rows))
				for i := range rows {
					result = append(result, actorProjectionEvidence{
						ActorType: rows[i].ActorType, ActorID: rows[i].ActorID,
						HumanUserID: rows[i].UploadedBy, ServicePrincipalID: rows[i].ServicePrincipalID,
					})
				}
				return result, err
			},
		},
		{
			name: "ticket_histories",
			load: func() ([]actorProjectionEvidence, error) {
				var rows []models.TicketHistory
				err := tx.
					Select("user_id", "actor_type", "actor_id", "service_principal_id", "action", "is_system").
					Find(&rows).Error
				result := make([]actorProjectionEvidence, 0, len(rows))
				for i := range rows {
					result = append(result, actorProjectionEvidence{
						ActorType: rows[i].ActorType, ActorID: rows[i].ActorID,
						HumanUserID: rows[i].UserID, ServicePrincipalID: rows[i].ServicePrincipalID,
						SystemEvidence: rows[i].IsSystem || rows[i].Action == models.HistoryActionSystem,
					})
				}
				return result, err
			},
		},
	}
	for _, validation := range validations {
		rows, err := validation.load()
		if err != nil {
			return fmt.Errorf("validate %s actor projections: %w", validation.name, err)
		}
		for index := range rows {
			resolved, err := resolveActorProjection(catalog, rows[index])
			if err != nil {
				return fmt.Errorf("validate %s row %d: %w", validation.name, index+1, err)
			}
			if !actorProjectionEqualsEvidence(resolved, rows[index]) {
				return fmt.Errorf("validate %s row %d: persisted actor projection is not canonical", validation.name, index+1)
			}
		}
	}
	return nil
}

func validateTicketActorProjections(tx *gorm.DB, catalog *actorProjectionCatalog) error {
	var tickets []models.Ticket
	if err := tx.Unscoped().
		Select(
			"created_by_id", "created_by_actor_type", "created_by_actor_id",
			"created_by_service_principal_id", "assigned_to_id", "assigned_to_actor_type",
			"assigned_to_actor_id", "assigned_to_service_principal_id",
		).
		Find(&tickets).Error; err != nil {
		return fmt.Errorf("validate ticket actor projections: %w", err)
	}
	for index := range tickets {
		creatorEvidence := actorProjectionEvidence{
			ActorType: tickets[index].CreatedByActorType, ActorID: tickets[index].CreatedByActorID,
			HumanUserID:        tickets[index].CreatedByID,
			ServicePrincipalID: tickets[index].CreatedByServicePrincipalID,
		}
		creator, err := resolveActorProjection(catalog, creatorEvidence)
		if err != nil {
			return fmt.Errorf("validate ticket row %d creator projection: %w", index+1, err)
		}
		if !actorProjectionEqualsEvidence(creator, creatorEvidence) {
			return fmt.Errorf("validate ticket row %d creator projection is not canonical", index+1)
		}
		assignmentEvidence := actorProjectionEvidence{
			ActorType: tickets[index].AssignedToActorType, ActorID: tickets[index].AssignedToActorID,
			HumanUserID:        tickets[index].AssignedToID,
			ServicePrincipalID: tickets[index].AssignedToServicePrincipalID,
		}
		assignment, assigned, err := resolveOptionalActorProjection(catalog, assignmentEvidence)
		if err != nil {
			return fmt.Errorf("validate ticket row %d assignee projection: %w", index+1, err)
		}
		if assigned && !actorProjectionEqualsEvidence(assignment, assignmentEvidence) {
			return fmt.Errorf("validate ticket row %d assignee projection is not canonical", index+1)
		}
	}
	return nil
}

func actorProjectionEqualsEvidence(
	resolved resolvedActorProjection,
	evidence actorProjectionEvidence,
) bool {
	return resolved.ActorType == evidence.ActorType &&
		resolved.ActorID == strings.TrimSpace(evidence.ActorID) &&
		uintPointersEqual(resolved.HumanUserID, normalizedUintPointer(evidence.HumanUserID)) &&
		stringPointersEqual(resolved.ServicePrincipalID, normalizedStringPointer(evidence.ServicePrincipalID))
}

func uintPointersEqual(left, right *uint) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func stringPointersEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func dropServicePrincipalHumanProjection(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn("service_principals", "compatibility_user_id") {
		return nil
	}
	var statement string
	switch tx.Dialector.Name() {
	case "postgres":
		statement = `ALTER TABLE service_principals DROP COLUMN IF EXISTS compatibility_user_id`
	case "sqlite":
		// GORM's SQLite migrator requires the removed field to remain in the
		// model schema and panics after that field has intentionally been
		// deleted. SQLite 3.35+ supports the direct, transactional statement.
		statement = `ALTER TABLE service_principals DROP COLUMN compatibility_user_id`
	default:
		return fmt.Errorf(
			"drop service principal human projection is unsupported for database dialect %q",
			tx.Dialector.Name(),
		)
	}
	if err := tx.Exec(statement).Error; err != nil {
		return fmt.Errorf("drop service principal human projection: %w", err)
	}
	return nil
}

func installActorProjectionConstraints(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "postgres":
		return installPostgresActorProjectionConstraints(tx)
	case "sqlite":
		return installSQLiteActorProjectionConstraints(tx)
	default:
		return fmt.Errorf("actor projection constraints are unsupported for database dialect %q", tx.Dialector.Name())
	}
}

func installPostgresActorProjectionConstraints(tx *gorm.DB) error {
	constraints := []struct {
		table      string
		name       string
		expression string
	}{
		{
			table: "tickets", name: "chk_tickets_creator_actor_projection",
			expression: actorProjectionCheckSQL(
				"created_by_actor_type", "created_by_actor_id",
				"created_by_id", "created_by_service_principal_id",
			),
		},
		{
			table: "tickets", name: "chk_tickets_assignee_actor_projection",
			expression: optionalActorProjectionCheckSQL(
				"assigned_to_actor_type", "assigned_to_actor_id",
				"assigned_to_id", "assigned_to_service_principal_id",
			),
		},
		{
			table: "ticket_comments", name: "chk_ticket_comments_actor_projection",
			expression: actorProjectionCheckSQL("actor_type", "actor_id", "user_id", "service_principal_id"),
		},
		{
			table: "ticket_attachments", name: "chk_ticket_attachments_actor_projection",
			expression: actorProjectionCheckSQL("actor_type", "actor_id", "uploaded_by", "service_principal_id"),
		},
		{
			table: "ticket_histories", name: "chk_ticket_histories_actor_projection",
			expression: actorProjectionCheckSQL("actor_type", "actor_id", "user_id", "service_principal_id"),
		},
	}
	for _, constraint := range constraints {
		statement := fmt.Sprintf(`
			DO $$
			BEGIN
				IF NOT EXISTS (
					SELECT 1 FROM pg_constraint
					WHERE conname = %s
					  AND conrelid = %s::regclass
				) THEN
					ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);
				END IF;
			END $$`,
			quoteSQLLiteral(constraint.name),
			quoteSQLLiteral(constraint.table),
			constraint.table,
			constraint.name,
			constraint.expression,
		)
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("install PostgreSQL actor projection constraint %s: %w", constraint.name, err)
		}
	}
	return nil
}

func actorProjectionCheckSQL(
	actorTypeColumn string,
	actorIDColumn string,
	humanColumn string,
	principalColumn string,
) string {
	return fmt.Sprintf(`(
		(%[1]s = 'human' AND %[2]s IS NOT NULL AND BTRIM(%[2]s) <> ''
			AND %[3]s IS NOT NULL AND %[4]s IS NULL AND %[2]s = CAST(%[3]s AS TEXT))
		OR
		(%[1]s = 'service_principal' AND %[2]s IS NOT NULL AND BTRIM(%[2]s) <> ''
			AND %[3]s IS NULL AND %[4]s IS NOT NULL AND %[2]s = %[4]s)
		OR
		(%[1]s = 'system' AND %[2]s IS NOT NULL AND BTRIM(%[2]s) <> ''
			AND %[3]s IS NULL AND %[4]s IS NULL)
	)`, actorTypeColumn, actorIDColumn, humanColumn, principalColumn)
}

func optionalActorProjectionCheckSQL(
	actorTypeColumn string,
	actorIDColumn string,
	humanColumn string,
	principalColumn string,
) string {
	return fmt.Sprintf(`(
		(COALESCE(%[1]s, '') = '' AND COALESCE(%[2]s, '') = ''
			AND %[3]s IS NULL AND %[4]s IS NULL)
		OR %s
	)`,
		actorTypeColumn,
		actorIDColumn,
		humanColumn,
		principalColumn,
		actorProjectionCheckSQL(actorTypeColumn, actorIDColumn, humanColumn, principalColumn),
	)
}

func quoteSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func installSQLiteActorProjectionConstraints(tx *gorm.DB) error {
	type triggerSpec struct {
		table      string
		name       string
		expression string
	}
	specs := []triggerSpec{
		{
			table: "tickets", name: "tickets_actor_projection",
			expression: "(" + sqliteActorProjectionExpression(
				"NEW.created_by_actor_type", "NEW.created_by_actor_id",
				"NEW.created_by_id", "NEW.created_by_service_principal_id",
			) + ") AND (" + sqliteOptionalActorProjectionExpression(
				"NEW.assigned_to_actor_type", "NEW.assigned_to_actor_id",
				"NEW.assigned_to_id", "NEW.assigned_to_service_principal_id",
			) + ")",
		},
		{
			table: "ticket_comments", name: "ticket_comments_actor_projection",
			expression: sqliteActorProjectionExpression(
				"NEW.actor_type", "NEW.actor_id", "NEW.user_id", "NEW.service_principal_id",
			),
		},
		{
			table: "ticket_attachments", name: "ticket_attachments_actor_projection",
			expression: sqliteActorProjectionExpression(
				"NEW.actor_type", "NEW.actor_id", "NEW.uploaded_by", "NEW.service_principal_id",
			),
		},
		{
			table: "ticket_histories", name: "ticket_histories_actor_projection",
			expression: sqliteActorProjectionExpression(
				"NEW.actor_type", "NEW.actor_id", "NEW.user_id", "NEW.service_principal_id",
			),
		},
	}
	for _, spec := range specs {
		insertStatement := fmt.Sprintf(`
			CREATE TRIGGER IF NOT EXISTS trg_%s_insert
			BEFORE INSERT ON %s
			WHEN NOT (%s)
			BEGIN
				SELECT RAISE(ABORT, 'invalid actor projection');
			END`,
			spec.name,
			spec.table,
			spec.expression,
		)
		updateStatement := fmt.Sprintf(`
			CREATE TRIGGER IF NOT EXISTS trg_%s_update
			BEFORE UPDATE ON %s
			WHEN NOT (%s)
			BEGIN
				SELECT RAISE(ABORT, 'invalid actor projection');
			END`,
			spec.name,
			spec.table,
			spec.expression,
		)
		for _, statement := range []string{insertStatement, updateStatement} {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("install SQLite actor projection constraint %s: %w", spec.name, err)
			}
		}
	}
	return nil
}

func sqliteActorProjectionExpression(
	actorTypeColumn string,
	actorIDColumn string,
	humanColumn string,
	principalColumn string,
) string {
	return fmt.Sprintf(`(
		(COALESCE(%[1]s, '') = 'human' AND COALESCE(TRIM(%[2]s), '') <> ''
			AND %[3]s IS NOT NULL AND %[4]s IS NULL AND %[2]s = CAST(%[3]s AS TEXT))
		OR
		(COALESCE(%[1]s, '') = 'service_principal' AND COALESCE(TRIM(%[2]s), '') <> ''
			AND %[3]s IS NULL AND %[4]s IS NOT NULL AND %[2]s = %[4]s)
		OR
		(COALESCE(%[1]s, '') = 'system' AND COALESCE(TRIM(%[2]s), '') <> ''
			AND %[3]s IS NULL AND %[4]s IS NULL)
	)`, actorTypeColumn, actorIDColumn, humanColumn, principalColumn)
}

func sqliteOptionalActorProjectionExpression(
	actorTypeColumn string,
	actorIDColumn string,
	humanColumn string,
	principalColumn string,
) string {
	return fmt.Sprintf(`(
		(COALESCE(%[1]s, '') = '' AND COALESCE(%[2]s, '') = ''
			AND %[3]s IS NULL AND %[4]s IS NULL)
		OR %s
	)`,
		actorTypeColumn,
		actorIDColumn,
		humanColumn,
		principalColumn,
		sqliteActorProjectionExpression(actorTypeColumn, actorIDColumn, humanColumn, principalColumn),
	)
}
