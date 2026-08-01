import React, { useCallback, useEffect, useRef, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  MenuItem,
  Paper,
  Stack,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from '@mui/material'
import {
  ResizableMuiTable,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
  humanApiRoutes,
  type AutomationLog,
  type AutomationLogPage,
  type ListProjectAutomationLogsOperationQuery,
} from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import { projectScopeChangedEvent } from '@/lib/projectScopeEvents'
import AutomationTabs from './AutomationTabs'
import { automationTriggerEventLabel } from './triggerEvents'

type AutomationLogFilters = {
  ruleID: string
  ticketID: string
  success: '' | 'true' | 'false'
}

const emptyFilters: AutomationLogFilters = {
  ruleID: '',
  ticketID: '',
  success: '',
}

const maxAutomationCursorPages = 100

const automationLogColumns: ResizableColumn[] = [
  { key: 'id', defaultWidth: 88, minWidth: 72, maxWidth: 136 },
  { key: 'rule', defaultWidth: 220, minWidth: 160, maxWidth: 420 },
  { key: 'ticket', defaultWidth: 160, minWidth: 120, maxWidth: 260 },
  { key: 'result', defaultWidth: 104, minWidth: 88, maxWidth: 160 },
  { key: 'event', defaultWidth: 200, minWidth: 144, maxWidth: 360 },
  { key: 'executed_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
  { key: 'execution_time', defaultWidth: 132, minWidth: 104, maxWidth: 200 },
  { key: 'diagnostic', defaultWidth: 300, minWidth: 180, maxWidth: 520 },
  { key: 'action', defaultWidth: 104, minWidth: 88, maxWidth: 160, sticky: 'right' },
]

const positiveID = (value: string) => value === '' || /^[1-9]\d*$/.test(value)
const isAbortedRequest = (error: unknown, signal: AbortSignal) =>
  signal.aborted
  || (error instanceof DOMException && error.name === 'AbortError')

const AutomationLogList: React.FC = () => {
  const [items, setItems] = useState<AutomationLog[]>([])
  const [filters, setFilters] = useState<AutomationLogFilters>(emptyFilters)
  const [appliedFilters, setAppliedFilters] = useState<AutomationLogFilters>(emptyFilters)
  const [nextCursor, setNextCursor] = useState('')
  const [hasMore, setHasMore] = useState(false)
  const [pageIndex, setPageIndex] = useState(0)
  const [cursorHistory, setCursorHistory] = useState<string[]>([''])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filterError, setFilterError] = useState('')
  const [selected, setSelected] = useState<AutomationLog | null>(null)
  const [scopeVersion, setScopeVersion] = useState(0)
  const requestController = useRef<AbortController | null>(null)
  const requestSequence = useRef(0)
  const retryTarget = useRef({ cursor: '', pageIndex: 0 })

  const fetchLogs = useCallback(async (
    targetCursor = '',
    targetPageIndex = 0,
  ) => {
    if (
      targetPageIndex < 0
      || targetPageIndex >= maxAutomationCursorPages
    ) {
      setError('本次浏览已达到 100 页上限，请缩小筛选范围后继续')
      return
    }
    requestController.current?.abort()
    const controller = new AbortController()
    const sequence = requestSequence.current + 1
    requestSequence.current = sequence
    requestController.current = controller
    retryTarget.current = {
      cursor: targetCursor,
      pageIndex: targetPageIndex,
    }
    try {
      setLoading(true)
      setError('')
      const projectKey = await resolveActiveProjectKey()
      if (
        controller.signal.aborted
        || requestSequence.current !== sequence
      ) return
      const query: ListProjectAutomationLogsOperationQuery = {
        limit: 25,
        ...(targetCursor ? { cursor: targetCursor } : {}),
        ...(appliedFilters.ruleID
          ? { rule_id: Number(appliedFilters.ruleID) }
          : {}),
        ...(appliedFilters.ticketID
          ? { ticket_id: Number(appliedFilters.ticketID) }
          : {}),
        ...(appliedFilters.success
          ? { success: appliedFilters.success === 'true' }
          : {}),
      }
      const path = humanApiRoutes.listProjectAutomationLogs(
        { projectKey },
        query,
      )
      const data = await apiFetch<AutomationLogPage>(
        path,
        { signal: controller.signal },
      )
      if (
        controller.signal.aborted
        || requestSequence.current !== sequence
      ) return
      const continuation = data.next_cursor ?? ''
      setItems(data.items ?? [])
      setNextCursor(continuation)
      setHasMore(Boolean(data.has_more))
      setPageIndex(targetPageIndex)
      setCursorHistory((previous) => {
        const history = targetPageIndex === 0
          ? ['']
          : previous.slice(0, targetPageIndex + 1)
        if (
          data.has_more
          && continuation
          && targetPageIndex + 1 < maxAutomationCursorPages
        ) {
          history[targetPageIndex + 1] = continuation
        }
        return history.slice(0, maxAutomationCursorPages)
      })
      setSelected(null)
    } catch (fetchError: unknown) {
      if (
        isAbortedRequest(fetchError, controller.signal)
        || requestSequence.current !== sequence
      ) return
      setError(localizedUnknownErrorMessage(
        fetchError,
        '加载自动化执行日志失败',
      ))
    } finally {
      if (
        !controller.signal.aborted
        && requestSequence.current === sequence
      ) {
        setLoading(false)
      }
    }
  }, [appliedFilters])

  useEffect(() => {
    setItems([])
    setNextCursor('')
    setHasMore(false)
    setPageIndex(0)
    setCursorHistory([''])
    setSelected(null)
    void fetchLogs('', 0)
    return () => requestController.current?.abort()
  }, [appliedFilters, fetchLogs, scopeVersion])

  useEffect(() => {
    const handleProjectScopeChanged = () => {
      requestController.current?.abort()
      requestSequence.current += 1
      setScopeVersion((version) => version + 1)
    }
    window.addEventListener(projectScopeChangedEvent, handleProjectScopeChanged)
    return () => {
      requestController.current?.abort()
      requestSequence.current += 1
      window.removeEventListener(
        projectScopeChangedEvent,
        handleProjectScopeChanged,
      )
    }
  }, [])

  const applyFilters = (event: React.FormEvent) => {
    event.preventDefault()
    if (!positiveID(filters.ruleID) || !positiveID(filters.ticketID)) {
      setFilterError('规则 ID 和工单 ID 必须是正整数')
      return
    }
    setFilterError('')
    setAppliedFilters({ ...filters })
  }

  const clearFilters = () => {
    setFilters(emptyFilters)
    setFilterError('')
    setAppliedFilters({ ...emptyFilters })
  }

  return (
    <Box>
      <AutomationTabs />
      <Paper
        component="form"
        variant="outlined"
        onSubmit={applyFilters}
        sx={{ p: 2, mb: 2 }}
      >
        <Stack direction={{ xs: 'column', md: 'row' }} spacing={1}>
          <TextField
            size="small"
            label="规则 ID"
            value={filters.ruleID}
            slotProps={{ htmlInput: { inputMode: 'numeric' } }}
            onChange={(event) => setFilters((previous) => ({
              ...previous,
              ruleID: event.target.value,
            }))}
          />
          <TextField
            size="small"
            label="工单 ID"
            value={filters.ticketID}
            slotProps={{ htmlInput: { inputMode: 'numeric' } }}
            onChange={(event) => setFilters((previous) => ({
              ...previous,
              ticketID: event.target.value,
            }))}
          />
          <TextField
            select
            size="small"
            label="执行结果"
            value={filters.success}
            sx={{ minWidth: 140 }}
            onChange={(event) => setFilters((previous) => ({
              ...previous,
              success: event.target.value as AutomationLogFilters['success'],
            }))}
          >
            <MenuItem value="">全部结果</MenuItem>
            <MenuItem value="true">成功</MenuItem>
            <MenuItem value="false">失败</MenuItem>
          </TextField>
          <Button type="submit" variant="contained">应用筛选</Button>
          <Button type="button" onClick={clearFilters}>清除</Button>
        </Stack>
        {filterError && (
          <Typography color="error" variant="body2" sx={{ mt: 1 }}>
            {filterError}
          </Typography>
        )}
      </Paper>

      {error && (
        <Alert
          severity="error"
          sx={{ mb: 2 }}
          action={(
            <Button
              color="inherit"
              size="small"
              disabled={loading}
              onClick={() => void fetchLogs(
                retryTarget.current.cursor,
                retryTarget.current.pageIndex,
              )}
            >
              重试
            </Button>
          )}
        >
          {error}
        </Alert>
      )}

      <TableContainer component={Paper}>
        <ResizableMuiTable
          tableId="automation.logs.cursor"
          columns={automationLogColumns}
          size="small"
          aria-label="自动化日志时间线"
        >
          <TableHead>
            <TableRow>
              <TableCell>ID</TableCell>
              <TableCell>规则</TableCell>
              <TableCell>工单</TableCell>
              <TableCell>结果</TableCell>
              <TableCell>触发事件</TableCell>
              <TableCell>执行时间</TableCell>
              <TableCell>耗时（毫秒）</TableCell>
              <TableCell>诊断摘要</TableCell>
              <TableCell align="right">操作</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id} hover>
                <TableCell>{item.id}</TableCell>
                <TableCell>
                  <TruncatedText title={item.rule?.name || `规则 #${item.rule_id}`}>
                    {item.rule?.name || `规则 #${item.rule_id}`}
                  </TruncatedText>
                </TableCell>
                <TableCell>
                  <TruncatedText
                    title={item.ticket?.ticket_number || `工单 #${item.ticket_id}`}
                  >
                    {item.ticket?.ticket_number || `工单 #${item.ticket_id}`}
                  </TruncatedText>
                </TableCell>
                <TableCell>
                  <Chip
                    size="small"
                    label={item.success ? '成功' : '失败'}
                    color={item.success ? 'success' : 'error'}
                  />
                </TableCell>
                <TableCell>
                  <TruncatedText title={item.trigger_event}>
                    {automationTriggerEventLabel(item.trigger_event)}
                  </TruncatedText>
                </TableCell>
                <TableCell>
                  {new Date(item.executed_at).toLocaleString('zh-CN')}
                </TableCell>
                <TableCell>{item.execution_time}</TableCell>
                <TableCell>
                  <TruncatedText title={item.error_message || '执行成功'}>
                    {item.error_message || '执行成功'}
                  </TruncatedText>
                </TableCell>
                <TableCell align="right" className="cd-table-sticky-right">
                  <Button size="small" onClick={() => setSelected(item)}>
                    查看
                  </Button>
                </TableCell>
              </TableRow>
            ))}
            {!loading && items.length === 0 && !error && (
              <TableRow>
                <TableCell colSpan={9} align="center">
                  暂无自动化执行日志
                </TableCell>
              </TableRow>
            )}
            {loading && items.length === 0 && (
              <TableRow>
                <TableCell colSpan={9} align="center">
                  <CircularProgress size={24} aria-label="正在加载自动化日志" />
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </ResizableMuiTable>
      </TableContainer>

      {(pageIndex > 0 || hasMore) && (
        <Stack
          direction="row"
          spacing={1}
          sx={{ mt: 2, alignItems: 'center', justifyContent: 'center' }}
        >
          <Button
            variant="outlined"
            disabled={loading || pageIndex === 0}
            onClick={() => void fetchLogs(
              cursorHistory[pageIndex - 1] ?? '',
              pageIndex - 1,
            )}
          >
            上一页
          </Button>
          <Typography variant="body2" color="text.secondary">
            第 {pageIndex + 1} 页
          </Typography>
          <Button
            variant="outlined"
            disabled={
              loading
              || !hasMore
              || !nextCursor
              || pageIndex >= maxAutomationCursorPages - 1
            }
            onClick={() => void fetchLogs(nextCursor, pageIndex + 1)}
          >
            {loading ? '加载中…' : '下一页'}
          </Button>
        </Stack>
      )}
      {hasMore && pageIndex >= maxAutomationCursorPages - 1 && (
        <Alert severity="info" sx={{ mt: 2 }}>
          本次浏览已达到 100 页上限，请使用规则、工单或执行结果筛选缩小范围。
        </Alert>
      )}

      {selected && (
        <Paper variant="outlined" sx={{ p: 2, mt: 2 }} aria-live="polite">
          <Typography component="h3" variant="subtitle1" gutterBottom>
            执行详情 #{selected.id}
          </Typography>
          <Stack spacing={0.5}>
            <Typography variant="body2">
              规则：{selected.rule?.name || `#${selected.rule_id}`}
            </Typography>
            <Typography variant="body2">
              工单：{selected.ticket?.ticket_number || `#${selected.ticket_id}`}
            </Typography>
            <Typography variant="body2">
              事件：{selected.trigger_event}
            </Typography>
            <Typography variant="body2">
              诊断：{selected.error_message || '无'}
            </Typography>
          </Stack>
        </Paper>
      )}
    </Box>
  )
}

export default AutomationLogList
