import React from 'react'
import {
    Admin,
    CustomRoutes,
    Menu,
    Resource,
    Title,
    type LayoutProps,
    usePermissions,
    useSidebarState,
} from 'react-admin'
import {
    Navigate,
    Route,
    useLocation,
    useNavigate,
} from 'react-router-dom'
import {
    Alert,
    Box,
    Button,
    CircularProgress,
    Collapse,
    List,
    ListItemButton,
    ListItemIcon,
    ListItemText,
    Paper,
    Stack,
    Tooltip,
    Typography,
} from '@mui/material'
import {
    createTheme,
    type SxProps,
    type Theme,
} from '@mui/material/styles'
import { useQueryClient } from '@tanstack/react-query'

import { dataProvider } from './lib/dataProvider'
import {
    applyRemoteHumanSignOut,
    authProvider,
} from './lib/authProvider'
import { readHumanAccessToken } from './lib/humanSessionRuntime'
import { subscribeHumanSessionMetadata } from './lib/humanSessionChannel'
import {
    getPlatformRoleLabel,
    hasPlatformCapability,
    parsePlatformRole,
    type AccessPermissions,
} from './lib/accessControl'
import {
    getProjectRoleLabel,
    hasExactProjectRole,
    hasProjectCapability,
    loadAuthorizedProjects,
    parseProjectRole,
    projectAccessInvalidatedEvent,
    projectRoleValues,
    clearActiveProjectSelection,
    resolveActiveProjectAccess,
    readHumanSessionBinding,
    setActiveProjectKey,
    type AuthorizedProject,
    type ProjectCapability,
    type ProjectRole,
} from './lib/projectScope'
import {
    projectInventoryChangedEvent,
    projectScopeChangedEvent,
    sessionInvalidatedEvent,
    sessionReplacedEvent,
    signalSessionReplaced,
} from './lib/projectScopeEvents'

import {
    AutoFixHigh as AutomationIcon,
    ConfirmationNumber as TicketIcon,
    History as HistoryIcon,
    Notifications as NotificationIcon,
    People as UsersIcon,
    ExpandMore,
} from '@mui/icons-material'

import { CustomLayout as Layout } from './layout/CustomLayout'
import { CustomAppBar } from './layout/CustomAppBar'
import { focusMainContent } from './layout/skipNavigation'
import {
    sidebarClosedWidth,
    sidebarDefaultWidth,
} from '@/layout/sidebarWidth'
import LoginPage from './components/auth/LoginPage'
import { AppNotification } from './components/layout/AppNotification'
import { i18nProvider, muiZhCN } from './i18n'
import {
    directNavigationEntry,
    navigationRegistry,
    resourceViewNavigationNode,
    visibleNavigationNodes,
    type AdminResourceName,
    type AdminResourceView,
    type CustomNavigationComponent,
    type NavigationLeafNode,
} from './navigation/navigationRegistry'
import { NavigationIconGlyph } from './navigation/navigationIcons'
import {
    expandActiveNavigationGroup,
    findActiveNavigationGroupID,
    isNavigationItemActive,
    isNavigationToggleKey,
    loadNavigationGroupState,
    navigationStateStorageKey,
    saveNavigationGroupState,
    toggleNavigationGroup,
    validNavigationGroupIDs,
    type NavigationGroupState,
} from './navigation/navigationTreeState'

const PageLoading = () => (
    <Box
        role="status"
        aria-label="正在加载页面"
        sx={{ display: 'grid', minHeight: 240, placeItems: 'center' }}
    >
        <CircularProgress size={32} />
    </Box>
)

const lazyPage = <P extends object>(
    loader: () => Promise<{ default: React.ComponentType<P> }>,
) => {
    const LazyComponent = React.lazy(loader)
    const LazyPage = (props: P) => (
        <React.Suspense fallback={<PageLoading />}>
            <LazyComponent {...props} />
        </React.Suspense>
    )
    LazyPage.displayName = 'LazyAdminPage'
    return LazyPage
}

