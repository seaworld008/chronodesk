import {
    expect,
    test,
    type Page,
    type Route,
} from '@playwright/test';
import { platformRoleValues } from '../src/lib/generated/human-api';
import type {
    AuthorizedProject as Project,
    AuthorizedProjectAccess as ProjectAccess,
    BusinessUnit,
    Organization,
    PlatformRole,
    ProjectRole,
} from '../src/lib/generated/human-api';

type SessionIdentity = {
    id: number;
    subject: string;
    sessionID: string;
    email: string;
    platformRole: PlatformRole;
};

type MockBackendState = {
    accesses: ProjectAccess[];
    accessesByToken: Map<string, ProjectAccess[]>;
    forbiddenCodes: Map<string, string>;
    projectListRequests: string[];
    scopedProjectRequests: string[];
    deniedProjectRequests: string[];
    platformRequests: string[];
};

type AuditRouteResponder = (
    route: Route,
    url: URL,
) => Promise<boolean>;

const contractTimestamp = '2026-07-30T08:00:00Z';

const organization: Organization = {
    id: 1,
    public_id: '00000000-0000-7000-8000-000000000001',
    created_at: contractTimestamp,
    updated_at: contractTimestamp,
    slug: 'e2e-organization',
    name: 'E2E 组织',
    description: 'Playwright 项目权限契约组织',
    status: 'active',
};

const businessUnit: BusinessUnit = {
    id: 1,
    public_id: '00000000-0000-7000-8000-000000000002',
    created_at: contractTimestamp,
    updated_at: contractTimestamp,
    organization_id: organization.id,
    organization,
    key: 'E2E',
    name: 'E2E 业务单元',
    description: 'Playwright 项目权限契约业务单元',
    status: 'active',
};

const project: Project = {
    id: 73,
    public_id: '00000000-0000-7000-8000-000000000073',
    created_at: contractTimestamp,
    updated_at: contractTimestamp,
    organization_id: 1,
    organization,
    business_unit_id: 1,
    business_unit: businessUnit,
    key: 'OPS',
    name: '运营支持',
    description: '运营支持项目',
    status: 'active',
    ticket_sequence: 9001,
};

const financeProject: Project = {
    ...project,
    id: 74,
    public_id: '00000000-0000-7000-8000-000000000074',
    key: 'FIN',
    name: '财务服务',
};

const defaultIdentity: SessionIdentity = {
    id: 42,
    subject: '42',
    sessionID: 'session-project-scope',
    email: 'project-scope@example.test',
    platformRole: 'member',
};

const mockTicket = {
    id: 9001,
    public_id: '00000000-0000-7000-8000-000000009001',
    project_id: project.id,
    ticket_number: 'OPS-9001',
    title: '项目角色按钮矩阵工单',
    description: '用于验证项目角色不会从平台角色推导',
    type: 'request',
    priority: 'normal',
    status: 'open',
    source: 'web',
    created_by_id: defaultIdentity.id,
    assigned_to_id: defaultIdentity.id,
    version: 1,
    tags: [],
    sla_breached: false,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T09:00:00Z',
};

const projectAccess = (
    projectValue: Project,
    projectRole: ProjectRole,
    canCreateKnowledgeDrafts =
        projectRole === 'project_admin' || projectRole === 'manager',
): ProjectAccess => ({
    project: projectValue,
    project_role: projectRole,
    can_create_knowledge_drafts: canCreateKnowledgeDrafts,
    scope: {
        organization_id: projectValue.organization_id,
        project_id: projectValue.id,
    },
});

const encodeBase64URL = (value: unknown): string =>
    btoa(JSON.stringify(value))
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/u, '');

const sessionToken = (
    identity: SessionIdentity,
    expiresAtSeconds = Math.floor(Date.now() / 1000) + 3600,
): string => [
    encodeBase64URL({ alg: 'none', typ: 'JWT' }),
    encodeBase64URL({
        sub: identity.subject,
        sid: identity.sessionID,
        platform_role: identity.platformRole,
        exp: expiresAtSeconds,
    }),
    'e2e-signature',
].join('.');

const installSession = async (
    page: Page,
    identity: SessionIdentity,
    activeProject?: Project,
) => {
    const expiresAtSeconds = Math.floor(Date.now() / 1000) + 3600;
    const token = sessionToken(identity, expiresAtSeconds);
    await page.addInitScript(
        ({ authToken, user, selectedProject, expiresAt }) => {
            if (localStorage.getItem('token') === authToken) {
                return;
            }
            localStorage.clear();
            sessionStorage.setItem(
                'user',
                JSON.stringify({
                    id: user.id,
                    username: `e2e-${user.id}`,
                    email: user.email,
                    platform_role: user.platformRole,
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                }),
            );
            sessionStorage.setItem(
                'tokenExpiresAt',
                String(expiresAt),
            );
            if (selectedProject) {
                sessionStorage.setItem(
                    'chronodesk.activeProject',
                    JSON.stringify({
                        subject: user.subject,
                        session_id: user.sessionID,
                        project_key: selectedProject.key,
                    }),
                );
            }
            localStorage.setItem('token', authToken);
        },
        {
            authToken: token,
            user: identity,
            selectedProject: activeProject,
            expiresAt: expiresAtSeconds * 1000,
        },
    );
};

const fulfillJSON = (
    route: Route,
    body: unknown,
    status = 200,
) => route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
});

const ticketListResponse = {
    code: 0,
    data: {
        items: [mockTicket],
        total: 1,
        page: 1,
        page_size: 25,
        total_pages: 1,
    },
};

