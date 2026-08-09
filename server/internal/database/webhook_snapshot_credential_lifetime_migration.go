package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	webhookSnapshotCredentialLifetimeCheckpointKey      = "20260810_webhook_snapshot_credential_lifetime_v1"
	webhookSnapshotCredentialLifetimeCheckpointVersion  = uint(1)
	webhookSnapshotCredentialLifetimeCheckpointChecksum = "74450a1a183581125f5fea992eda2488333644925fd472d13a753b0c6f09c9de"

	webhookSnapshotCredentialDeadlineIndex = "idx_webhook_snapshot_credential_deadline"
	outboxWebhookSnapshotPairDeadlineIndex = "idx_outbox_webhook_snapshot_pair_deadline"
)

var webhookCredentialConstraintDefinitions = map[string]struct {
	table      string
	expression string
}{
	"chk_projects_status": {
		table: "projects",
		expression: closedVocabularyINConstraintExpression(
			"status",
			models.ProjectStatusValues(),
		),
	},
	"chk_webhook_snapshot_scope": {
		table: "webhook_delivery_snapshots",
		expression: "organization_id > 0 AND project_id > 0 " +
			"AND event_id <> ''",
	},
	"chk_webhook_snapshot_shred_reason": {
		table: "webhook_delivery_snapshots",
		expression: nullableClosedVocabularyConstraintExpression(
			"credential_shred_reason",
			models.WebhookCredentialShredReasonValues(),
		),
	},
	"chk_webhook_snapshot_shred_state": {
		table: "webhook_delivery_snapshots",
		expression: "(credential_shredded_at IS NULL " +
			"AND credential_shred_reason IS NULL) OR " +
			"(credential_shredded_at IS NOT NULL " +
			"AND credential_shred_reason IS NOT NULL " +
			"AND secret IS NOT NULL AND secret = '' " +
			"AND previous_secret IS NOT NULL AND previous_secret = '' " +
			"AND access_token IS NOT NULL AND access_token = '')",
	},
	"chk_outbox_delivery_status": {
		table: "outbox_deliveries",
		expression: closedVocabularyConstraintExpression(
			"status",
			models.OutboxDeliveryStatusValues(),
		),
	},
	"chk_outbox_webhook_expires_at": {
		table: "outbox_deliveries",
		expression: "destination_type <> 'webhook' OR " +
			"(destination_type = 'webhook' AND expires_at IS NOT NULL)",
	},
	"chk_outbox_expired_at": {
		table: "outbox_deliveries",
		expression: "(status = 'expired' AND expired_at IS NOT NULL) OR " +
			"(status <> 'expired' AND expired_at IS NULL)",
	},
	"chk_outbox_expired_webhook": {
		table: "outbox_deliveries",
		expression: "status <> 'expired' OR " +
			"(status = 'expired' AND destination_type = 'webhook')",
	},
}

var webhookCredentialIndexDefinitions = []automationWebhookIndexDefinition{
	{
		name:   "idx_projects_scope_id",
		table:  "projects",
		unique: true,
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "id"},
		},
	},
	{
		name:  webhookSnapshotCredentialDeadlineIndex,
		table: "webhook_delivery_snapshots",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "credential_expires_at"},
			{name: "id"},
		},
	},
	{
		name:  outboxWebhookSnapshotPairDeadlineIndex,
		table: "outbox_deliveries",
		columns: []automationWebhookIndexColumn{
			{name: "organization_id"},
			{name: "project_id"},
			{name: "destination_type"},
			{name: "destination_id"},
			{name: "event_id"},
			{name: "expires_at"},
		},
	},
}

type webhookCredentialSnapshotRow struct {
	ID                    string     `gorm:"column:id"`
	CreatedAt             time.Time  `gorm:"column:created_at"`
	OrganizationID        uint       `gorm:"column:organization_id"`
	ProjectID             uint       `gorm:"column:project_id"`
	EventID               string     `gorm:"column:event_id"`
	Secret                string     `gorm:"column:secret"`
	PreviousSecret        string     `gorm:"column:previous_secret"`
	AccessToken           string     `gorm:"column:access_token"`
	CredentialExpiresAt   *time.Time `gorm:"column:credential_expires_at"`
	CredentialShreddedAt  *time.Time `gorm:"column:credential_shredded_at"`
	CredentialShredReason *string    `gorm:"column:credential_shred_reason"`
}

type webhookCredentialDeliveryRow struct {
	ID              string                      `gorm:"column:id"`
	OrganizationID  uint                        `gorm:"column:organization_id"`
	ProjectID       uint                        `gorm:"column:project_id"`
	EventID         string                      `gorm:"column:event_id"`
	DestinationType string                      `gorm:"column:destination_type"`
	DestinationID   string                      `gorm:"column:destination_id"`
	Status          models.OutboxDeliveryStatus `gorm:"column:status"`
	ExpiresAt       *time.Time                  `gorm:"column:expires_at"`
	ExpiredAt       *time.Time                  `gorm:"column:expired_at"`
}

type webhookCredentialEventRow struct {
	ID             string `gorm:"column:id"`
	OrganizationID uint   `gorm:"column:organization_id"`
	ProjectID      uint   `gorm:"column:project_id"`
}

