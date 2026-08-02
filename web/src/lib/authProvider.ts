import type { AuthProvider, UserIdentity } from 'react-admin'
import {
    humanApiRoutes,
    type ForgotPasswordRequest,
    type AuthSession,
    type HumanSessionUser,
    type LoginRequest,
    type LogoutRequest,
    type RefreshTokenRequest,
    type ResetHumanPasswordRequest,
} from '@/lib/generated/human-api'
import {
    containsChineseText,
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
    sessionAwareFetch,
} from './apiClient'
import {
    hasPlatformCapability,
    parsePlatformRole,
    type AccessPermissions,
} from './accessControl'
import {
    clearProjectScopeCache,
    hasProjectCapability,
    parseProjectRole,
    readHumanSessionBinding,
    resolveActiveProjectAccess,
} from './projectScope'
import {
    authenticationStorageKeys,
} from './humanSessionStorage'
import { bindHumanTabSession } from './humanTabSession'
import { joinApiUrl } from './apiUrl'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')
const buildUrl = (path: string) => joinApiUrl(apiBase, path)

const rememberDevicePreferenceKey = 'rememberDevicePreference'
type LoginParams = {
    username: string
    password: string
    remember?: boolean
    rememberDevice?: boolean
    otpCode?: string
    deviceName?: string
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const positiveInteger = (value: unknown): value is number =>
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value > 0

const nonEmptyString = (value: unknown): value is string =>
    typeof value === 'string' && value.length > 0

const safeFetch = async (
    input: RequestInfo | URL,
    init: RequestInit | undefined,
    fallback: string,
) => {
    try {
        return await sessionAwareFetch(input, init)
    } catch (error) {
        throw new Error(localizedUnknownErrorMessage(error, fallback))
    }
}

const responseData = (value: unknown): unknown => {
    if (isRecord(value) && 'data' in value) return value.data
    return value
}

const parseHumanSessionUser = (value: unknown): HumanSessionUser | null => {
    if (!isRecord(value)) return null
    const platformRole = parsePlatformRole(value.platform_role)
    if (
        !positiveInteger(value.id) ||
        !nonEmptyString(value.username) ||
        !nonEmptyString(value.email) ||
        platformRole === null ||
        !['active', 'inactive', 'suspended', 'deleted'].includes(
            typeof value.status === 'string' ? value.status : '',
        ) ||
        typeof value.email_verified !== 'boolean' ||
        typeof value.otp_enabled !== 'boolean'
    ) {
        return null
    }
    return value as HumanSessionUser
}

const parseAuthSession = (value: unknown): AuthSession | null => {
    if (!isRecord(value)) return null
    const user = parseHumanSessionUser(value.user)
    if (
        user === null ||
        !nonEmptyString(value.access_token) ||
        !nonEmptyString(value.refresh_token) ||
        !positiveInteger(value.expires_in) ||
        value.token_type !== 'Bearer'
    ) {
        return null
    }
    const binding = readHumanSessionBinding(value.access_token)
    if (
        binding === null ||
        binding.subject !== String(user.id) ||
        binding.platform_role !== user.platform_role
    ) {
        return null
    }
    return value as AuthSession
}

const readStoredUser = (): HumanSessionUser | null => {
    const serialized = localStorage.getItem('user')
    const binding = readHumanSessionBinding()
    if (!serialized || !binding) return null
    try {
        const user = parseHumanSessionUser(JSON.parse(serialized))
        if (
            user === null ||
            binding.subject !== String(user.id) ||
            binding.platform_role !== user.platform_role
        ) {
            return null
        }
        return user
    } catch {
        return null
    }
}

export const hasCompleteAuthenticationState = (): boolean =>
    readStoredUser() !== null

export const clearAuthenticationState = (): void => {
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
    }
    bindHumanTabSession(null)
    clearProjectScopeCache()
}

