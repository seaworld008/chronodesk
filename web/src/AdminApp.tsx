import React from 'react'
import {
    Admin,
    CustomRoutes,
    Menu,
    Resource,
    Title,
    type LayoutProps,
    usePermissions,
} from 'react-admin'
import { Navigate, Route, useNavigate } from 'react-router-dom'
import {
    Alert,
    Box,
    Button,
    CircularProgress,
    Paper,
    Stack,
    Typography,
} from '@mui/material'
import { createTheme } from '@mui/material/styles'
import { useQueryClient } from '@tanstack/react-query'

import { dataProvider } from './lib/dataProvider'
import { authProvider } from './lib/authProvider'
import {
    getPlatformRoleLabel,
    hasPlatformCapability,
    parsePlatformRole,
    type AccessPermissions,
    type PlatformCapability,
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
    setActiveProjectKey,
    type AuthorizedProject,
    type ProjectCapability,
    type ProjectRole,
} from './lib/projectScope'
import {
    projectScopeChangedEvent,
    sessionInvalidatedEvent,
} from './lib/projectScopeEvents'

import {
    AdminPanelSettings as AdminIcon,
    AutoFixHigh as AutomationIcon,
    ConfirmationNumber as TicketIcon,
    DashboardCustomize as WorkbenchIcon,
    FactCheck as AuditIcon,
    GroupAdd as MembershipIcon,
    History as HistoryIcon,
    Notifications as NotificationIcon,
    People as UsersIcon,
    AccountTree as PlatformProjectsIcon,
    Security as SecurityIcon,
    SmartToy as AgentIcon,
    Webhook as WebhookIcon,
} from '@mui/icons-material'

