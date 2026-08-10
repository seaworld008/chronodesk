import React, { useCallback, useEffect, useRef, useState } from 'react'
import { Title, useNotify } from 'react-admin'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControl,
  FormControlLabel,
  Grid,
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  Switch,
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
import {
  Add as AddIcon,
  Autorenew as RotateIcon,
  ContentCopy as CopyIcon,
  Gavel as PolicyIcon,
  PauseCircleOutlined as PausedIcon,
  Replay as ReplayIcon,
  Refresh as RefreshIcon,
  SmartToy as AgentIcon,
  StopCircle as StopIcon,
} from '@mui/icons-material'
import { apiFetch, localizedUnknownErrorMessage } from '../../lib/apiClient'
import {
  InlineDetails,
  ResizableMuiTable,
  TruncatedText,
  type ResizableColumn,
} from '../../components/tables/EnterpriseTable'
import { activeProjectStorageKey } from '../../lib/humanSessionStorage'
import { resolveActiveProjectKey } from '../../lib/projectScope'
import { projectScopeChangedEvent } from '../../lib/projectScopeEvents'
import {
  humanApiRoutes,
  type AdminAgentPolicy,
  type AdminAttachmentPage,
  type AdminAttachmentSummary,
  type AdminDomainEventCursorPage,
  type AdminLeasePage,
  type AdminOutboxDeliverySummary,
  type AdminOverview,
  type AdminOutboxPage,
  type AdminPolicyDecisionCursorPage,
  type AdminPolicyPage,
  type AdminPrincipalPage,
  type AdminServicePrincipalSummary,
  type AdminTicketLeaseSummary,
  type AgentPolicyCreate,
  type IssuedCredential,
  type ServicePrincipalCreate,
} from '../../lib/generated/human-api'

const AVAILABLE_SCOPES = [
  'tickets:read',
  'tickets:create',
  'tickets:update',
  'tickets:assign',
  'tickets:transition',
  'comments:write',
  'attachments:read',
  'attachments:write',
  'events:subscribe',
  'tasks:manage',
] as const

const principalColumns: ResizableColumn[] = [
  { key: 'agent', defaultWidth: 260, minWidth: 180, maxWidth: 420 },
  { key: 'status', defaultWidth: 300, minWidth: 220, maxWidth: 420 },
  { key: 'scopes', defaultWidth: 420, minWidth: 220, maxWidth: 640 },
  { key: 'limits', defaultWidth: 120, minWidth: 96, maxWidth: 200 },
  { key: 'last-used', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'actions', defaultWidth: 244, minWidth: 220, maxWidth: 320, sticky: 'right' },
]

const leaseColumns: ResizableColumn[] = [
  { key: 'ticket', defaultWidth: 160, minWidth: 112, maxWidth: 260 },
  { key: 'agent', defaultWidth: 220, minWidth: 140, maxWidth: 360 },
  { key: 'version', defaultWidth: 112, minWidth: 88, maxWidth: 160 },
  { key: 'acquired', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'expires', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'takeover', defaultWidth: 120, minWidth: 104, maxWidth: 180, sticky: 'right' },
]

const eventColumns: ResizableColumn[] = [
  { key: 'time', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'type', defaultWidth: 260, minWidth: 160, maxWidth: 420 },
  { key: 'subject', defaultWidth: 320, minWidth: 180, maxWidth: 560 },
  { key: 'actor', defaultWidth: 240, minWidth: 160, maxWidth: 420 },
  { key: 'version', defaultWidth: 100, minWidth: 80, maxWidth: 160 },
]

