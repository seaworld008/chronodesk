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
import type { TicketTemplate } from '@/lib/generated/human-api'
import AutomationDirectoryLayout from './AutomationDirectoryLayout'
import {
  automationDirectoryCommandPath,
  useAutomationDirectory,
} from './useAutomationDirectory'

interface TemplateForm {
  name: string
  description: string
  category: string
  isActive: boolean
  titleTemplate: string
  contentTemplate: string
  defaultType: string
  defaultPriority: string
  defaultStatus: string
}

const emptyForm: TemplateForm = {
  name: '',
  description: '',
  category: '',
  isActive: true,
  titleTemplate: '',
  contentTemplate: '',
  defaultType: 'request',
  defaultPriority: 'normal',
  defaultStatus: 'open',
}

const columns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 280, minWidth: 180, maxWidth: 480 },
  { key: 'category', defaultWidth: 160, minWidth: 112, maxWidth: 260 },
  { key: 'state', defaultWidth: 104, minWidth: 88, maxWidth: 160 },
  { key: 'defaults', defaultWidth: 260, minWidth: 180, maxWidth: 420 },
  { key: 'usage', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
  { key: 'updated', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
  {
    key: 'action',
    defaultWidth: 104,
    minWidth: 88,
    maxWidth: 160,
    sticky: 'right',
  },
]

const AutomationTemplateList = () => {
  const notify = useNotify()
  const [category, setCategory] = useState('')
  const [active, setActive] = useState('')
  const [appliedFilters, setAppliedFilters] = useState<{
    category?: string
    is_active?: boolean
  }>({})
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<TemplateForm>(emptyForm)
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [selected, setSelected] = useState<TicketTemplate | null>(null)
  const normalizedFilters = useMemo(
    () => appliedFilters,
    [appliedFilters],
  )
  const directory = useAutomationDirectory<TicketTemplate>(
    'templates',
    normalizedFilters,
    '加载工单模板失败，请稍后重试',
  )
  const items = directory.result?.items ?? []

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    const trimmedCategory = category.trim()
    if (trimmedCategory.length > 50) return
    directory.resetPage()
    setAppliedFilters({
      ...(trimmedCategory ? { category: trimmedCategory } : {}),
      ...(active ? { is_active: active === 'true' } : {}),
    })
  }

  const handleCreate = async () => {
    if (!form.name.trim() || !form.category.trim()) {
      setFormError('模板名称和分类不能为空')
      return
    }
    if (form.category.trim().length > 50) {
      setFormError('分类不能超过 50 个字符')
      return
    }
    setSaving(true)
    setFormError('')
    try {
      await apiFetch<TicketTemplate>(
        await automationDirectoryCommandPath('templates'),
        {
          method: 'POST',
          body: JSON.stringify({
            name: form.name.trim(),
            description: form.description.trim(),
            category: form.category.trim(),
            is_active: form.isActive,
            title_template: form.titleTemplate,
            content_template: form.contentTemplate,
            default_type: form.defaultType,
            default_priority: form.defaultPriority,
            default_status: form.defaultStatus,
          }),
        },
      )
      setFormOpen(false)
      setForm(emptyForm)
      directory.resetPage()
      directory.reload()
      notify('工单模板已创建', { type: 'success' })
    } catch (error: unknown) {
      setFormError(
        localizedUnknownErrorMessage(error, '创建工单模板失败'),
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <AutomationDirectoryLayout
      title="工单模板"
      description="管理当前项目的标准建单内容和默认属性。列表由服务端分页，模板详情按项目范围加载。"
      action={
        <Button
          variant="contained"
          startIcon={<AddIcon />}
          onClick={() => setFormOpen(true)}
        >
          新建工单模板
        </Button>
      }
      filters={
        <Stack
          component="form"
          direction={{ xs: 'column', md: 'row' }}
          spacing={1}
          onSubmit={applyFilters}
        >
          <TextField
            size="small"
            label="分类"
            value={category}
            error={category.trim().length > 50}
            helperText={
              category.trim().length > 50 ? '最多 50 个字符' : ' '
            }
            slotProps={{ htmlInput: { maxLength: 51 } }}
            onChange={(event) => setCategory(event.target.value)}
          />
          <TextField
            select
            size="small"
            label="启用状态"
            value={active}
            sx={{ minWidth: 160 }}
            onChange={(event) => setActive(event.target.value)}
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
              setCategory('')
              setActive('')
              setAppliedFilters({})
              directory.resetPage()
            }}
          >
            清空
          </Button>
        </Stack>
      }
      tableID="automation.templates"
      tableLabel="工单模板列表"
      columns={columns}
      loading={directory.loading}
      error={directory.error}
      empty={items.length === 0}
      emptyMessage="当前项目暂无工单模板"
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
            maxWidth="md"
            aria-labelledby="create-template-title"
          >
            <DialogTitle id="create-template-title">
              新建工单模板
            </DialogTitle>
            <DialogContent>
              <Stack spacing={2} sx={{ pt: 1 }}>
                {formError && <Alert severity="error">{formError}</Alert>}
                <TextField
                  autoFocus
                  required
                  label="模板名称"
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
                  required
                  label="分类"
                  value={form.category}
                  slotProps={{ htmlInput: { maxLength: 50 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      category: event.target.value,
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
                  label="标题模板"
                  value={form.titleTemplate}
                  slotProps={{ htmlInput: { maxLength: 200 } }}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      titleTemplate: event.target.value,
                    }))
                  }
                />
                <TextField
                  label="内容模板"
                  multiline
                  minRows={4}
                  value={form.contentTemplate}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      contentTemplate: event.target.value,
                    }))
                  }
                />
                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}>
                  <TextField
                    select
                    fullWidth
                    label="默认类型"
                    value={form.defaultType}
                    onChange={(event) =>
                      setForm((previous) => ({
                        ...previous,
                        defaultType: event.target.value,
                      }))
                    }
                  >
                    <MenuItem value="request">请求</MenuItem>
                    <MenuItem value="incident">事件</MenuItem>
                    <MenuItem value="problem">问题</MenuItem>
                    <MenuItem value="change">变更</MenuItem>
                  </TextField>
                  <TextField
                    select
                    fullWidth
                    label="默认优先级"
                    value={form.defaultPriority}
                    onChange={(event) =>
                      setForm((previous) => ({
                        ...previous,
                        defaultPriority: event.target.value,
                      }))
                    }
                  >
                    <MenuItem value="low">低</MenuItem>
                    <MenuItem value="normal">普通</MenuItem>
                    <MenuItem value="high">高</MenuItem>
                    <MenuItem value="urgent">紧急</MenuItem>
                  </TextField>
                  <TextField
                    select
                    fullWidth
                    label="默认状态"
                    value={form.defaultStatus}
                    onChange={(event) =>
                      setForm((previous) => ({
                        ...previous,
                        defaultStatus: event.target.value,
                      }))
                    }
                  >
                    <MenuItem value="open">待处理</MenuItem>
                    <MenuItem value="in_progress">处理中</MenuItem>
                    <MenuItem value="pending">挂起</MenuItem>
                  </TextField>
                </Stack>
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
                sx={{ width: { xs: 320, sm: 480 }, p: 3 }}
              >
                <Typography variant="h5" component="h2">
                  {selected.name}
                </Typography>
                <Stack direction="row" spacing={1}>
                  <Chip size="small" label={selected.category} />
                  <Chip
                    size="small"
                    color={selected.is_active ? 'success' : 'default'}
                    label={selected.is_active ? '启用' : '停用'}
                  />
                </Stack>
                <Typography>{selected.description || '暂无描述'}</Typography>
                <InlineDetails
                  primary={selected.title_template || '未设置标题模板'}
                  secondary={selected.content_template || '未设置内容模板'}
                  title={`${selected.title_template || '未设置标题模板'} · ${selected.content_template || '未设置内容模板'}`}
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
          <TableCell>模板名称</TableCell>
          <TableCell>分类</TableCell>
          <TableCell>状态</TableCell>
          <TableCell>默认属性</TableCell>
          <TableCell>使用次数</TableCell>
          <TableCell>更新时间</TableCell>
          <TableCell align="right">操作</TableCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {items.map((template) => (
          <TableRow key={template.id} hover>
            <TableCell>
              <InlineDetails
                primary={template.name}
                secondary={template.description || '暂无描述'}
                title={`${template.name} · ${template.description || '暂无描述'}`}
              />
            </TableCell>
            <TableCell>
              <Chip size="small" label={template.category || '未分类'} />
            </TableCell>
            <TableCell>
              <Chip
                size="small"
                color={template.is_active ? 'success' : 'default'}
                label={template.is_active ? '启用' : '停用'}
              />
            </TableCell>
            <TableCell>
              <TruncatedText
                title={`${template.default_type || '—'} / ${
                  template.default_priority || '—'
                } / ${template.default_status || '—'}`}
              >
                {template.default_type || '—'} /{' '}
                {template.default_priority || '—'} /{' '}
                {template.default_status || '—'}
              </TruncatedText>
            </TableCell>
            <TableCell>{template.usage_count ?? 0}</TableCell>
            <TableCell>
              {new Date(template.updated_at).toLocaleString('zh-CN')}
            </TableCell>
            <TableCell align="right">
              <Button
                size="small"
                aria-label={`查看模板：${template.name}`}
                onClick={() => setSelected(template)}
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

export default AutomationTemplateList
