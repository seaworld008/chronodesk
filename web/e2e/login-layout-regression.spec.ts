import AxeBuilder from '@axe-core/playwright'
import {
    expect,
    test,
    type Locator,
    type Page,
} from '@playwright/test'
import { monitorBrowserHealth } from './helpers/browserAudit'

type BrowserBox = NonNullable<
    Awaited<ReturnType<Locator['boundingBox']>>
>

type ViewportCase = {
    height: number
    label: string
    width: number
}

const mobileViewports: ViewportCase[] = [
    { width: 390, height: 844, label: '390×844' },
    { width: 320, height: 568, label: '320×568' },
]

const installApiTripwire = async (page: Page): Promise<string[]> => {
    const requests: string[] = []

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        requests.push(
            `${request.method()} ${new URL(request.url()).pathname}`,
        )
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({ code: 0, data: null }),
        })
    })

    return requests
}

const openPublicPage = async (
    page: Page,
    path: '/#/login' | '/#/register',
) => {
    await page.goto(path)
    await expect(page.getByTestId('public-auth-shell')).toBeVisible({
        timeout: 15_000,
    })
    await page.waitForLoadState('networkidle', {
        timeout: 15_000,
    })
}

const readBox = async (
    locator: Locator,
    description: string,
): Promise<BrowserBox> => {
    const box = await locator.boundingBox()
    expect(box, `${description} 应有可见几何区域`).not.toBeNull()
    if (!box) {
        throw new Error(`${description} 缺少可见几何区域`)
    }
    return box
}

const assertNoBlockingAxeViolations = async (
    page: Page,
    pageName: string,
) => {
    const scan = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
        .analyze()
    const blockingViolations = scan.violations
        .filter(
            ({ impact }) =>
                impact === 'critical' || impact === 'serious',
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
        }))

    expect(
        blockingViolations,
        `${pageName} 不得存在 axe serious/critical 无障碍问题`,
    ).toEqual([])
}

const assertDesktopSplitLayout = async (page: Page) => {
    const shell = page.getByTestId('public-auth-shell')
    const brandPanel = page.getByTestId('auth-brand-panel')
    const workspace = page.getByTestId('auth-workspace')
    const form = workspace.locator('form')
    const heroImage = brandPanel.locator('img')

    const [shellBox, brandBox, workspaceBox, formBox] =
        await Promise.all([
            readBox(shell, '公开认证骨架'),
            readBox(brandPanel, '左侧品牌主视觉'),
            readBox(workspace, '右侧认证工作区'),
            readBox(form, '认证表单'),
        ])

    expect(shellBox.x).toBeCloseTo(0, 0)
    expect(shellBox.width).toBeCloseTo(1440, 0)
    expect(brandBox.x).toBeCloseTo(0, 0)
    expect(brandBox.width).toBeGreaterThan(720)
    expect(workspaceBox.width).toBeGreaterThanOrEqual(460)
    expect(
        Math.abs(
            brandBox.x + brandBox.width - workspaceBox.x,
        ),
        '左右区域应连续且不能相互覆盖',
    ).toBeLessThanOrEqual(1)
    expect(
        workspaceBox.x + workspaceBox.width,
        '右侧工作区不得越过视口',
    ).toBeLessThanOrEqual(1441)
    expect(
        formBox.x,
        '表单必须完整位于右侧工作区',
    ).toBeGreaterThanOrEqual(workspaceBox.x + 20)
    expect(formBox.x + formBox.width).toBeLessThanOrEqual(
        workspaceBox.x + workspaceBox.width - 20,
    )
    expect(
        await brandPanel.evaluate(
            (element) => getComputedStyle(element).overflowX,
        ),
        '主视觉应在品牌区域内裁切，不能覆盖表单',
    ).toBe('hidden')
    await expect
        .poll(
            () =>
                heroImage.evaluate(
                    (element) =>
                        (element as HTMLImageElement).complete &&
                        (element as HTMLImageElement).naturalWidth > 0,
                ),
            {
                message: '登录主视觉资源应成功加载',
            },
        )
        .toBe(true)
}

