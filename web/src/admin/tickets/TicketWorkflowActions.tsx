import React, { useEffect, useMemo, useState } from 'react';
import {
    Autocomplete,
    Button,
    CircularProgress,
    Dialog,
    DialogTitle,
    DialogContent,
    DialogActions,
    MenuItem,
    Select,
    FormControl,
    InputLabel,
    TextField,
    Box,
    Chip,
    Alert,
    Stepper,
    Step,
    StepLabel,
    Tooltip,
} from '@mui/material';
import {
    Assignment,
    SwapHoriz as Transfer,
    TrendingUp as Escalate,
    CheckCircle,
} from '@mui/icons-material';
import {
    useGetIdentity,
    useGetList,
    useNotify,
    usePermissions,
    useRecordContext,
    useRefresh,
} from 'react-admin';
import { Ticket, TicketPriority, TicketStatus } from '@/types';
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient';
import {
    humanApiRoutes,
    type AssignTicketRequest,
    type EscalateTicketRequest,
    type TicketAllowedTransitions,
    type TransferTicketRequest,
    type UpdateTicketStatusRequest,
} from '@/lib/generated/human-api';
import { resolveActiveProjectKey } from '@/lib/projectScope';
import {
    canAssignTicket,
    canUseTicketWorkflow,
    type TicketRolePermissions,
} from './ticketAccess';

// 状态中文映射
const STATUS_LABELS = {
    open: '待处理',
    in_progress: '处理中',
    pending: '等待中',
    resolved: '已解决',
    closed: '已关闭',
    cancelled: '已取消'
};

// 优先级升级规则
const PRIORITY_ESCALATION: Record<TicketPriority, TicketPriority> = {
    low: 'normal',
    normal: 'high', 
    high: 'urgent',
    urgent: 'critical',
    critical: 'critical'
};

const PRIORITY_LABELS: Record<TicketPriority, string> = {
    low: '低',
    normal: '普通',
    high: '高',
    urgent: '紧急',
    critical: '严重',
};

interface WorkflowAction {
    type: 'assign' | 'transfer' | 'escalate' | 'status_change' | 'priority_change';
    label: string;
    icon: React.ReactNode;
    color: 'primary' | 'secondary' | 'warning' | 'error' | 'success';
    requiresInput?: boolean;
}

type AssigneeOption = {
    id: number;
    username: string;
    first_name?: string;
    last_name?: string;
};

const assigneeOptionLabel = (option: AssigneeOption) => {
    const displayName = [option.first_name, option.last_name]
        .filter(Boolean)
        .join(' ')
        .trim();
    return displayName
        ? `${option.username}（${displayName}）`
        : option.username;
};

/**
 * 工单工作流操作组件
 * 提供分配、转移、升级、状态变更等工单流转功能
 */
