import type {
    CreateAdminUserRequest,
    UpdateAdminUserRequest,
    UserStatus,
} from '@/lib/generated/human-api'
import { parsePlatformRole } from '@/lib/accessControl'

type FormRecord = Record<string, unknown>

const optionalString = (
    source: FormRecord,
    key: string,
): string | undefined => {
    const value = source[key]
    return typeof value === 'string' && value !== '' ? value : undefined
}

const requiredString = (source: FormRecord, key: string): string => {
    const value = optionalString(source, key)
    if (value === undefined) {
        throw new Error(`缺少必填字段：${key}`)
    }
    return value
}

const assignOptionalString = (
    target: Record<string, unknown>,
    source: FormRecord,
    key: string,
): void => {
    const value = source[key]
    if (typeof value === 'string') {
        target[key] = value
    }
}

const userStatuses = new Set<UserStatus>([
    'active',
    'inactive',
    'suspended',
    'deleted',
])

export const transformCreateAdminUser = (
    data: FormRecord,
): CreateAdminUserRequest => {
    const platformRole = parsePlatformRole(data.platform_role)
    if (platformRole === null) {
        throw new Error('平台职责无效')
    }
    const payload: CreateAdminUserRequest = {
        username: requiredString(data, 'username'),
        email: requiredString(data, 'email'),
        password: requiredString(data, 'password'),
        platform_role: platformRole,
    }
    for (const field of [
        'phone',
        'first_name',
        'last_name',
        'display_name',
        'department',
        'job_title',
    ] as const) {
        const value = optionalString(data, field)
        if (value !== undefined) payload[field] = value
    }
    if (data.manager_id === null) {
        payload.manager_id = null
    } else if (
        typeof data.manager_id === 'number' &&
        Number.isSafeInteger(data.manager_id) &&
        data.manager_id > 0
    ) {
        payload.manager_id = data.manager_id
    }
    return payload
}

export const transformUpdateAdminUser = (
    data: FormRecord,
): UpdateAdminUserRequest => {
    const payload: Record<string, unknown> = {}
    for (const field of [
        'email',
        'phone',
        'first_name',
        'last_name',
        'display_name',
        'timezone',
        'language',
        'department',
        'job_title',
    ] as const) {
        assignOptionalString(payload, data, field)
    }

    if (typeof data.platform_role !== 'undefined') {
        const platformRole = parsePlatformRole(data.platform_role)
        if (platformRole === null) {
            throw new Error('平台职责无效')
        }
        payload.platform_role = platformRole
    }
    if (typeof data.status !== 'undefined') {
        if (
            typeof data.status !== 'string' ||
            !userStatuses.has(data.status as UserStatus)
        ) {
            throw new Error('账户状态无效')
        }
        payload.status = data.status
    }
    if (typeof data.email_verified === 'boolean') {
        payload.email_verified = data.email_verified
    }
    if (data.manager_id === null) {
        payload.manager_id = null
    } else if (
        typeof data.manager_id === 'number' &&
        Number.isSafeInteger(data.manager_id) &&
        data.manager_id > 0
    ) {
        payload.manager_id = data.manager_id
    }
    return payload as UpdateAdminUserRequest
}