const assertNoHorizontalOverflow = async (
    page: Page,
    viewport: ViewportCase,
) => {
    const dimensions = await page.evaluate(() => ({
        bodyScrollWidth: document.body.scrollWidth,
        clientHeight: document.documentElement.clientHeight,
        clientWidth: document.documentElement.clientWidth,
        rootScrollHeight: document.documentElement.scrollHeight,
        rootScrollWidth: document.documentElement.scrollWidth,
    }))

    expect(dimensions.clientWidth).toBe(viewport.width)
    expect(
        dimensions.rootScrollWidth,
        `${viewport.label} 根节点不得产生横向滚动`,
    ).toBeLessThanOrEqual(dimensions.clientWidth + 1)
    expect(
        dimensions.bodyScrollWidth,
        `${viewport.label} body 不得产生横向滚动`,
    ).toBeLessThanOrEqual(dimensions.clientWidth + 1)
    expect(
        dimensions.rootScrollHeight,
        `${viewport.label} 内容超出一屏时必须可纵向滚动`,
    ).toBeGreaterThan(dimensions.clientHeight)
}

const assertReachable = async (
    locator: Locator,
    description: string,
) => {
    await locator.scrollIntoViewIfNeeded()
    await expect(locator, `${description} 应可滚动到视口内`).toBeVisible()
    await expect(locator, `${description} 应位于当前视口内`).toBeInViewport()
}