type webhookCredentialPair struct {
	snapshot webhookCredentialSnapshotRow
	delivery webhookCredentialDeliveryRow
}

// PrepareWebhookSnapshotCredentialLifetimeContract is retained for callers
// compiled against the original foundation seam. It now performs the complete
// cutover: nullable preparation must never commit without final constraints and
// the checkpoint.
func PrepareWebhookSnapshotCredentialLifetimeContract(db *gorm.DB) error {
	return MigrateWebhookSnapshotCredentialLifetimeContract(db)
}

func prepareWebhookSnapshotCredentialLifetimeContractAt(
	db *gorm.DB,
	cutoverAt time.Time,
) error {
	return migrateWebhookSnapshotCredentialLifetimeContractAt(db, cutoverAt)
}

func runWebhookSnapshotCredentialLifetimeCutover(
	db *gorm.DB,
	cutoverAt time.Time,
) error {
	if db == nil {
		return errors.New("webhook credential lifetime database is required")
	}
	hasSnapshots := db.Migrator().HasTable(
		&models.WebhookDeliverySnapshot{},
	)
	hasDeliveries := db.Migrator().HasTable(&models.OutboxDelivery{})
	if !hasSnapshots && !hasDeliveries {
		return nil
	}
	if !hasSnapshots || !hasDeliveries ||
		!db.Migrator().HasTable(&models.DomainEvent{}) {
		return errors.New(
			"webhook credential lifetime migration requires snapshot, Outbox, and DomainEvent tables",
		)
	}
	if !db.Migrator().HasTable(&models.SchemaMigrationCheckpoint{}) {
		return errors.New(
			"webhook credential lifetime migration requires schema migration checkpoints",
		)
	}
	return withWebhookCredentialOwnerAccess(db, func(tx *gorm.DB) error {
		if err := lockWebhookCredentialCheckpoint(tx); err != nil {
			return err
		}
		if err := prepareWebhookSnapshotCredentialLifetimeColumns(tx); err != nil {
			return err
		}
		if err := validatePreparedWebhookCredentialColumnContract(tx); err != nil {
			return err
		}
		checkpoint, exists, err := readWebhookCredentialCheckpoint(tx)
		if err != nil {
			return err
		}
		if exists {
			if err := validateWebhookCredentialOwnerSet(
				tx,
				true,
			); err != nil {
				return fmt.Errorf(
					"validate completed webhook credential lifetime migration: %w",
					err,
				)
			}
			if err := validateWebhookCredentialLifetimeCatalog(tx); err != nil {
				return fmt.Errorf(
					"validate completed webhook credential lifetime catalog: %w",
					err,
				)
			}
			return nil
		}
		hasLifetimeData, err := webhookCredentialLifetimeDataExists(tx)
		if err != nil {
			return err
		}
		if hasLifetimeData {
			return errors.New(
				"webhook credential lifetime data exists without its migration checkpoint",
			)
		}
		// These are not speculative performance indexes: the owner anti-joins
		// and set-based backfill below are their first consumers. Install them
		// inside the same rollback-safe cutover before scanning legacy volume.
		if err := createWebhookCredentialIndexes(tx); err != nil {
			return err
		}
		if err := validateWebhookCredentialOwnerSet(tx, false); err != nil {
			return fmt.Errorf(
				"validate legacy webhook credential pairs: %w",
				err,
			)
		}
		if err := validateWebhookProjectDirectoryReferences(tx); err != nil {
			return err
		}
		if cutoverAt.IsZero() {
			cutoverAt = time.Now().UTC()
		} else {
			cutoverAt = cutoverAt.UTC()
		}
		deadline := cutoverAt.Add(
			models.WebhookDeliveryCredentialLifetime,
		)
		var auditedPairCount int64
		if err := tx.Table("webhook_delivery_snapshots").
			Count(&auditedPairCount).Error; err != nil {
			return fmt.Errorf(
				"count audited legacy webhook credential pairs: %w",
				err,
			)
		}
		deliveryResult := tx.Exec(`
			UPDATE outbox_deliveries
			SET expires_at = ?
			WHERE destination_type = 'webhook'
			  AND expires_at IS NULL
			  AND expired_at IS NULL
			  AND EXISTS (
				SELECT 1
				FROM webhook_delivery_snapshots AS snapshot
				WHERE outbox_deliveries.destination_id =
					'snapshot:' || snapshot.id
				  AND outbox_deliveries.organization_id =
					snapshot.organization_id
				  AND outbox_deliveries.project_id =
					snapshot.project_id
				  AND outbox_deliveries.event_id =
					snapshot.event_id
			  )
		`, deadline)
		if deliveryResult.Error != nil {
			return fmt.Errorf(
				"set-based backfill webhook delivery deadlines: %w",
				deliveryResult.Error,
			)
		}
		if deliveryResult.RowsAffected != auditedPairCount {
			return fmt.Errorf(
				"set-based webhook delivery backfill changed %d rows, want %d",
				deliveryResult.RowsAffected,
				auditedPairCount,
			)
		}
		snapshotResult := tx.Exec(`
			UPDATE webhook_delivery_snapshots
			SET credential_expires_at = ?
			WHERE credential_expires_at IS NULL
			  AND credential_shredded_at IS NULL
			  AND credential_shred_reason IS NULL
			  AND EXISTS (
				SELECT 1
				FROM outbox_deliveries AS delivery
				WHERE delivery.destination_type = 'webhook'
				  AND delivery.destination_id =
					'snapshot:' || webhook_delivery_snapshots.id
				  AND delivery.organization_id =
					webhook_delivery_snapshots.organization_id
				  AND delivery.project_id =
					webhook_delivery_snapshots.project_id
				  AND delivery.event_id =
					webhook_delivery_snapshots.event_id
				  AND delivery.expires_at = ?
			  )
		`, deadline, deadline)
		if snapshotResult.Error != nil {
			return fmt.Errorf(
				"set-based backfill webhook snapshot deadlines: %w",
				snapshotResult.Error,
			)
		}
		if snapshotResult.RowsAffected != auditedPairCount {
			return fmt.Errorf(
				"set-based webhook snapshot backfill changed %d rows, want %d",
				snapshotResult.RowsAffected,
				auditedPairCount,
			)
		}
		if err := validateWebhookCredentialOwnerSet(tx, true); err != nil {
			return fmt.Errorf(
				"validate backfilled webhook credential pairs: %w",
				err,
			)
		}
		if err := finalizeWebhookCredentialLifetimeSchema(tx); err != nil {
			return err
		}
		checkpoint = models.SchemaMigrationCheckpoint{
			Key:         webhookSnapshotCredentialLifetimeCheckpointKey,
			Version:     webhookSnapshotCredentialLifetimeCheckpointVersion,
			Checksum:    webhookSnapshotCredentialLifetimeCheckpointChecksum,
			CompletedAt: cutoverAt,
		}
		if err := tx.Create(&checkpoint).Error; err != nil {
			return fmt.Errorf(
				"record webhook credential lifetime checkpoint: %w",
				err,
			)
		}
		if err := validateWebhookCredentialLifetimeCatalog(tx); err != nil {
			return err
		}
		return validateWebhookCredentialOwnerSet(tx, true)
	})
}

