import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { joinApiUrl } from '../src/lib/apiUrl.ts'
import {
    humanApiRoutes,
} from '../src/lib/generated/human-api.ts'

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const webDirectory = path.resolve(scriptDirectory, '..')
const repositoryDirectory = path.resolve(webDirectory, '..')
const contract = JSON.parse(
    await readFile(
        path.join(
            repositoryDirectory,
            'server',
            'internal',
            'humanopenapi',
            'openapi.json',
        ),
        'utf8',
    ),
)
const generated = await readFile(
    path.join(webDirectory, 'src', 'lib', 'generated', 'human-api.ts'),
    'utf8',
)
const accessControl = await readFile(
    path.join(webDirectory, 'src', 'lib', 'accessControl.ts'),
    'utf8',
)
const projectScope = await readFile(
    path.join(webDirectory, 'src', 'lib', 'projectScope.ts'),
    'utf8',
)
const readWebSource = (...segments) =>
    readFile(path.join(webDirectory, 'src', ...segments), 'utf8')
const [
    ticketTypes,
    workbenchTypes,
    adminApp,
    auditExplorer,
    workbenchPage,
    emailSettings,
    systemSettings,
    automationLogs,
    webhookSettings,
    agentControl,
    notificationCreate,
    dataProvider,
    humanApiTypeTest,
    ticketConversation,
    ticketWorkflowActions,
    authProvider,
    publicAuthApi,
    loginPage,
    customAppBar,
    loginHistory,
    ticketRelationships,
    agentCollaboration,
    automationSLA,
    automationTemplates,
    automationQuickReplies,
    automationDirectory,
    knowledgeManagement,
    knowledgeApi,
    knowledgeTypes,
    ticketCreate,
    ticketEdit,
    integrationRuntime,
    integrationData,
    integrationTypes,
] = await Promise.all([
    readWebSource('types', 'index.ts'),
    readWebSource('lib', 'types', 'crossProjectWorkbench.ts'),
    readWebSource('AdminApp.tsx'),
    readWebSource('admin', 'audit', 'PlatformAuditExplorer.tsx'),
    readWebSource('admin', 'workbench', 'CrossProjectWorkbench.tsx'),
    readWebSource('admin', 'settings', 'EmailSettings.tsx'),
    readWebSource('admin', 'settings', 'SystemSettings.tsx'),
    readWebSource('admin', 'automation', 'AutomationLogList.tsx'),
    readWebSource('admin', 'settings', 'WebhookSettings.tsx'),
    readWebSource('admin', 'agents', 'AgentControlCenter.tsx'),
    readWebSource('admin', 'notifications', 'NotificationCreate.tsx'),
    readWebSource('lib', 'dataProvider.ts'),
    readWebSource('lib', 'humanApiContract.type-test.ts'),
    readWebSource('admin', 'tickets', 'TicketConversationPanel.tsx'),
    readWebSource('admin', 'tickets', 'TicketWorkflowActions.tsx'),
    readWebSource('lib', 'authProvider.ts'),
    readWebSource('components', 'auth', 'publicAuthApi.ts'),
    readWebSource('components', 'auth', 'LoginPage.tsx'),
    readWebSource('layout', 'CustomAppBar.tsx'),
    readWebSource('admin', 'security', 'LoginHistory.tsx'),
    readWebSource('admin', 'tickets', 'TicketRelationshipsPanel.tsx'),
    readWebSource('admin', 'agents', 'AgentCollaborationWorkspace.tsx'),
    readWebSource('admin', 'automation', 'AutomationSLAList.tsx'),
    readWebSource('admin', 'automation', 'AutomationTemplateList.tsx'),
    readWebSource('admin', 'automation', 'AutomationQuickReplyList.tsx'),
    readWebSource('admin', 'automation', 'useAutomationDirectory.ts'),
    readWebSource('admin', 'knowledge', 'KnowledgeManagementPage.tsx'),
    readWebSource('admin', 'knowledge', 'knowledgeApi.ts'),
    readWebSource('admin', 'knowledge', 'types.ts'),
    readWebSource('admin', 'tickets', 'TicketCreate.tsx'),
    readWebSource('admin', 'tickets', 'TicketEdit.tsx'),
    readWebSource('admin', 'integrations', 'IntegrationRuntime.tsx'),
    readWebSource('admin', 'integrations', 'useIntegrationData.ts'),
    readWebSource('admin', 'integrations', 'integrationTypes.ts'),
])
const knowledgeClient = [
    knowledgeManagement,
    knowledgeApi,
    knowledgeTypes,
].join('\n')

assert.equal(contract.openapi, '3.2.0')
assert.equal(contract['x-chronodesk-types-generator'], '2.1.0')
assert.deepEqual(contract.components.schemas.PlatformRole.enum, [
    'platform_admin',
    'security_auditor',
    'emergency_operator',
    'member',
])
assert.deepEqual(contract.components.schemas.ProjectRole.enum, [
    'project_admin',
    'manager',
    'agent',
    'requester',
    'observer',
])
assert.equal(
    contract.components.schemas.PlatformRole['x-chronodesk-runtime-values'],
    'platformRoleValues',
)
assert.equal(
    contract.components.schemas.ProjectRole['x-chronodesk-runtime-values'],
    'projectRoleValues',
)
for (const [name, schema] of Object.entries(contract.components.schemas)) {
    if (!Array.isArray(schema.allOf)) continue
    const extendsSuccessEnvelope = schema.allOf.some(
        (branch) =>
            branch.$ref === '#/components/schemas/SuccessEnvelope',
    )
    if (!extendsSuccessEnvelope) continue
    assert.equal(
        schema.unevaluatedProperties,
        false,
        `${name} must close composed success fields`,
    )
    assert.equal(
        schema.allOf.some(
            (branch) => branch.additionalProperties === false,
        ),
        false,
        `${name} contains an unsatisfiable allOf branch`,
    )
}

