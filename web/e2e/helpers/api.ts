import type { APIRequestContext } from '@playwright/test';
import { platformRoleValues } from '../../src/lib/generated/human-api';
import type {
    AuthSession as GeneratedAuthSession,
    HumanSessionUser as GeneratedHumanSessionUser,
    PlatformRole as GeneratedPlatformRole,
} from '../../src/lib/generated/human-api';
import { assertDestructiveE2EAllowed, testBaseURL } from './safety';

export type Credentials = {
    email: string;
    password: string;
};

export type PlatformRole = GeneratedPlatformRole;
export type HumanSessionUser = GeneratedHumanSessionUser;
export type AuthSession = GeneratedAuthSession;

const isPlatformRole = (value: unknown): value is PlatformRole =>
    typeof value === 'string' &&
    platformRoleValues.some((role) => role === value);

const isHumanSessionUser = (value: unknown): value is HumanSessionUser => {
    if (!value || typeof value !== 'object') {
        return false;
    }
    const user = value as Record<string, unknown>;
    return (
        typeof user.id === 'number' &&
        Number.isSafeInteger(user.id) &&
        user.id > 0 &&
        typeof user.username === 'string' &&
        typeof user.email === 'string' &&
        isPlatformRole(user.platform_role) &&
        ['active', 'inactive', 'suspended', 'deleted'].includes(
            typeof user.status === 'string' ? user.status : '',
        ) &&
        typeof user.email_verified === 'boolean' &&
        typeof user.otp_enabled === 'boolean'
    );
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
        headers: {
            Origin: testBaseURL().origin,
        },
        data: {
            email: credentials.email,
            password: credentials.password,
        },
    });

    const json = await response.json().catch(() => ({}));
    if (!response.ok()) {
        throw new Error(extractErrorMessage(json));
    }

    const payload = json as Record<string, unknown>;
    if ((payload.code as number | undefined) === 1) {
        throw new Error(extractErrorMessage(payload));
    }

    const data = (payload.data ?? {}) as Record<string, unknown>;
    if (typeof data.access_token !== 'string' || data.access_token.length === 0) {
        throw new Error('登录响应缺少 access_token');
    }
    if (!isHumanSessionUser(data.user)) {
        throw new Error('登录响应缺少合法的 platform_role 用户身份');
    }
    if (
        typeof data.expires_in !== 'number' ||
        !Number.isSafeInteger(data.expires_in) ||
        data.expires_in <= 0 ||
        data.token_type !== 'Bearer'
    ) {
        throw new Error('登录响应缺少合法的过期信息');
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
    if (!response.ok()) {
        throw new Error(extractErrorMessage(json));
    }

    const payload = json as Record<string, unknown>;
    if (payload.success === false || payload.code === 1) {
        throw new Error(extractErrorMessage(payload));
    }

    return json as T;
};
