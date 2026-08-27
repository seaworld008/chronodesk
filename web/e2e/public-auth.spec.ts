import { expect, test, type Page } from '@playwright/test'
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    fulfillMockSessionRefresh,
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

const registrationPassword = 'ExamplePassword123!'

const submitRegistration = async (
    page: Page,
    username: string,
    email: string,
): Promise<void> => {
    await page.getByLabel('用户名').fill(username)
    await page.getByLabel('邮箱').fill(email)
    await page
        .getByRole('textbox', { name: '密码', exact: true })
        .fill(registrationPassword)
    await page
        .getByRole('textbox', { name: '确认密码', exact: true })
        .fill(registrationPassword)
    await page.getByRole('button', { name: '创建账号' }).click()
}

const readAuthenticationStorage = (page: Page) =>
    page.evaluate(() => ({
        token: localStorage.getItem('token'),
        refreshToken: localStorage.getItem('refreshToken'),
        user: localStorage.getItem('user'),
        tokenExpiresAt: localStorage.getItem('tokenExpiresAt'),
    }))

const emptyAuthenticationStorage = {
    token: null,
    refreshToken: null,
    user: null,
    tokenExpiresAt: null,
}

const encodeJwtSegment = (value: unknown): string =>
    btoa(JSON.stringify(value))
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/u, '')

const registrationTokenWithPayload = (payload: Record<string, unknown>) =>
    [
        encodeJwtSegment({ alg: 'none', typ: 'JWT' }),
        encodeJwtSegment(payload),
        'e2e-signature',
    ].join('.')

test('公开注册页无需会话并提交严格 Human API DTO', async ({ page }) => {
    const consoleErrors: string[] = []
    page.on('console', (message) => {
        if (message.type() === 'error') {
            consoleErrors.push(message.text())
        }
    })
    let submitted: Record<string, unknown> | null = null
    let projectListRequests = 0
    await page.addInitScript(() => {
        const tracedWindow = window as Window & {
            __chronodeskRegistrationCredentials?: Array<
                RequestCredentials | undefined
            >
        }
        tracedWindow.__chronodeskRegistrationCredentials = []
        const originalFetch = window.fetch.bind(window)
        window.fetch = (
            input: RequestInfo | URL,
            init?: RequestInit,
        ): Promise<Response> => {
            const target =
                typeof input === 'string'
                    ? input
                    : input instanceof URL
                      ? input.toString()
                      : input.url
            if (
                new URL(target, window.location.origin).pathname ===
                '/api/auth/register'
            ) {
                tracedWindow.__chronodeskRegistrationCredentials?.push(
                    init?.credentials,
                )
            }
            return originalFetch(input, init)
        }
    })
    await page.route('**/api/projects**', async (route) => {
        projectListRequests += 1
        await fulfillJSON(route, { code: 0, data: [] })
    })
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
    await submitRegistration(page, publicUser.username, publicUser.email)

    await expect(
        page.getByText('注册请求已完成，请查收验证邮件后再登录。'),
    ).toBeVisible()
    expect(submitted).toEqual({
        username: 'e2e-public-human',
        email: 'e2e-public-human@example.test',
        password: 'ExamplePassword123!',
        confirm_password: 'ExamplePassword123!',
    })
    expect(projectListRequests).toBe(0)
    expect(
        await page.evaluate(() => {
            const tracedWindow = window as Window & {
                __chronodeskRegistrationCredentials?: Array<
                    RequestCredentials | undefined
                >
            }
            return tracedWindow.__chronodeskRegistrationCredentials
        }),
    ).toEqual(['include'])
    expect(await readAuthenticationStorage(page)).toEqual(
        emptyAuthenticationStorage,
    )
    expect(consoleErrors).toEqual([])
})

test('已验证注册会话按登录路径写入并进入根路由', async ({ page }) => {
    const identity = {
        id: 78,
        sessionID: 'e2e-registration-complete-session',
        email: 'e2e-registration-complete@example.test',
        platformRole: 'member' as const,
    }
    const accessToken = mockSessionToken(identity)
    const user = {
        id: identity.id,
        username: 'e2e-registration-complete',
        email: identity.email,
        platform_role: identity.platformRole,
        status: 'active',
        email_verified: true,
        otp_enabled: false,
        last_login_at: null,
    }
    let projectListRequests = 0
    let projectListAuthorization = ''

    await page.addInitScript(() => {
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
                tracedWindow.__chronodeskAuthenticationWrites?.push(key)
            }
            originalSetItem.call(this, key, value)
        }
    })
    await page.route('**/api/**', async (route) => {
        const pathname = new URL(route.request().url()).pathname
        if (pathname === '/api/auth/register') {
            await fulfillJSON(
                route,
                {
                    code: 0,
                    msg: '注册成功',
                    data: {
                        user,
                        access_token: accessToken,
                        expires_in: 3600,
                        token_type: 'Bearer',
                    },
                },
                201,
            )
            return
        }
        if (pathname === '/api/projects') {
            projectListRequests += 1
            projectListAuthorization =
                route.request().headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
            return
        }
        await fulfillJSON(route, { code: 0, data: [] })
    })

    await page.goto('/#/register')
    await submitRegistration(page, user.username, user.email)

    await expect(page).toHaveURL(/\/#\/$/u)
    await expect(page.getByTestId('no-authorized-projects')).toBeVisible()
    expect(projectListRequests).toBeGreaterThan(0)
    expect(projectListAuthorization).toBe(`Bearer ${accessToken}`)
    await expect
        .poll(() => readAuthenticationStorage(page))
        .toMatchObject({
            token: null,
            refreshToken: null,
        })
    expect(
        await page.evaluate(() => {
            const tracedWindow = window as Window & {
                __chronodeskAuthenticationWrites?: string[]
            }
            return tracedWindow.__chronodeskAuthenticationWrites
        }),
    ).toEqual(['user', 'tokenExpiresAt'])
})

