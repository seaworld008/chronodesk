const userRoles = [
    'admin',
    'supervisor',
    'agent',
    'customer',
] as const

export type UserRole = (typeof userRoles)[number]

export type RolePermissions = {
    role?: string | null
}

const knownRoles = new Set<string>(userRoles)
const administrativeRoles = new Set<UserRole>([
    'admin',
    'supervisor',
])

export const normalizeUserRole = (role: unknown): UserRole | null => {
    const normalized = typeof role === 'string' ? role.trim().toLowerCase() : ''
    return knownRoles.has(normalized) ? (normalized as UserRole) : null
}

export const isAdministrativeRole = (role: unknown) => {
    const normalized = normalizeUserRole(role)
    return normalized !== null && administrativeRoles.has(normalized)
}

export const isAgentRole = (role: unknown) => normalizeUserRole(role) === 'agent'

export const isCustomerRole = (role: unknown) => {
    return normalizeUserRole(role) === 'customer'
}

const userRoleLabels: Record<UserRole, string> = {
    admin: '管理员',
    supervisor: '主管',
    agent: '客服代理',
    customer: '客户',
}

export const getUserRoleLabel = (role: unknown) => {
    const normalized = normalizeUserRole(role)
    if (normalized === null) {
        return '未知角色'
    }
    return userRoleLabels[normalized]
}

export const userRoleChoices = userRoles.map((role) => ({
    id: role,
    name: userRoleLabels[role],
}))

const assignableRoles = ['customer', 'agent', 'supervisor', 'admin'] as const

export const assignableUserRoleChoices = assignableRoles.map((role) => ({
    id: role,
    name: userRoleLabels[role],
}))