func prepareWebhookSnapshotCredentialLifetimeColumns(db *gorm.DB) error {
	if db == nil {
		return errors.New("webhook credential lifetime database is required")
	}
	columns := []struct {
		table      string
		column     string
		postgres   string
		sqliteType string
	}{
		{
			table:      "webhook_delivery_snapshots",
			column:     "credential_expires_at",
			postgres:   "TIMESTAMPTZ",
			sqliteType: "DATETIME",
		},
		{
			table:      "webhook_delivery_snapshots",
			column:     "credential_shredded_at",
			postgres:   "TIMESTAMPTZ",
			sqliteType: "DATETIME",
		},
		{
			table:      "webhook_delivery_snapshots",
			column:     "credential_shred_reason",
			postgres:   "VARCHAR(20)",
			sqliteType: "VARCHAR(20)",
		},
		{
			table:      "outbox_deliveries",
			column:     "expires_at",
			postgres:   "TIMESTAMPTZ",
			sqliteType: "DATETIME",
		},
		{
			table:      "outbox_deliveries",
			column:     "expired_at",
			postgres:   "TIMESTAMPTZ",
			sqliteType: "DATETIME",
		},
	}
	for _, column := range columns {
		hasColumn, err := hasExactDatabaseColumn(
			db,
			column.table,
			column.column,
		)
		if err != nil {
			return err
		}
		if hasColumn {
			continue
		}
		columnType := column.sqliteType
		if db.Dialector.Name() == "postgres" {
			columnType = column.postgres
		}
		if err := db.Exec(
			"ALTER TABLE " + column.table +
				" ADD COLUMN " + column.column + " " + columnType,
		).Error; err != nil {
			return fmt.Errorf(
				"add nullable %s.%s: %w",
				column.table,
				column.column,
				err,
			)
		}
	}
	return nil
}

