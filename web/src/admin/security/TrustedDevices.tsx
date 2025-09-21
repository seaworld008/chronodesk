import { useCallback, useEffect, useState } from 'react'
import {
    Box,
    Button,
    Card,
    CardContent,
    CardHeader,
    Chip,
    CircularProgress,
    Stack,
    Typography,
} from '@mui/material'
import SecurityIcon from '@mui/icons-material/Security'
import { useNotify } from 'react-admin'

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

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')
const buildUrl = (path: string) => `${apiBase}${path.startsWith('/') ? path : `/${path}`}`

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

    const fetchDevices = useCallback(async () => {
        const token = localStorage.getItem('token')
        if (!token) {
            return
        }
        setLoading(true)
        try {
            const response = await fetch(buildUrl('/user/trusted-devices'), {
                headers: {
                    Authorization: `Bearer ${token}`,
                },
            })
            if (!response.ok) {
                throw new Error('无法获取可信设备列表')
            }
            const result = await response.json()
            setDevices(result.data ?? [])
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

    const handleRevoke = async (deviceId: number) => {
        const token = localStorage.getItem('token')
        if (!token) {
            notify('未检测到登录令牌，请重新登录', { type: 'warning' })
            return
        }
        try {
            const response = await fetch(buildUrl(`/user/trusted-devices/${deviceId}`), {
                method: 'DELETE',
                headers: {
                    Authorization: `Bearer ${token}`,
                },
            })
            if (!response.ok) {
                const payload = await response.json().catch(() => ({}))
                throw new Error(payload?.msg || '撤销失败')
            }
            notify('设备已撤销', { type: 'info' })
            setRefreshFlag((flag) => flag + 1)
        } catch (error) {
            console.error(error)
            notify(error instanceof Error ? error.message : '撤销失败', { type: 'warning' })
        }
    }

    if (loading) {
        return (
            <Box display="flex" alignItems="center" justifyContent="center" minHeight="60vh">
                <CircularProgress />
            </Box>
        )
    }

    return (
        <Box padding={3} display="flex" justifyContent="center">
            <Card sx={{ maxWidth: 960, width: '100%' }}>
                <CardHeader
                    avatar={<SecurityIcon color="primary" />}
                    title="可信设备管理"
                    subheader="查看并管理已记住的登陆设备"
                />
                <CardContent>
                    {devices.length === 0 ? (
                        <Typography color="text.secondary">暂无可信设备记录。</Typography>
                    ) : (
                        <Stack spacing={2.5}>
                            {devices.map((device) => (
                                <Card key={device.id} variant="outlined">
                                    <CardContent>
                                        <Stack spacing={1.2}>
                                            <Typography variant="h6">{device.device_name || '未命名设备'}</Typography>
                                            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                                                <Chip label={`最近使用：${formatDateTime(device.last_used_at)}`} />
                                                <Chip label={`IP：${device.last_ip || '未知'}`} />
                                                <Chip label={device.revoked ? '已撤销' : '生效中'} color={device.revoked ? 'default' : 'success'} />
                                                <Chip label={`到期：${formatDateTime(device.expires_at)}`} />
                                            </Stack>
                                            <Typography variant="body2" color="text.secondary">
                                                User-Agent: {device.user_agent || '—'}
                                            </Typography>
                                            {!device.revoked && (
                                                <Box>
                                                    <Button
                                                        variant="outlined"
                                                        color="error"
                                                        onClick={() => handleRevoke(device.id)}
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
        </Box>
    )
}

export default TrustedDevices