const RegisterPage = lazyPage(
    () => import('./components/auth/RegisterPage'),
)
const ForgotPasswordPage = lazyPage(
    () => import('./components/auth/ForgotPasswordPage'),
)
const ResetPasswordPage = lazyPage(
    () => import('./components/auth/ResetPasswordPage'),
)
const VerifyEmailPage = lazyPage(
    () => import('./components/auth/VerifyEmailPage'),
)
const ResendVerificationPage = lazyPage(
    () => import('./components/auth/ResendVerificationPage'),
)
const TicketDashboard = lazyPage(() => import('./admin/tickets/TicketDashboard'))
const TicketList = lazyPage(() => import('./admin/tickets/TicketListEnhanced'))
const TicketShow = lazyPage(() => import('./admin/tickets/TicketShow'))
const TicketEdit = lazyPage(() => import('./admin/tickets/TicketEdit'))
const TicketCreate = lazyPage(() => import('./admin/tickets/TicketCreate'))
const UserList = lazyPage(() => import('./admin/users/UserList'))
const UserShow = lazyPage(() => import('./admin/users/UserShow'))
const UserEdit = lazyPage(() => import('./admin/users/UserEdit'))
const UserCreate = lazyPage(() => import('./admin/users/UserCreate'))
const NotificationList = lazyPage(
    () => import('./admin/notifications/NotificationList'),
)
const NotificationCreate = lazyPage(
    () => import('./admin/notifications/NotificationCreate'),
)
const AutomationRuleList = lazyPage(
    () => import('./admin/automation/AutomationRuleList'),
)
const AutomationRuleShow = lazyPage(
    () => import('./admin/automation/AutomationRuleShow'),
)
const AutomationRuleCreate = lazyPage(
    () => import('./admin/automation/AutomationRuleCreate'),
)
const AutomationRuleEdit = lazyPage(
    () => import('./admin/automation/AutomationRuleEdit'),
)
const AutomationLogList = lazyPage(
    () => import('./admin/automation/AutomationLogList'),
)
const AutomationSLAList = lazyPage(
    () => import('./admin/automation/AutomationSLAList'),
)
const AutomationTemplateList = lazyPage(
    () => import('./admin/automation/AutomationTemplateList'),
)
const AutomationQuickReplyList = lazyPage(
    () => import('./admin/automation/AutomationQuickReplyList'),
)
const EmailSettings = lazyPage(() => import('./admin/settings/EmailSettings'))
const WebhookSettings = lazyPage(() => import('./admin/settings/WebhookSettings'))
const SystemSettings = lazyPage(() => import('./admin/settings/SystemSettings'))
const TrustedDevices = lazyPage(() => import('./admin/security/TrustedDevices'))
const LoginHistory = lazyPage(() => import('./admin/security/LoginHistory'))
const AccountProfile = lazyPage(() => import('./admin/security/AccountProfile'))
const AccountSecurity = lazyPage(() => import('./admin/security/AccountSecurity'))
const EmergencyControls = lazyPage(
    () => import('./admin/security/EmergencyControls'),
)
const AgentControlCenter = lazyPage(
    () => import('./admin/agents/AgentControlCenter'),
)
const AgentCollaborationWorkspace = lazyPage(
    () => import('./admin/agents/AgentCollaborationWorkspace'),
)
const KnowledgeManagementPage = lazyPage(
    () => import('./admin/knowledge/KnowledgeManagementPage'),
)
const IntegrationRuntime = lazyPage(
    () => import('./admin/integrations/IntegrationRuntime'),
)
const CrossProjectWorkbench = lazyPage(
    () => import('./admin/workbench/CrossProjectWorkbench'),
)
const WorkbenchDashboard = lazyPage(
    () => import('./admin/workbench/WorkbenchDashboard'),
)
const ProjectMembershipPage = lazyPage(
    () => import('./admin/projects/ProjectMembershipPage'),
)
const ProjectBasicSettingsPage = lazyPage(
    () => import('./admin/projects/ProjectBasicSettingsPage'),
)
const ProjectIntakeSettingsPage = lazyPage(
    () => import('./admin/projects/ProjectIntakeSettingsPage'),
)
const ProjectQueueSettingsPage = lazyPage(
    () => import('./admin/projects/ProjectQueueSettingsPage'),
)
const ProjectNotificationChannelsPage = lazyPage(
    () => import('./admin/projects/ProjectNotificationChannelsPage'),
)
const PlatformProjectGovernancePage = lazyPage(
    () => import('./admin/projects/PlatformProjectGovernancePage'),
)
const PlatformAuditPage = lazyPage(
    () => import('./admin/audit/PlatformAuditExplorer'),
)

const theme = createTheme(
    {
        palette: {
            mode: 'light',
            primary: {
                main: '#2563eb',
                light: '#60a5fa',
                dark: '#1d4ed8',
                contrastText: '#ffffff',
            },
            secondary: {
                main: '#64748b',
                light: '#94a3b8',
                dark: '#475569',
                contrastText: '#ffffff',
            },
            warning: {
                main: '#b45309',
                light: '#ff9800',
                dark: '#e65100',
                contrastText: '#ffffff',
            },
            background: {
                default: '#f8fafc',
                paper: '#ffffff',
            },
        },
        typography: {
            fontFamily: [
                'Inter',
                '-apple-system',
                'BlinkMacSystemFont',
                '"Segoe UI"',
                'Roboto',
                '"Helvetica Neue"',
                'Arial',
                'sans-serif',
            ].join(','),
        },
        shape: { borderRadius: 12 },
        sidebar: {
            width: sidebarDefaultWidth,
            closedWidth: sidebarClosedWidth,
        },
        components: {
            MuiCircularProgress: {
                defaultProps: {
                    'aria-label': '正在加载',
                },
            },
            RaSkipNavigationButton: {
                defaultProps: {
                    onClick: focusMainContent,
                },
            },
            RaEmpty: {
                styleOverrides: {
                    root: {
                        '& .RaEmpty-message': {
                            color: 'rgba(0, 0, 0, 0.6)',
                        },
                    },
                },
            },
        },
    },
    muiZhCN,
)

type ActiveProjectAccessState = {
    access: AuthorizedProject | null
    projects: AuthorizedProject[]
    errorCode: string | null
    isPending: boolean
}

const errorCode = (error: unknown): string | null => {
    if (
        typeof error !== 'object' ||
        error === null ||
        !('body' in error) ||
        typeof error.body !== 'object' ||
        error.body === null ||
        !('code' in error.body) ||
        typeof error.body.code !== 'string'
    ) {
        return null
    }
    return error.body.code
}

