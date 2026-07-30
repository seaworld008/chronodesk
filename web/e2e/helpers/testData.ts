import type { APIRequestContext, Page } from '@playwright/test';
import {
    apiRequest,
    loginSession,
    type AuthSession,
    type Credentials,
} from './api';
import {
    assertDestructiveE2EAllowed,
    assertGlobalE2EAllowed,
    assertSecretMutationIsRestorable,
    installBrowserMutationGuard,
    isLoopbackE2E,
} from './safety';

export const E2E_PREFIX = 'E2E-';
export const DEFAULT_PASSWORD = 'Admin123!';
const e2eExecutionStartedAt = Date.now();
const e2eRunLabel = (
    process.env.CHRONODESK_E2E_RUN_ID?.trim() || 'local'
)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 24) || 'run';
const e2eWorkerIndex = (
    process.env.TEST_WORKER_INDEX?.trim() || '0'
).replace(/[^0-9]/g, '').slice(-3) || '0';
const e2eExecutionToken = [
    e2eExecutionStartedAt.toString(36),
    `p${process.pid.toString(36)}`,
    `w${e2eWorkerIndex}`,
].join('-');
const E2E_RUN_ID = `${e2eRunLabel}-${e2eExecutionToken}`;
export const E2E_MARKER = `${E2E_PREFIX}${E2E_RUN_ID}-`;
export const E2E_ACCOUNT_STEM = [
    'e2e',
    e2eRunLabel.slice(0, 8),
    e2eExecutionStartedAt.toString(36),
    `p${process.pid.toString(36)}`,
    `w${e2eWorkerIndex}`,
].join('_');

export const DEFAULT_ADMIN: Credentials = {
    email: 'admin@example.com',
    password: DEFAULT_PASSWORD,
};

type TrackedResource =
    | 'automationRules'
    | 'tickets'
    | 'notifications'
    | 'users'
    | 'webhooks'
    | 'agentPrincipals'
    | 'trustedDevices';

const trackedResources: Record<TrackedResource, Set<string>> = {
    automationRules: new Set(),
    tickets: new Set(),
    notifications: new Set(),
    users: new Set(),
    webhooks: new Set(),
    agentPrincipals: new Set(),
    trustedDevices: new Set(),
};

export const trackE2EResource = (
    resource: TrackedResource,
    id: string | number,
) => {
    trackedResources[resource].add(String(id));
};

export const untrackE2EResource = (
    resource: TrackedResource,
    id: string | number,
) => {
    trackedResources[resource].delete(String(id));
};

const trackedIDs = (resource: TrackedResource) => [
    ...trackedResources[resource],
];

const authSessions = new Map<string, Promise<AuthSession>>();
const projectKeyRequests = new Map<string, Promise<string>>();
const ticketCreateConfigurationRequests = new Map<
    string,
    Promise<TicketCreateConfiguration>
>();

type TicketCreateConfiguration = {
    requestTypeVersionID: string;
    workflowVersionID: string;
    workClass: string;
};

type TicketIntakeConfiguration = {
    request_types?: Array<{
        id?: unknown;
        status?: unknown;
        work_class?: unknown;
    }>;
    workflows?: Array<{
        id?: unknown;
        status?: unknown;
    }>;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null;

export const extractData = <T>(payload: unknown): T => {
    if (
        payload &&
        typeof payload === 'object' &&
        'data' in payload
    ) {
        return (payload as Record<string, unknown>).data as T;
    }
    return payload as T;
};

export const extractItems = <T>(payload: unknown): T[] => {
    const data = isRecord(payload) && 'data' in payload
        ? payload.data
        : payload;
    if (Array.isArray(data)) {
        return data as T[];
    }
    if (isRecord(data)) {
        for (const key of ['items', 'rules', 'logs'] as const) {
            if (Array.isArray(data[key])) {
                return data[key] as T[];
            }
        }
    }
    return [];
};

const getAuthSession = (
    request: APIRequestContext,
    credentials: Credentials,
): Promise<AuthSession> => {
    const key = credentials.email.toLowerCase();
    const existing = authSessions.get(key);
    if (existing) {
        return existing;
    }

    const pending = loginSession(request, credentials).catch((error) => {
        authSessions.delete(key);
        throw error;
    });
    authSessions.set(key, pending);
    return pending;
};

export const getAdminToken = async (request: APIRequestContext) =>
    (await getAuthSession(request, DEFAULT_ADMIN)).access_token;

export const resolveE2EProjectKey = (
    request: APIRequestContext,
    token: string,
): Promise<string> => {
    const existing = projectKeyRequests.get(token);
    if (existing) {
        return existing;
    }
    const pending = (async () => {
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            '/api/projects',
        );
        const accesses =
            extractData<Array<Record<string, unknown>>>(response) ?? [];
        const selected = accesses
            .map((access) => access.project)
            .find(
                (project): project is Record<string, unknown> =>
                    typeof project === 'object' &&
                    project !== null &&
                    project.status === 'active' &&
                    project.key === 'DEFAULT',
            );
        if (!selected || typeof selected.key !== 'string') {
            throw new Error('当前账号没有可用于 E2E 的活动 DEFAULT 项目');
        }
        return selected.key;
    })().catch((error) => {
        projectKeyRequests.delete(token);
        throw error;
    });
    projectKeyRequests.set(token, pending);
    return pending;
};

