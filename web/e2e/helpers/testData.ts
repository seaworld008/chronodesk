import type {
    APIRequestContext,
    APIResponse,
    Page,
} from '@playwright/test';
import {
    projectRoleValues,
    type UpdatePlatformConfigOperationRequest,
} from '../../src/lib/generated/human-api';
import {
    apiRequest,
    loginSession,
    type AuthSession,
    type Credentials,
    type PlatformRole,
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

type HumanSessionBinding = {
    subject: string;
    sessionID: string;
    expiresAt: number;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null;

const humanSessionBinding = (
    session: AuthSession,
): HumanSessionBinding => {
    const parts = session.access_token.split('.');
    if (parts.length !== 3) {
        throw new Error('E2E 登录响应 access_token 不是合法 JWT');
    }
    try {
        const normalized = parts[1]
            .replace(/-/g, '+')
            .replace(/_/g, '/');
        const padded = normalized.padEnd(
            normalized.length + ((4 - (normalized.length % 4)) % 4),
            '=',
        );
        const payload = JSON.parse(atob(padded)) as Record<string, unknown>;
        if (
            typeof payload.sub !== 'string' ||
            payload.sub.length === 0 ||
            typeof payload.sid !== 'string' ||
            payload.sid.length === 0 ||
            typeof payload.exp !== 'number' ||
            !Number.isSafeInteger(payload.exp) ||
            payload.exp <= 0 ||
            payload.platform_role !== session.user.platform_role
        ) {
            throw new Error('JWT 主体、会话或 platform_role 与登录用户不一致');
        }
        return {
            subject: payload.sub,
            sessionID: payload.sid,
            expiresAt: payload.exp * 1000,
        };
    } catch (error) {
        throw new Error(
            `E2E 登录响应缺少可绑定项目缓存的会话声明：${
                error instanceof Error ? error.message : String(error)
            }`,
        );
    }
};

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

export const getAdminToken = async (request: APIRequestContext) => {
    const session = await getAuthSession(request, DEFAULT_ADMIN);
    if (session.user.platform_role !== 'platform_admin') {
        throw new Error(
            `E2E 默认治理账号必须是 platform_admin，实际为 ${session.user.platform_role}`,
        );
    }
    return session.access_token;
};

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
            '/api/projects?page=1&page_size=100&sort_by=name&sort_order=asc',
        );
        const accesses = extractItems<Record<string, unknown>>(response);
        const selected = accesses
            .filter((access) =>
                typeof access.project_role === 'string' &&
                projectRoleValues.some(
                    (projectRole) =>
                        projectRole === access.project_role,
                ),
            )
            .map((access) => access.project)
            .find(
                (project): project is Record<string, unknown> =>
                    isRecord(project) &&
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

const waitForAuthenticatedShell = async (
    page: Page,
    expectedProjectKey: string,
) => {
    await page
        .getByRole('banner')
        .getByRole('button', { name: '账号', exact: true })
        .waitFor({ timeout: 15_000 });
    await page.getByRole('main').waitFor({ timeout: 15_000 });
    await page
        .getByTestId('project-switcher-loading')
        .waitFor({ state: 'hidden', timeout: 15_000 });
    await page
        .getByTestId('active-project-switcher')
        .waitFor({ state: 'visible', timeout: 15_000 });
    await page.waitForFunction((projectKey) => {
        const serialized = sessionStorage.getItem(
            'chronodesk.activeProject',
        );
        if (!serialized) return false;
        try {
            const selection = JSON.parse(serialized) as {
                project_key?: unknown;
            };
            return selection.project_key === projectKey;
        } catch {
            return false;
        }
    }, expectedProjectKey, { timeout: 15_000 });
};

export const authenticatePage = async (
    page: Page,
    credentials: Credentials = DEFAULT_ADMIN,
) => {
    const session = await getAuthSession(page.request, credentials);
    const binding = humanSessionBinding(session);
    const projectKey = await resolveE2EProjectKey(
        page.request,
        session.access_token,
    );
    await installBrowserMutationGuard(page);
    await page.addInitScript(({ auth, activeProjectKey, sessionBinding }) => {
        if (localStorage.getItem('token') === auth.access_token) {
            return;
        }
        for (const key of [
            'token',
            'refreshToken',
            'user',
            'tokenExpiresAt',
            'chronodesk.activeProject',
        ]) {
            localStorage.removeItem(key);
            sessionStorage.removeItem(key);
        }
        localStorage.setItem('token', auth.access_token);
        sessionStorage.setItem(
            'chronodesk.activeProject',
            JSON.stringify({
                subject: sessionBinding.subject,
                session_id: sessionBinding.sessionID,
                project_key: activeProjectKey,
            }),
        );
        sessionStorage.setItem('user', JSON.stringify(auth.user));
        sessionStorage.setItem(
            'tokenExpiresAt',
            String(sessionBinding.expiresAt),
        );
    }, {
        auth: session,
        activeProjectKey: projectKey,
        sessionBinding: binding,
    });
    await page.goto('/#/');
    await waitForAuthenticatedShell(page, projectKey);
};

export const selectDefaultProjectViaUI = async (page: Page) => {
    const projectSelection = page.getByTestId(
        'active-project-selection-required',
    );
    await projectSelection.waitFor({ timeout: 15_000 });
    await projectSelection
        .getByTestId('select-project-DEFAULT')
        .click();
    await projectSelection.waitFor({ state: 'hidden', timeout: 15_000 });
    await waitForAuthenticatedShell(page, 'DEFAULT');

    const selectedProjectKey = await page.evaluate(() => {
        const serialized = sessionStorage.getItem(
            'chronodesk.activeProject',
        );
        if (!serialized) {
            return null;
        }
        try {
            const selection = JSON.parse(serialized) as {
                project_key?: unknown;
            };
            return typeof selection.project_key === 'string'
                ? selection.project_key
                : null;
        } catch {
            return null;
        }
    });
    if (selectedProjectKey !== 'DEFAULT') {
        throw new Error(
            `E2E 登录后必须显式选择 DEFAULT 项目，实际为 ${String(selectedProjectKey)}`,
        );
    }
};

export const loginViaUI = async (page: Page, credentials: Credentials = DEFAULT_ADMIN) => {
    await installBrowserMutationGuard(page);
    await page.goto('/#/login');
    await page.getByLabel('邮箱').fill(credentials.email);
    await page.getByLabel('密码').fill(credentials.password);
    await page.getByRole('button', { name: '登录系统' }).click();
    await selectDefaultProjectViaUI(page);
};

export const findUserByEmail = async (
    request: APIRequestContext,
    token: string,
    email: string,
) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/platform/users?search=${encodeURIComponent(email)}&page=1&page_size=50`,
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
    platformRole: PlatformRole;
    projectRole: 'agent' | 'requester';
    optionLabel: string;
};

type ProjectMembership = {
    user_id?: unknown;
    role?: unknown;
    is_active?: unknown;
    version?: unknown;
};

export type TemporaryRoleAccounts = {
    agent: TemporaryRoleAccount;
    requester: TemporaryRoleAccount;
};

let temporaryRoleAccounts: Promise<TemporaryRoleAccounts> | undefined;
const trackedMembershipTargets = new Set<string>();
const trackedMembershipVersions = new Map<string, number>();

const membershipTrackingKey = (projectKey: string, userID: number) =>
    `${projectKey}:${userID}`;

const positiveVersion = (value: unknown): value is number =>
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value > 0;

const responsePayload = async (
    response: APIResponse,
): Promise<Record<string, unknown> | undefined> => {
    const payload = await response.json().catch(() => undefined);
    return isRecord(payload) ? payload : undefined;
};

const membershipVersion = (
    membership: ProjectMembership | undefined,
    userID: number,
    expectedRole?: TemporaryRoleAccount['projectRole'],
) => {
    if (
        membership?.user_id !== userID ||
        membership.is_active !== true ||
        (expectedRole !== undefined && membership.role !== expectedRole) ||
        !positiveVersion(membership.version)
    ) {
        return undefined;
    }
    return membership.version;
};

const findProjectMembership = async (
    request: APIRequestContext,
    token: string,
    projectKey: string,
    userID: number,
): Promise<ProjectMembership | undefined> => {
    let page = 1;
    while (true) {
        const query = new URLSearchParams({
            page: String(page),
            page_size: '100',
            sort_by: 'user_id',
            sort_order: 'asc',
        });
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `${projectAPIPath(projectKey, 'memberships')}?${query.toString()}`,
        );
        const directory = extractData<Record<string, unknown>>(response);
        const items = directory.items;
        const totalPages = directory.total_pages;
        if (
            !Array.isArray(items) ||
            typeof totalPages !== 'number' ||
            !Number.isSafeInteger(totalPages) ||
            totalPages < 0 ||
            directory.page !== page
        ) {
            throw new Error('项目成员对账响应缺少严格分页数据');
        }
        const matches = items.filter(
            (item): item is ProjectMembership =>
                isRecord(item) && item.user_id === userID,
        );
        if (matches.length > 1) {
            throw new Error(`项目成员对账发现重复用户：${userID}`);
        }
        if (matches.length === 1) {
            return matches[0];
        }
        if (page >= totalPages) {
            return undefined;
        }
        page += 1;
    }
};

const clearTrackedMembership = (projectKey: string, userID: number) => {
    const key = membershipTrackingKey(projectKey, userID);
    trackedMembershipTargets.delete(key);
    trackedMembershipVersions.delete(key);
};

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

const createProjectMembership = async (
    request: APIRequestContext,
    token: string,
    projectKey: string,
    userID: number,
    role: TemporaryRoleAccount['projectRole'],
) => {
    if (!trackedResources.users.has(String(userID))) {
        throw new Error(`拒绝为非本轮创建用户授权项目成员：${userID}`);
    }
    assertDestructiveE2EAllowed(
        `POST project membership ${projectKey}/${userID}`,
    );
    const key = membershipTrackingKey(projectKey, userID);
    trackedMembershipTargets.add(key);
    const path = projectAPIPath(projectKey, 'memberships');

    for (let attempt = 0; attempt < 2; attempt += 1) {
        let response: APIResponse;
        try {
            response = await request.post(path, {
                headers: {
                    Authorization: `Bearer ${token}`,
                    'Content-Type': 'application/json',
                },
                data: {
                    user_id: userID,
                    role,
                    expected_version: 0,
                },
            });
        } catch (error) {
            const current = await findProjectMembership(
                request,
                token,
                projectKey,
                userID,
            );
            const currentVersion = membershipVersion(
                current,
                userID,
                role,
            );
            if (currentVersion !== undefined) {
                trackedMembershipVersions.set(key, currentVersion);
                return;
            }
            if (current !== undefined || attempt === 1) {
                throw new Error(
                    `创建用户 ${userID} 的项目成员结果不确定，且对账未确认目标授权：${
                        error instanceof Error ? error.message : String(error)
                    }`,
                );
            }
            continue;
        }

        const payload = await responsePayload(response);
        if (response.status() === 200) {
            const persisted = extractData<ProjectMembership>(payload ?? {});
            let version = membershipVersion(persisted, userID, role);
            if (version === undefined) {
                version = membershipVersion(
                    await findProjectMembership(
                        request,
                        token,
                        projectKey,
                        userID,
                    ),
                    userID,
                    role,
                );
            }
            if (version === undefined) {
                throw new Error(
                    `创建用户 ${userID} 的项目成员后缺少有效版本`,
                );
            }
            trackedMembershipVersions.set(key, version);
            return;
        }

        if (response.status() === 409) {
            const current = await findProjectMembership(
                request,
                token,
                projectKey,
                userID,
            );
            const currentVersion = membershipVersion(
                current,
                userID,
                role,
            );
            if (currentVersion !== undefined) {
                trackedMembershipVersions.set(key, currentVersion);
                return;
            }
            if (current === undefined && attempt === 0) {
                continue;
            }
        }
        throw new Error(
            `创建用户 ${userID} 的项目成员失败：HTTP ${response.status()}`,
        );
    }
};

const deactivateProjectMembership = async (
    request: APIRequestContext,
    token: string,
    projectKey: string,
    userID: number,
) => {
    if (!trackedResources.users.has(String(userID))) {
        throw new Error(`拒绝清理非本轮创建用户的项目成员：${userID}`);
    }
    const key = membershipTrackingKey(projectKey, userID);
    if (!trackedMembershipTargets.has(key)) {
        return;
    }
    assertDestructiveE2EAllowed(
        `DELETE project membership ${projectKey}/${userID}`,
    );
    const path = projectAPIPath(
        projectKey,
        `memberships/${encodeURIComponent(userID)}`,
    );
    let version = trackedMembershipVersions.get(key);
    if (!positiveVersion(version)) {
        const current = await findProjectMembership(
            request,
            token,
            projectKey,
            userID,
        );
        if (current === undefined || current.is_active === false) {
            clearTrackedMembership(projectKey, userID);
            return;
        }
        version = membershipVersion(current, userID);
        if (version === undefined) {
            throw new Error(`用户 ${userID} 的项目成员清理版本无效`);
        }
        trackedMembershipVersions.set(key, version);
    }

    for (let attempt = 0; attempt < 2; attempt += 1) {
        let response: APIResponse;
        try {
            response = await request.delete(path, {
                headers: {
                    Authorization: `Bearer ${token}`,
                    'Content-Type': 'application/json',
                },
                params: { expected_version: version },
            });
        } catch (error) {
            const current = await findProjectMembership(
                request,
                token,
                projectKey,
                userID,
            );
            if (current === undefined || current.is_active === false) {
                clearTrackedMembership(projectKey, userID);
                return;
            }
            const refreshedVersion = membershipVersion(current, userID);
            if (refreshedVersion === undefined || attempt === 1) {
                throw new Error(
                    `撤销用户 ${userID} 的项目成员结果不确定，且对账仍为有效授权：${
                        error instanceof Error ? error.message : String(error)
                    }`,
                );
            }
            version = refreshedVersion;
            trackedMembershipVersions.set(key, version);
            continue;
        }

        if ([200, 204, 404].includes(response.status())) {
            clearTrackedMembership(projectKey, userID);
            return;
        }
        if (response.status() === 409) {
            const current = await findProjectMembership(
                request,
                token,
                projectKey,
                userID,
            );
            if (current === undefined || current.is_active === false) {
                clearTrackedMembership(projectKey, userID);
                return;
            }
            const refreshedVersion = membershipVersion(current, userID);
            if (refreshedVersion !== undefined && attempt === 0) {
                version = refreshedVersion;
                trackedMembershipVersions.set(key, version);
                continue;
            }
        }
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
                `/api/platform/users/${encodeURIComponent(id)}`,
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
        const requesterIdentity = temporaryRoleAccountIdentity('requester');
        const definitions = [
            {
                key: 'agent',
                ...agentIdentity,
                platformRole: 'member',
                projectRole: 'agent',
                first_name: 'Support',
                last_name: 'Agent',
                department: 'Support',
                job_title: 'Support Agent',
            },
            {
                key: 'requester',
                ...requesterIdentity,
                platformRole: 'member',
                projectRole: 'requester',
                first_name: 'Demo',
                last_name: 'Requester',
                department: 'Request',
                job_title: 'Requester',
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
                    '/api/platform/users',
                    {
                        method: 'POST',
                        data: {
                            username: definition.username,
                            email: definition.email,
                            password: DEFAULT_PASSWORD,
                            platform_role: definition.platformRole,
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
                await createProjectMembership(
                    request,
                    token,
                    projectKey,
                    created.id,
                    definition.projectRole,
                );
                result[definition.key] = {
                    id: created.id,
                    username: definition.username,
                    email: definition.email,
                    password: DEFAULT_PASSWORD,
                    platformRole: definition.platformRole,
                    projectRole: definition.projectRole,
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
        const webhookPath = projectAPIPath(
            projectKey,
            `webhooks/${encodeURIComponent(id)}`,
        );
        const detailResponse = await apiRequest<Record<string, unknown>>(
            request,
            token,
            webhookPath,
        );
        const resourceVersion = extractData<Record<string, unknown>>(
            detailResponse,
        ).resource_version;
        if (!positiveVersion(resourceVersion)) {
            throw new Error(
                `Webhook ${id} 清理响应缺少合法 resource_version`,
            );
        }
        await apiRequest(
            request,
            token,
            webhookPath,
            {
                method: 'DELETE',
                headers: {
                    'If-Match': `"v${resourceVersion}"`,
                },
            },
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
};

type AgentControlPage<T> = {
    items: T[];
    total: number;
    page: number;
    page_size: number;
    total_pages: number;
};

type AgentAttachmentScan = {
    id: number;
    original_name: string;
    virus_scan: string;
    resource_version: number;
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

const getAllAgentListItems = async <T>(
    request: APIRequestContext,
    token: string,
    path: string,
    sortBy: string,
    sortOrder: 'asc' | 'desc',
): Promise<T[]> => {
    const items: T[] = [];
    let page = 1;
    while (true) {
        const query = new URLSearchParams({
            page: String(page),
            page_size: '100',
            sort_by: sortBy,
            sort_order: sortOrder,
        });
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `${path}?${query.toString()}`,
        );
        const result = extractData<AgentControlPage<T>>(response);
        items.push(...(result.items ?? []));
        if (page >= result.total_pages) return items;
        page += 1;
    }
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
        const policyPage = extractData<AgentControlPage<Record<string, unknown>>>(
            policyResponse,
        );
        for (const policy of policyPage.items ?? []) {
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

        const principals = await getAllAgentListItems<AgentControlPrincipal>(
            request,
            token,
            `${agentAdminPath}/service-principals`,
            'created_at',
            'desc',
        );
        const principal = principals.find(
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
    const attachments = await getAllAgentListItems<AgentAttachmentScan>(
        request,
        token,
        `${agentAdminPath}/attachments`,
        'created_at',
        'desc',
    );
    const attachment = attachments.find(
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
        '/api/user/trusted-devices?page=1&page_size=100&sort_by=revoked&sort_order=asc',
    );
    const devicePage = extractData<{
        items: Array<Record<string, unknown>>;
    }>(response);
    const devices = devicePage?.items ?? [];
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
            `/api/platform/users/${encodeURIComponent(id)}`,
            { method: 'DELETE' },
        );
        untrackE2EResource('users', id);
    }
    if (temporaryRoleAccounts) {
        const accounts = await temporaryRoleAccounts.catch(() => undefined);
        if (accounts) {
            authSessions.delete(accounts.agent.email.toLowerCase());
            authSessions.delete(accounts.requester.email.toLowerCase());
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
        '/api/platform/email-config',
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
    await apiRequest(request, token, '/api/platform/email-config', {
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
    for (let page = 1; page <= 100; page += 1) {
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `/api/platform/configs?category=${encodeURIComponent(category)}&page=${page}&page_size=100&sort_by=group&sort_order=asc`,
        );
        const configPage = extractData<{
            items: SystemConfigSnapshot[];
            total_pages: number;
        }>(response);
        const config = configPage?.items.find(
            (candidate) => candidate.key === key,
        );
        if (config) {
            return config;
        }
        if (!configPage || page >= configPage.total_pages) {
            break;
        }
    }
    throw new Error(`未找到系统配置：${key}`);
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
    const restoreRequest: UpdatePlatformConfigOperationRequest = {
        value: original.value,
        value_type: original.value_type,
        description: original.description,
        category: original.category,
        group: original.group,
    };
    await apiRequest(
        request,
        token,
        `/api/platform/configs/${encodeURIComponent(original.key)}`,
        {
            method: 'PUT',
            data: restoreRequest,
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
