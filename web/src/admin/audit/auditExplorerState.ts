import type {
    ListPlatformAuditLogsOperationQuery,
    PlatformRole,
} from '@/lib/generated/human-api'

export interface AuditExplorerFilters {
    urlError: string
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
const supportedParameters = new Set([
    'actor',
    'platform_role',
    'action',
    'method',
    'path_prefix',
    'status',
    'result',
    'keyword',
    'time_preset',
    'start_time',
    'end_time',
    'cursor',
    'limit',
])

export const auditFiltersFromSearchParams = (
    searchParams: URLSearchParams,
): AuditExplorerFilters => {
    const platformRole = searchParams.get('platform_role') ?? ''
    const method = (searchParams.get('method') ?? '').toUpperCase()
    const result = searchParams.get('result') ?? ''
    const timePreset = searchParams.get('time_preset') ?? ''
    const limitValue = searchParams.get('limit')
    const rawLimit = Number(limitValue ?? 25)
    const status = searchParams.get('status') ?? ''
    const invalidFields: string[] = []
    for (const key of new Set(searchParams.keys())) {
        if (!supportedParameters.has(key)) {
            invalidFields.push(`未知参数 ${key}`)
        }
        if (searchParams.getAll(key).length !== 1) {
            invalidFields.push(`重复参数 ${key}`)
        }
    }
    if (
        timePreset &&
        (searchParams.has('start_time') || searchParams.has('end_time'))
    ) {
        invalidFields.push('时间预设与自定义时间范围不能同时使用')
    }
    if (platformRole && !platformRoles.has(platformRole)) {
        invalidFields.push('平台角色')
    }
    if (method && !methods.has(method)) {
        invalidFields.push('请求方法')
    }
    if (result && !results.has(result)) {
        invalidFields.push('操作结果')
    }
    if (timePreset && !presets.has(timePreset)) {
        invalidFields.push('时间范围')
    }
    if (
        limitValue !== null &&
        (!Number.isInteger(rawLimit) || rawLimit < 1 || rawLimit > 100)
    ) {
        invalidFields.push('每页数量')
    }
    if (
        status &&
        (!/^\d{3}$/u.test(status) ||
            Number(status) < 100 ||
            Number(status) > 599)
    ) {
        invalidFields.push('HTTP 状态')
    }
    return {
        urlError:
            invalidFields.length === 0
                ? ''
                : `URL 中的${invalidFields.join('、')}参数无效，请清除或修正筛选条件`,
        actor: searchParams.get('actor') ?? '',
        platformRole: platformRoles.has(platformRole)
            ? (platformRole as PlatformRole)
            : '',
        action: searchParams.get('action') ?? '',
        method: methods.has(method)
            ? (method as AuditExplorerFilters['method'])
            : '',
        pathPrefix: searchParams.get('path_prefix') ?? '',
        status,
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
    if (filters.urlError) {
        throw new Error(filters.urlError)
    }
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
