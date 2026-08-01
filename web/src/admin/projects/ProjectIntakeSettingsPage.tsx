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
  type ProjectIntakeConfiguration,
  type WorkClass,
} from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'

const requestTypeColumns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 240, minWidth: 180, maxWidth: 380 },
  { key: 'key', defaultWidth: 180, minWidth: 140, maxWidth: 280 },
  { key: 'class', defaultWidth: 140, minWidth: 112, maxWidth: 220 },
  { key: 'version', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
  { key: 'published', defaultWidth: 190, minWidth: 150, maxWidth: 260 },
  { key: 'description', defaultWidth: 360, minWidth: 220, maxWidth: 620 },
]

const workflowColumns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 240, minWidth: 180, maxWidth: 380 },
  { key: 'key', defaultWidth: 180, minWidth: 140, maxWidth: 280 },
  { key: 'version', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
  { key: 'published', defaultWidth: 190, minWidth: 150, maxWidth: 260 },
  { key: 'description', defaultWidth: 420, minWidth: 240, maxWidth: 720 },
]

const workClassLabels: Record<WorkClass, string> = {
  incident: '事件',
  request: '服务请求',
  problem: '问题',
  change: '变更',
  complaint: '投诉',
  consultation: '咨询',
}

const formatDateTime = (value?: string): string =>
  value
    ? new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'short',
      }).format(new Date(value))
    : '—'

type LoadState =
  | { status: 'loading'; data: null; error: '' }
  | { status: 'success'; data: ProjectIntakeConfiguration; error: '' }
  | { status: 'error'; data: null; error: string }

