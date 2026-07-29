import React from 'react';
import {
    Show,
    SimpleShowLayout,
    TextField,
    DateField,
    SelectField,
    ReferenceField,
    NumberField,
    RichTextField,
    useRecordContext,
    TopToolbar,
    EditButton,
    ListButton,
    TabbedShowLayout,
    Tab,
    ReferenceManyField,
    WrapperField,
    FunctionField,
    useGetIdentity,
    usePermissions,
} from 'react-admin';
import {
    Box,
    Typography,
    Chip,
    Card,
    CardContent,
    CardHeader,
    Stack,
    Alert,
    type ChipProps,
} from '@mui/material';
import {
    PriorityHigh as PriorityIcon,
    Person as PersonIcon,
    AccessTime as TimeIcon,
    Email as EmailIcon,
    Phone as PhoneIcon,
    Warning as WarningIcon,
    Schedule as ScheduleIcon,
} from '@mui/icons-material';
import { parseTagsToArray } from './tagUtils';
import BackButton from '../common/BackButton';
import { Ticket } from '@/types';
import TicketWorkflowActions from './TicketWorkflowActions';
import { TicketConversationPanel } from './TicketConversationPanel';
import {
    canDeleteTicket,
    canMutateTicket,
    type TicketRolePermissions,
} from './ticketAccess';
import {
    EnterpriseDatagrid,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable';
import { FocusSafeDeleteButton } from '@/components/actions/FocusSafeDeleteButtons';

const ticketHistoryColumns: ResizableColumn[] = [
    { key: 'action', defaultWidth: 180, minWidth: 128, maxWidth: 320 },
    { key: 'field_name', defaultWidth: 144, minWidth: 104, maxWidth: 240 },
    { key: 'old_value', defaultWidth: 300, minWidth: 160, maxWidth: 640 },
    { key: 'new_value', defaultWidth: 300, minWidth: 160, maxWidth: 640 },
    { key: 'actor', defaultWidth: 200, minWidth: 144, maxWidth: 360 },
    { key: 'created_at', defaultWidth: 188, minWidth: 144, maxWidth: 280 },
];

const historyActionLabels: Record<string, string> = {
    create: '创建工单',
    update: '更新工单',
    status_change: '变更状态',
    priority_change: '变更优先级',
    assign: '分配工单',
    unassign: '取消分配',
    comment: '添加评论',
    attachment: '添加附件',
    close: '关闭工单',
    reopen: '重新打开',
    escalate: '升级工单',
    merge: '合并工单',
    split: '拆分工单',
    transfer: '转移工单',
    resolve: '解决工单',
    reject: '拒绝工单',
    approve: '批准工单',
    system: '系统操作',
};

const historyFieldLabels: Record<string, string> = {
    title: '标题',
    description: '描述',
    status: '状态',
    priority: '优先级',
    type: '类型',
    source: '来源',
    assigned_to_id: '处理人',
    category_id: '分类',
    due_date: '截止时间',
    escalation: '升级状态',
};

const historyValueLabels: Record<string, string> = {
    open: '待处理',
    in_progress: '处理中',
    pending: '等待中',
    resolved: '已解决',
    closed: '已关闭',
    cancelled: '已取消',
    low: '低',
    normal: '普通',
    high: '高',
    urgent: '紧急',
    critical: '严重',
    incident: '事件',
    request: '请求',
    problem: '问题',
    change: '变更',
    complaint: '投诉',
    consultation: '咨询',
    web: '网页',
    email: '邮件',
    phone: '电话',
    chat: '聊天',
    api: 'API',
};

const translateHistoryField = (fieldName?: string) => {
    if (!fieldName) return '—';
    return fieldName
        .split(',')
        .map((field) => historyFieldLabels[field.trim()] || '其他字段')
        .join('、');
};

const translateHistoryValue = (value?: string) => {
    if (!value) return '—';
    return historyValueLabels[value] || value;
};

// 选项配置（与TicketList保持一致）
const statusChoices = [
    { id: 'open', name: '待处理' },
    { id: 'in_progress', name: '处理中' },
    { id: 'pending', name: '等待中' },
    { id: 'resolved', name: '已解决' },
    { id: 'closed', name: '已关闭' },
    { id: 'cancelled', name: '已取消' },
];

const priorityChoices = [
    { id: 'low', name: '低' },
    { id: 'normal', name: '普通' },
    { id: 'high', name: '高' },
    { id: 'urgent', name: '紧急' },
    { id: 'critical', name: '严重' },
];

const typeChoices = [
    { id: 'incident', name: '事件' },
    { id: 'request', name: '请求' },
    { id: 'problem', name: '问题' },
    { id: 'change', name: '变更' },
    { id: 'complaint', name: '投诉' },
    { id: 'consultation', name: '咨询' },
];

const sourceChoices = [
    { id: 'web', name: '网页' },
    { id: 'email', name: '邮件' },
    { id: 'phone', name: '电话' },
    { id: 'chat', name: '聊天' },
    { id: 'api', name: 'API' },
    { id: 'mobile', name: '移动端' },
];

/**
 * 工单标题卡片
 */
const TicketHeader: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const getStatusColor = (status: string): ChipProps['color'] => {
        switch (status) {
            case 'open': return 'primary';
            case 'in_progress': return 'warning';
            case 'pending': return 'info';
            case 'resolved': return 'success';
            case 'closed': return 'default';
            case 'cancelled': return 'error';
            default: return 'default';
        }
    };

    const getPriorityColor = (priority: string): ChipProps['color'] => {
        switch (priority) {
            case 'critical': return 'error';
            case 'urgent': return 'warning';
            case 'high': return 'info';
            case 'normal': return 'primary';
            case 'low': return 'success';
            default: return 'default';
        }
    };

    const statusName = statusChoices.find(s => s.id === record.status)?.name || '未知状态';
    const priorityName = priorityChoices.find(p => p.id === record.priority)?.name || '未知优先级';

    return (
        <Card sx={{ mb: 3, borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)', overflow: 'visible' }}>
            <CardHeader
                title={
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
                        <Typography
                            variant="h5"
                            component="h1"
                            sx={{
                                fontWeight: 700,
                                background: 'linear-gradient(45deg, #1e293b 30%, #334155 90%)',
                                WebkitBackgroundClip: 'text',
                                WebkitTextFillColor: 'transparent'
                            }}>
                            {record.title}
                        </Typography>
                        <Chip
                            label={`#${record.ticket_number}`}
                            color="primary"
                            variant="filled"
                            size="small"
                            sx={{ fontWeight: 600, borderRadius: 1 }}
                        />
                    </Box>
                }
                subheader={
                    <Stack direction="row" spacing={1} sx={{ mt: 1.5 }}>
                        <Chip label={statusName} color={getStatusColor(record.status)} size="small" sx={{ fontWeight: 500 }} />
                        <Chip
                            label={priorityName}
                            color={getPriorityColor(record.priority)}
                            icon={<PriorityIcon />}
                            size="small"
                            sx={{ fontWeight: 500 }}
                        />
                        {record.sla_breached && (
                            <Chip
                                label="SLA违反"
                                color="error"
                                icon={<WarningIcon />}
                                size="small"
                                sx={{ fontWeight: 500 }}
                            />
                        )}
                    </Stack>
                }
                sx={{
                    p: 3,
                    background: 'linear-gradient(to right, #ffffff, #f8fafc)',
                    borderBottom: '1px solid #f1f5f9'
                }}
            />
        </Card>
    );
};

/**
 * 客户信息卡片
 */
const CustomerInfoCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
            <CardHeader
                title="客户信息"
                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                avatar={<PersonIcon color="primary" />}
                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
            />
            <CardContent>
                <Stack spacing={2}>
                    <Box>
                        <Typography variant="subtitle2" sx={{
                            color: "text.secondary"
                        }}>
                            姓名
                        </Typography>
                        <Typography variant="body1" sx={{
                            fontWeight: 500
                        }}>
                            {record.customer_name || '未提供'}
                        </Typography>
                    </Box>

                    {record.customer_email && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                邮箱
                            </Typography>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                <EmailIcon fontSize="small" color="action" />
                                <Typography variant="body1">
                                    {record.customer_email}
                                </Typography>
                            </Box>
                        </Box>
                    )}

                    {record.customer_phone && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                电话
                            </Typography>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                <PhoneIcon fontSize="small" color="action" />
                                <Typography variant="body1">
                                    {record.customer_phone}
                                </Typography>
                            </Box>
                        </Box>
                    )}
                </Stack>
            </CardContent>
        </Card>
    );
};