test('已验证注册的残缺成功响应不写入部分会话', async ({ page }) => {
    const identity = {
        id: 79,
        sessionID: 'e2e-registration-incomplete-session',
        email: 'e2e-registration-incomplete@example.test',
        platformRole: 'member' as const,
    }
    await page.route('**/api/auth/register', async (route) => {
        await fulfillJSON(
            route,
            {
                code: 0,
                msg: '注册成功',
                data: {
                    user: {
                        id: identity.id,
                        username: 'e2e-registration-incomplete',
                        email: identity.email,
                        platform_role: identity.platformRole,
                        status: 'active',
                        email_verified: true,
                        otp_enabled: false,
                        last_login_at: null,
                    },
                    access_token: '',
                    expires_in: 3600,
                    token_type: 'Bearer',
                },
            },
            201,
        )
    })

    await page.goto('/#/register')
    await submitRegistration(
        page,
        'e2e-registration-incomplete',
        identity.email,
    )

    await expect(page.getByRole('alert')).toContainText(
        '注册响应包含无效的用户或会话信息',
    )
    await expect(page).toHaveURL(/\/#\/register$/u)
    await expect(
        page.getByText('注册成功，现在可以登录。'),
    ).toHaveCount(0)
    expect(await readAuthenticationStorage(page)).toEqual(
        emptyAuthenticationStorage,
    )
})

test('未验证注册拒绝混合会话而不请求项目列表', async ({ page }) => {
    let projectListRequests = 0
    await page.route('**/api/projects**', async (route) => {
        projectListRequests += 1
        await fulfillJSON(route, { code: 0, data: [] })
    })
    await page.route('**/api/auth/register', async (route) => {
        await fulfillJSON(
            route,
            {
                code: 0,
                msg: '注册成功',
                data: {
                    user: publicUser,
                    access_token: '',
                    refresh_token: 'unexpected-refresh-token',
                    expires_in: 0,
                    token_type: '',
                },
            },
            201,
        )
    })

    await page.goto('/#/register')
    await submitRegistration(page, publicUser.username, publicUser.email)

    await expect(page.getByRole('alert')).toContainText(
        '注册响应包含无效的用户或会话信息',
    )
    await expect(page).toHaveURL(/\/#\/register$/u)
    expect(projectListRequests).toBe(0)
    await expect(
        page.getByText('注册请求已完成，请查收验证邮件后再登录。'),
    ).toHaveCount(0)
})

const invalidRegistrationIdentity = {
    id: 80,
    sessionID: 'e2e-registration-validation-session',
    email: 'e2e-registration-validation@example.test',
    platformRole: 'member' as const,
}

const validVerifiedRegistrationResult = () => ({
    user: {
        id: invalidRegistrationIdentity.id,
        username: 'e2e-registration-validation',
        email: invalidRegistrationIdentity.email,
        platform_role: invalidRegistrationIdentity.platformRole,
        status: 'active',
        email_verified: true,
        otp_enabled: false,
        last_login_at: null,
    },
    access_token: mockSessionToken(invalidRegistrationIdentity),
    expires_in: 3600,
    token_type: 'Bearer',
})

const malformedRegistrationCases: Array<{
    name: string
    result: () => Record<string, unknown>
}> = [
    {
        name: '格式错误的 JWT',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            access_token: 'not-a-jwt',
        }),
    },
    {
        name: 'JWT subject 与用户不匹配',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            access_token: mockSessionToken({
                ...invalidRegistrationIdentity,
                id: invalidRegistrationIdentity.id + 1,
            }),
        }),
    },
    {
        name: 'JWT platform role 与用户不匹配',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            user: {
                ...validVerifiedRegistrationResult().user,
                platform_role: 'platform_admin',
            },
        }),
    },
    {
        name: 'JWT session ID 为空',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            access_token: registrationTokenWithPayload({
                sub: String(invalidRegistrationIdentity.id),
                sid: '',
                platform_role: invalidRegistrationIdentity.platformRole,
                exp: Math.floor(Date.now() / 1000) + 3600,
            }),
        }),
    },
    {
        name: 'JWT 缺少 session ID',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            access_token: registrationTokenWithPayload({
                sub: String(invalidRegistrationIdentity.id),
                platform_role: invalidRegistrationIdentity.platformRole,
                exp: Math.floor(Date.now() / 1000) + 3600,
            }),
        }),
    },
    {
        name: '会话字段类型错误',
        result: () => ({
            ...validVerifiedRegistrationResult(),
            expires_in: '3600',
        }),
    },
]

