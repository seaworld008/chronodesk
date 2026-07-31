// Package scopeddb routes GORM operations through a trusted transaction stored
// in context. It deliberately has no dependency on ChronoDesk's migration or
// service packages, so domain services can reuse an outer PostgreSQL RLS
// transaction without creating an import cycle.
package scopeddb

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

const pluginName = "chronodesk_project_scope_transaction_routing"

// GORM sessions copy Config while sharing its Plugins map and callback
// processors. GORM does not synchronize Use or callback registration, so a
// process-wide lock is required to make the identity check and initialization
// atomic even when callers hold different session clones. The lock is used
// only while installing the plugin; database operations never acquire it.
var installMu sync.Mutex

type transactionContextKey struct{}

type transactionBinding struct {
	rootPool       gorm.ConnPool
	scopedDB       *gorm.DB
	scopedPool     gorm.ConnPool
	organizationID uint
	projectIDs     []uint
}

// Install registers fail-closed callbacks that bind statements using a root
// GORM handle to the trusted transaction in Statement.Context.
func Install(db *gorm.DB) error {
	if err := requireDatabase(db); err != nil {
		return err
	}
	installMu.Lock()
	defer installMu.Unlock()

	err := db.Use(routingPlugin{})
	if errors.Is(err, gorm.ErrRegistered) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("install project scope transaction routing: %w", err)
	}
	return nil
}

type routingPlugin struct{}

func (routingPlugin) Name() string {
	return pluginName
}

func (routingPlugin) Initialize(db *gorm.DB) error {
	callbacks := []struct {
		registrar callbackRegistrar
		operation string
	}{
		{db.Callback().Create().Before("gorm:begin_transaction"), "create"},
		{db.Callback().Update().Before("gorm:begin_transaction"), "update"},
		{db.Callback().Delete().Before("gorm:begin_transaction"), "delete"},
		{db.Callback().Query().Before("gorm:query"), "query"},
		{db.Callback().Row().Before("gorm:row"), "row"},
		{db.Callback().Raw().Before("gorm:raw"), "raw"},
	}
	for _, callback := range callbacks {
		if err := callback.registrar.Register(
			pluginName+":"+callback.operation,
			bindTransaction,
		); err != nil {
			return fmt.Errorf(
				"register %s scoped database callback: %w",
				callback.operation,
				err,
			)
		}
	}
	return nil
}

type callbackRegistrar interface {
	Register(string, func(*gorm.DB)) error
}

func bindTransaction(db *gorm.DB) {
	if db == nil || db.Error != nil || db.Statement == nil ||
		db.Statement.Context == nil {
		return
	}
	binding, ok := fromContext(db.Statement.Context)
	if !ok {
		return
	}
	if err := binding.validate(); err != nil {
		_ = db.AddError(err)
		return
	}

	currentPool := db.Statement.ConnPool
	switch {
	case sameConnPool(currentPool, binding.scopedPool):
		return
	case sameConnPool(currentPool, binding.rootPool):
		db.Statement.ConnPool = binding.scopedPool
	default:
		_ = db.AddError(errors.New(
			"project-scoped operation attempted to use a different database transaction; use scopeddb.TransactionForContext",
		))
	}
}

// WithTransactionBinding derives a context that routes root-handle statements
// through scoped. The caller owns the transaction lifecycle and must not use
// the derived context after fn returns.
func WithTransactionBinding(
	ctx context.Context,
	root *gorm.DB,
	scoped *gorm.DB,
	scope models.ProjectScope,
	fn func(context.Context) error,
) error {
	return WithAuthorizedTransactionBinding(
		ctx,
		root,
		scoped,
		scope.OrganizationID,
		[]uint{scope.ProjectID},
		fn,
	)
}

// WithAuthorizedTransactionBinding is the multi-project counterpart used by
// an authorized cross-project workbench. projectIDs must be resolved from
// server-side memberships before this function is called; transport payloads
// are never a source of authorization.
func WithAuthorizedTransactionBinding(
	ctx context.Context,
	root *gorm.DB,
	scoped *gorm.DB,
	organizationID uint,
	projectIDs []uint,
	fn func(context.Context) error,
) error {
	if ctx == nil {
		return errors.New("project scope binding context is required")
	}
	if fn == nil {
		return errors.New("project scope binding callback is required")
	}
	if HasTransaction(ctx) {
		return errors.New("nested project scope context transactions are forbidden")
	}
	if err := requireDatabase(root); err != nil {
		return fmt.Errorf("project scope root database: %w", err)
	}
	if err := requireDatabase(scoped); err != nil {
		return fmt.Errorf("project scope transaction database: %w", err)
	}
	binding := transactionBinding{
		rootPool:       root.Statement.ConnPool,
		scopedDB:       scoped,
		scopedPool:     scoped.Statement.ConnPool,
		organizationID: organizationID,
		projectIDs:     append([]uint(nil), projectIDs...),
	}
	if err := binding.validate(); err != nil {
		return err
	}
	return fn(context.WithValue(ctx, transactionContextKey{}, binding))
}

