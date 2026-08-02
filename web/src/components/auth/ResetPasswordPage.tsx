import { useEffect, useState, type FormEvent } from 'react'
import { Alert, Button, Link, Stack, TextField } from '@mui/material'
import {
    Link as RouterLink,
    useNavigate,
    useSearchParams,
} from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import PublicAuthShell from './PublicAuthShell'
import { resetHumanPassword } from './publicAuthApi'

const ResetPasswordPage = () => {
    const navigate = useNavigate()
    const [searchParams] = useSearchParams()
    const [token] = useState(
        () => searchParams.get('token')?.trim() ?? '',
    )
    const [password, setPassword] = useState('')
    const [confirmation, setConfirmation] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [completed, setCompleted] = useState(false)
    const [error, setError] = useState<string | null>(null)

    useEffect(() => {
        if (searchParams.has('token')) {
            navigate('/reset-password', { replace: true })
        }
    }, [navigate, searchParams])

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        if (!token) {
            setError('密码重置链接缺少一次性令牌，请重新发起申请')
            return
        }
        if (password !== confirmation) {
            setError('两次输入的密码不一致')
            return
        }
        setSubmitting(true)
        setError(null)
        try {
            await resetHumanPassword({
                token,
                new_password: password,
            })
            setCompleted(true)
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '密码重置失败，请重新申请重置链接',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="重置密码"
            description="一次性链接使用成功后，原有登录会话会全部失效"
        >
            {completed ? (
                <Alert severity="success" role="status">
                    密码已重置，请使用新密码重新登录。
                </Alert>
            ) : (
                <Stack
                    component="form"
                    spacing={2}
                    onSubmit={handleSubmit}
                >
                    {!token && (
                        <Alert severity="error" role="alert">
                            密码重置链接无效，请重新发起申请。
                        </Alert>
                    )}
                    <TextField
                        autoComplete="new-password"
                        autoFocus
                        fullWidth
                        helperText="至少 8 个字符，并满足系统密码强度要求"
                        label="新密码"
                        onChange={(event) => setPassword(event.target.value)}
                        required
                        type="password"
                        value={password}
                    />
                    <TextField
                        autoComplete="new-password"
                        fullWidth
                        label="确认新密码"
                        onChange={(event) =>
                            setConfirmation(event.target.value)
                        }
                        required
                        type="password"
                        value={confirmation}
                    />
                    {error && (
                        <Alert severity="error" role="alert">
                            {error}
                        </Alert>
                    )}
                    <Button
                        disabled={submitting || !token}
                        size="large"
                        type="submit"
                        variant="contained"
                    >
                        {submitting ? '正在重置…' : '确认重置密码'}
                    </Button>
                </Stack>
            )}
            <Stack
                direction="row"
                spacing={2}
                sx={{ justifyContent: 'center' }}
            >
                <Link component={RouterLink} to="/forgot-password">
                    重新申请
                </Link>
                <Link component={RouterLink} to="/login">
                    返回登录
                </Link>
            </Stack>
        </PublicAuthShell>
    )
}

export default ResetPasswordPage
