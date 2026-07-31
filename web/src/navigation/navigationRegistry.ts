import {
    hasPlatformCapability,
    parsePlatformRole,
    type PlatformCapability,
    type PlatformRole,
} from '@/lib/accessControl'
import {
    hasProjectCapability,
    parseProjectRole,
    projectRoleValues,
    type ProjectCapability,
    type ProjectRole,
} from '@/lib/projectScope'

export type NavigationScope = 'global' | 'project' | 'platform'
export type NavigationPlacement = 'sidebar' | 'account'
export type NavigationIcon =
    | 'home'
    | 'workbench'
    | 'tickets'
    | 'notifications'
    | 'automation'
    | 'agents'
    | 'webhook'
    | 'integrationRuntime'
    | 'memberships'
    | 'users'
    | 'projects'
    | 'settings'
    | 'audit'
    | 'security'
    | 'loginHistory'

export type NavigationCapability =
    | { kind: 'platform'; value: PlatformCapability }
    | { kind: 'project'; value: ProjectCapability }
    | null

export type NavigationRoles =
    | { kind: 'platform'; values: readonly PlatformRole[] }
    | { kind: 'project'; values: readonly ProjectRole[] }
    | null

export type CustomNavigationComponent =
    | 'workbench'
    | 'workbenchDashboard'
    | 'automationIndex'
    | 'agentControl'
    | 'webhookSettings'
    | 'integrationRuntime'
    | 'projectMemberships'
    | 'platformProjects'
    | 'platformAudit'
    | 'platformConfig'
    | 'platformEmail'
    | 'accountProfile'
    | 'accountSecurity'
    | 'trustedDevices'
    | 'loginHistory'

export type NavigationRouteContract =
    | { kind: 'existing' }
    | {
        kind: 'custom'
        component: CustomNavigationComponent
        legacyPaths?: readonly string[]
    }

interface NavigationNodeBase {
    id: string
    label: string
    icon: NavigationIcon
    order: number
    scope: NavigationScope
    capability: NavigationCapability
    roles: NavigationRoles
    placement: NavigationPlacement
}

export interface NavigationLeafNode extends NavigationNodeBase {
    kind: 'leaf'
    path: string
    activePathPrefixes: readonly string[]
    activePathExclusions?: readonly string[]
    route: NavigationRouteContract
    children?: never
}

export interface NavigationGroupNode extends NavigationNodeBase {
    kind: 'group'
    path: null
    children: readonly NavigationLeafNode[]
}

export type NavigationNode = NavigationGroupNode | NavigationLeafNode

export type AdminResourceName =
    | 'tickets'
    | 'users'
    | 'notifications'
    | 'automation-rules'
    | 'automation-logs'

export type AdminResourceView = 'list' | 'show' | 'edit' | 'create'

type ResourceViewAccessOverride = {
    capability?: NavigationCapability
    roles?: NavigationRoles
}

export interface ResourceAccessContract {
    resource: AdminResourceName
    navigationNodeID: string
    views: Partial<Record<AdminResourceView, ResourceViewAccessOverride>>
}

const leaf = (
    placement: NavigationPlacement,
    node: Omit<NavigationLeafNode, 'kind' | 'placement' | 'route'> & {
        route?: NavigationRouteContract
    },
): NavigationLeafNode => ({
    kind: 'leaf',
    placement,
    route: { kind: 'existing' },
    ...node,
})

