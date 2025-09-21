import { useState, FormEvent } from 'react'
import {
    Box,
    Button,
    Card,
    CardContent,
    CardHeader,
    Checkbox,
    CircularProgress,
    FormControlLabel,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import { Notification, useLogin, useNotify } from 'react-admin'

type NavigatorWithUAData = Navigator & {
    userAgentData?: {
        platform?: string
    }
}

const getDefaultDeviceName = (): string => {
    if (typeof navigator !== 'undefined') {
        const enhancedNavigator = navigator as NavigatorWithUAData
        const platformFromUA = enhancedNavigator.userAgentData?.platform
        if (platformFromUA) {
            return platformFromUA
        }
        if (enhancedNavigator.platform) {
            return enhancedNavigator.platform
        }
    }
    return '当前设备'
}

const storedPreference = () => {
    if (typeof window === 'undefined') {
        return false
    }
    return localStorage.getItem('rememberDevicePreference') === 'true'
}

const LoginPage = () => {
    const login = useLogin()
    const notify = useNotify()

    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [otpCode, setOtpCode] = useState('')
    const [rememberDevice, setRememberDevice] = useState(storedPreference)
    const [deviceName, setDeviceName] = useState(getDefaultDeviceName())
    const [submitting, setSubmitting] = useState(false)
    const [error, setError] = useState<string | null>(null)

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        setSubmitting(true)
        setError(null)

        try {
            await login({
                username: email,
                password,
                remember: rememberDevice,
                rememberDevice,
                otpCode: otpCode || undefined,
                deviceName: rememberDevice ? deviceName : undefined,
            })
            notify('登录成功', { type: 'info' })
        } catch (err) {
            const message = err instanceof Error ? err.message : '登录失败'
            setError(message)
            notify(message, { type: 'warning' })
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <Box
            sx={{
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
                minHeight: '100vh',
                backgroundColor: 'background.default',
                padding: 2,
            }}
        >
            <Notification />
            <Card sx={{ maxWidth: 420, width: '100%' }}>
                <CardHeader
                    title="工单管理系统"
                    subheader="请输入账号密码登录"
                    sx={{ textAlign: 'center' }}
                />
                <CardContent>
                    <Box component="form" onSubmit={handleSubmit} noValidate>
                        <Stack spacing={2.5}>
                            <TextField
                                label="邮箱"
                                type="email"
                                value={email}
                                onChange={(event) => setEmail(event.target.value)}
                                required
                                fullWidth
                                autoComplete="email"
                                autoFocus
                            />
                            <TextField
                                label="密码"
                                type="password"
                                value={password}
                                onChange={(event) => setPassword(event.target.value)}
                                required
                                fullWidth
                                autoComplete="current-password"
                            />
                            <TextField
                                label="OTP 验证码（如开启双因子）"
                                value={otpCode}
                                onChange={(event) => setOtpCode(event.target.value)}
                                fullWidth
                                placeholder="无需时可留空"
                                inputProps={{ maxLength: 10 }}
                            />
                            <FormControlLabel
                                control={
                                    <Checkbox
                                        checked={rememberDevice}
                                        onChange={(event) => setRememberDevice(event.target.checked)}
                                        color="primary"
                                    />
                                }
                                label="记住此设备（跳过后续 OTP 验证）"
                            />
                            <TextField
                                label="设备名称"
                                value={deviceName}
                                onChange={(event) => setDeviceName(event.target.value)}
                                fullWidth
                                disabled={!rememberDevice}
                                helperText="用于在安全中心识别此设备"
                            />

                            {error && (
                                <Typography variant="body2" color="error">
                                    {error}
                                </Typography>
                            )}

                            <Button
                                type="submit"
                                variant="contained"
                                color="primary"
                                disabled={submitting}
                                size="large"
                            >
                                {submitting ? <CircularProgress size={24} /> : '登录'}
                            </Button>
                        </Stack>
                    </Box>
                </CardContent>
            </Card>
        </Box>
    )
}

export default LoginPage