const useActiveProjectAccess = (): ActiveProjectAccessState => {
    const [state, setState] = React.useState<ActiveProjectAccessState>({
        access: null,
        projects: [],
        errorCode: null,
        isPending: true,
    })
    const loadSequence = React.useRef(0)

    React.useEffect(() => {
        let active = true
        const loadAccess = async () => {
            const sequence = ++loadSequence.current
            setState((current) => ({ ...current, isPending: true }))
            try {
                const projects = await loadAuthorizedProjects()
                if (!active || sequence !== loadSequence.current) return
                try {
                    const access = await resolveActiveProjectAccess()
                    if (active && sequence === loadSequence.current) {
                        setState({
                            access,
                            projects,
                            errorCode: null,
                            isPending: false,
                        })
                    }
                } catch (requestError) {
                    if (active && sequence === loadSequence.current) {
                        setState({
                            access: null,
                            projects,
                            errorCode: errorCode(requestError),
                            isPending: false,
                        })
                    }
                }
            } catch (requestError) {
                if (active && sequence === loadSequence.current) {
                    setState({
                        access: null,
                        projects: [],
                        errorCode: errorCode(requestError),
                        isPending: false,
                    })
                }
            }
        }
        void loadAccess()
        const reloadAccess = () => void loadAccess()
        window.addEventListener(projectAccessInvalidatedEvent, reloadAccess)
        window.addEventListener(projectInventoryChangedEvent, reloadAccess)
        window.addEventListener(projectScopeChangedEvent, reloadAccess)
        return () => {
            active = false
            loadSequence.current += 1
            window.removeEventListener(
                projectAccessInvalidatedEvent,
                reloadAccess,
            )
            window.removeEventListener(
                projectInventoryChangedEvent,
                reloadAccess,
            )
            window.removeEventListener(projectScopeChangedEvent, reloadAccess)
        }
    }, [])

    return state
}

export const ExactProjectRoleRoute = ({
    roles,
    capability,
    children,
}: React.PropsWithChildren<{
    roles: readonly ProjectRole[]
    capability?: ProjectCapability
}>) => {
    const { access, isPending } = useActiveProjectAccess()
    if (isPending) return <PageLoading />
    const role = parseProjectRole(access?.project_role)
    if (
        role === null ||
        !hasExactProjectRole(role, roles) ||
        (capability !== undefined &&
            !hasProjectCapability(role, capability))
    ) {
        return <Navigate to="/" replace />
    }
    return <>{children}</>
}

const PlatformNavigationRoute = ({
    node,
    children,
}: React.PropsWithChildren<{ node: NavigationLeafNode }>) => {
    const { permissions, isPending } = usePermissions<AccessPermissions>()
    if (isPending) return <PageLoading />
    const role = parsePlatformRole(permissions?.platform_role)
    const allowedRoles = node.roles?.kind === 'platform'
        ? node.roles.values
        : null
    const capability = node.capability?.kind === 'platform'
        ? node.capability.value
        : null
    if (
        role === null ||
        (allowedRoles && !allowedRoles.includes(role)) ||
        (capability && !hasPlatformCapability(role, capability))
    ) {
        return <Navigate to="/" replace />
    }
    return <>{children}</>
}

const NavigationContractGuard = ({
    node,
    children,
}: React.PropsWithChildren<{ node: NavigationLeafNode }>) => {
    if (node.scope === 'project') {
        return (
            <ExactProjectRoleRoute
                roles={
                    node.roles?.kind === 'project'
                        ? node.roles.values
                        : projectRoleValues
                }
                capability={
                    node.capability?.kind === 'project'
                        ? node.capability.value
                        : undefined
                }
            >
                {children}
            </ExactProjectRoleRoute>
        )
    }
    if (node.scope === 'platform') {
        return (
            <PlatformNavigationRoute node={node}>
                {children}
            </PlatformNavigationRoute>
        )
    }
    return <>{children}</>
}

const withResourceViewContract = <P extends object>(
    Component: React.ComponentType<P>,
    resource: AdminResourceName,
    view: AdminResourceView,
) => {
    const node = resourceViewNavigationNode(resource, view)
    const GuardedView = (props: P) => (
        <NavigationContractGuard node={node}>
            <Component {...props} />
        </NavigationContractGuard>
    )
    GuardedView.displayName = `ResourceContract${
        Component.displayName || Component.name || 'View'
    }`
    return GuardedView
}

const PlatformUserList = withResourceViewContract(UserList, 'users', 'list')
const PlatformUserShow = withResourceViewContract(UserShow, 'users', 'show')
const PlatformUserEdit = withResourceViewContract(UserEdit, 'users', 'edit')
const PlatformUserCreate = withResourceViewContract(UserCreate, 'users', 'create')
const ProjectTicketList = withResourceViewContract(TicketList, 'tickets', 'list')
const ProjectTicketShow = withResourceViewContract(TicketShow, 'tickets', 'show')
const ProjectTicketEdit = withResourceViewContract(TicketEdit, 'tickets', 'edit')
const ProjectTicketCreate = withResourceViewContract(TicketCreate, 'tickets', 'create')
const ProjectNotificationList = withResourceViewContract(
    NotificationList,
    'notifications',
    'list',
)
const ProjectNotificationCreate = withResourceViewContract(
    NotificationCreate,
    'notifications',
    'create',
)
const ProjectAutomationRuleList = withResourceViewContract(
    AutomationRuleList,
    'automation-rules',
    'list',
)
const ProjectAutomationRuleShow = withResourceViewContract(
    AutomationRuleShow,
    'automation-rules',
    'show',
)
const ProjectAutomationRuleEdit = withResourceViewContract(
    AutomationRuleEdit,
    'automation-rules',
    'edit',
)
const ProjectAutomationRuleCreate = withResourceViewContract(
    AutomationRuleCreate,
    'automation-rules',
    'create',
)
const ProjectAutomationLogList = withResourceViewContract(
    AutomationLogList,
    'automation-logs',
    'list',
)

