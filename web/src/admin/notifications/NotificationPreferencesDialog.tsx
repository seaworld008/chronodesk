import { useState } from 'react'
import { useNotify } from 'react-admin'
import {
    Alert,
    Box,
    Button,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    FormControlLabel,
    Paper,
    Stack,
    Switch,
    TextField,
    Typography,
} from '@mui/material'
import {
    NotificationsOutlined as PreferencesIcon,
    Refresh as RefreshIcon,
} from '@mui/icons-material'
import {
    humanApiRoutes,
    type NotificationPreference,
    type NotificationPreferenceUpdate,
    type NotificationType,
    type UpdateNotificationPreferencesRequest,
} from '@/lib/generated/human-api'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'

const notificationTypeChoices = [
    { id: 'ticket_assigned', name: '工单分配' },
    { id: 'ticket_status_changed', name: '状态变更' },
    { id: 'ticket_commented', name: '评论通知' },
    { id: 'ticket_created', name: '工单创建' },
    { id: 'ticket_overdue', name: '工单逾期' },
    { id: 'ticket_resolved', name: '工单解决' },
    { id: 'ticket_closed', name: '工单关闭' },
    { id: 'system_maintenance', name: '系统维护' },
    { id: 'user_mention', name: '用户提及' },
    { id: 'system_alert', name: '系统警报' },
] as const satisfies readonly {
    id: NotificationType
    name: string
}[]

type EditablePreference = Omit<
    NotificationPreferenceUpdate,
    | 'do_not_disturb_start'
    | 'do_not_disturb_end'
    | 'max_daily_count'
    | 'webhook_enabled'
    | 'batch_delivery'
    | 'batch_interval'
> & {
    do_not_disturb_start: string
    do_not_disturb_end: string
    max_daily_count: string
}

const defaultPreference = (
    notificationType: NotificationType,
): EditablePreference => ({
    notification_type: notificationType,
    email_enabled: true,
    in_app_enabled: true,
    do_not_disturb_start: '',
    do_not_disturb_end: '',
    max_daily_count: '50',
})

const toLocalDateTime = (value: string | null): string => {
    if (!value) return ''
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return ''
    const local = new Date(
        parsed.getTime() - parsed.getTimezoneOffset() * 60_000,
    )
    return local.toISOString().slice(0, 16)
}

const buildEditablePreferences = (
    loaded: readonly NotificationPreference[],
): EditablePreference[] => {
    const byType = new Map(
        loaded.map((preference) => [
            preference.notification_type,
            preference,
        ]),
    )
    return notificationTypeChoices.map(({ id }) => {
        const preference = byType.get(id)
        if (!preference) return defaultPreference(id)
        return {
            notification_type: id,
            email_enabled: preference.email_enabled,
            in_app_enabled: preference.in_app_enabled,
            do_not_disturb_start: toLocalDateTime(
                preference.do_not_disturb_start,
            ),
            do_not_disturb_end: toLocalDateTime(
                preference.do_not_disturb_end,
            ),
            max_daily_count: String(preference.max_daily_count),
        }
    })
}

const boundedInteger = (
    value: string,
    minimum: number,
    maximum: number,
    label: string,
): number => {
    const parsed = Number(value)
    if (
        !Number.isSafeInteger(parsed) ||
        parsed < minimum ||
        parsed > maximum
    ) {
        throw new Error(`${label}必须是 ${minimum} 到 ${maximum} 之间的整数`)
    }
    return parsed
}

const toRequestPreference = (
    preference: EditablePreference,
    typeName: string,
): NotificationPreferenceUpdate => {
    const start = preference.do_not_disturb_start
    const end = preference.do_not_disturb_end
    if ((start === '') !== (end === '')) {
        throw new Error(`${typeName}的免打扰开始和结束时间必须同时填写`)
    }

    let startISO: string | null = null
    let endISO: string | null = null
    if (start && end) {
        const startTime = new Date(start)
        const endTime = new Date(end)
        if (
            Number.isNaN(startTime.getTime()) ||
            Number.isNaN(endTime.getTime())
        ) {
            throw new Error(`${typeName}的免打扰时间无效`)
        }
        if (startTime >= endTime) {
            throw new Error(`${typeName}的免打扰结束时间必须晚于开始时间`)
        }
        startISO = startTime.toISOString()
        endISO = endTime.toISOString()
    }

    return {
        notification_type: preference.notification_type,
        email_enabled: preference.email_enabled,
        in_app_enabled: preference.in_app_enabled,
        webhook_enabled: false,
        do_not_disturb_start: startISO,
        do_not_disturb_end: endISO,
        max_daily_count: boundedInteger(
            preference.max_daily_count,
            0,
            10_000,
            `${typeName}的每项目每日上限`,
        ),
        batch_delivery: false,
        batch_interval: 60,
    }
}

