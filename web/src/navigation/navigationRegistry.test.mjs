import assert from 'node:assert/strict'
import test, { after } from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
    appType: 'custom',
    server: { middlewareMode: true },
})
const registryModule = await vite.ssrLoadModule(
    '/src/navigation/navigationRegistry.ts',
)
const stateModule = await vite.ssrLoadModule(
    '/src/navigation/navigationTreeState.ts',
)

after(async () => {
    await vite.close()
})

const platformProjectAdmin = {
    platformRole: 'platform_admin',
    projectRole: 'project_admin',
    hasProject: true,
}

test('连续功能树按产品顺序输出且不保留视觉分区节点', () => {
    const nodes = registryModule.visibleNavigationNodes(
        'sidebar',
        platformProjectAdmin,
    )
    assert.deepEqual(nodes.map((node) => node.label), [
        '工作台',
        '项目运营',
        '智能运营',
        '集成中心',
        '项目配置',
        '治理中心',
        '系统设置',
    ])
    assert.ok(nodes.every((node) =>
        node.label !== '我的工作' && node.label !== '平台管理',
    ))
    assert.equal(nodes[0].kind, 'group')
    assert.deepEqual(
        nodes[0].children.map((item) => item.path),
        ['/workbench'],
    )
})

test('无当前项目隐藏全部项目树但保留工作台和精确治理入口', () => {
    const nodes = registryModule.visibleNavigationNodes('sidebar', {
        platformRole: 'platform_admin',
        projectRole: null,
        hasProject: false,
    })
    assert.deepEqual(
        nodes.map((node) => node.label),
        ['工作台', '治理中心', '系统设置'],
    )
    assert.ok(nodes.every((node) => node.scope !== 'project'))
})

test('平台职责不会推导项目 scope', () => {
    const items = registryModule.visibleNavigationItems('sidebar', {
        platformRole: 'platform_admin',
        projectRole: null,
        hasProject: false,
    })
    assert.ok(items.some((item) => item.id === 'users'))
    assert.ok(!items.some((item) => item.id === 'tickets'))
    assert.ok(!items.some((item) => item.id === 'agents'))
})

test('registry validator 拒绝重复 ID/path、非法 scope、非法 children 和循环', () => {
    const baseLeaf = {
        kind: 'leaf',
        id: 'same',
        label: '叶子',
        icon: 'home',
        order: 1,
        scope: 'global',
        capability: null,
        roles: null,
        placement: 'sidebar',
        path: '/same',
        activePathPrefixes: ['/same'],
        route: { kind: 'existing' },
    }
    const errors = registryModule.validateNavigationRegistry([
        baseLeaf,
        { ...baseLeaf },
        { ...baseLeaf, id: 'bad-scope', path: '/bad', scope: 'invalid' },
        { ...baseLeaf, id: 'bad-child', path: '/child', children: [] },
        {
            kind: 'group',
            id: 'empty',
            label: '空组',
            icon: 'home',
            order: 2,
            scope: 'global',
            capability: null,
            roles: null,
            placement: 'sidebar',
            path: null,
            children: [],
        },
    ])
    assert.ok(errors.some((error) => error.includes('id 重复')))
    assert.ok(errors.some((error) => error.includes('path 重复')))
    assert.ok(errors.some((error) => error.includes('scope 非法')))
    assert.ok(errors.some((error) => error.includes('不能包含 children')))
    assert.ok(errors.some((error) => error.includes('至少包含一个 leaf')))

    const cyclic = {
        kind: 'group',
        id: 'cycle',
        label: '循环',
        icon: 'home',
        order: 1,
        scope: 'global',
        capability: null,
        roles: null,
        placement: 'sidebar',
        path: null,
        children: [],
    }
    cyclic.children.push(cyclic)
    assert.ok(
        registryModule.validateNavigationRegistry([cyclic])
            .some((error) => error.includes('循环引用')),
    )
})

test('validator 检查 capability/role/icon/order/active path 与 parent taxonomy', () => {
    const badLeaf = {
        kind: 'leaf',
        id: 'bad-taxonomy',
        label: '非法',
        icon: 'unknown-icon',
        order: 0,
        scope: 'platform',
        capability: { kind: 'platform', value: 'unknown-capability' },
        roles: { kind: 'platform', values: ['unknown-role'] },
        placement: 'account',
        path: '/bad-taxonomy',
        activePathPrefixes: ['bad-prefix'],
        route: { kind: 'custom', component: 'unknown-component' },
    }
    const errors = registryModule.validateNavigationRegistry([{
        kind: 'group',
        id: 'parent',
        label: '父级',
        icon: 'home',
        order: 1,
        scope: 'global',
        capability: null,
        roles: null,
        placement: 'sidebar',
        path: null,
        children: [badLeaf],
    }])
    for (const expected of [
        'icon 非法',
        'order 非法',
        'capability value 非法',
        '非法 role',
        'scope/placement 不一致',
        '非法 active path',
        'component mapping 非法',
    ]) {
        assert.ok(
            errors.some((error) => error.includes(expected)),
            `${expected}: ${errors.join('；')}`,
        )
    }
})

