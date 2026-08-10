import { useEffect, useState } from 'react'
import { Alert, Button, Link, Stack } from '@mui/material'
import {
    Link as RouterLink,
    useNavigate,
    useSearchParams,
} from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import PublicAuthShell from './PublicAuthShell'
import { verifyHumanEmail } from './publicAuthApi'

type VerificationState = 'ready' | 'success' | 'error'

const VerifyEmailPage = () => {
    const navigate = useNavigate()
    const [searchParams] = useSearchParams()
    const [token] = useState(
        () => searchParams.get('token')?.trim() ?? '',
    )
    const [submitting, setSubmitting] = useState(false)
    const [state, setState] = useState<VerificationState>('ready')
    const [error, setError] = useState<string | null>(null)

    useEffect(() => {
        if (searchParams.has('token')) {
            navigate('/verify-email', { replace: true })
        }
    }, [navigate, searchParams])

    const handleVerify = async () => {
        if (!token) {
            setState('error')
            setError('邮箱验证链接无效，请重新申请验证邮件')
            return
        }
        setSubmitting(true)
        setError(null)
        try {
            await verifyHumanEmail({ token })
            setState('success')
        } catch (requestError) {
            setState('error')
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '邮箱验证失败，请重新申请验证邮件',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="验证邮箱"
            description="确认后会消费本链接中的一次性验证令牌"
        >
            {state === 'success' ? (
                <Alert severity="success" role="status">
                    邮箱验证成功，现在可以登录。
                </Alert>
            ) : state === 'error' || !token ? (
                <Alert severity="error" role="alert">
                    {error ?? '邮箱验证链接无效，请重新申请验证邮件'}
                </Alert>
            ) : (
                <Button
                    disabled={submitting}
                    onClick={() => void handleVerify()}
                    size="large"
                    variant="contained"
                >
                    {submitting ? '正在验证…' : '确认验证邮箱'}
                </Button>
            )}
            <Stack
                direction="row"
                spacing={2}
                sx={{ justifyContent: 'center' }}
            >
                <Link component={RouterLink} to="/resend-verification">
                    重发验证邮件
                </Link>
                <Link component={RouterLink} to="/login">
                    返回登录
                </Link>
            </Stack>
        </PublicAuthShell>
    )
}

export default VerifyEmailPage
