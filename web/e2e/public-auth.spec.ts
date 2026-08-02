import { expect, test } from '@playwright/test'
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    mockSessionToken,
    projectA,
    projectB,
} from './helpers/mockHumanSession'

const publicUser = {
    id: 77,
    username: 'e2e-public-human',
    email: 'e2e-public-human@example.test',
    platform_role: 'member',
    status: 'active',
    email_verified: false,
    otp_enabled: false,
    last_login_at: null,
}

test('公开注册页无需会话并提交严格 Human API DTO', async ({ page }) => {
    const consoleErrors: string[] = []
    page.on('console', (message) => {
        if (message.type() === 'error') {
            consoleErrors.push(message.text())
        }
    })
    let submitted: Record<string, unknown> | null = null
    await page.route('**/api/auth/register', async (route) => {
        submitted = route.request().postDataJSON() as Record<string, unknown>
        await route.fulfill({
            status: 201,
            contentType: 'application/json',
            body: JSON.stringify({
                code: 0,
                msg: '注册成功',
                data: {
                    user: publicUser,
                    access_token: '',
                    refresh_token: '',
                    expires_in: 0,
                    token_type: '',
                },
            }),
        })
    })

    await page.goto('/#/register')
    await expect(
        page.getByRole('heading', { name: '注册 ChronoDesk' }),
    ).toBeVisible()
    await page.getByLabel('用户名').fill('e2e-public-human')
    await page.getByLabel('邮箱').fill('e2e-public-human@example.test')
    await page
        .getByRole('textbox', { name: '密码', exact: true })
        .fill('ExamplePassword123!')
    await page
        .getByRole('textbox', { name: '确认密码', exact: true })
        .fill('ExamplePassword123!')
    await page.getByRole('button', { name: '创建账号' }).click()

    await expect(
        page.getByText('注册请求已完成，请查收验证邮件后再登录。'),
    ).toBeVisible()
    expect(submitted).toEqual({
        username: 'e2e-public-human',
        email: 'e2e-public-human@example.test',
        password: 'ExamplePassword123!',
        confirm_password: 'ExamplePassword123!',
    })
    expect(consoleErrors).toEqual([])
})

test('找回密码与重发验证保持防枚举反馈', async ({ page }) => {
    const requests: Array<{ path: string; body: unknown }> = []
    await page.route('**/api/auth/{forgot-password,resend-verification}', async (route) => {
        requests.push({
            path: new URL(route.request().url()).pathname,
            body: route.request().postDataJSON(),
        })
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
                success: true,
                message: '请求已接受',
            }),
        })
    })

    await page.goto('/#/forgot-password')
    await page.getByLabel('邮箱').fill('unknown@example.test')
    await page.getByRole('button', { name: '发送重置邮件' }).click()
    await expect(
        page.getByText(
            '如果该邮箱关联可重置账号，系统会发送一封密码重置邮件。',
        ),
    ).toBeVisible()

    await page.goto('/#/resend-verification')
    await page.getByLabel('邮箱').fill('unknown@example.test')
    await page.getByRole('button', { name: '重新发送' }).click()
    await expect(
        page.getByText(
            '如果该邮箱关联待验证账号，系统会发送一封新的验证邮件。',
        ),
    ).toBeVisible()

    expect(requests).toEqual([
        {
            path: '/api/auth/forgot-password',
            body: { email: 'unknown@example.test' },
        },
        {
            path: '/api/auth/resend-verification',
            body: { email: 'unknown@example.test' },
        },
    ])
})

test('HashRouter 重置页只在 JSON body 发送一次性 token', async ({ page }) => {
    const token = 'reset&token=含 空格'
    let requestURL = ''
    let requestBody: unknown
    await page.route('**/api/auth/reset-password', async (route) => {
        requestURL = route.request().url()
        requestBody = route.request().postDataJSON()
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
                success: true,
                message: '密码重置成功',
            }),
        })
    })

    await page.goto(`/#/reset-password?token=${encodeURIComponent(token)}`)
    await expect(page).toHaveURL(/\/#\/reset-password$/u)
    await page
        .getByRole('textbox', { name: '新密码', exact: true })
        .fill('NewExamplePassword123!')
    await page
        .getByRole('textbox', { name: '确认新密码', exact: true })
        .fill('NewExamplePassword123!')
    await page.getByRole('button', { name: '确认重置密码' }).click()

    await expect(page.getByText('密码已重置，请使用新密码重新登录。')).toBeVisible()
    expect(new URL(requestURL).search).toBe('')
    expect(requestBody).toEqual({
        token,
        new_password: 'NewExamplePassword123!',
    })
    await expect(page).toHaveURL(/\/#\/reset-password$/u)
})

