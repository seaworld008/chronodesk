package routeinventory

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Classification is the required data-volume contract for a registered GET.
type Classification string

const (
	ClassificationPage          Classification = "page"
	ClassificationCursor        Classification = "cursor"
	ClassificationBounded       Classification = "bounded"
	ClassificationNonList       Classification = "non-list"
	ClassificationMachinePublic Classification = "machine/public"
)

// Declaration classifies a source registration. Human list declarations also
// bind the registration to its published OpenAPI path and operation ID.
type Declaration struct {
	Classification Classification
	OpenAPIPath    string
	OperationID    string
}

// ValidateCoverage fails closed for both newly discovered registrations and
// stale declarations. This bidirectional comparison is what makes the
// manifest a classification layer over source discovery instead of a route
// snapshot that can silently miss additions.
func ValidateCoverage(
	registrations []Registration,
	declarations map[string]Declaration,
) error {
	discovered := make(map[string]Registration, len(registrations))
	var validationErrors []error
	for _, registration := range registrations {
		if _, exists := discovered[registration.Fingerprint]; exists {
			validationErrors = append(
				validationErrors,
				fmt.Errorf(
					"duplicate discovered registration %q",
					registration.Fingerprint,
				),
			)
			continue
		}
		discovered[registration.Fingerprint] = registration
		declaration, classified := declarations[registration.Fingerprint]
		if !classified {
			validationErrors = append(
				validationErrors,
				fmt.Errorf(
					"unclassified runtime GET registration %q",
					registration.Fingerprint,
				),
			)
			continue
		}
		if err := validateDeclaration(registration, declaration); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	stale := make([]string, 0)
	for fingerprint := range declarations {
		if _, exists := discovered[fingerprint]; !exists {
			stale = append(stale, fingerprint)
		}
	}
	sort.Strings(stale)
	for _, fingerprint := range stale {
		validationErrors = append(
			validationErrors,
			fmt.Errorf(
				"stale runtime GET declaration %q has no source registration",
				fingerprint,
			),
		)
	}
	return errors.Join(validationErrors...)
}

func validateDeclaration(
	registration Registration,
	declaration Declaration,
) error {
	switch declaration.Classification {
	case ClassificationPage,
		ClassificationCursor,
		ClassificationBounded:
		if declaration.OpenAPIPath == "" {
			return fmt.Errorf(
				"%q is a Human list but has no OpenAPI path",
				registration.Fingerprint,
			)
		}
		if declaration.OperationID == "" {
			return fmt.Errorf(
				"%q is a Human list but has no OpenAPI operation ID",
				registration.Fingerprint,
			)
		}
	case ClassificationNonList:
		if (declaration.OpenAPIPath == "") !=
			(declaration.OperationID == "") {
			return fmt.Errorf(
				"%q has a partial Human OpenAPI binding",
				registration.Fingerprint,
			)
		}
	case ClassificationMachinePublic:
		if declaration.OpenAPIPath != "" || declaration.OperationID != "" {
			return fmt.Errorf(
				"%q is machine/public and must not bind the Human OpenAPI",
				registration.Fingerprint,
			)
		}
	default:
		return fmt.Errorf(
			"%q has invalid classification %q",
			registration.Fingerprint,
			declaration.Classification,
		)
	}
	return nil
}

// HumanGETDeclarations returns an isolated copy of the reviewed runtime
// classification manifest. Keys are AST-derived source fingerprints.
func HumanGETDeclarations() map[string]Declaration {
	result := make(
		map[string]Declaration,
		len(humanGETDeclarations),
	)
	for fingerprint, declaration := range humanGETDeclarations {
		result[fingerprint] = declaration
	}
	return result
}

type manifestEntry struct {
	fingerprint string
	declaration Declaration
}

var humanGETDeclarations = buildManifest(
	// OAuth, Agent REST, and public contract discovery are not Human Web API.
	publicLiteral(
		"internal/agentauth/handler.go",
		"RegisterPublicRoutes",
		"router",
		"/.well-known/oauth-authorization-server",
	),
	publicExpression(
		"internal/agentauth/handler.go",
		"RegisterPublicRoutes",
		"router",
		"parsed.Path",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/agent-control/overview",
		ClassificationBounded,
		"/projects/{projectKey}/admin/agents/agent-control/overview",
		"getAgentControlOverviewV2",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/attachments",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/attachments",
		"listAgentAttachmentScans",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/events",
		ClassificationCursor,
		"/projects/{projectKey}/admin/agents/events",
		"listAgentDomainEvents",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/leases",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/leases",
		"listAgentTicketLeases",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/outbox",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/outbox",
		"listAgentOutboxDeliveries",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/policy-decisions",
		ClassificationCursor,
		"/projects/{projectKey}/admin/agents/policy-decisions",
		"listAgentPolicyDecisions",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/service-principals",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/service-principals",
		"listAgentServicePrincipals",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/service-principals/:id/policies",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
		"listServicePrincipalPoliciesV2",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/webhooks/tombstones",
		ClassificationPage,
		"/projects/{projectKey}/admin/agents/webhooks/tombstones",
		"listProjectWebhookEmergencyTombstones",
	),
	humanList(
		"internal/agentplatform/admin_handler.go",
		"RegisterRoutes",
		"group",
		"/webhooks/:webhookID/emergency-revoke",
		ClassificationBounded,
		"/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke",
		"getProjectWebhookEmergencyRevokePreflight",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/attachments/:id/content",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/capabilities",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/events",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/knowledge/articles",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/knowledge/articles/:knowledgeArticleID/document",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/tickets",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/tickets/:id",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/tickets/:id/attachments",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/tickets/:id/comments",
	),
	publicLiteral(
		"internal/agentplatform/api_handler.go",
		"RegisterRoutes",
		"api",
		"/tickets/:id/history",
	),

	// Composition-root Human routes.
	humanNonList("internal/app/app.go", "Run", "analytics", "/business", "", ""),
	humanNonList("internal/app/app.go", "Run", "analytics", "/dashboard", "", ""),
	humanNonList("internal/app/app.go", "Run", "analytics", "/export", "", ""),
	humanNonList("internal/app/app.go", "Run", "analytics", "/realtime", "", ""),
	humanNonList("internal/app/app.go", "Run", "analytics", "/system", "", ""),
	humanNonList("internal/app/app.go", "Run", "analytics", "/timerange", "", ""),
	humanList(
		"internal/app/app.go",
		"Run",
		"assignees",
		"",
		ClassificationPage,
		"/projects/{projectKey}/assignees",
		"listProjectAssignees",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"assignees",
		"/:id",
		"/projects/{projectKey}/assignees/{assigneeID}",
		"getProjectAssignee",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"auditExports",
		"/:publicID",
		"/platform/audit-exports/{auditExportPublicID}",
		"getPlatformAuditExport",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"auditExports",
		"/:publicID/download",
		"/platform/audit-exports/{auditExportPublicID}/download",
		"downloadPlatformAuditExport",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"authenticated",
		"/me",
		"/auth/me",
		"getHumanSessionUser",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"categories",
		"",
		ClassificationPage,
		"/projects/{projectKey}/categories",
		"listProjectCategories",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"categories",
		"/:id",
		"/projects/{projectKey}/categories/{categoryID}",
		"getProjectCategory",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"configs",
		"",
		ClassificationPage,
		"/platform/configs",
		"listPlatformConfigs",
	),
	humanNonList("internal/app/app.go", "Run", "configs", "/:key", "", ""),
	humanNonList("internal/app/app.go", "Run", "configs", "/export", "", ""),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"configs",
		"/security-policy",
		"",
		"",
	),
	humanNonList(
		"internal/app/app.go",
		"registerPlatformEmergencyControlRoutes",
		"routes",
		"/emergency-controls",
		"/platform/emergency-controls",
		"getPlatformEmergencyControls",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"externalTickets",
		"/stats",
		"",
		"",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"notificationPreferences",
		"",
		ClassificationBounded,
		"/notification-preferences",
		"getHumanNotificationPreferences",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"notifications",
		"",
		ClassificationPage,
		"/projects/{projectKey}/notifications",
		"listProjectNotifications",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"notifications",
		"/unread-count",
		"/projects/{projectKey}/notifications/unread-count",
		"getProjectUnreadNotificationCount",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"platformAdmin",
		"/email-config",
		"/platform/email-config",
		"getPlatformEmailConfig",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"platformAdmin",
		"/users",
		ClassificationPage,
		"/platform/users",
		"listPlatformUsers",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"platformAdmin",
		"/users/:id",
		"/platform/users/{userID}",
		"getPlatformUser",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"platformAdmin",
		"/users/stats",
		"/platform/users/stats",
		"getPlatformUserStats",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"platformAudit",
		"/audit-logs",
		ClassificationCursor,
		"/platform/audit-logs",
		"listPlatformAuditLogs",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"platformAudit",
		"/audit-logs/:id",
		"/platform/audit-logs/{auditLogID}",
		"getPlatformAuditLogDetail",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"projectScoped",
		"/context",
		ClassificationBounded,
		"/projects/{projectKey}/context",
		"getAuthorizedProjectContext",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"projectScoped",
		"/membership-candidates",
		ClassificationPage,
		"/projects/{projectKey}/membership-candidates",
		"searchProjectMembershipCandidates",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"projectScoped",
		"/memberships",
		ClassificationPage,
		"/projects/{projectKey}/memberships",
		"listProjectMemberships",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"projectScoped",
		"/queues",
		ClassificationPage,
		"/projects/{projectKey}/queues",
		"listProjectQueues",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"projects",
		"",
		ClassificationPage,
		"/projects",
		"listAuthorizedHumanProjects",
	),
	humanNonList(
		"internal/app/app.go",
		"Run",
		"projects",
		"/:projectKey/ws",
		"",
		"",
	),
	publicLiteral("internal/app/app.go", "Run", "r", "/healthz"),
	publicLiteral("internal/app/app.go", "Run", "r", "/metrics"),
	publicLiteral(
		"internal/app/app.go",
		"Run",
		"r",
		"/uploads/avatars/:userID/:filename",
	),
	publicExpression(
		"internal/app/app.go",
		"Run",
		"r",
		"a2a.AgentCardPath",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"",
		ClassificationPage,
		"/projects/{projectKey}/tickets",
		"listProjectTickets",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/:id",
		ClassificationBounded,
		"/projects/{projectKey}/tickets/{ticketID}",
		"getProjectTicket",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/:id/transitions",
		ClassificationBounded,
		"/projects/{projectKey}/tickets/{ticketID}/transitions",
		"getProjectTicketAllowedTransitions",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/:id/history",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/history",
		"listProjectTicketHistory",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/my-tickets",
		ClassificationPage,
		"/projects/{projectKey}/tickets/my-tickets",
		"listMyProjectTickets",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/overdue",
		ClassificationPage,
		"/projects/{projectKey}/tickets/overdue",
		"listProjectOverdueTickets",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/sla-breach",
		ClassificationPage,
		"/projects/{projectKey}/tickets/sla-breach",
		"listProjectSLABreachedTickets",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"tickets",
		"/unassigned",
		ClassificationPage,
		"/projects/{projectKey}/tickets/unassigned",
		"listUnassignedProjectTickets",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"user",
		"/login-history",
		ClassificationPage,
		"/user/login-history",
		"listLoginHistory",
	),
	humanNonList("internal/app/app.go", "Run", "user", "/stats", "", ""),
	humanList(
		"internal/app/app.go",
		"Run",
		"user",
		"/trusted-devices",
		ClassificationPage,
		"/user/trusted-devices",
		"listTrustedDevices",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"webhooks",
		"",
		ClassificationPage,
		"/projects/{projectKey}/webhooks",
		"listProjectWebhooks",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"webhooks",
		"/:id",
		ClassificationBounded,
		"/projects/{projectKey}/webhooks/{webhookID}",
		"getProjectWebhook",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"webhooks",
		"/:id/logs",
		ClassificationCursor,
		"/projects/{projectKey}/webhooks/{webhookID}/logs",
		"listProjectWebhookLogs",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"webhooks",
		"/:id/stats",
		ClassificationBounded,
		"/projects/{projectKey}/webhooks/{webhookID}/stats",
		"getProjectWebhookStats",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"workbench",
		"/dashboard",
		ClassificationBounded,
		"/workbench/dashboard",
		"getWorkbenchDashboard",
	),
	humanList(
		"internal/app/app.go",
		"Run",
		"workbench",
		"/tickets",
		ClassificationPage,
		"/workbench/tickets",
		"listCrossProjectWorkbenchTickets",
	),
	humanList(
		"internal/app/app.go",
		"registerPlatformProjectRoutes",
		"routes",
		"/project-business-units",
		ClassificationPage,
		"/platform/project-business-units",
		"listPlatformProjectBusinessUnits",
	),
	humanList(
		"internal/app/app.go",
		"registerPlatformProjectRoutes",
		"routes",
		"/project-creation-context",
		ClassificationBounded,
		"/platform/project-creation-context",
		"getPlatformProjectCreationContext",
	),
	humanList(
		"internal/app/app.go",
		"registerPlatformProjectRoutes",
		"routes",
		"/projects",
		ClassificationPage,
		"/platform/projects",
		"listPlatformProjects",
	),
	publicLiteral(
		"internal/asyncapi/handler.go",
		"RegisterRoutes",
		"router",
		"/asyncapi.yaml",
	),

	// Delegated Human handler route groups.
	humanList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/approvals",
		ClassificationPage,
		"/projects/{projectKey}/agent-collaboration/approvals",
		"listProjectApprovalTasks",
	),
	humanNonList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/approvals/:approvalID",
		"/projects/{projectKey}/agent-collaboration/approvals/{approvalID}",
		"getProjectApprovalTask",
	),
	humanList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/handoffs",
		ClassificationPage,
		"/projects/{projectKey}/agent-collaboration/handoffs",
		"listProjectHandoffs",
	),
	humanList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/handoffs/:handoffID",
		ClassificationBounded,
		"/projects/{projectKey}/agent-collaboration/handoffs/{handoffID}",
		"getProjectHandoff",
	),
	humanList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/proposals",
		ClassificationPage,
		"/projects/{projectKey}/agent-collaboration/proposals",
		"listProjectActionProposals",
	),
	humanNonList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/proposals/:proposalID",
		"/projects/{projectKey}/agent-collaboration/proposals/{proposalID}",
		"getProjectActionProposal",
	),
	humanList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/runs",
		ClassificationPage,
		"/projects/{projectKey}/agent-collaboration/runs",
		"listProjectAgentRuns",
	),
	humanNonList(
		"internal/handlers/agent_collaboration_handler.go",
		"RegisterRoutes",
		"collaboration",
		"/runs/:runID",
		"/projects/{projectKey}/agent-collaboration/runs/{runID}",
		"getProjectAgentRun",
	),
	humanList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"automation",
		"/logs",
		ClassificationCursor,
		"/projects/{projectKey}/admin/automation/logs",
		"listProjectAutomationLogs",
	),
	humanList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"quickReplies",
		"",
		ClassificationPage,
		"/projects/{projectKey}/admin/automation/quick-replies",
		"listProjectQuickReplies",
	),
	humanList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"rules",
		"",
		ClassificationPage,
		"/projects/{projectKey}/admin/automation/rules",
		"listProjectAutomationRules",
	),
	humanNonList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"rules",
		"/:id",
		"/projects/{projectKey}/admin/automation/rules/{ruleID}",
		"getProjectAutomationRule",
	),
	humanNonList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"rules",
		"/:id/stats",
		"",
		"",
	),
	humanList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"sla",
		"",
		ClassificationPage,
		"/projects/{projectKey}/admin/automation/sla",
		"listProjectSLAConfigs",
	),
	humanList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"templates",
		"",
		ClassificationPage,
		"/projects/{projectKey}/admin/automation/templates",
		"listProjectTicketTemplates",
	),
	humanNonList(
		"internal/handlers/automation_handler.go",
		"RegisterProjectRoutes",
		"templates",
		"/:id",
		"/projects/{projectKey}/admin/automation/templates/{automationConfigID}",
		"getProjectTicketTemplate",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/conflicts",
		ClassificationPage,
		"/projects/{projectKey}/integrations/conflicts",
		"listProjectIntegrationConflicts",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/connections",
		ClassificationPage,
		"/projects/{projectKey}/integrations/connections",
		"listProjectIntegrationConnections",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/connections/:connectionID/mappings",
		ClassificationPage,
		"/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
		"listProjectIntegrationMappings",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/connector-definitions",
		ClassificationPage,
		"/projects/{projectKey}/integrations/connector-definitions",
		"listProjectIntegrationConnectorDefinitions",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/dead-letters",
		ClassificationPage,
		"/projects/{projectKey}/integrations/dead-letters",
		"listProjectIntegrationDeadLetters",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/domain-events",
		ClassificationCursor,
		"/projects/{projectKey}/integrations/domain-events",
		"listProjectIntegrationDomainEvents",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/inbox",
		ClassificationPage,
		"/projects/{projectKey}/integrations/inbox",
		"listProjectIntegrationInboxMessages",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/inbox/:messageID/receipts",
		ClassificationPage,
		"/projects/{projectKey}/integrations/inbox/{messageID}/receipts",
		"listProjectIntegrationInboxReceipts",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/outbox",
		ClassificationPage,
		"/projects/{projectKey}/integrations/outbox",
		"listProjectIntegrationOutboxDeliveries",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/overview",
		ClassificationBounded,
		"/projects/{projectKey}/integrations/overview",
		"getProjectIntegrationOverview",
	),
	humanList(
		"internal/handlers/integration_handler.go",
		"RegisterRoutes",
		"integrations",
		"/sync-runs",
		ClassificationPage,
		"/projects/{projectKey}/integrations/sync-runs",
		"listProjectIntegrationSyncRuns",
	),
	humanList(
		"internal/handlers/knowledge_handler.go",
		"RegisterRoutes",
		"knowledge",
		"/articles",
		ClassificationPage,
		"/projects/{projectKey}/knowledge/articles",
		"listProjectKnowledgeArticles",
	),
	humanList(
		"internal/handlers/knowledge_handler.go",
		"RegisterRoutes",
		"knowledge",
		"/articles/:articleID/versions",
		ClassificationPage,
		"/projects/{projectKey}/knowledge/articles/{articleID}/versions",
		"listProjectKnowledgeVersions",
	),
	humanList(
		"internal/handlers/knowledge_handler.go",
		"RegisterExternalRoutes",
		"knowledge",
		"/articles/:articleID/document",
		ClassificationBounded,
		"/projects/{projectKey}/knowledge/articles/{articleID}/document",
		"getProjectKnowledgeArticleDocument",
	),
	humanNonList(
		"internal/handlers/knowledge_handler.go",
		"RegisterRoutes",
		"knowledge",
		"/index-rebuilds/current",
		"/projects/{projectKey}/knowledge/index-rebuilds/current",
		"getProjectKnowledgeIndexState",
	),
	humanList(
		"internal/handlers/knowledge_handler.go",
		"RegisterRoutes",
		"knowledge",
		"/ingestions",
		ClassificationPage,
		"/projects/{projectKey}/knowledge/ingestions",
		"listProjectKnowledgeIngestions",
	),
	humanList(
		"internal/handlers/project_configuration_handler.go",
		"RegisterRoutes",
		"configuration",
		"/intake",
		ClassificationBounded,
		"/projects/{projectKey}/configuration/intake",
		"getProjectIntakeConfiguration",
	),
	humanNonList(
		"internal/handlers/project_configuration_handler.go",
		"RegisterRoutes",
		"configuration",
		"/releases/current",
		"",
		"",
	),
	humanNonList(
		"internal/handlers/system_handler.go",
		"RegisterRoutes",
		"system",
		"/cleanup/config",
		"",
		"",
	),
	humanList(
		"internal/handlers/system_handler.go",
		"RegisterRoutes",
		"system",
		"/cleanup/logs",
		ClassificationPage,
		"/platform/system/cleanup/logs",
		"listPlatformCleanupLogs",
	),
	humanNonList(
		"internal/handlers/system_handler.go",
		"RegisterRoutes",
		"system",
		"/cleanup/stats",
		"",
		"",
	),
	humanNonList(
		"internal/handlers/ticket_content_handler.go",
		"RegisterExternalRoutes",
		"tickets",
		"/:id/attachments/:attachment_id/content",
		"/projects/{projectKey}/tickets/{ticketID}/attachments/{attachmentID}/content",
		"downloadProjectTicketAttachment",
	),
	humanList(
		"internal/handlers/ticket_content_handler.go",
		"RegisterRoutes",
		"tickets",
		"/:id/attachments",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/attachments",
		"listProjectTicketAttachments",
	),
	humanList(
		"internal/handlers/ticket_content_handler.go",
		"RegisterRoutes",
		"tickets",
		"/:id/comments",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/comments",
		"listProjectTicketComments",
	),
	humanList(
		"internal/handlers/ticket_content_handler.go",
		"RegisterRoutes",
		"tickets",
		"/:id/comments/:comment_id/replies",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies",
		"listProjectTicketCommentReplies",
	),
	humanList(
		"internal/handlers/ticket_relationship_handler.go",
		"RegisterRoutes",
		"tickets",
		"/entity-links",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/entity-links",
		"listProjectTicketEntityLinks",
	),
	humanList(
		"internal/handlers/ticket_relationship_handler.go",
		"RegisterRoutes",
		"tickets",
		"/relations",
		ClassificationPage,
		"/projects/{projectKey}/tickets/{ticketID}/relations",
		"listProjectTicketRelations",
	),
	publicLiteral(
		"internal/humanopenapi/handler.go",
		"RegisterRoutes",
		"router",
		"/human-openapi.json",
	),
	publicLiteral(
		"internal/openapi/handler.go",
		"RegisterRoutes",
		"router",
		"/openapi.yaml",
	),
)

