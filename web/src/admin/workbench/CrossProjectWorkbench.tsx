import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Title, useNotify } from 'react-admin'
import { useNavigate } from 'react-router-dom'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Paper,
  Stack,
  Tab,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TablePagination,
  TableRow,
  Tabs,
  Typography,
  type ChipProps,
} from '@mui/material'
import {
  ArrowForward as ArrowForwardIcon,
  DashboardCustomize as WorkbenchIcon,
  Refresh as RefreshIcon,
} from '@mui/icons-material'
import {
  InlineDetails,
  ResizableMuiTable,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
  apiFetch,
  localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
  loadAuthorizedProjects,
  setActiveProjectKey,
} from '@/lib/projectScope'
import { humanApiRoutes } from '@/lib/generated/human-api'
import type {
  CrossProjectTicketPriority,
  CrossProjectTicketStatus,
  CrossProjectWorkbenchPage,
  CrossProjectWorkbenchTicket,
  CrossProjectWorkbenchView,
} from '@/lib/types/crossProjectWorkbench'

const viewOptions: Array<{
  value: CrossProjectWorkbenchView
  label: string
  description: string
}> = [
  {
    value: 'todo',
    label: '我的待办',
    description: '分派给我且仍需处理的工单',
  },
  {
    value: 'created',
    label: '我创建的',
    description: '由我发起的全部工单',
  },
  {
    value: 'assigned',
    label: '分派给我的',
    description: '当前及历史上仍分派给我的工单',
  },
]

const columns: ResizableColumn[] = [
  { key: 'project', defaultWidth: 220, minWidth: 168, maxWidth: 360 },
  { key: 'ticket', defaultWidth: 360, minWidth: 220, maxWidth: 600 },
  { key: 'status', defaultWidth: 112, minWidth: 92, maxWidth: 180 },
  { key: 'priority', defaultWidth: 112, minWidth: 92, maxWidth: 180 },
  { key: 'assignee', defaultWidth: 168, minWidth: 120, maxWidth: 280 },
  { key: 'due', defaultWidth: 176, minWidth: 144, maxWidth: 260 },
  { key: 'updated', defaultWidth: 176, minWidth: 144, maxWidth: 260 },
  { key: 'actions', defaultWidth: 156, minWidth: 136, maxWidth: 220, sticky: 'right' },
]

const statusLabels: Record<CrossProjectTicketStatus, string> = {
  open: '待处理',
  in_progress: '处理中',
  pending: '等待中',
  resolved: '已解决',
  closed: '已关闭',
  cancelled: '已取消',
}

const statusColors: Record<CrossProjectTicketStatus, ChipProps['color']> = {
  open: 'warning',
  in_progress: 'primary',
  pending: 'info',
  resolved: 'success',
  closed: 'default',
  cancelled: 'default',
}

const priorityLabels: Record<CrossProjectTicketPriority, string> = {
  low: '低',
  normal: '普通',
  high: '高',
  urgent: '紧急',
  critical: '严重',
}

const priorityColors: Record<CrossProjectTicketPriority, ChipProps['color']> = {
  low: 'default',
  normal: 'info',
  high: 'warning',
  urgent: 'error',
  critical: 'error',
}

const dateFormatter = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

const formatDateTime = (value?: string) => {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '—'
  return dateFormatter.format(parsed)
}