func lockWebhookCredentialCheckpoint(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	if err := tx.Exec(
		`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Error; err != nil {
		return fmt.Errorf(
			"lock webhook credential lifetime checkpoint: %w",
			err,
		)
	}
	return nil
}

func readWebhookCredentialCheckpoint(
	tx *gorm.DB,
) (models.SchemaMigrationCheckpoint, bool, error) {
	var checkpoint models.SchemaMigrationCheckpoint
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"key = ?",
			webhookSnapshotCredentialLifetimeCheckpointKey,
		).
		Take(&checkpoint).Error
	switch {
	case err == nil:
		if checkpoint.Version !=
			webhookSnapshotCredentialLifetimeCheckpointVersion ||
			checkpoint.Checksum !=
				webhookSnapshotCredentialLifetimeCheckpointChecksum ||
			checkpoint.CompletedAt.IsZero() {
			return models.SchemaMigrationCheckpoint{}, false, fmt.Errorf(
				"webhook credential lifetime checkpoint %q is invalid",
				webhookSnapshotCredentialLifetimeCheckpointKey,
			)
		}
		return checkpoint, true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return models.SchemaMigrationCheckpoint{}, false, nil
	default:
		return models.SchemaMigrationCheckpoint{}, false, fmt.Errorf(
			"read webhook credential lifetime checkpoint: %w",
			err,
		)
	}
}

func webhookCredentialLifetimeDataExists(db *gorm.DB) (bool, error) {
	var snapshotCount int64
	if err := db.Table("webhook_delivery_snapshots").
		Where(
			"credential_expires_at IS NOT NULL " +
				"OR credential_shredded_at IS NOT NULL " +
				"OR credential_shred_reason IS NOT NULL",
		).
		Count(&snapshotCount).Error; err != nil {
		return false, fmt.Errorf(
			"inspect pre-checkpoint webhook snapshot lifetime data: %w",
			err,
		)
	}
	var deliveryCount int64
	if err := db.Table("outbox_deliveries").
		Where(
			"expires_at IS NOT NULL OR expired_at IS NOT NULL OR status = ?",
			models.OutboxDeliveryExpired,
		).
		Count(&deliveryCount).Error; err != nil {
		return false, fmt.Errorf(
			"inspect pre-checkpoint Outbox lifetime data: %w",
			err,
		)
	}
	return snapshotCount != 0 || deliveryCount != 0, nil
}

// MigrateWebhookSnapshotCredentialLifetimeContract performs the full legacy
// cutover in one top-level transaction. Callers must not wrap it in a broader
// migration transaction: PostgreSQL ACCESS EXCLUSIVE locks are released only
// when this transaction commits.
func MigrateWebhookSnapshotCredentialLifetimeContract(db *gorm.DB) error {
	return migrateWebhookSnapshotCredentialLifetimeContractAt(db, time.Time{})
}

func finalizeWebhookCredentialLifetimeSchema(tx *gorm.DB) error {
	if err := validateWebhookCredentialOwnerSet(tx, true); err != nil {
		return err
	}
	switch tx.Dialector.Name() {
	case "postgres":
		if err := tx.Exec(`
			ALTER TABLE webhook_delivery_snapshots
			ALTER COLUMN credential_expires_at SET NOT NULL
		`).Error; err != nil {
			return fmt.Errorf(
				"finalize webhook snapshot credential deadline: %w",
				err,
			)
		}
		if err := installPostgresWebhookCredentialConstraints(tx); err != nil {
			return err
		}
	case "sqlite":
		if err := installSQLiteWebhookCredentialConstraints(tx); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"webhook credential finalization is unsupported for database dialect %q",
			tx.Dialector.Name(),
		)
	}
	if err := createWebhookCredentialIndexes(tx); err != nil {
		return err
	}
	return installWebhookProjectScopeForeignKeys(tx)
}

// migrateWebhookSnapshotCredentialLifetimeContractAt is the deterministic
// state-machine seam used by SQLite and PostgreSQL tests.
func migrateWebhookSnapshotCredentialLifetimeContractAt(
	db *gorm.DB,
	cutoverAt time.Time,
) error {
	if db == nil {
		return errors.New("webhook credential lifetime database is required")
	}
	hasSnapshots := db.Migrator().HasTable(
		&models.WebhookDeliverySnapshot{},
	)
	hasDeliveries := db.Migrator().HasTable(&models.OutboxDelivery{})
	if !hasSnapshots && !hasDeliveries {
		return nil
	}
	if db.Dialector.Name() != "sqlite" {
		return db.Transaction(func(tx *gorm.DB) error {
			return runWebhookSnapshotCredentialLifetimeCutover(tx, cutoverAt)
		})
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return errors.New(
			"SQLite webhook credential cutover requires a top-level database handle",
		)
	}
	enabled, err := sqliteForeignKeysEnabled(db)
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New(
			"SQLite foreign_keys must be enabled before webhook credential cutover",
		)
	}
	if err := db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		return fmt.Errorf(
			"disable SQLite foreign keys for webhook credential cutover: %w",
			err,
		)
	}
	cutoverErr := db.Transaction(func(tx *gorm.DB) error {
		return runWebhookSnapshotCredentialLifetimeCutover(tx, cutoverAt)
	})
	restoreErr := db.Exec("PRAGMA foreign_keys = ON").Error
	if restoreErr != nil {
		restoreErr = fmt.Errorf(
			"restore SQLite foreign keys after webhook credential cutover: %w",
			restoreErr,
		)
	}
	if err := errors.Join(cutoverErr, restoreErr); err != nil {
		return err
	}
	var violations []struct {
		Table string `gorm:"column:table"`
		RowID int64  `gorm:"column:rowid"`
	}
	if err := db.Raw("PRAGMA foreign_key_check").
		Scan(&violations).Error; err != nil {
		return fmt.Errorf("run SQLite foreign key check: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf(
			"SQLite foreign key check found %d violations",
			len(violations),
		)
	}
	return validateWebhookCredentialLifetimeCatalog(db)
}

func createWebhookCredentialIndexes(db *gorm.DB) error {
	statements := []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_scope_id " +
			"ON projects(organization_id, id)",
		"CREATE INDEX IF NOT EXISTS " +
			webhookSnapshotCredentialDeadlineIndex +
			" ON webhook_delivery_snapshots(" +
			"organization_id, project_id, credential_expires_at, id)",
		"CREATE INDEX IF NOT EXISTS " +
			outboxWebhookSnapshotPairDeadlineIndex +
			" ON outbox_deliveries(" +
			"organization_id, project_id, destination_type, " +
			"destination_id, event_id, expires_at)",
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf(
				"create webhook credential lifetime index: %w",
				err,
			)
		}
	}
	return nil
}

func installPostgresWebhookCredentialConstraints(db *gorm.DB) error {
	names := make([]string, 0, len(webhookCredentialConstraintDefinitions))
	for name := range webhookCredentialConstraintDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := webhookCredentialConstraintDefinitions[name]
		var count int64
		if err := db.Raw(`
			SELECT COUNT(*)
			FROM pg_constraint AS constraint_state
			JOIN pg_class AS table_state
			  ON table_state.oid = constraint_state.conrelid
			JOIN pg_namespace AS namespace
			  ON namespace.oid = table_state.relnamespace
			WHERE namespace.nspname = CURRENT_SCHEMA()
			  AND table_state.relname = ?
			  AND constraint_state.conname = ?
		`, definition.table, name).Scan(&count).Error; err != nil {
			return fmt.Errorf(
				"inspect PostgreSQL webhook credential constraint %s: %w",
				name,
				err,
			)
		}
		if count == 0 {
			if err := db.Exec(
				"ALTER TABLE " + definition.table +
					" ADD CONSTRAINT " + name +
					" CHECK (" + definition.expression + ")",
			).Error; err != nil {
				return fmt.Errorf(
					"install PostgreSQL webhook credential constraint %s: %w",
					name,
					err,
				)
			}
		}
	}
	return nil
}

func validateWebhookCredentialLifetimeCatalog(db *gorm.DB) error {
	for _, required := range []struct {
		table  string
		column string
	}{
		{"webhook_delivery_snapshots", "credential_expires_at"},
		{"webhook_delivery_snapshots", "credential_shredded_at"},
		{"webhook_delivery_snapshots", "credential_shred_reason"},
		{"outbox_deliveries", "expires_at"},
		{"outbox_deliveries", "expired_at"},
	} {
		present, err := hasExactDatabaseColumn(
			db,
			required.table,
			required.column,
		)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf(
				"%s.%s is missing; run `go run ./cmd/migrate`",
				required.table,
				required.column,
			)
		}
	}
	if err := validateWebhookCredentialColumnContract(db); err != nil {
		return err
	}
	if err := validateWebhookProjectScopeForeignKeyCatalog(db); err != nil {
		return err
	}
	var checkpoint models.SchemaMigrationCheckpoint
	if err := db.Where(
		"key = ?",
		webhookSnapshotCredentialLifetimeCheckpointKey,
	).Take(&checkpoint).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New(
				"webhook credential lifetime checkpoint is missing; run `go run ./cmd/migrate`",
			)
		}
		return fmt.Errorf(
			"read webhook credential lifetime checkpoint: %w",
			err,
		)
	}
	if checkpoint.Version !=
		webhookSnapshotCredentialLifetimeCheckpointVersion ||
		checkpoint.Checksum !=
			webhookSnapshotCredentialLifetimeCheckpointChecksum ||
		checkpoint.CompletedAt.IsZero() {
		return fmt.Errorf(
			"webhook credential lifetime checkpoint %q is invalid",
			webhookSnapshotCredentialLifetimeCheckpointKey,
		)
	}
	for _, definition := range webhookCredentialIndexDefinitions {
		var (
			valid bool
			err   error
		)
		switch db.Dialector.Name() {
		case "postgres":
			valid, err = postgresAutomationWebhookIndexIsValid(
				db,
				definition,
			)
		case "sqlite":
			valid, err = sqliteAutomationWebhookIndexIsValid(
				db,
				definition,
			)
		default:
			return fmt.Errorf(
				"webhook credential index validation is unsupported for database dialect %q",
				db.Dialector.Name(),
			)
		}
		if err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf(
				"webhook credential lifetime index %s on %s has an incompatible definition; run `go run ./cmd/migrate`",
				definition.name,
				definition.table,
			)
		}
	}
	if db.Dialector.Name() == "postgres" {
		return validatePostgresWebhookCredentialCatalog(db)
	}
	return validateSQLiteWebhookCredentialCatalog(db)
}

type postgresWebhookConstraintState struct {
	Table      string `gorm:"column:table_name"`
	Name       string `gorm:"column:name"`
	Type       string `gorm:"column:constraint_type"`
	Expression string `gorm:"column:expression"`
	Validated  bool   `gorm:"column:validated"`
	NoInherit  bool   `gorm:"column:no_inherit"`
}

func validatePostgresWebhookCredentialCatalog(db *gorm.DB) error {
	var states []postgresWebhookConstraintState
	if err := db.Raw(`
		SELECT
			table_state.relname AS table_name,
			constraint_state.conname AS name,
			constraint_state.contype::text AS constraint_type,
			pg_get_expr(
				constraint_state.conbin,
				constraint_state.conrelid,
				false
			) AS expression,
			constraint_state.convalidated AS validated,
			constraint_state.connoinherit AS no_inherit
		FROM pg_constraint AS constraint_state
		JOIN pg_class AS table_state
		  ON table_state.oid = constraint_state.conrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND constraint_state.conname IN ?
		ORDER BY constraint_state.conname ASC
	`, webhookCredentialConstraintNames()).Scan(&states).Error; err != nil {
		return fmt.Errorf(
			"read PostgreSQL webhook credential constraints: %w",
			err,
		)
	}
	byKey := make(map[string]postgresWebhookConstraintState, len(states))
	for _, state := range states {
		byKey[state.Table+"."+state.Name] = state
	}
	for name, expected := range webhookCredentialConstraintDefinitions {
		state, exists := byKey[expected.table+"."+name]
		if !exists ||
			state.Type != "c" ||
			!state.Validated ||
			state.NoInherit {
			return fmt.Errorf(
				"PostgreSQL webhook credential constraint %s is missing or has incompatible catalog state",
				name,
			)
		}
		gotDefinition, err := canonicalWebhookConstraintDefinition(
			state.Expression,
		)
		if err != nil {
			return fmt.Errorf(
				"parse PostgreSQL webhook credential constraint %s expression %q: %w",
				name,
				state.Expression,
				err,
			)
		}
		wantDefinition, err := canonicalWebhookConstraintDefinition(
			"CHECK (" + expected.expression + ")",
		)
		if err != nil {
			return fmt.Errorf(
				"parse canonical webhook credential constraint %s: %w",
				name,
				err,
			)
		}
		if gotDefinition != wantDefinition {
			return fmt.Errorf(
				"PostgreSQL webhook credential constraint %s has definition %q, want %q",
				name,
				state.Expression,
				expected.expression,
			)
		}
	}
	return nil
}

func validateSQLiteWebhookCredentialCatalog(db *gorm.DB) error {
	var rows []struct {
		Name string `gorm:"column:name"`
		SQL  string `gorm:"column:sql"`
	}
	if err := db.Raw(`
		SELECT name, sql
		FROM sqlite_master
		WHERE type = 'table'
		  AND name IN (
			'projects',
			'webhook_delivery_snapshots',
			'outbox_deliveries'
		  )
		ORDER BY name ASC
	`).Scan(&rows).Error; err != nil {
		return fmt.Errorf(
			"read SQLite webhook credential constraints: %w",
			err,
		)
	}
	tableSQL := make(map[string]string, len(rows))
	for _, row := range rows {
		tableSQL[row.Name] = row.SQL
	}
	for name, expected := range webhookCredentialConstraintDefinitions {
		rawExpression, exists := sqliteWebhookConstraintExpression(
			tableSQL[expected.table],
			name,
		)
		if !exists {
			return fmt.Errorf(
				"SQLite webhook credential constraint %s is missing or duplicated in %q",
				name,
				tableSQL[expected.table],
			)
		}
		actualExpression, err := canonicalWebhookConstraintDefinition(
			rawExpression,
		)
		if err != nil {
			return fmt.Errorf(
				"parse SQLite webhook credential constraint %s expression %q: %w",
				name,
				rawExpression,
				err,
			)
		}
		expectedExpression, err := canonicalWebhookConstraintDefinition(
			expected.expression,
		)
		if err != nil {
			return fmt.Errorf(
				"parse canonical webhook credential constraint %s: %w",
				name,
				err,
			)
		}
		if actualExpression != expectedExpression {
			return fmt.Errorf(
				"SQLite webhook credential constraint %s is missing or weakened in %q",
				name,
				tableSQL[expected.table],
			)
		}
	}
	return nil
}

func sqliteWebhookConstraintExpression(
	tableSQL string,
	name string,
) (string, bool) {
	lowerSQL := strings.ToLower(tableSQL)
	start := strings.Index(lowerSQL, strings.ToLower(name))
	if start < 0 {
		return "", false
	}
	constraintOffset := strings.LastIndex(
		lowerSQL[:start],
		"constraint",
	)
	if constraintOffset < 0 {
		return "", false
	}
	if strings.Trim(
		lowerSQL[constraintOffset+len("constraint"):start],
		" \t\n\r`\"",
	) != "" {
		return "", false
	}
	valueStart := start + len(name)
	checkOffset := strings.Index(
		lowerSQL[valueStart:],
		"check",
	)
	if checkOffset < 0 {
		return "", false
	}
	openOffset := strings.Index(
		lowerSQL[valueStart+checkOffset+len("check"):],
		"(",
	)
	if openOffset < 0 {
		return "", false
	}
	open := valueStart + checkOffset + len("check") + openOffset
	close, ok := matchingSQLParenthesis(tableSQL, open)
	if !ok {
		return "", false
	}
	return tableSQL[open+1 : close], true
}