const ticketStatsResponse = {
    code: 0,
    data: {
        total: 1,
        open: 1,
        in_progress: 0,
        pending: 0,
        resolved: 0,
        overdue: 0,
        sla_breached: 0,
        my_tickets: 1,
        unassigned: 0,
        high_priority: 0,
        escalated: 0,
    },
};

const projectResponseBody = (
    pathname: string,
    access: ProjectAccess,
): unknown => {
    if (pathname.endsWith('/context')) {
        return { code: 0, data: access };
    }
    if (pathname.endsWith('/tickets/stats')) {
        return ticketStatsResponse;
    }
    if (/\/tickets\/9001$/u.test(pathname)) {
        return { code: 0, data: mockTicket };
    }
    if (
        pathname.includes('/tickets') &&
        !pathname.includes('/history') &&
        !pathname.includes('/comments') &&
        !pathname.includes('/attachments')
    ) {
        return ticketListResponse;
    }
    if (pathname.endsWith('/webhooks')) {
        return {
            code: 0,
            data: {
                items: [],
                total: 0,
                page: 1,
                page_size: 100,
            },
        };
    }
    if (pathname.endsWith('/admin/automation/rules')) {
        return {
            success: true,
            data: { rules: [], total: 0 },
        };
    }
    if (pathname.endsWith('/admin/automation/logs')) {
        return {
            success: true,
            data: { logs: [], total: 0 },
        };
    }
    if (pathname.endsWith('/admin/agents/agent-control/overview')) {
        return {
            code: 0,
            data: {
                global_read_only: false,
                emergency_stop: false,
                principal_count: 0,
                active_principal_count: 0,
                active_lease_count: 0,
                failed_outbox_count: 0,
                recent_event_count: 0,
                pending_attachment_scan_count: 0,
            },
        };
    }
    if (pathname.endsWith('/admin/agents/service-principals')) {
        return {
            code: 0,
            data: {
                items: [],
                total: 0,
                page: 1,
                page_size: 25,
                total_pages: 0,
            },
        };
    }
    if (pathname.endsWith('/notifications/unread-count')) {
        return { code: 0, data: { count: 0 } };
    }
    if (pathname.endsWith('/notifications')) {
        return {
            code: 0,
            data: {
                items: [],
                total: 0,
                page: 1,
                page_size: 25,
            },
        };
    }
    return { code: 0, data: [] };
};

const mockBackend = async (
    page: Page,
    accesses: ProjectAccess[] = [],
    accessesByToken = new Map<string, ProjectAccess[]>(),
    auditRouteResponder?: AuditRouteResponder,
): Promise<MockBackendState> => {
    const state: MockBackendState = {
        accesses,
        accessesByToken,
        forbiddenCodes: new Map(),
        projectListRequests: [],
        scopedProjectRequests: [],
        deniedProjectRequests: [],
        platformRequests: [],
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const requestLabel = `${request.method()} ${url.pathname}${url.search}`;
        const authorization = request.headers().authorization ?? '';
        const bearerToken = authorization.startsWith('Bearer ')
            ? authorization.slice('Bearer '.length)
            : '';
        const requestAccesses =
            state.accessesByToken.get(bearerToken) ?? state.accesses;

        if (
            request.method() === 'GET' &&
            url.pathname === '/api/projects'
        ) {
            state.projectListRequests.push(requestLabel);
            await fulfillJSON(route, {
                code: 0,
                data: requestAccesses,
            });
            return;
        }

        const scopedMatch = url.pathname.match(
            /^\/api\/projects\/([^/]+)\/(.+)$/u,
        );
        if (scopedMatch) {
            state.scopedProjectRequests.push(requestLabel);
            const forbiddenCode = state.forbiddenCodes.get(url.pathname);
            if (forbiddenCode) {
                await fulfillJSON(
                    route,
                    {
                        code: forbiddenCode,
                        msg: '当前操作权限不足',
                    },
                    403,
                );
                return;
            }
            const projectKey = decodeURIComponent(scopedMatch[1]);
            const access = requestAccesses.find(
                (candidate) => candidate.project.key === projectKey,
            );
            if (!access) {
                state.deniedProjectRequests.push(requestLabel);
                await fulfillJSON(
                    route,
                    {
                        code: 'project_access_revoked',
                        msg: '无权访问该项目',
                    },
                    403,
                );
                return;
            }
            await fulfillJSON(
                route,
                projectResponseBody(url.pathname, access),
            );
            return;
        }

        if (url.pathname.startsWith('/api/platform/')) {
            state.platformRequests.push(requestLabel);
            if (
                (url.pathname.startsWith('/api/platform/audit-logs') ||
                    url.pathname.startsWith(
                        '/api/platform/audit-exports',
                    )) &&
                auditRouteResponder &&
                (await auditRouteResponder(route, url))
            ) {
                return;
            }
            if (url.pathname === '/api/platform/audit-logs') {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: [],
                        total: 0,
                        page: 1,
                        limit: 25,
                    },
                });
                return;
            }
            if (url.pathname === '/api/platform/users') {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: [],
                        total: 0,
                        page: 1,
                        page_size: 25,
                        pages: 0,
                    },
                });
                return;
            }
            await fulfillJSON(route, { code: 0, data: [] });
            return;
        }

        if (url.pathname === '/api/workbench/tickets') {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [],
                    total: 0,
                    page: 1,
                    page_size: 20,
                    total_pages: 0,
                },
            });
            return;
        }

        await fulfillJSON(route, { code: 0, data: [] });
    });

    return state;
};

