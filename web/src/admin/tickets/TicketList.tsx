import React from 'react';
import {
    List,
    Datagrid,
    TextField,
    DateField,
    SelectField,
    ReferenceField,
    EditButton,
    ShowButton,
    DeleteButton,
    CreateButton,
    ExportButton,
    FilterButton,
    SearchInput,
    SelectInput,
    DateInput,
    ReferenceInput,
    AutocompleteInput,
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
} from '@mui/material';
import {
    PriorityHigh as PriorityIcon,
} from '@mui/icons-material';

// 工单状态选项
const statusChoices = [
    { id: 'open', name: '待处理' },
    { id: 'in_progress', name: '处理中' },
    { id: 'pending', name: '等待中' },
    { id: 'resolved', name: '已解决' },
    { id: 'closed', name: '已关闭' },
    { id: 'cancelled', name: '已取消' },
];

// 优先级选项
const priorityChoices = [
    { id: 'low', name: '低' },
    { id: 'normal', name: '普通' },
    { id: 'high', name: '高' },
    { id: 'urgent', name: '紧急' },
    { id: 'critical', name: '严重' },
];

// 工单类型选项
const typeChoices = [
    { id: 'incident', name: '事件' },
    { id: 'request', name: '请求' },
    { id: 'problem', name: '问题' },
    { id: 'change', name: '变更' },
    { id: 'complaint', name: '投诉' },
    { id: 'consultation', name: '咨询' },
];

// 来源选项
const sourceChoices = [
    { id: 'web', name: '网页' },
    { id: 'email', name: '邮件' },
    { id: 'phone', name: '电话' },
    { id: 'chat', name: '聊天' },
    { id: 'api', name: 'API' },
    { id: 'mobile', name: '移动端' },
];

/**
 * 状态标签组件
 */
const StatusChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getStatusColor = (status: string) => {
        switch (status) {
            case 'open':
                return { color: '#2563eb', backgroundColor: '#eff6ff' }; // blue
            case 'in_progress':
                return { color: '#d97706', backgroundColor: '#fefce8' }; // yellow
            case 'pending':
                return { color: '#7c3aed', backgroundColor: '#f3e8ff' }; // purple
            case 'resolved':
                return { color: '#059669', backgroundColor: '#f0fdf4' }; // green
            case 'closed':
                return { color: '#64748b', backgroundColor: '#f8fafc' }; // gray
            case 'cancelled':
                return { color: '#dc2626', backgroundColor: '#fef2f2' }; // red
            default:
                return { color: '#64748b', backgroundColor: '#f8fafc' };
        }
    };

    const { color, backgroundColor } = getStatusColor(record.status);
    const statusName = statusChoices.find(s => s.id === record.status)?.name || record.status;

    return (
        <Chip
            size="small"
            label={statusName}
            sx={{
                color,
                backgroundColor,
                fontWeight: 500,
                fontSize: '0.75rem',
            }}
        />
    );
};

/**
 * 优先级标签组件
 */
const PriorityChip: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    const getPriorityColor = (priority: string) => {
        switch (priority) {
            case 'critical':
                return { color: '#dc2626', backgroundColor: '#fef2f2' }; // red
            case 'urgent':
                return { color: '#ea580c', backgroundColor: '#fff7ed' }; // orange
            case 'high':
                return { color: '#d97706', backgroundColor: '#fefce8' }; // yellow
            case 'normal':
                return { color: '#2563eb', backgroundColor: '#eff6ff' }; // blue
            case 'low':
                return { color: '#059669', backgroundColor: '#f0fdf4' }; // green
            default:
                return { color: '#64748b', backgroundColor: '#f8fafc' };
        }
    };

    const { color, backgroundColor } = getPriorityColor(record.priority);
    const priorityName = priorityChoices.find(p => p.id === record.priority)?.name || record.priority;

    return (
        <Chip
            size="small"
            label={priorityName}
            icon={<PriorityIcon sx={{ fontSize: '0.8rem' }} />}
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
 * 工单编号组件
 */
const TicketNumber: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Typography variant="body2" fontWeight={600} color="primary">
                #{record.ticket_number}
            </Typography>
        </Box>
    );
};

/**
 * 客户信息组件
 */
const CustomerInfo: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    return (
        <Box>
            <Typography variant="body2" fontWeight={500}>
                {record.customer_name || '未知客户'}
            </Typography>
            {record.customer_email && (
                <Typography variant="caption" color="text.secondary">
                    {record.customer_email}
                </Typography>
            )}
        </Box>
    );
};

/**
 * SLA状态组件
 */
