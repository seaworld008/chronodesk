import { expect, test, type Locator, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {
    expectChineseOperations,
    monitorBrowserHealth,
    waitForPrimaryPage,
} from './helpers/browserAudit';
import {
    authenticatePage,
    createTicket,
    deleteTicket,
    E2E_MARKER,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';

type PrimaryPageCase = {
    caseID: string;
    name: string;
    path: string;
    navigation?: {
        group: string;
        leaf: string;
    };
    ready: (page: Page) => Locator;
};

const mainContent = (page: Page) => page.getByRole('main');

const navigationTree = [
    { label: '工作台', children: ['运营大屏', '跨项目工作台'] },
    {
        label: '项目运营',
        children: ['项目概览', '工单管理', '知识库', '项目通知'],
    },
    {
        label: '智能运营',
        children: ['人机协作', '自动化', '智能体管理'],
    },
    { label: '集成中心', children: ['Webhook', '集成运行'] },
    {
        label: '项目设置',
        children: [
            '基本信息',
            '项目成员',
            '建单配置',
            'SLA 策略',
            '受理队列',
            '工单模板',
            '快捷回复',
            '通知与外发',
        ],
    },
    {
        label: '治理中心',
        children: [
            '平台工作台',
            '项目治理',
            '平台身份与访问',
            '审计中心',
        ],
    },
    { label: '系统设置', children: ['公共配置', '邮件外发'] },
] as const;

type NavigationViewport = {
    label: string;
    width: number;
    height: number;
    expectOverflow?: boolean;
};

const cssViewports: NavigationViewport[] = [
    { label: 'CSS 视口 1440×900', width: 1440, height: 900 },
    { label: 'CSS 视口 1280×800', width: 1280, height: 800 },
    { label: 'CSS 视口 1024×720', width: 1024, height: 720 },
    { label: 'CSS 视口 820×640', width: 820, height: 640 },
    {
        label: 'CSS 视口 820×360（短高度）',
        width: 820,
        height: 360,
        expectOverflow: true,
    },
];

const twoHundredPercentReflowViewports: NavigationViewport[] = [
    {
        label: '1440×900 物理视口的 200% 等效重排（720×450 CSS）',
        width: 720,
        height: 450,
    },
    {
        label: '1280×800 物理视口的 200% 等效重排（640×400 CSS）',
        width: 640,
        height: 400,
    },
    {
        label: '1024×720 物理视口的 200% 等效重排（512×360 CSS）',
        width: 512,
        height: 360,
    },
    {
        label: '820×640 物理视口的 200% 等效重排（410×320 CSS）',
        width: 410,
        height: 320,
    },
];

const primaryPages: PrimaryPageCase[] = [
    {
        caseID: 'UI-003',
        name: '工单仪表盘',
        path: '/#/',
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '工单运营总览',
                exact: true,
            }),
    },
    {
        caseID: 'UI-004',
        name: '工单列表',
        path: '/#/tickets',
        ready: (page) =>
            mainContent(page)
                .getByRole('table', {
                    name: '工单列表',
                    exact: true,
                })
                .or(
                    mainContent(page).getByRole('heading', {
                        name: '暂无工单',
                        exact: true,
                    }),
                ),
    },
    {
        caseID: 'UI-014',
        name: '通知中心',
        path: '/#/notifications',
        ready: (page) =>
            mainContent(page)
                .getByPlaceholder('搜索通知', { exact: true })
                .or(
                    mainContent(page).getByRole('heading', {
                        name: '暂无通知',
                        exact: true,
                    }),
                ),
    },
    {
        caseID: 'UI-KNOWLEDGE',
        name: '项目知识库',
        path: '/#/knowledge',
        navigation: {
            group: '项目运营',
            leaf: '知识库',
        },
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '知识库',
                exact: true,
                level: 1,
            }),
    },
    {
        caseID: 'UI-KNOWLEDGE-PERMISSION',
        name: '项目成员与知识贡献授权',
        path: '/#/project-memberships',
        navigation: {
            group: '项目设置',
            leaf: '项目成员',
        },
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '项目成员管理',
                exact: true,
            }),
    },
    {
        caseID: 'UI-013',
        name: '平台用户管理',
        path: '/#/users',
        navigation: {
            group: '治理中心',
            leaf: '平台身份与访问',
        },
        ready: (page) =>
            mainContent(page).getByPlaceholder('搜索用户', { exact: true }),
    },
    {
        caseID: 'UI-015',
        name: '自动化规则',
        path: '/#/automation-rules',
        ready: (page) =>
            mainContent(page).getByRole('link', {
                name: '新建',
                exact: true,
            }),
    },
    {
        caseID: 'UI-016',
        name: '自动化日志',
        path: '/#/automation-logs',
        ready: (page) =>
            mainContent(page)
                .getByRole('separator', {
                    name: /调整“规则”列宽/,
                })
                .or(
                    mainContent(page).getByText('暂无自动化日志', {
                        exact: true,
                    }),
                ),
    },
    {
        caseID: 'UI-017',
        name: '系统设置',
        path: '/#/system-settings',
        navigation: {
            group: '系统设置',
            leaf: '公共配置',
        },
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '平台公共配置',
                exact: true,
            }),
    },
    {
        caseID: 'UI-018',
        name: '邮件设置',
        path: '/#/system-settings/email',
        navigation: {
            group: '系统设置',
            leaf: '邮件外发',
        },
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '平台邮件设置',
                exact: true,
            }),
    },
    {
        caseID: 'P1-PLATFORM-AUDIT',
        name: '平台审计探索器',
        path: '/#/platform/audit',
        navigation: {
            group: '治理中心',
            leaf: '审计中心',
        },
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '平台审计探索器',
                exact: true,
            }),
    },
    {
        caseID: 'UI-019',
        name: 'Webhook 设置',
        path: '/#/webhook-settings',
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: 'Webhook 集成',
                exact: true,
            }),
    },
    {
        caseID: 'UI-020',
        name: 'Agent 控制中心',
        path: '/#/agent-control',
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: 'AI 智能体控制中心',
                exact: true,
            }),
    },
    {
        caseID: 'UI-023',
        name: '可信设备',
        path: '/#/account/trusted-devices',
        ready: (page) =>
            mainContent(page).getByRole('heading', {
                name: '可信设备',
                exact: true,
                level: 1,
            }),
    },
];

