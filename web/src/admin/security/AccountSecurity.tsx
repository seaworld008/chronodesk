import { useCallback, useEffect, useState } from 'react'
import {
    Alert,
    Box,
    Button,
    Chip,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Grid,
    Paper,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import { Link as RouterLink, useNavigate } from 'react-router-dom'
import { useBlocker, useNotify } from 'react-admin'
import QRCode from 'react-qr-code'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import { clearAuthenticationState } from '@/lib/authProvider'
import {
    humanApiRoutes,
    type HumanSessionUser,
} from '@/lib/generated/human-api'
import PageShell from '@/components/layout/PageShell'
import AccountPageHeader from './AccountPageHeader'

interface OTPSetup {
    secret: string
    qr_code: string
    backup_codes: string[]
}

const AccountSecurity = () => {
    const notify = useNotify()
    const navigate = useNavigate()
    const [user, setUser] = useState<HumanSessionUser | null>(null)
    const [currentPassword, setCurrentPassword] = useState('')
    const [newPassword, setNewPassword] = useState('')
    const [confirmPassword, setConfirmPassword] = useState('')
    const [mfaPassword, setMfaPassword] = useState('')
    const [otpCode, setOtpCode] = useState('')
    const [otpSetup, setOtpSetup] = useState<OTPSetup | null>(null)
    const [otpMaterialsAcknowledged, setOtpMaterialsAcknowledged] = useState(true)
    const [busy, setBusy] = useState(false)
    const hasUnacknowledgedOTPMaterials =
        otpSetup !== null && !otpMaterialsAcknowledged
    const blocker = useBlocker(({ currentLocation, nextLocation }) =>
        hasUnacknowledgedOTPMaterials &&
        currentLocation.pathname !== nextLocation.pathname,
    )

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

    useEffect(() => {
        if (!hasUnacknowledgedOTPMaterials) return
        const warnBeforeLeaving = (event: BeforeUnloadEvent) => {
            event.preventDefault()
            event.returnValue = ''
        }
        window.addEventListener('beforeunload', warnBeforeLeaving)
        return () => window.removeEventListener('beforeunload', warnBeforeLeaving)
    }, [hasUnacknowledgedOTPMaterials])

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
            notify('密码已修改，所有会话（包括当前会话）均已失效，请重新登录', {
                type: 'success',
            })
            clearAuthenticationState({ notifyPeers: 'all_devices' })
            navigate('/login', { replace: true })
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
            setOtpMaterialsAcknowledged(false)
            await load()
            notify('MFA 已立即启用，请立即配置验证器并安全保存备用码', {
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
            notify('验证码有效；MFA 在生成密钥时已经启用', { type: 'success' })
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
            setOtpMaterialsAcknowledged(true)
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
        <PageShell
            title="账号安全"
            testId="account-page-shell"
        >
            <AccountPageHeader
                title="账号安全"
                description="管理当前账号的密码、MFA、可信设备和登录记录。"
            />
            <Stack spacing={3} sx={{ maxWidth: 960, mt: 3 }}>
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
                            <Typography variant="subtitle1" sx={{ fontWeight: 700 }}>
                                MFA 已立即启用
                            </Typography>
                            <Typography sx={{ mt: 0.5 }}>
                                此页面关闭后不会再次显示密钥和备用码。离开前请完成验证器配置，并把备用码保存到安全位置。
                            </Typography>
                            <Box
                                data-testid="mfa-setup-qr-code"
                                sx={{
                                    bgcolor: 'common.white',
                                    display: 'inline-flex',
                                    mt: 2,
                                    p: 1.5,
                                    borderRadius: 1,
                                }}
                            >
                                <QRCode
                                    value={otpSetup.qr_code}
                                    size={180}
                                    title="MFA 验证器配置二维码"
                                />
                            </Box>
                            <TextField
                                label="验证器配置 URI（qr_code）"
                                value={otpSetup.qr_code}
                                multiline
                                minRows={2}
                                fullWidth
                                sx={{ mt: 2 }}
                                slotProps={{ htmlInput: { readOnly: true } }}
                            />
                            <TextField
                                label="手动输入密钥"
                                value={otpSetup.secret}
                                fullWidth
                                sx={{ mt: 2 }}
                                slotProps={{ htmlInput: { readOnly: true } }}
                            />
                            <Typography variant="subtitle2" sx={{ mt: 2 }}>
                                一次性备用码
                            </Typography>
                            <Box
                                component="ul"
                                aria-label="MFA 备用码"
                                sx={{
                                    columns: { xs: 1, sm: 2 },
                                    m: 0,
                                    mt: 1,
                                    pl: 3,
                                    fontFamily: 'monospace',
                                }}
                            >
                                {otpSetup.backup_codes.map((code) => (
                                    <li key={code}>{code}</li>
                                ))}
                            </Box>
                            <Typography sx={{ mt: 1 }}>
                                手机丢失时可使用一个未使用的备用码恢复登录；每个备用码只能使用一次。
                            </Typography>
                            <Stack
                                direction={{ xs: 'column', sm: 'row' }}
                                spacing={1}
                                sx={{ alignItems: { sm: 'center' }, mt: 2 }}
                            >
                                <Button
                                    variant="outlined"
                                    disabled={otpMaterialsAcknowledged}
                                    onClick={() => setOtpMaterialsAcknowledged(true)}
                                >
                                    我已安全保存
                                </Button>
                                <Chip
                                    size="small"
                                    color={otpMaterialsAcknowledged ? 'success' : 'warning'}
                                    label={
                                        otpMaterialsAcknowledged
                                            ? '恢复材料已确认保存'
                                            : '尚未确认保存恢复材料'
                                    }
                                />
                            </Stack>
                            <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ mt: 2 }}>
                                <TextField label="测试 6 位验证码（不改变启用状态）" value={otpCode} onChange={(event) => setOtpCode(event.target.value)} slotProps={{ htmlInput: { maxLength: 6, inputMode: 'numeric' } }} />
                                <Button variant="contained" disabled={busy || otpCode.length !== 6} onClick={() => void verifyMFA()}>测试验证码</Button>
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
            <Dialog
                open={blocker.state === 'blocked'}
                aria-labelledby="mfa-leave-confirmation-title"
            >
                <DialogTitle id="mfa-leave-confirmation-title">
                    MFA 恢复材料尚未确认保存
                </DialogTitle>
                <DialogContent>
                    离开后将无法再次查看本次密钥和备用码。请先安全保存；若已完成，可确认离开。
                </DialogContent>
                <DialogActions>
                    <Button
                        onClick={() => {
                            if (blocker.state === 'blocked') blocker.reset()
                        }}
                    >
                        继续留在本页
                    </Button>
                    <Button
                        variant="contained"
                        color="warning"
                        onClick={() => {
                            if (blocker.state === 'blocked') {
                                setOtpMaterialsAcknowledged(true)
                                blocker.proceed()
                            }
                        }}
                    >
                        我已保存并离开
                    </Button>
                </DialogActions>
            </Dialog>
        </PageShell>
    )
}

export default AccountSecurity
