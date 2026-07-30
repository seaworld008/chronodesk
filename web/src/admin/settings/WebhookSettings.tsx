import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  FormControlLabel,
  Grid,
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  SelectChangeEvent,
  Stack,
  Switch,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import {
  Add as AddIcon,
  Delete as DeleteIcon,
  Edit as EditIcon,
  Science as TestIcon,
  Refresh as RefreshIcon,
} from '@mui/icons-material'
import { useNotify } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import { projectResourcePath } from '@/lib/projectScope'
import BackButton from '../common/BackButton'
import {
  InlineDetails,
  ResizableMuiTable,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'

const webhookColumns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 280, minWidth: 180, maxWidth: 480 },
  { key: 'provider', defaultWidth: 132, minWidth: 104, maxWidth: 220 },
  { key: 'status', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
  { key: 'events', defaultWidth: 360, minWidth: 200, maxWidth: 640 },
  { key: 'delivery', defaultWidth: 180, minWidth: 140, maxWidth: 280 },
  { key: 'last-success', defaultWidth: 188, minWidth: 150, maxWidth: 280 },
  { key: 'actions', defaultWidth: 152, minWidth: 136, maxWidth: 220, sticky: 'right' },
]

interface WebhookConfig {
  id: number
  name: string
  description: string
  provider: string
  webhook_url: string
  status: string
  enabled_events_list?: string[]
  filter_rules_obj?: {
    transition_statuses?: string[]
  }
  message_format?: string
  retry_count: number
  retry_interval: number
  timeout_seconds: number
  is_async: boolean
  rate_limit: number
  rate_limit_window: number
  secret?: string
  access_token?: string
  last_triggered_at?: string
  last_success_at?: string
  last_error_at?: string
  last_error?: string
  total_sent: number
  total_success: number
  total_failed: number
}

interface WebhookListResponse {
  items: WebhookConfig[]
  total: number
  page: number
  size: number
}

interface WebhookForm {
  name: string
  description: string
  provider: string
  webhook_url: string
  secret?: string
  access_token?: string
  enabled_events: string[]
  transition_statuses: string[]
  message_format: string
  retry_count: number
  retry_interval: number
  timeout_seconds: number
  is_async: boolean
  rate_limit: number
  rate_limit_window: number
  status: string
}

const providerOptions = [
  { value: 'wechat', label: '企业微信', hint: '需要配置机器人 Webhook URL，可选签名密钥。' },
  { value: 'dingtalk', label: '钉钉', hint: '机器人通常需要关键字或签名密钥。' },
  { value: 'lark', label: '飞书', hint: '支持自定义卡片消息，如需签名请填写签名密钥。' },
  { value: 'slack', label: 'Slack', hint: '使用 Slack 入站 Webhook URL。' },
  { value: 'teams', label: 'Microsoft Teams', hint: '使用 Teams 入站 Webhook。' },
  { value: 'custom', label: '自定义', hint: '自定义 HTTP 服务，支持自定义请求头。' },
]

const ticketTransitionedEvent = 'io.chronodesk.ticket.transitioned.v1'

const eventOptions = [
  'io.chronodesk.ticket.created.v1',
  'io.chronodesk.ticket.updated.v1',
  'io.chronodesk.ticket.assigned.v1',
  ticketTransitionedEvent,
  'io.chronodesk.ticket.escalated.v1',
  'io.chronodesk.ticket.comment.created.v1',
  'io.chronodesk.ticket.attachment.created.v1',
  'io.chronodesk.ticket.sla.breached.v1',
  'io.chronodesk.ticket.deleted.v1',
  'io.chronodesk.automation.notification.requested.v1',
  'io.chronodesk.system.alert.v1',
] as const

const eventLabels: Record<string, string> = {
  'io.chronodesk.ticket.created.v1': '工单创建',
  'io.chronodesk.ticket.updated.v1': '工单内容更新',
  'io.chronodesk.ticket.assigned.v1': '工单分配',
  [ticketTransitionedEvent]: '工单状态流转',
  'io.chronodesk.ticket.escalated.v1': '工单升级',
  'io.chronodesk.ticket.comment.created.v1': '工单评论新增',
  'io.chronodesk.ticket.attachment.created.v1': '工单附件新增',
  'io.chronodesk.ticket.sla.breached.v1': 'SLA 违约',
  'io.chronodesk.ticket.deleted.v1': '工单删除',
  'io.chronodesk.automation.notification.requested.v1': '自动化通知请求',
  'io.chronodesk.system.alert.v1': '系统警报',
}