// TransactionForContext reuses a trusted outer transaction when present.
// Outside a scoped operation it behaves like a normal GORM transaction.
func TransactionForContext(
	ctx context.Context,
	db *gorm.DB,
	fn func(*gorm.DB) error,
) error {
	if ctx == nil {
		return errors.New("transaction context is required")
	}
	if err := requireDatabase(db); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("transaction callback is required")
	}

	binding, scoped := fromContext(ctx)
	if !scoped {
		return db.WithContext(ctx).Transaction(fn)
	}
	if err := binding.validate(); err != nil {
		return err
	}
	currentPool := db.Statement.ConnPool
	if !sameConnPool(currentPool, binding.rootPool) &&
		!sameConnPool(currentPool, binding.scopedPool) {
		return errors.New(
			"transaction database does not belong to the active project scope transaction",
		)
	}
	// GORM detects the existing *sql.Tx and implements this nested
	// Transaction with a SAVEPOINT. Domain callback failure therefore rolls
	// back only that command's partial writes while the outer protocol
	// boundary may still persist a denied PolicyDecision, idempotency result,
	// or other auditable business-error state before committing.
	return binding.scopedDB.WithContext(ctx).
		Session(&gorm.Session{NewDB: true}).
		Transaction(fn)
}

// CanReuseProjectScopeTransaction reports whether ctx carries a trusted
// transaction binding that is exactly scoped to the requested single project.
// A valid multi-project binding is deliberately rejected even when it contains
// scope.ProjectID: reusing it for a project-owned operation would silently
// widen PostgreSQL RLS visibility beyond the OperationContext.
//
// A context without a transaction binding returns false so the caller can open
// a new single-project transaction. Any present but invalid, empty,
// multi-project, cross-organization, or different-project binding fails
// closed with an error.
func CanReuseProjectScopeTransaction(
	ctx context.Context,
	scope models.ProjectScope,
) (bool, error) {
	if ctx == nil {
		return false, errors.New("transaction context is required")
	}
	if err := scope.Validate(); err != nil {
		return false, fmt.Errorf("invalid project scope: %w", err)
	}
	binding, ok := fromContext(ctx)
	if !ok {
		return false, nil
	}
	if err := binding.validate(); err != nil {
		return false, err
	}
	if binding.organizationID != scope.OrganizationID ||
		len(binding.projectIDs) != 1 ||
		binding.projectIDs[0] != scope.ProjectID {
		return false, errors.New(
			"active transaction binding does not exactly match the project operation scope",
		)
	}
	return true, nil
}

func HasTransaction(ctx context.Context) bool {
	_, ok := fromContext(ctx)
	return ok
}

func fromContext(ctx context.Context) (transactionBinding, bool) {
	if ctx == nil {
		return transactionBinding{}, false
	}
	binding, ok := ctx.Value(transactionContextKey{}).(transactionBinding)
	return binding, ok
}

func (binding transactionBinding) validate() error {
	if binding.rootPool == nil || binding.scopedPool == nil ||
		binding.scopedDB == nil {
		return errors.New("project scope transaction binding is incomplete")
	}
	if sameConnPool(binding.rootPool, binding.scopedPool) {
		return errors.New(
			"project scope transaction binding did not acquire an isolated transaction",
		)
	}
	if binding.organizationID == 0 {
		return errors.New(
			"project scope transaction binding requires an organization",
		)
	}
	seen := make(map[uint]struct{}, len(binding.projectIDs))
	for _, projectID := range binding.projectIDs {
		if projectID == 0 {
			return errors.New(
				"project scope transaction binding contains an invalid project",
			)
		}
		if _, exists := seen[projectID]; exists {
			return errors.New(
				"project scope transaction binding contains duplicate projects",
			)
		}
		seen[projectID] = struct{}{}
	}
	return nil
}

func requireDatabase(db *gorm.DB) error {
	if db == nil || db.Config == nil || db.Statement == nil ||
		db.Dialector == nil || db.Statement.ConnPool == nil {
		return errors.New("database is required")
	}
	return nil
}

func sameConnPool(left, right gorm.ConnPool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left == right
}