const NoAuthorizedProjects = () => (
    <Box
        data-testid="no-authorized-projects"
        sx={{ p: 4, display: 'grid', placeItems: 'center', minHeight: 360 }}
    >
        <Paper sx={{ p: 4, maxWidth: 640, textAlign: 'center' }}>
            <Title title="暂无授权项目" />
            <Typography variant="h4" gutterBottom>
                暂无授权项目
            </Typography>
            <Typography color="text.secondary">
                当前账号尚未加入任何有效项目。请联系项目管理员在项目成员管理中授予职责。
            </Typography>
        </Paper>
    </Box>
)

const ProjectSelectionPage = ({
    projects,
    accessLost,
}: {
    projects: AuthorizedProject[]
    accessLost: boolean
}) => (
    <Box
        data-testid={
            accessLost
                ? 'active-project-access-lost'
                : 'active-project-selection-required'
        }
        sx={{ p: 4, display: 'grid', placeItems: 'center', minHeight: 360 }}
    >
        <Paper sx={{ p: 4, width: 'min(100%, 720px)' }}>
            <Title title={accessLost ? '项目访问权限已失效' : '请选择项目'} />
            <Typography variant="h4" gutterBottom>
                {accessLost ? '项目访问权限已失效' : '请选择要进入的项目'}
            </Typography>
            <Alert severity={accessLost ? 'warning' : 'info'} sx={{ mb: 3 }}>
                {accessLost
                    ? '你已无法访问之前的项目。为防止跨项目复用页面数据，请重新选择一个仍有授权的项目。'
                    : '进入项目资源前必须明确选择项目；系统不会自动切换到其他项目。'}
            </Alert>
            <Stack spacing={1.5}>
                {projects.map(({ project, project_role }) => (
                    <Button
                        key={project.public_id}
                        variant="outlined"
                        data-testid={`select-project-${project.key}`}
                        onClick={() => setActiveProjectKey(project.key, projects)}
                        sx={{ justifyContent: 'space-between' }}
                    >
                        <span>{project.name}</span>
                        <span>{getProjectRoleLabel(project_role)}</span>
                    </Button>
                ))}
            </Stack>
        </Paper>
    </Box>
)

const PlatformHome = ({ permissions }: { permissions: AccessPermissions }) => {
    const navigate = useNavigate()
    const platformRole = parsePlatformRole(permissions.platform_role)
    const actions = [
        {
            visible: hasPlatformCapability(
                platformRole,
                'manage_platform_users',
            ),
            label: '平台用户管理',
            path: '/users',
        },
        {
            visible: platformRole === 'platform_admin',
            label: '平台项目治理',
            path: '/platform/projects',
        },
        {
            visible: platformRole === 'platform_admin',
            label: '创建项目',
            path: '/platform/projects?create=1',
        },
        {
            visible: hasPlatformCapability(
                platformRole,
                'view_platform_audit',
            ),
            label: '平台审计',
            path: '/platform/audit',
        },
        {
            visible: hasPlatformCapability(
                platformRole,
                'operate_emergency_controls',
            ),
            label: '安全与应急',
            path: '/platform/emergency-controls',
        },
    ].filter(({ visible }) => visible)

    return (
        <Box data-testid="platform-home" sx={{ p: 4 }}>
            <Title title="平台工作台" />
            <Paper sx={{ p: 4 }}>
                <Typography variant="h4" gutterBottom>
                    平台工作台
                </Typography>
                <Typography color="text.secondary">
                    当前平台职责：
                    {getPlatformRoleLabel(platformRole)}
                </Typography>
                <Typography color="text.secondary" sx={{ mt: 1 }}>
                    平台职责不授予项目业务权限。进入工单、自动化或智能体控制前，
                    当前账号必须拥有目标项目的有效成员关系。
                </Typography>
                {actions.length > 0 ? (
                    <Stack
                        direction={{ xs: 'column', sm: 'row' }}
                        spacing={2}
                        sx={{ mt: 3 }}
                    >
                        {actions.map(({ label, path }) => (
                            <Button
                                key={path}
                                variant="contained"
                                onClick={() => navigate(path)}
                            >
                                {label}
                            </Button>
                        ))}
                    </Stack>
                ) : (
                    <Alert severity="info" sx={{ mt: 3 }}>
                        当前没有已声明的可操作平台入口。
                    </Alert>
                )}
            </Paper>
        </Box>
    )
}

const PlatformHomeRoute = () => {
    const { permissions, isPending } = usePermissions<AccessPermissions>()
    if (isPending) return <PageLoading />
    const role = parsePlatformRole(permissions?.platform_role)
    if (role === null || role === 'member' || !permissions) {
        return <Navigate to="/" replace />
    }
    return <PlatformHome permissions={permissions} />
}

const HomeDashboard = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const {
        access,
        projects,
        errorCode: activeProjectErrorCode,
        isPending: accessPending,
    } = useActiveProjectAccess()
    if (permissionsPending || accessPending) return <PageLoading />
    if (access && parseProjectRole(access.project_role) !== null) {
        return (
            <Box data-testid="project-home">
                <TicketDashboard />
            </Box>
        )
    }
    if (projects.length > 0) {
        return (
            <ProjectSelectionPage
                projects={projects}
                accessLost={
                    activeProjectErrorCode === 'active_project_access_lost'
                }
            />
        )
    }
    const platformRole = parsePlatformRole(permissions?.platform_role)
    if (permissions && platformRole !== null && platformRole !== 'member') {
        return <Navigate to="/platform/home" replace />
    }
    return <NoAuthorizedProjects />
}

