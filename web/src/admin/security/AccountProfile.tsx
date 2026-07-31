import { useCallback, useEffect, useState } from 'react'
import {
    Avatar,
    Box,
    Button,
    CircularProgress,
    Grid,
    Paper,
    Stack,
    TextField,
} from '@mui/material'
import { Save as SaveIcon, Upload as UploadIcon } from '@mui/icons-material'
import { Title, useNotify } from 'react-admin'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
    humanApiRoutes,
    type HumanSessionUser,
    type UpdateHumanProfileRequest,
} from '@/lib/generated/human-api'
import AccountPageHeader from './AccountPageHeader'

type ProfileForm = {
    first_name: string
    last_name: string
    timezone: string
    language: 'zh-CN' | 'en'
}

const emptyForm: ProfileForm = {
    first_name: '',
    last_name: '',
    timezone: 'Asia/Shanghai',
    language: 'zh-CN',
}

const storeCurrentUser = (user: HumanSessionUser) => {
    localStorage.setItem('user', JSON.stringify(user))
}

const AccountProfile = () => {
    const notify = useNotify()
    const [user, setUser] = useState<HumanSessionUser | null>(null)
    const [form, setForm] = useState<ProfileForm>(emptyForm)
    const [loading, setLoading] = useState(true)
    const [saving, setSaving] = useState(false)
    const [uploading, setUploading] = useState(false)

    const load = useCallback(async () => {
        setLoading(true)
        try {
            const current = await apiFetch<HumanSessionUser>(
                humanApiRoutes.getHumanSessionUser(),
            )
            setUser(current)
            setForm({
                first_name: current.profile?.first_name ?? '',
                last_name: current.profile?.last_name ?? '',
                timezone: current.profile?.timezone || 'Asia/Shanghai',
                language: current.profile?.language === 'en' ? 'en' : 'zh-CN',
            })
            storeCurrentUser(current)
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, '个人资料加载失败'), {
                type: 'error',
            })
        } finally {
            setLoading(false)
        }
    }, [notify])

    useEffect(() => {
        void load()
    }, [load])

    const save = async () => {
        setSaving(true)
        try {
            await apiFetch(humanApiRoutes.updateHumanProfile(), {
                method: 'PUT',
                body: JSON.stringify({
                    first_name: form.first_name.trim(),
                    last_name: form.last_name.trim(),
                    timezone: form.timezone,
                    language: form.language,
                } satisfies UpdateHumanProfileRequest),
            })
            await load()
            notify('个人资料已更新', { type: 'success' })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, '个人资料保存失败'), {
                type: 'error',
            })
        } finally {
            setSaving(false)
        }
    }

    const uploadAvatar = async (file: File) => {
        const body = new FormData()
        body.set('avatar', file)
        setUploading(true)
        try {
            const uploaded = await apiFetch<{ avatar_url: string }>(
                '/user/avatar',
                { method: 'POST', body },
            )
            const current = await apiFetch<HumanSessionUser>(
                humanApiRoutes.getHumanSessionUser(),
            )
            if (current.profile?.avatar !== uploaded.avatar_url) {
                throw new Error('头像保存后身份资料尚未同步')
            }
            setUser(current)
            storeCurrentUser(current)
            notify('头像已更新', { type: 'success' })
        } catch (error) {
            notify(localizedUnknownErrorMessage(error, '头像上传失败'), {
                type: 'error',
            })
        } finally {
            setUploading(false)
        }
    }

    if (loading && !user) {
        return (
            <Box role="status" sx={{ display: 'grid', minHeight: 320, placeItems: 'center' }}>
                <CircularProgress aria-label="正在加载个人资料" />
            </Box>
        )
    }

    return (
        <Box sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="个人资料" />
            <Paper
                data-testid="account-profile-page"
                sx={{ p: { xs: 2, md: 3 }, maxWidth: 1040, mx: 'auto' }}
            >
                <AccountPageHeader
                    title="个人资料"
                    description="仅可修改个人展示名称与本地化偏好；身份、职责和验证状态由专用流程管理。"
                    action={(
                        <Stack
                            data-testid="profile-avatar-panel"
                            direction="row"
                            spacing={1.5}
                            sx={{
                                alignItems: 'center',
                                justifyContent: { xs: 'flex-start', md: 'flex-end' },
                            }}
                        >
                            <Avatar
                                src={user?.profile?.avatar || undefined}
                                sx={{ width: 64, height: 64 }}
                            >
                                {user?.username?.slice(0, 1).toUpperCase()}
                            </Avatar>
                            <Button
                                component="label"
                                variant="outlined"
                                startIcon={uploading ? <CircularProgress size={18} /> : <UploadIcon />}
                                disabled={uploading}
                            >
                                更换头像
                                <input
                                    hidden
                                    type="file"
                                    accept="image/png,image/jpeg"
                                    onChange={(event) => {
                                        const file = event.target.files?.[0]
                                        if (file) void uploadAvatar(file)
                                        event.target.value = ''
                                    }}
                                />
                            </Button>
                        </Stack>
                    )}
                />
                <Grid
                    container
                    spacing={2}
                    data-testid="profile-main-form"
                    sx={{ mt: 2, maxWidth: 760 }}
                >
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField
                            label="名字"
                            value={form.first_name}
                            onChange={(event) => setForm((current) => ({ ...current, first_name: event.target.value }))}
                            slotProps={{ htmlInput: { maxLength: 50 } }}
                            fullWidth
                        />
                    </Grid>
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField
                            label="姓氏"
                            value={form.last_name}
                            onChange={(event) => setForm((current) => ({ ...current, last_name: event.target.value }))}
                            slotProps={{ htmlInput: { maxLength: 50 } }}
                            fullWidth
                        />
                    </Grid>
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField label="邮箱（只读）" value={user?.email ?? ''} helperText="邮箱变更必须通过验证流程完成" fullWidth disabled />
                    </Grid>
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField label="手机（只读）" value={user?.profile?.phone ?? ''} helperText="手机变更必须通过验证流程完成" fullWidth disabled />
                    </Grid>
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField
                            select
                            label="时区"
                            value={form.timezone}
                            onChange={(event) => setForm((current) => ({ ...current, timezone: event.target.value }))}
                            fullWidth
                            slotProps={{ select: { native: true } }}
                        >
                            <option value="Asia/Shanghai">Asia/Shanghai</option>
                            <option value="UTC">UTC</option>
                            <option value="Asia/Tokyo">Asia/Tokyo</option>
                            <option value="America/New_York">America/New_York</option>
                        </TextField>
                    </Grid>
                    <Grid size={{ xs: 12, sm: 6 }}>
                        <TextField
                            select
                            label="语言"
                            value={form.language}
                            onChange={(event) => setForm((current) => ({
                                ...current,
                                language: event.target.value === 'en' ? 'en' : 'zh-CN',
                            }))}
                            fullWidth
                            slotProps={{ select: { native: true } }}
                        >
                            <option value="zh-CN">简体中文</option>
                            <option value="en">English</option>
                        </TextField>
                    </Grid>
                </Grid>
                <Stack sx={{ mt: 3, maxWidth: 760, alignItems: 'flex-end' }}>
                    <Button
                        variant="contained"
                        startIcon={<SaveIcon />}
                        disabled={saving}
                        onClick={() => void save()}
                    >
                        {saving ? '保存中…' : '保存个人资料'}
                    </Button>
                </Stack>
            </Paper>
        </Box>
    )
}

export default AccountProfile
