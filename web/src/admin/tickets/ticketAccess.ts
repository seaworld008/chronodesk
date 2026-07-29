import {
    isAdministrativeRole,
    isAgentRole,
    isCustomerRole,
    type RolePermissions,
} from '@/lib/accessControl';

export type TicketAccessRecord = {
    created_by_id?: number;
    assigned_to_id?: number | null;
};

export type TicketRolePermissions = RolePermissions;

export const canDeleteTicket = (role?: string | null) =>
    isAdministrativeRole(role);

export const canMutateTicket = (
    record: TicketAccessRecord | undefined,
    role: string | null | undefined,
    identityId: string | number | undefined,
) => {
    if (!record || typeof identityId === 'undefined') {
        return false;
    }
    if (canDeleteTicket(role)) {
        return true;
    }
    const userId = Number(identityId);
    if (isAgentRole(role)) {
        return record.assigned_to_id == null || record.assigned_to_id === userId;
    }
    if (isCustomerRole(role)) {
        return record.created_by_id === userId;
    }
    return false;
};
