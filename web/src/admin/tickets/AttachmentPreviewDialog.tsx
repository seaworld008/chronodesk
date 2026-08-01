import { useEffect, useMemo, useState } from 'react'
import {
    Alert,
    Box,
    Button,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Stack,
    Typography,
} from '@mui/material'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { TicketAttachment } from '@/lib/generated/human-api'
import {
    getAttachmentPreviewDecision,
    isPreviewResponseMimeCompatible,
    normalizeAttachmentMimeType,
} from './attachmentPreviewPolicy'

type AttachmentPreviewDialogProps = {
    open: boolean
    attachment: TicketAttachment
    fetchContent: (
        attachment: TicketAttachment,
        signal: AbortSignal,
    ) => Promise<Response>
    onClose: () => void
}

type PreviewState =
    | { status: 'loading'; text: ''; objectUrl: ''; error: '' }
    | { status: 'ready'; text: string; objectUrl: string; error: '' }
    | { status: 'error'; text: ''; objectUrl: ''; error: string }

const ALLOWED_MARKDOWN_ELEMENTS = [
    'a',
    'blockquote',
    'br',
    'code',
    'del',
    'em',
    'h1',
    'h2',
    'h3',
    'h4',
    'h5',
    'h6',
    'hr',
    'li',
    'ol',
    'p',
    'pre',
    'strong',
    'table',
    'tbody',
    'td',
    'th',
    'thead',
    'tr',
    'ul',
]

const safeMarkdownLink = (url: string) => {
    const trimmed = url.trim()
    if (trimmed.startsWith('#')) return trimmed
    try {
        const parsed = new URL(trimmed)
        return ['http:', 'https:', 'mailto:'].includes(parsed.protocol)
            ? trimmed
            : ''
    } catch {
        return ''
    }
}

const boundedResponseBlob = async (
    response: Response,
    maxBytes: number,
    signal: AbortSignal,
) => {
    const declaredLength = response.headers.get('Content-Length')
    if (declaredLength) {
        const parsedLength = Number(declaredLength)
        if (
            !Number.isSafeInteger(parsedLength) ||
            parsedLength < 0 ||
            parsedLength > maxBytes
        ) {
            throw new Error('附件内容超过安全预览上限，已停止加载')
        }
    }

    if (!response.body) {
        const blob = await response.blob()
        if (blob.size > maxBytes) {
            throw new Error('附件内容超过安全预览上限，已停止加载')
        }
        return blob
    }

    const reader = response.body.getReader()
    const chunks: Uint8Array<ArrayBuffer>[] = []
    let total = 0
    try {
        while (true) {
            if (signal.aborted) {
                throw new DOMException('请求已取消', 'AbortError')
            }
            const { done, value } = await reader.read()
            if (done) break
            total += value.byteLength
            if (total > maxBytes) {
                await reader.cancel()
                throw new Error('附件内容超过安全预览上限，已停止加载')
            }
            const copied = new Uint8Array(value.byteLength)
            copied.set(value)
            chunks.push(copied)
        }
    } finally {
        reader.releaseLock()
    }
    return new Blob(chunks, {
        type: normalizeAttachmentMimeType(
            response.headers.get('Content-Type'),
        ),
    })
}

const PreviewContent = ({
    attachment,
    kind,
    objectUrl,
    text,
}: {
    attachment: TicketAttachment
    kind: NonNullable<
        ReturnType<typeof getAttachmentPreviewDecision>['kind']
    >
    objectUrl: string
    text: string
}) => {
    if (kind === 'image') {
        return (
            <Box
                component="img"
                src={objectUrl}
                alt={`${attachment.original_name} 的附件预览`}
                loading="lazy"
                decoding="async"
                sx={{
                    display: 'block',
                    maxWidth: '100%',
                    maxHeight: '65vh',
                    mx: 'auto',
                    objectFit: 'contain',
                }}
            />
        )
    }
    if (kind === 'audio') {
        return (
            <Box
                component="audio"
                src={objectUrl}
                controls
                preload="metadata"
                aria-label={`${attachment.original_name} 的音频预览`}
                sx={{ width: '100%' }}
            />
        )
    }
    if (kind === 'video') {
        return (
            <Box
                component="video"
                src={objectUrl}
                controls
                preload="metadata"
                aria-label={`${attachment.original_name} 的视频预览`}
                sx={{
                    display: 'block',
                    width: '100%',
                    maxHeight: '65vh',
                    bgcolor: 'common.black',
                }}
            />
        )
    }
    if (kind === 'pdf') {
        return (
            <Box
                component="iframe"
                src={objectUrl}
                title={`${attachment.original_name} 的 PDF 预览`}
                sandbox=""
                referrerPolicy="no-referrer"
                sx={{
                    display: 'block',
                    width: '100%',
                    height: { xs: '60vh', md: '70vh' },
                    border: 0,
                    bgcolor: 'background.default',
                }}
            />
        )
    }
    if (kind === 'markdown') {
        return (
            <Box
                data-testid="markdown-attachment-preview"
                sx={{
                    overflowWrap: 'anywhere',
                    '& pre': {
                        overflowX: 'auto',
                        p: 1.5,
                        bgcolor: 'action.hover',
                    },
                    '& table': {
                        borderCollapse: 'collapse',
                        width: '100%',
                    },
                    '& th, & td': {
                        border: 1,
                        borderColor: 'divider',
                        p: 1,
                        textAlign: 'left',
                    },
                }}
            >
                <ReactMarkdown
                    remarkPlugins={[remarkGfm]}
                    skipHtml
                    allowedElements={ALLOWED_MARKDOWN_ELEMENTS}
                    urlTransform={safeMarkdownLink}
                    components={{
                        a: ({ children, href, title }) => (
                            <a
                                href={href}
                                title={title}
                                target="_blank"
                                rel="noopener noreferrer nofollow"
                            >
                                {children}
                            </a>
                        ),
                    }}
                >
                    {text}
                </ReactMarkdown>
            </Box>
        )
    }
    return (
        <Box
            component="pre"
            data-testid="text-attachment-preview"
            sx={{
                m: 0,
                whiteSpace: 'pre-wrap',
                overflowWrap: 'anywhere',
                fontFamily: 'monospace',
                fontSize: '0.875rem',
            }}
        >
            {text}
        </Box>
    )
}

