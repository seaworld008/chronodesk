import { useState, type FormEvent } from 'react'
import { Alert, Button, Link, Stack, TextField } from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import PublicAuthShell from './PublicAuthShell'
import { resendHumanEmailVerification } from './publicAuthApi'

const ResendVerificationPage = () => {
    const [email, setEmail] = useState('')
    const [submitting, setSubmitting] = useState(false)
    const [accepted, setAccepted] = useState(false)
    const [error, setError] = useState<string | null>(null)

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        setSubmitting(true)
        setError(null)
        try {
            await resendHumanEmailVerification({ email })
            setAccepted(true)
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '无法提交验证邮件请求，请稍后重试',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="重发验证邮件"
            description="为尚未完成验证的账号请求新的单次验证链接"
        >
            {accepted ? (
                <Alert severity="success" role="status">
                    如果该邮箱关联待验证账号，系统会发送一封新的验证邮件。
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
                        {submitting ? '正在提交…' : '重新发送'}
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

export default ResendVerificationPage
