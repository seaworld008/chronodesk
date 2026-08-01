import { type FormEvent, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Drawer,
  FormControlLabel,
  MenuItem,
  Stack,
  Switch,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from '@mui/material'
import { Add as AddIcon } from '@mui/icons-material'
import { useNotify } from 'react-admin'
import {
  InlineDetails,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import type { SLAConfig } from '@/lib/generated/human-api'
import AutomationDirectoryLayout from './AutomationDirectoryLayout'
import {
  automationDirectoryCommandPath,
  useAutomationDirectory,
} from './useAutomationDirectory'

interface SLAForm {
  name: string
  description: string
  responseTime: string
  resolutionTime: string
  isActive: boolean
  isDefault: boolean
}

const emptyForm: SLAForm = {
  name: '',
  description: '',
  responseTime: '30',
  resolutionTime: '240',
  isActive: true,
  isDefault: false,
}

const columns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 280, minWidth: 180, maxWidth: 480 },
  { key: 'state', defaultWidth: 156, minWidth: 120, maxWidth: 220 },
  { key: 'response', defaultWidth: 128, minWidth: 104, maxWidth: 200 },
  { key: 'resolution', defaultWidth: 128, minWidth: 104, maxWidth: 200 },
  { key: 'scope', defaultWidth: 260, minWidth: 180, maxWidth: 420 },
  { key: 'compliance', defaultWidth: 160, minWidth: 120, maxWidth: 240 },
  { key: 'updated', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
  {
    key: 'action',
    defaultWidth: 104,
    minWidth: 88,
    maxWidth: 160,
    sticky: 'right',
  },
]

const SLAStateChips = ({ config }: { config: SLAConfig }) => (
  <Stack direction="row" spacing={0.5}>
    <Chip
      size="small"
      color={config.is_active ? 'success' : 'default'}
      label={config.is_active ? '启用' : '停用'}
    />
    {config.is_default && (
      <Chip size="small" color="primary" label="默认" />
    )}
  </Stack>
)

const AutomationSLAList = () => {
  const notify = useNotify()
  const [activeFilter, setActiveFilter] = useState('')
  const [appliedActive, setAppliedActive] = useState('')
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<SLAForm>(emptyForm)
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [selected, setSelected] = useState<SLAConfig | null>(null)
  const appliedFilters = useMemo(
    () => ({
      is_active:
        appliedActive === '' ? undefined : appliedActive === 'true',
    }),
    [appliedActive],
  )
  const directory = useAutomationDirectory<SLAConfig>(
    'sla',
    appliedFilters,
    '加载 SLA 配置失败，请稍后重试',
  )
  const items = directory.result?.items ?? []

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    directory.resetPage()
    setAppliedActive(activeFilter)
  }

  const handleCreate = async () => {
    const responseTime = Number(form.responseTime)
    const resolutionTime = Number(form.resolutionTime)
    if (!form.name.trim()) {
      setFormError('请输入 SLA 名称')
      return
    }
    if (
      !Number.isInteger(responseTime) ||
      responseTime < 1 ||
      !Number.isInteger(resolutionTime) ||
      resolutionTime < 1
    ) {
      setFormError('首次响应和解决时限必须是大于 0 的整数分钟')
      return
    }
    setSaving(true)
    setFormError('')
    try {
      await apiFetch<SLAConfig>(
        await automationDirectoryCommandPath('sla'),
        {
          method: 'POST',
          body: JSON.stringify({
            name: form.name.trim(),
            description: form.description.trim(),
            response_time: responseTime,
            resolution_time: resolutionTime,
            is_active: form.isActive,
            is_default: form.isDefault,
          }),
        },
      )
      setFormOpen(false)
      setForm(emptyForm)
      directory.resetPage()
      directory.reload()
      notify('SLA 配置已创建', { type: 'success' })
    } catch (error: unknown) {
      setFormError(
        localizedUnknownErrorMessage(error, '创建 SLA 配置失败'),
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <AutomationDirectoryLayout
      title="SLA 配置"
      description="定义当前项目的响应与解决时限。默认配置优先匹配，列表按默认状态和创建时间稳定排序。"
      action={
        <Button
          variant="contained"
          startIcon={<AddIcon />}
          onClick={() => setFormOpen(true)}
        >
          新建 SLA 配置
        </Button>
      }
      filters={
        <Stack
          component="form"
          direction={{ xs: 'column', sm: 'row' }}
          spacing={1}
          onSubmit={applyFilters}
        >
          <TextField
            select
            size="small"
            label="启用状态"
            value={activeFilter}
            sx={{ minWidth: 160 }}
            onChange={(event) => setActiveFilter(event.target.value)}
          >
            <MenuItem value="">全部状态</MenuItem>
            <MenuItem value="true">启用</MenuItem>
            <MenuItem value="false">停用</MenuItem>
          </TextField>
          <Button type="submit" variant="outlined">
            应用筛选
          </Button>
          <Button
            onClick={() => {
              setActiveFilter('')
              setAppliedActive('')
              directory.resetPage()
            }}
          >
            清空
          </Button>
        </Stack>
      }
      tableID="automation.sla"
      tableLabel="SLA 配置列表"
      columns={columns}
      loading={directory.loading}
      error={directory.error}
      empty={items.length === 0}
      emptyMessage="当前项目暂无 SLA 配置"
      total={directory.result?.total ?? 0}
      page={directory.page}
      pageSize={directory.pageSize}
      onRetry={directory.retry}
      onPageChange={directory.setPage}
      onPageSizeChange={directory.setPageSize}
      overlays={
        <>
          <Dialog
            open={formOpen}
            onClose={() => !saving && setFormOpen(false)}
            fullWidth
            maxWidth="sm"
            aria-labelledby="create-sla-title"
          >
            <DialogTitle id="create-sla-title">
              新建 SLA 配置
            </DialogTitle>
            <DialogContent>
              <Stack spacing={2} sx={{ pt: 1 }}>
                {formError && <Alert severity="error">{formError}</Alert>}
                <TextField
                  autoFocus
                  required
                  label="名称"
                  value={form.name}
                  slotProps={{ htmlInput: { maxLength: 100 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      name: event.target.value,
                    }))
                  }
                />
                <TextField
                  label="描述"
                  multiline
                  minRows={2}
                  value={form.description}
                  slotProps={{ htmlInput: { maxLength: 500 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      description: event.target.value,
                    }))
                  }
                />
                <TextField
                  required
                  label="首次响应时限（分钟）"
                  type="number"
                  value={form.responseTime}
                  slotProps={{ htmlInput: { min: 1 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      responseTime: event.target.value,
                    }))
                  }
                />
                <TextField
                  required
                  label="解决时限（分钟）"
                  type="number"
                  value={form.resolutionTime}
                  slotProps={{ htmlInput: { min: 1 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      resolutionTime: event.target.value,
                    }))
                  }
                />
                <FormControlLabel
                  control={
                    <Switch
                      checked={form.isActive}
                      onChange={(_, checked) =>
                        setForm((previous) => ({
                          ...previous,
                          isActive: checked,
                        }))
                      }
                    />
                  }
                  label="创建后启用"
                />
                <FormControlLabel
                  control={
                    <Switch
                      checked={form.isDefault}
                      onChange={(_, checked) =>
                        setForm((previous) => ({
                          ...previous,
                          isDefault: checked,
                        }))
                      }
                    />
                  }
                  label="设为当前项目默认 SLA"
                />
                {form.isDefault && (
                  <Alert severity="warning">
                    保存后会取消当前项目其他 SLA 的默认状态。
                  </Alert>
                )}
              </Stack>
            </DialogContent>
            <DialogActions>
              <Button onClick={() => setFormOpen(false)} disabled={saving}>
                取消
              </Button>
              <Button
                variant="contained"
                onClick={() => void handleCreate()}
                disabled={saving}
              >
                {saving ? '创建中…' : '确认创建'}
              </Button>
            </DialogActions>
          </Dialog>
          <Drawer
            anchor="right"
            open={selected !== null}
            onClose={() => setSelected(null)}
          >
            {selected && (
              <Stack
                spacing={2}
                sx={{ width: { xs: 320, sm: 440 }, p: 3 }}
              >
                <Typography variant="h5" component="h2">
                  {selected.name}
                </Typography>
                <SLAStateChips config={selected} />
                <Typography>{selected.description || '暂无描述'}</Typography>
                <InlineDetails
                  primary={`首次响应：${selected.response_time} 分钟`}
                  secondary={`解决时限：${selected.resolution_time} 分钟`}
                  title={`首次响应：${selected.response_time} 分钟 · 解决时限：${selected.resolution_time} 分钟`}
                />
                <InlineDetails
                  primary={`应用 ${selected.applied_count ?? 0} 次`}
                  secondary={`违约 ${selected.violation_count ?? 0} 次`}
                  title={`应用 ${selected.applied_count ?? 0} 次 · 违约 ${selected.violation_count ?? 0} 次`}
                />
                <Button onClick={() => setSelected(null)}>关闭</Button>
              </Stack>
            )}
          </Drawer>
        </>
      }
    >
      <TableHead>
        <TableRow>
          <TableCell>配置名称</TableCell>
          <TableCell>状态</TableCell>
          <TableCell>首次响应</TableCell>
          <TableCell>解决时限</TableCell>
          <TableCell>适用范围</TableCell>
          <TableCell>合规统计</TableCell>
          <TableCell>更新时间</TableCell>
          <TableCell align="right">操作</TableCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {items.map((config) => (
          <TableRow key={config.id} hover>
            <TableCell>
              <InlineDetails
                primary={config.name}
                secondary={config.description || '暂无描述'}
                title={`${config.name} · ${config.description || '暂无描述'}`}
              />
            </TableCell>
            <TableCell><SLAStateChips config={config} /></TableCell>
            <TableCell>{config.response_time} 分钟</TableCell>
            <TableCell>{config.resolution_time} 分钟</TableCell>
            <TableCell>
              <TruncatedText
                title={[
                  config.ticket_type && `类型：${config.ticket_type}`,
                  config.priority && `优先级：${config.priority}`,
                  config.category && `分类：${config.category}`,
                ].filter(Boolean).join('；') || '全部工单'}
              >
                {[
                  config.ticket_type,
                  config.priority,
                  config.category,
                ].filter(Boolean).join(' / ') || '全部工单'}
              </TruncatedText>
            </TableCell>
            <TableCell>
              {Number(config.compliance_rate ?? 0).toFixed(1)}%
            </TableCell>
            <TableCell>
              {new Date(config.updated_at).toLocaleString('zh-CN')}
            </TableCell>
            <TableCell align="right">
              <Button
                size="small"
                aria-label={`查看 SLA：${config.name}`}
                onClick={() => setSelected(config)}
              >
                查看
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </AutomationDirectoryLayout>
  )
}

export default AutomationSLAList
