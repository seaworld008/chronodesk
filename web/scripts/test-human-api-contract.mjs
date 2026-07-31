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
    webhookSettings,
    agentControl,
    notificationCreate,
    dataProvider,
    humanApiTypeTest,
    ticketConversation,
    ticketWorkflowActions,
    authProvider,
] = await Promise.all([
    readWebSource('types', 'index.ts'),
    readWebSource('lib', 'types', 'crossProjectWorkbench.ts'),
    readWebSource('AdminApp.tsx'),
    readWebSource('admin', 'audit', 'PlatformAuditExplorer.tsx'),
    readWebSource('admin', 'workbench', 'CrossProjectWorkbench.tsx'),
    readWebSource('admin', 'settings', 'EmailSettings.tsx'),
    readWebSource('admin', 'settings', 'SystemSettings.tsx'),
    readWebSource('admin', 'settings', 'WebhookSettings.tsx'),
    readWebSource('admin', 'agents', 'AgentControlCenter.tsx'),
    readWebSource('admin', 'notifications', 'NotificationCreate.tsx'),
    readWebSource('lib', 'dataProvider.ts'),
    readWebSource('lib', 'humanApiContract.type-test.ts'),
    readWebSource('admin', 'tickets', 'TicketConversationPanel.tsx'),
    readWebSource('admin', 'tickets', 'TicketWorkflowActions.tsx'),
    readWebSource('lib', 'authProvider.ts'),
])

assert.equal(contract.openapi, '3.2.0')
assert.equal(contract['x-chronodesk-types-generator'], '2.0.0')
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

for (const name of [
    'PlatformRole',
    'ProjectRole',
    'LoginRequest',
    'RefreshTokenRequest',
    'LogoutRequest',
    'HumanSessionUser',
    'AuthSession',
    'AuthSessionEnvelope',
    'AuthSessionSuccessEnvelope',
    'AuthorizedProject',
    'AuthorizedProjectAccess',
    'ProjectMembership',
    'ProjectMembershipEnvelope',
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
    ['/auth/me', 'get'],
    ['/auth/profile', 'put'],
    ['/projects/{projectKey}/context', 'get'],
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
]
for (const [operationPath, method] of requiredOperations) {
    assert.ok(
        contract.paths[operationPath]?.[method],
        `${method.toUpperCase()} ${operationPath} is missing`,
    )
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
    ['/auth/login', ['200', '400', '401', '403', '429', '503']],
    ['/auth/refresh', ['200', '400', '401', '408', '429', '503']],
    ['/auth/logout', ['200', '400', '429', '503']],
    ['/auth/logout-all', ['200', '401', '429', '500', '503']],
]) {
    assert.deepEqual(
        Object.keys(contract.paths[operationPath].post.responses),
        expectedStatuses,
    )
}
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
        'page',
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
assert.equal(requestBodyCount, 32)
assert.match(generated, /export const humanApiOperations = \{/)
assert.match(generated, /export interface HumanApiOperationTypes \{/)
assert.match(generated, /export const buildHumanApiRequest =/)
assert.match(generated, /export const humanApiRoutes = \{/)

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
assert.match(webhookSettings, /type WebhookConfig,/)
assert.match(webhookSettings, /humanApiRoutes\.queueProjectWebhookTest/)
assert.doesNotMatch(webhookSettings, /interface WebhookConfig\b/)
assert.doesNotMatch(webhookSettings, /projectResourcePath/)
assert.match(agentControl, /type AgentControlSnapshot = AdminOverview/)
assert.match(agentControl, /humanApiRoutes\.getAgentControlOverviewV2/)
assert.match(agentControl, /humanApiRoutes\.createServicePrincipalV2/)
assert.doesNotMatch(agentControl, /interface AgentControlSnapshot\b/)
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
assert.doesNotMatch(ticketConversation, /comments\.filter\(/)
assert.doesNotMatch(ticketConversation, /attachments\.filter\(/)
assert.match(dataProvider, /queryString\.stringify\(query\)/)
for (const routeHelper of [
    'assignProjectTicket',
    'transferProjectTicket',
    'escalateProjectTicket',
    'updateProjectTicketStatus',
]) {
    assert.match(
        ticketWorkflowActions,
        new RegExp(`humanApiRoutes\\.${routeHelper}`),
    )
}
assert.doesNotMatch(ticketWorkflowActions, /projectResourcePath/)
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
        'webhook_url',
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
const webhookLogResponse =
    contract.paths['/projects/{projectKey}/webhooks/{webhookID}/logs'].get
        .responses['200'].content['application/json'].schema
const webhookLogData = webhookLogResponse.properties.data
const webhookLogItem = webhookLogData.properties.items.items
assert.equal(webhookLogResponse.additionalProperties, false)
assert.equal(webhookLogData.additionalProperties, false)
assert.equal(webhookLogItem.additionalProperties, false)
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
