import { expect, test } from '@playwright/test';
import {
    authenticatePage,
    captureAgentGlobalControls,
    cleanupTrackedAgentPrincipals,
    E2E_MARKER,
    extractData,
    restoreAgentGlobalControls,
    trackE2EResource,
    type AgentGlobalControlSnapshot,
} from './helpers/testData';
import {
    assertDestructiveE2EAllowed,
} from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const principalName = `${E2E_MARKER}工单协作智能体`;
const principalRowName = new RegExp(
    principalName.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&'),
    'u',
);
const credentialGuardTestTitle =
    'AGT-014：一次性凭据写入期间锁定项目切换且保存后自动解锁';

const installAgentControlMockSession = async (
    page: import('@playwright/test').Page,
) => {
    const encode = (value: unknown) =>
        Buffer.from(JSON.stringify(value)).toString('base64url');
    const expiresAt = Math.floor(Date.now() / 1000) + 3600;
    const token = `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
        sub: '7',
        sid: 'agent-control-mock-session',
        platform_role: 'platform_admin',
        exp: expiresAt,
    })}.signature`;
    const user = {
        id: 7,
        username: 'agent-control-reviewer',
        email: 'agent-control@example.invalid',
        platform_role: 'platform_admin',
        status: 'active',
        email_verified: true,
        otp_enabled: false,
    };
    const projects = [
        {
            id: 71,
            public_id: '00000000-0000-7000-8000-000000000071',
            created_at: '2026-08-01T00:00:00Z',
            updated_at: '2026-08-01T00:00:00Z',
            organization_id: 1,
            business_unit_id: 1,
            key: 'WRITE-ORIGINAL',
            name: '原凭据项目',
            description: '',
            status: 'active',
        },
        {
            id: 72,
            public_id: '00000000-0000-7000-8000-000000000072',
            created_at: '2026-08-01T00:00:00Z',
            updated_at: '2026-08-01T00:00:00Z',
            organization_id: 1,
            business_unit_id: 1,
            key: 'WRITE-SWITCHED',
            name: '切换后项目',
            description: '',
            status: 'active',
        },
    ];
    await page.addInitScript(({ accessToken, exp, sessionUser }) => {
        localStorage.setItem('token', accessToken);
        localStorage.setItem('refreshToken', 'agent-control-mock-refresh');
        localStorage.setItem('tokenExpiresAt', String(exp * 1000));
        localStorage.setItem('user', JSON.stringify(sessionUser));
        localStorage.setItem(
            'chronodesk.activeProject',
            JSON.stringify({
                subject: '7',
                session_id: 'agent-control-mock-session',
                project_key: 'WRITE-ORIGINAL',
            }),
        );
    }, { accessToken: token, exp: expiresAt, sessionUser: user });
    await page.route('**/api/auth/me', async (route) => {
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({ code: 0, msg: 'ok', data: user }),
        });
    });
    await page.route('**/api/projects*', async (route) => {
        const url = new URL(route.request().url());
        if (route.request().method() !== 'GET' || url.pathname !== '/api/projects') {
            await route.fallback();
            return;
        }
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
                code: 0,
                msg: 'ok',
                data: projects.map((project) => ({
                    project,
                    project_role: 'project_admin',
                    scope: {
                        organization_id: project.organization_id,
                        project_id: project.id,
                    },
                })),
            }),
        });
    });
};