const expectMenuItem = async (
    page: Page,
    name: string,
    visible: boolean,
) => {
    const navigationNames: Record<string, { group: string; leaf: string }> = {
        平台用户管理: { group: '治理中心', leaf: '平台身份与访问' },
        系统设置: { group: '系统设置', leaf: '公共配置' },
        平台审计: { group: '治理中心', leaf: '审计中心' },
        工单管理: { group: '项目运营', leaf: '工单管理' },
        通知中心: { group: '项目运营', leaf: '项目通知' },
        自动化规则: { group: '智能运营', leaf: '自动化' },
        自动化日志: { group: '智能运营', leaf: '自动化' },
        'Webhook 集成': { group: '集成中心', leaf: 'Webhook' },
        'AI 智能体控制': { group: '智能运营', leaf: '智能体管理' },
        人机协作: { group: '智能运营', leaf: '人机协作' },
        知识管理: { group: '项目运营', leaf: '知识库' },
    };
    const target = navigationNames[name] ?? { group: '', leaf: name };
    if (target.group) {
        const toggle = page.getByRole('menuitem', {
            name: new RegExp(`^${target.group}`),
        });
        if (
            (await toggle.count()) > 0 &&
            (await toggle.getAttribute('aria-expanded')) !== 'true'
        ) {
            await toggle.click();
        }
    }
    const item = page.getByRole('menuitem', {
        name: target.leaf,
        exact: true,
    });
    if (visible) {
        if ((await item.count()) > 0) {
            await expect(item).toBeVisible();
        } else {
            await expect(
                page.getByRole('menuitem', {
                    name: target.group,
                    exact: true,
                }),
            ).toBeVisible();
        }
        return;
    }
    await expect(item).toHaveCount(0);
};

const projectMenuMatrix: Record<
    ProjectRole,
    {
        automation: boolean;
        agentControl: boolean;
        collaboration: boolean;
        knowledge: boolean;
        create: boolean;
        edit: boolean;
        delete: boolean;
    }
> = {
    project_admin: {
        automation: true,
        agentControl: true,
        collaboration: true,
        knowledge: true,
        create: true,
        edit: true,
        delete: true,
    },
    manager: {
        automation: true,
        agentControl: false,
        collaboration: true,
        knowledge: true,
        create: true,
        edit: true,
        delete: true,
    },
    agent: {
        automation: false,
        agentControl: false,
        collaboration: true,
        knowledge: true,
        create: true,
        edit: true,
        delete: false,
    },
    requester: {
        automation: false,
        agentControl: false,
        collaboration: true,
        knowledge: true,
        create: true,
        edit: true,
        delete: false,
    },
    observer: {
        automation: false,
        agentControl: false,
        collaboration: true,
        knowledge: true,
        create: false,
        edit: false,
        delete: false,
    },
};