for (const { name, result } of malformedRegistrationCases) {
    test(`已验证注册拒绝${name}且不写入认证状态`, async ({ page }) => {
        let projectListRequests = 0
        await page.route('**/api/projects**', async (route) => {
            projectListRequests += 1
            await fulfillJSON(route, { code: 0, data: [] })
        })
        await page.route('**/api/auth/register', async (route) => {
            await fulfillJSON(
                route,
                {
                    code: 0,
                    msg: '注册成功',
                    data: result(),
                },
                201,
            )
        })

        await page.goto('/#/register')
        await submitRegistration(
            page,
            'e2e-registration-validation',
            invalidRegistrationIdentity.email,
        )

        await expect(page.getByRole('alert')).toContainText(
            '注册响应包含无效的用户或会话信息',
        )
        await expect(page).toHaveURL(/\/#\/register$/u)
        expect(await readAuthenticationStorage(page)).toEqual(
            emptyAuthenticationStorage,
        )
        expect(projectListRequests).toBe(0)
    })
}

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
    let refreshRequests = 0
    let projectAuthorization = ''

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
                    msg: '登录成功',
                    data: {
                        user,
                        access_token: accessToken,
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
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST'
        ) {
            refreshRequests += 1
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: accessToken,
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
            projectAuthorization =
                request.headers().authorization ?? ''
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
        .toBeNull()
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
            'user',
            'tokenExpiresAt',
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
    expect(refreshRequests).toBeGreaterThanOrEqual(2)
    expect(projectAuthorization).toBe(`Bearer ${accessToken}`)
    await expect
        .poll(() =>
            loginPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBeNull()
})

test('同一 Human session 跨标签刷新不泄漏 bearer且本标签轮换后续请求', async ({
    context,
    page: protectedPage,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-same-session-refresh-rotation',
        email: 'same-session-refresh@example.test',
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    const oldToken = await installMockSession(
        protectedPage,
        identity,
        projectA,
    )
    const rotatedToken = mockSessionToken(
        identity,
        Math.floor(Date.now() / 1000) + 12 * 60 * 60,
    )
    const user = {
        id: identity.id,
        username: `e2e-${identity.id}`,
        email: identity.email,
        platform_role: identity.platformRole,
        status: 'active',
        email_verified: true,
        otp_enabled: false,
        last_login_at: null,
        profile: {
            id: identity.id,
            user_id: identity.id,
            first_name: '服务端名字',
            last_name: '已加载查询态',
            display_name: '服务端名字 已加载查询态',
            avatar: '',
            phone: '',
            department: '',
            position: '',
            timezone: 'Asia/Shanghai',
            language: 'zh-CN',
            created_at: '2026-08-10T00:00:00Z',
            updated_at: '2026-08-10T00:00:00Z',
        },
    }
    let refreshRequests = 0
    let logoutRequests = 0
    let profileGetRequests = 0
    let profileUpdateBearer = ''
    let staleRequestBearer = ''
    let markRefreshRequestStarted: (() => void) | undefined
    const refreshRequestStarted = new Promise<void>((resolve) => {
        markRefreshRequestStarted = resolve
    })
    let releaseRefreshResponse: (() => void) | undefined
    const refreshResponseReleased = new Promise<void>((resolve) => {
        releaseRefreshResponse = resolve
    })
    let markStaleRequestStarted: (() => void) | undefined
    const staleRequestStarted = new Promise<void>((resolve) => {
        markStaleRequestStarted = resolve
    })
    let releaseStaleResponse: (() => void) | undefined
    const staleResponseReleased = new Promise<void>((resolve) => {
        releaseStaleResponse = resolve
    })

    await protectedPage.addInitScript(() => {
        const key = 'chronodesk.e2e.protected-load-count'
        const next = Number(sessionStorage.getItem(key) ?? '0') + 1
        sessionStorage.setItem(key, String(next))
    })
    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        const authorization = request.headers().authorization ?? ''
        if (
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST'
        ) {
            refreshRequests += 1
            markRefreshRequestStarted?.()
            await refreshResponseReleased
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: rotatedToken,
                    expires_in: 12 * 60 * 60,
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
        if (
            pathname === '/api/auth/profile' &&
            request.method() === 'PUT'
        ) {
            profileUpdateBearer = authorization
            await fulfillJSON(route, { code: 0, data: user })
            return
        }
        if (pathname === '/api/e2e/stale-same-session-401') {
            staleRequestBearer = authorization
            markStaleRequestStarted?.()
            await staleResponseReleased
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    message: '旧标签 bearer 已失效',
                },
                401,
            )
            return
        }
        if (pathname === '/api/auth/me') {
            profileGetRequests += 1
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

    await protectedPage.goto('/#/account/profile')
    await expect(protectedPage.getByTestId('account-profile-page')).toBeVisible()
    await expect(protectedPage.getByLabel('姓氏')).toHaveValue('已加载查询态')
    await protectedPage.getByLabel('名字').fill('受保护标签未提交表单')
    const profileGetRequestsBeforeRotation = profileGetRequests
    expect(profileGetRequestsBeforeRotation).toBeGreaterThan(0)
    expect(
        await protectedPage.evaluate(() =>
            sessionStorage.getItem(
                'chronodesk.e2e.protected-load-count',
            ),
        ),
    ).toBe('1')

    const staleRequest = protectedPage.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        try {
            await apiModule.apiFetch('/e2e/stale-same-session-401')
            return null
        } catch (error) {
            return error instanceof Error
                ? error.message
                : String(error)
        }
    }, '/src/lib/apiClient.ts')
    await staleRequestStarted

    const refreshPage = await context.newPage()
    await refreshPage.clock.install({
        time: Date.now() + 3 * 60 * 60 * 1000,
    })
    const refreshNavigation = refreshPage.goto('/#/')
    await refreshRequestStarted

    await expect
        .poll(() =>
            protectedPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBeNull()
    await expect(protectedPage.getByLabel('名字')).toHaveValue(
        '受保护标签未提交表单',
    )

    releaseRefreshResponse?.()
    await refreshNavigation
    await expect
        .poll(() =>
            protectedPage.evaluate(async (modulePath) => {
                const runtime = await import(
                    /* @vite-ignore */ modulePath
                ) as {
                    readHumanAccessToken: () => string | null
                }
                return runtime.readHumanAccessToken()
            }, '/src/lib/humanSessionRuntime.ts'),
        )
        .toBe(rotatedToken)
    await expect
        .poll(() =>
            protectedPage.evaluate(() => localStorage.getItem('token')),
        )
        .toBeNull()
    releaseStaleResponse?.()
    expect(await staleRequest).toContain('登录状态已失效')
    await protectedPage.waitForTimeout(250)

    expect(refreshRequests).toBe(2)
    expect(logoutRequests).toBe(0)
    expect(staleRequestBearer).toBe(`Bearer ${oldToken}`)
    expect(profileGetRequests).toBe(profileGetRequestsBeforeRotation)
    await expect(protectedPage).toHaveURL(/\/#\/account\/profile$/u)
    await expect(protectedPage.getByLabel('名字')).toHaveValue(
        '受保护标签未提交表单',
    )
    await expect(protectedPage.getByLabel('姓氏')).toHaveValue('已加载查询态')
    expect(
        await protectedPage.evaluate(() =>
            sessionStorage.getItem(
                'chronodesk.e2e.protected-load-count',
            ),
        ),
    ).toBe('1')

    await protectedPage
        .getByRole('button', { name: '保存个人资料' })
        .click()
    await expect
        .poll(() => profileUpdateBearer)
        .toBe(`Bearer ${rotatedToken}`)
    expect(logoutRequests).toBe(0)
    expect(
        await protectedPage.evaluate(() =>
            sessionStorage.getItem(
                'chronodesk.e2e.protected-load-count',
            ),
        ),
    ).toBe('1')
})

test('当前 bearer 401 只清本标签且不撤销共享 refresh 会话', async ({
    page,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-local-only-passive-401',
        email: 'local-only-passive-401@example.test',
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    await installMockSession(page, identity, projectA)
    let logoutRequests = 0

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/e2e/current-session-401') {
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    message: '当前 bearer 已失效',
                },
                401,
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
    await expect(page.getByTestId('account-menu')).toBeVisible()
    const errorMessage = await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        try {
            await apiModule.apiFetch('/e2e/current-session-401')
            return null
        } catch (error) {
            return error instanceof Error
                ? error.message
                : String(error)
        }
    }, '/src/lib/apiClient.ts')

    expect(errorMessage).toContain('登录状态已失效')
    await expect(page).toHaveURL(/\/#\/login$/u)
    expect(logoutRequests).toBe(0)
    await expect
        .poll(() =>
            page.evaluate(() => ({
                user: sessionStorage.getItem('user'),
                expiresAt: sessionStorage.getItem('tokenExpiresAt'),
                project: sessionStorage.getItem(
                    'chronodesk.activeProject',
                ),
            })),
        )
        .toEqual({
            user: null,
            expiresAt: null,
            project: null,
        })
})

test('同会话远端同步瞬态失败恢复原标签 bearer', async ({ page }) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-remote-sync-transient-failure',
        email: 'remote-sync-transient@example.test',
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    const token = await installMockSession(page, identity, projectA)
    let refreshRequests = 0
    let probeAuthorization = ''

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST'
        ) {
            refreshRequests += 1
            await fulfillJSON(
                route,
                {
                    code: 'temporarily_unavailable',
                    message: '刷新服务暂时不可用',
                },
                503,
            )
            return
        }
        if (pathname === '/api/e2e/remote-sync-probe') {
            probeAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
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
    const synchronizationError = await page.evaluate(
        async ({ modulePath, metadata }) => {
            const authModule = await import(
                /* @vite-ignore */ modulePath
            ) as {
                synchronizeHumanSessionAfterRemoteAuthentication: (
                    value: typeof metadata,
                ) => Promise<boolean>
            }
            try {
                await authModule
                    .synchronizeHumanSessionAfterRemoteAuthentication(
                        metadata,
                    )
                return null
            } catch (error) {
                return error instanceof Error
                    ? error.message
                    : String(error)
            }
        },
        {
            modulePath: '/src/lib/authProvider.ts',
            metadata: {
                type: 'authenticated' as const,
                subject: String(identity.id),
                session_id: identity.sessionID,
                expires_at: Date.now() + 3600_000,
                issued_at: Date.now() + 1000,
            },
        },
    )
    expect(synchronizationError).toContain('刷新服务暂时不可用')

    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/remote-sync-probe')
    }, '/src/lib/apiClient.ts')

    expect(refreshRequests).toBe(1)
    expect(probeAuthorization).toBe(`Bearer ${token}`)
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page).toHaveURL(/\/#\/$/u)
})

