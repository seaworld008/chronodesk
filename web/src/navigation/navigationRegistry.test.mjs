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
        '项目设置',
        '治理中心',
        '系统设置',
    ])
    assert.ok(nodes.every((node) =>
        node.label !== '我的工作' && node.label !== '平台管理',
    ))
    assert.equal(nodes[0].kind, 'group')
    assert.deepEqual(
        nodes[0].children.map((item) => item.path),
        ['/workbench/dashboard', '/workbench'],
    )
    assert.equal(
        stateModule.isNavigationItemActive(
            nodes[0].children[0],
            '/workbench/dashboard',
        ),
        true,
    )
    assert.equal(
        stateModule.isNavigationItemActive(
            nodes[0].children[1],
            '/workbench/dashboard',
        ),
        false,
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
    const governance = nodes.find((node) => node.id === 'governance-center')
    assert.deepEqual(
        governance.children.map((item) => item.id),
        [
            'platform-home',
            'platform-projects',
            'users',
            'platform-audit',
        ],
    )
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

test('安全与应急仅紧急运维员可见且平台职责共享平台工作台', () => {
    for (const platformRole of [
        'platform_admin',
        'security_auditor',
        'emergency_operator',
    ]) {
        const items = registryModule.visibleNavigationItems('sidebar', {
            platformRole,
            projectRole: null,
            hasProject: false,
        })
        assert.ok(
            items.some((item) => item.id === 'platform-home'),
            `${platformRole} should see platform workbench`,
        )
        assert.equal(
            items.some((item) => item.id === 'emergency-controls'),
            platformRole === 'emergency_operator',
        )
    }
    const member = registryModule.visibleNavigationItems('sidebar', {
        platformRole: 'member',
        projectRole: null,
        hasProject: false,
    })
    assert.ok(!member.some((item) => item.id === 'platform-home'))
    assert.ok(!member.some((item) => item.id === 'emergency-controls'))
})

test('观察员只有集成读取能力，系统设置子项保持单一激活', () => {
    const observerItems = registryModule.visibleNavigationItems('sidebar', {
        platformRole: 'member',
        projectRole: 'observer',
        hasProject: true,
    })
    const integrationRuntime = observerItems.find(
        (item) => item.id === 'integration-runtime',
    )
    assert.deepEqual(integrationRuntime.capability, {
        kind: 'project',
        value: 'view_integrations',
    })
    assert.ok(!observerItems.some((item) => item.id === 'webhook'))

    const platformItems = registryModule.visibleNavigationItems(
        'sidebar',
        platformProjectAdmin,
    )
    const config = platformItems.find((item) => item.id === 'platform-config')
    const email = platformItems.find(
        (item) => item.id === 'platform-email-settings',
    )
    assert.equal(
        stateModule.isNavigationItemActive(config, '/system-settings/email'),
        false,
    )
    assert.equal(
        stateModule.isNavigationItemActive(email, '/system-settings/email'),
        true,
    )
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
    const invalidSubroutes = registryModule.validateNavigationRegistry([
        {
            ...badLeaf,
            id: 'bad-subroutes',
            icon: 'home',
            order: 1,
            scope: 'global',
            placement: 'sidebar',
            capability: null,
            roles: null,
            activePathPrefixes: ['/bad-taxonomy'],
            route: {
                kind: 'custom',
                component: 'workbench',
                subroutes: [
                    { path: 'missing-slash', component: 'workbench' },
                    { path: '/bad-taxonomy', component: 'unknown-component' },
                ],
            },
        },
    ])
    assert.ok(
        invalidSubroutes.some((error) => error.includes('非法 subroute')),
        invalidSubroutes.join('；'),
    )
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
            'agentCollaboration',
            'automationIndex',
            'automationQuickReplies',
            'automationSLA',
            'automationTemplates',
            'emergencyControls',
            'integrationRuntime',
            'knowledgeManagement',
            'loginHistory',
            'platformAudit',
            'platformConfig',
            'platformEmail',
            'platformHome',
            'platformProjects',
            'projectBasicSettings',
            'projectIntakeSettings',
            'projectMemberships',
            'projectNotificationChannels',
            'projectQueueSettings',
            'trustedDevices',
            'webhookSettings',
            'workbench',
            'workbenchDashboard',
        ].sort(),
    )
    const email = custom.find((node) => node.route.component === 'platformEmail')
    assert.deepEqual(email.route.legacyPaths, ['/email-settings'])
    assert.equal(email.scope, 'platform')
    assert.deepEqual(email.capability, {
        kind: 'platform',
        value: 'manage_email_settings',
    })
    const automation = custom.find(
        (node) => node.route.component === 'automationIndex',
    )
    assert.equal(automation.route.subroutes, undefined)
    assert.deepEqual(automation.capability, {
        kind: 'project',
        value: 'manage_automation',
    })
    for (const [component, canonical, legacy] of [
        ['automationSLA', '/project-settings/sla', '/automation-sla'],
        [
            'automationTemplates',
            '/project-settings/templates',
            '/automation-templates',
        ],
        [
            'automationQuickReplies',
            '/project-settings/quick-replies',
            '/automation-quick-replies',
        ],
    ]) {
        const setting = custom.find(
            (node) => node.route.component === component,
        )
        assert.equal(setting.path, canonical)
        assert.deepEqual(setting.route.legacyPaths, [legacy])
        assert.equal(setting.scope, 'project')
    }
})