const TicketTagsDisplay: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const tags = parseTagsToArray(record.tags);
    if (!tags.length) {
        return null;
    }

    return (
        <Box sx={{ flex: 1, minWidth: '150px' }}>
            <Typography variant="subtitle2" sx={{
                color: "text.secondary"
            }}>
                标签
            </Typography>
            <Stack direction="row" spacing={1} useFlexGap sx={{
                flexWrap: "wrap"
            }}>
                {tags.map((tag) => (
                    <Chip key={tag} label={tag} size="small" variant="outlined" />
                ))}
            </Stack>
        </Box>
    );
};

const ActorDisplayField: React.FC = () => {
    const record = useRecordContext<{
        actor?: { type?: string; id?: string }
        user?: { username?: string }
        service_principal?: { name?: string }
    }>();
    if (!record) return null;

    let label = record.user?.username || record.service_principal?.name;
    if (!label) {
        const actorType = record.actor?.type;
        const actorID = record.actor?.id;
        if (actorType === 'system') {
            label = actorID === 'sla-monitor' ? '系统 · SLA 监控' : `系统${actorID ? ` · ${actorID}` : ''}`;
        } else if (actorType === 'service_principal') {
            label = `AI 智能体${actorID ? ` · ${actorID}` : ''}`;
        } else if (actorType === 'human') {
            label = `用户${actorID ? ` · ${actorID}` : ''}`;
        } else {
            label = '系统';
        }
    }

    return <Typography variant="body2">{label}</Typography>;
};

