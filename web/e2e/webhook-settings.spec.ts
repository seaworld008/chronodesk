import { expect, test, type Page } from '@playwright/test';
import { apiRequest } from './helpers/api';
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

const selectOptionByRenderedText = async (
    page: Page,
    primary: string,
    secondary: string,
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
    await option.click();
    await expect(checkbox).toBeChecked();
};

const mockWebhook = {
    id: 731,
    name: '异步测试 Webhook',
    description: '仅验证浏览器异步入队契约',
    provider: 'custom',
    webhook_url: 'https://webhook.example.test/callback',
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
};

const installWebhookAsyncMockBackend = async (page: Page) => {
    const access = authorizedProjectAccess(projectA, 'manager');
    const webhooksPath = `/api/projects/${projectA.key}/webhooks`;
    let testCalls = 0;

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const pathname = new URL(request.url()).pathname;
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (pathname === webhooksPath && request.method() === 'GET') {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [mockWebhook],
                    total: 1,
                    page: 1,
                    size: 100,
                },
            });
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
        await fulfillJSON(route, { code: 0, data: [] });
    });

    return {
        webhooksPath,
        testCalls: () => testCalls,
    };
};

test.describe('Webhook 测试异步入队浏览器契约（mock）', () => {
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
        await selectOptionByRenderedText(
            page,
            '工单状态流转',
            'io.chronodesk.ticket.transitioned.v1',
        );
        await page.keyboard.press('Escape');

        await form.getByLabel('状态流转筛选', { exact: true }).click();
        await selectOptionByRenderedText(page, '已解决', 'resolved');
        await page.keyboard.press('Escape');
        await form
            .getByRole('button', { name: '清空已选订阅事件' })
            .click();
        await expect(
            form.getByLabel('状态流转筛选', { exact: true }),
        ).toHaveCount(0);
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
        expect(createdWebhook.status).toBe('active');
        trackE2EResource('webhooks', createdWebhook.id as number);
        const row = page.getByRole('row', { name: new RegExp(webhookName) });
        await expect(row).toBeVisible({ timeout: 15_000 });
        await expect(row).toContainText('工单创建');

        await row
            .getByRole('button', { name: `编辑 Webhook：${webhookName}` })
            .click();
        form = page.getByRole('dialog', { name: '编辑 Webhook' });
        await form.getByLabel('订阅事件', { exact: true }).click();
        await selectOptionByRenderedText(
            page,
            '工单创建',
            'io.chronodesk.ticket.created.v1',
        );
        await selectOptionByRenderedText(
            page,
            '工单状态流转',
            'io.chronodesk.ticket.transitioned.v1',
        );
        await page.keyboard.press('Escape');

        await form.getByLabel('状态流转筛选', { exact: true }).click();
        await selectOptionByRenderedText(page, '已解决', 'resolved');
        await selectOptionByRenderedText(page, '已关闭', 'closed');
        await page.keyboard.press('Escape');

        const update = page.waitForResponse(
            (response) =>
                response.request().method() === 'PUT' &&
                new URL(response.url()).pathname.startsWith(`${webhooksPath}/`),
        );
        await form.getByRole('button', { name: '保存' }).click();
        expect((await update).status()).toBe(200);
        await expect(row).toContainText('工单状态流转（已解决、已关闭）', {
            timeout: 15_000,
        });

        const listResponse = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `${webhooksPath}?page=1&page_size=100&name=${encodeURIComponent(webhookName)}`,
        );
        expect(JSON.stringify(listResponse)).not.toContain(markerSecret);
        const webhook = extractItems<Record<string, unknown>>(listResponse).find(
            (candidate) => candidate.name === webhookName,
        );
        expect(typeof webhook?.id).toBe('number');
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
                        `${webhooksPath}/${webhook!.id}/logs?page=1&page_size=20`,
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
    });
});
