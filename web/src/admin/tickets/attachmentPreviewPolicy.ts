import type { TicketAttachment } from '@/lib/generated/human-api'

export const ATTACHMENT_PREVIEW_LIMITS = {
    text: 1 * 1024 * 1024,
    image: 15 * 1024 * 1024,
    media: 25 * 1024 * 1024,
    pdf: 25 * 1024 * 1024,
} as const

export type AttachmentPreviewKind =
    | 'image'
    | 'audio'
    | 'video'
    | 'pdf'
    | 'text'
    | 'markdown'

export type AttachmentPreviewDecision =
    | {
        eligible: true
        kind: AttachmentPreviewKind
        maxBytes: number
        metadataMimeType: string
        reason: ''
    }
    | {
        eligible: false
        kind: null
        maxBytes: 0
        metadataMimeType: string
        reason: string
    }

const IMAGE_MIME_TYPES = new Set([
    'image/avif',
    'image/bmp',
    'image/gif',
    'image/jpeg',
    'image/png',
    'image/webp',
])

const AUDIO_MIME_TYPES = new Set([
    'audio/aac',
    'audio/flac',
    'audio/mp4',
    'audio/mpeg',
    'audio/ogg',
    'audio/wav',
    'audio/webm',
    'audio/x-flac',
    'audio/x-wav',
])

const VIDEO_MIME_TYPES = new Set([
    'video/mp4',
    'video/ogg',
    'video/quicktime',
    'video/webm',
    'video/x-m4v',
])

const TEXT_MIME_TYPES = new Set([
    'application/json',
    'application/ld+json',
    'application/toml',
    'application/xml',
    'application/x-yaml',
    'application/yaml',
    'text/csv',
    'text/plain',
    'text/tab-separated-values',
    'text/xml',
    'text/yaml',
])

const MARKDOWN_MIME_TYPES = new Set([
    'text/markdown',
    'text/x-markdown',
])

const MARKDOWN_EXTENSIONS = new Set([
    'markdown',
    'md',
    'mdown',
    'mkd',
])

export const normalizeAttachmentMimeType = (value: string | null | undefined) =>
    (value ?? '').split(';', 1)[0].trim().toLowerCase()

const normalizedExtension = (attachment: TicketAttachment) => {
    const declared = typeof attachment.extension === 'string'
        ? attachment.extension.trim().toLowerCase().replace(/^\./u, '')
        : ''
    if (declared) return declared
    const originalName = typeof attachment.original_name === 'string'
        ? attachment.original_name
        : ''
    const match = originalName.toLowerCase().match(/\.([a-z0-9]+)$/u)
    return match?.[1] ?? ''
}

const boundedDecision = (
    attachment: TicketAttachment,
    kind: AttachmentPreviewKind,
    maxBytes: number,
    metadataMimeType: string,
): AttachmentPreviewDecision => {
    if (
        !Number.isSafeInteger(attachment.file_size) ||
        attachment.file_size < 0 ||
        attachment.file_size > maxBytes
    ) {
        return {
            eligible: false,
            kind: null,
            maxBytes: 0,
            metadataMimeType,
            reason: '文件超过安全预览上限，仅支持下载',
        }
    }
    return {
        eligible: true,
        kind,
        maxBytes,
        metadataMimeType,
        reason: '',
    }
}

export const getAttachmentPreviewDecision = (
    attachment: TicketAttachment,
): AttachmentPreviewDecision => {
    const metadataMimeType = normalizeAttachmentMimeType(attachment.mime_type)
    if (attachment.virus_scan !== 'clean') {
        const reason = attachment.virus_scan === 'pending'
            ? '安全扫描通过后可预览'
            : '附件未通过安全扫描，禁止预览'
        return {
            eligible: false,
            kind: null,
            maxBytes: 0,
            metadataMimeType,
            reason,
        }
    }

    if (
        MARKDOWN_MIME_TYPES.has(metadataMimeType) ||
        (
            metadataMimeType === 'text/plain' &&
            MARKDOWN_EXTENSIONS.has(normalizedExtension(attachment))
        )
    ) {
        return boundedDecision(
            attachment,
            'markdown',
            ATTACHMENT_PREVIEW_LIMITS.text,
            metadataMimeType,
        )
    }
    if (IMAGE_MIME_TYPES.has(metadataMimeType)) {
        return boundedDecision(
            attachment,
            'image',
            ATTACHMENT_PREVIEW_LIMITS.image,
            metadataMimeType,
        )
    }
    if (AUDIO_MIME_TYPES.has(metadataMimeType)) {
        return boundedDecision(
            attachment,
            'audio',
            ATTACHMENT_PREVIEW_LIMITS.media,
            metadataMimeType,
        )
    }
    if (VIDEO_MIME_TYPES.has(metadataMimeType)) {
        return boundedDecision(
            attachment,
            'video',
            ATTACHMENT_PREVIEW_LIMITS.media,
            metadataMimeType,
        )
    }
    if (metadataMimeType === 'application/pdf') {
        return boundedDecision(
            attachment,
            'pdf',
            ATTACHMENT_PREVIEW_LIMITS.pdf,
            metadataMimeType,
        )
    }
    if (TEXT_MIME_TYPES.has(metadataMimeType)) {
        return boundedDecision(
            attachment,
            'text',
            ATTACHMENT_PREVIEW_LIMITS.text,
            metadataMimeType,
        )
    }

    return {
        eligible: false,
        kind: null,
        maxBytes: 0,
        metadataMimeType,
        reason: '此文件格式暂不支持安全预览，仅支持下载',
    }
}

export const isPreviewResponseMimeCompatible = (
    decision: AttachmentPreviewDecision,
    responseContentType: string | null,
) => {
    if (!decision.eligible) return false
    const responseMimeType = normalizeAttachmentMimeType(responseContentType)
    if (!responseMimeType) return false
    if (decision.kind === 'markdown') {
        return (
            responseMimeType === 'text/plain' ||
            MARKDOWN_MIME_TYPES.has(responseMimeType)
        )
    }
    return responseMimeType === decision.metadataMimeType
}
