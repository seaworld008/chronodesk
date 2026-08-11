import { expect, test, type Page } from '@playwright/test';
import { apiRequest } from './helpers/api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';
import type { ProjectRole } from '../src/lib/generated/human-api';
import {
    authenticatePage,
    cleanupE2EData,
    e2eRunID,
    E2E_PREFIX,
    extractData,
    extractItems,
    getAdminToken,
    projectAPIPath,
    resolveE2EProjectKey,
    trackE2EResource,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const runID = e2eRunID();
const webhookName = `${E2E_PREFIX}${runID}-Canonical-Webhook`;
const markerSecret = `${E2E_PREFIX}${runID}-仅用于密文测试`;

const setOptionSelectionByRenderedText = async (
    page: Page,
    primary: string,
    secondary: string,
    selected = true,
) => {
    const option = page
        .getByRole('option')
        .filter({ hasText: primary })
        .filter({ hasText: secondary });
    await expect(option).toHaveCount(1);
    await expect(option.getByText(primary, { exact: true })).toBeVisible();
    await expect(option.getByText(secondary, { exact: true })).toBeVisible();
    const checkbox = option.getByRole('checkbox');
    await expect(checkbox).toHaveCount(1);
    if ((await checkbox.isChecked()) !== selected) {
        await option.click();
    }
    if (selected) {
        await expect(checkbox).toBeChecked();
    } else {
        await expect(checkbox).not.toBeChecked();
    }
};

const mockWebhook = {
    id: 731,
    name: '异步测试 Webhook',
    description: '仅验证浏览器异步入队契约',
    provider: 'custom',
    webhook_url_masked: 'https://webhook.example.test/…',
    has_webhook_url: true,
    status: 'active',
    enabled_events_list: ['io.chronodesk.system.alert.v1'],
    filter_rules_obj: { transition_statuses: [] },
    message_format: 'markdown',
    retry_count: 0,
    retry_interval: 60,
    timeout_seconds: 30,
    is_async: true,
    rate_limit: 60,
    rate_limit_window: 60,
    last_triggered_at: null,
    last_success_at: null,
    last_error_at: null,
    last_error: '',
    total_sent: 0,
    total_success: 0,
    total_failed: 0,
    resource_version: 1,
};

const installWebhookAsyncMockBackend = async (
    page: Page,
    options: {
        failFirstDelivery?: boolean;
        emptyDeliveries?: boolean;
        holdContinuation?: boolean;
        projectRole?: ProjectRole;
    } = {},
) => {
    const access = authorizedProjectAccess(
        projectA,
        options.projectRole ?? 'manager',
    );
    const webhooksPath = `/api/projects/${projectA.key}/webhooks`;
    const emergencyRevokePath =
        `/api/projects/${projectA.key}/admin/agents/webhooks/${mockWebhook.id}/emergency-revoke`;
    let testCalls = 0;
    let emergencyRevokeCalls = 0;
    let emergencyRevokeHeaders: Record<string, string> = {};
    let deliveryCalls = 0;
    const listQueries: string[] = [];
    const deliveryQueries: string[] = [];
    let releaseContinuation: (() => void) | undefined;
    const continuationGate = new Promise<void>((resolve) => {
        releaseContinuation = resolve;
    });

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const pathname = new URL(request.url()).pathname;
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (pathname === webhooksPath && request.method() === 'GET') {
            listQueries.push(new URL(request.url()).search);
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [mockWebhook],
                    total: 1,
                    page: 1,
                    page_size: 25,
                    total_pages: 1,
                },
            });
            return;
        }
        if (
            pathname === `${webhooksPath}/${mockWebhook.id}/logs`
            && request.method() === 'GET'
        ) {
            deliveryCalls += 1;
            const target = new URL(request.url());
            deliveryQueries.push(target.search);
            if (options.failFirstDelivery && deliveryCalls === 1) {
                await fulfillJSON(route, {
                    code: 1,
                    error: 'internal_error',
                    msg: '服务暂时不可用',
                }, 500);
                return;
            }
            if (options.emptyDeliveries) {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: [],
                        next_cursor: '',
                        has_more: false,
                    },
                });
                return;
            }
            const continuation = target.searchParams.get('cursor');
            const failedFilter =
                target.searchParams.get('status') === 'failed';
            if (
                options.holdContinuation
                && continuation
                && !failedFilter
            ) {
                await continuationGate;
            }
            try {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: [{
                            id: failedFilter ? 732 : continuation ? 730 : 731,
                            created_at: continuation
                                ? '2026-07-31T07:59:00Z'
                                : '2026-07-31T08:00:00Z',
                            config_id: mockWebhook.id,
                            event_type: 'io.chronodesk.system.alert.v1',
                            status:
                                continuation || failedFilter
                                    ? 'failed'
                                    : 'success',
                            response_status:
                                continuation || failedFilter ? 503 : 204,
                            response_time:
                                continuation || failedFilter ? 99 : 12,
                            error_message:
                                failedFilter
                                    ? '筛选后的失败记录'
                                    : continuation ? '上游暂不可用' : '',
                        }],
                        next_cursor:
                            continuation || failedFilter
                                ? ''
                                : 'signed-next-page',
                        has_more: !(continuation || failedFilter),
                    },
                });
            } catch {
                // 被新筛选或抽屉关闭取消的旧请求没有可写响应体。
            }
            return;
        }
        if (
            pathname === `${webhooksPath}/${mockWebhook.id}/test`
            && request.method() === 'POST'
        ) {
            testCalls += 1;
            await fulfillJSON(
                route,
                {
                    code: 0,
                    msg: 'Webhook 测试已入队',
                    data: {
                        operation_id: '019fb64a-38ac-7a01-8000-000000000001',
                        event_id: '019fb64a-38ac-7a01-8000-000000000002',
                        delivery_id: '019fb64a-38ac-7a01-8000-000000000003',
                        snapshot_id: '019fb64a-38ac-7a01-8000-000000000004',
                        config_id: mockWebhook.id,
                        configuration_version:
                            'webhook-config:731@2026-07-31T08:00:00Z',
                        status: 'queued',
                        queued: true,
                        delivered: false,
                    },
                },
                202,
            );
            return;
        }
        if (
            pathname === emergencyRevokePath
            && request.method() === 'POST'
        ) {
            emergencyRevokeCalls += 1;
            emergencyRevokeHeaders = request.headers();
            await fulfillJSON(route, {
                code: 0,
                data: {
                    config_id: mockWebhook.id,
                    status: 'disabled',
                    expired_deliveries: 3,
                    in_flight_deliveries: 1,
                    shredded_snapshots: 4,
                    credential_shred_reason: 'revoked',
                },
                receipt: {
                    event_id: '019fb64a-38ac-7a01-8000-000000000011',
                    resource_type: 'webhook',
                    resource_id: String(mockWebhook.id),
                    resource_version: 2,
                    occurred_at: '2026-08-11T08:00:00Z',
                },
            });
            return;
        }
        await fulfillJSON(route, { code: 0, data: [] });
    });

    return {
        webhooksPath,
        emergencyRevokePath,
        testCalls: () => testCalls,
        emergencyRevokeCalls: () => emergencyRevokeCalls,
        emergencyRevokeHeaders: () => emergencyRevokeHeaders,
        deliveryCalls: () => deliveryCalls,
        listQueries,
        deliveryQueries,
        releaseContinuation: () => releaseContinuation?.(),
    };
};

