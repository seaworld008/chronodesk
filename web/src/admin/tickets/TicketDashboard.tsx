import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Assignment,
  Refresh as RefreshIcon,
  Timer,
  Warning,
  ArrowOutward,
} from '@mui/icons-material'
import {
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  LinearProgress,
  List,
  ListItem,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Skeleton,
  Stack,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
  useTheme,
  type ChipProps,
} from '@mui/material'
import { Title } from 'react-admin'
import { usePermissions } from 'react-admin'
import { useNavigate } from 'react-router-dom'
import { alpha } from '@mui/material/styles'
import { RatioRow } from '@/components/layout/RatioRow'

interface TicketStats {
  total: number
  open: number
  in_progress: number
  pending: number
  resolved: number
  overdue: number
  sla_breached: number
  my_tickets: number
  unassigned: number
  high_priority: number
  escalated: number
  avg_first_response_minutes?: number
  avg_resolution_minutes?: number
  today_created?: number
  today_resolved?: number
}

interface TicketItem {
  id: number
  ticket_number: string
  title: string
  priority: string
  status: string
  customer_name?: string
  created_at: string
  is_overdue: boolean
  sla_breached: boolean
}

type TimeRange = 'today' | 'yesterday' | '7d' | '30d'

const containerSx = {
  width: '100%',
  px: { xs: 2, md: 3, lg: 4, xl: 6 },
  py: { xs: 2, md: 3, lg: 4 },
  backgroundColor: 'transparent',
}

const cardBaseSx = {
  height: '100%',
  borderRadius: 2,
  boxShadow: 3,
  display: 'flex',
  flexDirection: 'column' as const,
  justifyContent: 'space-between',
  backgroundImage: 'none',
}

const kpiCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 128 },
}

const infoCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 150 },
}

const bottomCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 420 },
}

const scrollSectionSx = {
  overflow: 'auto',
  flex: 1,
  pr: { xs: 0, md: 1 },
}

  const rowSx = {
    display: 'grid',
    gridTemplateColumns: '1fr auto',
  alignItems: 'center',
  gap: 1.5,
  py: 1,
  borderBottom: '1px dashed rgba(15, 23, 42, 0.08)',
  '&:last-of-type': { borderBottom: 'none' },
  '& .title': {
    overflow: 'hidden',
    whiteSpace: 'nowrap' as const,
    textOverflow: 'ellipsis',
  },
  '& .status': {
    justifySelf: 'flex-end',
    whiteSpace: 'nowrap' as const,
  },
}

const SMALL_CARD_RATIOS: number[] = [1, 1, 1]
const BOTTOM_SECTION_RATIOS: number[] = [1, 1]
const DYNAMIC_SECTION_RATIOS: number[] = [1, 1]