for (const name of [
    'PlatformRole',
    'ProjectRole',
    'LoginRequest',
    'RefreshTokenRequest',
    'LogoutRequest',
    'RegenerateOTPBackupCodesRequest',
    'OTPBackupCodeRegenerationEnvelope',
    'HumanSessionUser',
    'AuthSession',
    'AuthSessionEnvelope',
    'AuthSessionSuccessEnvelope',
    'AuthorizedProject',
    'AuthorizedProjectAccess',
    'ProjectMembership',
    'ProjectMembershipEnvelope',
    'TrustedDevice',
    'TrustedDevicePage',
    'TrustedDevicePageEnvelope',
    'QueueStatus',
    'ProjectQueue',
    'ProjectQueuePage',
    'ProjectQueuePageEnvelope',
    'CleanupLog',
    'CleanupLogPage',
    'CleanupLogPageEnvelope',
    'AdminUser',
    'AdminUserStats',
    'AdminAuditLog',
    'AdminAuditLogPage',
    'PlatformProjectSummary',
    'PlatformProjectSummaryEnvelope',
    'PlatformProjectPage',
    'PlatformProjectPageEnvelope',
    'ProjectCreationContext',
    'PlatformBusinessUnitPage',
    'ProjectUserOptionPage',
    'StandardErrorEnvelope',
    'AuthErrorEnvelope',
    'CodedErrorEnvelope',
    'ErrorEnvelope',
    'LoginHistoryRecord',
    'LoginHistoryPage',
    'ProjectCategory',
    'ProjectCategoryPage',
    'ProjectAssignee',
    'ProjectAssigneePage',
    'TicketEntityLink',
    'TicketEntityLinkPage',
    'TicketRelation',
    'TicketRelationPage',
    'AgentRunSummary',
    'AgentRunDetail',
    'ActionProposalSummary',
    'ApprovalTaskSummary',
    'HandoffSummary',
    'ProjectIntakeConfiguration',
    'SLAConfig',
    'SLAConfigPage',
    'TicketTemplate',
    'TicketTemplatePage',
    'QuickReply',
    'QuickReplyPage',
    'KnowledgeArticle',
    'KnowledgeArticlePage',
    'KnowledgeVersion',
    'KnowledgeVersionPage',
    'KnowledgeIngestion',
    'KnowledgeIngestionPage',
    'CreateKnowledgeArticleRequest',
    'CreateKnowledgeDraftRequest',
    'KnowledgeSource',
    'KnowledgeAuthoredResult',
    'KnowledgeDocumentSection',
    'KnowledgeDocument',
    'KnowledgeSearchRequest',
    'KnowledgeCitation',
    'KnowledgeSearchResult',
    'KnowledgeIndexState',
    'IntegrationConnectorDefinitionSummary',
    'IntegrationConnectorDefinitionPage',
    'IntegrationConnectionSummary',
    'IntegrationConnectionPage',
    'IntegrationMappingSummary',
    'IntegrationMappingPage',
    'IntegrationInboxMessageSummary',
    'IntegrationInboxMessagePage',
    'IntegrationInboxReceiptSummary',
    'IntegrationInboxReceiptPage',
    'IntegrationSyncRunSummary',
    'IntegrationSyncRunPage',
    'IntegrationConflictSummary',
    'IntegrationConflictPage',
    'IntegrationDeadLetterSummary',
    'IntegrationDeadLetterPage',
    'IntegrationDomainEventSummary',
    'IntegrationDomainEventCursorPage',
    'IntegrationOutboxSummary',
    'IntegrationOutboxPage',
    'IntegrationOverview',
    'IntegrationMappingDryRunResult',
    'IntegrationInboundReplayResult',
]) {
    assert.match(generated, new RegExp(`export type ${name} =`))
}
assert.match(generated, /export const platformRoleValues = \[/)
assert.match(generated, /export const projectRoleValues = \[/)
assert.match(
    accessControl,
    /import \{ platformRoleValues \} from '@\/lib\/generated\/human-api'/,
)
assert.match(
    projectScope,
    /import \{ projectRoleValues \} from '@\/lib\/generated\/human-api'/,
)
assert.doesNotMatch(accessControl, /(?:platformRoles|platformRoleValues)\s*=\s*\[/)
assert.doesNotMatch(projectScope, /(?:projectRoles|projectRoleValues)\s*=\s*\[/)

const requiredOperations = [
    ['/auth/forgot-password', 'post'],
    ['/auth/reset-password', 'post'],
    ['/auth/logout', 'post'],
    ['/auth/logout-all', 'post'],
    ['/auth/otp/backup-codes', 'post'],
    ['/auth/me', 'get'],
    ['/auth/profile', 'put'],
    ['/user/trusted-devices', 'get'],
    ['/user/trusted-devices/{deviceID}', 'delete'],
    ['/user/login-history', 'get'],
    ['/user/login-history/{loginHistoryID}', 'delete'],
    ['/projects/{projectKey}/context', 'get'],
    ['/projects/{projectKey}/queues', 'get'],
    ['/projects/{projectKey}/categories', 'get'],
    ['/projects/{projectKey}/assignees', 'get'],
    ['/projects/{projectKey}/memberships', 'get'],
    ['/projects/{projectKey}/memberships', 'post'],
    ['/projects/{projectKey}/memberships/{userID}', 'delete'],
    ['/platform/projects', 'get'],
    ['/platform/projects', 'post'],
    ['/platform/projects/{projectPublicID}/archive', 'post'],
    ['/platform/users', 'get'],
    ['/platform/users', 'post'],
    ['/platform/users/stats', 'get'],
    ['/platform/users/{userID}', 'get'],
    ['/platform/users/{userID}', 'put'],
    ['/platform/users/{userID}', 'delete'],
    ['/platform/users/{userID}/reset-password', 'post'],
    ['/platform/audit-logs', 'get'],
    ['/platform/audit-logs/{auditLogID}', 'get'],
    ['/platform/system/cleanup/logs', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/entity-links', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/entity-links', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}/relations', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/relations', 'post'],
    ['/projects/{projectKey}/tickets/my-tickets', 'get'],
    ['/projects/{projectKey}/tickets/unassigned', 'get'],
    ['/projects/{projectKey}/agent-collaboration/runs', 'get'],
    ['/projects/{projectKey}/agent-collaboration/proposals', 'get'],
    ['/projects/{projectKey}/agent-collaboration/approvals', 'get'],
    ['/projects/{projectKey}/agent-collaboration/handoffs', 'get'],
    ['/projects/{projectKey}/configuration/intake', 'get'],
    ['/projects/{projectKey}/admin/automation/sla', 'get'],
    ['/projects/{projectKey}/admin/automation/templates', 'get'],
    ['/projects/{projectKey}/admin/automation/quick-replies', 'get'],
    ['/projects/{projectKey}/knowledge/articles', 'get'],
    ['/projects/{projectKey}/knowledge/articles', 'post'],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/drafts',
        'post',
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/document',
        'get',
    ],
    ['/projects/{projectKey}/knowledge/articles/{articleID}/versions', 'get'],
    [
        '/projects/{projectKey}/knowledge/versions/{versionID}/publication',
        'post',
    ],
    ['/projects/{projectKey}/knowledge/searches', 'post'],
    ['/projects/{projectKey}/knowledge/ingestions', 'get'],
    ['/projects/{projectKey}/knowledge/index-rebuilds/current', 'get'],
    ['/projects/{projectKey}/knowledge/index-rebuilds', 'post'],
    ['/projects/{projectKey}/integrations/connector-definitions', 'get'],
    ['/projects/{projectKey}/integrations/connector-definitions', 'post'],
    [
        '/projects/{projectKey}/integrations/connector-definitions/{definitionID}',
        'put',
    ],
    ['/projects/{projectKey}/integrations/connections', 'get'],
    ['/projects/{projectKey}/integrations/connections', 'post'],
    [
        '/projects/{projectKey}/integrations/connections/{connectionID}',
        'put',
    ],
    [
        '/projects/{projectKey}/integrations/connections/{connectionID}/mappings',
        'get',
    ],
    [
        '/projects/{projectKey}/integrations/connections/{connectionID}/mappings',
        'post',
    ],
    ['/projects/{projectKey}/integrations/mappings/{mappingID}', 'put'],
    [
        '/projects/{projectKey}/integrations/mappings/{mappingID}/dry-runs',
        'post',
    ],
    [
        '/projects/{projectKey}/integrations/mappings/{mappingID}/publication',
        'post',
    ],
    ['/projects/{projectKey}/integrations/overview', 'get'],
    ['/projects/{projectKey}/integrations/inbox', 'get'],
    [
        '/projects/{projectKey}/integrations/inbox/{messageID}/receipts',
        'get',
    ],
    ['/projects/{projectKey}/integrations/sync-runs', 'get'],
    ['/projects/{projectKey}/integrations/conflicts', 'get'],
    [
        '/projects/{projectKey}/integrations/conflicts/{conflictID}/resolution',
        'post',
    ],
    ['/projects/{projectKey}/integrations/dead-letters', 'get'],
    [
        '/projects/{projectKey}/integrations/dead-letters/{deadLetterID}/replays',
        'post',
    ],
    ['/projects/{projectKey}/integrations/domain-events', 'get'],
    ['/projects/{projectKey}/integrations/outbox', 'get'],
]
for (const [operationPath, method] of requiredOperations) {
    assert.ok(
        contract.paths[operationPath]?.[method],
        `${method.toUpperCase()} ${operationPath} is missing`,
    )
}

const registeredRuntimeListInventory = [
    [
        '/user/trusted-devices',
        'listTrustedDevices',
        'page',
        ['revoked ASC', 'expires_at DESC', 'id DESC'],
    ],
    [
        '/user/login-history',
        'listLoginHistory',
        'page',
        ['login_time DESC', 'id DESC'],
    ],
    [
        '/projects',
        'listAuthorizedHumanProjects',
        'page',
        ['name ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/queues',
        'listProjectQueues',
        'page',
        ['is_default DESC', 'name ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/memberships',
        'listProjectMemberships',
        'page',
        ['is_active DESC', 'role ASC', 'user_id ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/membership-candidates',
        'searchProjectMembershipCandidates',
        'page',
        ['display_name ASC', 'username ASC', 'id ASC'],
    ],
    [
        '/platform/projects',
        'listPlatformProjects',
        'page',
        ['name ASC', 'id ASC'],
    ],
    [
        '/platform/users',
        'listPlatformUsers',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/platform/audit-logs',
        'listPlatformAuditLogs',
        'cursor',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/workbench/tickets',
        'listCrossProjectWorkbenchTickets',
        'page',
        ['updated_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/tickets',
        'listProjectTickets',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/tickets/my-tickets',
        'listMyProjectTickets',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/tickets/unassigned',
        'listUnassignedProjectTickets',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/notifications',
        'listProjectNotifications',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/categories',
        'listProjectCategories',
        'page',
        ['sort_order ASC', 'name ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/assignees',
        'listProjectAssignees',
        'page',
        ['username ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/tickets/{ticketID}/entity-links',
        'listProjectTicketEntityLinks',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/tickets/{ticketID}/relations',
        'listProjectTicketRelations',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/agent-collaboration/runs',
        'listProjectAgentRuns',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/agent-collaboration/proposals',
        'listProjectActionProposals',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/agent-collaboration/approvals',
        'listProjectApprovalTasks',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/agent-collaboration/handoffs',
        'listProjectHandoffs',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/admin/automation/rules',
        'listProjectAutomationRules',
        'page',
        ['priority ASC', 'created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/admin/automation/logs',
        'listProjectAutomationLogs',
        'cursor',
        ['executed_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/admin/automation/sla',
        'listProjectSLAConfigs',
        'page',
        ['is_default DESC', 'created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/admin/automation/templates',
        'listProjectTicketTemplates',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/admin/automation/quick-replies',
        'listProjectQuickReplies',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/knowledge/articles',
        'listProjectKnowledgeArticles',
        'page',
        ['updated_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/versions',
        'listProjectKnowledgeVersions',
        'page',
        ['version DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/knowledge/ingestions',
        'listProjectKnowledgeIngestions',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/connector-definitions',
        'listProjectIntegrationConnectorDefinitions',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/connections',
        'listProjectIntegrationConnections',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/connections/{connectionID}/mappings',
        'listProjectIntegrationMappings',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/inbox',
        'listProjectIntegrationInboxMessages',
        'page',
        ['received_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/inbox/{messageID}/receipts',
        'listProjectIntegrationInboxReceipts',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/sync-runs',
        'listProjectIntegrationSyncRuns',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/conflicts',
        'listProjectIntegrationConflicts',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/dead-letters',
        'listProjectIntegrationDeadLetters',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/domain-events',
        'listProjectIntegrationDomainEvents',
        'cursor',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/integrations/outbox',
        'listProjectIntegrationOutboxDeliveries',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/platform/system/cleanup/logs',
        'listPlatformCleanupLogs',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/platform/configs',
        'listPlatformConfigs',
        'page',
        ['category ASC', 'group ASC', 'key ASC', 'id ASC'],
    ],
    [
        '/projects/{projectKey}/webhooks',
        'listProjectWebhooks',
        'page',
        ['created_at DESC', 'id DESC'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}/logs',
        'listProjectWebhookLogs',
        'cursor',
        ['created_at DESC', 'id DESC'],
    ],
]
for (const [operationPath, operationId, strategy, stableSort] of
    registeredRuntimeListInventory) {
    const operation = contract.paths[operationPath]?.get
    assert.ok(operation, `GET ${operationPath} is absent from Human OpenAPI`)
    assert.equal(operation.operationId, operationId)
    assert.equal(operation['x-list-strategy'], strategy)
    assert.deepEqual(operation['x-stable-sort'], stableSort)
}

for (const [source, helperNames, forbiddenPath] of [
    [
        loginHistory,
        ['listLoginHistory'],
        /\/user\/login-history\?/,
    ],
    [
        ticketRelationships,
        [
            'listProjectTicketEntityLinks',
            'listProjectTicketRelations',
            'createProjectTicketEntityLink',
            'createProjectTicketRelation',
        ],
        /\/tickets\/\$\{[^}]+\}\/(?:entity-links|relations)/,
    ],
    [
        agentCollaboration,
        [
            'listProjectAgentRuns',
            'listProjectActionProposals',
            'listProjectApprovalTasks',
            'listProjectHandoffs',
            'decideProjectAgentApproval',
            'takeOverProjectAgentRun',
        ],
        /\/agent-collaboration\//,
    ],
    [
        automationDirectory,
        [
            'listProjectSLAConfigs',
            'listProjectTicketTemplates',
            'listProjectQuickReplies',
            'createProjectSLAConfig',
            'createProjectTicketTemplate',
            'createProjectQuickReply',
            'useProjectQuickReply',
        ],
        /\/admin\/automation\//,
    ],
    [
        knowledgeClient,
        [
            'listProjectKnowledgeArticles',
            'listProjectKnowledgeVersions',
            'createProjectKnowledgeArticle',
            'createProjectKnowledgeArticleDraft',
            'getProjectKnowledgeArticleDocument',
            'publishProjectKnowledgeVersion',
            'searchProjectKnowledge',
        ],
        /\/knowledge\//,
    ],
    [
        integrationData,
        [
            'listProjectIntegrationConnectorDefinitions',
            'listProjectIntegrationConnections',
            'listProjectIntegrationMappings',
            'listProjectIntegrationInboxMessages',
            'listProjectIntegrationInboxReceipts',
            'listProjectIntegrationSyncRuns',
            'listProjectIntegrationConflicts',
            'listProjectIntegrationDeadLetters',
            'listProjectIntegrationDomainEvents',
            'listProjectIntegrationOutboxDeliveries',
            'getProjectIntegrationOverview',
        ],
        /\/projects\/\$\{[^}]+\}\/integrations\//,
    ],
    [
        integrationRuntime,
        [
            'listAgentOutboxDeliveries',
            'replayOutboxDeliveryV2',
            'resolveProjectIntegrationConflict',
            'replayProjectIntegrationDeadLetter',
        ],
        /\/projects\/\$\{[^}]+\}\/integrations\//,
    ],
    [
        ticketCreate,
        ['getProjectIntakeConfiguration'],
        /configuration\/intake/,
    ],
]) {
    for (const helperName of helperNames) {
        assert.match(
            source,
            new RegExp(`humanApiRoutes\\.${helperName}\\b`),
            `${helperName} generated route helper is not consumed`,
        )
    }
    assert.doesNotMatch(
        source,
        forbiddenPath,
        'Human API path must come from the generated route registry',
    )
}
for (const [source, typeNames] of [
    [loginHistory, ['LoginHistoryPage', 'LoginHistoryRecord']],
    [
        ticketRelationships,
        [
            'TicketEntityLinkPage',
            'TicketRelationPage',
            'AddTicketEntityLinkResult',
            'AddTicketRelationResult',
        ],
    ],
    [
        agentCollaboration,
        [
            'AgentRunSummary',
            'ActionProposalSummary',
            'ApprovalTaskSummary',
            'HandoffSummary',
        ],
    ],
    [automationSLA, ['SLAConfig']],
    [automationTemplates, ['TicketTemplate']],
    [automationQuickReplies, ['QuickReply']],
    [
        knowledgeClient,
        [
            'KnowledgeArticlePage',
            'KnowledgeVersionPage',
            'KnowledgeAuthoredResult',
            'KnowledgeDocument',
            'KnowledgeSearchResult',
        ],
    ],
    [
        integrationTypes,
        [
            'IntegrationConnectorDefinitionSummary',
            'IntegrationConnectionSummary',
            'IntegrationMappingSummary',
            'IntegrationInboxMessageSummary',
            'IntegrationInboxReceiptSummary',
            'IntegrationSyncRunSummary',
            'IntegrationConflictSummary',
            'IntegrationDeadLetterSummary',
            'IntegrationDomainEventSummary',
            'IntegrationOutboxSummary',
            'IntegrationOverview',
        ],
    ],
    [
        integrationRuntime,
        [
            'AdminOutboxPage',
            'ReplayIntegrationDeadLetterRequest',
            'ResolveIntegrationConflictRequest',
        ],
    ],
    [ticketCreate, ['ProjectIntakeConfiguration']],
]) {
    for (const typeName of typeNames) {
        assert.match(
            source,
            new RegExp(`\\b${typeName}\\b`),
            `${typeName} generated type is not consumed`,
        )
    }
}
assert.match(
    integrationRuntime,
    /role === 'project_admin'\s*&&\s*hasProjectCapability\(role,\s*'manage_agents'\)/u,
    'Integration Outbox replay must require project_admin manage_agents',
)
assert.match(
    integrationRuntime,
    /confirmation\.kind === 'outbox-replay'\s*&&\s*!canReplayOutbox/u,
    'Integration Outbox replay execution must fail closed without manage_agents',
)
assert.match(
    integrationRuntime,
    /row\.status === 'failed'\s*\|\|\s*row\.status === 'dead'/u,
    'Integration Outbox replay must be limited to failed or dead deliveries',
)
assert.match(
    integrationRuntime,
    /'Idempotency-Key': newIdempotencyKey\(\)[\s\S]*'If-Match': formatResourceVersion\(delivery\.resource_version\)/u,
    'Integration Outbox replay must preserve idempotency and version preconditions',
)
for (const [schemaName, forbidden] of [
    [
        'LoginHistoryRecord',
        ['user_id', 'username', 'email', 'session_id'],
    ],
    [
        'TicketTemplate',
        ['organization_id', 'project_id', 'created_user', 'assign_to_user'],
    ],
    [
        'QuickReply',
        ['organization_id', 'project_id', 'created_user', 'created_by'],
    ],
    [
        'IntakeRequestTypeVersion',
        [
            'organization_id',
            'project_id',
            'created_by_type',
            'created_by_id',
            'content_hash',
        ],
    ],
]) {
    const properties = contract.components.schemas[schemaName].properties
    for (const field of forbidden) {
        assert.equal(
            Object.hasOwn(properties, field),
            false,
            `${schemaName} exposes forbidden field ${field}`,
        )
    }
}

const allPlatformRoles = [
    'platform_admin',
    'security_auditor',
    'emergency_operator',
    'member',
]
const allProjectRoles = [
    'project_admin',
    'manager',
    'agent',
    'requester',
    'observer',
]
const exactRoleAllowlists = [
    [
        '/auth/logout-all',
        'post',
        'x-chronodesk-platform-roles',
        allPlatformRoles,
    ],
    ['/auth/me', 'get', 'x-chronodesk-platform-roles', allPlatformRoles],
    ['/auth/profile', 'put', 'x-chronodesk-platform-roles', allPlatformRoles],
    [
        '/user/trusted-devices',
        'get',
        'x-chronodesk-platform-roles',
        allPlatformRoles,
    ],
    [
        '/user/trusted-devices/{deviceID}',
        'delete',
        'x-chronodesk-platform-roles',
        allPlatformRoles,
    ],
    [
        '/projects/{projectKey}/context',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/tickets',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/tickets',
        'post',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager', 'agent', 'requester'],
    ],
    [
        '/projects/{projectKey}/tickets/{ticketID}',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/tickets/{ticketID}',
        'put',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager', 'agent', 'requester'],
    ],
    [
        '/projects/{projectKey}/memberships',
        'get',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/queues',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/memberships',
        'post',
        'x-chronodesk-project-roles',
        ['project_admin'],
    ],
    [
        '/projects/{projectKey}/memberships/{userID}',
        'delete',
        'x-chronodesk-project-roles',
        ['project_admin'],
    ],
    [
        '/projects/{projectKey}/knowledge/articles',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/knowledge/articles',
        'post',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/drafts',
        'post',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/document',
        'get',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/knowledge/versions/{versionID}/publication',
        'post',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/knowledge/searches',
        'post',
        'x-chronodesk-project-roles',
        allProjectRoles,
    ],
    [
        '/projects/{projectKey}/webhooks',
        'get',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks',
        'post',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}',
        'get',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}',
        'put',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}',
        'delete',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}/test',
        'post',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}/logs',
        'get',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}/stats',
        'get',
        'x-chronodesk-project-roles',
        ['project_admin', 'manager'],
    ],
    [
        '/platform/projects',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/projects',
        'post',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/projects/{projectPublicID}/archive',
        'post',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users',
        'post',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users/stats',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users/{userID}',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users/{userID}',
        'put',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users/{userID}',
        'delete',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/users/{userID}/reset-password',
        'post',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
    [
        '/platform/audit-logs',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin', 'security_auditor'],
    ],
    [
        '/platform/audit-logs/{auditLogID}',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin', 'security_auditor'],
    ],
    [
        '/platform/system/cleanup/logs',
        'get',
        'x-chronodesk-platform-roles',
        ['platform_admin'],
    ],
]
for (const [operationPath, method, extension, expected] of exactRoleAllowlists) {
    const operation = contract.paths[operationPath][method]
    assert.ok(operation.security?.length > 0)
    assert.equal(
        Object.hasOwn(operation, 'x-chronodesk-platform-roles'),
        extension === 'x-chronodesk-platform-roles',
    )
    assert.equal(
        Object.hasOwn(operation, 'x-chronodesk-project-roles'),
        extension === 'x-chronodesk-project-roles',
    )
    assert.deepEqual(operation[extension], expected)
}
assert.equal(
    Object.hasOwn(
        contract.paths['/projects'].get,
        'x-chronodesk-platform-roles',
    ),
    false,
)
assert.equal(
    Object.hasOwn(contract.paths['/projects'].get, 'x-chronodesk-project-roles'),
    false,
)

for (const [
    operationPath,
    method,
    operationId,
    strategy,
    successStatus,
    responseRef,
    requestRef,
    visibility,
] of [
    [
        '/projects/{projectKey}/knowledge/articles',
        'get',
        'listProjectKnowledgeArticles',
        'page',
        '200',
        '#/components/schemas/KnowledgeArticlePageEnvelope',
        undefined,
        'published-live-acl-or-explicit-management-view',
    ],
    [
        '/projects/{projectKey}/knowledge/articles',
        'post',
        'createProjectKnowledgeArticle',
        'bounded',
        '201',
        '#/components/schemas/KnowledgeAuthoredEnvelope',
        '#/components/schemas/CreateKnowledgeArticleRequest',
        undefined,
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/drafts',
        'post',
        'createProjectKnowledgeArticleDraft',
        'bounded',
        '201',
        '#/components/schemas/KnowledgeAuthoredEnvelope',
        '#/components/schemas/CreateKnowledgeDraftRequest',
        undefined,
    ],
    [
        '/projects/{projectKey}/knowledge/articles/{articleID}/document',
        'get',
        'getProjectKnowledgeArticleDocument',
        'bounded',
        '200',
        '#/components/schemas/KnowledgeDocumentEnvelope',
        undefined,
        'manager-or-published-read-acl-or-draft-manage-acl',
    ],
    [
        '/projects/{projectKey}/knowledge/versions/{versionID}/publication',
        'post',
        'publishProjectKnowledgeVersion',
        undefined,
        '200',
        '#/components/schemas/KnowledgeVersionEnvelope',
        undefined,
        undefined,
    ],
    [
        '/projects/{projectKey}/knowledge/searches',
        'post',
        'searchProjectKnowledge',
        'bounded',
        '200',
        '#/components/schemas/KnowledgeSearchEnvelope',
        '#/components/schemas/KnowledgeSearchRequest',
        'published-live-acl',
    ],
]) {
    const operation = contract.paths[operationPath][method]
    assert.equal(operation.operationId, operationId)
    if (strategy !== undefined) {
        assert.equal(operation['x-list-strategy'], strategy)
    }
    assert.equal(
        operation.responses[successStatus].content['application/json'].schema.$ref,
        responseRef,
    )
    if (requestRef !== undefined) {
        assert.equal(
            operation.requestBody.content['application/json'].schema.$ref,
            requestRef,
        )
    }
    if (visibility !== undefined) {
        assert.equal(
            operation['x-chronodesk-knowledge-visibility'],
            visibility,
        )
    }
}
assert.deepEqual(
    contract.paths['/projects/{projectKey}/knowledge/articles'].get[
        'x-stable-sort'
    ],
    ['updated_at DESC', 'id DESC'],
)

for (const [schemaName, required, propertyNames] of [
    [
        'CreateKnowledgeArticleRequest',
        ['key', 'title', 'markdown'],
        [
            'key',
            'title',
            'summary',
            'markdown',
            'source_ticket_id',
            'source_attachment_ids',
        ],
    ],
    [
        'CreateKnowledgeDraftRequest',
        ['title', 'markdown'],
        [
            'title',
            'markdown',
            'source_ticket_id',
            'source_attachment_ids',
        ],
    ],
    ['KnowledgeSearchRequest', ['query'], ['query', 'limit']],
]) {
    const schema = contract.components.schemas[schemaName]
    assert.equal(schema.additionalProperties, false)
    assert.deepEqual(schema.required, required)
    assert.deepEqual(Object.keys(schema.properties), propertyNames)
}
for (const schemaName of [
    'CreateKnowledgeArticleRequest',
    'CreateKnowledgeDraftRequest',
]) {
    const schema = contract.components.schemas[schemaName]
    assert.equal(schema.properties.markdown.maxLength, 128 * 1024)
    assert.equal(
        schema.properties.markdown['x-max-utf8-bytes'],
        128 * 1024,
    )
    assert.equal(schema.properties.source_attachment_ids.maxItems, 20)
    assert.equal(schema.properties.source_attachment_ids.uniqueItems, true)
    assert.equal(
        typeof schema['x-chronodesk-source-constraint'],
        'string',
    )
}
const knowledgeSourceProperties =
    contract.components.schemas.KnowledgeSource.properties
assert.deepEqual(Object.keys(knowledgeSourceProperties), [
    'ordinal',
    'kind',
    'visibility',
    'reference_label',
    'source_ticket_id',
    'source_attachment_id',
    'ticket_number',
    'ticket_title',
    'attachment_name',
    'attachment_hash',
])
assert.deepEqual(
    contract.components.schemas.KnowledgeSource.required,
    ['ordinal', 'kind', 'visibility', 'reference_label'],
)
assert.deepEqual(
    knowledgeSourceProperties.visibility.enum,
    ['full', 'restricted', 'unavailable'],
)
for (const forbidden of [
    'id',
    'article_id',
    'version_id',
    'created_at',
    'organization_id',
    'project_id',
    'created_by',
    'created_by_type',
    'created_by_id',
    'object_bucket',
    'object_key',
]) {
    assert.equal(Object.hasOwn(knowledgeSourceProperties, forbidden), false)
}
assert.equal(
    contract.components.schemas.KnowledgeAuthoredResult.properties.sources
        .maxItems,
    20,
)
assert.equal(
    contract.components.schemas.KnowledgeAuthoredResult.properties.receipt.$ref,
    '#/components/schemas/Receipt',
)
assert.equal(
    contract.components.schemas.KnowledgeDocument.properties.sections.maxItems,
    100,
)
assert.equal(
    contract.components.schemas.KnowledgeDocument.properties.sources.maxItems,
    20,
)
assert.equal(
    contract.components.schemas.KnowledgeSearchResult.properties.items.maxItems,
    50,
)

const inlineQueryParameters = (operationPath) =>
    Object.fromEntries(
        contract.paths[operationPath].get.parameters
            .map((parameter) => {
                if (!parameter.$ref) return parameter
                const prefix = '#/components/parameters/'
                assert.ok(parameter.$ref.startsWith(prefix))
                return contract.components.parameters[
                    parameter.$ref.slice(prefix.length)
                ]
            })
            .filter((parameter) => parameter.in === 'query')
            .map((parameter) => [parameter.name, parameter]),
    )
for (const [
    operationPath,
    names,
    sortByDefault,
    sortOrderDefault,
    sortFields,
    responseRef,
] of [
    [
        '/user/trusted-devices',
        ['page', 'page_size', 'sort_by', 'sort_order'],
        'revoked',
        'asc',
        [
            'created_at',
            'updated_at',
            'last_used_at',
            'expires_at',
            'revoked',
            'device_name',
        ],
        '#/components/schemas/TrustedDevicePageEnvelope',
    ],
    [
        '/projects/{projectKey}/queues',
        ['page', 'page_size', 'sort_by', 'sort_order'],
        'is_default',
        'desc',
        ['created_at', 'updated_at', 'name', 'key', 'is_default'],
        '#/components/schemas/ProjectQueuePageEnvelope',
    ],
    [
        '/platform/system/cleanup/logs',
        ['page', 'page_size', 'sort_by', 'sort_order', 'task_type'],
        'created_at',
        'desc',
        [
            'created_at',
            'start_time',
            'end_time',
            'status',
            'task_type',
            'records_deleted',
        ],
        '#/components/schemas/CleanupLogPageEnvelope',
    ],
]) {
    const query = inlineQueryParameters(operationPath)
    assert.deepEqual(Object.keys(query).sort(), [...names].sort())
    assert.deepEqual(
        [query.page.schema.minimum, query.page.schema.default],
        [1, 1],
    )
    assert.deepEqual(
        [
            query.page_size.schema.minimum,
            query.page_size.schema.maximum,
            query.page_size.schema.default,
        ],
        [1, 100, 25],
    )
    assert.equal(query.sort_by.schema.default, sortByDefault)
    assert.deepEqual(query.sort_by.schema.enum, sortFields)
    assert.equal(query.sort_order.schema.default, sortOrderDefault)
    assert.deepEqual(query.sort_order.schema.enum, ['asc', 'desc'])
    assert.equal(
        contract.paths[operationPath].get.responses['200'].content[
            'application/json'
        ].schema.$ref,
        responseRef,
    )
    for (const status of ['400', '401', '500']) {
        assert.ok(contract.paths[operationPath].get.responses[status])
    }
}
assert.equal(
    inlineQueryParameters('/projects/{projectKey}/memberships').sort_by.schema
        .default,
    'is_active',
)
assert.equal(
    inlineQueryParameters('/projects/{projectKey}/memberships').sort_order.schema
        .default,
    'desc',
)
assert.deepEqual(
    contract.paths['/projects/{projectKey}/memberships'].get['x-stable-sort'],
    ['is_active DESC', 'role ASC', 'user_id ASC', 'id ASC'],
)
const membershipUpsert =
    contract.paths['/projects/{projectKey}/memberships'].post
const membershipUpsertSchema =
    contract.components.schemas.UpsertProjectMembershipRequest
assert.ok(membershipUpsertSchema.required.includes('expected_version'))
assert.equal(
    membershipUpsertSchema.properties.expected_version.minimum,
    0,
)
for (const status of ['409', '428']) {
    assert.ok(membershipUpsert.responses[status])
}
const membershipDeactivate =
    contract.paths['/projects/{projectKey}/memberships/{userID}'].delete
const membershipDeactivateExpectedVersion =
    membershipDeactivate.parameters.find(
        (parameter) =>
            parameter.in === 'query' &&
            parameter.name === 'expected_version',
    )
assert.ok(membershipDeactivateExpectedVersion)
assert.equal(membershipDeactivateExpectedVersion.required, true)
assert.equal(membershipDeactivateExpectedVersion.schema.minimum, 1)
for (const status of ['409', '428']) {
    assert.ok(membershipDeactivate.responses[status])
}
assert.deepEqual(
    Object.keys(contract.components.schemas.TrustedDevice.properties).sort(),
    [
        'id',
        'device_name',
        'last_used_at',
        'last_ip',
        'user_agent',
        'expires_at',
        'revoked',
        'created_at',
        'updated_at',
    ].sort(),
)
const projectQueueProperties =
    contract.components.schemas.ProjectQueue.properties
assert.deepEqual(Object.keys(projectQueueProperties).sort(), [
    'created_at',
    'description',
    'is_default',
    'key',
    'name',
    'public_id',
    'status',
    'team_name',
    'team_public_id',
    'updated_at',
])
for (const forbidden of ['id', 'project_id', 'project', 'team_id', 'team']) {
    assert.equal(Object.hasOwn(projectQueueProperties, forbidden), false)
}
assert.deepEqual(
    Object.keys(contract.components.schemas.CleanupLog.properties).sort(),
    [
        'id',
        'created_at',
        'task_type',
        'status',
        'start_time',
        'end_time',
        'duration',
        'records_processed',
        'records_deleted',
        'error_message',
        'retention_days',
        'cutoff_date',
        'trigger_type',
        'trigger_by',
    ].sort(),
)
for (const [operationPath, statuses, response] of [
    [
        '/projects/{projectKey}/admin/automation/rules',
        ['400', '500'],
        '#/components/responses/LegacyError',
    ],
    [
        '/projects/{projectKey}/admin/automation/logs',
        ['400', '500', '503'],
        '#/components/responses/LegacyError',
    ],
    [
        '/projects/{projectKey}/webhooks',
        ['400', '500'],
        '#/components/responses/StandardError',
    ],
    [
        '/projects/{projectKey}/webhooks/{webhookID}/logs',
        ['400', '404', '500', '503'],
        '#/components/responses/StandardError',
    ],
]) {
    for (const status of statuses) {
        assert.equal(
            contract.paths[operationPath].get.responses[status].$ref,
            response,
        )
    }
}
assert.equal(
    contract.components.responses.LegacyError.content['application/json'].schema
        .$ref,
    '#/components/schemas/LegacyErrorEnvelope',
)
assert.equal(
    contract.components.responses.StandardError.content['application/json']
        .schema.$ref,
    '#/components/schemas/StandardErrorEnvelope',
)

assert.equal(
    contract.paths['/auth/login'].post.responses['200'].content['application/json']
        .schema.$ref,
    '#/components/schemas/AuthSessionEnvelope',
)
assert.equal(
    contract.paths['/auth/login'].post.requestBody.required,
    true,
)
assert.equal(
    contract.paths['/auth/login'].post.requestBody.content['application/json']
        .schema.$ref,
    '#/components/schemas/LoginRequest',
)
assert.equal(
    contract.components.schemas.LoginRequest.additionalProperties,
    false,
)
assert.deepEqual(contract.components.schemas.LoginRequest.required, [
    'email',
    'password',
])
assert.deepEqual(
    Object.keys(contract.components.schemas.LoginRequest.properties),
    ['email', 'password', 'otp_code', 'remember_device', 'device_name'],
)
assert.equal(
    Object.hasOwn(
        contract.components.schemas.LoginRequest.properties,
        'device_token',
    ),
    false,
)
for (const [schemaName, propertyName] of [
    ['RegisterHumanRequest', 'email'],
    ['LoginRequest', 'email'],
    ['LoginRequest', 'device_name'],
    ['ForgotPasswordRequest', 'email'],
    ['ResendHumanEmailVerificationRequest', 'email'],
]) {
    assert.equal(
        contract.components.schemas[schemaName].properties[propertyName]
            .maxLength,
        100,
    )
}
assert.equal(
    contract.paths['/auth/refresh'].post.responses['200'].content[
        'application/json'
    ].schema.$ref,
    '#/components/schemas/AuthSessionSuccessEnvelope',
)
assert.equal(
    contract.paths['/auth/refresh'].post.requestBody.required,
    true,
)
assert.equal(
    contract.paths['/auth/refresh'].post.requestBody.content['application/json']
        .schema.$ref,
    '#/components/schemas/RefreshTokenRequest',
)
assert.equal(
    contract.components.schemas.RefreshTokenRequest.additionalProperties,
    false,
)
assert.deepEqual(contract.components.schemas.RefreshTokenRequest.required, [
    'refresh_token',
])
assert.deepEqual(
    Object.keys(contract.components.schemas.RefreshTokenRequest.properties),
    ['refresh_token'],
)
assert.equal(contract.paths['/auth/logout'].post.requestBody.required, false)
assert.equal(
    contract.paths['/auth/logout'].post.requestBody.content['application/json']
        .schema.$ref,
    '#/components/schemas/LogoutRequest',
)
assert.equal(
    contract.components.schemas.LogoutRequest.additionalProperties,
    false,
)
assert.deepEqual(
    Object.keys(contract.components.schemas.LogoutRequest.properties),
    ['refresh_token'],
)
for (const [operationPath, expectedStatuses] of [
    ['/auth/register', ['201', '400', '409', '413', '429', '500', '503']],
    ['/auth/forgot-password', ['200', '400', '413', '429', '503']],
    [
        '/auth/reset-password',
        ['200', '400', '413', '429', '500', '503'],
    ],
    ['/auth/verify-email', ['200', '400', '413', '429', '500', '503']],
    ['/auth/resend-verification', ['200', '400', '413', '429', '503']],
    ['/auth/login', ['200', '400', '401', '403', '413', '429', '503']],
    ['/auth/refresh', ['200', '400', '401', '408', '413', '429', '503']],
    ['/auth/logout', ['200', '400', '413', '429', '503']],
    ['/auth/logout-all', ['200', '401', '429', '500', '503']],
]) {
    assert.deepEqual(
        Object.keys(contract.paths[operationPath].post.responses),
        expectedStatuses,
    )
}
assert.deepEqual(
    Object.keys(contract.paths['/auth/profile'].put.responses),
    ['200', '400', '401', '413'],
)
assert.equal(
    contract.paths['/platform/projects'].get.responses['200'].content[
        'application/json'
    ].schema.$ref,
    '#/components/schemas/PlatformProjectPageEnvelope',
)
assert.equal(
    contract.paths['/platform/projects'].post.responses['201'].content[
        'application/json'
    ].schema.$ref,
    '#/components/schemas/PlatformProjectSummaryEnvelope',
)
const archiveProjectOperation =
    contract.paths['/platform/projects/{projectPublicID}/archive'].post
assert.equal(
    archiveProjectOperation.responses['200'].content[
        'application/json'
    ].schema.$ref,
    '#/components/schemas/PlatformProjectSummaryEnvelope',
)
assert.equal(
    contract.components.schemas.PlatformProjectSummaryEnvelope
        .additionalProperties,
    false,
)
assert.deepEqual(
    contract.components.schemas.PlatformProjectSummaryEnvelope.required,
    ['code', 'msg', 'data'],
)
assert.equal(
    contract.components.schemas.PlatformProjectSummaryEnvelope.properties.data
        .$ref,
    '#/components/schemas/PlatformProjectSummary',
)
assert.equal(
    contract.components.schemas.PlatformProjectPageEnvelope
        .additionalProperties,
    false,
)
assert.deepEqual(
    contract.components.schemas.PlatformProjectPageEnvelope.required,
    ['code', 'msg', 'data'],
)
assert.equal(
    contract.components.schemas.PlatformProjectPageEnvelope.properties
        .data.$ref,
    '#/components/schemas/PlatformProjectPage',
)
assert.deepEqual(
    contract.paths['/platform/projects'].get.parameters.map(
        (parameter) => parameter.name,
    ),
    [
        'page',
        'page_size',
        'search',
        'status',
        'business_unit_public_id',
        'order_by',
        'order',
    ],
)
assert.deepEqual(
    Object.keys(contract.paths['/platform/projects'].get.responses),
    ['200', '400', '401', '403', '429', '500', '503'],
)
assert.deepEqual(Object.keys(archiveProjectOperation.responses), [
    '200',
    '400',
    '401',
    '403',
    '404',
    '409',
    '429',
    '500',
    '503',
])
assert.deepEqual(archiveProjectOperation.parameters, [
    { $ref: '#/components/parameters/ProjectPublicID' },
])
assert.equal(
    contract.components.schemas.HumanUserProfile.additionalProperties,
    false,
)
for (const forbidden of [
    'id',
    'project_id',
    'organization_id',
    'business_unit_id',
    'scope',
]) {
    assert.equal(
        Object.hasOwn(
            contract.components.schemas.PlatformProjectSummary.properties,
            forbidden,
        ),
        false,
    )
}

const authorizedProjectFields = [
    'id',
    'public_id',
    'created_at',
    'updated_at',
    'organization_id',
    'business_unit_id',
    'key',
    'name',
    'description',
    'status',
]
assert.equal(
    contract.components.schemas.AuthorizedProject.additionalProperties,
    false,
)
assert.deepEqual(
    contract.components.schemas.AuthorizedProject.required,
    authorizedProjectFields,
)
assert.deepEqual(
    Object.keys(contract.components.schemas.AuthorizedProject.properties),
    authorizedProjectFields,
)
for (const unstable of [
    'organization',
    'business_unit',
    'ticket_sequence',
]) {
    assert.equal(
        Object.hasOwn(
            contract.components.schemas.AuthorizedProject.properties,
            unstable,
        ),
        false,
    )
}

for (const name of [
    'StandardErrorEnvelope',
    'AuthErrorEnvelope',
    'CodedErrorEnvelope',
    'RecoveryErrorEnvelope',
]) {
    assert.equal(contract.components.schemas[name].additionalProperties, false)
}
assert.equal(
    contract.components.schemas.AuthErrorEnvelope.properties.code.type,
    'string',
)
assert.deepEqual(
    contract.components.schemas.ErrorEnvelope.oneOf.map((item) => item.$ref),
    [
        '#/components/schemas/StandardErrorEnvelope',
        '#/components/schemas/AuthErrorEnvelope',
        '#/components/schemas/CodedErrorEnvelope',
        '#/components/schemas/RecoveryErrorEnvelope',
    ],
)

const parameterName = (reference) => {
    const prefix = '#/components/parameters/'
    assert.ok(reference.startsWith(prefix))
    return contract.components.parameters[reference.slice(prefix.length)].name
}
assert.deepEqual(
    contract.paths['/platform/users'].get.parameters.map((item) =>
        parameterName(item.$ref),
    ),
    [
        'page',
        'page_size',
        'platform_role',
        'status',
        'search',
        'order_by',
        'order',
    ],
)
assert.deepEqual(
    contract.paths['/platform/audit-logs'].get.parameters.map((item) =>
        parameterName(item.$ref),
    ),
    [
        'user_id',
        'actor',
        'platform_role',
        'action',
        'method',
        'path',
        'path_prefix',
        'status',
        'keyword',
        'result',
        'time_preset',
        'start_time',
        'end_time',
        'limit',
        'cursor',
    ],
)

for (const schema of [
    contract.components.parameters.ProjectKey.schema,
    contract.components.schemas.CreatePlatformProjectRequest.properties.key,
    contract.components.schemas.PlatformProjectSummary.properties.key,
    contract.components.schemas.AuthorizedProject.properties.key,
]) {
    assert.equal(schema.minLength, 1)
    assert.equal(schema.maxLength, 32)
    assert.equal(schema.pattern, '^[A-Z][A-Z0-9_-]{0,31}$')
}
const projectPublicIDPattern =
    '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
assert.equal(
    contract.components.parameters.ProjectPublicID.schema.format,
    'uuid',
)
assert.equal(
    contract.components.parameters.ProjectPublicID.schema.pattern,
    projectPublicIDPattern,
)
for (const schema of [
    contract.components.schemas.PublicUUIDv7,
    contract.components.schemas.AuthorizedProject.properties.public_id,
]) {
    assert.equal(schema.format, 'uuid')
    assert.equal(schema.pattern, projectPublicIDPattern)
}

assert.deepEqual(contract.components.schemas.HumanSessionUser.required, [
    'id',
    'username',
    'email',
    'platform_role',
    'status',
    'email_verified',
    'otp_enabled',
    'last_login_at',
])
assert.deepEqual(contract.components.schemas.AdminUser.required, [
    'id',
    'created_at',
    'updated_at',
    'username',
    'email',
    'phone',
    'first_name',
    'last_name',
    'display_name',
    'avatar',
    'timezone',
    'language',
    'platform_role',
    'status',
    'email_verified',
    'phone_verified',
    'two_factor_enabled',
    'last_login_at',
    'department',
    'job_title',
    'manager_id',
    'tickets_created',
    'tickets_assigned',
    'tickets_resolved',
])

assert.doesNotMatch(generated, /eslint-disable/)
assert.doesNotMatch(
    generated,
    /export type PlatformRole = [^\n]*(?:"admin"|"supervisor"|"customer")/,
)
assert.match(generated, /profile\?: HumanUserProfile/)
assert.match(generated, /project: AuthorizedProject/)
assert.match(generated, /scope: ProjectScope/)

const p1Operations = [
    ['/auth/forgot-password', 'post'],
    ['/auth/reset-password', 'post'],
    ['/platform/projects/{projectPublicID}/archive', 'post'],
    ['/workbench/tickets', 'get'],
    ['/projects/{projectKey}/tickets', 'get'],
    ['/projects/{projectKey}/tickets', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}', 'put'],
    ['/projects/{projectKey}/tickets/{ticketID}', 'delete'],
    ['/projects/{projectKey}/tickets/overdue', 'get'],
    ['/projects/{projectKey}/tickets/sla-breach', 'get'],
    ['/projects/{projectKey}/notifications', 'get'],
    ['/projects/{projectKey}/notifications', 'post'],
    ['/projects/{projectKey}/notifications/{notificationID}', 'delete'],
    ['/projects/{projectKey}/notifications/{notificationID}/read', 'put'],
    ['/projects/{projectKey}/notifications/read-all', 'put'],
    ['/projects/{projectKey}/notifications/unread-count', 'get'],
    ['/notification-preferences', 'get'],
    ['/notification-preferences', 'put'],
    ['/projects/{projectKey}/admin/automation/rules', 'get'],
    ['/projects/{projectKey}/admin/automation/rules', 'post'],
    ['/projects/{projectKey}/admin/automation/rules/{ruleID}', 'get'],
    ['/projects/{projectKey}/admin/automation/rules/{ruleID}', 'put'],
    ['/projects/{projectKey}/admin/automation/rules/{ruleID}', 'delete'],
    ['/projects/{projectKey}/admin/automation/logs', 'get'],
    ['/platform/email-config', 'get'],
    ['/platform/email-config', 'put'],
    ['/platform/email-config/test', 'post'],
    ['/platform/configs', 'get'],
    ['/platform/configs/{configKey}', 'put'],
    ['/projects/{projectKey}/webhooks', 'get'],
    ['/projects/{projectKey}/webhooks', 'post'],
    ['/projects/{projectKey}/webhooks/{webhookID}', 'get'],
    ['/projects/{projectKey}/webhooks/{webhookID}', 'put'],
    ['/projects/{projectKey}/webhooks/{webhookID}', 'delete'],
    ['/projects/{projectKey}/webhooks/{webhookID}/test', 'post'],
    ['/projects/{projectKey}/webhooks/{webhookID}/logs', 'get'],
    ['/projects/{projectKey}/webhooks/{webhookID}/stats', 'get'],
    ['/projects/{projectKey}/admin/agents/agent-control/overview', 'get'],
    ['/projects/{projectKey}/admin/agents/service-principals', 'post'],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/status',
        'put',
    ],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/rotate',
        'post',
    ],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/{credentialId}',
        'delete',
    ],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies',
        'get',
    ],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies',
        'post',
    ],
    [
        '/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies/{policyId}',
        'delete',
    ],
    [
        '/projects/{projectKey}/admin/agents/leases/{leaseId}/force-release',
        'post',
    ],
    [
        '/projects/{projectKey}/admin/agents/attachments/{attachmentId}/scan',
        'post',
    ],
    [
        '/projects/{projectKey}/admin/agents/outbox/{deliveryId}/replay',
        'post',
    ],
    ['/projects/{projectKey}/tickets/{ticketID}/assign', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}/transfer', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}/escalate', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}/status', 'post'],
    ['/projects/{projectKey}/tickets/{ticketID}/transitions', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/history', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/comments', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/comments', 'post'],
    [
        '/projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies',
        'get',
    ],
    ['/projects/{projectKey}/tickets/{ticketID}/attachments', 'get'],
    ['/projects/{projectKey}/tickets/{ticketID}/attachments', 'post'],
    [
        '/projects/{projectKey}/tickets/{ticketID}/attachments/{attachmentID}/content',
        'get',
    ],
]
for (const [operationPath, method] of p1Operations) {
    assert.ok(
        contract.paths[operationPath]?.[method],
        `${method.toUpperCase()} ${operationPath} is missing from the P1 contract`,
    )
}
const notificationListParameters =
    contract.paths['/projects/{projectKey}/notifications'].get.parameters
const notificationPageSize = notificationListParameters.find(
    (parameter) => parameter.name === 'page_size',
)
assert.ok(
    notificationListParameters.some(
        (parameter) =>
            parameter.$ref === '#/components/parameters/ContentPage',
    ),
)
assert.equal(notificationPageSize.schema.default, 25)
assert.equal(notificationPageSize.schema.maximum, 100)
assert.equal(contract.components.parameters.ContentPage.schema.minimum, 1)
assert.equal(contract.components.parameters.ContentPage.schema.maximum, 1_000_000)
assert.equal(contract.components.parameters.ContentPage.schema.default, 1)
for (const path of [
    '/projects/{projectKey}/tickets/overdue',
    '/projects/{projectKey}/tickets/sla-breach',
]) {
    const operation = contract.paths[path].get
    assert.equal(
        operation.responses['200'].content['application/json'].schema.$ref,
        '#/components/schemas/TicketListPageEnvelope',
    )
}

const resolveComponent = (value, componentType) => {
    if (!value?.$ref) return value
    const prefix = `#/components/${componentType}/`
    assert.ok(value.$ref.startsWith(prefix), value.$ref)
    return contract.components[componentType][value.$ref.slice(prefix.length)]
}
const operationMethods = new Set(['get', 'post', 'put', 'patch', 'delete'])
const seenOperationIDs = new Set()
let operationCount = 0
let requestBodyCount = 0
for (const [operationPath, pathItem] of Object.entries(contract.paths)) {
    for (const [method, operation] of Object.entries(pathItem)) {
        if (!operationMethods.has(method)) continue
        operationCount += 1
        assert.match(operation.operationId, /^[A-Za-z_$][A-Za-z0-9_$]*$/)
        assert.equal(
            seenOperationIDs.has(operation.operationId),
            false,
            `duplicate operationId ${operation.operationId}`,
        )
        seenOperationIDs.add(operation.operationId)

        const successStatus = Object.keys(operation.responses)
            .filter((status) => /^2\d\d$/.test(status))
            .sort()[0]
        assert.ok(
            successStatus,
            `${method.toUpperCase()} ${operationPath} has no 2xx response`,
        )
        const response = resolveComponent(
            operation.responses[successStatus],
            'responses',
        )
        assert.ok(
            Object.values(response.content ?? {}).some((media) => media?.schema),
            `${method.toUpperCase()} ${operationPath} has no typed success`,
        )
        if (operation.requestBody) {
            requestBodyCount += 1
            const media = Object.values(operation.requestBody.content ?? {})
                .find((candidate) => candidate?.schema)
            assert.ok(media?.schema)
            const requestSchema = resolveComponent(media.schema, 'schemas')
            assert.equal(
                requestSchema.type,
                'object',
                `${method.toUpperCase()} ${operationPath} request must be an object`,
            )
            assert.equal(
                requestSchema.additionalProperties,
                false,
                `${method.toUpperCase()} ${operationPath} request must reject unpublished fields`,
            )
        }

        const pathParameters = [
            ...(pathItem.parameters ?? []),
            ...(operation.parameters ?? []),
        ]
            .map((parameter) => resolveComponent(parameter, 'parameters'))
            .filter((parameter) => parameter.in === 'path')
            .map((parameter) => parameter.name)
            .sort()
        const placeholders = [
            ...operationPath.matchAll(/\{([^}]+)\}/g),
        ]
            .map((match) => match[1])
            .sort()
        assert.deepEqual(
            pathParameters,
            placeholders,
            `${method.toUpperCase()} ${operationPath} path parameters drifted`,
        )

        const operationType = `${operation.operationId[0].toUpperCase()}${operation.operationId.slice(1)}Operation`
        assert.match(
            generated,
            new RegExp(`export type ${operationType}PathParameters =`),
        )
        assert.match(
            generated,
            new RegExp(`export type ${operationType}Response =`),
        )
        assert.match(
            generated,
            new RegExp(`\\b${operation.operationId}: \\(`),
        )
    }
}
assert.ok(operationCount >= 77, `only ${operationCount} Human operations generated`)
assert.ok(
    requestBodyCount >= 32,
    `only ${requestBodyCount} closed Human request bodies generated`,
)
assert.match(generated, /export const humanApiOperations = \{/)
for (const [operationId, strategy] of [
    ['listAgentServicePrincipals', 'page'],
    ['listServicePrincipalPoliciesV2', 'page'],
    ['listAgentTicketLeases', 'page'],
    ['listAgentAttachmentScans', 'page'],
    ['listAgentOutboxDeliveries', 'page'],
    ['listAgentDomainEvents', 'cursor'],
    ['listAgentPolicyDecisions', 'cursor'],
]) {
    assert.match(
        generated,
        new RegExp(
            `${operationId}: \\{[\\s\\S]*?listStrategy: "${strategy}"`,
        ),
        `${operationId} list strategy was not retained`,
    )
}
assert.match(generated, /export interface HumanApiOperationTypes \{/)
assert.match(generated, /export const buildHumanApiRequest =/)
assert.match(generated, /export const humanApiRoutes = \{/)
assert.match(
    generated,
    /regenerateOTPBackupCodes: \(query: RegenerateOTPBackupCodesOperationQuery = \{\}\) =>[\s\S]*?humanApiRoute\("regenerateOTPBackupCodes", \{\}, query\)/,
)
assert.match(
    humanApiTypeTest,
    /buildHumanApiRequest\('regenerateOTPBackupCodes'/,
)

const exportedNames = [
    ...generated.matchAll(
        /^export (?:type|interface|const) ([A-Za-z_$][A-Za-z0-9_$]*)/gm,
    ),
].map((match) => match[1])
assert.equal(
    new Set(exportedNames).size,
    exportedNames.length,
    'generated Human API exports contain a name collision',
)
assert.match(generated, /export type UpdateHumanProfileOperationRequest =/)
assert.doesNotMatch(
    generated,
    /export type UpdateHumanProfileRequest = UpdateHumanProfileRequest/,
)
assert.match(
    generated,
    /PolicyConditionValue> \| \{ \[key: string\]: PolicyConditionValue \}/,
)
assert.doesNotMatch(
    generated,
    /Record<string, PolicyConditionValue>/,
)
assert.match(
    generated,
    /createPlatformAuditExport: \(query: CreatePlatformAuditExportOperationQuery\) =>/,
)
assert.doesNotMatch(
    generated,
    /createPlatformAuditExport: \(query: CreatePlatformAuditExportOperationQuery = \{\}\) =>/,
)

assert.equal(
    contract.paths['/workbench/tickets'].get[
        'x-chronodesk-project-membership-filtered'
    ],
    true,
)
assert.equal(
    Object.hasOwn(
        contract.paths['/workbench/tickets'].get,
        'x-chronodesk-project-roles',
    ),
    false,
)
assert.deepEqual(
    Object.keys(contract.paths['/platform/audit-logs'].get.responses),
    ['200', '400', '401', '403', '500', '503', 'default'],
)
assert.equal(
    contract.paths['/projects/{projectKey}/webhooks/{webhookID}/test'].post
        .responses['202'].content['application/json'].schema.$ref,
    '#/components/schemas/WebhookTestReceiptEnvelope',
)

assert.match(ticketTypes, /CreateTicketRequest as HumanCreateTicketRequest/)
assert.match(ticketTypes, /export type Ticket = HumanTicket/)
assert.doesNotMatch(ticketTypes, /export interface Ticket\b/)
assert.doesNotMatch(ticketTypes, /export interface CreateTicketRequest\b/)
assert.doesNotMatch(ticketTypes, /export interface UpdateTicketRequest\b/)
assert.match(workbenchTypes, /CrossProjectWorkbenchTicket/)
assert.doesNotMatch(workbenchTypes, /export interface CrossProjectWorkbench/)
const notificationTicketSummary =
    contract.components.schemas.NotificationTicketSummary
assert.equal(notificationTicketSummary.type, 'object')
assert.equal(notificationTicketSummary.additionalProperties, false)
assert.deepEqual(notificationTicketSummary.required, [
    'id',
    'ticket_number',
    'title',
])
assert.deepEqual(
    Object.keys(notificationTicketSummary.properties).sort(),
    ['id', 'ticket_number', 'title'],
)
assert.ok(
    contract.components.schemas.Notification.required.includes(
        'related_ticket',
    ),
)
assert.deepEqual(
    contract.components.schemas.Notification.properties.related_ticket.anyOf,
    [
        {
            $ref: '#/components/schemas/NotificationTicketSummary',
        },
        { type: 'null' },
    ],
)
for (const observerOptional of [
    'customer_email',
    'customer_phone',
    'customer_name',
    'created_by_id',
    'created_by',
    'created_by_actor',
    'assigned_to_id',
    'assigned_to',
    'assigned_to_actor',
    'agent_context',
]) {
    assert.equal(
        contract.components.schemas.Ticket.required.includes(observerOptional),
        false,
        `Ticket.required must not include observer-omitted ${observerOptional}`,
    )
}
assert.match(generated, /export type NotificationTicketSummary = \{/)
assert.match(
    generated,
    /related_ticket: NotificationTicketSummary \| null/,
)
assert.doesNotMatch(generated, /related_ticket\?: Ticket/)
assert.match(adminApp, /admin\/audit\/PlatformAuditExplorer/)
assert.match(auditExplorer, /humanApiRoutes\.listPlatformAuditLogs/)
assert.match(auditExplorer, /humanApiRoutes\.getPlatformAuditLogDetail/)
assert.doesNotMatch(adminApp, /type PlatformAuditItem\b/)
assert.match(workbenchPage, /humanApiRoutes\.listCrossProjectWorkbenchTickets/)
assert.doesNotMatch(workbenchPage, /["'`]\/workbench\/tickets/)
assert.match(emailSettings, /humanApiRoutes\.getPlatformEmailConfig/)
assert.match(emailSettings, /humanApiRoutes\.updatePlatformEmailConfig/)
assert.doesNotMatch(emailSettings, /interface EmailConfigResponse\b/)
assert.match(systemSettings, /humanApiRoutes\.listPlatformConfigs/)
assert.match(systemSettings, /humanApiRoutes\.updatePlatformConfig/)
assert.match(systemSettings, /type SystemConfigPage,/)
assert.doesNotMatch(systemSettings, /interface SystemConfigPage\b/)
assert.doesNotMatch(systemSettings, /new URLSearchParams/)
assert.match(automationLogs, /type AutomationLogPage,/)
assert.doesNotMatch(automationLogs, /type AutomationLogItem\b/)
assert.doesNotMatch(automationLogs, /new URLSearchParams/)
assert.match(webhookSettings, /type WebhookConfig,/)
assert.match(webhookSettings, /type WebhookEmergencyRevokeResult,/)
assert.match(webhookSettings, /type WebhookLogPage,/)
assert.match(webhookSettings, /humanApiRoutes\.queueProjectWebhookTest/)
assert.match(
    webhookSettings,
    /humanApiRoutes\.emergencyRevokeProjectWebhook/,
)
assert.doesNotMatch(webhookSettings, /interface WebhookConfig\b/)
assert.doesNotMatch(webhookSettings, /type WebhookDefinition\b/)
assert.doesNotMatch(webhookSettings, /type WebhookDelivery\b/)
assert.doesNotMatch(webhookSettings, /new URLSearchParams/)
assert.doesNotMatch(webhookSettings, /projectResourcePath/)
assert.match(agentControl, /humanApiRoutes\.getAgentControlOverviewV2/)
for (const routeHelper of [
    'listAgentServicePrincipals',
    'listServicePrincipalPoliciesV2',
    'listAgentTicketLeases',
    'listAgentAttachmentScans',
    'listAgentOutboxDeliveries',
    'listAgentDomainEvents',
    'listAgentPolicyDecisions',
]) {
    assert.match(agentControl, new RegExp(`humanApiRoutes\\.${routeHelper}`))
}
assert.match(agentControl, /humanApiRoutes\.createServicePrincipalV2/)
assert.match(agentControl, /projectScopeChangedEvent/)
assert.match(agentControl, /AbortController/)
assert.match(agentControl, /loadingByKey/)
assert.match(agentControl, /errorByKey/)
assert.doesNotMatch(agentControl, /AgentControlSnapshot/)
assert.doesNotMatch(agentControl, /snapshot\?/)
assert.doesNotMatch(agentControl, /resolveAgentAdminPath/)
assert.match(
    notificationCreate,
    /import type \{[\s\S]*\bCreateNotificationRequest\b[\s\S]*\} from '@\/lib\/generated\/human-api'/,
)
assert.doesNotMatch(notificationCreate, /type CreateNotificationRequest = \{/)
for (const routeHelper of [
    'listPlatformUsers',
    'createPlatformUser',
    'getPlatformUser',
    'updatePlatformUser',
    'deletePlatformUser',
]) {
    assert.match(dataProvider, new RegExp(`humanApiRoutes\\.${routeHelper}`))
}
assert.doesNotMatch(dataProvider, /platform\/users/)
for (const routeHelper of [
    'listProjectTickets',
    'getProjectTicket',
    'createProjectTicket',
    'updateProjectTicket',
    'deleteProjectTicket',
    'listProjectNotifications',
    'listProjectAutomationRules',
    'listProjectAutomationLogs',
    'listProjectTicketComments',
    'listProjectTicketHistory',
]) {
    assert.match(dataProvider, new RegExp(`humanApiRoutes\\.${routeHelper}`))
}
for (const routeHelper of [
    'listProjectTicketComments',
    'listProjectTicketCommentReplies',
    'createProjectTicketComment',
    'listProjectTicketAttachments',
    'uploadProjectTicketAttachment',
    'downloadProjectTicketAttachment',
]) {
    assert.match(
        ticketConversation,
        new RegExp(`humanApiRoutes\\.${routeHelper}`),
    )
}
assert.doesNotMatch(ticketConversation, /type TicketComment\s*=/)
assert.doesNotMatch(ticketConversation, /type TicketAttachment\s*=/)
assert.doesNotMatch(ticketConversation, /projectResourcePath/)
assert.match(ticketConversation, /<Pagination/)
assert.match(ticketConversation, /aria-label="评论分页"/)
assert.match(ticketConversation, /aria-label="附件分页"/)
assert.match(ticketConversation, /LatestRequestGate/)
assert.match(ticketConversation, /signal: request\.signal/)
assert.match(ticketConversation, /lastPageAfterAppend\(commentsTotal\)/)
assert.match(ticketConversation, />\s*重试\s*</)
assert.match(
    ticketConversation,
    /comments\.filter\(\(item\) => item\.type !== 'internal'\)/,
)
assert.match(
    ticketConversation,
    /attachments\.filter\(\(attachment\) => attachment\.is_public\)/,
)
assert.match(ticketConversation, /visibilityAdjustedPageMeta/)
assert.match(ticketConversation, /visibleReplyPages/)
assert.match(dataProvider, /queryString\.stringify\(query\)/)
for (const routeHelper of [
    'assignProjectTicket',
    'transferProjectTicket',
    'escalateProjectTicket',
    'getProjectTicketAllowedTransitions',
    'updateProjectTicketStatus',
]) {
    assert.match(
        ticketWorkflowActions,
        new RegExp(`humanApiRoutes\\.${routeHelper}`),
    )
}
assert.doesNotMatch(ticketWorkflowActions, /projectResourcePath/)
assert.match(ticketWorkflowActions, /<Autocomplete/)
assert.match(ticketWorkflowActions, /perPage: 25/)
assert.match(ticketWorkflowActions, /q: debouncedAssigneeSearch/)
assert.doesNotMatch(ticketWorkflowActions, /perPage: 100/)
for (const source of [ticketCreate, ticketEdit]) {
    assert.match(
        source,
        /<ReferenceInput[\s\S]*?source="assigned_to_id"[\s\S]*?<EnterpriseReferenceAutocompleteInput/,
    )
}
for (const routeHelper of [
    'createHumanSession',
    'refreshHumanSession',
    'deleteHumanSession',
    'deleteAllHumanSessions',
    'getHumanSessionUser',
    'requestHumanPasswordReset',
    'resetHumanPassword',
]) {
    assert.match(authProvider, new RegExp(`humanApiRoutes\\.${routeHelper}`))
}
assert.doesNotMatch(authProvider, /buildUrl\(\s*['"`]\/auth\//)

for (const routeHelper of [
    'registerHuman',
    'requestHumanPasswordReset',
    'resetHumanPassword',
    'verifyHumanEmail',
    'resendHumanEmailVerification',
]) {
    assert.match(
        publicAuthApi,
        new RegExp(`humanApiRoutes\\.${routeHelper}`),
    )
}
assert.doesNotMatch(publicAuthApi, /['"`]\/auth\//)
for (const publicRoute of [
    '/register',
    '/forgot-password',
    '/reset-password',
    '/verify-email',
    '/resend-verification',
]) {
    assert.match(adminApp, new RegExp(`path=["']${publicRoute}["']`))
}
assert.match(adminApp, /<CustomRoutes noLayout>/)
assert.match(loginPage, /to=["']\/register["']/)
assert.match(loginPage, /to=["']\/forgot-password["']/)
assert.match(loginPage, /to=["']\/resend-verification["']/)
assert.match(customAppBar, /replace\(publicLoginHashTarget\)/)
assert.doesNotMatch(customAppBar, /location\.(?:assign|replace)\(['"]\/login/)

for (const requestSchema of [
    'CreateTicketRequest',
    'UpdateTicketRequest',
    'CreateNotificationRequest',
    'AutomationRuleRequest',
    'UpdateEmailConfigRequest',
    'TestEmailRequest',
    'UpdateSystemConfigRequest',
    'ForgotPasswordRequest',
    'ResetHumanPasswordRequest',
    'RegisterHumanRequest',
    'VerifyHumanEmailRequest',
    'ResendHumanEmailVerificationRequest',
    'AssignTicketRequest',
    'TransferTicketRequest',
    'EscalateTicketRequest',
    'UpdateTicketStatusRequest',
    'CreateTicketCommentRequest',
    'UploadTicketAttachmentRequest',
]) {
    assert.equal(
        contract.components.schemas[requestSchema].additionalProperties,
        false,
        `${requestSchema} must reject unpublished fields`,
    )
}
assert.equal(
    contract.components.schemas.CreateAdminUserRequest.properties.phone.pattern,
    '^\\+[1-9][0-9]{1,14}$',
)
assert.equal(
    contract.components.schemas.CreateAdminUserRequest.properties.manager_id
        .minimum,
    1,
)
assert.match(systemSettings, /type UpdateSystemConfigRequest/)
assert.doesNotMatch(systemSettings, /\bis_required:\s*config\.is_required/)
assert.doesNotMatch(systemSettings, /\bis_active:\s*config\.is_active/)

assert.match(
    humanApiTypeTest,
    /HumanApiRequestOptions<'deleteHumanSession'>/,
)
assert.match(
    humanApiTypeTest,
    /@ts-expect-error Login has requestBody\.required=true/,
)
assert.equal(
    contract.paths['/auth/logout'].post.requestBody.required,
    false,
)
assert.match(
    generated,
    /deleteHumanSession: \{[\s\S]*?requestBody: "optional"/,
)
assert.match(
    generated,
    /createHumanSession: \{[\s\S]*?requestBody: "required"/,
)
assert.match(
    generated,
    /getHumanSessionUser: \{[\s\S]*?requestBody: "none"/,
)

const generatedArchiveRoute = humanApiRoutes.archivePlatformProject({
    projectPublicID: '019fb344-fa16-7e13-9c5b-08eb95478098',
})
assert.equal(
    generatedArchiveRoute,
    '/platform/projects/019fb344-fa16-7e13-9c5b-08eb95478098/archive',
)
assert.equal(
    humanApiRoutes.listPlatformProjects(),
    '/platform/projects',
)

const generatedTicketRoute = humanApiRoutes.listProjectTickets(
    { projectKey: 'OPS' },
    { page: 1, page_size: 25, search: '需要 编码' },
)
assert.ok(generatedTicketRoute.startsWith('/'))
assert.equal(
    generatedTicketRoute,
    '/projects/OPS/tickets?page=1&page_size=25&search=%E9%9C%80%E8%A6%81+%E7%BC%96%E7%A0%81',
)
const joinedTicketURL = joinApiUrl('/api/', generatedTicketRoute)
assert.equal(
    joinedTicketURL,
    '/api/projects/OPS/tickets?page=1&page_size=25&search=%E9%9C%80%E8%A6%81+%E7%BC%96%E7%A0%81',
)
assert.equal(
    new URL(joinedTicketURL, 'http://chronodesk.invalid').pathname.includes('//'),
    false,
)
assert.equal(
    joinApiUrl('https://chronodesk.invalid/api/', '/projects/OPS/tickets'),
    'https://chronodesk.invalid/api/projects/OPS/tickets',
)

assert.equal(
    contract.components.schemas.AutomationLog.additionalProperties,
    false,
)
assert.equal(
    contract.components.schemas.AutomationLog.properties.rule.$ref,
    '#/components/schemas/AutomationRuleLogSummary',
)
assert.equal(
    contract.components.schemas.AutomationLog.properties.ticket.$ref,
    '#/components/schemas/AutomationTicketLogSummary',
)
assert.match(
    generated,
    /export type AutomationLog = \{[\s\S]*rule\?: AutomationRuleLogSummary[\s\S]*ticket\?: AutomationTicketLogSummary/,
)
assert.equal(
    contract.components.schemas.CreateWebhookRequest.additionalProperties,
    false,
)
assert.equal(
    Object.hasOwn(
        contract.components.schemas.CreateWebhookRequest.properties,
        'status',
    ),
    false,
)
assert.equal(
    contract.components.schemas.UpdateWebhookRequest.additionalProperties,
    false,
)
assert.equal(
    Object.hasOwn(
        contract.components.schemas.UpdateWebhookRequest.properties,
        'status',
    ),
    true,
)
assert.equal(
    contract.components.schemas.WebhookConfig.additionalProperties,
    false,
)
assert.deepEqual(
    Object.keys(contract.components.schemas.WebhookConfig.properties).sort(),
    [
        'id',
        'created_at',
        'updated_at',
        'organization_id',
        'project_id',
        'name',
        'description',
        'provider',
        'webhook_url_masked',
        'has_webhook_url',
        'status',
        'previous_secret_expires_at',
        'enabled_events',
        'enabled_events_list',
        'message_template',
        'message_format',
        'filter_rules',
        'filter_rules_obj',
        'retry_count',
        'retry_interval',
        'timeout_seconds',
        'is_async',
        'rate_limit',
        'rate_limit_window',
        'resource_version',
        'last_triggered_at',
        'last_success_at',
        'last_error_at',
        'last_error',
        'total_sent',
        'total_success',
        'total_failed',
        'created_by',
        'updated_by',
    ].sort(),
)
for (const forbidden of [
    'webhook_url',
    'secret',
    'previous_secret',
    'access_token',
]) {
    assert.equal(
        Object.hasOwn(
            contract.components.schemas.WebhookConfig.properties,
            forbidden,
        ),
        false,
    )
}
const webhookEmergencyRevokePath =
    '/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke'
const webhookEmergencyRevoke =
    contract.paths[webhookEmergencyRevokePath].post
assert.equal(
    webhookEmergencyRevoke.operationId,
    'emergencyRevokeProjectWebhook',
)
assert.deepEqual(
    webhookEmergencyRevoke['x-chronodesk-project-roles'],
    ['project_admin'],
)
assert.equal(
    humanApiRoutes.emergencyRevokeProjectWebhook({
        projectKey: 'OPS',
        webhookID: 731,
    }),
    '/projects/OPS/admin/agents/webhooks/731/emergency-revoke',
)
const webhookEmergencyResult =
    contract.components.schemas.WebhookEmergencyRevokeResult
assert.equal(webhookEmergencyResult.additionalProperties, false)
assert.deepEqual(
    Object.keys(webhookEmergencyResult.properties).sort(),
    [
        'config_id',
        'status',
        'expired_deliveries',
        'in_flight_deliveries',
        'shredded_snapshots',
        'credential_shred_reason',
    ].sort(),
)
assert.equal(webhookEmergencyResult.properties.status.const, 'disabled')
assert.equal(
    webhookEmergencyResult.properties.credential_shred_reason.const,
    'revoked',
)
const webhookLogResponse =
    contract.paths['/projects/{projectKey}/webhooks/{webhookID}/logs'].get
        .responses['200'].content['application/json'].schema
assert.equal(
    webhookLogResponse.$ref,
    '#/components/schemas/WebhookLogPageEnvelope',
)
const webhookLogData = contract.components.schemas.WebhookLogPage
const webhookLogItem = contract.components.schemas.WebhookLog
assert.equal(webhookLogData.additionalProperties, false)
assert.equal(webhookLogItem.additionalProperties, false)
assert.equal(
    webhookLogData.properties.items.items.$ref,
    '#/components/schemas/WebhookLog',
)
assert.deepEqual(Object.keys(webhookLogItem.properties).sort(), [
    'config_id',
    'created_at',
    'error_message',
    'event_type',
    'id',
    'response_status',
    'response_time',
    'status',
])
const webhookStatsResponse =
    contract.paths['/projects/{projectKey}/webhooks/{webhookID}/stats'].get
        .responses['200'].content['application/json'].schema
const webhookStatsData = webhookStatsResponse.properties.data
const webhookStatsSummary = webhookStatsData.properties.summary
const webhookDailyStats = webhookStatsData.properties.daily_stats.items
assert.equal(webhookStatsResponse.additionalProperties, false)
assert.equal(webhookStatsData.additionalProperties, false)
assert.deepEqual(Object.keys(webhookStatsData.properties).sort(), [
    'daily_stats',
    'period',
    'summary',
])
assert.equal(webhookStatsSummary.additionalProperties, false)
assert.deepEqual(Object.keys(webhookStatsSummary.properties).sort(), [
    'total_failed',
    'total_sent',
    'total_success',
])
assert.equal(webhookDailyStats.additionalProperties, false)
assert.deepEqual(Object.keys(webhookDailyStats.properties).sort(), [
    'date',
    'failed',
    'sent',
    'success',
])
assert.match(
    generated,
    /summary: \{[\s\S]*total_sent: number[\s\S]*total_success: number[\s\S]*total_failed: number[\s\S]*daily_stats: Array<\{[\s\S]*sent: number/,
)
assert.match(webhookSettings, /buildPayload\(currentId !== null\)/)
assert.doesNotMatch(
    webhookSettings,
    /rate_limit_window:[^\n]+,\s*\n\s*status: form\.status/,
)