test.describe('登录注册企业级布局回归', () => {
    test('1440×900 登录页保持左主视觉、右表单且互不覆盖', async ({
        page,
    }) => {
        await page.setViewportSize({ width: 1440, height: 900 })
        const apiRequests = await installApiTripwire(page)
        const browserHealth = monitorBrowserHealth(page)

        await openPublicPage(page, '/#/login')

        await expect(
            page.getByRole('heading', {
                name: '欢迎回来',
                exact: true,
            }),
        ).toBeVisible()
        await assertDesktopSplitLayout(page)
        await assertNoBlockingAxeViolations(page, '桌面登录页')

        expect(
            apiRequests,
            '只查看登录页不应调用任何业务 API',
        ).toEqual([])
        browserHealth.assertClean()
    })

    for (const viewport of mobileViewports) {
        test(`${viewport.label} 登录页可纵向滚动且无横向溢出`, async ({
            page,
        }) => {
            await page.setViewportSize({
                width: viewport.width,
                height: viewport.height,
            })
            const apiRequests = await installApiTripwire(page)
            const browserHealth = monitorBrowserHealth(page)

            await openPublicPage(page, '/#/login')

            const brandPanel = page.getByTestId('auth-brand-panel')
            const workspace = page.getByTestId('auth-workspace')
            const brandBox = await readBox(
                brandPanel,
                `${viewport.label} 品牌顶栏`,
            )
            const workspaceBox = await readBox(
                workspace,
                `${viewport.label} 登录工作区`,
            )
            expect(
                brandBox.y + brandBox.height,
                '窄屏品牌区应位于表单上方',
            ).toBeLessThanOrEqual(workspaceBox.y + 1)

            await assertNoHorizontalOverflow(page, viewport)
            await assertReachable(page.getByLabel('邮箱'), '邮箱输入框')
            await assertReachable(page.getByLabel('密码'), '密码输入框')
            await assertReachable(
                page.getByLabel('记住此设备（免 OTP）'),
                '可信设备选项',
            )
            await assertReachable(
                page.getByRole('button', {
                    name: '登录系统',
                    exact: true,
                }),
                '登录按钮',
            )
            await assertReachable(
                page.getByText(
                    '会话与操作受组织策略保护，并保留必要审计记录',
                    { exact: true },
                ),
                '底部审计声明',
            )
            expect(
                await page.evaluate(() => window.scrollY),
                '访问底部操作时页面应实际发生纵向滚动',
            ).toBeGreaterThan(0)

            await assertNoBlockingAxeViolations(
                page,
                `${viewport.label} 登录页`,
            )
            expect(
                apiRequests,
                '窄屏浏览登录页不应调用任何业务 API',
            ).toEqual([])
            browserHealth.assertClean()
        })
    }

    test('注册路由复用同一企业认证骨架且完整显示表单', async ({
        page,
    }) => {
        await page.setViewportSize({ width: 1440, height: 900 })
        const apiRequests = await installApiTripwire(page)
        const browserHealth = monitorBrowserHealth(page)

        await openPublicPage(page, '/#/register')

        await expect(
            page.getByRole('heading', {
                name: '注册 ChronoDesk',
                exact: true,
            }),
        ).toBeVisible()
        await assertDesktopSplitLayout(page)
        await expect(page.getByLabel('用户名')).toBeVisible()
        await expect(page.getByLabel('邮箱')).toBeVisible()
        const registrationPasswords = page
            .getByTestId('auth-workspace')
            .locator('input[autocomplete="new-password"]')
        await expect(registrationPasswords).toHaveCount(2)
        await expect(registrationPasswords.nth(0)).toBeVisible()
        await expect(registrationPasswords.nth(1)).toBeVisible()
        await expect(
            page.getByRole('button', {
                name: '创建账号',
                exact: true,
            }),
        ).toBeVisible()
        await assertNoBlockingAxeViolations(page, '桌面注册页')

        expect(
            apiRequests,
            '只查看注册页不应调用任何业务 API',
        ).toEqual([])
        browserHealth.assertClean()
    })

    test('390×844 注册页保持品牌顶栏并让完整注册表单可滚动到达', async ({
        page,
    }) => {
        const viewport = { width: 390, height: 844, label: '390×844' }
        await page.setViewportSize({
            width: viewport.width,
            height: viewport.height,
        })
        const apiRequests = await installApiTripwire(page)
        const browserHealth = monitorBrowserHealth(page)

        await openPublicPage(page, '/#/register')
        await assertNoHorizontalOverflow(page, viewport)
        await expect(
            page.getByRole('heading', {
                name: '注册 ChronoDesk',
                exact: true,
            }),
        ).toBeVisible()
        await assertReachable(
            page.getByRole('button', {
                name: '创建账号',
                exact: true,
            }),
            '移动端创建账号按钮',
        )
        await assertReachable(
            page.getByRole('link', {
                name: '返回登录',
                exact: true,
            }),
            '移动端返回登录入口',
        )
        await assertNoBlockingAxeViolations(page, '移动端注册页')

        expect(apiRequests).toEqual([])
        browserHealth.assertClean()
    })

    test('932×430 横屏双栏尊重显示切口安全区且关键操作可达', async ({
        page,
    }) => {
        const viewport = { width: 932, height: 430 }
        const safeArea = {
            top: 12,
            right: 44,
            bottom: 21,
            left: 44,
        }
        await page.setViewportSize(viewport)
        const cdp = await page.context().newCDPSession(page)
        await cdp.send('Emulation.setSafeAreaInsetsOverride', {
            insets: safeArea,
        })
        const apiRequests = await installApiTripwire(page)
        const browserHealth = monitorBrowserHealth(page)

        await openPublicPage(page, '/#/login')

        const brandPanel = page.getByTestId('auth-brand-panel')
        const workspace = page.getByTestId('auth-workspace')
        const brandContent = brandPanel.locator(
            ':scope > .MuiStack-root',
        )
        const workspaceContent = workspace.locator(
            ':scope > .MuiStack-root',
        )
        const [brandPadding, workspacePadding, dimensions] =
            await Promise.all([
                brandContent.evaluate((element) => {
                    const style = getComputedStyle(element)
                    return {
                        top: Number.parseFloat(style.paddingTop),
                        right: Number.parseFloat(style.paddingRight),
                        bottom: Number.parseFloat(style.paddingBottom),
                        left: Number.parseFloat(style.paddingLeft),
                    }
                }),
                workspaceContent.evaluate((element) => {
                    const style = getComputedStyle(element)
                    return {
                        top: Number.parseFloat(style.paddingTop),
                        right: Number.parseFloat(style.paddingRight),
                        bottom: Number.parseFloat(style.paddingBottom),
                        left: Number.parseFloat(style.paddingLeft),
                    }
                }),
                page.evaluate(() => ({
                    clientWidth: document.documentElement.clientWidth,
                    rootScrollWidth:
                        document.documentElement.scrollWidth,
                    bodyScrollWidth: document.body.scrollWidth,
                })),
            ])

        expect(brandPadding.left).toBeGreaterThanOrEqual(
            safeArea.left + 40,
        )
        expect(brandPadding.right).toBeGreaterThanOrEqual(
            safeArea.right + 40,
        )
        expect(brandPadding.top).toBeGreaterThanOrEqual(
            safeArea.top + 40,
        )
        expect(brandPadding.bottom).toBeGreaterThanOrEqual(
            safeArea.bottom + 40,
        )
        expect(workspacePadding.left).toBeGreaterThanOrEqual(
            safeArea.left + 32,
        )
        expect(workspacePadding.right).toBeGreaterThanOrEqual(
            safeArea.right + 32,
        )
        expect(workspacePadding.top).toBeGreaterThanOrEqual(
            safeArea.top + 38,
        )
        expect(workspacePadding.bottom).toBeGreaterThanOrEqual(
            safeArea.bottom + 38,
        )
        expect(dimensions.clientWidth).toBe(viewport.width)
        expect(dimensions.rootScrollWidth).toBeLessThanOrEqual(
            dimensions.clientWidth + 1,
        )
        expect(dimensions.bodyScrollWidth).toBeLessThanOrEqual(
            dimensions.clientWidth + 1,
        )

        await assertReachable(
            page.getByRole('button', {
                name: '登录系统',
                exact: true,
            }),
            '横屏登录按钮',
        )
        await assertReachable(
            page.getByRole('link', {
                name: '创建账号',
                exact: true,
            }),
            '横屏注册入口',
        )
        await assertNoBlockingAxeViolations(page, '横屏登录页')

        expect(apiRequests).toEqual([])
        browserHealth.assertClean()
        await cdp.detach()
    })

    test('纯键盘可完成密码显隐并展开可信设备名称', async ({
        page,
    }) => {
        await page.setViewportSize({ width: 1440, height: 900 })
        const apiRequests = await installApiTripwire(page)
        const browserHealth = monitorBrowserHealth(page)

        await openPublicPage(page, '/#/login')

        const email = page.getByLabel('邮箱')
        const password = page.getByLabel('密码')
        const otp = page.getByLabel('OTP 验证码')
        const rememberDevice = page.getByLabel(
            '记住此设备（免 OTP）',
        )
        const loginButton = page.getByRole('button', {
            name: '登录系统',
            exact: true,
        })

        await email.focus()
        await expect(email).toBeFocused()
        await page.keyboard.press('Tab')
        await expect(password).toBeFocused()
        await password.fill('ExamplePassword123!')
        await expect(password).toHaveAttribute('type', 'password')

        await page.keyboard.press('Tab')
        await expect(
            page.getByRole('button', {
                name: '显示已输入内容',
                exact: true,
            }),
        ).toBeFocused()
        await page.keyboard.press('Enter')
        await expect(password).toHaveAttribute('type', 'text')
        await expect(
            page.getByRole('button', {
                name: '隐藏已输入内容',
                exact: true,
            }),
        ).toBeFocused()
        await page.keyboard.press('Enter')
        await expect(password).toHaveAttribute('type', 'password')

        await page.keyboard.press('Tab')
        await expect(
            page.getByRole('link', {
                name: '忘记密码？',
                exact: true,
            }),
        ).toBeFocused()
        await page.keyboard.press('Tab')
        await expect(otp).toBeFocused()
        await page.keyboard.press('Tab')
        await expect(rememberDevice).toBeFocused()
        await page.keyboard.press('Space')
        await expect(rememberDevice).toBeChecked()

        const deviceName = page.getByLabel('设备名称')
        await expect(deviceName).toBeVisible()
        await page.keyboard.press('Tab')
        await expect(deviceName).toBeFocused()
        await page.keyboard.press('Tab')
        await expect(loginButton).toBeFocused()

        expect(
            apiRequests,
            '键盘交互但不提交时不应调用任何业务 API',
        ).toEqual([])
        browserHealth.assertClean()
    })
})
