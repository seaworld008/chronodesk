import React from 'react';
import {
    List,
    DatagridConfigurable,
    DatagridHeader,
    DateField,
    ReferenceField,
    FunctionField,
    EditButton,
    ShowButton,
    FilterButton,
    CreateButton,
    ExportButton,
    SelectField,
    TopToolbar,
    WrapperField,
    SelectColumnsButton,
    useRecordContext,
    useRedirect,
    useGetIdentity,
    usePermissions,
} from 'react-admin';
import { Box, Chip, Typography, Tooltip, IconButton, type ChipProps } from '@mui/material';
import {
    Assignment,
    TrendingUp,
    Schedule,
    Warning,
    PriorityHigh,
    CheckCircle,
    Timer,
    PersonAdd,
    ArrowUpward,
    SwapHoriz,
} from '@mui/icons-material';
import TicketBulkUpdateButton from './TicketBulkUpdateButton';
import { parseTagsToArray } from './tagUtils';
import { Ticket } from '@/types';
import {
    canDeleteTicket,
    canEditTicket,
    canUseTicketWorkflow,
    type TicketRolePermissions,
} from './ticketAccess';
import {
    InlineDetails,
    PersistentResizableDatagridHeader,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable';
import { EnterpriseSearchInput } from '@/components/inputs/EnterpriseSearchInput';
import {
    EnterpriseBooleanFilterInput,
    EnterpriseSelectFilterInput,
    EnterpriseTextFilterInput,
} from '@/components/inputs/EnterpriseFilterInputs';
import { FocusSafeBulkDeleteWithConfirmButton } from '@/components/actions/FocusSafeDeleteButtons';
import { hasProjectCapability } from '@/lib/projectScope';

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

const ticketColumnDefaults: ResizableColumn[] = [
    { key: 'ticket_number', defaultWidth: 260, minWidth: 180, maxWidth: 480 },
    { key: 'status', defaultWidth: 108, minWidth: 88, maxWidth: 180 },
    { key: 'priority', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
    { key: 'type', defaultWidth: 104, minWidth: 80, maxWidth: 180 },
    { key: '标签', defaultWidth: 180, minWidth: 112, maxWidth: 360 },
    { key: 'assigned_to_id', defaultWidth: 152, minWidth: 112, maxWidth: 280 },
    { key: 'customer_name', defaultWidth: 168, minWidth: 112, maxWidth: 320 },
    { key: 'sla_due_date', defaultWidth: 132, minWidth: 104, maxWidth: 220 },
    { key: 'category_id', defaultWidth: 152, minWidth: 112, maxWidth: 280 },
    { key: 'created_at', defaultWidth: 148, minWidth: 128, maxWidth: 240 },
    { key: 'due_date', defaultWidth: 148, minWidth: 128, maxWidth: 240 },
    { key: '操作', defaultWidth: 196, minWidth: 176, maxWidth: 240, sticky: 'right' },
];

const TicketDatagridHeader: React.FC<React.ComponentProps<typeof DatagridHeader>> = (props) => (
    <PersistentResizableDatagridHeader
        {...props}
        tableId="tickets.main"
        columnDefaults={ticketColumnDefaults}
    />
);

/**
 * 增强的优先级显示组件
 */
const EnhancedPriorityField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const getPriorityConfig = (priority: string) => {
        switch (priority) {
            case 'critical':
                // 深红背景，白字
                return {
                    bgColor: '#d32f2f',
                    color: '#ffffff',
                    icon: <Warning sx={{ color: '#ffffff !important' }} />
                };
            case 'urgent':
                // 深橙背景，白字
                return {
                    bgColor: '#ed6c02',
                    color: '#ffffff',
                    icon: <PriorityHigh sx={{ color: '#ffffff !important' }} />
                };
            case 'high':
                // 琥珀色背景，深灰字
                return {
                    bgColor: '#ffca28',
                    color: '#263238',
                    icon: <TrendingUp sx={{ color: '#263238 !important' }} />
                };
            case 'normal':
                // 浅蓝背景，蓝字（保持清爽）
                return {
                    bgColor: '#e3f2fd',
                    color: '#1565c0',
                    icon: <Assignment sx={{ color: '#1565c0 !important' }} />
                };
            case 'low':
                // 浅灰背景，灰字
                return {
                    bgColor: '#f5f5f5',
                    color: '#616161',
                    icon: <CheckCircle sx={{ color: '#616161 !important' }} />
                };
            default:
                return {
                    bgColor: '#f5f5f5',
                    color: '#757575',
                    icon: <Assignment />
                };
        }
    };

    const config = getPriorityConfig(record.priority);
    const label = priorityChoices.find((p) => p.id === record.priority)?.name || '未知优先级';

    return (
        <Chip
            icon={config.icon}
            label={label}
            size="small"
            sx={{
                backgroundColor: config.bgColor,
                color: config.color,
                fontWeight: 'bold',
                borderRadius: '6px', // Slightly rounded square
                height: '24px',
                '& .MuiChip-label': {
                    paddingLeft: '8px',
                    paddingRight: '8px',
                },
                '& .MuiChip-icon': {
                    color: 'inherit',
                    marginLeft: '4px',
                },
                boxShadow: record.priority === 'critical' ? '0 2px 4px rgba(211, 47, 47, 0.2)' : 'none',
            }}
        />
    );
};

const TicketTagsField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    const tags = parseTagsToArray(record.tags);
    if (!tags.length) {
        return (
            <Typography variant="body2" sx={{
                color: "text.secondary"
            }}>--</Typography>
        );
    }

    return (
        <Tooltip title={tags.join('、')} enterDelay={500}>
            <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'nowrap', overflow: 'hidden' }}>
                {tags.map((tag) => (
                    <Chip key={tag} label={tag} size="small" color="default" variant="outlined" />
                ))}
            </Box>
        </Tooltip>
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
                return { color: 'default', label: '未知状态' };
        }
    };

    const { color, label } = getStatusConfig(record.status);

    return (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <Chip label={label} color={color} size="small" variant="filled" />
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

    const number = `#${record.ticket_number}`;
    return (
        <InlineDetails
            primary={number}
            secondary={record.title}
            title={`${number} · ${record.title}`}
        />
    );
};

