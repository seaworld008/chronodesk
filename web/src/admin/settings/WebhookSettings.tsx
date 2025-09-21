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
  Grid,
  IconButton,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  SelectChangeEvent,
  Stack,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
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
import { apiFetch } from '@/lib/apiClient'
import BackButton from '../common/BackButton'

interface WebhookConfig {
  id: number
  name: string
  description: string
  provider: string
  webhook_url: string
  status: string
  enabled_events_list?: string[]
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
  { value: 'wechat', label: '企业微信', hint: '需要配置机器人 Webhook URL，可选加签 Secret。' },
  { value: 'dingtalk', label: '钉钉', hint: '机器人通常需要关键字或加签 Secret。' },
  { value: 'lark', label: '飞书', hint: '支持自定义卡片消息，如需签名请填写 Secret。' },
  { value: 'slack', label: 'Slack', hint: '使用 Slack Incoming Webhook URL。' },
  { value: 'teams', label: 'Microsoft Teams', hint: '使用 Teams Incoming Webhook。' },
  { value: 'custom', label: '自定义', hint: '自定义 HTTP 服务，支持自定义 Header。' },
]

const eventOptions = [
  'ticket.created',
  'ticket.assigned',
  'ticket.updated',
  'ticket.resolved',
  'ticket.closed',
  'ticket.comment',
  'ticket.escalated',
  'user.registered',
  'system.alert',
]

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
  enabled_events: ['ticket.created'],
  message_format: 'markdown',
  retry_count: 3,
  retry_interval: 60,
  timeout_seconds: 30,
  is_async: true,
  rate_limit: 60,
  rate_limit_window: 60,
  status: 'active',
}

const statusColor = {
  active: 'success',
  inactive: 'default',
  disabled: 'warning',
  error: 'error',
} as const

