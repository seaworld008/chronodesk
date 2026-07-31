import { expect, test, type Page } from '@playwright/test'

const encode = (value: unknown) =>
    Buffer.from(JSON.stringify(value)).toString('base64url')

const installSession = async (page: Page, sessionID = 'task3-session') => {
    const expiresAt = Math.floor(Date.now() / 1000) + 3600
    const token = `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
        sub: '1',
        sid: sessionID,
        platform_role: 'platform_admin',
        exp: expiresAt,
    })}.signature`
    await page.addInitScript(({ accessToken, exp, sid }) => {
        if (sessionStorage.getItem('task3-preserve-session') === 'true') {
            return
        }
        localStorage.setItem('token', accessToken)
        localStorage.setItem('refreshToken', 'task3-refresh')
        localStorage.setItem('tokenExpiresAt', String(exp * 1000))
        localStorage.setItem('user', JSON.stringify({
            id: 1,
            username: 'task3-admin',
            email: 'task3@example.invalid',
            platform_role: 'platform_admin',
            status: 'active',
            email_verified: true,
            otp_enabled: false,
        }))
        localStorage.setItem('chronodesk.activeProject', JSON.stringify({
            subject: '1',
            session_id: sid,
            project_key: 'OPS',
        }))
    }, { accessToken: token, exp: expiresAt, sid: sessionID })
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

test.describe('Task 3 导航、账号与多选回归（mock）', () => {
    test('树契约、单叶直达、持久化、账号白名单、Webhook clear 和登录分页', async ({ page }) => {
        await installSession(page)
        const profileWrites: Array<Record<string, unknown>> = []
        const loginPages: number[] = []
        const passwordWrites: Array<Record<string, unknown>> = []
        const otpVerifications: string[] = []
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
        await expect(page.getByRole('heading', { name: '平台公共配置' })).toBeVisible()
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

        await page.goto('/#/account/profile')
        await expect(page.getByRole('heading', { name: '个人资料' })).toBeVisible()
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
        await expect(page.getByTestId('account-profile-page'))
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
                form: rect('profile-main-form'),
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
            { path: 'profile', title: '个人资料' },
            { path: 'security', title: '账号安全' },
            { path: 'trusted-devices', title: '可信设备' },
            { path: 'login-history', title: '登录历史' },
        ]) {
            await page.goto(`/#/account/${accountPage.path}`)
            const header = page.getByTestId('account-page-header')
            await expect(header).toHaveCount(1)
            await expect(header).toHaveCSS('flex-direction', 'column')
            await expect(header).toContainText(accountPage.title)
        }
        await page.setViewportSize({ width: 1280, height: 720 })
        for (const accountPage of [
            { path: 'profile', title: '个人资料' },
            { path: 'security', title: '账号安全' },
            { path: 'trusted-devices', title: '可信设备' },
            { path: 'login-history', title: '登录历史' },
        ]) {
            await page.goto(`/#/account/${accountPage.path}`)
            const header = page.getByTestId('account-page-header')
            await expect(header).toHaveCount(1)
            await expect(header).toHaveCSS('flex-direction', 'row')
            await expect(header).toContainText(accountPage.title)
        }

        await page.goto('/#/account/login-history')
        await expect(page.getByRole('heading', { name: '登录历史' })).toBeVisible()
        await page.getByRole('button', { name: /下一页|next page/i }).click()
        await expect.poll(() => loginPages).toContain(2)

        await page.goto('/#/webhook-settings')
        await expect(page.getByRole('heading', { name: 'Webhook 集成' })).toBeVisible()
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
        await expect(page.getByRole('heading', { name: '账号安全' })).toBeVisible()
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
})
