package scopeddb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const postgresScopeMaxID = uint64(1<<63 - 1)

// ConfigureProjectScopeTransaction installs a single project scope on an
// already-open top-level transaction. This narrow escape hatch is required
// when a platform command creates a Project and its first protected
// configuration rows atomically: the scope does not exist before that same
// transaction begins.
func ConfigureProjectScopeTransaction(
	tx *gorm.DB,
	scope models.ProjectScope,
) error {
	if err := requireDatabase(tx); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("invalid project scope: %w", err)
	}
	if uint64(scope.OrganizationID) > postgresScopeMaxID ||
		uint64(scope.ProjectID) > postgresScopeMaxID {
		return errors.New(
			"invalid project scope: identifiers must fit PostgreSQL BIGINT",
		)
	}
	if _, inTransaction := tx.Statement.ConnPool.(gorm.TxCommitter); !inTransaction {
		return errors.New(
			"project scope configuration requires an active database transaction",
		)
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	organizationID := strconv.FormatUint(uint64(scope.OrganizationID), 10)
	projectID := strconv.FormatUint(uint64(scope.ProjectID), 10)
	configured := struct {
		OrganizationID string `gorm:"column:organization_id"`
		ProjectID      string `gorm:"column:project_id"`
		ProjectIDs     string `gorm:"column:project_ids"`
	}{}
	if err := tx.Raw(
		`SELECT
			set_config('chronodesk.organization_id', ?, true) AS organization_id,
			set_config('chronodesk.project_id', ?, true) AS project_id,
			set_config('chronodesk.project_ids', '', true) AS project_ids`,
		organizationID,
		projectID,
	).Scan(&configured).Error; err != nil {
		return fmt.Errorf("set local project scope: %w", err)
	}
	if configured.OrganizationID != organizationID ||
		configured.ProjectID != projectID ||
		configured.ProjectIDs != "" {
		return errors.New(
			"PostgreSQL did not retain the requested local project scope",
		)
	}
	return nil
}

// ConfigureAuthorizedProjectScopeTransaction installs a bounded cross-project
// scope on an already-open top-level transaction. Callers must resolve and
// revalidate the project IDs from authoritative memberships or principal
// grants in this same transaction before calling this function.
func ConfigureAuthorizedProjectScopeTransaction(
	tx *gorm.DB,
	organizationID uint,
	projectIDs []uint,
) error {
	if err := requireDatabase(tx); err != nil {
		return err
	}
	if organizationID == 0 ||
		uint64(organizationID) > postgresScopeMaxID {
		return errors.New(
			"authorized project set organization must fit PostgreSQL BIGINT",
		)
	}
	if _, inTransaction := tx.Statement.ConnPool.(gorm.TxCommitter); !inTransaction {
		return errors.New(
			"authorized project scope configuration requires an active database transaction",
		)
	}
	authorizedIDs, err := normalizeAuthorizedProjectIDs(projectIDs)
	if err != nil {
		return err
	}
	if len(authorizedIDs) > 0 {
		var matchingProjects int64
		if err := tx.Table("projects").
			Where(
				"organization_id = ? AND id IN ?",
				organizationID,
				authorizedIDs,
			).
			Count(&matchingProjects).Error; err != nil {
			return fmt.Errorf(
				"validate authorized project ownership: %w",
				err,
			)
		}
		if matchingProjects != int64(len(authorizedIDs)) {
			return errors.New(
				"authorized project set contains a missing or cross-organization project",
			)
		}
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}

	organizationValue := strconv.FormatUint(uint64(organizationID), 10)
	projectValues := make([]string, 0, len(authorizedIDs))
	for _, projectID := range authorizedIDs {
		projectValues = append(
			projectValues,
			strconv.FormatUint(uint64(projectID), 10),
		)
	}
	projectSetValue := strings.Join(projectValues, ",")
	configured := struct {
		OrganizationID string `gorm:"column:organization_id"`
		ProjectID      string `gorm:"column:project_id"`
		ProjectIDs     string `gorm:"column:project_ids"`
	}{}
	if err := tx.Raw(
		`SELECT
			set_config(
				'chronodesk.organization_id',
				?,
				true
			) AS organization_id,
			set_config(
				'chronodesk.project_id',
				'',
				true
			) AS project_id,
			set_config(
				'chronodesk.project_ids',
				?,
				true
			) AS project_ids`,
		organizationValue,
		projectSetValue,
	).Scan(&configured).Error; err != nil {
		return fmt.Errorf(
			"set local authorized project scope: %w",
			err,
		)
	}
	if configured.OrganizationID != organizationValue ||
		configured.ProjectID != "" ||
		configured.ProjectIDs != projectSetValue {
		return errors.New(
			"PostgreSQL did not retain the authorized project set",
		)
	}
	return nil
}