export const projectAPIPath = (
    projectKey: string,
    suffix: string,
) => `/api/projects/${encodeURIComponent(projectKey)}/${suffix.replace(/^\/+/, '')}`;

export const resolveE2ETicketCreateConfiguration = (
    request: APIRequestContext,
    token: string,
    projectKey: string,
): Promise<TicketCreateConfiguration> => {
    const cacheKey = `${token}:${projectKey}`;
    const existing = ticketCreateConfigurationRequests.get(cacheKey);
    if (existing) {
        return existing;
    }

    const pending = (async () => {
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            projectAPIPath(projectKey, 'configuration/intake'),
        );
        const intake = extractData<TicketIntakeConfiguration>(response);
        const requestType = intake.request_types?.find(
            (candidate) =>
                candidate.status === 'published' &&
                candidate.work_class === 'request' &&
                typeof candidate.id === 'string' &&
                candidate.id.length > 0,
        );
        const workflow = intake.workflows?.find(
            (candidate) =>
                candidate.status === 'published' &&
                typeof candidate.id === 'string' &&
                candidate.id.length > 0,
        );
        if (
            !requestType ||
            typeof requestType.id !== 'string' ||
            typeof requestType.work_class !== 'string' ||
            !workflow ||
            typeof workflow.id !== 'string'
        ) {
            throw new Error(
                `项目 ${projectKey} 缺少可用于 E2E 建单的已发布请求类型或工作流`,
            );
        }
        return {
            requestTypeVersionID: requestType.id,
            workflowVersionID: workflow.id,
            workClass: requestType.work_class,
        };
    })().catch((error) => {
        ticketCreateConfigurationRequests.delete(cacheKey);
        throw error;
    });
    ticketCreateConfigurationRequests.set(cacheKey, pending);
    return pending;
};

export const authenticatePage = async (
    page: Page,
    credentials: Credentials = DEFAULT_ADMIN,
) => {
    const session = await getAuthSession(page.request, credentials);
    const projectKey = await resolveE2EProjectKey(
        page.request,
        session.access_token,
    );
    await installBrowserMutationGuard(page);
    await page.addInitScript(({ auth, activeProjectKey }) => {
        localStorage.setItem('token', auth.access_token);
        localStorage.setItem(
            'chronodesk.activeProjectKey',
            activeProjectKey,
        );
        if (auth.refresh_token) {
            localStorage.setItem('refreshToken', auth.refresh_token);
        }
        if (auth.user) {
            localStorage.setItem('user', JSON.stringify(auth.user));
        }
        if (auth.permissions) {
            localStorage.setItem('permissions', JSON.stringify(auth.permissions));
        }
        if (auth.expires_in) {
            localStorage.setItem(
                'tokenExpiresAt',
                String(Date.now() + auth.expires_in * 1000),
            );
        }
    }, { auth: session, activeProjectKey: projectKey });
    await page.goto('/#/');
    await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
};

