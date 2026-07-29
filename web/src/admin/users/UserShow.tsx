import React from 'react';
import {
    Show,
    SimpleShowLayout,
    TextField,
    DateField,
    SelectField,
    BooleanField,
    useRecordContext,
    TopToolbar,
    EditButton,
    ListButton,
    TabbedShowLayout,
    Tab,
    ReferenceManyField,
    NumberField,
    FunctionField,
} from 'react-admin';
import { FocusSafeDeleteButton } from '@/components/actions/FocusSafeDeleteButtons';
import {
    Box,
    Typography,
    Chip,
    Card,
    CardContent,
    CardHeader,
    Stack,
    Avatar,
    Divider,
    Alert,
    type ChipProps,
    type AlertColor,
} from '@mui/material';
import {
    Person as PersonIcon,
    Email as EmailIcon,
    Phone as PhoneIcon,
    Language as LanguageIcon,
    Schedule as TimezoneIcon,
    AdminPanelSettings as AdminIcon,
    Support as SupportIcon,
    Business as CustomerIcon,
    SupervisorAccount as SupervisorIcon,
    CheckCircle as CheckCircleIcon,
    Cancel as CancelIcon,
    Warning as WarningIcon,
    Security as SecurityIcon,
} from '@mui/icons-material';
import BackButton from '../common/BackButton';
import { User } from '@/types';
import {
    EnterpriseDatagrid,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable';
import {
    getUserRoleLabel,
    normalizeUserRole,
    userRoleChoices,
} from '@/lib/accessControl';

const relatedTicketColumns: ResizableColumn[] = [
    { key: 'ticket_number', defaultWidth: 156, minWidth: 120, maxWidth: 240 },
    { key: 'title', defaultWidth: 360, minWidth: 200, maxWidth: 640 },
    { key: 'status', defaultWidth: 132, minWidth: 104, maxWidth: 220 },
    { key: 'timestamp', defaultWidth: 188, minWidth: 144, maxWidth: 280 },
];

// 状态选项（与UserList保持一致）
const statusChoices = [
    { id: 'active', name: '激活' },
    { id: 'inactive', name: '未激活' },
    { id: 'suspended', name: '暂停' },
    { id: 'deleted', name: '删除' },
];

/**
 * 用户头像和基本信息卡片
 */
type ChipConfig = {
    color: ChipProps['color'];
    icon: React.ReactElement;
};

type AccountAlert = {
    severity: AlertColor;
    message: string;
};

const UserHeader: React.FC = () => {
    const record = useRecordContext<User>();
    if (!record) return null;

    const getInitials = (firstName?: string, lastName?: string, username?: string) => {
        if (firstName && lastName) {
            return `${firstName[0]}${lastName[0]}`.toUpperCase();
        }
        if (username) {
            return username.substring(0, 2).toUpperCase();
        }
        return 'U';
    };

    const initials = getInitials(record.first_name, record.last_name, record.username);
    const fullName = `${record.first_name || ''} ${record.last_name || ''}`.trim();
    const displayName = record.display_name || fullName || record.username;

    const getRoleConfig = (role: User['role']): ChipConfig => {
        switch (normalizeUserRole(role)) {
            case 'admin':
                return { color: 'error', icon: <AdminIcon /> };
            case 'agent':
                return { color: 'primary', icon: <SupportIcon /> };
            case 'supervisor':
                return { color: 'secondary', icon: <SupervisorIcon /> };
            case 'customer':
                return { color: 'success', icon: <CustomerIcon /> };
            default:
                return { color: 'default', icon: <PersonIcon /> };
        }
    };

    const getStatusConfig = (status: User['status']): ChipConfig => {
        switch (status) {
            case 'active':
                return { color: 'success', icon: <CheckCircleIcon /> };
            case 'inactive':
                return { color: 'warning', icon: <WarningIcon /> };
            case 'suspended':
                return { color: 'error', icon: <CancelIcon /> };
            case 'deleted':
                return { color: 'default', icon: <CancelIcon /> };
            default:
                return { color: 'default', icon: <WarningIcon /> };
        }
    };

    const typedStatusConfig = getStatusConfig(record.status);

    const roleConfig = getRoleConfig(record.role);
    const roleName = getUserRoleLabel(record.role);
    const statusName = statusChoices.find(s => s.id === record.status)?.name || '未知状态';

    return (
        <Card sx={{ mb: 3 }}>
            <CardHeader
                avatar={
                    <Avatar
                        src={record.avatar}
                        sx={{
                            width: 80,
                            height: 80,
                            bgcolor: 'primary.main',
                            fontSize: '1.5rem',
                        }}
                    >
                        {initials}
                    </Avatar>
                }
                title={
                    <Box>
                        <Typography variant="h4" component="h1" sx={{
                            fontWeight: 600
                        }}>
                            {displayName}
                        </Typography>
                        <Typography
                            variant="h6"
                            sx={{
                                color: "text.secondary",
                                mb: 1
                            }}>
                            @{record.username}
                        </Typography>
                        <Stack direction="row" spacing={1}>
                            <Chip label={roleName} color={roleConfig.color} icon={roleConfig.icon} size="medium" />
                            <Chip
                                label={statusName}
                                color={typedStatusConfig.color}
                                icon={typedStatusConfig.icon}
                                size="medium"
                                variant={record.status === 'active' ? 'filled' : 'outlined'}
                            />
                            {!record.email_verified && (
                                <Chip
                                    label="邮箱未验证"
                                    color="warning"
                                    size="medium"
                                    variant="outlined"
                                />
                            )}
                        </Stack>
                    </Box>
                }
            />
        </Card>
    );
};

/**
 * 联系信息卡片
 */
const ContactInfoCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Card>
            <CardHeader
                title="联系信息"
                avatar={<PersonIcon color="primary" />}
            />
            <CardContent>
                <Stack spacing={3}>
                    <Box>
                        <Typography variant="subtitle2" gutterBottom sx={{
                            color: "text.secondary"
                        }}>
                            邮箱地址
                        </Typography>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                            <EmailIcon fontSize="small" color="action" />
                            <Typography variant="body1" sx={{
                                fontWeight: 500
                            }}>
                                {record.email}
                            </Typography>
                            {record.email_verified ? (
                                <CheckCircleIcon color="success" fontSize="small" />
                            ) : (
                                <WarningIcon color="warning" fontSize="small" />
                            )}
                        </Box>
                    </Box>
                    
                    {record.phone && (
                        <Box>
                            <Typography variant="subtitle2" gutterBottom sx={{
                                color: "text.secondary"
                            }}>
                                电话号码
                            </Typography>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                <PhoneIcon fontSize="small" color="action" />
                                <Typography variant="body1">
                                    {record.phone}
                                </Typography>
                            </Box>
                        </Box>
                    )}
                </Stack>
            </CardContent>
        </Card>
    );
};