const reloadProjectSelectionScenarios = [
    {
        name: '同一 subject 和 session ID 时保留项目选择',
        refreshedIdentity: {
            ...defaultMockIdentity,
            sessionID: 'e2e-reload-project-same-session',
            email: 'reload-project-same-session@example.test',
        },
        storedSelection: 'valid',
        shouldPreserve: true,
    },
    {
        name: 'session ID 变化时清除项目选择',
        refreshedIdentity: {
            ...defaultMockIdentity,
            sessionID: 'e2e-reload-project-replaced-session',
            email: 'reload-project-replaced-session@example.test',
        },
        storedSelection: 'valid',
        shouldPreserve: false,
    },
    {
        name: 'subject 变化时清除项目选择',
        refreshedIdentity: {
            ...defaultMockIdentity,
            id: defaultMockIdentity.id + 1,
            sessionID: 'e2e-reload-project-other-subject',
            email: 'reload-project-other-subject@example.test',
        },
        storedSelection: 'valid',
        shouldPreserve: false,
    },
    {
        name: '项目绑定记录畸形时清除项目选择',
        refreshedIdentity: {
            ...defaultMockIdentity,
            sessionID: 'e2e-reload-project-malformed-selection',
            email: 'reload-project-malformed-selection@example.test',
        },
        storedSelection: 'malformed',
        shouldPreserve: false,
    },
] as const

for (const scenario of reloadProjectSelectionScenarios) {
    test(`页面 reload 后 cookie refresh ${scenario.name}`, async ({ page }) => {
        const originalIdentity = {
            ...scenario.refreshedIdentity,
            id: defaultMockIdentity.id,
            sessionID:
                scenario.name === 'session ID 变化时清除项目选择'
                    ? 'e2e-reload-project-original-session'
                    : scenario.refreshedIdentity.sessionID,
            email: 'reload-project-original@example.test',
        }
        const access = authorizedProjectAccess(projectA, 'requester')
        await installMockSession(page, originalIdentity, projectA)
        let refreshRequests = 0

        await page.route('**/api/**', async (route) => {
            const request = route.request()
            const pathname = new URL(request.url()).pathname
            if (
                pathname === '/api/auth/refresh' &&
                request.method() === 'POST'
            ) {
                refreshRequests += 1
                await fulfillMockSessionRefresh(
                    route,
                    scenario.refreshedIdentity,
                )
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
        await expect(page.getByTestId('active-project-switcher')).toBeVisible()
        expect(refreshRequests).toBe(0)
        if (scenario.storedSelection === 'malformed') {
            await page.evaluate(() => {
                sessionStorage.setItem(
                    'chronodesk.activeProject',
                    JSON.stringify({
                        subject: String(42),
                        project_key: 'OPS',
                        epoch: 1,
                    }),
                )
            })
        }
        await page.reload()
        await expect.poll(() => refreshRequests).toBe(1)

        if (scenario.shouldPreserve) {
            await expect(
                page.getByTestId('active-project-switcher'),
            ).toBeVisible()
            await expect
                .poll(() =>
                    page.evaluate(() => {
                        const serialized = sessionStorage.getItem(
                            'chronodesk.activeProject',
                        )
                        return serialized
                            ? JSON.parse(serialized).project_key
                            : null
                    }),
                )
                .toBe(projectA.key)
            return
        }

        await expect(
            page.getByTestId('active-project-selection-required'),
        ).toBeVisible()
        await expect
            .poll(() =>
                page.evaluate(() =>
                    sessionStorage.getItem('chronodesk.activeProject'),
                ),
            )
            .toBeNull()
    })
}

const reloadRefreshFailureScenarios = [
    {
        name: '401',
        outcome: 'unauthorized',
        shouldClearSelection: true,
    },
    {
        name: '无效成功响应',
        outcome: 'invalid',
        shouldClearSelection: true,
    },
    {
        name: '网络瞬态失败',
        outcome: 'transient',
        shouldClearSelection: false,
    },
] as const

for (const scenario of reloadRefreshFailureScenarios) {
    test(`页面 reload 后 cookie refresh ${scenario.name} 不授予项目访问`, async ({
        page,
    }) => {
        const identity = {
            ...defaultMockIdentity,
            sessionID: `e2e-reload-refresh-${scenario.outcome}`,
            email: `reload-refresh-${scenario.outcome}@example.test`,
        }
        const access = authorizedProjectAccess(projectA, 'requester')
        await installMockSession(page, identity, projectA)
        let refreshRequests = 0

        await page.route('**/api/**', async (route) => {
            const request = route.request()
            const pathname = new URL(request.url()).pathname
            if (
                pathname === '/api/auth/refresh' &&
                request.method() === 'POST'
            ) {
                refreshRequests += 1
                if (scenario.outcome === 'unauthorized') {
                    await fulfillJSON(
                        route,
                        { code: 'unauthorized', message: '无有效会话' },
                        401,
                    )
                    return
                }
                if (scenario.outcome === 'invalid') {
                    await fulfillJSON(route, {
                        success: true,
                        message: '登录令牌刷新成功',
                        data: {
                            user: {
                                id: identity.id,
                                username: `e2e-${identity.id}`,
                                email: identity.email,
                                platform_role: identity.platformRole,
                                status: 'active',
                                email_verified: true,
                                otp_enabled: false,
                                last_login_at: null,
                            },
                            access_token: 'invalid-session-token',
                            expires_in: 3600,
                            token_type: 'Bearer',
                        },
                    })
                    return
                }
                await fulfillJSON(
                    route,
                    {
                        code: 'temporarily_unavailable',
                        message: '刷新服务暂时不可用',
                    },
                    503,
                )
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
        await expect(page.getByTestId('active-project-switcher')).toBeVisible()
        expect(refreshRequests).toBe(0)

        await page.evaluate(() => {
            window.location.hash = '#/login'
        })
        await page.reload()
        await expect(page).toHaveURL(/\/#\/login$/u)
        expect(refreshRequests).toBe(0)
        const refreshError = await page.evaluate(async (modulePath) => {
            const auth = await import(
                /* @vite-ignore */ modulePath
            ) as {
                bootstrapHumanSession: () => Promise<void>
            }
            try {
                await auth.bootstrapHumanSession()
                return null
            } catch (error) {
                return error instanceof Error ? error.message : String(error)
            }
        }, '/src/lib/authProvider.ts')
        await expect.poll(() => refreshRequests).toBe(1)
        expect(refreshError).not.toBeNull()
        await expect(page.getByTestId('active-project-switcher')).toHaveCount(0)

        const readableProjectKey = await page.evaluate(async (modulePath) => {
            const projectScope = await import(
                /* @vite-ignore */ modulePath
            ) as {
                activeProjectKey: () => string | undefined
            }
            return projectScope.activeProjectKey() ?? null
        }, '/src/lib/projectScope.ts')
        expect(readableProjectKey).toBeNull()

        const storedProjectKey = await page.evaluate(() => {
            const serialized = sessionStorage.getItem(
                'chronodesk.activeProject',
            )
            if (!serialized) return null
            const parsed = JSON.parse(serialized) as {
                project_key?: unknown
            }
            return typeof parsed.project_key === 'string'
                ? parsed.project_key
                : null
        })
        expect(storedProjectKey).toBe(
            scenario.shouldClearSelection ? null : projectA.key,
        )
    })
}

test('当前退出和全设备退出失败时保留已提交会话', async ({ page }) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-failed-logout-preserves-session',
    }
    const token = await installMockSession(page, identity, projectA)
    const access = authorizedProjectAccess(projectA, 'requester')
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
    let logoutRequests = 0
    let logoutAllRequests = 0
    let probeAuthorization = ''

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/auth/logout') {
            logoutRequests += 1
            await fulfillJSON(
                route,
                {
                    code: 'logout_failed',
                    message: '退出登录失败，请稍后重试',
                },
                503,
            )
            return
        }
        if (pathname === '/api/auth/logout-all') {
            logoutAllRequests += 1
            await fulfillJSON(
                route,
                {
                    code: 'logout_failed',
                    message: '无法从所有设备退出登录',
                },
                503,
            )
            return
        }
        if (pathname === '/api/e2e/logout-session-probe') {
            probeAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
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

    await page.goto('/#/')
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await page.getByTestId('account-menu').locator('button').first().click()
    await page
        .getByRole('menuitem', { name: '退出登录', exact: true })
        .click()

    await expect(page).toHaveURL(/\/#\/$/u)
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page.getByText('退出登录失败，请稍后重试')).toBeVisible()
    expect(logoutRequests).toBe(1)

    await page.getByTestId('account-menu').locator('button').first().click()
    await page.getByTestId('logout-all-sessions').click()

    await expect(page).toHaveURL(/\/#\/$/u)
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page.getByText('无法从所有设备退出登录')).toBeVisible()
    expect(logoutAllRequests).toBe(1)

    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/logout-session-probe')
    }, '/src/lib/apiClient.ts')
    expect(probeAuthorization).toBe(`Bearer ${token}`)
})

test('旧 bearer 的延迟 401 不撤销刷新后提交的新会话', async ({
    page,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-stale-401-after-refresh',
    }
    const oldToken = await installMockSession(page, identity, projectA)
    const rotatedToken = mockSessionToken(
        identity,
        Math.floor(Date.now() / 1000) + 7200,
    )
    const access = authorizedProjectAccess(projectA, 'requester')
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
    let logoutRequests = 0
    let probeAuthorization = ''
    let markStaleRequestStarted: (() => void) | undefined
    const staleRequestStarted = new Promise<void>((resolve) => {
        markStaleRequestStarted = resolve
    })
    let releaseStaleResponse: (() => void) | undefined
    const staleResponseReleased = new Promise<void>((resolve) => {
        releaseStaleResponse = resolve
    })
    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/e2e/stale-401') {
            markStaleRequestStarted?.()
            await staleResponseReleased
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    message: '旧 bearer 已失效',
                },
                401,
            )
            return
        }
        if (
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST'
        ) {
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: rotatedToken,
                    expires_in: 7200,
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
        if (pathname === '/api/e2e/stale-401-probe') {
            probeAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
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

    await page.goto('/#/')
    await expect(page.getByTestId('account-menu')).toBeVisible()

    const staleRequest = page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        try {
            await apiModule.apiFetch('/e2e/stale-401')
            return null
        } catch (error) {
            return error instanceof Error
                ? error.message
                : String(error)
        }
    }, '/src/lib/apiClient.ts')
    await staleRequestStarted

    await page.evaluate(async (modulePath) => {
        const authModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            bootstrapHumanSession: () => Promise<void>
        }
        await authModule.bootstrapHumanSession()
    }, '/src/lib/authProvider.ts')
    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/stale-401-probe')
    }, '/src/lib/apiClient.ts')
    expect(probeAuthorization).toBe(`Bearer ${rotatedToken}`)

    releaseStaleResponse?.()
    expect(await staleRequest).toContain('登录状态已失效')
    await page.waitForTimeout(250)

    expect(logoutRequests).toBe(0)
    expect(probeAuthorization).not.toBe(`Bearer ${oldToken}`)
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page).toHaveURL(/\/#\/$/u)
})