const expectSidebarUsable = async (
    page: Page,
    viewport: NavigationViewport,
) => {
    const openMenu = page.getByRole('button', {
        name: '打开菜单',
        exact: true,
    });
    if (await openMenu.isVisible()) {
        await openMenu.click();
    }

    const menu = page.getByRole('menu');
    // React Admin 的 role="main" 是同时包住 Sidebar 与页面内容的布局外壳。
    // #main-content 是跳过导航链接使用的稳定内容目标，几何断言应测量它。
    const content = page.locator('#main-content');
    await expect(menu).toHaveCount(1);
    await expect(menu).toBeVisible();
    await expect(content).toBeVisible();
    await menu.evaluate(async (element) => {
        let scrollContainer: HTMLElement | null = element as HTMLElement;
        while (scrollContainer) {
            if (
                /auto|scroll/u.test(
                    getComputedStyle(scrollContainer).overflowY,
                )
            ) {
                break;
            }
            scrollContainer = scrollContainer.parentElement;
        }

        await new Promise<void>((resolve) => {
            requestAnimationFrame(() =>
                requestAnimationFrame(() => resolve()),
            );
        });
        const animations = new Set([
            ...(scrollContainer?.getAnimations() ?? []),
            ...element.getAnimations({ subtree: true }),
        ]);
        await Promise.all(
            [...animations].map((animation) =>
                animation.finished.catch(() => undefined),
            ),
        );
    });
    await expect
        .poll(
            () =>
                menu.evaluate((element) => {
                    let candidate: HTMLElement | null =
                        element as HTMLElement;
                    while (candidate) {
                        const style = getComputedStyle(candidate);
                        if (/auto|scroll/u.test(style.overflowY)) {
                            const containerBox =
                                candidate.getBoundingClientRect();
                            const menuBox = element.getBoundingClientRect();
                            return (
                                containerBox.left >= -1 &&
                                containerBox.right <= window.innerWidth + 1 &&
                                menuBox.left >= containerBox.left - 1 &&
                                menuBox.right <= containerBox.right + 1
                            );
                        }
                        candidate = candidate.parentElement;
                    }
                    return false;
                }),
            {
                message: `${viewport.label} 下侧栏必须完成展开并进入视口`,
            },
        )
        .toBe(true);

    const scrollMetrics = await menu.evaluate((element) => {
        let candidate: HTMLElement | null = element as HTMLElement;
        while (candidate) {
            const style = getComputedStyle(candidate);
            if (/auto|scroll/u.test(style.overflowY)) {
                const rect = candidate.getBoundingClientRect();
                return {
                    overflowX: style.overflowX,
                    overflowY: style.overflowY,
                    clientHeight: candidate.clientHeight,
                    scrollHeight: candidate.scrollHeight,
                    left: rect.left,
                    right: rect.right,
                    top: rect.top,
                    bottom: rect.bottom,
                };
            }
            candidate = candidate.parentElement;
        }
        return null;
    });
    expect(scrollMetrics, '菜单必须位于可纵向滚动的导航容器内').not.toBeNull();
    expect(scrollMetrics!.overflowX).toBe('hidden');
    expect(scrollMetrics!.overflowY).toMatch(/auto|scroll/);
    if (viewport.expectOverflow) {
        expect(
            scrollMetrics!.scrollHeight,
            `${viewport.label} 下导航内容必须真实溢出并可滚动`,
        ).toBeGreaterThan(scrollMetrics!.clientHeight);
    }

    for (const group of navigationTree) {
        if (group.children.length === 0) {
            const leaf = menu.getByRole('menuitem', {
                name: group.label,
                exact: true,
            });
            await leaf.scrollIntoViewIfNeeded();
            await expect(leaf).toBeVisible();
            continue;
        }
        const toggle = menu.getByRole('menuitem', {
            name: new RegExp(`^${group.label}`),
        });
        await toggle.scrollIntoViewIfNeeded();
        await expect(toggle).toBeVisible();
        await expect(toggle).toHaveAttribute('aria-controls');
        if ((await toggle.getAttribute('aria-expanded')) !== 'true') {
            await toggle.click();
        }
        await expect(toggle).toHaveAttribute('aria-expanded', 'true');
        const contentID = await toggle.getAttribute('aria-controls');
        if (!contentID) {
            throw new Error(`${group.label} 导航组缺少 aria-controls`);
        }
        await expect(page.locator(`#${contentID}`)).toHaveClass(
            /MuiCollapse-entered/u,
        );

        for (const itemName of group.children) {
            const item = menu.getByRole('menuitem', {
                name: itemName,
                exact: true,
            });
            await item.scrollIntoViewIfNeeded();
            await expect(item).toBeVisible();
            await expect(item).toBeInViewport({ ratio: 0.5 });
            const itemBox = await item.boundingBox();
            expect(itemBox, `${itemName} 必须具有可交互矩形`).not.toBeNull();
            expect(itemBox!.x).toBeGreaterThanOrEqual(scrollMetrics!.left - 1);
            expect(itemBox!.x + itemBox!.width).toBeLessThanOrEqual(
                scrollMetrics!.right + 1,
            );
            expect(itemBox!.y).toBeGreaterThanOrEqual(
                Math.max(0, scrollMetrics!.top) - 1,
            );
            expect(itemBox!.y + itemBox!.height).toBeLessThanOrEqual(
                Math.min(page.viewportSize()!.height, scrollMetrics!.bottom) + 1,
            );
            expect(
                await item.evaluate((element) => {
                    const box = element.getBoundingClientRect();
                    const hit = document.elementFromPoint(
                        box.left + box.width / 2,
                        box.top + box.height / 2,
                    );
                    return (
                        hit === element ||
                        (hit !== null && element.contains(hit))
                    );
                }),
                `${itemName} 的中心点不能被内容层或遮罩挡住`,
            ).toBe(true);
        }
    }

    // 达到 MUI `md` 断点时使用永久侧栏，内容区不能覆盖导航；
    // 临时抽屉按设计覆盖内容，因此只校验上面的可见性与命中测试。
    if (viewport.width >= 900) {
        const contentBox = await content.boundingBox();
        expect(contentBox).not.toBeNull();
        expect(
            contentBox!.x,
            `${viewport.label} 的永久侧栏覆盖主内容：${JSON.stringify({
                menuLeft: scrollMetrics!.left,
                menuRight: scrollMetrics!.right,
                contentBox,
            })}`,
        ).toBeGreaterThanOrEqual(
            scrollMetrics!.right - 1,
        );
    }

    // 每个视口独立验证。若保持打开状态跨越 `md` 断点，永久侧栏会变成
    // 模态抽屉并按设计隐藏底层 main，污染下一档的页面就绪判断。
    if (viewport.width < 900) {
        await page.keyboard.press('Escape');
        await expect(menu).toBeHidden();
        await expect(page.getByRole('main')).toBeVisible();
    } else {
        const closeMenu = page.getByRole('button', {
            name: '关闭菜单',
            exact: true,
        });
        await expect(closeMenu).toBeVisible();
        await closeMenu.click();
        await expect(
            page.getByRole('button', {
                name: '打开菜单',
                exact: true,
            }),
        ).toBeVisible();
    }
};