export const AttachmentPreviewDialog = ({
    open,
    attachment,
    fetchContent,
    onClose,
}: AttachmentPreviewDialogProps) => {
    const decision = useMemo(
        () => getAttachmentPreviewDecision(attachment),
        [attachment],
    )
    const [preview, setPreview] = useState<PreviewState>({
        status: 'loading',
        text: '',
        objectUrl: '',
        error: '',
    })

    useEffect(() => {
        if (!open) return
        if (!decision.eligible) {
            setPreview({
                status: 'error',
                text: '',
                objectUrl: '',
                error: decision.reason,
            })
            return
        }

        const controller = new AbortController()
        let generatedObjectUrl = ''
        setPreview({
            status: 'loading',
            text: '',
            objectUrl: '',
            error: '',
        })
        void (async () => {
            try {
                const response = await fetchContent(
                    attachment,
                    controller.signal,
                )
                if (!response.ok) {
                    throw new Error('附件内容加载失败，请稍后重试')
                }
                if (
                    !isPreviewResponseMimeCompatible(
                        decision,
                        response.headers.get('Content-Type'),
                    )
                ) {
                    throw new Error(
                        '附件响应类型与安全预览声明不一致，已停止预览',
                    )
                }
                const blob = await boundedResponseBlob(
                    response,
                    decision.maxBytes,
                    controller.signal,
                )
                if (controller.signal.aborted) return
                if (decision.kind === 'text' || decision.kind === 'markdown') {
                    const text = await blob.text()
                    if (controller.signal.aborted) return
                    setPreview({
                        status: 'ready',
                        text,
                        objectUrl: '',
                        error: '',
                    })
                    return
                }
                generatedObjectUrl = URL.createObjectURL(blob)
                setPreview({
                    status: 'ready',
                    text: '',
                    objectUrl: generatedObjectUrl,
                    error: '',
                })
            } catch (error) {
                if (
                    controller.signal.aborted ||
                    (error instanceof DOMException && error.name === 'AbortError')
                ) {
                    return
                }
                setPreview({
                    status: 'error',
                    text: '',
                    objectUrl: '',
                    error: error instanceof Error && /[\u3400-\u9fff]/u.test(error.message)
                        ? error.message
                        : '附件预览失败，请下载后查看',
                })
            }
        })()

        return () => {
            controller.abort()
            if (generatedObjectUrl) {
                URL.revokeObjectURL(generatedObjectUrl)
            }
        }
    }, [attachment, decision, fetchContent, open])

    return (
        <Dialog
            open={open}
            onClose={onClose}
            fullWidth
            maxWidth="lg"
            aria-labelledby="attachment-preview-dialog-title"
        >
            <DialogTitle id="attachment-preview-dialog-title">
                附件预览
            </DialogTitle>
            <DialogContent dividers>
                <Stack spacing={2}>
                    <Box>
                        <Typography sx={{ fontWeight: 600 }}>
                            {attachment.original_name}
                        </Typography>
                        <Typography variant="body2" color="text.secondary">
                            安全扫描已通过 · 按需加载预览
                        </Typography>
                    </Box>
                    {preview.status === 'loading' && (
                        <Stack
                            role="status"
                            aria-label="正在加载附件预览"
                            spacing={1}
                            sx={{ alignItems: 'center', py: 6 }}
                        >
                            <CircularProgress size={32} />
                            <Typography color="text.secondary">
                                正在安全加载预览…
                            </Typography>
                        </Stack>
                    )}
                    {preview.status === 'error' && (
                        <Alert severity="warning" role="alert">
                            {preview.error}
                        </Alert>
                    )}
                    {preview.status === 'ready' && decision.eligible && (
                        <PreviewContent
                            attachment={attachment}
                            kind={decision.kind}
                            objectUrl={preview.objectUrl}
                            text={preview.text}
                        />
                    )}
                </Stack>
            </DialogContent>
            <DialogActions>
                <Button onClick={onClose} autoFocus>
                    关闭
                </Button>
            </DialogActions>
        </Dialog>
    )
}