import { CustomLayout as Layout } from './layout/CustomLayout'
import { CustomAppBar } from './layout/CustomAppBar'
import LoginPage from './components/auth/LoginPage'
import { AppNotification } from './components/layout/AppNotification'
import { i18nProvider, muiZhCN } from './i18n'

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
const SimpleWorkingSystemSettings = lazyPage(
    () => import('./admin/settings/SimpleWorkingSystemSettings'),
)
const EmailSettings = lazyPage(() => import('./admin/settings/EmailSettings'))
const WebhookSettings = lazyPage(() => import('./admin/settings/WebhookSettings'))
const SystemSettings = lazyPage(() => import('./admin/settings/SystemSettings'))
const TrustedDevices = lazyPage(() => import('./admin/security/TrustedDevices'))
const AgentControlCenter = lazyPage(
    () => import('./admin/agents/AgentControlCenter'),
)
const CrossProjectWorkbench = lazyPage(
    () => import('./admin/workbench/CrossProjectWorkbench'),
)
const ProjectMembershipPage = lazyPage(
    () => import('./admin/projects/ProjectMembershipPage'),
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
                main: '#3b82f6',
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

    React.useEffect(() => {
        let active = true
        const loadAccess = async () => {
            setState((current) => ({ ...current, isPending: true }))
            try {
                const projects = await loadAuthorizedProjects()
                try {
                    const access = await resolveActiveProjectAccess()
                    if (active) {
                        setState({
                            access,
                            projects,
                            errorCode: null,
                            isPending: false,
                        })
                    }
                } catch (requestError) {
                    if (active) {
                        setState({
                            access: null,
                            projects,
                            errorCode: errorCode(requestError),
                            isPending: false,
                        })
                    }
                }
            } catch (requestError) {
                if (active) {
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
        window.addEventListener(projectScopeChangedEvent, reloadAccess)
        return () => {
            active = false
            window.removeEventListener(
                projectAccessInvalidatedEvent,
                reloadAccess,
            )
            window.removeEventListener(projectScopeChangedEvent, reloadAccess)
        }
    }, [])

    return state
}

export const PlatformCapabilityRoute = ({
    capability,
    children,
}: React.PropsWithChildren<{ capability: PlatformCapability }>) => {
    const { permissions, isPending } = usePermissions<AccessPermissions>()
    if (isPending) return <PageLoading />
    if (
        !hasPlatformCapability(permissions?.platform_role, capability)
    ) {
        return <Navigate to="/" replace />
    }
    return <>{children}</>
}

const PlatformAdminRoute = ({ children }: React.PropsWithChildren) => {
    const { permissions, isPending } = usePermissions<AccessPermissions>()
    if (isPending) return <PageLoading />
    if (parsePlatformRole(permissions?.platform_role) !== 'platform_admin') {
        return <Navigate to="/" replace />
    }
    return <>{children}</>
}

export const ProjectRequiredRoute = ({
    children,
}: React.PropsWithChildren) => {
    const { access, isPending } = useActiveProjectAccess()
    if (isPending) return <PageLoading />
    if (!access || parseProjectRole(access.project_role) === null) {
        return <Navigate to="/" replace />
    }
    return <>{children}</>
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

const withPlatformCapability = <P extends object>(
    Component: React.ComponentType<P>,
    capability: PlatformCapability,
) => {
    const GuardedView = (props: P) => (
        <PlatformCapabilityRoute capability={capability}>
            <Component {...props} />
        </PlatformCapabilityRoute>
    )
    GuardedView.displayName = `PlatformCapability${
        Component.displayName || Component.name || 'View'
    }`
    return GuardedView
}

const withProjectRequired = <P extends object>(
    Component: React.ComponentType<P>,
) => {
    const GuardedView = (props: P) => (
        <ProjectRequiredRoute>
            <Component {...props} />
        </ProjectRequiredRoute>
    )
    GuardedView.displayName = `ProjectRequired${
        Component.displayName || Component.name || 'View'
    }`
    return GuardedView
}

const withProjectCapability = <P extends object>(
    Component: React.ComponentType<P>,
    capability: ProjectCapability,
    roles: readonly ProjectRole[] = projectRoleValues,
) => {
    const GuardedView = (props: P) => (
        <ExactProjectRoleRoute roles={roles} capability={capability}>
            <Component {...props} />
        </ExactProjectRoleRoute>
    )
    GuardedView.displayName = `ProjectCapability${
        Component.displayName || Component.name || 'View'
    }`
    return GuardedView
}

const PlatformUserList = withPlatformCapability(
    UserList,
    'manage_platform_users',
)
const PlatformUserShow = withPlatformCapability(
    UserShow,
    'manage_platform_users',
)
const PlatformUserEdit = withPlatformCapability(
    UserEdit,
    'manage_platform_users',
)
const PlatformUserCreate = withPlatformCapability(
    UserCreate,
    'manage_platform_users',
)
const ProjectTicketList = withProjectRequired(TicketList)
const ProjectTicketShow = withProjectRequired(TicketShow)
const ProjectTicketEdit = withProjectCapability(
    TicketEdit,
    'edit_ticket_safe_fields',
)
const ProjectTicketCreate = withProjectCapability(TicketCreate, 'create_ticket')
const ProjectNotificationList = withProjectRequired(NotificationList)
const ProjectNotificationCreate = withProjectCapability(
    NotificationCreate,
    'manage_notifications',
    ['project_admin', 'manager'],
)
const ProjectAutomationRuleList = withProjectCapability(
    AutomationRuleList,
    'manage_automation',
)
const ProjectAutomationRuleShow = withProjectCapability(
    AutomationRuleShow,
    'manage_automation',
)
const ProjectAutomationRuleEdit = withProjectCapability(
    AutomationRuleEdit,
    'manage_automation',
)
const ProjectAutomationRuleCreate = withProjectCapability(
    AutomationRuleCreate,
    'manage_automation',
)
const ProjectAutomationLogList = withProjectCapability(
    AutomationLogList,
    'manage_automation',
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
            visible: hasPlatformCapability(
                platformRole,
                'manage_platform_settings',
            ),
            label: '系统设置',
            path: '/system-settings',
        },
        {
            visible: hasPlatformCapability(
                platformRole,
                'view_platform_audit',
            ),
            label: '平台审计',
            path: '/platform/audit',
        },
    ].filter(({ visible }) => visible)

    return (
        <Box data-testid="platform-home" sx={{ p: 4 }}>
            <Title title="平台治理中心" />
            <Paper sx={{ p: 4 }}>
                <Typography variant="h4" gutterBottom>
                    平台治理中心
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
        return <PlatformHome permissions={permissions} />
    }
    return <NoAuthorizedProjects />
}

const CustomMenu: React.FC = () => {
    const { permissions } = usePermissions<AccessPermissions>()
    const { access, isPending } = useActiveProjectAccess()
    const projectRole = parseProjectRole(access?.project_role)
    const hasProject = !isPending && projectRole !== null

    return (
        <Menu aria-label="主导航">
            <Menu.DashboardItem
                primaryText={hasProject ? '项目仪表盘' : '首页'}
            />
            {hasProject && (
                <>
                    <Menu.Item
                        to="/workbench"
                        primaryText="我的跨项目工作台"
                        leftIcon={<WorkbenchIcon />}
                    />
                    <Menu.Item
                        to="/tickets"
                        primaryText="工单管理"
                        leftIcon={<TicketIcon />}
                    />
                    <Menu.Item
                        to="/notifications"
                        primaryText="通知中心"
                        leftIcon={<NotificationIcon />}
                    />
                </>
            )}
            {hasProjectCapability(projectRole, 'manage_automation') && (
                <>
                    <Menu.Item
                        to="/automation-rules"
                        primaryText="自动化规则"
                        leftIcon={<AutomationIcon />}
                    />
                    <Menu.Item
                        to="/automation-logs"
                        primaryText="自动化日志"
                        leftIcon={<HistoryIcon />}
                    />
                </>
            )}
            {hasProjectCapability(projectRole, 'manage_integrations') && (
                <Menu.Item
                    to="/webhook-settings"
                    primaryText="Webhook 集成"
                    leftIcon={<WebhookIcon />}
                />
            )}
            {projectRole === 'project_admin' && (
                <Menu.Item
                    to="/project-memberships"
                    primaryText="项目成员管理"
                    leftIcon={<MembershipIcon />}
                />
            )}
            {projectRole === 'project_admin' && (
                <Menu.Item
                    to="/agent-control"
                    primaryText="AI 智能体控制"
                    leftIcon={<AgentIcon />}
                />
            )}
            {hasPlatformCapability(
                permissions?.platform_role,
                'manage_platform_users',
            ) && (
                <Menu.Item
                    to="/users"
                    primaryText="平台用户管理"
                    leftIcon={<UsersIcon />}
                />
            )}
            {parsePlatformRole(permissions?.platform_role) ===
                'platform_admin' && (
                <Menu.Item
                    to="/platform/projects"
                    primaryText="平台项目治理"
                    leftIcon={<PlatformProjectsIcon />}
                />
            )}
            {hasPlatformCapability(
                permissions?.platform_role,
                'manage_platform_settings',
            ) && (
                <Menu.Item
                    to="/system-settings"
                    primaryText="系统设置"
                    leftIcon={<AdminIcon />}
                />
            )}
            {hasPlatformCapability(
                permissions?.platform_role,
                'view_platform_audit',
            ) && (
                <Menu.Item
                    to="/platform/audit"
                    primaryText="平台审计"
                    leftIcon={<AuditIcon />}
                />
            )}
            <Menu.Item
                to="/account/trusted-devices"
                primaryText="账号安全"
                leftIcon={<SecurityIcon />}
            />
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
        const handleProjectAccessInvalidated = () => {
            if (
                handlingSessionInvalidation.current ||
                !localStorage.getItem('token')
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
        return () => {
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
            <Route
                path="/workbench"
                element={
                    <ProjectRequiredRoute>
                        <CrossProjectWorkbench />
                    </ProjectRequiredRoute>
                }
            />
            <Route
                path="/system-settings"
                element={
                    <PlatformCapabilityRoute capability="manage_platform_settings">
                        <SimpleWorkingSystemSettings />
                    </PlatformCapabilityRoute>
                }
            />
            <Route
                path="/email-settings"
                element={
                    <PlatformCapabilityRoute capability="manage_email_settings">
                        <EmailSettings />
                    </PlatformCapabilityRoute>
                }
            />
            <Route
                path="/system-settings/overview"
                element={
                    <PlatformCapabilityRoute capability="manage_platform_settings">
                        <SystemSettings />
                    </PlatformCapabilityRoute>
                }
            />
            <Route
                path="/platform/audit"
                element={
                    <PlatformCapabilityRoute capability="view_platform_audit">
                        <PlatformAuditPage />
                    </PlatformCapabilityRoute>
                }
            />
            <Route
                path="/platform/projects"
                element={
                    <PlatformAdminRoute>
                        <PlatformProjectGovernancePage />
                    </PlatformAdminRoute>
                }
            />
            <Route
                path="/webhook-settings"
                element={
                    <ExactProjectRoleRoute
                        roles={projectRoleValues}
                        capability="manage_integrations"
                    >
                        <WebhookSettings />
                    </ExactProjectRoleRoute>
                }
            />
            <Route
                path="/project-memberships"
                element={
                    <ExactProjectRoleRoute
                        roles={['project_admin']}
                        capability="manage_memberships"
                    >
                        <ProjectMembershipPage />
                    </ExactProjectRoleRoute>
                }
            />
            <Route
                path="/agent-control"
                element={
                    <ExactProjectRoleRoute
                        roles={['project_admin']}
                        capability="manage_agents"
                    >
                        <AgentControlCenter />
                    </ExactProjectRoleRoute>
                }
            />
            <Route
                path="/account/trusted-devices"
                element={<TrustedDevices />}
            />
        </CustomRoutes>
    </Admin>
)

export default AdminApp
