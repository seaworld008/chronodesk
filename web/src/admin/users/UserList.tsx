import React from 'react';
import {
    List,
    Datagrid,
    TextField,
    DateField,
    BooleanField,
    EditButton,
    ShowButton,
    DeleteButton,
    CreateButton,
    ExportButton,
    FilterButton,
    SearchInput,
    SelectInput,
    DateInput,
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

// 用户角色选项
const roleChoices = [
    { id: 'admin', name: '管理员' },
    { id: 'agent', name: '客服代理' },
    { id: 'customer', name: '客户' },
    { id: 'supervisor', name: '主管' },
];

// 用户状态选项
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
    { id: 'deleted', name: '删除' },
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
            <Box>
                <Typography variant="body2" fontWeight={600}>
                    {record.display_name || `${record.first_name || ''} ${record.last_name || ''}`.trim() || record.username}
                </Typography>
                <Typography variant="caption" color="text.secondary">
                    @{record.username}
                </Typography>
            </Box>
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
                    icon: <PersonIcon sx={{ fontSize: '0.8rem' }} />
                };
        }
    };

    const { label, color, backgroundColor, icon } = getRoleConfig(record.role);

    return (
        <Chip
            size="small"
            label={label}
            icon={icon}
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

    return (
        <Box>
            <Typography variant="body2" fontWeight={500}>
                {record.email}
            </Typography>
            {record.phone && (
                <Typography variant="caption" color="text.secondary">
                    {record.phone}
                </Typography>
            )}
        </Box>
    );
};

/**
 * 最后登录时间组件
 */
const LastLoginInfo: React.FC = () => {
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
    <SearchInput source="q" placeholder="搜索用户" alwaysOn />,
    <SelectInput source="role" label="角色" choices={roleChoices} />,
    <SelectInput source="status" label="状态" choices={statusChoices} />,
    <DateInput source="created_at_gte" label="注册时间从" />,
    <DateInput source="created_at_lte" label="注册时间到" />,
    <DateInput source="last_login_at_gte" label="最后登录从" />,
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
                <Typography variant="body1" color="text.secondary" paragraph>
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
        <DeleteButton label="批量删除" />
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
            <Datagrid
                rowClick="show"
                bulkActionButtons={<UserBulkActionButtons />}
                sx={{
                    '& .RaDatagrid-table': {
                        '& .RaDatagrid-tbody .RaDatagrid-row:hover': {
                            backgroundColor: '#f8fafc',
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
                <TextField 
                    source="timezone" 
                    label="时区"
                    sx={{ minWidth: '120px' }}
                />
                
                <TextField 
                    source="language" 
                    label="语言"
                    sx={{ minWidth: '80px' }}
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
                <Stack direction="row" spacing={1}>
                    <ShowButton label="查看" />
                    <EditButton label="编辑" />
                </Stack>
            </Datagrid>
        </List>
    );
};

export default UserList;