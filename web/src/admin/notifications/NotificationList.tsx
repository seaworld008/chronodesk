import React, { useMemo } from 'react';
import {
    List,
    DateField,
    ReferenceField,
    FilterButton,
    ExportButton,
    ReferenceInput,
    TopToolbar,
    useRecordContext,
    WrapperField,
    FunctionField,
    usePermissions,
} from 'react-admin';
import {
    Chip,
    Box,
    Typography,
    Stack,
    Card,
    CardContent,
    Avatar,
    Tooltip,
} from '@mui/material';
import {
    NotificationsActive as NotificationIcon,
    Email as EmailIcon,
    Webhook as WebhookIcon,
    Cloud as WebSocketIcon,
    PhoneIphone as InAppIcon,
    PriorityHigh as HighPriorityIcon,
    Warning as UrgentIcon,
    Info as NormalIcon,
    KeyboardArrowDown as LowIcon,
    CheckCircle as ReadIcon,
    RadioButtonUnchecked as UnreadIcon,
    Send as SentIcon,
    Schedule as PendingIcon,
    Assignment as TicketAssignedIcon,
    Update as StatusChangedIcon,
    Comment as CommentedIcon,
    Create as CreatedIcon,
    AccessTime as OverdueIcon,
    CheckCircleOutlined as ResolvedIcon,
    Close as ClosedIcon,
    Build as MaintenanceIcon,
    AlternateEmail as MentionIcon,
    Error as AlertIcon,
} from '@mui/icons-material';
import {
    EnterpriseDatagrid,
    InlineDetails,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable';
import { EnterpriseSearchInput } from '@/components/inputs/EnterpriseSearchInput';
import {
    EnterpriseBooleanFilterInput,
    EnterpriseDateFilterInput,
    EnterpriseReferenceAutocompleteInput,
    EnterpriseSelectFilterInput,
} from '@/components/inputs/EnterpriseFilterInputs';
import {
    isAdministrativeRole,
    type RolePermissions,
} from '@/lib/accessControl';

// 通知类型选项
const notificationTypeChoices = [
    { id: 'ticket_assigned', name: '工单分配' },
    { id: 'ticket_status_changed', name: '状态变更' },
    { id: 'ticket_commented', name: '评论通知' },
    { id: 'ticket_created', name: '工单创建' },
    { id: 'ticket_overdue', name: '工单逾期' },
    { id: 'ticket_resolved', name: '工单解决' },
    { id: 'ticket_closed', name: '工单关闭' },
    { id: 'system_maintenance', name: '系统维护' },
    { id: 'user_mention', name: '用户提及' },
    { id: 'system_alert', name: '系统警报' },
];

// 优先级选项
const priorityChoices = [
    { id: 'low', name: '低' },
    { id: 'normal', name: '普通' },
    { id: 'high', name: '高' },
    { id: 'urgent', name: '紧急' },
];

// 通知渠道选项
const channelChoices = [
    { id: 'in_app', name: '应用内' },
    { id: 'email', name: '邮件' },
    { id: 'webhook', name: 'Webhook' },
    { id: 'websocket', name: 'WebSocket' },
];

const notificationColumns: ResizableColumn[] = [
    { key: 'type', defaultWidth: 164, minWidth: 120, maxWidth: 260 },
    { key: 'column-2', defaultWidth: 360, minWidth: 220, maxWidth: 600 },
    { key: 'priority', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
    { key: 'channel', defaultWidth: 136, minWidth: 104, maxWidth: 220 },
    { key: 'recipient_id', defaultWidth: 176, minWidth: 128, maxWidth: 300 },
    { key: 'sender_id', defaultWidth: 176, minWidth: 128, maxWidth: 300 },
    { key: 'column-7', defaultWidth: 180, minWidth: 144, maxWidth: 260 },
    { key: 'related_ticket_id', defaultWidth: 160, minWidth: 120, maxWidth: 260 },
    { key: 'created_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
    { key: 'read_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
    { key: 'column-11', defaultWidth: 132, minWidth: 104, maxWidth: 200 },
];

/**
 * 通知类型图标组件
 */
const NotificationTypeIcon: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getTypeIcon = (type: string) => {
        switch (type) {
            case 'ticket_assigned':
                return <TicketAssignedIcon fontSize="small" />;
            case 'ticket_status_changed':
                return <StatusChangedIcon fontSize="small" />;
            case 'ticket_commented':
                return <CommentedIcon fontSize="small" />;
            case 'ticket_created':
                return <CreatedIcon fontSize="small" />;
            case 'ticket_overdue':
                return <OverdueIcon fontSize="small" />;
            case 'ticket_resolved':
                return <ResolvedIcon fontSize="small" />;
            case 'ticket_closed':
                return <ClosedIcon fontSize="small" />;
            case 'system_maintenance':
                return <MaintenanceIcon fontSize="small" />;
            case 'user_mention':
                return <MentionIcon fontSize="small" />;
            case 'system_alert':
                return <AlertIcon fontSize="small" />;
            default:
                return <NotificationIcon fontSize="small" />;
        }
    };

    const typeName = notificationTypeChoices.find(t => t.id === record.type)?.name || '未知类型';
    const icon = getTypeIcon(record.type);

    return (
        <Tooltip title={`通知类型代码：${record.type || '—'}`}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                {icon}
                <Typography variant="body2" noWrap>
                    {typeName}
                </Typography>
            </Box>
        </Tooltip>
    );
};

/**
 * 优先级标签组件
 */
const PriorityChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getPriorityConfig = (priority: string) => {
        switch (priority) {
            case 'urgent':
                return {
                    label: '紧急',
                    color: '#dc2626',
                    backgroundColor: '#fef2f2',
                    icon: <UrgentIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'high':
                return {
                    label: '高',
                    color: '#ea580c',
                    backgroundColor: '#fff7ed',
                    icon: <HighPriorityIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'normal':
                return {
                    label: '普通',
                    color: '#2563eb',
                    backgroundColor: '#eff6ff',
                    icon: <NormalIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'low':
                return {
                    label: '低',
                    color: '#059669',
                    backgroundColor: '#f0fdf4',
                    icon: <LowIcon sx={{ fontSize: '0.8rem' }} />
                };
            default:
                return {
                    label: '普通',
                    color: '#64748b',
                    backgroundColor: '#f8fafc',
                    icon: <NormalIcon sx={{ fontSize: '0.8rem' }} />
                };
        }
    };

    const { label, color, backgroundColor, icon } = getPriorityConfig(record.priority);

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
 * 通知渠道标签组件
 */
const ChannelChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getChannelConfig = (channel: string) => {
        switch (channel) {
            case 'email':
                return {
                    label: '邮件',
                    color: '#7c3aed',
                    backgroundColor: '#f3e8ff',
                    icon: <EmailIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'webhook':
                return {
                    label: 'Webhook',
                    color: '#059669',
                    backgroundColor: '#f0fdf4',
                    icon: <WebhookIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'websocket':
                return {
                    label: 'WebSocket',
                    color: '#d97706',
                    backgroundColor: '#fefce8',
                    icon: <WebSocketIcon sx={{ fontSize: '0.8rem' }} />
                };
            case 'in_app':
            default:
                return {
                    label: '应用内',
                    color: '#2563eb',
                    backgroundColor: '#eff6ff',
                    icon: <InAppIcon sx={{ fontSize: '0.8rem' }} />
                };
        }
    };

    const { label, color, backgroundColor, icon } = getChannelConfig(record.channel);

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
 * 通知内容组件
 */
const NotificationContent: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const fullContent = [record.title, record.content].filter(Boolean).join(' — ');

    return (
        <InlineDetails
            primary={record.title}
            secondary={record.content}
            title={fullContent}
        />
    );
};

/**
 * 状态标签组件
 */
const StatusChips: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Stack direction="row" spacing={0.5} sx={{ flexWrap: 'nowrap' }}>
            {/* 已读状态 */}
            <Chip
                size="small"
                label={record.is_read ? '已读' : '未读'}
                icon={record.is_read ? <ReadIcon sx={{ fontSize: '0.7rem' }} /> : <UnreadIcon sx={{ fontSize: '0.7rem' }} />}
                color={record.is_read ? 'success' : 'warning'}
                variant={record.is_read ? 'filled' : 'outlined'}
                sx={{ fontSize: '0.7rem' }}
            />
            {/* 发送状态 */}
            <Chip
                size="small"
                label={record.is_sent ? '已发送' : '待发送'}
                icon={record.is_sent ? <SentIcon sx={{ fontSize: '0.7rem' }} /> : <PendingIcon sx={{ fontSize: '0.7rem' }} />}
                color={record.is_sent ? 'success' : 'default'}
                variant={record.is_sent ? 'filled' : 'outlined'}
                sx={{ fontSize: '0.7rem' }}
            />
        </Stack>
    );
};

/**
 * 用户信息组件
 */
const UserInfo: React.FC<{ userType: 'recipient' | 'sender' }> = ({ userType }) => {
    const record = useRecordContext();
    if (!record) return null;

    const user = userType === 'recipient' ? record.recipient : record.sender;
    if (!user) return (
        <Typography variant="caption" sx={{
            color: "text.secondary"
        }}>--</Typography>
    );

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Avatar sx={{ width: 24, height: 24, fontSize: '0.7rem' }}>
                {user.username?.charAt(0).toUpperCase()}
            </Avatar>
            <Box sx={{ minWidth: 0 }}>
                <TruncatedText title={user.display_name || user.username} fontWeight={500}>
                    {user.display_name || user.username}
                </TruncatedText>
            </Box>
        </Box>
    );
};

/**
 * 过滤器组件
 */
type NotificationRecord = {
    is_delivered?: boolean
    is_sent?: boolean
    retry_count?: number
}

const buildNotificationFilters = (canAdminister: boolean) => [
    <EnterpriseSearchInput key="q" source="q" placeholder="搜索通知" alwaysOn />,
    <EnterpriseSelectFilterInput
        key="type"
        source="type"
        label="通知类型"
        choices={notificationTypeChoices}
    />,
    <EnterpriseSelectFilterInput
        key="priority"
        source="priority"
        label="优先级"
        choices={priorityChoices}
    />,
    <EnterpriseSelectFilterInput
        key="channel"
        source="channel"
        label="通知渠道"
        choices={channelChoices}
    />,
    <EnterpriseBooleanFilterInput key="is_read" source="is_read" label="已读" />,
    <EnterpriseBooleanFilterInput key="is_sent" source="is_sent" label="已发送" />,
    ...(canAdminister
        ? [
            <ReferenceInput
                key="recipient_id"
                source="recipient_id"
                reference="users"
                label="接收者"
            >
                <EnterpriseReferenceAutocompleteInput label="接收者" optionText="username" />
            </ReferenceInput>,
            <ReferenceInput
                key="sender_id"
                source="sender_id"
                reference="users"
                label="发送者"
            >
                <EnterpriseReferenceAutocompleteInput label="发送者" optionText="username" />
            </ReferenceInput>,
        ]
        : []),
    <EnterpriseDateFilterInput
        key="created_at_gte"
        source="created_at_gte"
        label="创建时间从"
    />,
    <EnterpriseDateFilterInput
        key="created_at_lte"
        source="created_at_lte"
        label="创建时间到"
    />,
];

/**
 * 列表工具栏
 */
const NotificationListActions = () => (
    <TopToolbar>
        <FilterButton />
        <ExportButton label="导出" />
    </TopToolbar>
);

/**
 * 空状态组件
 */
const NotificationEmpty = () => (
    <Box sx={{ textAlign: 'center', mt: 4 }}>
        <Card sx={{ maxWidth: 600, mx: 'auto', p: 4 }}>
            <CardContent>
                <NotificationIcon sx={{ fontSize: 64, color: 'text.secondary', mb: 2 }} />
                <Typography variant="h5" component="h2" gutterBottom>
                    暂无通知
                </Typography>
                <Typography variant="body1" sx={{
                    color: "text.secondary"
                }}>
                    当前没有任何通知记录。当系统产生通知时，它们会出现在这里。
                </Typography>
            </CardContent>
        </Card>
    </Box>
);

/**
 * 通知列表组件
 */
const NotificationList: React.FC = () => {
    const { permissions } = usePermissions<RolePermissions>();
    const canAdminister = isAdministrativeRole(permissions?.role);
    const filters = useMemo(
        () => buildNotificationFilters(canAdminister),
        [canAdminister],
    );

    return (
        <List
            filters={filters}
            actions={<NotificationListActions />}
            empty={<NotificationEmpty />}
            perPage={25}
            sort={{ field: 'created_at', order: 'DESC' }}
            title="通知管理"
        >
            <EnterpriseDatagrid
                tableId="notifications.main"
                columns={notificationColumns}
                aria-label="通知列表"
                bulkActionButtons={false}
                sx={{
                    '& .RaDatagrid-table': {
                        '& .RaDatagrid-tbody .RaDatagrid-row': {
                            '&:hover': {
                                backgroundColor: '#f8fafc',
                            },
                            '&[data-record-is_read="false"]': {
                                backgroundColor: '#eff6ff',
                                fontWeight: 500,
                            },
                        },
                    },
                }}
            >
                {/* 通知类型 */}
                <WrapperField label="类型" sortBy="type">
                    <NotificationTypeIcon />
                </WrapperField>

                {/* 通知内容 */}
                <WrapperField label="通知内容">
                    <NotificationContent />
                </WrapperField>

                {/* 优先级 */}
                <WrapperField label="优先级" sortBy="priority">
                    <PriorityChip />
                </WrapperField>

                {/* 通知渠道 */}
                <WrapperField label="渠道" sortBy="channel">
                    <ChannelChip />
                </WrapperField>

                {/* 接收者 */}
                <WrapperField label="接收者" sortBy="recipient_id">
                    <UserInfo userType="recipient" />
                </WrapperField>

                {/* 发送者 */}
                <WrapperField label="发送者" sortBy="sender_id">
                    <UserInfo userType="sender" />
                </WrapperField>

                {/* 状态 */}
                <WrapperField label="状态">
                    <StatusChips />
                </WrapperField>

                {/* 相关工单 */}
                <ReferenceField 
                    source="related_ticket_id" 
                    reference="tickets" 
                    label="相关工单"
                    emptyText="--"
                >
                    <FunctionField
                        render={(record) => (
                            <TruncatedText title={record?.ticket_number || '—'}>
                                {record?.ticket_number || '—'}
                            </TruncatedText>
                        )}
                    />
                </ReferenceField>

                {/* 创建时间 */}
                <DateField 
                    source="created_at" 
                    label="创建时间"
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

                {/* 已读时间 */}
                <DateField 
                    source="read_at" 
                    label="已读时间"
                    emptyText="未读"
                    showTime
                    locales="zh-CN"
                    options={{
                        month: 'short',
                        day: 'numeric',
                        hour: '2-digit',
                        minute: '2-digit',
                    }}
                />

                {/* 发送状态 */}
                <FunctionField<NotificationRecord>
                    label="投递状态"
                    render={(record) => {
                        if (!record) {
                            return <Chip size="small" label="--" />
                        }
                        if (record.is_delivered) {
                            return (
                                <Chip
                                    size="small"
                                    label="已投递"
                                    color="success"
                                    sx={{ fontSize: '0.75rem' }}
                                />
                            );
                        }
                        if (record.is_sent) {
                            return (
                                <Chip
                                    size="small"
                                    label="已发送"
                                    color="primary"
                                    sx={{ fontSize: '0.75rem' }}
                                />
                            );
                        }
                        if ((record.retry_count ?? 0) > 0) {
                            return (
                                <Chip
                                    size="small"
                                    label={`重试${record.retry_count}次`}
                                    color="warning"
                                    sx={{ fontSize: '0.75rem' }}
                                />
                            );
                        }
                        return (
                            <Chip
                                size="small"
                                label="待发送"
                                color="default"
                                sx={{ fontSize: '0.75rem' }}
                            />
                        );
                    }}
                />
            </EnterpriseDatagrid>
        </List>
    );
};

export default NotificationList;
