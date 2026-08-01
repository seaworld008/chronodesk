import React from 'react'
import { Title } from 'react-admin'
import { useSearchParams } from 'react-router-dom'
import {
    Alert,
    Box,
    Button,
    Chip,
    CircularProgress,
    Divider,
    Drawer,
    FormControl,
    InputLabel,
    MenuItem,
    Paper,
    Select,
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
    getPlatformRoleLabel,
    parsePlatformRole,
} from '@/lib/accessControl'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    humanApiRoutes,
    type AdminAuditLog,
    type AdminAuditLogDetail,
    type AdminAuditLogPage,
} from '@/lib/generated/human-api'
import {
    ResizableMuiTable,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
    auditFiltersFromSearchParams,
    auditFiltersToQuery,
    auditFiltersToSearchParams,
    type AuditExplorerFilters,
} from './auditExplorerState'
import AuditExportDialog from './AuditExportDialog'

const columns: ResizableColumn[] = [
    { key: 'time', defaultWidth: 176, minWidth: 152, maxWidth: 240 },
    { key: 'actor', defaultWidth: 150, minWidth: 120, maxWidth: 240 },
    { key: 'role', defaultWidth: 136, minWidth: 116, maxWidth: 180 },
    { key: 'action', defaultWidth: 220, minWidth: 160, maxWidth: 360 },
    { key: 'method', defaultWidth: 92, minWidth: 80, maxWidth: 120 },
    { key: 'path', defaultWidth: 300, minWidth: 200, maxWidth: 520 },
    { key: 'result', defaultWidth: 140, minWidth: 120, maxWidth: 180 },
    { key: 'latency', defaultWidth: 112, minWidth: 96, maxWidth: 150 },
    { key: 'ip', defaultWidth: 144, minWidth: 120, maxWidth: 200 },
]

const maxAuditCursorHistory = 100

const auditActorType = (item: object): string =>
    'actor_type' in item && typeof item.actor_type === 'string'
        ? item.actor_type
        : ''

const isNonHumanAuditActor = (item: object): boolean => {
    const actorType = auditActorType(item)
    return actorType === 'system' || actorType === 'service_principal'
}

const auditRoleLabel = (
    item: AdminAuditLog | AdminAuditLogDetail,
): string => {
    const role = parsePlatformRole(item.platform_role)
    if (role) return getPlatformRoleLabel(role)
    return auditActorType(item) === 'system' ? '系统' : '—'
}

const validPage = (value: unknown): value is AdminAuditLogPage =>
    typeof value === 'object' &&
    value !== null &&
    'items' in value &&
    Array.isArray(value.items) &&
    value.items.every(
        (item) =>
            typeof item === 'object' &&
            item !== null &&
            typeof item.id === 'number' &&
            typeof item.created_at === 'string' &&
            typeof item.username === 'string' &&
            (parsePlatformRole(item.platform_role) !== null ||
                isNonHumanAuditActor(item)) &&
            typeof item.action === 'string' &&
            typeof item.method === 'string' &&
            typeof item.path === 'string' &&
            typeof item.status_code === 'number' &&
            typeof item.masked_ip === 'string' &&
            typeof item.latency_ms === 'number' &&
            typeof item.result === 'string',
    ) &&
    'next_cursor' in value &&
    typeof value.next_cursor === 'string' &&
    'has_more' in value &&
    typeof value.has_more === 'boolean' &&
    (value.has_more
        ? value.next_cursor.length > 0
        : value.next_cursor.length === 0)

const detailLine = (label: string, value?: string | number) => (
    <Box>
        <Typography variant="caption" color="text.secondary">
            {label}
        </Typography>
        <Typography sx={{ overflowWrap: 'anywhere', whiteSpace: 'pre-wrap' }}>
            {value === undefined || value === '' ? '—' : value}
        </Typography>
    </Box>
)