/**
 * 分配信息组件
 */
const AssignmentField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    if (!record) return null;

    if (record.assigned_to_actor?.type === 'service_principal') {
        return (
            <Tooltip title={record.assigned_to_actor.id}>
                <Chip
                    label={`AI 智能体 · ${record.assigned_to_actor.id}`}
                    color="info"
                    size="small"
                    variant="outlined"
                />
            </Tooltip>
        );
    }

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
            <TruncatedText title={record.assigned_to.username}>
                {record.assigned_to.username}
            </TruncatedText>
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
        return (
            <Typography variant="caption" sx={{
                color: "text.secondary"
            }}>无 SLA</Typography>
        );
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
 * 快捷操作按钮组件
 */
const QuickActionsField: React.FC = () => {
    const record = useRecordContext<Ticket>();
    const redirect = useRedirect();
    const { permissions } = usePermissions<TicketRolePermissions>();
    const { identity } = useGetIdentity();

    if (!record) return null;
    const canEdit = canEditTicket(
        record,
        permissions?.project_role,
        identity?.id,
    );
    const canUseWorkflow = canUseTicketWorkflow(
        record,
        permissions?.project_role,
        identity?.id,
    );

    const openWorkflow = (e: React.MouseEvent) => {
        e.stopPropagation();
        redirect('show', 'tickets', record.id);
    };

    return (
        <Box className="cd-table-actions" sx={{ alignItems: 'center' }}>
            {canUseWorkflow && (
                <>
                    <Tooltip title="在详情页分配工单">
                        <IconButton
                            size="small"
                            aria-label="在详情页分配工单"
                            onClick={openWorkflow}
                            color="primary"
                        >
                            <PersonAdd fontSize="small" />
                        </IconButton>
                    </Tooltip>
                    <Tooltip title="在详情页升级工单">
                        <IconButton
                            size="small"
                            aria-label="在详情页升级工单"
                            onClick={openWorkflow}
                            color="warning"
                        >
                            <ArrowUpward fontSize="small" />
                        </IconButton>
                    </Tooltip>
                    <Tooltip title="在详情页变更状态">
                        <IconButton
                            size="small"
                            aria-label="在详情页变更状态"
                            onClick={openWorkflow}
                            color="success"
                        >
                            <SwapHoriz fontSize="small" />
                        </IconButton>
                    </Tooltip>
                </>
            )}
            <ShowButton label="查看" />
            {canEdit && <EditButton label="编辑" />}
        </Box>
    );
};

/**
 * 过滤器组件
 */
const TicketFilters = [
    <EnterpriseSearchInput source="q" placeholder="搜索工单" alwaysOn />,
    <EnterpriseSelectFilterInput source="status" label="状态" choices={statusChoices} />,
    <EnterpriseSelectFilterInput source="priority" label="优先级" choices={priorityChoices} />,
    <EnterpriseSelectFilterInput source="type" label="类型" choices={typeChoices} />,
    <EnterpriseTextFilterInput source="tags" label="标签" placeholder="支持逗号分隔多个标签" />,
    <EnterpriseBooleanFilterInput source="is_overdue" label="已逾期" />,
    <EnterpriseBooleanFilterInput source="sla_breached" label="SLA 违约" />,
    <EnterpriseBooleanFilterInput source="unassigned" label="未分配" />,
];