const storeAuthSession = (
    session: AuthSession,
    preserveProjectScope: boolean,
): void => {
    const binding = readHumanSessionBinding(session.access_token)
    if (!binding) {
        throw new Error('登录响应中的会话标识无效')
    }
    if (!preserveProjectScope) {
        clearProjectScopeCache()
    }
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
    }
    localStorage.setItem('refreshToken', session.refresh_token)
    localStorage.setItem('user', JSON.stringify(session.user))
    localStorage.setItem('tokenExpiresAt', String(binding.expires_at))
    // Cross-tab listeners treat token as the session commit marker. Keep it
    // last so they never observe a partially written authentication state.
    localStorage.setItem('token', session.access_token)
    bindHumanTabSession(session.access_token)
}

const refreshStoredSession = async (): Promise<void> => {
    const refreshToken = localStorage.getItem('refreshToken')
    const previousBinding = readHumanSessionBinding()
    if (!refreshToken || !previousBinding) {
        clearAuthenticationState()
        throw new Error('登录状态已过期，请重新登录')
    }

    try {
        const payload: RefreshTokenRequest = {
            refresh_token: refreshToken,
        }
        const response = await safeFetch(
            buildUrl(humanApiRoutes.refreshHumanSession()),
            {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            },
            '登录状态刷新失败',
        )
        const body: unknown = await response.json().catch(() => ({}))
        const session = parseAuthSession(responseData(body))
        const nextBinding = session
            ? readHumanSessionBinding(session.access_token)
            : null
        if (
            !response.ok ||
            session === null ||
            nextBinding === null ||
            nextBinding.subject !== previousBinding.subject ||
            nextBinding.session_id !== previousBinding.session_id
        ) {
            throw new Error(
                localizedApiErrorMessage(
                    body,
                    response.status,
                    '登录状态刷新失败',
                ),
            )
        }
        storeAuthSession(session, true)
    } catch (error) {
        clearAuthenticationState()
        throw error
    }
}

const userIdentity = (user: HumanSessionUser): UserIdentity => {
    const userRecord: Record<string, unknown> = user
    const profile = isRecord(userRecord.profile) ? userRecord.profile : null
    const firstName =
        typeof profile?.first_name === 'string' ? profile.first_name : ''
    const lastName =
        typeof profile?.last_name === 'string' ? profile.last_name : ''
    const displayName =
        typeof profile?.display_name === 'string'
            ? profile.display_name
            : ''
    const avatar =
        typeof profile?.avatar === 'string' && profile.avatar
            ? profile.avatar
            : undefined
    return {
        id: user.id,
        fullName:
            [firstName, lastName].filter(Boolean).join(' ') ||
            displayName ||
            user.username ||
            user.email,
        avatar,
        email: user.email,
    }
}

const fetchCurrentUser = async (): Promise<HumanSessionUser> => {
    const token = localStorage.getItem('token')
    const binding = readHumanSessionBinding(token)
    if (!token || !binding) {
        clearAuthenticationState()
        throw new Error('未找到有效登录令牌，请重新登录')
    }
    const response = await safeFetch(
        buildUrl(humanApiRoutes.getHumanSessionUser()),
        {
            credentials: 'include',
            headers: { Authorization: `Bearer ${token}` },
        },
        '获取当前用户身份失败',
    )
    const body: unknown = await response.json().catch(() => ({}))
    const user = parseHumanSessionUser(responseData(body))
    if (
        !response.ok ||
        user === null ||
        binding.subject !== String(user.id) ||
        binding.platform_role !== user.platform_role
    ) {
        if (response.status === 401) {
            clearAuthenticationState()
        }
        throw new Error(
            localizedApiErrorMessage(body, response.status, '获取当前用户身份失败'),
        )
    }
    localStorage.setItem('user', JSON.stringify(user))
    return user
}