const TicketUserDisplay: React.FC<{ kind: 'creator' | 'assignee' }> = ({ kind }) => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;
    const user = kind === 'creator' ? record.created_by : record.assigned_to;
    if (!user) {
        return <Typography variant="body2">{kind === 'creator' ? '未知' : '未分配'}</Typography>;
    }
    return (
        <Typography variant="body2">
            {user.display_name || user.username}
        </Typography>
    );
};

/**
 * 时间跟踪卡片
 */
const TimeTrackingCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
            <CardHeader
                title="时间跟踪"
                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                avatar={<TimeIcon color="primary" />}
                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
            />
            <CardContent>
                <Stack spacing={2}>
                    <Box>
                        <Typography variant="subtitle2" sx={{
                            color: "text.secondary"
                        }}>
                            创建时间
                        </Typography>
                        <Typography variant="body1">
                            {new Date(record.created_at).toLocaleString('zh-CN')}
                        </Typography>
                    </Box>

                    {record.due_date && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                截止时间
                            </Typography>
                            <Typography
                                variant="body1"
                                color={new Date(record.due_date) < new Date() && !record.closed_at ? 'error' : 'text.primary'}
                            >
                                {new Date(record.due_date).toLocaleString('zh-CN')}
                                {new Date(record.due_date) < new Date() && !record.closed_at && ' (已逾期)'}
                            </Typography>
                        </Box>
                    )}

                    {record.first_reply_at && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                首次回复时间
                            </Typography>
                            <Typography variant="body1">
                                {new Date(record.first_reply_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}

                    {record.resolved_at && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                解决时间
                            </Typography>
                            <Typography variant="body1" sx={{
                                color: "success.main"
                            }}>
                                {new Date(record.resolved_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}

                    {record.closed_at && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                关闭时间
                            </Typography>
                            <Typography variant="body1">
                                {new Date(record.closed_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}
                </Stack>
            </CardContent>
        </Card>
    );
};

/**
 * SLA信息卡片
 */
const SLAInfoCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    if (!record.sla_due_date && !record.response_time && !record.resolution_time) {
        return null;
    }

    return (
        <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
            <CardHeader
                title="SLA信息"
                slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                avatar={<ScheduleIcon color="primary" />}
                sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
            />
            <CardContent>
                <Stack spacing={2}>
                    {record.sla_breached && (
                        <Alert severity="error" icon={<WarningIcon />}>
                            SLA已违反
                        </Alert>
                    )}

                    {record.sla_due_date && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                SLA截止时间
                            </Typography>
                            <Typography
                                variant="body1"
                                color={record.sla_breached ? 'error' : 'text.primary'}
                            >
                                {new Date(record.sla_due_date).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}

                    {record.response_time && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                响应时间
                            </Typography>
                            <Typography variant="body1">
                                {record.response_time} 分钟
                            </Typography>
                        </Box>
                    )}

                    {record.resolution_time && (
                        <Box>
                            <Typography variant="subtitle2" sx={{
                                color: "text.secondary"
                            }}>
                                解决时间
                            </Typography>
                            <Typography variant="body1">
                                {record.resolution_time} 分钟
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
const TicketShowActions = () => {
    const record = useRecordContext<Ticket>();
    const { permissions } = usePermissions<TicketRolePermissions>();
    const { identity } = useGetIdentity();
    const canMutate = canMutateTicket(record, permissions?.role, identity?.id);

    return (
        <TopToolbar>
            <ListButton label="返回列表" />
            {canMutate && <EditButton label="编辑" />}
            {canDeleteTicket(permissions?.role) && (
                <FocusSafeDeleteButton label="删除" mutationMode="pessimistic" />
            )}
        </TopToolbar>
    );
};

/**
 * 工单详情页面
 */
const TicketShow: React.FC = () => {
    return (
        <Show actions={<TicketShowActions />} title="工单详情">
            <Box sx={{ p: 0 }}>
                <TicketHeader />
                <Box sx={{ px: 3, mb: 2 }}>
                    <TicketWorkflowActions />
                </Box>
                <Box sx={{ px: 3 }}>
                    <BackButton />
                </Box>

                <TabbedShowLayout>
                    {/* 基本信息 */}
                    <Tab label="基本信息">
                        <Box sx={{ display: 'flex', gap: 3, flexWrap: 'wrap' }}>
                            <Box sx={{ flex: 2, minWidth: '500px' }}>
                                <Stack spacing={3}>
                                    {/* 工单描述 */}
                                    <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                        <CardHeader
                                            title="工单描述"
                                            slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                            sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                        />
                                        <CardContent>
                                            <RichTextField source="description" />
                                        </CardContent>
                                    </Card>

                                    {/* 基本信息 */}
                                    <Card sx={{ borderRadius: 3, boxShadow: '0 4px 20px rgba(0,0,0,0.05)' }}>
                                        <CardHeader
                                            title="基本信息"
                                            slotProps={{ title: { variant: 'h6', sx: { fontWeight: 600 } } }}
                                            sx={{ borderBottom: '1px solid #f1f5f9', bgcolor: '#f8fafc' }}
                                        />
                                        <CardContent>
                                            <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        类型
                                                    </Typography>
                                                    <SelectField source="type" choices={typeChoices} />
                                                </Box>

                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        来源
                                                    </Typography>
                                                    <SelectField source="source" choices={sourceChoices} />
                                                </Box>

                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        创建人
                                                    </Typography>
                                                    <TicketUserDisplay kind="creator" />
                                                </Box>

                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        分配给
                                                    </Typography>
                                                    <TicketUserDisplay kind="assignee" />
                                                </Box>

                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" sx={{
                                                        color: "text.secondary"
                                                    }}>
                                                        分类
                                                    </Typography>
                                                    <ReferenceField source="category_id" reference="categories" emptyText="未分类">
                                                        <TextField source="name" />
                                                    </ReferenceField>
                                                </Box>

                                                <TicketTagsDisplay />
                                            </Box>
                                        </CardContent>
                                    </Card>
                                </Stack>
                            </Box>

                            <Box sx={{ flex: 1, minWidth: '300px' }}>
                                <Stack spacing={3}>
                                    <CustomerInfoCard />
                                    <TimeTrackingCard />
                                    <SLAInfoCard />
                                </Stack>
                            </Box>
                        </Box>
                    </Tab>

                    {/* 评论历史 */}
                    <Tab label="评论历史">
                        <TicketConversationPanel />
                    </Tab>

                    {/* 历史记录 */}
                    <Tab label="历史记录">
                        <ReferenceManyField
                            reference="ticket_history"
                            target="ticket_id"
                            label="历史记录"
                            perPage={20}
                            sort={{ field: 'created_at', order: 'DESC' }}
                        >
                            <EnterpriseDatagrid
                                tableId="tickets.show.history"
                                columns={ticketHistoryColumns}
                                aria-label="工单历史列表"
                                bulkActionButtons={false}
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
                                <FunctionField
                                    label="操作"
                                    render={(record) => (
                                        <TruncatedText title={record?.action || '—'}>
                                            {historyActionLabels[record?.action] || '其他操作'}
                                        </TruncatedText>
                                    )}
                                />
                                <FunctionField
                                    label="字段"
                                    render={(record) => (
                                        <TruncatedText title={record?.field_name || '—'}>
                                            {translateHistoryField(record?.field_name)}
                                        </TruncatedText>
                                    )}
                                />
                                <FunctionField
                                    label="原值"
                                    render={(record) => (
                                        <TruncatedText title={record?.old_value || '—'}>
                                            {translateHistoryValue(record?.old_value)}
                                        </TruncatedText>
                                    )}
                                />
                                <FunctionField
                                    label="新值"
                                    render={(record) => (
                                        <TruncatedText title={record?.new_value || '—'}>
                                            {translateHistoryValue(record?.new_value)}
                                        </TruncatedText>
                                    )}
                                />
                                <WrapperField label="操作者">
                                    <ActorDisplayField />
                                </WrapperField>
                                <DateField
                                    source="created_at"
                                    label="时间"
                                    showTime
                                    locales="zh-CN"
                                />
                            </EnterpriseDatagrid>
                        </ReferenceManyField>
                    </Tab>

                    {/* 附加信息 */}
                    <Tab label="附加信息">
                        <SimpleShowLayout>
                            <TextField source="internal_notes" label="内部备注" />
                            <NumberField source="view_count" label="查看次数" />
                            <NumberField source="comment_count" label="评论数量" />
                            <NumberField source="rating" label="客户评分" />
                            <TextField source="rating_comment" label="评分备注" />
                            <DateField source="updated_at" label="最后更新" showTime locales="zh-CN" />
                        </SimpleShowLayout>
                    </Tab>
                </TabbedShowLayout>
            </Box>
        </Show>
    );
};

export default TicketShow;
