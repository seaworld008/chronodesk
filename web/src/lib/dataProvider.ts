import { DataProvider, fetchUtils, HttpError } from 'react-admin'
import queryString from 'query-string'
import { containsChineseText, localizedApiErrorMessage } from './apiClient'

const apiUrl = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

/**
 * 自定义HTTP客户端，处理JWT认证和请求格式化
 */
type HttpClientOptions = RequestInit & { headers?: Headers }

const httpClient = async (url: string, options: HttpClientOptions = {}) => {
    const token = localStorage.getItem('token')
    const headers = new Headers(options.headers ?? { Accept: 'application/json' })

    if (!(options.body instanceof FormData)) {
        headers.set('Content-Type', 'application/json')
    }

    if (token) {
        headers.set('Authorization', `Bearer ${token}`)
    }

    try {
        return await fetchUtils.fetchJson(url, { ...options, headers })
    } catch (error: unknown) {
        return handleHttpError(error)
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

function handleHttpError(error: unknown): never {
    if (error instanceof HttpError) {
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

const parseListResponse = (resource: string, json: unknown, headers: Headers) => {
    if (isRecord(json) && resource === 'automation-logs' && isRecord(json.data) && Array.isArray(json.data.logs)) {
        const data = json.data.logs
        const total = (json.data.total as number | undefined) ?? data.length
        return { data, total }
    }

    if (isRecord(json) && resource === 'automation-rules' && isRecord(json.data) && Array.isArray(json.data.rules)) {
        const data = json.data.rules
        const total = (json.data.total as number | undefined) ?? data.length
        return { data, total }
    }

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
            } else if (resource === 'users' || resource === 'admin/users') {
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
            } else if (resource === 'users' || resource === 'admin/users') {
                const searchValue = extractSearchValue();
                if (searchValue) {
                    query.search = searchValue;
                }
                if (filter.role) {
                    query.role = filter.role;
                    delete filter.role;
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
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users'; // 管理员用户管理API
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url);

        return parseListResponse(resource, json, headers);
    },

    // 获取单个资源
    getOne: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        const url = `${apiUrl}/${apiPath}/${params.id}`;
        const { json } = await httpClient(url);
        
        return { data: extractTypedResponseData(json) };
    },

    // 获取多个资源
    getMany: async (resource, params) => {
        if (resource === 'users' || resource === 'user') {
            const records = await Promise.all(
                params.ids.map(async (id) => {
                    const { json } = await httpClient(`${apiUrl}/admin/users/${id}`)
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

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json } = await httpClient(url);
        
        const payload = extractResponseData(json);
        if (isRecord(payload) && Array.isArray(payload.items)) {
            return { data: payload.items };
        }
        if (Array.isArray(payload)) {
            return { data: payload };
        }
        if (isRecord(payload)) {
            return { data: [payload] };
        }
        return { data: [] };
    },

    // 获取引用资源
    getManyReference: async (resource, params) => {
        const { page, perPage } = params.pagination || { page: 1, perPage: 10 };
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

        if (params.target === 'ticket_id' && (resource === 'comments' || resource === 'ticket_history')) {
            const nestedResource = resource === 'comments' ? 'comments' : 'history'
            const url = `${apiUrl}/tickets/${params.id}/${nestedResource}?${queryString.stringify({
                page,
                page_size: perPage,
                sort: query.sort,
            })}`
            const { json, headers } = await httpClient(url)
            return parseListResponse(resource, json, headers)
        }

        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url);

        return parseListResponse(resource, json, headers);
    },

    // 创建资源
    create: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        const url = `${apiUrl}/${apiPath}`;
        
        try {
            const { json } = await httpClient(url, {
                method: 'POST',
                body: JSON.stringify(params.data),
            });

            return { data: extractTypedResponseData(json) };
        } catch (error: unknown) {
            return handleHttpError(error)
        }
    },

    // 更新资源
    update: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        const url = `${apiUrl}/${apiPath}/${params.id}`;
        
        try {
            const { json } = await httpClient(url, {
                method: 'PUT',
                body: JSON.stringify(params.data),
            });

            return { data: extractTypedResponseData(json) };
        } catch (error: unknown) {
            return handleHttpError(error)
        }
    },

    // 批量更新
    updateMany: async (resource, params) => {
        // 如果后端支持批量更新
        if (resource === 'tickets') {
            const updates = params.data ?? {};
            if (!updates || Object.keys(updates).length === 0) {
                throw new HttpError('请先选择需要更新的字段', 400);
            }

            const url = `${apiUrl}/tickets/bulk-update`;
            await httpClient(url, {
                method: 'POST',
                body: JSON.stringify({
                    ticket_ids: params.ids,
                    updates,
                }),
            });
            return { data: params.ids };
        }

        // 否则逐个更新
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        await Promise.all(
            params.ids.map(id =>
                httpClient(`${apiUrl}/${apiPath}/${id}`, {
                    method: 'PUT',
                    body: JSON.stringify(params.data),
                })
            )
        );
        
        return { data: params.ids };
    },

    // 删除资源
    delete: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        const url = `${apiUrl}/${apiPath}/${params.id}`;
        const { json } = await httpClient(url, {
            method: 'DELETE',
        });

        if (json.code === 0 && json.data) {
            return { data: json.data };
        } else if (json.data) {
            return { data: json.data };
        }
        return { data: json };
    },

    // 批量删除
    deleteMany: async (resource, params) => {
        // 如果后端支持批量删除
        if (resource === 'tickets') {
            const url = `${apiUrl}/tickets/bulk-delete`;
            await httpClient(url, {
                method: 'DELETE',
                body: JSON.stringify({ ids: params.ids }),
            });
            return { data: params.ids };
        }

        // 否则逐个删除
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        }

        await Promise.all(
            params.ids.map(id =>
                httpClient(`${apiUrl}/${apiPath}/${id}`, {
                    method: 'DELETE',
                })
            )
        );
        
        return { data: params.ids };
    },
};

export default dataProvider;
