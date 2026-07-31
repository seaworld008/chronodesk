import { expect, test, type Locator, type Page } from '@playwright/test'

const encode = (value: unknown) =>
    Buffer.from(JSON.stringify(value)).toString('base64url')

const installSession = async (
    page: Page,
    sessionID = 'task3-session',
    displayName = '',
) => {
    const expiresAt = Math.floor(Date.now() / 1000) + 3600
    const token = `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
        sub: '1',
        sid: sessionID,
        platform_role: 'platform_admin',
        exp: expiresAt,
    })}.signature`
    await page.addInitScript(({ accessToken, exp, sid, fullName }) => {
        if (sessionStorage.getItem('task3-preserve-session') === 'true') {
            return
        }
        localStorage.setItem('token', accessToken)
        localStorage.setItem('refreshToken', 'task3-refresh')
        localStorage.setItem('tokenExpiresAt', String(exp * 1000))
        const storedUser: Record<string, unknown> = {
            id: 1,
            username: 'task3-admin',
            email: 'task3@example.invalid',
            platform_role: 'platform_admin',
            status: 'active',
            email_verified: true,
            otp_enabled: false,
        }
        if (fullName) {
            storedUser.profile = {
                first_name: '',
                last_name: '',
                display_name: fullName,
                avatar: '',
            }
        }
        localStorage.setItem('user', JSON.stringify(storedUser))
        localStorage.setItem('chronodesk.activeProject', JSON.stringify({
            subject: '1',
            session_id: sid,
            project_key: 'OPS',
        }))
    }, {
        accessToken: token,
        exp: expiresAt,
        fullName: displayName,
        sid: sessionID,
    })
}

const projectAccess = [{
    project: {
        id: 7,
        public_id: '00000000-0000-4000-8000-000000000007',
        key: 'OPS',
        name: '运营项目',
        description: '',
        business_unit_id: 1,
        organization_id: 1,
        status: 'active',
    },
    scope: { organization_id: 1, project_id: 7 },
    project_role: 'project_admin',
}]

const installLayoutMocks = async (page: Page) => {
    const user = {
        id: 1,
        username: 'task3-admin',
        email: 'task3@example.invalid',
        platform_role: 'platform_admin',
        status: 'active',
        email_verified: true,
        otp_enabled: false,
        profile: {
            id: 1,
            user_id: 1,
            first_name: 'Chrono',
            last_name: 'Desk',
            display_name: 'Chrono Desk',
            avatar: '',
            phone: '',
            department: '',
            position: '',
            timezone: 'Asia/Shanghai',
            language: 'zh-CN',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
        },
    }
    const emailConfig = {
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
    }

    await page.route('**/api/**', async (route) => {
        const url = new URL(route.request().url())
        let data: unknown = []
        if (url.pathname === '/api/projects') {
            data = projectAccess
        } else if (url.pathname === '/api/auth/me') {
            data = user
        } else if (url.pathname === '/api/platform/configs') {
            data = []
        } else if (url.pathname === '/api/platform/email-config') {
            data = emailConfig
        } else if (url.pathname === '/api/projects/OPS/webhooks') {
            data = { items: [], total: 0, page: 1, page_size: 100 }
        } else if (url.pathname === '/api/user/trusted-devices') {
            data = []
        } else if (url.pathname === '/api/user/login-history') {
            data = { items: [], total: 0, page: 1, page_size: 20 }
        } else if (url.pathname === '/api/workbench/tickets') {
            data = { items: [], total: 0, page: 1, page_size: 20, total_pages: 0 }
        }
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({ code: 0, msg: 'ok', data }),
        })
    })
}

const expectReachable = async (locator: Locator, viewportWidth: number) => {
    await expect(locator).toBeVisible()
    await locator.scrollIntoViewIfNeeded()
    await expect(locator).toBeInViewport()
    const box = await locator.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewportWidth + 0.5)
    await locator.click({ trial: true })
}

const navigateFromAccountMenu = async (page: Page, itemID: string) => {
    const temporarySidebar = page.locator('.RaSidebar-modal')
    await expect(temporarySidebar).toBeHidden()
    await page.getByTestId('account-menu').locator('button').first().click()
    await page.getByTestId(`account-menu-${itemID}`).click()
}

const deferred = () => {
    let resolve!: () => void
    const promise = new Promise<void>((done) => {
        resolve = done
    })
    return { promise, resolve }
}