const customNavigationComponents: Record<
    CustomNavigationComponent,
    React.ComponentType
> = {
    workbench: CrossProjectWorkbench,
    workbenchDashboard: WorkbenchDashboard,
    automationIndex: () => <Navigate to="/automation-rules" replace />,
    automationSLA: AutomationSLAList,
    automationTemplates: AutomationTemplateList,
    automationQuickReplies: AutomationQuickReplyList,
    agentControl: AgentControlCenter,
    agentCollaboration: AgentCollaborationWorkspace,
    knowledgeManagement: KnowledgeManagementPage,
    webhookSettings: WebhookSettings,
    integrationRuntime: IntegrationRuntime,
    projectBasicSettings: ProjectBasicSettingsPage,
    projectMemberships: ProjectMembershipPage,
    projectIntakeSettings: ProjectIntakeSettingsPage,
    projectQueueSettings: ProjectQueueSettingsPage,
    projectNotificationChannels: ProjectNotificationChannelsPage,
    platformHome: PlatformHomeRoute,
    platformProjects: PlatformProjectGovernancePage,
    platformAudit: PlatformAuditPage,
    emergencyControls: EmergencyControls,
    platformConfig: SystemSettings,
    platformEmail: EmailSettings,
    accountProfile: AccountProfile,
    accountSecurity: AccountSecurity,
    trustedDevices: TrustedDevices,
    loginHistory: LoginHistory,
}

const customNavigationLeaves = navigationRegistry.flatMap((node) =>
    node.kind === 'leaf'
        ? node.route.kind === 'custom' ? [node] : []
        : node.children.filter((child) => child.route.kind === 'custom'),
)

const navigationRouteElement = (
    node: NavigationLeafNode,
    legacy = false,
    component?: CustomNavigationComponent,
) => {
    if (node.route.kind !== 'custom') return null
    const Component = customNavigationComponents[
        component ?? node.route.component
    ]
    return (
        <NavigationContractGuard node={node}>
            {legacy ? <Navigate to={node.path} replace /> : <Component />}
        </NavigationContractGuard>
    )
}

const persistedNavigationGroupIDs = validNavigationGroupIDs(
    navigationRegistry.filter((node) => node.placement === 'sidebar'),
)

const useNavigationTreeState = (
    nodes: ReturnType<typeof visibleNavigationNodes>,
    pathname: string,
) => {
    const session = readHumanSessionBinding()
    const sessionSubject = session?.subject ?? ''
    const sessionID = session?.session_id ?? ''
    const binding = React.useMemo(
        () => sessionSubject && sessionID
            ? { subject: sessionSubject, session_id: sessionID }
            : null,
        [sessionID, sessionSubject],
    )
    const bindingKey = binding ? navigationStateStorageKey(binding) : ''
    const activeGroupID = React.useMemo(
        () => findActiveNavigationGroupID(nodes, pathname),
        [nodes, pathname],
    )
    const loadState = React.useCallback(() => {
        const stored = binding
            ? loadNavigationGroupState(
                localStorage,
                binding,
                persistedNavigationGroupIDs,
            )
            : {}
        return expandActiveNavigationGroup(stored, activeGroupID)
    }, [activeGroupID, binding])
    const [treeState, setTreeState] = React.useState<{
        activeGroupID: string | null
        bindingKey: string
        expanded: NavigationGroupState
        pathname: string
    }>(() => ({
        activeGroupID,
        bindingKey,
        expanded: loadState(),
        pathname,
    }))
    const expanded = React.useMemo(() => {
        if (treeState.bindingKey !== bindingKey) {
            return loadState()
        }
        if (
            treeState.activeGroupID !== activeGroupID ||
            treeState.pathname !== pathname
        ) {
            return expandActiveNavigationGroup(
                treeState.expanded,
                activeGroupID,
            )
        }
        return treeState.expanded
    }, [
        activeGroupID,
        bindingKey,
        loadState,
        pathname,
        treeState,
    ])

    React.useEffect(() => {
        setTreeState((current) => {
            if (
                current.bindingKey === bindingKey &&
                current.activeGroupID === activeGroupID &&
                current.pathname === pathname
            ) {
                return current
            }
            const currentExpanded = current.bindingKey === bindingKey
                ? current.expanded
                : loadState()
            const next = expandActiveNavigationGroup(
                currentExpanded,
                activeGroupID,
            )
            if (
                current.bindingKey === bindingKey &&
                next !== currentExpanded &&
                binding
            ) {
                saveNavigationGroupState(
                    localStorage,
                    binding,
                    next,
                    persistedNavigationGroupIDs,
                )
            }
            return {
                activeGroupID,
                bindingKey,
                expanded: next,
                pathname,
            }
        })
    }, [
        activeGroupID,
        binding,
        bindingKey,
        loadState,
        pathname,
    ])

    const toggleGroup = React.useCallback((groupID: string) => {
        setTreeState((current) => {
            const currentExpanded = current.bindingKey === bindingKey
                ? (
                    current.activeGroupID === activeGroupID &&
                    current.pathname === pathname
                        ? current.expanded
                        : expandActiveNavigationGroup(
                            current.expanded,
                            activeGroupID,
                        )
                )
                : loadState()
            const next = toggleNavigationGroup(
                currentExpanded,
                groupID,
            )
            if (binding) {
                saveNavigationGroupState(
                    localStorage,
                    binding,
                    next,
                    persistedNavigationGroupIDs,
                )
            }
            return {
                activeGroupID,
                bindingKey,
                expanded: next,
                pathname,
            }
        })
    }, [
        activeGroupID,
        binding,
        bindingKey,
        loadState,
        pathname,
    ])

    return { activeGroupID, expanded, pathname, toggleGroup }
}