const SLAStatus: React.FC = () => {
    const record = useRecordContext();
    if (!record) return null;

    if (record.sla_breached) {
        return (
            <Chip
                size="small"
                label="SLA违反"
                color="error"
                sx={{ fontSize: '0.75rem' }}
            />
        );
    }

    if (record.sla_due_date) {
        const dueDate = new Date(record.sla_due_date);
        const now = new Date();
        const timeDiff = dueDate.getTime() - now.getTime();
        const hoursLeft = Math.floor(timeDiff / (1000 * 3600));

        if (hoursLeft < 0) {
            return (
                <Chip
                    size="small"
                    label="已逾期"
                    color="error"
                    sx={{ fontSize: '0.75rem' }}
                />
            );
        } else if (hoursLeft < 24) {
            return (
                <Chip
                    size="small"
                    label={`${hoursLeft}小时内`}
                    color="warning"
                    sx={{ fontSize: '0.75rem' }}
                />
            );
        }
    }

    return (
        <Chip
            size="small"
            label="正常"
            color="success"
            sx={{ fontSize: '0.75rem' }}
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
    <SelectInput source="source" label="来源" choices={sourceChoices} />,
    <ReferenceInput source="assigned_to_id" reference="users" label="分配给">
        <AutocompleteInput optionText="username" />
    </ReferenceInput>,
    <ReferenceInput source="created_by_id" reference="users" label="创建人">
        <AutocompleteInput optionText="username" />
    </ReferenceInput>,
    <DateInput source="created_at_gte" label="创建时间从" />,
    <DateInput source="created_at_lte" label="创建时间到" />,
    <DateInput source="due_date_lte" label="截止时间" />,
];

/**
 * 列表工具栏
 */
const TicketListActions = () => (
    <TopToolbar>
        <FilterButton />
        <CreateButton label="创建工单" />
        <ExportButton label="导出" />
    </TopToolbar>
);

/**
 * 空状态组件
 */
const TicketEmpty = () => (
    <Box sx={{ textAlign: 'center', mt: 4 }}>
        <Card sx={{ maxWidth: 600, mx: 'auto', p: 4 }}>
            <CardContent>
                <Typography variant="h5" component="h2" gutterBottom>
                    还没有工单
                </Typography>
                <Typography variant="body1" color="text.secondary" paragraph>
                    系统中暂时没有任何工单。创建第一个工单来开始使用工单管理系统。
                </Typography>
                <CreateButton label="创建第一个工单" variant="contained" />
            </CardContent>
        </Card>
    </Box>
);

/**
 * 工单列表组件
 * 采用React Admin的专业数据表格展示
 */
const TicketList: React.FC = () => {
    return (
        <List
            filters={TicketFilters}
            actions={<TicketListActions />}
            empty={<TicketEmpty />}
            perPage={25}
            sort={{ field: 'created_at', order: 'DESC' }}
            title="工单管理"
        >
            <Datagrid
                rowClick="show"
                bulkActionButtons={
                    <>
                        <DeleteButton label="批量删除" />
                    </>
                }
                sx={{
                    '& .RaDatagrid-table': {
                        '& .RaDatagrid-tbody .RaDatagrid-row:hover': {
                            backgroundColor: '#f8fafc',
                        },
                    },
                }}
            >
                {/* 工单编号 */}
                <WrapperField label="工单编号" sortBy="ticket_number">
                    <TicketNumber />
                </WrapperField>

                {/* 标题 */}
                <TextField 
                    source="title" 
                    label="标题"
                    sx={{ 
                        maxWidth: '300px',
                        '& .MuiTableCell-root': {
                            maxWidth: '300px',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                        }
                    }}
                />

                {/* 状态 */}
                <WrapperField label="状态" sortBy="status">
                    <StatusChip />
                </WrapperField>

                {/* 优先级 */}
                <WrapperField label="优先级" sortBy="priority">
                    <PriorityChip />
                </WrapperField>

                {/* 类型 */}
                <SelectField 
                    source="type" 
                    label="类型" 
                    choices={typeChoices}
                    sx={{ minWidth: '80px' }}
                />

                {/* 创建人 */}
                <ReferenceField source="created_by_id" reference="users" label="创建人">
                    <TextField source="username" />
                </ReferenceField>

                {/* 分配给 */}
                <ReferenceField source="assigned_to_id" reference="users" label="分配给" emptyText="未分配">
                    <TextField source="username" />
                </ReferenceField>

                {/* 客户信息 */}
                <WrapperField label="客户">
                    <CustomerInfo />
                </WrapperField>

                {/* SLA状态 */}
                <WrapperField label="SLA" sortBy="sla_breached">
                    <SLAStatus />
                </WrapperField>

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

                {/* 截止时间 */}
                <DateField 
                    source="due_date" 
                    label="截止时间"
                    emptyText="无"
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

                {/* 操作按钮 */}
                <Stack direction="row" spacing={1}>
                    <ShowButton label="查看" />
                    <EditButton label="编辑" />
                </Stack>
            </Datagrid>
        </List>
    );
};

export default TicketList;