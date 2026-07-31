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
import { humanApiRoutes } from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import AutomationTabs from './AutomationTabs'
import { automationTriggerEventLabel } from './triggerEvents'

type AutomationLogItem = {
  id: number
  created_at: string
  rule_id: number
  rule?: {
    id: number
    name: string
  }
  ticket_id: number
  ticket?: {
    id: number
    ticket_number: string
    title: string
    status: string
  }
  trigger_event: string
  executed_at: string
  success: boolean
  error_message?: string
  execution_time: number
}

type AutomationLogCursorPage = {
  items: AutomationLogItem[]
  next_cursor: string
  has_more: boolean
}

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

const AutomationLogList: React.FC = () => {
  const [items, setItems] = useState<AutomationLogItem[]>([])
  const [filters, setFilters] = useState<AutomationLogFilters>(emptyFilters)
  const [appliedFilters, setAppliedFilters] = useState<AutomationLogFilters>(emptyFilters)
  const [cursor, setCursor] = useState('')
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [filterError, setFilterError] = useState('')
  const [selected, setSelected] = useState<AutomationLogItem | null>(null)
  const lastAutomaticQuery = useRef('')

  const fetchLogs = useCallback(async (nextCursor = '', append = false) => {
    try {
      setLoading(true)
      setError('')
      const projectKey = await resolveActiveProjectKey()
      const parameters = new URLSearchParams({ limit: '25' })
      if (nextCursor) parameters.set('cursor', nextCursor)
      if (appliedFilters.ruleID) parameters.set('rule_id', appliedFilters.ruleID)
      if (appliedFilters.ticketID) parameters.set('ticket_id', appliedFilters.ticketID)
      if (appliedFilters.success) parameters.set('success', appliedFilters.success)
      const basePath = humanApiRoutes.listProjectAutomationLogs({ projectKey }, {})
      const data = await apiFetch<AutomationLogCursorPage>(
        `${basePath}?${parameters.toString()}`,
      )
      setItems((previous) => append
        ? [...previous, ...(data.items ?? [])]
        : (data.items ?? []))
      setCursor(data.next_cursor ?? '')
      setHasMore(Boolean(data.has_more))
      if (!append) setSelected(null)
    } catch (fetchError: unknown) {
      setError(localizedUnknownErrorMessage(
        fetchError,
        '加载自动化执行日志失败',
      ))
    } finally {
      setLoading(false)
    }
  }, [appliedFilters])

  useEffect(() => {
    const queryKey = JSON.stringify(appliedFilters)
    if (lastAutomaticQuery.current === queryKey) return
    lastAutomaticQuery.current = queryKey
    void fetchLogs()
  }, [appliedFilters, fetchLogs])

  const applyFilters = (event: React.FormEvent) => {
    event.preventDefault()
    if (!positiveID(filters.ruleID) || !positiveID(filters.ticketID)) {
      setFilterError('规则 ID 和工单 ID 必须是正整数')
      return
    }
    setFilterError('')
    setAppliedFilters(filters)
  }

  const clearFilters = () => {
    setFilters(emptyFilters)
    setFilterError('')
    setAppliedFilters(emptyFilters)
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
            <Button color="inherit" size="small" onClick={() => void fetchLogs()}>
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

      {hasMore && (
        <Stack sx={{ mt: 2, alignItems: 'center' }}>
          <Button
            variant="outlined"
            disabled={loading}
            onClick={() => void fetchLogs(cursor, true)}
          >
            {loading ? '加载中…' : '加载更多'}
          </Button>
        </Stack>
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
