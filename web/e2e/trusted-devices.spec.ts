import { expect, test } from '@playwright/test';
import {
    DEFAULT_ADMIN,
    E2E_MARKER,
    revokeE2ETrustedDevices,
    selectDefaultProjectViaUI,
    trackTrustedDeviceByName,
    untrackE2EResource,
} from './helpers/testData';
import {
    assertDestructiveE2EAllowed,
    installBrowserMutationGuard,
} from './helpers/safety';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

const deviceName = `${E2E_MARKER}Chrome`;

test.describe('可信设备管理', () => {
    test.beforeAll(() => {
        assertDestructiveE2EAllowed('可信设备 E2E');
    });

    test.afterAll(async ({ request }) => {
        await revokeE2ETrustedDevices(request);
    });

    test('AUTH-019 AUTH-022 UI-023：可信设备仅使用 HttpOnly Cookie 并可撤销', async ({
        page,
        context,
    }) => {
        await installBrowserMutationGuard(page);
        await page.goto('/#/login');
        await page.getByLabel('邮箱').fill(DEFAULT_ADMIN.email);
        await page.getByLabel('密码').fill(DEFAULT_ADMIN.password);
        await page.getByLabel('记住此设备（免 OTP）').check();
        await page
            .getByPlaceholder('设备名称，例如：MacBook Pro')
            .fill(deviceName);
        const loginResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === '/api/auth/login',
        );
        await page
            .getByRole('button', { name: '登录系统', exact: true })
            .click();
        const successfulLogin = await loginResponse;
        expect(successfulLogin.status()).toBe(200);
        // Cookie 属于实际浏览器请求的 origin。测试环境可使用 localhost、
        // 127.0.0.1 或受控的远程 origin，不能把查询范围写死为 localhost。
        const trustedDeviceCookieURL = successfulLogin.url();
        expect(new URL(trustedDeviceCookieURL).pathname).toBe(
            '/api/auth/login',
        );
        await selectDefaultProjectViaUI(page);

        await expect
            .poll(
                async () =>
                    (
                        await context.cookies(
                            trustedDeviceCookieURL,
                        )
                    )
                        .filter(
                            (candidate) =>
                                candidate.name ===
                                'chronodesk_trusted_device',
                        )
                        .map((candidate) => ({
                            name: candidate.name,
                            httpOnly: candidate.httpOnly,
                            sameSite: candidate.sameSite,
                            path: candidate.path,
                        })),
                {
                    message: '登录响应应写入唯一的 HttpOnly 可信设备 Cookie',
                    timeout: 5_000,
                },
            )
            .toEqual([
                {
                    name: 'chronodesk_trusted_device',
                    httpOnly: true,
                    sameSite: 'Strict',
                    path: '/api/auth/login',
                },
            ]);
        expect(
            await page.evaluate(
                () => localStorage.getItem('trustedDeviceToken') === null,
            ),
        ).toBe(true);
        const deviceID = await trackTrustedDeviceByName(
            page.request,
            deviceName,
        );

        const trustedDeviceListResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'GET' &&
                new URL(response.url()).pathname ===
                    '/api/user/trusted-devices',
        );
        await page.goto('/#/account/trusted-devices');
        const trustedDeviceListURL = new URL(
            (await trustedDeviceListResponse).url(),
        );
        expect(trustedDeviceListURL.searchParams.get('page')).toBe('1');
        expect(trustedDeviceListURL.searchParams.get('page_size')).toBe('25');
        expect(trustedDeviceListURL.searchParams.get('sort_by')).toBe(
            'revoked',
        );
        expect(trustedDeviceListURL.searchParams.get('sort_order')).toBe(
            'asc',
        );
        const main = page.getByRole('main');
        await expect(
            main.getByText('可信设备管理', { exact: true }),
        ).toBeVisible({ timeout: 15_000 });
        const deviceRegion = main.getByRole('region', {
            name: `可信设备：${deviceName}`,
            exact: true,
        });
        await expect(deviceRegion).toBeVisible();
        await expect(
            deviceRegion.getByText('生效中', { exact: true }),
        ).toBeVisible();

        await deviceRegion
            .getByRole('button', { name: '撤销该设备', exact: true })
            .click();
        const confirmation = page.getByRole('dialog', {
            name: '撤销可信设备',
            exact: true,
        });
        await expect(confirmation).toContainText(
            `撤销“${deviceName}”后，该设备需要重新验证身份。确定继续吗？`,
        );
        const revokeResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'DELETE' &&
                new URL(response.url()).pathname ===
                    `/api/user/trusted-devices/${deviceID}`,
        );
        await confirmation
            .getByRole('button', { name: '确认撤销', exact: true })
            .click();
        expect((await revokeResponse).status()).toBe(200);
        await expect(
            page.getByText('设备已撤销', { exact: true }),
        ).toBeVisible({
            timeout: 10_000,
        });
        await expect(
            deviceRegion.getByText('已撤销', { exact: true }),
        ).toBeVisible({ timeout: 10_000 });
        await expect(
            deviceRegion.getByRole('button', {
                name: '撤销该设备',
                exact: true,
            }),
        ).toHaveCount(0);
        untrackE2EResource('trustedDevices', deviceID);
    });
});

