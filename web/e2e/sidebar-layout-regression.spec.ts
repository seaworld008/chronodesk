import { expect, test, type Locator, type Page } from '@playwright/test';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    fulfillMockSessionRefresh,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';
import { monitorBrowserHealth } from './helpers/browserAudit';

const sidebarDefaultWidth = 240;
const sidebarMinWidth = 216;
const sidebarMaxWidth = 360;
const sidebarKeyboardStep = 8;

const platformAdmin = {
    ...defaultMockIdentity,
    id: 87,
    sessionID: 'sidebar-layout-regression',
    email: 'sidebar-layout@example.test',
    platformRole: 'platform_admin' as const,
};

const installSidebarMocks = async (page: Page) => {
    await installMockSession(page, platformAdmin, projectA);

    const unexpectedApiRequests: string[] = [];

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const signature = `${request.method()} ${url.pathname}`;

        if (
            request.method() === 'POST' &&
            url.pathname === '/api/auth/refresh'
        ) {
            await fulfillMockSessionRefresh(route, platformAdmin);
            return;
        }

        if (
            request.method() === 'GET' &&
            url.pathname === '/api/projects'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: [
                    authorizedProjectAccess(projectA, 'project_admin'),
                ],
            });
            return;
        }

        if (
            request.method() === 'GET' &&
            url.pathname === '/api/auth/me'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    id: platformAdmin.id,
                    username: 'sidebar-layout-admin',
                    email: platformAdmin.email,
                    platform_role: platformAdmin.platformRole,
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                    profile: {
                        id: platformAdmin.id,
                        user_id: platformAdmin.id,
                        first_name: 'Sidebar',
                        last_name: 'Layout',
                        display_name: '侧栏布局管理员',
                        avatar: '',
                        phone: '',
                        department: '',
                        position: '',
                        timezone: 'Asia/Shanghai',
                        language: 'zh-CN',
                        created_at: '2026-08-12T00:00:00Z',
                        updated_at: '2026-08-12T00:00:00Z',
                    },
                },
            });
            return;
        }

        if (
            request.method() === 'GET' &&
            url.pathname ===
                '/api/projects/OPS/agent-collaboration/runs'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    items: [],
                    total: 0,
                    page: 1,
                    page_size: 25,
                    total_pages: 0,
                },
            });
            return;
        }

        if (
            request.method() === 'GET' &&
            url.pathname === '/api/platform/configs'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    items: [],
                    total: 0,
                    page: 1,
                    page_size: 25,
                    total_pages: 0,
                },
            });
            return;
        }

        if (
            request.method() === 'GET' &&
            url.pathname === '/api/platform/email-config'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    email_verification_enabled: false,
                    smtp_host: '',
                    smtp_port: 587,
                    smtp_username: '',
                    smtp_use_tls: true,
                    smtp_use_ssl: false,
                    from_email: '',
                    from_name: 'ChronoDesk',
                    welcome_email_subject: '欢迎使用 ChronoDesk',
                    welcome_email_template: '',
                    otp_email_subject: '验证码',
                    otp_email_template: '',
                    is_configured: false,
                    can_send_email: false,
                },
            });
            return;
        }

        unexpectedApiRequests.push(signature);
        await fulfillJSON(route, {
            type: 'about:blank',
            title: 'Unexpected mocked API request',
            status: 404,
            detail: signature,
            code: 'not_found',
            request_id: 'sidebar-layout-unexpected-api',
            retryable: false,
        }, 404);
    });

    return { unexpectedApiRequests };
};

const expectSidebarWidth = async (
    sidebar: Locator,
    expectedWidth: number,
) => {
    await expect(sidebar).toHaveAttribute(
        'data-sidebar-width',
        String(expectedWidth),
    );
    await expect.poll(async () => {
        const box = await sidebar.boundingBox();
        return box === null ? null : Math.round(box.width);
    }).toBe(expectedWidth);
};

