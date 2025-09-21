import React, { useCallback, useEffect, useMemo, useState } from 'react'
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
  FormControlLabel,
  Grid,
  Paper,
  Stack,
  Switch,
  TextField,
  Typography,
} from '@mui/material'
import { useNotify } from 'react-admin'
import { apiFetch } from '@/lib/apiClient'
import BackButton from '../common/BackButton'

interface EmailConfigResponse {
  id: number
  email_verification_enabled: boolean
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_use_tls: boolean
  smtp_use_ssl: boolean
  from_email: string
  from_name: string
  welcome_email_subject: string
  welcome_email_template: string
  otp_email_subject: string
  otp_email_template: string
  is_active: boolean
  is_configured: boolean
  can_send_email: boolean
}

interface EmailForm extends EmailConfigResponse {
  smtp_password?: string
}

interface TestForm {
  to_email: string
  subject: string
  content: string
}

const defaultTestForm: TestForm = {
  to_email: '',
  subject: '测试邮件',
  content: '这是一封来自工单管理系统的测试邮件，如果您收到此邮件说明配置已生效。',
}

const EmailSettings: React.FC = () => {
  const notify = useNotify()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testDialogOpen, setTestDialogOpen] = useState(false)
  const [config, setConfig] = useState<EmailForm | null>(null)
  const [originalConfig, setOriginalConfig] = useState<EmailForm | null>(null)
  const [testForm, setTestForm] = useState<TestForm>(defaultTestForm)

  const extractErrorMessage = useCallback((error: unknown, fallback: string) => {
    if (error instanceof Error) {
      return error.message
    }
    return fallback
  }, [])

  const loadConfig = useCallback(async () => {
    try {
      setLoading(true)
      const data = await apiFetch<EmailConfigResponse>('/admin/email-config')
      const mapped: EmailForm = {
        ...data,
      }
      setConfig(mapped)
      setOriginalConfig(mapped)
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '加载邮件配置失败'), { type: 'error' })
    } finally {
      setLoading(false)
    }
  }, [notify, extractErrorMessage])

  useEffect(() => {
    loadConfig()
  }, [loadConfig])

  const handleChange = <K extends keyof EmailForm>(key: K, value: EmailForm[K]) => {
    if (!config) return
    setConfig({ ...config, [key]: value })
  }

  const handleSave = async () => {
    if (!config) return
    setSaving(true)
    try {
      const payload: Record<string, unknown> = {
        email_verification_enabled: config.email_verification_enabled,
        smtp_host: config.smtp_host,
        smtp_port: Number(config.smtp_port || 0),
        smtp_username: config.smtp_username,
        smtp_use_tls: config.smtp_use_tls,
        smtp_use_ssl: config.smtp_use_ssl,
        from_email: config.from_email,
        from_name: config.from_name,
        welcome_email_subject: config.welcome_email_subject,
        welcome_email_template: config.welcome_email_template,
        otp_email_subject: config.otp_email_subject,
        otp_email_template: config.otp_email_template,
      }

      if (config.smtp_password && config.smtp_password.trim() !== '') {
        payload.smtp_password = config.smtp_password
      }

      await apiFetch('/admin/email-config', {
        method: 'PUT',
        body: JSON.stringify(payload),
      })

      notify('邮件配置保存成功', { type: 'success' })
      setOriginalConfig({ ...config })
      if (config.smtp_password) {
        setConfig({ ...config, smtp_password: '' })
      }
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '保存邮件配置失败'), { type: 'error' })
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    try {
      await apiFetch('/admin/email-config/test', {
        method: 'POST',
        body: JSON.stringify(testForm),
      })
      notify('测试邮件发送成功，请检查收件箱', { type: 'success' })
      setTestDialogOpen(false)
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '测试邮件发送失败'), { type: 'error' })
    } finally {
      setTesting(false)
    }
  }

  const hasChanges = useMemo(() => {
    if (!config || !originalConfig) return false
    const keys: (keyof EmailForm)[] = [
      'email_verification_enabled',
      'smtp_host',
      'smtp_port',
      'smtp_username',
      'smtp_use_tls',
      'smtp_use_ssl',
      'from_email',
      'from_name',
      'welcome_email_subject',
      'welcome_email_template',
      'otp_email_subject',
      'otp_email_template',
    ]
    return keys.some((key) => config[key] !== originalConfig[key]) || Boolean(config?.smtp_password && config.smtp_password !== '')
  }, [config, originalConfig])

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', mt: 6 }}>
        <CircularProgress />
      </Box>
    )
  }

  if (!config) {
    return (
      <Alert severity="error" action={<Button onClick={loadConfig}>重试</Button>}>
        无法加载邮件配置
      </Alert>
    )
  }

  return (
    <Box sx={{ p: 3 }}>
      <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 2 }}>
        <Stack direction="row" spacing={2} alignItems="center">
          <BackButton fallbackPath="/system-settings" />
          <Box>
            <Typography variant="h4" gutterBottom>
              邮件通知配置
            </Typography>
            <Typography color="text.secondary">
              配置 SMTP 服务器与模板，用于系统通知邮件发送。
            </Typography>
          </Box>
        </Stack>
      </Stack>

      <Paper sx={{ p: 3, mb: 3 }}>
        <Stack direction="row" spacing={2} alignItems="center" sx={{ mb: 2 }}>
          <FormControlLabel
            control={<Switch checked={config.email_verification_enabled} onChange={(e) => handleChange('email_verification_enabled', e.target.checked)} />}
            label="启用邮件功能"
          />
          {config.is_configured ? (
            <Chip label="配置完整" color="success" size="small" />
          ) : (
            <Chip label="配置不完整" color="warning" size="small" />
          )}
          {config.can_send_email ? (
            <Chip label="可发送" color="success" size="small" />
          ) : (
            <Chip label="不可发送" color="default" size="small" />
          )}
        </Stack>

        <Grid container spacing={2}>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="SMTP 主机"
              value={config.smtp_host}
              onChange={(e) => handleChange('smtp_host', e.target.value)}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="SMTP 端口"
              type="number"
              value={config.smtp_port}
              onChange={(e) => handleChange('smtp_port', Number(e.target.value))}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="SMTP 用户名"
              value={config.smtp_username}
              onChange={(e) => handleChange('smtp_username', e.target.value)}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              type="password"
              label="SMTP 密码 (留空则不修改)"
              value={config.smtp_password ?? ''}
              onChange={(e) => handleChange('smtp_password', e.target.value)}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <FormControlLabel
              control={<Switch checked={config.smtp_use_tls} onChange={(e) => handleChange('smtp_use_tls', e.target.checked)} />}
              label="启用 TLS"
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <FormControlLabel
              control={<Switch checked={config.smtp_use_ssl} onChange={(e) => handleChange('smtp_use_ssl', e.target.checked)} />}
              label="启用 SSL"
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="发件人邮箱"
              value={config.from_email}
              onChange={(e) => handleChange('from_email', e.target.value)}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="发件人名称"
              value={config.from_name}
              onChange={(e) => handleChange('from_name', e.target.value)}
            />
          </Grid>
        </Grid>
      </Paper>

      <Paper sx={{ p: 3, mb: 3 }}>
        <Typography variant="h6" gutterBottom>
          邮件模板
        </Typography>
        <Grid container spacing={2}>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="欢迎邮件标题"
              value={config.welcome_email_subject}
              onChange={(e) => handleChange('welcome_email_subject', e.target.value)}
            />
          </Grid>
          <Grid item xs={12} md={6}>
            <TextField
              fullWidth
              label="验证码邮件标题"
              value={config.otp_email_subject}
              onChange={(e) => handleChange('otp_email_subject', e.target.value)}
            />
          </Grid>
          <Grid item xs={12}>
            <TextField
              fullWidth
              multiline
              minRows={4}
              label="欢迎邮件模板"
              value={config.welcome_email_template}
              onChange={(e) => handleChange('welcome_email_template', e.target.value)}
            />
          </Grid>
          <Grid item xs={12}>
            <TextField
              fullWidth
              multiline
              minRows={4}
              label="验证码邮件模板"
              value={config.otp_email_template}
              onChange={(e) => handleChange('otp_email_template', e.target.value)}
            />
          </Grid>
        </Grid>
      </Paper>

      <Stack direction="row" spacing={2}>
        <Button
          variant="contained"
          onClick={handleSave}
          disabled={!hasChanges || saving}
        >
          {saving ? '保存中…' : '保存配置'}
        </Button>
        <Button
          variant="outlined"
          onClick={() => {
            if (originalConfig) {
              setConfig({ ...originalConfig, smtp_password: '' })
            }
          }}
          disabled={!hasChanges}
        >
          还原修改
        </Button>
        <Button
          variant="outlined"
          onClick={() => setTestDialogOpen(true)}
          disabled={!config.can_send_email}
        >
          发送测试邮件
        </Button>
        <Button variant="text" onClick={loadConfig}>
          刷新
        </Button>
      </Stack>

      <Dialog open={testDialogOpen} onClose={() => setTestDialogOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>发送测试邮件</DialogTitle>
        <DialogContent dividers>
          <Stack spacing={2}>
            <TextField
              label="收件人邮箱"
              value={testForm.to_email}
              onChange={(e) => setTestForm({ ...testForm, to_email: e.target.value })}
            />
            <TextField
              label="标题"
              value={testForm.subject}
              onChange={(e) => setTestForm({ ...testForm, subject: e.target.value })}
            />
            <TextField
              label="内容"
              multiline
              minRows={4}
              value={testForm.content}
              onChange={(e) => setTestForm({ ...testForm, content: e.target.value })}
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setTestDialogOpen(false)}>取消</Button>
          <Button onClick={handleTest} variant="contained" disabled={testing}>
            {testing ? '发送中…' : '发送'}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

export default EmailSettings
