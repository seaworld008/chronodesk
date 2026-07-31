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
            language: 'zh-CN',
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
            otp_enabled: false,
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

        await expect(page.getByRole('button', { name: /^工作台/ })).toHaveCount(0)
        const workbench = page.getByRole('menuitem', { name: '工作台', exact: true })
        await expect(workbench).toBeVisible()
        await workbench.click()
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
        await page.getByLabel('名字').fill('Updated')
        await page.getByRole('button', { name: '保存个人资料' }).click()
        await expect.poll(() => profileWrites.length).toBe(1)
        expect(Object.keys(profileWrites[0]).sort()).toEqual([
            'first_name',
            'language',
            'last_name',
            'timezone',
        ])

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
    })
})