test('邮箱验证只在员工确认后消费 token 并立即清理片段参数', async ({
    page,
}) => {
    const token = 'verify&token=含 空格'
    const requests: unknown[] = []
    await page.route('**/api/auth/verify-email', async (route) => {
        requests.push(route.request().postDataJSON())
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
                success: true,
                message: '邮箱验证成功',
            }),
        })
    })

    await page.goto(`/#/verify-email?token=${encodeURIComponent(token)}`)
    await expect(page).toHaveURL(/\/#\/verify-email$/u)
    await expect(
        page.getByRole('button', { name: '确认验证邮箱' }),
    ).toBeVisible()
    expect(requests).toEqual([])

    await page.getByRole('button', { name: '确认验证邮箱' }).click()
    await expect(page.getByText('邮箱验证成功，现在可以登录。')).toBeVisible()
    expect(requests).toEqual([{ token }])
    await expect(page).toHaveURL(/\/#\/verify-email$/u)
})

test('公共认证标签不会用旧 checkAuth 状态注销新密码会话', async ({
    context,
    page: resetPage,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-cross-tab-password-reset',
        email: 'cross-tab-reset@example.test',
    }
    const user = {
        id: identity.id,
        username: `e2e-${identity.id}`,
        email: identity.email,
        platform_role: identity.platformRole,
        status: 'active',
        email_verified: true,
        otp_enabled: false,
        last_login_at: null,
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    const accessToken = mockSessionToken(identity)
    const oldPassword = 'OldExamplePassword123!'
    const newPassword = 'NewExamplePassword123!'
    let oldPasswordAttempts = 0
    let newPasswordAttempts = 0
    let logoutRequests = 0

    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (
            pathname === '/api/auth/reset-password' ||
            pathname === '/api/auth/verify-email'
        ) {
            await fulfillJSON(route, {
                success: true,
                message: '公开认证操作成功',
            })
            return
        }
        if (
            pathname === '/api/auth/login' &&
            request.method() === 'POST'
        ) {
            const body = request.postDataJSON() as {
                password?: unknown
            }
            if (body.password === oldPassword) {
                oldPasswordAttempts += 1
                await fulfillJSON(
                    route,
                    { code: 1, msg: '邮箱或密码错误' },
                    401,
                )
                return
            }
            if (body.password === newPassword) {
                newPasswordAttempts += 1
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        user,
                        access_token: accessToken,
                        refresh_token: `${identity.sessionID}-refresh`,
                        expires_in: 3600,
                        token_type: 'Bearer',
                    },
                })
                return
            }
            await fulfillJSON(
                route,
                { code: 1, msg: '测试密码不在允许范围内' },
                400,
            )
            return
        }
        if (
            pathname === '/api/auth/logout' &&
            request.method() === 'POST'
        ) {
            logoutRequests += 1
            await fulfillJSON(route, {
                success: true,
                message: '已退出当前会话',
            })
            return
        }
        if (pathname === '/api/auth/me') {
            await fulfillJSON(route, { code: 0, data: user })
            return
        }
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] })
            return
        }
        if (pathname === `/api/projects/${projectA.key}/context`) {
            await fulfillJSON(route, { code: 0, data: access })
            return
        }
        await fulfillJSON(route, { code: 0, data: [] })
    })

    const verifyPage = await context.newPage()
    const loginPage = await context.newPage()
    await loginPage.addInitScript(() => {
        const tracedWindow = window as Window & {
            __chronodeskAuthenticationWrites?: string[]
        }
        tracedWindow.__chronodeskAuthenticationWrites = []
        const originalSetItem = Storage.prototype.setItem
        Storage.prototype.setItem = function (
            key: string,
            value: string,
        ): void {
            if (
                [
                    'refreshToken',
                    'user',
                    'tokenExpiresAt',
                    'token',
                ].includes(key)
            ) {
                tracedWindow.__chronodeskAuthenticationWrites?.push(
                    key,
                )
            }
            originalSetItem.call(this, key, value)
        }
    })

    await resetPage.goto('/#/reset-password?token=cross-tab-reset')
    await resetPage
        .getByRole('textbox', { name: '新密码', exact: true })
        .fill(newPassword)
    await resetPage
        .getByRole('textbox', { name: '确认新密码', exact: true })
        .fill(newPassword)
    await resetPage
        .getByRole('button', { name: '确认重置密码' })
        .click()
    await expect(
        resetPage.getByText('密码已重置，请使用新密码重新登录。'),
    ).toBeVisible()

    await verifyPage.goto('/#/verify-email?token=cross-tab-verify')
    await verifyPage
        .getByRole('button', { name: '确认验证邮箱' })
        .click()
    await expect(
        verifyPage.getByText('邮箱验证成功，现在可以登录。'),
    ).toBeVisible()

    await loginPage.goto('/#/login')
    await loginPage.getByLabel('邮箱').fill(identity.email)
    await loginPage.getByLabel('密码').fill(oldPassword)
    await loginPage
        .getByRole('button', { name: '登录系统', exact: true })
        .click()
    await expect(loginPage.getByRole('alert')).toContainText(
        '邮箱或密码错误',
    )

    await loginPage.evaluate(() => {
        const tracedWindow = window as Window & {
            __chronodeskAuthenticationWrites?: string[]
        }
        tracedWindow.__chronodeskAuthenticationWrites?.splice(0)
    })
    await loginPage.getByLabel('密码').fill(newPassword)
    await loginPage
        .getByRole('button', { name: '登录系统', exact: true })
        .click()
    await expect(loginPage).toHaveURL(/\/#\/$/u)
    await expect
        .poll(() =>
            loginPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBe(accessToken)
    await expect
        .poll(() =>
            loginPage.evaluate(() => {
                const tracedWindow = window as Window & {
                    __chronodeskAuthenticationWrites?: string[]
                }
                return tracedWindow.__chronodeskAuthenticationWrites
            }),
        )
        .toEqual([
            'refreshToken',
            'user',
            'tokenExpiresAt',
            'token',
        ])

    for (const publicPage of [resetPage, verifyPage]) {
        await publicPage.evaluate(
            () =>
                new Promise<void>((resolve) => {
                    requestAnimationFrame(() =>
                        requestAnimationFrame(() => resolve()),
                    )
                }),
        )
        await publicPage.evaluate(() => {
            window.location.hash = '#/'
        })
        await expect(
            publicPage.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toBeVisible()
    }

    expect(oldPasswordAttempts).toBe(1)
    expect(newPasswordAttempts).toBe(1)
    expect(logoutRequests).toBe(0)
    await expect
        .poll(() =>
            loginPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBe(accessToken)
})

test('受保护标签在跨账号登录和退出后重载身份且不提交旧缓存', async ({
    context,
    page: loginPage,
}) => {
    const identityA = {
        ...defaultMockIdentity,
        sessionID: 'e2e-protected-tab-user-a',
        email: 'protected-user-a@example.test',
    }
    const identityB = {
        ...defaultMockIdentity,
        id: 84,
        sessionID: 'e2e-protected-tab-user-b',
        email: 'protected-user-b@example.test',
    }
    const sharedExpiresAtSeconds =
        Math.floor(Date.now() / 1000) + 3600
    let tokenA = ''
    const tokenB = mockSessionToken(
        identityB,
        sharedExpiresAtSeconds,
    )
    const accessA = authorizedProjectAccess(projectA, 'requester')
    const accessB = authorizedProjectAccess(projectB, 'observer')
    let logoutRequests = 0
    let markLoginRequestStarted: (() => void) | undefined
    const loginRequestStarted = new Promise<void>((resolve) => {
        markLoginRequestStarted = resolve
    })
    let releaseLoginResponse: (() => void) | undefined
    const loginResponseReleased = new Promise<void>((resolve) => {
        releaseLoginResponse = resolve
    })

    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        const authorization = request.headers().authorization ?? ''
        const usingA = authorization === `Bearer ${tokenA}`
        const usingB = authorization === `Bearer ${tokenB}`
        if (
            authorization.startsWith('Bearer ') &&
            !usingA &&
            !usingB
        ) {
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    message: '测试拒绝未知会话',
                },
                401,
            )
            return
        }
        const identity = usingB ? identityB : identityA
        const access = usingB ? accessB : accessA
        const user = {
            id: identity.id,
            username: `e2e-${identity.id}`,
            email: identity.email,
            platform_role: identity.platformRole,
            status: 'active',
            email_verified: true,
            otp_enabled: false,
            last_login_at: null,
        }
        if (
            pathname === '/api/auth/login' &&
            request.method() === 'POST'
        ) {
            const body = request.postDataJSON() as {
                password?: unknown
            }
            if (body.password === 'WrongPassword123!') {
                await fulfillJSON(
                    route,
                    { code: 1, msg: '邮箱或密码错误' },
                    401,
                )
                return
            }
            markLoginRequestStarted?.()
            await loginResponseReleased
            await fulfillJSON(route, {
                code: 0,
                data: {
                    user: {
                        ...user,
                        id: identityB.id,
                        username: `e2e-${identityB.id}`,
                        email: identityB.email,
                    },
                    access_token: tokenB,
                    refresh_token: `${identityB.sessionID}-refresh`,
                    expires_in: 3600,
                    token_type: 'Bearer',
                },
            })
            return
        }
        if (
            pathname === '/api/auth/logout' &&
            request.method() === 'POST'
        ) {
            logoutRequests += 1
            await fulfillJSON(route, {
                success: true,
                message: '已退出当前会话',
            })
            return
        }
        if (pathname === '/api/auth/me') {
            await fulfillJSON(route, { code: 0, data: user })
            return
        }
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] })
            return
        }
        if (
            pathname ===
            `/api/projects/${access.project.key}/context`
        ) {
            await fulfillJSON(route, { code: 0, data: access })
            return
        }
        await fulfillJSON(route, { code: 0, data: [] })
    })

    await loginPage.goto('/#/login')
    await expect(
        loginPage.getByRole('button', {
            name: '登录系统',
            exact: true,
        }),
    ).toBeVisible()

    const protectedPage = await context.newPage()
    tokenA = await installMockSession(
        protectedPage,
        identityA,
        undefined,
        sharedExpiresAtSeconds,
    )
    await protectedPage.goto('/#/')
    await expect
        .poll(() =>
            protectedPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBe(tokenA)
    await expect(
        protectedPage.getByTestId('account-menu'),
    ).toContainText(`e2e-${identityA.id}`)

    await loginPage.getByLabel('邮箱').fill(identityB.email)
    await loginPage.getByLabel('密码').fill('WrongPassword123!')
    await loginPage
        .getByRole('button', { name: '登录系统', exact: true })
        .click()
    await expect(loginPage.getByRole('alert')).toContainText(
        '邮箱或密码错误',
    )
    await expect
        .poll(() =>
            protectedPage.evaluate(() =>
                localStorage.getItem('token'),
            ),
        )
        .toBe(tokenA)
    await expect(
        protectedPage.getByTestId('account-menu'),
    ).toContainText(`e2e-${identityA.id}`)

    await loginPage.getByLabel('邮箱').fill(identityB.email)
    await loginPage.getByLabel('密码').fill('CorrectPassword123!')
    const loginSubmission = loginPage
        .getByRole('button', { name: '登录系统', exact: true })
        .click()
    await loginRequestStarted
    await expect
        .poll(() =>
            protectedPage.evaluate(() =>
                localStorage.getItem('token'),
            ),
        )
        .toBe(tokenA)
    await expect(
        protectedPage.getByTestId('account-menu'),
    ).toContainText(`e2e-${identityA.id}`)
    releaseLoginResponse?.()
    await loginSubmission
    await expect(loginPage).toHaveURL(/\/#\/$/u)
    await expect(
        loginPage.getByTestId('account-menu'),
    ).toContainText(`e2e-${identityB.id}`)

    await expect
        .poll(async () =>
            protectedPage
                .getByTestId('account-menu')
                .textContent(),
        )
        .toContain(`e2e-${identityB.id}`)
    expect(logoutRequests).toBe(0)
    await expect(
        protectedPage.getByText(projectA.name, { exact: true }),
    ).toHaveCount(0)

    await loginPage
        .getByTestId('account-menu')
        .locator('button')
        .first()
        .click()
    await loginPage
        .getByRole('menuitem', { name: '退出登录', exact: true })
        .click()
    await expect(loginPage).toHaveURL(/\/#\/login$/u)
    await expect(protectedPage).toHaveURL(/\/#\/login$/u)
    expect(logoutRequests).toBe(1)
})

test('全设备退出在同一应用路径内替换为登录 Hash 路由', async ({ page }) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-logout-all-hash-navigation',
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    await installMockSession(page, identity, projectA)
    let logoutAllRequests = 0
    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (
            pathname === '/api/auth/logout-all' &&
            request.method() === 'POST'
        ) {
            logoutAllRequests += 1
            await fulfillJSON(route, {
                success: true,
                message: '已从所有设备退出登录',
            })
            return
        }
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] })
            return
        }
        if (pathname === `/api/projects/${projectA.key}/context`) {
            await fulfillJSON(route, { code: 0, data: access })
            return
        }
        await fulfillJSON(route, { code: 0, data: [] })
    })

    await page.goto('/#/')
    await page.getByTestId('account-menu').locator('button').first().click()
    await page.getByTestId('logout-all-sessions').click()

    await expect(page).toHaveURL(/\/#\/login$/u)
    expect(new URL(page.url()).pathname).toBe('/')
    expect(logoutAllRequests).toBe(1)
    await expect
        .poll(() =>
            page.evaluate(() => localStorage.getItem('token')),
        )
        .toBeNull()
})

test('无会话访问受保护资源仍由 checkAuth 强制跳转登录', async ({ page }) => {
    await page.goto('/#/login')
    await expect(
        page.getByRole('button', { name: '登录系统', exact: true }),
    ).toBeVisible()

    await page.goto('/#/tickets')

    await expect(page).toHaveURL(/\/#\/login$/u)
    await expect(
        page.getByRole('button', { name: '登录系统', exact: true }),
    ).toBeVisible()
    await expect(page.getByRole('heading', { name: '工单管理' })).toHaveCount(0)
})