const CrossProjectWorkbench: React.FC = () => {
  const navigate = useNavigate()
  const notify = useNotify()
  const [view, setView] = useState<CrossProjectWorkbenchView>('todo')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [result, setResult] = useState<CrossProjectWorkbenchPage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [reloadToken, setReloadToken] = useState(0)

  const selectedView = useMemo(
    () => viewOptions.find((option) => option.value === view) ?? viewOptions[0],
    [view],
  )

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    void apiFetch<CrossProjectWorkbenchPage>(
      humanApiRoutes.listCrossProjectWorkbenchTickets({
        view,
        page,
        page_size: pageSize,
      }),
      { signal: controller.signal },
    )
      .then((response) => {
        setResult(response)
      })
      .catch((requestError: unknown) => {
        if (
          controller.signal.aborted ||
          (requestError instanceof DOMException && requestError.name === 'AbortError')
        ) {
          return
        }
        setError(
          localizedUnknownErrorMessage(
            requestError,
            '跨项目工作台加载失败，请稍后重试',
          ),
        )
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [page, pageSize, reloadToken, view])

  const openTicket = useCallback(async (
    ticket: CrossProjectWorkbenchTicket,
  ) => {
    try {
      const projects = await loadAuthorizedProjects(true)
      const authorized = projects.find(
        ({ project }) => project.key === ticket.project_key,
      )
      if (!authorized) {
        throw new Error('该项目授权已变化，请刷新工作台')
      }
      setActiveProjectKey(ticket.project_key, projects)
      navigate(`/tickets/${ticket.id}/show`)
    } catch (navigationError: unknown) {
      notify(
        localizedUnknownErrorMessage(
          navigationError,
          '无法进入该项目工单，请刷新后重试',
        ),
        { type: 'warning' },
      )
    }
  }, [navigate, notify])

  return (
    <>
      <Title title="我的跨项目工作台" />
      <Box sx={{ p: { xs: 2, md: 3 }, minWidth: 0 }}>
        <Stack
          direction={{ xs: 'column', md: 'row' }}
          spacing={2}
          sx={{
            alignItems: { xs: 'stretch', md: 'center' },
            justifyContent: 'space-between',
            mb: 2,
          }}
        >
          <Box>
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <WorkbenchIcon color="primary" />
              <Typography variant="h4">我的跨项目工作台</Typography>
            </Stack>
            <Typography color="text.secondary" sx={{ mt: 0.5 }}>
              仅汇总你明确加入的项目；每条工单都标明项目来源。
            </Typography>
          </Box>
          <Button
            startIcon={<RefreshIcon />}
            onClick={() => setReloadToken((current) => current + 1)}
            disabled={loading}
          >
            刷新
          </Button>
        </Stack>

        <Paper variant="outlined" sx={{ mb: 2 }}>
          <Tabs
            value={view}
            onChange={(_, nextView: CrossProjectWorkbenchView) => {
              setView(nextView)
              setPage(1)
            }}
            aria-label="跨项目工作台视图"
            variant="scrollable"
            scrollButtons="auto"
          >
            {viewOptions.map((option) => (
              <Tab
                key={option.value}
                value={option.value}
                label={option.label}
              />
            ))}
          </Tabs>
        </Paper>

        <Typography color="text.secondary" sx={{ mb: 1.5 }}>
          {selectedView.description}
        </Typography>

        {error && (
          <Alert
            severity="error"
            sx={{ mb: 2 }}
            action={(
              <Button
                color="inherit"
                size="small"
                onClick={() => setReloadToken((current) => current + 1)}
              >
                重试
              </Button>
            )}
          >
            {error}
          </Alert>
        )}

        <Paper variant="outlined">
          {loading && !result ? (
            <Box
              role="status"
              aria-label="正在加载跨项目工单"
              sx={{ display: 'grid', minHeight: 280, placeItems: 'center' }}
            >
              <CircularProgress size={32} />
            </Box>
          ) : (
            <>
              <TableContainer sx={{ width: '100%', overflowX: 'auto' }}>
                <ResizableMuiTable
                  tableId={`workbench.cross-project.${view}`}
                  columns={columns}
                  size="small"
                  aria-label={`${selectedView.label}工单列表`}
                >
                  <TableHead>
                    <TableRow>
                      <TableCell>项目来源</TableCell>
                      <TableCell>工单</TableCell>
                      <TableCell>状态</TableCell>
                      <TableCell>优先级</TableCell>
                      <TableCell>处理人</TableCell>
                      <TableCell>截止时间</TableCell>
                      <TableCell>最近更新</TableCell>
                      <TableCell>操作</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {(result?.items ?? []).map((ticket) => (
                      <TableRow key={`${ticket.project_id}:${ticket.id}`} hover>
                        <TableCell>
                          <InlineDetails
                            primary={ticket.project_name}
                            secondary={ticket.project_key}
                            title={`${ticket.project_name}（${ticket.project_key}）`}
                          />
                        </TableCell>
                        <TableCell>
                          <InlineDetails
                            primary={ticket.title}
                            secondary={ticket.ticket_number}
                            title={`${ticket.ticket_number} · ${ticket.title}`}
                          />
                        </TableCell>
                        <TableCell>
                          <Chip
                            size="small"
                            color={statusColors[ticket.status]}
                            label={statusLabels[ticket.status]}
                          />
                        </TableCell>
                        <TableCell>
                          <Chip
                            size="small"
                            variant="outlined"
                            color={priorityColors[ticket.priority]}
                            label={priorityLabels[ticket.priority]}
                          />
                        </TableCell>
                        <TableCell>
                          <Typography variant="body2" noWrap>
                            {ticket.assigned_to_name || '未分派'}
                          </Typography>
                        </TableCell>
                        <TableCell>
                          <Typography
                            variant="body2"
                            color={ticket.sla_breached ? 'error.main' : 'text.primary'}
                            noWrap
                          >
                            {formatDateTime(ticket.sla_due_date ?? ticket.due_date)}
                          </Typography>
                        </TableCell>
                        <TableCell>
                          <Typography variant="body2" noWrap>
                            {formatDateTime(ticket.updated_at)}
                          </Typography>
                        </TableCell>
                        <TableCell>
                          <Button
                            size="small"
                            endIcon={<ArrowForwardIcon />}
                            onClick={() => void openTicket(ticket)}
                            aria-label={`进入 ${ticket.project_name} 的工单 ${ticket.ticket_number}`}
                          >
                            进入项目
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                    {!loading && (result?.items.length ?? 0) === 0 && (
                      <TableRow>
                        <TableCell colSpan={columns.length} align="center">
                          <Typography color="text.secondary" sx={{ py: 5 }}>
                            当前视图暂无工单
                          </Typography>
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </ResizableMuiTable>
              </TableContainer>
              <TablePagination
                component="div"
                count={result?.total ?? 0}
                page={Math.max(0, (result?.page ?? page) - 1)}
                rowsPerPage={result?.page_size ?? pageSize}
                rowsPerPageOptions={[10, 20, 50, 100]}
                onPageChange={(_, nextPage) => setPage(nextPage + 1)}
                onRowsPerPageChange={(event) => {
                  setPageSize(Number(event.target.value))
                  setPage(1)
                }}
                labelRowsPerPage="每页"
                labelDisplayedRows={({ from, to, count }) =>
                  `${from}-${to} / ${count === -1 ? '更多' : count}`
                }
              />
            </>
          )}
        </Paper>
      </Box>
    </>
  )
}

export default CrossProjectWorkbench