test.describe('左侧导航布局回归（mock）', () => {
    test('桌面收起无乱码，且宽度支持键盘、拖拽与持久化', async ({
        page,
    }) => {
        await page.setViewportSize({ width: 1440, height: 900 });
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installSidebarMocks(page);
        await page.goto('/#/system-settings');

        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible();

        const sidebar = page.getByTestId('desktop-sidebar');
        const menu = page.getByRole('menu', {
            name: '主导航',
            exact: true,
        });
        const systemSettings = menu.locator(
            '[data-navigation-level="primary"]' +
            '[data-navigation-id="system-settings"]',
        );
        const platformConfig = menu.locator(
            '[data-navigation-level="secondary"]' +
            '[data-navigation-id="platform-config"]',
        );
        const resizeHandle = page.getByRole('separator', {
            name: '调整主导航宽度',
            exact: true,
        });

        await expectSidebarWidth(sidebar, sidebarDefaultWidth);
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(platformConfig).toBeVisible();
        await expect(resizeHandle).toHaveAttribute(
            'aria-orientation',
            'vertical',
        );
        await expect(resizeHandle).toHaveAttribute(
            'aria-controls',
            'chronodesk-primary-navigation',
        );
        await expect(resizeHandle).toHaveAttribute(
            'aria-valuemin',
            String(sidebarMinWidth),
        );
        await expect(resizeHandle).toHaveAttribute(
            'aria-valuemax',
            String(sidebarMaxWidth),
        );
        await expect(resizeHandle).toHaveAttribute(
            'aria-valuenow',
            String(sidebarDefaultWidth),
        );

        await page.getByRole('button', {
            name: '关闭菜单',
            exact: true,
        }).click();
        await expectSidebarWidth(sidebar, 56);
        await expect(
            menu.locator('[data-navigation-level="secondary"]'),
        ).toHaveCount(0);
        await expect(resizeHandle).toHaveCount(0);
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'false',
        );

        const collapsedMetrics = await menu.evaluate((element) => {
            const menuRect = element.getBoundingClientRect();
            const visibleLabels = [
                ...element.querySelectorAll<HTMLElement>(
                    '[data-navigation-level] .MuiTypography-root,' +
                    '[data-navigation-level] .MuiListItemText-root',
                ),
            ]
                .filter((label) => {
                    const style = getComputedStyle(label);
                    const rect = label.getBoundingClientRect();
                    return (
                        style.display !== 'none' &&
                        style.visibility !== 'hidden' &&
                        Number.parseFloat(style.opacity) > 0 &&
                        rect.width > 0 &&
                        rect.height > 0
                    );
                })
                .map((label) => label.textContent?.trim() ?? '');
            const overflowRows = [
                ...element.querySelectorAll<HTMLElement>(
                    '[data-navigation-level="primary"]',
                ),
            ]
                .filter((row) => {
                    const rect = row.getBoundingClientRect();
                    return (
                        rect.left < menuRect.left - 1 ||
                        rect.right > menuRect.right + 1
                    );
                })
                .map((row) => row.dataset.navigationId ?? 'unknown');

            return {
                clientWidth: element.clientWidth,
                scrollWidth: element.scrollWidth,
                visibleLabels,
                overflowRows,
            };
        });
        expect(collapsedMetrics.visibleLabels).toEqual([]);
        expect(collapsedMetrics.overflowRows).toEqual([]);
        expect(collapsedMetrics.scrollWidth).toBeLessThanOrEqual(
            collapsedMetrics.clientWidth,
        );

        await systemSettings.click();
        await expectSidebarWidth(sidebar, sidebarDefaultWidth);
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(platformConfig).toBeVisible();
        await expect(resizeHandle).toBeVisible();

        await resizeHandle.focus();
        await expect(resizeHandle).toBeFocused();
        await page.keyboard.press('Home');
        await expect(resizeHandle).toHaveAttribute(
            'aria-valuenow',
            String(sidebarMinWidth),
        );
        await expectSidebarWidth(sidebar, sidebarMinWidth);
        await page.keyboard.press('ArrowLeft');
        await expectSidebarWidth(sidebar, sidebarMinWidth);

        await page.keyboard.press('ArrowRight');
        await expectSidebarWidth(
            sidebar,
            sidebarMinWidth + sidebarKeyboardStep,
        );
        await page.keyboard.press('End');
        await expectSidebarWidth(sidebar, sidebarMaxWidth);
        await page.keyboard.press('ArrowRight');
        await expectSidebarWidth(sidebar, sidebarMaxWidth);

        await resizeHandle.dblclick();
        await expectSidebarWidth(sidebar, sidebarDefaultWidth);

        const handleBox = await resizeHandle.boundingBox();
        expect(handleBox).not.toBeNull();
        const pointerX = handleBox!.x + handleBox!.width / 2;
        const pointerY = handleBox!.y + Math.min(120, handleBox!.height / 2);
        await page.mouse.move(pointerX, pointerY);
        await page.mouse.down();
        await page.mouse.move(pointerX + 48, pointerY);
        await page.mouse.up();
        await expectSidebarWidth(sidebar, sidebarDefaultWidth + 48);
        await expect(resizeHandle).toHaveAttribute(
            'aria-valuenow',
            String(sidebarDefaultWidth + 48),
        );

        await page.reload();
        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible();
        await expectSidebarWidth(sidebar, sidebarDefaultWidth + 48);
        await expect(page.getByRole('separator', {
            name: '调整主导航宽度',
            exact: true,
        })).toHaveAttribute(
            'aria-valuenow',
            String(sidebarDefaultWidth + 48),
        );

        expect(backend.unexpectedApiRequests).toEqual([]);
        browserHealth.assertClean();
    });

    test('移动端使用临时抽屉且不渲染宽度调节器', async ({ page }) => {
        await page.setViewportSize({ width: 390, height: 844 });
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installSidebarMocks(page);
        await page.goto('/#/system-settings');

        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible();
        await expect(page.getByTestId('desktop-sidebar')).toHaveCount(0);
        await expect(page.getByRole('separator', {
            name: '调整主导航宽度',
            exact: true,
        })).toHaveCount(0);

        await page.getByRole('button', {
            name: '打开菜单',
            exact: true,
        }).click();
        await expect(page.getByTestId('mobile-sidebar')).toBeVisible();
        await expect(page.getByRole('menu', {
            name: '主导航',
            exact: true,
        })).toBeVisible();
        await expect(page.getByRole('separator', {
            name: '调整主导航宽度',
            exact: true,
        })).toHaveCount(0);

        await page.keyboard.press('Escape');
        await expect(page.getByRole('menu', {
            name: '主导航',
            exact: true,
        })).toBeHidden();
        expect(backend.unexpectedApiRequests).toEqual([]);
        browserHealth.assertClean();
    });
});
