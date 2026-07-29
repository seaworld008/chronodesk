import React from 'react';
import {
    List,
    DateField,
    BooleanField,
    FunctionField,
    EditButton,
    ShowButton,
    CreateButton,
    ExportButton,
    FilterButton,
    TopToolbar,
    useRecordContext,
    WrapperField,
} from 'react-admin';
import {
    Chip,
    Box,
    Typography,
    Stack,
    Card,
    CardContent,
    Avatar,
} from '@mui/material';
import {
    Person as PersonIcon,
    AdminPanelSettings as AdminIcon,
    Support as SupportIcon,
    Business as CustomerIcon,
    SupervisorAccount as SupervisorIcon,
    CheckCircle as ActiveIcon,
    Block as InactiveIcon,
    Pause as SuspendedIcon,
    Delete as DeletedIcon,
} from '@mui/icons-material';
import {
    EnterpriseDatagrid,
    InlineDetails,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable';
import { EnterpriseSearchInput } from '@/components/inputs/EnterpriseSearchInput';
import { EnterpriseSelectFilterInput } from '@/components/inputs/EnterpriseFilterInputs';
import {
    getUserRoleLabel,
    normalizeUserRole,
    userRoleChoices,
} from '@/lib/accessControl';
import { FocusSafeBulkDeleteWithConfirmButton } from '@/components/actions/FocusSafeDeleteButtons';

// 用户状态选项
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
    { id: 'deleted', name: '删除' },
];

const userColumns: ResizableColumn[] = [
    { key: 'username', defaultWidth: 260, minWidth: 180, maxWidth: 440 },
    { key: 'role', defaultWidth: 128, minWidth: 104, maxWidth: 200 },
    { key: 'status', defaultWidth: 128, minWidth: 104, maxWidth: 200 },
    { key: 'column-4', defaultWidth: 260, minWidth: 180, maxWidth: 440 },
    { key: 'timezone', defaultWidth: 160, minWidth: 120, maxWidth: 260 },
    { key: 'language', defaultWidth: 104, minWidth: 80, maxWidth: 160 },
    { key: 'last_login_at', defaultWidth: 152, minWidth: 120, maxWidth: 240 },
    { key: 'created_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
    { key: 'email_verified', defaultWidth: 144, minWidth: 120, maxWidth: 200 },
    { key: 'column-10', defaultWidth: 160, minWidth: 144, maxWidth: 220, sticky: 'right' },
];

/**
 * 用户头像组件
 */
const UserAvatar: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getInitials = (firstName: string, lastName: string, username: string) => {
        if (firstName && lastName) {
            return `${firstName[0]}${lastName[0]}`.toUpperCase();
        }
        if (username) {
            return username.substring(0, 2).toUpperCase();
        }
        return 'U';
    };

    const initials = getInitials(record.first_name, record.last_name, record.username);
    const displayName =
        record.display_name ||
        `${record.first_name || ''} ${record.last_name || ''}`.trim() ||
        record.username;

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
            <Avatar
                src={record.avatar}
                sx={{
                    width: 40,
                    height: 40,
                    bgcolor: 'primary.main',
                    fontSize: '0.875rem',
                }}
            >
                {initials}
            </Avatar>
            <InlineDetails
                primary={displayName}
                secondary={`@${record.username}`}
                title={`${displayName} · @${record.username}`}
            />
        </Box>
    );
};

/**
 * 角色标签组件
 */
const RoleChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getRoleConfig = (role: string) => {
        const label = getUserRoleLabel(role);

        switch (normalizeUserRole(role)) {
            case 'admin':
                return {
                    label,
                    color: '#b91c1c',
                    backgroundColor: '#fee2e2',
                    border: '1px solid #fecaca',
                    icon: <AdminIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'agent':
                return {
                    label,
                    color: '#1d4ed8',
                    backgroundColor: '#dbeafe',
                    border: '1px solid #bfdbfe',
                    icon: <SupportIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'supervisor':
                return {
                    label,
                    color: '#7e22ce',
                    backgroundColor: '#f3e8ff',
                    border: '1px solid #d8b4fe',
                    icon: <SupervisorIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'customer':
                return {
                    label,
                    color: '#15803d',
                    backgroundColor: '#dcfce7',
                    border: '1px solid #bbf7d0',
                    icon: <CustomerIcon sx={{ fontSize: '0.8rem' }} />
                };
            default:
                return {
                    label: '未知',
                    color: '#374151',
                    backgroundColor: '#f3f4f6',
                    border: '1px solid #e5e7eb',
                    icon: <PersonIcon sx={{ fontSize: '0.8rem' }} />
                };
        }
    };

    const { label, color, backgroundColor, border, icon } = getRoleConfig(record.role);

    return (
        <Chip
            size="small"
            label={label}
            icon={icon}
            sx={{
                color,
                backgroundColor,
                border,
                fontWeight: 600,
                fontSize: '0.75rem',
                height: 24,
                '& .MuiChip-icon': { color },
            }}
        />
    );
};

/**
 * 状态标签组件
 */
const StatusChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getStatusConfig = (status: string) => {
        switch (status) {
            case 'active':
                return {
                    label: '激活',
                    color: '#059669',
                    backgroundColor: '#f0fdf4',
                    icon: <ActiveIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'inactive':
                return {
                    label: '未激活',
                    color: '#d97706',
                    backgroundColor: '#fefce8',
                    icon: <InactiveIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'suspended':
                return {
                    label: '暂停',
                    color: '#dc2626',
                    backgroundColor: '#fef2f2',
                    icon: <SuspendedIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'deleted':
                return {
                    label: '删除',
                    color: '#64748b',
                    backgroundColor: '#f8fafc',
                    icon: <DeletedIcon sx={{ fontSize: '0.8rem' }} />
                };
            default:
                return {
                    label: '未知',
                    color: '#64748b',
                    backgroundColor: '#f8fafc',
                    icon: null
                };
        }
    };

    const { label, color, backgroundColor, icon } = getStatusConfig(record.status);

    return (
        <Chip
            size="small"
            label={label}
            {...(icon && { icon })}
            sx={{
                color,
                backgroundColor,
                fontWeight: 500,
                fontSize: '0.75rem',
                '& .MuiChip-icon': { color },
            }}
        />
    );
};

/**
 * 联系信息组件
 */
const ContactInfo: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const summary = record.phone ? `${record.email} · ${record.phone}` : record.email;
    return (
        <InlineDetails
            primary={record.email}
            secondary={record.phone}
            title={summary}
            primaryFontWeight={500}
        />
    );
};

const languageLabels: Record<string, string> = {
    'zh-CN': '简体中文',
    'zh-TW': '繁体中文',
    'en-US': '英语（美国）',
    'en-GB': '英语（英国）',
};

/**
 * 最后登录时间组件
 */
const LastLoginInfo: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    if (!record.last_login_at) {
        return (
            <Typography variant="caption" sx={{
                color: "text.secondary"
            }}>从未登录
                            </Typography>
        );
    }

    const lastLogin = new Date(record.last_login_at);
    const now = new Date();
    const diffInHours = (now.getTime() - lastLogin.getTime()) / (1000 * 3600);

    let displayText = '';
    let color = 'text.secondary';

    if (diffInHours < 24) {
        displayText = `${Math.floor(diffInHours)}小时前`;
        color = 'success.main';
    } else if (diffInHours < 24 * 7) {
        displayText = `${Math.floor(diffInHours / 24)}天前`;
        color = 'text.primary';
    } else {
        displayText = lastLogin.toLocaleDateString('zh-CN');
        color = 'text.secondary';
    }

    return (
        <Typography variant="caption" color={color}>
            {displayText}
        </Typography>
    );
};

/**
 * 过滤器组件
 */
const UserFilters = [
    <EnterpriseSearchInput source="q" placeholder="搜索用户" alwaysOn />,
    <EnterpriseSelectFilterInput source="role" label="角色" choices={userRoleChoices} />,
    <EnterpriseSelectFilterInput source="status" label="状态" choices={statusChoices} />,
];

/**
 * 列表工具栏
 */
const UserListActions = () => (
    <TopToolbar>
        <FilterButton />
        <CreateButton label="创建用户" />
        <ExportButton label="导出" />
    </TopToolbar>
);

/**
 * 空状态组件
 */
const UserEmpty = () => (
    <Box sx={{ textAlign: 'center', mt: 4 }}>
        <Card sx={{ maxWidth: 600, mx: 'auto', p: 4 }}>
            <CardContent>
                <PersonIcon sx={{ fontSize: 64, color: 'text.secondary', mb: 2 }} />
                <Typography variant="h5" component="h2" gutterBottom>
                    还没有用户
                </Typography>
                <Typography
                    variant="body1"
                    sx={{
                        color: "text.secondary",
                        marginBottom: "16px"
                    }}>
                    系统中暂时没有任何用户。创建第一个用户来开始使用用户管理系统。
                </Typography>
                <CreateButton label="创建第一个用户" variant="contained" />
            </CardContent>
        </Card>
    </Box>
);

/**
 * 批量操作按钮
 */
const UserBulkActionButtons = () => (
    <>
        <FocusSafeBulkDeleteWithConfirmButton label="批量删除" mutationMode="pessimistic" />
    </>
);

/**
 * 用户列表组件
 */
const UserList: React.FC = () => {
    return (
        <List
            filters={UserFilters}
            actions={<UserListActions />}
            empty={<UserEmpty />}
            perPage={25}
            sort={{ field: 'created_at', order: 'DESC' }}
            title="用户管理"
        >
            <EnterpriseDatagrid
                tableId="users.main"
                columns={userColumns}
                aria-label="用户列表"
                rowClick="show"
                bulkActionButtons={<UserBulkActionButtons />}
                sx={{
                    '& .RaDatagrid-table': {
                        borderCollapse: 'separate',
                        borderSpacing: '0 8px',
                        backgroundColor: 'transparent',
                        '& .RaDatagrid-headerCell': {
                            backgroundColor: 'transparent',
                            color: 'text.secondary',
                            fontWeight: 600,
                            borderBottom: 'none',
                        },
                        '& .RaDatagrid-tbody': {
                            '& .RaDatagrid-row': {
                                backgroundColor: '#ffffff',
                                borderRadius: 2,
                                boxShadow: '0 2px 4px rgba(0,0,0,0.02)',
                                transition: 'transform 0.2s, box-shadow 0.2s',
                                '&:hover': {
                                    transform: 'translateY(-2px)',
                                    boxShadow: '0 4px 12px rgba(0,0,0,0.05)',
                                    backgroundColor: '#ffffff',
                                },
                                '& .RaDatagrid-rowCell': {
                                    borderTop: '1px solid #f1f5f9',
                                    borderBottom: '1px solid #f1f5f9',
                                    '&:first-of-type': {
                                        borderLeft: '1px solid #f1f5f9',
                                        borderTopLeftRadius: 8,
                                        borderBottomLeftRadius: 8,
                                    },
                                    '&:last-child': {
                                        borderRight: '1px solid #f1f5f9',
                                        borderTopRightRadius: 8,
                                        borderBottomRightRadius: 8,
                                    },
                                },
                            },
                        },
                    },
                }}
            >
                {/* 用户信息 */}
                <WrapperField label="用户" sortBy="username">
                    <UserAvatar />
                </WrapperField>

                {/* 角色 */}
                <WrapperField label="角色" sortBy="role">
                    <RoleChip />
                </WrapperField>

                {/* 状态 */}
                <WrapperField label="状态" sortBy="status">
                    <StatusChip />
                </WrapperField>

                {/* 联系信息 */}
                <WrapperField label="联系方式">
                    <ContactInfo />
                </WrapperField>

                {/* 时区和语言 */}
                <FunctionField
                    source="timezone"
                    label="时区"
                    render={(record) => (
                        <TruncatedText title={record?.timezone || '—'}>
                            {record?.timezone || '—'}
                        </TruncatedText>
                    )}
                />

                <FunctionField
                    source="language"
                    label="语言"
                    render={(record) => (
                        <TruncatedText title={record?.language ? `语言代码：${record.language}` : '—'}>
                            {languageLabels[record?.language] || record?.language || '—'}
                        </TruncatedText>
                    )}
                />

                {/* 最后登录 */}
                <WrapperField label="最后登录" sortBy="last_login_at">
                    <LastLoginInfo />
                </WrapperField>

                {/* 注册时间 */}
                <DateField
                    source="created_at"
                    label="注册时间"
                    showTime
                    locales="zh-CN"
                    options={{
                        year: 'numeric',
                        month: 'short',
                        day: 'numeric',
                        hour: '2-digit',
                        minute: '2-digit',
                    }}
                />

                {/* 邮箱验证状态 */}
                <BooleanField
                    source="email_verified"
                    label="邮箱已验证"
                    TrueIcon={ActiveIcon}
                    FalseIcon={InactiveIcon}
                />

                {/* 操作按钮 */}
                <WrapperField
                    label="操作"
                    cellClassName="cd-table-sticky-right"
                    headerClassName="cd-table-sticky-right"
                >
                    <Stack className="cd-table-actions" direction="row" spacing={1}>
                        <ShowButton label="查看" />
                        <EditButton label="编辑" />
                    </Stack>
                </WrapperField>
            </EnterpriseDatagrid>
        </List>
    );
};

export default UserList;
