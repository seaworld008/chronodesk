import React from 'react'
import {
    Alert,
    Box,
    Button,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    FormControlLabel,
    Paper,
    Stack,
    Switch,
    Typography,
} from '@mui/material'
import {
    Refresh as RefreshIcon,
    Save as SaveIcon,
    Security as SecurityIcon,
} from '@mui/icons-material'
import { useNotify } from 'react-admin'

import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'
import {
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
    apiFetch,
} from '@/lib/apiClient'
import {
    humanApiRoutes,
    type EmergencyControlSnapshot,
    type UpdateEmergencyControlsRequest,
} from '@/lib/generated/human-api'

type Draft = Pick<
    EmergencyControlSnapshot,
    'global_read_only' | 'emergency_stop'
>

const isSnapshot = (value: unknown): value is EmergencyControlSnapshot =>
    typeof value === 'object' &&
    value !== null &&
    'global_read_only' in value &&
    typeof value.global_read_only === 'boolean' &&
    'emergency_stop' in value &&
    typeof value.emergency_stop === 'boolean' &&
    'version' in value &&
    typeof value.version === 'number' &&
    Number.isSafeInteger(value.version) &&
    value.version > 0 &&
    'updated_at' in value &&
    typeof value.updated_at === 'string'

const readEnvelopeData = (payload: unknown): unknown =>
    typeof payload === 'object' &&
    payload !== null &&
    'data' in payload
        ? payload.data
        : payload

const parseResponse = async (
    response: Response,
): Promise<EmergencyControlSnapshot> => {
    const payload = await response.json().catch(() => null)
    if (!response.ok) {
        throw Object.assign(
            new Error(localizedApiErrorMessage(payload, response.status)),
            { status: response.status },
        )
    }
    const snapshot = readEnvelopeData(payload)
    if (!isSnapshot(snapshot)) {
        throw new Error('安全控制响应格式无效')
    }
    return snapshot
}

const responseETag = (response: Response): string => {
    const etag = response.headers.get('ETag') ?? ''
    if (!/^"v[1-9][0-9]*"$/u.test(etag)) {
        throw new Error('安全控制响应缺少有效版本')
    }
    return etag
}