/**
 * 列表操作工具栏
 */
const TicketListActions = () => {
    const { permissions } = usePermissions<TicketRolePermissions>();
    const canCreate = hasProjectCapability(
        permissions?.project_role,
        'create_ticket',
    );
    return (
        <TopToolbar>
            <SelectColumnsButton />
            <FilterButton />
            {canCreate && <CreateButton label="创建工单" />}
            <ExportButton label="导出" />
        </TopToolbar>
    );
};

/**
 * 批量操作按钮
 */
const TicketBulkActionButtons = () => (
    <>
        <TicketBulkUpdateButton />
        <FocusSafeBulkDeleteWithConfirmButton label="批量删除" mutationMode="pessimistic" />
    </>
);

/**
 * 空状态组件
 */
const TicketListEmpty = () => {
    const { permissions } = usePermissions<TicketRolePermissions>();
    const canCreate = hasProjectCapability(
        permissions?.project_role,
        'create_ticket',
    );
    return (
        <Box sx={{ textAlign: 'center', mt: 4 }}>
            <Assignment sx={{ fontSize: 64, color: 'text.secondary', mb: 2 }} />
            <Typography variant="h5" component="h2" gutterBottom>
                暂无工单
            </Typography>
            <Typography variant="body1" sx={{ color: 'text.secondary' }}>
                {canCreate
                    ? '创建第一个工单开始管理客户请求'
                    : '当前项目职责仅允许查看工单'}
            </Typography>
            {canCreate && <CreateButton label="创建工单" sx={{ mt: 2 }} />}
        </Box>
    );
};

/**
 * 增强的工单列表组件
 * 集成工作流操作和智能显示
 */
const TicketListEnhanced: React.FC = () => {
    const { permissions } = usePermissions<TicketRolePermissions>();
    const canBulkManage = canDeleteTicket(permissions?.project_role);

    return (
        <List
            filters={TicketFilters}
            actions={<TicketListActions />}
            empty={<TicketListEmpty />}
            perPage={25}
            sort={{ field: 'created_at', order: 'DESC' }}
            title="工单管理"
        >
            <DatagridConfigurable
                aria-label="工单列表"
                bulkActionButtons={canBulkManage ? <TicketBulkActionButtons /> : false}
                rowClick="show"
                header={TicketDatagridHeader}
                className="cd-enterprise-table cd-enterprise-datagrid"
                sx={{
                    width: '100%',
                    maxWidth: '100%',
                    minWidth: 0,
                    '& .RaDatagrid-tableWrapper': {
                        contain: 'inline-size',
                        display: 'block',
                        width: '100%',
                        maxWidth: '100%',
                        minWidth: 0,
                        overflowX: 'auto',
                    },
                    '& .RaDatagrid-table': {
                        tableLayout: 'fixed',
                        minWidth: '100%',
                        borderCollapse: 'separate',
                    },
                    '& .RaDatagrid-row:hover': {
                        backgroundColor: '#f8fafc',
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
                <SelectField source="type" label="类型" choices={typeChoices} />

                {/* 标签 */}
                <WrapperField label="标签">
                    <TicketTagsField />
                </WrapperField>

                {/* 分配信息 */}
                <WrapperField label="分配给" sortBy="assigned_to_id">
                    <AssignmentField />
                </WrapperField>

                {/* 客户信息 */}
                <FunctionField<Ticket>
                    source="customer_name"
                    label="客户"
                    render={(record) => (
                        <TruncatedText title={record?.customer_name || '—'}>
                            {record?.customer_name || '—'}
                        </TruncatedText>
                    )}
                />

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
                    <FunctionField<{ name?: string }>
                        render={(record) => (
                            <TruncatedText title={record?.name || '—'}>
                                {record?.name || '—'}
                            </TruncatedText>
                        )}
                    />
                </ReferenceField>

                {/* 创建时间 */}
                <DateField
                    source="created_at"
                    label="创建"
                    showTime
                    locales="zh-CN"
                    options={{ month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }}
                />

                {/* 截止时间 */}
                <DateField
                    source="due_date"
                    label="截止"
                    emptyText="-"
                    locales="zh-CN"
                    options={{ month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }}
                />

                {/* 操作 */}
                <WrapperField
                    label="操作"
                    cellClassName="cd-table-sticky-right"
                    headerClassName="cd-table-sticky-right"
                >
                    <QuickActionsField />
                </WrapperField>
            </DatagridConfigurable>
        </List>
    );
};

export default TicketListEnhanced;
