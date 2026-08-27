import type { AuthProvider, UserIdentity } from 'react-admin'
import {
    humanApiRoutes,
    type ForgotPasswordRequest,
    type AuthSession,
    type HumanSessionUser,
    type LoginRequest,
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
    readHumanSessionMetadata,
    writeHumanSessionMetadata,
} from './humanSessionStorage'
import {
    bindHumanTabSession,
    readCommittedHumanTabSessionToken,
} from './humanTabSession'
import {
    clearHumanAccessToken,
    commitHumanAccessToken,
    readHumanAccessToken,
} from './humanSessionRuntime'
import {
    humanSessionSignOutMatchesBinding,
    publishAuthenticatedHumanSession,
    publishSignedOutHumanSession,
    type HumanSessionMetadata,
} from './humanSessionChannel'
import { withHumanSessionLifecycleLock } from './humanSessionLifecycle'
import { joinApiUrl } from './apiUrl'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')
const buildUrl = (path: string) => joinApiUrl(apiBase, path)
const publicHumanAuthPaths = new Set([
    '/login',
    '/register',
    '/forgot-password',
    '/reset-password',
    '/verify-email',
])

const isPublicHumanAuthRoute = (): boolean => {
    if (typeof window === 'undefined') return false
    const hashPath = window.location.hash
        .replace(/^#/, '')
        .split(/[?&]/u, 1)[0]
    return publicHumanAuthPaths.has(hashPath)
}

const rememberDevicePreferenceKey = 'rememberDevicePreference'
let remoteSignOutObserved = false
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

const hasOnlyKeys = (
    value: Record<string, unknown>,
    allowedKeys: ReadonlySet<string>,
): boolean => Object.keys(value).every((key) => allowedKeys.has(key))

const dateTimeString = (value: unknown): value is string =>
    typeof value === 'string' &&
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/u
        .test(value) &&
    !Number.isNaN(Date.parse(value))

const humanUserProfileKeys = new Set([
    'id',
    'user_id',
    'first_name',
    'last_name',
    'display_name',
    'avatar',
    'phone',
    'department',
    'position',
    'timezone',
    'language',
    'created_at',
    'updated_at',
])

const humanSessionUserKeys = new Set([
    'id',
    'username',
    'email',
    'platform_role',
    'status',
    'email_verified',
    'otp_enabled',
    'last_login_at',
    'profile',
])

const authSessionKeys = new Set([
    'user',
    'access_token',
    'expires_in',
    'token_type',
])

const loginSessionEnvelopeKeys = new Set(['code', 'msg', 'data'])
const refreshSessionEnvelopeKeys = new Set(['success', 'message', 'data'])

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

const validHumanUserProfile = (value: unknown): boolean => {
    if (!isRecord(value) || !hasOnlyKeys(value, humanUserProfileKeys)) {
        return false
    }
    return (
        positiveInteger(value.id) &&
        positiveInteger(value.user_id) &&
        typeof value.first_name === 'string' &&
        typeof value.last_name === 'string' &&
        typeof value.display_name === 'string' &&
        typeof value.avatar === 'string' &&
        typeof value.phone === 'string' &&
        typeof value.department === 'string' &&
        typeof value.position === 'string' &&
        typeof value.timezone === 'string' &&
        typeof value.language === 'string' &&
        dateTimeString(value.created_at) &&
        dateTimeString(value.updated_at)
    )
}

const parseStoredHumanSessionUser = (
    value: unknown,
): HumanSessionUser | null => {
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

const parseHumanSessionUser = (value: unknown): HumanSessionUser | null => {
    if (!isRecord(value) || !hasOnlyKeys(value, humanSessionUserKeys)) {
        return null
    }
    const user = parseStoredHumanSessionUser(value)
    if (
        user === null ||
        (
            value.last_login_at !== null &&
            !dateTimeString(value.last_login_at)
        ) ||
        (
            value.profile !== undefined &&
            !validHumanUserProfile(value.profile)
        )
    ) {
        return null
    }
    return user
}

const parseAuthSession = (value: unknown): AuthSession | null => {
    if (!isRecord(value) || !hasOnlyKeys(value, authSessionKeys)) {
        return null
    }
    const user = parseHumanSessionUser(value.user)
    if (
        user === null ||
        !nonEmptyString(value.access_token) ||
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

export const parseHumanLoginSessionResponse = (
    value: unknown,
): AuthSession | null => {
    if (
        !isRecord(value) ||
        !hasOnlyKeys(value, loginSessionEnvelopeKeys) ||
        value.code !== 0 ||
        typeof value.msg !== 'string'
    ) {
        return null
    }
    return parseAuthSession(value.data)
}

export const parseHumanRefreshSessionResponse = (
    value: unknown,
): AuthSession | null => {
    if (
        !isRecord(value) ||
        !hasOnlyKeys(value, refreshSessionEnvelopeKeys) ||
        value.success !== true ||
        value.message !== '登录令牌刷新成功'
    ) {
        return null
    }
    return parseAuthSession(value.data)
}

const readStoredUser = (): HumanSessionUser | null => {
    const serialized = readHumanSessionMetadata('user')
    const binding = readHumanSessionBinding()
    if (!serialized || !binding) return null
    try {
        const user = parseStoredHumanSessionUser(JSON.parse(serialized))
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

export const clearAuthenticationState = (
    options: {
        notifyPeers?: false | 'current_session' | 'all_devices'
    } = {},
): void => {
    const binding =
        options.notifyPeers === false || options.notifyPeers === undefined
            ? null
            : readHumanSessionBinding()
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
        sessionStorage.removeItem(key)
    }
    clearHumanAccessToken()
    bindHumanTabSession(null)
    clearProjectScopeCache()
    if (binding !== null && options.notifyPeers === 'current_session') {
        publishSignedOutHumanSession({
            scope: 'current_session',
            subject: binding.subject,
            session_id: binding.session_id,
        })
    } else if (binding !== null && options.notifyPeers === 'all_devices') {
        publishSignedOutHumanSession({
            scope: 'all_devices',
            subject: binding.subject,
        })
    }
}

export const applyRemoteHumanSignOut = (
    metadata: Extract<HumanSessionMetadata, { type: 'signed_out' }>,
): boolean => {
    if (
        !humanSessionSignOutMatchesBinding(
            metadata,
            readHumanSessionBinding(),
        )
    ) {
        return false
    }
    remoteSignOutObserved = true
    clearAuthenticationState({ notifyPeers: false })
    return true
}

const storeAuthSession = (
    session: AuthSession,
    preserveProjectScope: boolean,
): void => {
    remoteSignOutObserved = false
    const binding = readHumanSessionBinding(session.access_token)
    if (!binding) {
        throw new Error('登录响应中的会话标识无效')
    }
    if (!preserveProjectScope) {
        clearProjectScopeCache()
    }
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
        sessionStorage.removeItem(key)
    }
    writeHumanSessionMetadata('user', JSON.stringify(session.user))
    writeHumanSessionMetadata(
        'tokenExpiresAt',
        String(binding.expires_at),
    )
    commitHumanAccessToken(session.access_token)
    bindHumanTabSession(session.access_token)
    publishAuthenticatedHumanSession({
        subject: binding.subject,
        session_id: binding.session_id,
        expires_at: binding.expires_at,
    })
}

export type RegistrationSessionOutcome =
    | 'authenticated'
    | 'verification_required'

export const consumeRegistrationResult = (
    value: unknown,
): RegistrationSessionOutcome => {
    if (!isRecord(value)) {
        throw new Error('注册响应包含无效的用户或会话信息')
    }
    const user = parseHumanSessionUser(value.user)
    if (user === null) {
        throw new Error('注册响应包含无效的用户或会话信息')
    }

    if (!user.email_verified) {
        if (
            value.access_token === '' &&
            (
                value.refresh_token === undefined ||
                value.refresh_token === ''
            ) &&
            value.token_type === '' &&
            value.expires_in === 0
        ) {
            return 'verification_required'
        }
        throw new Error('注册响应包含无效的用户或会话信息')
    }

    const session = parseAuthSession(value)
    if (session === null) {
        throw new Error('注册响应包含无效的用户或会话信息')
    }
    storeAuthSession(session, false)
    return 'authenticated'
}

let refreshSessionRequest: Promise<void> | null = null

const performSessionRefresh = (): Promise<void> =>
    withHumanSessionLifecycleLock(async () => {
        const previousBinding = readHumanSessionBinding()

        const response = await safeFetch(
            buildUrl(humanApiRoutes.refreshHumanSession()),
            {
                method: 'POST',
                credentials: 'include',
            },
            '登录状态刷新失败',
        )
        const body: unknown = await response.json().catch(() => ({}))
        if (!response.ok) {
            if (response.status === 401) {
                remoteSignOutObserved = true
                clearAuthenticationState({ notifyPeers: false })
            }
            throw new Error(
                localizedApiErrorMessage(
                    body,
                    response.status,
                    '登录状态刷新失败',
                ),
            )
        }
        const session = parseHumanRefreshSessionResponse(body)
        const nextBinding = session
            ? readHumanSessionBinding(session.access_token)
            : null
        if (
            session === null ||
            nextBinding === null ||
            (
                previousBinding !== null &&
                (
                    nextBinding.subject !== previousBinding.subject ||
                    nextBinding.session_id !== previousBinding.session_id
                )
            )
        ) {
            remoteSignOutObserved = true
            clearAuthenticationState({ notifyPeers: false })
            throw new Error('登录状态刷新响应无效，请重新登录')
        }
        storeAuthSession(session, previousBinding !== null)
    })

const refreshStoredSession = async (): Promise<void> => {
    if (refreshSessionRequest) return refreshSessionRequest
    const request = performSessionRefresh()
    refreshSessionRequest = request
    try {
        await request
    } finally {
        if (refreshSessionRequest === request) {
            refreshSessionRequest = null
        }
    }
}

export const bootstrapHumanSession = async (): Promise<void> => {
    await refreshStoredSession()
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
    const token = readHumanAccessToken()
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
    writeHumanSessionMetadata('user', JSON.stringify(user))
    return user
}

export const logoutAllSessions = (): Promise<void> =>
    withHumanSessionLifecycleLock(async () => {
        const token = readHumanAccessToken()
        const binding = readHumanSessionBinding(token)
        if (!token || binding === null) {
            if (remoteSignOutObserved) {
                clearAuthenticationState({ notifyPeers: false })
                return
            }
            throw new Error('未找到有效登录会话，无法从所有设备退出')
        }
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
        clearAuthenticationState({ notifyPeers: 'all_devices' })
    })

export const logoutCurrentSession = (): Promise<void> =>
    withHumanSessionLifecycleLock(async () => {
        if (remoteSignOutObserved) {
            clearAuthenticationState({ notifyPeers: false })
            return
        }
        const token = readHumanAccessToken()
        const binding = readHumanSessionBinding(token)
        if (!token || binding === null) {
            throw new Error('未找到有效登录会话，无法安全退出')
        }
        const headers = new Headers({
            Authorization: `Bearer ${token}`,
            'X-Chronodesk-Session-ID': binding.session_id,
        })
        const response = await safeFetch(
            buildUrl(humanApiRoutes.deleteHumanSession()),
            {
                method: 'POST',
                credentials: 'include',
                headers,
            },
            '退出登录请求失败',
        )
        if (!response.ok) {
            const body: unknown = await response.json().catch(() => ({}))
            throw new Error(
                localizedApiErrorMessage(
                    body,
                    response.status,
                    '退出登录请求失败',
                ),
            )
        }
        clearAuthenticationState({ notifyPeers: 'current_session' })
    })

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

        await withHumanSessionLifecycleLock(async () => {
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
                    typeof rawMessage === 'string' &&
                    containsChineseText(rawMessage)
                        ? rawMessage
                        : typeof rawMessage === 'string' &&
                            /otp/i.test(rawMessage)
                          ? '动态验证码无效或已过期，请重新输入'
                          : typeof rawMessage === 'string' &&
                              /device/i.test(rawMessage)
                            ? '受信任设备凭据已失效，请重新验证'
                            : '登录失败，请检查邮箱、密码和验证码'
                throw new Error(message)
            }

            const session = parseHumanLoginSessionResponse(body)
            if (session === null) {
                throw new Error('登录响应包含无效的平台角色或会话信息')
            }
            storeAuthSession(session, false)
        })
    },

    logout: logoutCurrentSession,

    checkAuth: async () => {
        let committedToken = readCommittedHumanTabSessionToken()
        let binding = readHumanSessionBinding(committedToken)
        if (!committedToken || !binding || readStoredUser() === null) {
            if (isPublicHumanAuthRoute()) {
                throw new Error('当前页面无需登录')
            }
            await refreshStoredSession()
            committedToken = readCommittedHumanTabSessionToken()
            binding = readHumanSessionBinding(committedToken)
        }
        if (!committedToken || !binding || readStoredUser() === null) {
            clearAuthenticationState()
            throw new Error('登录状态已过期，请重新登录')
        }
        if (Date.now() >= binding.expires_at) {
            await refreshStoredSession()
            committedToken = readCommittedHumanTabSessionToken()
            binding = readHumanSessionBinding(committedToken)
        }
        if (!committedToken || !binding || readStoredUser() === null) {
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