test('自动化只承载规则与日志，服务策略严格归入项目设置', () => {
    const managerItems = registryModule.visibleNavigationItems('sidebar', {
        platformRole: 'member',
        projectRole: 'manager',
        hasProject: true,
    })
    const agentItems = registryModule.visibleNavigationItems('sidebar', {
        platformRole: 'member',
        projectRole: 'agent',
        hasProject: true,
    })
    assert.ok(managerItems.some((item) => item.id === 'automation'))
    assert.ok(!agentItems.some((item) => item.id === 'automation'))
    const managerNodes = registryModule.visibleNavigationNodes('sidebar', {
        platformRole: 'member',
        projectRole: 'manager',
        hasProject: true,
    })
    assert.equal(
        stateModule.findActiveNavigationGroupID(
            managerNodes,
            '/automation-logs',
        ),
        'intelligent-operations',
    )
    assert.equal(
        stateModule.findActiveNavigationGroupID(
            managerNodes,
            '/project-settings/quick-replies',
        ),
        'project-configuration',
    )
    assert.equal(
        stateModule.findActiveNavigationGroupID(
            managerNodes,
            '/automation-quick-replies',
        ),
        'project-configuration',
    )
})

test('人机协作与知识库对所有项目成员可见且知识库只有一个运营入口', () => {
    for (const projectRole of [
        'project_admin',
        'manager',
        'agent',
        'requester',
        'observer',
    ]) {
        const items = registryModule.visibleNavigationItems('sidebar', {
            platformRole: 'member',
            projectRole,
            hasProject: true,
        })
        assert.ok(
            items.some((item) => item.id === 'agent-collaboration'),
            `${projectRole} should see safe collaboration workspace`,
        )
        assert.equal(
            items.some((item) => item.id === 'knowledge-management'),
            true,
        )
    }
    const managerNodes = registryModule.visibleNavigationNodes('sidebar', {
        platformRole: 'member',
        projectRole: 'manager',
        hasProject: true,
    })
    const projectSettings = managerNodes.find(
        (node) => node.id === 'project-configuration',
    )
    assert.equal(projectSettings.label, '项目设置')
    assert.deepEqual(
        projectSettings.children.map((item) => item.id),
        [
            'project-basic-settings',
            'memberships',
            'project-intake-settings',
            'project-sla-settings',
            'project-queue-settings',
            'project-ticket-templates',
            'project-quick-replies',
            'project-notification-channels',
        ],
    )
    const projectOperations = managerNodes.find(
        (node) => node.id === 'project-operations',
    )
    assert.deepEqual(
        projectOperations.children.map((item) => item.id),
        [
            'project-overview',
            'tickets',
            'knowledge-management',
            'notifications',
        ],
    )
    const knowledge = projectOperations.children.find(
        (item) => item.id === 'knowledge-management',
    )
    assert.equal(knowledge.label, '知识库')
    assert.equal(knowledge.path, '/knowledge')
    assert.deepEqual(
        knowledge.route.legacyPaths,
        ['/project-settings/knowledge'],
    )
    assert.deepEqual(knowledge.capability, {
        kind: 'project',
        value: 'view_project',
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

    assert.deepEqual(
        registryModule.resourceAccessContracts.map((contract) =>
            contract.resource,
        ),
        [
            'tickets',
            'users',
            'notifications',
            'automation-rules',
            'automation-logs',
        ],
    )
    assert.deepEqual(
        registryModule.resourceViewNavigationNode('tickets', 'edit').capability,
        { kind: 'project', value: 'edit_ticket_safe_fields' },
    )
    assert.deepEqual(
        registryModule.resourceViewNavigationNode(
            'notifications',
            'create',
        ).roles,
        {
            kind: 'project',
            values: ['project_admin', 'manager'],
        },
    )
    assert.deepEqual(
        registryModule.resourceViewNavigationNode(
            'automation-rules',
            'create',
        ).capability,
        { kind: 'project', value: 'manage_automation' },
    )
    assert.deepEqual(
        registryModule.resourceViewNavigationNode('users', 'edit').capability,
        { kind: 'platform', value: 'manage_platform_users' },
    )
    assert.throws(
        () => registryModule.resourceViewNavigationNode(
            'automation-logs',
            'edit',
        ),
        /未声明 edit 访问契约/,
    )
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