const outboxColumns: ResizableColumn[] = [
  { key: 'event', defaultWidth: 280, minWidth: 180, maxWidth: 480 },
  { key: 'destination', defaultWidth: 240, minWidth: 160, maxWidth: 420 },
  { key: 'status', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
  { key: 'attempts', defaultWidth: 88, minWidth: 72, maxWidth: 136 },
  { key: 'retry-at', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'expires-at', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'expired-at', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'error', defaultWidth: 360, minWidth: 200, maxWidth: 640 },
  { key: 'actions', defaultWidth: 96, minWidth: 80, maxWidth: 136, sticky: 'right' },
]

const attachmentColumns: ResizableColumn[] = [
  { key: 'attachment', defaultWidth: 300, minWidth: 180, maxWidth: 500 },
  { key: 'ticket', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
  { key: 'type', defaultWidth: 180, minWidth: 120, maxWidth: 280 },
  { key: 'size', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
  { key: 'scan', defaultWidth: 160, minWidth: 120, maxWidth: 240 },
  { key: 'updated', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
]

const policyAuditColumns: ResizableColumn[] = [
  { key: 'time', defaultWidth: 188, minWidth: 150, maxWidth: 260 },
  { key: 'actor', defaultWidth: 280, minWidth: 180, maxWidth: 480 },
  { key: 'scope-action', defaultWidth: 260, minWidth: 180, maxWidth: 460 },
  { key: 'resource', defaultWidth: 220, minWidth: 140, maxWidth: 400 },
  { key: 'source', defaultWidth: 132, minWidth: 104, maxWidth: 220 },
  { key: 'decision', defaultWidth: 160, minWidth: 112, maxWidth: 240 },
]

type PrincipalStatus = AdminServicePrincipalSummary['status']
type ServicePrincipal = AdminServicePrincipalSummary
type TicketLease = AdminTicketLeaseSummary
type OutboxDelivery = AdminOutboxDeliverySummary
type AgentPolicy = AdminAgentPolicy
type AttachmentScan = AdminAttachmentSummary
type CreatePrincipalForm = Required<
  Pick<
    ServicePrincipalCreate,
    'name' | 'description' | 'scopes' | 'rate_limit' | 'concurrency_limit'
  >
>
type CredentialResult = IssuedCredential
type PolicyForm = Required<
  Pick<
    AgentPolicyCreate,
    'effect' | 'scope' | 'action' | 'resource_type' | 'resource_id'
  >
>

type ScopeSnapshot = {
  projectKey: string
  epoch: number
}

type ScopedWriteResult<T> =
  | { current: true; data: T }
  | { current: false }

type CredentialProtection = {
  projectKey: string
  storedProjectSelection: string | null
  sessionBinding: string
  credential: CredentialResult | null
}

let protectedCredential: CredentialProtection | null = null
const protectedCredentialListeners = new Set<(
  value: CredentialProtection | null,
) => void>()

const publishProtectedCredential = (
  value: CredentialProtection | null,
) => {
  protectedCredential = value
  for (const listener of protectedCredentialListeners) listener(value)
}

const projectSelectionSessionBinding = (serialized: string | null) => {
  if (!serialized) return ''
  try {
    const value: unknown = JSON.parse(serialized)
    if (
      typeof value !== 'object'
      || value === null
      || !('subject' in value)
      || !('session_id' in value)
      || typeof value.subject !== 'string'
      || typeof value.session_id !== 'string'
    ) return ''
    return `${value.subject}\u0000${value.session_id}`
  } catch {
    return ''
  }
}

const protectedCredentialForCurrentSession = () => {
  if (!protectedCredential || typeof window === 'undefined') {
    return protectedCredential
  }
  const currentBinding = projectSelectionSessionBinding(
    window.localStorage.getItem(activeProjectStorageKey),
  )
  if (
    !currentBinding
    || currentBinding !== protectedCredential.sessionBinding
  ) {
    protectedCredential = null
  }
  return protectedCredential
}

interface ConfirmationRequest {
  title: string
  description: string
  confirmLabel: string
  color: 'primary' | 'warning' | 'error'
  action: () => Promise<void>
}

const initialCreateForm: CreatePrincipalForm = {
  name: '',
  description: '',
  scopes: ['tickets:read'],
  rate_limit: 60,
  concurrency_limit: 4,
}

const initialPolicyForm: PolicyForm = {
  effect: 'deny',
  scope: 'tickets:update',
  action: '',
  resource_type: 'ticket',
  resource_id: '',
}

const formatDate = (value?: string | null) => {
  if (!value) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString('zh-CN')
}

const principalGrantState = (
  principal: ServicePrincipal,
  now = Date.now(),
): {
  usable: boolean
  label: string
  color: 'success' | 'warning' | 'error'
  detail: string
} => {
  const expiresAt = principal.grant.expires_at
  const expiresAtMillis = expiresAt ? Date.parse(expiresAt) : Number.POSITIVE_INFINITY
  const invalidExpiry = Boolean(expiresAt) && Number.isNaN(expiresAtMillis)
  const expired = Number.isFinite(expiresAtMillis) && expiresAtMillis <= now
  const detail = expiresAt
    ? `项目授权到期时间：${formatDate(expiresAt)}`
    : '项目授权长期有效'
  if (!principal.grant.is_active) {
    return {
      usable: false,
      label: '项目授权已停用',
      color: 'warning',
      detail,
    }
  }
  if (invalidExpiry) {
    return {
      usable: false,
      label: '项目授权时间异常',
      color: 'error',
      detail: '项目授权到期时间无效，已按不可用状态处理',
    }
  }
  if (expired) {
    return {
      usable: false,
      label: '项目授权已过期',
      color: 'error',
      detail,
    }
  }
  return {
    usable: true,
    label: expiresAt ? `授权至 ${new Date(expiresAt).toLocaleDateString('zh-CN')}` : '项目授权有效',
    color: 'success',
    detail,
  }
}

const formatETag = (version: number) => `"v${version}"`

const newIdempotencyKey = () => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `web-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

const adminWrite = <T,>(
  path: string,
  options: RequestInit,
  resourceVersion?: number,
) => {
  const headers = new Headers(options.headers ?? {})
  headers.set('Idempotency-Key', newIdempotencyKey())
  if (resourceVersion !== undefined) {
    headers.set('If-Match', formatETag(resourceVersion))
  }
  return apiFetch<T>(path, { ...options, headers })
}

const statusColor = (status: string): 'success' | 'warning' | 'error' | 'default' => {
  if (status === 'active' || status === 'delivered' || status === 'succeeded' || status === 'completed') return 'success'
  if (status === 'pending' || status === 'retrying') return 'warning'
  if (status === 'failed' || status === 'dead' || status === 'expired' || status === 'suspended') return 'error'
  return 'default'
}

const statusLabel = (status: string) => ({
  active: '启用',
  inactive: '停用',
  revoked: '已撤销',
  delivered: '已送达',
  succeeded: '成功',
  completed: '已完成',
  pending: '待处理',
  retrying: '重试中',
  failed: '失败',
  dead: '终止',
  expired: '已过期',
  suspended: '已暂停',
  processing: '处理中',
} as Record<string, string>)[status] ?? '未知状态'

const scopeLabels: Record<string, string> = {
  'tickets:read': '读取工单',
  'tickets:create': '创建工单',
  'tickets:update': '更新工单',
  'tickets:assign': '分配工单',
  'tickets:transition': '流转工单状态',
  'comments:write': '添加评论',
  'attachments:read': '读取附件',
  'attachments:write': '上传附件',
  'events:subscribe': '订阅事件',
  'tasks:manage': '管理协作任务',
}

const reasonCodeLabels: Record<string, string> = {
  scope_allowed: '权限范围允许',
  explicit_allow: '显式策略允许',
  explicit_deny: '显式策略拒绝',
  explicit_allow_required: '缺少显式允许策略',
  global_emergency_stop: '全局紧急停止',
  global_read_only: '全局只读限制',
  principal_read_only: '服务主体只读限制',
  principal_not_found: '服务主体不存在',
  principal_disabled: '服务主体已停用',
  principal_expired: '服务主体已过期',
  invalid_credential: '凭据无效或已撤销',
  credential_expired: '凭据已过期',
  scope_not_granted: '未授予所需权限范围',
  execution_guard_unavailable: '安全执行保护不可用',
  automation_loop: '检测到自动化循环',
  object_access_revoked: '对象访问权限已撤销',
  policy_denied: '策略拒绝',
  read_only_mode: '只读模式限制',
}

const eventTypeLabels: Record<string, string> = {
  'agent_control.emergency_stop.updated': '全局紧急停止已更新',
  'agent_control.read_only.updated': '全局只读模式已更新',
  'service_principal.controls.updated': '服务主体控制项已更新',
  'service_principal.created': '服务主体已创建',
  'service_principal.credential.revoked': '服务主体凭据已撤销',
  'service_principal.credential.rotated': '服务主体凭据已轮换',
  'service_principal.policy.created': '服务主体策略已创建',
  'service_principal.policy.disabled': '服务主体策略已停用',
  'ticket.lease.force_released': '工单租约已强制释放',
  'outbox.replayed': '事件投递已重新排队',
  'io.chronodesk.a2a.task.updated.v1': '智能体协作任务已更新',
  'io.chronodesk.queue.updated.v1': '工单队列已更新',
  'io.chronodesk.ticket.assigned.v1': '工单已分配',
  'io.chronodesk.ticket.attachment.created.v1': '工单附件已创建',
  'io.chronodesk.ticket.comment.created.v1': '工单评论已创建',
  'io.chronodesk.ticket.created.v1': '工单已创建',
  'io.chronodesk.ticket.deleted.v1': '工单已删除',
  'io.chronodesk.ticket.escalated.v1': '工单已升级',
  'io.chronodesk.ticket.lease.claimed.v1': '工单租约已领取',
  'io.chronodesk.ticket.lease.heartbeat.v1': '工单租约已续期',
  'io.chronodesk.ticket.lease.released.v1': '工单租约已释放',
  'io.chronodesk.ticket.transitioned.v1': '工单状态已流转',
  'io.chronodesk.ticket.updated.v1': '工单已更新',
}

const actorTypeLabels: Record<string, string> = {
  human: '人工用户',
  service_principal: 'AI 智能体',
  system: '系统',
}

const sourceProtocolLabels: Record<string, string> = {
  rest: 'REST 接口',
  mcp: 'MCP 工具',
  a2a: 'A2A 协作',
  automation: '自动化规则',
  scheduler: '定时任务',
  system: '系统内部',
}

const actionLabels: Record<string, string> = {
  'ticket.list': '查询工单列表',
  'ticket.read': '读取工单',
  'ticket.query': '查询工单',
  'ticket.create': '创建工单',
  'ticket.update': '更新工单',
  'ticket.assign': '分配工单',
  'ticket.transition': '流转工单状态',
  'ticket.escalate': '升级工单',
  'ticket.comment.create': '添加工单评论',
  'ticket.attachment.create': '上传工单附件',
  'ticket.attachment.read': '读取工单附件',
  'ticket.subscribe': '订阅工单',
  'event.list': '查询事件列表',
  'event.read': '读取事件',
  'resource:subscribe': '订阅资源',
  'external.notification.send': '发送外部通知',
}

const resourceTypeLabels: Record<string, string> = {
  ticket: '工单',
  tickets: '工单',
  comment: '评论',
  attachment: '附件',
  event: '事件',
  queue: '队列',
  task: '协作任务',
  system: '系统',
}

const scopeLabel = (scope: string) => scopeLabels[scope] ?? '自定义权限范围'
const reasonCodeLabel = (code: string) => reasonCodeLabels[code] ?? '其他策略原因'
const eventTypeLabel = (type: string) => eventTypeLabels[type] ?? '自定义领域事件'
const actorTypeLabel = (type: string) => actorTypeLabels[type] ?? '其他操作者'
const sourceProtocolLabel = (protocol: string) => sourceProtocolLabels[protocol] ?? '其他来源'
const actionLabel = (action: string) => {
  if (!action || action === '*') return '全部操作'
  return actionLabels[action] ?? '自定义操作'
}
const resourceTypeLabel = (resourceType: string) => {
  if (!resourceType || resourceType === '*') return '全部资源'
  return resourceTypeLabels[resourceType] ?? '其他资源'
}
const resourceIDLabel = (resourceID: string) => resourceID && resourceID !== '*' ? resourceID : '全部'
const MetricCard = ({ label, value, helper }: { label: string; value: number; helper: string }) => (
  <Paper variant="outlined" sx={{ p: 2.5, height: '100%' }}>
    <Typography variant="body2" sx={{
      color: "text.secondary"
    }}>
      {label}
    </Typography>
    <Typography variant="h4" sx={{ mt: 0.5, fontWeight: 650 }}>
      {value}
    </Typography>
    <Typography variant="caption" sx={{
      color: "text.secondary"
    }}>
      {helper}
    </Typography>
  </Paper>
)

const EmptyRow = ({ colSpan, message }: { colSpan: number; message: string }) => (
  <TableRow>
    <TableCell colSpan={colSpan} align="center" sx={{ py: 6, color: 'text.secondary' }}>
      {message}
    </TableCell>
  </TableRow>
)

export type AgentControlSurface = 'agent' | 'integration'
type AgentControlTab = 'principals' | 'leases' | 'attachments' | 'events' | 'outbox' | 'policy'

const surfaceTabs: Record<
  AgentControlSurface,
  readonly { id: AgentControlTab; label: string }[]
> = {
  agent: [
    { id: 'principals', label: '服务主体' },
    { id: 'leases', label: '实时租约' },
    { id: 'attachments', label: '附件扫描' },
    { id: 'outbox', label: '事件投递（Outbox）' },
    { id: 'policy', label: '策略审计' },
  ],
  integration: [
    { id: 'events', label: '领域事件' },
    { id: 'outbox', label: '事件投递（Outbox）' },
  ],
}

export const AgentControlCenter: React.FC<{
  surface?: AgentControlSurface
}> = ({ surface = 'agent' }) => {
  const notify = useNotify()
  const notifyRef = useRef(notify)
  notifyRef.current = notify
  type LoadKey = 'overview' | AgentControlTab | 'policies'
  const [projectKey, setProjectKey] = useState('')
  const [overview, setOverview] = useState<AdminOverview | null>(null)
  const [principals, setPrincipals] = useState<AdminPrincipalPage | null>(null)
  const [leases, setLeases] = useState<AdminLeasePage | null>(null)
  const [attachments, setAttachments] = useState<AdminAttachmentPage | null>(null)
  const [events, setEvents] = useState<AdminDomainEventCursorPage | null>(null)
  const [outbox, setOutbox] = useState<AdminOutboxPage | null>(null)
  const [decisions, setDecisions] = useState<AdminPolicyDecisionCursorPage | null>(null)
  const [loadingByKey, setLoadingByKey] = useState<Partial<Record<LoadKey, boolean>>>({})
  const [errorByKey, setErrorByKey] = useState<Partial<Record<LoadKey, string>>>({})
  const [tab, setTab] = useState<AgentControlTab>(surfaceTabs[surface][0].id)
  const [principalPage, setPrincipalPage] = useState(1)
  const [leasePage, setLeasePage] = useState(1)
  const [attachmentPage, setAttachmentPage] = useState(1)
  const [outboxPage, setOutboxPage] = useState(1)
  const [eventCursor, setEventCursor] = useState('')
  const [eventCursorHistory, setEventCursorHistory] = useState<string[]>([])
  const [decisionCursor, setDecisionCursor] = useState('')
  const [decisionCursorHistory, setDecisionCursorHistory] = useState<string[]>([])
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<CreatePrincipalForm>(initialCreateForm)
  const [submitting, setSubmitting] = useState(false)
  const [credentialProtectionState, setCredentialProtectionState] =
    useState<CredentialProtection | null>(
      protectedCredentialForCurrentSession,
    )
  const [policyPrincipal, setPolicyPrincipal] = useState<ServicePrincipal | null>(null)
  const [policies, setPolicies] = useState<AdminPolicyPage | null>(null)
  const [policyPage, setPolicyPage] = useState(1)
  const [policyForm, setPolicyForm] = useState<PolicyForm>(initialPolicyForm)
  const [confirmation, setConfirmation] = useState<ConfirmationRequest | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [grantNow, setGrantNow] = useState(() => Date.now())
  const requestControllers = useRef<Partial<Record<LoadKey, AbortController>>>({})
  const writeControllers = useRef<Set<AbortController>>(new Set())
  const scopeEpoch = useRef(0)
  const activeProjectKey = useRef('')
  const submittingToken = useRef<symbol | null>(null)
  const confirmingToken = useRef<symbol | null>(null)
  const credential = credentialProtectionState?.credential ?? null
  const credentialProjectKey = credentialProtectionState?.projectKey ?? ''
  const credentialProtectionActive = credentialProtectionState !== null

  const beginSubmitting = useCallback(() => {
    const token = Symbol('agent-control-submit')
    submittingToken.current = token
    setSubmitting(true)
    return token
  }, [])

  const endSubmitting = useCallback((token: symbol) => {
    if (submittingToken.current !== token) return
    submittingToken.current = null
    setSubmitting(false)
  }, [])

  const beginCredentialProtection = useCallback((scope: ScopeSnapshot) => {
    const storedProjectSelection = window.localStorage.getItem(
      activeProjectStorageKey,
    )
    publishProtectedCredential({
      projectKey: scope.projectKey,
      storedProjectSelection,
      sessionBinding: projectSelectionSessionBinding(storedProjectSelection),
      credential: null,
    })
  }, [])

  const endCredentialProtection = useCallback(() => {
    publishProtectedCredential(null)
  }, [])

  const abortScopedRequests = useCallback(() => {
    for (const controller of Object.values(requestControllers.current)) controller?.abort()
    for (const controller of writeControllers.current) controller.abort()
    requestControllers.current = {}
    writeControllers.current.clear()
  }, [])

  const isScopeCurrent = useCallback((scope: ScopeSnapshot): boolean => (
    scopeEpoch.current === scope.epoch
    && (!activeProjectKey.current || activeProjectKey.current === scope.projectKey)
  ), [])

  const resolveScopeSnapshot = useCallback(async (): Promise<ScopeSnapshot | null> => {
    const epoch = scopeEpoch.current
    const projectKey = activeProjectKey.current || await resolveActiveProjectKey()
    const scope = { projectKey, epoch }
    return isScopeCurrent(scope) ? scope : null
  }, [isScopeCurrent])

  const runScopedWrite = useCallback(async <T,>(
    scope: ScopeSnapshot,
    path: string,
    options: RequestInit,
    resourceVersion?: number,
  ): Promise<ScopedWriteResult<T>> => {
    if (!isScopeCurrent(scope)) return { current: false }
    const controller = new AbortController()
    writeControllers.current.add(controller)
    try {
      const data = await adminWrite<T>(
        path,
        { ...options, signal: controller.signal },
        resourceVersion,
      )
      if (controller.signal.aborted || !isScopeCurrent(scope)) {
        return { current: false }
      }
      return { current: true, data }
    } catch (requestError) {
      if (controller.signal.aborted || !isScopeCurrent(scope)) {
        return { current: false }
      }
      throw requestError
    } finally {
      writeControllers.current.delete(controller)
    }
  }, [isScopeCurrent])

  const runCredentialWrite = useCallback(async (
    scope: ScopeSnapshot,
    path: string,
    options: RequestInit,
    resourceVersion?: number,
  ): Promise<CredentialResult | null> => {
    if (!isScopeCurrent(scope)) return null
    beginCredentialProtection(scope)
    try {
      // Credential creation and rotation may commit before the response
      // reaches the browser. Do not attach these one-time-secret responses to
      // the ordinary scope AbortController/epoch path: project switching is
      // locked until the operator explicitly acknowledges the secret.
      const result = await adminWrite<CredentialResult>(
        path,
        options,
        resourceVersion,
      )
      const protection = protectedCredential
      if (protection?.projectKey === scope.projectKey) {
        publishProtectedCredential({ ...protection, credential: result })
      }
      return result
    } catch (requestError) {
      endCredentialProtection()
      throw requestError
    }
  }, [beginCredentialProtection, endCredentialProtection, isScopeCurrent])

  const runRequest = useCallback(async <T,>(
    key: LoadKey,
    projectKey: string,
    path: string,
    fallback: string,
    setData: (data: T) => void,
  ): Promise<T | null> => {
    const scope = { projectKey, epoch: scopeEpoch.current }
    if (!isScopeCurrent(scope)) return null
    requestControllers.current[key]?.abort()
    const controller = new AbortController()
    requestControllers.current[key] = controller
    setLoadingByKey((current) => ({ ...current, [key]: true }))
    setErrorByKey((current) => ({ ...current, [key]: '' }))
    try {
      const result = await apiFetch<T>(
        path,
        { signal: controller.signal },
      )
      if (
        controller.signal.aborted
        || requestControllers.current[key] !== controller
        || !isScopeCurrent(scope)
      ) return null
      setData(result)
      return result
    } catch (requestError) {
      if (
        controller.signal.aborted
        || requestControllers.current[key] !== controller
        || !isScopeCurrent(scope)
      ) return null
      setErrorByKey((current) => ({
        ...current,
        [key]: localizedUnknownErrorMessage(requestError, fallback),
      }))
      return null
    } finally {
      if (requestControllers.current[key] === controller) {
        delete requestControllers.current[key]
        setLoadingByKey((current) => ({ ...current, [key]: false }))
      }
    }
  }, [isScopeCurrent])

  const loadOverview = useCallback((key: string) => runRequest<AdminOverview>(
    'overview',
    key,
    humanApiRoutes.getAgentControlOverviewV2({ projectKey: key }),
    '智能体控制指标加载失败',
    setOverview,
  ), [runRequest])

  const loadPrincipals = useCallback((key: string, page: number) => runRequest<AdminPrincipalPage>(
    'principals',
    key,
    humanApiRoutes.listAgentServicePrincipals(
      { projectKey: key },
      { page, page_size: 25, sort_by: 'created_at', sort_order: 'desc' },
    ),
    '服务主体加载失败',
    setPrincipals,
  ), [runRequest])

  const loadLeases = useCallback((key: string, page: number) => runRequest<AdminLeasePage>(
    'leases',
    key,
    humanApiRoutes.listAgentTicketLeases(
      { projectKey: key },
      { page, page_size: 25, sort_by: 'expires_at', sort_order: 'asc' },
    ),
    '工单租约加载失败',
    setLeases,
  ), [runRequest])

  const loadAttachments = useCallback((key: string, page: number) => runRequest<AdminAttachmentPage>(
    'attachments',
    key,
    humanApiRoutes.listAgentAttachmentScans(
      { projectKey: key },
      { page, page_size: 25, sort_by: 'created_at', sort_order: 'desc' },
    ),
    '附件扫描状态加载失败',
    setAttachments,
  ), [runRequest])

  const loadEvents = useCallback((key: string, cursor: string) => runRequest<AdminDomainEventCursorPage>(
    'events',
    key,
    humanApiRoutes.listAgentDomainEvents(
      { projectKey: key },
      { cursor, limit: 25 },
    ),
    '领域事件加载失败',
    setEvents,
  ), [runRequest])

  const loadOutbox = useCallback((key: string, page: number) => runRequest<AdminOutboxPage>(
    'outbox',
    key,
    humanApiRoutes.listAgentOutboxDeliveries(
      { projectKey: key },
      { page, page_size: 25, sort_by: 'created_at', sort_order: 'desc' },
    ),
    '事件投递记录加载失败',
    setOutbox,
  ), [runRequest])

  const loadDecisions = useCallback((key: string, cursor: string) => runRequest<AdminPolicyDecisionCursorPage>(
    'policy',
    key,
    humanApiRoutes.listAgentPolicyDecisions(
      { projectKey: key },
      { cursor, limit: 25 },
    ),
    '策略决策加载失败',
    setDecisions,
  ), [runRequest])

  const loadPolicies = useCallback((
    key: string,
    principalID: string,
    page: number,
  ) => runRequest<AdminPolicyPage>(
    'policies',
    key,
    humanApiRoutes.listServicePrincipalPoliciesV2(
      { projectKey: key, principalId: principalID },
      { page, page_size: 25, sort_by: 'priority', sort_order: 'desc' },
    ),
    '策略加载失败',
    setPolicies,
  ), [runRequest])

  const clearScopedState = useCallback(() => {
    scopeEpoch.current += 1
    activeProjectKey.current = ''
    abortScopedRequests()
    setProjectKey('')
    setOverview(null)
    setPrincipals(null)
    setLeases(null)
    setAttachments(null)
    setEvents(null)
    setOutbox(null)
    setDecisions(null)
    setPolicies(null)
    setPolicyPrincipal(null)
    setCreateOpen(false)
    setCreateForm(initialCreateForm)
    setPolicyForm(initialPolicyForm)
    setConfirmation(null)
    submittingToken.current = null
    confirmingToken.current = null
    setSubmitting(false)
    setConfirming(false)
    setLoadingByKey({})
    setErrorByKey({})
    setPrincipalPage(1)
    setLeasePage(1)
    setAttachmentPage(1)
    setOutboxPage(1)
    setEventCursor('')
    setEventCursorHistory([])
    setDecisionCursor('')
    setDecisionCursorHistory([])
    setPolicyPage(1)
  }, [abortScopedRequests])

  useEffect(() => {
    const updateCredentialProtection = (
      value: CredentialProtection | null,
    ) => {
      if (
        value
        && value.sessionBinding !== projectSelectionSessionBinding(
          window.localStorage.getItem(activeProjectStorageKey),
        )
      ) {
        protectedCredential = null
        setCredentialProtectionState(null)
        return
      }
      setCredentialProtectionState(value)
    }
    protectedCredentialListeners.add(updateCredentialProtection)
    setCredentialProtectionState(protectedCredentialForCurrentSession())
    return () => {
      protectedCredentialListeners.delete(updateCredentialProtection)
    }
  }, [])

  useEffect(() => {
    if (!credentialProtectionActive) return

    const switcher = document.querySelector<HTMLElement>(
      '[data-testid="active-project-switcher"]',
    )
    const previousAriaDisabled = switcher?.getAttribute('aria-disabled')
    switcher?.setAttribute('aria-disabled', 'true')

    const blockProjectSwitcherInteraction = (event: Event) => {
      const target = event.target
      if (
        target instanceof Element
        && target.closest('[data-testid="work-project-control"]')
      ) {
        event.preventDefault()
        event.stopImmediatePropagation()
      }
    }
    document.addEventListener('pointerdown', blockProjectSwitcherInteraction, true)
    document.addEventListener('keydown', blockProjectSwitcherInteraction, true)
    return () => {
      document.removeEventListener('pointerdown', blockProjectSwitcherInteraction, true)
      document.removeEventListener('keydown', blockProjectSwitcherInteraction, true)
      if (previousAriaDisabled == null) {
        switcher?.removeAttribute('aria-disabled')
      } else {
        switcher?.setAttribute('aria-disabled', previousAriaDisabled)
      }
    }
  }, [credentialProtectionActive])

  useEffect(() => {
    const timer = window.setInterval(() => setGrantNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    let mounted = true
    const initialEpoch = scopeEpoch.current
    const activate = (nextProjectKey: string) => {
      if (!mounted || !nextProjectKey.trim()) return
      clearScopedState()
      activeProjectKey.current = nextProjectKey
      setProjectKey(nextProjectKey)
    }
    void resolveActiveProjectKey().then((nextProjectKey) => {
      if (scopeEpoch.current === initialEpoch) activate(nextProjectKey)
    }).catch((requestError) => {
      if (!mounted) return
      setErrorByKey({
        overview: localizedUnknownErrorMessage(requestError, '当前项目加载失败'),
      })
    })
    const handleScopeChange = (event: Event) => {
      const nextProjectKey = (event as CustomEvent<{ project_key?: string }>).detail?.project_key
      const protection = protectedCredential
      if (nextProjectKey && protection) {
        if (protection.storedProjectSelection === null) {
          window.localStorage.removeItem(activeProjectStorageKey)
        } else {
          window.localStorage.setItem(
            activeProjectStorageKey,
            protection.storedProjectSelection,
          )
        }
        event.preventDefault()
        event.stopImmediatePropagation()
        notifyRef.current(
          `项目 ${protection.projectKey} 的一次性凭据正在签发或尚未保存，请保存后再切换项目`,
          { type: 'warning' },
        )
        return
      }
      if (nextProjectKey) activate(nextProjectKey)
    }
    window.addEventListener(projectScopeChangedEvent, handleScopeChange, true)
    return () => {
      mounted = false
      window.removeEventListener(projectScopeChangedEvent, handleScopeChange, true)
      scopeEpoch.current += 1
      activeProjectKey.current = ''
      abortScopedRequests()
    }
  }, [abortScopedRequests, clearScopedState])

  useEffect(() => {
    if (projectKey) void loadOverview(projectKey)
  }, [loadOverview, projectKey])
  useEffect(() => {
    if (projectKey && tab === 'principals') void loadPrincipals(projectKey, principalPage)
  }, [loadPrincipals, principalPage, projectKey, tab])
  useEffect(() => {
    if (projectKey && tab === 'leases') void loadLeases(projectKey, leasePage)
  }, [leasePage, loadLeases, projectKey, tab])
  useEffect(() => {
    if (projectKey && tab === 'attachments') void loadAttachments(projectKey, attachmentPage)
  }, [attachmentPage, loadAttachments, projectKey, tab])
  useEffect(() => {
    if (projectKey && tab === 'events') void loadEvents(projectKey, eventCursor)
  }, [eventCursor, loadEvents, projectKey, tab])
  useEffect(() => {
    if (projectKey && tab === 'outbox') void loadOutbox(projectKey, outboxPage)
  }, [loadOutbox, outboxPage, projectKey, tab])
  useEffect(() => {
    if (projectKey && tab === 'policy') void loadDecisions(projectKey, decisionCursor)
  }, [decisionCursor, loadDecisions, projectKey, tab])
  useEffect(() => {
    if (projectKey && policyPrincipal) {
      void loadPolicies(projectKey, policyPrincipal.id, policyPage)
    }
  }, [loadPolicies, policyPage, policyPrincipal, projectKey])

  const createPrincipal = async () => {
    if (!createForm.name.trim() || createForm.scopes.length === 0) {
      notify('请填写名称并至少选择一个权限范围', { type: 'warning' })
      return
    }

    const submitting = beginSubmitting()
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const result = await runCredentialWrite(
        scope,
        humanApiRoutes.createServicePrincipalV2({ projectKey: scope.projectKey }),
        {
          method: 'POST',
          body: JSON.stringify({
            ...createForm,
            name: createForm.name.trim(),
            description: createForm.description.trim(),
          }),
        },
      )
      if (!result) return
      setCreateOpen(false)
      setCreateForm(initialCreateForm)
      notify('服务主体已创建，请立即保存一次性密钥', { type: 'success' })
      setPrincipalPage(1)
      await Promise.all([
        loadPrincipals(scope.projectKey, 1),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '创建失败'), { type: 'error' })
    } finally {
      endSubmitting(submitting)
    }
  }

  const rotateCredential = async (principal: ServicePrincipal) => {
    const grantState = principalGrantState(principal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能轮换凭据`, { type: 'warning' })
      return
    }
    const submitting = beginSubmitting()
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const result = await runCredentialWrite(
        scope,
        humanApiRoutes.rotateServicePrincipalCredentialV2({
          projectKey: scope.projectKey,
          principalId: principal.id,
        }),
        { method: 'POST' },
        principal.resource_version,
      )
      if (!result) return
      notify('凭据已轮换，旧凭据已撤销', { type: 'success' })
      await Promise.all([
        loadPrincipals(scope.projectKey, principalPage),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '凭据轮换失败'), { type: 'error' })
    } finally {
      endSubmitting(submitting)
    }
  }

  const togglePrincipal = async (principal: ServicePrincipal) => {
    const grantState = principalGrantState(principal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能修改服务主体状态`, { type: 'warning' })
      return
    }
    const nextStatus: PrincipalStatus = principal.status === 'active' ? 'inactive' : 'active'
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.setServicePrincipalStatusV2({
          projectKey: scope.projectKey,
          principalId: principal.id,
        }),
        {
          method: 'PUT',
          body: JSON.stringify({ status: nextStatus }),
        },
        principal.resource_version,
      )
      if (!write.current) return
      notify(nextStatus === 'active' ? '智能体已启用' : '智能体已停用', { type: 'success' })
      await Promise.all([
        loadPrincipals(scope.projectKey, principalPage),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '状态更新失败'), { type: 'error' })
    }
  }

  const togglePrincipalEmergency = async (principal: ServicePrincipal) => {
    const grantState = principalGrantState(principal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能修改熔断状态`, { type: 'warning' })
      return
    }
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.setServicePrincipalStatusV2({
          projectKey: scope.projectKey,
          principalId: principal.id,
        }),
        {
          method: 'PUT',
          body: JSON.stringify({ emergency_disabled: !principal.emergency_disabled }),
        },
        principal.resource_version,
      )
      if (!write.current) return
      notify(principal.emergency_disabled ? '智能体熔断已解除' : '智能体已立即熔断', {
        type: principal.emergency_disabled ? 'success' : 'warning',
      })
      await Promise.all([
        loadPrincipals(scope.projectKey, principalPage),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '智能体熔断更新失败'), { type: 'error' })
    }
  }

  const openPolicies = (principal: ServicePrincipal) => {
    const grantState = principalGrantState(principal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能管理策略`, { type: 'warning' })
      return
    }
    setPolicyPrincipal(principal)
    setPolicyForm(initialPolicyForm)
    setPolicies(null)
    setPolicyPage(1)
  }

  const createPolicy = async () => {
    if (!policyPrincipal) return
    const grantState = principalGrantState(policyPrincipal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能新增策略`, { type: 'warning' })
      return
    }
    const submitting = beginSubmitting()
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.createServicePrincipalPolicyV2({
          projectKey: scope.projectKey,
          principalId: policyPrincipal.id,
        }),
        {
          method: 'POST',
          body: JSON.stringify(policyForm),
        },
        policyPrincipal.resource_version,
      )
      if (!write.current) return
      await loadPolicies(scope.projectKey, policyPrincipal.id, policyPage)
      if (!isScopeCurrent(scope)) return
      setPolicyForm(initialPolicyForm)
      const refreshed = await loadPrincipals(scope.projectKey, principalPage)
      await loadOverview(scope.projectKey)
      if (!isScopeCurrent(scope)) return
      const refreshedPrincipal = refreshed?.items.find((item) => item.id === policyPrincipal.id)
      if (refreshedPrincipal) setPolicyPrincipal(refreshedPrincipal)
      notify('策略已创建', { type: 'success' })
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '策略创建失败'), { type: 'error' })
    } finally {
      endSubmitting(submitting)
    }
  }

  const disablePolicy = async (policy: AgentPolicy) => {
    if (!policyPrincipal) return
    const grantState = principalGrantState(policyPrincipal)
    if (!grantState.usable) {
      notify(`${grantState.label}，不能停用策略`, { type: 'warning' })
      return
    }
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.disableServicePrincipalPolicyV2({
          projectKey: scope.projectKey,
          principalId: policyPrincipal.id,
          policyId: policy.id,
        }),
        {
          method: 'DELETE',
        },
        policy.resource_version,
      )
      if (!write.current) return
      setPolicies((current) => current ? {
        ...current,
        items: current.items.map((item) => (
          item.id === policy.id ? { ...item, is_active: false } : item
        )),
      } : current)
      notify('策略已停用', { type: 'success' })
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '策略停用失败'), { type: 'error' })
    }
  }

  const forceReleaseLease = async (lease: TicketLease) => {
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.forceReleaseTicketLeaseV2({
          projectKey: scope.projectKey,
          leaseId: lease.id,
        }),
        { method: 'POST' },
        lease.resource_version,
      )
      if (!write.current) return
      notify('工单租约已强制释放', { type: 'success' })
      await Promise.all([
        loadLeases(scope.projectKey, leasePage),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '租约释放失败'), { type: 'error' })
    }
  }

  const replayDelivery = async (delivery: OutboxDelivery) => {
    try {
      const scope = await resolveScopeSnapshot()
      if (!scope) return
      const write = await runScopedWrite(
        scope,
        humanApiRoutes.replayOutboxDeliveryV2({
          projectKey: scope.projectKey,
          deliveryId: delivery.id,
        }),
        { method: 'POST' },
        delivery.resource_version,
      )
      if (!write.current) return
      notify('事件投递已重新排队', { type: 'success' })
      await Promise.all([
        loadOutbox(scope.projectKey, outboxPage),
        loadOverview(scope.projectKey),
      ])
    } catch (requestError) {
      if (
        projectKey
        && activeProjectKey.current === projectKey
      ) {
        await loadOutbox(projectKey, outboxPage)
      }
      notify(localizedUnknownErrorMessage(requestError, '事件投递回放失败'), { type: 'error' })
    }
  }

  const requestConfirmation = (request: ConfirmationRequest) => {
    setConfirmation(request)
  }

  const runConfirmedAction = async () => {
    if (!confirmation) return

    const requestedConfirmation = confirmation
    const token = Symbol('agent-control-confirmation')
    confirmingToken.current = token
    setConfirming(true)
    try {
      await requestedConfirmation.action()
      if (confirmingToken.current === token) {
        setConfirmation((current) => (
          current === requestedConfirmation ? null : current
        ))
      }
    } finally {
      if (confirmingToken.current === token) {
        confirmingToken.current = null
        setConfirming(false)
      }
    }
  }

  const confirmRotateCredential = (principal: ServicePrincipal) => {
    requestConfirmation({
      title: '确认轮换智能体凭据',
      description: `轮换后，${principal.name} 的所有旧凭据会立即撤销，使用旧密钥的运行中智能体将无法继续访问。`,
      confirmLabel: '轮换并撤销旧凭据',
      color: 'warning',
      action: () => rotateCredential(principal),
    })
  }

  const confirmTogglePrincipal = (principal: ServicePrincipal) => {
    if (principal.status === 'revoked') return

    const activating = principal.status !== 'active'
    requestConfirmation({
      title: activating ? '确认启用智能体' : '确认停用智能体',
      description: activating
        ? `启用后，${principal.name} 可再次获取令牌并在权限范围与策略允许范围内执行操作。`
        : `停用后，${principal.name} 将无法继续获取或使用智能体访问权限。`,
      confirmLabel: activating ? '确认启用' : '确认停用',
      color: activating ? 'primary' : 'warning',
      action: () => togglePrincipal(principal),
    })
  }

  const confirmTogglePrincipalEmergency = (principal: ServicePrincipal) => {
    const releasing = principal.emergency_disabled
    requestConfirmation({
      title: releasing ? '确认解除智能体熔断' : '确认立即熔断智能体',
      description: releasing
        ? `解除后，${principal.name} 会立即恢复访问，请确认异常行为已处置。`
        : `熔断后，${principal.name} 的所有请求会立即被拒绝。`,
      confirmLabel: releasing ? '解除熔断' : '立即熔断',
      color: 'error',
      action: () => togglePrincipalEmergency(principal),
    })
  }

  const confirmDisablePolicy = (policy: AgentPolicy) => {
    requestConfirmation({
      title: '确认停用智能体策略',
      description: `将停用“${scopeLabel(policy.scope)} / ${actionLabel(policy.action || '')}”策略。停用允许策略可能中断智能体，停用拒绝策略可能扩大权限。`,
      confirmLabel: '确认停用策略',
      color: 'warning',
      action: () => disablePolicy(policy),
    })
  }

  const confirmForceReleaseLease = (lease: TicketLease) => {
    requestConfirmation({
      title: '确认强制释放工单租约',
      description: `将强制释放 ${lease.ticket_number || `工单 #${lease.ticket_id}`} 的租约。当前智能体可能仍在处理，后续提交将发生租约冲突。`,
      confirmLabel: '强制释放租约',
      color: 'warning',
      action: () => forceReleaseLease(lease),
    })
  }

  const confirmReplayDelivery = (delivery: OutboxDelivery) => {
    requestConfirmation({
      title: '确认回放事件投递',
      description: `事件 ${delivery.event_id} 将重新进入投递队列。接收端必须按事件 ID 去重，否则可能产生重复副作用。`,
      confirmLabel: '确认重新投递',
      color: 'warning',
      action: () => replayDelivery(delivery),
    })
  }

  const confirmCreateAllowPolicy = () => {
    requestConfirmation({
      title: '确认新增允许策略',
      description: `这会为 ${policyPrincipal?.name ?? '该智能体'} 授予“${scopeLabel(policyForm.scope)} / ${actionLabel(policyForm.action || '')}”权限，请确认资源范围“${resourceTypeLabel(policyForm.resource_type || '')}：${resourceIDLabel(policyForm.resource_id)}”。`,
      confirmLabel: '确认授予权限',
      color: 'warning',
      action: createPolicy,
    })
  }

  const copySecret = async () => {
    if (!credential) return
    await navigator.clipboard.writeText(credential.client_secret)
    notify('密钥已复制', { type: 'info' })
  }

  const loadActiveTab = () => {
    if (!projectKey) return
    switch (tab) {
      case 'principals':
        void loadPrincipals(projectKey, principalPage)
        break
      case 'leases':
        void loadLeases(projectKey, leasePage)
        break
      case 'attachments':
        void loadAttachments(projectKey, attachmentPage)
        break
      case 'events':
        void loadEvents(projectKey, eventCursor)
        break
      case 'outbox':
        void loadOutbox(projectKey, outboxPage)
        break
      case 'policy':
        void loadDecisions(projectKey, decisionCursor)
        break
    }
  }
  const activeTabHasData = {
    principals: principals !== null,
    leases: leases !== null,
    attachments: attachments !== null,
    events: events !== null,
    outbox: outbox !== null,
    policy: decisions !== null,
  }[tab]
  const activeTabLoading = loadingByKey[tab] === true
  const activeTabError = errorByKey[tab] ?? ''
  const policyGrantState = policyPrincipal
    ? principalGrantState(policyPrincipal, grantNow)
    : null

  return (
    <>
      <Title title={surface === 'agent' ? 'AI 智能体控制中心' : '集成运行监控'} />
      <Box sx={{ p: { xs: 2, md: 3 } }}>
        <Stack
          direction={{ xs: 'column', md: 'row' }}
          spacing={2}
          sx={{
            justifyContent: "space-between",
            alignItems: { xs: 'stretch', md: 'center' },
            mb: 3
          }}>
          <Box>
            <Stack direction="row" spacing={1} sx={{
              alignItems: "center"
            }}>
              <AgentIcon color="primary" />
              <Typography variant="h4">
                {surface === 'agent' ? 'AI 智能体控制中心' : '集成运行监控'}
              </Typography>
            </Stack>
            <Typography
              sx={{
                color: "text.secondary",
                mt: 0.5
              }}>
              {surface === 'agent'
                ? '管理服务主体、最小权限、实时租约与策略决策。'
                : '查看项目领域事件与 Outbox 投递状态。'}
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} sx={{
            alignItems: "center"
          }}>
            {surface === 'agent' && (
              <>
                <FormControlLabel
                  control={
                    <Switch
                      checked={overview?.emergency_stop ?? false}
                      color="error"
                      disabled
                      slotProps={{ input: { 'aria-label': '智能体全局紧急停止' } }}
                    />
                  }
                  label="紧急停止"
                />
                <FormControlLabel
                  control={
                    <Switch
                      checked={overview?.global_read_only ?? false}
                      color="warning"
                      disabled
                      slotProps={{ input: { 'aria-label': '智能体全局只读模式' } }}
                    />
                  }
                  label="全局只读"
                />
              </>
            )}
            <Tooltip title="刷新">
              <span>
                <IconButton
                  onClick={() => {
                    if (projectKey) void loadOverview(projectKey)
                    loadActiveTab()
                  }}
                  disabled={loadingByKey.overview || activeTabLoading}
                  aria-label="刷新控制面"
                >
                  <RefreshIcon />
                </IconButton>
              </span>
            </Tooltip>
            {surface === 'agent' && (
              <Button
                variant="contained"
                startIcon={<AddIcon />}
                onClick={() => setCreateOpen(true)}
                disabled={credentialProtectionActive}
              >
                新建智能体
              </Button>
            )}
          </Stack>
        </Stack>

        {surface === 'agent' && (
          <Alert severity="info" sx={{ mb: 3 }}>
            全局只读和紧急停止属于平台级安全控制，本项目页面仅展示状态；变更入口已与项目业务操作隔离。
          </Alert>
        )}

        {credentialProtectionActive && (
          <Alert severity="warning" sx={{ mb: 3 }}>
            一次性凭据正在签发或尚未保存，保存前已暂时锁定项目切换。
          </Alert>
        )}

        {surface === 'agent' && overview?.global_read_only && (
          <Alert severity="warning" icon={<PausedIcon />} sx={{ mb: 3 }}>
            智能体全局只读模式已开启。MCP 与 A2A 查询仍可用，所有智能体写操作将被策略层拒绝。
          </Alert>
        )}
        {surface === 'agent' && overview?.emergency_stop && (
          <Alert severity="error" icon={<StopIcon />} sx={{ mb: 3 }}>
            智能体全局紧急停止已启用。所有智能体请求都会被拒绝，管理员仍可检查审计并接管租约。
          </Alert>
        )}
        {errorByKey.overview && (
          <Alert
            severity="error"
            action={<Button onClick={() => projectKey && void loadOverview(projectKey)}>重试</Button>}
            sx={{ mb: 3 }}
          >
            {errorByKey.overview}
          </Alert>
        )}

        <Grid container spacing={2} sx={{ mb: 3 }}>
          {surface === 'agent' ? (
            <>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="活跃智能体" value={overview?.active_principal_count ?? 0} helper="服务端统计的有效项目授权" />
              </Grid>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="实时租约" value={overview?.active_lease_count ?? 0} helper="服务端统计的活跃租约" />
              </Grid>
            </>
          ) : (
            <>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="近期事件" value={overview?.recent_event_count ?? 0} helper="最近 24 小时的项目事件" />
              </Grid>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="投递失败" value={overview?.failed_outbox_count ?? 0} helper="服务端统计的失败与终止投递" />
              </Grid>
            </>
          )}
        </Grid>

        <Paper variant="outlined">
          <Tabs
            value={tab}
            onChange={(_, nextTab: AgentControlTab) => setTab(nextTab)}
            variant="scrollable"
            scrollButtons="auto"
            aria-label={surface === 'agent' ? 'AI 智能体控制面数据' : '集成运行监控数据'}
          >
            {surfaceTabs[surface].map((item) => (
              <Tab key={item.id} value={item.id} label={item.label} />
            ))}
          </Tabs>
          <Divider />

          {activeTabError && (
            <Alert
              severity="error"
              action={<Button onClick={loadActiveTab}>重试</Button>}
              sx={{ m: 2 }}
            >
              {activeTabError}
            </Alert>
          )}
          {activeTabLoading && !activeTabHasData ? (
            <Stack
              aria-busy="true"
              sx={{
                alignItems: "center",
                py: 8
              }}>
              <CircularProgress size={30} />
              <Typography
                sx={{
                  color: "text.secondary",
                  mt: 2
                }}>
                正在加载当前列表…
              </Typography>
            </Stack>
          ) : (
            <TableContainer>
              {tab === 'principals' && (
                <ResizableMuiTable tableId="agent-control.principals" columns={principalColumns} size="small" aria-label="服务主体列表">
                  <TableHead>
                    <TableRow>
                      <TableCell>AI 智能体</TableCell>
                      <TableCell>状态</TableCell>
                      <TableCell>权限范围（Scope）</TableCell>
                      <TableCell>限额</TableCell>
                      <TableCell>最近使用</TableCell>
                      <TableCell align="right">操作</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(principals?.items ?? []).map((principal) => {
                      const grantState = principalGrantState(principal, grantNow)
                      return (
                      <TableRow key={principal.id} hover>
                        <TableCell>
                          <InlineDetails
                            primary={principal.name}
                            secondary={principal.client_id}
                            title={`${principal.name} · ${principal.client_id}`}
                          />
                        </TableCell>
                        <TableCell>
                          <Stack
                            direction="row"
                            spacing={0.5}
                            sx={{ alignItems: 'center', flexWrap: 'nowrap', overflow: 'hidden' }}
                          >
                            <Tooltip title={`状态代码：${principal.status}`}>
                              <Chip
                                size="small"
                                label={statusLabel(principal.status)}
                                color={statusColor(principal.status)}
                                variant="outlined"
                              />
                            </Tooltip>
                            <Tooltip title={grantState.detail}>
                              <Chip
                                size="small"
                                label={grantState.label}
                                color={grantState.color}
                                variant="outlined"
                              />
                            </Tooltip>
                            {principal.emergency_disabled && <Chip size="small" label="已熔断" color="error" />}
                            {principal.read_only && <Chip size="small" label="只读" color="warning" />}
                          </Stack>
                        </TableCell>
                        <TableCell sx={{ maxWidth: 430 }}>
                          <Stack
                            direction="row"
                            sx={{
                              gap: 0.5,
                              flexWrap: "nowrap",
                              overflow: "hidden"
                            }}>
                            {principal.scopes.map((scope) => (
                              <Tooltip key={scope} title={`权限代码：${scope}`}>
                                <Chip size="small" label={scopeLabel(scope)} />
                              </Tooltip>
                            ))}
                          </Stack>
                        </TableCell>
                        <TableCell>
                          <InlineDetails
                            primary={`${principal.rate_limit}/分钟`}
                            secondary={`并发 ${principal.concurrency_limit}`}
                            title={`速率限制：${principal.rate_limit}/分钟 · 并发限制：${principal.concurrency_limit}`}
                            primaryFontWeight={400}
                          />
                        </TableCell>
                        <TableCell>{formatDate(principal.last_used_at)}</TableCell>
                        <TableCell align="right">
                          <Tooltip title={grantState.usable ? '权限范围策略' : `${grantState.label}，策略管理不可用`}>
                            <span>
                              <IconButton
                                size="small"
                                onClick={() => void openPolicies(principal)}
                                disabled={!grantState.usable || submitting}
                                aria-label={`管理 ${principal.name} 的策略`}
                              >
                                <PolicyIcon fontSize="small" />
                              </IconButton>
                            </span>
                          </Tooltip>
                          <Tooltip title={
                            credentialProtectionActive
                              ? '请先保存当前一次性凭据'
                              : grantState.usable
                                ? '轮换凭据'
                                : `${grantState.label}，凭据轮换不可用`
                          }>
                            <span>
                              <IconButton
                                size="small"
                                onClick={() => confirmRotateCredential(principal)}
                                disabled={
                                  !grantState.usable
                                  || submitting
                                  || credentialProtectionActive
                                }
                                aria-label={`轮换 ${principal.name} 的凭据`}
                              >
                                <RotateIcon fontSize="small" />
                              </IconButton>
                            </span>
                          </Tooltip>
                          <Tooltip title={
                            grantState.usable
                              ? principal.emergency_disabled ? '解除单个智能体熔断' : '立即熔断智能体'
                              : `${grantState.label}，熔断操作不可用`
                          }>
                            <span>
                              <IconButton
                                size="small"
                                color={principal.emergency_disabled ? 'success' : 'error'}
                                onClick={() => confirmTogglePrincipalEmergency(principal)}
                                disabled={!grantState.usable || submitting}
                                aria-label={`${principal.emergency_disabled ? '解除' : '启用'} ${principal.name} 的熔断`}
                              >
                                <StopIcon fontSize="small" />
                              </IconButton>
                            </span>
                          </Tooltip>
                          <Tooltip title={
                            grantState.usable ? '' : `${grantState.label}，状态操作不可用`
                          }>
                            <span>
                              <Button
                                size="small"
                                color={principal.status === 'active' ? 'warning' : 'primary'}
                                onClick={() => confirmTogglePrincipal(principal)}
                                disabled={principal.status === 'revoked' || !grantState.usable || submitting}
                              >
                                {principal.status === 'revoked' ? '已撤销' : principal.status === 'active' ? '停用' : '启用'}
                              </Button>
                            </span>
                          </Tooltip>
                        </TableCell>
                      </TableRow>
                      )
                    })}
                    {(principals?.items.length ?? 0) === 0 && (
                      <EmptyRow colSpan={6} message="还没有服务主体。创建后即可通过 OAuth 获取智能体访问令牌。" />
                    )}
                  </TableBody>
                </ResizableMuiTable>
              )}

              {tab === 'leases' && (
                <ResizableMuiTable tableId="agent-control.leases" columns={leaseColumns} size="small" aria-label="工单租约列表">
                  <TableHead>
                    <TableRow>
                      <TableCell>工单</TableCell>
                      <TableCell>AI 智能体</TableCell>
                      <TableCell>资源版本</TableCell>
                      <TableCell>领取时间</TableCell>
                      <TableCell>到期时间</TableCell>
                      <TableCell align="right">接管</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(leases?.items ?? []).map((lease) => (
                      <TableRow key={lease.id} hover>
                        <TableCell>
                          <TruncatedText title={lease.ticket_number || `工单 #${lease.ticket_id}`}>
                            {lease.ticket_number || `工单 #${lease.ticket_id}`}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <InlineDetails
                            primary={lease.holder_display_name}
                            secondary={`${actorTypeLabel(lease.holder_actor_type)}：${lease.holder_actor_id}`}
                            title={`${lease.holder_display_name} · ${lease.holder_actor_type}:${lease.holder_actor_id}`}
                            primaryFontWeight={400}
                          />
                        </TableCell>
                        <TableCell>v{lease.ticket_version}</TableCell>
                        <TableCell>{formatDate(lease.acquired_at)}</TableCell>
                        <TableCell>{formatDate(lease.expires_at)}</TableCell>
                        <TableCell align="right">
                          <Button size="small" color="warning" onClick={() => confirmForceReleaseLease(lease)}>
                            强制释放
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                    {(leases?.items.length ?? 0) === 0 && <EmptyRow colSpan={6} message="当前没有活跃工单租约。" />}
                  </TableBody>
                </ResizableMuiTable>
              )}

              {tab === 'attachments' && (
                <ResizableMuiTable tableId="agent-control.attachments" columns={attachmentColumns} size="small" aria-label="附件扫描列表">
                  <TableHead>
                    <TableRow>
                      <TableCell>附件</TableCell>
                      <TableCell>工单</TableCell>
                      <TableCell>类型</TableCell>
                      <TableCell>大小</TableCell>
                      <TableCell>扫描状态</TableCell>
                      <TableCell>更新时间</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(attachments?.items ?? []).map((attachment: AttachmentScan) => (
                      <TableRow key={attachment.id} hover>
                        <TableCell>
                          <TruncatedText title={attachment.original_name}>
                            {attachment.original_name}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>#{attachment.ticket_id}</TableCell>
                        <TableCell>
                          <TruncatedText title={attachment.mime_type || '—'}>
                            {attachment.mime_type || '—'}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>{attachment.file_size.toLocaleString('zh-CN')} 字节</TableCell>
                        <TableCell>
                          <Chip
                            size="small"
                            label={{
                              pending: '待扫描',
                              clean: '安全',
                              infected: '已感染',
                              error: '扫描异常',
                            }[attachment.virus_scan]}
                            color={
                              attachment.virus_scan === 'clean'
                                ? 'success'
                                : attachment.virus_scan === 'pending'
                                  ? 'warning'
                                  : 'error'
                            }
                            variant="outlined"
                          />
                        </TableCell>
                        <TableCell>{formatDate(attachment.updated_at)}</TableCell>
                      </TableRow>
                    ))}
                    {(attachments?.items.length ?? 0) === 0 && (
                      <EmptyRow colSpan={6} message="暂无附件扫描记录。" />
                    )}
                  </TableBody>
                </ResizableMuiTable>
              )}

              {tab === 'events' && (
                <ResizableMuiTable tableId="agent-control.events" columns={eventColumns} size="small" aria-label="领域事件列表">
                  <TableHead>
                    <TableRow>
                      <TableCell>时间</TableCell>
                      <TableCell>事件类型</TableCell>
                      <TableCell>事件主题</TableCell>
                      <TableCell>操作者</TableCell>
                      <TableCell>版本</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(events?.items ?? []).map((event) => (
                      <TableRow key={event.id} hover>
                        <TableCell>{formatDate(event.time)}</TableCell>
                        <TableCell>
                          <Tooltip title={`事件类型代码：${event.type}`}>
                            <Chip size="small" label={eventTypeLabel(event.type)} variant="outlined" />
                          </Tooltip>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={event.subject}>{event.subject}</TruncatedText>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={`操作者代码：${event.actor_type}${event.actor_id ? `:${event.actor_id}` : ''}`}>
                            {actorTypeLabel(event.actor_type)}{event.actor_id ? `：${event.actor_id}` : ''}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>v{event.resource_version}</TableCell>
                      </TableRow>
                    ))}
                    {(events?.items.length ?? 0) === 0 && <EmptyRow colSpan={5} message="暂无领域事件。" />}
                  </TableBody>
                </ResizableMuiTable>
              )}

              {tab === 'outbox' && (
                <ResizableMuiTable tableId="agent-control.outbox" columns={outboxColumns} size="small" aria-label="事件投递列表">
                  <TableHead>
                    <TableRow>
                      <TableCell>事件</TableCell>
                      <TableCell>目标</TableCell>
                      <TableCell>状态</TableCell>
                      <TableCell>尝试</TableCell>
                      <TableCell>下次重试</TableCell>
                      <TableCell>截止时间</TableCell>
                      <TableCell>过期时间</TableCell>
                      <TableCell>错误</TableCell>
                      <TableCell align="right">操作</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(outbox?.items ?? []).map((delivery) => (
                      <TableRow key={delivery.id} hover>
                        <TableCell>
                          <TruncatedText title={delivery.event_id}>{delivery.event_id}</TruncatedText>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={`投递类型：${delivery.destination_type}`}>
                            {delivery.destination_label}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <Tooltip title={`状态代码：${delivery.status}`}>
                            <Chip size="small" label={statusLabel(delivery.status)} color={statusColor(delivery.status)} variant="outlined" />
                          </Tooltip>
                        </TableCell>
                        <TableCell>{delivery.attempts}</TableCell>
                        <TableCell>{formatDate(delivery.next_attempt_at)}</TableCell>
                        <TableCell>{formatDate(delivery.expires_at)}</TableCell>
                        <TableCell>{formatDate(delivery.expired_at)}</TableCell>
                        <TableCell sx={{ maxWidth: 320 }}>
                          <TruncatedText title={delivery.last_error || '—'}>
                            {delivery.last_error || '—'}
                          </TruncatedText>
                        </TableCell>
                        <TableCell align="right">
                          {(delivery.status === 'failed' || delivery.status === 'dead') && (
                            <Tooltip title="重新投递">
                              <IconButton
                                size="small"
                                onClick={() => confirmReplayDelivery(delivery)}
                                aria-label={`重新投递 ${delivery.id}`}
                              >
                                <ReplayIcon fontSize="small" />
                              </IconButton>
                            </Tooltip>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                    {(outbox?.items.length ?? 0) === 0 && <EmptyRow colSpan={9} message="暂无事件投递记录。" />}
                  </TableBody>
                </ResizableMuiTable>
              )}

              {tab === 'policy' && (
                <ResizableMuiTable tableId="agent-control.policy-audit" columns={policyAuditColumns} size="small" aria-label="智能体策略决策审计">
                  <TableHead>
                    <TableRow>
                      <TableCell>时间</TableCell>
                      <TableCell>操作者 / 凭据</TableCell>
                      <TableCell>权限范围 / 动作</TableCell>
                      <TableCell>资源</TableCell>
                      <TableCell>来源</TableCell>
                      <TableCell>决策</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(decisions?.items ?? []).map((decision) => (
                      <TableRow key={decision.id} hover>
                        <TableCell>{formatDate(decision.created_at)}</TableCell>
                        <TableCell>
                          <InlineDetails
                            primary={`操作者：${decision.actor_id}`}
                            secondary={`凭据：${decision.credential_id || '—'}`}
                            title={`操作者 ID：${decision.actor_id} · 凭据 ID：${decision.credential_id || '—'}`}
                            primaryFontWeight={400}
                          />
                        </TableCell>
                        <TableCell>
                          <InlineDetails
                            primary={`权限：${scopeLabel(decision.scope)}`}
                            secondary={`操作：${actionLabel(decision.action)}`}
                            title={`权限代码：${decision.scope} · 操作代码：${decision.action}`}
                            primaryFontWeight={400}
                          />
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={`资源代码：${decision.resource_type}:${decision.resource_id || '*'}`}>
                            {resourceTypeLabel(decision.resource_type)}：{resourceIDLabel(decision.resource_id)}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={`来源协议：${decision.source_protocol || '—'}`}>
                            {decision.source_protocol ? sourceProtocolLabel(decision.source_protocol) : '—'}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <Stack
                            direction="row"
                            spacing={0.75}
                            sx={{ alignItems: 'center', minWidth: 0, flexWrap: 'nowrap' }}
                          >
                            <Tooltip title={`策略原因代码：${decision.reason_code || '—'}`}>
                              <Chip
                                size="small"
                                label={decision.allowed ? '允许' : reasonCodeLabel(decision.reason_code)}
                                color={decision.allowed ? 'success' : 'error'}
                                variant="outlined"
                              />
                            </Tooltip>
                            <TruncatedText
                              title={`策略原因代码：${decision.reason_code || '—'}`}
                              color="text.secondary"
                            >
                              代码：{decision.reason_code || '—'}
                            </TruncatedText>
                          </Stack>
                        </TableCell>
                      </TableRow>
                    ))}
                    {(decisions?.items.length ?? 0) === 0 && (
                      <EmptyRow colSpan={6} message="暂无智能体策略决策记录。" />
                    )}
                  </TableBody>
                </ResizableMuiTable>
              )}
              {tab === 'principals' && principals && (
                <TablePagination
                  component="div"
                  count={principals.total}
                  page={principals.page - 1}
                  rowsPerPage={principals.page_size}
                  rowsPerPageOptions={[25]}
                  onPageChange={(_, nextPage) => setPrincipalPage(nextPage + 1)}
                  labelRowsPerPage="每页"
                />
              )}
              {tab === 'leases' && leases && (
                <TablePagination
                  component="div"
                  count={leases.total}
                  page={leases.page - 1}
                  rowsPerPage={leases.page_size}
                  rowsPerPageOptions={[25]}
                  onPageChange={(_, nextPage) => setLeasePage(nextPage + 1)}
                  labelRowsPerPage="每页"
                />
              )}
              {tab === 'attachments' && attachments && (
                <TablePagination
                  component="div"
                  count={attachments.total}
                  page={attachments.page - 1}
                  rowsPerPage={attachments.page_size}
                  rowsPerPageOptions={[25]}
                  onPageChange={(_, nextPage) => setAttachmentPage(nextPage + 1)}
                  labelRowsPerPage="每页"
                />
              )}
              {tab === 'outbox' && outbox && (
                <TablePagination
                  component="div"
                  count={outbox.total}
                  page={outbox.page - 1}
                  rowsPerPage={outbox.page_size}
                  rowsPerPageOptions={[25]}
                  onPageChange={(_, nextPage) => setOutboxPage(nextPage + 1)}
                  labelRowsPerPage="每页"
                />
              )}
              {tab === 'events' && events && (
                <Stack direction="row" spacing={1} sx={{ justifyContent: 'flex-end', p: 2 }}>
                  <Button
                    disabled={eventCursorHistory.length === 0 || activeTabLoading}
                    onClick={() => {
                      const history = [...eventCursorHistory]
                      setEventCursor(history.pop() ?? '')
                      setEventCursorHistory(history)
                    }}
                  >
                    上一页
                  </Button>
                  <Button
                    disabled={!events.has_more || !events.next_cursor || activeTabLoading}
                    onClick={() => {
                      setEventCursorHistory((history) => [...history, eventCursor])
                      setEventCursor(events.next_cursor)
                    }}
                  >
                    下一页
                  </Button>
                </Stack>
              )}
              {tab === 'policy' && decisions && (
                <Stack direction="row" spacing={1} sx={{ justifyContent: 'flex-end', p: 2 }}>
                  <Button
                    disabled={decisionCursorHistory.length === 0 || activeTabLoading}
                    onClick={() => {
                      const history = [...decisionCursorHistory]
                      setDecisionCursor(history.pop() ?? '')
                      setDecisionCursorHistory(history)
                    }}
                  >
                    上一页
                  </Button>
                  <Button
                    disabled={!decisions.has_more || !decisions.next_cursor || activeTabLoading}
                    onClick={() => {
                      setDecisionCursorHistory((history) => [...history, decisionCursor])
                      setDecisionCursor(decisions.next_cursor)
                    }}
                  >
                    下一页
                  </Button>
                </Stack>
              )}
            </TableContainer>
          )}
        </Paper>
      </Box>
      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>新建 AI 智能体服务主体</DialogTitle>
        <DialogContent>
          <Stack spacing={2.5} sx={{ pt: 1 }}>
            <TextField
              autoFocus
              required
              label="名称"
              value={createForm.name}
              onChange={(event) => setCreateForm({ ...createForm, name: event.target.value })}
              helperText="使用能反映用途的名称，例如：incident-triage-agent"
            />
            <TextField
              label="说明"
              multiline
              minRows={2}
              value={createForm.description}
              onChange={(event) => setCreateForm({ ...createForm, description: event.target.value })}
            />
            <FormControl fullWidth required>
              <InputLabel id="agent-scopes-label">权限范围（Scope）</InputLabel>
              <Select
                labelId="agent-scopes-label"
                multiple
                label="权限范围（Scope）"
                value={createForm.scopes}
                onChange={(event) => setCreateForm({
                  ...createForm,
                  scopes: event.target.value as CreatePrincipalForm['scopes'],
                })}
                renderValue={(selected) => selected.join(', ')}
              >
                {AVAILABLE_SCOPES.map((scope) => (
                  <MenuItem key={scope} value={scope}>
                    {scopeLabel(scope)}（{scope}）
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
              <TextField
                fullWidth
                label="每分钟请求上限"
                type="number"
                value={createForm.rate_limit}
                onChange={(event) => setCreateForm({ ...createForm, rate_limit: Number(event.target.value) })}
                slotProps={{ htmlInput: { min: 1, max: 10000 } }}
              />
              <TextField
                fullWidth
                label="并发上限"
                type="number"
                value={createForm.concurrency_limit}
                onChange={(event) => setCreateForm({ ...createForm, concurrency_limit: Number(event.target.value) })}
                slotProps={{ htmlInput: { min: 1, max: 100 } }}
              />
            </Stack>
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateOpen(false)}>取消</Button>
          <Button
            variant="contained"
            onClick={() => void createPrincipal()}
            disabled={submitting || credentialProtectionActive}
          >
            {submitting ? '创建中…' : '创建并签发凭据'}
          </Button>
        </DialogActions>
      </Dialog>
      <Dialog
        open={Boolean(policyPrincipal)}
        onClose={() => setPolicyPrincipal(null)}
        maxWidth="md"
        fullWidth
      >
        <DialogTitle>{policyPrincipal?.name} · 权限范围策略</DialogTitle>
        <DialogContent>
          {policyGrantState && !policyGrantState.usable && (
            <Alert severity="warning" sx={{ mb: 2 }}>
              {policyGrantState.label}，当前策略仅供查看，不能新增或停用。
            </Alert>
          )}
          <Alert severity="info" sx={{ mb: 2 }}>
            拒绝策略始终优先；分配、状态流转和升级等风险动作必须存在匹配的显式允许策略。
            智能体触发外部 Webhook 还必须具备“订阅事件”权限，并显式允许“发送外部通知”操作；
            否则事件只在 ChronoDesk 内部投递。技术代码可在各字段提示中查看。
          </Alert>
          <Grid container spacing={2}>
            <Grid
              size={{
                xs: 12,
                sm: 3
              }}>
              <FormControl fullWidth>
                <InputLabel id="policy-effect-label">效果</InputLabel>
                <Select
                  labelId="policy-effect-label"
                  label="效果"
                  value={policyForm.effect}
                  onChange={(event) => setPolicyForm({ ...policyForm, effect: event.target.value as 'allow' | 'deny' })}
                >
                  <MenuItem value="deny">拒绝</MenuItem>
                  <MenuItem value="allow">允许</MenuItem>
                </Select>
              </FormControl>
            </Grid>
            <Grid
              size={{
                xs: 12,
                sm: 4
              }}>
              <FormControl fullWidth>
                <InputLabel id="policy-scope-label">权限范围（Scope）</InputLabel>
                <Select
                  labelId="policy-scope-label"
                  label="权限范围（Scope）"
                  value={policyForm.scope}
                  onChange={(event) => setPolicyForm({ ...policyForm, scope: event.target.value })}
                >
                  {AVAILABLE_SCOPES.map((scope) => (
                    <MenuItem key={scope} value={scope}>
                      {scopeLabel(scope)}（{scope}）
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            </Grid>
            <Grid
              size={{
                xs: 12,
                sm: 5
              }}>
              <TextField
                fullWidth
                label="操作（可选）"
                placeholder="ticket.transition"
                value={policyForm.action}
                onChange={(event) => setPolicyForm({ ...policyForm, action: event.target.value })}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                sm: 4
              }}>
              <TextField
                fullWidth
                label="资源类型"
                value={policyForm.resource_type}
                onChange={(event) => setPolicyForm({ ...policyForm, resource_type: event.target.value })}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                sm: 8
              }}>
              <TextField
                fullWidth
                label="资源 ID（留空表示全部）"
                value={policyForm.resource_id}
                onChange={(event) => setPolicyForm({ ...policyForm, resource_id: event.target.value })}
              />
            </Grid>
          </Grid>
          <Button
            variant="contained"
            startIcon={<PolicyIcon />}
            onClick={() => {
              if (policyForm.effect === 'allow') {
                confirmCreateAllowPolicy()
              } else {
                void createPolicy()
              }
            }}
            disabled={submitting || !policyGrantState?.usable}
            sx={{ mt: 2 }}
          >
            新增策略
          </Button>
          <Divider sx={{ my: 2 }} />
          {errorByKey.policies && (
            <Alert
              severity="error"
              action={
                <Button onClick={() => {
                  if (projectKey && policyPrincipal) {
                    void loadPolicies(projectKey, policyPrincipal.id, policyPage)
                  }
                }}>
                  重试
                </Button>
              }
              sx={{ mb: 2 }}
            >
              {errorByKey.policies}
            </Alert>
          )}
          {loadingByKey.policies && !policies && (
            <Stack aria-busy="true" sx={{ alignItems: 'center', py: 3 }}>
              <CircularProgress size={24} />
            </Stack>
          )}
          <Stack spacing={1}>
            {(policies?.items ?? []).map((policy) => (
              <Paper key={policy.id} variant="outlined" sx={{ p: 1.5 }}>
                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{
                  justifyContent: "space-between"
                }}>
                  <Stack
                    direction="row"
                    spacing={1}
                    sx={{
                      alignItems: "center",
                      flexWrap: "wrap"
                    }}>
                    <Chip
                      size="small"
                      label={policy.effect === 'allow' ? '允许' : '拒绝'}
                      color={policy.effect === 'allow' ? 'success' : 'error'}
                    />
                    <Tooltip title={`权限代码：${policy.scope}`}>
                      <Typography variant="body2" sx={{ fontWeight: 600 }}>
                        {scopeLabel(policy.scope)}
                      </Typography>
                    </Tooltip>
                    <Tooltip
                      title={`操作与资源代码：${policy.action || '*'} · ${policy.resource_type || '*'}:${policy.resource_id || '*'}`}
                    >
                      <Typography variant="body2" sx={{ color: 'text.secondary' }}>
                        {actionLabel(policy.action || '')} · {resourceTypeLabel(policy.resource_type || '')}：
                        {resourceIDLabel(policy.resource_id || '')}
                      </Typography>
                    </Tooltip>
                    {!policy.is_active && <Chip size="small" label="已停用" />}
                  </Stack>
                  {policy.is_active && (
                    <Tooltip title={
                      policyGrantState?.usable ? '' : `${policyGrantState?.label ?? '项目授权不可用'}，不能停用策略`
                    }>
                      <span>
                        <Button
                          size="small"
                          color="warning"
                          onClick={() => confirmDisablePolicy(policy)}
                          disabled={!policyGrantState?.usable}
                        >
                          停用
                        </Button>
                      </span>
                    </Tooltip>
                  )}
                </Stack>
              </Paper>
            ))}
            {(policies?.items.length ?? 0) === 0 && !loadingByKey.policies && (
              <Typography
                align="center"
                sx={{
                  color: "text.secondary",
                  py: 2
                }}>
                尚未配置细粒度策略，普通权限范围默认允许，风险动作默认拒绝。
              </Typography>
            )}
          </Stack>
          {policies && (
            <TablePagination
              component="div"
              count={policies.total}
              page={policies.page - 1}
              rowsPerPage={policies.page_size}
              rowsPerPageOptions={[25]}
              onPageChange={(_, nextPage) => setPolicyPage(nextPage + 1)}
              labelRowsPerPage="每页"
            />
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPolicyPrincipal(null)}>关闭</Button>
        </DialogActions>
      </Dialog>
      <Dialog
        open={Boolean(confirmation)}
        onClose={(_event, reason) => {
          if (reason === 'escapeKeyDown' && confirming) return
          if (!confirming) setConfirmation(null)
        }}
        maxWidth="sm"
        fullWidth
        aria-labelledby="agent-operation-confirmation-title"
      >
        <DialogTitle id="agent-operation-confirmation-title">{confirmation?.title}</DialogTitle>
        <DialogContent>
          <Alert severity={confirmation?.color === 'error' ? 'error' : 'warning'}>
            {confirmation?.description}
          </Alert>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmation(null)} disabled={confirming}>
            取消
          </Button>
          <Button
            variant="contained"
            color={confirmation?.color ?? 'warning'}
            onClick={() => void runConfirmedAction()}
            disabled={confirming}
          >
            {confirming ? '执行中…' : confirmation?.confirmLabel}
          </Button>
        </DialogActions>
      </Dialog>
      <Dialog
        open={Boolean(credential)}
        maxWidth="sm"
        fullWidth
        aria-labelledby="agent-credential-title"
      >
        <DialogTitle id="agent-credential-title">保存一次性凭据</DialogTitle>
        <DialogContent>
          <Alert severity="warning" sx={{ mb: 2 }}>
            客户端密钥关闭后不会再次显示。请立即保存到安全的密钥管理系统。
          </Alert>
          <Stack spacing={2}>
            <TextField
              label="客户端 ID"
              value={credential?.client_id ?? ''}
              slotProps={{ input: { readOnly: true } }}
            />
            <TextField
              label="客户端密钥"
              value={credential?.client_secret ?? ''}
              slotProps={{
                input: {
                  readOnly: true,
                  endAdornment: (
                    <IconButton onClick={() => void copySecret()} aria-label="复制客户端密钥">
                      <CopyIcon />
                    </IconButton>
                  ),
                },
              }}
            />
            {credentialProjectKey && (
              <Typography variant="body2" sx={{ color: 'text.secondary' }}>
                凭据签发项目：{credentialProjectKey}
              </Typography>
            )}
            {credential?.expires_at && (
              <Typography variant="body2" sx={{
                color: "text.secondary"
              }}>
                凭据到期时间：{formatDate(credential.expires_at)}
              </Typography>
            )}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button
            variant="contained"
            onClick={() => {
              endCredentialProtection()
            }}
          >
            我已安全保存
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

export default AgentControlCenter