export const loginViaUI = async (page: Page, credentials: Credentials = DEFAULT_ADMIN) => {
    await installBrowserMutationGuard(page);
    await page.goto('/#/login');
    await page.getByLabel('邮箱').fill(credentials.email);
    await page.getByLabel('密码').fill(credentials.password);
    await page.getByRole('button', { name: '登录系统' }).click();
    await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
};

export const findUserByEmail = async (
    request: APIRequestContext,
    token: string,
    email: string,
) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/admin/users?search=${encodeURIComponent(email)}&page=1&page_size=50`,
    );
    const users = extractItems<Record<string, unknown>>(response);
    return users.find((user) => user.email === email);
};

export const e2eRunID = () => E2E_RUN_ID;
let adminCommandSequence = 0;

const adminCommand = async <T>(
    request: APIRequestContext,
    token: string,
    path: string,
    options: {
        method: 'POST' | 'PUT' | 'DELETE';
        version?: number;
        data?: unknown;
    },
) => apiRequest<T>(request, token, path, {
    method: options.method,
    data: options.data,
    headers: {
        'Idempotency-Key': `e2e-${E2E_RUN_ID}-${++adminCommandSequence}`,
        ...(options.version !== undefined
            ? { 'If-Match': `"v${options.version}"` }
            : {}),
    },
});

type TemporaryRoleAccount = Credentials & {
    id: number;
    username: string;
    role: 'agent' | 'customer';
    optionLabel: string;
};

export type TemporaryRoleAccounts = {
    agent: TemporaryRoleAccount;
    customer: TemporaryRoleAccount;
};

let temporaryRoleAccounts: Promise<TemporaryRoleAccounts> | undefined;

const temporaryRoleAccountIdentity = (
    account: keyof TemporaryRoleAccounts,
) => {
    const username = `${E2E_ACCOUNT_STEM}_workflow_${account}`;
    const email = `${username}@example.test`;
    if (username.length > 50 || email.length > 100) {
        throw new Error('临时角色账号标识超过数据库字段上限');
    }
    return { username, email };
};

const deactivateProjectMembership = async (
    request: APIRequestContext,
    token: string,
    projectKey: string,
    userID: number,
) => {
    assertDestructiveE2EAllowed(
        `DELETE project membership ${projectKey}/${userID}`,
    );
    const response = await request.delete(
        projectAPIPath(
            projectKey,
            `memberships/${encodeURIComponent(userID)}`,
        ),
        {
            headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
            },
        },
    );
    if (![200, 204, 404].includes(response.status())) {
        throw new Error(
            `撤销临时用户 ${userID} 的项目成员关系失败：HTTP ${response.status()}`,
        );
    }
};

const compensateTemporaryUsers = async (
    request: APIRequestContext,
    token: string,
    projectKey: string,
    userIDs: number[],
) => {
    const cleanupErrors: string[] = [];
    for (const id of [...userIDs].reverse()) {
        try {
            await deactivateProjectMembership(
                request,
                token,
                projectKey,
                id,
            );
            await apiRequest(
                request,
                token,
                `/api/admin/users/${encodeURIComponent(id)}`,
                { method: 'DELETE' },
            );
            untrackE2EResource('users', id);
        } catch (error) {
            cleanupErrors.push(
                `${id}: ${error instanceof Error ? error.message : String(error)}`,
            );
        }
    }
    if (cleanupErrors.length > 0) {
        throw new Error(
            `临时角色账号补偿清理失败：${cleanupErrors.join('；')}`,
        );
    }
};

export const ensureRoleAccounts = async (
    request: APIRequestContext,
): Promise<TemporaryRoleAccounts> => {
    if (temporaryRoleAccounts) {
        return temporaryRoleAccounts;
    }

    temporaryRoleAccounts = (async () => {
        assertDestructiveE2EAllowed('创建本轮临时角色账号');
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const agentIdentity = temporaryRoleAccountIdentity('agent');
        const customerIdentity = temporaryRoleAccountIdentity('customer');
        const definitions = [
            {
                key: 'agent',
                ...agentIdentity,
                role: 'agent',
                first_name: 'Support',
                last_name: 'Agent',
                department: 'Support',
                job_title: 'Support Agent',
            },
            {
                key: 'customer',
                ...customerIdentity,
                role: 'customer',
                first_name: 'Demo',
                last_name: 'Customer',
                department: 'Customer',
                job_title: 'Customer',
            },
        ] as const;

        const result = {} as TemporaryRoleAccounts;
        const createdUserIDs: number[] = [];
        try {
            for (const definition of definitions) {
                const existing = await findUserByEmail(
                    request,
                    token,
                    definition.email,
                );
                if (existing) {
                    throw new Error(
                        `本轮临时角色账号已存在，拒绝重置其角色或密码：${definition.email}`,
                    );
                }

                const response = await apiRequest<Record<string, unknown>>(
                    request,
                    token,
                    '/api/admin/users',
                    {
                        method: 'POST',
                        data: {
                            username: definition.username,
                            email: definition.email,
                            password: DEFAULT_PASSWORD,
                            role: definition.role,
                            first_name: definition.first_name,
                            last_name: definition.last_name,
                            department: definition.department,
                            job_title: definition.job_title,
                        },
                    },
                );
                const created = extractData<Record<string, unknown>>(response);
                if (typeof created.id !== 'number') {
                    throw new Error(
                        `创建临时角色账号后响应缺少 ID：${definition.email}`,
                    );
                }
                createdUserIDs.push(created.id);
                trackE2EResource('users', created.id);
                await apiRequest(
                    request,
                    token,
                    projectAPIPath(projectKey, 'memberships'),
                    {
                        method: 'POST',
                        data: {
                            user_id: created.id,
                            role:
                                definition.role === 'agent'
                                    ? 'agent'
                                    : 'requester',
                        },
                    },
                );
                result[definition.key] = {
                    id: created.id,
                    username: definition.username,
                    email: definition.email,
                    password: DEFAULT_PASSWORD,
                    role: definition.role,
                    optionLabel: `${definition.username} (${definition.first_name} ${definition.last_name})`,
                };
            }
        } catch (error) {
            try {
                await compensateTemporaryUsers(
                    request,
                    token,
                    projectKey,
                    createdUserIDs,
                );
            } catch (cleanupError) {
                throw new Error(
                    `创建临时角色账号失败且补偿清理未完成；原始错误：${
                        error instanceof Error ? error.message : String(error)
                    }；清理错误：${
                        cleanupError instanceof Error
                            ? cleanupError.message
                            : String(cleanupError)
                    }`,
                );
            }
            throw error;
        }
        return result;
    })().catch((error) => {
        temporaryRoleAccounts = undefined;
        throw error;
    });

    return temporaryRoleAccounts;
};

export const createNotification = async (
    request: APIRequestContext,
    options: { title: string; content: string; recipientEmail?: string },
) => {
    if (!options.title.startsWith(E2E_MARKER)) {
        throw new Error('测试通知标题必须包含本轮唯一 marker');
    }
    const token = await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    const recipientEmail = options.recipientEmail ?? DEFAULT_ADMIN.email;
    const recipient = await findUserByEmail(request, token, recipientEmail);
    if (!recipient) {
        throw new Error(`未找到通知接收者: ${recipientEmail}`);
    }

    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(projectKey, 'notifications'),
        {
            method: 'POST',
            data: {
                type: 'system_alert',
                title: options.title,
                content: options.content,
                priority: 'normal',
                channel: 'in_app',
                recipient_id: recipient.id,
            },
        },
    );

    const data = (response.data as Record<string, unknown>) ?? {};
    const id = data.id as number;
    trackE2EResource('notifications', id);
    return id;
};

export const deleteNotification = async (request: APIRequestContext, id: number) => {
    if (!trackedResources.notifications.has(String(id))) {
        throw new Error(`拒绝删除未由本轮创建的通知：${id}`);
    }
    const token = await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    await apiRequest(
        request,
        token,
        projectAPIPath(projectKey, `notifications/${encodeURIComponent(id)}`),
        { method: 'DELETE' },
    );
    untrackE2EResource('notifications', id);
};

export const createTicket = async (request: APIRequestContext, title: string) => {
    if (!title.startsWith(E2E_MARKER)) {
        throw new Error('测试工单标题必须包含本轮唯一 marker');
    }
    const token = await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    const configuration = await resolveE2ETicketCreateConfiguration(
        request,
        token,
        projectKey,
    );
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(projectKey, 'tickets'),
        {
            method: 'POST',
            data: {
                title,
                description: `${title} 自动化测试描述`,
                type: configuration.workClass,
                priority: 'normal',
                source: 'web',
                request_type_version_id:
                    configuration.requestTypeVersionID,
                workflow_version_id: configuration.workflowVersionID,
            },
        },
    );
    const ticket = extractData<Record<string, unknown>>(response);
    if (
        typeof ticket.id !== 'number' ||
        !Number.isSafeInteger(ticket.id) ||
        ticket.id <= 0
    ) {
        throw new Error('创建 E2E 工单后响应缺少有效 ID');
    }
    const id = ticket.id;
    trackE2EResource('tickets', id);
    return id;
};

const deleteVersionedTicket = async (
    request: APIRequestContext,
    token: string,
    id: string | number,
) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    const ticketPath = projectAPIPath(
        projectKey,
        `tickets/${encodeURIComponent(id)}`,
    );
    const detail = await apiRequest<Record<string, unknown>>(
        request,
        token,
        ticketPath,
    );
    const ticket = extractData<Record<string, unknown>>(detail);
    const version = ticket.version;
    if (
        typeof version !== 'number' ||
        !Number.isSafeInteger(version) ||
        version <= 0
    ) {
        throw new Error(`工单 ${id} 缺少有效版本，拒绝无条件清理`);
    }
    await apiRequest(request, token, ticketPath, {
        method: 'DELETE',
        headers: { 'If-Match': `"v${version}"` },
    });
};

export const deleteTicket = async (request: APIRequestContext, id: number) => {
    if (!trackedResources.tickets.has(String(id))) {
        throw new Error(`拒绝删除未由本轮创建的工单：${id}`);
    }
    const token = await getAdminToken(request);
    await deleteVersionedTicket(request, token, id);
    untrackE2EResource('tickets', id);
};

export const createAutomationRule = async (
    request: APIRequestContext,
    name: string,
    active = false,
) => {
    if (!name.startsWith(E2E_MARKER)) {
        throw new Error('测试自动化规则名称必须包含本轮唯一 marker');
    }
    const token = await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(projectKey, 'admin/automation/rules'),
        {
            method: 'POST',
            data: {
                name,
                description: `${name} Playwright 表格测试规则`,
                rule_type: 'assignment',
                trigger_event: 'io.chronodesk.ticket.created.v1',
                priority: 100,
                is_active: active,
                conditions: [],
                actions: [],
            },
        },
    );
    const id = extractData<Record<string, unknown>>(response).id as number;
    trackE2EResource('automationRules', id);
    return id;
};

const deleteAutomationRules = async (request: APIRequestContext, token: string) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    for (const id of trackedIDs('automationRules')) {
        await apiRequest(
            request,
            token,
            projectAPIPath(
                projectKey,
                `admin/automation/rules/${encodeURIComponent(id)}`,
            ),
            { method: 'DELETE' },
        );
        untrackE2EResource('automationRules', id);
    }
};

const deleteTickets = async (request: APIRequestContext, token: string) => {
    for (const id of trackedIDs('tickets')) {
        await deleteVersionedTicket(request, token, id);
        untrackE2EResource('tickets', id);
    }
};

const deleteNotifications = async (request: APIRequestContext, token: string) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    for (const id of trackedIDs('notifications')) {
        await apiRequest(
            request,
            token,
            projectAPIPath(
                projectKey,
                `notifications/${encodeURIComponent(id)}`,
            ),
            { method: 'DELETE' },
        );
        untrackE2EResource('notifications', id);
    }
};

const deleteWebhooks = async (request: APIRequestContext, token: string) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    for (const id of trackedIDs('webhooks')) {
        await apiRequest(
            request,
            token,
            projectAPIPath(
                projectKey,
                `webhooks/${encodeURIComponent(id)}`,
            ),
            { method: 'DELETE' },
        );
        untrackE2EResource('webhooks', id);
    }
};

type AgentControlPrincipal = {
    id: string;
    name: string;
    status: 'active' | 'inactive' | 'revoked';
    read_only: boolean;
    emergency_disabled: boolean;
    resource_version: number;
};

type AgentControlSnapshot = {
    global_read_only: boolean;
    emergency_stop: boolean;
    principals: AgentControlPrincipal[];
    attachments?: Array<{
        id: number;
        original_name: string;
        virus_scan: string;
        resource_version: number;
    }>;
};

const getAgentControlSnapshot = async (
    request: APIRequestContext,
    suppliedToken?: string,
) => {
    const token = suppliedToken ?? await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(projectKey, 'admin/agents/agent-control/overview'),
    );
    return extractData<AgentControlSnapshot>(response);
};

export type AgentGlobalControlSnapshot = Pick<
    AgentControlSnapshot,
    | 'global_read_only'
    | 'emergency_stop'
>;

export const captureAgentGlobalControls = async (
    request: APIRequestContext,
): Promise<AgentGlobalControlSnapshot> => {
    const snapshot = await getAgentControlSnapshot(request);
    return {
        global_read_only: snapshot.global_read_only,
        emergency_stop: snapshot.emergency_stop,
    };
};

export const restoreAgentGlobalControls = async (
    request: APIRequestContext,
    original: AgentGlobalControlSnapshot,
) => {
    const token = await getAdminToken(request);
    const current = await getAgentControlSnapshot(request, token);
    if (
        current.global_read_only === original.global_read_only &&
        current.emergency_stop === original.emergency_stop
    ) {
        return;
    }
    throw new Error('项目级 Agent 控制面不允许修改平台全局安全开关');
};

const disableTrackedAgentPrincipals = async (
    request: APIRequestContext,
    token: string,
) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    const agentAdminPath = projectAPIPath(projectKey, 'admin/agents');
    for (const principalID of trackedIDs('agentPrincipals')) {
        const policyResponse = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `${agentAdminPath}/service-principals/${principalID}/policies`,
        );
        const policies = extractData<Array<Record<string, unknown>>>(
            policyResponse,
        ) ?? [];
        for (const policy of policies) {
            if (
                policy.is_active !== true ||
                typeof policy.id !== 'string' ||
                typeof policy.resource_version !== 'number'
            ) {
                continue;
            }
            await adminCommand(
                request,
                token,
                `${agentAdminPath}/service-principals/${principalID}/policies/${policy.id}`,
                {
                    method: 'DELETE',
                    version: policy.resource_version,
                },
            );
        }

        const snapshot = await getAgentControlSnapshot(request, token);
        const principal = snapshot.principals.find(
            (candidate) => candidate.id === principalID,
        );
        if (!principal) {
            untrackE2EResource('agentPrincipals', principalID);
            continue;
        }
        if (
            principal.status !== 'inactive' ||
            principal.read_only ||
            principal.emergency_disabled
        ) {
            await adminCommand(
                request,
                token,
                `${agentAdminPath}/service-principals/${principalID}/status`,
                {
                    method: 'PUT',
                    version: principal.resource_version,
                    data: {
                        status: 'inactive',
                        read_only: false,
                        emergency_disabled: false,
                    },
                },
            );
        }
        untrackE2EResource('agentPrincipals', principalID);
    }
};

export const cleanupTrackedAgentPrincipals = async (
    request: APIRequestContext,
) => {
    const token = await getAdminToken(request);
    await disableTrackedAgentPrincipals(request, token);
};

export const markE2EAttachmentClean = async (
    request: APIRequestContext,
    originalName: string,
) => {
    if (!originalName.startsWith(E2E_MARKER)) {
        throw new Error('拒绝修改不属于本轮 marker 的附件扫描状态');
    }
    const token = await getAdminToken(request);
    const projectKey = await resolveE2EProjectKey(request, token);
    const agentAdminPath = projectAPIPath(projectKey, 'admin/agents');
    const snapshot = await getAgentControlSnapshot(request, token);
    const attachment = (snapshot.attachments ?? []).find(
        (candidate) => candidate.original_name === originalName,
    );
    if (!attachment) {
        throw new Error(`未找到待扫描 E2E 附件：${originalName}`);
    }
    await adminCommand(
        request,
        token,
        `${agentAdminPath}/attachments/${attachment.id}/scan`,
        {
            method: 'POST',
            version: attachment.resource_version,
            data: {
                status: 'clean',
                details: 'Playwright E2E 小文件安全扫描通过',
            },
        },
    );
    return attachment.id;
};

export const revokeE2ETrustedDevices = async (
    request: APIRequestContext,
    suppliedToken?: string,
) => {
    const token = suppliedToken ?? await getAdminToken(request);
    for (const id of trackedIDs('trustedDevices')) {
        await apiRequest(
            request,
            token,
            `/api/user/trusted-devices/${encodeURIComponent(id)}`,
            { method: 'DELETE' },
        );
        untrackE2EResource('trustedDevices', id);
    }
};

export const trackTrustedDeviceByName = async (
    request: APIRequestContext,
    deviceName: string,
) => {
    if (!deviceName.startsWith(E2E_MARKER)) {
        throw new Error(`拒绝登记不属于本轮 marker 的可信设备：${deviceName}`);
    }
    const token = await getAdminToken(request);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        '/api/user/trusted-devices',
    );
    const devices = extractData<Array<Record<string, unknown>>>(response) ?? [];
    const device = devices.find(
        (candidate) => candidate.device_name === deviceName,
    );
    if (typeof device?.id !== 'number') {
        throw new Error(`未找到本轮可信设备：${deviceName}`);
    }
    trackE2EResource('trustedDevices', device.id);
    return device.id;
};

const deleteTestUsers = async (request: APIRequestContext, token: string) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    for (const id of trackedIDs('users')) {
        await deactivateProjectMembership(
            request,
            token,
            projectKey,
            Number(id),
        );
        await apiRequest(
            request,
            token,
            `/api/admin/users/${encodeURIComponent(id)}`,
            { method: 'DELETE' },
        );
        untrackE2EResource('users', id);
    }
    if (temporaryRoleAccounts) {
        const accounts = await temporaryRoleAccounts.catch(() => undefined);
        if (accounts) {
            authSessions.delete(accounts.agent.email.toLowerCase());
            authSessions.delete(accounts.customer.email.toLowerCase());
        }
        temporaryRoleAccounts = undefined;
    }
};

export type EmailConfigSnapshot = {
    id: number;
    email_verification_enabled: boolean;
    smtp_host: string;
    smtp_port: number;
    smtp_username: string;
    smtp_use_tls: boolean;
    smtp_use_ssl: boolean;
    from_email: string;
    from_name: string;
    welcome_email_subject: string;
    welcome_email_template: string;
    otp_email_subject: string;
    otp_email_template: string;
    is_configured: boolean;
};

const EMAIL_RESTORABLE_KEYS = [
    'email_verification_enabled',
    'smtp_host',
    'smtp_port',
    'smtp_username',
    'smtp_use_tls',
    'smtp_use_ssl',
    'from_email',
    'from_name',
    'welcome_email_subject',
    'welcome_email_template',
    'otp_email_subject',
    'otp_email_template',
] as const satisfies ReadonlyArray<keyof EmailConfigSnapshot>;

const comparableEmailConfig = (config: EmailConfigSnapshot) =>
    Object.fromEntries(
        EMAIL_RESTORABLE_KEYS.map((key) => [key, config[key]]),
    );

export const captureEmailConfig = async (
    request: APIRequestContext,
): Promise<EmailConfigSnapshot> => {
    const token = await getAdminToken(request);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        '/api/admin/email-config',
    );
    return extractData<EmailConfigSnapshot>(response);
};

export const assertEmailConfigMutationSafe = (
    snapshot: EmailConfigSnapshot,
    touchesSMTPPassword = false,
) => {
    assertGlobalE2EAllowed('修改邮件配置');
    assertSecretMutationIsRestorable(
        '修改 SMTP 密码',
        touchesSMTPPassword,
    );
    if (!isLoopbackE2E() && snapshot.email_verification_enabled) {
        throw new Error(
            '拒绝在非回环环境修改已启用的邮件配置：保存动作会触发真实 SMTP 连接。',
        );
    }
};

export const restoreEmailConfig = async (
    request: APIRequestContext,
    original: EmailConfigSnapshot,
    expectedAfterTest: EmailConfigSnapshot,
) => {
    const token = await getAdminToken(request);
    const current = await captureEmailConfig(request);
    const originalComparable = comparableEmailConfig(original);
    const currentComparable = comparableEmailConfig(current);
    if (JSON.stringify(currentComparable) === JSON.stringify(originalComparable)) {
        return;
    }
    if (
        JSON.stringify(currentComparable) !==
        JSON.stringify(comparableEmailConfig(expectedAfterTest))
    ) {
        throw new Error(
            '邮件配置在测试期间被其他写入修改，拒绝覆盖并停止恢复。',
        );
    }

    assertGlobalE2EAllowed('恢复邮件配置');
    await apiRequest(request, token, '/api/admin/email-config', {
        method: 'PUT',
        data: {
            ...originalComparable,
            skip_smtp_test: true,
        },
    });
    const restored = await captureEmailConfig(request);
    if (
        JSON.stringify(comparableEmailConfig(restored)) !==
        JSON.stringify(originalComparable)
    ) {
        throw new Error('邮件配置未恢复到测试前快照');
    }
};

export type SystemConfigSnapshot = {
    id: number;
    key: string;
    value: string;
    value_type: 'string' | 'int' | 'bool' | 'json';
    description: string;
    category: string;
    group: string;
    is_required: boolean;
    is_active: boolean;
    version: number;
};

export const captureSystemConfig = async (
    request: APIRequestContext,
    category: string,
    key: string,
): Promise<SystemConfigSnapshot> => {
    const token = await getAdminToken(request);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/admin/configs?category=${encodeURIComponent(category)}`,
    );
    const config = extractData<SystemConfigSnapshot[]>(response).find(
        (candidate) => candidate.key === key,
    );
    if (!config) {
        throw new Error(`未找到系统配置：${key}`);
    }
    return config;
};

