import { test, expect } from '@playwright/test';
import { apiRequest } from './helpers/api';
import {
    authenticatePage,
    captureSystemConfig,
    e2eRunID,
    getAdminToken,
    restoreSystemConfig,
} from './helpers/testData';
import {
    assertDestructiveE2EAllowed,
    assertGlobalE2EAllowed,
} from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};
const configKey = `security.e2e_${e2eRunID()
    .replace(/[^a-z0-9]+/gu, '_')
    .slice(0, 48)}_password_min_length`;

test.describe('System Settings', () => {
    let configCreated = false;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('系统安全配置 E2E');
        assertGlobalE2EAllowed('系统安全配置 E2E');
        const token = await getAdminToken(request);
        await apiRequest(request, token, '/api/admin/configs', {
            method: 'POST',
            data: {
                key: configKey,
                value: '8',
                value_type: 'int',
                description: 'Playwright 本轮密码长度配置',
                category: 'security',
                group: 'e2e',
                is_required: false,
                is_active: true,
                default_value: '8',
            },
        });
        configCreated = true;
    });

    test.afterAll(async ({ request }) => {
        if (!configCreated) {
            return;
        }
        const token = await getAdminToken(request);
        await apiRequest(
            request,
            token,
            `/api/admin/configs/${encodeURIComponent(configKey)}`,
            { method: 'DELETE' },
        );
    });

    test('UI-017：安全配置使用修改前快照并在 finally 恢复', async ({
        page,
        request,
    }) => {
        const key = configKey;
        const original = await captureSystemConfig(request, 'security', key);
        const numericValue = Number(original.value || '8');
        const updatedValue = String(numericValue + 1);
        const expectedAfterTest = { ...original, value: updatedValue };

        try {
            await authenticatePage(page, TEST_USER);
            await page.goto('/#/system-settings/overview');
            await page
                .getByRole('heading', { name: '系统设置概览' })
                .waitFor({ timeout: 15_000 });
            await page.getByRole('tab', { name: '安全策略' }).click();

            const table = page.getByRole('table', {
                name: '系统配置列表',
                exact: true,
            });
            const row = table.getByRole('row', {
                name: new RegExp(key.replaceAll('.', '\\.'), 'u'),
            });
            const valueInput = row.getByRole('spinbutton');
            await expect(valueInput).toHaveValue(String(numericValue));
            await valueInput.fill(updatedValue);
            const update = page.waitForResponse(
                (response) =>
                    response.request().method() === 'PUT' &&
                    new URL(response.url()).pathname ===
                        `/api/admin/configs/${key}`,
            );
            await row
                .getByRole('button', {
                    name: `保存配置：${key}`,
                    exact: true,
                })
                .click();
            expect((await update).status()).toBe(200);
            await expect(page.getByText('配置已更新')).toBeVisible({
                timeout: 10_000,
            });
            await expectChineseOperations(page);

            await page
                .getByRole('main')
                .getByRole('button', { name: '刷新', exact: true })
                .click();
            const refreshedRow = table.getByRole('row', {
                name: new RegExp(key.replaceAll('.', '\\.'), 'u'),
            });
            await expect(refreshedRow.getByRole('spinbutton')).toHaveValue(
                updatedValue,
            );
        } finally {
            await restoreSystemConfig(request, original, expectedAfterTest);
        }
    });
});