test('DataProvider 的延迟 401 不会经 checkError 清除新会话', async ({
    page,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-stale-data-provider-401',
    }
    await installMockSession(page, identity, projectA)
    const rotatedToken = mockSessionToken(
        identity,
        Math.floor(Date.now() / 1000) + 7200,
    )
    const access = authorizedProjectAccess(projectA, 'requester')
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
    let probeAuthorization = ''
    let markStaleRequestStarted: (() => void) | undefined
    const staleRequestStarted = new Promise<void>((resolve) => {
        markStaleRequestStarted = resolve
    })
    let releaseStaleResponse: (() => void) | undefined
    const staleResponseReleased = new Promise<void>((resolve) => {
        releaseStaleResponse = resolve
    })
    let holdCurrentUserResponse = false
    let markCurrentUserRequestStarted: (() => void) | undefined
    const currentUserRequestStarted = new Promise<void>((resolve) => {
        markCurrentUserRequestStarted = resolve
    })
    let releaseCurrentUserResponse: (() => void) | undefined
    const currentUserResponseReleased = new Promise<void>((resolve) => {
        releaseCurrentUserResponse = resolve
    })

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/e2e-stale-data-provider') {
            markStaleRequestStarted?.()
            await staleResponseReleased
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    message: '旧 DataProvider bearer 已失效',
                },
                401,
            )
            return
        }
        if (
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST'
        ) {
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: rotatedToken,
                    expires_in: 7200,
                    token_type: 'Bearer',
                },
            })
            return
        }
        if (pathname === '/api/e2e/stale-data-provider-probe') {
            probeAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
            return
        }
        if (pathname === '/api/auth/me') {
            if (holdCurrentUserResponse) {
                holdCurrentUserResponse = false
                markCurrentUserRequestStarted?.()
                await currentUserResponseReleased
                await fulfillJSON(
                    route,
                    {
                        code: 'unauthorized',
                        message: '旧身份查询已失效',
                    },
                    401,
                )
                return
            }
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

    await page.goto('/#/')
    await expect(page.getByTestId('account-menu')).toBeVisible()

    const staleRequest = page.evaluate(
        async ({ authModulePath, dataModulePath }) => {
            const dataModule = await import(
                /* @vite-ignore */ dataModulePath
            ) as {
                dataProvider: {
                    getList: (
                        resource: string,
                        params: {
                            filter: Record<string, unknown>
                            pagination: {
                                page: number
                                perPage: number
                            }
                            sort: {
                                field: string
                                order: 'ASC' | 'DESC'
                            }
                        },
                    ) => Promise<unknown>
                }
            }
            const authModule = await import(
                /* @vite-ignore */ authModulePath
            ) as {
                authProvider: {
                    checkError: (error: unknown) => Promise<void>
                }
            }
            try {
                await dataModule.dataProvider.getList(
                    'e2e-stale-data-provider',
                    {
                        filter: {},
                        pagination: { page: 1, perPage: 10 },
                        sort: { field: 'id', order: 'ASC' },
                    },
                )
                return {
                    checkErrorRejected: false,
                    message: null,
                }
            } catch (error) {
                let checkErrorRejected = false
                try {
                    await authModule.authProvider.checkError(error)
                } catch {
                    checkErrorRejected = true
                }
                return {
                    checkErrorRejected,
                    message:
                        error instanceof Error
                            ? error.message
                            : String(error),
                }
            }
        },
        {
            authModulePath: '/src/lib/authProvider.ts',
            dataModulePath: '/src/lib/dataProvider.ts',
        },
    )
    await staleRequestStarted

    await page.evaluate(async (modulePath) => {
        const authModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            bootstrapHumanSession: () => Promise<void>
        }
        await authModule.bootstrapHumanSession()
    }, '/src/lib/authProvider.ts')

    releaseStaleResponse?.()
    const result = await staleRequest
    expect(result.message).toContain('登录状态已失效')
    expect(result.checkErrorRejected).toBe(false)

    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/stale-data-provider-probe')
    }, '/src/lib/apiClient.ts')

    expect(probeAuthorization).toBe(`Bearer ${rotatedToken}`)
    await expect(page.getByTestId('account-menu')).toBeVisible()
    await expect(page).toHaveURL(/\/#\/$/u)

    holdCurrentUserResponse = true
    const staleIdentityRequest = page.evaluate(
        async (modulePath) => {
            const authModule = await import(
                /* @vite-ignore */ modulePath
            ) as {
                authProvider: {
                    getIdentity: () => Promise<{
                        id: string | number
                        email?: string
                    }>
                }
            }
            const originalGetItem = Storage.prototype.getItem
            let hideStoredUserOnce = true
            Storage.prototype.getItem = function getItem(key: string) {
                if (
                    this === sessionStorage &&
                    key === 'user' &&
                    hideStoredUserOnce
                ) {
                    hideStoredUserOnce = false
                    return null
                }
                return originalGetItem.call(this, key)
            }
            try {
                return await authModule.authProvider.getIdentity()
            } finally {
                Storage.prototype.getItem = originalGetItem
            }
        },
        '/src/lib/authProvider.ts',
    )
    await currentUserRequestStarted
    await page.evaluate(async (modulePath) => {
        const authModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            bootstrapHumanSession: () => Promise<void>
        }
        await authModule.bootstrapHumanSession()
    }, '/src/lib/authProvider.ts')
    releaseCurrentUserResponse?.()

    const currentIdentity = await staleIdentityRequest
    expect(currentIdentity.id).toBe(identity.id)
    expect(currentIdentity.email).toBe(identity.email)
    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/stale-data-provider-probe')
    }, '/src/lib/apiClient.ts')
    expect(probeAuthorization).toBe(`Bearer ${rotatedToken}`)
    await expect(page.getByTestId('account-menu')).toBeVisible()
})

