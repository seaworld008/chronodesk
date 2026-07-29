import { AuthProvider, UserIdentity } from 'react-admin'
import { containsChineseText, localizedApiErrorMessage } from './apiClient'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

const buildUrl = (path: string) => `${apiBase}${path.startsWith('/') ? path : `/${path}`}`

const trustedDeviceTokenKey = 'trustedDeviceToken'
const rememberDevicePreferenceKey = 'rememberDevicePreference'
const administrativeRoles = new Set(['supervisor', 'admin', 'superuser'])

type LoginParams = {
    username: string
    password: string
    remember?: boolean
    rememberDevice?: boolean
    otpCode?: string
    deviceName?: string
}

/**
 * 工单管理系统认证提供器
 * 完美适配Go JWT认证体系
 */
export const authProvider: AuthProvider = {
    // 登录处理
    login: async (params) => {
        const { username, password, remember, rememberDevice, otpCode, deviceName } = params as LoginParams

        const payload: Record<string, unknown> = {
            email: username,
            password,
        }

        const deviceToken = localStorage.getItem(trustedDeviceTokenKey)
        if (deviceToken) {
            payload.device_token = deviceToken
        }

        const shouldRememberDevice = Boolean(rememberDevice ?? remember)
        if (shouldRememberDevice) {
            payload.remember_device = true
            if (deviceName) {
                payload.device_name = deviceName
            }
        }

        if (otpCode) {
            payload.otp_code = otpCode
        }

        localStorage.setItem(rememberDevicePreferenceKey, shouldRememberDevice ? 'true' : 'false')

        try {
            const response = await fetch(buildUrl('/auth/login'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload),
            })

            const result = await response.json().catch(() => ({}))

            if (!response.ok || result.code === 1) {
                const rawMessage =
                    typeof result.msg === 'string'
                        ? result.msg
                        : typeof result.message === 'string'
                            ? result.message
                            : ''
                if (/otp/i.test(rawMessage) || /device/i.test(rawMessage)) {
                    localStorage.removeItem(trustedDeviceTokenKey)
                }
                const message = containsChineseText(rawMessage)
                    ? rawMessage
                    : /otp/i.test(rawMessage)
                        ? '动态验证码无效或已过期，请重新输入'
                        : /device/i.test(rawMessage)
                            ? '受信任设备凭据已失效，请重新验证'
                            : '登录失败，请检查邮箱、密码和验证码'
                throw new Error(message)
            }

            const data = result.data ?? {}

            if (!data.access_token) {
                throw new Error('响应格式不正确，缺少 access_token')
            }

            localStorage.setItem('token', data.access_token)
            if (data.refresh_token) {
                localStorage.setItem('refreshToken', data.refresh_token)
            }
            if (data.user) {
                localStorage.setItem('user', JSON.stringify(data.user))
            }
            if (data.permissions) {
                localStorage.setItem('permissions', JSON.stringify(data.permissions))
            }
            if (data.expires_in) {
                const expiresAt = Date.now() + data.expires_in * 1000
                localStorage.setItem('tokenExpiresAt', expiresAt.toString())
            }

            if (data.trusted_device_token && shouldRememberDevice) {
                localStorage.setItem(trustedDeviceTokenKey, data.trusted_device_token)
            }

            if (!shouldRememberDevice) {
                localStorage.removeItem(trustedDeviceTokenKey)
            }

            return Promise.resolve()
        } catch (error) {
            console.error('登录失败：', error)
            throw error
        }
    },

    // 注销处理
    logout: async () => {
        try {
            const token = localStorage.getItem('token')
            const refreshToken = localStorage.getItem('refreshToken')
            if (token) {
                // 调用后端注销API
                await fetch(buildUrl('/auth/logout'), {
                    method: 'POST',
                    headers: {
                        Authorization: `Bearer ${token}`,
                        'Content-Type': 'application/json',
                    },
                    body: refreshToken
                        ? JSON.stringify({ refresh_token: refreshToken })
                        : undefined,
                })
            }
        } catch (error) {
            console.error('退出登录接口调用失败：', error)
            // 即使API调用失败，也要清理本地存储
        } finally {
            // 清理所有认证相关的本地存储
            localStorage.removeItem('token')
            localStorage.removeItem('refreshToken')
            localStorage.removeItem('user')
            localStorage.removeItem('permissions')
            localStorage.removeItem('tokenExpiresAt')
            localStorage.removeItem(trustedDeviceTokenKey)
        }
        return Promise.resolve()
    },

    // 检查认证状态
    checkAuth: async (params) => {
        const token = localStorage.getItem('token')
        const tokenExpiresAt = localStorage.getItem('tokenExpiresAt')

        if (!token) {
            return Promise.reject({ message: '未找到登录令牌，请重新登录' })
        }

        // 检查token是否过期
        if (tokenExpiresAt && Date.now() > parseInt(tokenExpiresAt)) {
            // 尝试刷新token
            const refreshToken = localStorage.getItem('refreshToken')
            if (refreshToken) {
                try {
                    const response = await fetch(buildUrl('/auth/refresh'), {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                        },
                        body: JSON.stringify({ refresh_token: refreshToken }),
                    })

                    if (response.ok) {
                        const auth = await response.json()
                        if (auth.data && auth.data.access_token) {
                            localStorage.setItem('token', auth.data.access_token)
                            if (auth.data.refresh_token) {
                                localStorage.setItem('refreshToken', auth.data.refresh_token)
                            }
                            if (auth.data.expires_in) {
                                const expiresAt = Date.now() + auth.data.expires_in * 1000
                                localStorage.setItem('tokenExpiresAt', expiresAt.toString())
                            }
                            return Promise.resolve()
                        }
                    }
                } catch (error) {
                    console.error('登录令牌刷新失败：', error)
                }
            }

            // 如果刷新失败，清理存储并拒绝
            localStorage.removeItem('token')
            localStorage.removeItem('refreshToken')
            localStorage.removeItem('user')
            localStorage.removeItem('permissions')
            localStorage.removeItem('tokenExpiresAt')
            return Promise.reject({ message: '登录状态已过期，请重新登录' })
        }

        // 对于特定路由的权限检查
        if (params?.resource && params?.resource.startsWith('admin/')) {
            const user = JSON.parse(localStorage.getItem('user') || '{}')
            if (!administrativeRoles.has(user.role)) {
                return Promise.reject({ message: '当前账号没有管理权限' })
            }
        }

        return Promise.resolve()
    },

    // 检查错误：只有 401 表示当前登录态失效。
    // 403 是已认证用户缺少某项权限，不能因此清除会话，否则一次正常的
    // 对象级授权拒绝就会把用户强制登出。
    checkError: async (error) => {
        const status = error.status
        if (status === 401) {
            // 清理认证信息
            localStorage.removeItem('token')
            localStorage.removeItem('refreshToken')
            localStorage.removeItem('user')
            localStorage.removeItem('permissions')
            localStorage.removeItem('tokenExpiresAt')
            localStorage.removeItem(trustedDeviceTokenKey)
            return Promise.reject({ message: '身份认证失败，请重新登录' })
        }
        // 403 及其他业务错误保留当前登录态，由调用页面展示拒绝原因。
        return Promise.resolve()
    },

    // 获取当前用户身份
    getIdentity: async (): Promise<UserIdentity> => {
        try {
            const token = localStorage.getItem('token')
            if (!token) {
                throw new Error('未找到登录令牌，请重新登录')
            }

            // 优先从本地存储获取用户信息
            const storedUser = localStorage.getItem('user')
            if (storedUser) {
                const user = JSON.parse(storedUser)
                return Promise.resolve({
                    id: user.id,
                    fullName: user.first_name && user.last_name 
                        ? `${user.first_name} ${user.last_name}`
                        : user.username || user.email,
                    avatar: user.avatar || undefined,
                    email: user.email,
                });
            }

            // 如果本地没有，从API获取
            const response = await fetch(buildUrl('/auth/me'), {
                headers: new Headers({
                    Authorization: `Bearer ${token}`,
                }),
            })

            if (!response.ok) {
                throw new Error('获取当前用户身份失败')
            }

            const result = await response.json()
            const user = result.data || result

            // 更新本地存储
            localStorage.setItem('user', JSON.stringify(user))

            return {
                id: user.id,
                fullName: user.first_name && user.last_name 
                    ? `${user.first_name} ${user.last_name}`
                    : user.username || user.email,
                avatar: user.avatar || undefined,
                email: user.email,
            };
        } catch (error) {
            console.error('获取当前用户身份失败：', error);
            return Promise.reject(error);
        }
    },

    // 获取用户权限
    getPermissions: async () => {
        try {
            const user = JSON.parse(localStorage.getItem('user') || '{}');
            const permissions = JSON.parse(localStorage.getItem('permissions') || '[]');

            return Promise.resolve({
                role: user.role || 'user',
                permissions,
            });
        } catch (error) {
            console.error('获取用户权限失败：', error);
            return Promise.resolve({ role: 'user', permissions: [] });
        }
    },

    // React Admin 5 会在资源路由和操作按钮渲染前调用顶层
    // canAccess。对象级判断仍由后端执行，这里同步隐藏不可执行入口，
    // 避免普通账号看到管理按钮后再收到 403。
    canAccess: async ({ resource, action, record }) => {
        const user = JSON.parse(localStorage.getItem('user') || '{}');
        const role = user.role || 'user';
        if (administrativeRoles.has(role)) {
            return true;
        }

        if (resource === 'tickets') {
            if (['list', 'show', 'create', 'read'].includes(action)) {
                return true;
            }
            if (action === 'delete') {
                return false;
            }
            if (action === 'edit') {
                // EditBase 会先做一次不含 record 的资源级检查；加载记录后，
                // 按钮和页面内守卫再执行下面的对象级判断。
                if (!record) {
                    return true;
                }
                const userID = Number(user.id);
                if (role === 'agent') {
                    return record.assigned_to_id == null || record.assigned_to_id === userID;
                }
                if (role === 'user' || role === 'customer') {
                    return record.created_by_id === userID;
                }
            }
            return false;
        }

        if (resource === 'notifications') {
            return ['list', 'show', 'edit', 'read'].includes(action);
        }

        return false;
    },

    // 忘记密码
    forgotPassword: async ({ email }: { email: string }) => {
        const request = new Request(buildUrl('/auth/forgot-password'), {
            method: 'POST',
            body: JSON.stringify({ email }),
            headers: new Headers({ 'Content-Type': 'application/json' }),
        });

        const response = await fetch(request);
        if (response.status < 200 || response.status >= 300) {
            const error = await response.json().catch(() => null);
            throw new Error(localizedApiErrorMessage(error, response.status, '发送密码重置请求失败'));
        }

        return Promise.resolve();
    },

    // 重置密码
    resetPassword: async ({ token, password }: { token: string; password: string }) => {
        const request = new Request(buildUrl('/auth/reset-password'), {
            method: 'POST',
            body: JSON.stringify({ token, new_password: password }),
            headers: new Headers({ 'Content-Type': 'application/json' }),
        });

        const response = await fetch(request);
        if (response.status < 200 || response.status >= 300) {
            const error = await response.json().catch(() => null);
            throw new Error(localizedApiErrorMessage(error, response.status, '重置密码失败'));
        }

        return Promise.resolve();
    },
};

export default authProvider;
