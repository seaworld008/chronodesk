import assert from 'node:assert/strict'
import test, { after } from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
    appType: 'custom',
    server: { middlewareMode: true },
})
const scopeModule = await vite.ssrLoadModule('/src/layout/routePageScope.ts')

after(async () => {
    await vite.close()
})

test('页面作用域由最具体的 route node 决定', () => {
    assert.deepEqual(scopeModule.resolveRoutePageScope('/workbench'), {
        kind: 'global',
        navigationNodeID: 'cross-project-workbench',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/webhook-settings'), {
        kind: 'project',
        navigationNodeID: 'webhook',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/system-settings'), {
        kind: 'platform',
        navigationNodeID: 'platform-config',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/system-settings/email'), {
        kind: 'platform',
        navigationNodeID: 'platform-email-settings',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/account/profile'), {
        kind: 'account',
        navigationNodeID: 'account-profile',
    })
})

test('根路由、旧路由和未知路由均有稳定作用域', () => {
    assert.deepEqual(scopeModule.resolveRoutePageScope('/'), {
        kind: 'project',
        navigationNodeID: 'project-overview',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/email-settings'), {
        kind: 'platform',
        navigationNodeID: 'platform-email-settings',
    })
    assert.deepEqual(scopeModule.resolveRoutePageScope('/not-registered'), {
        kind: 'global',
        navigationNodeID: null,
    })
})
