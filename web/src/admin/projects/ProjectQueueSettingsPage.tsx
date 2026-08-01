import React from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Paper,
  Stack,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TablePagination,
  TableRow,
  Typography,
} from '@mui/material'
import { Refresh as RefreshIcon } from '@mui/icons-material'
import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'
import {
  ResizableMuiTable,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
  apiFetch,
  localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
  humanApiRoutes,
  type ProjectQueue,
  type ProjectQueuePage,
} from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'

const queueColumns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 240, minWidth: 180, maxWidth: 400 },
  { key: 'key', defaultWidth: 180, minWidth: 140, maxWidth: 280 },
  { key: 'team', defaultWidth: 240, minWidth: 160, maxWidth: 400 },
  { key: 'default', defaultWidth: 128, minWidth: 104, maxWidth: 180 },
  { key: 'status', defaultWidth: 128, minWidth: 104, maxWidth: 180 },
  { key: 'description', defaultWidth: 420, minWidth: 240, maxWidth: 720 },
]

type QueueLoadState =
  | { key: string; status: 'loading'; data: null; error: '' }
  | { key: string; status: 'success'; data: ProjectQueuePage; error: '' }
  | { key: string; status: 'error'; data: null; error: string }

const ProjectQueueSettingsPage: React.FC = () => {
  const [page, setPage] = React.useState(0)
  const [pageSize, setPageSize] = React.useState(25)
  const [reloadToken, setReloadToken] = React.useState(0)
  const requestKey = `${page}:${pageSize}:${reloadToken}`
  const [state, setState] = React.useState<QueueLoadState | null>(null)

  React.useEffect(() => {
    const controller = new AbortController()
    setState({ key: requestKey, status: 'loading', data: null, error: '' })
    void resolveActiveProjectKey()
      .then((projectKey) =>
        apiFetch<ProjectQueuePage>(
          humanApiRoutes.listProjectQueues(
            { projectKey },
            {
              page: page + 1,
              page_size: pageSize,
              sort_by: 'is_default',
              sort_order: 'desc',
            },
          ),
          { signal: controller.signal },
        ),
      )
      .then((data) => {
        if (!controller.signal.aborted) {
          if (data.total_pages > 0 && page + 1 > data.total_pages) {
            setPage(data.total_pages - 1)
            return
          }
          setState({ key: requestKey, status: 'success', data, error: '' })
        }
      })
      .catch((error: unknown) => {
        if (
          controller.signal.aborted ||
          (error instanceof DOMException && error.name === 'AbortError')
        ) return
        setState({
          key: requestKey,
          status: 'error',
          data: null,
          error: localizedUnknownErrorMessage(
            error,
            '项目队列加载失败，请稍后重试',
          ),
        })
      })
    return () => controller.abort()
  }, [page, pageSize, reloadToken, requestKey])

  const activeState = state?.key === requestKey ? state : null
  const data = activeState?.status === 'success' ? activeState.data : null
  const loading = activeState === null || activeState.status === 'loading'
  const error = activeState?.status === 'error' ? activeState.error : ''

  return (
    <PageShell title="受理队列" testId="project-queue-settings-page">
      <Stack spacing={2}>
        <PageHeader
          title="受理队列"
          description="查看当前项目的有效受理队列、默认队列和团队绑定；当前版本不在此页面编辑高级路由规则。"
          action={(
            <Button
              startIcon={<RefreshIcon />}
              disabled={loading}
              onClick={() => setReloadToken((current) => current + 1)}
            >
              刷新
            </Button>
          )}
        />

        {error && (
          <Alert
            severity="error"
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

        {loading ? (
          <Box
            role="status"
            aria-label="正在加载项目队列"
            sx={{ display: 'grid', minHeight: 260, placeItems: 'center' }}
          >
            <CircularProgress />
          </Box>
        ) : data && data.items.length === 0 ? (
          <Alert severity="info">当前项目暂无有效队列。</Alert>
        ) : data ? (
          <Paper variant="outlined">
            <TableContainer>
              <ResizableMuiTable
                tableId="project-settings.queues"
                columns={queueColumns}
                size="small"
                aria-label="项目队列列表"
              >
                <TableHead>
                  <TableRow>
                    <TableCell>队列</TableCell>
                    <TableCell>Key</TableCell>
                    <TableCell>绑定团队</TableCell>
                    <TableCell>默认队列</TableCell>
                    <TableCell>状态</TableCell>
                    <TableCell>说明</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {data.items.map((queue: ProjectQueue) => (
                    <TableRow key={queue.public_id} hover>
                      <TableCell><Typography noWrap>{queue.name}</Typography></TableCell>
                      <TableCell><Typography noWrap>{queue.key}</Typography></TableCell>
                      <TableCell>
                        <Typography noWrap title={queue.team_name}>
                          {queue.team_name || '未绑定团队'}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        {queue.is_default
                          ? <Chip size="small" color="primary" label="默认" />
                          : '—'}
                      </TableCell>
                      <TableCell>
                        <Chip
                          size="small"
                          color={queue.status === 'active' ? 'success' : 'default'}
                          label={queue.status === 'active' ? '启用' : '已归档'}
                        />
                      </TableCell>
                      <TableCell>
                        <Typography noWrap title={queue.description}>
                          {queue.description || '—'}
                        </Typography>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </ResizableMuiTable>
            </TableContainer>
            <TablePagination
              component="div"
              count={data.total}
              page={page}
              rowsPerPage={pageSize}
              rowsPerPageOptions={[25, 50, 100]}
              onPageChange={(_, nextPage) => setPage(nextPage)}
              onRowsPerPageChange={(event) => {
                setPageSize(Number(event.target.value))
                setPage(0)
              }}
              labelRowsPerPage="每页数量"
              labelDisplayedRows={({ from, to, count }) =>
                `${from}–${to} / ${count}`
              }
              slotProps={{
                select: {
                  inputProps: { 'aria-label': '项目队列每页数量' },
                },
              }}
            />
          </Paper>
        ) : null}
      </Stack>
    </PageShell>
  )
}

export default ProjectQueueSettingsPage
