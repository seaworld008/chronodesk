import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Paper,
  Stack,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  Tab,
  TextField,
  Typography,
  Tooltip,
} from '@mui/material'
import { Save as SaveIcon, Refresh as RefreshIcon } from '@mui/icons-material'
import { useNotify } from 'react-admin'
import { apiFetch } from '@/lib/apiClient'
import BackButton from '../common/BackButton'

interface SystemConfig {
  id: number
  key: string
  value: string
  value_type: 'string' | 'int' | 'bool' | 'json'
  description: string
  category: string
  group: string
  is_required: boolean
  is_active: boolean
  default_value?: string
  min_value?: number
  max_value?: number
}

interface EditableConfig extends SystemConfig {
  dirty?: boolean
  boolValue?: boolean
  intValue?: number
  jsonValue?: string
}

const categories = [
  { id: 'system', label: '系统信息' },
  { id: 'security', label: '安全策略' },
  { id: 'email', label: '邮件模板' },
  { id: 'ticket', label: '工单设置' },
  { id: 'notify', label: '通知设置' },
]

const parseInitialValue = (config: SystemConfig): EditableConfig => {
  const editable: EditableConfig = { ...config }
  switch (config.value_type) {
    case 'bool':
      try {
        editable.boolValue = JSON.parse(config.value)
      } catch (error) {
        editable.boolValue = config.value === 'true'
      }
      break
    case 'int':
      try {
        editable.intValue = JSON.parse(config.value)
      } catch (error) {
        editable.intValue = Number(config.value)
      }
      break
    case 'json':
      editable.jsonValue = config.value
      break
    default:
      break
  }
  return editable
}

