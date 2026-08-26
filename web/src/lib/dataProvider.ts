import { DataProvider, fetchUtils, HttpError } from 'react-admin'
import queryString from 'query-string'
import {
    containsChineseText,
    localizedApiErrorMessage,
    requireCommittedHumanBearerHeaders,
} from './apiClient'
import {
    assertProjectScopeSnapshot,
    captureProjectScopeSnapshot,
    projectResourcePath,
    resolveActiveProjectAccess,
    type ProjectScopeSnapshot,
} from './projectScope'
import { humanApiRoutes } from './generated/human-api'
import { joinApiUrl } from './apiUrl'
import {
    projectAccessInvalidatedEvent,
    projectScopeChangedEvent,
    sessionInvalidatedEvent,
    shouldInvalidateActiveProjectAccess,
    signalProjectAccessInvalidated,
    signalSessionInvalidated,
} from './projectScopeEvents'

const apiUrl = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

const projectScopedResources = new Set([
    'tickets',
    'comments',
    'ticket_history',
    'categories',
    'assignees',
    'notifications',
    'automation-rules',
    'automation-logs',
])

const scopedApiPath = async (
    resource: string,
    apiPath: string,
    snapshot?: ProjectScopeSnapshot,
): Promise<string> => {
    if (!projectScopedResources.has(resource)) return apiPath
    const scope = snapshot ?? captureProjectScopeSnapshot()
    const access = await resolveActiveProjectAccess()
    assertProjectScopeSnapshot(scope)
    if (access.project.key !== scope.project_key) {
        throw new HttpError(
            '项目范围已变化，请刷新页面后重试',
            409,
            { code: 'project_scope_changed' },
        )
    }
    const projectKey = scope.project_key
    switch (resource) {
        case 'tickets':
            return humanApiRoutes.listProjectTickets({ projectKey })
        case 'notifications':
            return humanApiRoutes.listProjectNotifications({ projectKey })
        case 'automation-rules':
            return humanApiRoutes.listProjectAutomationRules({ projectKey })
        case 'automation-logs':
            return humanApiRoutes.listProjectAutomationLogs({ projectKey })
        default:
            return projectResourcePath(apiPath, scope)
    }
}

const captureResourceScope = (
    resource: string,
): ProjectScopeSnapshot | undefined =>
    projectScopedResources.has(resource)
        ? captureProjectScopeSnapshot()
        : undefined

/**
 * 自定义HTTP客户端，处理JWT认证和请求格式化
 */
type HttpClientOptions = RequestInit & { headers?: Headers }

const httpClient = async (
    url: string,
    options: HttpClientOptions = {},
    snapshot?: ProjectScopeSnapshot,
) => {
    if (snapshot) assertProjectScopeSnapshot(snapshot)
    const token = localStorage.getItem('token')
    const headers = new Headers(options.headers ?? { Accept: 'application/json' })

    if (!(options.body instanceof FormData)) {
        headers.set('Content-Type', 'application/json')
    }

    if (token) {
        headers.set('Authorization', `Bearer ${token}`)
    }

    try {
        requireCommittedHumanBearerHeaders(headers)
        return await fetchUtils.fetchJson(url, { ...options, headers })
    } catch (error: unknown) {
        return handleHttpError(error, url)
    }
}

/**
 * 将React Admin的排序参数转换为Go API格式
 */
const convertSortToGoFormat = (sort: { field: string; order: string }) => {
    return JSON.stringify([sort.field, sort.order.toUpperCase()]);
};

/**
 * 将React Admin的分页参数转换为Go API格式
 */

/**
 * 将React Admin的过滤参数转换为Go API格式
 */
const convertFilterToGoFormat = (filter: Record<string, unknown>) => {
    return JSON.stringify(filter)
}

/**
 * 解析Go API的响应头以获取总数
 */
const getTotalFromHeaders = (headers: Headers, defaultTotal: number = 0): number => {
    const contentRange = headers.get('content-range') || headers.get('x-total-count');
    if (contentRange) {
        if (contentRange.includes('/')) {
            // 格式: "tickets 0-9/50"
            return parseInt(contentRange.split('/')[1], 10);
        } else {
            // 格式: "50" (直接总数)
            return parseInt(contentRange, 10);
        }
    }
    return defaultTotal;
};