// WithProjectScopeTransaction is the low-level worker/repository boundary. It
// sets PostgreSQL scope with transaction-local settings and passes the actual
// GORM transaction to fn. Workers should keep claim and finalize operations in
// separate calls so network or model I/O never holds a database transaction.
func WithProjectScopeTransaction(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	fn func(*gorm.DB) error,
) error {
	if ctx == nil {
		return errors.New("project scope transaction context is required")
	}
	if err := requireDatabase(db); err != nil {
		return err
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("invalid project scope: %w", err)
	}
	if uint64(scope.OrganizationID) > postgresScopeMaxID ||
		uint64(scope.ProjectID) > postgresScopeMaxID {
		return errors.New(
			"invalid project scope: identifiers must fit PostgreSQL BIGINT",
		)
	}
	if fn == nil {
		return errors.New("project scope transaction callback is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("project scope transaction context: %w", err)
	}
	if _, alreadyInTransaction := db.Statement.ConnPool.(gorm.TxCommitter); alreadyInTransaction {
		return errors.New(
			"project scope transaction requires a top-level database handle; nested transactions can leak SET LOCAL scope",
		)
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			configured := struct {
				OrganizationID string `gorm:"column:organization_id"`
				ProjectID      string `gorm:"column:project_id"`
				ProjectIDs     string `gorm:"column:project_ids"`
			}{}
			organizationID := strconv.FormatUint(
				uint64(scope.OrganizationID),
				10,
			)
			projectID := strconv.FormatUint(uint64(scope.ProjectID), 10)
			if err := tx.Raw(
				`SELECT
					set_config(
						'chronodesk.organization_id',
						?,
						true
					) AS organization_id,
					set_config(
						'chronodesk.project_id',
						?,
						true
					) AS project_id,
					set_config(
						'chronodesk.project_ids',
						'',
						true
					) AS project_ids`,
				organizationID,
				projectID,
			).Scan(&configured).Error; err != nil {
				return fmt.Errorf("set local project scope: %w", err)
			}
			if configured.OrganizationID != organizationID ||
				configured.ProjectID != projectID ||
				configured.ProjectIDs != "" {
				return errors.New(
					"PostgreSQL did not retain the requested local project scope",
				)
			}
		}
		return fn(tx)
	})
}

// WithProjectScopeContextTransaction adds transparent root-handle routing on
// top of WithProjectScopeTransaction. It is appropriate for a bounded HTTP or
// protocol command whose repositories already propagate ctx.
func WithProjectScopeContextTransaction(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	fn func(context.Context) error,
) error {
	if ctx == nil {
		return errors.New("project scope context transaction context is required")
	}
	if fn == nil {
		return errors.New("project scope context transaction callback is required")
	}
	if HasTransaction(ctx) {
		return errors.New("nested project scope context transactions are forbidden")
	}
	if err := Install(db); err != nil {
		return err
	}
	return WithProjectScopeTransaction(ctx, db, scope, func(scoped *gorm.DB) error {
		return WithTransactionBinding(ctx, db, scoped, scope, fn)
	})
}

// WithAuthorizedProjectScopeTransaction is the cross-project boundary. The
// project set must be the server-side result of membership/grant resolution.
// It verifies that every ID belongs to organizationID before installing the
// transaction-local array setting. Empty sets are valid and expose zero rows.
func WithAuthorizedProjectScopeTransaction(
	ctx context.Context,
	db *gorm.DB,
	organizationID uint,
	projectIDs []uint,
	fn func(context.Context) error,
) error {
	if ctx == nil {
		return errors.New(
			"authorized project set transaction context is required",
		)
	}
	if err := requireDatabase(db); err != nil {
		return err
	}
	if organizationID == 0 ||
		uint64(organizationID) > postgresScopeMaxID {
		return errors.New(
			"authorized project set organization must fit PostgreSQL BIGINT",
		)
	}
	if fn == nil {
		return errors.New(
			"authorized project set transaction callback is required",
		)
	}
	if HasTransaction(ctx) {
		return errors.New(
			"nested authorized project set transactions are forbidden",
		)
	}
	authorizedIDs, err := normalizeAuthorizedProjectIDs(projectIDs)
	if err != nil {
		return err
	}
	if err := Install(db); err != nil {
		return err
	}

	return db.WithContext(ctx).Transaction(func(scoped *gorm.DB) error {
		if err := ConfigureAuthorizedProjectScopeTransaction(
			scoped,
			organizationID,
			authorizedIDs,
		); err != nil {
			return err
		}
		return WithAuthorizedTransactionBinding(
			ctx,
			db,
			scoped,
			organizationID,
			authorizedIDs,
			fn,
		)
	})
}

func normalizeAuthorizedProjectIDs(projectIDs []uint) ([]uint, error) {
	authorizedIDs := append([]uint(nil), projectIDs...)
	seen := make(map[uint]struct{}, len(authorizedIDs))
	for _, projectID := range authorizedIDs {
		if projectID == 0 ||
			uint64(projectID) > postgresScopeMaxID {
			return nil, errors.New(
				"authorized project IDs must fit PostgreSQL BIGINT",
			)
		}
		if _, exists := seen[projectID]; exists {
			return nil, fmt.Errorf(
				"authorized project set contains duplicate project ID %d",
				projectID,
			)
		}
		seen[projectID] = struct{}{}
	}
	sort.Slice(authorizedIDs, func(left, right int) bool {
		return authorizedIDs[left] < authorizedIDs[right]
	})
	return authorizedIDs, nil
}