test.describe('AI 智能体控制中心', () => {
    test.describe.configure({ mode: 'serial' });
    let globalControlsBeforeTest: AgentGlobalControlSnapshot | undefined;
    let usedRealBackend = false;

    test.beforeEach(async ({ request }, testInfo) => {
        assertDestructiveE2EAllowed('Agent 控制中心写操作 E2E');
        if (testInfo.title === credentialGuardTestTitle) return;
        usedRealBackend = true;
        globalControlsBeforeTest = await captureAgentGlobalControls(request);
    });

    test.afterEach(async ({ request }, testInfo) => {
        if (
            testInfo.title === credentialGuardTestTitle
            || !globalControlsBeforeTest
        ) return;
        await restoreAgentGlobalControls(request, globalControlsBeforeTest);
        globalControlsBeforeTest = undefined;
    });

    test.afterAll(async ({ request }) => {
        if (!usedRealBackend) return;
        await cleanupTrackedAgentPrincipals(request);
    });

    test('AGT-009 UI-020 UI-021：页面完整且平台级安全开关只读展示', async ({
        page,
    }) => {
        await authenticatePage(page);
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('heading', {
                name: 'AI 智能体控制中心',
                exact: true,
            }),
        ).toBeVisible({ timeout: 15_000 });

        for (const helper of [
            '服务端统计的有效项目授权',
            '服务端统计的活跃租约',
        ] as const) {
            await expect(
                main.getByText(helper, { exact: true }),
            ).toBeVisible();
        }

        const tabs = [
            ['服务主体', '服务主体列表'],
            ['实时租约', '工单租约列表'],
            ['附件扫描', '附件扫描列表'],
            ['策略审计', '智能体策略决策审计'],
        ] as const;
        for (const [tab, table] of tabs) {
            await main.getByRole('tab', { name: tab, exact: true }).click();
            await expect(
                main.getByRole('table', { name: table, exact: true }),
            ).toBeVisible();
        }

        const readOnly = main.getByLabel('智能体全局只读模式', {
            exact: true,
        });
        await expect(readOnly).toBeChecked({
            checked: globalControlsBeforeTest.global_read_only,
        });
        await expect(readOnly).toBeDisabled();
        const emergencyStop = main.getByLabel('智能体全局紧急停止', {
            exact: true,
        });
        await expect(emergencyStop).toBeChecked({
            checked: globalControlsBeforeTest.emergency_stop,
        });
        await expect(emergencyStop).toBeDisabled();
        await expect(
            main.getByText(
                '全局只读和紧急停止属于平台级安全控制，本项目页面仅展示状态；变更入口已与项目业务操作隔离。',
                { exact: true },
            ),
        ).toBeVisible();
        await expectChineseOperations(page);
    });

    test('AGT-010：各标签只请求自己的严格分页或游标端点', async ({
        page,
    }) => {
        await authenticatePage(page);
        const requests: URL[] = [];
        page.on('request', (request) => {
            const url = new URL(request.url());
            if (
                request.method() === 'GET'
                && url.pathname.includes('/admin/agents/')
            ) {
                requests.push(url);
            }
        });
        const overviewResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith(
                    '/admin/agents/agent-control/overview',
                );
        });
        const principalResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith(
                    '/admin/agents/service-principals',
                );
        });
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('table', { name: '服务主体列表', exact: true }),
        ).toBeVisible({ timeout: 15_000 });

        expect((await overviewResponse).status()).toBe(200);
        const principalURL = new URL((await principalResponse).url());
        expect(principalURL.searchParams.get('page')).toBe('1');
        expect(principalURL.searchParams.get('page_size')).toBe('25');
        expect(principalURL.searchParams.get('sort_by')).toBe(
            'created_at',
        );
        expect(
            requests.some((url) => url.pathname.endsWith('/leases')),
        ).toBe(false);

        const leaseResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith('/admin/agents/leases');
        });
        await main.getByRole('tab', { name: '实时租约', exact: true }).click();
        const leaseURL = new URL((await leaseResponse).url());
        expect(leaseURL.searchParams.get('page')).toBe('1');
        expect(leaseURL.searchParams.get('page_size')).toBe('25');
        expect(leaseURL.searchParams.get('sort_by')).toBe('expires_at');
        expect(leaseURL.searchParams.get('sort_order')).toBe('asc');

        const decisionResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith('/admin/agents/policy-decisions');
        });
        await main.getByRole('tab', { name: '策略审计', exact: true }).click();
        const decisionURL = new URL((await decisionResponse).url());
        expect(decisionURL.searchParams.get('limit')).toBe('25');
        expect(decisionURL.searchParams.has('page')).toBe(false);
    });

    test('AGT-013：集成中心使用独立项目端点并请求真实事件与 Outbox 分页', async ({
        page,
    }) => {
        await authenticatePage(page);
        const integrationRequests: URL[] = [];
        page.on('request', (request) => {
            const url = new URL(request.url());
            if (
                request.method() === 'GET'
                && url.pathname.includes('/integrations/')
            ) {
                integrationRequests.push(url);
            }
        });
        await page.goto('/#/integration-runtime');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('heading', {
                name: '集成中心',
                exact: true,
            }),
        ).toBeVisible({ timeout: 15_000 });
        await expect(
            main.getByRole('table', { name: '连接实例列表', exact: true }),
        ).toBeVisible();

        const eventResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith('/integrations/domain-events');
        });
        await main.getByRole('tab', { name: '领域事件', exact: true }).click();
        const eventURL = new URL((await eventResponse).url());
        expect(eventURL.searchParams.get('limit')).toBe('25');
        expect(eventURL.searchParams.has('page')).toBe(false);
        await expect(
            main.getByRole('table', { name: '领域事件列表', exact: true }),
        ).toBeVisible();
        await expect(
            main.getByRole('tab', { name: '服务主体', exact: true }),
        ).toHaveCount(0);

        const outboxResponse = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return response.request().method() === 'GET'
                && url.pathname.endsWith('/integrations/outbox');
        });
        await main
            .getByRole('tab', {
                name: 'Outbox',
                exact: true,
            })
            .click();
        const outboxURL = new URL((await outboxResponse).url());
        expect(outboxURL.searchParams.get('page')).toBe('1');
        expect(outboxURL.searchParams.get('page_size')).toBe('25');
        expect(outboxURL.searchParams.get('sort_by')).toBe('created_at');
        expect(outboxURL.searchParams.get('sort_order')).toBe('desc');
        await expect(
            main.getByRole('table', { name: 'Outbox 投递列表', exact: true }),
        ).toBeVisible();
        expect(
            integrationRequests.some((url) =>
                url.pathname.endsWith('/service-principals'),
            ),
        ).toBe(false);
        expect(new URL(page.url()).hash).toBe('#/integration-runtime');
    });

    test('AGT-011：标签错误局部展示且可独立重试', async ({ page }) => {
        await authenticatePage(page);
        let leaseAttempts = 0;
        await page.route(
            '**/api/projects/*/admin/agents/leases?*',
            async (route) => {
                leaseAttempts += 1;
                if (leaseAttempts === 1) {
                    await route.fulfill({
                        status: 500,
                        contentType: 'application/problem+json',
                        body: JSON.stringify({
                            type: 'https://chronodesk.local/problems/internal_error',
                            title: 'internal error',
                            status: 500,
                            code: 'internal_error',
                            request_id: 'e2e-agent-list-error',
                            retryable: true,
                        }),
                    });
                    return;
                }
                await route.continue();
            },
        );
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await main.getByRole('tab', { name: '实时租约', exact: true }).click();
        await expect(
            main.getByText('服务暂时不可用，请稍后重试', { exact: true }),
        ).toBeVisible();
        const retryResponse = page.waitForResponse((response) =>
            response.request().method() === 'GET'
            && new URL(response.url()).pathname.endsWith(
                '/admin/agents/leases',
            ),
        );
        await main.getByRole('button', { name: '重试', exact: true }).click();
        expect((await retryResponse).status()).toBe(200);
        await expect(
            main.getByRole('table', { name: '工单租约列表', exact: true }),
        ).toBeVisible();
        expect(leaseAttempts).toBe(2);
    });

    test('AGT-012：项目切换取消旧请求并清空旧行', async ({ page }) => {
        await authenticatePage(page);
        let firstProjectKey = '';
        const projectAccess = (
            id: number,
            key: string,
            name: string,
        ) => ({
            project: {
                id,
                public_id: `00000000-0000-7000-8200-${String(id).padStart(12, '0')}`,
                created_at: '2026-07-31T00:00:00Z',
                updated_at: '2026-07-31T00:00:00Z',
                organization_id: 1,
                business_unit_id: 1,
                key,
                name,
                description: '',
                status: 'active',
            },
            project_role: 'project_admin',
            scope: {
                organization_id: 1,
                project_id: id,
            },
        });
        await page.route('**/api/projects?*', async (route) => {
            const url = new URL(route.request().url());
            if (
                route.request().method() !== 'GET'
                || url.pathname !== '/api/projects'
            ) {
                await route.fallback();
                return;
            }
            const items = [
                projectAccess(1, 'DEFAULT', '默认项目'),
                projectAccess(2, 'SWITCHED', '切换后项目'),
            ];
            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({
                    code: 0,
                    msg: 'ok',
                    data: {
                        items,
                        total: items.length,
                        page: 1,
                        page_size: 100,
                        total_pages: 1,
                    },
                }),
            });
        });
        await page.route(
            '**/api/projects/SWITCHED/tickets**',
            async (route) => {
                const url = new URL(route.request().url());
                if (route.request().method() !== 'GET') {
                    await route.fallback();
                    return;
                }
                const data = url.pathname.endsWith('/stats')
                    ? {
                        total: 0,
                        open: 0,
                        in_progress: 0,
                        pending: 0,
                        resolved: 0,
                        overdue: 0,
                        sla_breached: 0,
                        my_tickets: 0,
                        unassigned: 0,
                        high_priority: 0,
                        escalated: 0,
                    }
                    : {
                        items: [],
                        total: 0,
                        page: 1,
                        page_size: 10,
                        total_pages: 0,
                    };
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({ code: 0, msg: 'ok', data }),
                });
            },
        );
        const pageEnvelope = (name: string, idSuffix: string) => ({
            data: {
                items: [{
                    id: `00000000-0000-7000-8000-${idSuffix}`,
                    client_id: `00000000-0000-7000-8000-${idSuffix}`,
                    name,
                    description: '',
                    status: 'active',
                    scopes: ['tickets:read'],
                    rate_limit: 60,
                    concurrency_limit: 4,
                    last_used_at: null,
                    expires_at: null,
                    created_at: '2026-07-31T00:00:00Z',
                    read_only: false,
                    emergency_disabled: false,
                    resource_version: 1,
                    grant: {
                        id: 1,
                        project_id: 1,
                        role: 'agent',
                        scopes: ['tickets:read'],
                        is_active: true,
                        expires_at: null,
                        created_at: '2026-07-31T00:00:00Z',
                    },
                }],
                total: 1,
                page: 1,
                page_size: 25,
                total_pages: 1,
            },
            meta: { request_id: `e2e-${name}` },
        });
        await page.route(
            '**/api/projects/SWITCHED/admin/agents/agent-control/overview',
            async (route) => {
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({
                        data: {
                            global_read_only: false,
                            emergency_stop: false,
                            principal_count: 1,
                            active_principal_count: 1,
                            active_lease_count: 0,
                            failed_outbox_count: 0,
                            recent_event_count: 0,
                            pending_attachment_scan_count: 0,
                        },
                        meta: { request_id: 'e2e-switched-overview' },
                    }),
                });
            },
        );
        await page.route(
            '**/api/projects/*/admin/agents/service-principals?*',
            async (route) => {
                const segments = new URL(route.request().url()).pathname
                    .split('/')
                    .filter(Boolean);
                const key = decodeURIComponent(
                    segments[segments.indexOf('projects') + 1],
                );
                if (key === 'SWITCHED') {
                    await route.fulfill({
                        status: 200,
                        contentType: 'application/json',
                        body: JSON.stringify(
                            pageEnvelope('切换后智能体', '000000000002'),
                        ),
                    });
                    return;
                }
                firstProjectKey = key;
                await new Promise((resolve) => setTimeout(resolve, 600));
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify(
                        pageEnvelope('旧项目智能体', '000000000001'),
                    ),
                }).catch(() => undefined);
            },
        );
        await page.goto('/#/agent-control');
        await expect.poll(() => firstProjectKey).not.toBe('');
        await page.evaluate(() => {
            localStorage.removeItem('chronodesk.activeProject');
            window.dispatchEvent(new CustomEvent(
                'chronodesk:project-scope-changed',
                { detail: { project_key: null } },
            ));
        });
        const selection = page.getByTestId(
            'active-project-selection-required',
        );
        await expect(selection).toBeVisible();
        await selection.getByTestId('select-project-SWITCHED').click();
        await expect(page.getByTestId('project-home')).toBeVisible();
        const selectedProjectKey = () => page.evaluate(() => {
            const serialized = localStorage.getItem(
                'chronodesk.activeProject',
            );
            if (!serialized) return null;
            try {
                const selectionValue = JSON.parse(serialized) as {
                    project_key?: unknown;
                };
                return typeof selectionValue.project_key === 'string'
                    ? selectionValue.project_key
                    : null;
            } catch {
                return null;
            }
        });
        await expect.poll(selectedProjectKey).toBe('SWITCHED');
        await expect(
            page.getByTestId('project-switcher-loading'),
        ).toBeHidden();
        await expect(
            page.getByTestId('active-project-switcher'),
        ).toContainText('切换后项目');
        await page.goto('/#/agent-control');
        await expect.poll(selectedProjectKey).toBe('SWITCHED');
        const table = page.getByRole('table', {
            name: '服务主体列表',
            exact: true,
        });
        await expect(table.getByText('切换后智能体', { exact: true })).toBeVisible();
        await page.waitForTimeout(700);
        await expect(table.getByText('旧项目智能体', { exact: true })).toHaveCount(0);
    });

    test(credentialGuardTestTitle, async ({
        page,
    }) => {
        await installAgentControlMockSession(page);
        let originalProjectKey = '';
        let writeStarted = false;
        let switchedListLoaded = false;
        const principalPage = (name?: string) => ({
            data: {
                items: name ? [{
                    id: '00000000-0000-7000-8100-000000000001',
                    client_id: '00000000-0000-7000-8100-000000000001',
                    name,
                    description: '',
                    status: 'active',
                    scopes: ['tickets:read'],
                    rate_limit: 60,
                    concurrency_limit: 4,
                    last_used_at: null,
                    expires_at: null,
                    created_at: '2026-07-31T00:00:00Z',
                    read_only: false,
                    emergency_disabled: false,
                    resource_version: 1,
                    grant: {
                        id: 1,
                        project_id: 1,
                        role: 'agent',
                        scopes: ['tickets:read'],
                        is_active: true,
                        expires_at: null,
                        created_at: '2026-07-31T00:00:00Z',
                    },
                }] : [],
                total: name ? 1 : 0,
                page: 1,
                page_size: 25,
                total_pages: name ? 1 : 0,
            },
            meta: { request_id: 'e2e-agent-write-scope' },
        });
        await page.route(
            '**/api/projects/*/admin/agents/agent-control/overview',
            async (route) => {
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({
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
                        meta: { request_id: 'e2e-agent-write-overview' },
                    }),
                });
            },
        );
        await page.route(
            '**/api/projects/*/admin/agents/service-principals*',
            async (route) => {
                const request = route.request();
                const segments = new URL(request.url()).pathname
                    .split('/')
                    .filter(Boolean);
                const key = decodeURIComponent(
                    segments[segments.indexOf('projects') + 1],
                );
                if (request.method() === 'GET') {
                    if (key === 'WRITE-SWITCHED') {
                        switchedListLoaded = true;
                        await route.fulfill({
                            status: 200,
                            contentType: 'application/json',
                            body: JSON.stringify(
                                principalPage('切换后智能体'),
                            ),
                        });
                        return;
                    }
                    originalProjectKey = key;
                    await route.fulfill({
                        status: 200,
                        contentType: 'application/json',
                        body: JSON.stringify(principalPage('原项目智能体')),
                    });
                    return;
                }
                if (request.method() === 'POST') {
                    writeStarted = true;
                    await new Promise((resolve) => setTimeout(resolve, 800));
                    await route.fulfill({
                        status: 201,
                        contentType: 'application/json',
                        body: JSON.stringify({
                            data: {
                                client_id: '00000000-0000-7000-8200-000000000001',
                                client_secret: 'committed-secret-must-render',
                                project_key: originalProjectKey,
                            },
                            meta: { request_id: 'e2e-stale-write' },
                        }),
                    }).catch(() => undefined);
                    return;
                }
                await route.continue();
            },
        );
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('table', { name: '服务主体列表', exact: true }),
        ).toContainText('原项目智能体');
        await main
            .getByRole('button', { name: '新建智能体', exact: true })
            .click();
        const createDialog = page.getByRole('dialog', {
            name: '新建 AI 智能体服务主体',
        });
        await createDialog.getByLabel('名称').fill('旧作用域待取消智能体');
        await createDialog
            .getByRole('button', { name: '创建并签发凭据' })
            .click();
        await expect.poll(() => writeStarted).toBe(true);
        await expect(
            page.getByText(
                '一次性凭据正在签发或尚未保存，保存前已暂时锁定项目切换。',
                { exact: true },
            ),
        ).toBeVisible();
        await expect(
            page.getByTestId('active-project-switcher'),
        ).toHaveAttribute('aria-disabled', 'true');
        await page.evaluate(() => {
            window.dispatchEvent(new CustomEvent(
                'chronodesk:project-scope-changed',
                { detail: { project_key: 'WRITE-SWITCHED' } },
            ));
        });
        expect(switchedListLoaded).toBe(false);

        const credentialDialog = page.getByRole('dialog', {
            name: '保存一次性凭据',
        });
        await expect(credentialDialog).toBeVisible();
        await expect(
            credentialDialog.getByRole('textbox', {
                name: '客户端密钥',
                exact: true,
            }),
        ).toHaveValue('committed-secret-must-render');
        await expect(
            credentialDialog.getByText(
                `凭据签发项目：${originalProjectKey}`,
                { exact: true },
            ),
        ).toBeVisible();
        await expect(
            page.getByText('服务主体已创建，请立即保存一次性密钥', {
                exact: true,
            }),
        ).toBeVisible();

        await credentialDialog
            .getByRole('button', { name: '我已安全保存' })
            .click();
        await expect(credentialDialog).toHaveCount(0);
        await expect(
            page.getByText(
                '一次性凭据正在签发或尚未保存，保存前已暂时锁定项目切换。',
                { exact: true },
            ),
        ).toHaveCount(0);
        await expect(
            page.getByTestId('active-project-switcher'),
        ).not.toHaveAttribute('aria-disabled', 'true');
        await expect(
            main.getByRole('button', { name: '新建智能体', exact: true }),
        ).toBeEnabled();

        await page.getByTestId('active-project-switcher').click();
        await page
            .getByRole('option', {
                name: '切换后项目 · 项目管理员',
                exact: true,
            })
            .click();
        await expect.poll(() =>
            page.evaluate(() => {
                const serialized = localStorage.getItem(
                    'chronodesk.activeProject',
                );
                if (!serialized) return null;
                return (
                    JSON.parse(serialized) as { project_key?: string }
                ).project_key;
            }),
        ).toBe('WRITE-SWITCHED');
        await page.goto('/#/agent-control');
        await expect.poll(() => switchedListLoaded).toBe(true);
        await expect(
            main.getByRole('table', { name: '服务主体列表', exact: true }),
        ).toContainText('切换后智能体');
        await expect(
            main.getByRole('table', { name: '服务主体列表', exact: true }),
        ).not.toContainText('原项目智能体');
    });

    test('AGT-015：项目授权状态与到期时间明确展示并禁用不可用操作', async ({
        page,
    }) => {
        await authenticatePage(page);
        const principal = (
            idSuffix: string,
            name: string,
            isActive: boolean,
            expiresAt: string | null,
        ) => ({
            id: `00000000-0000-7000-8300-${idSuffix}`,
            client_id: `00000000-0000-7000-8300-${idSuffix}`,
            name,
            description: '',
            status: 'active',
            scopes: ['tickets:read'],
            rate_limit: 60,
            concurrency_limit: 4,
            last_used_at: null,
            expires_at: null,
            created_at: '2026-07-31T00:00:00Z',
            read_only: false,
            emergency_disabled: false,
            resource_version: 1,
            grant: {
                id: Number(idSuffix.slice(-2)),
                project_id: 1,
                role: 'agent',
                scopes: ['tickets:read'],
                is_active: isActive,
                expires_at: expiresAt,
                created_at: '2026-07-31T00:00:00Z',
            },
        });
        await page.route(
            '**/api/projects/*/admin/agents/service-principals?*',
            async (route) => {
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({
                        data: {
                            items: [
                                principal(
                                    '000000000001',
                                    '有效授权智能体',
                                    true,
                                    null,
                                ),
                                principal(
                                    '000000000002',
                                    '停用授权智能体',
                                    false,
                                    null,
                                ),
                                principal(
                                    '000000000003',
                                    '过期授权智能体',
                                    true,
                                    '2020-01-01T00:00:00Z',
                                ),
                            ],
                            total: 3,
                            page: 1,
                            page_size: 25,
                            total_pages: 1,
                        },
                        meta: { request_id: 'e2e-grant-state' },
                    }),
                });
            },
        );
        await page.goto('/#/agent-control');
        const table = page.getByRole('table', {
            name: '服务主体列表',
            exact: true,
        });
        const activeRow = table.getByRole('row', { name: /有效授权智能体/u });
        const inactiveRow = table.getByRole('row', { name: /停用授权智能体/u });
        const expiredRow = table.getByRole('row', { name: /过期授权智能体/u });
        await expect(activeRow.getByText('项目授权有效', { exact: true })).toBeVisible();
        await expect(inactiveRow.getByText('项目授权已停用', { exact: true })).toBeVisible();
        await expect(expiredRow.getByText('项目授权已过期', { exact: true })).toBeVisible();
        for (const row of [inactiveRow, expiredRow]) {
            await expect(row.getByRole('button')).toHaveCount(4);
            for (const action of await row.getByRole('button').all()) {
                await expect(action).toBeDisabled();
            }
        }
        for (const action of await activeRow.getByRole('button').all()) {
            await expect(action).toBeEnabled();
        }
    });

    test('AGT-001 AGT-002 AGT-008 UI-020：服务主体、策略、凭据与单体熔断', async ({
        page,
    }) => {
        await authenticatePage(page);
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('heading', {
                name: 'AI 智能体控制中心',
                exact: true,
            }),
        ).toBeVisible({ timeout: 15_000 });

        await main
            .getByRole('button', { name: '新建智能体', exact: true })
            .click();
        let dialog = page.getByRole('dialog', {
            name: '新建 AI 智能体服务主体',
        });
        await dialog.getByLabel('名称').fill(principalName);
        await dialog
            .getByLabel('说明')
            .fill('Playwright E2E 最小权限服务主体');
        await dialog.getByLabel('权限范围（Scope）').click();
        await page
            .getByRole('option', { name: '更新工单（tickets:update）' })
            .click();
        await page.keyboard.press('Escape');
        const create = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /^\/api\/projects\/[^/]+\/admin\/agents\/service-principals$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await dialog
            .getByRole('button', { name: '创建并签发凭据' })
            .click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(201);
        const createdPrincipal = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof createdPrincipal.client_id).toBe('string');
        trackE2EResource(
            'agentPrincipals',
            createdPrincipal.client_id as string,
        );

        dialog = page.getByRole('dialog', { name: '保存一次性凭据' });
        const firstSecret = await dialog
            .getByRole('textbox', {
                name: '客户端密钥',
                exact: true,
            })
            .inputValue();
        expect(firstSecret.length).toBeGreaterThan(20);
        await dialog.getByRole('button', { name: '我已安全保存' }).click();
        await expect(dialog).toHaveCount(0);

        const principalTable = main.getByRole('table', {
            name: '服务主体列表',
            exact: true,
        });
        const row = principalTable.getByRole('row', {
            name: principalRowName,
        });
        await expect(row).toBeVisible({ timeout: 15_000 });
        await expect(row).toContainText('读取工单');
        await expect(row).toContainText('更新工单');

        await row
            .getByRole('button', { name: `管理 ${principalName} 的策略` })
            .click();
        dialog = page.getByRole('dialog', {
            name: `${principalName} · 权限范围策略`,
        });
        const activePolicyButtons = dialog.getByRole('button', {
            name: '停用',
            exact: true,
        });
        const initialActivePolicyCount = await activePolicyButtons.count();
        await dialog.getByLabel('操作（可选）').fill('ticket.update');
        let createPolicy = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/api\/projects\/[^/]+\/admin\/agents\/service-principals\/[^/]+\/policies$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await dialog.getByRole('button', { name: '新增策略' }).click();
        expect((await createPolicy).status()).toBe(201);
        await expect(dialog).toContainText('更新工单');
        await expect(activePolicyButtons).toHaveCount(
            initialActivePolicyCount + 1,
        );

        await dialog.getByLabel('效果').click();
        await page.getByRole('option', { name: '允许', exact: true }).click();
        await dialog.getByLabel('权限范围（Scope）').click();
        await page
            .getByRole('option', {
                name: '流转工单状态（tickets:transition）',
            })
            .click();
        await dialog
            .getByLabel('操作（可选）')
            .fill('ticket.transition');
        await dialog.getByRole('button', { name: '新增策略' }).click();
        let confirmation = page.getByRole('dialog', {
            name: '确认新增允许策略',
        });
        createPolicy = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/api\/projects\/[^/]+\/admin\/agents\/service-principals\/[^/]+\/policies$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await confirmation
            .getByRole('button', { name: '确认授予权限' })
            .click();
        expect((await createPolicy).status()).toBe(201);
        await expect(dialog).toContainText('流转工单状态');
        await expect(activePolicyButtons).toHaveCount(
            initialActivePolicyCount + 2,
        );
        await dialog.getByRole('button', { name: '关闭' }).click();

        await row
            .getByRole('button', { name: `轮换 ${principalName} 的凭据` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认轮换智能体凭据',
        });
        const rotate = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/credentials\/rotate$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await confirmation
            .getByRole('button', { name: '轮换并撤销旧凭据' })
            .click();
        expect((await rotate).status()).toBe(200);
        dialog = page.getByRole('dialog', { name: '保存一次性凭据' });
        const rotatedSecret = await dialog
            .getByRole('textbox', {
                name: '客户端密钥',
                exact: true,
            })
            .inputValue();
        expect(rotatedSecret.length).toBeGreaterThan(20);
        expect(rotatedSecret === firstSecret).toBe(false);
        await dialog.getByRole('button', { name: '我已安全保存' }).click();

        await row
            .getByRole('button', { name: `启用 ${principalName} 的熔断` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认立即熔断智能体',
        });
        await confirmation
            .getByRole('button', { name: '立即熔断' })
            .click();
        await expect(
            row.getByText('已熔断', { exact: true }),
        ).toBeVisible({ timeout: 10_000 });

        await row
            .getByRole('button', { name: `解除 ${principalName} 的熔断` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认解除智能体熔断',
        });
        await confirmation
            .getByRole('button', { name: '解除熔断' })
            .click();
        await expect(
            row.getByText('已熔断', { exact: true }),
        ).toHaveCount(0, {
            timeout: 10_000,
        });

        await row.getByRole('button', { name: '停用', exact: true }).click();
        confirmation = page.getByRole('dialog', {
            name: '确认停用智能体',
        });
        await confirmation
            .getByRole('button', { name: '确认停用' })
            .click();
        await expect(
            row.getByText('停用', { exact: true }),
        ).toBeVisible({ timeout: 10_000 });
        await expectChineseOperations(page);
    });
});
