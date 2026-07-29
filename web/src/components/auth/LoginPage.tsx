import { useState, FormEvent } from 'react'
import {
    Box,
    Button,
    Card,
    CardContent,
    Checkbox,
    CircularProgress,
    FormControlLabel,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import { useLogin, useNotify } from 'react-admin'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'

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
            const message = localizedUnknownErrorMessage(err, '登录失败')
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
                background: 'linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%)',
                position: 'relative',
                overflow: 'hidden',
                '&::before': {
                    content: '""',
                    position: 'absolute',
                    top: '-10%',
                    left: '-10%',
                    width: '50%',
                    height: '50%',
                    background: 'radial-gradient(circle, rgba(37, 99, 235, 0.1) 0%, rgba(255,255,255,0) 70%)',
                    borderRadius: '50%',
                    filter: 'blur(60px)',
                },
                '&::after': {
                    content: '""',
                    position: 'absolute',
                    bottom: '-10%',
                    right: '-10%',
                    width: '50%',
                    height: '50%',
                    background: 'radial-gradient(circle, rgba(79, 70, 229, 0.1) 0%, rgba(255,255,255,0) 70%)',
                    borderRadius: '50%',
                    filter: 'blur(60px)',
                },
            }}
        >
            <Card sx={{
                maxWidth: 440,
                width: '100%',
                mx: 2,
                borderRadius: 4,
                boxShadow: '0 8px 32px rgba(0, 0, 0, 0.08)',
                backdropFilter: 'blur(20px)',
                backgroundColor: 'rgba(255, 255, 255, 0.9)',
                border: '1px solid rgba(255, 255, 255, 0.5)',
            }}>
                <Box sx={{ pt: 6, pb: 2, textAlign: 'center' }}>
                    <Box
                        sx={{
                            width: 48,
                            height: 48,
                            borderRadius: 2,
                            background: 'linear-gradient(135deg, #2563eb 0%, #4f46e5 100%)',
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            margin: '0 auto 16px',
                            color: 'white',
                            fontWeight: 'bold',
                            fontSize: 24,
                            boxShadow: '0 4px 12px rgba(37, 99, 235, 0.3)',
                        }}
                    >
                        T
                    </Box>
                    <Typography
                        variant="h5"
                        gutterBottom
                        sx={{
                            fontWeight: 700,
                            color: "#1e293b"
                        }}>
                        ChronoDesk 工单自动化平台
                    </Typography>
                    <Typography variant="body2" sx={{
                        color: "text.secondary"
                    }}>
                        欢迎回来，请登录您的账号
                    </Typography>
                </Box>
                <CardContent sx={{ px: 4, pb: 6 }}>
                    <Box component="form" onSubmit={handleSubmit} noValidate>
                        <Stack spacing={3}>
                            <TextField
                                label="邮箱"
                                type="email"
                                value={email}
                                onChange={(event) => setEmail(event.target.value)}
                                required
                                fullWidth
                                autoComplete="email"
                                autoFocus
                                variant="outlined"
                                sx={{
                                    '& .MuiOutlinedInput-root': {
                                        borderRadius: 2,
                                        backgroundColor: 'rgba(255, 255, 255, 0.5)',
                                    }
                                }}
                            />
                            <TextField
                                label="密码"
                                type="password"
                                value={password}
                                onChange={(event) => setPassword(event.target.value)}
                                required
                                fullWidth
                                autoComplete="current-password"
                                variant="outlined"
                                sx={{
                                    '& .MuiOutlinedInput-root': {
                                        borderRadius: 2,
                                        backgroundColor: 'rgba(255, 255, 255, 0.5)',
                                    }
                                }}
                            />
                            <TextField
                                label="OTP 验证码"
                                value={otpCode}
                                onChange={(event) => setOtpCode(event.target.value)}
                                fullWidth
                                placeholder="如开启双因子认证请填写"
                                slotProps={{ htmlInput: { maxLength: 10 } }}
                                variant="outlined"
                                sx={{
                                    '& .MuiOutlinedInput-root': {
                                        borderRadius: 2,
                                        backgroundColor: 'rgba(255, 255, 255, 0.5)',
                                    }
                                }}
                            />

                            <Box sx={{
                                p: 2,
                                borderRadius: 2,
                                bgcolor: 'rgba(241, 245, 249, 0.5)',
                                border: '1px solid rgba(226, 232, 240, 0.8)'
                            }}>
                                <FormControlLabel
                                    control={
                                        <Checkbox
                                            checked={rememberDevice}
                                            onChange={(event) => setRememberDevice(event.target.checked)}
                                            color="primary"
                                            size="small"
                                        />
                                    }
                                    label={<Typography variant="body2" sx={{
                                        color: "text.secondary"
                                    }}>记住此设备（免 OTP）</Typography>}
                                />
                                {rememberDevice && (
                                    <TextField
                                        size="small"
                                        placeholder="设备名称，例如：MacBook Pro"
                                        value={deviceName}
                                        onChange={(event) => setDeviceName(event.target.value)}
                                        fullWidth
                                        sx={{ mt: 1, '& .MuiOutlinedInput-root': { bgcolor: 'white' } }}
                                    />
                                )}
                            </Box>

                            {error && (
                                <Box sx={{
                                    p: 1.5,
                                    borderRadius: 2,
                                    bgcolor: 'error.lighter',
                                    color: 'error.main',
                                    display: 'flex',
                                    alignItems: 'center',
                                    gap: 1
                                }}>
                                    <Typography variant="body2" sx={{
                                        fontWeight: 500
                                    }}>
                                        {error}
                                    </Typography>
                                </Box>
                            )}

                            <Button
                                type="submit"
                                variant="contained"
                                disabled={submitting}
                                size="large"
                                sx={{
                                    py: 1.5,
                                    borderRadius: 2,
                                    fontSize: '1rem',
                                    fontWeight: 600,
                                    textTransform: 'none',
                                    background: 'linear-gradient(135deg, #2563eb 0%, #4f46e5 100%)',
                                    boxShadow: '0 4px 12px rgba(37, 99, 235, 0.2)',
                                    '&:hover': {
                                        background: 'linear-gradient(135deg, #1d4ed8 0%, #4338ca 100%)',
                                        boxShadow: '0 6px 16px rgba(37, 99, 235, 0.3)',
                                    }
                                }}
                            >
                                {submitting ? <CircularProgress size={24} color="inherit" /> : '登录系统'}
                            </Button>
                        </Stack>
                    </Box>
                </CardContent>
            </Card>
        </Box>
    );
}

export default LoginPage