type NavigationRowLevel = 'primary' | 'secondary'

const oinkNavigationColors = {
    active: '#245f94',
    activeBackground: 'rgba(36, 95, 148, 0.1)',
    branch: '#16222e',
    branchHover: 'rgba(22, 34, 46, 0.06)',
    child: '#586b80',
    rail: 'rgba(22, 34, 46, 0.1)',
} as const

const navigationRowSx = (
    level: NavigationRowLevel,
    currentPage: boolean,
    compact = false,
): SxProps<Theme> => (rowTheme) => {
    const isSecondary = level === 'secondary'
    const rowColor = currentPage
        ? oinkNavigationColors.active
        : isSecondary
            ? oinkNavigationColors.child
            : oinkNavigationColors.branch

    return {
        position: 'relative',
        width: '100%',
        height: 36,
        minHeight: '36px !important',
        mt: isSecondary ? 0 : 0.25,
        px: compact ? 0 : 1,
        py: 0.75,
        pl: compact ? 0 : isSecondary ? 3 : 1,
        borderRadius: '10px',
        overflow: 'hidden',
        justifyContent: compact ? 'center' : 'flex-start',
        color: rowColor,
        backgroundColor: currentPage
            ? oinkNavigationColors.activeBackground
            : 'transparent',
        boxShadow: 'none',
        transition: currentPage
            ? 'none'
            : 'background-color 150ms',
        '&::before': {
            position: 'absolute',
            zIndex: 2,
            top: 6,
            bottom: 6,
            left: 10,
            width: '1px',
            backgroundColor: oinkNavigationColors.active,
            content: currentPage && isSecondary ? '""' : 'none',
        },
        '& .RaMenuItemLink-icon, & > .MuiListItemIcon-root': {
            minWidth: compact ? 0 : 24,
            color: 'inherit',
            justifyContent: 'center',
        },
        '& [data-navigation-icon]': {
            width: '16px !important',
            height: '16px !important',
        },
        '& [data-navigation-icon] .MuiSvgIcon-root': {
            fontSize: 16,
        },
        '& .MuiTypography-root': {
            color: 'inherit',
            display: compact ? 'none' : undefined,
            fontSize: 14,
            fontWeight: isSecondary ? 400 : 500,
            lineHeight: '23.8px',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
        },
        '& .MuiListItemText-root': {
            minWidth: 0,
        },
        '&.RaMenuItemLink-active': {
            color: rowColor,
            backgroundColor: currentPage
                ? oinkNavigationColors.activeBackground
                : 'transparent',
        },
        '&:hover': {
            color: currentPage
                ? rowColor
                : oinkNavigationColors.branch,
            backgroundColor: currentPage
                ? oinkNavigationColors.activeBackground
                : oinkNavigationColors.branchHover,
            transition: 'none',
        },
        '&.Mui-focusVisible, &:focus-visible': {
            outline: `2px solid ${oinkNavigationColors.active}`,
            outlineOffset: -2,
        },
        [rowTheme.breakpoints.down('md')]: {
            height: 44,
            minHeight: '44px !important',
            py: 1.25,
            '&::before': {
                top: 10,
                bottom: 10,
            },
        },
    }
}