const NotificationPreferenceCard = ({
    preference,
    typeName,
    disabled,
    onChange,
}: {
    preference: EditablePreference
    typeName: string
    disabled: boolean
    onChange: (patch: Partial<EditablePreference>) => void
}) => {
    const headingID = `notification-preference-${preference.notification_type}`
    return (
        <Paper
            aria-labelledby={headingID}
            component="section"
            sx={{ p: 2 }}
            variant="outlined"
        >
            <Typography component="h3" id={headingID} variant="subtitle1">
                {typeName}
            </Typography>
            <Stack
                direction={{ xs: 'column', sm: 'row' }}
                spacing={{ xs: 0, sm: 2 }}
                sx={{ mt: 0.5 }}
            >
                <FormControlLabel
                    control={
                        <Switch
                            checked={preference.in_app_enabled}
                            disabled={disabled}
                            onChange={(_, checked) =>
                                onChange({ in_app_enabled: checked })
                            }
                            slotProps={{
                                input: {
                                    'aria-label': `${typeName}应用内通知`,
                                },
                            }}
                        />
                    }
                    label="应用内"
                />
                <FormControlLabel
                    control={
                        <Switch
                            checked={preference.email_enabled}
                            disabled={disabled}
                            onChange={(_, checked) =>
                                onChange({ email_enabled: checked })
                            }
                            slotProps={{
                                input: {
                                    'aria-label': `${typeName}邮件通知`,
                                },
                            }}
                        />
                    }
                    label="邮件"
                />
            </Stack>
            <Box
                sx={{
                    display: 'grid',
                    gap: 2,
                    gridTemplateColumns: {
                        xs: 'minmax(0, 1fr)',
                        md: 'repeat(2, minmax(0, 1fr))',
                    },
                    mt: 1,
                }}
            >
                <TextField
                    disabled={disabled}
                    helperText="0 表示每个项目内不限制"
                    label={`${typeName}每项目每日上限`}
                    onChange={(event) =>
                        onChange({ max_daily_count: event.target.value })
                    }
                    required
                    size="small"
                    slotProps={{
                        htmlInput: {
                            inputMode: 'numeric',
                            max: 10_000,
                            min: 0,
                            step: 1,
                        },
                    }}
                    type="number"
                    value={preference.max_daily_count}
                />
                <TextField
                    disabled={disabled}
                    label={`${typeName}免打扰开始`}
                    onChange={(event) =>
                        onChange({
                            do_not_disturb_start: event.target.value,
                        })
                    }
                    size="small"
                    slotProps={{
                        inputLabel: { shrink: true },
                    }}
                    type="datetime-local"
                    value={preference.do_not_disturb_start}
                />
                <TextField
                    disabled={disabled}
                    label={`${typeName}免打扰结束`}
                    onChange={(event) =>
                        onChange({
                            do_not_disturb_end: event.target.value,
                        })
                    }
                    size="small"
                    slotProps={{
                        inputLabel: { shrink: true },
                    }}
                    type="datetime-local"
                    value={preference.do_not_disturb_end}
                />
            </Box>
        </Paper>
    )
}

