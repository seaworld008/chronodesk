// Package agentcontract contains protocol-neutral machine identity contracts.
// It must not depend on persistence, domain, or transport Modules.
package agentcontract

const (
	ScopeTicketsRead       = "tickets:read"
	ScopeTicketsCreate     = "tickets:create"
	ScopeTicketsUpdate     = "tickets:update"
	ScopeTicketsAssign     = "tickets:assign"
	ScopeTicketsTransition = "tickets:transition"
	ScopeCommentsWrite     = "comments:write"
	ScopeAttachmentsRead   = "attachments:read"
	ScopeAttachmentsWrite  = "attachments:write"
	ScopeEventsSubscribe   = "events:subscribe"
	ScopeTasksManage       = "tasks:manage"
)

var supportedScopes = []string{
	ScopeTicketsRead,
	ScopeTicketsCreate,
	ScopeTicketsUpdate,
	ScopeTicketsAssign,
	ScopeTicketsTransition,
	ScopeCommentsWrite,
	ScopeAttachmentsRead,
	ScopeAttachmentsWrite,
	ScopeEventsSubscribe,
	ScopeTasksManage,
}

var scopeDescriptions = map[string]string{
	ScopeTicketsRead:       "Read visible tickets and task context.",
	ScopeTicketsCreate:     "Create tickets through an authorized operation.",
	ScopeTicketsUpdate:     "Update authorized ticket fields.",
	ScopeTicketsAssign:     "Assign tickets to an authorized Actor.",
	ScopeTicketsTransition: "Transition the ticket lifecycle.",
	ScopeCommentsWrite:     "Add ticket comments.",
	ScopeAttachmentsRead:   "Read authorized ticket attachments.",
	ScopeAttachmentsWrite:  "Attach content to tickets.",
	ScopeEventsSubscribe:   "Subscribe to task and ticket events.",
	ScopeTasksManage:       "Create, inspect, continue, and cancel Agent tasks.",
}

// SupportedScopes returns a copy so callers cannot mutate the shared contract.
func SupportedScopes() []string {
	return append([]string(nil), supportedScopes...)
}

// ScopeDescriptions returns a copy for OAuth discovery and Agent Cards.
func ScopeDescriptions() map[string]string {
	result := make(map[string]string, len(scopeDescriptions))
	for scope, description := range scopeDescriptions {
		result[scope] = description
	}
	return result
}
