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

test.describe('AI 智能体控制中心', () => {
    test.describe.configure({ mode: 'serial' });
    let globalControlsBeforeTest: AgentGlobalControlSnapshot;

    test.beforeEach(async ({ request }) => {
        assertDestructiveE2EAllowed('Agent 控制中心写操作 E2E');
        globalControlsBeforeTest = await captureAgentGlobalControls(request);
    });

    test.afterEach(async ({ request }) => {
        await restoreAgentGlobalControls(request, globalControlsBeforeTest);
    });

    test.afterAll(async ({ request }) => {
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
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('table', { name: '服务主体列表', exact: true }),
        ).toBeVisible({ timeout: 15_000 });

        expect(
            requests.some((url) =>
                url.pathname.endsWith('/agent-control/overview'),
            ),
        ).toBe(true);
        const principalRequest = requests.find((url) =>
            url.pathname.endsWith('/service-principals'),
        );
        expect(principalRequest?.searchParams.get('page')).toBe('1');
        expect(principalRequest?.searchParams.get('page_size')).toBe('25');
        expect(principalRequest?.searchParams.get('sort_by')).toBe(
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
            window.dispatchEvent(new CustomEvent(
                'chronodesk:project-scope-changed',
                { detail: { project_key: 'SWITCHED' } },
            ));
        });
        const table = page.getByRole('table', {
            name: '服务主体列表',
            exact: true,
        });
        await expect(table.getByText('切换后智能体', { exact: true })).toBeVisible();
        await page.waitForTimeout(700);
        await expect(table.getByText('旧项目智能体', { exact: true })).toHaveCount(0);
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
