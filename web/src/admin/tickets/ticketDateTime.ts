export const normalizeTicketDateTimeForSubmit = (
    value: unknown,
): unknown => {
    if (value === null || typeof value === 'undefined') {
        return value
    }
    if (value instanceof Date) {
        return Number.isNaN(value.getTime()) ? value : value.toISOString()
    }
    if (typeof value !== 'string') {
        return value
    }

    const trimmed = value.trim()
    if (trimmed === '') {
        return null
    }
    const parsed = new Date(trimmed)
    return Number.isNaN(parsed.getTime()) ? value : parsed.toISOString()
}