const eventLabel = (event: string) => eventLabels[event] ?? '不受支持的事件'

const transitionStatusOptions = [
  { value: 'open', label: '待处理' },
  { value: 'in_progress', label: '处理中' },
  { value: 'pending', label: '挂起' },
  { value: 'resolved', label: '已解决' },
  { value: 'closed', label: '已关闭' },
  { value: 'cancelled', label: '已取消' },
]

const transitionStatusLabel = (status: string) =>
  transitionStatusOptions.find((item) => item.value === status)?.label ?? '未知状态'

const webhookEventSummary = (webhook: WebhookConfig) => {
  const statuses = webhook.filter_rules_obj?.transition_statuses ?? []
  return (webhook.enabled_events_list ?? [])
    .map((event) => {
      if (event !== ticketTransitionedEvent || statuses.length === 0) {
        return eventLabel(event)
      }
      return `${eventLabel(event)}（${statuses.map(transitionStatusLabel).join('、')}）`
    })
    .join('、')
}

const statusOptions = [
  { value: 'active', label: '启用' },
  { value: 'inactive', label: '停用' },
  { value: 'disabled', label: '禁用' },
  { value: 'error', label: '错误' },
]

const defaultForm: WebhookForm = {
  name: '',
  description: '',
  provider: 'wechat',
  webhook_url: '',
  secret: '',
  access_token: '',
  enabled_events: ['io.chronodesk.ticket.created.v1'],
  transition_statuses: [],
  message_format: 'markdown',
  retry_count: 3,
  retry_interval: 60,
  timeout_seconds: 30,
  is_async: true,
  rate_limit: 60,
  rate_limit_window: 60,
  status: 'inactive',
}

const webhookFormFieldIDs = {
  status: 'webhook-status',
  provider: 'webhook-provider',
  enabledEvents: 'webhook-enabled-events',
  transitionStatuses: 'webhook-transition-statuses',
} as const

const statusColor = {
  active: 'success',
  inactive: 'default',
  disabled: 'warning',
  error: 'error',
} as const

type FormErrors = Record<string, string>
type PendingWebhookAction = {
  kind: 'delete' | 'test'
  id: number
  name: string
}