test.describe('平台职责与项目 Membership 入口隔离', () => {
    const noMembershipCapabilities: Record<
        PlatformRole,
        {
            landing: string;
            platformUsers: boolean;
            systemSettings: boolean;
            platformAudit: boolean;
        }
    > = {
        platform_admin: {
            landing: '平台工作台',
            platformUsers: true,
            systemSettings: true,
            platformAudit: true,
        },
        security_auditor: {
            landing: '平台工作台',
            platformUsers: false,
            systemSettings: false,
            platformAudit: true,
        },
        emergency_operator: {
            landing: '平台工作台',
            platformUsers: false,
            systemSettings: false,
            platformAudit: false,
        },
        member: {
            landing: '暂无授权项目',
            platformUsers: false,
            systemSettings: false,
            platformAudit: false,
        },
    };
    const noMembershipCases = platformRoleValues.map((platformRole) => ({
        platformRole,
        ...noMembershipCapabilities[platformRole],
    }));

    for (const roleCase of noMembershipCases) {
        test(`${roleCase.platformRole} 无 Membership 不产生项目作用域请求`, async ({
            page,
        }) => {
            const identity: SessionIdentity = {
                ...defaultIdentity,
                subject: String(defaultIdentity.id),
                sessionID: `session-${roleCase.platformRole}`,
                email: `${roleCase.platformRole}@example.test`,
                platformRole: roleCase.platformRole,
            };
            await installSession(page, identity);
            const backend = await mockBackend(page);

            await page.goto('/#/');
            await expect(
                page.getByTestId(
                    roleCase.platformRole === 'member'
                        ? 'no-authorized-projects'
                        : 'platform-home',
                ),
            ).toBeVisible();
            await expect(
                page.getByTestId(
                    roleCase.platformRole === 'member'
                        ? 'no-authorized-projects'
                        : 'platform-home',
                ).getByRole('heading', {
                    name: roleCase.landing,
                    exact: true,
                }),
            ).toBeVisible();

            await expectMenuItem(
                page,
                '平台用户管理',
                roleCase.platformUsers,
            );
            await expectMenuItem(
                page,
                '系统设置',
                roleCase.systemSettings,
            );
            await expectMenuItem(
                page,
                '平台审计',
                roleCase.platformAudit,
            );
            await expectMenuItem(page, '工单管理', false);
            await expectMenuItem(page, '通知中心', false);
            await expectMenuItem(page, '自动化规则', false);
            await expectMenuItem(page, 'AI 智能体控制', false);
            await expectMenuItem(page, '人机协作', false);
            await expectMenuItem(page, '知识管理', false);

            await page.goto('/#/tickets');
            await expect(
                page.getByTestId(
                    roleCase.platformRole === 'member'
                        ? 'no-authorized-projects'
                        : 'platform-home',
                ),
            ).toBeVisible();
            await expect(
                page.getByTestId(
                    roleCase.platformRole === 'member'
                        ? 'no-authorized-projects'
                        : 'platform-home',
                ).getByRole('heading', {
                    name: roleCase.landing,
                    exact: true,
                }),
            ).toBeVisible();
            expect(backend.scopedProjectRequests).toEqual([]);
        });
    }

    test('member + project_admin 只有项目能力，不获得平台治理能力', async ({
        page,
    }) => {
        await installSession(page, defaultIdentity, project);
        const backend = await mockBackend(page, [
            projectAccess(project, 'project_admin'),
        ]);

        await page.goto('/#/');
        await expect(page.getByTestId('project-home')).toBeVisible();
        await expectMenuItem(page, '工单管理', true);
        await expectMenuItem(page, '通知中心', true);
        await expectMenuItem(page, '自动化规则', true);
        await expectMenuItem(page, 'Webhook 集成', true);
        await expectMenuItem(page, 'AI 智能体控制', true);
        await expectMenuItem(page, '人机协作', true);
        await expectMenuItem(page, '知识管理', true);
        await expectMenuItem(page, '平台用户管理', false);
        await expectMenuItem(page, '系统设置', false);
        await expectMenuItem(page, '平台审计', false);

        await page.goto('/#/users');
        await expectMenuItem(page, '工单管理', true);
        expect(
            backend.platformRequests.filter((request) =>
                request.includes('/api/platform/users'),
            ),
        ).toEqual([]);
    });

    test('security_auditor 仅能读取平台审计，直接访问平台写入口在请求前被拒绝', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-security-auditor',
            email: 'security-auditor@example.test',
            platformRole: 'security_auditor',
        };
        await installSession(page, identity);
        const backend = await mockBackend(page);

        await page.goto('/#/');
        const governance = page.getByRole('menuitem', {
            name: /^治理中心/u,
        });
        if ((await governance.getAttribute('aria-expanded')) !== 'true') {
            await governance.click();
        }
        await page.getByRole('menuitem', {
            name: '审计中心',
            exact: true,
        }).click();
        await expect(page.getByTestId('platform-audit-page')).toBeVisible();
        await expect(
            page.getByRole('heading', {
                name: '平台审计探索器',
                exact: true,
            }),
        ).toBeVisible();
        await expect.poll(() =>
            backend.platformRequests.some((request) =>
                request.startsWith('GET /api/platform/audit-logs'),
            ),
        ).toBe(true);
        expect(
            backend.platformRequests.filter((request) =>
                !request.startsWith('GET /api/platform/audit-logs'),
            ),
        ).toEqual([]);

        await page.goto('/#/users');
        await expect(page.getByTestId('platform-home')).toBeVisible();
        await expect(
            page.getByTestId('platform-home').getByRole('heading', {
                name: '平台工作台',
                exact: true,
            }),
        ).toBeVisible();
        expect(
            backend.platformRequests.filter((request) =>
                request.includes('/api/platform/users'),
            ),
        ).toEqual([]);
    });

    test('平台审计展示加载、错误重试与空状态且不会越权请求', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-audit-states',
            email: 'audit-states@example.test',
            platformRole: 'security_auditor',
        };
        await installSession(page, identity);
        let releaseFirstRequest = () => {};
        const firstRequestGate = new Promise<void>((resolve) => {
            releaseFirstRequest = resolve;
        });
        let listCalls = 0;
        const backend = await mockBackend(
            page,
            [],
            new Map(),
            async (route, url) => {
                if (url.pathname !== '/api/platform/audit-logs') {
                    return false;
                }
                listCalls += 1;
                if (listCalls <= 2) {
                    await firstRequestGate;
                    await fulfillJSON(
                        route,
                        { code: 1, msg: '平台审计暂时不可用' },
                        500,
                    );
                    return true;
                }
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: [],
                        next_cursor: '',
                        has_more: false,
                    },
                });
                return true;
            },
        );

        await page.goto('/#/platform/audit');
        await expect(
            page.getByRole('status', {
                name: '正在加载平台审计记录',
            }),
        ).toBeVisible();
        releaseFirstRequest();
        await expect(
            page.getByText('平台审计暂时不可用', { exact: true }),
        ).toBeVisible();
        await page.getByRole('button', { name: '重试', exact: true }).click();
        await expect(
            page.getByText('暂无符合条件的平台审计记录', {
                exact: true,
            }),
        ).toBeVisible();
        expect(listCalls).toBeGreaterThanOrEqual(3);
        expect(
            backend.platformRequests.filter((request) =>
                !request.startsWith('GET /api/platform/audit-logs'),
            ),
        ).toEqual([]);
    });

    test('平台审计支持键盘打开详情抽屉并使用游标翻页', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-audit-keyboard-pagination',
            email: 'audit-keyboard@example.test',
            platformRole: 'security_auditor',
        };
        await installSession(page, identity);
        const listQueries: string[] = [];
        const auditItem = (
            id: number,
            username: string,
        ) => ({
            id,
            created_at: `2026-07-31T08:00:0${id}Z`,
            username,
            platform_role: 'security_auditor',
            action: 'platform.user.update',
            action_code: 'platform.user.update',
            resource_type: 'user',
            resource_public_id: String(id),
            method: 'PUT',
            path: `/api/platform/users/${id}`,
            status_code: 200,
            masked_ip: '192.0.*.*',
            latency_ms: 12,
            result: 'success',
        });
        await mockBackend(
            page,
            [],
            new Map(),
            async (route, url) => {
                if (url.pathname === '/api/platform/audit-logs') {
                    listQueries.push(url.search);
                    const cursor = url.searchParams.get('cursor');
                    await fulfillJSON(route, {
                        code: 0,
                        data: {
                            items: cursor
                                ? [auditItem(2, 'Bob')]
                                : [auditItem(1, 'Alice')],
                            next_cursor: cursor ? '' : 'cursor-page-2',
                            has_more: !cursor,
                        },
                    });
                    return true;
                }
                if (url.pathname === '/api/platform/audit-logs/1') {
                    await fulfillJSON(route, {
                        code: 0,
                        data: {
                            ...auditItem(1, 'Alice'),
                            query: 'view=compact',
                            user_agent: 'browser',
                            notes: '',
                            request_id: 'request-1',
                        },
                    });
                    return true;
                }
                return false;
            },
        );

        await page.goto('/#/platform/audit');
        const firstRow = page.getByRole('button', {
            name: /查看 Alice/u,
        });
        await expect(firstRow).toBeVisible();
        await firstRow.focus();
        await firstRow.press('Enter');
        await expect(page.getByLabel('平台审计详情')).toBeVisible();
        await expect(
            page.getByText('view=compact', { exact: true }),
        ).toBeVisible();
        await page.getByRole('button', { name: '关闭', exact: true }).click();

        await page
            .getByRole('button', { name: '下一页', exact: true })
            .click();
        await expect(
            page.getByRole('button', { name: /查看 Bob/u }),
        ).toBeVisible();
        expect(
            listQueries.filter((query) => query === '?limit=25').length,
        ).toBeGreaterThanOrEqual(1);
        expect(listQueries[listQueries.length - 1]).toBe(
            '?limit=25&cursor=cursor-page-2',
        );
        await expect(
            page.getByRole('button', { name: '上一页', exact: true }),
        ).toBeEnabled();
    });

    test('平台审计异步导出显式时间范围、轮询并在完整响应后下载', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-audit-export',
            email: 'audit-export@example.test',
            platformRole: 'security_auditor',
        };
        await installSession(page, identity);
        const publicID = '0198a342-7386-7dc2-9de3-8d91b47509c2';
        const exportRequests: string[] = [];
        let statusCalls = 0;
        await mockBackend(
            page,
            [],
            new Map(),
            async (route, url) => {
                const request = route.request();
                if (url.pathname === '/api/platform/audit-logs') {
                    await fulfillJSON(route, {
                        code: 0,
                        data: {
                            items: [],
                            total: 0,
                            page: 1,
                            limit: 25,
                        },
                    });
                    return true;
                }
                if (
                    request.method() === 'POST' &&
                    url.pathname === '/api/platform/audit-exports'
                ) {
                    exportRequests.push(url.search);
                    await fulfillJSON(
                        route,
                        {
                            code: 0,
                            data: {
                                public_id: publicID,
                                state: 'queued',
                                requested_at: contractTimestamp,
                                row_count: 0,
                                truncated: false,
                                size_bytes: 0,
                            },
                        },
                        202,
                    );
                    return true;
                }
                if (
                    request.method() === 'GET' &&
                    url.pathname ===
                        `/api/platform/audit-exports/${publicID}`
                ) {
                    statusCalls += 1;
                    await fulfillJSON(route, {
                        code: 0,
                        data: {
                            public_id: publicID,
                            state:
                                statusCalls === 1
                                    ? 'processing'
                                    : 'completed',
                            requested_at: contractTimestamp,
                            started_at: contractTimestamp,
                            completed_at:
                                statusCalls > 1
                                    ? '2026-07-31T08:01:00Z'
                                    : undefined,
                            expires_at:
                                statusCalls > 1
                                    ? '2026-08-01T08:01:00Z'
                                    : undefined,
                            row_count: statusCalls > 1 ? 27 : 0,
                            truncated: false,
                            sha256:
                                statusCalls > 1
                                    ? 'a'.repeat(64)
                                    : undefined,
                            size_bytes: statusCalls > 1 ? 128 : 0,
                        },
                    });
                    return true;
                }
                if (
                    request.method() === 'GET' &&
                    url.pathname ===
                        `/api/platform/audit-exports/${publicID}/download`
                ) {
                    await route.fulfill({
                        status: 200,
                        contentType: 'text/csv; charset=utf-8',
                        headers: {
                            'Content-Disposition':
                                `attachment; filename="chronodesk-audit-${publicID}.csv"`,
                        },
                        body: 'time,actor\n2026-07-31T08:00:00Z,审计员\n',
                    });
                    return true;
                }
                return false;
            },
        );

        await page.goto('/#/platform/audit?time_preset=7d');
        await page
            .getByRole('button', { name: '导出 CSV', exact: true })
            .click();
        await expect(
            page.getByRole('dialog', { name: '导出脱敏审计 CSV' }),
        ).toBeVisible();
        await page
            .getByRole('button', { name: '创建导出任务', exact: true })
            .click();
        await expect(
            page.getByText('正在生成脱敏 CSV…', { exact: true }),
        ).toBeVisible();
        await expect(
            page.getByText(/已生成 27 条/u),
        ).toBeVisible({ timeout: 8_000 });
        expect(exportRequests).toHaveLength(1);
        const exported = new URLSearchParams(exportRequests[0]);
        expect(exported.has('start_time')).toBe(true);
        expect(exported.has('end_time')).toBe(true);
        expect(exported.has('time_preset')).toBe(false);
        expect(exported.has('cursor')).toBe(false);
        expect(exported.has('limit')).toBe(false);

        const downloadPromise = page.waitForEvent('download');
        await page
            .getByRole('button', { name: '下载 CSV', exact: true })
            .click();
        const download = await downloadPromise;
        expect(download.suggestedFilename()).toBe(
            `chronodesk-audit-${publicID}.csv`,
        );
        await expect(
            page.getByText(
                '文件响应已完整接收，并已交给浏览器下载。',
                { exact: true },
            ),
        ).toBeVisible();
    });

    test('平台审计拒绝非法 URL 筛选且不发起扩大查询', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-audit-invalid-url',
            email: 'audit-invalid-url@example.test',
            platformRole: 'security_auditor',
        };
        await installSession(page, identity);
        const backend = await mockBackend(page);
        for (const testCase of [
            {
                query: 'platform_role=administrator&limit=500',
                message: /平台角色、每页数量/u,
            },
            {
                query: 'role=admin',
                message: /未知参数 role/u,
            },
            {
                query:
                    'platform_role=member&platform_role=security_auditor',
                message: /重复参数 platform_role/u,
            },
            {
                query:
                    'time_preset=24h&start_time=2026-07-31T00%3A00%3A00Z',
                message: /时间预设与自定义时间范围不能同时使用/u,
            },
        ]) {
            await page.goto(`/#/platform/audit?${testCase.query}`);
            await expect(page.getByText(testCase.message)).toBeVisible();
        }
        expect(
            backend.platformRequests.filter((request) =>
                request.includes('/api/platform/audit-logs'),
            ),
        ).toEqual([]);
        await expect(
            page.getByRole('button', {
                name: '清除无效筛选',
                exact: true,
            }),
        ).toBeVisible();
    });

    test('emergency_operator 不继承未声明的平台或项目入口', async ({
        page,
    }) => {
        const identity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-emergency-operator',
            email: 'emergency-operator@example.test',
            platformRole: 'emergency_operator',
        };
        await installSession(page, identity);
        const backend = await mockBackend(page);

        for (const path of [
            '/#/users',
            '/#/system-settings',
            '/#/tickets',
            '/#/agent-control',
        ]) {
            await page.goto(path);
            await expect(page.getByTestId('platform-home')).toBeVisible();
            await expect(
                page.getByTestId('platform-home').getByRole('heading', {
                    name: '平台工作台',
                    exact: true,
                }),
            ).toBeVisible();
        }

        expect(backend.platformRequests).toEqual([]);
        expect(backend.scopedProjectRequests).toEqual([]);
    });
});

