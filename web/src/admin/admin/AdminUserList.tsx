import React from 'react';
import {
    List,
    Datagrid,
    TextField,
    EmailField,
    DateField,
    BooleanField,
    NumberField,
    EditButton,
    DeleteButton,
    FilterButton,
    ExportButton,
    SearchInput,
    SelectInput,
    DateInput,
    BooleanInput,
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
    Alert,
    AlertTitle,
} from '@mui/material';
import {
    AdminPanelSettings as AdminIcon,
    Support as SupportIcon,
    Business as CustomerIcon,
    SupervisorAccount as SupervisorIcon,
    CheckCircle as ActiveIcon,
    Block as InactiveIcon,
    Pause as SuspendedIcon,
    Delete as DeletedIcon,
} from '@mui/icons-material';

// 角色选项
const roleChoices = [
    { id: 'admin', name: '管理员' },
    { id: 'agent', name: '客服代理' },
    { id: 'customer', name: '客户' },
    { id: 'supervisor', name: '主管' },
];

// 状态选项
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
    { id: 'deleted', name: '删除' },
];

/**
 * 管理员视角用户头像组件
 */
const AdminUserAvatar: React.FC = () => {
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

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
            <Avatar
                src={record.avatar}
                sx={{
                    width: 32,
                    height: 32,
                    bgcolor: 'primary.main',
                    fontSize: '0.8rem',
                }}
            >
                {initials}
            </Avatar>
            <Box>
                <Typography variant="body2" fontWeight={600}>
                    {record.display_name || `${record.first_name || ''} ${record.last_name || ''}`.trim() || record.username}
                </Typography>
                <Typography variant="caption" color="text.secondary">
                    ID: {record.id}
                </Typography>
            </Box>
        </Box>
    );
};

/**
 * 角色标签组件
 */
const AdminRoleChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getRoleConfig = (role: string) => {
        switch (role) {
            case 'admin':
                return {
                    label: '管理员',
                    color: '#dc2626',
                    backgroundColor: '#fef2f2',
                    icon: <AdminIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'agent':
                return {
                    label: '客服代理',
                    color: '#2563eb',
                    backgroundColor: '#eff6ff',
                    icon: <SupportIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'supervisor':
                return {
                    label: '主管',
                    color: '#7c3aed',
                    backgroundColor: '#f3e8ff',
                    icon: <SupervisorIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'customer':
                return {
                    label: '客户',
                    color: '#059669',
                    backgroundColor: '#f0fdf4',
                    icon: <CustomerIcon sx={{ fontSize: '0.8rem' }} />
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

    const { label, color, backgroundColor, icon } = getRoleConfig(record.role);

    return (
        <Chip
            size="small"
            label={label}
            icon={icon || undefined}
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
 * 状态标签组件
 */
const AdminStatusChip: React.FC = () => {
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
            icon={icon || undefined}
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
 * 安全信息组件
 */
const SecurityInfo: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const alerts = [];
    
    if (!record.email_verified) {
        alerts.push('未验证');
    }
    
    if (record.failed_login_count > 0) {
        alerts.push(`${record.failed_login_count}次失败`);
    }

    if (alerts.length === 0) {
        return (
            <Typography variant="caption" color="success.main">
                正常
            </Typography>
        );
    }

    return (
        <Stack spacing={0.5}>
            {alerts.map((alert, index) => (
                <Chip
                    key={index}
                    size="small"
                    label={alert}
                    color="warning"
                    variant="outlined"
                    sx={{ fontSize: '0.7rem', height: '20px' }}
                />
            ))}
        </Stack>
    );
};

/**
 * 活动状态组件
 */
const ActivityStatus: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    if (!record.last_login_at) {
        return (
            <Typography variant="caption" color="text.secondary">
                从未登录
            </Typography>
        );
    }

    const lastLogin = new Date(record.last_login_at);
    const now = new Date();
    const diffInHours = (now.getTime() - lastLogin.getTime()) / (1000 * 3600);

    if (diffInHours < 24) {
        return (
            <Typography variant="caption" color="success.main">
                {Math.floor(diffInHours)}小时前活跃
            </Typography>
        );
    } else if (diffInHours < 24 * 7) {
        return (
            <Typography variant="caption" color="text.primary">
                {Math.floor(diffInHours / 24)}天前活跃
            </Typography>
        );
    } else {
        return (
            <Typography variant="caption" color="text.secondary">
                {lastLogin.toLocaleDateString('zh-CN')}
            </Typography>
        );
    }
};

/**
 * 过滤器组件
 */
const AdminUserFilters = [
    <SearchInput source="q" placeholder="搜索用户" alwaysOn />,
    <SelectInput source="role" label="角色" choices={roleChoices} />,
    <SelectInput source="status" label="状态" choices={statusChoices} />,
    <BooleanInput source="email_verified" label="已验证邮箱" />,
    <DateInput source="created_at_gte" label="注册时间从" />,
    <DateInput source="created_at_lte" label="注册时间到" />,
    <DateInput source="last_login_at_gte" label="最后登录从" />,
];

/**
 * 列表工具栏
 */
const AdminUserListActions = () => (
    <TopToolbar>
        <FilterButton />
        <ExportButton label="导出用户数据" />
    </TopToolbar>
);

/**
 * 空状态组件
 */
const AdminUserEmpty = () => (
    <Box sx={{ textAlign: 'center', mt: 4 }}>
        <Card sx={{ maxWidth: 600, mx: 'auto', p: 4 }}>
            <CardContent>
                <AdminIcon sx={{ fontSize: 64, color: 'text.secondary', mb: 2 }} />
                <Typography variant="h5" component="h2" gutterBottom>
                    暂无用户数据
                </Typography>
                <Typography variant="body1" color="text.secondary">
                    系统中暂时没有用户记录。
                </Typography>
            </CardContent>
        </Card>
    </Box>
);

/**
 * 批量操作按钮
 */
const AdminUserBulkActionButtons = () => (
    <>
        <DeleteButton label="批量删除" />
    </>
);

/**
 * 管理员用户列表组件
 */
const AdminUserList: React.FC = () => {
    return (
        <>
            <Alert severity="info" sx={{ mb: 2 }}>
                <AlertTitle>管理员用户管理</AlertTitle>
                <Typography variant="body2">
                    这是系统用户的管理员视角，包含详细的安全信息和活动状态。
                    请谨慎操作用户权限和状态变更。
                </Typography>
            </Alert>
            
            <List
                filters={AdminUserFilters}
                actions={<AdminUserListActions />}
                empty={<AdminUserEmpty />}
                perPage={50}
                sort={{ field: 'created_at', order: 'DESC' }}
                title="系统用户管理"
            >
                <Datagrid
                    bulkActionButtons={<AdminUserBulkActionButtons />}
                    sx={{
                        '& .RaDatagrid-table': {
                            '& .RaDatagrid-tbody .RaDatagrid-row:hover': {
                                backgroundColor: '#f8fafc',
                            },
                            // 高亮管理员用户
                            '& .RaDatagrid-tbody .RaDatagrid-row[data-record-role="admin"]': {
                                backgroundColor: '#fef2f2',
                                '&:hover': {
                                    backgroundColor: '#fecaca',
                                },
                            },
                            // 高亮问题用户
                            '& .RaDatagrid-tbody .RaDatagrid-row[data-record-status="suspended"]': {
                                backgroundColor: '#fff7ed',
                                '&:hover': {
                                    backgroundColor: '#fed7aa',
                                },
                            },
                        },
                    }}
                >
                    {/* ID */}
                    <NumberField source="id" label="ID" />

                    {/* 用户信息 */}
                    <WrapperField label="用户信息" sortBy="username">
                        <AdminUserAvatar />
                    </WrapperField>

                    {/* 用户名和邮箱 */}
                    <Box>
                        <TextField source="username" label="用户名" />
                        <EmailField source="email" label="邮箱" />
                    </Box>

                    {/* 角色 */}
                    <WrapperField label="角色" sortBy="role">
                        <AdminRoleChip />
                    </WrapperField>

                    {/* 状态 */}
                    <WrapperField label="状态" sortBy="status">
                        <AdminStatusChip />
                    </WrapperField>

                    {/* 安全信息 */}
                    <WrapperField label="安全状态">
                        <SecurityInfo />
                    </WrapperField>

                    {/* 活动状态 */}
                    <WrapperField label="活动状态" sortBy="last_login_at">
                        <ActivityStatus />
                    </WrapperField>

                    {/* 系统信息 */}
                    <TextField source="timezone" label="时区" />
                    <TextField source="language" label="语言" />

                    {/* 时间信息 */}
                    <DateField 
                        source="created_at" 
                        label="注册时间"
                        showTime
                        locales="zh-CN"
                        options={{
                            year: '2-digit',
                            month: 'short',
                            day: 'numeric',
                        }}
                    />

                    <DateField 
                        source="last_login_at" 
                        label="最后登录"
                        emptyText="从未登录"
                        showTime
                        locales="zh-CN"
                        options={{
                            month: 'short',
                            day: 'numeric',
                            hour: '2-digit',
                            minute: '2-digit',
                        }}
                    />

                    {/* 统计信息 */}
                    <NumberField source="failed_login_count" label="失败登录" />

                    {/* 验证状态 */}
                    <BooleanField 
                        source="email_verified" 
                        label="邮箱已验证"
                        TrueIcon={ActiveIcon}
                        FalseIcon={InactiveIcon}
                    />

                    {/* 操作按钮 */}
                    <Stack direction="row" spacing={1}>
                        <EditButton label="编辑" />
                    </Stack>
                </Datagrid>
            </List>
        </>
    );
};

export default AdminUserList;