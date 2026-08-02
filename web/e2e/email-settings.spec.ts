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
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

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

test.describe('Email Settings 配置错误反馈（mock）', () => {
    test('SMTP 主机 400 错误持久显示并关联输入字段', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                platformRole: 'platform_admin',
                sessionID: 'e2e-email-config-validation',
            },
            projectA,
        );

        const emailConfig = {
            id: 1,
            created_at: '2026-08-02T08:00:00Z',
            updated_at: '2026-08-02T08:00:00Z',
            email_verification_enabled: false,
            smtp_host: 'smtp.example.com',
            smtp_port: 587,
            smtp_username: 'chronodesk',
            smtp_use_tls: true,
            smtp_use_ssl: false,
            from_email: 'chronodesk@example.test',
            from_name: 'ChronoDesk',
            welcome_email_subject: '欢迎使用 ChronoDesk',
            welcome_email_template: '欢迎',
            otp_email_subject: 'ChronoDesk 邮箱验证码',
            otp_email_template: '验证码',
            is_active: true,
            is_configured: true,
            can_send_email: true,
            updated_by_id: 42,
        };
        const submittedHosts: string[] = [];
        const invalidHostMessage =
            'SMTP 主机必须是合法的主机名、IPv4 或未加方括号的 IPv6 地址，且不能包含协议、端口或路径';

        await page.route('**/api/**', async (route) => {
            const request = route.request();
            const url = new URL(request.url());
            if (url.pathname === '/api/projects') {
                await fulfillJSON(route, {
                    code: 0,
                    data: [
                        authorizedProjectAccess(
                            projectA,
                            'project_admin',
                        ),
                    ],
                });
                return;
            }
            if (
                url.pathname === '/api/platform/email-config'
                && request.method() === 'GET'
            ) {
                await fulfillJSON(route, { code: 0, data: emailConfig });
                return;
            }
            if (
                url.pathname === '/api/platform/email-config'
                && request.method() === 'PUT'
            ) {
                const payload =
                    request.postDataJSON() as Record<string, unknown>;
                const host = String(payload.smtp_host ?? '');
                submittedHosts.push(host);
                if (host.startsWith('smtp://')) {
                    await fulfillJSON(
                        route,
                        {
                            code: 400,
                            msg: 'invalid_smtp_host',
                            data: invalidHostMessage,
                        },
                        400,
                    );
                    return;
                }
                emailConfig.smtp_host = host;
                await fulfillJSON(route, { code: 0, data: emailConfig });
                return;
            }
            await fulfillJSON(route, { code: 0, data: [] });
        });

        await page.goto('/#/email-settings');
        await expect(
            page.getByRole('switch', {
                name: '新注册用户必须验证邮箱',
            }),
        ).toBeVisible();
        await expect(page.getByText(
            '此开关仅控制注册验证与未验证账号登录；邮件发送能力由 SMTP 配置完整性和状态决定。',
        )).toBeVisible();

        const hostInput = page.getByLabel('SMTP 主机');
        await expect(hostInput).toHaveValue('smtp.example.com');
        await hostInput.fill('smtp://127.0.0.1');
        await page.getByRole('button', { name: '保存配置' }).click();

        const persistentError =
            page.getByTestId('email-config-save-error');
        await expect(persistentError).toContainText(invalidHostMessage);
        await expect(hostInput).toHaveAttribute('aria-invalid', 'true');
        const helperTextID =
            await hostInput.getAttribute('aria-describedby');
        expect(helperTextID).toBeTruthy();
        await expect(page.locator(`#${helperTextID}`))
            .toHaveText(invalidHostMessage);

        await hostInput.fill('127.0.0.1');
        await expect(persistentError).toBeHidden();
        await expect(hostInput).toHaveAttribute('aria-invalid', 'false');
        await page.getByRole('button', { name: '保存配置' }).click();
        await expect(page.getByText('邮件配置保存成功')).toBeVisible();
        expect(submittedHosts).toEqual([
            'smtp://127.0.0.1',
            '127.0.0.1',
        ]);
    });
});