/**
 * ChronoDesk 数据 Provider
 * 完美适配Go Gin后端API
 */
const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

function handleHttpError(error: unknown, url?: string): never {
    if (error instanceof HttpError) {
        if (error.status === 401) {
            signalSessionInvalidated()
        }
        if (
            error.status === 403 &&
            typeof url === 'string' &&
            shouldInvalidateActiveProjectAccess(url, error.body)
        ) {
            signalProjectAccessInvalidated()
        }
        const message = localizedApiErrorMessage(error.body, error.status)
        throw new HttpError(message, error.status, error.body)
    }

    const rawMessage = error instanceof Error ? error.message : ''
    const message = rawMessage && containsChineseText(rawMessage)
        ? rawMessage
        : '请求失败，请检查网络连接后重试'
    throw new HttpError(message, 500, {})
}

const extractResponseData = (json: unknown): unknown => {
    if (isRecord(json)) {
        if (json.code === 0 && 'data' in json) {
            return json.data
        }
        if ('data' in json) {
            return json.data
        }
    }
    return json
}

const extractTypedResponseData = <T>(json: unknown): T => extractResponseData(json) as T

const ticketVersionFromUpdate = (
    previousData: unknown,
    data: unknown,
): number => {
    const candidates = [previousData, data]
    for (const candidate of candidates) {
        if (!isRecord(candidate)) continue
        const version = candidate.version
        if (
            typeof version === 'number' &&
            Number.isSafeInteger(version) &&
            version > 0
        ) {
            return version
        }
    }
    throw new HttpError(
        '工单版本信息缺失，请刷新页面后重试',
        428,
        { code: 'precondition_required' },
    )
}

const ticketIfMatchHeaders = (version: number): Headers => {
    const headers = new Headers()
    headers.set('If-Match', `"v${version}"`)
    return headers
}

const ticketVersionCache = new Map<string, number>()
const recordScopeCache = new Map<string, ProjectScopeSnapshot>()

const recordScopeCacheKey = (
    resource: string,
    id: string | number,
    projectKey: string,
): string => `${projectKey}\u0000${resource}\u0000${String(id)}`

const rememberRecordScopes = (
    resource: string,
    records: unknown[],
    snapshot?: ProjectScopeSnapshot,
): void => {
    if (!snapshot) return
    for (const record of records) {
        if (!isRecord(record)) continue
        const id = record.id
        if (typeof id !== 'number' && typeof id !== 'string') continue
        recordScopeCache.set(
            recordScopeCacheKey(resource, id, snapshot.project_key),
            snapshot,
        )
    }
}

const requireRecordScope = (
    resource: string,
    id: string | number,
): ProjectScopeSnapshot => {
    const current = captureProjectScopeSnapshot()
    const snapshot = recordScopeCache.get(
        recordScopeCacheKey(resource, id, current.project_key),
    )
    if (!snapshot) {
        throw new HttpError(
            '记录所属项目已变化，请刷新页面后重试',
            409,
            { code: 'project_scope_changed', resource_id: id },
        )
    }
    assertProjectScopeSnapshot(snapshot)
    return snapshot
}

const ticketVersionCacheKey = (
    id: string | number,
    snapshot: ProjectScopeSnapshot,
): string => `${snapshot.project_key}\u0000${String(id)}`

if (typeof window !== 'undefined') {
    const clearTicketVersionCache = () => {
        ticketVersionCache.clear()
        recordScopeCache.clear()
    }
    window.addEventListener(
        projectAccessInvalidatedEvent,
        clearTicketVersionCache,
    )
    window.addEventListener(projectScopeChangedEvent, clearTicketVersionCache)
    window.addEventListener(sessionInvalidatedEvent, clearTicketVersionCache)
}

const rememberTicketVersions = (
    records: unknown[],
    snapshot: ProjectScopeSnapshot,
): void => {
    for (const record of records) {
        if (!isRecord(record)) continue
        const { id, version } = record
        if (
            (typeof id === 'number' || typeof id === 'string') &&
            typeof version === 'number' &&
            Number.isSafeInteger(version) &&
            version > 0
        ) {
            ticketVersionCache.set(ticketVersionCacheKey(id, snapshot), version)
        }
    }
}

