import { test, expect, type Page } from '@playwright/test';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';
import {
    authenticatePage,
    cleanupE2EData,
    E2E_MARKER,
    extractData,
    getAdminToken,
    projectAPIPath,
    resolveE2EProjectKey,
    trackE2EResource,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

const installAutomationLogMockBackend = async (
    page: Page,
    options: {
        failFirst?: boolean;
        empty?: boolean;
        holdContinuation?: boolean;
    } = {},
) => {
    const access = authorizedProjectAccess(projectA, 'manager');
    const logsPath =
        `/api/projects/${projectA.key}/admin/automation/logs`;
    const queries: string[] = [];
    let calls = 0;
    let releaseContinuation: (() => void) | undefined;
    const continuationGate = new Promise<void>((resolve) => {
        releaseContinuation = resolve;
    });
    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const target = new URL(request.url());
        if (target.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (target.pathname === logsPath && request.method() === 'GET') {
            calls += 1;
            queries.push(target.search);
            if (options.failFirst && calls === 1) {
                await fulfillJSON(route, {
                    success: false,
                    error: 'internal_error',
                    message: '自动化日志服务暂不可用',
                }, 500);
                return;
            }
            if (options.empty) {
                await fulfillJSON(route, {
                    success: true,
                    data: {
                        items: [],
                        next_cursor: '',
                        has_more: false,
                    },
                });
                return;
            }
            const continuation = target.searchParams.get('cursor');
            const filtered = target.searchParams.get('success') === 'false';
            if (
                options.holdContinuation
                && continuation
                && !filtered
            ) {
                await continuationGate;
            }
            try {
                await fulfillJSON(route, {
                    success: true,
                    data: {
                        items: [{
                            id: filtered ? 399 : continuation ? 300 : 301,
                            created_at: '2026-07-31T08:00:00Z',
                            rule_id: 7,
                            rule: { id: 7, name: '自动分派' },
                            ticket_id: filtered
                                ? 49
                                : continuation ? 41 : 42,
                            ticket: {
                                id: filtered
                                    ? 49
                                    : continuation ? 41 : 42,
                                ticket_number: filtered
                                    ? 'OPS-49'
                                    : continuation ? 'OPS-41' : 'OPS-42',
                                title: '示例工单',
                                status: 'open',
                            },
                            trigger_event:
                                'io.chronodesk.ticket.created.v1',
                            executed_at: continuation
                                ? '2026-07-31T07:59:00Z'
                                : '2026-07-31T08:00:00Z',
                            success: !(continuation || filtered),
                            error_message:
                                continuation || filtered
                                    ? '动作执行失败'
                                    : '',
                            execution_time:
                                continuation || filtered ? 24 : 12,
                        }],
                        next_cursor:
                            continuation || filtered
                                ? ''
                                : 'automation-next',
                        has_more: !(continuation || filtered),
                    },
                });
            } catch {
                // 被新筛选或项目切换取消的旧请求没有可写响应体。
            }
            return;
        }
        await fulfillJSON(route, { code: 0, data: [] });
    });
    return {
        calls: () => calls,
        queries,
        releaseContinuation: () => releaseContinuation?.(),
    };
};

