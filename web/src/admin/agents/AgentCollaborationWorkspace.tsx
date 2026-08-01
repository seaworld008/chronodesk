import {
    useCallback,
    useEffect,
    useMemo,
    useRef,
    useState,
} from 'react'
import {
    Approval as ApprovalIcon,
    PanToolAlt as TakeoverIcon,
    Refresh as RefreshIcon,
    Visibility as DetailIcon,
} from '@mui/icons-material'
import {
    Alert,
    Autocomplete,
    Box,
    Button,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Divider,
    Drawer,
    IconButton,
    Link,
    Paper,
    Stack,
    Tab,
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TablePagination,
    TableRow,
    Tabs,
    TextField,
    Tooltip,
    Typography,
} from '@mui/material'
import { useNotify, usePermissions } from 'react-admin'
import {
    Link as RouterLink,
    useSearchParams,
} from 'react-router-dom'
import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'
import {
    ResizableMuiTable,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import type { AccessPermissions } from '@/lib/accessControl'
import {
    humanApiRoutes,
    type ActionProposalDetail,
    type ActionProposalPage,
    type ActionProposalSummary,
    type AgentRunDetail,
    type AgentRunPage,
    type AgentRunSummary,
    type ApprovalTaskDetail,
    type ApprovalTaskPage,
    type ApprovalTaskSummary,
    type HandoffDetail,
    type HandoffPage,
    type HandoffSummary,
} from '@/lib/generated/human-api'
import {
    parseProjectRole,
    resolveActiveProjectKey,
} from '@/lib/projectScope'

type CollaborationTab = 'runs' | 'proposals' | 'approvals' | 'handoffs'

type CollaborationPage =
    | AgentRunPage
    | ActionProposalPage
    | ApprovalTaskPage
    | HandoffPage

type TicketProjection = Pick<
    AgentRunSummary,
    'ticket_id' | 'ticket_number' | 'ticket_title'
>

type CollaborationItem =
    | AgentRunSummary
    | ActionProposalSummary
    | ApprovalTaskSummary
    | HandoffSummary

type CollaborationDetail =
    | AgentRunDetail
    | ActionProposalDetail
    | ApprovalTaskDetail
    | HandoffDetail

type CollaborationPageView =
    Omit<AgentRunPage, 'items'> & { items: CollaborationItem[] }

const tabLabels: Record<CollaborationTab, string> = {
    runs: 'Agent 运行',
    proposals: '行动提案',
    approvals: '审批任务',
    handoffs: '人机交接',
}

const tabDescriptions: Record<CollaborationTab, string> = {
    runs: '查看 AI 智能体在当前项目中的执行状态，并在必要时进行人工接管。',
    proposals: '审阅智能体提出的结构化变更，不展示原始提示词、凭据或隐藏推理。',
    approvals: '集中处理高风险行动审批，审批结果会写入不可变审计记录。',
    handoffs: '追踪智能体与人工团队之间的工作交接和待补充信息。',
}

const columns: Record<CollaborationTab, ResizableColumn[]> = {
    runs: [
        { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'ticket', defaultWidth: 320, minWidth: 200, maxWidth: 560 },
        { key: 'status', defaultWidth: 160, minWidth: 120, maxWidth: 220 },
        { key: 'updated_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'actions', defaultWidth: 140, minWidth: 112, maxWidth: 200 },
    ],
    proposals: [
        { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'ticket', defaultWidth: 300, minWidth: 200, maxWidth: 520 },
        { key: 'action_type', defaultWidth: 260, minWidth: 160, maxWidth: 520 },
        { key: 'risk_level', defaultWidth: 140, minWidth: 112, maxWidth: 200 },
        { key: 'status', defaultWidth: 150, minWidth: 120, maxWidth: 220 },
        { key: 'actions', defaultWidth: 112, minWidth: 96, maxWidth: 160 },
    ],
    approvals: [
        { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'ticket', defaultWidth: 300, minWidth: 200, maxWidth: 520 },
        { key: 'progress', defaultWidth: 180, minWidth: 144, maxWidth: 280 },
        { key: 'expires_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'status', defaultWidth: 150, minWidth: 120, maxWidth: 220 },
        { key: 'actions', defaultWidth: 180, minWidth: 144, maxWidth: 240 },
    ],
    handoffs: [
        { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
        { key: 'ticket', defaultWidth: 340, minWidth: 200, maxWidth: 600 },
        { key: 'direction', defaultWidth: 180, minWidth: 144, maxWidth: 280 },
        { key: 'run', defaultWidth: 300, minWidth: 180, maxWidth: 520 },
        { key: 'actions', defaultWidth: 112, minWidth: 96, maxWidth: 160 },
    ],
}

const statusLabels: Record<string, string> = {
    queued: '排队中',
    running: '运行中',
    waiting_approval: '等待审批',
    succeeded: '已成功',
    failed: '失败',
    cancelled: '已取消',
    taken_over: '已人工接管',
    pending: '待处理',
    approved: '已批准',
    rejected: '已拒绝',
    executed: '已执行',
    invalidated: '已失效',
    expired: '已过期',
}

const riskLabels: Record<string, string> = {
    low: '低风险',
    medium: '中风险',
    high: '高风险',
    critical: '严重风险',
}

const directionLabels: Record<string, string> = {
    human_to_agent: '人工 → 智能体',
    agent_to_human: '智能体 → 人工',
    queue_to_team: '队列 → 团队',
}

const detailLabels: Record<string, string> = {
    id: '记录标识',
    created_at: '创建时间',
    updated_at: '更新时间',
    ticket_id: '工单 ID',
    ticket_number: '工单编号',
    ticket_title: '工单标题',
    status: '状态',
    model_provider: '模型提供方',
    model_name: '模型',
    prompt_version: '提示模板版本',
    toolset_version: '工具集版本',
    policy_version: '策略版本',
    input_summary: '输入摘要',
    output_summary: '输出摘要',
    prompt_tokens: '输入 Token',
    completion_tokens: '输出 Token',
    cost_micros: '成本（微单位）',
    started_at: '开始时间',
    finished_at: '结束时间',
    termination_reason: '终止原因',
    agent_run_id: '运行标识',
    action_type: '行动类型',
    risk_level: '风险等级',
    target_ticket_version: '目标工单版本',
    expires_at: '过期时间',
    executed_at: '执行时间',
    proposal_id: '提案标识',
    required_approvals: '所需审批数',
    completed_at: '完成时间',
    approvals_recorded: '已批准数',
    rejections_recorded: '已拒绝数',
    direction: '交接方向',
    reason: '交接原因',
    completed_summary: '已完成工作',
    missing_information: '缺失信息',
    preview: '安全变更预览',
}

const dateTime = (value: string) => {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime())
        ? value
        : parsed.toLocaleString('zh-CN')
}

const statusColor = (
    status: string,
): 'default' | 'primary' | 'success' | 'warning' | 'error' => {
    if (['succeeded', 'approved', 'executed'].includes(status)) return 'success'
    if (['failed', 'rejected'].includes(status)) return 'error'
    if (['waiting_approval', 'pending', 'expired'].includes(status)) return 'warning'
    if (['queued', 'running'].includes(status)) return 'primary'
    return 'default'
}

const riskColor = (
    risk: string,
): 'default' | 'info' | 'warning' | 'error' => {
    if (risk === 'critical' || risk === 'high') return 'error'
    if (risk === 'medium') return 'warning'
    if (risk === 'low') return 'info'
    return 'default'
}

const validTab = (value: string | null): value is CollaborationTab =>
    value === 'runs'
    || value === 'proposals'
    || value === 'approvals'
    || value === 'handoffs'

const validPageSize = (value: string | null) => {
    const parsed = Number(value)
    return parsed === 25 || parsed === 50 || parsed === 100 ? parsed : 25
}

const validPage = (value: string | null) => {
    const parsed = Number(value)
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 1
}

const ticketCell = (item: TicketProjection) => (
    <Link
        component={RouterLink}
        to={`/tickets/${item.ticket_id}/show`}
        underline="hover"
        onClick={(event) => event.stopPropagation()}
    >
        <TruncatedText title={`#${item.ticket_number} ${item.ticket_title}`}>
            #{item.ticket_number} {item.ticket_title}
        </TruncatedText>
    </Link>
)

const detailValue = (key: string, value: unknown) => {
    if (value === null || value === undefined || value === '') return '—'
    if (
        typeof value === 'string'
        && (key.endsWith('_at') || key === 'created_at' || key === 'updated_at')
    ) {
        return dateTime(value)
    }
    if (key === 'status' && typeof value === 'string') {
        return statusLabels[value] ?? value
    }
    if (key === 'risk_level' && typeof value === 'string') {
        return riskLabels[value] ?? value
    }
    if (key === 'direction' && typeof value === 'string') {
        return directionLabels[value] ?? value
    }
    if (Array.isArray(value)) {
        return value.length === 0 ? '—' : value.join('、')
    }
    if (typeof value === 'object') {
        return JSON.stringify(value, null, 2)
    }
    return String(value)
}

const collaborationListPath = (
    tab: CollaborationTab,
    projectKey: string,
    page: number,
    pageSize: number,
) => {
    const path = { projectKey }
    const query = { page, page_size: pageSize }
    switch (tab) {
        case 'runs':
            return humanApiRoutes.listProjectAgentRuns(path, query)
        case 'proposals':
            return humanApiRoutes.listProjectActionProposals(path, query)
        case 'approvals':
            return humanApiRoutes.listProjectApprovalTasks(path, query)
        case 'handoffs':
            return humanApiRoutes.listProjectHandoffs(path, query)
    }
}

const collaborationDetailPath = (
    tab: CollaborationTab,
    projectKey: string,
    id: string,
) => {
    switch (tab) {
        case 'runs':
            return humanApiRoutes.getProjectAgentRun({
                projectKey,
                runID: id,
            })
        case 'proposals':
            return humanApiRoutes.getProjectActionProposal({
                projectKey,
                proposalID: id,
            })
        case 'approvals':
            return humanApiRoutes.getProjectApprovalTask({
                projectKey,
                approvalID: id,
            })
        case 'handoffs':
            return humanApiRoutes.getProjectHandoff({
                projectKey,
                handoffID: id,
            })
    }
}

const terminalRunStatuses = new Set([
    'succeeded',
    'failed',
    'cancelled',
    'taken_over',
])

const AgentCollaborationWorkspace = () => {
    const { permissions } = usePermissions<AccessPermissions>()
    const notify = useNotify()
    const [searchParams, setSearchParams] = useSearchParams()
    const pendingSearchParams = useRef(searchParams)
    const tab = validTab(searchParams.get('tab'))
        ? searchParams.get('tab') as CollaborationTab
        : 'runs'
    const page = validPage(searchParams.get('page'))
    const pageSize = validPageSize(searchParams.get('page_size'))
    const [projectKey, setProjectKey] = useState('')
    const [result, setResult] = useState<CollaborationPageView | null>(null)
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState('')
    const requestController = useRef<AbortController | null>(null)
    const requestSequence = useRef(0)
    const [detailOpen, setDetailOpen] = useState(false)
    const [detail, setDetail] = useState<CollaborationDetail | null>(null)
    const [detailLoading, setDetailLoading] = useState(false)
    const [detailError, setDetailError] = useState('')
    const detailController = useRef<AbortController | null>(null)
    const [approvalTarget, setApprovalTarget] =
        useState<ApprovalTaskSummary | null>(null)
    const [approvalDecision, setApprovalDecision] =
        useState<'approve' | 'reject'>('approve')
    const [approvalComment, setApprovalComment] = useState('')
    const [takeoverTarget, setTakeoverTarget] =
        useState<AgentRunSummary | null>(null)
    const [takeoverReason, setTakeoverReason] = useState('')
    const [completedSummary, setCompletedSummary] = useState('')
    const [evidenceDigest, setEvidenceDigest] = useState('')
    const [missingInformation, setMissingInformation] = useState<string[]>([])
    const [submitting, setSubmitting] = useState(false)

    const role = parseProjectRole(permissions?.project_role)
    const canApprove = role === 'project_admin' || role === 'manager'
    const canTakeover =
        role === 'project_admin' || role === 'manager' || role === 'agent'

    useEffect(() => {
        pendingSearchParams.current = searchParams
    }, [searchParams])

    const updateQuery = useCallback((
        updates: Partial<{
            tab: CollaborationTab
            page: number
            pageSize: number
        }>,
    ) => {
        // React Router 的函数式 updater 仍读取渲染时的快照；先同步记录
        // 待导航值，保证连续操作基于前一次尚未渲染的更新继续合并。
        const next = new URLSearchParams(pendingSearchParams.current)
        if (updates.tab) next.set('tab', updates.tab)
        if (updates.page) next.set('page', String(updates.page))
        if (updates.pageSize) {
            next.set('page_size', String(updates.pageSize))
        }
        pendingSearchParams.current = next
        setSearchParams(next, { replace: true })
    }, [setSearchParams])

    const load = useCallback(async () => {
        requestController.current?.abort()
        const controller = new AbortController()
        const sequence = requestSequence.current + 1
        requestSequence.current = sequence
        requestController.current = controller
        setLoading(true)
        setError('')
        try {
            const currentProjectKey = await resolveActiveProjectKey()
            const pageResult = await apiFetch<CollaborationPage>(
                collaborationListPath(
                    tab,
                    currentProjectKey,
                    page,
                    pageSize,
                ),
                { signal: controller.signal },
            )
            if (
                controller.signal.aborted
                || requestSequence.current !== sequence
            ) return
            setProjectKey(currentProjectKey)
            setResult({
                ...pageResult,
                items: (pageResult.items ?? []) as CollaborationItem[],
            })
        } catch (loadError) {
            if (
                controller.signal.aborted
                || requestSequence.current !== sequence
            ) return
            setError(
                localizedUnknownErrorMessage(
                    loadError,
                    'AI 协作记录加载失败',
                ),
            )
        } finally {
            if (
                !controller.signal.aborted
                && requestSequence.current === sequence
            ) {
                setLoading(false)
            }
        }
    }, [page, pageSize, tab])

    useEffect(() => {
        void load()
        return () => requestController.current?.abort()
    }, [load])

    useEffect(() => () => {
        requestController.current?.abort()
        detailController.current?.abort()
        requestSequence.current += 1
    }, [])

    const openDetail = async (item: CollaborationItem) => {
        detailController.current?.abort()
        const controller = new AbortController()
        detailController.current = controller
        setDetailOpen(true)
        setDetail(null)
        setDetailError('')
        setDetailLoading(true)
        try {
            const currentProjectKey =
                projectKey || await resolveActiveProjectKey()
            const loaded = await apiFetch<CollaborationDetail>(
                collaborationDetailPath(tab, currentProjectKey, item.id),
                { signal: controller.signal },
            )
            if (!controller.signal.aborted) setDetail(loaded)
        } catch (loadError) {
            if (controller.signal.aborted) return
            setDetailError(
                localizedUnknownErrorMessage(
                    loadError,
                    '协作记录详情加载失败',
                ),
            )
        } finally {
            if (!controller.signal.aborted) setDetailLoading(false)
        }
    }

    const closeApprovalDialog = (force = false) => {
        if (submitting && !force) return
        setApprovalTarget(null)
        setApprovalDecision('approve')
        setApprovalComment('')
    }

    const submitApproval = async () => {
        if (!approvalTarget || !projectKey) return
        setSubmitting(true)
        try {
            await apiFetch(
                humanApiRoutes.decideProjectAgentApproval({
                    projectKey,
                    approvalID: approvalTarget.id,
                }),
                {
                    method: 'POST',
                    body: JSON.stringify({
                        decision: approvalDecision,
                        comment: approvalComment.trim(),
                    }),
                },
            )
            closeApprovalDialog(true)
            notify(
                approvalDecision === 'approve'
                    ? '审批已批准'
                    : '审批已拒绝',
                { type: 'success' },
            )
            await load()
        } catch (submitError) {
            notify(
                localizedUnknownErrorMessage(
                    submitError,
                    '审批提交失败',
                ),
                { type: 'error' },
            )
        } finally {
            setSubmitting(false)
        }
    }

    const closeTakeoverDialog = (force = false) => {
        if (submitting && !force) return
        setTakeoverTarget(null)
        setTakeoverReason('')
        setCompletedSummary('')
        setEvidenceDigest('')
        setMissingInformation([])
    }

    const submitTakeover = async () => {
        if (!takeoverTarget || !projectKey) return
        setSubmitting(true)
        try {
            await apiFetch(
                humanApiRoutes.takeOverProjectAgentRun({
                    projectKey,
                    runID: takeoverTarget.id,
                }),
                {
                    method: 'POST',
                    body: JSON.stringify({
                        reason: takeoverReason.trim(),
                        completed_summary: completedSummary.trim(),
                        missing_information: missingInformation,
                        evidence_digest: evidenceDigest.trim(),
                    }),
                },
            )
            closeTakeoverDialog(true)
            notify('Agent 运行已转为人工接管', { type: 'success' })
            await load()
        } catch (submitError) {
            notify(
                localizedUnknownErrorMessage(
                    submitError,
                    '人工接管失败',
                ),
                { type: 'error' },
            )
        } finally {
            setSubmitting(false)
        }
    }

    const headerCells = useMemo(() => {
        if (tab === 'runs') {
            return ['创建时间', '工单', '状态', '更新时间', '操作']
        }
        if (tab === 'proposals') {
            return ['创建时间', '工单', '行动类型', '风险', '状态', '操作']
        }
        if (tab === 'approvals') {
            return ['创建时间', '工单', '审批要求', '过期时间', '状态', '操作']
        }
        return ['交接时间', '工单', '交接方向', 'Agent 运行', '操作']
    }, [tab])

    const renderActions = (item: CollaborationItem) => {
        if (tab === 'runs') {
            const run = item as AgentRunSummary
            return (
                <Stack direction="row" spacing={0.5}>
                    <Tooltip title="查看详情">
                        <IconButton
                            size="small"
                            aria-label="查看 Agent 运行详情"
                            onClick={(event) => {
                                event.stopPropagation()
                                void openDetail(item)
                            }}
                        >
                            <DetailIcon fontSize="small" />
                        </IconButton>
                    </Tooltip>
                    {canTakeover && !terminalRunStatuses.has(run.status) && (
                        <Tooltip title="人工接管">
                            <IconButton
                                size="small"
                                color="warning"
                                aria-label="人工接管 Agent 运行"
                                onClick={(event) => {
                                    event.stopPropagation()
                                    setTakeoverTarget(run)
                                }}
                            >
                                <TakeoverIcon fontSize="small" />
                            </IconButton>
                        </Tooltip>
                    )}
                </Stack>
            )
        }
        if (tab === 'approvals') {
            const approval = item as ApprovalTaskSummary
            return (
                <Stack direction="row" spacing={0.5}>
                    <Tooltip title="查看详情">
                        <IconButton
                            size="small"
                            aria-label="查看审批任务详情"
                            onClick={(event) => {
                                event.stopPropagation()
                                void openDetail(item)
                            }}
                        >
                            <DetailIcon fontSize="small" />
                        </IconButton>
                    </Tooltip>
                    {canApprove && approval.status === 'pending' && (
                        <Tooltip title="处理审批">
                            <IconButton
                                size="small"
                                color="primary"
                                aria-label="处理 Agent 行动审批"
                                onClick={(event) => {
                                    event.stopPropagation()
                                    setApprovalTarget(approval)
                                }}
                            >
                                <ApprovalIcon fontSize="small" />
                            </IconButton>
                        </Tooltip>
                    )}
                </Stack>
            )
        }
        return (
            <Tooltip title="查看详情">
                <IconButton
                    size="small"
                    aria-label={`查看${tabLabels[tab]}详情`}
                    onClick={(event) => {
                        event.stopPropagation()
                        void openDetail(item)
                    }}
                >
                    <DetailIcon fontSize="small" />
                </IconButton>
            </Tooltip>
        )
    }

    const renderRow = (item: CollaborationItem) => {
        const commonProps = {
            hover: true,
            tabIndex: 0,
            sx: { cursor: 'pointer' },
            onClick: () => void openDetail(item),
            onKeyDown: (event: React.KeyboardEvent<HTMLTableRowElement>) => {
                if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    void openDetail(item)
                }
            },
        }
        if (tab === 'runs') {
            const run = item as AgentRunSummary
            return (
                <TableRow key={run.id} {...commonProps}>
                    <TableCell>{dateTime(run.created_at)}</TableCell>
                    <TableCell>{ticketCell(run)}</TableCell>
                    <TableCell>
                        <Chip
                            size="small"
                            color={statusColor(run.status)}
                            variant="outlined"
                            label={statusLabels[run.status] ?? run.status}
                        />
                    </TableCell>
                    <TableCell>{dateTime(run.updated_at)}</TableCell>
                    <TableCell>{renderActions(run)}</TableCell>
                </TableRow>
            )
        }
        if (tab === 'proposals') {
            const proposal = item as ActionProposalSummary
            return (
                <TableRow key={proposal.id} {...commonProps}>
                    <TableCell>{dateTime(proposal.created_at)}</TableCell>
                    <TableCell>{ticketCell(proposal)}</TableCell>
                    <TableCell>
                        <TruncatedText title={proposal.action_type}>
                            {proposal.action_type}
                        </TruncatedText>
                    </TableCell>
                    <TableCell>
                        <Chip
                            size="small"
                            color={riskColor(proposal.risk_level)}
                            variant="outlined"
                            label={
                                riskLabels[proposal.risk_level]
                                ?? proposal.risk_level
                            }
                        />
                    </TableCell>
                    <TableCell>
                        <Chip
                            size="small"
                            color={statusColor(proposal.status)}
                            variant="outlined"
                            label={
                                statusLabels[proposal.status]
                                ?? proposal.status
                            }
                        />
                    </TableCell>
                    <TableCell>{renderActions(proposal)}</TableCell>
                </TableRow>
            )
        }
        if (tab === 'approvals') {
            const approval = item as ApprovalTaskSummary
            return (
                <TableRow key={approval.id} {...commonProps}>
                    <TableCell>{dateTime(approval.created_at)}</TableCell>
                    <TableCell>{ticketCell(approval)}</TableCell>
                    <TableCell>
                        需要 {approval.required_approvals} 人批准
                    </TableCell>
                    <TableCell>{dateTime(approval.expires_at)}</TableCell>
                    <TableCell>
                        <Chip
                            size="small"
                            color={statusColor(approval.status)}
                            variant="outlined"
                            label={
                                statusLabels[approval.status]
                                ?? approval.status
                            }
                        />
                    </TableCell>
                    <TableCell>{renderActions(approval)}</TableCell>
                </TableRow>
            )
        }
        const handoff = item as HandoffSummary
        return (
            <TableRow key={handoff.id} {...commonProps}>
                <TableCell>{dateTime(handoff.created_at)}</TableCell>
                <TableCell>{ticketCell(handoff)}</TableCell>
                <TableCell>
                    <Chip
                        size="small"
                        variant="outlined"
                        label={
                            directionLabels[handoff.direction]
                            ?? handoff.direction
                        }
                    />
                </TableCell>
                <TableCell>
                    <TruncatedText title={handoff.agent_run_id || '—'}>
                        {handoff.agent_run_id || '—'}
                    </TruncatedText>
                </TableCell>
                <TableCell>{renderActions(handoff)}</TableCell>
            </TableRow>
        )
    }

    return (
        <PageShell
            title="AI 人机协作"
            testId="agent-collaboration-workspace"
        >
            <Stack spacing={2}>
                <PageHeader
                    title="AI 人机协作"
                    description={
                        projectKey
                            ? `当前项目：${projectKey} · ${tabDescriptions[tab]}`
                            : tabDescriptions[tab]
                    }
                    action={(
                        <Tooltip title="刷新当前列表">
                            <span>
                                <IconButton
                                    aria-label="刷新 AI 协作列表"
                                    disabled={loading}
                                    onClick={() => void load()}
                                >
                                    <RefreshIcon />
                                </IconButton>
                            </span>
                        </Tooltip>
                    )}
                />
                <Paper>
                    <Tabs
                        value={tab}
                        onChange={(_, nextTab: CollaborationTab) =>
                            updateQuery({ tab: nextTab, page: 1 })}
                        variant="scrollable"
                        scrollButtons="auto"
                        aria-label="AI 人机协作分类"
                    >
                        {(Object.keys(tabLabels) as CollaborationTab[]).map(
                            (item) => (
                                <Tab
                                    key={item}
                                    value={item}
                                    label={tabLabels[item]}
                                />
                            ),
                        )}
                    </Tabs>
                </Paper>
                {error && (
                    <Alert
                        severity="error"
                        action={
                            <Button size="small" onClick={() => void load()}>
                                重试
                            </Button>
                        }
                    >
                        {error}
                    </Alert>
                )}
                {loading && !result ? (
                    <Box
                        role="status"
                        sx={{ display: 'grid', minHeight: 280, placeItems: 'center' }}
                    >
                        <CircularProgress aria-label="正在加载 AI 协作记录" />
                    </Box>
                ) : (
                    <Paper>
                        <TableContainer>
                            <ResizableMuiTable
                                tableId={`agent-collaboration.${tab}`}
                                columns={columns[tab]}
                                size="small"
                                aria-label={`${tabLabels[tab]}列表`}
                            >
                                <TableHead>
                                    <TableRow>
                                        {headerCells.map((label) => (
                                            <TableCell key={label}>
                                                {label}
                                            </TableCell>
                                        ))}
                                    </TableRow>
                                </TableHead>
                                <TableBody>
                                    {(result?.items ?? []).map(renderRow)}
                                    {(result?.items.length ?? 0) === 0 && (
                                        <TableRow>
                                            <TableCell
                                                colSpan={headerCells.length}
                                                align="center"
                                                sx={{ py: 7 }}
                                            >
                                                暂无{tabLabels[tab]}。
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </ResizableMuiTable>
                        </TableContainer>
                        <TablePagination
                            component="div"
                            count={result?.total ?? 0}
                            page={page - 1}
                            rowsPerPage={pageSize}
                            rowsPerPageOptions={[25, 50, 100]}
                            onPageChange={(_, nextPage) =>
                                updateQuery({ page: nextPage + 1 })}
                            onRowsPerPageChange={(event) =>
                                updateQuery({
                                    page: 1,
                                    pageSize: Number(event.target.value),
                                })}
                            labelRowsPerPage="每页记录数"
                            labelDisplayedRows={({ from, to, count }) =>
                                `${from}–${to} / ${count}`}
                            showFirstButton
                            showLastButton
                        />
                    </Paper>
                )}
            </Stack>

            <Drawer
                anchor="right"
                open={detailOpen}
                onClose={() => {
                    detailController.current?.abort()
                    setDetailOpen(false)
                }}
                slotProps={{
                    paper: {
                        sx: {
                            boxSizing: 'border-box',
                            p: 3,
                            width: { xs: '100%', sm: 520 },
                        },
                    },
                }}
            >
                <Typography variant="h6" component="h2">
                    {tabLabels[tab]}详情
                </Typography>
                <Typography
                    variant="body2"
                    color="text.secondary"
                    sx={{ mt: 0.5, mb: 2 }}
                >
                    仅展示安全投影；凭据、原始提示词和隐藏推理不会返回浏览器。
                </Typography>
                <Divider sx={{ mb: 2 }} />
                {detailLoading && (
                    <Box
                        role="status"
                        sx={{ display: 'grid', minHeight: 240, placeItems: 'center' }}
                    >
                        <CircularProgress aria-label="正在加载协作详情" />
                    </Box>
                )}
                {detailError && (
                    <Alert severity="error">{detailError}</Alert>
                )}
                {detail && (
                    <Stack spacing={2}>
                        {Object.entries(detail).map(([key, value]) => (
                            <Box key={key}>
                                <Typography
                                    variant="caption"
                                    color="text.secondary"
                                >
                                    {detailLabels[key] ?? key}
                                </Typography>
                                <Typography
                                    component="pre"
                                    variant="body2"
                                    sx={{
                                        m: 0,
                                        mt: 0.5,
                                        overflowWrap: 'anywhere',
                                        whiteSpace: 'pre-wrap',
                                    }}
                                >
                                    {detailValue(key, value)}
                                </Typography>
                            </Box>
                        ))}
                    </Stack>
                )}
            </Drawer>

            <Dialog
                open={approvalTarget !== null}
                onClose={() => closeApprovalDialog()}
                fullWidth
                maxWidth="sm"
                aria-labelledby="approval-decision-title"
            >
                <DialogTitle id="approval-decision-title">
                    处理 Agent 行动审批
                </DialogTitle>
                <DialogContent>
                    <Stack spacing={2.5} sx={{ pt: 1 }}>
                        <Alert severity="warning">
                            请先查看提案及目标工单。审批会作为实名、不可变记录保存。
                        </Alert>
                        <Autocomplete
                            disableClearable
                            options={[
                                { id: 'approve' as const, label: '批准' },
                                { id: 'reject' as const, label: '拒绝' },
                            ]}
                            value={
                                approvalDecision === 'approve'
                                    ? { id: 'approve' as const, label: '批准' }
                                    : { id: 'reject' as const, label: '拒绝' }
                            }
                            isOptionEqualToValue={(option, value) =>
                                option.id === value.id}
                            onChange={(_, value) =>
                                setApprovalDecision(value.id)}
                            renderInput={(params) => (
                                <TextField
                                    {...params}
                                    label="审批决定"
                                    slotProps={{
                                        ...params.slotProps,
                                        htmlInput: {
                                            ...params.slotProps.htmlInput,
                                            'aria-label': '选择审批决定',
                                        },
                                    }}
                                />
                            )}
                        />
                        <TextField
                            autoFocus
                            label="审批备注"
                            value={approvalComment}
                            onChange={(event) =>
                                setApprovalComment(event.target.value)}
                            multiline
                            minRows={3}
                            slotProps={{ htmlInput: { maxLength: 1000 } }}
                            helperText={`${approvalComment.length}/1000`}
                        />
                    </Stack>
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={submitting}
                        onClick={() => closeApprovalDialog()}
                    >
                        取消
                    </Button>
                    <Button
                        variant="contained"
                        color={
                            approvalDecision === 'approve'
                                ? 'primary'
                                : 'error'
                        }
                        disabled={submitting}
                        onClick={() => void submitApproval()}
                    >
                        {submitting ? '正在提交…' : '确认提交'}
                    </Button>
                </DialogActions>
            </Dialog>

            <Dialog
                open={takeoverTarget !== null}
                onClose={() => closeTakeoverDialog()}
                fullWidth
                maxWidth="md"
                aria-labelledby="takeover-agent-run-title"
            >
                <DialogTitle id="takeover-agent-run-title">
                    人工接管 Agent 运行
                </DialogTitle>
                <DialogContent>
                    <Stack spacing={2.5} sx={{ pt: 1 }}>
                        <Alert severity="warning">
                            接管后该 Agent 运行不能继续写入。请记录原因和证据摘要，便于审计追踪。
                        </Alert>
                        <TextField
                            autoFocus
                            required
                            label="接管原因"
                            value={takeoverReason}
                            onChange={(event) =>
                                setTakeoverReason(event.target.value)}
                            multiline
                            minRows={3}
                            slotProps={{ htmlInput: { maxLength: 1000 } }}
                            helperText={`${takeoverReason.length}/1000`}
                        />
                        <TextField
                            label="Agent 已完成工作摘要"
                            value={completedSummary}
                            onChange={(event) =>
                                setCompletedSummary(event.target.value)}
                            multiline
                            minRows={3}
                            slotProps={{ htmlInput: { maxLength: 10000 } }}
                            helperText={`${completedSummary.length}/10000`}
                        />
                        <Autocomplete
                            multiple
                            freeSolo
                            limitTags={3}
                            options={[]}
                            value={missingInformation}
                            getLimitTagsText={(more) => `+${more}`}
                            onChange={(_, values) => {
                                const normalized = Array.from(new Set(
                                    values
                                        .map((value) => value.trim())
                                        .filter(Boolean),
                                )).slice(0, 50)
                                setMissingInformation(normalized)
                            }}
                            renderInput={(params) => (
                                <TextField
                                    {...params}
                                    label="仍缺失的信息"
                                    helperText={`已选择 ${missingInformation.length} 项，最多 50 项；输入后按 Enter 添加`}
                                    slotProps={{
                                        ...params.slotProps,
                                        htmlInput: {
                                            ...params.slotProps.htmlInput,
                                            'aria-label': '逐项添加仍缺失的信息',
                                            maxLength: 2000,
                                        },
                                    }}
                                />
                            )}
                        />
                        <TextField
                            required
                            label="证据摘要"
                            value={evidenceDigest}
                            onChange={(event) =>
                                setEvidenceDigest(event.target.value)}
                            placeholder="例如 sha256:abc123 或 incident-2026-001"
                            slotProps={{ htmlInput: { maxLength: 128 } }}
                            helperText="1–128 个字母、数字或 - _ . :"
                        />
                    </Stack>
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={submitting}
                        onClick={() => closeTakeoverDialog()}
                    >
                        取消
                    </Button>
                    <Button
                        variant="contained"
                        color="warning"
                        disabled={
                            submitting
                            || !takeoverReason.trim()
                            || !/^[A-Za-z0-9_.:-]{1,128}$/u.test(
                                evidenceDigest.trim(),
                            )
                        }
                        onClick={() => void submitTakeover()}
                    >
                        {submitting ? '正在接管…' : '确认人工接管'}
                    </Button>
                </DialogActions>
            </Dialog>
        </PageShell>
    )
}

export default AgentCollaborationWorkspace
