package database

import (
	"context"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

// InstallProjectScopeTransactionRouting installs the fail-closed GORM routing
// callbacks used by WithProjectScopeContextTransaction.
func InstallProjectScopeTransactionRouting(db *gorm.DB) error {
	return scopeddb.Install(db)
}

// WithProjectScopeContextTransaction executes one complete project operation
// inside a PostgreSQL transaction carrying transaction-local RLS scope. The
// derived context routes repository operations that use the shared root GORM
// handle through the same transaction.
func WithProjectScopeContextTransaction(
	ctx context.Context,
	db *gorm.DB,
	scope models.ProjectScope,
	fn func(context.Context) error,
) error {
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		scope,
		fn,
	)
}

// WithAuthorizedProjectSetContextTransaction opens one transaction for an
// explicitly authorized set of projects in the same organization. projectIDs
// must come from a server-side membership/grant resolution result, never from
// HTTP body or query input. An empty authorized set is valid and exposes zero
// project rows. Duplicate, zero, BIGINT-overflow, missing, or cross-
// organization project IDs fail before project data can be queried.
func WithAuthorizedProjectSetContextTransaction(
	ctx context.Context,
	db *gorm.DB,
	organizationID uint,
	projectIDs []uint,
	fn func(context.Context) error,
) error {
	return scopeddb.WithAuthorizedProjectScopeTransaction(
		ctx,
		db,
		organizationID,
		projectIDs,
		fn,
	)
}

// TransactionForContext preserves the former database-package API for
// adapters and tests. Domain services import the lower-level scopeddb package
// directly, avoiding a services → database → auth → services import cycle.
func TransactionForContext(
	ctx context.Context,
	db *gorm.DB,
	fn func(*gorm.DB) error,
) error {
	return scopeddb.TransactionForContext(ctx, db, fn)
}
