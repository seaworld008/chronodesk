import { useCallback, useEffect, useState } from 'react'
import {
    Box,
    Button,
    Card,
    CardContent,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    IconButton,
    Stack,
    Tooltip,
    Typography,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useNotify } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import PageShell from '@/components/layout/PageShell'
import AccountPageHeader from './AccountPageHeader'

interface TrustedDevice {
    id: number
    device_name: string
    last_used_at: string
    last_ip: string
    user_agent: string
    expires_at: string
    revoked: boolean
    created_at: string
    updated_at: string
}

const formatDateTime = (value: string) => {
    if (!value) return '—'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) {
        return value
    }
    return date.toLocaleString('zh-CN')
}

const TrustedDevices = () => {
    const notify = useNotify()
    const [devices, setDevices] = useState<TrustedDevice[]>([])
    const [loading, setLoading] = useState(true)
    const [refreshFlag, setRefreshFlag] = useState(0)
    const [revokeTarget, setRevokeTarget] = useState<TrustedDevice | null>(null)
    const [revoking, setRevoking] = useState(false)

    const fetchDevices = useCallback(async () => {
        setLoading(true)
        try {
            const result = await apiFetch<TrustedDevice[]>('/user/trusted-devices')
            setDevices(result ?? [])
        } catch (error) {
            console.error(error)
            notify('获取可信设备失败，请稍后重试', { type: 'warning' })
        } finally {
            setLoading(false)
        }
    }, [notify])

    useEffect(() => {
        fetchDevices()
    }, [fetchDevices, refreshFlag])

    const handleRevoke = async () => {
        if (!revokeTarget) return
        setRevoking(true)
        try {
            await apiFetch(`/user/trusted-devices/${revokeTarget.id}`, {
                method: 'DELETE',
            })
            notify('设备已撤销', { type: 'info' })
            setRevokeTarget(null)
            setRefreshFlag((flag) => flag + 1)
        } catch (error) {
            console.error(error)
            notify(localizedUnknownErrorMessage(error, '撤销失败'), { type: 'warning' })
        } finally {
            setRevoking(false)
        }
    }

    return (
        <PageShell
            title="可信设备"
            testId="account-page-shell"
        >
            <AccountPageHeader
                title="可信设备"
                description="查看并管理已记住的登录设备。"
                action={(
                    <Tooltip title="刷新">
                        <span>
                            <IconButton
                                aria-label="刷新可信设备"
                                disabled={loading}
                                onClick={() => setRefreshFlag((flag) => flag + 1)}
                            >
                                <RefreshIcon />
                            </IconButton>
                        </span>
                    </Tooltip>
                )}
            />
            <Card sx={{ maxWidth: 960, mt: 3, width: '100%' }}>
                <CardContent>
                    {loading ? (
                        <Box
                            role="status"
                            sx={{
                                display: 'grid',
                                minHeight: 240,
                                placeItems: 'center',
                            }}
                        >
                            <CircularProgress aria-label="正在加载可信设备" />
                        </Box>
                    ) : devices.length === 0 ? (
                        <Typography sx={{
                            color: "text.secondary"
                        }}>暂无可信设备记录。</Typography>
                    ) : (
                        <Stack spacing={2.5}>
                            {devices.map((device) => (
                                <Card
                                    key={device.id}
                                    variant="outlined"
                                    role="region"
                                    aria-label={`可信设备：${device.device_name || '未命名设备'}`}
                                >
                                    <CardContent>
                                        <Stack spacing={1.2}>
                                            <Typography variant="h6">{device.device_name || '未命名设备'}</Typography>
                                            <Stack direction="row" spacing={1} useFlexGap sx={{
                                                flexWrap: "wrap"
                                            }}>
                                                <Chip label={`最近使用：${formatDateTime(device.last_used_at)}`} />
                                                <Chip label={`IP：${device.last_ip || '未知'}`} />
                                                <Chip label={device.revoked ? '已撤销' : '生效中'} color={device.revoked ? 'default' : 'success'} />
                                                <Chip label={`到期：${formatDateTime(device.expires_at)}`} />
                                            </Stack>
                                            <Typography variant="body2" sx={{
                                                color: "text.secondary"
                                            }}>
                                                浏览器标识（User-Agent）：{device.user_agent || '—'}
                                            </Typography>
                                            {!device.revoked && (
                                                <Box>
                                                    <Button
                                                        variant="outlined"
                                                        color="error"
                                                        onClick={() => setRevokeTarget(device)}
                                                    >
                                                        撤销该设备
                                                    </Button>
                                                </Box>
                                            )}
                                        </Stack>
                                    </CardContent>
                                </Card>
                            ))}
                        </Stack>
                    )}
                </CardContent>
            </Card>
            <Dialog
                open={Boolean(revokeTarget)}
                onClose={() => {
                    if (!revoking) setRevokeTarget(null)
                }}
                maxWidth="xs"
                fullWidth
            >
                <DialogTitle>撤销可信设备</DialogTitle>
                <DialogContent>
                    撤销“{revokeTarget?.device_name || '未命名设备'}”后，该设备需要重新验证身份。确定继续吗？
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setRevokeTarget(null)} disabled={revoking}>
                        取消
                    </Button>
                    <Button
                        color="error"
                        variant="contained"
                        onClick={() => void handleRevoke()}
                        disabled={revoking}
                    >
                        {revoking ? '撤销中…' : '确认撤销'}
                    </Button>
                </DialogActions>
            </Dialog>
        </PageShell>
    );
}

export default TrustedDevices
