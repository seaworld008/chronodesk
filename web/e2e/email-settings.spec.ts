import { test, expect } from '@playwright/test';
import {
    assertEmailConfigMutationSafe,
    authenticatePage,
    captureEmailConfig,
    E2E_MARKER,
    restoreEmailConfig,
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

test.describe('Email Settings', () => {
    test.beforeAll(() => {
        assertDestructiveE2EAllowed('邮件配置 E2E');
        assertGlobalE2EAllowed('邮件配置 E2E');
    });

    test('UI-018：邮件配置使用快照恢复且不改写不可回显秘密', async ({
        page,
        request,
    }) => {
        const original = await captureEmailConfig(request);
        assertEmailConfigMutationSafe(original, false);
        const testFromName = `${E2E_MARKER}邮件发送方`;
        const expectedAfterTest = {
            ...original,
            from_name: testFromName,
        };

        try {
            await authenticatePage(page, TEST_USER);
            await page.goto('/#/email-settings');
            await expect(page.getByLabel('发件人名称')).toHaveValue(
                original.from_name,
                { timeout: 15_000 },
            );
            await expect(
                page.getByLabel('SMTP 密码（留空则不修改）'),
            ).toHaveValue('');

            await page.getByLabel('发件人名称').fill(testFromName);
            const save = page.waitForResponse(
                (response) =>
                    response.request().method() === 'PUT' &&
                    new URL(response.url()).pathname ===
                        '/api/platform/email-config',
            );
            await page.getByRole('button', { name: '保存配置' }).click();
            expect((await save).status()).toBe(200);
            await expect(page.getByText('邮件配置保存成功')).toBeVisible({
                timeout: 10_000,
            });
            await expectChineseOperations(page);
            const saved = await captureEmailConfig(request);
            expect(saved.from_name).toBe(testFromName);
        } finally {
            await restoreEmailConfig(
                request,
                original,
                expectedAfterTest,
            );
        }
    });
});
