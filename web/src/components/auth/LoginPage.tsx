import { useState, type FormEvent } from 'react'
import {
    Alert,
    Box,
    Button,
    Checkbox,
    CircularProgress,
    Divider,
    FormControlLabel,
    IconButton,
    InputAdornment,
    Link,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import {
    VisibilityOffOutlined,
    VisibilityOutlined,
} from '@mui/icons-material'
import { useAuthProvider, useNotify } from 'react-admin'
import { useQueryClient } from '@tanstack/react-query'
import {
    Link as RouterLink,
    useLocation,
    useNavigate,
} from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import { markHumanAuthQueryAuthenticated } from '@/lib/authQueryState'
import PublicAuthShell from './PublicAuthShell'

type NavigatorWithUAData = Navigator & {
    userAgentData?: {
        platform?: string
    }
}

const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/u

const validateEmail = (email: string): string | null => {
    if (email.length === 0) {
        return '请输入邮箱'
    }
    if (!emailPattern.test(email)) {
        return '请输入有效的邮箱地址'
    }
    return null
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

const afterLoginPath = (state: unknown): string => {
    if (typeof state !== 'object' || state === null) {
        return '/'
    }
    const nextPathname =
        'nextPathname' in state && typeof state.nextPathname === 'string'
            ? state.nextPathname
            : ''
    if (!nextPathname.startsWith('/') || nextPathname.startsWith('//')) {
        return '/'
    }
    const nextSearch =
        'nextSearch' in state &&
        typeof state.nextSearch === 'string' &&
        state.nextSearch.startsWith('?')
            ? state.nextSearch
            : ''
    return `${nextPathname}${nextSearch}`
}

const LoginPage = () => {
    const authProvider = useAuthProvider()
    const notify = useNotify()
    const queryClient = useQueryClient()
    const location = useLocation()
    const navigate = useNavigate()

    const [email, setEmail] = useState('')
    const [password, setPassword] = useState('')
    const [showPassword, setShowPassword] = useState(false)
    const [otpCode, setOtpCode] = useState('')
    const [rememberDevice, setRememberDevice] = useState(storedPreference)
    const [deviceName, setDeviceName] = useState(getDefaultDeviceName())
    const [submitting, setSubmitting] = useState(false)
    const [error, setError] = useState<string | null>(null)
    const [emailError, setEmailError] = useState<string | null>(null)
    const [passwordError, setPasswordError] = useState<string | null>(null)

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        setError(null)

        const normalizedEmail = email.trim()
        const nextEmailError = validateEmail(normalizedEmail)
        const nextPasswordError = password.length === 0 ? '请输入密码' : null
        setEmail(normalizedEmail)
        setEmailError(nextEmailError)
        setPasswordError(nextPasswordError)
        if (nextEmailError || nextPasswordError) {
            return
        }

        setSubmitting(true)
        try {
            if (!authProvider) {
                throw new Error('认证服务尚未就绪，请稍后重试')
            }
            await authProvider.login({
                username: normalizedEmail,
                password,
                remember: rememberDevice,
                rememberDevice,
                otpCode: otpCode || undefined,
                deviceName: rememberDevice ? deviceName : undefined,
            })
            markHumanAuthQueryAuthenticated(queryClient)
            void queryClient.invalidateQueries({
                queryKey: ['auth', 'getPermissions'],
            })
            navigate(afterLoginPath(location.state), { replace: true })
            notify('登录成功', { type: 'info' })
        } catch (err) {
            const message = localizedUnknownErrorMessage(err, '登录失败')
            setError(message)
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="欢迎回来"
            description="登录以继续处理可信任务流"
            contentWidth={468}
        >
            <Stack
                component="form"
                spacing={2}
                onSubmit={handleSubmit}
                noValidate
            >
                <TextField
                    label="邮箱"
                    type="email"
                    value={email}
                    onChange={(event) => {
                        setEmail(event.target.value)
                        setEmailError(null)
                    }}
                    error={emailError !== null}
                    helperText={emailError}
                    required
                    fullWidth
                    autoComplete="email"
                    autoFocus
                />

                <Box>
                    <TextField
                        id="chronodesk-login-password"
                        label="密码"
                        type={showPassword ? 'text' : 'password'}
                        value={password}
                        onChange={(event) => {
                            setPassword(event.target.value)
                            setPasswordError(null)
                        }}
                        error={passwordError !== null}
                        helperText={passwordError}
                        required
                        fullWidth
                        autoComplete="current-password"
                        slotProps={{
                            input: {
                                endAdornment: (
                                    <InputAdornment position="end">
                                        <IconButton
                                            aria-label={
                                                showPassword
                                                    ? '隐藏已输入内容'
                                                    : '显示已输入内容'
                                            }
                                            aria-controls={
                                                'chronodesk-login-password'
                                            }
                                            edge="end"
                                            onClick={() =>
                                                setShowPassword(
                                                    (current) => !current,
                                                )
                                            }
                                        >
                                            {showPassword ? (
                                                <VisibilityOffOutlined />
                                            ) : (
                                                <VisibilityOutlined />
                                            )}
                                        </IconButton>
                                    </InputAdornment>
                                ),
                            },
                        }}
                    />
                    <Box sx={{ mt: 0.75, textAlign: 'right' }}>
                        <Link
                            component={RouterLink}
                            to="/forgot-password"
                            underline="hover"
                            sx={{ fontSize: 13, fontWeight: 600 }}
                        >
                            忘记密码？
                        </Link>
                    </Box>
                </Box>

                <TextField
                    label="OTP 验证码"
                    value={otpCode}
                    onChange={(event) => setOtpCode(event.target.value)}
                    helperText="账号启用双因子认证时填写"
                    fullWidth
                    autoComplete="one-time-code"
                    placeholder="可选"
                    slotProps={{
                        htmlInput: {
                            maxLength: 10,
                            spellCheck: false,
                        },
                    }}
                />

                <Box
                    sx={{
                        p: 1.5,
                        border: '1px solid #e2e8f0',
                        borderRadius: '10px',
                        bgcolor: '#f1f5f9',
                    }}
                >
                    <FormControlLabel
                        control={
                            <Checkbox
                                checked={rememberDevice}
                                onChange={(event) =>
                                    setRememberDevice(event.target.checked)
                                }
                                color="primary"
                                size="small"
                            />
                        }
                        label={
                            <Box>
                                <Typography
                                    sx={{
                                        color: '#334155',
                                        fontSize: 13,
                                        fontWeight: 650,
                                    }}
                                >
                                    记住此设备（免 OTP）
                                </Typography>
                                <Typography
                                    sx={{
                                        mt: 0.2,
                                        color: '#586b80',
                                        fontSize: 11,
                                    }}
                                >
                                    仅在受控的个人设备上启用
                                </Typography>
                            </Box>
                        }
                        sx={{
                            m: 0,
                            alignItems: 'flex-start',
                            '& .MuiCheckbox-root': {
                                mt: -0.45,
                                ml: -0.5,
                            },
                        }}
                    />
                    {rememberDevice && (
                        <TextField
                            label="设备名称"
                            size="small"
                            placeholder="设备名称，例如：MacBook Pro"
                            value={deviceName}
                            onChange={(event) =>
                                setDeviceName(event.target.value)
                            }
                            fullWidth
                            autoComplete="off"
                            sx={{ mt: 1.5 }}
                        />
                    )}
                </Box>

                {error && (
                    <Alert severity="error" role="alert">
                        {error}
                    </Alert>
                )}

                <Button
                    type="submit"
                    variant="contained"
                    disabled={submitting}
                    size="large"
                    aria-label={submitting ? '正在登录' : undefined}
                    sx={{ gap: 1 }}
                >
                    {submitting ? (
                        <>
                            <CircularProgress
                                size={18}
                                color="inherit"
                                aria-hidden="true"
                            />
                            正在验证…
                        </>
                    ) : (
                        '登录系统'
                    )}
                </Button>
            </Stack>

            <Divider
                sx={{
                    color: '#64748b',
                    fontSize: 11,
                    '&::before, &::after': {
                        borderColor: '#e2e8f0',
                    },
                }}
            >
                账号帮助
            </Divider>

            <Stack spacing={1.25} sx={{ alignItems: 'center' }}>
                <Typography sx={{ color: '#64748b', fontSize: 13 }}>
                    尚未加入 ChronoDesk？{' '}
                    <Link
                        component={RouterLink}
                        to="/register"
                        underline="hover"
                        sx={{ fontWeight: 700 }}
                    >
                        创建账号
                    </Link>
                </Typography>
                <Typography sx={{ color: '#64748b', fontSize: 12 }}>
                    未收到验证邮件？{' '}
                    <Link
                        component={RouterLink}
                        to="/resend-verification"
                        underline="hover"
                        sx={{ fontWeight: 600 }}
                    >
                        重新发送
                    </Link>
                </Typography>
            </Stack>
        </PublicAuthShell>
    )
}

export default LoginPage
