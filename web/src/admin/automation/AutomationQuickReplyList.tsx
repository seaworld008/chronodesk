import { type FormEvent, useMemo, useState } from 'react'
import {
  Alert,
  Autocomplete,
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
import type { QuickReply } from '@/lib/generated/human-api'
import {
  MAX_TICKET_TAG_LENGTH,
  MAX_TICKET_TAGS,
  normalizeTagList,
  validateTagsInput,
} from '@/admin/tickets/tagUtils'
import AutomationDirectoryLayout from './AutomationDirectoryLayout'
import {
  automationDirectoryCommandPath,
  useAutomationDirectory,
} from './useAutomationDirectory'

interface QuickReplyForm {
  name: string
  category: string
  content: string
  tags: string[]
  isPublic: boolean
}

const emptyForm: QuickReplyForm = {
  name: '',
  category: '',
  content: '',
  tags: [],
  isPublic: false,
}

const columns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 240, minWidth: 160, maxWidth: 420 },
  { key: 'category', defaultWidth: 144, minWidth: 104, maxWidth: 240 },
  { key: 'content', defaultWidth: 340, minWidth: 200, maxWidth: 560 },
  { key: 'tags', defaultWidth: 360, minWidth: 220, maxWidth: 600 },
  { key: 'scope', defaultWidth: 104, minWidth: 88, maxWidth: 160 },
  { key: 'usage', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
  {
    key: 'action',
    defaultWidth: 160,
    minWidth: 136,
    maxWidth: 240,
    sticky: 'right',
  },
]

const normalizeTags = (values: readonly string[]) => {
  const normalized = normalizeTagList(values)
  return normalized.filter(
    (tag) => Array.from(tag).length <= MAX_TICKET_TAG_LENGTH,
  ).slice(0, MAX_TICKET_TAGS)
}

const parseTags = (value: string) =>
  normalizeTags(value.split(',').filter(Boolean))

const TagSummary = ({ tags }: { tags: readonly string[] }) => {
  if (tags.length === 0) return <Typography color="text.secondary">无标签</Typography>
  const visible = tags.slice(0, 3)
  return (
    <Stack spacing={0.5}>
      <Stack direction="row" spacing={0.5}>
        {visible.map((tag) => (
          <Chip key={tag.toLocaleLowerCase('zh-CN')} size="small" label={tag} />
        ))}
        {tags.length > visible.length && (
          <Chip size="small" label={`+${tags.length - visible.length}`} />
        )}
      </Stack>
      <Typography variant="caption" color="text.secondary">
        已选择 {tags.length} 项
      </Typography>
    </Stack>
  )
}

