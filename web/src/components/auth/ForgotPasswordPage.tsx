import { useState, type FormEvent } from 'react'
import { Alert, Button, Link, Stack, TextField } from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import PublicAuthShell from './PublicAuthShell'
import { requestHumanPasswordReset } from './publicAuthApi'

const ForgotPasswordPage = () => {
    const [email, setEmail] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [accepted, setAccepted] = useState(false)
    const [error, setError] = useState<string | null>(null)

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        setSubmitting(true)
        setError(null)
        try {
            await requestHumanPasswordReset(email)
            setAccepted(true)
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '无法提交密码重置请求，请稍后重试',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="找回密码"
            description="输入账号邮箱以请求一次性密码重置链接"
        >
            {accepted ? (
                <Alert severity="success" role="status">
                    如果该邮箱关联可重置账号，系统会发送一封密码重置邮件。
                </Alert>
            ) : (
                <Stack
                    component="form"
                    spacing={2}
                    onSubmit={handleSubmit}
                >
                    <TextField
                        autoComplete="email"
                        autoFocus
                        fullWidth
                        label="邮箱"
                        onChange={(event) => setEmail(event.target.value)}
                        required
                        type="email"
                        value={email}
                    />
                    {error && (
                        <Alert severity="error" role="alert">
                            {error}
                        </Alert>
                    )}
                    <Button
                        disabled={submitting}
                        size="large"
                        type="submit"
                        variant="contained"
                    >
                        {submitting ? '正在提交…' : '发送重置邮件'}
                    </Button>
                </Stack>
            )}
            <Link
                component={RouterLink}
                sx={{ textAlign: 'center' }}
                to="/login"
            >
                返回登录
            </Link>
        </PublicAuthShell>
    )
}

export default ForgotPasswordPage
