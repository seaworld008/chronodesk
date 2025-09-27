import React from 'react';
import {
    List,
    Datagrid,
    TextField,
    DateField,
    ReferenceField,
    EditButton,
    ShowButton,
    FilterButton,
    CreateButton,
    ExportButton,
    SearchInput,
    SelectInput,
    DateInput,
    BooleanInput,
    TextInput,
    TopToolbar,
    BulkDeleteWithConfirmButton,
    WrapperField,
    useRecordContext,
} from 'react-admin';
import { Box, Chip, Typography, Tooltip, type ChipProps } from '@mui/material';
import {
    Assignment,
    TrendingUp,
    Schedule,
    Warning,
    PriorityHigh,
    CheckCircle,
    Timer,
} from '@mui/icons-material';
import TicketWorkflowActions from './TicketWorkflowActions';
import TicketBulkUpdateButton from './TicketBulkUpdateButton';
import { parseTagsToArray } from './tagUtils';
import { Ticket } from '@/types';

type PriorityConfig = {
    color: ChipProps['color'];
    icon: React.ReactNode;
    bgColor: string;
};

// 过滤器选项
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

/**
 * 增强的优先级显示组件
 */
const EnhancedPriorityField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const getPriorityConfig = (priority: string): PriorityConfig => {
        switch (priority) {
            case 'critical':
                return { color: 'error', icon: <Warning />, bgColor: '#ffebee' };
            case 'urgent':
                return { color: 'error', icon: <PriorityHigh />, bgColor: '#fff3e0' };
            case 'high':
                return { color: 'warning', icon: <TrendingUp />, bgColor: '#fff8e1' };
            case 'normal':
                return { color: 'primary', icon: <Assignment />, bgColor: '#e3f2fd' };
            case 'low':
                return { color: 'default', icon: <CheckCircle />, bgColor: '#f5f5f5' };
            default:
                return { color: 'default', icon: <Assignment />, bgColor: '#f5f5f5' };
        }
    };

    const { color, icon, bgColor } = getPriorityConfig(record.priority);
    const label = priorityChoices.find((p) => p.id === record.priority)?.name || record.priority;

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            {icon}
            <Chip label={label} color={color} size="small" sx={{ backgroundColor: bgColor, fontWeight: 500 }} />
        </Box>
    );
};

const TicketTagsField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const tags = parseTagsToArray(record.tags);
    if (!tags.length) {
        return <Typography variant="body2" color="text.secondary">--</Typography>;
    }

    return (
        <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
            {tags.map((tag) => (
                <Chip key={tag} label={tag} size="small" color="default" variant="outlined" />
            ))}
        </Box>
    );
};

/**
 * 增强的状态显示组件
 */
const EnhancedStatusField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const getStatusConfig = (status: string): { color: ChipProps['color']; label: string } => {
        switch (status) {
            case 'open':
                return { color: 'warning', label: '待处理' };
            case 'in_progress':
                return { color: 'primary', label: '处理中' };
            case 'pending':
                return { color: 'secondary', label: '等待中' };
            case 'resolved':
                return { color: 'success', label: '已解决' };
            case 'closed':
                return { color: 'default', label: '已关闭' };
            case 'cancelled':
                return { color: 'error', label: '已取消' };
            default:
                return { color: 'default', label: status };
        }
    };

    const { color, label } = getStatusConfig(record.status);

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Chip label={label} color={color} size="small" variant="filled" />
            {record.is_overdue && (
                <Tooltip title="工单已逾期">
                    <Timer color="error" fontSize="small" />
                </Tooltip>
            )}
            {record.sla_breached && (
                <Tooltip title="SLA已违约">
                    <Warning color="error" fontSize="small" />
                </Tooltip>
            )}
        </Box>
    );
};

/**
 * 工单标题和编号组件
 */
const TicketTitleField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    return (
        <Box sx={{ maxWidth: 300 }}>
            <Typography variant="body2" fontWeight={600} sx={{ mb: 0.5 }}>
                #{record.ticket_number}
            </Typography>
            <Typography 
                variant="body2" 
                sx={{
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                }}
                title={record.title}
            >
                {record.title}
            </Typography>
        </Box>
    );
};

/**
 * 分配信息组件
 */
const AssignmentField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    if (!record.assigned_to) {
        return (
            <Chip
                label="未分配"
                color="warning"
                size="small"
                variant="outlined"
            />
        );
    }

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Assignment fontSize="small" color="primary" />
            <Typography variant="body2">
                {record.assigned_to.username}
            </Typography>
        </Box>
    );
};

/**
 * SLA 状态组件
 */
const SLAStatusField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    if (!record.sla_due_date) {
        return <Typography variant="caption" color="text.secondary">无 SLA</Typography>;
    }

    const now = new Date();
    const slaDate = new Date(record.sla_due_date);
    const hoursLeft = Math.ceil((slaDate.getTime() - now.getTime()) / (1000 * 3600));

    if (record.sla_breached) {
        return (
            <Chip
                label="SLA违约"
                color="error"
                size="small"
                icon={<Warning />}
            />
        );
    }

    if (hoursLeft <= 0) {
        return (
            <Chip
                label="已逾期"
                color="error"
                size="small"
                icon={<Timer />}
            />
        );
    }

    const color = hoursLeft <= 4 ? 'error' : hoursLeft <= 8 ? 'warning' : 'success';
    
    return (
        <Chip
            label={`${hoursLeft}小时内`}
            color={color}
            size="small"
            icon={<Schedule />}
        />
    );
};

