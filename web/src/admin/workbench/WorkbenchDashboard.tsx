import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { Title } from 'react-admin'
import { useSearchParams } from 'react-router-dom'
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Chip,
  CircularProgress,
  Divider,
  FormControlLabel,
  Paper,
  Stack,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
} from '@mui/material'
import {
  Assessment as DashboardIcon,
  Refresh as RefreshIcon,
} from '@mui/icons-material'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import {
  ResizableMuiTable,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
  apiFetch,
  localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import { humanApiRoutes } from '@/lib/generated/human-api'
import {
  loadAuthorizedProjects,
  type AuthorizedProject,
} from '@/lib/projectScope'
import type { WorkbenchDashboard as WorkbenchDashboardData } from '@/lib/types/workbenchDashboard'

const allowedDays = [7, 30, 90] as const
type DashboardDays = typeof allowedDays[number]

const projectColumns: ResizableColumn[] = [
  { key: 'project', defaultWidth: 260, minWidth: 180, maxWidth: 420 },
  { key: 'total', defaultWidth: 140, minWidth: 100, maxWidth: 220 },
  { key: 'share', defaultWidth: 160, minWidth: 120, maxWidth: 240 },
  { key: 'sla', defaultWidth: 150, minWidth: 110, maxWidth: 220 },
  { key: 'overdue', defaultWidth: 150, minWidth: 110, maxWidth: 220 },
]

const metricCards = (
  dashboard: WorkbenchDashboardData,
): Array<{ label: string; value: number; note: string }> => [
  {
    label: '工单总量',
    value: dashboard.summary.total,
    note: '当前聚合范围内的全部工单',
  },
  {
    label: '处理中',
    value: dashboard.summary.status.in_progress,
    note: '当前处于处理中状态',
  },
  {
    label: 'SLA 已违约',
    value: dashboard.summary.sla_breached,
    note: '已标记 SLA 违约',
  },
  {
    label: '已逾期',
    value: dashboard.summary.overdue,
    note: '非终态且截止时间早于生成时间',
  },
  {
    label: '未分派',
    value: dashboard.summary.assignment.unassigned,
    note: '尚未分派给 Human 或服务主体',
  },
]

const parseDays = (value: string | null): DashboardDays => {
  const parsed = Number(value)
  return allowedDays.includes(parsed as DashboardDays)
    ? parsed as DashboardDays
    : 30
}

const WorkbenchDashboard: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams()
  const [projects, setProjects] = useState<AuthorizedProject[]>([])
  const [projectsLoading, setProjectsLoading] = useState(true)
  const [dashboard, setDashboard] = useState<WorkbenchDashboardData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [reloadToken, setReloadToken] = useState(0)

  const days = parseDays(searchParams.get('days'))
  const serializedSearch = searchParams.toString()
  const selectedKeys = useMemo(
    () => {
      const keys = new URLSearchParams(serializedSearch).getAll('project_keys')
      return keys.length === 0 ? null : keys
    },
    [serializedSearch],
  )
  const activeProjects = useMemo(
    () => projects.filter(({ project }) => project.status === 'active'),
    [projects],
  )

  useEffect(() => {
    let active = true
    setProjectsLoading(true)
    void loadAuthorizedProjects(true)
      .then((authorized) => {
        if (active) setProjects(authorized)
      })
      .catch((projectError: unknown) => {
        if (active) {
          setError(localizedUnknownErrorMessage(
            projectError,
            '授权项目列表加载失败，请稍后重试',
          ))
        }
      })
      .finally(() => {
        if (active) setProjectsLoading(false)
      })
    return () => {
      active = false
    }
  }, [reloadToken])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    void apiFetch<WorkbenchDashboardData>(
      humanApiRoutes.getWorkbenchDashboard({
        ...(selectedKeys === null ? {} : { project_keys: selectedKeys }),
        days,
      }),
      { signal: controller.signal },
    )
      .then(setDashboard)
      .catch((requestError: unknown) => {
        if (
          controller.signal.aborted ||
          (requestError instanceof DOMException &&
            requestError.name === 'AbortError')
        ) return
        setError(localizedUnknownErrorMessage(
          requestError,
          '运营大屏加载失败，请检查项目授权后重试',
        ))
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [days, reloadToken, selectedKeys])

  const updateURL = useCallback((
    nextKeys: string[] | null,
    nextDays: DashboardDays = days,
  ) => {
    const next = new URLSearchParams()
    if (nextKeys !== null) {
      nextKeys.forEach((key) => next.append('project_keys', key))
    }
    next.set('days', String(nextDays))
    setSearchParams(next, { replace: true })
  }, [days, setSearchParams])

  const toggleProject = useCallback((key: string) => {
    const current = selectedKeys ?? []
    const next = current.includes(key)
      ? current.filter((candidate) => candidate !== key)
      : [...current, key]
    updateURL(next.length === 0 ? null : next)
  }, [selectedKeys, updateURL])

  const scopeLabel = selectedKeys === null
    ? '全部授权项目'
    : `已选 ${selectedKeys.length} 个项目`
  const statusData = dashboard
    ? [
        { name: '待处理', value: dashboard.summary.status.open },
        { name: '处理中', value: dashboard.summary.status.in_progress },
        { name: '等待中', value: dashboard.summary.status.pending },
        { name: '已解决', value: dashboard.summary.status.resolved },
        { name: '已关闭', value: dashboard.summary.status.closed },
        { name: '已取消', value: dashboard.summary.status.cancelled },
      ]
    : []
  const priorityData = dashboard
    ? [
        { name: '低', value: dashboard.summary.priority.low },
        { name: '普通', value: dashboard.summary.priority.normal },
        { name: '高', value: dashboard.summary.priority.high },
        { name: '紧急', value: dashboard.summary.priority.urgent },
        { name: '严重', value: dashboard.summary.priority.critical },
      ]
    : []

  return (
    <>
      <Title title="运营大屏" />
      <Box sx={{ p: { xs: 2, md: 3 }, minWidth: 0 }}>
        <Stack
          direction={{ xs: 'column', md: 'row' }}
          spacing={2}
          sx={{ justifyContent: 'space-between', mb: 2 }}
        >
          <Box>
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <DashboardIcon color="primary" />
              <Typography variant="h4">运营大屏</Typography>
              <Chip size="small" color="primary" label={scopeLabel} />
            </Stack>
            <Typography color="text.secondary" sx={{ mt: 0.5 }}>
              仅聚合你具有有效成员关系的项目，平台角色不会扩大统计范围。
            </Typography>
          </Box>
          <Button
            startIcon={<RefreshIcon />}
            onClick={() => setReloadToken((current) => current + 1)}
            disabled={loading || projectsLoading}
          >
            刷新
          </Button>
        </Stack>

        <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
          <Stack spacing={1.5}>
            <Stack
              direction={{ xs: 'column', md: 'row' }}
              spacing={2}
              sx={{ justifyContent: 'space-between' }}
            >
              <Box>
                <Typography variant="subtitle2">统计周期</Typography>
                <ToggleButtonGroup
                  exclusive
                  size="small"
                  value={days}
                  onChange={(_, value: DashboardDays | null) => {
                    if (value !== null) updateURL(selectedKeys, value)
                  }}
                  aria-label="运营大屏统计周期"
                  sx={{ mt: 0.75 }}
                >
                  {allowedDays.map((value) => (
                    <ToggleButton key={value} value={value}>
                      近 {value} 天
                    </ToggleButton>
                  ))}
                </ToggleButtonGroup>
              </Box>
              <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                <Chip
                  variant="outlined"
                  label={scopeLabel}
                  aria-label={`当前范围：${scopeLabel}`}
                />
                {selectedKeys !== null && (
                  <Button size="small" onClick={() => updateURL(null)}>
                    清除筛选
                  </Button>
                )}
              </Stack>
            </Stack>
            <Divider />
            <FormControlLabel
              control={(
                <Checkbox
                  checked={selectedKeys === null}
                  onChange={() => updateURL(null)}
                />
              )}
              label={`全部授权项目（${activeProjects.length}）`}
            />
            {selectedKeys !== null && (
              <Stack
                direction="row"
                spacing={1}
                useFlexGap
                sx={{ flexWrap: 'wrap' }}
                aria-label="已选项目"
              >
                {selectedKeys.map((key) => {
                  const selected = activeProjects.find(
                    ({ project }) => project.key === key,
                  )
                  return (
                    <Chip
                      key={key}
                      size="small"
                      label={selected
                        ? `${selected.project.name} · ${key}`
                        : key}
                      onDelete={() => toggleProject(key)}
                    />
                  )
                })}
              </Stack>
            )}
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: {
                  xs: '1fr',
                  sm: 'repeat(2, minmax(0, 1fr))',
                  lg: 'repeat(3, minmax(0, 1fr))',
                },
                gap: 0.5,
              }}
            >
              {activeProjects.map(({ project }) => (
                <Box
                  key={project.key}
                  component="label"
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    borderRadius: 1,
                    cursor: 'pointer',
                    bgcolor: selectedKeys?.includes(project.key)
                      ? 'action.selected'
                      : undefined,
                    '&:hover': { bgcolor: 'action.hover' },
                  }}
                >
                  <Checkbox
                    checked={selectedKeys?.includes(project.key) ?? false}
                    onChange={() => toggleProject(project.key)}
                    slotProps={{
                      input: { 'aria-label': `选择项目 ${project.name}` },
                    }}
                  />
                  <Box sx={{ minWidth: 0 }}>
                    <Typography noWrap>{project.name}</Typography>
                    <Typography variant="caption" color="text.secondary">
                      {project.key}
                    </Typography>
                  </Box>
                </Box>
              ))}
            </Box>
          </Stack>
        </Paper>

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

        {loading && !dashboard ? (
          <Box
            role="status"
            aria-label="正在加载运营大屏"
            sx={{ display: 'grid', minHeight: 320, placeItems: 'center' }}
          >
            <CircularProgress size={34} />
          </Box>
        ) : dashboard && dashboard.selected_projects.length === 0 ? (
          <Alert severity="info">
            当前账号没有有效项目成员关系，运营大屏暂无可汇总数据。
          </Alert>
        ) : dashboard ? (
          <Stack spacing={2}>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: {
                  xs: '1fr',
                  sm: 'repeat(2, minmax(0, 1fr))',
                  xl: 'repeat(5, minmax(0, 1fr))',
                },
                gap: 2,
              }}
            >
              {metricCards(dashboard).map((metric) => (
                <Paper key={metric.label} variant="outlined" sx={{ p: 2 }}>
                  <Typography color="text.secondary" variant="body2">
                    {metric.label}
                  </Typography>
                  <Typography variant="h4" sx={{ my: 0.5 }}>
                    {metric.value.toLocaleString('zh-CN')}
                  </Typography>
                  <Typography variant="caption" color="text.secondary">
                    {metric.note}
                  </Typography>
                </Paper>
              ))}
            </Box>

            <Paper variant="outlined" sx={{ p: 2 }}>
              <Typography variant="h6">工单创建趋势</Typography>
              <Typography variant="body2" color="text.secondary">
                最近 {dashboard.days} 天按日统计
              </Typography>
              <Box sx={{ width: '100%', height: { xs: 260, md: 320 }, mt: 2 }}>
                <ResponsiveContainer>
                  <LineChart data={dashboard.daily_trend}>
                    <CartesianGrid strokeDasharray="3 3" />
                    <XAxis dataKey="date" minTickGap={28} />
                    <YAxis allowDecimals={false} width={36} />
                    <Tooltip />
                    <Line
                      type="monotone"
                      dataKey="created"
                      name="新建工单"
                      stroke="#2563eb"
                      strokeWidth={2}
                      dot={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </Box>
            </Paper>

            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: { xs: '1fr', lg: 'repeat(2, 1fr)' },
                gap: 2,
              }}
            >
              {[
                { title: '状态分布', data: statusData, color: '#2563eb' },
                { title: '优先级分布', data: priorityData, color: '#64748b' },
              ].map((chart) => (
                <Paper key={chart.title} variant="outlined" sx={{ p: 2 }}>
                  <Typography variant="h6">{chart.title}</Typography>
                  <Box sx={{ width: '100%', height: 280, mt: 1 }}>
                    <ResponsiveContainer>
                      <BarChart data={chart.data}>
                        <CartesianGrid strokeDasharray="3 3" />
                        <XAxis dataKey="name" />
                        <YAxis allowDecimals={false} width={36} />
                        <Tooltip />
                        <Legend />
                        <Bar
                          dataKey="value"
                          name="工单数"
                          fill={chart.color}
                          radius={[4, 4, 0, 0]}
                        />
                      </BarChart>
                    </ResponsiveContainer>
                  </Box>
                </Paper>
              ))}
            </Box>

            <Paper variant="outlined">
              <Box sx={{ p: 2 }}>
                <Typography variant="h6">项目贡献</Typography>
                <Typography variant="body2" color="text.secondary">
                  按工单量从高到低排列
                </Typography>
              </Box>
              <TableContainer sx={{ overflowX: 'auto' }}>
                <ResizableMuiTable
                  tableId="workbench.dashboard.projects"
                  columns={projectColumns}
                  size="small"
                  aria-label="项目贡献列表"
                >
                  <TableHead>
                    <TableRow>
                      <TableCell>项目</TableCell>
                      <TableCell>工单数</TableCell>
                      <TableCell>占比</TableCell>
                      <TableCell>SLA 已违约</TableCell>
                      <TableCell>已逾期</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {dashboard.project_breakdown.map((project) => (
                      <TableRow key={project.project_key} hover>
                        <TableCell>
                          <Typography noWrap>{project.project_name}</Typography>
                          <Typography variant="caption" color="text.secondary" noWrap>
                            {project.project_key}
                          </Typography>
                        </TableCell>
                        <TableCell>{project.total}</TableCell>
                        <TableCell>
                          {dashboard.summary.total === 0
                            ? '0%'
                            : `${Math.round(
                              (project.total / dashboard.summary.total) * 100,
                            )}%`}
                        </TableCell>
                        <TableCell>{project.sla_breached}</TableCell>
                        <TableCell>{project.overdue}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </ResizableMuiTable>
              </TableContainer>
            </Paper>
          </Stack>
        ) : null}
      </Box>
    </>
  )
}

export default WorkbenchDashboard
