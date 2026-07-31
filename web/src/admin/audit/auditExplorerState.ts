import type {
    ListPlatformAuditLogsOperationQuery,
    PlatformRole,
} from '@/lib/generated/human-api'

export interface AuditExplorerFilters {
    actor: string
    platformRole: PlatformRole | ''
    action: string
    method: NonNullable<ListPlatformAuditLogsOperationQuery['method']> | ''
    pathPrefix: string
    status: string
    result: NonNullable<ListPlatformAuditLogsOperationQuery['result']> | ''
    keyword: string
    timePreset:
        | NonNullable<ListPlatformAuditLogsOperationQuery['time_preset']>
        | ''
    startTime: string
    endTime: string
    cursor: string
    limit: number
}

const platformRoles = new Set([
    'platform_admin',
    'security_auditor',
    'emergency_operator',
    'member',
])
const methods = new Set([
    'GET',
    'HEAD',
    'POST',
    'PUT',
    'PATCH',
    'DELETE',
    'OPTIONS',
])
const results = new Set(['pending', 'success', 'error'])
const presets = new Set(['1h', '24h', '7d', '30d'])

export const auditFiltersFromSearchParams = (
    searchParams: URLSearchParams,
): AuditExplorerFilters => {
    const platformRole = searchParams.get('platform_role') ?? ''
    const method = (searchParams.get('method') ?? '').toUpperCase()
    const result = searchParams.get('result') ?? ''
    const timePreset = searchParams.get('time_preset') ?? ''
    const rawLimit = Number(searchParams.get('limit') ?? 25)
    return {
        actor: searchParams.get('actor') ?? '',
        platformRole: platformRoles.has(platformRole)
            ? (platformRole as PlatformRole)
            : '',
        action: searchParams.get('action') ?? '',
        method: methods.has(method)
            ? (method as AuditExplorerFilters['method'])
            : '',
        pathPrefix: searchParams.get('path_prefix') ?? '',
        status: searchParams.get('status') ?? '',
        result: results.has(result)
            ? (result as AuditExplorerFilters['result'])
            : '',
        keyword: searchParams.get('keyword') ?? '',
        timePreset: presets.has(timePreset)
            ? (timePreset as AuditExplorerFilters['timePreset'])
            : '',
        startTime: searchParams.get('start_time') ?? '',
        endTime: searchParams.get('end_time') ?? '',
        cursor: searchParams.get('cursor') ?? '',
        limit:
            Number.isInteger(rawLimit) && rawLimit >= 1 && rawLimit <= 100
                ? rawLimit
                : 25,
    }
}

export const auditFiltersToSearchParams = (
    filters: AuditExplorerFilters,
): URLSearchParams => {
    const params = new URLSearchParams()
    const values: Array<[string, string]> = [
        ['actor', filters.actor.trim()],
        ['platform_role', filters.platformRole],
        ['action', filters.action.trim()],
        ['method', filters.method],
        ['path_prefix', filters.pathPrefix.trim()],
        ['status', filters.status.trim()],
        ['result', filters.result],
        ['keyword', filters.keyword.trim()],
        ['time_preset', filters.timePreset],
        ['start_time', filters.timePreset ? '' : filters.startTime],
        ['end_time', filters.timePreset ? '' : filters.endTime],
        ['cursor', filters.cursor],
    ]
    for (const [key, value] of values) {
        if (value) params.set(key, value)
    }
    if (filters.limit !== 25) {
        params.set('limit', String(filters.limit))
    }
    return params
}

export const auditFiltersToQuery = (
    filters: AuditExplorerFilters,
): ListPlatformAuditLogsOperationQuery => {
    const query: ListPlatformAuditLogsOperationQuery = {
        limit: filters.limit,
    }
    if (filters.actor.trim()) query.actor = filters.actor.trim()
    if (filters.platformRole) query.platform_role = filters.platformRole
    if (filters.action.trim()) query.action = filters.action.trim()
    if (filters.method) query.method = filters.method
    if (filters.pathPrefix.trim()) {
        query.path_prefix = filters.pathPrefix.trim()
    }
    if (filters.status.trim()) query.status = Number(filters.status)
    if (filters.result) query.result = filters.result
    if (filters.keyword.trim()) query.keyword = filters.keyword.trim()
    if (filters.timePreset) {
        query.time_preset = filters.timePreset
    } else {
        if (filters.startTime) {
            query.start_time = normalizedAuditTime(filters.startTime)
        }
        if (filters.endTime) {
            query.end_time = normalizedAuditTime(filters.endTime)
        }
    }
    if (filters.cursor) query.cursor = filters.cursor
    return query
}

const normalizedAuditTime = (value: string): string => {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : parsed.toISOString()
}