/**
 * 过滤器组件
 */
const TicketFilters = [
    <SearchInput source="q" placeholder="搜索工单" alwaysOn />,
    <SelectInput source="status" label="状态" choices={statusChoices} />,
    <SelectInput source="priority" label="优先级" choices={priorityChoices} />,
    <SelectInput source="type" label="类型" choices={typeChoices} />,
    <TextInput source="tags" label="标签" placeholder="支持逗号分隔多个标签" />,
    <BooleanInput source="is_overdue" label="已逾期" />,
    <BooleanInput source="sla_breached" label="SLA违约" />,
    <BooleanInput source="unassigned" label="未分配" />,
    <DateInput source="created_at_gte" label="创建时间从" />,
    <DateInput source="created_at_lte" label="创建时间到" />,
    <DateInput source="due_date_lte" label="截止时间到" />,
];

/**
 * 列表操作工具栏
 */
const TicketListActions = () => (
    <TopToolbar>
        <FilterButton />
        <CreateButton label="创建工单" />
        <ExportButton label="导出" />
    </TopToolbar>
);

/**
 * 批量操作按钮
 */
const TicketBulkActionButtons = () => (
    <>
        <TicketBulkUpdateButton />
        <BulkDeleteWithConfirmButton label="批量删除" />
    </>
);

/**
 * 空状态组件
 */
const TicketListEmpty = () => (
    <Box sx={{ textAlign: 'center', mt: 4 }}>
        <Assignment sx={{ fontSize: 64, color: 'text.secondary', mb: 2 }} />
        <Typography variant="h5" component="h2" gutterBottom>
            暂无工单
        </Typography>
        <Typography variant="body1" color="text.secondary">
            创建第一个工单开始管理客户请求
        </Typography>
    </Box>
);

/**
 * 增强的工单列表组件
 * 集成工作流操作和智能显示
 */
const TicketListEnhanced: React.FC = () => {
    return (
        <List
            filters={TicketFilters}
            actions={<TicketListActions />}
            empty={<TicketListEmpty />}
            perPage={25}
            sort={{ field: 'created_at', order: 'DESC' }}
            title="工单管理"
        >
            <Datagrid
                bulkActionButtons={<TicketBulkActionButtons />}
                sx={{
                    '& .RaDatagrid-table': {
                        '& .RaDatagrid-tbody .RaDatagrid-row': {
                            '&:hover': {
                                backgroundColor: '#f8fafc',
                            },
                            // 高亮紧急工单
                            '&[data-record-priority="urgent"], &[data-record-priority="critical"]': {
                                backgroundColor: '#fef2f2',
                                '&:hover': {
                                    backgroundColor: '#fecaca',
                                },
                            },
                            // 高亮逾期工单
                            '&[data-record-is_overdue="true"]': {
                                backgroundColor: '#fff7ed',
                                '&:hover': {
                                    backgroundColor: '#fed7aa',
                                },
                            },
                        },
                    },
                }}
            >
                {/* 工单信息 */}
                <WrapperField label="工单信息" sortBy="ticket_number">
                    <TicketTitleField />
                </WrapperField>

                {/* 状态 */}
                <WrapperField label="状态" sortBy="status">
                    <EnhancedStatusField />
                </WrapperField>

                {/* 优先级 */}
                <WrapperField label="优先级" sortBy="priority">
                    <EnhancedPriorityField />
                </WrapperField>

                {/* 类型 */}
                <TextField source="type" label="类型" />

                {/* 标签 */}
                <WrapperField label="标签">
                    <TicketTagsField />
                </WrapperField>

                {/* 分配信息 */}
                <WrapperField label="分配给" sortBy="assigned_to_id">
                    <AssignmentField />
                </WrapperField>

                {/* 客户信息 */}
                <TextField source="customer_name" label="客户" emptyText="--" />

                {/* SLA状态 */}
                <WrapperField label="SLA状态" sortBy="sla_due_date">
                    <SLAStatusField />
                </WrapperField>

                {/* 分类 */}
                <ReferenceField 
                    source="category_id" 
                    reference="categories" 
                    label="分类"
                    emptyText="--"
                >
                    <TextField source="name" />
                </ReferenceField>

                {/* 创建时间 */}
                <DateField 
                    source="created_at" 
                    label="创建时间"
                    showTime
                    locales="zh-CN"
                    options={{
                        month: 'short',
                        day: 'numeric',
                        hour: '2-digit',
                        minute: '2-digit',
                    }}
                />

                {/* 截止时间 */}
                <DateField 
                    source="due_date" 
                    label="截止时间"
                    emptyText="--"
                    locales="zh-CN"
                    options={{
                        month: 'short',
                        day: 'numeric',
                        hour: '2-digit',
                        minute: '2-digit',
                    }}
                />

                {/* 工作流操作 */}
                <WrapperField label="操作">
                    <Box sx={{ display: 'flex', gap: 0.5 }}>
                        <TicketWorkflowActions />
                        <ShowButton label="" />
                        <EditButton label="" />
                    </Box>
                </WrapperField>
            </Datagrid>
        </List>
    );
};

export default TicketListEnhanced;