export const navigationRegistry: readonly NavigationNode[] = [
    {
        kind: 'group',
        id: 'workbench',
        label: '工作台',
        path: null,
        order: 10,
        scope: 'global',
        capability: null,
        roles: null,
        icon: 'workbench',
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'workbench-dashboard',
                label: '运营大屏',
                path: '/workbench/dashboard',
                activePathPrefixes: ['/workbench/dashboard'],
                order: 10,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'workbench',
                route: {
                    kind: 'custom',
                    component: 'workbenchDashboard',
                },
            }),
            leaf('sidebar', {
                id: 'cross-project-workbench',
                label: '跨项目工作台',
                path: '/workbench',
                activePathPrefixes: ['/workbench'],
                activePathExclusions: ['/workbench/dashboard'],
                order: 20,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'workbench',
                route: { kind: 'custom', component: 'workbench' },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'project-operations',
        label: '项目运营',
        icon: 'tickets',
        order: 20,
        scope: 'project',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'project-overview',
                label: '项目概览',
                path: '/',
                activePathPrefixes: ['/'],
                order: 10,
                scope: 'project',
                capability: null,
                roles: null,
                icon: 'home',
            }),
            leaf('sidebar', {
                id: 'tickets',
                label: '工单管理',
                path: '/tickets',
                activePathPrefixes: ['/tickets'],
                order: 20,
                scope: 'project',
                capability: null,
                roles: null,
                icon: 'tickets',
            }),
            leaf('sidebar', {
                id: 'notifications',
                label: '项目通知',
                path: '/notifications',
                activePathPrefixes: ['/notifications'],
                order: 30,
                scope: 'project',
                capability: null,
                roles: null,
                icon: 'notifications',
            }),
        ],
    },
    {
        kind: 'group',
        id: 'intelligent-operations',
        label: '智能运营',
        icon: 'agents',
        order: 30,
        scope: 'project',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'agents',
                label: 'AI 智能体',
                path: '/agent-control',
                activePathPrefixes: ['/agent-control'],
                order: 10,
                scope: 'project',
                capability: { kind: 'project', value: 'manage_agents' },
                roles: { kind: 'project', values: ['project_admin'] },
                icon: 'agents',
                route: { kind: 'custom', component: 'agentControl' },
            }),
            leaf('sidebar', {
                id: 'automation',
                label: '自动化',
                path: '/automation',
                activePathPrefixes: [
                    '/automation',
                    '/automation-rules',
                    '/automation-logs',
                ],
                order: 20,
                scope: 'project',
                capability: { kind: 'project', value: 'manage_automation' },
                roles: null,
                icon: 'automation',
                route: { kind: 'custom', component: 'automationIndex' },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'integration-center',
        label: '集成中心',
        icon: 'webhook',
        order: 40,
        scope: 'project',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'webhook',
                label: 'Webhook',
                path: '/webhook-settings',
                activePathPrefixes: ['/webhook-settings'],
                order: 10,
                scope: 'project',
                capability: { kind: 'project', value: 'manage_integrations' },
                roles: null,
                icon: 'webhook',
                route: { kind: 'custom', component: 'webhookSettings' },
            }),
            leaf('sidebar', {
                id: 'integration-runtime',
                label: '事件投递',
                path: '/integration-runtime',
                activePathPrefixes: ['/integration-runtime'],
                order: 20,
                scope: 'project',
                capability: { kind: 'project', value: 'manage_agents' },
                roles: { kind: 'project', values: ['project_admin'] },
                icon: 'integrationRuntime',
                route: { kind: 'custom', component: 'integrationRuntime' },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'project-configuration',
        label: '项目配置',
        icon: 'memberships',
        order: 50,
        scope: 'project',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'memberships',
                label: '项目成员',
                path: '/project-memberships',
                activePathPrefixes: ['/project-memberships'],
                order: 10,
                scope: 'project',
                capability: { kind: 'project', value: 'manage_memberships' },
                roles: { kind: 'project', values: ['project_admin'] },
                icon: 'memberships',
                route: { kind: 'custom', component: 'projectMemberships' },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'governance-center',
        label: '治理中心',
        icon: 'settings',
        order: 60,
        scope: 'platform',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'platform-projects',
                label: '项目治理',
                path: '/platform/projects',
                activePathPrefixes: ['/platform/projects'],
                order: 10,
                scope: 'platform',
                capability: null,
                roles: { kind: 'platform', values: ['platform_admin'] },
                icon: 'projects',
                route: { kind: 'custom', component: 'platformProjects' },
            }),
            leaf('sidebar', {
                id: 'users',
                label: '平台身份与访问',
                path: '/users',
                activePathPrefixes: ['/users'],
                order: 20,
                scope: 'platform',
                capability: { kind: 'platform', value: 'manage_platform_users' },
                roles: null,
                icon: 'users',
            }),
            leaf('sidebar', {
                id: 'platform-audit',
                label: '审计中心',
                path: '/platform/audit',
                activePathPrefixes: ['/platform/audit'],
                order: 30,
                scope: 'platform',
                capability: { kind: 'platform', value: 'view_platform_audit' },
                roles: null,
                icon: 'audit',
                route: { kind: 'custom', component: 'platformAudit' },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'system-settings',
        label: '系统设置',
        icon: 'settings',
        order: 70,
        scope: 'platform',
        capability: null,
        roles: null,
        path: null,
        placement: 'sidebar',
        children: [
            leaf('sidebar', {
                id: 'platform-config',
                label: '公共配置',
                path: '/system-settings',
                activePathPrefixes: ['/system-settings'],
                order: 10,
                scope: 'platform',
                capability: { kind: 'platform', value: 'manage_platform_settings' },
                roles: null,
                icon: 'settings',
                route: {
                    kind: 'custom',
                    component: 'platformConfig',
                    legacyPaths: ['/system-settings/overview'],
                },
            }),
            leaf('sidebar', {
                id: 'platform-email-settings',
                label: '邮件外发',
                path: '/system-settings/email',
                activePathPrefixes: ['/system-settings/email', '/email-settings'],
                order: 20,
                scope: 'platform',
                capability: { kind: 'platform', value: 'manage_email_settings' },
                roles: null,
                icon: 'notifications',
                route: {
                    kind: 'custom',
                    component: 'platformEmail',
                    legacyPaths: ['/email-settings'],
                },
            }),
        ],
    },
    {
        kind: 'group',
        id: 'account',
        label: '账号',
        icon: 'security',
        order: 10,
        scope: 'global',
        capability: null,
        roles: null,
        path: null,
        placement: 'account',
        children: [
            leaf('account', {
                id: 'account-profile',
                label: '个人资料',
                path: '/account/profile',
                activePathPrefixes: ['/account/profile'],
                order: 10,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'users',
                route: { kind: 'custom', component: 'accountProfile' },
            }),
            leaf('account', {
                id: 'account-security',
                label: '账号安全',
                path: '/account/security',
                activePathPrefixes: ['/account/security'],
                order: 20,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'security',
                route: { kind: 'custom', component: 'accountSecurity' },
            }),
            leaf('account', {
                id: 'trusted-devices',
                label: '可信设备',
                path: '/account/trusted-devices',
                activePathPrefixes: ['/account/trusted-devices'],
                order: 30,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'security',
                route: { kind: 'custom', component: 'trustedDevices' },
            }),
            leaf('account', {
                id: 'login-history',
                label: '登录历史',
                path: '/account/login-history',
                activePathPrefixes: ['/account/login-history'],
                order: 40,
                scope: 'global',
                capability: null,
                roles: null,
                icon: 'loginHistory',
                route: { kind: 'custom', component: 'loginHistory' },
            }),
        ],
    },
] as const

