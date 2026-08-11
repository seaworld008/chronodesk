import { expect, test, type Page } from '@playwright/test'

type LoginRequestBody = Record<string, unknown>

type LoginApiCapture = {
    loginRequests: LoginRequestBody[]
    unexpectedApiRequests: string[]
}

const installLoginApiMock = async (
    page: Page,
): Promise<LoginApiCapture> => {
    const loginRequests: LoginRequestBody[] = []
    const unexpectedApiRequests: string[] = []

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const pathname = new URL(request.url()).pathname
        if (
            request.method() === 'POST' &&
            pathname === '/api/auth/login'
        ) {
            loginRequests.push(
                request.postDataJSON() as LoginRequestBody,
            )
            await route.fulfill({
                status: 401,
                contentType: 'application/json',
                body: JSON.stringify({
                    code: 1,
                    msg: '测试登录请求已拦截',
                }),
            })
            return
        }

        unexpectedApiRequests.push(`${request.method()} ${pathname}`)
        await route.fulfill({
            status: 500,
            contentType: 'application/json',
            body: JSON.stringify({
                code: 1,
                msg: '测试拒绝未登记的 API 请求',
            }),
        })
    })

    return { loginRequests, unexpectedApiRequests }
}

const openLoginPage = async (page: Page): Promise<void> => {
    await page.goto('/#/login')
    await expect(
        page.getByRole('button', {
            name: '登录系统',
            exact: true,
        }),
    ).toBeVisible({ timeout: 15_000 })
}

test('空邮箱和空密码显示中文字段错误且不发登录请求', async ({
    page,
}) => {
    const api = await installLoginApiMock(page)
    await openLoginPage(page)

    await page
        .getByRole('button', { name: '登录系统', exact: true })
        .click()

    await expect(page.getByText('请输入邮箱', { exact: true })).toBeVisible()
    await expect(page.getByText('请输入密码', { exact: true })).toBeVisible()
    expect(api.loginRequests).toEqual([])
    expect(api.unexpectedApiRequests).toEqual([])
})

test('非法邮箱格式显示中文字段错误且不发登录请求', async ({
    page,
}) => {
    const api = await installLoginApiMock(page)
    await openLoginPage(page)

    await page.getByLabel('邮箱').fill('not-an-email')
    await page.getByLabel('密码').fill('ExamplePassword123!')
    await page
        .getByRole('button', { name: '登录系统', exact: true })
        .click()

    await expect(
        page.getByText('请输入有效的邮箱地址', { exact: true }),
    ).toBeVisible()
    expect(api.loginRequests).toEqual([])
    expect(api.unexpectedApiRequests).toEqual([])
})

test('缺少密码显示中文字段错误且不发登录请求', async ({
    page,
}) => {
    const api = await installLoginApiMock(page)
    await openLoginPage(page)

    await page.getByLabel('邮箱').fill('valid@example.test')
    await page
        .getByRole('button', { name: '登录系统', exact: true })
        .click()

    await expect(page.getByText('请输入密码', { exact: true })).toBeVisible()
    expect(api.loginRequests).toEqual([])
    expect(api.unexpectedApiRequests).toEqual([])
})

test('登录请求使用去除首尾空白的邮箱且不改写密码', async ({
    page,
}) => {
    const api = await installLoginApiMock(page)
    await openLoginPage(page)
    const password = ' ExamplePassword123! '
    const paddedEmail = '\u00a0valid@example.test\u00a0'

    await page.getByLabel('邮箱').fill(paddedEmail)
    await page.getByLabel('密码').fill(password)
    await page
        .getByRole('button', { name: '登录系统', exact: true })
        .click()

    await expect
        .poll(() => api.loginRequests.length, {
            message: '合法表单应只发出一次登录请求',
        })
        .toBe(1)
    expect(api.loginRequests).toEqual([
        {
            email: 'valid@example.test',
            password,
        },
    ])
    expect(api.unexpectedApiRequests).toEqual([])
})
