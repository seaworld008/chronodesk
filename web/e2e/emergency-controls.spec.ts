import { expect, test } from '@playwright/test'

import { monitorBrowserHealth } from './helpers/browserAudit'
import {
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    type MockSessionIdentity,
} from './helpers/mockHumanSession'

const timestamp = '2026-08-01T10:00:00Z'

const sessionUser = (identity: MockSessionIdentity) => ({
    id: identity.id,
    username: `emergency-${identity.id}`,
    email: identity.email,
    platform_role: identity.platformRole,
    status: 'active',
    email_verified: true,
    otp_enabled: false,
})

test('紧急运维员从平台工作台独立更新一个安全控制', async ({ page }) => {
    const identity: MockSessionIdentity = {
        ...defaultMockIdentity,
        sessionID: 'emergency-control-operator',
        platformRole: 'emergency_operator',
    }
    await installMockSession(page, identity)
    const browserHealth = monitorBrowserHealth(page)
    const writes: Array<{
        body: unknown
        ifMatch: string | undefined
    }> = []
    let snapshot = {
        global_read_only: false,
        emergency_stop: false,
        version: 4,
        updated_at: timestamp,
    }

    await page.route('**/api/**', async (route) => {
        const request = route.request()
        const url = new URL(request.url())
        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, msg: 'ok', data: [] })
            return
        }
        if (url.pathname === '/api/auth/me') {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: sessionUser(identity),
            })
            return
        }
        if (url.pathname === '/api/platform/emergency-controls') {
            if (request.method() === 'GET') {
                await fulfillJSON(
                    route,
                    { code: 0, msg: 'ok', data: snapshot },
                    200,
                    { ETag: `"v${snapshot.version}"` },
                )
                return
            }
            writes.push({
                body: request.postDataJSON(),
                ifMatch: request.headers()['if-match'],
            })
            snapshot = {
                ...snapshot,
                ...(request.postDataJSON() as Record<string, boolean>),
                version: snapshot.version + 1,
                updated_at: '2026-08-01T10:01:00Z',
            }
            await fulfillJSON(
                route,
                { code: 0, msg: 'ok', data: snapshot },
                200,
                { ETag: `"v${snapshot.version}"` },
            )
            return
        }
        await fulfillJSON(route, { code: 'not_found', msg: 'unexpected' }, 404)
    })

    await page.goto('/#/')
    await expect(page).toHaveURL(/#\/platform\/home$/u)
    await expect(
        page.getByTestId('platform-home').getByRole(
            'heading',
            { name: '平台工作台', exact: true },
        ),
    ).toBeVisible()
    await expect(
        page.getByRole('button', { name: '平台公共配置', exact: true }),
    ).toHaveCount(0)
    await expect(
        page.getByRole('button', { name: '安全与应急', exact: true }),
    ).toBeVisible()

    await page.getByRole('button', {
        name: '安全与应急',
        exact: true,
    }).click()
    await expect(page).toHaveURL(/#\/platform\/emergency-controls$/u)
    await expect(
        page.getByRole('heading', { name: '安全与应急', level: 1 }),
    ).toBeVisible()
    const readOnly = page.getByRole('switch', {
        name: '智能体全局只读模式',
        exact: true,
    })
    const emergency = page.getByRole('switch', {
        name: '智能体全局紧急停止',
        exact: true,
    })
    await expect(readOnly).not.toBeChecked()
    await expect(emergency).not.toBeChecked()
    await readOnly.check()
    await expect(page.getByText('有 1 项未保存修改')).toBeVisible()
    await page.getByRole('button', {
        name: '保存安全控制',
        exact: true,
    }).click()
    const confirmation = page.getByRole('dialog', {
        name: '确认更新平台安全控制',
        exact: true,
    })
    await expect(confirmation).toContainText('本次将修改 1 项平台级控制')
    await confirmation.getByRole('button', {
        name: '确认并写入审计',
        exact: true,
    }).click()

    await expect(page.getByText('平台安全控制已更新并写入审计'))
        .toBeVisible()
    await expect(page.getByText('资源版本 v5')).toBeVisible()
    expect(writes).toEqual([{
        body: { global_read_only: true },
        ifMatch: '"v4"',
    }])
    await expect(emergency).not.toBeChecked()
    browserHealth.assertClean()
})

for (const role of [
    'platform_admin',
    'security_auditor',
    'member',
] as const) {
    test(`${role} 无法显示或直接打开安全与应急`, async ({ page }) => {
        const identity: MockSessionIdentity = {
            ...defaultMockIdentity,
            id: defaultMockIdentity.id + role.length,
            email: `${role}@example.test`,
            sessionID: `emergency-control-denied-${role}`,
            platformRole: role,
        }
        await installMockSession(page, identity)
        let emergencyRequests = 0
        await page.route('**/api/**', async (route) => {
            const url = new URL(route.request().url())
            if (url.pathname === '/api/platform/emergency-controls') {
                emergencyRequests++
                await fulfillJSON(
                    route,
                    { code: 'insufficient_permissions', msg: '权限不足' },
                    403,
                )
                return
            }
            if (url.pathname === '/api/projects') {
                await fulfillJSON(route, { code: 0, msg: 'ok', data: [] })
                return
            }
            if (url.pathname === '/api/auth/me') {
                await fulfillJSON(route, {
                    code: 0,
                    msg: 'ok',
                    data: sessionUser(identity),
                })
                return
            }
            await fulfillJSON(route, { code: 0, msg: 'ok', data: [] })
        })

        await page.goto('/#/platform/emergency-controls')
        await expect(
            page.getByRole('heading', { name: '安全与应急', level: 1 }),
        ).toHaveCount(0)
        await expect(
            page.getByRole('menuitem', { name: '安全与应急', exact: true }),
        ).toHaveCount(0)
        expect(emergencyRequests).toBe(0)
    })
}
