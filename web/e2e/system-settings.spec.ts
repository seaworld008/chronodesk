import { test, expect } from '@playwright/test';
import { loginViaUI } from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('System Settings', () => {
    test('should update security config and restore', async ({ page }) => {
        await loginViaUI(page, TEST_USER);

        await page.goto('/#/system-settings/overview');
        await page.getByRole('heading', { name: '系统设置概览' }).waitFor({ timeout: 15000 });
        await page.getByRole('tab', { name: '安全策略' }).click();

        const row = page.locator('tr', { hasText: 'security.password_min_length' });
        const valueInput = row.getByRole('spinbutton');
        const originalValue = await valueInput.inputValue();
        const numericValue = Number(originalValue || '8');
        const updatedValue = String(numericValue + 1);

        await valueInput.fill(updatedValue);
        await row.getByRole('button', { name: '保存' }).click();
        await expect(page.getByText('配置已更新')).toBeVisible({ timeout: 10000 });

        await page.getByRole('button', { name: '刷新' }).click();
        const refreshedRow = page.locator('tr', { hasText: 'security.password_min_length' });
        await expect(refreshedRow.getByRole('spinbutton')).toHaveValue(updatedValue);

        await refreshedRow.getByRole('spinbutton').fill(String(numericValue));
        await refreshedRow.getByRole('button', { name: '保存' }).click();
        await expect(page.getByText('配置已更新')).toBeVisible({ timeout: 10000 });
    });
});
