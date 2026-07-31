import type { AccessPermissions } from '@/lib/accessControl'
import {
    hasProjectCapability,
    parseProjectRole,
    type ProjectCapability,
    type ProjectRole,
} from '@/lib/projectScope'

export type TicketAccessRecord = {
    created_by_id?: number
    assigned_to_id?: number | null
}

export type TicketRolePermissions = AccessPermissions

const validIdentityID = (
    identityId: string | number | undefined,
): number | null => {
    if (typeof identityId === 'undefined') return null
    const userID = Number(identityId)
    return Number.isSafeInteger(userID) && userID > 0 ? userID : null
}

const roleCanActOnRecord = (
    record: TicketAccessRecord | undefined,
    role: ProjectRole,
    identityId: string | number | undefined,
): boolean => {
    if (!record) return false
    if (role === 'project_admin' || role === 'manager') return true
    const userID = validIdentityID(identityId)
    if (userID === null) return false
    if (role === 'agent') {
        return (
            record.assigned_to_id == null ||
            record.assigned_to_id === userID
        )
    }
    if (role === 'requester') {
        return record.created_by_id === userID
    }
    return false
}

const canPerformTicketCapability = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
    capability: ProjectCapability,
): boolean => {
    const projectRole = parseProjectRole(role)
    return (
        projectRole !== null &&
        hasProjectCapability(projectRole, capability) &&
        roleCanActOnRecord(record, projectRole, identityId)
    )
}

export const canDeleteTicket = (role: unknown): boolean =>
    hasProjectCapability(role, 'delete_ticket')

export const canEditTicket = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
): boolean =>
    canPerformTicketCapability(
        record,
        role,
        identityId,
        'edit_ticket_safe_fields',
    )

export const canUseTicketWorkflow = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
): boolean =>
    canPerformTicketCapability(
        record,
        role,
        identityId,
        'manage_ticket_workflow',
    )

export const canAssignTicket = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
): boolean =>
    canPerformTicketCapability(
        record,
        role,
        identityId,
        'assign_ticket',
    )

export const canWritePublicTicketContent = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
): boolean =>
    canPerformTicketCapability(
        record,
        role,
        identityId,
        'write_public_content',
    )

export const canWriteInternalTicketContent = (
    record: TicketAccessRecord | undefined,
    role: unknown,
    identityId: string | number | undefined,
): boolean =>
    canPerformTicketCapability(
        record,
        role,
        identityId,
        'write_internal_content',
    )
