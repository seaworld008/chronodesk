// Package mcp implements ChronoDesk's Model Context Protocol endpoint.
//
// The package deliberately depends on a small Backend interface instead of the
// application's handlers or persistence models. This keeps MCP as a protocol
// adapter: callers are responsible for mapping the stable MCP contracts to the
// domain service.
package mcp

import (
	"context"
	"errors"
)

const (
	// ProtocolVersion is the only MCP revision implemented by this server.
	ProtocolVersion = "2026-07-28"

	// OAuthClientCredentialsExtension is the official MCP extension used by
	// unattended service principals to obtain scoped access tokens.
	OAuthClientCredentialsExtension = "io.modelcontextprotocol/oauth-client-credentials"

	HeaderProtocolVersion = "MCP-Protocol-Version"
	HeaderMethod          = "Mcp-Method"
	HeaderName            = "Mcp-Name"
)

// Principal is the authenticated actor presented to the MCP backend.
// Scopes are security controls, not advisory tool metadata.
type Principal struct {
	Type         string         `json:"type"`
	ID           string         `json:"id"`
	CredentialID string         `json:"credential_id,omitempty"`
	Scopes       []string       `json:"scopes,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// HasScopes reports whether the principal owns every requested scope. A single
// "*" scope is reserved for explicitly configured administrative principals.
func (p Principal) HasScopes(required ...string) bool {
	if len(required) == 0 {
		return true
	}

	available := make(map[string]struct{}, len(p.Scopes))
	for _, scope := range p.Scopes {
		available[scope] = struct{}{}
	}
	if _, ok := available["*"]; ok {
		return true
	}
	for _, scope := range required {
		if _, ok := available[scope]; !ok {
			return false
		}
	}
	return true
}

func (p Principal) fingerprint() string {
	return p.Type + "\x00" + p.ID + "\x00" + p.CredentialID
}

type principalContextKey struct{}

func contextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the authenticated MCP principal. Backends can
// use this in addition to the explicit principal argument for downstream
// logging and policy propagation.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

// Authenticator verifies an HTTP bearer token and constructs a principal.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerToken string) (Principal, error)
	// Revalidate performs the same security checks without recording token
	// usage. Long-lived streams call it periodically to detect revocation
	// without amplifying database writes.
	Revalidate(ctx context.Context, bearerToken string) (Principal, error)
}

// AuthorizationRequest is the complete, parsed policy input for one MCP
// operation. ResourceURI and Arguments let policy engines make object-level
// decisions instead of treating a tool name as sufficient authorization.
type AuthorizationRequest struct {
	Action         string
	RequiredScopes []string
	ResourceURI    string
	Arguments      map[string]any
}

// Authorizer is an optional policy hook invoked after static scope checks.
// Implementations must evaluate the concrete resource or tool arguments carried
// by request; a method name alone is never an object-level authorization result.
type Authorizer interface {
	Authorize(ctx context.Context, principal Principal, request AuthorizationRequest) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, Principal, AuthorizationRequest) error

func (f AuthorizerFunc) Authorize(ctx context.Context, principal Principal, request AuthorizationRequest) error {
	return f(ctx, principal, request)
}

// PolicyError lets an Authorizer return safe denial metadata without exposing
// an internal error string. Empty fields are omitted from the JSON-RPC error.
type PolicyError struct {
	DecisionID string
	ReasonCode string
}

func (e *PolicyError) Error() string {
	return "policy denied"
}

// Backend is the complete domain-facing interface required by the MCP server.
// Tool results must match the advertised output schema for that tool.
type Backend interface {
	CallTool(ctx context.Context, principal Principal, name string, arguments map[string]any) (map[string]any, error)
	ReadResource(ctx context.Context, principal Principal, uri string) (ResourceContent, error)
	// ValidateSubscription is a side-effect-free current-access check used
	// immediately before delivering a resource update. A false result is a
	// definitive revocation; an error is transient and must not revoke a
	// subscription.
	ValidateSubscription(ctx context.Context, principal Principal, uri string) (bool, error)
}

// EventBackend can optionally be implemented by Backend. Events are fanned out
// only to active subscriptions/listen streams that requested the resource URI.
type EventBackend interface {
	Events() <-chan ResourceEvent
}

// ResourceEvent requests a notifications/resources/updated message.
type ResourceEvent struct {
	URI string
}

// ResourceContent is the domain-facing representation of MCP resource content.
// Exactly one of Text and Blob should be set. Blob contains base64 data.
type ResourceContent struct {
	URI         string         `json:"uri"`
	MIMEType    string         `json:"mimeType,omitempty"`
	Text        string         `json:"text,omitempty"`
	Blob        string         `json:"blob,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

// BackendError is a safe, machine-readable tool execution failure. Message and
// Details are returned to the MCP client and therefore must not contain secrets.
type BackendError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *BackendError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func normalizeBackendError(err error) BackendError {
	var backendErr *BackendError
	if errors.As(err, &backendErr) && backendErr != nil {
		result := *backendErr
		if result.Code == "" {
			result.Code = "backend_error"
		}
		if result.Message == "" {
			result.Message = "tool execution failed"
		}
		return result
	}
	return BackendError{
		Code:      "backend_error",
		Message:   "tool execution failed",
		Retryable: false,
	}
}
