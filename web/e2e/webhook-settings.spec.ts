import { expect, test } from '@playwright/test';
import { apiRequest } from './helpers/api';
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
        const createdWebhook = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof createdWebhook.id).toBe('number');
        trackE2EResource('webhooks', createdWebhook.id as number);
        const row = page.getByRole('row', { name: new RegExp(webhookName) });
        await expect(row).toBeVisible({ timeout: 15_000 });
        await expect(row).toContainText('工单创建');

        await row
            .getByRole('button', { name: `编辑 Webhook：${webhookName}` })
            .click();
        form = page.getByRole('dialog', { name: '编辑 Webhook' });
        await form.getByLabel('订阅事件', { exact: true }).click();
        await page
            .getByRole('option', {
                name: /工单创建（io\.chronodesk\.ticket\.created\.v1）/,
            })
            .click();
        await page
            .getByRole('option', {
                name: /工单状态流转（io\.chronodesk\.ticket\.transitioned\.v1）/,
            })
            .click();
        await page.keyboard.press('Escape');

        await form.getByLabel('状态流转筛选', { exact: true }).click();
        await page
            .getByRole('option', { name: '已解决（resolved）' })
            .click();
        await page
            .getByRole('option', { name: '已关闭（closed）' })
            .click();
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
            `测试将立即向“${webhookName}”配置的地址发送一条真实请求，确定继续吗？`,
        );
        const testResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    `${webhooksPath}/${webhook!.id}/test`,
        );
        await confirmation
            .getByRole('button', {
                name: '确认发送测试',
                exact: true,
            })
            .click();
        const tested = await testResponse;
        expect(tested.status()).toBe(200);
        const testPayload = await tested.json() as Record<string, unknown>;
        expect(testPayload.code).toBe(1);
        expect(extractData<Record<string, unknown>>(testPayload).status).toBe(
            'failed',
        );
        await expect(
            page.getByText(/测试失败|webhook目标地址未通过安全校验/i),
        ).toBeVisible({ timeout: 10_000 });
        await expectChineseOperations(page);

        let logs: Record<string, unknown>[] = [];
        await expect
            .poll(
                async () => {
                    const logsResponse = await apiRequest<Record<string, unknown>>(
                        request,
                        token,
                        `${webhooksPath}/${webhook!.id}/logs?page=1&page_size=20`,
                    );
                    logs = extractItems<Record<string, unknown>>(logsResponse);
                    return logs.length;
                },
                {
                    message: 'Webhook 测试失败日志应在项目作用域内持久化',
                    timeout: 10_000,
                },
            )
            .toBeGreaterThan(0);
        expect(logs[0].event_type).toBe('io.chronodesk.system.alert.v1');
        expect(logs[0].status).toBe('failed');
        expect(JSON.stringify(logs)).not.toContain(markerSecret);
    });
});
