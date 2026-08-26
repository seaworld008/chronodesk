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

type NavigationLevel = 'primary' | 'secondary';

type RowVisual = {
    backgroundColor: string;
    borderRadius: string;
    color: string;
    fontWeight: string;
    height: number;
};

type RailVisual = {
    color: string;
    kind: 'border' | 'before' | 'after';
    opacity: number;
    style: string;
    width: string;
};

const platformAdmin = {
    ...defaultMockIdentity,
    id: 86,
    sessionID: 'oink-navigation-interaction-states',
    email: 'oink-navigation@example.test',
    platformRole: 'platform_admin' as const,
};

const installNavigationMocks = async (
    page: Page,
    projectRole: Parameters<typeof authorizedProjectAccess>[1] =
        'project_admin',
) => {
    await installMockSession(page, platformAdmin, projectA);

    const unexpectedApiRequests: string[] = [];
    const refreshBodies: Array<string | null> = [];

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const signature = `${request.method()} ${url.pathname}`;

        if (
            request.method() === 'POST' &&
            url.pathname === '/api/auth/refresh'
        ) {
            refreshBodies.push(request.postData());
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
                    authorizedProjectAccess(projectA, projectRole),
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
                    username: 'oink-navigation-admin',
                    email: platformAdmin.email,
                    platform_role: platformAdmin.platformRole,
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                    profile: {
                        id: platformAdmin.id,
                        user_id: platformAdmin.id,
                        first_name: 'OINK',
                        last_name: 'Navigation',
                        display_name: 'OINK 导航管理员',
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
            request_id: 'oink-navigation-unexpected-api',
            retryable: false,
        }, 404);
    });

    return { unexpectedApiRequests, refreshBodies };
};

const navigationRow = (
    menu: Locator,
    level: NavigationLevel,
    id: string,
) => menu.locator(
    `[data-navigation-level="${level}"][data-navigation-id="${id}"]`,
);

const readRowVisual = (row: Locator): Promise<RowVisual> =>
    row.evaluate((element) => {
        const rowStyle = getComputedStyle(element);
        const label = element.querySelector(
            '.MuiListItemText-primary, .RaMenuItemLink-primary, .MuiTypography-root',
        ) ?? element;
        const labelStyle = getComputedStyle(label);

        return {
            backgroundColor: rowStyle.backgroundColor,
            borderRadius: rowStyle.borderRadius,
            color: labelStyle.color,
            fontWeight: labelStyle.fontWeight,
            height: element.getBoundingClientRect().height,
        };
    });

const readRailVisuals = (row: Locator): Promise<RailVisual[]> =>
    row.evaluate((element) => {
        const root = getComputedStyle(element);
        const before = getComputedStyle(element, '::before');
        const after = getComputedStyle(element, '::after');

        return [
            {
                kind: 'border' as const,
                width: root.borderLeftWidth,
                color: root.borderLeftColor,
                style: root.borderLeftStyle,
                opacity: Number.parseFloat(root.opacity),
            },
            ...[
                ['before', before],
                ['after', after],
            ].map(([kind, style]) => {
                const pseudo = style as CSSStyleDeclaration;
                return {
                    kind: kind as 'before' | 'after',
                    width: pseudo.width,
                    color: pseudo.backgroundColor,
                    style:
                        pseudo.content === 'none' ||
                        pseudo.display === 'none' ||
                        pseudo.visibility === 'hidden'
                            ? 'none'
                            : pseudo.position,
                    opacity: Number.parseFloat(pseudo.opacity),
                };
            }),
        ];
    });

const readTransform = (locator: Locator) =>
    locator.evaluate((element) => getComputedStyle(element).transform);

const readStableTransform = async (locator: Locator): Promise<string> => {
    let previous = '';
    let stableSamples = 0;

    await expect.poll(async () => {
        const current = await readTransform(locator);
        if (current === previous) {
            stableSamples += 1;
        } else {
            previous = current;
            stableSamples = 0;
        }
        return stableSamples;
    }, {
        intervals: [40, 40, 40, 40, 40, 40, 40, 40],
        timeout: 3_000,
    }).toBeGreaterThanOrEqual(2);

    return previous;
};

