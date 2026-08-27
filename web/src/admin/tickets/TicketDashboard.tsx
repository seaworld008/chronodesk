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
  List,
  ListItem,
  ListItemButton,
  ListItemText,
  Skeleton,
  Stack,
  Typography,
  useTheme,
  type ChipProps,
  Avatar,
  ListItemAvatar,
  Divider,
} from '@mui/material'
import { Title } from 'react-admin'
import { usePermissions } from 'react-admin'
import { useNavigate } from 'react-router-dom'
import { alpha } from '@mui/material/styles'
import { RatioRow } from '@/components/layout/RatioRow'
import {
  API_BASE,
  localizedUnknownErrorMessage,
  sessionAwareFetch,
} from '@/lib/apiClient'
import { joinApiUrl } from '@/lib/apiUrl'
import type { AccessPermissions } from '@/lib/accessControl'
import { humanApiRoutes } from '@/lib/generated/human-api'
import { readHumanAccessToken } from '@/lib/humanSessionRuntime'
import {
  parseProjectRole,
  projectResourcePath,
  resolveActiveProjectKey,
} from '@/lib/projectScope'
import {
  PieChart,
  Pie,
  Cell,
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  Sector,
  type PieLabelRenderProps,
} from 'recharts'

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

const ticketStatusLabels: Record<string, string> = {
  open: '待处理',
  in_progress: '处理中',
  pending: '等待中',
  resolved: '已解决',
  closed: '已关闭',
  cancelled: '已取消',
}

const ticketPriorityLabels: Record<string, string> = {
  low: '低',
  normal: '普通',
  high: '高',
  urgent: '紧急',
  critical: '严重',
}

const containerSx = {
  width: '100%',
  px: { xs: 2, md: 4, lg: 6 },
  py: { xs: 3, md: 5 },
  backgroundColor: 'transparent',
}

const cardBaseSx = {
  height: '100%',
  borderRadius: 4,
  boxShadow: '0 4px 20px rgba(0,0,0,0.05)',
  display: 'flex',
  flexDirection: 'column' as const,
  justifyContent: 'space-between',
  backgroundImage: 'none',
  transition: 'transform 0.2s, box-shadow 0.2s',
  '&:hover': {
    transform: 'translateY(-2px)',
    boxShadow: '0 8px 24px rgba(0,0,0,0.08)',
  },
}

const kpiCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 140 },
  background: 'linear-gradient(135deg, #ffffff 0%, #f8fafc 100%)',
}

const infoCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 300 }, // Increased height for charts
}

const bottomCardSx = {
  ...cardBaseSx,
  minHeight: { xs: 'auto', md: 450 },
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
  py: 1.5,
  borderBottom: '1px dashed rgba(15, 23, 42, 0.06)',
  '&:last-of-type': { borderBottom: 'none' },
  '& .title': {
    overflow: 'hidden',
    whiteSpace: 'nowrap' as const,
    textOverflow: 'ellipsis',
    fontWeight: 500,
  },
  '& .status': {
    justifySelf: 'flex-end',
    whiteSpace: 'nowrap' as const,
  },
}

const SMALL_CARD_RATIOS: number[] = [1, 1, 1]
const DYNAMIC_SECTION_RATIOS: number[] = [1, 1]

type PieActiveShapeProps = {
  cx: number
  cy: number
  innerRadius: number
  outerRadius: number
  startAngle: number
  endAngle: number
  fill?: string
}

const renderActiveShape = (props: PieActiveShapeProps) => {
  const { cx, cy, innerRadius, outerRadius, startAngle, endAngle, fill } = props
  return (
    <g>
      <Sector
        cx={cx}
        cy={cy}
        innerRadius={innerRadius}
        outerRadius={outerRadius + 6}
        startAngle={startAngle}
        endAngle={endAngle}
        fill={fill}
      />
    </g>
  )
}