const EmergencyControls = () => {
    const notify = useNotify()
    const [snapshot, setSnapshot] =
        React.useState<EmergencyControlSnapshot | null>(null)
    const [draft, setDraft] = React.useState<Draft | null>(null)
    const [etag, setETag] = React.useState('')
    const [loading, setLoading] = React.useState(true)
    const [saving, setSaving] = React.useState(false)
    const [error, setError] = React.useState('')
    const [confirmOpen, setConfirmOpen] = React.useState(false)
    const requestController = React.useRef<AbortController | null>(null)

    const load = React.useCallback(async () => {
        requestController.current?.abort()
        const controller = new AbortController()
        requestController.current = controller
        setLoading(true)
        setError('')
        try {
            const response = await apiFetch<Response>(
                humanApiRoutes.getPlatformEmergencyControls(),
                { rawResponse: true, signal: controller.signal },
            )
            const nextETag = responseETag(response)
            const next = await parseResponse(response)
            if (controller.signal.aborted) return
            setSnapshot(next)
            setDraft({
                global_read_only: next.global_read_only,
                emergency_stop: next.emergency_stop,
            })
            setETag(nextETag)
        } catch (requestError) {
            if (controller.signal.aborted) return
            setError(localizedUnknownErrorMessage(
                requestError,
                '安全与应急控制加载失败，请稍后重试',
            ))
        } finally {
            if (!controller.signal.aborted) setLoading(false)
        }
    }, [])

    React.useEffect(() => {
        void load()
        return () => requestController.current?.abort()
    }, [load])

    const changes = React.useMemo(() => {
        if (!snapshot || !draft) return []
        const result: Array<keyof Draft> = []
        if (draft.global_read_only !== snapshot.global_read_only) {
            result.push('global_read_only')
        }
        if (draft.emergency_stop !== snapshot.emergency_stop) {
            result.push('emergency_stop')
        }
        return result
    }, [draft, snapshot])

    const save = async () => {
        if (!draft || !snapshot || !etag || changes.length === 0) return
        const request: UpdateEmergencyControlsRequest = {}
        if (changes.includes('global_read_only')) {
            request.global_read_only = draft.global_read_only
        }
        if (changes.includes('emergency_stop')) {
            request.emergency_stop = draft.emergency_stop
        }
        setSaving(true)
        setConfirmOpen(false)
        try {
            const response = await apiFetch<Response>(
                humanApiRoutes.updatePlatformEmergencyControls(),
                {
                    method: 'PUT',
                    headers: { 'If-Match': etag },
                    body: JSON.stringify(request),
                    rawResponse: true,
                },
            )
            if (response.status === 412) {
                await load()
                notify('安全控制已被其他操作更新，已刷新为最新状态', {
                    type: 'warning',
                })
                return
            }
            const nextETag = responseETag(response)
            const next = await parseResponse(response)
            setSnapshot(next)
            setDraft({
                global_read_only: next.global_read_only,
                emergency_stop: next.emergency_stop,
            })
            setETag(nextETag)
            notify('平台安全控制已更新并写入审计', { type: 'success' })
        } catch (requestError) {
            notify(localizedUnknownErrorMessage(
                requestError,
                '平台安全控制更新失败，请刷新后重试',
            ), { type: 'error' })
        } finally {
            setSaving(false)
        }
    }

    return (
        <PageShell title="安全与应急" maxWidth={960} testId="emergency-controls-page">
            <PageHeader
                title="安全与应急"
                description="平台级 Agent 安全控制，仅紧急运维员可以查看和修改。"
                leading={<SecurityIcon color="error" />}
                action={
                    <Stack direction="row" spacing={1}>
                        <Button
                            startIcon={<RefreshIcon />}
                            onClick={() => void load()}
                            disabled={loading || saving}
                        >
                            刷新
                        </Button>
                        <Button
                            variant="contained"
                            color="error"
                            startIcon={<SaveIcon />}
                            disabled={loading || saving || changes.length === 0}
                            onClick={() => setConfirmOpen(true)}
                        >
                            保存安全控制
                        </Button>
                    </Stack>
                }
            />

            <Alert severity="warning" sx={{ mt: 2 }}>
                这里的控制会立即影响所有项目中的 AI Agent。每次修改都使用
                ETag 防止覆盖他人操作，并记录不可变的平台审计。
            </Alert>

            {loading && (
                <Box
                    role="status"
                    aria-label="正在加载安全与应急控制"
                    sx={{ display: 'grid', minHeight: 280, placeItems: 'center' }}
                >
                    <CircularProgress size={32} />
                </Box>
            )}
            {!loading && error && (
                <Alert
                    severity="error"
                    action={<Button onClick={() => void load()}>重试</Button>}
                    sx={{ mt: 2 }}
                >
                    {error}
                </Alert>
            )}
            {!loading && !error && snapshot && draft && (
                <Stack spacing={2} sx={{ mt: 2 }}>
                    <Paper variant="outlined" sx={{ p: { xs: 2, sm: 3 } }}>
                        <Stack
                            direction={{ xs: 'column', sm: 'row' }}
                            spacing={2}
                            sx={{
                                alignItems: { xs: 'stretch', sm: 'center' },
                                justifyContent: 'space-between',
                            }}
                        >
                            <Box>
                                <Typography component="h2" variant="h6">
                                    全局只读模式
                                </Typography>
                                <Typography color="text.secondary">
                                    保留 Agent 读取与检索能力，拒绝工单、评论和自动化写操作。
                                </Typography>
                            </Box>
                            <FormControlLabel
                                label={draft.global_read_only ? '已启用' : '未启用'}
                                labelPlacement="start"
                                control={
                                    <Switch
                                        checked={draft.global_read_only}
                                        onChange={(_, checked) =>
                                            setDraft({
                                                ...draft,
                                                global_read_only: checked,
                                            })
                                        }
                                        slotProps={{
                                            input: {
                                                'aria-label': '智能体全局只读模式',
                                            },
                                        }}
                                    />
                                }
                            />
                        </Stack>
                    </Paper>

                    <Paper variant="outlined" sx={{ p: { xs: 2, sm: 3 } }}>
                        <Stack
                            direction={{ xs: 'column', sm: 'row' }}
                            spacing={2}
                            sx={{
                                alignItems: { xs: 'stretch', sm: 'center' },
                                justifyContent: 'space-between',
                            }}
                        >
                            <Box>
                                <Typography component="h2" variant="h6">
                                    全局紧急停止
                                </Typography>
                                <Typography color="text.secondary">
                                    立即停止全部 Agent 执行；持久化不可用时系统也会失败关闭。
                                </Typography>
                            </Box>
                            <FormControlLabel
                                label={draft.emergency_stop ? '已停止' : '正常运行'}
                                labelPlacement="start"
                                control={
                                    <Switch
                                        color="error"
                                        checked={draft.emergency_stop}
                                        onChange={(_, checked) =>
                                            setDraft({
                                                ...draft,
                                                emergency_stop: checked,
                                            })
                                        }
                                        slotProps={{
                                            input: {
                                                'aria-label': '智能体全局紧急停止',
                                            },
                                        }}
                                    />
                                }
                            />
                        </Stack>
                    </Paper>

                    <Stack
                        direction="row"
                        spacing={1}
                        useFlexGap
                        sx={{ flexWrap: 'wrap' }}
                    >
                        <Chip label={`资源版本 v${snapshot.version}`} />
                        <Chip
                            color={snapshot.emergency_stop ? 'error' : 'success'}
                            label={
                                snapshot.emergency_stop
                                    ? 'Agent 已全局停止'
                                    : snapshot.global_read_only
                                        ? 'Agent 仅允许读取'
                                        : 'Agent 正常运行'
                            }
                        />
                        {changes.length > 0 && (
                            <Chip
                                color="warning"
                                label={`有 ${changes.length} 项未保存修改`}
                            />
                        )}
                    </Stack>
                </Stack>
            )}

            <Dialog
                open={confirmOpen}
                onClose={() => !saving && setConfirmOpen(false)}
                aria-labelledby="emergency-control-confirm-title"
            >
                <DialogTitle id="emergency-control-confirm-title">
                    确认更新平台安全控制
                </DialogTitle>
                <DialogContent>
                    <Typography>
                        本次将修改 {changes.length} 项平台级控制，并记录操作人、结果和请求链路。
                    </Typography>
                    {changes.includes('emergency_stop') && draft && (
                        <Alert severity="warning" sx={{ mt: 2 }}>
                            {draft.emergency_stop
                                ? '启用后，所有项目中的 Agent 执行会立即停止。'
                                : '解除后，符合策略和权限的 Agent 执行将恢复。'}
                        </Alert>
                    )}
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setConfirmOpen(false)} disabled={saving}>
                        取消
                    </Button>
                    <Button
                        variant="contained"
                        color="error"
                        onClick={() => void save()}
                        disabled={saving}
                    >
                        确认并写入审计
                    </Button>
                </DialogActions>
            </Dialog>
        </PageShell>
    )
}

export default EmergencyControls