func webhookCredentialConstraintNames() []string {
	names := make([]string, 0, len(webhookCredentialConstraintDefinitions))
	for name := range webhookCredentialConstraintDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateWebhookSnapshotCredentialLifetimeContract is the privileged owner
// gate used by migration tooling. Runtime callers use the catalog and scoped
// data gates separately.
func ValidateWebhookSnapshotCredentialLifetimeContract(db *gorm.DB) error {
	if db == nil {
		return errors.New("webhook credential lifetime database is required")
	}
	return withWebhookCredentialOwnerAccess(db, func(tx *gorm.DB) error {
		if err := validateWebhookCredentialLifetimeCatalog(tx); err != nil {
			return err
		}
		return validateWebhookCredentialOwnerSet(tx, true)
	})
}

// ValidateWebhookSnapshotCredentialLifetimeRuntimeData enumerates the trusted
// Project directory, then validates each active or archived scope in a short
// transaction with SET LOCAL scope. It never treats an unscoped FORCE-RLS zero
// row result as evidence.
func ValidateWebhookSnapshotCredentialLifetimeRuntimeData(
	ctx context.Context,
	db *gorm.DB,
) error {
	return validateWebhookCredentialRuntimeSnapshot(ctx, db)
}

func loadAndValidateWebhookCredentialPairs(
	db *gorm.DB,
	requireDeadlines bool,
	expectedScope *models.ProjectScope,
) ([]webhookCredentialPair, error) {
	snapshotQuery := db.Table("webhook_delivery_snapshots").
		Select(
			"id, created_at, organization_id, project_id, event_id, " +
				"secret, previous_secret, access_token, " +
				"credential_expires_at, credential_shredded_at, " +
				"credential_shred_reason",
		)
	deliveryQuery := db.Table("outbox_deliveries").
		Select(
			"id, organization_id, project_id, event_id, destination_type, " +
				"destination_id, status, expires_at, expired_at",
		)
	if expectedScope != nil {
		snapshotQuery = snapshotQuery.Where(
			"organization_id = ? AND project_id = ?",
			expectedScope.OrganizationID,
			expectedScope.ProjectID,
		)
		deliveryQuery = deliveryQuery.Where(
			"organization_id = ? AND project_id = ?",
			expectedScope.OrganizationID,
			expectedScope.ProjectID,
		)
	}
	var snapshots []webhookCredentialSnapshotRow
	if err := snapshotQuery.Order("id ASC").Scan(&snapshots).Error; err != nil {
		return nil, fmt.Errorf(
			"load webhook credential snapshots: %w",
			err,
		)
	}
	var deliveries []webhookCredentialDeliveryRow
	if err := deliveryQuery.Order("id ASC").Scan(&deliveries).Error; err != nil {
		return nil, fmt.Errorf(
			"load webhook credential Outbox deliveries: %w",
			err,
		)
	}

	eventIDs := make([]string, 0, len(snapshots)+len(deliveries))
	seenEventIDs := make(map[string]struct{}, cap(eventIDs))
	addEventID := func(eventID string) {
		if _, exists := seenEventIDs[eventID]; exists {
			return
		}
		seenEventIDs[eventID] = struct{}{}
		eventIDs = append(eventIDs, eventID)
	}
	for _, snapshot := range snapshots {
		addEventID(snapshot.EventID)
	}
	for _, delivery := range deliveries {
		if delivery.DestinationType == "webhook" {
			addEventID(delivery.EventID)
		}
	}
	var events []webhookCredentialEventRow
	if len(eventIDs) > 0 {
		eventQuery := db.Table("domain_events").
			Select("id, organization_id, project_id").
			Where("id IN ?", eventIDs)
		if expectedScope != nil {
			eventQuery = eventQuery.Where(
				"organization_id = ? AND project_id = ?",
				expectedScope.OrganizationID,
				expectedScope.ProjectID,
			)
		}
		if err := eventQuery.Order("id ASC").Scan(&events).Error; err != nil {
			return nil, fmt.Errorf(
				"load webhook credential DomainEvents: %w",
				err,
			)
		}
	}
	eventByID := make(map[string]webhookCredentialEventRow, len(events))
	for _, event := range events {
		if _, err := parseCanonicalUUID(event.ID, "DomainEvent"); err != nil {
			return nil, err
		}
		eventByID[event.ID] = event
	}

	snapshotByID := make(
		map[string]webhookCredentialSnapshotRow,
		len(snapshots),
	)
	for _, snapshot := range snapshots {
		if _, err := models.ParseWebhookDeliverySnapshotID(
			snapshot.ID,
		); err != nil {
			return nil, err
		}
		if snapshot.OrganizationID == 0 ||
			snapshot.ProjectID == 0 ||
			strings.TrimSpace(snapshot.EventID) == "" {
			return nil, fmt.Errorf(
				"webhook snapshot %s has invalid project scope or event",
				snapshot.ID,
			)
		}
		event, exists := eventByID[snapshot.EventID]
		if !exists {
			return nil, fmt.Errorf(
				"webhook snapshot %s is missing DomainEvent %s",
				snapshot.ID,
				snapshot.EventID,
			)
		}
		if event.OrganizationID != snapshot.OrganizationID ||
			event.ProjectID != snapshot.ProjectID {
			return nil, fmt.Errorf(
				"webhook snapshot %s scope does not match DomainEvent %s",
				snapshot.ID,
				snapshot.EventID,
			)
		}
		if requireDeadlines && snapshot.CredentialExpiresAt == nil {
			return nil, fmt.Errorf(
				"webhook snapshot %s credential deadline is missing",
				snapshot.ID,
			)
		}
		if err := validateWebhookCredentialShredRow(snapshot); err != nil {
			return nil, err
		}
		if _, exists := snapshotByID[snapshot.ID]; exists {
			return nil, fmt.Errorf(
				"duplicate webhook snapshot %s",
				snapshot.ID,
			)
		}
		snapshotByID[snapshot.ID] = snapshot
	}

	deliveryBySnapshot := make(map[string]webhookCredentialDeliveryRow)
	for _, delivery := range deliveries {
		if _, err := parseCanonicalUUID(delivery.ID, "Outbox delivery"); err != nil {
			return nil, err
		}
		if !delivery.Status.IsValid() {
			return nil, fmt.Errorf(
				"Outbox delivery %s has invalid status %q",
				delivery.ID,
				delivery.Status,
			)
		}
		isExpired := delivery.Status == models.OutboxDeliveryExpired
		if isExpired != (delivery.ExpiredAt != nil) {
			return nil, fmt.Errorf(
				"Outbox delivery %s expired status and timestamp disagree",
				delivery.ID,
			)
		}
		if delivery.DestinationType != "webhook" {
			if isExpired {
				return nil, fmt.Errorf(
					"non-webhook Outbox delivery %s has expired status",
					delivery.ID,
				)
			}
			continue
		}
		if delivery.OrganizationID == 0 ||
			delivery.ProjectID == 0 ||
			strings.TrimSpace(delivery.EventID) == "" {
			return nil, fmt.Errorf(
				"webhook Outbox delivery %s has invalid project scope or event",
				delivery.ID,
			)
		}
		event, exists := eventByID[delivery.EventID]
		if !exists {
			return nil, fmt.Errorf(
				"webhook Outbox delivery %s is missing DomainEvent %s",
				delivery.ID,
				delivery.EventID,
			)
		}
		if event.OrganizationID != delivery.OrganizationID ||
			event.ProjectID != delivery.ProjectID {
			return nil, fmt.Errorf(
				"webhook Outbox delivery %s scope does not match DomainEvent %s",
				delivery.ID,
				delivery.EventID,
			)
		}
		snapshotID, err :=
			models.ParseWebhookDeliverySnapshotDestinationID(
				delivery.DestinationID,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"malformed webhook delivery %s destination: %w",
				delivery.ID,
				err,
			)
		}
		snapshot, exists := snapshotByID[snapshotID]
		if !exists {
			return nil, fmt.Errorf(
				"webhook delivery %s is missing snapshot %s",
				delivery.ID,
				snapshotID,
			)
		}
		if _, duplicate := deliveryBySnapshot[snapshotID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate webhook delivery for snapshot %s",
				snapshotID,
			)
		}
		if delivery.EventID != snapshot.EventID {
			return nil, fmt.Errorf(
				"webhook delivery %s event does not match snapshot %s",
				delivery.ID,
				snapshotID,
			)
		}
		if delivery.OrganizationID != snapshot.OrganizationID ||
			delivery.ProjectID != snapshot.ProjectID {
			return nil, fmt.Errorf(
				"webhook delivery %s scope does not match snapshot %s",
				delivery.ID,
				snapshotID,
			)
		}
		if requireDeadlines {
			if delivery.ExpiresAt == nil ||
				snapshot.CredentialExpiresAt == nil {
				return nil, fmt.Errorf(
					"webhook delivery %s deadline is missing",
					delivery.ID,
				)
			}
			if !delivery.ExpiresAt.Equal(
				*snapshot.CredentialExpiresAt,
			) {
				return nil, fmt.Errorf(
					"webhook delivery %s deadline does not match snapshot %s",
					delivery.ID,
					snapshotID,
				)
			}
		}
		deliveryBySnapshot[snapshotID] = delivery
	}

	pairs := make([]webhookCredentialPair, 0, len(snapshots))
	for _, snapshot := range snapshots {
		delivery, exists := deliveryBySnapshot[snapshot.ID]
		if !exists {
			return nil, fmt.Errorf(
				"webhook snapshot %s is missing its delivery",
				snapshot.ID,
			)
		}
		pairs = append(pairs, webhookCredentialPair{
			snapshot: snapshot,
			delivery: delivery,
		})
	}
	return pairs, nil
}

func validateWebhookCredentialShredRow(
	snapshot webhookCredentialSnapshotRow,
) error {
	hasTimestamp := snapshot.CredentialShreddedAt != nil
	hasReason := snapshot.CredentialShredReason != nil
	if hasTimestamp != hasReason {
		return fmt.Errorf(
			"webhook snapshot %s shred timestamp and reason disagree",
			snapshot.ID,
		)
	}
	if !hasReason {
		return nil
	}
	reason := models.WebhookCredentialShredReason(
		*snapshot.CredentialShredReason,
	)
	if !reason.IsValid() {
		return fmt.Errorf(
			"webhook snapshot %s has invalid shred reason %q",
			snapshot.ID,
			reason,
		)
	}
	if snapshot.Secret != "" ||
		snapshot.PreviousSecret != "" ||
		snapshot.AccessToken != "" {
		return fmt.Errorf(
			"webhook snapshot %s retains a credential envelope after shredding",
			snapshot.ID,
		)
	}
	return nil
}

func parseCanonicalUUID(value, label string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil ||
		parsed.String() != value {
		return "", fmt.Errorf(
			"%s id %q must be a canonical lowercase UUID",
			label,
			value,
		)
	}
	return value, nil
}