const ProjectIntakeSettingsPage: React.FC = () => {
  const [reloadToken, setReloadToken] = React.useState(0)
  const [state, setState] = React.useState<LoadState>({
    status: 'loading',
    data: null,
    error: '',
  })

  React.useEffect(() => {
    const controller = new AbortController()
    setState({ status: 'loading', data: null, error: '' })
    void resolveActiveProjectKey()
      .then((projectKey) =>
        apiFetch<ProjectIntakeConfiguration>(
          humanApiRoutes.getProjectIntakeConfiguration({ projectKey }),
          { signal: controller.signal },
        ),
      )
      .then((data) => {
        if (!controller.signal.aborted) {
          setState({ status: 'success', data, error: '' })
        }
      })
      .catch((error: unknown) => {
        if (
          controller.signal.aborted ||
          (error instanceof DOMException && error.name === 'AbortError')
        ) return
        setState({
          status: 'error',
          data: null,
          error: localizedUnknownErrorMessage(
            error,
            '当前项目建单配置加载失败，请稍后重试',
          ),
        })
      })
    return () => controller.abort()
  }, [reloadToken])

  const configuration = state.status === 'success' ? state.data : null

  return (
    <PageShell title="建单配置" testId="project-intake-settings-page">
      <Stack spacing={2}>
        <PageHeader
          title="建单配置"
          description="查看当前项目已经发布并真正用于创建工单的请求类型与工作流。草稿不会影响运行中的工单。"
          action={(
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <Chip size="small" variant="outlined" label="只读 · 当前发布" />
              <Button
                startIcon={<RefreshIcon />}
                disabled={state.status === 'loading'}
                onClick={() => setReloadToken((current) => current + 1)}
              >
                刷新
              </Button>
            </Stack>
          )}
        />

        {state.status === 'loading' && (
          <Box
            role="status"
            aria-label="正在加载建单配置"
            sx={{ display: 'grid', minHeight: 260, placeItems: 'center' }}
          >
            <CircularProgress />
          </Box>
        )}
        {state.status === 'error' && (
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
            {state.error}
          </Alert>
        )}

        {configuration && (
          <>
            <Paper variant="outlined" sx={{ p: 2 }}>
              <Stack
                direction={{ xs: 'column', sm: 'row' }}
                spacing={1}
                useFlexGap
                sx={{ alignItems: { sm: 'center' }, flexWrap: 'wrap' }}
              >
                <Typography variant="h6">当前发布版本</Typography>
                <Chip
                  size="small"
                  color="success"
                  label={`Release v${configuration.release_version}`}
                />
                <Typography
                  variant="body2"
                  color="text.secondary"
                  sx={{ minWidth: 0, overflowWrap: 'anywhere' }}
                >
                  {configuration.release_id}
                </Typography>
              </Stack>
            </Paper>

            <Paper variant="outlined">
              <Box sx={{ p: 2 }}>
                <Typography variant="h6">请求类型</Typography>
                <Typography variant="body2" color="text.secondary">
                  已发布 {configuration.request_types.length} 项；创建工单时按所选类型校验扩展字段。
                </Typography>
              </Box>
              {configuration.request_types.length === 0 ? (
                <Alert severity="warning" sx={{ m: 2, mt: 0 }}>
                  当前发布版本没有可用请求类型，用户将无法完成标准建单。
                </Alert>
              ) : (
                <TableContainer>
                  <ResizableMuiTable
                    tableId="project-settings.intake.request-types"
                    columns={requestTypeColumns}
                    size="small"
                    aria-label="已发布请求类型"
                  >
                    <TableHead>
                      <TableRow>
                        <TableCell>名称</TableCell>
                        <TableCell>Key</TableCell>
                        <TableCell>业务类型</TableCell>
                        <TableCell>版本</TableCell>
                        <TableCell>发布时间</TableCell>
                        <TableCell>说明</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {configuration.request_types.map((requestType) => (
                        <TableRow key={requestType.id} hover>
                          <TableCell><Typography noWrap>{requestType.name}</Typography></TableCell>
                          <TableCell><Typography noWrap>{requestType.key}</Typography></TableCell>
                          <TableCell>{workClassLabels[requestType.work_class]}</TableCell>
                          <TableCell>v{requestType.version}</TableCell>
                          <TableCell>{formatDateTime(requestType.published_at)}</TableCell>
                          <TableCell>
                            <Typography noWrap title={requestType.description}>
                              {requestType.description || '—'}
                            </Typography>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </ResizableMuiTable>
                </TableContainer>
              )}
            </Paper>

            <Paper variant="outlined">
              <Box sx={{ p: 2 }}>
                <Typography variant="h6">工作流</Typography>
                <Typography variant="body2" color="text.secondary">
                  已发布 {configuration.workflows.length} 项；状态和流转规则由版本快照固定。
                </Typography>
              </Box>
              {configuration.workflows.length === 0 ? (
                <Alert severity="warning" sx={{ m: 2, mt: 0 }}>
                  当前发布版本没有可用工作流。
                </Alert>
              ) : (
                <TableContainer>
                  <ResizableMuiTable
                    tableId="project-settings.intake.workflows"
                    columns={workflowColumns}
                    size="small"
                    aria-label="已发布工作流"
                  >
                    <TableHead>
                      <TableRow>
                        <TableCell>名称</TableCell>
                        <TableCell>Key</TableCell>
                        <TableCell>版本</TableCell>
                        <TableCell>发布时间</TableCell>
                        <TableCell>说明</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {configuration.workflows.map((workflow) => (
                        <TableRow key={workflow.id} hover>
                          <TableCell><Typography noWrap>{workflow.name}</Typography></TableCell>
                          <TableCell><Typography noWrap>{workflow.key}</Typography></TableCell>
                          <TableCell>v{workflow.version}</TableCell>
                          <TableCell>{formatDateTime(workflow.published_at)}</TableCell>
                          <TableCell>
                            <Typography noWrap title={workflow.description}>
                              {workflow.description || '—'}
                            </Typography>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </ResizableMuiTable>
                </TableContainer>
              )}
            </Paper>
          </>
        )}
      </Stack>
    </PageShell>
  )
}

export default ProjectIntakeSettingsPage