test.describe('项目角色菜单与工单按钮矩阵', () => {
    for (const [projectRole, expected] of Object.entries(
        projectMenuMatrix,
    ) as Array<[ProjectRole, (typeof projectMenuMatrix)[ProjectRole]]>) {
        test(`${projectRole} 使用精确 ProjectRole 能力`, async ({ page }) => {
            await installSession(page, defaultIdentity, project);
            const backend = await mockBackend(page, [
                projectAccess(project, projectRole),
            ]);

            await page.goto('/#/');
            await expect(page.getByTestId('project-home')).toBeVisible();
            await expect.poll(() =>
                backend.scopedProjectRequests.some((request) =>
                    request.includes('/tickets/stats'),
                ),
            ).toBe(true);
            if (projectRole === 'agent') {
                await expect.poll(() =>
                    backend.scopedProjectRequests.some((request) =>
                        request.includes('/tickets/my-tickets'),
                    ),
                ).toBe(true);
            }
            await expectMenuItem(page, '工单管理', true);
            await expectMenuItem(page, '通知中心', true);
            await expectMenuItem(
                page,
                '自动化规则',
                expected.automation,
            );
            await expectMenuItem(
                page,
                '自动化日志',
                expected.automation,
            );
            await expectMenuItem(
                page,
                'Webhook 集成',
                expected.automation,
            );
            await expectMenuItem(
                page,
                'AI 智能体控制',
                expected.agentControl,
            );
            await expectMenuItem(
                page,
                '人机协作',
                expected.collaboration,
            );
            await expectMenuItem(
                page,
                '知识管理',
                expected.knowledge,
            );
            await expectMenuItem(page, '平台用户管理', false);
            await expectMenuItem(page, '系统设置', false);

            await page
                .getByRole('menuitem', {
                    name: '工单管理',
                    exact: true,
                })
                .click();
            const main = page.getByRole('main');
            const ticketRow = main
                .getByRole('row')
                .filter({ hasText: mockTicket.title });
            await expect(ticketRow).toBeVisible();

            const createButton = main.getByRole('link', {
                name: '创建工单',
                exact: true,
            });
            if (expected.create) {
                await expect(createButton.first()).toBeVisible();
            } else {
                await expect(createButton).toHaveCount(0);
            }

            const editButton = ticketRow.getByRole('link', {
                name: '编辑',
                exact: true,
            });
            if (expected.edit) {
                await expect(editButton).toBeVisible();
            } else {
                await expect(editButton).toHaveCount(0);
            }

            await ticketRow
                .getByRole('link', { name: '查看', exact: true })
                .click();
            await expect(
                main.getByRole('heading', { name: mockTicket.title }),
            ).toBeVisible();

            const deleteButton = main.getByRole('button', {
                name: '删除',
                exact: true,
            });
            if (expected.delete) {
                await expect(deleteButton).toBeVisible();
            } else {
                await expect(deleteButton).toHaveCount(0);
            }

            await expect.poll(() =>
                backend.scopedProjectRequests.some((request) =>
                    request.includes('/api/projects/OPS/tickets'),
                ),
            ).toBe(true);
            const requestedMyTickets =
                backend.scopedProjectRequests.some((request) =>
                    request.includes('/tickets/my-tickets'),
                );
            expect(requestedMyTickets).toBe(projectRole === 'agent');
        });
    }
});

