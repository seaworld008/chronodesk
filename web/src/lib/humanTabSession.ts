import type { PlatformRole } from '@/lib/generated/human-api'
import { parsePlatformRole } from './accessControl'

export type HumanSessionBinding = {
    subject: string
    session_id: string
    platform_role: PlatformRole
    expires_at: number
}

let boundAccessToken: string | null =
    typeof window === 'undefined'
        ? null
        : window.localStorage.getItem('token')

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const positiveInteger = (value: unknown): value is number =>
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value > 0

const nonEmptyString = (value: unknown): value is string =>
    typeof value === 'string' && value.length > 0

const decodeBase64Url = (value: string): string => {
    const normalized = value.replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized.padEnd(
        normalized.length + ((4 - (normalized.length % 4)) % 4),
        '=',
    )
    return atob(padded)
}

export const readHumanSessionBinding = (
    token = localStorage.getItem('token'),
): HumanSessionBinding | null => {
    if (!token) return null
    const parts = token.split('.')
    if (parts.length !== 3) return null
    try {
        const payload: unknown = JSON.parse(decodeBase64Url(parts[1]))
        if (!isRecord(payload)) return null
        const platformRole = parsePlatformRole(payload.platform_role)
        if (
            !nonEmptyString(payload.sub) ||
            !nonEmptyString(payload.sid) ||
            platformRole === null ||
            !positiveInteger(payload.exp)
        ) {
            return null
        }
        return {
            subject: payload.sub,
            session_id: payload.sid,
            platform_role: platformRole,
            expires_at: payload.exp * 1000,
        }
    } catch {
        return null
    }
}

export const readCommittedHumanTabSessionToken = (): string | null => {
    const token = localStorage.getItem('token')
    const refreshToken = localStorage.getItem('refreshToken')
    const serializedUser = localStorage.getItem('user')
    const serializedExpiresAt = localStorage.getItem('tokenExpiresAt')
    const binding = readHumanSessionBinding(token)
    if (
        !token ||
        !nonEmptyString(refreshToken) ||
        !serializedUser ||
        !serializedExpiresAt ||
        binding === null ||
        serializedExpiresAt !== String(binding.expires_at)
    ) {
        return null
    }
    try {
        const user: unknown = JSON.parse(serializedUser)
        if (
            !isRecord(user) ||
            !positiveInteger(user.id) ||
            !nonEmptyString(user.username) ||
            !nonEmptyString(user.email) ||
            parsePlatformRole(user.platform_role) !== binding.platform_role ||
            binding.subject !== String(user.id) ||
            !['active', 'inactive', 'suspended', 'deleted'].includes(
                typeof user.status === 'string' ? user.status : '',
            ) ||
            typeof user.email_verified !== 'boolean' ||
            typeof user.otp_enabled !== 'boolean'
        ) {
            return null
        }
        return token
    } catch {
        return null
    }
}

export const bindHumanTabSession = (accessToken: string | null): void => {
    boundAccessToken = accessToken
}

export const humanTabSessionMatches = (
    accessToken: string | null,
): boolean => boundAccessToken === accessToken

const sameStableBinding = (
    left: HumanSessionBinding,
    right: HumanSessionBinding,
): boolean =>
    left.subject === right.subject &&
    left.session_id === right.session_id

export const adoptHumanTabSessionRotation = (
    accessToken: string,
): boolean => {
    const boundBinding = readHumanSessionBinding(boundAccessToken)
    const nextBinding = readHumanSessionBinding(accessToken)
    if (
        boundBinding === null ||
        nextBinding === null ||
        !sameStableBinding(boundBinding, nextBinding)
    ) {
        return false
    }
    boundAccessToken = accessToken
    return true
}

export const resolveHumanBearerForRequest = (
    capturedAccessToken: string,
): string | null => {
    const committedAccessToken = readCommittedHumanTabSessionToken()
    const capturedBinding = readHumanSessionBinding(capturedAccessToken)
    const committedBinding = readHumanSessionBinding(committedAccessToken)
    if (
        committedAccessToken === null ||
        capturedBinding === null ||
        committedBinding === null ||
        !sameStableBinding(capturedBinding, committedBinding)
    ) {
        return null
    }
    if (
        !humanTabSessionMatches(committedAccessToken) &&
        !adoptHumanTabSessionRotation(committedAccessToken)
    ) {
        return null
    }
    return committedAccessToken
}
