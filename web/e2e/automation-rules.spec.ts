import { test, expect } from '@playwright/test';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

const login = async (page: import('@playwright/test').Page) => {
    await page.goto('/#/login');
    await page.getByLabel('邮箱').fill(TEST_USER.email);
    await page.getByLabel('密码').fill(TEST_USER.password);
    await page.getByRole('button', { name: '登录系统' }).click();
    await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
};

test.describe('Automation Rules', () => {
    test('should create an automation rule', async ({ page }) => {
        await login(page);

        await page.goto('/#/automation-rules');
        await page.getByRole('link', { name: /create/i }).click();

        await page.getByLabel('名称').fill('E2E 自动化规则 20260203');
        await page.getByLabel('描述').fill('Playwright E2E 创建自动化规则');

        await page.getByLabel('规则类型').click();
        await page.getByRole('option', { name: '自动分配' }).click();

        await page.getByLabel('触发事件').click();
        await page.getByRole('option', { name: '工单创建' }).click();

        await page.getByLabel('条件 (JSON数组)').fill('[]');
        await page.getByLabel('动作 (JSON数组)').fill('[{"type":"assign","params":{"user_id":1}}]');

        await page.getByRole('button', { name: 'Save' }).click();

        await expect(page).toHaveURL(/#\/automation-rules/);
        await expect(page.getByText('E2E 自动化规则 20260203')).toBeVisible({ timeout: 10000 });
    });
});