const WebhookSettings: React.FC = () => {
  const notify = useNotify()
  const [loading, setLoading] = useState(false)
  const [items, setItems] = useState<WebhookConfig[]>([])
  const [formOpen, setFormOpen] = useState(false)
  const [currentId, setCurrentId] = useState<number | null>(null)
  const [form, setForm] = useState<WebhookForm>(defaultForm)
  const [errors, setErrors] = useState<FormErrors>({})
  const [testId, setTestId] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [pendingAction, setPendingAction] = useState<PendingWebhookAction | null>(null)

  const extractErrorMessage = useCallback((error: unknown, fallback: string) => {
    return localizedUnknownErrorMessage(error, fallback)
  }, [])

  const fetchWebhooks = useCallback(async () => {
    try {
      setLoading(true)
      const path = await projectResourcePath('webhooks?page=1&page_size=100')
      const data = await apiFetch<WebhookListResponse>(path)
      setItems(data.items ?? [])
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '加载 Webhook 列表失败'), { type: 'error' })
    } finally {
      setLoading(false)
    }
  }, [notify, extractErrorMessage])

  useEffect(() => {
    fetchWebhooks()
  }, [fetchWebhooks])

  const openCreate = () => {
    setCurrentId(null)
    setForm(defaultForm)
    setErrors({})
    setFormOpen(true)
  }

  const openEdit = (webhook: WebhookConfig) => {
    setCurrentId(webhook.id)
    setForm({
      name: webhook.name,
      description: webhook.description || '',
      provider: webhook.provider,
      webhook_url: webhook.webhook_url,
      secret: webhook.secret || '',
      access_token: webhook.access_token || '',
      enabled_events: webhook.enabled_events_list ?? [],
      transition_statuses: webhook.filter_rules_obj?.transition_statuses ?? [],
      message_format: webhook.message_format || 'markdown',
      retry_count: webhook.retry_count,
      retry_interval: webhook.retry_interval,
      timeout_seconds: webhook.timeout_seconds,
      is_async: webhook.is_async,
      rate_limit: webhook.rate_limit,
      rate_limit_window: webhook.rate_limit_window,
      status: webhook.status,
    })
    setErrors({})
    setFormOpen(true)
  }

  const closeForm = () => {
    setFormOpen(false)
    setCurrentId(null)
    setForm(defaultForm)
    setErrors({})
  }

  const handleFormChange = <K extends keyof WebhookForm>(key: K, value: WebhookForm[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }))
    setErrors((prev) => ({ ...prev, [key]: '' }))
  }

  const handleEventsChange = (event: SelectChangeEvent<string[]>) => {
    const value = typeof event.target.value === 'string' ? event.target.value.split(',') : event.target.value
    setForm((previous) => ({
      ...previous,
      enabled_events: value,
      transition_statuses: value.includes(ticketTransitionedEvent)
        ? previous.transition_statuses
        : [],
    }))
    setErrors((previous) => ({ ...previous, enabled_events: '' }))
  }

  const handleTransitionStatusesChange = (event: SelectChangeEvent<string[]>) => {
    const value = typeof event.target.value === 'string' ? event.target.value.split(',') : event.target.value
    handleFormChange('transition_statuses', value)
  }

  const validate = (): boolean => {
    const next: FormErrors = {}
    if (!form.name.trim()) {
      next.name = '请输入名称'
    }
    if (!form.webhook_url.trim()) {
      next.webhook_url = '请输入Webhook URL'
    } else if (!/^https:\/\//i.test(form.webhook_url)) {
      next.webhook_url = 'Webhook 地址必须使用公网 HTTPS'
    }
    if (form.enabled_events.length === 0) {
      next.enabled_events = '至少选择一个订阅事件'
    }
    if (form.retry_count < 0 || !Number.isFinite(form.retry_count)) {
      next.retry_count = '最大重试次数需为非负整数'
    }
    if (form.retry_interval <= 0) {
      next.retry_interval = '重试间隔需大于 0'
    }
    if (form.timeout_seconds <= 0) {
      next.timeout_seconds = '超时时间需大于 0'
    }
    if (form.rate_limit < 0) {
      next.rate_limit = '每分钟限制需为非负数'
    }
    if (form.rate_limit_window <= 0) {
      next.rate_limit_window = '限流窗口需大于 0'
    }
    setErrors(next)
    return Object.keys(next).length === 0
  }

  const buildPayload = () => {
    const payload: Record<string, unknown> = {
      name: form.name.trim(),
      description: form.description.trim(),
      provider: form.provider,
      webhook_url: form.webhook_url.trim(),
      enabled_events: form.enabled_events,
      filter_rules: {
        transition_statuses: form.enabled_events.includes(ticketTransitionedEvent)
          ? form.transition_statuses
          : [],
      },
      message_format: form.message_format.trim(),
      retry_count: Number(form.retry_count || 0),
      retry_interval: Number(form.retry_interval || 0),
      timeout_seconds: Number(form.timeout_seconds || 0),
      is_async: form.is_async,
      rate_limit: Number(form.rate_limit || 0),
      rate_limit_window: Number(form.rate_limit_window || 0),
      status: form.status,
    }

    if (form.secret && form.secret.trim() !== '') {
      payload.secret = form.secret.trim()
    }
    if (form.access_token && form.access_token.trim() !== '') {
      payload.access_token = form.access_token.trim()
    }

    return payload
  }

  const handleSave = async () => {
    if (!validate()) return

    setSaving(true)
    try {
      const payload = buildPayload()
      if (currentId) {
        const path = await projectResourcePath(`webhooks/${currentId}`)
        await apiFetch(path, {
          method: 'PUT',
          body: JSON.stringify(payload),
        })
        notify('Webhook 更新成功', { type: 'success' })
      } else {
        const path = await projectResourcePath('webhooks')
        await apiFetch(path, {
          method: 'POST',
          body: JSON.stringify(payload),
        })
        notify('Webhook 创建成功', { type: 'success' })
      }
      closeForm()
      fetchWebhooks()
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '保存失败'), { type: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const executeDelete = async (id: number) => {
    try {
      const path = await projectResourcePath(`webhooks/${id}`)
      await apiFetch(path, { method: 'DELETE' })
      notify('删除成功', { type: 'success' })
      fetchWebhooks()
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '删除失败'), { type: 'error' })
    }
  }

  const executeTest = async (id: number) => {
    setTestId(id)
    try {
      const path = await projectResourcePath(`webhooks/${id}/test`)
      await apiFetch(path, { method: 'POST' })
      notify('Webhook 测试成功', { type: 'success' })
    } catch (error: unknown) {
      notify(extractErrorMessage(error, 'Webhook 测试失败'), { type: 'error' })
    } finally {
      setTestId(null)
    }
  }

  const confirmPendingAction = async () => {
    const action = pendingAction
    if (!action) return
    setPendingAction(null)
    if (action.kind === 'delete') {
      await executeDelete(action.id)
      return
    }
    await executeTest(action.id)
  }

  const providerHint = useMemo(() => {
    const meta = providerOptions.find((item) => item.value === form.provider)
    return meta?.hint ?? ''
  }, [form.provider])

  return (
    <Box sx={{ p: 3 }}>
      <Stack
        direction="row"
        sx={{
          justifyContent: "space-between",
          alignItems: "center",
          mb: 3
        }}>
        <Stack direction="row" spacing={1.5} sx={{
          alignItems: "center"
        }}>
          <BackButton fallbackPath="/" />
          <Box>
            <Typography variant="h4" sx={{ fontWeight: 600 }}>
              Webhook 集成
            </Typography>
            <Typography variant="body2" sx={{
              color: "text.secondary"
            }}>
              管理企业微信、钉钉、飞书等即时通讯渠道的自动通知。
            </Typography>
          </Box>
        </Stack>
        <Stack direction="row" spacing={1} sx={{
          alignItems: "center"
        }}>
          <Button variant="outlined" startIcon={<RefreshIcon />} onClick={fetchWebhooks}>
            刷新
          </Button>
          <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
            新增 Webhook
          </Button>
        </Stack>
      </Stack>
      <TableContainer component={Paper}>
        <ResizableMuiTable
          tableId="settings.webhooks"
          columns={webhookColumns}
          size="small"
          aria-label="Webhook 配置列表"
        >
          <TableHead>
            <TableRow>
              <TableCell>名称</TableCell>
              <TableCell>提供商</TableCell>
              <TableCell>状态</TableCell>
              <TableCell>事件</TableCell>
              <TableCell>发送情况</TableCell>
              <TableCell>最近成功</TableCell>
              <TableCell align="right">操作</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {items.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} align="center">
                  {loading ? '加载中…' : '暂无 Webhook 配置'}
                </TableCell>
              </TableRow>
            )}
            {items.map((item) => (
              <TableRow key={item.id} hover>
                <TableCell>
                  <InlineDetails
                    primary={item.name}
                    secondary={item.description || '—'}
                    title={`${item.name} · ${item.description || '—'}`}
                  />
                </TableCell>
                <TableCell>
                  <Tooltip title={`提供商代码：${item.provider}`}>
                    <span>{providerOptions.find((opt) => opt.value === item.provider)?.label || '其他提供商'}</span>
                  </Tooltip>
                </TableCell>
                <TableCell>
                  <Tooltip title={`状态代码：${item.status}`}>
                    <Chip
                      size="small"
                      label={statusOptions.find((opt) => opt.value === item.status)?.label || '未知状态'}
                      color={statusColor[item.status as keyof typeof statusColor] ?? 'default'}
                    />
                  </Tooltip>
                </TableCell>
                <TableCell>
                  <TruncatedText
                    title={(item.enabled_events_list ?? []).join('、') || '未订阅事件'}
                  >
                    {webhookEventSummary(item) || '未订阅事件'}
                  </TruncatedText>
                </TableCell>
                <TableCell>
                  <TruncatedText
                    title={`总计 ${item.total_sent} · 成功 ${item.total_success} · 失败 ${item.total_failed}`}
                  >
                    总计 {item.total_sent} · 成功 {item.total_success} · 失败 {item.total_failed}
                  </TruncatedText>
                </TableCell>
                <TableCell>
                  <Typography variant="body2">
                    {item.last_success_at ? new Date(item.last_success_at).toLocaleString('zh-CN') : '—'}
                  </Typography>
                </TableCell>
                <TableCell align="right">
                  <Stack direction="row" spacing={1} sx={{
                    justifyContent: "flex-end"
                  }}>
                    <IconButton
                      size="small"
                      aria-label={`测试 Webhook：${item.name}`}
                      title="发送测试请求"
                      onClick={() => setPendingAction({
                        kind: 'test',
                        id: item.id,
                        name: item.name,
                      })}
                      disabled={testId === item.id}
                    >
                      {testId === item.id ? <CircularProgress size={16} /> : <TestIcon fontSize="small" />}
                    </IconButton>
                    <IconButton
                      size="small"
                      aria-label={`编辑 Webhook：${item.name}`}
                      title="编辑 Webhook"
                      onClick={() => openEdit(item)}
                    >
                      <EditIcon fontSize="small" />
                    </IconButton>
                    <IconButton
                      size="small"
                      aria-label={`删除 Webhook：${item.name}`}
                      title="删除 Webhook"
                      onClick={() => setPendingAction({
                        kind: 'delete',
                        id: item.id,
                        name: item.name,
                      })}
                    >
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </Stack>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </ResizableMuiTable>
      </TableContainer>
      <Dialog open={formOpen} onClose={closeForm} maxWidth="md" fullWidth>
        <DialogTitle>{currentId ? '编辑 Webhook' : '新增 Webhook'}</DialogTitle>
        <DialogContent dividers>
          <Grid container spacing={2}>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <TextField
                fullWidth
                label="名称"
                value={form.name}
                onChange={(e) => handleFormChange('name', e.target.value)}
                error={Boolean(errors.name)}
                helperText={errors.name || '用于区分不同 Webhook 规则'}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <FormControl fullWidth error={Boolean(errors.status)}>
                <InputLabel id={`${webhookFormFieldIDs.status}-label`}>状态</InputLabel>
                <Select
                  id={webhookFormFieldIDs.status}
                  labelId={`${webhookFormFieldIDs.status}-label`}
                  label="状态"
                  value={form.status}
                  onChange={(e) => handleFormChange('status', e.target.value)}
                >
                  {statusOptions.map((opt) => (
                    <MenuItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            </Grid>
            <Grid size={12}>
              <TextField
                fullWidth
                label="描述"
                value={form.description}
                placeholder="用于说明通知场景，便于团队协作"
                onChange={(e) => handleFormChange('description', e.target.value)}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <FormControl fullWidth>
                <InputLabel id={`${webhookFormFieldIDs.provider}-label`}>提供商</InputLabel>
                <Select
                  id={webhookFormFieldIDs.provider}
                  labelId={`${webhookFormFieldIDs.provider}-label`}
                  label="提供商"
                  value={form.provider}
                  onChange={(e) => handleFormChange('provider', e.target.value)}
                >
                  {providerOptions.map((opt) => (
                    <MenuItem key={opt.value} value={opt.value}>
                      {opt.label}
                    </MenuItem>
                  ))}
                </Select>
                {providerHint && (
                  <Typography variant="caption" sx={{
                    color: "text.secondary"
                  }}>
                    {providerHint}
                  </Typography>
                )}
              </FormControl>
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <TextField
                fullWidth
                label="Webhook 地址"
                value={form.webhook_url}
                onChange={(e) => handleFormChange('webhook_url', e.target.value)}
                error={Boolean(errors.webhook_url)}
                helperText={errors.webhook_url || '复制自各渠道机器人配置界面'}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <TextField
                fullWidth
                label="签名密钥（可选）"
                value={form.secret}
                onChange={(e) => handleFormChange('secret', e.target.value)}
                helperText="部分渠道使用加签时需要填写"
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 6
              }}>
              <TextField
                fullWidth
                label="访问令牌（可选）"
                value={form.access_token}
                onChange={(e) => handleFormChange('access_token', e.target.value)}
              />
            </Grid>
            <Grid size={12}>
              <FormControl fullWidth error={Boolean(errors.enabled_events)}>
                <InputLabel id={`${webhookFormFieldIDs.enabledEvents}-label`}>订阅事件</InputLabel>
                <Select
                  id={webhookFormFieldIDs.enabledEvents}
                  labelId={`${webhookFormFieldIDs.enabledEvents}-label`}
                  label="订阅事件"
                  multiple
                  value={form.enabled_events}
                  onChange={handleEventsChange}
                  renderValue={(selected) => selected.map(eventLabel).join('、')}
                >
                  {eventOptions.map((evt) => (
                    <MenuItem key={evt} value={evt}>
                      {eventLabel(evt)}（{evt}）
                    </MenuItem>
                  ))}
                </Select>
                <Typography variant="caption" color={errors.enabled_events ? 'error' : 'text.secondary'}>
                  {errors.enabled_events || '仅支持当前完整 CloudEvent 类型；事件标识会原样写入投递日志和请求体。'}
                </Typography>
              </FormControl>
            </Grid>
            {form.enabled_events.includes(ticketTransitionedEvent) && (
              <Grid size={12}>
                <FormControl fullWidth>
                  <InputLabel id={`${webhookFormFieldIDs.transitionStatuses}-label`}>
                    状态流转筛选
                  </InputLabel>
                  <Select
                    id={webhookFormFieldIDs.transitionStatuses}
                    labelId={`${webhookFormFieldIDs.transitionStatuses}-label`}
                    label="状态流转筛选"
                    multiple
                    value={form.transition_statuses}
                    onChange={handleTransitionStatusesChange}
                    renderValue={(selected) =>
                      selected.length > 0
                        ? selected.map(transitionStatusLabel).join('、')
                        : '全部状态'
                    }
                  >
                    {transitionStatusOptions.map((status) => (
                      <MenuItem key={status.value} value={status.value}>
                        {status.label}（{status.value}）
                      </MenuItem>
                    ))}
                  </Select>
                  <Typography variant="caption" color="text.secondary">
                    留空表示订阅全部状态流转。若只关心已解决或已关闭，请在此明确选择，事件类型仍为
                    {' '}
                    {ticketTransitionedEvent}
                    。
                  </Typography>
                </FormControl>
              </Grid>
            )}
            <Grid
              size={{
                xs: 12,
                md: 4
              }}>
              <TextField
                fullWidth
                label="消息格式"
                value={form.message_format}
                onChange={(e) => handleFormChange('message_format', e.target.value)}
                helperText="支持 Markdown、纯文本或卡片格式"
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <FormControlLabel
                sx={{ height: '100%', m: 0 }}
                control={(
                  <Switch
                    checked={form.is_async}
                    onChange={(e) => handleFormChange('is_async', e.target.checked)}
                  />
                )}
                label="异步发送"
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <TextField
                fullWidth
                type="number"
                label="最大重试"
                value={form.retry_count}
                onChange={(e) => handleFormChange('retry_count', Number(e.target.value))}
                error={Boolean(errors.retry_count)}
                helperText={errors.retry_count || undefined}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <TextField
                fullWidth
                type="number"
                label="重试间隔(秒)"
                value={form.retry_interval}
                onChange={(e) => handleFormChange('retry_interval', Number(e.target.value))}
                error={Boolean(errors.retry_interval)}
                helperText={errors.retry_interval || undefined}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <TextField
                fullWidth
                type="number"
                label="超时时间(秒)"
                value={form.timeout_seconds}
                onChange={(e) => handleFormChange('timeout_seconds', Number(e.target.value))}
                error={Boolean(errors.timeout_seconds)}
                helperText={errors.timeout_seconds || undefined}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <TextField
                fullWidth
                type="number"
                label="每分钟限制"
                value={form.rate_limit}
                onChange={(e) => handleFormChange('rate_limit', Number(e.target.value))}
                error={Boolean(errors.rate_limit)}
                helperText={errors.rate_limit || undefined}
              />
            </Grid>
            <Grid
              size={{
                xs: 12,
                md: 2
              }}>
              <TextField
                fullWidth
                type="number"
                label="限流窗口(秒)"
                value={form.rate_limit_window}
                onChange={(e) => handleFormChange('rate_limit_window', Number(e.target.value))}
                error={Boolean(errors.rate_limit_window)}
                helperText={errors.rate_limit_window || undefined}
              />
            </Grid>
          </Grid>
        </DialogContent>
        <DialogActions>
          <Button onClick={closeForm}>取消</Button>
          <Button onClick={handleSave} variant="contained" disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </Button>
        </DialogActions>
      </Dialog>
      <Dialog
        open={Boolean(pendingAction)}
        onClose={() => setPendingAction(null)}
        maxWidth="xs"
        fullWidth
      >
        <DialogTitle>
          {pendingAction?.kind === 'delete' ? '删除 Webhook' : '测试 Webhook'}
        </DialogTitle>
        <DialogContent>
          {pendingAction?.kind === 'delete'
            ? `确定删除“${pendingAction.name}”吗？删除后将停止对应事件投递。`
            : `测试将立即向“${pendingAction?.name || ''}”配置的地址发送一条真实请求，确定继续吗？`}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPendingAction(null)}>取消</Button>
          <Button
            color={pendingAction?.kind === 'delete' ? 'error' : 'primary'}
            variant="contained"
            onClick={() => void confirmPendingAction()}
          >
            {pendingAction?.kind === 'delete' ? '确认删除' : '确认发送测试'}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}

export default WebhookSettings