test.describe('Webhook 测试异步入队浏览器契约（mock）', () => {
    test('紧急撤销仅限项目管理员并发送 CAS 与幂等前置条件', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-emergency-revoke',
            },
            projectA,
        );
        const backend = await installWebhookAsyncMockBackend(page, {
            projectRole: 'project_admin',
        });
        await page.goto('/#/webhook-settings');

        const row = page.getByRole('row', { name: /异步测试 Webhook/u });
        await expect(row).toBeVisible();
        await row.getByRole('button', {
            name: '紧急撤销 Webhook：异步测试 Webhook',
        }).click();

        const confirmation = page.getByRole('dialog', {
            name: '紧急撤销 Webhook',
            exact: true,
        });
        await expect(confirmation).toContainText(
            '普通停用或删除不会撤回已经冻结的投递',
        );
        await expect(confirmation).toContainText(
            '待处理、失败和终止的投递会立即过期',
        );
        await expect(confirmation).toContainText(
            '投递中的请求无法召回',
        );
        await expect(confirmation).toContainText(
            '凭据将被不可逆粉碎',
        );

        const responsePromise = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST'
                && new URL(response.url()).pathname
                    === backend.emergencyRevokePath,
        );
        await confirmation.getByRole('button', {
            name: '确认紧急撤销',
            exact: true,
        }).click();
        expect((await responsePromise).status()).toBe(200);

        expect(backend.emergencyRevokeCalls()).toBe(1);
        expect(backend.emergencyRevokeHeaders()['if-match']).toBe('"v1"');
        expect(
            backend.emergencyRevokeHeaders()['idempotency-key'],
        ).toEqual(expect.any(String));
        expect(
            backend.emergencyRevokeHeaders()['idempotency-key'].length,
        ).toBeGreaterThan(0);
        await expect(
            page.getByText(
                '紧急撤销完成：3 条投递已过期，4 份凭据已粉碎；仍有 1 条投递正在执行，无法召回',
            ),
        ).toBeVisible();
    });

    test('项目经理看不到紧急撤销入口', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-emergency-revoke-manager',
            },
            projectA,
        );
        await installWebhookAsyncMockBackend(page, {
            projectRole: 'manager',
        });
        await page.goto('/#/webhook-settings');
        const row = page.getByRole('row', { name: /异步测试 Webhook/u });
        await expect(row).toBeVisible();
        await expect(row.getByRole('button', {
            name: '紧急撤销 Webhook：异步测试 Webhook',
        })).toHaveCount(0);
    });

    test('HTTP 202 queued receipt 只提示已入队并等待投递结果', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-async-contract',
            },
            projectA,
        );
        const backend = await installWebhookAsyncMockBackend(page);
        await page.goto('/#/webhook-settings');

        const row = page.getByRole('row', { name: /异步测试 Webhook/u });
        await expect(row).toBeVisible();
        await row
            .getByRole('button', { name: '测试 Webhook：异步测试 Webhook' })
            .click();

        const confirmation = page.getByRole('dialog', {
            name: '测试 Webhook',
            exact: true,
        });
        await expect(confirmation).toContainText(
            '入队不代表发送成功，请随后查看投递日志',
        );
        const responsePromise = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST'
                && new URL(response.url()).pathname
                    === `${backend.webhooksPath}/${mockWebhook.id}/test`,
        );
        await confirmation
            .getByRole('button', { name: '确认入队', exact: true })
            .click();

        const response = await responsePromise;
        expect(response.status()).toBe(202);
        const payload = await response.json() as Record<string, unknown>;
        expect(payload.code).toBe(0);
        expect(extractData<Record<string, unknown>>(payload)).toMatchObject({
            config_id: mockWebhook.id,
            status: 'queued',
            queued: true,
            delivered: false,
        });
        await expect(
            page.getByText('Webhook 测试已入队，请等待投递结果'),
        ).toBeVisible();
        await expect(page.getByText('Webhook 测试成功')).toHaveCount(0);
        expect(backend.testCalls()).toBe(1);
    });

    test('Checkbox 与 ListItemText 结构可独立选择 canonical 事件和状态', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-canonical-selector',
            },
            projectA,
        );
        await installWebhookAsyncMockBackend(page);
        await page.goto('/#/webhook-settings');
        await page.getByRole('button', { name: '新增 Webhook' }).click();
        const form = page.getByRole('dialog', { name: '新增 Webhook' });

        await form.getByLabel('订阅事件', { exact: true }).click();
        await setOptionSelectionByRenderedText(
            page,
            '工单状态流转',
            'io.chronodesk.ticket.transitioned.v1',
        );
        await page.keyboard.press('Escape');

        await form.getByLabel('状态流转筛选', { exact: true }).click();
        await setOptionSelectionByRenderedText(page, '已解决', 'resolved');
        await page.keyboard.press('Escape');
        await form
            .getByRole('button', { name: '清空已选订阅事件' })
            .click();
        await expect(
            form.getByLabel('状态流转筛选', { exact: true }),
        ).toHaveCount(0);
    });

    test('真实分页请求与键盘可达投递抽屉使用游标续页', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-cursor-drawer',
            },
            projectA,
        );
        const backend = await installWebhookAsyncMockBackend(page);
        await page.goto('/#/webhook-settings');
        const row = page.getByRole('row', { name: /异步测试 Webhook/u });
        await expect(row).toBeVisible();
        expect(backend.listQueries.at(-1)).toContain('page=1');
        expect(backend.listQueries.at(-1)).toContain('page_size=25');

        const deliveryButton = row.getByRole('button', {
            name: '查看投递记录：异步测试 Webhook',
        });
        await deliveryButton.focus();
        await page.keyboard.press('Enter');
        await expect(
            page.getByRole('heading', { name: '投递记录' }),
        ).toBeVisible();
        const firstDelivery = page.getByRole('button')
            .filter({ hasText: '系统警报' })
            .filter({ hasText: '成功' });
        await expect(firstDelivery).toBeVisible();
        expect(backend.deliveryQueries[0]).toBe('?limit=25');

        await firstDelivery.focus();
        await page.keyboard.press('Enter');
        await expect(
            page.getByRole('heading', { name: '投递详情 #731' }),
        ).toBeVisible();

        await page.getByRole('button', { name: '下一页' }).click();
        await expect(
            page.getByRole('button')
                .filter({ hasText: '系统警报' })
                .filter({ hasText: '失败' }),
        ).toBeVisible();
        expect(backend.deliveryQueries.at(-1)).toContain(
            'cursor=signed-next-page',
        );
        expect(backend.deliveryCalls()).toBe(2);
        await expect(
            page.getByRole('button', { name: '上一页' }),
        ).toBeEnabled();
    });

    test('投递抽屉展示错误并可重试', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-delivery-retry',
            },
            projectA,
        );
        const backend = await installWebhookAsyncMockBackend(page, {
            failFirstDelivery: true,
        });
        await page.goto('/#/webhook-settings');
        await page.getByRole('button', {
            name: '查看投递记录：异步测试 Webhook',
        }).click();
        const alert = page.getByRole('alert');
        await expect(alert).toContainText('服务暂时不可用');
        await alert.getByRole('button', { name: '重试' }).click();
        await expect(
            page.getByRole('button')
                .filter({ hasText: '系统警报' })
                .filter({ hasText: '成功' }),
        ).toBeVisible();
        expect(backend.deliveryCalls()).toBe(2);
    });

    test('投递筛选会取消旧续页请求且旧响应不能覆盖当前页', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-request-identity',
            },
            projectA,
        );
        const backend = await installWebhookAsyncMockBackend(page, {
            holdContinuation: true,
        });
        await page.goto('/#/webhook-settings');
        await page.getByRole('button', {
            name: '查看投递记录：异步测试 Webhook',
        }).click();
        await expect(
            page.getByRole('heading', { name: '投递详情 #731' }),
        ).toHaveCount(0);

        await page.getByRole('button', { name: '下一页' }).click();
        await expect.poll(() => backend.deliveryCalls()).toBe(2);
        await page.getByLabel('投递状态').click();
        await page.getByRole('option', { name: '失败' }).click();
        const filteredDelivery = page.getByRole('button')
            .filter({ hasText: '系统警报' })
            .filter({ hasText: '失败' });
        await expect(filteredDelivery).toBeVisible();
        await filteredDelivery.click();
        await expect(
            page.getByRole('heading', { name: '投递详情 #732' }),
        ).toBeVisible();
        await expect(page.getByText(/筛选后的失败记录/u)).toBeVisible();

        backend.releaseContinuation();
        await expect(
            page.getByRole('heading', { name: '投递详情 #732' }),
        ).toBeVisible();
        await expect(
            page.getByRole('heading', { name: '投递详情 #730' }),
        ).toHaveCount(0);
        expect(backend.deliveryQueries.at(-1)).toContain('status=failed');
        expect(backend.deliveryQueries.at(-1)).not.toContain('cursor=');
    });

    test('投递抽屉有明确空状态', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-webhook-delivery-empty',
            },
            projectA,
        );
        await installWebhookAsyncMockBackend(page, {
            emptyDeliveries: true,
        });
        await page.goto('/#/webhook-settings');
        await page.getByRole('button', {
            name: '查看投递记录：异步测试 Webhook',
        }).click();
        await expect(page.getByText('暂无投递记录')).toBeVisible();
    });
});