test.describe('OINK 左侧导航交互状态（mock）', () => {
    test('当前路由主项在首次可交互帧前完成自动展开', async ({
        page,
    }) => {
        await page.addInitScript(() => {
            const state = window as typeof window & {
                __chronodeskActiveNavigationGroupStates?: string[];
            };
            const start = () => {
                state.__chronodeskActiveNavigationGroupStates = [];
                const record = (
                    activeGroup: HTMLElement,
                    expanded: string | null,
                ) => {
                    const observation = [
                        activeGroup.dataset.navigationId,
                        expanded,
                    ].join(':');
                    const observations =
                        state.__chronodeskActiveNavigationGroupStates ?? [];
                    if (observations.at(-1) !== observation) {
                        observations.push(observation);
                    }
                };
                const capture = () => {
                    const activeGroup = document.querySelector<HTMLElement>(
                        '[data-navigation-level="primary"]' +
                        '[data-navigation-state="active"]' +
                        '[aria-expanded]',
                    );
                    if (!activeGroup) return;
                    record(
                        activeGroup,
                        activeGroup.getAttribute('aria-expanded'),
                    );
                };
                new MutationObserver((mutations) => {
                    for (const mutation of mutations) {
                        if (
                            mutation.type !== 'attributes' ||
                            mutation.attributeName !== 'aria-expanded' ||
                            !(mutation.target instanceof HTMLElement) ||
                            mutation.target.dataset.navigationState !== 'active'
                        ) {
                            continue;
                        }
                        record(mutation.target, mutation.oldValue);
                    }
                    capture();
                }).observe(
                    document.documentElement,
                    {
                        attributes: true,
                        attributeOldValue: true,
                        childList: true,
                        subtree: true,
                        attributeFilter: [
                            'aria-expanded',
                            'data-navigation-state',
                        ],
                    },
                );
                capture();
            };

            if (document.readyState === 'loading') {
                document.addEventListener('DOMContentLoaded', start, {
                    once: true,
                });
            } else {
                start();
            }
        });

        const backend = await installNavigationMocks(page);
        await page.goto('/#/');
        await expect(page.getByTestId('project-home')).toBeVisible();

        const projectOperations = navigationRow(
            page.getByRole('menu', { name: '主导航' }),
            'primary',
            'project-operations',
        );
        await expect(projectOperations).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        const observations = await page.evaluate(() => (
            window as typeof window & {
                __chronodeskActiveNavigationGroupStates?: string[];
            }
        ).__chronodeskActiveNavigationGroupStates ?? []);
        expect(
            observations.filter((observation) =>
                observation.startsWith('project-operations:'),
            ),
        ).toEqual(['project-operations:true']);
        expect(backend.unexpectedApiRequests).toEqual([]);
    });

    test('精确区分主项、功能页、hover、focus 与展开状态', async ({
        page,
    }) => {
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installNavigationMocks(page);
        await page.goto('/#/system-settings');

        const heading = page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        });
        await expect(heading).toBeVisible();

        const menu = page.getByRole('menu', { name: '主导航' });
        const systemSettings = navigationRow(
            menu,
            'primary',
            'system-settings',
        );
        const governanceCenter = navigationRow(
            menu,
            'primary',
            'governance-center',
        );
        const platformConfig = navigationRow(
            menu,
            'secondary',
            'platform-config',
        );
        const emailSettings = navigationRow(
            menu,
            'secondary',
            'platform-email-settings',
        );

        await expect(systemSettings).toHaveCount(1);
        await expect(systemSettings).toHaveAttribute(
            'data-navigation-state',
            'active',
        );
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(governanceCenter).toHaveAttribute(
            'data-navigation-state',
            'idle',
        );

        await expect(platformConfig).toHaveCount(1);
        await expect(platformConfig).toHaveAttribute(
            'data-navigation-state',
            'active',
        );
        await expect(platformConfig).toHaveAttribute(
            'aria-current',
            'page',
        );
        await expect(
            menu.locator(
                '[data-navigation-level][aria-current="page"]',
            ),
        ).toHaveCount(1);
        await expect(emailSettings).toHaveAttribute(
            'data-navigation-state',
            'idle',
        );

        const expectedPrimaryVisual: RowVisual = {
            backgroundColor: 'rgba(0, 0, 0, 0)',
            borderRadius: '10px',
            color: 'rgb(22, 34, 46)',
            fontWeight: '500',
            height: 36,
        };
        await expect.poll(() => readRowVisual(systemSettings))
            .toEqual(expectedPrimaryVisual);
        await expect.poll(() => readRowVisual(governanceCenter))
            .toEqual(expectedPrimaryVisual);

        await expect.poll(() => readRowVisual(platformConfig)).toEqual({
            backgroundColor: 'rgba(36, 95, 148, 0.1)',
            borderRadius: '10px',
            color: 'rgb(36, 95, 148)',
            fontWeight: '400',
            height: 36,
        });

        const emailVisual = await readRowVisual(emailSettings);
        expect(emailVisual.color).toBe('rgb(88, 107, 128)');
        expect(emailVisual.fontWeight).toBe('400');

        const railVisuals = await readRailVisuals(platformConfig);
        expect(
            railVisuals.some((rail) =>
                rail.width === '1px' &&
                rail.color === 'rgb(36, 95, 148)' &&
                rail.style !== 'none' &&
                rail.opacity > 0,
            ),
            JSON.stringify(railVisuals),
        ).toBe(true);

        await governanceCenter.hover();
        await expect(governanceCenter).toHaveCSS(
            'background-color',
            'rgba(22, 34, 46, 0.06)',
        );
        await heading.hover();
        await expect(governanceCenter).toHaveCSS(
            'background-color',
            'rgba(0, 0, 0, 0)',
        );

        await emailSettings.hover();
        await expect(emailSettings).toHaveCSS(
            'background-color',
            'rgba(22, 34, 46, 0.06)',
        );
        await expect.poll(() => readRowVisual(emailSettings)).toMatchObject({
            color: 'rgb(22, 34, 46)',
        });
        await heading.hover();
        await expect(emailSettings).toHaveCSS(
            'background-color',
            'rgba(0, 0, 0, 0)',
        );
        await expect.poll(() => readRowVisual(emailSettings)).toMatchObject({
            color: 'rgb(88, 107, 128)',
        });

        await page.keyboard.press('Tab');
        await governanceCenter.focus();
        await expect(governanceCenter).toBeFocused();
        await expect.poll(async () => governanceCenter.evaluate((element) => {
            const style = getComputedStyle(element);
            if (style.outlineStyle === 'none') return 0;
            return Number.parseFloat(style.outlineWidth);
        })).toBeGreaterThanOrEqual(2);

        const systemChevron = systemSettings.locator('svg').last();
        await expect(systemChevron).toBeVisible();
        const expandedTransform = await readStableTransform(systemChevron);

        await systemSettings.click();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'false',
        );
        const collapsedTransform = await readStableTransform(systemChevron);
        expect(collapsedTransform).not.toBe(expandedTransform);

        await systemSettings.click();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect.poll(() => readTransform(systemChevron))
            .toBe(expandedTransform);
        await expect(platformConfig).toBeVisible();

        const governanceChevron = governanceCenter.locator('svg').last();
        const governanceCollapsedTransform =
            await readStableTransform(governanceChevron);
        await governanceCenter.click();
        await expect(governanceCenter).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        const governanceExpandedTransform =
            await readStableTransform(governanceChevron);
        expect(governanceExpandedTransform)
            .not.toBe(governanceCollapsedTransform);

        await expect(systemSettings).toHaveAttribute(
            'data-navigation-state',
            'active',
        );
        await expect(platformConfig).toHaveAttribute(
            'data-navigation-state',
            'active',
        );
        await expect(platformConfig).toHaveAttribute(
            'aria-current',
            'page',
        );
        await expect(governanceCenter).toHaveAttribute(
            'data-navigation-state',
            'idle',
        );
        await expect(
            menu.locator(
                '[data-navigation-level][aria-current="page"]',
            ),
        ).toHaveCount(1);

        await emailSettings.click();
        await expect(page.getByRole('heading', {
            name: '平台邮件设置',
            level: 1,
        })).toBeVisible();
        await expect(emailSettings).toHaveAttribute(
            'aria-current',
            'page',
        );
        await expect(platformConfig).not.toHaveAttribute(
            'aria-current',
            'page',
        );

        await systemSettings.click();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'false',
        );
        await page.goBack();
        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(platformConfig).toHaveAttribute(
            'aria-current',
            'page',
        );
        await expect(emailSettings).not.toHaveAttribute(
            'aria-current',
            'page',
        );

        await systemSettings.click();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'false',
        );
        await page.reload();
        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible();
        await expect(systemSettings).toHaveAttribute(
            'aria-expanded',
            'true',
        );
        await expect(platformConfig).toHaveAttribute(
            'aria-current',
            'page',
        );
        expect(backend.refreshBodies).toEqual([null]);
        await expect.poll(() => page.evaluate(() => ({
            token: localStorage.getItem('token'),
            refreshToken: localStorage.getItem('refreshToken'),
        }))).toEqual({ token: null, refreshToken: null });

        expect(backend.unexpectedApiRequests).toEqual([]);
        browserHealth.assertClean();
    });

    test('权限过滤后的单子项直达入口仍使用当前功能页蓝色', async ({
        page,
    }) => {
        const browserHealth = monitorBrowserHealth(page);
        const backend = await installNavigationMocks(page, 'requester');
        await page.goto('/#/agent-collaboration');
        await expect(page.getByRole('heading', {
            name: 'AI 人机协作',
            level: 1,
        })).toBeVisible();

        const menu = page.getByRole('menu', { name: '主导航' });
        const collaboration = navigationRow(
            menu,
            'primary',
            'agent-collaboration',
        );
        await expect(collaboration).toHaveAttribute(
            'data-navigation-state',
            'active',
        );
        await expect(collaboration).toHaveAttribute(
            'aria-current',
            'page',
        );
        await expect.poll(() => readRowVisual(collaboration)).toEqual({
            backgroundColor: 'rgba(36, 95, 148, 0.1)',
            borderRadius: '10px',
            color: 'rgb(36, 95, 148)',
            fontWeight: '500',
            height: 36,
        });
        await expect(menu.getByRole('menuitem', {
            name: '智能运营',
            exact: true,
        })).toHaveCount(0);
        expect(backend.unexpectedApiRequests).toEqual([]);
        browserHealth.assertClean();
    });
});
