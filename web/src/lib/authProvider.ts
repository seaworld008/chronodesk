import { AuthProvider, UserIdentity } from 'react-admin'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').replace(/\/$/, '')

const buildUrl = (path: string) => `${apiBase}${path.startsWith('/') ? path : `/${path}`}`

const trustedDeviceTokenKey = 'trustedDeviceToken'
const rememberDevicePreferenceKey = 'rememberDevicePreference'

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
                const message = result.msg || result.message || '登录失败'
                if (/otp/i.test(message) || /device/i.test(message)) {
                    localStorage.removeItem(trustedDeviceTokenKey)
                }
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
            console.error('Login error:', error)
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
            console.error('Logout API call failed:', error)
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
            return Promise.reject({ message: 'No token found' })
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
                    console.error('Token refresh failed:', error)
                }
            }

            // 如果刷新失败，清理存储并拒绝
            localStorage.removeItem('token')
            localStorage.removeItem('refreshToken')
            localStorage.removeItem('user')
            localStorage.removeItem('permissions')
            localStorage.removeItem('tokenExpiresAt')
            return Promise.reject({ message: 'Token expired' })
        }

        // 对于特定路由的权限检查
        if (params?.resource && params?.resource.startsWith('admin/')) {
            const user = JSON.parse(localStorage.getItem('user') || '{}')
            if (user.role !== 'admin') {
                return Promise.reject({ message: 'Admin access required' })
            }
        }

        return Promise.resolve()
    },

    // 检查错误（处理401/403响应）
    checkError: async (error) => {
        const status = error.status
        if (status === 401 || status === 403) {
            // 清理认证信息
            localStorage.removeItem('token')
            localStorage.removeItem('refreshToken')
            localStorage.removeItem('user')
            localStorage.removeItem('permissions')
            localStorage.removeItem('tokenExpiresAt')
            localStorage.removeItem(trustedDeviceTokenKey)
            return Promise.reject({ message: 'Authentication failed' })
        }
        // 对于其他错误，不做处理
        return Promise.resolve()
    },

    // 获取当前用户身份
    getIdentity: async (): Promise<UserIdentity> => {
        try {
            const token = localStorage.getItem('token')
            if (!token) {
                throw new Error('No token found')
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
                throw new Error('Failed to fetch user identity')
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
            console.error('Get identity error:', error);
            return Promise.reject(error);
        }
    },

    // 获取用户权限
    getPermissions: async () => {
        try {
            const user = JSON.parse(localStorage.getItem('user') || '{}');
            const permissions = JSON.parse(localStorage.getItem('permissions') || '[]');
            
            // 根据用户角色返回权限
            const userPermissions = {
                role: user.role || 'user',
                permissions: permissions,
                canAccess: (resource: string, action: string) => {
                    if (user.role === 'admin') {
                        return true; // 管理员拥有所有权限
                    }
                    
                    // 普通用户权限检查
                    if (resource === 'tickets') {
                        return ['list', 'show', 'create', 'edit'].includes(action);
                    }
                    
                    if (resource === 'users' && action !== 'list') {
                        return false; // 普通用户不能管理其他用户
                    }
                    
                    return true;
                },
            };

            return Promise.resolve(userPermissions);
        } catch (error) {
            console.error('Get permissions error:', error);
            return Promise.resolve({ role: 'user', permissions: [] });
        }
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
            const error = await response.json();
            throw new Error(error.message || 'Password reset request failed');
        }

        return Promise.resolve();
    },

    // 重置密码
    resetPassword: async ({ token, password }: { token: string; password: string }) => {
        const request = new Request(buildUrl('/auth/reset-password'), {
            method: 'POST',
            body: JSON.stringify({ token, password }),
            headers: new Headers({ 'Content-Type': 'application/json' }),
        });

        const response = await fetch(request);
        if (response.status < 200 || response.status >= 300) {
            const error = await response.json();
            throw new Error(error.message || 'Password reset failed');
        }

        return Promise.resolve();
    },
};

export default authProvider;
