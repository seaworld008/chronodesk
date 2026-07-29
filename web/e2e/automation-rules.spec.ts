import { test, expect } from '@playwright/test';
import { authenticatePage, cleanupE2EData, E2E_PREFIX } from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Automation Rules', () => {
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

        const ruleName = `${E2E_PREFIX}自动化规则-${Date.now()}`;
        await page.getByLabel('名称').fill(ruleName);
        await page.getByLabel('描述').fill('Playwright E2E 创建自动化规则');

        await page.getByLabel('规则类型').click();
        await page.getByRole('option', { name: '自动分配' }).click();

        await page.getByLabel('触发事件').click();
        await page.getByRole('option', { name: '工单创建' }).click();

        await page.getByLabel('条件（JSON 数组）').fill('[]');
        await page.getByLabel('动作（JSON 数组）').fill('[{"type":"assign","params":{"user_id":1}}]');

        await page.getByRole('button', { name: '保存' }).click();

        await expect(page).toHaveURL(/#\/automation-rules/);
        await expect(page.getByText(ruleName)).toBeVisible({ timeout: 10000 });
    });
});
