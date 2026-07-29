import { FormEvent, useCallback, useEffect, useState } from 'react'
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
    Select,
    Stack,
    TextField,
    Typography,
} from '@mui/material'
import {
    AttachFile as AttachFileIcon,
    CloudDownload as DownloadIcon,
    Send as SendIcon,
} from '@mui/icons-material'
import {
    useGetIdentity,
    useNotify,
    usePermissions,
    useRecordContext,
} from 'react-admin'
import type { Ticket } from '@/types'
import {
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import { canMutateTicket, type TicketRolePermissions } from './ticketAccess'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

type ActorRef = {
    type?: string
    id?: string
}

type TicketComment = {
    id: number
    content: string
    type: 'public' | 'internal' | 'system'
    created_at: string
    actor?: ActorRef
    user?: {
        username?: string
        first_name?: string
        last_name?: string
    }
    service_principal?: {
        name?: string
    }
}

type TicketAttachment = {
    id: number
    original_name: string
    file_size: number
    mime_type?: string
    hash?: string
    virus_scan: 'pending' | 'clean' | 'infected' | 'error'
    is_public: boolean
    created_at: string
}

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

export const TicketConversationPanel = () => {
    const ticket = useRecordContext<Ticket>()
    const notify = useNotify()
    const { permissions } = usePermissions<TicketRolePermissions>()
    const { identity } = useGetIdentity()
    const [comments, setComments] = useState<TicketComment[]>([])
    const [attachments, setAttachments] = useState<TicketAttachment[]>([])
    const [loading, setLoading] = useState(true)
    const [error, setError] = useState('')
    const [comment, setComment] = useState('')
    const [commentType, setCommentType] = useState<'public' | 'internal'>('public')
    const [commentSubmitting, setCommentSubmitting] = useState(false)
    const [file, setFile] = useState<File>()
    const [attachmentPublic, setAttachmentPublic] = useState(false)
    const [attachmentSubmitting, setAttachmentSubmitting] = useState(false)
    const [resourceVersion, setResourceVersion] = useState(ticket?.version ?? 0)

    useEffect(() => {
        setResourceVersion(ticket?.version ?? 0)
    }, [ticket?.id, ticket?.version])

    const loadConversation = useCallback(async () => {
        if (!ticket?.id) return
        setLoading(true)
        setError('')
        try {
            const [commentsResponse, attachmentsResponse] = await Promise.all([
                fetch(`${apiBase}/tickets/${ticket.id}/comments`, {
                    headers: authHeaders(),
                }),
                fetch(`${apiBase}/tickets/${ticket.id}/attachments`, {
                    headers: authHeaders(),
                }),
            ])
            if (!commentsResponse.ok) {
                throw new Error(
                    await responseMessage(commentsResponse, '加载评论失败'),
                )
            }
            if (!attachmentsResponse.ok) {
                throw new Error(
                    await responseMessage(attachmentsResponse, '加载附件失败'),
                )
            }
            const [commentsPayload, attachmentsPayload] = await Promise.all([
                commentsResponse.json(),
                attachmentsResponse.json(),
            ])
            setComments(listPayload<TicketComment>(commentsPayload))
            setAttachments(listPayload<TicketAttachment>(attachmentsPayload))
        } catch (loadError) {
            setError(localizedUnknownErrorMessage(loadError, '加载工单会话失败'))
        } finally {
            setLoading(false)
        }
    }, [ticket?.id])

    useEffect(() => {
        void loadConversation()
    }, [loadConversation])

    if (!ticket) {
        return null
    }
    const canWrite = canMutateTicket(ticket, permissions?.role, identity?.id)

    const submitComment = async (event: FormEvent) => {
        event.preventDefault()
        const content = comment.trim()
        if (!content) return
        setCommentSubmitting(true)
        try {
            const headers = authHeaders('application/json')
            headers.set('If-Match', `"v${resourceVersion}"`)
            const response = await fetch(`${apiBase}/tickets/${ticket.id}/comments`, {
                method: 'POST',
                headers,
                body: JSON.stringify({
                    content,
                    content_type: 'text',
                    type: commentType,
                }),
            })
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
            await loadConversation()
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
            form.append('visibility', attachmentPublic ? 'public' : 'internal')
            const headers = authHeaders()
            headers.set('If-Match', `"v${resourceVersion}"`)
            const response = await fetch(`${apiBase}/tickets/${ticket.id}/attachments`, {
                method: 'POST',
                headers,
                body: form,
            })
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
            await loadConversation()
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
            const response = await fetch(
                `${apiBase}/tickets/${ticket.id}/attachments/${attachment.id}/content`,
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
            {canWrite ? (
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

            {error && <Alert severity="error">{error}</Alert>}
            {loading ? (
                <Box sx={{ display: 'flex', justifyContent: 'center', py: 3 }}>
                    <CircularProgress size={28} />
                </Box>
            ) : (
                <Card variant="outlined" role="region" aria-label="工单评论记录">
                    <CardContent>
                        <Typography variant="h6">评论记录</Typography>
                        {comments.length === 0 ? (
                            <Typography color="text.secondary" sx={{ mt: 2 }}>
                                暂无评论
                            </Typography>
                        ) : (
                            <List disablePadding>
                                {comments.map((item, index) => (
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
                                                    </>
                                                }
                                            />
                                        </ListItem>
                                    </Box>
                                ))}
                            </List>
                        )}
                    </CardContent>
                </Card>
            )}

            <Card variant="outlined" role="region" aria-label="工单附件">
                <CardContent>
                    <Typography variant="h6" gutterBottom>
                        附件
                    </Typography>
                    <Stack spacing={2}>
                        {canWrite && <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
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
                            <Button
                                variant="contained"
                                onClick={submitAttachment}
                                disabled={!file || attachmentSubmitting}
                            >
                                {attachmentSubmitting ? '上传中…' : '上传附件'}
                            </Button>
                        </Stack>}

                        {attachments.length === 0 ? (
                            <Typography color="text.secondary">暂无附件</Typography>
                        ) : (
                            <List disablePadding>
                                {attachments.map((attachment, index) => (
                                    <Box key={attachment.id}>
                                        {index > 0 && <Divider />}
                                        <ListItem
                                            secondaryAction={
                                                <Button
                                                    size="small"
                                                    startIcon={<DownloadIcon />}
                                                    disabled={attachment.virus_scan !== 'clean'}
                                                    onClick={() => downloadAttachment(attachment)}
                                                >
                                                    下载
                                                </Button>
                                            }
                                        >
                                            <ListItemText
                                                primary={attachment.original_name}
                                                secondary={`${formatBytes(attachment.file_size)} · ${
                                                    scanLabel[attachment.virus_scan]
                                                } · ${attachment.is_public ? '公开' : '内部'}${
                                                    attachment.hash
                                                        ? ` · SHA-256 ${attachment.hash.slice(0, 12)}…`
                                                        : ''
                                                }`}
                                            />
                                        </ListItem>
                                    </Box>
                                ))}
                            </List>
                        )}
                    </Stack>
                </CardContent>
            </Card>
        </Stack>
    )
}
