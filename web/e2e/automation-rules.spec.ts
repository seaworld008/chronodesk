import { test, expect } from '@playwright/test';
import {
    authenticatePage,
    cleanupE2EData,
    E2E_MARKER,
    extractData,
    trackE2EResource,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

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

    test('should create an automation rule', async ({ page }) => {
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
                    '/api/admin/automation/rules',
        );
        await page.getByRole('button', { name: '保存' }).click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(201);
        const created = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof created.id).toBe('number');
        trackE2EResource('automationRules', created.id as number);

        await expect(page).toHaveURL(/#\/automation-rules/);
        await expect(page.getByText(ruleName)).toBeVisible({ timeout: 10000 });
    });
});
