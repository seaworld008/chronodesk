import React from 'react';
import {
    Show,
    SimpleShowLayout,
    TextField,
    DateField,
    SelectField,
    ReferenceField,
    BooleanField,
    NumberField,
    RichTextField,
    useRecordContext,
    TopToolbar,
    EditButton,
    DeleteButton,
    ListButton,
    TabbedShowLayout,
    Tab,
    ReferenceManyField,
    Datagrid,
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

    const statusName = statusChoices.find(s => s.id === record.status)?.name || record.status;
    const priorityName = priorityChoices.find(p => p.id === record.priority)?.name || record.priority;

    return (
        <Card sx={{ mb: 3 }}>
            <CardHeader
                title={
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
                        <Typography variant="h5" component="h1" fontWeight={600}>
                            {record.title}
                        </Typography>
                        <Chip
                            label={`#${record.ticket_number}`}
                            color="primary"
                            variant="outlined"
                            size="small"
                        />
                    </Box>
                }
                subheader={
                    <Stack direction="row" spacing={1} sx={{ mt: 1 }}>
                        <Chip label={statusName} color={getStatusColor(record.status)} size="small" />
                        <Chip
                            label={priorityName}
                            color={getPriorityColor(record.priority)}
                            icon={<PriorityIcon />}
                            size="small"
                        />
                        {record.sla_breached && (
                            <Chip
                                label="SLA违反"
                                color="error"
                                icon={<WarningIcon />}
                                size="small"
                            />
                        )}
                    </Stack>
                }
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
        <Card>
            <CardHeader
                title="客户信息"
                avatar={<PersonIcon color="primary" />}
            />
            <CardContent>
                <Stack spacing={2}>
                    <Box>
                        <Typography variant="subtitle2" color="text.secondary">
                            姓名
                        </Typography>
                        <Typography variant="body1" fontWeight={500}>
                            {record.customer_name || '未提供'}
                        </Typography>
                    </Box>
                    
                    {record.customer_email && (
                        <Box>
                            <Typography variant="subtitle2" color="text.secondary">
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
                            <Typography variant="subtitle2" color="text.secondary">
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
            <Typography variant="subtitle2" color="text.secondary">
                标签
            </Typography>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                {tags.map((tag) => (
                    <Chip key={tag} label={tag} size="small" variant="outlined" />
                ))}
            </Stack>
        </Box>
    );
};

/**
 * 时间跟踪卡片
 */
const TimeTrackingCard: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Card>
            <CardHeader
                title="时间跟踪"
                avatar={<TimeIcon color="primary" />}
            />
            <CardContent>
                <Stack spacing={2}>
                    <Box>
                        <Typography variant="subtitle2" color="text.secondary">
                            创建时间
                        </Typography>
                        <Typography variant="body1">
                            {new Date(record.created_at).toLocaleString('zh-CN')}
                        </Typography>
                    </Box>
                    
                    {record.due_date && (
                        <Box>
                            <Typography variant="subtitle2" color="text.secondary">
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
                            <Typography variant="subtitle2" color="text.secondary">
                                首次回复时间
                            </Typography>
                            <Typography variant="body1">
                                {new Date(record.first_reply_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}
                    
                    {record.resolved_at && (
                        <Box>
                            <Typography variant="subtitle2" color="text.secondary">
                                解决时间
                            </Typography>
                            <Typography variant="body1" color="success.main">
                                {new Date(record.resolved_at).toLocaleString('zh-CN')}
                            </Typography>
                        </Box>
                    )}
                    
                    {record.closed_at && (
                        <Box>
                            <Typography variant="subtitle2" color="text.secondary">
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
        <Card>
            <CardHeader
                title="SLA信息"
                avatar={<ScheduleIcon color="primary" />}
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
                            <Typography variant="subtitle2" color="text.secondary">
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
                            <Typography variant="subtitle2" color="text.secondary">
                                响应时间
                            </Typography>
                            <Typography variant="body1">
                                {record.response_time} 分钟
                            </Typography>
                        </Box>
                    )}
                    
                    {record.resolution_time && (
                        <Box>
                            <Typography variant="subtitle2" color="text.secondary">
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
const TicketShowActions = () => (
    <TopToolbar>
        <ListButton label="返回列表" />
        <EditButton label="编辑" />
        <DeleteButton label="删除" />
    </TopToolbar>
);

/**
 * 工单详情页面
 */
const TicketShow: React.FC = () => {
    return (
        <Show actions={<TicketShowActions />} title="工单详情">
            <Box sx={{ p: 0 }}>
                <TicketHeader />
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
                                    <Card>
                                        <CardHeader title="工单描述" />
                                        <CardContent>
                                            <RichTextField source="description" />
                                        </CardContent>
                                    </Card>
                                    
                                    {/* 基本信息 */}
                                    <Card>
                                        <CardHeader title="基本信息" />
                                        <CardContent>
                                            <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" color="text.secondary">
                                                        类型
                                                    </Typography>
                                                    <SelectField source="type" choices={typeChoices} />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" color="text.secondary">
                                                        来源
                                                    </Typography>
                                                    <SelectField source="source" choices={sourceChoices} />
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" color="text.secondary">
                                                        创建人
                                                    </Typography>
                                                    <ReferenceField source="created_by_id" reference="users">
                                                        <TextField source="username" />
                                                    </ReferenceField>
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" color="text.secondary">
                                                        分配给
                                                    </Typography>
                                                    <ReferenceField source="assigned_to_id" reference="users" emptyText="未分配">
                                                        <TextField source="username" />
                                                    </ReferenceField>
                                                </Box>
                                                
                                                <Box sx={{ flex: 1, minWidth: '150px' }}>
                                                    <Typography variant="subtitle2" color="text.secondary">
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
                        <ReferenceManyField
                            reference="comments"
                            target="ticket_id"
                            label="评论"
                            perPage={20}
                            sort={{ field: 'created_at', order: 'DESC' }}
                        >
                            <Datagrid bulkActionButtons={false}>
                                <TextField source="content" label="内容" />
                                <ReferenceField source="user_id" reference="users" label="用户">
                                    <TextField source="username" />
                                </ReferenceField>
                                <DateField 
                                    source="created_at" 
                                    label="创建时间" 
                                    showTime 
                                    locales="zh-CN"
                                />
                                <BooleanField source="is_internal" label="内部评论" />
                            </Datagrid>
                        </ReferenceManyField>
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
                            <Datagrid bulkActionButtons={false}>
                                <TextField source="action" label="操作" />
                                <TextField source="field_name" label="字段" />
                                <TextField source="old_value" label="原值" />
                                <TextField source="new_value" label="新值" />
                                <ReferenceField source="user_id" reference="users" label="用户">
                                    <TextField source="username" />
                                </ReferenceField>
                                <DateField 
                                    source="created_at" 
                                    label="时间" 
                                    showTime 
                                    locales="zh-CN"
                                />
                            </Datagrid>
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
