import {
    apiFetch,
    localizedApiErrorMessage,
} from '@/lib/apiClient'
import {
    humanApiRoutes,
    type Ticket,
    type TicketAttachment,
    type TicketPage,
} from '@/lib/generated/human-api'
import type {
    CreateKnowledgeDraftInput,
    CreateKnowledgeDraftResult,
    CreateKnowledgeVersionDraftInput,
    KnowledgeArticlePage,
    KnowledgeArticleStatus,
    KnowledgeDocument,
    KnowledgeSearchResult,
    KnowledgeVersion,
    KnowledgeVersionPage,
} from './types'

export const listKnowledgeArticles = (
    projectKey: string,
    query: {
        page: number
        pageSize: number
        status?: KnowledgeArticleStatus
        keyword?: string
        view?: 'mine' | 'manage'
        signal?: AbortSignal
    },
) => apiFetch<KnowledgeArticlePage>(
        humanApiRoutes.listProjectKnowledgeArticles(
            { projectKey },
            {
                page: query.page,
                page_size: query.pageSize,
                sort_by: 'updated_at',
                sort_order: 'desc',
                status: query.status || undefined,
                q: query.keyword || undefined,
                view: query.view,
            },
        ),
        { signal: query.signal },
    )

export const listKnowledgeVersions = (
    projectKey: string,
    articleID: string,
    query: {
        page: number
        pageSize: number
        signal?: AbortSignal
    },
) => {
    return apiFetch<KnowledgeVersionPage>(
        humanApiRoutes.listProjectKnowledgeVersions(
            { projectKey, articleID },
            {
                page: query.page,
                page_size: query.pageSize,
                sort_by: 'version',
                sort_order: 'desc',
            },
        ),
        { signal: query.signal },
    )
}

export const createKnowledgeArticleDraft = (
    projectKey: string,
    input: CreateKnowledgeDraftInput,
) => apiFetch<CreateKnowledgeDraftResult>(
    humanApiRoutes.createProjectKnowledgeArticle({ projectKey }),
    {
        method: 'POST',
        body: JSON.stringify(input),
    },
)

export const createKnowledgeDraft = (
    projectKey: string,
    articleID: string,
    input: CreateKnowledgeVersionDraftInput,
) => apiFetch<CreateKnowledgeDraftResult>(
    humanApiRoutes.createProjectKnowledgeArticleDraft({
        projectKey,
        articleID,
    }),
    {
        method: 'POST',
        body: JSON.stringify(input),
    },
)