const TicketWorkflowActions: React.FC = () => {
    const record = useRecordContext<Ticket>();
    const notify = useNotify();
    const refresh = useRefresh();
    const { permissions } = usePermissions<TicketRolePermissions>();
    const { identity } = useGetIdentity();
    
    const [dialogOpen, setDialogOpen] = useState(false);
    const [currentAction, setCurrentAction] = useState<WorkflowAction | null>(null);
    const [assignee, setAssignee] = useState<AssigneeOption | null>(null);
    const [assigneeSearch, setAssigneeSearch] = useState('');
    const [debouncedAssigneeSearch, setDebouncedAssigneeSearch] = useState('');
    const [transferDepartment, setTransferDepartment] = useState('');
    const [escalationReason, setEscalationReason] = useState('');
    const [newStatus, setNewStatus] = useState<TicketStatus | ''>('');
    const [comment, setComment] = useState('');
    const [allowedStatuses, setAllowedStatuses] =
        useState<TicketStatus[]>([]);
    const [workflowError, setWorkflowError] = useState('');
    const workflowAllowed = canUseTicketWorkflow(
        record ?? undefined,
        permissions?.project_role,
        identity?.id,
    );
    const { data: assignees = [], isPending: assigneesPending } = useGetList<AssigneeOption>(
        'assignees',
        {
            pagination: { page: 1, perPage: 25 },
            sort: { field: 'username', order: 'ASC' },
            filter: debouncedAssigneeSearch
                ? { q: debouncedAssigneeSearch }
                : {},
        },
        {
            enabled:
                dialogOpen &&
                currentAction !== null &&
                ['assign', 'transfer', 'escalate'].includes(currentAction.type),
        },
    );
    const assigneeOptions = useMemo(() => {
        if (
            assignee === null
            || assignees.some((candidate) => candidate.id === assignee.id)
        ) {
            return assignees;
        }
        return [assignee, ...assignees];
    }, [assignee, assignees]);
    const assigneeId = assignee?.id ?? null;

    useEffect(() => {
        const timer = window.setTimeout(
            () => setDebouncedAssigneeSearch(assigneeSearch.trim()),
            250,
        );
        return () => window.clearTimeout(timer);
    }, [assigneeSearch]);

    useEffect(() => {
        let active = true;
        setAllowedStatuses([]);
        setWorkflowError('');
        if (!record?.id || !workflowAllowed) {
            return () => {
                active = false;
            };
        }
        void resolveActiveProjectKey()
            .then((projectKey) => apiFetch<TicketAllowedTransitions>(
                humanApiRoutes.getProjectTicketAllowedTransitions({
                    projectKey,
                    ticketID: Number(record.id),
                }),
            ))
            .then((result) => {
                if (active) {
                    if (!Array.isArray(result.allowed_next_statuses)) {
                        throw new Error('服务端未返回合法工单状态');
                    }
                    setAllowedStatuses(result.allowed_next_statuses);
                }
            })
            .catch((error: unknown) => {
                if (!active) return;
                setWorkflowError(localizedUnknownErrorMessage(
                    error,
                    '加载工单绑定工作流失败',
                ));
            });
        return () => {
            active = false;
        };
    }, [record?.id, record?.version, workflowAllowed]);

    if (!record) return null;

    // 获取当前可执行的操作
    const getAvailableActions = (): WorkflowAction[] => {
        const actions: WorkflowAction[] = [];
        
        // 分配操作（未分配或重新分配）
        if (
            canAssign &&
            (!record.assigned_to_id || record.status === 'open')
        ) {
            actions.push({
                type: 'assign',
                label: record.assigned_to_id ? '重新分配' : '分配工单',
                icon: <Assignment />,
                color: 'primary',
                requiresInput: true
            });
        }

        // 转移操作（已分配的工单）
        if (
            canAssign &&
            record.assigned_to_id &&
            ['open', 'in_progress'].includes(record.status)
        ) {
            actions.push({
                type: 'transfer', 
                label: '转移工单',
                icon: <Transfer />,
                color: 'secondary',
                requiresInput: true
            });
        }

        // 升级操作
        if (
            canAssign &&
            ['open', 'in_progress', 'pending'].includes(record.status)
        ) {
            actions.push({
                type: 'escalate',
                label: '升级工单',
                icon: <Escalate />,
                color: 'warning',
                requiresInput: true
            });
        }

        // 状态变更
        if (canUseWorkflow && allowedStatuses.length > 0) {
            actions.push({
                type: 'status_change',
                label: '状态变更',
                icon: <CheckCircle />,
                color: 'success',
                requiresInput: true
            });
        }

        return actions;
    };

    // 执行工作流操作
    const executeAction = async () => {
        if (!currentAction || !record) return;

        try {
            if (!Number.isSafeInteger(record.version) || record.version <= 0) {
                throw new Error('工单版本信息缺失，请刷新页面后重试');
            }
            const pathParameters = {
                projectKey: await resolveActiveProjectKey(),
                ticketID: Number(record.id),
            };
            let endpoint = '';
            let payload:
                | AssignTicketRequest
                | TransferTicketRequest
                | EscalateTicketRequest
                | UpdateTicketStatusRequest;

            switch (currentAction.type) {
                case 'assign': {
                    if (!assigneeId) {
                        throw new Error('请选择工单负责人');
                    }
                    endpoint = humanApiRoutes.assignProjectTicket(pathParameters);
                    payload = {
                        assigned_to_id: assigneeId,
                        ...(comment ? { comment } : {}),
                    };
                    break;
                }
                case 'transfer': {
                    if (!assigneeId) {
                        throw new Error('请选择工单接收人');
                    }
                    endpoint = humanApiRoutes.transferProjectTicket(pathParameters);
                    payload = {
                        assigned_to_id: assigneeId,
                        ...(transferDepartment
                            ? { department: transferDepartment }
                            : {}),
                        ...(comment ? { comment } : {}),
                    };
                    break;
                }
                case 'escalate': {
                    if (!assigneeId) {
                        throw new Error('请选择升级接收人');
                    }
                    endpoint = humanApiRoutes.escalateProjectTicket(pathParameters);
                    payload = {
                        reason: escalationReason,
                        escalate_to_id: assigneeId,
                        ...(comment ? { comment } : {}),
                    };
                    break;
                }
                case 'status_change': {
                    if (!newStatus) {
                        throw new Error('请选择新状态');
                    }
                    endpoint =
                        humanApiRoutes.updateProjectTicketStatus(pathParameters);
                    payload = {
                        status: newStatus,
                        ...(comment ? { comment } : {}),
                    };
                    break;
                }
                default:
                    throw new Error('不支持的工单工作流操作');
            }

            await apiFetch(endpoint, {
                method: 'POST',
                body: JSON.stringify(payload),
                headers: {
                    'If-Match': `"v${record.version}"`,
                },
            });

            notify(`工单${currentAction.label}成功`, { type: 'success' });
            refresh();
            setDialogOpen(false);
            resetForm();
        } catch (error) {
            const message = localizedUnknownErrorMessage(
                error,
                `工单${currentAction.label}失败`,
            );
            notify(message, { type: 'error' });
        }
    };

    const resetForm = () => {
        setAssignee(null);
        setAssigneeSearch('');
        setDebouncedAssigneeSearch('');
        setTransferDepartment('');
        setEscalationReason('');
        setNewStatus('');
        setComment('');
        setCurrentAction(null);
    };

    const openDialog = (action: WorkflowAction) => {
        setCurrentAction(action);
        setDialogOpen(true);
    };

    const canUseWorkflow = workflowAllowed;
    const canAssign = canAssignTicket(
        record,
        permissions?.project_role,
        identity?.id,
    );
    const availableActions =
        canUseWorkflow || canAssign ? getAvailableActions() : [];
    const renderAssigneeSelector = (label: string) => (
        <Autocomplete
            fullWidth
            options={assigneeOptions}
            value={assignee}
            inputValue={assigneeSearch}
            loading={assigneesPending}
            filterOptions={(options) => options}
            getOptionLabel={assigneeOptionLabel}
            isOptionEqualToValue={(option, value) => option.id === value.id}
            onChange={(_, value) => {
                setAssignee(value);
                setAssigneeSearch(value ? assigneeOptionLabel(value) : '');
            }}
            onInputChange={(_, value, reason) => {
                if (reason === 'input' || reason === 'clear') {
                    setAssigneeSearch(value);
                }
            }}
            noOptionsText={
                debouncedAssigneeSearch
                    ? '未找到匹配的项目成员'
                    : '暂无可分配成员'
            }
            renderInput={(params) => (
                <TextField
                    {...params}
                    label={label}
                    helperText="输入姓名或用户名搜索；每次最多显示 25 项"
                    slotProps={{
                        ...params.slotProps,
                        htmlInput: {
                            ...params.slotProps.htmlInput,
                            'aria-label': label,
                        },
                        input: {
                            ...params.slotProps.input,
                            endAdornment: (
                                <>
                                    {assigneesPending && (
                                        <CircularProgress
                                            color="inherit"
                                            size={18}
                                            aria-label="正在搜索可分配成员"
                                        />
                                    )}
                                    {params.slotProps.input.endAdornment}
                                </>
                            ),
                        },
                    }}
                />
            )}
        />
    );

    return (
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
            {/* 工单状态显示 */}
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mr: 2 }}>
                <Chip 
                    label={STATUS_LABELS[record.status as keyof typeof STATUS_LABELS] || '未知状态'}
                    color={record.status === 'resolved' ? 'success' : 
                           record.status === 'closed' ? 'default' : 
                           record.status === 'cancelled' ? 'error' : 'warning'}
                    size="small"
                />
                {record.sla_breached && (
                    <Chip label="SLA违约" color="error" size="small" />
                )}
                {record.is_overdue && (
                    <Chip label="已逾期" color="error" size="small" />
                )}
                {!canUseWorkflow && !canAssign && (
                    <Chip
                        label="无工作流权限"
                        size="small"
                        variant="outlined"
                    />
                )}
                {canUseWorkflow && workflowError && (
                    <Chip
                        label="工作流加载失败"
                        color="error"
                        size="small"
                        variant="outlined"
                    />
                )}
            </Box>

            {/* 操作按钮 */}
            {availableActions.map((action, index) => (
                <Button
                    key={index}
                    size="small"
                    variant="outlined"
                    color={action.color}
                    startIcon={action.icon}
                    onClick={() => openDialog(action)}
                >
                    {action.label}
                </Button>
            ))}

            {/* 操作对话框 */}
            <Dialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                maxWidth="sm"
                fullWidth
            >
                <DialogTitle>
                    {currentAction?.label}
                </DialogTitle>
                <DialogContent>
                    {currentAction?.type === 'assign' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="info">
                                选择要分配给的用户。分配后该用户将成为工单的负责人。
                            </Alert>
                            {renderAssigneeSelector('分配给')}
                        </Box>
                    )}

                    {currentAction?.type === 'transfer' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="info">
                                将工单转移到其他部门或用户。转移历史将被记录。
                            </Alert>
                            <FormControl fullWidth>
                                <InputLabel id="ticket-transfer-department-label">
                                    目标部门
                                </InputLabel>
                                <Select
                                    labelId="ticket-transfer-department-label"
                                    label="目标部门"
                                    value={transferDepartment}
                                    onChange={(e) => setTransferDepartment(e.target.value)}
                                >
                                    <MenuItem value="technical">技术支持</MenuItem>
                                    <MenuItem value="sales">销售部</MenuItem>
                                    <MenuItem value="billing">财务部</MenuItem>
                                    <MenuItem value="management">管理层</MenuItem>
                                </Select>
                            </FormControl>
                            {renderAssigneeSelector('转移给')}
                        </Box>
                    )}

                    {currentAction?.type === 'escalate' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="warning">
                                升级工单将提高其优先级并通知上级。请说明升级原因。
                            </Alert>
                            <TextField
                                fullWidth
                                label="升级原因"
                                multiline
                                rows={3}
                                value={escalationReason}
                                onChange={(e) => setEscalationReason(e.target.value)}
                                required
                            />
                            {renderAssigneeSelector('升级给')}
                            <Box>
                                <Tooltip
                                    title={`优先级代码：${record.priority} → ${PRIORITY_ESCALATION[record.priority as keyof typeof PRIORITY_ESCALATION]}`}
                                >
                                    <strong>
                                        优先级将从 {PRIORITY_LABELS[record.priority as TicketPriority] || '未知'}
                                        {' '}升级为{' '}
                                        {PRIORITY_LABELS[PRIORITY_ESCALATION[record.priority as TicketPriority]] || '未知'}
                                    </strong>
                                </Tooltip>
                            </Box>
                        </Box>
                    )}

                    {currentAction?.type === 'status_change' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="info">
                                更改工单状态。状态变更将触发相应的自动化流程。
                            </Alert>
                            <FormControl fullWidth>
                                <InputLabel id="ticket-status-change-label">
                                    新状态
                                </InputLabel>
                                <Select
                                    labelId="ticket-status-change-label"
                                    label="新状态"
                                    value={newStatus}
                                    onChange={(e) => setNewStatus(e.target.value)}
                                >
                                    {allowedStatuses.map(status => (
                                        <MenuItem key={status} value={status}>
                                            {STATUS_LABELS[status as keyof typeof STATUS_LABELS]}
                                        </MenuItem>
                                    ))}
                                </Select>
                            </FormControl>

                            {/* 状态流程可视化 */}
                            <Stepper activeStep={-1} alternativeLabel>
                                {['open', 'in_progress', 'pending', 'resolved', 'closed'].map((step) => (
                                    <Step key={step} completed={false}>
                                        <StepLabel>{STATUS_LABELS[step as keyof typeof STATUS_LABELS]}</StepLabel>
                                    </Step>
                                ))}
                            </Stepper>
                        </Box>
                    )}

                    {/* 通用备注 */}
                    <TextField
                        fullWidth
                        label="操作备注"
                        multiline
                        rows={2}
                        value={comment}
                        onChange={(e) => setComment(e.target.value)}
                        sx={{ mt: 2 }}
                        helperText="可选：记录此操作的相关信息"
                    />
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setDialogOpen(false)}>
                        取消
                    </Button>
                    <Button 
                        onClick={executeAction}
                        variant="contained"
                        color={currentAction?.color}
                        disabled={
                            (currentAction?.type === 'assign' && !assigneeId) ||
                            (currentAction?.type === 'transfer' && (!transferDepartment || !assigneeId)) ||
                            (currentAction?.type === 'escalate' && (!escalationReason || !assigneeId)) ||
                            (currentAction?.type === 'status_change' && !newStatus)
                        }
                    >
                        确认{currentAction?.label}
                    </Button>
                </DialogActions>
            </Dialog>
        </Box>
    );
};

export default TicketWorkflowActions;
