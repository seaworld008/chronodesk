import React from 'react'
import {
    Alert,
    Button,
    Chip,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    LinearProgress,
    Stack,
    TextField,
    Typography,
} from '@mui/material'

import {
    apiFetch,
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    auditFiltersToExportQuery,
    type AuditExplorerFilters,
} from './auditExplorerState'
import {
    humanApiRoutes,
    type AdminAuditExport,
} from '@/lib/generated/human-api'

interface AuditExportDialogProps {
    open: boolean
    filters: AuditExplorerFilters
    onClose: () => void
}

const exportFailureLabels: Record<string, string> = {
    storage_unavailable: '导出存储暂时不可用，请稍后重试',
    query_failed: '审计数据查询失败，请稍后重试',
    generation_failed: '导出文件生成失败，请稍后重试',
    lease_lost: '导出任务已安全转交，请重新创建任务',
}

const localDateTimeValue = (date: Date) => {
    const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
    return local.toISOString().slice(0, 16)
}

const initialRange = (
    filters: AuditExplorerFilters,
): { start: string; end: string } => {
    if (!filters.timePreset && filters.startTime && filters.endTime) {
        return { start: filters.startTime, end: filters.endTime }
    }
    const end = new Date()
    const duration =
        filters.timePreset === '1h'
            ? 60 * 60 * 1000
            : filters.timePreset === '7d'
              ? 7 * 24 * 60 * 60 * 1000
              : filters.timePreset === '30d'
                ? 30 * 24 * 60 * 60 * 1000
                : 24 * 60 * 60 * 1000
    return {
        start: localDateTimeValue(new Date(end.getTime() - duration)),
        end: localDateTimeValue(end),
    }
}

const validAuditExportView = (value: unknown): value is AdminAuditExport => {
    if (typeof value !== 'object' || value === null) return false
    const candidate = value as Partial<AdminAuditExport>
    return (
        typeof candidate.public_id === 'string' &&
        ['queued', 'processing', 'completed', 'failed', 'expired'].includes(
            candidate.state ?? '',
        ) &&
        typeof candidate.requested_at === 'string' &&
        typeof candidate.row_count === 'number' &&
        typeof candidate.truncated === 'boolean' &&
        typeof candidate.size_bytes === 'number'
    )
}