export const NotificationPreferencesButton = () => {
    const notify = useNotify()
    const [open, setOpen] = useState(false)
    const [loading, setLoading] = useState(false)
    const [saving, setSaving] = useState(false)
    const [error, setError] = useState('')
    const [loadFailed, setLoadFailed] = useState(false)
    const [preferences, setPreferences] = useState<EditablePreference[]>([])

    const loadPreferences = async () => {
        setLoading(true)
        setError('')
        setLoadFailed(false)
        setPreferences([])
        try {
            const loaded = await apiFetch<NotificationPreference[]>(
                humanApiRoutes.getHumanNotificationPreferences(),
            )
            setPreferences(buildEditablePreferences(loaded))
        } catch (loadError: unknown) {
            setLoadFailed(true)
            setError(
                localizedUnknownErrorMessage(
                    loadError,
                    '加载通知偏好失败，请稍后重试',
                ),
            )
        } finally {
            setLoading(false)
        }
    }

    const openPreferences = () => {
        setOpen(true)
        void loadPreferences()
    }

    const updatePreference = (
        notificationType: NotificationType,
        patch: Partial<EditablePreference>,
    ) => {
        setPreferences((current) =>
            current.map((preference) =>
                preference.notification_type === notificationType
                    ? { ...preference, ...patch }
                    : preference,
            ),
        )
    }

    const savePreferences = async () => {
        setError('')
        setLoadFailed(false)
        let request: UpdateNotificationPreferencesRequest
        try {
            request = {
                preferences: preferences.map((preference) => {
                    const typeName =
                        notificationTypeChoices.find(
                            ({ id }) =>
                                id === preference.notification_type,
                        )?.name ?? '通知'
                    return toRequestPreference(preference, typeName)
                }),
            }
        } catch (validationError: unknown) {
            setError(
                localizedUnknownErrorMessage(
                    validationError,
                    '通知偏好设置无效',
                ),
            )
            return
        }

        setSaving(true)
        try {
            await apiFetch<void>(
                humanApiRoutes.updateHumanNotificationPreferences(),
                {
                    body: JSON.stringify(request),
                    method: 'PUT',
                },
            )
            setOpen(false)
            notify('通知偏好已保存', { type: 'success' })
        } catch (saveError: unknown) {
            setError(
                localizedUnknownErrorMessage(
                    saveError,
                    '保存通知偏好失败，请稍后重试',
                ),
            )
        } finally {
            setSaving(false)
        }
    }

    return (
        <>
            <Button
                onClick={openPreferences}
                startIcon={<PreferencesIcon />}
            >
                通知偏好
            </Button>
            <Dialog
                fullWidth
                maxWidth="lg"
                open={open}
                onClose={() => {
                    if (!saving) setOpen(false)
                }}
            >
                <DialogTitle>通知偏好</DialogTitle>
                <DialogContent dividers>
                    <Alert role="note" severity="info" sx={{ mb: 2 }}>
                        这些偏好属于当前登录员工，并在其所有项目中生效。免打扰和每项目 UTC 每日上限仅控制应用内通知，时间按当前浏览器时区填写；用户级通知偏好不提供 Webhook 或批量投递。
                    </Alert>
                    {error && (
                        <Alert
                            action={
                                loadFailed && !loading ? (
                                    <Button
                                        color="inherit"
                                        onClick={() => void loadPreferences()}
                                        size="small"
                                        startIcon={<RefreshIcon />}
                                    >
                                        重新加载
                                    </Button>
                                ) : undefined
                            }
                            role="alert"
                            severity="error"
                            sx={{ mb: 2 }}
                        >
                            {error}
                        </Alert>
                    )}
                    {loading ? (
                        <Stack
                            aria-busy="true"
                            aria-label="正在加载通知偏好"
                            role="status"
                            spacing={1}
                            sx={{ alignItems: 'center', py: 6 }}
                        >
                            <CircularProgress size={28} />
                            <Typography color="text.secondary">
                                正在加载通知偏好…
                            </Typography>
                        </Stack>
                    ) : (
                        <Stack spacing={2}>
                            {preferences.map((preference) => {
                                const typeName =
                                    notificationTypeChoices.find(
                                        ({ id }) =>
                                            id ===
                                            preference.notification_type,
                                    )?.name ?? '通知'
                                return (
                                    <NotificationPreferenceCard
                                        key={preference.notification_type}
                                        disabled={saving}
                                        onChange={(patch) =>
                                            updatePreference(
                                                preference.notification_type,
                                                patch,
                                            )
                                        }
                                        preference={preference}
                                        typeName={typeName}
                                    />
                                )
                            })}
                        </Stack>
                    )}
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={saving}
                        onClick={() => setOpen(false)}
                        sx={{ color: 'primary.dark' }}
                    >
                        取消
                    </Button>
                    <Button
                        disabled={
                            loading ||
                            saving ||
                            preferences.length !==
                                notificationTypeChoices.length
                        }
                        onClick={() => void savePreferences()}
                        sx={{
                            borderColor: 'primary.dark',
                            color: 'primary.dark',
                            '&:hover': {
                                borderColor: 'primary.dark',
                            },
                        }}
                        variant="outlined"
                    >
                        {saving ? '正在保存…' : '保存偏好'}
                    </Button>
                </DialogActions>
            </Dialog>
        </>
    )
}