test.describe('项目授权缓存与撤销', () => {
    test('同一浏览器连续登录两个无交集项目账号不泄漏旧项目缓存', async ({
        page,
    }) => {
        const firstIdentity: SessionIdentity = {
            ...defaultIdentity,
            subject: String(defaultIdentity.id),
            sessionID: 'session-account-ops',
            email: 'account-ops@example.test',
        };
        const secondIdentity: SessionIdentity = {
            ...defaultIdentity,
            id: 43,
            subject: '43',
            sessionID: 'session-account-fin',
            email: 'account-fin@example.test',
        };
        const firstToken = sessionToken(firstIdentity);
        const secondToken = sessionToken(secondIdentity);
        const accessesByToken = new Map<string, ProjectAccess[]>([
            [firstToken, [projectAccess(project, 'agent')]],
            [
                secondToken,
                [projectAccess(financeProject, 'requester')],
            ],
        ]);
        const backend = await mockBackend(page, [], accessesByToken);
        const sessions = new Map([
            [firstIdentity.email, { identity: firstIdentity, token: firstToken }],
            [secondIdentity.email, { identity: secondIdentity, token: secondToken }],
        ]);
        await page.route('**/api/auth/login', async (route) => {
            const body = route.request().postDataJSON() as {
                email?: unknown;
            };
            const session =
                typeof body.email === 'string'
                    ? sessions.get(body.email)
                    : undefined;
            if (!session) {
                await fulfillJSON(
                    route,
                    {
                        code: 1,
                        message: '测试账号不存在',
                    },
                    401,
                );
                return;
            }
            await fulfillJSON(route, {
                code: 0,
                data: {
                    access_token: session.token,
                    expires_in: 3600,
                    token_type: 'Bearer',
                    user: {
                        id: session.identity.id,
                        username: `e2e-${session.identity.id}`,
                        email: session.identity.email,
                        platform_role: session.identity.platformRole,
                        status: 'active',
                        email_verified: true,
                        otp_enabled: false,
                    },
                },
            });
        });

        const login = async (identity: SessionIdentity) => {
            await page.getByLabel('邮箱').fill(identity.email);
            await page.getByLabel('密码').fill('E2E-only-password');
            await page
                .getByRole('button', { name: '登录系统', exact: true })
                .click();
        };

        await page.goto('/#/login');
        await login(firstIdentity);
        await expect(
            page.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toBeVisible();
        await page
            .getByRole('button', {
                name: new RegExp(project.name, 'u'),
            })
            .click();
        await expect(page.getByTestId('active-project-switcher')).toContainText(
            project.name,
        );
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = sessionStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!raw) return null;
                return (JSON.parse(raw) as { project_key?: unknown })
                    .project_key;
            }),
        ).toBe(project.key);

        await page
            .getByRole('button', { name: '账号', exact: true })
            .click();
        await page.getByTestId('logout-current-session').click();
        await expect(page.getByLabel('邮箱')).toBeVisible();
        await expect.poll(() =>
            page.evaluate(() =>
                sessionStorage.getItem('chronodesk.activeProject'),
            ),
        ).toBeNull();

        // 即使登录页被扩展或旧浏览器状态重新注入了上一主体的项目记录，
        // 下一次登录也必须在读取项目列表前清除它。
        await page.evaluate((staleProject) => {
            localStorage.setItem(
                'chronodesk.activeProject',
                JSON.stringify(staleProject),
            );
        }, {
            subject: firstIdentity.subject,
            session_id: firstIdentity.sessionID,
            project_key: project.key,
        });

        const scopedRequestCountBeforeSecondLogin =
            backend.scopedProjectRequests.length;
        await login(secondIdentity);
        await expect(
            page.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toBeVisible();
        await page
            .getByRole('button', {
                name: new RegExp(financeProject.name, 'u'),
            })
            .click();
        await expect(page.getByTestId('active-project-switcher')).toContainText(
            financeProject.name,
        );
        await expect(page.getByText(project.name, { exact: true })).toHaveCount(
            0,
        );
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = sessionStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!raw) return null;
                return JSON.parse(raw) as Record<string, unknown>;
            }),
        ).toEqual({
            subject: secondIdentity.subject,
            session_id: secondIdentity.sessionID,
            project_key: financeProject.key,
            epoch: 1,
        });
        expect(
            backend.scopedProjectRequests
                .slice(scopedRequestCountBeforeSecondLogin)
                .some((request) =>
                    request.includes('/api/projects/OPS/'),
                ),
        ).toBe(false);
    });

    test('切换项目后只请求新项目且保存主体绑定的 active project', async ({
        page,
    }) => {
        await installSession(page, defaultIdentity, project);
        const backend = await mockBackend(page, [
            projectAccess(project, 'agent'),
            projectAccess(financeProject, 'project_admin'),
        ]);

        await page.goto('/#/');
        await page.getByTestId('active-project-switcher').click();
        await page
            .getByRole('option', {
                name: '财务服务 · 项目管理员',
                exact: true,
            })
            .click();

        await expect.poll(() =>
            page.evaluate(() => {
                const raw = sessionStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!raw) return null;
                try {
                    return JSON.parse(raw) as Record<string, unknown>;
                } catch {
                    return null;
                }
            }),
        ).toEqual({
            subject: defaultIdentity.subject,
            session_id: defaultIdentity.sessionID,
            project_key: financeProject.key,
            epoch: 2,
        });
        await expect.poll(() =>
            backend.scopedProjectRequests.some((request) =>
                request.includes('/api/projects/FIN/tickets'),
            ),
        ).toBe(true);
    });

    test('Membership 撤销后旧页请求收到 403，项目入口随缓存失效消失', async ({
        page,
    }) => {
        await installSession(page, defaultIdentity, project);
        const backend = await mockBackend(page, [
            projectAccess(project, 'project_admin'),
        ]);

        await page.goto('/#/agent-control');
        await expect(
            page.getByRole('main').getByRole('heading', {
                name: 'AI 智能体控制中心',
                exact: true,
            }),
        ).toBeVisible();

        await expect.poll(() => {
            const requests = backend.scopedProjectRequests;
            return requests.some((request) =>
                request.includes(
                    '/api/projects/OPS/admin/agents/agent-control/overview',
                ),
            ) && requests.some((request) =>
                request.includes(
                    '/api/projects/OPS/admin/agents/service-principals',
                ),
            );
        }).toBe(true);
        const refreshControl = page
            .getByRole('main')
            .getByRole('button', {
                name: '刷新控制面',
                exact: true,
            });
        await expect(refreshControl).toBeVisible();
        await expect(refreshControl).toBeEnabled();
        const deniedResponse = page.waitForResponse((response) =>
            response.status() === 403 &&
            new URL(response.url()).pathname.startsWith(
                '/api/projects/OPS/',
            ),
        );
        backend.accesses = [];
        // 403 会立即撤销当前项目并卸载整页；直接派发已验证可操作按钮的
        // click，避免 Playwright 在成功卸载后仍等待旧 DOM 节点稳定。
        await refreshControl.dispatchEvent('click');

        expect(
            (await (await deniedResponse).json() as { code?: unknown }).code,
        ).toBe('project_access_revoked');
        await expect.poll(
            () => backend.deniedProjectRequests.length,
        ).toBeGreaterThanOrEqual(1);
        expect(
            backend.deniedProjectRequests.every((request) =>
                request.includes('/api/projects/OPS/admin/agents/'),
            ),
        ).toBe(true);
        await expect(
            page.getByTestId('no-authorized-projects'),
        ).toBeVisible();
        await expectMenuItem(page, 'AI 智能体控制', false);
        await expectMenuItem(page, '工单管理', false);
        await expect.poll(() => backend.projectListRequests.length).toBeGreaterThan(
            1,
        );
    });

    test('数据 Provider 收到普通对象级 403 时保留当前项目', async ({
        page,
    }) => {
        await installSession(page, defaultIdentity, project);
        const backend = await mockBackend(page, [
            projectAccess(project, 'project_admin'),
        ]);
        const ticketsPath = '/api/projects/OPS/tickets';
        backend.forbiddenCodes.set(ticketsPath, 'ticket_access_denied');

        const forbiddenResponse = page.waitForResponse((response) =>
            response.status() === 403 &&
            new URL(response.url()).pathname === ticketsPath,
        );
        await page.goto('/#/tickets');
        expect(
            (await (await forbiddenResponse).json() as { code?: unknown }).code,
        ).toBe('ticket_access_denied');

        await expect.poll(() =>
            page.evaluate(() => {
                const raw = sessionStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!raw) return null;
                return (JSON.parse(raw) as { project_key?: unknown })
                    .project_key;
            }),
        ).toBe(project.key);
        await expect(
            page.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toHaveCount(0);
    });

    test('知识能力 403 后软刷新 ProjectAccess 且保留当前项目', async ({
        page,
    }) => {
        await installSession(page, defaultIdentity, project);
        const backend = await mockBackend(page, [
            projectAccess(project, 'agent', false),
        ]);
        const knowledgeArticlesPath =
            '/api/projects/OPS/knowledge/articles';

        await page.goto('/#/knowledge');
        await expect(
            page.getByRole('button', {
                name: '沉淀知识',
                exact: true,
            }),
        ).toHaveCount(0);
        const projectListRequestCount = backend.projectListRequests.length;

        backend.accesses = [projectAccess(project, 'agent', true)];
        backend.forbiddenCodes.set(
            knowledgeArticlesPath,
            'knowledge_contributor_required',
        );
        const forbiddenResponse = page.waitForResponse((response) =>
            response.status() === 403 &&
            new URL(response.url()).pathname === knowledgeArticlesPath,
        );
        await page
            .getByRole('textbox', { name: '筛选知识文章', exact: true })
            .fill('触发权限刷新');
        await forbiddenResponse;
        backend.forbiddenCodes.delete(knowledgeArticlesPath);

        await expect.poll(
            () => backend.projectListRequests.length,
        ).toBeGreaterThan(projectListRequestCount);
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = sessionStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!raw) return null;
                return (JSON.parse(raw) as { project_key?: unknown })
                    .project_key;
            }),
        ).toBe(project.key);
        await expect(
            page.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('button', {
                name: '沉淀知识',
                exact: true,
            }),
        ).toBeVisible();
    });
});