const AuditExportDialog = ({
    open,
    filters,
    onClose,
}: AuditExportDialogProps) => {
    const [startTime, setStartTime] = React.useState('')
    const [endTime, setEndTime] = React.useState('')
    const [job, setJob] = React.useState<AdminAuditExport | null>(null)
    const [creating, setCreating] = React.useState(false)
    const [downloading, setDownloading] = React.useState(false)
    const [downloadSucceeded, setDownloadSucceeded] = React.useState(false)
    const [error, setError] = React.useState('')

    React.useEffect(() => {
        if (!open) return
        const range = initialRange(filters)
        setStartTime(range.start)
        setEndTime(range.end)
        setJob(null)
        setError('')
        setDownloadSucceeded(false)
    }, [filters, open])

    React.useEffect(() => {
        if (
            !open ||
            !job ||
            (job.state !== 'queued' && job.state !== 'processing')
        ) {
            return
        }
        const controller = new AbortController()
        let timer: ReturnType<typeof setTimeout> | undefined
        const poll = async () => {
            try {
                const value = await apiFetch<AdminAuditExport>(
                    humanApiRoutes.getPlatformAuditExport({
                        auditExportPublicID: job.public_id,
                    }),
                    { signal: controller.signal },
                )
                if (!validAuditExportView(value)) {
                    throw new Error('审计导出状态响应格式无效')
                }
                if (!controller.signal.aborted) {
                    setJob(value)
                    setError('')
                    if (
                        value.state === 'queued' ||
                        value.state === 'processing'
                    ) {
                        timer = setTimeout(() => void poll(), 2_000)
                    }
                }
            } catch (requestError: unknown) {
                if (!controller.signal.aborted) {
                    setError(
                        localizedUnknownErrorMessage(
                            requestError,
                            '获取审计导出进度失败，请稍后重试',
                        ),
                    )
                }
            }
        }
        timer = setTimeout(() => void poll(), 1_000)
        return () => {
            controller.abort()
            if (timer) clearTimeout(timer)
        }
    }, [job, open])

    const createExport = async () => {
        setCreating(true)
        setError('')
        setDownloadSucceeded(false)
        try {
            const query = auditFiltersToExportQuery(
                filters,
                startTime,
                endTime,
            )
            const value = await apiFetch<AdminAuditExport>(
                humanApiRoutes.createPlatformAuditExport(query),
                { method: 'POST' },
            )
            if (!validAuditExportView(value)) {
                throw new Error('审计导出创建响应格式无效')
            }
            setJob(value)
        } catch (requestError: unknown) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '创建审计导出失败，请稍后重试',
                ),
            )
        } finally {
            setCreating(false)
        }
    }

    const downloadExport = async () => {
        if (!job || job.state !== 'completed') return
        setDownloading(true)
        setError('')
        setDownloadSucceeded(false)
        try {
            const response = await apiFetch<Response>(
                humanApiRoutes.downloadPlatformAuditExport({
                    auditExportPublicID: job.public_id,
                }),
                { rawResponse: true },
            )
            if (!response.ok) {
                const payload: unknown = await response
                    .clone()
                    .json()
                    .catch(() => null)
                throw new Error(
                    localizedApiErrorMessage(
                        payload,
                        response.status,
                        '审计导出下载失败',
                    ),
                )
            }
            const blob = await response.blob()
            const objectURL = URL.createObjectURL(blob)
            try {
                const link = document.createElement('a')
                link.href = objectURL
                link.download = `chronodesk-audit-${job.public_id}.csv`
                link.style.display = 'none'
                document.body.append(link)
                link.click()
                link.remove()
            } finally {
                URL.revokeObjectURL(objectURL)
            }
            setDownloadSucceeded(true)
        } catch (requestError: unknown) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '审计导出下载失败，请稍后重试',
                ),
            )
        } finally {
            setDownloading(false)
        }
    }

    const busy =
        creating ||
        downloading ||
        job?.state === 'queued' ||
        job?.state === 'processing'
    const rangeDisabled = job !== null && job.state !== 'failed'

    return (
        <Dialog
            open={open}
            onClose={busy ? undefined : onClose}
            fullWidth
            maxWidth="sm"
            aria-labelledby="audit-export-title"
        >
            <DialogTitle id="audit-export-title">导出脱敏审计 CSV</DialogTitle>
            <DialogContent>
                <DialogContentText sx={{ mb: 2 }}>
                    使用当前筛选条件异步生成文件。单次最长 30 天、最多
                    100,000 条，文件将在生成后 24 小时过期。
                </DialogContentText>
                <Stack spacing={2}>
                    <TextField
                        label="开始时间"
                        type="datetime-local"
                        value={startTime}
                        onChange={(event) => setStartTime(event.target.value)}
                        disabled={rangeDisabled}
                        slotProps={{ inputLabel: { shrink: true } }}
                        required
                    />
                    <TextField
                        label="结束时间"
                        type="datetime-local"
                        value={endTime}
                        onChange={(event) => setEndTime(event.target.value)}
                        disabled={rangeDisabled}
                        slotProps={{ inputLabel: { shrink: true } }}
                        required
                    />
                    {error && <Alert severity="error">{error}</Alert>}
                    {(job?.state === 'queued' ||
                        job?.state === 'processing') && (
                        <Stack spacing={1} role="status">
                            <LinearProgress />
                            <Typography>
                                {job.state === 'queued'
                                    ? '任务已排队，等待生成…'
                                    : '正在生成脱敏 CSV…'}
                            </Typography>
                        </Stack>
                    )}
                    {job?.state === 'completed' && (
                        <Alert
                            severity="success"
                            action={
                                <Button
                                    color="inherit"
                                    onClick={() => void downloadExport()}
                                    disabled={downloading}
                                >
                                    {downloading ? '下载中…' : '下载 CSV'}
                                </Button>
                            }
                        >
                            已生成 {job.row_count.toLocaleString('zh-CN')} 条
                            {job.truncated ? '（已达到 100,000 条上限）' : ''}
                            ，文件大小{' '}
                            {(job.size_bytes / 1024).toFixed(1)} KB
                            {job.expires_at
                                ? `，${new Date(job.expires_at).toLocaleString('zh-CN')} 过期`
                                : ''}
                        </Alert>
                    )}
                    {job?.state === 'failed' && (
                        <Alert severity="error">
                            {exportFailureLabels[job.failure_code ?? ''] ??
                                '审计导出生成失败，请重新创建'}
                        </Alert>
                    )}
                    {job?.state === 'expired' && (
                        <Alert severity="warning">
                            导出文件已过期，请重新创建。
                        </Alert>
                    )}
                    {downloadSucceeded && (
                        <Alert severity="info">
                            文件响应已完整接收，并已交给浏览器下载。
                        </Alert>
                    )}
                    {job && (
                        <Stack direction="row" spacing={1}>
                            <Chip
                                size="small"
                                label={`任务 ${job.public_id.slice(0, 8)}`}
                            />
                            {job.sha256 && (
                                <Chip
                                    size="small"
                                    variant="outlined"
                                    label={`SHA-256 ${job.sha256.slice(0, 12)}…`}
                                />
                            )}
                        </Stack>
                    )}
                </Stack>
            </DialogContent>
            <DialogActions>
                <Button onClick={onClose} disabled={busy}>
                    关闭
                </Button>
                {(!job ||
                    job.state === 'failed' ||
                    job.state === 'expired') && (
                    <Button
                        variant="contained"
                        onClick={() => void createExport()}
                        disabled={creating || !startTime || !endTime}
                    >
                        {creating ? '正在创建…' : '创建导出任务'}
                    </Button>
                )}
            </DialogActions>
        </Dialog>
    )
}

export default AuditExportDialog