export const resourceAccessContracts: readonly ResourceAccessContract[] = [
    {
        resource: 'tickets',
        navigationNodeID: 'tickets',
        views: {
            list: {},
            show: {},
            edit: {
                capability: {
                    kind: 'project',
                    value: 'edit_ticket_safe_fields',
                },
            },
            create: {
                capability: { kind: 'project', value: 'create_ticket' },
            },
        },
    },
    {
        resource: 'users',
        navigationNodeID: 'users',
        views: { list: {}, show: {}, edit: {}, create: {} },
    },
    {
        resource: 'notifications',
        navigationNodeID: 'notifications',
        views: {
            list: {},
            create: {
                capability: {
                    kind: 'project',
                    value: 'manage_notifications',
                },
                roles: {
                    kind: 'project',
                    values: ['project_admin', 'manager'],
                },
            },
        },
    },
    {
        resource: 'automation-rules',
        navigationNodeID: 'automation',
        views: {
            list: {},
            show: {},
            edit: {},
            create: {},
        },
    },
    {
        resource: 'automation-logs',
        navigationNodeID: 'automation',
        views: { list: {} },
    },
] as const

const allNavigationLeaves = (): NavigationLeafNode[] =>
    navigationRegistry.flatMap((node) =>
        node.kind === 'leaf' ? [node] : [...node.children],
    )