test.describe('Webhook canonical CloudEvent 管理', () => {
    test.beforeAll(() => {
        assertDestructiveE2EAllowed('Webhook 设置 E2E');
    });

    test.afterAll(async ({ request }) => {
        await cleanupE2EData(request, {
            automationRules: false,
            tickets: false,
            notifications: false,
            users: false,
            emailConfig: false,
            webhooks: true,
        });
    });

    test('EVT-002 EVT-007 EVT-008 EVT-010 UI-019：创建、编辑、测试并核对日志', async ({
        page,
        request,
    }) => {
        test.setTimeout(60_000);
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const webhooksPath = projectAPIPath(projectKey, 'webhooks');
        await authenticatePage(page);
        await page.goto('/#/webhook-settings');
        await page
            .getByRole('main')
            .getByRole('heading', { name: 'Webhook 集成' })
            .waitFor({ timeout: 15_000 });

        await page.getByRole('button', { name: '新增 Webhook' }).click();
        let form = page.getByRole('dialog', { name: '新增 Webhook' });
        await form.getByLabel('名称', { exact: true }).fill(webhookName);
        await form
            .getByLabel('描述', { exact: true })
            .fill('Playwright canonical CloudEvent 验证');
        await form.getByLabel('提供商', { exact: true }).click();
        await page.getByRole('option', { name: '自定义', exact: true }).click();
        await form
            .getByLabel('Webhook 地址', { exact: true })
            .fill('https://chronodesk-e2e.invalid/callback');
        await form
            .getByLabel('签名密钥（可选）', { exact: true })
            .fill(markerSecret);
        await expect(form.getByLabel('状态', { exact: true })).toHaveCount(0);
        await form.getByLabel('最大重试', { exact: true }).fill('0');
        await expect(
            form.getByLabel('异步发送', { exact: true }),
        ).toBeVisible();

        const create = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === webhooksPath,
        );
        await form.getByRole('button', { name: '保存' }).click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(200);
        const createBody = createResponse.request().postDataJSON() as Record<
            string,
            unknown
        >;
        expect(createBody).not.toHaveProperty('status');
        const createdWebhook = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof createdWebhook.id).toBe('number');
        expect(createdWebhook.status).toBe('inactive');
        trackE2EResource('webhooks', createdWebhook.id as number);
        const row = page.getByRole('row', { name: new RegExp(webhookName) });
        await expect(row).toBeVisible({ timeout: 15_000 });
        await expect(row).toContainText('工单创建');

        await row
            .getByRole('button', { name: `编辑 Webhook：${webhookName}` })
            .click();
        form = page.getByRole('dialog', { name: '编辑 Webhook' });
        await expect(
            form.getByLabel('Webhook 地址', { exact: true }),
        ).toHaveValue('');
        await form.getByLabel('订阅事件', { exact: true }).click();
        await setOptionSelectionByRenderedText(
            page,
            '工单创建',
            'io.chronodesk.ticket.created.v1',
            false,
        );
        await setOptionSelectionByRenderedText(
            page,
            '工单状态流转',
            'io.chronodesk.ticket.transitioned.v1',
        );
        await page.keyboard.press('Escape');

        await form.getByLabel('状态流转筛选', { exact: true }).click();
        await setOptionSelectionByRenderedText(page, '已解决', 'resolved');
        await setOptionSelectionByRenderedText(page, '已关闭', 'closed');
        await page.keyboard.press('Escape');

        const update = page.waitForResponse(
            (response) =>
                response.request().method() === 'PUT' &&
                new URL(response.url()).pathname.startsWith(`${webhooksPath}/`),
        );
        await form.getByRole('button', { name: '保存' }).click();
        const updateResponse = await update;
        expect(updateResponse.status()).toBe(200);
        expect(
            updateResponse.request().postDataJSON() as Record<string, unknown>,
        ).not.toHaveProperty('webhook_url');
        await expect(row).toContainText('工单状态流转（已解决、已关闭）', {
            timeout: 15_000,
        });

        const listResponse = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `${webhooksPath}?page=1&page_size=100`,
        );
        expect(JSON.stringify(listResponse)).not.toContain(markerSecret);
        const webhook = extractItems<Record<string, unknown>>(listResponse).find(
            (candidate) => candidate.name === webhookName,
        );
        expect(typeof webhook?.id).toBe('number');
        expect(webhook).not.toHaveProperty('webhook_url');
        expect(webhook?.webhook_url_masked).toBe(
            'https://chronodesk-e2e.invalid/…',
        );
        expect(webhook?.has_webhook_url).toBe(true);
        expect(webhook?.enabled_events_list).toEqual([
            'io.chronodesk.ticket.transitioned.v1',
        ]);
        expect(webhook?.filter_rules_obj).toEqual({
            transition_statuses: ['resolved', 'closed'],
        });

        await row
            .getByRole('button', { name: `测试 Webhook：${webhookName}` })
            .click();
        const confirmation = page.getByRole('dialog', {
            name: '测试 Webhook',
            exact: true,
        });
        await expect(confirmation).toContainText(
            `测试会将一条真实请求加入“${webhookName}”的投递队列。入队不代表发送成功，请随后查看投递日志，确定继续吗？`,
        );
        const testResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    `${webhooksPath}/${webhook!.id}/test`,
        );
        await confirmation
            .getByRole('button', {
                name: '确认入队',
                exact: true,
            })
            .click();
        const tested = await testResponse;
        expect(tested.status()).toBe(202);
        const testPayload = await tested.json() as Record<string, unknown>;
        expect(testPayload.code).toBe(0);
        expect(testPayload.msg).toBe('Webhook 测试已入队');
        const queuedReceipt =
            extractData<Record<string, unknown>>(testPayload);
        expect(queuedReceipt).toMatchObject({
            config_id: webhook!.id,
            status: 'queued',
            queued: true,
            delivered: false,
        });
        for (
            const field of [
                'operation_id',
                'event_id',
                'delivery_id',
                'snapshot_id',
                'configuration_version',
            ]
        ) {
            expect(queuedReceipt[field]).toEqual(expect.any(String));
            expect((queuedReceipt[field] as string).length).toBeGreaterThan(0);
        }
        await expect(
            page.getByText('Webhook 测试已入队，请等待投递结果'),
        ).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText('Webhook 测试成功')).toHaveCount(0);
        await expectChineseOperations(page);

        let logs: Record<string, unknown>[] = [];
        let finalLog: Record<string, unknown> | undefined;
        await expect
            .poll(
                async () => {
                    const logsResponse = await apiRequest<Record<string, unknown>>(
                        request,
                        token,
                        `${webhooksPath}/${webhook!.id}/logs?limit=20`,
                    );
                    logs = extractItems<Record<string, unknown>>(logsResponse);
                    finalLog = logs.find(
                        (entry) =>
                            entry.event_type
                                === 'io.chronodesk.system.alert.v1'
                            && ['success', 'failed'].includes(
                                String(entry.status),
                            ),
                    );
                    return finalLog?.status ?? 'waiting';
                },
                {
                    message:
                        'Webhook worker 应在提交后执行投递并持久化最终日志',
                    timeout: 15_000,
                },
            )
            .toBe('failed');
        expect(finalLog?.event_type).toBe(
            'io.chronodesk.system.alert.v1',
        );
        expect(finalLog?.status).toBe('failed');
        expect(JSON.stringify(logs)).not.toContain(markerSecret);

        await row
            .getByRole('button', { name: `编辑 Webhook：${webhookName}` })
            .click();
        const activationForm = page.getByRole('dialog', {
            name: '编辑 Webhook',
        });
        await activationForm.getByLabel('状态', { exact: true }).click();
        await page.getByRole('option', { name: '启用', exact: true }).click();
        const activation = page.waitForResponse(
            (response) =>
                response.request().method() === 'PUT' &&
                new URL(response.url()).pathname ===
                    `${webhooksPath}/${webhook!.id}`,
        );
        await activationForm.getByRole('button', { name: '保存' }).click();
        expect((await activation).status()).toBe(200);
        await expect(row).toContainText('启用');
    });
});