const ticketPreconditions = (
    ids: readonly (string | number)[],
    snapshot: ProjectScopeSnapshot,
): Array<{ id: string | number; version: number }> =>
    ids.map((id) => {
        const version = ticketVersionCache.get(
            ticketVersionCacheKey(id, snapshot),
        )
        if (version === undefined) {
            throw new HttpError(
                `工单 ${id} 的版本信息缺失，请刷新列表后重试`,
                428,
                { code: 'precondition_required', resource_id: id },
            )
        }
        return { id, version }
    })

const parseListResponse = (resource: string, json: unknown, headers: Headers) => {
    const payload = extractResponseData(json)

    if (isRecord(payload) && Array.isArray(payload.items)) {
        const data = payload.items
        const total =
            (payload.total as number | undefined) ||
            (payload.count as number | undefined) ||
            data.length
        return { data, total }
    }

    if (Array.isArray(payload)) {
        const data = payload
        const total =
            (isRecord(json) ? (json.total as number | undefined) || (json.count as number | undefined) : undefined) ??
            getTotalFromHeaders(headers, data.length)
        return { data, total }
    }

    if (isRecord(payload)) {
        return { data: [payload], total: 1 }
    }

    console.warn(`Unexpected response format for resource ${resource}:`, json)
    return { data: [], total: 0 }
}

