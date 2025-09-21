import { DataProvider, fetchUtils, HttpError } from 'react-admin'
import queryString from 'query-string'

const apiUrl = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

/**
 * 自定义HTTP客户端，处理JWT认证和请求格式化
 */
type HttpClientOptions = RequestInit & { headers?: Headers }

const httpClient = (url: string, options: HttpClientOptions = {}) => {
    const token = localStorage.getItem('token')
    const headers = new Headers(options.headers ?? { Accept: 'application/json' })

    if (!(options.body instanceof FormData)) {
        headers.set('Content-Type', 'application/json')
    }

    if (token) {
        headers.set('Authorization', `Bearer ${token}`)
    }

    return fetchUtils.fetchJson(url, { ...options, headers })
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
 * 工单管理系统专用数据提供器
 * 完美适配Go Gin后端API
 */
const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const handleHttpError = (error: unknown): never => {
    if (error instanceof HttpError) {
        if (isRecord(error.body)) {
            const message =
                (error.body.msg as string | undefined) ||
                (error.body.message as string | undefined) ||
                error.message
            throw new HttpError(message, error.status, error.body)
        }
        throw error
    }

    const message = error instanceof Error ? error.message : '请求失败'
    throw new HttpError(message, 500, {})
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

        // 添加排序参数
        if (field && order) {
            query.sort = convertSortToGoFormat({ field, order });
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
        } else if (params.filter && Object.keys(params.filter).length > 0) {
            query.filter = convertFilterToGoFormat(params.filter as Record<string, unknown>);
        }

        // 特殊处理不同资源的API路径
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users'; // 管理员用户管理API
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        } else if (resource === 'automation-logs') {
            apiPath = 'admin/automation/logs';
        } else if (resource === 'system-settings') {
            // 系统设置是虚拟资源，返回空数据
            return {
                data: [],
                total: 0,
            };
        }

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url);

        // 处理不同的响应格式
        let data: unknown[] = [];
        let total = 0;

        if (isRecord(json) && resource === 'automation-logs' && isRecord(json.data) && Array.isArray(json.data.logs)) {
            data = json.data.logs;
            total = (json.data.total as number | undefined) ?? data.length;
        } else if (isRecord(json) && resource === 'automation-rules' && isRecord(json.data) && Array.isArray(json.data.rules)) {
            data = json.data.rules;
            total = (json.data.total as number | undefined) ?? data.length;
        } else if (isRecord(json) && json.code === 0 && json.data) {
            // Go API标准响应格式: {code: 0, data: {items: [...], total: number}}
            if (isRecord(json.data) && Array.isArray(json.data.items)) {
                data = json.data.items;
                total = (json.data.total as number | undefined) || (json.data.count as number | undefined) || data.length;
            } else if (Array.isArray(json.data)) {
                // 直接数组在data字段中: {code: 0, data: [...]}
                data = json.data;
                total = (json.total as number | undefined) || (json.count as number | undefined) || data.length;
            } else {
                // 其他嵌套格式
                data = [json.data];
                total = 1;
            }
        } else if (isRecord(json) && json.data && Array.isArray(json.data)) {
            // 旧格式支持: {data: [...], total: number}
            data = json.data;
            total = (json.total as number | undefined) || (json.count as number | undefined) || data.length;
        } else if (Array.isArray(json)) {
            // 直接数组格式
            data = json;
            total = getTotalFromHeaders(headers, data.length);
        } else {
            // 其他格式的错误处理
            console.warn(`Unexpected response format for resource ${resource}:`, json);
            data = [];
            total = 0;
        }

        return {
            data,
            total,
        };
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
        
        // 处理不同响应格式
        if (json.code === 0 && json.data) {
            return { data: json.data };
        } else if (json.data) {
            return { data: json.data };
        }
        return { data: json };
    },

    // 获取多个资源
    getMany: async (resource, params) => {
        // 如果后端支持批量查询
        const query = {
            filter: convertFilterToGoFormat({ ids: params.ids }),
        };

        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json } = await httpClient(url);
        
        let data = [];
        if (json.code === 0 && json.data) {
            if (json.data.items && Array.isArray(json.data.items)) {
                data = json.data.items;
            } else if (Array.isArray(json.data)) {
                data = json.data;
            } else {
                data = [json.data];
            }
        } else if (json.data && Array.isArray(json.data)) {
            data = json.data;
        } else if (Array.isArray(json)) {
            data = json;
        }

        return { data };
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

        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        } else if (resource === 'automation-rules') {
            apiPath = 'admin/automation/rules';
        }

        const url = `${apiUrl}/${apiPath}?${queryString.stringify(query)}`;
        const { json, headers } = await httpClient(url);

        let data = [];
        let total = 0;

        if (json.code === 0 && json.data) {
            // Go API标准响应格式: {code: 0, data: {items: [...], total: number}}
            if (json.data.items && Array.isArray(json.data.items)) {
                data = json.data.items;
                total = json.data.total || json.data.count || data.length;
            } else if (Array.isArray(json.data)) {
                // 直接数组在data字段中: {code: 0, data: [...]}
                data = json.data;
                total = json.total || json.count || data.length;
            } else {
                // 其他嵌套格式
                data = [json.data];
                total = 1;
            }
        } else if (json.data && Array.isArray(json.data)) {
            // 旧格式支持: {data: [...], total: number}
            data = json.data;
            total = json.total || json.count || data.length;
        } else if (Array.isArray(json)) {
            // 直接数组格式
            data = json;
            total = getTotalFromHeaders(headers, data.length);
        }

        return { data, total };
    },

    // 创建资源
    create: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        }

        const url = `${apiUrl}/${apiPath}`;
        
        try {
            const { json } = await httpClient(url, {
                method: 'POST',
                body: JSON.stringify(params.data),
            });

            if (isRecord(json) && json.code === 0 && json.data) {
                return { data: json.data };
            }
            if (isRecord(json) && json.data) {
                return { data: json.data };
            }
            return { data: json };
        } catch (error: unknown) {
            handleHttpError(error)
        }
    },

    // 更新资源
    update: async (resource, params) => {
        let apiPath = resource;
        if (resource === 'users' || resource === 'user') {
            apiPath = 'admin/users';
        }

        const url = `${apiUrl}/${apiPath}/${params.id}`;
        
        try {
            const { json } = await httpClient(url, {
                method: 'PUT',
                body: JSON.stringify(params.data),
            });

            if (isRecord(json) && json.code === 0 && json.data) {
                return { data: json.data };
            }
            if (isRecord(json) && json.data) {
                return { data: json.data };
            }
            return { data: json };
        } catch (error: unknown) {
            handleHttpError(error)
        }
    },

    // 批量更新
    updateMany: async (resource, params) => {
        // 如果后端支持批量更新
        if (resource === 'tickets') {
            const url = `${apiUrl}/tickets/bulk-update`;
            await httpClient(url, {
                method: 'POST',
                body: JSON.stringify({
                    ids: params.ids,
                    data: params.data,
                }),
            });
            return { data: params.ids };
        }

        // 否则逐个更新
        await Promise.all(
            params.ids.map(id =>
                httpClient(`${apiUrl}/${resource}/${id}`, {
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
        await Promise.all(
            params.ids.map(id =>
                httpClient(`${apiUrl}/${resource}/${id}`, {
                    method: 'DELETE',
                })
            )
        );
        
        return { data: params.ids };
    },

    // 自定义方法支持 - 用于系统设置等特殊API调用
    customMethod: async (resource: string, params: Record<string, unknown>, type: string) => {
        const method = typeof params.method === 'string' ? params.method.toUpperCase() : 'GET'
        const data = params.data as unknown
        const otherParams = Object.fromEntries(
            Object.entries(params).filter(([key]) => !['method', 'data'].includes(key))
        )

        let url = `${apiUrl}`

        if (resource.startsWith('admin/')) {
            url += `/${resource}`
        } else if (resource === 'email-config') {
            url += '/admin/email-config'
        } else if (resource === 'email-config/test') {
            url += '/admin/email-config/test'
        } else if (resource.startsWith('webhooks')) {
            url += `/${resource}`
        } else if (resource.startsWith('system/')) {
            url += `/admin/${resource}`
        } else {
            url += `/${resource}`
        }

        const requestOptions: HttpClientOptions = {
            method,
        }

        if (data && ['POST', 'PUT', 'PATCH'].includes(method)) {
            requestOptions.body = JSON.stringify(data)
        }

        if (method === 'GET' && Object.keys(otherParams).length > 0) {
            const queryParams = queryString.stringify(otherParams)
            if (queryParams) {
                url += `?${queryParams}`
            }
        }

        try {
            const { json, headers } = await httpClient(url, requestOptions)

            switch (type) {
                case 'getList': {
                    let listData: unknown[] = []
                    let total = 0

                    if (isRecord(json) && json.code === 0 && json.data) {
                        if (isRecord(json.data) && Array.isArray(json.data.items)) {
                            listData = json.data.items
                            total = (json.data.total as number | undefined) || (json.data.count as number | undefined) || listData.length
                        } else if (Array.isArray(json.data)) {
                            listData = json.data
                            total = (json.total as number | undefined) || (json.count as number | undefined) || listData.length
                        } else {
                            listData = [json.data]
                            total = 1
                        }
                    } else if (isRecord(json) && Array.isArray(json.data)) {
                        listData = json.data
                        total = (json.total as number | undefined) || (json.count as number | undefined) || listData.length
                    } else if (Array.isArray(json)) {
                        listData = json
                        total = getTotalFromHeaders(headers, listData.length)
                    }

                    return { data: listData, total }
                }

                case 'get':
                case 'getOne':
                case 'create':
                case 'update':
                case 'delete':
                    if (isRecord(json) && json.code === 0 && json.data) {
                        return { data: json.data }
                    }
                    if (isRecord(json) && json.data) {
                        return { data: json.data }
                    }
                    return { data: json }

                default:
                    return { data: json }
            }
        } catch (error: unknown) {
            handleHttpError(error)
        }
    },
};

export default dataProvider;