const PlatformAuditExplorer = () => {
    const [searchParams, setSearchParams] = useSearchParams()
    const filters = React.useMemo(
        () => auditFiltersFromSearchParams(searchParams),
        [searchParams],
    )
    const [draft, setDraft] = React.useState(filters)
    const [page, setPage] = React.useState<AdminAuditLogPage | null>(null)
    const [loading, setLoading] = React.useState(true)
    const [error, setError] = React.useState('')
    const [cursorHistory, setCursorHistory] = React.useState<string[]>([])
    const [selected, setSelected] = React.useState<AdminAuditLog | null>(null)
    const [detail, setDetail] = React.useState<AdminAuditLogDetail | null>(null)
    const [detailLoading, setDetailLoading] = React.useState(false)
    const [detailError, setDetailError] = React.useState('')
    const [retryNonce, setRetryNonce] = React.useState(0)
    const [exportOpen, setExportOpen] = React.useState(false)

    React.useEffect(() => setDraft(filters), [filters])

    React.useEffect(() => {
        if (filters.urlError) {
            setPage(null)
            setLoading(false)
            setError(filters.urlError)
            return
        }
        const controller = new AbortController()
        setLoading(true)
        setError('')
        void apiFetch<unknown>(
            humanApiRoutes.listPlatformAuditLogs(
                auditFiltersToQuery(filters),
            ),
            { signal: controller.signal },
        )
            .then((value) => {
                if (!validPage(value)) {
                    throw new Error('平台审计响应格式无效')
                }
                if (!controller.signal.aborted) setPage(value)
            })
            .catch((requestError: unknown) => {
                if (!controller.signal.aborted) {
                    setError(
                        localizedUnknownErrorMessage(
                            requestError,
                            '平台审计记录加载失败，请稍后重试',
                        ),
                    )
                }
            })
            .finally(() => {
                if (!controller.signal.aborted) setLoading(false)
            })
        return () => controller.abort()
    }, [filters, retryNonce])

    React.useEffect(() => {
        if (!selected) {
            setDetail(null)
            setDetailError('')
            return
        }
        const controller = new AbortController()
        setDetailLoading(true)
        setDetailError('')
        void apiFetch<AdminAuditLogDetail>(
            humanApiRoutes.getPlatformAuditLogDetail({
                auditLogID: selected.id,
            }),
            { signal: controller.signal },
        )
            .then((value) => {
                if (!controller.signal.aborted) setDetail(value)
            })
            .catch((requestError: unknown) => {
                if (!controller.signal.aborted) {
                    setDetailError(
                        localizedUnknownErrorMessage(
                            requestError,
                            '审计详情加载失败，请稍后重试',
                        ),
                    )
                }
            })
            .finally(() => {
                if (!controller.signal.aborted) setDetailLoading(false)
            })
        return () => controller.abort()
    }, [selected])

    const updateDraft = <K extends keyof AuditExplorerFilters>(
        key: K,
        value: AuditExplorerFilters[K],
    ) =>
        setDraft((current) => ({
            ...current,
            [key]: value,
            cursor: '',
            urlError: '',
        }))

    const applyFilters = (event: React.FormEvent) => {
        event.preventDefault()
        setCursorHistory([])
        setSearchParams(auditFiltersToSearchParams({ ...draft, cursor: '' }))
    }

    const clearFilters = () => {
        const cleared = auditFiltersFromSearchParams(new URLSearchParams())
        setDraft(cleared)
        setCursorHistory([])
        setSearchParams(new URLSearchParams())
    }

    const openNextPage = () => {
        if (!page?.has_more || !page.next_cursor) return
        setCursorHistory((history) =>
            [...history, filters.cursor].slice(-maxAuditCursorHistory),
        )
        setSearchParams(
            auditFiltersToSearchParams({
                ...filters,
                cursor: page.next_cursor ?? '',
            }),
        )
    }

    const openPreviousPage = () => {
        const previous = cursorHistory[cursorHistory.length - 1]
        if (previous === undefined) return
        setCursorHistory((history) => history.slice(0, -1))
        setSearchParams(
            auditFiltersToSearchParams({ ...filters, cursor: previous }),
        )
    }

    const changePageSize = (value: number) => {
        if (![25, 50, 100].includes(value)) return
        setCursorHistory([])
        setSearchParams(
            auditFiltersToSearchParams({
                ...filters,
                cursor: '',
                limit: value,
            }),
        )
    }

    return (
        <Box
            data-testid="platform-audit-page"
            sx={{ p: { xs: 2, md: 3 }, minWidth: 0 }}
        >
            <Title title="平台审计" />
            <Stack
                direction={{ xs: 'column', sm: 'row' }}
                spacing={1.5}
                sx={{
                    alignItems: { xs: 'stretch', sm: 'flex-start' },
                    justifyContent: 'space-between',
                    mb: 2,
                }}
            >
                <Box>
                    <Typography variant="h4" gutterBottom>
                        平台审计探索器
                    </Typography>
                    <Typography color="text.secondary">
                        只读查看平台治理操作。列表仅展示脱敏摘要，选择记录后按需读取脱敏详情。
                    </Typography>
                </Box>
                <Button
                    variant="outlined"
                    onClick={() => setExportOpen(true)}
                    disabled={Boolean(filters.urlError)}
                    sx={{ flexShrink: 0 }}
                >
                    导出 CSV
                </Button>
            </Stack>

            <Paper
                component="form"
                onSubmit={applyFilters}
                variant="outlined"
                sx={{ p: 2, mb: 2 }}
                aria-label="平台审计筛选"
            >
                <Stack
                    direction={{ xs: 'column', md: 'row' }}
                    spacing={1.5}
                    useFlexGap
                    sx={{ flexWrap: 'wrap' }}
                >
                    <TextField
                        size="small"
                        label="操作人"
                        value={draft.actor}
                        onChange={(event) =>
                            updateDraft('actor', event.target.value)
                        }
                    />
                    <FormControl size="small" sx={{ minWidth: 150 }}>
                        <InputLabel id="audit-role-label">平台角色</InputLabel>
                        <Select
                            labelId="audit-role-label"
                            label="平台角色"
                            value={draft.platformRole}
                            onChange={(event) =>
                                updateDraft(
                                    'platformRole',
                                    event.target
                                        .value as AuditExplorerFilters['platformRole'],
                                )
                            }
                        >
                            <MenuItem value="">全部角色</MenuItem>
                            <MenuItem value="platform_admin">平台管理员</MenuItem>
                            <MenuItem value="security_auditor">安全审计员</MenuItem>
                            <MenuItem value="emergency_operator">应急操作员</MenuItem>
                            <MenuItem value="member">普通成员</MenuItem>
                        </Select>
                    </FormControl>
                    <TextField
                        size="small"
                        label="操作"
                        value={draft.action}
                        onChange={(event) =>
                            updateDraft('action', event.target.value)
                        }
                    />
                    <FormControl size="small" sx={{ minWidth: 120 }}>
                        <InputLabel id="audit-method-label">方法</InputLabel>
                        <Select
                            labelId="audit-method-label"
                            label="方法"
                            value={draft.method}
                            onChange={(event) =>
                                updateDraft(
                                    'method',
                                    event.target
                                        .value as AuditExplorerFilters['method'],
                                )
                            }
                        >
                            <MenuItem value="">全部方法</MenuItem>
                            {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(
                                (method) => (
                                    <MenuItem key={method} value={method}>
                                        {method}
                                    </MenuItem>
                                ),
                            )}
                        </Select>
                    </FormControl>
                    <TextField
                        size="small"
                        label="资源路径前缀"
                        placeholder="/api/platform"
                        value={draft.pathPrefix}
                        onChange={(event) =>
                            updateDraft('pathPrefix', event.target.value)
                        }
                    />
                    <TextField
                        size="small"
                        label="HTTP 状态"
                        type="number"
                        slotProps={{ htmlInput: { min: 100, max: 599 } }}
                        value={draft.status}
                        onChange={(event) =>
                            updateDraft('status', event.target.value)
                        }
                    />
                    <FormControl size="small" sx={{ minWidth: 130 }}>
                        <InputLabel id="audit-result-label">结果</InputLabel>
                        <Select
                            labelId="audit-result-label"
                            label="结果"
                            value={draft.result}
                            onChange={(event) =>
                                updateDraft(
                                    'result',
                                    event.target
                                        .value as AuditExplorerFilters['result'],
                                )
                            }
                        >
                            <MenuItem value="">全部结果</MenuItem>
                            <MenuItem value="success">成功</MenuItem>
                            <MenuItem value="error">失败</MenuItem>
                            <MenuItem value="pending">处理中</MenuItem>
                        </Select>
                    </FormControl>
                    <TextField
                        size="small"
                        label="关键词"
                        value={draft.keyword}
                        onChange={(event) =>
                            updateDraft('keyword', event.target.value)
                        }
                    />
                    <FormControl size="small" sx={{ minWidth: 140 }}>
                        <InputLabel id="audit-time-label">时间范围</InputLabel>
                        <Select
                            labelId="audit-time-label"
                            label="时间范围"
                            value={draft.timePreset}
                            onChange={(event) =>
                                updateDraft(
                                    'timePreset',
                                    event.target
                                        .value as AuditExplorerFilters['timePreset'],
                                )
                            }
                        >
                            <MenuItem value="">自定义/全部</MenuItem>
                            <MenuItem value="1h">最近 1 小时</MenuItem>
                            <MenuItem value="24h">最近 24 小时</MenuItem>
                            <MenuItem value="7d">最近 7 天</MenuItem>
                            <MenuItem value="30d">最近 30 天</MenuItem>
                        </Select>
                    </FormControl>
                    {!draft.timePreset && (
                        <>
                            <TextField
                                size="small"
                                label="开始时间"
                                type="datetime-local"
                                slotProps={{ inputLabel: { shrink: true } }}
                                value={draft.startTime}
                                onChange={(event) =>
                                    updateDraft(
                                        'startTime',
                                        event.target.value,
                                    )
                                }
                            />
                            <TextField
                                size="small"
                                label="结束时间"
                                type="datetime-local"
                                slotProps={{ inputLabel: { shrink: true } }}
                                value={draft.endTime}
                                onChange={(event) =>
                                    updateDraft('endTime', event.target.value)
                                }
                            />
                        </>
                    )}
                    <Button type="submit" variant="contained">
                        应用筛选
                    </Button>
                    <Button type="button" onClick={clearFilters}>
                        清除
                    </Button>
                </Stack>
            </Paper>

            {error && (
                <Alert
                    severity="error"
                    action={
                        filters.urlError ? (
                            <Button color="inherit" onClick={clearFilters}>
                                清除无效筛选
                            </Button>
                        ) : (
                            <Button
                                color="inherit"
                                onClick={() =>
                                    setRetryNonce((value) => value + 1)
                                }
                            >
                                重试
                            </Button>
                        )
                    }
                    sx={{ mb: 2 }}
                >
                    {error}
                </Alert>
            )}
            {loading && (
                <Box
                    role="status"
                    aria-label="正在加载平台审计记录"
                    sx={{ display: 'grid', minHeight: 260, placeItems: 'center' }}
                >
                    <CircularProgress size={32} />
                </Box>
            )}
            {!loading && !error && page && (
                <>
                    <Typography color="text.secondary" sx={{ mb: 1 }}>
                        当前显示 {page.items.length} 条记录，按稳定游标翻页
                    </Typography>
                    <TableContainer component={Paper} variant="outlined">
                        <ResizableMuiTable
                            tableId="platform.audit.explorer"
                            columns={columns}
                            aria-label="平台审计记录列表"
                        >
                            <TableHead>
                                <TableRow>
                                    <TableCell>时间</TableCell>
                                    <TableCell>操作人</TableCell>
                                    <TableCell>角色</TableCell>
                                    <TableCell>操作</TableCell>
                                    <TableCell>方法</TableCell>
                                    <TableCell>资源路径</TableCell>
                                    <TableCell>状态/结果</TableCell>
                                    <TableCell>耗时</TableCell>
                                    <TableCell>来源 IP</TableCell>
                                </TableRow>
                            </TableHead>
                            <TableBody>
                                {page.items.length === 0 ? (
                                    <TableRow>
                                        <TableCell colSpan={9} align="center">
                                            暂无符合条件的平台审计记录
                                        </TableCell>
                                    </TableRow>
                                ) : (
                                    page.items.map((item) => (
                                        <TableRow
                                            key={item.id}
                                            hover
                                            tabIndex={0}
                                            role="button"
                                            aria-label={`查看 ${item.username} 在 ${new Date(item.created_at).toLocaleString('zh-CN')} 的审计详情`}
                                            onClick={() => setSelected(item)}
                                            onKeyDown={(event) => {
                                                if (
                                                    event.key === 'Enter' ||
                                                    event.key === ' '
                                                ) {
                                                    event.preventDefault()
                                                    setSelected(item)
                                                }
                                            }}
                                            sx={{ cursor: 'pointer' }}
                                        >
                                            <TableCell>
                                                {new Date(
                                                    item.created_at,
                                                ).toLocaleString('zh-CN')}
                                            </TableCell>
                                            <TableCell>{item.username}</TableCell>
                                            <TableCell>
                                                {auditRoleLabel(item)}
                                            </TableCell>
                                            <TableCell>{item.action}</TableCell>
                                            <TableCell>{item.method}</TableCell>
                                            <TableCell>{item.path}</TableCell>
                                            <TableCell>
                                                <Chip
                                                    size="small"
                                                    color={
                                                        item.result ===
                                                            'success'
                                                            ? 'success'
                                                            : item.result ===
                                                                'error'
                                                              ? 'error'
                                                              : 'default'
                                                    }
                                                    label={`${item.status_code || '—'} · ${item.result}`}
                                                />
                                            </TableCell>
                                            <TableCell>
                                                {item.latency_ms} ms
                                            </TableCell>
                                            <TableCell>
                                                {item.masked_ip || '—'}
                                            </TableCell>
                                        </TableRow>
                                    ))
                                )}
                            </TableBody>
                        </ResizableMuiTable>
                    </TableContainer>
                    <Stack
                        direction="row"
                        spacing={1}
                        sx={{
                            mt: 2,
                            alignItems: 'center',
                            justifyContent: 'flex-end',
                        }}
                    >
                        <FormControl size="small" sx={{ minWidth: 120 }}>
                            <InputLabel id="audit-page-size-label">
                                每页条数
                            </InputLabel>
                            <Select
                                labelId="audit-page-size-label"
                                label="每页条数"
                                value={filters.limit}
                                onChange={(event) =>
                                    changePageSize(
                                        Number(event.target.value),
                                    )
                                }
                            >
                                {[25, 50, 100].map((size) => (
                                    <MenuItem key={size} value={size}>
                                        {size} 条
                                    </MenuItem>
                                ))}
                            </Select>
                        </FormControl>
                        <Button
                            disabled={cursorHistory.length === 0}
                            onClick={openPreviousPage}
                        >
                            上一页
                        </Button>
                        <Button
                            variant="outlined"
                            disabled={!page.has_more}
                            onClick={openNextPage}
                        >
                            下一页
                        </Button>
                    </Stack>
                </>
            )}

            <Drawer
                anchor="right"
                open={selected !== null}
                onClose={() => setSelected(null)}
                slotProps={{
                    paper: {
                        sx: { width: { xs: '100%', sm: 520 }, p: 3 },
                        'aria-label': '平台审计详情',
                    },
                }}
            >
                <Stack
                    direction="row"
                    sx={{ justifyContent: 'space-between' }}
                >
                    <Typography variant="h5">审计详情</Typography>
                    <Button onClick={() => setSelected(null)} autoFocus>
                        关闭
                    </Button>
                </Stack>
                <Divider sx={{ my: 2 }} />
                {detailLoading && (
                    <Box role="status" aria-label="正在加载审计详情">
                        <CircularProgress size={28} />
                    </Box>
                )}
                {detailError && <Alert severity="error">{detailError}</Alert>}
                {!detailLoading && detail && (
                    <Stack spacing={2}>
                        {detailLine('操作人', detail.username)}
                        {detailLine(
                            '平台角色',
                            auditRoleLabel(detail),
                        )}
                        {detailLine('操作', detail.action)}
                        {detailLine(
                            '请求',
                            `${detail.method} ${detail.path}`,
                        )}
                        {detailLine(
                            '状态/结果',
                            `${detail.status_code} · ${detail.result}`,
                        )}
                        {detailLine('脱敏来源 IP', detail.masked_ip)}
                        {detailLine('查询参数（已脱敏）', detail.query)}
                        {detailLine(
                            'User-Agent（已脱敏）',
                            detail.user_agent,
                        )}
                        {detailLine('备注（已脱敏）', detail.notes)}
                        {detailLine('Request ID', detail.request_id)}
                        {detailLine('Trace ID', detail.trace_id)}
                        {detailLine(
                            'Correlation ID',
                            detail.correlation_id,
                        )}
                    </Stack>
                )}
            </Drawer>
            <AuditExportDialog
                open={exportOpen}
                filters={filters}
                onClose={() => setExportOpen(false)}
            />
        </Box>
    )
}

export default PlatformAuditExplorer