test.describe('可信设备目录分页 mock', () => {
    test('mock：失败可重试且翻页请求保持有界', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-trusted-device-page',
            },
            projectA,
        );
        const queries: string[] = [];
        let failListRequests = true;
        const devices = Array.from({ length: 30 }, (_, index) => ({
            id: index + 1,
            device_name: `可信设备 ${index + 1}`,
            last_used_at: '2026-07-31T08:00:00Z',
            last_ip: '192.0.2.1',
            user_agent: 'Mock Chromium',
            expires_at: '2026-08-31T08:00:00Z',
            revoked: false,
            created_at: '2026-07-31T08:00:00Z',
            updated_at: '2026-07-31T08:00:00Z',
        }));
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
                url.pathname === '/api/user/trusted-devices' &&
                request.method() === 'GET'
            ) {
                queries.push(url.search);
                if (failListRequests) {
                    await route.fulfill({
                        status: 503,
                        contentType: 'application/json',
                        body: JSON.stringify({
                            code: 'service_unavailable',
                            msg: '服务暂时不可用，请稍后重试',
                        }),
                    });
                    return;
                }
                const pageNumber = Number(
                    url.searchParams.get('page') ?? '1',
                );
                const pageSize = Number(
                    url.searchParams.get('page_size') ?? '25',
                );
                const start = (pageNumber - 1) * pageSize;
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        items: devices.slice(start, start + pageSize),
                        total: devices.length,
                        page: pageNumber,
                        page_size: pageSize,
                        total_pages: Math.ceil(
                            devices.length / pageSize,
                        ),
                    },
                });
                return;
            }
            await fulfillJSON(route, { code: 0, data: [] });
        });

        await page.goto('/#/account/trusted-devices');
        await expect(page.getByRole('alert')).toContainText(
            '暂时不可用，请稍后重试',
        );
        failListRequests = false;
        await page
            .getByRole('button', { name: '重试', exact: true })
            .click();
        await expect(
            page.getByRole('region', {
                name: '可信设备：可信设备 1',
                exact: true,
            }),
        ).toBeVisible();
        expect(queries.at(-1)).toContain('page_size=25');
        expect(queries.at(-1)).toContain('sort_by=revoked');

        await page
            .getByRole('button', { name: /下一页|next page/iu })
            .click();
        await expect(
            page.getByRole('region', {
                name: '可信设备：可信设备 26',
                exact: true,
            }),
        ).toBeVisible();
        expect(queries.at(-1)).toContain('page=2');
        expect(queries.at(-1)).toContain('page_size=25');
    });
});