test('延迟的全设备退出不会清除操作后新建的同账号会话', async ({
    context,
    page: protectedPage,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-new-session-after-logout-all',
    }
    const token = await installMockSession(
        protectedPage,
        identity,
        projectA,
    )
    const access = authorizedProjectAccess(projectA, 'requester')
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
    let probeAuthorization = ''

    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/e2e/delayed-all-devices-probe') {
            probeAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
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

    await protectedPage.goto('/#/')
    await expect(
        protectedPage.getByTestId('account-menu'),
    ).toBeVisible()

    const senderPage = await context.newPage()
    await senderPage.goto('/#/login')
    await senderPage.evaluate((subject) => {
        const channel = new BroadcastChannel(
            'chronodesk:human-session:v2',
        )
        channel.postMessage({
            type: 'signed_out',
            scope: 'all_devices',
            subject,
            issued_at: Date.now() - 60_000,
        })
        channel.close()
    }, String(identity.id))
    await protectedPage.waitForTimeout(250)

    await protectedPage.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/delayed-all-devices-probe')
    }, '/src/lib/apiClient.ts')

    expect(probeAuthorization).toBe(`Bearer ${token}`)
    await expect(
        protectedPage.getByTestId('account-menu'),
    ).toBeVisible()
    await expect(protectedPage).toHaveURL(/\/#\/$/u)
    await senderPage.close()
})

test('refresh 瞬态失败保留会话并允许重试，确定性失效才清理', async ({
    page,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-transient-refresh-preserves-session',
    }
    const oldToken = await installMockSession(page, identity, projectA)
    const rotatedToken = mockSessionToken(
        identity,
        Math.floor(Date.now() / 1000) + 7200,
    )
    const access = authorizedProjectAccess(projectA, 'requester')
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
    const probeAuthorizations: string[] = []
    let refreshRequests = 0

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/auth/refresh') {
            refreshRequests += 1
            if (refreshRequests === 1) {
                await route.abort('failed')
                return
            }
            if (refreshRequests === 2) {
                await fulfillJSON(
                    route,
                    {
                        code: 'rate_limited',
                        message: '刷新请求过于频繁，请稍后重试',
                    },
                    429,
                )
                return
            }
            if (refreshRequests === 3) {
                await fulfillJSON(
                    route,
                    {
                        code: 'refresh_failed',
                        message: '刷新服务暂时不可用',
                    },
                    503,
                )
                return
            }
            if (refreshRequests === 5) {
                await fulfillJSON(
                    route,
                    {
                        code: 'invalid_token',
                        message: '登录会话已失效',
                    },
                    401,
                )
                return
            }
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: rotatedToken,
                    expires_in: 7200,
                    token_type: 'Bearer',
                },
            })
            return
        }
        if (pathname === '/api/e2e/refresh-preserved-probe') {
            probeAuthorizations.push(
                request.headers().authorization ?? '',
            )
            await fulfillJSON(route, { code: 0, data: [] })
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

    await page.goto('/#/')
    await expect(page.getByTestId('account-menu')).toBeVisible()

    for (let attempt = 1; attempt <= 3; attempt += 1) {
        const failure = await page.evaluate(async (modulePath) => {
            const authModule = await import(
                /* @vite-ignore */ modulePath
            ) as {
                bootstrapHumanSession: () => Promise<void>
            }
            try {
                await authModule.bootstrapHumanSession()
                return null
            } catch (error) {
                return error instanceof Error
                    ? error.message
                    : String(error)
            }
        }, '/src/lib/authProvider.ts')
        expect(failure).not.toBeNull()
        await page.evaluate(async (modulePath) => {
            const apiModule = await import(
                /* @vite-ignore */ modulePath
            ) as {
                apiFetch: (path: string) => Promise<unknown>
            }
            await apiModule.apiFetch('/e2e/refresh-preserved-probe')
        }, '/src/lib/apiClient.ts')
        expect(probeAuthorizations).toEqual(
            Array.from(
                { length: attempt },
                () => `Bearer ${oldToken}`,
            ),
        )
    }

    await page.evaluate(async (modulePath) => {
        const authModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            bootstrapHumanSession: () => Promise<void>
        }
        await authModule.bootstrapHumanSession()
    }, '/src/lib/authProvider.ts')
    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/refresh-preserved-probe')
    }, '/src/lib/apiClient.ts')
    expect(probeAuthorizations.at(-1)).toBe(`Bearer ${rotatedToken}`)

    const deterministicFailure = await page.evaluate(
        async (modulePath) => {
            const authModule = await import(
                /* @vite-ignore */ modulePath
            ) as {
                bootstrapHumanSession: () => Promise<void>
            }
            try {
                await authModule.bootstrapHumanSession()
                return null
            } catch (error) {
                return error instanceof Error
                    ? error.message
                    : String(error)
            }
        },
        '/src/lib/authProvider.ts',
    )
    expect(deterministicFailure).toContain('登录会话已失效')
    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/refresh-preserved-probe')
    }, '/src/lib/apiClient.ts')
    expect(probeAuthorizations).toEqual([
        `Bearer ${oldToken}`,
        `Bearer ${oldToken}`,
        `Bearer ${oldToken}`,
        `Bearer ${rotatedToken}`,
        '',
    ])
    expect(refreshRequests).toBe(5)
})

