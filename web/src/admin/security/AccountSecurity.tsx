import { useCallback, useEffect, useState } from 'react'
import {
    Alert,
    Box,
    Button,
    Chip,
    Grid,
    Paper,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import { Link as RouterLink } from 'react-router-dom'
import { Title, useNotify } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
    humanApiRoutes,
    type HumanSessionUser,
} from '@/lib/generated/human-api'

interface OTPSetup {
    secret: string
    qr_code: string
    backup_codes: string[]
}

const AccountSecurity = () => {
    const notify = useNotify()
    const [user, setUser] = useState<HumanSessionUser | null>(null)
    const [currentPassword, setCurrentPassword] = useState('')
    const [newPassword, setNewPassword] = useState('')
    const [confirmPassword, setConfirmPassword] = useState('')
    const [mfaPassword, setMfaPassword] = useState('')
    const [otpCode, setOtpCode] = useState('')
    const [otpSetup, setOtpSetup] = useState<OTPSetup | null>(null)
    const [busy, setBusy] = useState(false)

    const load = useCallback(async () => {
        const current = await apiFetch<HumanSessionUser>(
            humanApiRoutes.getHumanSessionUser(),
        )
        setUser(current)
    }, [])

    useEffect(() => {
        void load().catch(() => {
            notify('账号安全状态加载失败', { type: 'error' })
        })
    }, [load, notify])

    const changePassword = async () => {
        if (newPassword.length < 8 || newPassword !== confirmPassword) {
            notify('新密码至少 8 位，且两次输入必须一致', { type: 'warning' })
            return
        }
        setBusy(true)
        try {
            await apiFetch('/auth/change-password', {
                method: 'POST',
                body: JSON.stringify({
                    current_password: currentPassword,
                    new_password: newPassword,
                }),
            })
            setCurrentPassword('')
            setNewPassword('')
            setConfirmPassword('')
            notify('密码已修改，其他会话已失效', { type: 'success' })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, '密码修改失败'), {
                type: 'error',
            })
        } finally {
            setBusy(false)
        }
    }

    const beginMFA = async () => {
        setBusy(true)
        try {
            const setup = await apiFetch<OTPSetup>('/auth/enable-otp', {
                method: 'POST',
                body: JSON.stringify({ password: mfaPassword }),
            })
            setOtpSetup(setup)
            await load()
            notify('MFA 密钥已生成，请完成验证码确认并安全保存备用码', {
                type: 'success',
            })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, 'MFA 启用失败'), {
                type: 'error',
            })
        } finally {
            setBusy(false)
        }
    }

    const verifyMFA = async () => {
        setBusy(true)
        try {
            await apiFetch('/auth/verify-otp', {
                method: 'POST',
                body: JSON.stringify({ code: otpCode }),
            })
            setOtpCode('')
            await load()
            notify('MFA 验证成功', { type: 'success' })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, 'MFA 验证失败'), {
                type: 'error',
            })
        } finally {
            setBusy(false)
        }
    }

    const disableMFA = async () => {
        setBusy(true)
        try {
            await apiFetch('/auth/disable-otp', {
                method: 'POST',
                body: JSON.stringify({ password: mfaPassword }),
            })
            setMfaPassword('')
            setOtpSetup(null)
            await load()
            notify('MFA 已关闭', { type: 'success' })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, 'MFA 关闭失败'), {
                type: 'error',
            })
        } finally {
            setBusy(false)
        }
    }

    return (
        <Box sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="账号安全" />
            <Stack spacing={3} sx={{ maxWidth: 960, mx: 'auto' }}>
                <Paper sx={{ p: { xs: 2, md: 3 } }}>
                    <Typography variant="h4" gutterBottom>账号安全</Typography>
                    <Typography color="text.secondary">
                        管理当前账号的密码、MFA、可信设备和登录记录。
                    </Typography>
                </Paper>
                <Paper sx={{ p: { xs: 2, md: 3 } }}>
                    <Typography variant="h6" gutterBottom>修改密码</Typography>
                    <Grid container spacing={2}>
                        <Grid size={12}><TextField type="password" autoComplete="current-password" label="当前密码" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} fullWidth /></Grid>
                        <Grid size={{ xs: 12, sm: 6 }}><TextField type="password" autoComplete="new-password" label="新密码" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} fullWidth /></Grid>
                        <Grid size={{ xs: 12, sm: 6 }}><TextField type="password" autoComplete="new-password" label="确认新密码" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} fullWidth /></Grid>
                    </Grid>
                    <Button variant="contained" disabled={busy} onClick={() => void changePassword()} sx={{ mt: 2 }}>修改密码</Button>
                </Paper>
                <Paper sx={{ p: { xs: 2, md: 3 } }}>
                    <Stack direction="row" spacing={1} sx={{ alignItems: 'center', mb: 2 }}>
                        <Typography variant="h6">多因素认证（MFA）</Typography>
                        <Chip size="small" color={user?.otp_enabled ? 'success' : 'default'} label={user?.otp_enabled ? '已启用' : '未启用'} />
                    </Stack>
                    <TextField type="password" label="当前密码" value={mfaPassword} onChange={(event) => setMfaPassword(event.target.value)} fullWidth />
                    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ mt: 2 }}>
                        {user?.otp_enabled ? (
                            <Button color="warning" variant="outlined" disabled={busy} onClick={() => void disableMFA()}>关闭 MFA</Button>
                        ) : (
                            <Button variant="outlined" disabled={busy} onClick={() => void beginMFA()}>启用 MFA</Button>
                        )}
                    </Stack>
                    {otpSetup && (
                        <Alert severity="warning" sx={{ mt: 2 }}>
                            <Typography>密钥：{otpSetup.secret}</Typography>
                            <Typography>备用码：{otpSetup.backup_codes.join('、')}</Typography>
                            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ mt: 1 }}>
                                <TextField label="6 位验证码" value={otpCode} onChange={(event) => setOtpCode(event.target.value)} slotProps={{ htmlInput: { maxLength: 6, inputMode: 'numeric' } }} />
                                <Button variant="contained" disabled={busy || otpCode.length !== 6} onClick={() => void verifyMFA()}>验证</Button>
                            </Stack>
                        </Alert>
                    )}
                </Paper>
                <Paper sx={{ p: { xs: 2, md: 3 } }}>
                    <Typography variant="h6" gutterBottom>安全记录</Typography>
                    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}>
                        <Button component={RouterLink} to="/account/trusted-devices" variant="outlined">可信设备</Button>
                        <Button component={RouterLink} to="/account/login-history" variant="outlined">登录记录</Button>
                    </Stack>
                </Paper>
            </Stack>
        </Box>
    )
}

export default AccountSecurity