export const resourceViewNavigationNode = (
    resource: AdminResourceName,
    view: AdminResourceView,
): NavigationLeafNode => {
    const contract = resourceAccessContracts.find(
        (candidate) => candidate.resource === resource,
    )
    if (!contract || !(view in contract.views)) {
        throw new Error(`资源 ${resource} 未声明 ${view} 访问契约`)
    }
    const node = allNavigationLeaves().find(
        (candidate) => candidate.id === contract.navigationNodeID,
    )
    if (!node) {
        throw new Error(
            `资源 ${resource} 引用了不存在的导航节点 ${contract.navigationNodeID}`,
        )
    }
    const override = contract.views[view] ?? {}
    return {
        ...node,
        capability: override.capability === undefined
            ? node.capability
            : override.capability,
        roles: override.roles === undefined ? node.roles : override.roles,
    }
}

const allowedScopes = new Set(['global', 'project', 'platform'])
const allowedPlacements = new Set(['sidebar', 'account'])
const allowedIcons = new Set<NavigationIcon>([
    'home', 'workbench', 'tickets', 'notifications', 'automation', 'agents',
    'webhook', 'integrationRuntime', 'memberships', 'users', 'projects',
    'settings', 'audit', 'security', 'loginHistory',
])
const allowedPlatformCapabilities = new Set<PlatformCapability>([
    'manage_platform_users',
    'manage_platform_settings',
    'manage_email_settings',
    'view_platform_audit',
    'operate_emergency_controls',
])
const allowedProjectCapabilities = new Set<ProjectCapability>([
    'view_project',
    'create_ticket',
    'edit_ticket_safe_fields',
    'manage_ticket_workflow',
    'assign_ticket',
    'delete_ticket',
    'write_public_content',
    'write_internal_content',
    'manage_notifications',
    'manage_automation',
    'manage_integrations',
    'manage_memberships',
    'manage_agents',
])
const allowedPlatformRoles = new Set<PlatformRole>([
    'platform_admin',
    'security_auditor',
    'emergency_operator',
    'member',
])
const allowedProjectRoles = new Set<ProjectRole>(projectRoleValues)
const allowedCustomComponents = new Set<CustomNavigationComponent>([
    'workbench',
    'workbenchDashboard',
    'automationIndex',
    'agentControl',
    'webhookSettings',
    'integrationRuntime',
    'projectMemberships',
    'platformProjects',
    'platformAudit',
    'platformConfig',
    'platformEmail',
    'accountProfile',
    'accountSecurity',
    'trustedDevices',
    'loginHistory',
])
const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

