import type { TicketSource, UpdateTicketRequest } from '@/types'
import type { ProjectRole } from '@/lib/projectScope'
import {
    normalizeCustomFieldsForSubmit,
    normalizeTagsForSubmit,
} from './tagUtils'
import { normalizeTicketDateTimeForSubmit } from './ticketDateTime'

type TicketEditFormValues = Omit<UpdateTicketRequest, 'source'> & {
    source?: TicketSource
    tags?: unknown
    custom_fields?: unknown
    [key: string]: unknown
}

export const transformTicketUpdate = (
    data: TicketEditFormValues,
    projectRole: ProjectRole | null,
): Record<string, unknown> => {
    const payload: Record<string, unknown> = {}
    const requester = projectRole === 'requester'
    for (const field of [
        'title',
        'description',
        'type',
        'priority',
        'source',
        'assigned_to_id',
        'category_id',
        'subcategory_id',
        'due_date',
        'customer_name',
        'customer_email',
        'customer_phone',
        'internal_notes',
        'rating',
        'rating_comment',
    ] as const) {
        if (
            requester &&
            (
                field === 'assigned_to_id' ||
                field === 'internal_notes'
            )
        ) {
            continue
        }
        if (field === 'source' && data[field] === 'agent') {
            continue
        }
        if (typeof data[field] !== 'undefined') {
            payload[field] = field === 'due_date'
                ? normalizeTicketDateTimeForSubmit(data[field])
                : data[field]
        }
    }

    const normalizedTags = normalizeTagsForSubmit(data.tags)
    if (typeof normalizedTags !== 'undefined') {
        payload.tags = normalizedTags
    }

    const normalizedCustomFields =
        normalizeCustomFieldsForSubmit(data.custom_fields)
    if (typeof normalizedCustomFields !== 'undefined') {
        payload.custom_fields = normalizedCustomFields
    }

    return payload
}
