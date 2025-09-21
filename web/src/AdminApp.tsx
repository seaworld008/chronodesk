import React from 'react';
import { Admin, Resource, CustomRoutes, Menu, LayoutProps } from 'react-admin';
import { Route } from 'react-router-dom';
import { createTheme } from '@mui/material/styles';

// Data and Auth providers
import { dataProvider } from './lib/dataProvider';
import { authProvider } from './lib/authProvider';

// Icons
import {
    ConfirmationNumber as TicketIcon,
    People as UsersIcon,
    Notifications as NotificationIcon,
    AdminPanelSettings as AdminIcon,
    AutoFixHigh as AutomationIcon,
    History as HistoryIcon,
    Security as SecurityIcon,
} from '@mui/icons-material';

// Enhanced Dashboard
import { TicketDashboard } from './admin/tickets';

// Ticket Management Components
import { TicketList, TicketShow, TicketEdit, TicketCreate } from './admin/tickets';

// User Management Components
import { UserList, UserShow, UserEdit, UserCreate } from './admin/users';

// Notification Components
import { NotificationList } from './admin/notifications';

// Automation Components
import {
    AutomationRuleList,
    AutomationRuleShow,
    AutomationRuleCreate,
    AutomationRuleEdit,
    AutomationLogList,
} from './admin/automation';

// Admin Components  
// import { AdminUserList, AdminEmailSettings, AdminSystemConfig } from './admin/admin';

// System Settings Components
import { SimpleWorkingSystemSettings, EmailSettings, WebhookSettings, SystemSettings } from './admin/settings';
import { CustomLayout as Layout } from './layout/CustomLayout';
import LoginPage from './components/auth/LoginPage';
import TrustedDevices from './admin/security/TrustedDevices';

/**
 * 自定义MUI主题
 */
const theme = createTheme({
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
});

/**
 * 自定义菜单组件 - 添加系统设置菜单项
 */
const CustomMenu: React.FC = () => (
    <Menu>
        <Menu.DashboardItem />
        <Menu.ResourceItems />
        <Menu.Item
            to="/system-settings"
            primaryText="系统设置"
            leftIcon={<AdminIcon />}
        />
        <Menu.Item
            to="/account/trusted-devices"
            primaryText="账号安全"
            leftIcon={<SecurityIcon />}
        />
    </Menu>
);

/**
 * 自定义布局组件
 */
const LayoutWithMenu: React.FC<LayoutProps> = (props) => (
    <Layout {...props} menu={CustomMenu} />
);

/**
 * 工单管理系统 - React Admin版本
 */
export const AdminApp: React.FC = () => {
    return (
        <Admin
            dataProvider={dataProvider}
            authProvider={authProvider}
            dashboard={TicketDashboard}
            theme={theme}
            title="工单管理系统"
            layout={LayoutWithMenu}
            loginPage={LoginPage}
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
                list={UserList}
                show={UserShow}
                edit={UserEdit}
                create={UserCreate}
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
                list={AutomationRuleList}
                show={AutomationRuleShow}
                edit={AutomationRuleEdit}
                create={AutomationRuleCreate}
                icon={AutomationIcon}
                options={{
                    label: '自动化规则',
                }}
            />

            <Resource
                name="automation-logs"
                list={AutomationLogList}
                icon={HistoryIcon}
                options={{
                    label: '自动化日志',
                }}
            />


            {/* 自定义路由 */}
            <CustomRoutes>
                {/* 系统设置主页面 */}
                <Route path="/system-settings" element={<SimpleWorkingSystemSettings />} />

                {/* 邮件设置 */}
                <Route path="/email-settings" element={<EmailSettings />} />

                {/* Webhook设置 */}
                <Route path="/webhook-settings" element={<WebhookSettings />} />
                <Route path="/system-settings/overview" element={<SystemSettings />} />
                <Route path="/account/trusted-devices" element={<TrustedDevices />} />
            </CustomRoutes>
        </Admin>
    );
};

export default AdminApp;