export const validateNavigationRegistry = (value: unknown): string[] => {
    const errors: string[] = []
    const ids = new Set<string>()
    const paths = new Set<string>()
    const visiting = new WeakSet<object>()

    const visit = (
        node: unknown,
        depth: number,
        parent?: Record<string, unknown>,
    ) => {
        if (!isRecord(node)) {
            errors.push('导航节点必须是对象')
            return
        }
        if (visiting.has(node)) {
            errors.push('导航 registry 不能包含循环引用')
            return
        }
        visiting.add(node)
        const id = typeof node.id === 'string' ? node.id : ''
        if (!id) errors.push('导航节点必须有稳定 id')
        else if (ids.has(id)) errors.push(`导航节点 id 重复：${id}`)
        else ids.add(id)
        const scope = String(node.scope)
        if (!allowedScopes.has(scope)) errors.push(`导航节点 ${id} 的 scope 非法`)
        if (!allowedPlacements.has(String(node.placement))) {
            errors.push(`导航节点 ${id} 的 placement 非法`)
        }
        if (!allowedIcons.has(node.icon as NavigationIcon)) {
            errors.push(`导航节点 ${id} 的 icon 非法`)
        }
        if (
            typeof node.order !== 'number' ||
            !Number.isSafeInteger(node.order) ||
            node.order <= 0
        ) errors.push(`导航节点 ${id} 的 order 非法`)
        if (
            parent &&
            (
                node.scope !== parent.scope ||
                node.placement !== parent.placement
            )
        ) errors.push(`导航节点 ${id} 与 parent 的 scope/placement 不一致`)
        const capability = node.capability
        if (
            capability !== null &&
            (
                !isRecord(capability) ||
                !['project', 'platform'].includes(String(capability.kind))
            )
        ) errors.push(`导航节点 ${id} 的 capability 非法`)
        else if (isRecord(capability)) {
            if (
                (capability.kind === 'project' && scope !== 'project') ||
                (capability.kind === 'platform' && scope !== 'platform')
            ) errors.push(`导航节点 ${id} 的 capability 与 scope 不匹配`)
            const allowed = capability.kind === 'project'
                ? allowedProjectCapabilities
                : allowedPlatformCapabilities
            if (!allowed.has(capability.value as never)) {
                errors.push(`导航节点 ${id} 的 capability value 非法`)
            }
        }
        const roles = node.roles
        if (
            roles !== null &&
            (
                !isRecord(roles) ||
                !['project', 'platform'].includes(String(roles.kind)) ||
                !Array.isArray(roles.values)
            )
        ) errors.push(`导航节点 ${id} 的 roles 非法`)
        else if (isRecord(roles)) {
            if (
                (roles.kind === 'project' && scope !== 'project') ||
                (roles.kind === 'platform' && scope !== 'platform')
            ) errors.push(`导航节点 ${id} 的 roles 与 scope 不匹配`)
            const allowed = roles.kind === 'project'
                ? allowedProjectRoles
                : allowedPlatformRoles
            if (
                (roles.values as unknown[]).some((role) =>
                    !allowed.has(role as never),
                )
            ) errors.push(`导航节点 ${id} 包含非法 role`)
        }

        if (node.kind === 'group') {
            if (depth !== 0) errors.push(`导航分组 ${id} 只能位于第一层`)
            if (node.path !== null) errors.push(`导航分组 ${id} 不能配置 path`)
            if (!Array.isArray(node.children) || node.children.length === 0) {
                errors.push(`导航分组 ${id} 必须至少包含一个 leaf`)
            } else {
                for (const child of node.children) visit(child, depth + 1, node)
            }
        } else if (node.kind === 'leaf') {
            if (depth > 1) errors.push(`导航 leaf ${id} 最多只能位于第二层`)
            if ('children' in node) errors.push(`导航 leaf ${id} 不能包含 children`)
            const path = typeof node.path === 'string' ? node.path : ''
            if (!path.startsWith('/')) errors.push(`导航 leaf ${id} 的 path 非法`)
            else if (paths.has(path)) errors.push(`导航 path 重复：${path}`)
            else paths.add(path)
            const route = node.route
            if (
                !isRecord(route) ||
                !['existing', 'custom'].includes(String(route.kind))
            ) errors.push(`导航 leaf ${id} 的 route contract 非法`)
            else if (
                route.kind === 'custom' &&
                !allowedCustomComponents.has(
                    route.component as CustomNavigationComponent,
                )
            ) errors.push(`导航 leaf ${id} 的 component mapping 非法`)
            else if (
                route.kind === 'custom' &&
                route.legacyPaths !== undefined
            ) {
                if (!Array.isArray(route.legacyPaths)) {
                    errors.push(`导航 leaf ${id} 的 legacyPaths 非法`)
                } else {
                    for (const legacyPath of route.legacyPaths) {
                        if (
                            typeof legacyPath !== 'string' ||
                            !legacyPath.startsWith('/')
                        ) {
                            errors.push(`导航 leaf ${id} 包含非法 legacy path`)
                        } else if (paths.has(legacyPath)) {
                            errors.push(`导航 path 重复：${legacyPath}`)
                        } else {
                            paths.add(legacyPath)
                        }
                    }
                }
            }
            if (
                !Array.isArray(node.activePathPrefixes) ||
                node.activePathPrefixes.length === 0
            ) errors.push(`导航 leaf ${id} 必须声明 activePathPrefixes`)
            else if (
                node.activePathPrefixes.some((prefix) =>
                    typeof prefix !== 'string' || !prefix.startsWith('/'),
                )
            ) errors.push(`导航 leaf ${id} 包含非法 active path`)
            else if (
                new Set(node.activePathPrefixes).size !==
                node.activePathPrefixes.length
            ) errors.push(`导航 leaf ${id} 的 active path 重复`)
            if (
                node.activePathExclusions !== undefined &&
                (
                    !Array.isArray(node.activePathExclusions) ||
                    node.activePathExclusions.some((prefix) =>
                        typeof prefix !== 'string' || !prefix.startsWith('/'),
                    )
                )
            ) errors.push(`导航 leaf ${id} 包含非法 active path exclusion`)
        } else {
            errors.push(`导航节点 ${id || '(unknown)'} 的 kind 非法`)
        }
        visiting.delete(node)
    }

    if (!Array.isArray(value)) return ['导航 registry 必须是数组']
    for (const node of value) visit(node, 0)
    return errors
}