test.describe('Automation execution cursor timeline（mock）', () => {
    test('使用游标续页、严格筛选并支持键盘查看详情', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-automation-cursor-timeline',
            },
            projectA,
        );
        const backend = await installAutomationLogMockBackend(page);
        await page.goto('/#/automation-logs');
        await expect(page.getByText('OPS-42')).toBeVisible();
        expect(backend.queries[0]).toBe('?limit=25');

        const detail = page.getByRole('button', { name: '查看' }).first();
        await detail.focus();
        await page.keyboard.press('Enter');
        await expect(
            page.getByRole('heading', { name: '执行详情 #301' }),
        ).toBeVisible();

        await page.getByRole('button', { name: '下一页' }).click();
        await expect(page.getByText('OPS-41')).toBeVisible();
        expect(backend.queries.at(-1)).toContain('cursor=automation-next');
        await expect(
            page.getByRole('button', { name: '上一页' }),
        ).toBeEnabled();

        await page.getByLabel('规则 ID').fill('7');
        await page.getByLabel('执行结果').click();
        await page.getByRole('option', { name: '失败' }).click();
        await page.getByRole('button', { name: '应用筛选' }).click();
        await expect.poll(() => backend.calls()).toBe(3);
        expect(backend.queries.at(-1)).toContain('rule_id=7');
        expect(backend.queries.at(-1)).toContain('success=false');
        expect(backend.queries.at(-1)).not.toContain('page=');
    });

    test('新筛选会取消旧续页请求且旧响应不能覆盖当前页', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-automation-request-identity',
            },
            projectA,
        );
        const backend = await installAutomationLogMockBackend(page, {
            holdContinuation: true,
        });
        await page.goto('/#/automation-logs');
        await expect(page.getByText('OPS-42')).toBeVisible();

        await page.getByRole('button', { name: '下一页' }).click();
        await expect.poll(() => backend.calls()).toBe(2);
        await page.getByLabel('执行结果').click();
        await page.getByRole('option', { name: '失败' }).click();
        await page.getByRole('button', { name: '应用筛选' }).click();
        await expect(page.getByText('OPS-49')).toBeVisible();

        backend.releaseContinuation();
        await expect(page.getByText('OPS-49')).toBeVisible();
        await expect(page.getByText('OPS-41')).toHaveCount(0);
        expect(backend.queries.at(-1)).toContain('success=false');
        expect(backend.queries.at(-1)).not.toContain('cursor=');
    });

    test('错误态可重试', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-automation-cursor-retry',
            },
            projectA,
        );
        const backend = await installAutomationLogMockBackend(page, {
            failFirst: true,
        });
        await page.goto('/#/automation-logs');
        const alert = page.getByRole('alert');
        await expect(alert).toContainText('自动化日志服务暂不可用');
        await alert.getByRole('button', { name: '重试' }).click();
        await expect(page.getByText('OPS-42')).toBeVisible();
        expect(backend.calls()).toBe(2);
    });

    test('空时间线有明确提示', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-automation-cursor-empty',
            },
            projectA,
        );
        await installAutomationLogMockBackend(page, { empty: true });
        await page.goto('/#/automation-logs');
        await expect(page.getByText('暂无自动化执行日志')).toBeVisible();
    });
});

test.describe('Automation Rules', () => {
    test.beforeAll(() => {
        assertDestructiveE2EAllowed('自动化规则 E2E');
    });

    test.afterAll(async ({ request }) => {
        await cleanupE2EData(request, {
            tickets: false,
            notifications: false,
            emailConfig: false,
        });
    });

    test('should create an automation rule', async ({ page, request }) => {
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const automationRulesPath = projectAPIPath(
            projectKey,
            'admin/automation/rules',
        );
        await authenticatePage(page, TEST_USER);

        await page.goto('/#/automation-rules');
        await page.getByRole('link', { name: '新建' }).click();
        await expect(page).toHaveURL(/#\/automation-rules\/create$/);

        const ruleName = `${E2E_MARKER}自动化规则`;
        await page
            .getByRole('textbox', { name: '名称', exact: true })
            .fill(ruleName);
        await page
            .getByRole('textbox', { name: '描述', exact: true })
            .fill('Playwright E2E 创建自动化规则');

        await page
            .getByRole('combobox', { name: /^规则类型/ })
            .click();
        await page.getByRole('option', { name: '自动分配' }).click();

        await page
            .getByRole('combobox', { name: /^触发事件/ })
            .click();
        await page.getByRole('option', { name: '工单创建' }).click();

        await page
            .getByRole('textbox', {
                name: '条件（JSON 数组）',
                exact: true,
            })
            .fill('[]');
        await page
            .getByRole('textbox', {
                name: '动作（JSON 数组）',
                exact: true,
            })
            .fill('[{"type":"assign","params":{"user_id":1}}]');

        const create = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    automationRulesPath,
        );
        await page.getByRole('button', { name: '保存' }).click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(201);
        const created = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof created.id).toBe('number');
        trackE2EResource('automationRules', created.id as number);

        await page.getByRole('link', { name: '返回列表' }).click();
        await expect(page).toHaveURL(/#\/automation-rules$/);
        await expect(page.getByText(ruleName)).toBeVisible({ timeout: 10000 });

        const row = page.getByRole('row', { name: new RegExp(ruleName) });
        await row.getByRole('link', { name: '编辑', exact: true }).click();
        await page
            .getByRole('textbox', { name: '描述', exact: true })
            .fill('Playwright E2E 更新自动化规则');
        const update = page.waitForResponse(
            (response) =>
                response.request().method() === 'PUT' &&
                new URL(response.url()).pathname ===
                    `${automationRulesPath}/${created.id}`,
        );
        await page.getByRole('button', { name: '保存' }).click();
        const updateResponse = await update;
        expect(updateResponse.status()).toBe(200);
        const updated = extractData<Record<string, unknown>>(
            await updateResponse.json(),
        );
        expect(updated.id).toBe(created.id);
        expect(typeof updated.conditions).toBe('string');
        expect(typeof updated.actions).toBe('string');
        expect(JSON.parse(updated.conditions as string)).toEqual([]);
        expect(JSON.parse(updated.actions as string)).toEqual([
            { type: 'assign', params: { user_id: 1 } },
        ]);
    });
});