export const logoutAllSessions = async (): Promise<void> => {
    const token = localStorage.getItem('token')
    try {
        if (token) {
            const response = await safeFetch(
                buildUrl(humanApiRoutes.deleteAllHumanSessions()),
                {
                    method: 'POST',
                    credentials: 'include',
                    headers: { Authorization: `Bearer ${token}` },
                },
                '从所有设备退出失败',
            )
            if (!response.ok) {
                const body: unknown = await response.json().catch(() => ({}))
                throw new Error(
                    localizedApiErrorMessage(
                        body,
                        response.status,
                        '从所有设备退出失败',
                    ),
                )
            }
        }
    } finally {
        clearAuthenticationState()
    }
}

export const authProvider: AuthProvider = {
    login: async (params) => {
        const {
            username,
            password,
            remember,
            rememberDevice,
            otpCode,
            deviceName,
        } = params as LoginParams

        // Preserve the shared session until a replacement is fully validated.
        // Clearing it before the network round trip can log out other tabs and
        // leave them without a listener for the eventual replacement commit.
        const payload: LoginRequest = {
            email: username,
            password,
        }
        const shouldRememberDevice = Boolean(rememberDevice ?? remember)
        if (shouldRememberDevice) {
            payload.remember_device = true
            if (deviceName) payload.device_name = deviceName
        }
        if (otpCode) payload.otp_code = otpCode
        localStorage.setItem(
            rememberDevicePreferenceKey,
            shouldRememberDevice ? 'true' : 'false',
        )

        const response = await safeFetch(
            buildUrl(humanApiRoutes.createHumanSession()),
            {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                credentials: 'include',
                body: JSON.stringify(payload),
            },
            '网络连接失败，无法登录',
        )
        const body: unknown = await response.json().catch(() => ({}))
        if (!response.ok || (isRecord(body) && body.code === 1)) {
            const rawMessage = isRecord(body)
                ? [body.msg, body.message].find(
                    (value) => typeof value === 'string',
                )
                : undefined
            const message =
                typeof rawMessage === 'string' && containsChineseText(rawMessage)
                    ? rawMessage
                    : typeof rawMessage === 'string' && /otp/i.test(rawMessage)
                      ? '动态验证码无效或已过期，请重新输入'
                      : typeof rawMessage === 'string' &&
                          /device/i.test(rawMessage)
                        ? '受信任设备凭据已失效，请重新验证'
                        : '登录失败，请检查邮箱、密码和验证码'
            throw new Error(message)
        }

        const session = parseAuthSession(responseData(body))
        if (session === null) {
            throw new Error('登录响应包含无效的平台角色或会话信息')
        }
        storeAuthSession(session, false)
    },

    logout: async () => {
        try {
            const token = localStorage.getItem('token')
            const refreshToken = localStorage.getItem('refreshToken')
            if (token) {
                const payload: LogoutRequest | undefined = refreshToken
                    ? { refresh_token: refreshToken }
                    : undefined
                await safeFetch(
                    buildUrl(humanApiRoutes.deleteHumanSession()),
                    {
                        method: 'POST',
                        credentials: 'include',
                        headers: {
                            Authorization: `Bearer ${token}`,
                            'Content-Type': 'application/json',
                        },
                        body: payload ? JSON.stringify(payload) : undefined,
                    },
                    '退出登录请求失败',
                )
            }
        } finally {
            clearAuthenticationState()
        }
    },

    checkAuth: async () => {
        let binding = readHumanSessionBinding()
        if (!binding || readStoredUser() === null) {
            clearAuthenticationState()
            throw new Error('登录会话无效，请重新登录')
        }
        if (Date.now() >= binding.expires_at) {
            await refreshStoredSession()
            binding = readHumanSessionBinding()
        }
        if (!binding || readStoredUser() === null) {
            clearAuthenticationState()
            throw new Error('登录状态已过期，请重新登录')
        }
    },

    checkError: async (error) => {
        const status = isRecord(error) ? error.status : undefined
        if (status === 401) {
            clearAuthenticationState()
            throw new Error('身份认证失败，请重新登录')
        }
    },

    getIdentity: async (): Promise<UserIdentity> => {
        const stored = readStoredUser()
        return userIdentity(stored ?? (await fetchCurrentUser()))
    },

    getPermissions: async (): Promise<AccessPermissions> => {
        const user = readStoredUser()
        if (!user) {
            clearAuthenticationState()
            throw new Error('登录会话无效，请重新登录')
        }
        try {
            const access = await resolveActiveProjectAccess()
            const projectRole = parseProjectRole(access.project_role)
            if (projectRole === null) {
                throw new Error('当前项目角色无效')
            }
            return {
                platform_role: user.platform_role,
                project_role: projectRole,
                project_key: access.project.key,
                can_create_knowledge_drafts:
                    access.can_create_knowledge_drafts,
            }
        } catch {
            if (readStoredUser() === null) {
                throw new Error('登录会话无效，请重新登录')
            }
            return {
                platform_role: user.platform_role,
                project_role: null,
                project_key: null,
                can_create_knowledge_drafts: false,
            }
        }
    },

    canAccess: async ({ resource, action, record }) => {
        const user = readStoredUser()
        if (!user) return false

        if (resource === 'users') {
            return hasPlatformCapability(
                user.platform_role,
                'manage_platform_users',
            )
        }

        let access
        try {
            access = await resolveActiveProjectAccess()
        } catch {
            return false
        }
        const projectRole = parseProjectRole(access.project_role)
        if (projectRole === null) return false

        if (resource === 'tickets') {
            if (['list', 'show', 'read'].includes(action)) {
                return hasProjectCapability(projectRole, 'view_project')
            }
            if (action === 'create') {
                return hasProjectCapability(projectRole, 'create_ticket')
            }
            if (action === 'delete') {
                return hasProjectCapability(projectRole, 'delete_ticket')
            }
            if (action !== 'edit') return false
            if (projectRole === 'project_admin' || projectRole === 'manager') {
                return true
            }
            if (!record) {
                return projectRole === 'agent' || projectRole === 'requester'
            }
            const userID = Number(user.id)
            if (projectRole === 'agent') {
                return (
                    record.assigned_to_id == null ||
                    record.assigned_to_id === userID
                )
            }
            if (projectRole === 'requester') {
                return record.created_by_id === userID
            }
            return false
        }

        if (resource === 'notifications') {
            if (['list', 'show', 'read', 'edit'].includes(action)) {
                return hasProjectCapability(projectRole, 'view_project')
            }
            return hasProjectCapability(projectRole, 'manage_notifications')
        }

        if (
            resource === 'automation-rules' ||
            resource === 'automation-logs'
        ) {
            return hasProjectCapability(projectRole, 'manage_automation')
        }

        return false
    },

    forgotPassword: async ({ email }: { email: string }) => {
        const payload: ForgotPasswordRequest = { email }
        const response = await safeFetch(
            new Request(buildUrl(humanApiRoutes.requestHumanPasswordReset()), {
                method: 'POST',
                body: JSON.stringify(payload),
                headers: new Headers({ 'Content-Type': 'application/json' }),
            }),
            undefined,
            '发送密码重置请求失败',
        )
        if (!response.ok) {
            const error: unknown = await response.json().catch(() => null)
            throw new Error(
                localizedApiErrorMessage(
                    error,
                    response.status,
                    '发送密码重置请求失败',
                ),
            )
        }
    },

    resetPassword: async ({
        token,
        password,
    }: {
        token: string
        password: string
    }) => {
        const payload: ResetHumanPasswordRequest = {
            token,
            new_password: password,
        }
        const response = await safeFetch(
            new Request(buildUrl(humanApiRoutes.resetHumanPassword()), {
                method: 'POST',
                body: JSON.stringify(payload),
                headers: new Headers({ 'Content-Type': 'application/json' }),
            }),
            undefined,
            '重置密码失败',
        )
        if (!response.ok) {
            const error: unknown = await response.json().catch(() => null)
            throw new Error(
                localizedApiErrorMessage(
                    error,
                    response.status,
                    '重置密码失败',
                ),
            )
        }
    },
}