const registryErrors = validateNavigationRegistry(navigationRegistry)
if (registryErrors.length > 0) {
    throw new Error(`导航 registry 无效：${registryErrors.join('；')}`)
}
const resourceNames = new Set<AdminResourceName>()
for (const contract of resourceAccessContracts) {
    if (resourceNames.has(contract.resource)) {
        throw new Error(`资源访问契约重复：${contract.resource}`)
    }
    resourceNames.add(contract.resource)
    for (const view of Object.keys(contract.views) as AdminResourceView[]) {
        resourceViewNavigationNode(contract.resource, view)
    }
}

export interface NavigationAccessContext {
    platformRole: PlatformRole | null | undefined
    projectRole: ProjectRole | null | undefined
    hasProject: boolean
}

export const isNavigationNodeVisible = (
    node: NavigationNode,
    context: NavigationAccessContext,
): boolean => {
    const platformRole = parsePlatformRole(context.platformRole)
    const projectRole = parseProjectRole(context.projectRole)
    if (node.scope === 'project' && (!context.hasProject || projectRole === null)) return false
    if (node.roles?.kind === 'platform' && (
        platformRole === null || !node.roles.values.includes(platformRole)
    )) return false
    if (node.roles?.kind === 'project' && (
        projectRole === null || !node.roles.values.includes(projectRole)
    )) return false
    if (node.capability?.kind === 'platform') {
        return hasPlatformCapability(platformRole, node.capability.value)
    }
    if (node.capability?.kind === 'project') {
        return hasProjectCapability(projectRole, node.capability.value)
    }
    return true
}

export const filterNavigationRegistry = (
    registry: readonly NavigationNode[],
    placement: NavigationPlacement,
    context: NavigationAccessContext,
): NavigationNode[] =>
    registry
        .filter((node) =>
            node.placement === placement &&
            isNavigationNodeVisible(node, context),
        )
        .map((node) => node.kind === 'leaf'
            ? node
            : {
                ...node,
                children: node.children
                    .filter((child) =>
                        child.placement === placement &&
                        isNavigationNodeVisible(child, context),
                    )
                    .sort((left, right) => left.order - right.order),
            })
        .filter((node) => node.kind === 'leaf' || node.children.length > 0)
        .sort((left, right) => left.order - right.order)

export const visibleNavigationNodes = (
    placement: NavigationPlacement,
    context: NavigationAccessContext,
): NavigationNode[] =>
    filterNavigationRegistry(navigationRegistry, placement, context)

export const visibleNavigationItems = (
    placement: NavigationPlacement,
    context: NavigationAccessContext,
): NavigationLeafNode[] =>
    visibleNavigationNodes(placement, context)
        .flatMap((node) => node.kind === 'leaf' ? [node] : node.children)