export const TicketDashboard: React.FC = () => {
  const { permissions } = usePermissions()
  const navigate = useNavigate()
  const theme = useTheme()
  const [stats, setStats] = useState<TicketStats>({
    total: 0,
    open: 0,
    in_progress: 0,
    pending: 0,
    resolved: 0,
    overdue: 0,
    sla_breached: 0,
    my_tickets: 0,
    unassigned: 0,
    high_priority: 0,
    escalated: 0,
  })
  const [urgentTickets, setUrgentTickets] = useState<TicketItem[]>([])
  const [recentTickets, setRecentTickets] = useState<TicketItem[]>([])
  const [myTickets, setMyTickets] = useState<TicketItem[]>([])
  const [timeRange, setTimeRange] = useState<TimeRange>('7d')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)

  const isAgent = permissions?.role === 'agent'

  useEffect(() => {
    const controller = new AbortController()
    const fetchDashboard = async () => {
      setLoading(true)
      setError(null)
      try {
        const headers: HeadersInit = {
          Authorization: `Bearer ${localStorage.getItem('token') || ''}`,
        }
        const query = `range=${timeRange}`

        const [statsRes, urgentRes, recentRes, myRes] = await Promise.all([
          fetch(`/api/tickets/stats?${query}`, { headers, signal: controller.signal }),
          fetch(`/api/tickets?priority=urgent,critical&status=open,in_progress&limit=10&${query}`, {
            headers,
            signal: controller.signal,
          }),
          fetch(`/api/tickets?limit=10&sort=created_at:desc&${query}`, { headers, signal: controller.signal }),
          isAgent
            ? fetch(`/api/tickets?assigned_to_me=true&limit=10&${query}`, { headers, signal: controller.signal })
            : Promise.resolve(null),
        ])

        const statsJson = statsRes ? await statsRes.json() : {}
        setStats((prev) => ({ ...prev, ...(statsJson?.data ?? statsJson ?? {}) }))

        const urgentJson = await urgentRes.json()
        setUrgentTickets(urgentJson?.data?.items ?? urgentJson?.data ?? urgentJson ?? [])

        const recentJson = await recentRes.json()
        setRecentTickets(recentJson?.data?.items ?? recentJson?.data ?? recentJson ?? [])

        if (isAgent && myRes) {
          const myJson = await myRes.json()
          setMyTickets(myJson?.data?.items ?? myJson?.data ?? myJson ?? [])
        }
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') {
          return
        }
        const message = err instanceof Error ? err.message : '仪表盘数据加载失败'
        setError(message)
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      }
    }

    fetchDashboard()
    return () => controller.abort()
  }, [timeRange, refreshKey, isAgent])

  const handleRefresh = () => setRefreshKey((key) => key + 1)

  const handleNavigateToTickets = useCallback(
    (filter: Record<string, unknown>) => {
      const params = new URLSearchParams({ filter: JSON.stringify(filter) }).toString()
      navigate({ pathname: '/tickets', search: `?${params}` })
    },
    [navigate]
  )

  const handleNavigateToTicketDetail = useCallback(
    (ticketId: number) => {
      navigate(`/tickets/${ticketId}/show`)
    },
    [navigate]
  )

  const getPriorityColor = (priority: string): ChipProps['color'] => {
    switch (priority) {
      case 'critical':
      case 'urgent':
        return 'error'
      case 'high':
        return 'warning'
      case 'normal':
        return 'primary'
      default:
        return 'default'
    }
  }

  const getStatusColor = (status: string): ChipProps['color'] => {
    switch (status) {
      case 'resolved':
        return 'success'
      case 'closed':
        return 'default'
      case 'in_progress':
        return 'primary'
      case 'pending':
      case 'open':
        return 'warning'
      default:
        return 'default'
    }
  }

  const statusDistribution = useMemo(() => {
    const { total, open, in_progress, pending, resolved } = stats
    const entries = [
      { label: '待处理', value: open, color: 'warning' as const, filter: { status: 'open' } },
      { label: '处理中', value: in_progress, color: 'primary' as const, filter: { status: 'in_progress' } },
      { label: '等待中', value: pending, color: 'secondary' as const, filter: { status: 'pending' } },
      { label: '已解决', value: resolved, color: 'success' as const, filter: { status: 'resolved' } },
    ]
    return entries.map((item) => ({
      ...item,
      percent: total > 0 ? Math.round((item.value / total) * 100) : 0,
    }))
  }, [stats])

  const kpis = useMemo(
    () => [
      {
        label: '总工单',
        value: stats.total,
        helper: `${stats.resolved} 已解决`,
        color: 'primary.main',
        onClick: () => handleNavigateToTickets({}),
      },
      {
        label: '高优先级',
        value: stats.high_priority,
        helper: `${stats.sla_breached} SLA 违约`,
        color: 'error.main',
        onClick: () => handleNavigateToTickets({ priority: ['high', 'urgent', 'critical'] }),
      },
      {
        label: '待分配',
        value: stats.unassigned,
        helper: '点击查看待分配',
        color: 'warning.main',
        onClick: () => handleNavigateToTickets({ assigned_to_id: null }),
      },
      {
        label: '已升级',
        value: stats.escalated,
        helper: '查看升级链路',
        color: 'info.main',
        onClick: () => handleNavigateToTickets({ status: 'pending', priority: ['urgent', 'critical'] }),
      },
      {
        label: '逾期工单',
        value: stats.overdue,
        helper: '优先处理风险',
        color: 'warning.dark',
        onClick: () => handleNavigateToTickets({ is_overdue: true }),
      },
      {
        label: 'SLA 违约',
        value: stats.sla_breached,
        helper: '查看 SLA 风险',
        color: 'error.dark',
        onClick: () => handleNavigateToTickets({ sla_breached: true }),
      },
    ],
    [stats, handleNavigateToTickets]
  )

  const kpiRatios = useMemo(() => Array(kpis.length).fill(1), [kpis.length])

  const snapshotMetrics = useMemo(() => {
    const items = [
      {
        label: '今日新增',
        value: stats.today_created ?? 0,
        color: theme.palette.primary.main,
      },
      {
        label: '今日解决',
        value: stats.today_resolved ?? 0,
        color: theme.palette.success.main,
      },
      {
        label: '待回复 24 小时',
        value: stats.pending ?? 0,
        color: theme.palette.warning.main,
      },
      {
        label: '首次响应 (分)',
        value: stats.avg_first_response_minutes ?? 0,
        color: theme.palette.info.main,
      },
      {
        label: '解决平均 (分)',
        value: stats.avg_resolution_minutes ?? 0,
        color: theme.palette.secondary.main,
      },
    ]

    return items.map((metric) => {
      const numericValue = Number.isFinite(metric.value) ? metric.value : 0
      return {
        ...metric,
        value: numericValue,
        displayValue: numericValue.toLocaleString(),
        gradient: `linear-gradient(180deg, ${alpha(metric.color, 0.45)} 0%, ${alpha(metric.color, 0.9)} 100%)`,
        shadow: `0 14px 32px ${alpha(metric.color, 0.24)}`,
      }
    })
  }, [stats, theme])

  const snapshotMax = useMemo(
    () => Math.max(...snapshotMetrics.map((metric) => metric.value), 1),
    [snapshotMetrics]
  )

  const renderTicketColumn = (
    title: string,
    items: TicketItem[],
    emptyLabel: string,
    filter?: Record<string, unknown>
  ) => (
    <Stack spacing={1.5} sx={{ minHeight: 0 }}>
      <Stack direction="row" justifyContent="space-between" alignItems="center">
        <Typography variant="subtitle1" fontWeight={600}>
          {title}
        </Typography>
        {items.length > 0 && filter && (
          <Button size="small" endIcon={<ArrowOutward />} onClick={() => handleNavigateToTickets(filter)}>
            全部
          </Button>
        )}
      </Stack>
      {loading ? (
        <Stack spacing={1}>
          {[...Array(4)].map((_, index) => (
            <Skeleton key={index} variant="rounded" height={44} />
          ))}
        </Stack>
      ) : items.length > 0 ? (
        <List dense disablePadding sx={{ m: 0 }}>
          {items.slice(0, 6).map((ticket) => (
            <ListItem disablePadding key={ticket.id}>
            <ListItemButton onClick={() => handleNavigateToTicketDetail(ticket.id)}>
                <ListItemIcon sx={{ minWidth: 32 }}>
                  {ticket.is_overdue ? (
                    <Warning color="error" />
                  ) : ticket.sla_breached ? (
                    <Timer color="error" />
                  ) : (
                    <Assignment color="primary" />
                  )}
                </ListItemIcon>
                <ListItemText
                  primary={
                    <Stack direction="row" spacing={1} alignItems="center" sx={{ overflow: 'hidden' }}>
                      <Typography variant="body2" fontWeight={600} className="title" noWrap>
                        {ticket.title}
                      </Typography>
                      <Chip size="small" label={ticket.priority} color={getPriorityColor(ticket.priority)} />
                      <Chip
                        size="small"
                        variant="outlined"
                        label={ticket.status}
                        color={getStatusColor(ticket.status)}
                      />
                    </Stack>
                  }
                  secondary={
                    <Typography variant="caption" color="text.secondary" noWrap>
                      #{ticket.ticket_number} · {ticket.customer_name || '匿名用户'} · {new Date(ticket.created_at).toLocaleString()}
                    </Typography>
                  }
                />
              </ListItemButton>
            </ListItem>
          ))}
        </List>
      ) : (
        <Typography variant="body2" color="text.secondary" textAlign="center">
          {emptyLabel}
        </Typography>
      )}
    </Stack>
  )

  return (
    <Box sx={containerSx}>
      <Title title="工单运营总览" />
      <Stack spacing={3} sx={{ flex: 1 }}>
        <Stack
          direction={{ xs: 'column', md: 'row' }}
          justifyContent="space-between"
          alignItems={{ xs: 'flex-start', md: 'center' }}
          spacing={2}
        >
          <Box>
            <Typography variant="h4" fontWeight={600} gutterBottom>
              工单运营总览
            </Typography>
            <Typography variant="body2" color="text.secondary">
              快速掌握工单态势、风险指标与团队动作，所有筛选与工单列表保持联动。
            </Typography>
          </Box>
          <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center" justifyContent="flex-end">
            <ToggleButtonGroup
              size="small"
              exclusive
              value={timeRange}
              onChange={(_, value: TimeRange | null) => value && setTimeRange(value)}
              sx={{ flexWrap: 'wrap', gap: 1 }}
            >
              <ToggleButton value="today">今日</ToggleButton>
              <ToggleButton value="yesterday">昨日</ToggleButton>
              <ToggleButton value="7d">近 7 天</ToggleButton>
              <ToggleButton value="30d">近 30 天</ToggleButton>
            </ToggleButtonGroup>
            <Button startIcon={<RefreshIcon />} onClick={handleRefresh}>
              刷新
            </Button>
          </Stack>
        </Stack>

        {error && (
          <Card sx={cardBaseSx}>
            <CardContent>
              <Typography color="error">{error}</Typography>
            </CardContent>
          </Card>
        )}

        <RatioRow ratios={kpiRatios} gap={2} breakAt="md" alignItems="stretch">
          {kpis.map((item) => (
            <Card key={item.label} sx={kpiCardSx}>
              <CardContent>
                {loading ? (
                  <Skeleton variant="rounded" height={72} />
                ) : (
                  <Stack spacing={1.5} alignItems="flex-start">
                    <Typography variant="h3" color={item.color}>
                      {item.value}
                    </Typography>
                    <Typography variant="body2" color="text.secondary">
                      {item.label}
                    </Typography>
                    <Chip size="small" label={item.helper} onClick={item.onClick} />
                  </Stack>
                )}
              </CardContent>
            </Card>
          ))}
        </RatioRow>

        <RatioRow ratios={SMALL_CARD_RATIOS} gap={2} breakAt="md" alignItems="stretch">
          <Card sx={infoCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
              <Typography variant="subtitle1" fontWeight={600}>
                状态分布
              </Typography>
              <Stack spacing={1.5}>
                {loading
                  ? [...Array(4)].map((_, index) => <Skeleton key={index} variant="rounded" height={28} />)
                  : statusDistribution.map((item) => (
                      <Box key={item.label} sx={{ cursor: 'pointer' }} onClick={() => handleNavigateToTickets(item.filter)}>
                        <Stack direction="row" justifyContent="space-between" alignItems="center" sx={{ mb: 0.5 }}>
                          <Typography variant="body2" color="text.secondary">
                            {item.label}
                          </Typography>
                          <Typography variant="body2" color="text.primary">
                            {item.value}（{item.percent}%）
                          </Typography>
                        </Stack>
                        <LinearProgress variant="determinate" value={item.percent} color={item.color} sx={{ height: 8, borderRadius: 5 }} />
                      </Box>
                    ))}
              </Stack>
            </CardContent>
          </Card>

          <Card sx={infoCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
              <Typography variant="subtitle1" fontWeight={600}>
                SLA 与风险
              </Typography>
              {loading ? (
                <Stack spacing={1.5}>
                  {[...Array(3)].map((_, index) => <Skeleton key={index} variant="rounded" height={28} />)}
                </Stack>
              ) : (
                <Stack spacing={1.5}>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" color="text.secondary">
                      SLA 违约
                    </Typography>
                    <Chip className="status" color="error" label={stats.sla_breached} onClick={() => handleNavigateToTickets({ sla_breached: true })} />
                  </Box>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" color="text.secondary">
                      逾期工单
                    </Typography>
                    <Chip className="status" color="warning" label={stats.overdue} onClick={() => handleNavigateToTickets({ is_overdue: true })} />
                  </Box>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" color="text.secondary">
                      解决率
                    </Typography>
                    <Typography className="status" variant="h6" color="success.main">
                      {stats.total > 0 ? Math.round((stats.resolved / stats.total) * 100) : 0}%
                    </Typography>
                  </Box>
                </Stack>
              )}
            </CardContent>
          </Card>

          <Card sx={infoCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
              <Typography variant="subtitle1" fontWeight={600}>
                团队关注
              </Typography>
              {loading ? (
                <Stack spacing={1.5}>
                  {[...Array(3)].map((_, index) => <Skeleton key={index} variant="rounded" height={28} />)}
                </Stack>
              ) : isAgent ? (
                <Stack spacing={1}>
                  <Typography variant="body2" color="text.secondary">
                    当前分配给你的工单
                  </Typography>
                  <Typography variant="h4" color="primary.main">
                    {stats.my_tickets}
                  </Typography>
                  <Button variant="contained" onClick={() => handleNavigateToTickets({ assigned_to_me: true })}>
                    查看我的工单
                  </Button>
                </Stack>
              ) : (
                <Stack spacing={1}>
                  <Typography variant="body2" color="text.secondary">
                    待分配工单
                  </Typography>
                  <Typography variant="h4" color="warning.main">
                    {stats.unassigned}
                  </Typography>
                  <Button variant="contained" color="warning" onClick={() => handleNavigateToTickets({ assigned_to_id: null })}>
                    快速分配
                  </Button>
                </Stack>
              )}
            </CardContent>
          </Card>
        </RatioRow>

        <RatioRow ratios={BOTTOM_SECTION_RATIOS} gap={2} breakAt="md" alignItems="stretch">
          <Card sx={bottomCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
              <Typography variant="h6" fontWeight={600}>
                运营快照
              </Typography>
              {loading ? (
                <Stack spacing={1.5}>
                  <Skeleton variant="rounded" height={200} />
                  <Skeleton variant="rounded" width="40%" height={36} />
                </Stack>
              ) : (
                <Box sx={{ ...scrollSectionSx, display: 'flex', flexDirection: 'column', gap: 2 }}>
                  <Box
                    role="img"
                    aria-label="运营快照指标柱状图"
                    sx={{
                      flex: 1,
                      display: 'flex',
                      alignItems: 'flex-end',
                      justifyContent: 'space-between',
                      gap: { xs: 1.5, sm: 2.5, md: 3 },
                      minHeight: { xs: 160, sm: 210 },
                      pb: 1,
                    }}
                  >
                    {snapshotMetrics.map((metric) => {
                      const heightPercent = snapshotMax > 0 ? Math.round((metric.value / snapshotMax) * 100) : 0
                      const computedHeight = metric.value > 0 ? Math.max(heightPercent, 12) : 0
                      return (
                        <Stack key={metric.label} spacing={1} alignItems="center" sx={{ flex: 1, minWidth: 0 }}>
                          <Typography variant="body2" fontWeight={600} color="text.primary">
                            {metric.displayValue}
                          </Typography>
                          <Box
                            sx={{
                              width: '100%',
                              maxWidth: 80,
                              height: { xs: 140, sm: 200 },
                              display: 'flex',
                              alignItems: 'flex-end',
                              justifyContent: 'center',
                            }}
                          >
                            <Box
                              aria-hidden
                              sx={{
                                width: '65%',
                                minWidth: 32,
                                height: `${computedHeight}%`,
                                minHeight: metric.value > 0 ? '16px' : 0,
                                borderRadius: 3,
                                background: metric.gradient,
                                boxShadow: metric.shadow,
                                transition: 'height 0.4s ease',
                              }}
                            />
                          </Box>
                          <Typography variant="caption" color="text.secondary" textAlign="center" sx={{ maxWidth: 96 }}>
                            {metric.label}
                          </Typography>
                        </Stack>
                      )
                    })}
                  </Box>
                  <Button
                    variant="outlined"
                    size="small"
                    onClick={() => navigate('/automation-rules')}
                    sx={{ alignSelf: 'flex-start' }}
                  >
                    查看自动化执行
                  </Button>
                </Box>
              )}
            </CardContent>
          </Card>

          <Card sx={bottomCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
              <Typography variant="h6" fontWeight={600}>
                工单动态
              </Typography>
              <RatioRow ratios={DYNAMIC_SECTION_RATIOS} gap={2} breakAt="sm" sx={scrollSectionSx}>
                {renderTicketColumn('紧急工单', urgentTickets, '暂无紧急工单', {
                  priority: ['urgent', 'critical'],
                  status: ['open', 'in_progress'],
                })}
                {renderTicketColumn(
                  isAgent ? '我的最新工单' : '最新工单',
                  isAgent ? myTickets : recentTickets,
                  '暂无工单',
                  {
                    ...(isAgent ? { assigned_to_me: true } : {}),
                  }
                )}
              </RatioRow>
            </CardContent>
          </Card>
        </RatioRow>
      </Stack>
    </Box>
  )
}

export default TicketDashboard
