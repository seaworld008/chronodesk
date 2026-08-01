import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Paper,
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
} from '@mui/material'
import {
  Refresh as RefreshIcon,
  Save as SaveIcon,
} from '@mui/icons-material'
import { useBlocker, useNotify } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
  humanApiRoutes,
  type ListPlatformConfigsOperationQuery,
  type SystemConfig,
  type SystemConfigPage,
  type UpdateSystemConfigRequest,
} from '@/lib/generated/human-api'
import {
  InlineDetails,
  ResizableMuiTable,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'
import BackButton from '../common/BackButton'

const systemConfigColumns: ResizableColumn[] = [
  { key: 'config', defaultWidth: 260, minWidth: 180, maxWidth: 460 },
  { key: 'description', defaultWidth: 340, minWidth: 200, maxWidth: 560 },
  { key: 'value', defaultWidth: 360, minWidth: 220, maxWidth: 640 },
  { key: 'type', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
  {
    key: 'actions',
    defaultWidth: 120,
    minWidth: 104,
    maxWidth: 180,
    sticky: 'right',
  },
]

interface EditableConfig extends SystemConfig {
  boolValue?: boolean
  intValue?: number
  jsonValue?: string
}

interface CategoryPagination {
  page: number
  pageSize: number
}

const categories = [
  { id: 'system', label: '系统信息' },
  { id: 'security', label: '安全策略' },
  { id: 'email', label: '邮件模板' },
  { id: 'ticket', label: '工单设置' },
  { id: 'notify', label: '通知设置' },
]

const defaultPagination: CategoryPagination = {
  page: 0,
  pageSize: 25,
}

const valueTypeLabels: Record<SystemConfig['value_type'], string> = {
  string: '文本',
  int: '整数',
  bool: '布尔值',
  json: 'JSON 数据',
}

const pageCacheKey = (
  category: string,
  pagination: CategoryPagination,
) => `${category}:${pagination.page}:${pagination.pageSize}`

const parseInitialValue = (config: SystemConfig): EditableConfig => {
  const editable: EditableConfig = { ...config }
  switch (config.value_type) {
    case 'bool':
      try {
        editable.boolValue = Boolean(JSON.parse(config.value))
      } catch {
        editable.boolValue = config.value === 'true'
      }
      break
    case 'int':
      try {
        editable.intValue = Number(JSON.parse(config.value))
      } catch {
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

const sameSavedDraft = (
  current: EditableConfig,
  submitted: EditableConfig,
) =>
  current.value === submitted.value &&
  current.value_type === submitted.value_type &&
  current.description === submitted.description &&
  current.category === submitted.category &&
  current.group === submitted.group

const SystemSettings = () => {
  const notify = useNotify()
  const [activeTab, setActiveTab] = useState(categories[0].id)
  const [paginationByCategory, setPaginationByCategory] = useState<
    Record<string, CategoryPagination>
  >({})
  const [serverPages, setServerPages] = useState<
    Record<string, SystemConfigPage>
  >({})
  const [draftsByKey, setDraftsByKey] = useState<
    Record<string, EditableConfig>
  >({})
  const [loading, setLoading] = useState(false)
  const [listError, setListError] = useState('')
  const [savingKey, setSavingKey] = useState<string | null>(null)
  const [bulkSaving, setBulkSaving] = useState(false)
  const requestController = useRef<AbortController | null>(null)

  const currentPagination =
    paginationByCategory[activeTab] ?? defaultPagination
  const currentPageKey = pageCacheKey(activeTab, currentPagination)
  const currentServerPage = serverPages[currentPageKey]
  const dirtyCount = Object.keys(draftsByKey).length
  const blocker = useBlocker(({ currentLocation, nextLocation }) =>
    dirtyCount > 0 &&
    currentLocation.pathname !== nextLocation.pathname,
  )

  const currentConfigs = useMemo(
    () =>
      (currentServerPage?.items ?? []).map(
        (config) => draftsByKey[config.key] ?? parseInitialValue(config),
      ),
    [currentServerPage, draftsByKey],
  )

  const loadConfigs = useCallback(
    async (
      category: string,
      pagination: CategoryPagination,
    ) => {
      requestController.current?.abort()
      const controller = new AbortController()
      requestController.current = controller
      setLoading(true)
      setListError('')
      try {
        const query: ListPlatformConfigsOperationQuery = {
          category,
          page: pagination.page + 1,
          page_size: pagination.pageSize,
          sort_by: 'group',
          sort_order: 'asc',
        }
        const result = await apiFetch<SystemConfigPage>(
          humanApiRoutes.listPlatformConfigs(query),
          { signal: controller.signal },
        )
        if (controller.signal.aborted) return
        if (
          result.total_pages > 0 &&
          pagination.page + 1 > result.total_pages
        ) {
          setPaginationByCategory((previous) => ({
            ...previous,
            [category]: {
              ...pagination,
              page: result.total_pages - 1,
            },
          }))
          return
        }
        setServerPages((previous) => ({
          ...previous,
          [pageCacheKey(category, pagination)]: result,
        }))
      } catch (error) {
        if (controller.signal.aborted) return
        setListError(
          localizedUnknownErrorMessage(error, '加载配置失败，请稍后重试'),
        )
      } finally {
        if (
          !controller.signal.aborted &&
          requestController.current === controller
        ) {
          setLoading(false)
        }
      }
    },
    [],
  )

  useEffect(() => {
    void loadConfigs(activeTab, currentPagination)
    return () => requestController.current?.abort()
  }, [activeTab, currentPagination, loadConfigs])

  useEffect(() => {
    if (dirtyCount === 0) return
    const warnBeforeLeaving = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warnBeforeLeaving)
    return () =>
      window.removeEventListener('beforeunload', warnBeforeLeaving)
  }, [dirtyCount])

  const updatePagination = (next: CategoryPagination) => {
    setPaginationByCategory((previous) => ({
      ...previous,
      [activeTab]: next,
    }))
  }

  const handleValueChange = (
    source: EditableConfig,
    value: string | number | boolean,
  ) => {
    const config = { ...(draftsByKey[source.key] ?? source) }
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
    setDraftsByKey((previous) => ({
      ...previous,
      [config.key]: config,
    }))
  }

  const handleSave = async (
    config: EditableConfig,
    silent = false,
  ) => {
    setSavingKey(config.key)
    try {
      const payload: UpdateSystemConfigRequest = {
        value: config.value,
        value_type: config.value_type,
        description: config.description,
        category: config.category,
        group: config.group,
      }
      await apiFetch(
        humanApiRoutes.updatePlatformConfig({
          configKey: config.key,
        }),
        {
          method: 'PUT',
          body: JSON.stringify(payload),
        },
      )
      setServerPages((previous) => {
        const updated = { ...previous }
        for (const [key, page] of Object.entries(updated)) {
          if (!page.items.some((item) => item.key === config.key)) continue
          updated[key] = {
            ...page,
            items: page.items.map((item) =>
              item.key === config.key
                ? {
                    ...item,
                    value: config.value,
                    value_type: config.value_type,
                    description: config.description,
                    category: config.category,
                    group: config.group,
                  }
                : item,
            ),
          }
        }
        return updated
      })
      setDraftsByKey((previous) => {
        const current = previous[config.key]
        if (!current || !sameSavedDraft(current, config)) return previous
        const next = { ...previous }
        delete next[config.key]
        return next
      })
      if (!silent) {
        notify('配置已更新', { type: 'success' })
      }
      return true
    } catch (error) {
      if (!silent) {
        notify(
          localizedUnknownErrorMessage(error, '保存失败，请稍后重试'),
          { type: 'error' },
        )
      }
      return false
    } finally {
      setSavingKey(null)
    }
  }

  const bulkSave = async () => {
    const dirtyConfigs = Object.values(draftsByKey)
    if (dirtyConfigs.length === 0) return
    setBulkSaving(true)
    let failures = 0
    for (const config of dirtyConfigs) {
      if (!(await handleSave(config, true))) failures += 1
    }
    if (failures === 0) {
      notify(`已保存 ${dirtyConfigs.length} 项配置`, {
        type: 'success',
      })
    } else {
      notify(
        `${dirtyConfigs.length - failures} 项已保存，${failures} 项失败，请重试`,
        { type: 'warning' },
      )
    }
    setBulkSaving(false)
  }

  const handleRefresh = async () => {
    if (
      dirtyCount > 0 &&
      !window.confirm(
        `刷新将放弃全部 ${dirtyCount} 项未保存修改，确定继续吗？`,
      )
    ) {
      return
    }
    setDraftsByKey({})
    setServerPages((previous) => {
      const next = { ...previous }
      delete next[currentPageKey]
      return next
    })
    await loadConfigs(activeTab, currentPagination)
  }

  const renderValueCell = (config: EditableConfig) => {
    const accessibleName = `配置“${config.key}”的值`
    switch (config.value_type) {
      case 'bool':
        return (
          <Switch
            checked={config.boolValue ?? false}
            onChange={(event) =>
              handleValueChange(config, event.target.checked)
            }
            slotProps={{
              input: { 'aria-label': accessibleName },
            }}
          />
        )
      case 'int':
        return (
          <TextField
            type="number"
            size="small"
            value={config.intValue ?? ''}
            onChange={(event) =>
              handleValueChange(config, event.target.value)
            }
            sx={{ width: 160 }}
            slotProps={{
              htmlInput: { 'aria-label': accessibleName },
            }}
          />
        )
      case 'json':
        return (
          <TextField
            size="small"
            value={config.jsonValue ?? ''}
            onChange={(event) =>
              handleValueChange(config, event.target.value)
            }
            sx={{ width: 320 }}
            placeholder="请输入合法 JSON"
            slotProps={{
              htmlInput: { 'aria-label': accessibleName },
            }}
          />
        )
      default:
        return (
          <TextField
            size="small"
            value={config.value}
            onChange={(event) =>
              handleValueChange(config, event.target.value)
            }
            sx={{ width: 240 }}
            slotProps={{
              htmlInput: { 'aria-label': accessibleName },
            }}
          />
        )
    }
  }

  return (
    <PageShell
      title="平台公共配置"
      testId="system-settings-page-shell"
    >
      <PageHeader
        title="平台公共配置"
        description="仅管理平台级公共默认与安全基线，不修改当前项目的版本化配置。"
        leading={<BackButton fallbackPath="/system-settings" />}
        action={
          <Stack
            direction="row"
            spacing={1}
            useFlexGap
            sx={{ flexWrap: 'wrap' }}
          >
            <Button
              startIcon={<RefreshIcon />}
              disabled={loading}
              onClick={() => void handleRefresh()}
            >
              刷新
            </Button>
            <Button
              startIcon={<SaveIcon />}
              onClick={() => void bulkSave()}
              disabled={
                dirtyCount === 0 ||
                bulkSaving ||
                savingKey !== null
              }
              variant="contained"
            >
              {bulkSaving
                ? '保存中…'
                : `保存全部 (${dirtyCount})`}
            </Button>
          </Stack>
        }
      />
      <Paper sx={{ mb: 2 }}>
        <Tabs
          value={activeTab}
          onChange={(_, value: string) => setActiveTab(value)}
          textColor="primary"
          indicatorColor="primary"
          variant="scrollable"
        >
          {categories.map((category) => (
            <Tab
              key={category.id}
              value={category.id}
              label={category.label}
            />
          ))}
        </Tabs>
      </Paper>
      {listError && (
        <Alert
          severity="error"
          sx={{ mb: 2 }}
          action={
            <Button
              color="inherit"
              size="small"
              onClick={() =>
                void loadConfigs(activeTab, currentPagination)
              }
            >
              重试
            </Button>
          }
        >
          {listError}
        </Alert>
      )}
      {loading && !currentServerPage && (
        <Box
          role="status"
          sx={{ display: 'grid', minHeight: 240, placeItems: 'center' }}
        >
          <CircularProgress aria-label="正在加载系统配置" />
        </Box>
      )}
      {!loading && !listError && currentConfigs.length === 0 && (
        <Alert severity="info">当前分类暂无配置</Alert>
      )}
      {currentServerPage && (
        <Paper>
          <TableContainer>
            <ResizableMuiTable
              tableId="settings.system-config"
              columns={systemConfigColumns}
              size="small"
              aria-label="系统配置列表"
            >
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
                {currentConfigs.map((config) => {
                  const dirty = draftsByKey[config.key] !== undefined
                  return (
                    <TableRow
                      key={config.key}
                      hover
                      selected={dirty}
                    >
                      <TableCell>
                        <InlineDetails
                          primary={config.key}
                          secondary={`分组：${config.group || '默认'}`}
                          title={`${config.key} · 分组：${config.group || '默认'}`}
                        />
                      </TableCell>
                      <TableCell>
                        <InlineDetails
                          primary={config.description || '—'}
                          secondary={
                            config.default_value
                              ? `默认值：${config.default_value}`
                              : undefined
                          }
                          title={`${config.description || '—'}${
                            config.default_value
                              ? ` · 默认值：${config.default_value}`
                              : ''
                          }`}
                          primaryFontWeight={400}
                        />
                      </TableCell>
                      <TableCell>{renderValueCell(config)}</TableCell>
                      <TableCell>
                        <Tooltip
                          title={`类型代码：${config.value_type}`}
                        >
                          <span>
                            {valueTypeLabels[config.value_type]}
                          </span>
                        </Tooltip>
                      </TableCell>
                      <TableCell align="right">
                        <Tooltip title="保存">
                          <span>
                            <Button
                              size="small"
                              aria-label={`保存配置：${config.key}`}
                              startIcon={
                                <SaveIcon fontSize="inherit" />
                              }
                              onClick={() =>
                                void handleSave(config)
                              }
                              disabled={
                                !dirty ||
                                savingKey === config.key ||
                                bulkSaving
                              }
                            >
                              {savingKey === config.key
                                ? '保存中…'
                                : '保存'}
                            </Button>
                          </span>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </ResizableMuiTable>
          </TableContainer>
          <TablePagination
            component="div"
            count={currentServerPage.total}
            page={currentPagination.page}
            rowsPerPage={currentPagination.pageSize}
            rowsPerPageOptions={[25, 50, 100]}
            onPageChange={(_, page) =>
              updatePagination({
                ...currentPagination,
                page,
              })
            }
            onRowsPerPageChange={(event) =>
              updatePagination({
                page: 0,
                pageSize: Number(event.target.value),
              })
            }
            labelRowsPerPage="每页配置数"
            labelDisplayedRows={({ from, to, count }) =>
              `${from}–${to} / ${count}`
            }
            slotProps={{
              select: {
                inputProps: {
                  'aria-label': '系统配置每页数量',
                },
              },
            }}
          />
        </Paper>
      )}
      <Dialog
        open={blocker.state === 'blocked'}
        aria-labelledby="system-settings-leave-title"
      >
        <DialogTitle id="system-settings-leave-title">
          存在未保存的平台配置
        </DialogTitle>
        <DialogContent>
          当前有 {dirtyCount} 项修改尚未保存。离开将放弃这些修改，是否继续？
        </DialogContent>
        <DialogActions>
          <Button
            onClick={() => {
              if (blocker.state === 'blocked') blocker.reset()
            }}
          >
            继续编辑
          </Button>
          <Button
            color="warning"
            variant="contained"
            onClick={() => {
              if (blocker.state === 'blocked') {
                setDraftsByKey({})
                blocker.proceed()
              }
            }}
          >
            放弃修改并离开
          </Button>
        </DialogActions>
      </Dialog>
    </PageShell>
  )
}

export default SystemSettings
