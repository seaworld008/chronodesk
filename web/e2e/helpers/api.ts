import type { APIRequestContext } from '@playwright/test';
import { assertDestructiveE2EAllowed } from './safety';

export type Credentials = {
    email: string;
    password: string;
};

export type AuthSession = {
    access_token: string;
    refresh_token?: string;
    user?: Record<string, unknown>;
    permissions?: unknown;
    expires_in?: number;
};

const extractErrorMessage = (payload: unknown): string => {
    if (!payload || typeof payload !== 'object') {
        return '请求失败';
    }
    const data = payload as Record<string, unknown>;
    return (data.msg as string) || (data.message as string) || '请求失败';
};

export const loginSession = async (
    request: APIRequestContext,
    credentials: Credentials,
): Promise<AuthSession> => {
    const response = await request.post('/api/auth/login', {
        data: {
            email: credentials.email,
            password: credentials.password,
        },
    });

    const json = await response.json().catch(() => ({}));
    if (!response.ok) {
        throw new Error(extractErrorMessage(json));
    }

    const payload = json as Record<string, unknown>;
    if ((payload.code as number | undefined) === 1) {
        throw new Error(extractErrorMessage(payload));
    }

    const data = (payload.data ?? {}) as Partial<AuthSession>;
    if (!data.access_token) {
        throw new Error('登录响应缺少 access_token');
    }

    return data as AuthSession;
};

type RequestOptions = {
    method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
    data?: unknown;
    headers?: Record<string, string>;
};

export const apiRequest = async <T>(
    request: APIRequestContext,
    token: string,
    url: string,
    options: RequestOptions = {},
): Promise<T> => {
    const method = options.method ?? 'GET';
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
        assertDestructiveE2EAllowed(`${method} ${url}`);
    }
    const response = await request.fetch(url, {
        method,
        headers: {
            Authorization: `Bearer ${token}`,
            'Content-Type': 'application/json',
            ...options.headers,
        },
        data: options.data,
    });

    const json = await response.json().catch(() => ({}));
    if (!response.ok) {
        throw new Error(extractErrorMessage(json));
    }

    const payload = json as Record<string, unknown>;
    if (payload.success === false || payload.code === 1) {
        throw new Error(extractErrorMessage(payload));
    }

    return json as T;
};
