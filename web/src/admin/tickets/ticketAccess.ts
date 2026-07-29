export type TicketAccessRecord = {
    created_by_id?: number;
    assigned_to_id?: number | null;
};

export type TicketRolePermissions = {
    role?: string;
};

export const administrativeTicketRoles = new Set(['supervisor', 'admin', 'superuser']);

export const canDeleteTicket = (role?: string) =>
    administrativeTicketRoles.has(role ?? '');

export const canMutateTicket = (
    record: TicketAccessRecord | undefined,
    role: string | undefined,
    identityId: string | number | undefined,
) => {
    if (!record || typeof identityId === 'undefined') {
        return false;
    }
    if (canDeleteTicket(role)) {
        return true;
    }
    const userId = Number(identityId);
    if (role === 'agent') {
        return record.assigned_to_id == null || record.assigned_to_id === userId;
    }
    if (role === 'user' || role === 'customer') {
        return record.created_by_id === userId;
    }
    return false;
};
