import type {
    PlatformRole,
    ProjectRole,
} from '@/lib/generated/human-api'
import { platformRoleValues } from '@/lib/generated/human-api'
/*
 * Keep this as a runtime import from the generated contract. The parser and
 * choices must never grow a second handwritten role registry.
 */

export type { PlatformRole, ProjectRole }
export { platformRoleValues }

const knownPlatformRoles = new Set<string>(platformRoleValues)

export const parsePlatformRole = (value: unknown): PlatformRole | null =>
    typeof value === 'string' && knownPlatformRoles.has(value)
        ? (value as PlatformRole)
        : null

export type PlatformCapability =
    | 'manage_platform_users'
    | 'manage_platform_settings'
    | 'manage_email_settings'
    | 'view_platform_audit'
    | 'operate_emergency_controls'

const platformCapabilities: Record<
    PlatformRole,
    ReadonlySet<PlatformCapability>
> = {
    platform_admin: new Set([
        'manage_platform_users',
        'manage_platform_settings',
        'manage_email_settings',
        'view_platform_audit',
    ]),
    security_auditor: new Set(['view_platform_audit']),
    emergency_operator: new Set(['operate_emergency_controls']),
    member: new Set(),
}

export const hasPlatformCapability = (
    role: unknown,
    capability: PlatformCapability,
): boolean => {
    const parsed = parsePlatformRole(role)
    return parsed !== null && platformCapabilities[parsed].has(capability)
}

const platformRoleLabels: Record<PlatformRole, string> = {
    platform_admin: '平台管理员',
    security_auditor: '安全审计员',
    emergency_operator: '紧急运维员',
    member: '普通成员',
}

export const getPlatformRoleLabel = (role: unknown): string => {
    const parsed = parsePlatformRole(role)
    return parsed === null ? '未知平台角色' : platformRoleLabels[parsed]
}

export const platformRoleChoices = platformRoleValues.map((role) => ({
    id: role,
    name: platformRoleLabels[role],
}))

export type AccessPermissions = {
    platform_role: PlatformRole
    project_role: ProjectRole | null
    project_key: string | null
}