const navigateToPrimaryPage = async (
    page: Page,
    target: PrimaryPageCase,
) => {
    if (!target.navigation) {
        await page.goto(target.path);
        return;
    }

    const menu = page.getByRole('menu', { name: '主导航', exact: true });
    const groupToggle = menu.getByRole('menuitem', {
        name: target.navigation.group,
        exact: true,
    });
    if ((await groupToggle.getAttribute('aria-expanded')) !== 'true') {
        await groupToggle.click();
    }
    await expect(groupToggle).toHaveAttribute('aria-expanded', 'true');
    await menu
        .getByRole('group', {
            name: `${target.navigation.group}导航`,
            exact: true,
        })
        .getByRole('menuitem', {
            name: target.navigation.leaf,
            exact: true,
        })
        .click();
};

const assertNoSeriousOrCriticalAccessibilityIssues = async (
    page: Page,
    pageName: string,
) => {
    const scan = await new AxeBuilder({ page })
        .withTags([
            'wcag2a',
            'wcag2aa',
            'wcag21a',
            'wcag21aa',
        ])
        .analyze();
    const blockingViolations = scan.violations
        .filter(
            (violation) =>
                violation.impact === 'critical' ||
                violation.impact === 'serious',
        )
        .map((violation) => ({
            id: violation.id,
            impact: violation.impact,
            help: violation.help,
            targets: violation.nodes
                .flatMap((node) => node.target)
                .slice(0, 10),
            html: violation.nodes
                .map((node) => node.html)
                .slice(0, 3),
            failureSummaries: violation.nodes
                .map((node) => node.failureSummary)
                .slice(0, 3),
        }));

    expect(
        blockingViolations,
        `${pageName} 不得存在 axe serious/critical 无障碍问题`,
    ).toEqual([]);
};