func humanList(
	file string,
	function string,
	receiver string,
	path string,
	classification Classification,
	openAPIPath string,
	operationID string,
) manifestEntry {
	return literalEntry(
		file,
		function,
		receiver,
		path,
		Declaration{
			Classification: classification,
			OpenAPIPath:    openAPIPath,
			OperationID:    operationID,
		},
	)
}

func humanNonList(
	file string,
	function string,
	receiver string,
	path string,
	openAPIPath string,
	operationID string,
) manifestEntry {
	return literalEntry(
		file,
		function,
		receiver,
		path,
		Declaration{
			Classification: ClassificationNonList,
			OpenAPIPath:    openAPIPath,
			OperationID:    operationID,
		},
	)
}

func publicLiteral(
	file string,
	function string,
	receiver string,
	path string,
) manifestEntry {
	return literalEntry(
		file,
		function,
		receiver,
		path,
		Declaration{Classification: ClassificationMachinePublic},
	)
}

func publicExpression(
	file string,
	function string,
	receiver string,
	pathExpression string,
) manifestEntry {
	return manifestEntry{
		fingerprint: makeFingerprint(
			file,
			function,
			receiver,
			pathExpression,
		),
		declaration: Declaration{
			Classification: ClassificationMachinePublic,
		},
	}
}

func literalEntry(
	file string,
	function string,
	receiver string,
	path string,
	declaration Declaration,
) manifestEntry {
	return manifestEntry{
		fingerprint: makeFingerprint(
			file,
			function,
			receiver,
			strconv.Quote(path),
		),
		declaration: declaration,
	}
}

func buildManifest(entries ...manifestEntry) map[string]Declaration {
	result := make(map[string]Declaration, len(entries))
	for _, entry := range entries {
		if _, exists := result[entry.fingerprint]; exists {
			panic("duplicate Human GET manifest entry: " + entry.fingerprint)
		}
		result[entry.fingerprint] = entry.declaration
	}
	return result
}
