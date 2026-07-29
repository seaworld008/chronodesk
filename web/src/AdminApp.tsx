import React from 'react';
import { Admin, Resource, CustomRoutes, Menu, LayoutProps, usePermissions } from 'react-admin';
import { Navigate, Route } from 'react-router-dom';
import { Box, CircularProgress } from '@mui/material';
import { createTheme } from '@mui/material/styles';

// Data and Auth providers
import { dataProvider } from './lib/dataProvider';
import { authProvider } from './lib/authProvider';
import {
    isAdministrativeRole,
    type RolePermissions,
} from './lib/accessControl';

// Icons
import {
    ConfirmationNumber as TicketIcon,
    People as UsersIcon,
    Notifications as NotificationIcon,
    AdminPanelSettings as AdminIcon,
    AutoFixHigh as AutomationIcon,
    History as HistoryIcon,
    Security as SecurityIcon,
    SmartToy as AgentIcon,
} from '@mui/icons-material';

import { CustomLayout as Layout } from './layout/CustomLayout';
import { CustomAppBar } from './layout/CustomAppBar';
import LoginPage from './components/auth/LoginPage';
import { AppNotification } from './components/layout/AppNotification';
import { i18nProvider, muiZhCN } from './i18n';

const PageLoading = () => (
    <Box
        role="status"
        aria-label="正在加载页面"
        sx={{ display: 'grid', minHeight: 240, placeItems: 'center' }}
    >
        <CircularProgress size={32} />
    </Box>
);

const lazyPage = <P extends object>(
    loader: () => Promise<{ default: React.ComponentType<P> }>,
) => {
    const LazyComponent = React.lazy(loader);
    const LazyPage = (props: P) => (
        <React.Suspense fallback={<PageLoading />}>
            <LazyComponent {...props} />
        </React.Suspense>
    );
    LazyPage.displayName = 'LazyAdminPage';
    return LazyPage;
};

const TicketDashboard = lazyPage(() => import('./admin/tickets/TicketDashboard'));
const TicketList = lazyPage(() => import('./admin/tickets/TicketListEnhanced'));
const TicketShow = lazyPage(() => import('./admin/tickets/TicketShow'));
const TicketEdit = lazyPage(() => import('./admin/tickets/TicketEdit'));
const TicketCreate = lazyPage(() => import('./admin/tickets/TicketCreate'));
const UserList = lazyPage(() => import('./admin/users/UserList'));
const UserShow = lazyPage(() => import('./admin/users/UserShow'));
const UserEdit = lazyPage(() => import('./admin/users/UserEdit'));
const UserCreate = lazyPage(() => import('./admin/users/UserCreate'));
const NotificationList = lazyPage(() => import('./admin/notifications/NotificationList'));
const AutomationRuleList = lazyPage(() => import('./admin/automation/AutomationRuleList'));
const AutomationRuleShow = lazyPage(() => import('./admin/automation/AutomationRuleShow'));
const AutomationRuleCreate = lazyPage(() => import('./admin/automation/AutomationRuleCreate'));
const AutomationRuleEdit = lazyPage(() => import('./admin/automation/AutomationRuleEdit'));
const AutomationLogList = lazyPage(() => import('./admin/automation/AutomationLogList'));
const SimpleWorkingSystemSettings = lazyPage(
    () => import('./admin/settings/SimpleWorkingSystemSettings'),
);
const EmailSettings = lazyPage(() => import('./admin/settings/EmailSettings'));
const WebhookSettings = lazyPage(() => import('./admin/settings/WebhookSettings'));
const SystemSettings = lazyPage(() => import('./admin/settings/SystemSettings'));
const TrustedDevices = lazyPage(() => import('./admin/security/TrustedDevices'));
const AgentControlCenter = lazyPage(() => import('./admin/agents/AgentControlCenter'));

/**
 * 自定义MUI主题
 */
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
        shape: {
            borderRadius: 12,
        },
    },
    muiZhCN,
);

const AdministrativeRoute = ({ children }: React.PropsWithChildren) => {
    const { permissions, isPending } = usePermissions<RolePermissions>();

    if (isPending) {
        return null;
    }

    if (!isAdministrativeRole(permissions?.role)) {
        return <Navigate to="/" replace />;
    }

    return <>{children}</>;
};

const withAdministrativeAccess = <P extends object,>(Component: React.ComponentType<P>) => {
    const GuardedAdministrativeView = (props: P) => (
        <AdministrativeRoute>
            <Component {...props} />
        </AdministrativeRoute>
    );
    GuardedAdministrativeView.displayName = `Administrative${
        Component.displayName || Component.name || 'View'
    }`;
    return GuardedAdministrativeView;
};

const AdministrativeUserList = withAdministrativeAccess(UserList);
const AdministrativeUserShow = withAdministrativeAccess(UserShow);
const AdministrativeUserEdit = withAdministrativeAccess(UserEdit);
const AdministrativeUserCreate = withAdministrativeAccess(UserCreate);
const AdministrativeAutomationRuleList = withAdministrativeAccess(AutomationRuleList);
const AdministrativeAutomationRuleShow = withAdministrativeAccess(AutomationRuleShow);
const AdministrativeAutomationRuleEdit = withAdministrativeAccess(AutomationRuleEdit);
const AdministrativeAutomationRuleCreate = withAdministrativeAccess(AutomationRuleCreate);
const AdministrativeAutomationLogList = withAdministrativeAccess(AutomationLogList);

