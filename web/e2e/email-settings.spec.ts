import { test, expect } from '@playwright/test';
import { authenticatePage, cleanupE2EData, E2E_PREFIX } from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Email Settings', () => {
    test.afterAll(async ({ request }) => {
        await cleanupE2EData(request, {
            tickets: false,
            automationRules: false,
            notifications: false,
            users: false,
        });
    });

    test('should save email config with test data', async ({ page }) => {
        await authenticatePage(page, TEST_USER);

        await page.goto('/#/email-settings');

        const enableSwitch = page.getByLabel('启用邮件功能');
        if (!(await enableSwitch.isChecked())) {
            await enableSwitch.click();
        }

        const suffix = Date.now().toString();
        await page.getByLabel('SMTP 主机').fill('smtp.test.local');
        await page.getByLabel('SMTP 端口').fill('587');
        await page.getByLabel('SMTP 用户名').fill(`test_user_${suffix}`);
        await page.getByLabel('SMTP 密码（留空则不修改）').fill('test_pass_20260203');
        await page.getByLabel('发件人邮箱').fill('test@example.com');
        await page.getByLabel('发件人名称').fill(`${E2E_PREFIX}工单系统-${suffix}`);

        await page.getByRole('button', { name: '保存配置' }).click();

        await expect(page.getByText('邮件配置保存成功')).toBeVisible({ timeout: 10000 });
    });
});
