import React, { useState } from 'react';
import {
    Button,
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
} from '@mui/material';
import {
    Assignment,
    SwapHoriz as Transfer,
    TrendingUp as Escalate,
    CheckCircle,
} from '@mui/icons-material';
import { useRecordContext, useNotify, useRefresh, ReferenceInput, AutocompleteInput } from 'react-admin';
import { Ticket, TicketStatus } from '@/types';

// 工单状态流转定义
const TICKET_WORKFLOWS = {
    open: ['in_progress', 'pending', 'cancelled'],
    in_progress: ['pending', 'resolved', 'cancelled'],
    pending: ['in_progress', 'resolved', 'cancelled'], 
    resolved: ['closed', 'reopened'],
    closed: [],
    cancelled: ['reopened']
};

// 状态中文映射
const STATUS_LABELS = {
    open: '待处理',
    in_progress: '处理中',
    pending: '等待中',
    resolved: '已解决',
    closed: '已关闭',
    cancelled: '已取消',
    reopened: '重新打开'
};

// 优先级升级规则
const PRIORITY_ESCALATION = {
    low: 'normal',
    normal: 'high', 
    high: 'urgent',
    urgent: 'critical'
};

interface WorkflowAction {
    type: 'assign' | 'transfer' | 'escalate' | 'status_change' | 'priority_change';
    label: string;
    icon: React.ReactNode;
    color: 'primary' | 'secondary' | 'warning' | 'error' | 'success';
    requiresInput?: boolean;
}

/**
 * 工单工作流操作组件
 * 提供分配、转移、升级、状态变更等工单流转功能
 */
const TicketWorkflowActions: React.FC = () => {
    const record = useRecordContext<Ticket>();
    const notify = useNotify();
    const refresh = useRefresh();
    
    const [dialogOpen, setDialogOpen] = useState(false);
    const [currentAction, setCurrentAction] = useState<WorkflowAction | null>(null);
    const [assigneeId, setAssigneeId] = useState<number | null>(null);
    const [transferDepartment, setTransferDepartment] = useState('');
    const [escalationReason, setEscalationReason] = useState('');
    const [newStatus, setNewStatus] = useState<TicketStatus | ''>('');
    const [comment, setComment] = useState('');

    if (!record) return null;

    // 获取当前可执行的操作
    const getAvailableActions = (): WorkflowAction[] => {
        const actions: WorkflowAction[] = [];
        
        // 分配操作（未分配或重新分配）
        if (!record.assigned_to_id || record.status === 'open') {
            actions.push({
                type: 'assign',
                label: record.assigned_to_id ? '重新分配' : '分配工单',
                icon: <Assignment />,
                color: 'primary',
                requiresInput: true
            });
        }

        // 转移操作（已分配的工单）
        if (record.assigned_to_id && ['open', 'in_progress'].includes(record.status)) {
            actions.push({
                type: 'transfer', 
                label: '转移工单',
                icon: <Transfer />,
                color: 'secondary',
                requiresInput: true
            });
        }

        // 升级操作
        if (['open', 'in_progress', 'pending'].includes(record.status)) {
            actions.push({
                type: 'escalate',
                label: '升级工单',
                icon: <Escalate />,
                color: 'warning',
                requiresInput: true
            });
        }

        // 状态变更
        const availableStatuses = TICKET_WORKFLOWS[record.status as keyof typeof TICKET_WORKFLOWS];
        if (availableStatuses.length > 0) {
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
            let endpoint = '';
            const payload: Record<string, unknown> = { comment };

            switch (currentAction.type) {
                case 'assign':
                    endpoint = `/tickets/${record.id}/assign`;
                    payload.assigned_to_id = assigneeId;
                    break;
                
                case 'transfer':
                    endpoint = `/tickets/${record.id}/transfer`;
                    payload.department = transferDepartment;
                    payload.assigned_to_id = assigneeId;
                    break;
                
                case 'escalate':
                    endpoint = `/tickets/${record.id}/escalate`;
                    payload.reason = escalationReason;
                    payload.priority = PRIORITY_ESCALATION[record.priority as keyof typeof PRIORITY_ESCALATION];
                    break;
                
                case 'status_change':
                    endpoint = `/tickets/${record.id}/status`;
                    payload.status = newStatus;
                    break;
            }

            // 调用后端 API（这里需要根据实际后端实现调整）
            const response = await fetch(`/api${endpoint}`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${localStorage.getItem('token')}`
                },
                body: JSON.stringify(payload)
            });

            if (response.ok) {
                notify(`工单${currentAction.label}成功`, { type: 'success' });
                refresh();
                setDialogOpen(false);
                resetForm();
            } else {
                throw new Error('操作失败');
            }
        } catch (error) {
            const message = error instanceof Error ? error.message : `工单${currentAction.label}失败`;
            notify(message, { type: 'error' });
        }
    };

    const resetForm = () => {
        setAssigneeId(null);
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

    const availableActions = getAvailableActions();

    return (
        <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
            {/* 工单状态显示 */}
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mr: 2 }}>
                <Chip 
                    label={STATUS_LABELS[record.status as keyof typeof STATUS_LABELS]} 
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
                            <ReferenceInput 
                                source="assigned_to_id" 
                                reference="users" 
                                label="分配给"
                            >
                                <AutocompleteInput 
                                    optionText={(user) => `${user.username} (${user.first_name} ${user.last_name})`}
                                    onChange={(value) => setAssigneeId(value)}
                                />
                            </ReferenceInput>
                        </Box>
                    )}

                    {currentAction?.type === 'transfer' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="info">
                                将工单转移到其他部门或用户。转移历史将被记录。
                            </Alert>
                            <FormControl fullWidth>
                                <InputLabel>目标部门</InputLabel>
                                <Select
                                    value={transferDepartment}
                                    onChange={(e) => setTransferDepartment(e.target.value)}
                                >
                                    <MenuItem value="technical">技术支持</MenuItem>
                                    <MenuItem value="sales">销售部</MenuItem>
                                    <MenuItem value="billing">财务部</MenuItem>
                                    <MenuItem value="management">管理层</MenuItem>
                                </Select>
                            </FormControl>
                            <ReferenceInput 
                                source="assigned_to_id" 
                                reference="users" 
                                label="转移给"
                            >
                                <AutocompleteInput 
                                    optionText={(user) => `${user.username} (${user.first_name} ${user.last_name})`}
                                    onChange={(value) => setAssigneeId(value)}
                                />
                            </ReferenceInput>
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
                            <Box>
                                <strong>优先级将从 {record.priority} 升级为 {PRIORITY_ESCALATION[record.priority as keyof typeof PRIORITY_ESCALATION]}</strong>
                            </Box>
                        </Box>
                    )}

                    {currentAction?.type === 'status_change' && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2 }}>
                            <Alert severity="info">
                                更改工单状态。状态变更将触发相应的自动化流程。
                            </Alert>
                            <FormControl fullWidth>
                                <InputLabel>新状态</InputLabel>
                                <Select
                                    value={newStatus}
                                    onChange={(e) => setNewStatus(e.target.value)}
                                >
                                    {TICKET_WORKFLOWS[record.status as keyof typeof TICKET_WORKFLOWS].map(status => (
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
                            (currentAction?.type === 'escalate' && !escalationReason) ||
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