export const dataProvider: DataProvider = {
    // 获取资源列表
    getList: async (resource, params) => {
        const scope = captureResourceScope(resource)
        const { page, perPage } = params.pagination || { page: 1, perPage: 10 };
        const { field, order } = params.sort || { field: 'id', order: 'ASC' };

        // 构建查询参数 - 适配Go API格式
        const query: Record<string, unknown> = {
            page,
            page_size: perPage,
        };

        // 复制过滤器，后续会删除已映射的字段
        const rawFilter = (params.filter ?? {}) as Record<string, unknown>;
        const filter: Record<string, unknown> = { ...rawFilter };

        const normalizeBoolean = (value: unknown): undefined | boolean => {
            if (typeof value === 'boolean') return value;
            if (typeof value === 'string' && value.trim() !== '') {
                if (value === 'true') return true;
                if (value === 'false') return false;
            }
            return undefined;
        };

        const extractSearchValue = () => {
            const value = (filter.q as string | undefined) ?? (filter.search as string | undefined);
            if (typeof value === 'string' && value.trim() !== '') {
                delete filter.q;
                delete filter.search;
                return value.trim();
            }
            return undefined;
        };

        // 添加排序参数
        if (field && order) {
            const normalizedOrder = order.toLowerCase() === 'asc' ? 'asc' : 'desc';

            if (resource === 'automation-logs' || resource === 'automation-rules' || resource === 'notifications') {
                query.sort = convertSortToGoFormat({ field, order });
            } else if (resource === 'tickets') {
                query.sort_by = field;
                query.sort_order = normalizedOrder;
            } else if (resource === 'users') {
                query.order_by = field;
                query.order = normalizedOrder;
            } else {
                query.sort = convertSortToGoFormat({ field, order });
            }
        }

        // 添加过滤参数
        if (resource === 'automation-logs') {
            const { filter = {} as Record<string, unknown> } = params
            if (filter.rule_id) {
                query.rule_id = filter.rule_id
            }
            if (filter.ticket_id) {
                query.ticket_id = filter.ticket_id
            }
            if (typeof filter.success !== 'undefined' && filter.success !== null && filter.success !== '') {
                if (typeof filter.success === 'string') {
                    query.success = filter.success === 'true'
                } else {
                    query.success = filter.success
                }
            }
        } else if (resource === 'automation-rules') {
            const { filter = {} as Record<string, unknown> } = params
            if (filter.rule_type) {
                query.rule_type = filter.rule_type
            }
            if (filter.trigger_event) {
                query.trigger_event = filter.trigger_event
            }
            if (typeof filter.is_active !== 'undefined' && filter.is_active !== null && filter.is_active !== '') {
                if (typeof filter.is_active === 'string') {
                    query.is_active = filter.is_active === 'true'
                } else {
                    query.is_active = filter.is_active
                }
            }
            const searchValue = filter.q ?? filter.search
            if (searchValue) {
                query.search = searchValue
            }
        } else {
            if (resource === 'tickets') {
                const searchValue = extractSearchValue();
                if (searchValue) {
                    query.search = searchValue;
                }

                if (filter.status) {
                    query.status = filter.status;
                    delete filter.status;
                }
                if (filter.priority) {
                    query.priority = filter.priority;
                    delete filter.priority;
                }
                if (filter.type) {
                    query.type = filter.type;
                    delete filter.type;
                }
                if (filter.source) {
                    query.source = filter.source;
                    delete filter.source;
                }
                if (filter.assigned_to_id) {
                    query.assigned_to = filter.assigned_to_id;
                    delete filter.assigned_to_id;
                }
                if (filter.created_by_id) {
                    query.created_by = filter.created_by_id;
                    delete filter.created_by_id;
                }
                const slaBreached = normalizeBoolean(filter.sla_breached);
                if (typeof slaBreached !== 'undefined') {
                    query.sla_breached = slaBreached;
                    delete filter.sla_breached;
                }
                const isOverdue = normalizeBoolean(filter.is_overdue);
                if (typeof isOverdue !== 'undefined') {
                    query.is_overdue = isOverdue;
                    delete filter.is_overdue;
                }
                const unassigned = normalizeBoolean(filter.unassigned);
                if (typeof unassigned !== 'undefined') {
                    query.unassigned = unassigned;
                    delete filter.unassigned;
                }
                const assignedToMe = normalizeBoolean(filter.assigned_to_me);
                if (typeof assignedToMe !== 'undefined') {
                    query.assigned_to_me = assignedToMe;
                    delete filter.assigned_to_me;
                }
            } else if (resource === 'users') {
                const searchValue = extractSearchValue();
                if (searchValue) {
                    query.search = searchValue;
                }
                if (filter.platform_role) {
                    query.platform_role = filter.platform_role;
                    delete filter.platform_role;
                }
                if (filter.status) {
                    query.status = filter.status;
                    delete filter.status;
                }
            } else if (resource === 'notifications') {
                const searchValue = extractSearchValue();
                if (searchValue) {
                    filter.q = searchValue;
                }
                // 通知列表的服务端语义固定为“当前登录用户的通知中心”。
                // 丢弃旧书签或持久化列表状态中的跨用户筛选，避免向
                // 当前用户接口发送看似可用、实际会被拒绝的身份条件。
                delete filter.recipient_id;
                delete filter.sender_id;
            }

            const cleanedFilter = Object.fromEntries(
                Object.entries(filter).filter(([, value]) => value !== undefined && value !== null && value !== '')
            );

            if (Object.keys(cleanedFilter).length > 0) {
                query.filter = convertFilterToGoFormat(cleanedFilter);
            }
        }

        // 特殊处理不同资源的API路径
        let apiPath = resource;
        if (resource === 'users') {
            apiPath = humanApiRoutes.listPlatformUsers();
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const url = `${joinApiUrl(apiUrl, apiPath)}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url, {}, scope);

        const result = parseListResponse(resource, json, headers);
        rememberRecordScopes(resource, result.data, scope)
        if (resource === 'tickets' && scope) {
            rememberTicketVersions(result.data, scope);
        }
        return result;
    },

    // 获取单个资源
    getOne: async (resource, params) => {
        const scope = captureResourceScope(resource)
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const urlPath = resource === 'users'
            ? humanApiRoutes.getPlatformUser({ userID: Number(params.id) })
            : resource === 'tickets'
            ? humanApiRoutes.getProjectTicket(
                { projectKey: scope?.project_key ?? '', ticketID: Number(params.id) },
            )
            : resource === 'automation-rules'
                ? humanApiRoutes.getProjectAutomationRule(
                    { projectKey: scope?.project_key ?? '', ruleID: Number(params.id) },
                )
                : `${apiPath}/${params.id}`
        const url = joinApiUrl(apiUrl, urlPath);
        const { json } = await httpClient(url, {}, scope);
        const data = extractResponseData(json);
        rememberRecordScopes(resource, [data], scope)
        if (resource === 'tickets' && scope) {
            rememberTicketVersions([data], scope);
        }
        return { data: extractTypedResponseData(json) };
    },

    // 获取多个资源
    getMany: async (resource, params) => {
        const scope = captureResourceScope(resource)
        if (resource === 'users') {
            const records = await Promise.all(
                params.ids.map(async (id) => {
                    const { json } = await httpClient(
                        joinApiUrl(
                            apiUrl,
                            humanApiRoutes.getPlatformUser({
                                userID: Number(id),
                            }),
                        ),
                    )
                    return extractTypedResponseData(json)
                }),
            )
            return { data: records }
        }

        // 如果后端支持批量查询
        const query = {
            filter: convertFilterToGoFormat({ ids: params.ids }),
        };

        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const url = `${joinApiUrl(apiUrl, apiPath)}?${queryString.stringify(query)}`;
        const { json } = await httpClient(url, {}, scope);
        
        const payload = extractResponseData(json);
        if (isRecord(payload) && Array.isArray(payload.items)) {
            rememberRecordScopes(resource, payload.items, scope)
            if (resource === 'tickets' && scope) {
                rememberTicketVersions(payload.items, scope)
            }
            return { data: payload.items };
        }
        if (Array.isArray(payload)) {
            rememberRecordScopes(resource, payload, scope)
            if (resource === 'tickets' && scope) {
                rememberTicketVersions(payload, scope)
            }
            return { data: payload };
        }
        if (isRecord(payload)) {
            rememberRecordScopes(resource, [payload], scope)
            if (resource === 'tickets' && scope) {
                rememberTicketVersions([payload], scope)
            }
            return { data: [payload] };
        }
        return { data: [] };
    },

    // 获取引用资源
    getManyReference: async (resource, params) => {
        const scope = captureResourceScope(resource)
        const { page, perPage } = params.pagination || { page: 1, perPage: 10 };

        if (params.target === 'ticket_id' && resource === 'ticket_history') {
            const route = humanApiRoutes.listProjectTicketHistory(
                {
                    projectKey: scope?.project_key ?? '',
                    ticketID: Number(params.id),
                },
                {
                    page,
                    page_size: perPage,
                },
            )
            const { json, headers } = await httpClient(
                joinApiUrl(apiUrl, route),
                {},
                scope,
            )
            const result = parseListResponse(resource, json, headers)
            rememberRecordScopes(resource, result.data, scope)
            return result
        }

        const { field, order } = params.sort || { field: 'id', order: 'ASC' };
        const query: Record<string, unknown> = {
            page,
            page_size: perPage,
        };

        if (field && order) {
            query.sort = convertSortToGoFormat({ field, order });
        }

        // 添加引用过滤
        const filter = {
            ...params.filter,
            [params.target]: params.id,
        };
        query.filter = convertFilterToGoFormat(filter);

        if (params.target === 'ticket_id' && resource === 'comments') {
            const pathParameters = {
                projectKey: scope?.project_key ?? '',
                ticketID: Number(params.id),
            }
            const route = humanApiRoutes.listProjectTicketComments(pathParameters)
            const url = `${joinApiUrl(apiUrl, route)}?${queryString.stringify(query)}`
            const { json, headers } = await httpClient(url, {}, scope)
            const result = parseListResponse(resource, json, headers)
            rememberRecordScopes(resource, result.data, scope)
            return result
        }

        let apiPath = resource;
        if (resource === 'users') {
            apiPath = humanApiRoutes.listPlatformUsers();
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const url = `${joinApiUrl(apiUrl, apiPath)}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url, {}, scope);

        const result = parseListResponse(resource, json, headers)
        rememberRecordScopes(resource, result.data, scope)
        return result;
    },

    // 创建资源
    create: async (resource, params) => {
        const scope = captureResourceScope(resource)
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const urlPath = resource === 'users'
            ? humanApiRoutes.createPlatformUser()
            : resource === 'tickets'
            ? humanApiRoutes.createProjectTicket({
                projectKey: scope?.project_key ?? '',
            })
            : resource === 'notifications'
                ? humanApiRoutes.createProjectNotification({
                    projectKey: scope?.project_key ?? '',
                })
                : resource === 'automation-rules'
                    ? humanApiRoutes.createProjectAutomationRule({
                        projectKey: scope?.project_key ?? '',
                    })
                    : apiPath
        const url = joinApiUrl(apiUrl, urlPath);
        
        try {
            const { json } = await httpClient(url, {
                method: 'POST',
                body: JSON.stringify(params.data),
            }, scope);
            const data = extractResponseData(json);
            rememberRecordScopes(resource, [data], scope)
            if (resource === 'tickets' && scope) {
                rememberTicketVersions([data], scope);
            }
            return { data: extractTypedResponseData(json) };
        } catch (error: unknown) {
            return handleHttpError(error)
        }
    },

    // 更新资源
    update: async (resource, params) => {
        const scope = projectScopedResources.has(resource)
            ? requireRecordScope(resource, params.id)
            : undefined
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const urlPath = resource === 'users'
            ? humanApiRoutes.updatePlatformUser({
                userID: Number(params.id),
            })
            : resource === 'tickets'
            ? humanApiRoutes.updateProjectTicket(
                { projectKey: scope?.project_key ?? '', ticketID: Number(params.id) },
            )
            : resource === 'automation-rules'
                ? humanApiRoutes.updateProjectAutomationRule(
                    { projectKey: scope?.project_key ?? '', ruleID: Number(params.id) },
                )
                : `${apiPath}/${params.id}`
        const url = joinApiUrl(apiUrl, urlPath);
        
        try {
            const headers = resource === 'tickets'
                ? ticketIfMatchHeaders(
                    ticketVersionFromUpdate(params.previousData, params.data),
                )
                : undefined
            const { json } = await httpClient(url, {
                method: 'PUT',
                body: JSON.stringify(params.data),
                headers,
            }, scope);
            const data = extractResponseData(json);
            rememberRecordScopes(resource, [data], scope)
            if (resource === 'tickets' && scope) {
                rememberTicketVersions([data], scope);
            }
            return { data: extractTypedResponseData(json) };
        } catch (error: unknown) {
            return handleHttpError(error)
        }
    },

    // 批量更新
    updateMany: async (resource, params) => {
        const scope =
            projectScopedResources.has(resource) && params.ids.length > 0
                ? requireRecordScope(resource, params.ids[0])
                : captureResourceScope(resource)
        if (scope) {
            for (const id of params.ids.slice(1)) {
                const candidate = requireRecordScope(resource, id)
                if (
                    candidate.project_key !== scope.project_key ||
                    candidate.epoch !== scope.epoch ||
                    candidate.session_id !== scope.session_id
                ) {
                    throw new HttpError(
                        '批量记录不属于同一项目范围，请刷新列表后重试',
                        409,
                        { code: 'project_scope_changed' },
                    )
                }
            }
        }
        // 如果后端支持批量更新
        if (resource === 'tickets') {
            const updates = params.data ?? {};
            if (!updates || Object.keys(updates).length === 0) {
                throw new HttpError('请先选择需要更新的字段', 400);
            }

            const ticketsPath = await projectResourcePath('tickets', scope)
            const url = joinApiUrl(apiUrl, `${ticketsPath}/bulk-update`);
            const { json } = await httpClient(url, {
                method: 'POST',
                body: JSON.stringify({
                    tickets: ticketPreconditions(params.ids, scope!),
                    updates,
                }),
            }, scope);
            const payload = extractResponseData(json);
            if (isRecord(payload) && Array.isArray(payload.tickets)) {
                rememberRecordScopes(resource, payload.tickets, scope)
                rememberTicketVersions(payload.tickets, scope!);
            }
            return { data: params.ids };
        }

        // 否则逐个更新
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        await Promise.all(
            params.ids.map(id =>
                httpClient(joinApiUrl(
                    apiUrl,
                    resource === 'users'
                        ? humanApiRoutes.updatePlatformUser({
                            userID: Number(id),
                        })
                        : `${apiPath}/${id}`,
                ), {
                    method: 'PUT',
                    body: JSON.stringify(params.data),
                }, scope)
            )
        );
        
        return { data: params.ids };
    },

    // 删除资源
    delete: async (resource, params) => {
        const scope = projectScopedResources.has(resource)
            ? requireRecordScope(resource, params.id)
            : undefined
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        const urlPath = resource === 'users'
            ? humanApiRoutes.deletePlatformUser({
                userID: Number(params.id),
            })
            : resource === 'tickets'
            ? humanApiRoutes.deleteProjectTicket(
                { projectKey: scope?.project_key ?? '', ticketID: Number(params.id) },
            )
            : resource === 'notifications'
                ? humanApiRoutes.deleteProjectNotification({
                    projectKey: scope?.project_key ?? '',
                    notificationID: Number(params.id),
                })
                : resource === 'automation-rules'
                    ? humanApiRoutes.deleteProjectAutomationRule({
                        projectKey: scope?.project_key ?? '',
                        ruleID: Number(params.id),
                    })
                    : `${apiPath}/${params.id}`
        const url = joinApiUrl(apiUrl, urlPath);
        const cachedVersion = resource === 'tickets' && scope
            ? ticketVersionCache.get(ticketVersionCacheKey(params.id, scope))
            : undefined
        const headers = resource === 'tickets'
            ? ticketIfMatchHeaders(
                ticketVersionFromUpdate(
                    params.previousData,
                    cachedVersion === undefined
                        ? undefined
                        : { version: cachedVersion },
                ),
            )
            : undefined
        const { json } = await httpClient(url, {
            method: 'DELETE',
            headers,
        }, scope);

        if (resource === 'tickets' && scope) {
            ticketVersionCache.delete(ticketVersionCacheKey(params.id, scope))
            recordScopeCache.delete(
                recordScopeCacheKey(resource, params.id, scope.project_key),
            )
            return {
                data: params.previousData ?? { id: params.id },
            }
        }
        if (json.code === 0 && json.data) {
            return { data: json.data };
        } else if (json.data) {
            return { data: json.data };
        }
        return { data: json };
    },

    // 批量删除
    deleteMany: async (resource, params) => {
        const scope =
            projectScopedResources.has(resource) && params.ids.length > 0
                ? requireRecordScope(resource, params.ids[0])
                : captureResourceScope(resource)
        if (scope) {
            for (const id of params.ids.slice(1)) {
                requireRecordScope(resource, id)
            }
        }
        // 如果后端支持批量删除
        if (resource === 'tickets') {
            const ticketsPath = await projectResourcePath('tickets', scope)
            const url = joinApiUrl(apiUrl, `${ticketsPath}/bulk-delete`);
            const { json } = await httpClient(url, {
                method: 'DELETE',
                body: JSON.stringify({
                    tickets: ticketPreconditions(params.ids, scope!),
                }),
            }, scope);
            const payload = extractResponseData(json)
            const deletedIds = isRecord(payload) && Array.isArray(payload.deleted_ids)
                ? payload.deleted_ids
                : []
            const failedIds = isRecord(payload) && Array.isArray(payload.failed_ids)
                ? payload.failed_ids
                : []
            for (const id of deletedIds) {
                ticketVersionCache.delete(ticketVersionCacheKey(id, scope!))
                recordScopeCache.delete(
                    recordScopeCacheKey(
                        resource,
                        id as string | number,
                        scope!.project_key,
                    ),
                )
            }
            if (failedIds.length > 0) {
                throw new HttpError(
                    deletedIds.length > 0
                        ? '部分工单因版本冲突或权限变化未能删除，列表已刷新'
                        : '工单删除失败，请刷新列表后重试',
                    409,
                    payload,
                )
            }
            return { data: deletedIds };
        }

        // 否则逐个删除
        let apiPath = resource;
        if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        apiPath = await scopedApiPath(resource, apiPath, scope)
        await Promise.all(
            params.ids.map(id =>
                httpClient(joinApiUrl(
                    apiUrl,
                    resource === 'users'
                        ? humanApiRoutes.deletePlatformUser({
                            userID: Number(id),
                        })
                        : `${apiPath}/${id}`,
                ), {
                    method: 'DELETE',
                }, scope)
            )
        );
        
        return { data: params.ids };
    },
};