const SystemSettings: React.FC = () => {
  const notify = useNotify()
  const [activeTab, setActiveTab] = useState(categories[0].id)
  const [loading, setLoading] = useState(false)
  const [configs, setConfigs] = useState<Record<string, EditableConfig[]>>({})
  const [savingKey, setSavingKey] = useState<string | null>(null)
  const [bulkSaving, setBulkSaving] = useState(false)

  const extractErrorMessage = useCallback((error: unknown, fallback: string) => {
    if (error instanceof Error) {
      return error.message
    }
    return fallback
  }, [])

  const loadConfigs = useCallback(async (category: string, silent = false) => {
    try {
      if (!silent) setLoading(true)
      const data = await apiFetch<SystemConfig[]>(`/admin/configs?category=${category}`)
      setConfigs((prev) => ({
        ...prev,
        [category]: data.map(parseInitialValue),
      }))
    } catch (error: unknown) {
      notify(extractErrorMessage(error, '加载配置失败'), { type: 'error' })
    } finally {
      if (!silent) setLoading(false)
    }
  }, [notify, extractErrorMessage])

  useEffect(() => {
    loadConfigs(activeTab)
  }, [activeTab, loadConfigs])

  const currentConfigs = useMemo(() => configs[activeTab] ?? [], [configs, activeTab])

  const handleValueChange = (index: number, value: string | number | boolean) => {
    setConfigs((prev) => {
      const list = prev[activeTab] ? [...prev[activeTab]] : []
      const config = { ...list[index] }
      switch (config.value_type) {
        case 'bool':
          config.boolValue = Boolean(value)
          config.value = JSON.stringify(Boolean(value))
          break
        case 'int':
          config.intValue = Number(value)
          config.value = JSON.stringify(Number(value))
          break
        case 'json':
          config.jsonValue = String(value)
          config.value = String(value)
          break
        default:
          config.value = String(value)
          break
      }
      config.dirty = true
      list[index] = config
      return {
        ...prev,
        [activeTab]: list,
      }
    })
  }

  const handleSave = async (config: EditableConfig, silent = false) => {
    try {
      setSavingKey(config.key)
      const payload = {
        key: config.key,
        value: config.value,
        value_type: config.value_type,
        description: config.description,
        category: config.category,
        group: config.group,
        is_required: config.is_required,
        is_active: config.is_active,
      }
      await apiFetch(`/admin/configs/${encodeURIComponent(config.key)}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      })
      if (!silent) {
        notify('配置已更新', { type: 'success' })
      }
      setConfigs((prev) => {
        const list = prev[activeTab] ? [...prev[activeTab]] : []
        const idx = list.findIndex((item) => item.key === config.key)
        if (idx !== -1) {
          list[idx] = { ...config, dirty: false }
        }
        return {
          ...prev,
          [activeTab]: list,
        }
      })
      return true
    } catch (error: unknown) {
      if (!silent) {
        notify(extractErrorMessage(error, '保存失败'), { type: 'error' })
      }
      return false
    } finally {
      setSavingKey(null)
    }
  }

  const handleRefresh = () => loadConfigs(activeTab)

  const renderValueCell = (config: EditableConfig, index: number) => {
    switch (config.value_type) {
      case 'bool':
        return (
          <Switch
            checked={config.boolValue ?? false}
            onChange={(e) => handleValueChange(index, e.target.checked)}
          />
        )
      case 'int':
        return (
          <TextField
            type="number"
            size="small"
            value={config.intValue ?? ''}
            onChange={(e) => handleValueChange(index, e.target.value)}
            sx={{ width: 160 }}
          />
        )
      case 'json':
        return (
          <TextField
            size="small"
            multiline
            minRows={2}
            value={config.jsonValue ?? ''}
            onChange={(e) => handleValueChange(index, e.target.value)}
            sx={{ width: 320 }}
            helperText="请输入合法 JSON"
          />
        )
      default:
        return (
          <TextField
            size="small"
            value={config.value}
            onChange={(e) => handleValueChange(index, e.target.value)}
            sx={{ width: 240 }}
          />
        )
    }
  }

  const dirtyCount = useMemo(() => currentConfigs.filter((config) => config.dirty).length, [currentConfigs])

  const bulkSave = async () => {
    const dirtyConfigs = currentConfigs.filter((config) => config.dirty)
    if (dirtyConfigs.length === 0) return
    setBulkSaving(true)
    for (const cfg of dirtyConfigs) {
      const result = await handleSave(cfg, true)
      if (!result) {
        notify(`更新 ${cfg.key} 失败`, { type: 'error' })
      }
    }
    notify('批量更新已完成', { type: 'success' })
    setBulkSaving(false)
  }

  return (
    <Box sx={{ p: 3 }}>
      <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 2 }}>
        <Stack direction="row" spacing={2} alignItems="center">
          <BackButton fallbackPath="/system-settings" />
          <Typography variant="h4">系统设置概览</Typography>
        </Stack>
        <Stack direction="row" spacing={1}>
          <Button startIcon={<RefreshIcon />} onClick={handleRefresh}>刷新</Button>
          <Button
            startIcon={<SaveIcon />}
            onClick={bulkSave}
            disabled={dirtyCount === 0 || bulkSaving}
            variant="contained"
          >
            {bulkSaving ? '保存中…' : `保存全部 (${dirtyCount})`}
          </Button>
        </Stack>
      </Stack>

      <Paper sx={{ mb: 2 }}>
        <Tabs value={activeTab} onChange={(_, value) => setActiveTab(value)} textColor="primary" indicatorColor="primary" variant="scrollable">
          {categories.map((category) => (
            <Tab key={category.id} value={category.id} label={category.label} />
          ))}
        </Tabs>
      </Paper>

      {loading && (
        <Box sx={{ display: 'flex', justifyContent: 'center', mt: 4 }}>
          <CircularProgress />
        </Box>
      )}

      {!loading && currentConfigs.length === 0 && (
        <Alert severity="info">当前分类暂无配置</Alert>
      )}

      {!loading && currentConfigs.length > 0 && (
        <TableContainer component={Paper}>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>配置项</TableCell>
                <TableCell>描述</TableCell>
                <TableCell>值</TableCell>
                <TableCell>类型</TableCell>
                <TableCell align="right">操作</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {currentConfigs.map((config, index) => (
                <TableRow key={config.key} hover selected={config.dirty}>
                  <TableCell>
                    <Typography fontWeight={600}>{config.key}</Typography>
                    <Typography variant="caption" color="text.secondary">分组：{config.group || '默认'}</Typography>
                  </TableCell>
                  <TableCell sx={{ maxWidth: 280 }}>
                    <Typography variant="body2">{config.description || '—'}</Typography>
                    {config.default_value && (
                      <Typography variant="caption" color="text.secondary">默认值：{config.default_value}</Typography>
                    )}
                  </TableCell>
                  <TableCell>{renderValueCell(config, index)}</TableCell>
                  <TableCell>{config.value_type}</TableCell>
                  <TableCell align="right">
                    <Tooltip title="保存">
                      <span>
                        <Button
                          size="small"
                          startIcon={<SaveIcon fontSize="inherit" />}
                          onClick={() => handleSave(config)}
                          disabled={!config.dirty || savingKey === config.key}
                        >
                          {savingKey === config.key ? '保存中…' : '保存'}
                        </Button>
                      </span>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      )}
    </Box>
  )
}

export default SystemSettings