test('custom route contract 完整承载 path、权限、角色和兼容重定向', () => {
    const custom = registryModule.navigationRegistry
        .flatMap((node) => node.kind === 'leaf' ? [node] : node.children)
        .filter((node) => node.route.kind === 'custom')
    assert.deepEqual(
        custom.map((node) => node.route.component).sort(),
        [
            'accountProfile',
            'accountSecurity',
            'agentControl',
            'automationIndex',
            'integrationRuntime',
            'loginHistory',
            'platformAudit',
            'platformConfig',
            'platformEmail',
            'platformProjects',
            'projectMemberships',
            'trustedDevices',
            'webhookSettings',
            'workbench',
        ].sort(),
    )
    const email = custom.find((node) => node.route.component === 'platformEmail')
    assert.deepEqual(email.route.legacyPaths, ['/email-settings'])
    assert.equal(email.scope, 'platform')
    assert.deepEqual(email.capability, {
        kind: 'platform',
        value: 'manage_email_settings',
    })
})

test('React Admin Resource 入口与 registry scope/capability 契约一致', () => {
    const existing = registryModule.navigationRegistry
        .flatMap((node) => node.kind === 'leaf' ? [node] : node.children)
        .filter((node) => node.route.kind === 'existing')
    const contracts = Object.fromEntries(existing.map((node) => [
        node.id,
        {
            path: node.path,
            scope: node.scope,
            capability: node.capability,
            roles: node.roles,
        },
    ]))
    assert.deepEqual(contracts.tickets, {
        path: '/tickets',
        scope: 'project',
        capability: null,
        roles: null,
    })
    assert.deepEqual(contracts.notifications, {
        path: '/notifications',
        scope: 'project',
        capability: null,
        roles: null,
    })
    assert.deepEqual(contracts.users, {
        path: '/users',
        scope: 'platform',
        capability: {
            kind: 'platform',
            value: 'manage_platform_users',
        },
        roles: null,
    })
})

test('未来新增 leaf 只登记 registry 数据即可进入通用过滤结果', () => {
    const future = {
        kind: 'leaf',
        id: 'future-governance',
        label: '未来治理功能',
        icon: 'settings',
        order: 99,
        scope: 'platform',
        capability: { kind: 'platform', value: 'manage_platform_settings' },
        roles: null,
        placement: 'sidebar',
        path: '/future-governance',
        activePathPrefixes: ['/future-governance'],
        route: { kind: 'existing' },
    }
    const registry = registryModule.navigationRegistry.map((node) =>
        node.id === 'governance-center'
            ? { ...node, children: [...node.children, future] }
            : node,
    )
    const items = registryModule.filterNavigationRegistry(
        registry,
        'sidebar',
        platformProjectAdmin,
    ).flatMap((node) => node.kind === 'leaf' ? [node] : node.children)
    assert.ok(items.some((item) => item.id === 'future-governance'))
})

test('当前路由强制展开所属 group 且不破坏其他展开选择', () => {
    const nodes = registryModule.visibleNavigationNodes(
        'sidebar',
        platformProjectAdmin,
    )
    assert.equal(
        stateModule.findActiveNavigationGroupID(nodes, '/automation-logs'),
        'intelligent-operations',
    )
    assert.deepEqual(
        stateModule.expandActiveNavigationGroup(
            { 'project-operations': true },
            'intelligent-operations',
        ),
        {
            'project-operations': true,
            'intelligent-operations': true,
        },
    )
    assert.deepEqual(
        stateModule.toggleNavigationGroup(
            { 'intelligent-operations': true },
            'intelligent-operations',
            'intelligent-operations',
        ),
        { 'intelligent-operations': true },
    )
})

test('展开状态使用版本化 subject/session key 隔离并清理过期 group ID', () => {
    const values = new Map()
    const storage = {
        getItem: (key) => values.get(key) ?? null,
        setItem: (key, value) => values.set(key, value),
    }
    const alice = { subject: 'alice', session_id: 'session-a' }
    const bob = { subject: 'bob', session_id: 'session-b' }
    assert.match(
        stateModule.navigationStateStorageKey(alice),
        /navigation-groups\.v2/,
    )
    stateModule.saveNavigationGroupState(
        storage,
        alice,
        { 'project-operations': true, expired: true },
        ['project-operations'],
    )
    assert.deepEqual(
        stateModule.loadNavigationGroupState(
            storage,
            alice,
            ['project-operations', 'governance-center'],
        ),
        { 'project-operations': true },
    )
    assert.deepEqual(
        stateModule.loadNavigationGroupState(
            storage,
            bob,
            ['project-operations'],
        ),
        {},
    )
})

test('树节点键盘约定覆盖 Enter/Space 且账号节点不进入侧栏', () => {
    assert.equal(stateModule.isNavigationToggleKey('Enter'), true)
    assert.equal(stateModule.isNavigationToggleKey(' '), true)
    assert.equal(stateModule.isNavigationToggleKey('Escape'), false)
    assert.deepEqual(
        registryModule.visibleNavigationItems('account', {
            platformRole: 'member',
            projectRole: null,
            hasProject: false,
        }).map((item) => item.id),
        ['account-profile', 'account-security', 'trusted-devices', 'login-history'],
    )
})
