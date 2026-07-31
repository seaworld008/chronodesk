import { useCallback, useEffect, useState } from 'react'
import { History as HistoryIcon, Refresh as RefreshIcon } from '@mui/icons-material'
import {
    Alert,
    Box,
    Button,
    Chip,
    CircularProgress,
    IconButton,
    Paper,
    Stack,
    TableBody,
    TableCell,
    TableHead,
    TablePagination,
    TableRow,
    Tooltip,
    Typography,
} from '@mui/material'
import { Title } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
    ResizableMuiTable,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'

interface LoginHistoryRecord {
    id: number
    ip_address: string
    login_time: string
    login_status: string
    login_method: string
    failure_reason?: string
    location: string
    device_info: string
    session_duration: string
    is_current_session: boolean
    is_active: boolean
}

interface LoginHistoryPage {
    items: LoginHistoryRecord[]
    total: number
    page: number
    page_size: number
}

const columns: ResizableColumn[] = [
    { key: 'time', defaultWidth: 190, minWidth: 150, maxWidth: 300 },
    { key: 'status', defaultWidth: 150, minWidth: 120, maxWidth: 220 },
    { key: 'device', defaultWidth: 300, minWidth: 180, maxWidth: 520 },
    { key: 'ip', defaultWidth: 160, minWidth: 120, maxWidth: 240 },
    { key: 'location', defaultWidth: 220, minWidth: 140, maxWidth: 360 },
    { key: 'duration', defaultWidth: 150, minWidth: 120, maxWidth: 220 },
]

const dateTime = (value: string) => {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString('zh-CN')
}

const statusLabel = (record: LoginHistoryRecord) => {
    if (record.is_current_session) return '当前会话'
    if (record.login_status === 'success') return record.is_active ? '活跃' : '成功'
    if (record.login_status === 'blocked') return '已阻止'
    if (record.login_status === 'suspended') return '已暂停'
    return '失败'
}

const LoginHistory = () => {
    const [page, setPage] = useState<LoginHistoryPage | null>(null)
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState('')
    const [pageIndex, setPageIndex] = useState(0)
    const [pageSize, setPageSize] = useState(20)

    const load = useCallback(async () => {
        setLoading(true)
        setError('')
        try {
            const result = await apiFetch<LoginHistoryPage>(
                `/user/login-history?page=${pageIndex + 1}&page_size=${pageSize}&order_by=login_time&order=desc`,
            )
            setPage({ ...result, items: result.items ?? [] })
        } catch (requestError) {
            setError(localizedUnknownErrorMessage(requestError, '登录历史加载失败'))
        } finally {
            setLoading(false)
        }
    }, [pageIndex, pageSize])

    useEffect(() => {
        void load()
    }, [load])

    return (
        <Box sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="登录历史" />
            <Paper sx={{ p: { xs: 2, md: 3 } }}>
                <Stack
                    direction="row"
                    spacing={1}
                    sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 2 }}
                >
                    <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                        <HistoryIcon color="primary" />
                        <Box>
                            <Typography variant="h4">登录历史</Typography>
                            <Typography color="text.secondary">
                                最近 {page?.items.length ?? 0} 条记录（共 {page?.total ?? 0} 条）
                            </Typography>
                        </Box>
                    </Stack>
                    <Tooltip title="刷新">
                        <span>
                            <IconButton
                                aria-label="刷新登录历史"
                                disabled={loading}
                                onClick={() => void load()}
                            >
                                <RefreshIcon />
                            </IconButton>
                        </span>
                    </Tooltip>
                </Stack>

                {error && (
                    <Alert
                        severity="error"
                        sx={{ mb: 2 }}
                        action={<Button onClick={() => void load()}>重试</Button>}
                    >
                        {error}
                    </Alert>
                )}
                {loading && !page ? (
                    <Box role="status" sx={{ py: 8, textAlign: 'center' }}>
                        <CircularProgress aria-label="正在加载登录历史" size={30} />
                    </Box>
                ) : (
                    <Box sx={{ overflowX: 'auto' }}>
                        <ResizableMuiTable
                            tableId="account.login-history"
                            columns={columns}
                            size="small"
                            aria-label="登录历史列表"
                        >
                            <TableHead>
                                <TableRow>
                                    <TableCell>登录时间</TableCell>
                                    <TableCell>状态 / 方式</TableCell>
                                    <TableCell>设备</TableCell>
                                    <TableCell>IP 地址</TableCell>
                                    <TableCell>位置</TableCell>
                                    <TableCell>会话时长</TableCell>
                                </TableRow>
                            </TableHead>
                            <TableBody>
                                {(page?.items ?? []).map((record) => (
                                    <TableRow key={record.id} hover>
                                        <TableCell>{dateTime(record.login_time)}</TableCell>
                                        <TableCell>
                                            <Stack direction="row" spacing={0.5}>
                                                <Chip
                                                    size="small"
                                                    label={statusLabel(record)}
                                                    color={
                                                        record.is_current_session || record.is_active
                                                            ? 'success'
                                                            : record.login_status === 'success'
                                                              ? 'default'
                                                              : 'error'
                                                    }
                                                    variant="outlined"
                                                />
                                                <Chip
                                                    size="small"
                                                    label={record.login_method || '未知方式'}
                                                    variant="outlined"
                                                />
                                            </Stack>
                                        </TableCell>
                                        <TableCell>
                                            <TruncatedText title={record.device_info || '未知设备'}>
                                                {record.device_info || '未知设备'}
                                            </TruncatedText>
                                        </TableCell>
                                        <TableCell>{record.ip_address || '—'}</TableCell>
                                        <TableCell>
                                            <TruncatedText title={record.location || '未知位置'}>
                                                {record.location || '未知位置'}
                                            </TruncatedText>
                                        </TableCell>
                                        <TableCell>{record.session_duration || '—'}</TableCell>
                                    </TableRow>
                                ))}
                                {(page?.items.length ?? 0) === 0 && (
                                    <TableRow>
                                        <TableCell colSpan={6} align="center" sx={{ py: 6 }}>
                                            暂无登录历史。
                                        </TableCell>
                                    </TableRow>
                                )}
                            </TableBody>
                        </ResizableMuiTable>
                        <TablePagination
                            component="div"
                            count={page?.total ?? 0}
                            page={pageIndex}
                            onPageChange={(_, nextPage) => setPageIndex(nextPage)}
                            rowsPerPage={pageSize}
                            onRowsPerPageChange={(event) => {
                                setPageSize(Number(event.target.value))
                                setPageIndex(0)
                            }}
                            rowsPerPageOptions={[10, 20, 50, 100]}
                            labelRowsPerPage="每页记录数"
                            labelDisplayedRows={({ from, to, count }) =>
                                `${from}–${to} / ${count}`
                            }
                            showFirstButton
                            showLastButton
                        />
                    </Box>
                )}
            </Paper>
        </Box>
    )
}

export default LoginHistory
