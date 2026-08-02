import { useState, type FormEvent } from 'react'
import {
    Alert,
    Button,
    Link,
    Stack,
    TextField,
} from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import PublicAuthShell from './PublicAuthShell'
import { registerHumanAccount } from './publicAuthApi'

interface RegistrationForm {
    username: string
    email: string
    password: string
    confirmation: string
    firstName: string
    lastName: string
}

const initialForm: RegistrationForm = {
    username: '',
    email: '',
    password: '',
    confirmation: '',
    firstName: '',
    lastName: '',
}

const RegisterPage = () => {
    const [form, setForm] = useState(initialForm)
    const [submitting, setSubmitting] = useState(false)
    const [registered, setRegistered] = useState(false)
    const [requiresVerification, setRequiresVerification] = useState(false)
    const [error, setError] = useState<string | null>(null)

    const updateField = (
        field: keyof RegistrationForm,
        value: string,
    ) => {
        setForm((current) => ({ ...current, [field]: value }))
    }

    const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
        event.preventDefault()
        if (form.password !== form.confirmation) {
            setError('两次输入的密码不一致')
            return
        }
        setSubmitting(true)
        setError(null)
        try {
            const result = await registerHumanAccount({
                username: form.username.trim(),
                email: form.email.trim(),
                password: form.password,
                confirm_password: form.confirmation,
                ...(form.firstName.trim()
                    ? { first_name: form.firstName.trim() }
                    : {}),
                ...(form.lastName.trim()
                    ? { last_name: form.lastName.trim() }
                    : {}),
            })
            setRequiresVerification(!result.user.email_verified)
            setRegistered(true)
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '注册失败，请检查输入后重试',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <PublicAuthShell
            title="注册 ChronoDesk"
            description="创建普通成员账号；项目访问仍需管理员显式授权"
        >
            {registered ? (
                <Alert severity="success" role="status">
                    {requiresVerification
                        ? '注册请求已完成，请查收验证邮件后再登录。'
                        : '注册成功，现在可以登录。'}
                </Alert>
            ) : (
                <Stack
                    component="form"
                    spacing={2}
                    onSubmit={handleSubmit}
                >
                    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
                        <TextField
                            autoComplete="given-name"
                            fullWidth
                            label="名（选填）"
                            onChange={(event) =>
                                updateField('firstName', event.target.value)
                            }
                            value={form.firstName}
                        />
                        <TextField
                            autoComplete="family-name"
                            fullWidth
                            label="姓（选填）"
                            onChange={(event) =>
                                updateField('lastName', event.target.value)
                            }
                            value={form.lastName}
                        />
                    </Stack>
                    <TextField
                        autoComplete="username"
                        autoFocus
                        fullWidth
                        label="用户名"
                        onChange={(event) =>
                            updateField('username', event.target.value)
                        }
                        required
                        slotProps={{
                            htmlInput: { minLength: 3, maxLength: 50 },
                        }}
                        value={form.username}
                    />
                    <TextField
                        autoComplete="email"
                        fullWidth
                        label="邮箱"
                        onChange={(event) =>
                            updateField('email', event.target.value)
                        }
                        required
                        type="email"
                        value={form.email}
                    />
                    <TextField
                        autoComplete="new-password"
                        fullWidth
                        helperText="至少 8 个字符，并满足系统密码强度要求"
                        label="密码"
                        onChange={(event) =>
                            updateField('password', event.target.value)
                        }
                        required
                        slotProps={{ htmlInput: { minLength: 8 } }}
                        type="password"
                        value={form.password}
                    />
                    <TextField
                        autoComplete="new-password"
                        fullWidth
                        label="确认密码"
                        onChange={(event) =>
                            updateField('confirmation', event.target.value)
                        }
                        required
                        type="password"
                        value={form.confirmation}
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
                        {submitting ? '正在注册…' : '创建账号'}
                    </Button>
                </Stack>
            )}
            <Stack
                direction="row"
                spacing={2}
                sx={{ justifyContent: 'center' }}
            >
                {registered && requiresVerification && (
                    <Link component={RouterLink} to="/resend-verification">
                        重发验证邮件
                    </Link>
                )}
                <Link component={RouterLink} to="/login">
                    返回登录
                </Link>
            </Stack>
        </PublicAuthShell>
    )
}

export default RegisterPage
