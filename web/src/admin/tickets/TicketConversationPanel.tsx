import {
    FormEvent,
    lazy,
    Suspense,
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react'
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Chip,
    CircularProgress,
    Divider,
    FormControl,
    InputLabel,
    List,
    ListItem,
    ListItemText,
    MenuItem,
    Pagination,
    Paper,
    Select,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import {
    AttachFile as AttachFileIcon,
    CloudDownload as DownloadIcon,
    Send as SendIcon,
    Visibility as PreviewIcon,
} from '@mui/icons-material'
import {
    useGetIdentity,
    useNotify,
    usePermissions,
    useRecordContext,
} from 'react-admin'
import type { Ticket } from '@/types'
import {
    humanApiRoutes,
    type CreateTicketCommentRequest,
    type TicketAttachment,
    type TicketComment,
} from '@/lib/generated/human-api'
import {
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
    sessionAwareFetch,
} from '@/lib/apiClient'
import { joinApiUrl } from '@/lib/apiUrl'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import {
    canWriteInternalTicketContent,
    canWritePublicTicketContent,
    type TicketRolePermissions,
} from './ticketAccess'
import {
    LatestRequestGate,
    lastPageAfterAppend,
} from './pagedRequestGate'
import { getAttachmentPreviewDecision } from './attachmentPreviewPolicy'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

const AttachmentPreviewDialog = lazy(() =>
    import('./AttachmentPreviewDialog').then((module) => ({
        default: module.AttachmentPreviewDialog,
    })),
)

const authHeaders = (contentType?: string) => {
    const headers = new Headers({ Accept: 'application/json' })
    const token = localStorage.getItem('token')
    if (token) {
        headers.set('Authorization', `Bearer ${token}`)
    }
    if (contentType) {
        headers.set('Content-Type', contentType)
    }
    return headers
}

const responseMessage = async (response: Response, fallback: string) => {
    const payload = await response.json().catch(() => ({}))
    return localizedApiErrorMessage(payload, response.status, fallback)
}

const listPayload = <T,>(payload: unknown): T[] => {
    if (!payload || typeof payload !== 'object') {
        return []
    }
    const data = 'data' in payload ? payload.data : payload
    if (Array.isArray(data)) {
        return data as T[]
    }
    if (data && typeof data === 'object' && 'items' in data && Array.isArray(data.items)) {
        return data.items as T[]
    }
    return []
}

const pagePayload = <T,>(payload: unknown) => {
    const items = listPayload<T>(payload)
    if (!payload || typeof payload !== 'object') {
        return { items, total: items.length, totalPages: items.length > 0 ? 1 : 0 }
    }
    const total = 'total' in payload && typeof payload.total === 'number'
        ? payload.total
        : items.length
    const totalPages = 'total_pages' in payload && typeof payload.total_pages === 'number'
        ? payload.total_pages
        : total > 0 ? 1 : 0
    return { items, total, totalPages }
}

const pagedURL = (path: string, page: number, pageSize = 25) =>
    `${joinApiUrl(apiBase, path)}?${new URLSearchParams({
        page: String(page),
        page_size: String(pageSize),
    })}`

const operationResourceVersion = (
    response: Response,
    payload: unknown,
): number | undefined => {
    const etag = response.headers.get('ETag')?.trim()
    const etagMatch = etag?.match(/^"v([1-9]\d*)"$/u)
    if (etagMatch) {
        return Number(etagMatch[1])
    }
    if (!payload || typeof payload !== 'object' || !('receipt' in payload)) {
        return undefined
    }
    const receipt = payload.receipt
    if (!receipt || typeof receipt !== 'object' || !('resource_version' in receipt)) {
        return undefined
    }
    const version = receipt.resource_version
    return typeof version === 'number' && Number.isSafeInteger(version) && version > 0
        ? version
        : undefined
}

const actorName = (comment: TicketComment) => {
    if (comment.user?.username) {
        return comment.user.username
    }
    if (comment.service_principal?.name) {
        return comment.service_principal.name
    }
    if (comment.actor?.type || comment.actor?.id) {
        const actorTypeLabels: Record<string, string> = {
            human: '用户',
            service_principal: 'AI 智能体',
            system: '系统',
        }
        const type = actorTypeLabels[comment.actor.type ?? ''] ?? '操作者'
        return comment.actor.id ? `${type}：${comment.actor.id}` : type
    }
    return '系统'
}

const formatBytes = (size: number) => {
    if (size < 1024) return `${size} B`
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
    return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

const scanLabel = {
    pending: '待安全扫描',
    clean: '扫描通过',
    infected: '检测到风险',
    error: '扫描失败',
}

type ReplyPageState = {
    items: TicketComment[]
    page: number
    total: number
    totalPages: number
    loading: boolean
    error: string
}

const visibilityAdjustedPageMeta = (
    rawItemCount: number,
    visibleItemCount: number,
    total: number,
    totalPages: number,
) => {
    if (rawItemCount === visibleItemCount) {
        return { total, totalPages }
    }
    return {
        total: visibleItemCount,
        totalPages: visibleItemCount > 0 ? 1 : 0,
    }
}

export const TicketConversationPanel = () => {
    const ticket = useRecordContext<Ticket>()
    const notify = useNotify()
    const { permissions } = usePermissions<TicketRolePermissions>()
    const { identity } = useGetIdentity()
    const [comments, setComments] = useState<TicketComment[]>([])
    const [attachments, setAttachments] = useState<TicketAttachment[]>([])
    const [commentsPage, setCommentsPage] = useState(1)
    const [commentsTotal, setCommentsTotal] = useState(0)
    const [commentsTotalPages, setCommentsTotalPages] = useState(0)
    const [attachmentsPage, setAttachmentsPage] = useState(1)
    const [attachmentsTotal, setAttachmentsTotal] = useState(0)
    const [attachmentsTotalPages, setAttachmentsTotalPages] = useState(0)
    const [replyPages, setReplyPages] = useState<Record<number, ReplyPageState>>({})
    const [commentsLoading, setCommentsLoading] = useState(true)
    const [commentsError, setCommentsError] = useState('')
    const [attachmentsLoading, setAttachmentsLoading] = useState(true)
    const [attachmentsError, setAttachmentsError] = useState('')
    const [comment, setComment] = useState('')
    const [commentType, setCommentType] = useState<'public' | 'internal'>('public')
    const [commentSubmitting, setCommentSubmitting] = useState(false)
    const [file, setFile] = useState<File>()
    const [attachmentPublic, setAttachmentPublic] = useState(false)
    const [attachmentSubmitting, setAttachmentSubmitting] = useState(false)
    const [previewAttachment, setPreviewAttachment] =
        useState<TicketAttachment | null>(null)
    const [resourceVersion, setResourceVersion] = useState(ticket?.version ?? 0)
    const commentsGate = useRef(new LatestRequestGate())
    const attachmentsGate = useRef(new LatestRequestGate())
    const replyGates = useRef(new Map<number, LatestRequestGate>())

    useEffect(() => {
        setResourceVersion(ticket?.version ?? 0)
    }, [ticket?.id, ticket?.version])

    const loadComments = useCallback(async () => {
        if (!ticket?.id) return
        const request = commentsGate.current.start()
        setCommentsLoading(true)
        setCommentsError('')
        try {
            const response = await sessionAwareFetch(
                pagedURL(
                    humanApiRoutes.listProjectTicketComments({
                        projectKey: await resolveActiveProjectKey(),
                        ticketID: Number(ticket.id),
                    }),
                    commentsPage,
                ),
                { headers: authHeaders(), signal: request.signal },
            )
            if (!response.ok) {
                throw new Error(
                    await responseMessage(response, '加载评论失败'),
                )
            }
            const page = pagePayload<TicketComment>(await response.json())
            if (!commentsGate.current.isCurrent(request.token)) return
            setComments(page.items)
            setCommentsTotal(page.total)
            setCommentsTotalPages(page.totalPages)
        } catch (loadError) {
            if (!commentsGate.current.isCurrent(request.token)) return
            setCommentsError(
                localizedUnknownErrorMessage(loadError, '加载评论失败'),
            )
        } finally {
            if (commentsGate.current.isCurrent(request.token)) {
                setCommentsLoading(false)
            }
        }
    }, [commentsPage, ticket?.id])

    const loadAttachments = useCallback(async () => {
        if (!ticket?.id) return
        const request = attachmentsGate.current.start()
        setAttachmentsLoading(true)
        setAttachmentsError('')
        try {
            const response = await sessionAwareFetch(
                pagedURL(
                    humanApiRoutes.listProjectTicketAttachments({
                        projectKey: await resolveActiveProjectKey(),
                        ticketID: Number(ticket.id),
                    }),
                    attachmentsPage,
                ),
                { headers: authHeaders(), signal: request.signal },
            )
            if (!response.ok) {
                throw new Error(
                    await responseMessage(response, '加载附件失败'),
                )
            }
            const page = pagePayload<TicketAttachment>(await response.json())
            if (!attachmentsGate.current.isCurrent(request.token)) return
            setAttachments(page.items)
            setAttachmentsTotal(page.total)
            setAttachmentsTotalPages(page.totalPages)
        } catch (loadError) {
            if (!attachmentsGate.current.isCurrent(request.token)) return
            setAttachmentsError(
                localizedUnknownErrorMessage(loadError, '加载附件失败'),
            )
        } finally {
            if (attachmentsGate.current.isCurrent(request.token)) {
                setAttachmentsLoading(false)
            }
        }
    }, [attachmentsPage, ticket?.id])

    useEffect(() => {
        void loadComments()
    }, [loadComments])

    useEffect(() => {
        void loadAttachments()
    }, [loadAttachments])

    useEffect(() => {
        setCommentsPage(1)
        setAttachmentsPage(1)
        setReplyPages({})
        setPreviewAttachment(null)
    }, [ticket?.id])

    useEffect(() => () => {
        commentsGate.current.abort()
        attachmentsGate.current.abort()
        for (const gate of replyGates.current.values()) {
            gate.abort()
        }
        replyGates.current.clear()
    }, [])

    useEffect(() => {
        for (const gate of replyGates.current.values()) {
            gate.abort()
        }
        replyGates.current.clear()
        setReplyPages({})
    }, [commentsPage, ticket?.id])

    const loadReplies = useCallback(async (commentID: number, page: number) => {
        if (!ticket?.id) return
        let gate = replyGates.current.get(commentID)
        if (!gate) {
            gate = new LatestRequestGate()
            replyGates.current.set(commentID, gate)
        }
        const request = gate.start()
        setReplyPages((current) => ({
            ...current,
            [commentID]: {
                items: current[commentID]?.items ?? [],
                page,
                total: current[commentID]?.total ?? 0,
                totalPages: current[commentID]?.totalPages ?? 0,
                loading: true,
                error: '',
            },
        }))
        try {
            const path = humanApiRoutes.listProjectTicketCommentReplies({
                projectKey: await resolveActiveProjectKey(),
                ticketID: Number(ticket.id),
                commentID,
            })
            const response = await sessionAwareFetch(pagedURL(path, page), {
                headers: authHeaders(),
                signal: request.signal,
            })
            if (!response.ok) {
                throw new Error(await responseMessage(response, '加载回复失败'))
            }
            const payload = pagePayload<TicketComment>(await response.json())
            if (!gate.isCurrent(request.token)) return
            setReplyPages((current) => ({
                ...current,
                [commentID]: {
                    items: payload.items,
                    page,
                    total: payload.total,
                    totalPages: payload.totalPages,
                    loading: false,
                    error: '',
                },
            }))
        } catch (replyError) {
            if (!gate.isCurrent(request.token)) return
            setReplyPages((current) => ({
                ...current,
                [commentID]: {
                    items: current[commentID]?.items ?? [],
                    page,
                    total: current[commentID]?.total ?? 0,
                    totalPages: current[commentID]?.totalPages ?? 0,
                    loading: false,
                    error: localizedUnknownErrorMessage(replyError, '加载回复失败'),
                },
            }))
        }
    }, [ticket?.id])

    const fetchAttachmentContent = useCallback(
        async (attachment: TicketAttachment, signal: AbortSignal) => {
            if (!ticket?.id) {
                throw new Error('当前工单不可用，无法预览附件')
            }
            const attachmentPath =
                humanApiRoutes.downloadProjectTicketAttachment({
                    projectKey: await resolveActiveProjectKey(),
                    ticketID: Number(ticket.id),
                    attachmentID: attachment.id,
                })
            return sessionAwareFetch(
                joinApiUrl(apiBase, attachmentPath),
                {
                    headers: authHeaders(),
                    signal,
                },
            )
        },
        [ticket?.id],
    )

    if (!ticket) {
        return null
    }
    const canWritePublic = canWritePublicTicketContent(
        ticket,
        permissions?.project_role,
        identity?.id,
    )
    const canWriteInternal = canWriteInternalTicketContent(
        ticket,
        permissions?.project_role,
        identity?.id,
    )
    const visibleComments = canWriteInternal
        ? comments
        : comments.filter((item) => item.type !== 'internal')
    const visibleAttachments = canWriteInternal
        ? attachments
        : attachments.filter((attachment) => attachment.is_public)
    const visibleCommentMeta = visibilityAdjustedPageMeta(
        comments.length,
        visibleComments.length,
        commentsTotal,
        commentsTotalPages,
    )
    const visibleAttachmentMeta = visibilityAdjustedPageMeta(
        attachments.length,
        visibleAttachments.length,
        attachmentsTotal,
        attachmentsTotalPages,
    )
    const visibleReplyPages = Object.fromEntries(
        Object.entries(replyPages).map(([commentID, state]) => {
            const items = canWriteInternal
                ? state.items
                : state.items.filter((reply) => reply.type !== 'internal')
            const meta = visibilityAdjustedPageMeta(
                state.items.length,
                items.length,
                state.total,
                state.totalPages,
            )
            return [
                commentID,
                {
                    ...state,
                    items,
                    total: meta.total,
                    totalPages: meta.totalPages,
                },
            ]
        }),
    ) as Record<number, ReplyPageState>
    const submitComment = async (event: FormEvent) => {
        event.preventDefault()
        const content = comment.trim()
        if (!content) return
        setCommentSubmitting(true)
        try {
            const headers = authHeaders('application/json')
            headers.set('If-Match', `"v${resourceVersion}"`)
            const request: CreateTicketCommentRequest = {
                content,
                content_type: 'text',
                type: canWriteInternal ? commentType : 'public',
            }
            const response = await sessionAwareFetch(
                joinApiUrl(
                    apiBase,
                    humanApiRoutes.createProjectTicketComment({
                        projectKey: await resolveActiveProjectKey(),
                        ticketID: Number(ticket.id),
                    }),
                ),
                {
                    method: 'POST',
                    headers,
                    body: JSON.stringify(request),
                },
            )
            if (!response.ok) {
                throw new Error(await responseMessage(response, '添加评论失败'))
            }
            const payload = await response.json().catch(() => ({}))
            const nextVersion = operationResourceVersion(response, payload)
            if (nextVersion) {
                setResourceVersion(nextVersion)
            }
            setComment('')
            notify('评论已添加', { type: 'success' })
            const lastPage = lastPageAfterAppend(commentsTotal)
            if (commentsPage === lastPage) {
                await loadComments()
            } else {
                setCommentsPage(lastPage)
            }
        } catch (submitError) {
            notify(
                localizedUnknownErrorMessage(submitError, '添加评论失败'),
                { type: 'error' },
            )
        } finally {
            setCommentSubmitting(false)
        }
    }

    const submitAttachment = async () => {
        if (!file) return
        setAttachmentSubmitting(true)
        try {
            const form = new FormData()
            form.append('file', file)
            form.append(
                'visibility',
                canWriteInternal && !attachmentPublic ? 'internal' : 'public',
            )
            const headers = authHeaders()
            headers.set('If-Match', `"v${resourceVersion}"`)
            const response = await sessionAwareFetch(
                joinApiUrl(
                    apiBase,
                    humanApiRoutes.uploadProjectTicketAttachment({
                        projectKey: await resolveActiveProjectKey(),
                        ticketID: Number(ticket.id),
                    }),
                ),
                {
                    method: 'POST',
                    headers,
                    body: form,
                },
            )
            if (!response.ok) {
                throw new Error(await responseMessage(response, '上传附件失败'))
            }
            const payload = await response.json().catch(() => ({}))
            const nextVersion = operationResourceVersion(response, payload)
            if (nextVersion) {
                setResourceVersion(nextVersion)
            }
            setFile(undefined)
            notify('附件已上传，等待安全扫描', { type: 'success' })
            if (attachmentsPage === 1) {
                await loadAttachments()
            } else {
                setAttachmentsPage(1)
            }
        } catch (uploadError) {
            notify(
                localizedUnknownErrorMessage(uploadError, '上传附件失败'),
                { type: 'error' },
            )
        } finally {
            setAttachmentSubmitting(false)
        }
    }

    const downloadAttachment = async (attachment: TicketAttachment) => {
        try {
            const attachmentPath =
                humanApiRoutes.downloadProjectTicketAttachment({
                    projectKey: await resolveActiveProjectKey(),
                    ticketID: Number(ticket.id),
                    attachmentID: attachment.id,
                })
            const response = await sessionAwareFetch(
                joinApiUrl(apiBase, attachmentPath),
                { headers: authHeaders() },
            )
            if (!response.ok) {
                throw new Error(await responseMessage(response, '下载附件失败'))
            }
            const blob = await response.blob()
            const objectUrl = URL.createObjectURL(blob)
            const link = document.createElement('a')
            link.href = objectUrl
            link.download = attachment.original_name
            link.click()
            URL.revokeObjectURL(objectUrl)
        } catch (downloadError) {
            notify(
                localizedUnknownErrorMessage(downloadError, '下载附件失败'),
                { type: 'error' },
            )
        }
    }

    return (
        <Stack spacing={3}>
            {canWritePublic ? (
                <Card variant="outlined" role="region" aria-label="添加工单评论">
                    <CardContent>
                        <Typography variant="h6" gutterBottom>
                            添加评论
                        </Typography>
                        <Box component="form" onSubmit={submitComment}>
                            <Stack spacing={2}>
                                <TextField
                                    label="评论内容"
                                    value={comment}
                                    onChange={(event) => setComment(event.target.value)}
                                    multiline
                                    minRows={3}
                                    required
                                    slotProps={{ htmlInput: { maxLength: 10000 } }}
                                />
                                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
                                    {canWriteInternal ? (
                                        <FormControl sx={{ minWidth: 180 }}>
                                            <InputLabel id="comment-visibility-label">可见性</InputLabel>
                                            <Select
                                                labelId="comment-visibility-label"
                                                label="可见性"
                                                value={commentType}
                                                onChange={(event) =>
                                                    setCommentType(event.target.value as 'public' | 'internal')
                                                }
                                            >
                                                <MenuItem value="public">公开评论</MenuItem>
                                                <MenuItem value="internal">内部评论</MenuItem>
                                            </Select>
                                        </FormControl>
                                    ) : (
                                        <Chip label="公开评论" variant="outlined" />
                                    )}
                                    <Button
                                        type="submit"
                                        variant="contained"
                                        startIcon={
                                            commentSubmitting ? (
                                                <CircularProgress size={16} color="inherit" />
                                            ) : (
                                                <SendIcon />
                                            )
                                        }
                                        disabled={commentSubmitting || !comment.trim()}
                                    >
                                        添加评论
                                    </Button>
                                </Stack>
                            </Stack>
                        </Box>
                    </CardContent>
                </Card>
            ) : (
                <Alert severity="info">当前工单为只读，不能添加评论或上传附件。</Alert>
            )}

            {commentsError && (
                <Alert
                    severity="error"
                    action={
                        <Button
                            color="inherit"
                            size="small"
                            onClick={() => void loadComments()}
                        >
                            重试
                        </Button>
                    }
                >
                    {commentsError}
                </Alert>
            )}
            {commentsLoading ? (
                <Box sx={{ display: 'flex', justifyContent: 'center', py: 3 }}>
                    <CircularProgress
                        size={28}
                        aria-label="正在加载工单评论"
                    />
                </Box>
            ) : (
                <Card variant="outlined" role="region" aria-label="工单评论记录">
                    <CardContent>
                        <Typography variant="h6">
                            评论记录（{visibleCommentMeta.total}）
                        </Typography>
                        {visibleComments.length === 0 ? (
                            <Typography color="text.secondary" sx={{ mt: 2 }}>
                                暂无评论
                            </Typography>
                        ) : (
                            <List disablePadding>
                                {visibleComments.map((item, index) => (
                                    <Box key={item.id}>
                                        {index > 0 && <Divider />}
                                        <ListItem alignItems="flex-start">
                                            <ListItemText
                                                primary={
                                                    <Stack
                                                        direction="row"
                                                        spacing={1}
                                                        sx={{ alignItems: 'center' }}
                                                    >
                                                        <Typography sx={{ fontWeight: 600 }}>
                                                            {actorName(item)}
                                                        </Typography>
                                                        <Chip
                                                            size="small"
                                                            label={
                                                                item.type === 'internal'
                                                                    ? '内部'
                                                                    : item.type === 'system'
                                                                      ? '系统'
                                                                      : '公开'
                                                            }
                                                            color={
                                                                item.type === 'internal'
                                                                    ? 'warning'
                                                                    : 'default'
                                                            }
                                                        />
                                                    </Stack>
                                                }
                                                secondary={
                                                    <>
                                                        <Box
                                                            component="span"
                                                            sx={{
                                                                display: 'block',
                                                                color: 'text.primary',
                                                                whiteSpace: 'pre-wrap',
                                                                my: 1,
                                                            }}
                                                        >
                                                            {item.content}
                                                        </Box>
                                                        {new Date(item.created_at).toLocaleString('zh-CN')}
                                                        {item.reply_count > 0 && (
                                                            <Box component="span" sx={{ display: 'block', mt: 1 }}>
                                                                <Button
                                                                    size="small"
                                                                    onClick={() => {
                                                                        if (replyPages[item.id]) {
                                                                            replyGates.current.get(item.id)?.abort()
                                                                            replyGates.current.delete(item.id)
                                                                            setReplyPages((current) => {
                                                                                const next = { ...current }
                                                                                delete next[item.id]
                                                                                return next
                                                                            })
                                                                        } else {
                                                                            void loadReplies(item.id, 1)
                                                                        }
                                                                    }}
                                                                >
                                                                    {replyPages[item.id]
                                                                        ? '收起回复'
                                                                        : canWriteInternal
                                                                          ? `查看 ${item.reply_count} 条回复`
                                                                          : '查看回复'}
                                                                </Button>
                                                            </Box>
                                                        )}
                                                    </>
                                                }
                                            />
                                        </ListItem>
                                        {visibleReplyPages[item.id] && (
                                            <Box sx={{ pl: { xs: 2, sm: 6 }, pb: 2 }}>
                                                {visibleReplyPages[item.id].error ? (
                                                    <Alert
                                                        severity="error"
                                                        action={
                                                            <Button
                                                                color="inherit"
                                                                size="small"
                                                                onClick={() =>
                                                                    void loadReplies(
                                                                        item.id,
                                                                        visibleReplyPages[item.id].page,
                                                                    )
                                                                }
                                                            >
                                                                重试
                                                            </Button>
                                                        }
                                                    >
                                                        {visibleReplyPages[item.id].error}
                                                    </Alert>
                                                ) : visibleReplyPages[item.id].loading ? (
                                                    <CircularProgress size={20} aria-label="正在加载回复" />
                                                ) : visibleReplyPages[item.id].items.length === 0 ? (
                                                    <Typography color="text.secondary">
                                                        暂无可见回复
                                                    </Typography>
                                                ) : (
                                                    <Stack spacing={1}>
                                                        {visibleReplyPages[item.id].items.map((reply) => (
                                                            <Paper
                                                                key={reply.id}
                                                                variant="outlined"
                                                                sx={{ p: 1.5 }}
                                                            >
                                                                <Typography sx={{ fontWeight: 600 }}>
                                                                    {actorName(reply)}
                                                                </Typography>
                                                                <Typography sx={{ whiteSpace: 'pre-wrap' }}>
                                                                    {reply.content}
                                                                </Typography>
                                                                <Typography variant="caption" color="text.secondary">
                                                                    {new Date(reply.created_at).toLocaleString('zh-CN')}
                                                                </Typography>
                                                            </Paper>
                                                        ))}
                                                        {visibleReplyPages[item.id].totalPages > 1 && (
                                                            <Pagination
                                                                page={visibleReplyPages[item.id].page}
                                                                count={visibleReplyPages[item.id].totalPages}
                                                                onChange={(_event, nextPage) =>
                                                                    void loadReplies(item.id, nextPage)
                                                                }
                                                                size="small"
                                                                aria-label={`评论 ${item.id} 的回复分页`}
                                                            />
                                                        )}
                                                    </Stack>
                                                )}
                                            </Box>
                                        )}
                                    </Box>
                                ))}
                            </List>
                        )}
                        {visibleCommentMeta.totalPages > 1 && (
                            <Pagination
                                page={commentsPage}
                                count={visibleCommentMeta.totalPages}
                                onChange={(_event, page) => setCommentsPage(page)}
                                sx={{ mt: 2 }}
                                aria-label="评论分页"
                            />
                        )}
                    </CardContent>
                </Card>
            )}

            <Card variant="outlined" role="region" aria-label="工单附件">
                <CardContent>
                    <Typography variant="h6" gutterBottom>
                        附件（{visibleAttachmentMeta.total}）
                    </Typography>
                    <Stack spacing={2}>
                        {canWritePublic && <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
                            <Button
                                component="label"
                                variant="outlined"
                                startIcon={<AttachFileIcon />}
                            >
                                选择附件
                                <input
                                    hidden
                                    type="file"
                                    onChange={(event) => setFile(event.target.files?.[0])}
                                />
                            </Button>
                            <Typography sx={{ alignSelf: 'center' }} color="text.secondary">
                                {file?.name ?? '未选择文件'}
                            </Typography>
                            {canWriteInternal ? (
                                <FormControl sx={{ minWidth: 160 }}>
                                    <InputLabel id="attachment-visibility-label">可见性</InputLabel>
                                    <Select
                                        labelId="attachment-visibility-label"
                                        label="可见性"
                                        value={attachmentPublic ? 'public' : 'internal'}
                                        onChange={(event) =>
                                            setAttachmentPublic(event.target.value === 'public')
                                        }
                                    >
                                        <MenuItem value="internal">内部附件</MenuItem>
                                        <MenuItem value="public">公开附件</MenuItem>
                                    </Select>
                                </FormControl>
                            ) : (
                                <Chip label="公开附件" variant="outlined" />
                            )}
                            <Button
                                variant="contained"
                                onClick={submitAttachment}
                                disabled={!file || attachmentSubmitting}
                            >
                                {attachmentSubmitting ? '上传中…' : '上传附件'}
                            </Button>
                        </Stack>}

                        {attachmentsError && (
                            <Alert
                                severity="error"
                                action={
                                    <Button
                                        color="inherit"
                                        size="small"
                                        onClick={() => void loadAttachments()}
                                    >
                                        重试
                                    </Button>
                                }
                            >
                                {attachmentsError}
                            </Alert>
                        )}
                        {attachmentsLoading ? (
                            <Box
                                role="status"
                                aria-label="正在加载工单附件"
                                sx={{
                                    display: 'flex',
                                    justifyContent: 'center',
                                    py: 3,
                                }}
                            >
                                <CircularProgress size={24} />
                            </Box>
                        ) : attachmentsError ? null : visibleAttachments.length === 0 ? (
                            <Typography color="text.secondary">暂无附件</Typography>
                        ) : (
                            <List disablePadding>
                                {visibleAttachments.map((attachment, index) => {
                                    const previewDecision =
                                        getAttachmentPreviewDecision(attachment)
                                    return <Box key={attachment.id}>
                                        {index > 0 && <Divider />}
                                        <ListItem
                                            sx={{
                                                gap: 1,
                                                alignItems: 'center',
                                                flexWrap: { xs: 'wrap', sm: 'nowrap' },
                                            }}
                                        >
                                            <ListItemText
                                                sx={{
                                                    minWidth: 0,
                                                    flexBasis: { xs: '100%', sm: 'auto' },
                                                }}
                                                primary={attachment.original_name}
                                                secondary={`${formatBytes(attachment.file_size)} · ${
                                                    scanLabel[attachment.virus_scan]
                                                } · ${attachment.is_public ? '公开' : '内部'}${
                                                    attachment.hash
                                                        ? ` · SHA-256 ${attachment.hash.slice(0, 12)}…`
                                                        : ''
                                                }${
                                                    !previewDecision.eligible
                                                        ? ` · ${previewDecision.reason}`
                                                        : ''
                                                }`}
                                            />
                                            <Stack
                                                direction="row"
                                                spacing={1}
                                                sx={{ flexShrink: 0, ml: { sm: 'auto' } }}
                                            >
                                                <Button
                                                    size="small"
                                                    startIcon={<PreviewIcon />}
                                                    disabled={!previewDecision.eligible}
                                                    onClick={() =>
                                                        setPreviewAttachment(attachment)
                                                    }
                                                >
                                                    预览
                                                </Button>
                                                <Button
                                                    size="small"
                                                    startIcon={<DownloadIcon />}
                                                    disabled={attachment.virus_scan !== 'clean'}
                                                    onClick={() => downloadAttachment(attachment)}
                                                >
                                                    下载
                                                </Button>
                                            </Stack>
                                        </ListItem>
                                    </Box>
                                })}
                            </List>
                        )}
                        {!attachmentsLoading &&
                            !attachmentsError &&
                            visibleAttachmentMeta.totalPages > 1 && (
                            <Pagination
                                page={attachmentsPage}
                                count={visibleAttachmentMeta.totalPages}
                                onChange={(_event, page) => setAttachmentsPage(page)}
                                aria-label="附件分页"
                            />
                        )}
                    </Stack>
                </CardContent>
            </Card>
            {previewAttachment && (
                <Suspense
                    fallback={
                        <CircularProgress
                            size={24}
                            aria-label="正在打开附件预览"
                        />
                    }
                >
                    <AttachmentPreviewDialog
                        open
                        attachment={previewAttachment}
                        fetchContent={fetchAttachmentContent}
                        onClose={() => setPreviewAttachment(null)}
                    />
                </Suspense>
            )}
        </Stack>
    )
}