test('旧标签退出响应提交后新标签才允许登录', async ({
    context,
    page: logoutPage,
}) => {
    const identityA = {
        ...defaultMockIdentity,
        sessionID: 'e2e-lifecycle-lock-session-a',
        email: 'lifecycle-lock-a@example.test',
    }
    const identityB = {
        ...defaultMockIdentity,
        sessionID: 'e2e-lifecycle-lock-session-b',
        email: 'lifecycle-lock-b@example.test',
    }
    await installMockSession(logoutPage, identityA, projectA)
    const tokenB = mockSessionToken(identityB)
    const accessA = authorizedProjectAccess(projectA, 'requester')
    const accessB = authorizedProjectAccess(projectB, 'observer')
    const events: string[] = []
    let logoutSessionID = ''
    let loginRequests = 0
    let replacementProbeAuthorization = ''
    let markLogoutStarted: (() => void) | undefined
    const logoutStarted = new Promise<void>((resolve) => {
        markLogoutStarted = resolve
    })
    let releaseLogout: (() => void) | undefined
    const logoutReleased = new Promise<void>((resolve) => {
        releaseLogout = resolve
    })

    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        const authorization = request.headers().authorization ?? ''
        const usingB = authorization === `Bearer ${tokenB}`
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
        if (pathname === '/api/auth/logout') {
            events.push('logout:start')
            logoutSessionID =
                request.headers()['x-chronodesk-session-id'] ?? ''
            markLogoutStarted?.()
            await logoutReleased
            events.push('logout:end')
            await fulfillJSON(route, {
                success: true,
                message: '退出登录成功',
            })
            return
        }
        if (pathname === '/api/auth/login') {
            loginRequests += 1
            events.push('login:start')
            await fulfillJSON(route, {
                code: 0,
                msg: '登录成功',
                data: {
                    user: {
                        id: identityB.id,
                        username: `e2e-${identityB.id}`,
                        email: identityB.email,
                        platform_role: identityB.platformRole,
                        status: 'active',
                        email_verified: true,
                        otp_enabled: false,
                        last_login_at: null,
                    },
                    access_token: tokenB,
                    expires_in: 3600,
                    token_type: 'Bearer',
                },
            })
            return
        }
        if (pathname === '/api/e2e/delayed-sign-out-probe') {
            replacementProbeAuthorization = authorization
            await fulfillJSON(route, { code: 0, data: [] })
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

    await logoutPage.goto('/#/')
    await expect(logoutPage.getByTestId('account-menu')).toBeVisible()
    const loginPage = await context.newPage()
    await loginPage.goto('/#/login')

    await logoutPage
        .getByTestId('account-menu')
        .locator('button')
        .first()
        .click()
    const logoutAction = logoutPage
        .getByRole('menuitem', { name: '退出登录', exact: true })
        .click()
    await logoutStarted

    await loginPage.getByLabel('邮箱').fill(identityB.email)
    await loginPage.getByLabel('密码').fill('CorrectPassword123!')
    const loginAction = loginPage
        .getByRole('button', { name: '登录系统', exact: true })
        .click()
    await loginPage.waitForTimeout(150)
    expect(loginRequests).toBe(0)

    releaseLogout?.()
    await logoutAction
    await loginAction
    await expect(loginPage).toHaveURL(/\/#\/$/u)
    await expect(loginPage.getByTestId('account-menu')).toContainText(
        `e2e-${identityB.id}`,
    )
    expect(logoutSessionID).toBe(identityA.sessionID)
    expect(events.slice(0, 3)).toEqual([
        'logout:start',
        'logout:end',
        'login:start',
    ])

    await logoutPage.evaluate((staleSession) => {
        const channel = new BroadcastChannel(
            'chronodesk:human-session:v2',
        )
        channel.postMessage({
            type: 'signed_out',
            scope: 'current_session',
            subject: staleSession.subject,
            session_id: staleSession.sessionID,
            issued_at: Date.now() - 1_000,
        })
        channel.close()
    }, {
        subject: String(identityA.id),
        sessionID: identityA.sessionID,
    })
    await loginPage.evaluate(
        () =>
            new Promise<void>((resolve) => {
                requestAnimationFrame(() =>
                    requestAnimationFrame(() => resolve()),
                )
            }),
    )
    await expect(loginPage).toHaveURL(/\/#\/$/u)
    await expect(loginPage.getByTestId('account-menu')).toContainText(
        `e2e-${identityB.id}`,
    )
    await loginPage.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/delayed-sign-out-probe')
    }, '/src/lib/apiClient.ts')
    expect(replacementProbeAuthorization).toBe(`Bearer ${tokenB}`)
})

test('三个最小标签页 bootstrap refresh 通过生产生命周期锁全局串行', async ({
    context,
    page: firstPage,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-three-tab-refresh-lock',
    }
    const access = authorizedProjectAccess(projectA, 'requester')
    const pages = [
        firstPage,
        await context.newPage(),
        await context.newPage(),
    ]
    for (const page of pages) {
        await installMockSession(page, identity, projectA)
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
    const events: string[] = []
    let refreshRequests = 0
    let inFlight = 0
    let maximumInFlight = 0

    const lifecycleShellPath =
        '/__e2e__/human-session-lifecycle-lock.html'
    await context.route(`**${lifecycleShellPath}`, async (route) => {
        await route.fulfill({
            status: 200,
            contentType: 'text/html',
            body:
                '<!doctype html><html><head><meta charset="utf-8">' +
                '<title>Human session lifecycle lock probe</title>' +
                '</head><body></body></html>',
        })
    })
    await context.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/auth/refresh') {
            refreshRequests += 1
            const sequence = refreshRequests
            events.push(`refresh:${sequence}:start`)
            inFlight += 1
            maximumInFlight = Math.max(maximumInFlight, inFlight)
            await new Promise((resolve) => setTimeout(resolve, 100))
            inFlight -= 1
            events.push(`refresh:${sequence}:end`)
            const expiresAt =
                Math.floor(Date.now() / 1000) + 7200 + sequence
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user,
                    access_token: mockSessionToken(
                        identity,
                        expiresAt,
                    ),
                    expires_in: 7200 + sequence,
                    token_type: 'Bearer',
                },
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

    for (const page of pages) {
        await page.goto(lifecycleShellPath)
    }
    await Promise.all(
        pages.map((page) =>
            page.evaluate(async (modulePath) => {
                const authModule = await import(
                    /* @vite-ignore */ modulePath
                ) as {
                    bootstrapHumanSession: () => Promise<void>
                }
                await authModule.bootstrapHumanSession()
            }, '/src/lib/authProvider.ts'),
        ),
    )

    expect(refreshRequests).toBe(3)
    expect(maximumInFlight).toBe(1)
    expect(events).toEqual([
        'refresh:1:start',
        'refresh:1:end',
        'refresh:2:start',
        'refresh:2:end',
        'refresh:3:start',
        'refresh:3:end',
    ])
})

for (const replacement of [
    {
        name: 'subject 变化',
        identity: {
            ...defaultMockIdentity,
            id: defaultMockIdentity.id + 1,
            sessionID: 'e2e-replacement-initial-session',
            email: 'different-subject@example.test',
        },
    },
    {
        name: 'session ID 变化',
        identity: {
            ...defaultMockIdentity,
            sessionID: 'e2e-different-session-id',
        },
    },
]) {
    test(`受保护标签在${replacement.name}时仍按账号或会话替换重载`, async ({
        context,
        page: protectedPage,
    }) => {
        const initialIdentity = {
            ...defaultMockIdentity,
            sessionID: 'e2e-replacement-initial-session',
        }
        await installMockSession(
            protectedPage,
            initialIdentity,
            projectA,
        )
        const replacementToken = mockSessionToken(replacement.identity)
        const access = authorizedProjectAccess(projectA, 'requester')
        let replacementLoggedIn = false
        await protectedPage.addInitScript(() => {
            const key = 'chronodesk.e2e.replacement-load-count'
            const next = Number(sessionStorage.getItem(key) ?? '0') + 1
            sessionStorage.setItem(key, String(next))
        })
        await context.route('**/api/**', async (route) => {
            const authorization =
                route.request().headers().authorization ?? ''
            const usingReplacement =
                authorization === `Bearer ${replacementToken}`
            const identity = usingReplacement
                ? replacement.identity
                : initialIdentity
            const pathname = new URL(route.request().url()).pathname
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
                route.request().method() === 'POST'
            ) {
                replacementLoggedIn = true
                await fulfillJSON(route, {
                    code: 0,
                    msg: '登录成功',
                    data: {
                        user: {
                            id: replacement.identity.id,
                            username: `e2e-${replacement.identity.id}`,
                            email: replacement.identity.email,
                            platform_role:
                                replacement.identity.platformRole,
                            status: 'active',
                            email_verified: true,
                            otp_enabled: false,
                            last_login_at: null,
                        },
                        access_token: replacementToken,
                        expires_in: 3600,
                        token_type: 'Bearer',
                    },
                })
                return
            }
            if (
                pathname === '/api/auth/refresh' &&
                route.request().method() === 'POST' &&
                replacementLoggedIn
            ) {
                await fulfillJSON(route, {
                    success: true,
                    message: '登录令牌刷新成功',
                    data: {
                        user: {
                            id: replacement.identity.id,
                            username: `e2e-${replacement.identity.id}`,
                            email: replacement.identity.email,
                            platform_role:
                                replacement.identity.platformRole,
                            status: 'active',
                            email_verified: true,
                            otp_enabled: false,
                            last_login_at: null,
                        },
                        access_token: replacementToken,
                        expires_in: 3600,
                        token_type: 'Bearer',
                    },
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

        await protectedPage.goto('/#/')
        await expect(protectedPage.getByTestId('account-menu')).toBeVisible()
        expect(
            await protectedPage.evaluate(() =>
                sessionStorage.getItem(
                    'chronodesk.e2e.replacement-load-count',
                ),
            ),
        ).toBe('1')
        await expect
            .poll(() =>
                protectedPage.evaluate(() => localStorage.getItem('token')),
            )
            .toBeNull()

        const committingPage = await context.newPage()
        await committingPage.goto('/#/login')
        await committingPage
            .getByLabel('邮箱')
            .fill(replacement.identity.email)
        await committingPage
            .getByLabel('密码')
            .fill('CorrectPassword123!')
        const protectedReload = protectedPage.waitForEvent('load')
        await committingPage
            .getByRole('button', {
                name: '登录系统',
                exact: true,
            })
            .click()
        await expect(committingPage).toHaveURL(/\/#\/$/u)
        await protectedReload

        expect(
            await protectedPage.evaluate(() =>
                Number(
                    sessionStorage.getItem(
                        'chronodesk.e2e.replacement-load-count',
                    ) ?? '0',
                ),
            ),
        ).toBeGreaterThan(1)
        await expect
            .poll(() =>
                protectedPage.evaluate(() => localStorage.getItem('token')),
            )
            .toBeNull()
    })
}

test('页面加载后的持久化 bearer 注入被清除且不会替换内存会话', async ({
    page,
}) => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: 'e2e-late-persisted-bearer',
    }
    const token = await installMockSession(page, identity, projectA)
    const access = authorizedProjectAccess(projectA, 'requester')
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
    let capturedAuthorization = ''

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (pathname === '/api/e2e/late-persisted-bearer') {
            capturedAuthorization =
                request.headers().authorization ?? ''
            await fulfillJSON(route, { code: 0, data: [] })
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

    await page.goto('/#/account/profile')
    await expect(page.getByTestId('account-profile-page')).toBeVisible()
    await page.evaluate(() => {
        localStorage.setItem('token', 'late-malformed-bearer')
        localStorage.setItem('refreshToken', 'late-refresh-bearer')
    })
    await page.evaluate(async (modulePath) => {
        const apiModule = await import(
            /* @vite-ignore */ modulePath
        ) as {
            apiFetch: (path: string) => Promise<unknown>
        }
        await apiModule.apiFetch('/e2e/late-persisted-bearer')
    }, '/src/lib/apiClient.ts')

    expect(capturedAuthorization).toBe(`Bearer ${token}`)
    expect(await readAuthenticationStorage(page)).toMatchObject({
        token: null,
        refreshToken: null,
    })
    await expect(page).toHaveURL(/\/#\/account\/profile$/u)
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
    let identityBSessionActive = false
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
            identityBSessionActive = true
            await fulfillJSON(route, {
                code: 0,
                msg: '登录成功',
                data: {
                    user: {
                        ...user,
                        id: identityB.id,
                        username: `e2e-${identityB.id}`,
                        email: identityB.email,
                    },
                    access_token: tokenB,
                    expires_in: 3600,
                    token_type: 'Bearer',
                },
            })
            return
        }
        if (
            pathname === '/api/auth/refresh' &&
            request.method() === 'POST' &&
            identityBSessionActive
        ) {
            await fulfillJSON(route, {
                success: true,
                message: '登录令牌刷新成功',
                data: {
                    user: {
                        id: identityB.id,
                        username: `e2e-${identityB.id}`,
                        email: identityB.email,
                        platform_role: identityB.platformRole,
                        status: 'active',
                        email_verified: true,
                        otp_enabled: false,
                        last_login_at: null,
                    },
                    access_token: tokenB,
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
        .toBeNull()
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
        .toBeNull()
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
        .toBeNull()
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
    let refreshRequests = 0
    let logoutRequests = 0
    await page.route('**/api/auth/**', async (route) => {
        const pathname = new URL(route.request().url()).pathname
        if (pathname === '/api/auth/refresh') {
            refreshRequests += 1
            await fulfillJSON(
                route,
                { code: 'unauthorized', message: '无有效会话' },
                401,
            )
            return
        }
        if (pathname === '/api/auth/logout') {
            logoutRequests += 1
            await fulfillJSON(route, {
                success: true,
                message: '已退出当前会话',
            })
            return
        }
        await fulfillJSON(route, { code: 0, data: [] })
    })

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
    expect(refreshRequests).toBe(1)
    expect(logoutRequests).toBe(0)
})
