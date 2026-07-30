package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"github.com/seaworld008/chronodesk/server/internal/scopeddb"
	"gorm.io/gorm"
)

// SourceProtocol identifies the authenticated Adapter that initiated a domain
// operation. It is trusted control data populated by server-side middleware,
// never by a request body or Agent payload.
type SourceProtocol string

const (
	SourceProtocolHumanREST SourceProtocol = "human_rest"
	SourceProtocolAgentREST SourceProtocol = "agent_rest"
	SourceProtocolMCP       SourceProtocol = "mcp"
	SourceProtocolA2A       SourceProtocol = "a2a"
	SourceProtocolConnector SourceProtocol = "connector"
	SourceProtocolWorker    SourceProtocol = "worker"
)

func (protocol SourceProtocol) Validate() error {
	switch protocol {
	case SourceProtocolHumanREST,
		SourceProtocolAgentREST,
		SourceProtocolMCP,
		SourceProtocolA2A,
		SourceProtocolConnector,
		SourceProtocolWorker:
		return nil
	default:
		return fmt.Errorf("unsupported source protocol %q", protocol)
	}
}

// transactionForContext is the only project-aware transaction entry point
// used inside the services package. It reuses the request's transaction-local
// PostgreSQL RLS scope when present and behaves as a normal transaction for
// platform-only operations.
func transactionForContext(
	ctx context.Context,
	db *gorm.DB,
	fn func(*gorm.DB) error,
) error {
	return scopeddb.TransactionForContext(ctx, db, fn)
}

// runProjectOperation guarantees that a trusted OperationContext is paired
// with transaction-local PostgreSQL RLS settings. HTTP adapters may already
// own that boundary; machine adapters and direct service callers get the same
// fail-closed behavior without copying scope setup.
func runProjectOperation(
	ctx context.Context,
	db *gorm.DB,
	fn func(context.Context) error,
) error {
	if db == nil || fn == nil {
		return errors.New("project operation database and callback are required")
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return err
	}
	reusable, err := scopeddb.CanReuseProjectScopeTransaction(
		ctx,
		operation.Scope,
	)
	if err != nil {
		return err
	}
	if reusable {
		return fn(ctx)
	}
	return scopeddb.WithProjectScopeContextTransaction(
		ctx,
		db,
		operation.Scope,
		fn,
	)
}

// OperationContext is the protocol-neutral, authenticated control context for
// every domain command and query. Adapters resolve a public project key to this
// numeric scope only after membership or principal-grant authorization.
//
// User content, A2A metadata, Connector payloads and request bodies must never
// be allowed to construct or overwrite this value.
type OperationContext struct {
	Scope         models.ProjectScope `json:"scope"`
	Actor         models.ActorRef     `json:"actor"`
	Source        SourceProtocol      `json:"source"`
	CredentialID  string              `json:"credential_id,omitempty"`
	TraceID       string              `json:"trace_id,omitempty"`
	CorrelationID string              `json:"correlation_id,omitempty"`
}

func (operation OperationContext) Validate() error {
	if err := operation.Scope.Validate(); err != nil {
		return fmt.Errorf("invalid project scope: %w", err)
	}
	if err := operation.Actor.Validate(); err != nil {
		return fmt.Errorf("invalid actor: %w", err)
	}
	if err := operation.Source.Validate(); err != nil {
		return err
	}
	if operation.Actor.Type == models.ActorTypeServicePrincipal &&
		strings.TrimSpace(operation.CredentialID) == "" {
		return errors.New("service principal operation requires credential id")
	}
	if operation.Actor.Type != models.ActorTypeServicePrincipal &&
		strings.TrimSpace(operation.CredentialID) != "" {
		return errors.New("only service principal operations may carry a credential id")
	}
	return nil
}

type operationContextKey struct{}

// WithOperationContext installs a validated operation context. Invalid or
// incomplete contexts fail closed so callers cannot accidentally execute an
// unscoped repository operation.
func WithOperationContext(
	ctx context.Context,
	operation OperationContext,
) (context.Context, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := operation.Validate(); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, operationContextKey{}, operation), nil
}

// OperationContextFromContext returns only a previously validated context.
func OperationContextFromContext(ctx context.Context) (OperationContext, error) {
	if ctx == nil {
		return OperationContext{}, errors.New("context is required")
	}
	operation, ok := ctx.Value(operationContextKey{}).(OperationContext)
	if !ok {
		return OperationContext{}, errors.New("trusted operation context is required")
	}
	if err := operation.Validate(); err != nil {
		return OperationContext{}, fmt.Errorf("trusted operation context is invalid: %w", err)
	}
	return operation, nil
}

// RequireProjectScope is the common repository guard. Every project-owned
// query calls this before constructing SQL, while PostgreSQL RLS supplies the
// independent database-level boundary.
func RequireProjectScope(ctx context.Context) (models.ProjectScope, error) {
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		return models.ProjectScope{}, err
	}
	return operation.Scope, nil
}

// EnsureSystemProjectOperationContext binds a trusted project scope to an
// internal worker operation. Existing contexts are never overwritten: a
// mismatched actor or scope fails closed.
func EnsureSystemProjectOperationContext(
	ctx context.Context,
	scope models.ProjectScope,
	actor models.ActorRef,
	traceID string,
	correlationID string,
) (context.Context, error) {
	if actor.Type != models.ActorTypeSystem {
		return nil, errors.New("worker operation requires a system actor")
	}
	if err := scope.Validate(); err != nil {
		return nil, fmt.Errorf("worker project scope: %w", err)
	}
	if existing, err := OperationContextFromContext(ctx); err == nil {
		if existing.Scope != scope || existing.Actor != actor ||
			existing.Source != SourceProtocolWorker {
			return nil, errors.New(
				"existing operation context does not match worker project scope",
			)
		}
		return ctx, nil
	}
	return WithOperationContext(ctx, OperationContext{
		Scope:         scope,
		Actor:         actor,
		Source:        SourceProtocolWorker,
		TraceID:       strings.TrimSpace(traceID),
		CorrelationID: strings.TrimSpace(correlationID),
	})
}