const CustomMenu: React.FC = () => {
    const { pathname } = useLocation()
    const {
        permissions,
        isPending: permissionsPending,
    } = usePermissions<AccessPermissions>()
    const [sidebarOpen, setSidebarOpen] = useSidebarState()
    const {
        access,
        projects,
        isPending: projectAccessPending,
    } = useActiveProjectAccess()
    const projectRole = parseProjectRole(access?.project_role)
    const navigationPending =
        permissionsPending || projectAccessPending
    const hasProject = !navigationPending && projectRole !== null
    const platformRole = parsePlatformRole(permissions?.platform_role)
    const navigationPathname =
        !navigationPending &&
        pathname === '/' &&
        !hasProject &&
        projects.length === 0 &&
        platformRole !== null &&
        platformRole !== 'member'
            ? '/platform/home'
            : pathname
    const nodes = React.useMemo(
        () => navigationPending
            ? []
            : visibleNavigationNodes('sidebar', {
                platformRole,
                projectRole,
                hasProject,
            }),
        [
            hasProject,
            navigationPending,
            platformRole,
            projectRole,
        ],
    )
    const {
        activeGroupID,
        expanded,
        pathname: activePathname,
        toggleGroup,
    } = useNavigationTreeState(nodes, navigationPathname)

    if (navigationPending) {
        return (
            <Menu aria-label="主导航" aria-busy="true">
                <Box
                    component="li"
                    role="none"
                    sx={{
                        display: 'grid',
                        minHeight: 72,
                        placeItems: 'center',
                    }}
                >
                    <CircularProgress
                        role="status"
                        aria-label="正在加载导航"
                        size={20}
                    />
                </Box>
            </Menu>
        )
    }

    return (
        <Menu aria-label="主导航">
            {nodes.map((node, nodeIndex) => {
                const directEntry = directNavigationEntry(node)
                if (directEntry) {
                    const active = isNavigationItemActive(
                        directEntry,
                        activePathname,
                    )
                    return (
                        <Menu.Item
                            key={node.id}
                            to={directEntry.path}
                            primaryText={directEntry.label}
                            leftIcon={
                                <NavigationIconGlyph icon={directEntry.icon} />
                            }
                            data-navigation-id={directEntry.id}
                            data-navigation-level="primary"
                            data-navigation-state={
                                active ? 'active' : 'idle'
                            }
                            aria-current={active ? 'page' : undefined}
                            sx={navigationRowSx(
                                'primary',
                                active,
                                !sidebarOpen,
                            )}
                        />
                    )
                }
                if (node.kind !== 'group') return null
                const contentID = `navigation-group-${node.id}-children`
                const isExpanded = expanded[node.id] === true
                const isActive = activeGroupID === node.id
                const renderedExpanded = sidebarOpen && isExpanded
                const activateGroup = () => {
                    if (!sidebarOpen) {
                        setSidebarOpen(true)
                        if (!isExpanded) toggleGroup(node.id)
                        return
                    }
                    toggleGroup(node.id)
                }
                return (
                    <Box component="li" key={node.id} role="none">
                        <Tooltip
                            title={sidebarOpen ? '' : node.label}
                            placement="right"
                        >
                            <ListItemButton
                                component="button"
                                type="button"
                                role="menuitem"
                                aria-label={node.label}
                                aria-expanded={renderedExpanded}
                                aria-controls={
                                    sidebarOpen ? contentID : undefined
                                }
                                data-navigation-id={node.id}
                                data-navigation-level="primary"
                                data-navigation-state={
                                    isActive ? 'active' : 'idle'
                                }
                                data-testid={
                                    `navigation-group-${nodeIndex}-toggle`
                                }
                                onClick={activateGroup}
                                onKeyDown={(event) => {
                                    if (!isNavigationToggleKey(event.key)) {
                                        return
                                    }
                                    event.preventDefault()
                                    activateGroup()
                                }}
                                sx={navigationRowSx(
                                    'primary',
                                    false,
                                    !sidebarOpen,
                                )}
                            >
                                <ListItemIcon>
                                    <NavigationIconGlyph icon={node.icon} />
                                </ListItemIcon>
                                {sidebarOpen ? (
                                    <>
                                        <ListItemText
                                            primary={node.label}
                                            slotProps={{
                                                primary: {
                                                    variant: 'body2',
                                                    sx: {
                                                        fontWeight: 'inherit',
                                                    },
                                                },
                                            }}
                                        />
                                        <ExpandMore
                                            aria-hidden="true"
                                            sx={{
                                                width: 24,
                                                height: 24,
                                                p: 0.5,
                                                color:
                                                    oinkNavigationColors.child,
                                                transform: isExpanded
                                                    ? 'rotate(0deg)'
                                                    : 'rotate(-90deg)',
                                                transition: 'transform 150ms',
                                            }}
                                        />
                                    </>
                                ) : null}
                            </ListItemButton>
                        </Tooltip>
                        {sidebarOpen ? (
                            <Collapse
                                in={isExpanded}
                                timeout={150}
                                unmountOnExit
                                id={contentID}
                            >
                                <List
                                    component="div"
                                    role="group"
                                    aria-label={`${node.label}导航`}
                                    disablePadding
                                    sx={{
                                        position: 'relative',
                                        '&::before': {
                                            position: 'absolute',
                                            top: 4,
                                            bottom: 4,
                                            left: 10,
                                            width: '1px',
                                            backgroundColor:
                                                oinkNavigationColors.rail,
                                            content: '""',
                                            pointerEvents: 'none',
                                        },
                                    }}
                                >
                                    {node.children.map((item) => {
                                        const active =
                                            isNavigationItemActive(
                                                item,
                                                activePathname,
                                            )
                                        return (
                                            <Menu.Item
                                                key={item.id}
                                                to={item.path}
                                                primaryText={item.label}
                                                leftIcon={
                                                    <NavigationIconGlyph
                                                        icon={item.icon}
                                                    />
                                                }
                                                data-navigation-id={item.id}
                                                data-navigation-level={
                                                    'secondary'
                                                }
                                                data-navigation-state={
                                                    active
                                                        ? 'active'
                                                        : 'idle'
                                                }
                                                aria-current={
                                                    active
                                                        ? 'page'
                                                        : undefined
                                                }
                                                sx={navigationRowSx(
                                                    'secondary',
                                                    active,
                                                )}
                                            />
                                        )
                                    })}
                                </List>
                            </Collapse>
                        ) : null}
                    </Box>
                )
            })}
        </Menu>
    )
}