test.describe('Task 3 导航、账号与多选回归（mock）', () => {
    test('树契约、单叶直达、持久化、账号白名单、Webhook clear 和登录分页', async ({ page }) => {
        await installSession(page)
        const profileWrites: Array<Record<string, unknown>> = []
        const loginPages: number[] = []
        const passwordWrites: Array<Record<string, unknown>> = []
        const otpVerifications: string[] = []
        const uploadedAvatarURL =
            'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='
        let otpEnabled = false
        let profile = {
            id: 1,
            user_id: 1,
            first_name: 'Chrono',
            last_name: 'Desk',
            display_name: 'Chrono Desk',
            avatar: '',
            phone: '+8613800138000',
            department: '',
            position: '',
            timezone: 'Asia/Shanghai',
            language: 'en',
            created_at: '2026-01-01T00:00:00Z',
            updated_at: '2026-01-01T00:00:00Z',
        }
        const currentUser = () => ({
            id: 1,
            username: 'task3-admin',
            email: 'task3@example.invalid',
            platform_role: 'platform_admin',
            status: 'active',
            email_verified: true,
            otp_enabled: otpEnabled,
            last_login_at: '2026-07-31T00:00:00Z',
            profile,
        })

        await page.route('**/api/**', async (route) => {
            const request = route.request()
            const url = new URL(request.url())
            let data: unknown = []
            if (url.pathname === '/api/projects') {
                data = projectAccess
            } else if (url.pathname === '/api/auth/me') {
                data = currentUser()
            } else if (
                url.pathname === '/api/auth/profile' &&
                request.method() === 'PUT'
            ) {
                const payload = request.postDataJSON() as Record<string, unknown>
                profileWrites.push(payload)
                profile = { ...profile, ...payload }
                data = null
            } else if (
                url.pathname === '/api/user/avatar' &&
                request.method() === 'POST'
            ) {
                profile = { ...profile, avatar: uploadedAvatarURL }
                data = { avatar_url: uploadedAvatarURL }
            } else if (url.pathname === '/api/user/login-history') {
                const requestedPage = Number(url.searchParams.get('page'))
                loginPages.push(requestedPage)
                data = {
                    items: [{
                        id: requestedPage,
                        ip_address: '127.0.0.1',
                        login_time: '2026-07-31T00:00:00Z',
                        login_status: 'success',
                        login_method: 'password',
                        location: '本地',
                        device_info: 'Chromium',
                        session_duration: '1 分钟',
                        is_current_session: requestedPage === 1,
                    is_active: true,
                }],
                    total: 45,
                    page: requestedPage,
                    page_size: 20,
                }
            } else if (
                url.pathname === '/api/auth/enable-otp' &&
                request.method() === 'POST'
            ) {
                otpEnabled = true
                data = {
                    secret: 'TASK3SECRET',
                    qr_code: 'otpauth://totp/ChronoDesk:task3@example.invalid?secret=TASK3SECRET',
                    backup_codes: ['BACKUP-01', 'BACKUP-02'],
                }
            } else if (
                url.pathname === '/api/auth/verify-otp' &&
                request.method() === 'POST'
            ) {
                const payload = request.postDataJSON() as { code: string }
                otpVerifications.push(payload.code)
                data = null
            } else if (
                url.pathname === '/api/auth/change-password' &&
                request.method() === 'POST'
            ) {
                passwordWrites.push(
                    request.postDataJSON() as Record<string, unknown>,
                )
                data = null
            } else if (url.pathname === '/api/projects/OPS/webhooks') {
                data = { items: [], total: 0, page: 1, page_size: 100 }
            } else if (url.pathname === '/api/workbench/tickets') {
                data = { items: [], total: 0, page: 1, page_size: 20, total_pages: 0 }
            }
            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({ code: 0, msg: 'ok', data }),
            })
        })

        await page.goto('/#/system-settings')
        await expect(page.getByRole('heading', {
            name: '平台公共配置',
            level: 1,
        })).toBeVisible()
        await expect(page.getByTestId('page-scope-badge'))
            .toHaveAttribute('data-page-scope', 'platform')
        await expect(page.getByTestId('scope-badge')).toContainText('平台级')
        const settingsToggle = page.getByRole('button', { name: /^系统设置/ })
        await expect(settingsToggle).toHaveAttribute('aria-expanded', 'true')
        await expect(settingsToggle).toHaveAttribute('aria-controls')

        const workbench = page.getByRole('button', { name: /^工作台/ })
        await expect(workbench).toBeVisible()
        await workbench.click()
        const crossProjectWorkbench = page.getByRole('menuitem', {
            name: '跨项目工作台',
            exact: true,
        })
        await expect(crossProjectWorkbench).toHaveAttribute('href', /workbench$/)
        await crossProjectWorkbench.evaluate((element) =>
            (element as HTMLElement).click(),
        )
        await expect(page).toHaveURL(/#\/workbench$/)
        await expect(page.getByTestId('page-scope-badge'))
            .toHaveAttribute('data-page-scope', 'global')
        await expect(page.getByTestId('scope-badge')).toContainText('跨项目')

        await page.goto('/#/system-settings')
        const governance = page.getByRole('button', { name: /^治理中心/ })
        await governance.focus()
        await governance.press('Enter')
        await expect(governance).toHaveAttribute('aria-expanded', 'true')
        const storedKey = await page.evaluate(() =>
            Object.keys(localStorage).find((key) =>
                key.includes('navigation-groups.v2.1.task3-session'),
            ),
        )
        expect(storedKey).toBeTruthy()
        await page.reload()
        await expect(page.getByRole('button', { name: /^治理中心/ }))
            .toHaveAttribute('aria-expanded', 'true')
        const otherExpiry = Math.floor(Date.now() / 1000) + 3600
        const otherToken = `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
            sub: '1',
            sid: 'task3-other-session',
            platform_role: 'platform_admin',
            exp: otherExpiry,
        })}.signature`
        await page.evaluate(({ token, exp }) => {
            sessionStorage.setItem('task3-preserve-session', 'true')
            localStorage.setItem('token', token)
            localStorage.setItem('tokenExpiresAt', String(exp * 1000))
            localStorage.setItem('chronodesk.activeProject', JSON.stringify({
                subject: '1',
                session_id: 'task3-other-session',
                project_key: 'OPS',
            }))
        }, { token: otherToken, exp: otherExpiry })
        await page.reload()
        await expect(page.getByRole('button', { name: /^治理中心/ }))
            .toHaveAttribute('aria-expanded', 'false')
        await expect(page.getByRole('button', { name: /^系统设置/ }))
            .toHaveAttribute('aria-expanded', 'true')

        const accountTrigger = page.getByTestId('account-menu').locator('button').first()
        await accountTrigger.click()
        const profileMenuItem = page.getByTestId('account-menu-account-profile')
        await expect(profileMenuItem).toBeVisible()
        await profileMenuItem.click()
        await expect(profileMenuItem).toBeHidden()
        await expect(page).toHaveURL(/#\/account\/profile$/)
        await expect(page.getByTestId('page-scope-badge'))
            .toHaveAttribute('data-page-scope', 'account')
        await expect(page.getByTestId('scope-badge')).toContainText('个人账号')
        await expect(page.getByRole('heading', {
            name: '个人资料',
            level: 1,
        })).toBeVisible()
        await expect(page.getByLabel('邮箱（只读）')).toBeDisabled()
        await expect(page.getByLabel('手机（只读）')).toBeDisabled()
        await expect(page.getByLabel('语言')).toHaveValue('en')
        await page.getByLabel('名字').fill('Updated')
        await page.getByRole('button', { name: '保存个人资料' }).click()
        await expect.poll(() => profileWrites.length).toBe(1)
        expect(Object.keys(profileWrites[0]).sort()).toEqual([
            'first_name',
            'language',
            'last_name',
            'timezone',
        ])
        expect(profileWrites[0].language).toBe('en')
        await expect(page.getByTestId('account-menu').locator('button').first())
            .toContainText('Updated Desk')
        await page.keyboard.press('Escape')
        await expect(page.getByText('个人资料已更新')).toHaveCount(0)
        const desktopAvatar = await page.getByTestId('profile-avatar-panel')
            .boundingBox()
        const desktopForm = await page.getByTestId('profile-main-form')
            .boundingBox()
        expect(desktopAvatar).not.toBeNull()
        expect(desktopForm).not.toBeNull()
        expect(desktopAvatar!.x).toBeGreaterThan(desktopForm!.x + 400)
        expect(desktopAvatar!.y).toBeLessThan(desktopForm!.y)
        await expect(page.getByTestId('account-page-shell'))
            .toHaveScreenshot('account-profile-desktop.png')
        await expect(page.locator('.MuiSnackbar-root')).toBeHidden({
            timeout: 7_000,
        })

        await page.setViewportSize({ width: 390, height: 844 })
        await page.goto('/#/account/profile')
        await expect(page.getByTestId('account-profile-page')).toBeVisible()
        await expect(page.getByTestId('account-page-header'))
            .toHaveCSS('flex-direction', 'column')
        const mobileLayout = await page.evaluate(() => {
            const rect = (testID: string) => {
                const element = document.querySelector(
                    `[data-testid="${testID}"]`,
                )
                if (!(element instanceof HTMLElement)) return null
                const box = element.getBoundingClientRect()
                return { x: box.x, y: box.y, width: box.width }
            }
            return {
                header: rect('account-page-header'),
                avatar: rect('profile-avatar-panel'),
                form: rect('account-profile-page'),
            }
        })
        expect(mobileLayout.header).not.toBeNull()
        expect(mobileLayout.avatar).not.toBeNull()
        expect(mobileLayout.form).not.toBeNull()
        expect(Math.abs(mobileLayout.avatar!.x - mobileLayout.header!.x))
            .toBeLessThan(4)
        expect(Math.abs(mobileLayout.form!.x - mobileLayout.header!.x))
            .toBeLessThan(4)
        expect(mobileLayout.avatar!.y).toBeLessThan(mobileLayout.form!.y)
        await expect(page).toHaveScreenshot('account-profile-mobile.png')
        for (const accountPage of [
            { id: 'account-profile', title: '个人资料' },
            { id: 'account-security', title: '账号安全' },
            { id: 'trusted-devices', title: '可信设备' },
            { id: 'login-history', title: '登录历史' },
        ]) {
            await navigateFromAccountMenu(page, accountPage.id)
            const header = page.getByTestId('account-page-header')
            await expect(header).toHaveCount(1)
            await expect(header).toHaveCSS('flex-direction', 'column')
            await expect(header).toContainText(accountPage.title)
        }
        await page.setViewportSize({ width: 1280, height: 720 })
        for (const accountPage of [
            { id: 'account-profile', title: '个人资料' },
            { id: 'account-security', title: '账号安全' },
            { id: 'trusted-devices', title: '可信设备' },
            { id: 'login-history', title: '登录历史' },
        ]) {
            await navigateFromAccountMenu(page, accountPage.id)
            const header = page.getByTestId('account-page-header')
            await expect(header).toHaveCount(1)
            await expect(header).toHaveCSS('flex-direction', 'row')
            await expect(header).toContainText(accountPage.title)
        }

        await page.goto('/#/account/profile')
        await page.locator('input[type="file"][accept="image/png,image/jpeg"]')
            .setInputFiles({
                name: 'avatar.png',
                mimeType: 'image/png',
                buffer: Buffer.from(
                    uploadedAvatarURL.split(',')[1],
                    'base64',
                ),
            })
        await expect(page.getByTestId('profile-avatar-panel').locator('img'))
            .toHaveAttribute('src', uploadedAvatarURL)
        await expect(page.getByTestId('account-menu').locator('img'))
            .toHaveAttribute('src', uploadedAvatarURL)

        await page.goto('/#/account/login-history')
        await expect(page.getByRole('heading', {
            name: '登录历史',
            level: 1,
        })).toBeVisible()
        await page.getByRole('button', { name: /下一页|next page/i }).click()
        await expect.poll(() => loginPages).toContain(2)

        await page.goto('/#/webhook-settings')
        await expect(page.getByRole('heading', {
            name: 'Webhook 集成',
            level: 1,
        })).toBeVisible()
        await expect(page.getByTestId('page-scope-badge'))
            .toHaveAttribute('data-page-scope', 'project')
        await expect(page.getByTestId('scope-badge'))
            .toContainText('当前项目：运营项目')
        await page.getByRole('button', { name: '新增 Webhook' }).click()
        await page.getByRole('combobox', { name: '订阅事件' }).click()
        const transitioned = page.getByRole('option', {
            name: /工单状态流转/,
        })
        await transitioned.click()
        await page.keyboard.press('Escape')
        await page.getByRole('combobox', { name: '状态流转筛选' }).click()
        await page.getByRole('option', { name: /已解决/ }).click()
        await page.keyboard.press('Escape')
        await page.getByRole('button', { name: '清空已选订阅事件' }).click()
        await expect(
            page.getByRole('combobox', { name: '状态流转筛选' }),
        ).toHaveCount(0)

        await page.goto('/#/account/security')
        await expect(page.getByRole('heading', {
            name: '账号安全',
            level: 1,
        })).toBeVisible()
        await page.getByLabel('当前密码').last().fill('CurrentPassword123!')
        await page.getByRole('button', { name: '启用 MFA' }).click()
        await expect(page.getByText('MFA 已立即启用', { exact: true })).toBeVisible()
        await expect(page.getByLabel('验证器配置 URI（qr_code）'))
            .toHaveValue(/otpauth:\/\/totp/)
        await expect(page.getByLabel('手动输入密钥')).toHaveValue('TASK3SECRET')
        await expect(page.getByRole('list', { name: 'MFA 备用码' }))
            .toContainText('BACKUP-01')
        const qrCode = page.getByTestId('mfa-setup-qr-code')
        await expect(qrCode).toBeVisible()
        await expect(qrCode.locator('svg')).toHaveCount(1)
        await expect(
            qrCode.getByRole('img', {
                name: 'MFA 验证器配置二维码',
            }),
        ).toBeVisible()
        await expect(qrCode.locator('svg rect, svg path').first()).toBeVisible()
        await expect(page.getByText(/离开前请完成验证器配置/)).toBeVisible()
        await expect(page.getByRole('button', { name: '我已安全保存' }))
            .toBeVisible()
        await expect(page.getByText('尚未确认保存恢复材料')).toBeVisible()
        await page.getByLabel('测试 6 位验证码（不改变启用状态）')
            .fill('123456')
        await page.getByRole('button', { name: '测试验证码' }).click()
        await expect.poll(() => otpVerifications).toContain('123456')

        const leaveDialog = page.getByRole('dialog', {
            name: 'MFA 恢复材料尚未确认保存',
        })
        const integrationToggle = page.getByRole('button', {
            name: /^集成中心/,
        })
        if (await integrationToggle.getAttribute('aria-expanded') !== 'true') {
            await integrationToggle.click()
        }
        await page.getByRole('menuitem', { name: 'Webhook', exact: true }).click()
        await expect(leaveDialog).toBeVisible()
        await expect(page).toHaveURL(/#\/account\/security$/)
        await leaveDialog
            .getByRole('button', { name: '继续留在本页' })
            .click()
        await expect(leaveDialog).toHaveCount(0)

        await page.getByRole('link', { name: '可信设备' }).click()
        await expect(leaveDialog).toBeVisible()
        await expect(page).toHaveURL(/#\/account\/security$/)
        await leaveDialog
            .getByRole('button', { name: '继续留在本页' })
            .click()
        await expect(leaveDialog).toHaveCount(0)
        await page.getByRole('link', { name: '可信设备' }).click()
        await leaveDialog
            .getByRole('button', { name: '我已保存并离开' })
            .click()
        await expect(page).toHaveURL(/#\/account\/trusted-devices$/)
        await expect(page.getByText('暂无可信设备记录。')).toBeVisible()

        await page.goto('/#/account/security')
        await page.getByLabel('当前密码').first().fill('CurrentPassword123!')
        await page.getByLabel('新密码', { exact: true })
            .fill('ChangedPassword123!')
        await page.getByLabel('确认新密码').fill('ChangedPassword123!')
        await page.getByRole('button', { name: '修改密码' }).click()
        await expect.poll(() => passwordWrites.length).toBe(1)
        await expect(page).toHaveURL(/#\/login$/)
        await expect.poll(() => page.evaluate(() => ({
            token: localStorage.getItem('token'),
            refreshToken: localStorage.getItem('refreshToken'),
            user: localStorage.getItem('user'),
        }))).toEqual({
            token: null,
            refreshToken: null,
            user: null,
        })
    })

    test('AppBar、响应式侧栏、页面操作与头像使用真实几何矩阵', async ({ page }) => {
        await installSession(page, 'task3-layout-session')
        await installLayoutMocks(page)

        for (const viewport of [
            { width: 320, height: 720 },
            { width: 390, height: 844 },
            { width: 640, height: 720 },
            { width: 720, height: 800 },
            { width: 1280, height: 800 },
        ]) {
            await page.setViewportSize(viewport)
            await page.goto('/#/system-settings')
            await expect(page.getByRole('heading', {
                name: '平台公共配置',
                level: 1,
            })).toBeVisible()
            await expect(page.getByTestId('active-project-switcher')).toBeVisible()
            await expect(page.getByTestId('page-scope-badge'))
                .toHaveAttribute('data-page-scope', 'platform')
            await expect(page.getByTestId('scope-badge')).toContainText('平台级')
            await expect(page.getByTestId('appbar-context-controls')
                .locator('.MuiChip-root')).toHaveCount(1)

            const geometry = await page.evaluate(() => {
                const rect = (selector: string) => {
                    const element = document.querySelector(selector)
                    if (!(element instanceof HTMLElement)) return null
                    const box = element.getBoundingClientRect()
                    return {
                        bottom: box.bottom,
                        height: box.height,
                        left: box.left,
                        right: box.right,
                        top: box.top,
                        width: box.width,
                    }
                }
                const permanentSidebar = document.querySelector(
                    '.RaSidebar-root:not(.ChronoDeskSidebar-temporary)',
                )
                return {
                    account: rect('[data-testid="account-menu"] button'),
                    controls: rect('[data-testid="appbar-context-controls"]'),
                    main: rect('#main-content'),
                    permanentSidebar: permanentSidebar instanceof HTMLElement
                        ? rect('.RaSidebar-root:not(.ChronoDeskSidebar-temporary)')
                        : null,
                    scope: rect('[data-testid="page-scope-badge"]'),
                    scrollWidth: document.documentElement.scrollWidth,
                    switcher: rect('[data-testid="work-project-control"]'),
                    titleDisplay: getComputedStyle(
                        document.querySelector(
                            '[data-testid="appbar-title-portal"]',
                        )!,
                    ).display,
                    toolbar: rect('.RaAppBar-toolbar'),
                    viewportWidth: window.innerWidth,
                }
            })

            expect(geometry.toolbar).not.toBeNull()
            expect(geometry.account).not.toBeNull()
            expect(geometry.controls).not.toBeNull()
            expect(geometry.scope).not.toBeNull()
            expect(geometry.switcher).not.toBeNull()
            expect(geometry.main).not.toBeNull()
            expect(geometry.toolbar!.height).toBeLessThanOrEqual(64)
            expect(geometry.toolbar!.height).toBeGreaterThanOrEqual(44)
            expect(geometry.viewportWidth - geometry.account!.right)
                .toBeGreaterThanOrEqual(0)
            expect(geometry.viewportWidth - geometry.account!.right)
                .toBeLessThanOrEqual(16)
            expect(geometry.controls!.right)
                .toBeLessThanOrEqual(geometry.account!.left + 0.5)
            expect(geometry.scope!.right)
                .toBeLessThanOrEqual(geometry.switcher!.left + 0.5)
            expect(geometry.account!.top)
                .toBeGreaterThanOrEqual(geometry.toolbar!.top)
            expect(geometry.account!.bottom)
                .toBeLessThanOrEqual(geometry.toolbar!.bottom)
            expect(geometry.scrollWidth)
                .toBeLessThanOrEqual(geometry.viewportWidth + 1)

            if (viewport.width <= 720) {
                expect(geometry.permanentSidebar).toBeNull()
                expect(geometry.main!.left).toBeLessThanOrEqual(1)
                expect(geometry.main!.width)
                    .toBeGreaterThanOrEqual(viewport.width - 1)
                expect(geometry.titleDisplay).toBe('none')
            } else {
                expect(geometry.titleDisplay).not.toBe('none')
            }

            if (viewport.width === 390 || viewport.width === 640) {
                await page.locator('.RaAppBar-menuButton').click()
                await expect(page.locator('.RaSidebar-modal')).toBeVisible()
                await page.keyboard.press('Escape')
                await expect(page.locator('.RaSidebar-modal')).toBeHidden()
            }
        }

        for (const width of [320, 640]) {
            await page.setViewportSize({ width, height: 800 })
            await page.goto('/#/system-settings')
            await expectReachable(
                page.getByTestId('page-header-action')
                    .getByRole('button', { name: '刷新', exact: true }),
                width,
            )

            await page.goto('/#/webhook-settings')
            await expect(page.getByRole('heading', {
                name: 'Webhook 集成',
                level: 1,
            })).toBeVisible()
            await expect(page.getByTestId('page-scope-badge'))
                .toHaveAttribute('data-page-scope', 'project')
            for (const buttonName of ['刷新', '新增 Webhook']) {
                await expectReachable(
                    page.getByTestId('page-header-action')
                        .getByRole('button', {
                            name: buttonName,
                            exact: true,
                        }),
                    width,
                )
            }

            await page.goto('/#/system-settings/email')
            await expect(page.getByRole('heading', {
                name: '平台邮件设置',
                level: 1,
            })).toBeVisible()
            await expectReachable(
                page.getByTestId('email-settings-page-shell')
                    .getByRole('button', { name: '刷新', exact: true }),
                width,
            )

            await page.goto('/#/account/profile')
            await expect(page.getByRole('heading', {
                name: '个人资料',
                level: 1,
            })).toBeVisible()
            await expectReachable(
                page.getByRole('button', { name: '更换头像' }),
                width,
            )
            await expectReachable(
                page.getByRole('button', { name: '保存个人资料' }),
                width,
            )
        }

        await page.setViewportSize({ width: 1280, height: 800 })
        await page.goto('/#/account/profile')
        await expect(page.getByTestId('profile-avatar-panel')).toBeVisible()
        const profileGeometry = await page.evaluate(() => {
            const bounds = (testID: string) => {
                const element = document.querySelector(
                    `[data-testid="${testID}"]`,
                )
                if (!(element instanceof HTMLElement)) return null
                const rect = element.getBoundingClientRect()
                return {
                    left: rect.left,
                    right: rect.right,
                    width: rect.width,
                }
            }
            const main = document.querySelector('#main-content')
                ?.getBoundingClientRect()
            return {
                avatar: bounds('profile-avatar-panel'),
                form: bounds('account-profile-page'),
                main: main
                    ? { left: main.left, right: main.right, width: main.width }
                    : null,
            }
        })
        expect(profileGeometry.avatar).not.toBeNull()
        expect(profileGeometry.form).not.toBeNull()
        expect(profileGeometry.main).not.toBeNull()
        expect(profileGeometry.main!.right - profileGeometry.avatar!.right)
            .toBeGreaterThanOrEqual(16)
        expect(profileGeometry.main!.right - profileGeometry.avatar!.right)
            .toBeLessThanOrEqual(24.5)
        expect(profileGeometry.form!.width).toBeLessThanOrEqual(760)
        expect(profileGeometry.avatar!.left)
            .toBeGreaterThan(profileGeometry.form!.right)
    })

    test('超长账号名、项目名与项目加载态保持 AppBar 三段几何', async ({ page }) => {
        test.setTimeout(60_000)
        const longDisplayName =
            '企业全球支持与自动化平台主管-超长账号展示名称-'.repeat(8)
        const longProjectName =
            '全球企业服务与智能自动化联合运营项目-超长项目名称-'.repeat(6)
        const longProjectAccess = [{
            ...projectAccess[0],
            project: {
                ...projectAccess[0].project,
                name: longProjectName,
            },
        }]
        const longUser = {
            id: 1,
            username: 'task3-admin',
            email: 'task3@example.invalid',
            platform_role: 'platform_admin',
            status: 'active',
            email_verified: true,
            otp_enabled: false,
            profile: {
                first_name: '',
                last_name: '',
                display_name: longDisplayName,
                avatar: '',
            },
        }

        await installSession(
            page,
            'task3-long-appbar-session',
            longDisplayName,
        )

        let projectGate = deferred()
        let projectRequestStarted = deferred()
        let reportedProjectRequest = false
        const armProjectLoading = () => {
            projectGate = deferred()
            projectRequestStarted = deferred()
            reportedProjectRequest = false
        }

        await page.route('**/api/**', async (route) => {
            const url = new URL(route.request().url())
            let data: unknown = []
            if (url.pathname === '/api/projects') {
                if (!reportedProjectRequest) {
                    reportedProjectRequest = true
                    projectRequestStarted.resolve()
                }
                await projectGate.promise
                data = longProjectAccess
            } else if (url.pathname === '/api/auth/me') {
                data = longUser
            } else if (url.pathname === '/api/platform/configs') {
                data = []
            } else if (url.pathname === '/api/projects/OPS/webhooks') {
                data = { items: [], total: 0, page: 1, page_size: 100 }
            }
            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({ code: 0, msg: 'ok', data }),
            })
        })

        for (const [index, viewport] of [
            { width: 1280, height: 800 },
            { width: 1024, height: 768 },
            { width: 640, height: 720 },
        ].entries()) {
            armProjectLoading()
            await page.setViewportSize(viewport)
            await page.goto('/#/system-settings')
            if (index > 0) {
                await page.reload()
            }
            await projectRequestStarted.promise
            await expect(page.getByTestId('project-switcher-loading'))
                .toBeVisible()
            await expect(
                page.getByTestId('account-menu').locator('button').first(),
            ).toContainText(longDisplayName)

            const loadingGeometry = await page.evaluate(() => {
                const bounds = (selector: string) => {
                    const element = document.querySelector(selector)
                    if (!(element instanceof HTMLElement)) return null
                    const rect = element.getBoundingClientRect()
                    return {
                        bottom: rect.bottom,
                        height: rect.height,
                        left: rect.left,
                        right: rect.right,
                        top: rect.top,
                        width: rect.width,
                    }
                }
                return {
                    account: bounds('[data-testid="account-menu"] button'),
                    controls: bounds('[data-testid="appbar-context-controls"]'),
                    loading: bounds('[data-testid="project-switcher-loading"]'),
                    scrollWidth: document.documentElement.scrollWidth,
                    toolbar: bounds('.RaAppBar-toolbar'),
                    viewportWidth: window.innerWidth,
                }
            })
            expect(loadingGeometry.account).not.toBeNull()
            expect(loadingGeometry.controls).not.toBeNull()
            expect(loadingGeometry.loading).not.toBeNull()
            expect(loadingGeometry.toolbar).not.toBeNull()
            expect(loadingGeometry.viewportWidth - loadingGeometry.account!.right)
                .toBeGreaterThanOrEqual(0)
            expect(loadingGeometry.viewportWidth - loadingGeometry.account!.right)
                .toBeLessThanOrEqual(16)
            expect(loadingGeometry.controls!.width).toBeGreaterThan(96)
            expect(loadingGeometry.loading!.width).toBeGreaterThanOrEqual(44)
            expect(loadingGeometry.controls!.right)
                .toBeLessThanOrEqual(loadingGeometry.account!.left + 0.5)
            expect(loadingGeometry.account!.top)
                .toBeGreaterThanOrEqual(loadingGeometry.toolbar!.top)
            expect(loadingGeometry.account!.bottom)
                .toBeLessThanOrEqual(loadingGeometry.toolbar!.bottom)
            expect(loadingGeometry.scrollWidth)
                .toBeLessThanOrEqual(viewport.width + 1)

            projectGate.resolve()
            await expect(page.getByTestId('active-project-switcher'))
                .toBeVisible()
            await page.goto('/#/webhook-settings')
            await expect(page.getByRole('heading', {
                name: 'Webhook 集成',
                level: 1,
            })).toBeVisible()
            await expect(page.getByTestId('scope-badge'))
                .toContainText(`当前项目：${longProjectName}`)
            await expect(page.getByTestId('active-project-switcher'))
                .toContainText(longProjectName)
            const accountTrigger =
                page.getByTestId('account-menu').locator('button').first()
            await expect(accountTrigger).toContainText(longDisplayName)

            const longTextGeometry = await page.evaluate(() => {
                const bounds = (selector: string) => {
                    const element = document.querySelector(selector)
                    if (!(element instanceof HTMLElement)) return null
                    const rect = element.getBoundingClientRect()
                    return {
                        clientWidth: element.clientWidth,
                        fontSize: getComputedStyle(element).fontSize,
                        left: rect.left,
                        overflow: getComputedStyle(element).overflow,
                        right: rect.right,
                        scrollWidth: element.scrollWidth,
                        textOverflow: getComputedStyle(element).textOverflow,
                        width: rect.width,
                    }
                }
                return {
                    account: bounds('[data-testid="account-menu"] button'),
                    controls: bounds('[data-testid="appbar-context-controls"]'),
                    scope: bounds(
                        '[data-testid="page-scope-badge"] .MuiChip-label',
                    ),
                    scrollWidth: document.documentElement.scrollWidth,
                    select: bounds(
                        '[data-testid="active-project-switcher"] .MuiSelect-select',
                    ),
                    viewportWidth: window.innerWidth,
                }
            })
            expect(longTextGeometry.account).not.toBeNull()
            expect(longTextGeometry.controls).not.toBeNull()
            expect(longTextGeometry.scope).not.toBeNull()
            expect(longTextGeometry.select).not.toBeNull()
            expect(longTextGeometry.viewportWidth - longTextGeometry.account!.right)
                .toBeGreaterThanOrEqual(0)
            expect(longTextGeometry.viewportWidth - longTextGeometry.account!.right)
                .toBeLessThanOrEqual(16)
            expect(longTextGeometry.account!.width).toBeGreaterThanOrEqual(40)
            expect(longTextGeometry.account!.width).toBeLessThanOrEqual(192)
            expect(longTextGeometry.controls!.width).toBeGreaterThan(96)
            expect(longTextGeometry.controls!.right)
                .toBeLessThanOrEqual(longTextGeometry.account!.left + 0.5)
            expect(longTextGeometry.scope!.right)
                .toBeLessThanOrEqual(longTextGeometry.select!.left + 0.5)
            expect(longTextGeometry.scope!.scrollWidth)
                .toBeGreaterThan(longTextGeometry.scope!.clientWidth)
            expect(longTextGeometry.select!.scrollWidth)
                .toBeGreaterThan(longTextGeometry.select!.clientWidth)
            expect(longTextGeometry.scrollWidth)
                .toBeLessThanOrEqual(viewport.width + 1)

            if (viewport.width >= 1200) {
                expect(longTextGeometry.account!.textOverflow).toBe('ellipsis')
                expect(longTextGeometry.account!.overflow).toBe('hidden')
                expect(longTextGeometry.account!.scrollWidth)
                    .toBeGreaterThan(longTextGeometry.account!.clientWidth)
            } else {
                expect(longTextGeometry.account!.fontSize).toBe('0px')
                expect(longTextGeometry.account!.width).toBeLessThanOrEqual(44)
            }

            await accountTrigger.click()
            await expect(page.getByTestId('account-menu-account-profile'))
                .toBeVisible()
            await page.keyboard.press('Escape')
            await expect(page.getByTestId('account-menu-account-profile'))
                .toBeHidden()
        }
    })

    test('个人资料和邮件设置在 loading/error 分支保留统一 PageHeader', async ({ page }) => {
        await installSession(page, 'task3-page-header-session')
        const profileGate = deferred()
        const profileStarted = deferred()
        const emailGate = deferred()
        const emailStarted = deferred()
        const user = {
            id: 1,
            username: 'task3-admin',
            email: 'task3@example.invalid',
            platform_role: 'platform_admin',
            status: 'active',
            email_verified: true,
            otp_enabled: false,
            profile: {
                first_name: 'Chrono',
                last_name: 'Desk',
                display_name: 'Chrono Desk',
                avatar: '',
                phone: '',
                timezone: 'Asia/Shanghai',
                language: 'zh-CN',
            },
        }

        await page.route('**/api/**', async (route) => {
            const url = new URL(route.request().url())
            if (url.pathname === '/api/projects') {
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({
                        code: 0,
                        msg: 'ok',
                        data: projectAccess,
                    }),
                })
                return
            }
            if (url.pathname === '/api/auth/me') {
                profileStarted.resolve()
                await profileGate.promise
                await route.fulfill({
                    status: 200,
                    contentType: 'application/json',
                    body: JSON.stringify({ code: 0, msg: 'ok', data: user }),
                })
                return
            }
            if (url.pathname === '/api/platform/email-config') {
                emailStarted.resolve()
                await emailGate.promise
                await route.fulfill({
                    status: 500,
                    contentType: 'application/json',
                    body: JSON.stringify({
                        code: 500,
                        msg: 'mock failure',
                        data: null,
                    }),
                })
                return
            }
            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify({ code: 0, msg: 'ok', data: [] }),
            })
        })

        await page.goto('/#/account/profile')
        await profileStarted.promise
        await expect(page.getByTestId('account-page-header')).toBeVisible()
        await expect(page.getByRole('heading', {
            name: '个人资料',
            level: 1,
        })).toBeVisible()
        await expect(page.getByRole('status', {
            name: '正在加载个人资料',
        })).toBeVisible()
        profileGate.resolve()
        await expect(page.getByTestId('account-profile-page')).toBeVisible()

        await page.goto('/#/system-settings/email')
        await emailStarted.promise
        await expect(page.getByTestId('page-header')).toBeVisible()
        await expect(page.getByRole('heading', {
            name: '平台邮件设置',
            level: 1,
        })).toBeVisible()
        await expect(page.getByRole('status', {
            name: '正在加载平台邮件设置',
        })).toBeVisible()
        emailGate.resolve()
        await expect(
            page.getByTestId('email-settings-page-shell')
                .getByText('无法加载邮件配置'),
        ).toBeVisible()
        await expect(page.getByTestId('page-header')).toBeVisible()
        await expect(page.getByRole('heading', {
            name: '平台邮件设置',
            level: 1,
        })).toBeVisible()
    })
})