const TicketDashboard: React.FC = () => {
  const { permissions } = usePermissions<AccessPermissions>()
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
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)
  const isAgent = parseProjectRole(permissions?.project_role) === 'agent'

  useEffect(() => {
    const controller = new AbortController()
    const fetchDashboard = async () => {
      setLoading(true)
      setError(null)
      try {
        const headers: HeadersInit = {
          Authorization: `Bearer ${readHumanAccessToken() || ''}`,
        }
        const ticketsPath = await projectResourcePath('tickets')
        const ticketsURL = joinApiUrl(API_BASE, ticketsPath)
        const projectKey = await resolveActiveProjectKey()
        const myTicketsURL = joinApiUrl(
          API_BASE,
          humanApiRoutes.listMyProjectTickets(
            { projectKey },
            {
              page: 1,
              page_size: 10,
              sort_by: 'created_at',
              sort_order: 'desc',
            },
          ),
        )
        const [statsRes, urgentRes, recentRes, myRes] = await Promise.all([
          sessionAwareFetch(`${ticketsURL}/stats`, { headers, signal: controller.signal }),
          sessionAwareFetch(`${ticketsURL}?priority=urgent,critical&status=open,in_progress&page_size=10`, {
            headers,
            signal: controller.signal,
          }),
          sessionAwareFetch(`${ticketsURL}?page_size=10&sort_by=created_at&sort_order=desc`, { headers, signal: controller.signal }),
          isAgent
            ? sessionAwareFetch(myTicketsURL, { headers, signal: controller.signal })
            : Promise.resolve(null),
        ])

        const responses = [statsRes, urgentRes, recentRes, ...(myRes ? [myRes] : [])]
        if (responses.some((response) => !response.ok)) {
          throw new Error('仪表盘数据请求失败')
        }

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
        const message = localizedUnknownErrorMessage(err, '仪表盘数据加载失败')
        setError(message)
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
        }
      }
    }

    fetchDashboard()
    return () => controller.abort()
  }, [refreshKey, isAgent])

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

  const statusData = useMemo(() => {
    const { open, in_progress, pending, resolved } = stats
    return [
      { name: '待处理', value: open, color: theme.palette.warning.main },
      { name: '处理中', value: in_progress, color: theme.palette.primary.main },
      { name: '等待中', value: pending, color: theme.palette.secondary.main },
      { name: '已解决', value: resolved, color: theme.palette.success.main },
    ].filter(item => item.value > 0)
  }, [stats, theme])

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
    return [
      {
        label: '待处理',
        value: stats.open,
        fill: theme.palette.primary.main,
      },
      {
        label: '处理中',
        value: stats.in_progress,
        fill: theme.palette.success.main,
      },
      {
        label: '等待中',
        value: stats.pending,
        fill: theme.palette.warning.main,
      },
      {
        label: '已解决',
        value: stats.resolved,
        fill: theme.palette.info.main,
      },
      {
        label: '待分配',
        value: stats.unassigned,
        fill: theme.palette.secondary.main,
      },
    ]
  }, [stats, theme])

  const renderTicketColumn = (
    title: string,
    items: TicketItem[],
    emptyLabel: string,
    filter?: Record<string, unknown>
  ) => (
    <Stack spacing={2} sx={{ minHeight: 0 }}>
      <Stack
        direction="row"
        sx={{
          justifyContent: "space-between",
          alignItems: "center"
        }}>
        <Typography
          variant="subtitle1"
          sx={{
            fontWeight: 600,
            display: 'flex',
            alignItems: 'center',
            gap: 1
          }}>
          <Box sx={{ width: 4, height: 16, bgcolor: 'primary.main', borderRadius: 1 }} />
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
            <Skeleton key={index} variant="rounded" height={60} />
          ))}
        </Stack>
      ) : items.length > 0 ? (
        <List disablePadding sx={{ width: '100%', bgcolor: 'background.paper' }}>
          {items.slice(0, 5).map((ticket, index) => (
            <React.Fragment key={ticket.id}>
              <ListItem disablePadding sx={{ display: 'block' }}>
                <ListItemButton
                  alignItems="flex-start"
                  onClick={() => handleNavigateToTicketDetail(ticket.id)}
                  sx={{
                    borderRadius: 2,
                    px: 1,
                    py: 1.5,
                    mb: 0.5,
                    '&:hover': { bgcolor: alpha(theme.palette.primary.main, 0.04) }
                  }}
                >
                  <ListItemAvatar sx={{ minWidth: 48, mt: 0 }}>
                    <Avatar sx={{
                      width: 36, height: 36,
                      bgcolor: ticket.is_overdue || ticket.sla_breached
                        ? alpha(theme.palette.error.main, 0.1)
                        : alpha(theme.palette.primary.main, 0.1),
                      color: ticket.is_overdue || ticket.sla_breached
                        ? 'error.main'
                        : 'primary.main'
                    }}>
                      {ticket.is_overdue ? <Warning fontSize="small" /> :
                        ticket.sla_breached ? <Timer fontSize="small" /> :
                          <Assignment fontSize="small" />}
                    </Avatar>
                  </ListItemAvatar>
                  <ListItemText
                    primary={
                      <Stack
                        direction="row"
                        spacing={1}
                        sx={{
                          justifyContent: "space-between",
                          alignItems: "center"
                        }}>
                        <Typography
                          variant="subtitle2"
                          noWrap
                          sx={{
                            fontWeight: 600,
                            maxWidth: '70%'
                          }}>
                          {ticket.title}
                        </Typography>
                        <Chip
                          size="small"
                          label={ticketStatusLabels[ticket.status] ?? '未知状态'}
                          color={getStatusColor(ticket.status)}
                          sx={{ height: 20, fontSize: '0.7rem', fontWeight: 500 }}
                        />
                      </Stack>
                    }
                    secondary={
                      <Stack
                        direction="row"
                        spacing={1}
                        component="span"
                        sx={{
                          alignItems: "center",
                          mt: 0.5
                        }}>
                        <Typography
                          variant="caption"
                          sx={{
                            color: "text.secondary",
                            fontFamily: 'monospace'
                          }}>
                          #{ticket.ticket_number}
                        </Typography>
                        <Typography variant="caption" sx={{
                          color: "text.disabled"
                        }}>•</Typography>
                        <Typography variant="caption" sx={{
                          color: "text.secondary"
                        }}>
                          {ticket.customer_name || '匿名'}
                        </Typography>
                        <Typography variant="caption" sx={{
                          color: "text.disabled"
                        }}>•</Typography>
                        <Typography variant="caption" sx={{
                          color: "text.secondary"
                        }}>
                          {ticketPriorityLabels[ticket.priority] ?? '未知优先级'}
                        </Typography>
                      </Stack>
                    }
                  />
                </ListItemButton>
                {index < items.slice(0, 5).length - 1 && (
                  <Divider variant="inset" sx={{ ml: 7 }} />
                )}
              </ListItem>
            </React.Fragment>
          ))}
        </List>
      ) : (
        <Box sx={{ py: 4, display: 'flex', justifyContent: 'center' }}>
          <Typography variant="body2" sx={{
            color: "text.secondary"
          }}>
            {emptyLabel}
          </Typography>
        </Box>
      )}
    </Stack>
  )

  return (
    <Box sx={containerSx}>
      <Title title="工单运营总览" />
      <Stack spacing={3} sx={{ flex: 1 }}>
        <Stack
          direction={{ xs: 'column', md: 'row' }}
          spacing={2}
          sx={{
            justifyContent: "space-between",
            alignItems: { xs: 'flex-start', md: 'center' }
          }}>
          <Box>
            <Typography
              variant="h4"
              gutterBottom
              sx={{
                fontWeight: 700,
                background: 'linear-gradient(45deg, #2563eb 30%, #4f46e5 90%)',
                WebkitBackgroundClip: 'text',
                WebkitTextFillColor: 'transparent',
                mb: 1
              }}>
              工单运营总览
            </Typography>
            <Typography
              variant="body1"
              sx={{
                color: "text.secondary",
                maxWidth: 600
              }}>
              实时监控工单状态、SLA 达标率及团队绩效，助力高效运营决策。
            </Typography>
          </Box>
          <Stack
            direction="row"
            spacing={1}
            sx={{
              flexWrap: "wrap",
              alignItems: "center",
              justifyContent: "flex-end"
            }}>
            <Button
              startIcon={<RefreshIcon />}
              onClick={handleRefresh}
              sx={{ color: 'primary.dark' }}
            >
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

        <RatioRow ratios={kpiRatios} gap={2} breakAt="md">
          {kpis.map((item) => (
            <Card key={item.label} sx={{
              ...kpiCardSx,
              position: 'relative',
              overflow: 'hidden',
              '&::after': {
                content: '""',
                position: 'absolute',
                top: 0,
                right: 0,
                width: '100px',
                height: '100%',
                background: `linear-gradient(90deg, transparent, ${alpha(theme.palette.primary.main, 0.05)})`,
                transform: 'skewX(-20deg) translateX(50%)',
              }
            }}>
              <CardContent>
                {loading ? (
                  <Skeleton variant="rounded" height={72} />
                ) : (
                  <Stack spacing={2} sx={{
                    alignItems: "flex-start"
                  }}>
                    <Typography
                      variant="h3"
                      sx={{
                        fontWeight: 700,
                        color: item.color
                      }}>
                      {item.value.toLocaleString('zh-CN')}
                    </Typography>
                    <Box>
                      <Typography
                        variant="subtitle2"
                        sx={{
                          color: "text.secondary",
                          fontWeight: 600
                        }}>
                        {item.label}
                      </Typography>
                      <Chip
                        size="small"
                        label={item.helper}
                        onClick={item.onClick}
                        sx={{
                          mt: 1,
                          height: 24,
                          fontSize: '0.75rem',
                          backgroundColor: alpha(theme.palette.primary.main, 0.08),
                          color: 'primary.main',
                          '&:hover': {
                            backgroundColor: alpha(theme.palette.primary.main, 0.15),
                          }
                        }}
                      />
                    </Box>
                  </Stack>
                )}
              </CardContent>
            </Card>
          ))}
        </RatioRow>

        <RatioRow ratios={SMALL_CARD_RATIOS} gap={2} breakAt="md">
          <Card sx={infoCardSx}>
            <CardContent sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
              <Typography variant="subtitle1" gutterBottom sx={{
                fontWeight: 600
              }}>
                状态分布
              </Typography>
              <Box sx={{ flex: 1, minHeight: 200, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                {loading ? (
                  <Skeleton variant="circular" width={180} height={180} />
                ) : statusData.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie
                        data={statusData}
                        cx="50%"
                        cy="50%"
                        innerRadius={45}
                        outerRadius={70}
                        paddingAngle={5}
                        dataKey="value"
                        labelLine={true}
                        activeShape={renderActiveShape}
                        label={({ name, value, percent, cx, x, y }: PieLabelRenderProps) => {
                          const labelName = String(name ?? '')
                          const labelValue = Number(value ?? 0)
                          const percentage = Number(percent ?? 0)
                          const centerX = Number(cx ?? 0)
                          const labelX = Number(x ?? 0)
                          const labelY = Number(y ?? 0)
                          return (
                            <text x={labelX} y={labelY} fill="#666" textAnchor={labelX > centerX ? 'start' : 'end'} dominantBaseline="central">
                              <tspan x={labelX} dy="-0.5em" fontSize="10" fontWeight="bold">{labelName}</tspan>
                              <tspan x={labelX} dy="1.2em" fontSize="10">{labelValue} ({(percentage * 100).toFixed(0)}%)</tspan>
                            </text>
                          );
                        }}
                      >
                        {statusData.map((entry, index) => (
                          <Cell key={`cell-${index}`} fill={entry.color} />
                        ))}
                      </Pie>
                      <Tooltip />
                    </PieChart>
                  </ResponsiveContainer>
                ) : (
                  <Typography sx={{
                    color: "text.secondary"
                  }}>暂无数据</Typography>
                )}
              </Box>
            </CardContent>
          </Card>

          <Card sx={infoCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
              <Typography variant="subtitle1" sx={{
                fontWeight: 600
              }}>
                SLA 与风险
              </Typography>
              {loading ? (
                <Stack spacing={1.5}>
                  {[...Array(3)].map((_, index) => <Skeleton key={index} variant="rounded" height={28} />)}
                </Stack>
              ) : (
                <Stack spacing={1.5}>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" sx={{
                      color: "text.secondary"
                    }}>
                      SLA 违约
                    </Typography>
                    <Chip className="status" color="error" label={stats.sla_breached} onClick={() => handleNavigateToTickets({ sla_breached: true })} />
                  </Box>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" sx={{
                      color: "text.secondary"
                    }}>
                      逾期工单
                    </Typography>
                    <Chip className="status" color="warning" label={stats.overdue} onClick={() => handleNavigateToTickets({ is_overdue: true })} />
                  </Box>
                  <Box sx={rowSx}>
                    <Typography className="title" variant="body2" sx={{
                      color: "text.secondary"
                    }}>
                      解决率
                    </Typography>
                    <Typography className="status" variant="h6" sx={{
                      color: "success.main"
                    }}>
                      {stats.total > 0 ? Math.round((stats.resolved / stats.total) * 100) : 0}%
                    </Typography>
                  </Box>
                </Stack>
              )}
            </CardContent>
          </Card>

          <Card sx={infoCardSx}>
            <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
              <Typography variant="subtitle1" sx={{
                fontWeight: 600
              }}>
                团队关注
              </Typography>
              {loading ? (
                <Stack spacing={1.5}>
                  {[...Array(3)].map((_, index) => <Skeleton key={index} variant="rounded" height={28} />)}
                </Stack>
              ) : isAgent ? (
                <Stack spacing={1}>
                  <Typography variant="body2" sx={{
                    color: "text.secondary"
                  }}>
                    当前分配给你的工单
                  </Typography>
                  <Typography variant="h4" sx={{
                    color: "primary.main"
                  }}>
                    {stats.my_tickets}
                  </Typography>
                  <Button variant="contained" onClick={() => handleNavigateToTickets({ assigned_to_me: true })}>
                    查看我的工单
                  </Button>
                </Stack>
              ) : (
                <Stack spacing={1}>
                  <Typography variant="body2" sx={{
                    color: "text.secondary"
                  }}>
                    待分配工单
                  </Typography>
                  <Typography variant="h4" sx={{
                    color: "warning.main"
                  }}>
                    {stats.unassigned}
                  </Typography>
                  <Button
                    variant="contained"
                    color="warning"
                    onClick={() => handleNavigateToTickets({ unassigned: true })}
                  >
                    快速分配
                  </Button>
                </Stack>
              )}
            </CardContent>
          </Card>
        </RatioRow>

        <Card sx={{ ...bottomCardSx, minHeight: 400 }}>
          <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
            <Typography variant="h6" sx={{
              fontWeight: 600
            }}>
              运营快照
            </Typography>
            {loading ? (
              <Stack spacing={1.5}>
                <Skeleton variant="rounded" height={200} />
              </Stack>
            ) : <Box sx={{ ...scrollSectionSx, height: 300, width: '100%' }}>
              <ResponsiveContainer width="100%" height={300}>
                <BarChart data={snapshotMetrics} margin={{ top: 20, right: 30, left: 0, bottom: 5 }}>
                  <CartesianGrid strokeDasharray="3 3" vertical={false} />
                  <XAxis dataKey="label" fontSize={12} tick={{ fill: theme.palette.text.secondary }} />
                  <YAxis fontSize={12} tick={{ fill: theme.palette.text.secondary }} />
                  <Tooltip
                    cursor={{ fill: 'rgba(0,0,0,0.05)' }}
                    contentStyle={{ borderRadius: 8, border: 'none', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
                  />
                  <Bar dataKey="value" name="数值" radius={[8, 8, 0, 0]} barSize={32}>
                    {snapshotMetrics.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.fill} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </Box>
            }
          </CardContent>
        </Card>

        <Card sx={{ ...bottomCardSx, minHeight: 'auto' }}>
          <CardContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, flex: 1 }}>
            <Typography variant="h6" sx={{
              fontWeight: 600
            }}>
              工单动态
            </Typography>
            <RatioRow ratios={DYNAMIC_SECTION_RATIOS} gap={4} breakAt="sm" sx={scrollSectionSx}>
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
      </Stack>
    </Box>
  );
}

export default TicketDashboard