export const restoreSystemConfig = async (
    request: APIRequestContext,
    original: SystemConfigSnapshot,
    expectedAfterTest: SystemConfigSnapshot,
) => {
    const current = await captureSystemConfig(
        request,
        original.category,
        original.key,
    );
    if (current.value === original.value) {
        return;
    }
    if (current.value !== expectedAfterTest.value) {
        throw new Error(
            `系统配置 ${original.key} 在测试期间被其他写入修改，拒绝覆盖。`,
        );
    }

    assertGlobalE2EAllowed(`恢复系统配置 ${original.key}`);
    const token = await getAdminToken(request);
    await apiRequest(
        request,
        token,
        `/api/admin/configs/${encodeURIComponent(original.key)}`,
        {
            method: 'PUT',
            data: {
                key: original.key,
                value: original.value,
                value_type: original.value_type,
                description: original.description,
                category: original.category,
                group: original.group,
                is_required: original.is_required,
                is_active: original.is_active,
            },
        },
    );
    const restored = await captureSystemConfig(
        request,
        original.category,
        original.key,
    );
    if (restored.value !== original.value) {
        throw new Error(`系统配置 ${original.key} 未恢复到测试前快照`);
    }
};

type CleanupOptions = {
    automationRules?: boolean;
    tickets?: boolean;
    notifications?: boolean;
    users?: boolean;
    emailConfig?: boolean;
    webhooks?: boolean;
    agentControl?: boolean;
};

const defaultCleanupOptions: Required<CleanupOptions> = {
    automationRules: true,
    tickets: true,
    notifications: true,
    users: true,
    // 邮件配置只能使用用例自己的快照恢复，通用清理绝不能写入共享配置。
    emailConfig: false,
    webhooks: false,
    agentControl: false,
};

export const cleanupE2EData = async (request: APIRequestContext, options: CleanupOptions = {}) => {
    const token = await getAdminToken(request);
    const config = { ...defaultCleanupOptions, ...options };

    if (config.automationRules) {
        await deleteAutomationRules(request, token);
    }
    if (config.tickets) {
        await deleteTickets(request, token);
    }
    if (config.notifications) {
        await deleteNotifications(request, token);
    }
    if (config.users) {
        await deleteTestUsers(request, token);
    }
    if (config.emailConfig) {
        throw new Error('邮件配置必须使用 captureEmailConfig/restoreEmailConfig 精确恢复');
    }
    if (config.webhooks) {
        await deleteWebhooks(request, token);
    }
    if (config.agentControl) {
        await disableTrackedAgentPrincipals(request, token);
    }
};
