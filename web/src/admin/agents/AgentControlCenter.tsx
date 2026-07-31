import React, { useCallback, useEffect, useMemo, useState } from 'react'
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
import { resolveActiveProjectKey } from '../../lib/projectScope'
import {
  humanApiRoutes,
  type AdminAgentPolicy,
  type AdminOutboxDeliverySummary,
  type AdminOverview,
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
  { key: 'status', defaultWidth: 180, minWidth: 128, maxWidth: 280 },
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
  { key: 'error', defaultWidth: 360, minWidth: 200, maxWidth: 640 },
  { key: 'actions', defaultWidth: 96, minWidth: 80, maxWidth: 136, sticky: 'right' },
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
type AgentControlSnapshot = AdminOverview
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
  if (status === 'failed' || status === 'dead' || status === 'suspended') return 'error'
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

const destinationTypeLabels: Record<string, string> = {
  webhook: 'Webhook 回调',
  event_stream: '事件流',
  automation: '自动化规则',
  notification: '系统通知',
  sla: 'SLA 升级',
  sla_escalation: 'SLA 升级',
  attachment_cleanup: '附件清理',
  a2a_push: 'A2A 推送',
}

const notificationDestinationLabels: Record<string, string> = {
  ticket_assigned: '工单分配通知',
  ticket_status_changed: '工单状态变更通知',
  ticket_comment_added: '工单评论通知',
  sla_warning: 'SLA 预警通知',
  sla_breach: 'SLA 违约通知',
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
const destinationLabel = (destination: string) => {
  const [type, ...rest] = destination.split(':')
  const [detail, identifier] = rest

  if (type === 'event_stream' && detail === 'default') return '默认事件流'
  if (type === 'automation' && detail === 'rules') return '自动化规则引擎'
  if (type === 'webhook' && detail === 'configured') return '已配置 Webhook 回调'
  if (type === 'sla_escalation' && detail === 'breach') return 'SLA 违约升级'
  if (type === 'notification') {
    const label = notificationDestinationLabels[detail] ?? '系统通知'
    return identifier ? `${label}（接收者 #${identifier}）` : label
  }

  return destinationTypeLabels[type] ?? '其他投递目标'
}

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
type AgentControlTab = 'principals' | 'leases' | 'events' | 'outbox' | 'policy'

const surfaceTabs: Record<
  AgentControlSurface,
  readonly { id: AgentControlTab; label: string }[]
> = {
  agent: [
    { id: 'principals', label: '服务主体' },
    { id: 'leases', label: '实时租约' },
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
  const [snapshot, setSnapshot] = useState<AgentControlSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [tab, setTab] = useState<AgentControlTab>(surfaceTabs[surface][0].id)
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm, setCreateForm] = useState<CreatePrincipalForm>(initialCreateForm)
  const [submitting, setSubmitting] = useState(false)
  const [credential, setCredential] = useState<CredentialResult | null>(null)
  const [policyPrincipal, setPolicyPrincipal] = useState<ServicePrincipal | null>(null)
  const [policies, setPolicies] = useState<AgentPolicy[]>([])
  const [policyForm, setPolicyForm] = useState<PolicyForm>(initialPolicyForm)
  const [confirmation, setConfirmation] = useState<ConfirmationRequest | null>(null)
  const [confirming, setConfirming] = useState(false)

  const loadSnapshot = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const projectKey = await resolveActiveProjectKey()
      const result = await apiFetch<AgentControlSnapshot>(
        humanApiRoutes.getAgentControlOverviewV2({ projectKey }),
      )
      const normalized: AgentControlSnapshot = {
        ...result,
        global_read_only: Boolean(result.global_read_only),
        emergency_stop: Boolean(result.emergency_stop),
        principals: result.principals ?? [],
        leases: result.leases ?? [],
        events: result.events ?? [],
        outbox: result.outbox ?? [],
        attachments: result.attachments ?? [],
        policy_decisions: result.policy_decisions ?? [],
      }
      setSnapshot(normalized)
      return normalized
    } catch (requestError) {
      setError(localizedUnknownErrorMessage(requestError, '智能体控制面加载失败'))
      return null
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadSnapshot()
  }, [loadSnapshot])

  const metrics = useMemo(() => {
    const principals = snapshot?.principals ?? []
    const outbox = snapshot?.outbox ?? []
    return {
      active: principals.filter((item) => item.status === 'active').length,
      leases: snapshot?.leases.length ?? 0,
      failedDeliveries: outbox.filter((item) => item.status === 'failed' || item.status === 'dead').length,
      recentEvents: snapshot?.events.length ?? 0,
    }
  }, [snapshot])

  const createPrincipal = async () => {
    if (!createForm.name.trim() || createForm.scopes.length === 0) {
      notify('请填写名称并至少选择一个权限范围', { type: 'warning' })
      return
    }

    setSubmitting(true)
    try {
      const projectKey = await resolveActiveProjectKey()
      const result = await adminWrite<CredentialResult>(
        humanApiRoutes.createServicePrincipalV2({ projectKey }),
        {
        method: 'POST',
        body: JSON.stringify({
          ...createForm,
          name: createForm.name.trim(),
          description: createForm.description.trim(),
        }),
        },
      )
      setCredential(result)
      setCreateOpen(false)
      setCreateForm(initialCreateForm)
      notify('服务主体已创建，请立即保存一次性密钥', { type: 'success' })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '创建失败'), { type: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  const rotateCredential = async (principal: ServicePrincipal) => {
    setSubmitting(true)
    try {
      const projectKey = await resolveActiveProjectKey()
      const result = await adminWrite<CredentialResult>(
        humanApiRoutes.rotateServicePrincipalCredentialV2({
          projectKey,
          principalId: principal.id,
        }),
        { method: 'POST' },
        principal.resource_version,
      )
      setCredential(result)
      notify('凭据已轮换，旧凭据已撤销', { type: 'success' })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '凭据轮换失败'), { type: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  const togglePrincipal = async (principal: ServicePrincipal) => {
    const nextStatus: PrincipalStatus = principal.status === 'active' ? 'inactive' : 'active'
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(humanApiRoutes.setServicePrincipalStatusV2({
        projectKey,
        principalId: principal.id,
      }), {
        method: 'PUT',
        body: JSON.stringify({ status: nextStatus }),
      }, principal.resource_version)
      notify(nextStatus === 'active' ? '智能体已启用' : '智能体已停用', { type: 'success' })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '状态更新失败'), { type: 'error' })
    }
  }

  const togglePrincipalEmergency = async (principal: ServicePrincipal) => {
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(humanApiRoutes.setServicePrincipalStatusV2({
        projectKey,
        principalId: principal.id,
      }), {
        method: 'PUT',
        body: JSON.stringify({ emergency_disabled: !principal.emergency_disabled }),
      }, principal.resource_version)
      notify(principal.emergency_disabled ? '智能体熔断已解除' : '智能体已立即熔断', {
        type: principal.emergency_disabled ? 'success' : 'warning',
      })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '智能体熔断更新失败'), { type: 'error' })
    }
  }

  const openPolicies = async (principal: ServicePrincipal) => {
    setPolicyPrincipal(principal)
    setPolicyForm(initialPolicyForm)
    try {
      const projectKey = await resolveActiveProjectKey()
      const result = await apiFetch<AgentPolicy[]>(
        humanApiRoutes.listServicePrincipalPoliciesV2({
          projectKey,
          principalId: principal.id,
        }),
      )
      setPolicies(result ?? [])
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '策略加载失败'), { type: 'error' })
    }
  }

  const createPolicy = async () => {
    if (!policyPrincipal) return
    setSubmitting(true)
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(humanApiRoutes.createServicePrincipalPolicyV2({
        projectKey,
        principalId: policyPrincipal.id,
      }), {
        method: 'POST',
        body: JSON.stringify(policyForm),
      }, policyPrincipal.resource_version)
      const result = await apiFetch<AgentPolicy[]>(
        humanApiRoutes.listServicePrincipalPoliciesV2({
          projectKey,
          principalId: policyPrincipal.id,
        }),
      )
      setPolicies(result ?? [])
      setPolicyForm(initialPolicyForm)
      const refreshed = await loadSnapshot()
      const refreshedPrincipal = refreshed?.principals.find((item) => item.id === policyPrincipal.id)
      if (refreshedPrincipal) setPolicyPrincipal(refreshedPrincipal)
      notify('策略已创建', { type: 'success' })
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '策略创建失败'), { type: 'error' })
    } finally {
      setSubmitting(false)
    }
  }

  const disablePolicy = async (policy: AgentPolicy) => {
    if (!policyPrincipal) return
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(humanApiRoutes.disableServicePrincipalPolicyV2({
        projectKey,
        principalId: policyPrincipal.id,
        policyId: policy.id,
      }), {
        method: 'DELETE',
      }, policy.resource_version)
      setPolicies(policies.map((item) => (item.id === policy.id ? { ...item, is_active: false } : item)))
      notify('策略已停用', { type: 'success' })
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '策略停用失败'), { type: 'error' })
    }
  }

  const forceReleaseLease = async (lease: TicketLease) => {
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(
        humanApiRoutes.forceReleaseTicketLeaseV2({
          projectKey,
          leaseId: lease.id,
        }),
        { method: 'POST' },
        lease.resource_version,
      )
      notify('工单租约已强制释放', { type: 'success' })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '租约释放失败'), { type: 'error' })
    }
  }

  const replayDelivery = async (delivery: OutboxDelivery) => {
    try {
      const projectKey = await resolveActiveProjectKey()
      await adminWrite(
        humanApiRoutes.replayOutboxDeliveryV2({
          projectKey,
          deliveryId: delivery.id,
        }),
        { method: 'POST' },
        delivery.resource_version,
      )
      notify('事件投递已重新排队', { type: 'success' })
      await loadSnapshot()
    } catch (requestError) {
      notify(localizedUnknownErrorMessage(requestError, '事件投递回放失败'), { type: 'error' })
    }
  }

  const requestConfirmation = (request: ConfirmationRequest) => {
    setConfirmation(request)
  }

  const runConfirmedAction = async () => {
    if (!confirmation) return

    setConfirming(true)
    try {
      await confirmation.action()
      setConfirmation(null)
    } finally {
      setConfirming(false)
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
                      checked={snapshot?.emergency_stop ?? false}
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
                      checked={snapshot?.global_read_only ?? false}
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
                <IconButton onClick={() => void loadSnapshot()} disabled={loading} aria-label="刷新控制面">
                  <RefreshIcon />
                </IconButton>
              </span>
            </Tooltip>
            {surface === 'agent' && (
              <Button variant="contained" startIcon={<AddIcon />} onClick={() => setCreateOpen(true)}>
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

        {surface === 'agent' && snapshot?.global_read_only && (
          <Alert severity="warning" icon={<PausedIcon />} sx={{ mb: 3 }}>
            智能体全局只读模式已开启。MCP 与 A2A 查询仍可用，所有智能体写操作将被策略层拒绝。
          </Alert>
        )}
        {surface === 'agent' && snapshot?.emergency_stop && (
          <Alert severity="error" icon={<StopIcon />} sx={{ mb: 3 }}>
            智能体全局紧急停止已启用。所有智能体请求都会被拒绝，管理员仍可检查审计并接管租约。
          </Alert>
        )}
        {error && (
          <Alert severity="error" action={<Button onClick={() => void loadSnapshot()}>重试</Button>} sx={{ mb: 3 }}>
            {error}
          </Alert>
        )}

        <Grid container spacing={2} sx={{ mb: 3 }}>
          {surface === 'agent' ? (
            <>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="活跃智能体" value={metrics.active} helper="可签发令牌的服务主体" />
              </Grid>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="实时租约" value={metrics.leases} helper="正在处理的工单" />
              </Grid>
            </>
          ) : (
            <>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="近期事件" value={metrics.recentEvents} helper="最近一页领域事件" />
              </Grid>
              <Grid size={{ xs: 12, sm: 6 }}>
                <MetricCard label="投递失败" value={metrics.failedDeliveries} helper="需要关注的事件投递记录" />
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

          {loading && !snapshot ? (
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
                正在加载 AI 智能体控制面…
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
                    {(snapshot?.principals ?? []).map((principal) => (
                      <TableRow key={principal.id} hover>
                        <TableCell>
                          <InlineDetails
                            primary={principal.name}
                            secondary={principal.client_id}
                            title={`${principal.name} · ${principal.client_id}`}
                          />
                        </TableCell>
                        <TableCell>
                          <Stack direction="row" spacing={0.5}>
                            <Tooltip title={`状态代码：${principal.status}`}>
                              <Chip
                                size="small"
                                label={statusLabel(principal.status)}
                                color={statusColor(principal.status)}
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
                          <Tooltip title="权限范围策略">
                            <IconButton
                              size="small"
                              onClick={() => void openPolicies(principal)}
                              aria-label={`管理 ${principal.name} 的策略`}
                            >
                              <PolicyIcon fontSize="small" />
                            </IconButton>
                          </Tooltip>
                          <Tooltip title="轮换凭据">
                            <span>
                              <IconButton
                                size="small"
                                onClick={() => confirmRotateCredential(principal)}
                                disabled={submitting}
                                aria-label={`轮换 ${principal.name} 的凭据`}
                              >
                                <RotateIcon fontSize="small" />
                              </IconButton>
                            </span>
                          </Tooltip>
                          <Tooltip title={principal.emergency_disabled ? '解除单个智能体熔断' : '立即熔断智能体'}>
                            <IconButton
                              size="small"
                              color={principal.emergency_disabled ? 'success' : 'error'}
                              onClick={() => confirmTogglePrincipalEmergency(principal)}
                              aria-label={`${principal.emergency_disabled ? '解除' : '启用'} ${principal.name} 的熔断`}
                            >
                              <StopIcon fontSize="small" />
                            </IconButton>
                          </Tooltip>
                          <Button
                            size="small"
                            color={principal.status === 'active' ? 'warning' : 'primary'}
                            onClick={() => confirmTogglePrincipal(principal)}
                            disabled={principal.status === 'revoked'}
                          >
                            {principal.status === 'revoked' ? '已撤销' : principal.status === 'active' ? '停用' : '启用'}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                    {(snapshot?.principals.length ?? 0) === 0 && (
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
                    {(snapshot?.leases ?? []).map((lease) => (
                      <TableRow key={lease.id} hover>
                        <TableCell>
                          <TruncatedText title={lease.ticket_number || `工单 #${lease.ticket_id}`}>
                            {lease.ticket_number || `工单 #${lease.ticket_id}`}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={lease.principal_name}>{lease.principal_name}</TruncatedText>
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
                    {(snapshot?.leases.length ?? 0) === 0 && <EmptyRow colSpan={6} message="当前没有活跃工单租约。" />}
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
                    {(snapshot?.events ?? []).map((event) => (
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
                    {(snapshot?.events.length ?? 0) === 0 && <EmptyRow colSpan={5} message="暂无领域事件。" />}
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
                      <TableCell>错误</TableCell>
                      <TableCell align="right">操作</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(snapshot?.outbox ?? []).map((delivery) => (
                      <TableRow key={delivery.id} hover>
                        <TableCell>
                          <TruncatedText title={delivery.event_id}>{delivery.event_id}</TruncatedText>
                        </TableCell>
                        <TableCell>
                          <TruncatedText title={`投递目标代码：${delivery.destination}`}>
                            {destinationLabel(delivery.destination)}
                          </TruncatedText>
                        </TableCell>
                        <TableCell>
                          <Tooltip title={`状态代码：${delivery.status}`}>
                            <Chip size="small" label={statusLabel(delivery.status)} color={statusColor(delivery.status)} variant="outlined" />
                          </Tooltip>
                        </TableCell>
                        <TableCell>{delivery.attempts}</TableCell>
                        <TableCell>{formatDate(delivery.next_attempt_at)}</TableCell>
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
                    {(snapshot?.outbox.length ?? 0) === 0 && <EmptyRow colSpan={7} message="暂无事件投递记录。" />}
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
                    {(snapshot?.policy_decisions ?? []).map((decision) => (
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
                    {(snapshot?.policy_decisions.length ?? 0) === 0 && (
                      <EmptyRow colSpan={6} message="暂无智能体策略决策记录。" />
                    )}
                  </TableBody>
                </ResizableMuiTable>
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
          <Button variant="contained" onClick={() => void createPrincipal()} disabled={submitting}>
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
            disabled={submitting}
            sx={{ mt: 2 }}
          >
            新增策略
          </Button>
          <Divider sx={{ my: 2 }} />
          <Stack spacing={1}>
            {policies.map((policy) => (
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
                    <Button size="small" color="warning" onClick={() => confirmDisablePolicy(policy)}>
                      停用
                    </Button>
                  )}
                </Stack>
              </Paper>
            ))}
            {policies.length === 0 && (
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
          <Button variant="contained" onClick={() => setCredential(null)}>我已安全保存</Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

export default AgentControlCenter