const AutomationQuickReplyList = () => {
  const notify = useNotify()
  const [category, setCategory] = useState('')
  const [keyword, setKeyword] = useState('')
  const [publicFilter, setPublicFilter] = useState('')
  const [appliedFilters, setAppliedFilters] = useState<{
    category?: string
    keyword?: string
    is_public?: boolean
  }>({})
  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<QuickReplyForm>(emptyForm)
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [usingID, setUsingID] = useState<number | null>(null)
  const [selected, setSelected] = useState<QuickReply | null>(null)
  const normalizedFilters = useMemo(
    () => appliedFilters,
    [appliedFilters],
  )
  const directory = useAutomationDirectory<QuickReply>(
    'quick-replies',
    normalizedFilters,
    '加载快捷回复失败，请稍后重试',
  )
  const items = directory.result?.items ?? []

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    const normalizedCategory = category.trim()
    const normalizedKeyword = keyword.trim()
    if (
      normalizedCategory.length > 50 ||
      normalizedKeyword.length > 200
    ) return
    directory.resetPage()
    setAppliedFilters({
      ...(normalizedCategory ? { category: normalizedCategory } : {}),
      ...(normalizedKeyword ? { keyword: normalizedKeyword } : {}),
      ...(publicFilter
        ? { is_public: publicFilter === 'true' }
        : {}),
    })
  }

  const handleCreate = async () => {
    const tags = normalizeTags(form.tags)
    const serializedTags = tags.join(',')
    const tagError = validateTagsInput(form.tags)
    if (!form.name.trim() || !form.content.trim()) {
      setFormError('快捷回复名称和内容不能为空')
      return
    }
    if (tagError) {
      setFormError(tagError)
      return
    }
    if (serializedTags.length > 200) {
      setFormError('标签合计不能超过 200 个字符，请减少标签数量或长度')
      return
    }
    setSaving(true)
    setFormError('')
    try {
      await apiFetch<QuickReply>(
        await automationDirectoryCommandPath('quick-replies'),
        {
          method: 'POST',
          body: JSON.stringify({
            name: form.name.trim(),
            category: form.category.trim(),
            content: form.content.trim(),
            tags: serializedTags,
            is_public: form.isPublic,
          }),
        },
      )
      setFormOpen(false)
      setForm(emptyForm)
      directory.resetPage()
      directory.reload()
      notify('快捷回复已创建', { type: 'success' })
    } catch (error: unknown) {
      setFormError(
        localizedUnknownErrorMessage(error, '创建快捷回复失败'),
      )
    } finally {
      setSaving(false)
    }
  }

  const recordReplyUse = async (reply: QuickReply) => {
    setUsingID(reply.id)
    try {
      await apiFetch<void>(
        await automationDirectoryCommandPath(
          `quick-replies/${reply.id}/use`,
        ),
        { method: 'POST' },
      )
      directory.reload()
      notify('已记录快捷回复使用', { type: 'success' })
    } catch (error: unknown) {
      notify(
        localizedUnknownErrorMessage(error, '快捷回复使用失败'),
        { type: 'error' },
      )
    } finally {
      setUsingID(null)
    }
  }

  return (
    <AutomationDirectoryLayout
      title="快捷回复"
      description="维护当前项目可复用的回复内容。私有回复仅创建者可用，公共回复向项目处理人开放。"
      action={
        <Button
          variant="contained"
          startIcon={<AddIcon />}
          onClick={() => setFormOpen(true)}
        >
          新建快捷回复
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
            slotProps={{ htmlInput: { maxLength: 51 } }}
            onChange={(event) => setCategory(event.target.value)}
          />
          <TextField
            size="small"
            label="关键词"
            value={keyword}
            error={keyword.trim().length > 200}
            slotProps={{ htmlInput: { maxLength: 201 } }}
            onChange={(event) => setKeyword(event.target.value)}
          />
          <TextField
            select
            size="small"
            label="可见范围"
            value={publicFilter}
            sx={{ minWidth: 160 }}
            onChange={(event) => setPublicFilter(event.target.value)}
          >
            <MenuItem value="">全部范围</MenuItem>
            <MenuItem value="true">公共</MenuItem>
            <MenuItem value="false">我的或公共</MenuItem>
          </TextField>
          <Button type="submit" variant="outlined">
            应用筛选
          </Button>
          <Button
            onClick={() => {
              setCategory('')
              setKeyword('')
              setPublicFilter('')
              setAppliedFilters({})
              directory.resetPage()
            }}
          >
            清空
          </Button>
        </Stack>
      }
      tableID="automation.quick-replies"
      tableLabel="快捷回复列表"
      columns={columns}
      loading={directory.loading}
      error={directory.error}
      empty={items.length === 0}
      emptyMessage="当前筛选条件下暂无快捷回复"
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
            aria-labelledby="create-quick-reply-title"
          >
            <DialogTitle id="create-quick-reply-title">
              新建快捷回复
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
                  required
                  label="回复内容"
                  multiline
                  minRows={5}
                  value={form.content}
                  onChange={(event) =>
                    setForm((previous) => ({
                      ...previous,
                      content: event.target.value,
                    }))
                  }
                />
                <Autocomplete
                  multiple
                  freeSolo
                  options={[]}
                  value={form.tags}
                  filterSelectedOptions
                  onChange={(_, values) =>
                    setForm((previous) => ({
                      ...previous,
                      tags: normalizeTags(values),
                    }))
                  }
                  renderValue={(values, getItemProps) =>
                    values.map((tag, index) => {
                      const { key, ...itemProps } = getItemProps({ index })
                      return (
                        <Chip
                          {...itemProps}
                          key={key}
                          size="small"
                          label={tag}
                        />
                      )
                    })
                  }
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label="标签"
                      helperText={`已选择 ${form.tags.length} 项，最多 ${MAX_TICKET_TAGS} 项；每项最多 ${MAX_TICKET_TAG_LENGTH} 个字符`}
                    />
                  )}
                />
                <FormControlLabel
                  control={
                    <Switch
                      checked={form.isPublic}
                      onChange={(_, checked) =>
                        setForm((previous) => ({
                          ...previous,
                          isPublic: checked,
                        }))
                      }
                    />
                  }
                  label="设为项目公共回复"
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
                  <Chip
                    size="small"
                    label={selected.category || '未分类'}
                  />
                  <Chip
                    size="small"
                    color={selected.is_public ? 'primary' : 'default'}
                    label={selected.is_public ? '公共' : '私有'}
                  />
                </Stack>
                <Typography sx={{ whiteSpace: 'pre-wrap' }}>
                  {selected.content}
                </Typography>
                <TagSummary tags={parseTags(selected.tags)} />
                <Button onClick={() => setSelected(null)}>关闭</Button>
              </Stack>
            )}
          </Drawer>
        </>
      }
    >
      <TableHead>
        <TableRow>
          <TableCell>回复名称</TableCell>
          <TableCell>分类</TableCell>
          <TableCell>内容</TableCell>
          <TableCell>标签</TableCell>
          <TableCell>范围</TableCell>
          <TableCell>使用次数</TableCell>
          <TableCell align="right">操作</TableCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {items.map((reply) => {
          const tags = parseTags(reply.tags)
          return (
            <TableRow key={reply.id} hover>
              <TableCell>
                <InlineDetails
                  primary={reply.name}
                  secondary={reply.is_public ? '项目共享' : '仅自己可见'}
                  title={`${reply.name} · ${
                    reply.is_public ? '项目共享' : '仅自己可见'
                  }`}
                />
              </TableCell>
              <TableCell>
                <Chip size="small" label={reply.category || '未分类'} />
              </TableCell>
              <TableCell>
                <TruncatedText title={reply.content}>
                  {reply.content}
                </TruncatedText>
              </TableCell>
              <TableCell><TagSummary tags={tags} /></TableCell>
              <TableCell>
                <Chip
                  size="small"
                  color={reply.is_public ? 'primary' : 'default'}
                  label={reply.is_public ? '公共' : '私有'}
                />
              </TableCell>
              <TableCell>{reply.usage_count ?? 0}</TableCell>
              <TableCell align="right">
                <Stack
                  direction="row"
                  spacing={0.5}
                  sx={{ justifyContent: 'flex-end' }}
                >
                  <Button
                    size="small"
                    aria-label={`查看快捷回复：${reply.name}`}
                    onClick={() => setSelected(reply)}
                  >
                    查看
                  </Button>
                  <Button
                    size="small"
                    aria-label={`使用快捷回复：${reply.name}`}
                    disabled={usingID === reply.id}
                    onClick={() => void recordReplyUse(reply)}
                  >
                    {usingID === reply.id ? '记录中…' : '使用'}
                  </Button>
                </Stack>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </AutomationDirectoryLayout>
  )
}

export default AutomationQuickReplyList