export type KnowledgeSourceAttachmentPage = {
    items: TicketAttachment[]
    total: number
    page: number
    pageSize: number
    totalPages: number
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const invalidAttachmentPage = () =>
    new Error('来源附件响应格式无效，请稍后重试')

const pageInteger = (
    value: unknown,
    minimum: number,
    maximum = Number.MAX_SAFE_INTEGER,
): number => {
    if (
        typeof value !== 'number'
        || !Number.isSafeInteger(value)
        || value < minimum
        || value > maximum
    ) {
        throw invalidAttachmentPage()
    }
    return value
}

const attachmentVirusScans = new Set([
    'pending',
    'clean',
    'infected',
    'error',
])
const attachmentFileTypes = new Set([
    'image',
    'document',
    'video',
    'audio',
    'archive',
    'other',
])

const isTicketAttachment = (
    value: unknown,
): value is TicketAttachment => {
    if (!isRecord(value)) return false
    return (
        typeof value.id === 'number'
        && Number.isSafeInteger(value.id)
        && value.id > 0
        && typeof value.ticket_id === 'number'
        && Number.isSafeInteger(value.ticket_id)
        && value.ticket_id > 0
        && typeof value.original_name === 'string'
        && value.original_name.length > 0
        && typeof value.file_size === 'number'
        && Number.isSafeInteger(value.file_size)
        && value.file_size >= 0
        && typeof value.mime_type === 'string'
        && typeof value.file_type === 'string'
        && attachmentFileTypes.has(value.file_type)
        && typeof value.extension === 'string'
        && typeof value.is_public === 'boolean'
        && typeof value.virus_scan === 'string'
        && attachmentVirusScans.has(value.virus_scan)
        && typeof value.created_at === 'string'
        && typeof value.updated_at === 'string'
    )
}

const pageMetadataValue = (
    metadata: Record<string, unknown>,
    envelope: Record<string, unknown>,
    field: string,
): unknown => {
    const nestedValue = metadata[field]
    const envelopeValue = envelope[field]
    if (
        metadata !== envelope
        && nestedValue !== undefined
        && envelopeValue !== undefined
        && nestedValue !== envelopeValue
    ) {
        throw invalidAttachmentPage()
    }
    return nestedValue ?? envelopeValue
}

export const listKnowledgeSourceAttachments = async (
    projectKey: string,
    ticketID: number,
    query: {
        page: number
        pageSize: number
        signal?: AbortSignal
    },
): Promise<KnowledgeSourceAttachmentPage> => {
    const response = await apiFetch<Response>(
        humanApiRoutes.listProjectTicketAttachments(
            { projectKey, ticketID },
            {
                page: query.page,
                page_size: query.pageSize,
            },
        ),
        {
            rawResponse: true,
            signal: query.signal,
        },
    )
    const payload: unknown = await response.json().catch(() => null)
    if (!response.ok) {
        throw new Error(localizedApiErrorMessage(
            payload,
            response.status,
            '来源附件加载失败，请稍后重试',
        ))
    }
    if (
        isRecord(payload)
        && (
            payload.success === false
            || (
                typeof payload.code === 'number'
                && payload.code !== 0
            )
        )
    ) {
        throw new Error(localizedApiErrorMessage(
            payload,
            response.status,
            '来源附件加载失败，请稍后重试',
        ))
    }

    if (
        !isRecord(payload)
        || !(
            payload.success === true
            || payload.code === 0
        )
    ) {
        throw invalidAttachmentPage()
    }
    const envelope = payload
    const data = envelope.data
    const pageEnvelope = isRecord(data) ? data : envelope
    const itemsValue = Array.isArray(data)
        ? data
        : pageEnvelope.items
    if (
        !Array.isArray(itemsValue)
        || !itemsValue.every(isTicketAttachment)
    ) {
        throw invalidAttachmentPage()
    }
    const items = itemsValue
    const total = pageInteger(
        pageMetadataValue(pageEnvelope, envelope, 'total'),
        0,
    )
    const page = pageInteger(
        pageMetadataValue(pageEnvelope, envelope, 'page'),
        1,
    )
    const pageSize = pageInteger(
        pageMetadataValue(pageEnvelope, envelope, 'page_size'),
        1,
        100,
    )
    const totalPages = pageInteger(
        pageMetadataValue(pageEnvelope, envelope, 'total_pages'),
        0,
    )
    const expectedTotalPages =
        total === 0 ? 0 : Math.ceil(total / pageSize)
    const uniqueIDs = new Set(items.map((item) => item.id))
    if (
        page !== query.page
        || pageSize !== query.pageSize
        || totalPages !== expectedTotalPages
        || items.length > pageSize
        || items.length > total
        || uniqueIDs.size !== items.length
        || (
            totalPages === 0
            && items.length !== 0
        )
        || (
            totalPages > 0
            && page > totalPages
            && items.length !== 0
        )
    ) {
        throw invalidAttachmentPage()
    }

    return {
        items,
        total,
        page,
        pageSize,
        totalPages,
    }
}

export const searchKnowledgeSourceTickets = (
    projectKey: string,
    search: string,
    signal?: AbortSignal,
) => apiFetch<TicketPage>(
    humanApiRoutes.listProjectTickets(
        { projectKey },
        {
            page: 1,
            page_size: 25,
            search,
            sort_by: 'updated_at',
            sort_order: 'desc',
        },
    ),
    { signal },
).then((page) => ({
    ...page,
    items: (page.items ?? []) as Ticket[],
}))

export const getKnowledgeDocument = (
    projectKey: string,
    articleID: string,
    options: {
        versionID?: string
        preferLatestDraft?: boolean
        signal?: AbortSignal
    } = {},
) => {
    return apiFetch<KnowledgeDocument>(
        humanApiRoutes.getProjectKnowledgeArticleDocument(
            { projectKey, articleID },
            {
                version_id: options.versionID,
                prefer_latest_draft: options.preferLatestDraft || undefined,
            },
        ),
        { signal: options.signal },
    )
}

export const publishKnowledgeVersion = (
    projectKey: string,
    versionID: string,
) => apiFetch<KnowledgeVersion>(
    humanApiRoutes.publishProjectKnowledgeVersion({
        projectKey,
        versionID,
    }),
    { method: 'POST' },
)

export const searchKnowledge = (
    projectKey: string,
    query: string,
    signal?: AbortSignal,
) => apiFetch<KnowledgeSearchResult>(
    humanApiRoutes.searchProjectKnowledge({ projectKey }),
    {
        method: 'POST',
        body: JSON.stringify({ query, limit: 20 }),
        signal,
    },
)