test.describe('全站一级页面健康巡航', () => {
    let dashboardTicketID = 0;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('全站页面健康仪表盘有数据分支');
        dashboardTicketID = await createTicket(
            request,
            `${E2E_MARKER}仪表盘列表语义回归`,
        );
    });

    test.afterAll(async ({ request }) => {
        if (dashboardTicketID > 0) {
            await deleteTicket(request, dashboardTicketID);
        }
    });

    test('UI-025：键盘跳过导航后焦点停留在主要内容', async ({
        page,
    }) => {
        await authenticatePage(page);
        await page.goto('/#/');
        await waitForPrimaryPage(page);

        await page.keyboard.press('Tab');
        const skipNavigation = page.getByRole('button', {
            name: '跳到主要内容',
            exact: true,
        });
        await expect(skipNavigation).toHaveCount(1);
        await expect(skipNavigation).toBeFocused();
        await page.keyboard.press('Enter');
        await expect(page.locator('#main-content')).toBeFocused();
    });

    test('UI-002：左侧导航在多视口与 200% 等效重排下无遮挡且全部可达', async ({
        page,
    }) => {
        test.setTimeout(90_000);
        await page.setViewportSize({ width: 1440, height: 900 });
        await authenticatePage(page);

        for (const viewport of [
            ...cssViewports,
            ...twoHundredPercentReflowViewports,
        ]) {
            await test.step(viewport.label, async () => {
                await page.setViewportSize({
                    width: viewport.width,
                    height: viewport.height,
                });
                await page.goto('/#/');
                await waitForPrimaryPage(page);
                await expectSidebarUsable(page, viewport);
            });
        }
    });

    test('UI-030：网络中断与英文 Problem 响应只显示中文 Toast 或 Alert', async ({
        page,
    }) => {
        await authenticatePage(page);

        await page.route('**/api/platform/configs**', async (route) => {
            const url = new URL(route.request().url());
            if (
                route.request().method() === 'GET' &&
                url.pathname === '/api/platform/configs' &&
                url.searchParams.get('category') === 'system'
            ) {
                await route.abort('connectionfailed');
                return;
            }
            await route.continue();
        });
        await page.goto('/#/system-settings/overview');
        const networkAlert = page
            .getByRole('alert')
            .filter({ hasText: '网络连接失败，请检查网络后重试' });
        await expect(networkAlert).toBeVisible({ timeout: 15_000 });
        await expectChineseOperations(page);

        await page.unrouteAll({ behavior: 'wait' });
        await page.route(
            '**/api/projects/*/admin/agents/agent-control/overview',
            async (route) => {
                await route.fulfill({
                    status: 503,
                    contentType: 'application/problem+json',
                    body: JSON.stringify({
                        type: 'about:blank',
                        title: 'Service Unavailable',
                        status: 503,
                        code: 'new_backend_failure',
                        detail: 'Database connection failed unexpectedly',
                    }),
                });
            },
        );
        await page.goto('/#/agent-control');
        const problemAlert = page
            .getByRole('main')
            .getByRole('alert')
            .filter({ hasText: '服务暂时不可用，请稍后重试' });
        await expect(problemAlert).toBeVisible({ timeout: 15_000 });
        await expect(problemAlert).not.toContainText(
            /Service Unavailable|Database connection|failed unexpectedly/iu,
        );
        await expectChineseOperations(page);
    });

    test('UI-025 UI-030 UI-031：页面健康、中文反馈与无障碍门禁', async ({
        page,
    }) => {
        test.setTimeout(180_000);
        const health = monitorBrowserHealth(page);
        await authenticatePage(page);

        for (const target of primaryPages) {
            await test.step(
                `${target.caseID} ${target.name}`,
                async () => {
                    await navigateToPrimaryPage(page, target);
                    await waitForPrimaryPage(page);
                    await expect(target.ready(page)).toBeVisible({
                        timeout: 15_000,
                    });
                    await expectChineseOperations(page);
                    await assertNoSeriousOrCriticalAccessibilityIssues(
                        page,
                        target.name,
                    );
                },
            );
        }

        health.assertClean();
    });
});