const AppRuntimeCoordinator = () => {
    const queryClient = useQueryClient()
    const navigate = useNavigate()
    const handlingSessionInvalidation = React.useRef(false)

    React.useEffect(() => {
        const clearRuntimeCaches = () => {
            void queryClient.cancelQueries()
            queryClient.clear()
        }
        const handleProjectScopeChanged = () => {
            clearRuntimeCaches()
        }
        const handleProjectInventoryChanged = () => {
            void queryClient.refetchQueries({
                queryKey: ['auth', 'getPermissions'],
                type: 'active',
            })
        }
        const handleProjectAccessInvalidated = () => {
            if (
                handlingSessionInvalidation.current ||
                !readHumanAccessToken()
            ) {
                return
            }
            clearActiveProjectSelection()
            clearRuntimeCaches()
            navigate('/', { replace: true })
        }
        const handleSessionInvalidated = () => {
            if (handlingSessionInvalidation.current) return
            handlingSessionInvalidation.current = true
            clearRuntimeCaches()
            void Promise.resolve(authProvider.logout({}))
                .catch(() => undefined)
                .finally(() => {
                    clearRuntimeCaches()
                    navigate('/login', { replace: true })
                    handlingSessionInvalidation.current = false
                })
        }
        const handleSessionReplaced = () => {
            if (handlingSessionInvalidation.current) return
            handlingSessionInvalidation.current = true
            clearActiveProjectSelection()
            clearRuntimeCaches()
            if (readHumanAccessToken()) {
                window.location.reload()
                return
            }
            navigate('/login', { replace: true })
            handlingSessionInvalidation.current = false
        }
        const unsubscribeHumanSessionMetadata =
            subscribeHumanSessionMetadata((metadata) => {
                if (metadata.type === 'signed_out') {
                    if (handlingSessionInvalidation.current) return
                    handlingSessionInvalidation.current = true
                    applyRemoteHumanSignOut()
                    clearActiveProjectSelection()
                    clearRuntimeCaches()
                    navigate('/login', { replace: true })
                    handlingSessionInvalidation.current = false
                    return
                }
                const binding = readHumanSessionBinding()
                if (
                    binding !== null &&
                    (
                        binding.subject !== metadata.subject ||
                        binding.session_id !== metadata.session_id
                    )
                ) {
                    signalSessionReplaced()
                }
            })

        window.addEventListener(
            projectInventoryChangedEvent,
            handleProjectInventoryChanged,
        )
        window.addEventListener(
            projectScopeChangedEvent,
            handleProjectScopeChanged,
        )
        window.addEventListener(
            projectAccessInvalidatedEvent,
            handleProjectAccessInvalidated,
        )
        window.addEventListener(
            sessionInvalidatedEvent,
            handleSessionInvalidated,
        )
        window.addEventListener(
            sessionReplacedEvent,
            handleSessionReplaced,
        )
        return () => {
            unsubscribeHumanSessionMetadata()
            window.removeEventListener(
                projectInventoryChangedEvent,
                handleProjectInventoryChanged,
            )
            window.removeEventListener(
                projectScopeChangedEvent,
                handleProjectScopeChanged,
            )
            window.removeEventListener(
                projectAccessInvalidatedEvent,
                handleProjectAccessInvalidated,
            )
            window.removeEventListener(
                sessionInvalidatedEvent,
                handleSessionInvalidated,
            )
            window.removeEventListener(
                sessionReplacedEvent,
                handleSessionReplaced,
            )
        }
    }, [navigate, queryClient])

    return null
}

const LayoutWithMenu: React.FC<LayoutProps> = (props) => (
    <>
        <AppRuntimeCoordinator />
        <Layout {...props} menu={CustomMenu} appBar={CustomAppBar} />
    </>
)

const AdminApp: React.FC = () => (
    <Admin
        dataProvider={dataProvider}
        authProvider={authProvider}
        i18nProvider={i18nProvider}
        dashboard={HomeDashboard}
        theme={theme}
        title="ChronoDesk 工单自动化平台"
        layout={LayoutWithMenu}
        loginPage={LoginPage}
        notification={AppNotification}
        requireAuth
    >
        <CustomRoutes noLayout>
            <Route path="/register" element={<RegisterPage />} />
            <Route
                path="/forgot-password"
                element={<ForgotPasswordPage />}
            />
            <Route
                path="/reset-password"
                element={<ResetPasswordPage />}
            />
            <Route path="/verify-email" element={<VerifyEmailPage />} />
            <Route
                path="/resend-verification"
                element={<ResendVerificationPage />}
            />
        </CustomRoutes>
        <Resource
            name="tickets"
            list={ProjectTicketList}
            show={ProjectTicketShow}
            edit={ProjectTicketEdit}
            create={ProjectTicketCreate}
            icon={TicketIcon}
            recordRepresentation="title"
            options={{ label: '工单管理' }}
        />
        <Resource
            name="users"
            list={PlatformUserList}
            show={PlatformUserShow}
            edit={PlatformUserEdit}
            create={PlatformUserCreate}
            icon={UsersIcon}
            recordRepresentation={(record) =>
                `${record.first_name || ''} ${record.last_name || ''}`.trim() ||
                record.username
            }
            options={{ label: '平台用户管理' }}
        />
        <Resource
            name="notifications"
            list={ProjectNotificationList}
            create={ProjectNotificationCreate}
            icon={NotificationIcon}
            options={{ label: '通知中心' }}
        />
        <Resource
            name="automation-rules"
            list={ProjectAutomationRuleList}
            show={ProjectAutomationRuleShow}
            edit={ProjectAutomationRuleEdit}
            create={ProjectAutomationRuleCreate}
            icon={AutomationIcon}
            options={{ label: '自动化规则' }}
        />
        <Resource
            name="automation-logs"
            list={ProjectAutomationLogList}
            icon={HistoryIcon}
            options={{ label: '自动化日志' }}
        />

        <CustomRoutes>
            {customNavigationLeaves.flatMap((node) => {
                if (node.route.kind !== 'custom') return []
                return [
                    <Route
                        key={node.path}
                        path={node.path}
                        element={navigationRouteElement(node)}
                    />,
                    ...(node.route.legacyPaths ?? []).map((legacyPath) => (
                        <Route
                            key={legacyPath}
                            path={legacyPath}
                            element={navigationRouteElement(node, true)}
                        />
                    )),
                    ...(node.route.subroutes ?? []).map((subroute) => (
                        <Route
                            key={subroute.path}
                            path={subroute.path}
                            element={navigationRouteElement(
                                node,
                                false,
                                subroute.component,
                            )}
                        />
                    )),
                ]
            })}
        </CustomRoutes>
    </Admin>
)

export default AdminApp