/**
 * 自定义菜单组件 - 只展示当前角色可访问的管理入口
 */
const CustomMenu: React.FC = () => {
    const { permissions } = usePermissions<RolePermissions>();
    const canAdminister = isAdministrativeRole(permissions?.role);

    return (
        <Menu aria-label="主导航">
            <Menu.DashboardItem primaryText="仪表盘" />
            <Menu.Item to="/tickets" primaryText="工单管理" leftIcon={<TicketIcon />} />
            <Menu.Item to="/notifications" primaryText="通知中心" leftIcon={<NotificationIcon />} />
            {canAdminister && <Menu.Item to="/users" primaryText="用户管理" leftIcon={<UsersIcon />} />}
            {canAdminister && (
                <Menu.Item to="/automation-rules" primaryText="自动化规则" leftIcon={<AutomationIcon />} />
            )}
            {canAdminister && (
                <Menu.Item to="/automation-logs" primaryText="自动化日志" leftIcon={<HistoryIcon />} />
            )}
            {canAdminister && (
                <Menu.Item
                    to="/system-settings"
                    primaryText="系统设置"
                    leftIcon={<AdminIcon />}
                />
            )}
            {canAdminister && (
                <Menu.Item
                    to="/agent-control"
                    primaryText="AI 智能体控制"
                    leftIcon={<AgentIcon />}
                />
            )}
            <Menu.Item
                to="/account/trusted-devices"
                primaryText="账号安全"
                leftIcon={<SecurityIcon />}
            />
        </Menu>
    );
};

/**
 * 自定义布局组件
 */
const LayoutWithMenu: React.FC<LayoutProps> = (props) => (
    <Layout {...props} menu={CustomMenu} appBar={CustomAppBar} />
);

/**
 * ChronoDesk 工单自动化平台
 */
const AdminApp: React.FC = () => {
    return (
        <Admin
            dataProvider={dataProvider}
            authProvider={authProvider}
            i18nProvider={i18nProvider}
            dashboard={TicketDashboard}
            theme={theme}
            title="ChronoDesk 工单自动化平台"
            layout={LayoutWithMenu}
            loginPage={LoginPage}
            notification={AppNotification}
            requireAuth
        >
            {/* 工单管理资源 */}
            <Resource
                name="tickets"
                list={TicketList}
                show={TicketShow}
                edit={TicketEdit}
                create={TicketCreate}
                icon={TicketIcon}
                recordRepresentation="title"
                options={{
                    label: '工单管理',
                }}
            />

            {/* 用户管理资源 */}
            <Resource
                name="users"
                list={AdministrativeUserList}
                show={AdministrativeUserShow}
                edit={AdministrativeUserEdit}
                create={AdministrativeUserCreate}
                icon={UsersIcon}
                recordRepresentation={(record) => 
                    `${record.first_name || ''} ${record.last_name || ''}`.trim() || record.username
                }
                options={{
                    label: '用户管理',
                }}
            />

            {/* 通知管理资源 */}
            <Resource
                name="notifications"
                list={NotificationList}
                icon={NotificationIcon}
                options={{
                    label: '通知中心',
                }}
            />

            {/* 自动化规则 */}
            <Resource
                name="automation-rules"
                list={AdministrativeAutomationRuleList}
                show={AdministrativeAutomationRuleShow}
                edit={AdministrativeAutomationRuleEdit}
                create={AdministrativeAutomationRuleCreate}
                icon={AutomationIcon}
                options={{
                    label: '自动化规则',
                }}
            />

            <Resource
                name="automation-logs"
                list={AdministrativeAutomationLogList}
                icon={HistoryIcon}
                options={{
                    label: '自动化日志',
                }}
            />


            {/* 自定义路由 */}
            <CustomRoutes>
                {/* 系统设置主页面 */}
                <Route
                    path="/system-settings"
                    element={(
                        <AdministrativeRoute>
                            <SimpleWorkingSystemSettings />
                        </AdministrativeRoute>
                    )}
                />

                {/* 邮件设置 */}
                <Route
                    path="/email-settings"
                    element={(
                        <AdministrativeRoute>
                            <EmailSettings />
                        </AdministrativeRoute>
                    )}
                />

                {/* Webhook设置 */}
                <Route
                    path="/webhook-settings"
                    element={(
                        <AdministrativeRoute>
                            <WebhookSettings />
                        </AdministrativeRoute>
                    )}
                />
                <Route
                    path="/system-settings/overview"
                    element={(
                        <AdministrativeRoute>
                            <SystemSettings />
                        </AdministrativeRoute>
                    )}
                />
                <Route path="/account/trusted-devices" element={<TrustedDevices />} />
                <Route
                    path="/agent-control"
                    element={(
                        <AdministrativeRoute>
                            <AgentControlCenter />
                        </AdministrativeRoute>
                    )}
                />
            </CustomRoutes>
        </Admin>
    );
};

export default AdminApp;