/**
 * 偏好设置卡片
 */
const PreferencesCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Card>
            <CardHeader
                title="偏好设置"
                avatar={<LanguageIcon color="primary" />}
            />
            <CardContent>
                <Stack spacing={3}>
                    <Box>
                        <Typography variant="subtitle2" gutterBottom sx={{
                            color: "text.secondary"
                        }}>
                            时区
                        </Typography>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                            <TimezoneIcon fontSize="small" color="action" />
                            <Typography variant="body1">
                                {record.timezone || 'Asia/Shanghai'}
                            </Typography>
                        </Box>
                    </Box>
                    
                    <Box>
                        <Typography variant="subtitle2" gutterBottom sx={{
                            color: "text.secondary"
                        }}>
                            语言设置
                        </Typography>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                            <LanguageIcon fontSize="small" color="action" />
                            <Typography variant="body1">
                                {record.language === 'zh-CN' ? '中文简体' : record.language || '中文简体'}
                            </Typography>
                        </Box>
                    </Box>
                </Stack>
            </CardContent>
        </Card>
    );
};

/**
 * 账户状态卡片
 */
const AccountStatusCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getAccountAlerts = (): AccountAlert[] => {
        const alerts: AccountAlert[] = [];
        
        if (!record.email_verified) {
            alerts.push({
                severity: 'warning',
                message: '邮箱地址尚未验证'
            });
        }
        
        if (record.status === 'suspended') {
            alerts.push({
                severity: 'error',
                message: '账户已被暂停'
            });
        }
        
        if (record.status === 'inactive') {
            alerts.push({
                severity: 'info',
                message: '账户尚未激活'
            });
        }

        if (record.failed_login_count && record.failed_login_count > 0) {
            alerts.push({
                severity: 'warning',
                message: `近期有 ${record.failed_login_count} 次登录失败记录`
            });
        }

        return alerts;
    };

    const alerts = getAccountAlerts();

    return (
        <Card>
            <CardHeader
                title="账户状态"
                avatar={<SecurityIcon color="primary" />}
            />
            <CardContent>
                <Stack spacing={2}>
                    {alerts.length > 0 ? (
                        alerts.map((alert, index) => (
                            <Alert key={index} severity={alert.severity}>
                                {alert.message}
                            </Alert>
                        ))
                    ) : (
                        <Alert severity="success">
                            账户状态正常
                        </Alert>
                    )}
                    
                    <Divider />
                    
                    <Box>
                        <Typography variant="subtitle2" gutterBottom sx={{
                            color: "text.secondary"
                        }}>
                            注册时间
                        </Typography>
                        <Typography variant="body1">
                            {new Date(record.created_at).toLocaleString('zh-CN')}
                        </Typography>
                    </Box>
                    
                    {record.last_login_at && (
                        <Box>
                            <Typography variant="subtitle2" gutterBottom sx={{
                                color: "text.secondary"
                            }}>
                                最后登录
                            </Typography>
                            <Typography variant="body1">
                                {new Date(record.last_login_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}
                    
                    {record.email_verified_at && (
                        <Box>
                            <Typography variant="subtitle2" gutterBottom sx={{
                                color: "text.secondary"
                            }}>
                                邮箱验证时间
                            </Typography>
                            <Typography variant="body1">
                                {new Date(record.email_verified_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}
                </Stack>
            </CardContent>
        </Card>
    );
};

/**
 * 顶部工具栏
 */
const UserShowActions = () => (
    <TopToolbar>
        <ListButton label="返回列表" />
        <EditButton label="编辑" />
        <FocusSafeDeleteButton label="删除" mutationMode="pessimistic" />
    </TopToolbar>
);

/**
 * 用户详情页面
 */
const UserShow: React.FC = () => {
    return (
        <Show actions={<UserShowActions />} title="用户详情">
            <Box sx={{ p: 0 }}>
                <UserHeader />
                <Box sx={{ px: 3 }}>
                    <BackButton fallbackPath="/users" />
                </Box>

                <TabbedShowLayout>
                    {/* 基本信息 */}
                    <Tab label="基本信息">
                        <Box sx={{ display: 'flex', gap: 3, flexWrap: 'wrap' }}>
                            <Box sx={{ flex: 2, minWidth: '500px' }}>
                                <Stack spacing={3}>
                                    {/* 个人详细信息 */}
                                    <Card>
                                        <CardHeader title="个人信息" />
                                        <CardContent>
                                            <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        姓
                                                    </Typography>
                                                    <TextField source="first_name" />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        名
                                                    </Typography>
                                                    <TextField source="last_name" />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        显示名称
                                                    </Typography>
                                                    <TextField source="display_name" />
                                                </Box>
                                            </Box>
                                        </CardContent>
                                    </Card>
                                    
                                    {/* 系统信息 */}
                                    <Card>
                                        <CardHeader title="系统信息" />
                                        <CardContent>
                                            <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        用户ID
                                                    </Typography>
                                                    <TextField source="id" />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        角色
                                                    </Typography>
                                                    <SelectField source="role" choices={userRoleChoices} />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        状态
                                                    </Typography>
                                                    <SelectField source="status" choices={statusChoices} />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        邮箱已验证
                                                    </Typography>
                                                    <BooleanField source="email_verified" />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" gutterBottom sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        登录失败次数
                                                    </Typography>
                                                    <NumberField source="failed_login_count" />
                                                </Box>
                                            </Box>
                                        </CardContent>
                                    </Card>
                                </Stack>
                            </Box>
                            
                            <Box sx={{ flex: 1, minWidth: '300px' }}>
                                <Stack spacing={3}>
                                    <ContactInfoCard />
                                    <PreferencesCard />
                                    <AccountStatusCard />
                                </Stack>
                            </Box>
                        </Box>
                    </Tab>
                    
                    {/* 工单历史 */}
                    <Tab label="相关工单">
                        <ReferenceManyField
                            reference="tickets"
                            target="created_by_id"
                            label="创建的工单"
                            perPage={10}
                            sort={{ field: 'created_at', order: 'DESC' }}
                        >
                            <EnterpriseDatagrid
                                tableId="users.show.created-tickets"
                                columns={relatedTicketColumns}
                                aria-label="该用户创建的工单列表"
                                bulkActionButtons={false}
                            >
                                <TextField source="ticket_number" label="工单编号" />
                                <FunctionField
                                    label="标题"
                                    sortBy="title"
                                    render={(record) => (
                                        <TruncatedText title={record?.title}>{record?.title || '—'}</TruncatedText>
                                    )}
                                />
                                <SelectField 
                                    source="status" 
                                    label="状态" 
                                    choices={[
                                        { id: 'open', name: '待处理' },
                                        { id: 'in_progress', name: '处理中' },
                                        { id: 'pending', name: '等待中' },
                                        { id: 'resolved', name: '已解决' },
                                        { id: 'closed', name: '已关闭' },
                                        { id: 'cancelled', name: '已取消' },
                                    ]}
                                />
                                <DateField 
                                    source="created_at" 
                                    label="创建时间" 
                                    showTime 
                                    locales="zh-CN"
                                />
                            </EnterpriseDatagrid>
                        </ReferenceManyField>
                        
                        <Box sx={{ mt: 4 }}>
                            <Typography variant="h6" gutterBottom>
                                分配的工单
                            </Typography>
                            <ReferenceManyField
                                reference="tickets"
                                target="assigned_to_id"
                                label=""
                                perPage={10}
                                sort={{ field: 'created_at', order: 'DESC' }}
                            >
                                <EnterpriseDatagrid
                                    tableId="users.show.assigned-tickets"
                                    columns={relatedTicketColumns}
                                    aria-label="该用户负责的工单列表"
                                    bulkActionButtons={false}
                                >
                                    <TextField source="ticket_number" label="工单编号" />
                                    <FunctionField
                                        label="标题"
                                        sortBy="title"
                                        render={(record) => (
                                            <TruncatedText title={record?.title}>{record?.title || '—'}</TruncatedText>
                                        )}
                                    />
                                    <SelectField 
                                        source="status" 
                                        label="状态" 
                                        choices={[
                                            { id: 'open', name: '待处理' },
                                            { id: 'in_progress', name: '处理中' },
                                            { id: 'pending', name: '等待中' },
                                            { id: 'resolved', name: '已解决' },
                                            { id: 'closed', name: '已关闭' },
                                            { id: 'cancelled', name: '已取消' },
                                        ]}
                                    />
                                    <DateField 
                                        source="updated_at" 
                                        label="最后更新" 
                                        showTime 
                                        locales="zh-CN"
                                    />
                                </EnterpriseDatagrid>
                            </ReferenceManyField>
                        </Box>
                    </Tab>
                    
                    {/* 活动日志 */}
                    <Tab label="活动日志">
                        <SimpleShowLayout>
                            <DateField source="created_at" label="注册时间" showTime locales="zh-CN" />
                            <DateField source="updated_at" label="最后更新" showTime locales="zh-CN" />
                            <DateField source="last_login_at" label="最后登录" showTime locales="zh-CN" />
                            <DateField source="email_verified_at" label="邮箱验证时间" showTime locales="zh-CN" />
                            <NumberField source="failed_login_count" label="失败登录次数" />
                        </SimpleShowLayout>
                    </Tab>
                </TabbedShowLayout>
            </Box>
        </Show>
    );
};

export default UserShow;