type FormErrors = Record<string, string>

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

  const extractErrorMessage = useCallback((error: unknown, fallback: string) => {
    if (error instanceof Error) {
      return error.message
    }
    return fallback
  }, [])

  const fetchWebhooks = useCallback(async () => {
    try {
      setLoading(true)
      const data = await apiFetch<WebhookListResponse>('/webhooks?page=1&page_size=100')
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
    handleFormChange('enabled_events', value)
  }

  const validate = (): boolean => {
    const next: FormErrors = {}
    if (!form.name.trim()) {
      next.name = '请输入名称'
    }
    if (!form.webhook_url.trim()) {
      next.webhook_url = '请输入Webhook URL'
    } else if (!/^https?:\/\//i.test(form.webhook_url)) {
      next.webhook_url = 'URL 需以 http/https 开头'
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
        await apiFetch(`/webhooks/${currentId}`, {
          method: 'PUT',
          body: JSON.stringify(payload),
        })
        notify('Webhook 更新成功', { type: 'success' })
      } else {
        await apiFetch('/webhooks', {
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

  const handleDelete = async (id: number) => {
    if (!window.confirm('确定要删除该 Webhook 吗？')) return
    try {
      await apiFetch(`/webhooks/${id}`, { method: 'DELETE' })
      notify('删除成功', { type: 'success' })
      fetchWebhooks()
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '删除失败'), { type: 'error' })
    }
  }

  const handleTest = async (id: number) => {
    setTestId(id)
    try {
      await apiFetch(`/webhooks/${id}/test`, { method: 'POST' })
      notify('Webhook 测试成功', { type: 'success' })
    } catch (error: unknown) {
      notify(extractErrorMessage(error, 'Webhook 测试失败'), { type: 'error' })
    } finally {
      setTestId(null)
    }
  }

  const providerHint = useMemo(() => {
    const meta = providerOptions.find((item) => item.value === form.provider)
    return meta?.hint ?? ''
  }, [form.provider])

  return (
    <Box sx={{ p: 3 }}>
      <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 3 }}>
        <Stack direction="row" spacing={1.5} alignItems="center">
          <BackButton fallbackPath="/system-settings" />
          <Box>
            <Typography variant="h4" sx={{ fontWeight: 600 }}>
              Webhook 集成
            </Typography>
            <Typography color="text.secondary" variant="body2">
              管理企业微信、钉钉、飞书等即时通讯渠道的自动通知。
            </Typography>
          </Box>
        </Stack>
        <Stack direction="row" spacing={1} alignItems="center">
          <Button variant="outlined" startIcon={<RefreshIcon />} onClick={fetchWebhooks}>
            刷新
          </Button>
          <Button variant="contained" startIcon={<AddIcon />} onClick={openCreate}>
            新增 Webhook
          </Button>
        </Stack>
      </Stack>

      <TableContainer component={Paper}>
        <Table size="small">
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
                  <Typography fontWeight={600}>{item.name}</Typography>
                  <Typography variant="body2" color="text.secondary">
                    {item.description || '—'}
                  </Typography>
                </TableCell>
                <TableCell>{providerOptions.find((opt) => opt.value === item.provider)?.label || item.provider}</TableCell>
                <TableCell>
                  <Chip
                    size="small"
                    label={statusOptions.find((opt) => opt.value === item.status)?.label || item.status}
                    color={statusColor[item.status as keyof typeof statusColor] ?? 'default'}
                  />
                </TableCell>
                <TableCell>
                  {(item.enabled_events_list ?? []).slice(0, 3).map((evt) => (
                    <Chip key={`${item.id}-${evt}`} label={evt} size="small" sx={{ mr: 0.5, mb: 0.5 }} />
                  ))}
                  {(item.enabled_events_list ?? []).length > 3 ? '…' : null}
                </TableCell>
                <TableCell>
                  <Typography variant="body2">总计 {item.total_sent}</Typography>
                  <Typography variant="caption" color="success.main">成功 {item.total_success}</Typography>
                  <Typography variant="caption" color="error.main" sx={{ ml: 1 }}>失败 {item.total_failed}</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="body2">
                    {item.last_success_at ? new Date(item.last_success_at).toLocaleString() : '—'}
                  </Typography>
                </TableCell>
                <TableCell align="right">
                  <Stack direction="row" spacing={1} justifyContent="flex-end">
                    <IconButton size="small" onClick={() => handleTest(item.id)} disabled={testId === item.id}>
                      {testId === item.id ? <CircularProgress size={16} /> : <TestIcon fontSize="small" />}
                    </IconButton>
                    <IconButton size="small" onClick={() => openEdit(item)}>
                      <EditIcon fontSize="small" />
                    </IconButton>
                    <IconButton size="small" onClick={() => handleDelete(item.id)}>
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </Stack>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableContainer>

      <Dialog open={formOpen} onClose={closeForm} maxWidth="md" fullWidth>
        <DialogTitle>{currentId ? '编辑 Webhook' : '新增 Webhook'}</DialogTitle>
        <DialogContent dividers>
          <Grid container spacing={2}>
            <Grid item xs={12} md={6}>
              <TextField
                fullWidth
                label="名称"
                value={form.name}
                onChange={(e) => handleFormChange('name', e.target.value)}
                error={Boolean(errors.name)}
                helperText={errors.name || '用于区分不同 Webhook 规则'}
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <FormControl fullWidth error={Boolean(errors.status)}>
                <InputLabel>状态</InputLabel>
                <Select
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
            <Grid item xs={12}>
              <TextField
                fullWidth
                label="描述"
                value={form.description}
                placeholder="用于说明通知场景，便于团队协作"
                onChange={(e) => handleFormChange('description', e.target.value)}
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <FormControl fullWidth>
                <InputLabel>提供商</InputLabel>
                <Select
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
                  <Typography variant="caption" color="text.secondary">
                    {providerHint}
                  </Typography>
                )}
              </FormControl>
            </Grid>
            <Grid item xs={12} md={6}>
              <TextField
                fullWidth
                label="Webhook URL"
                value={form.webhook_url}
                onChange={(e) => handleFormChange('webhook_url', e.target.value)}
                error={Boolean(errors.webhook_url)}
                helperText={errors.webhook_url || '复制自各渠道机器人配置界面'}
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <TextField
                fullWidth
                label="Secret (可选)"
                value={form.secret}
                onChange={(e) => handleFormChange('secret', e.target.value)}
                helperText="部分渠道使用加签时需要填写"
              />
            </Grid>
            <Grid item xs={12} md={6}>
              <TextField
                fullWidth
                label="Access Token (可选)"
                value={form.access_token}
                onChange={(e) => handleFormChange('access_token', e.target.value)}
              />
            </Grid>
            <Grid item xs={12}>
              <FormControl fullWidth error={Boolean(errors.enabled_events)}>
                <InputLabel>订阅事件</InputLabel>
                <Select
                  label="订阅事件"
                  multiple
                  value={form.enabled_events}
                  onChange={handleEventsChange}
                  renderValue={(selected) => selected.join(', ')}
                >
                  {eventOptions.map((evt) => (
                    <MenuItem key={evt} value={evt}>
                      {evt}
                    </MenuItem>
                  ))}
                </Select>
                <Typography variant="caption" color={errors.enabled_events ? 'error' : 'text.secondary'}>
                  {errors.enabled_events || 'Webhook 将在所选事件发生时触发'}
                </Typography>
              </FormControl>
            </Grid>
            <Grid item xs={12} md={4}>
              <TextField
                fullWidth
                label="消息格式"
                value={form.message_format}
                onChange={(e) => handleFormChange('message_format', e.target.value)}
                helperText="markdown / text / card"
              />
            </Grid>
            <Grid item xs={12} md={2}>
              <Box sx={{ display: 'flex', alignItems: 'center', height: '100%' }}>
                <Switch
                  checked={form.is_async}
                  onChange={(e) => handleFormChange('is_async', e.target.checked)}
                />
                <Typography variant="body2" sx={{ ml: 1 }}>
                  异步发送
                </Typography>
              </Box>
            </Grid>
            <Grid item xs={12} md={2}>
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
            <Grid item xs={12} md={2}>
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
            <Grid item xs={12} md={2}>
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
            <Grid item xs={12} md={2}>
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
            <Grid item xs={12} md={2}>
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
    </Box>
  )
}

export default WebhookSettings
