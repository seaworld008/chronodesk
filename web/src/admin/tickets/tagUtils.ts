export const MAX_TICKET_TAGS = 20
export const MAX_TICKET_TAG_LENGTH = 50

const rawTags = (value: unknown): string[] => {
    if (value == null) return []
    if (Array.isArray(value)) return value.map(String)
    if (typeof value !== 'string') return []
    const trimmed = value.trim()
    if (!trimmed) return []
    try {
        const parsed: unknown = JSON.parse(trimmed)
        if (Array.isArray(parsed)) return parsed.map(String)
    } catch {
        // One-release compatibility for records written by the old comma input.
    }
    return trimmed.split(',')
}

export const normalizeTagList = (value: unknown): string[] => {
    const seen = new Set<string>()
    const normalized: string[] = []
    for (const rawTag of rawTags(value)) {
        const tag = rawTag.trim()
        if (!tag) continue
        const key = tag.toLocaleLowerCase()
        if (seen.has(key)) continue
        seen.add(key)
        normalized.push(tag)
    }
    return normalized
}

export const validateTagsInput = (value: unknown): string | undefined => {
    const tags = normalizeTagList(value)
    if (tags.length > MAX_TICKET_TAGS) {
        return `标签最多 ${MAX_TICKET_TAGS} 个`
    }
    const tooLong = tags.find(
        (tag) => Array.from(tag).length > MAX_TICKET_TAG_LENGTH,
    )
    if (tooLong) {
        return `每个标签不能超过 ${MAX_TICKET_TAG_LENGTH} 个字符`
    }
    return undefined
}

export const normalizeTagsForSubmit = (value: unknown): string[] | undefined => {
    if (value == null) {
        return undefined
    }
    const error = validateTagsInput(value)
    if (error) throw new Error(error)
    return normalizeTagList(value)
}

export const parseTagsToArray = (value: unknown): string[] => {
    return normalizeTagList(value)
}

export const normalizeCustomFieldsForSubmit = (value: unknown): Record<string, unknown> | undefined => {
    if (value == null) {
        return undefined;
    }

    if (typeof value === 'string') {
        const trimmed = value.trim();
        if (!trimmed) {
            return {};
        }

        try {
            const parsed = JSON.parse(trimmed);
            if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
                return parsed as Record<string, unknown>;
            }
        } catch (error) {
            // ignore parse errors and fall back to empty object
        }

        return {};
    }

    if (typeof value === 'object' && !Array.isArray(value)) {
        return value as Record<string, unknown>;
    }

    return {};
};

export const formatCustomFieldsInputValue = (value: unknown): string => {
    if (value == null || value === '') {
        return '';
    }
    if (typeof value === 'string') {
        return value;
    }
    if (typeof value === 'object' && !Array.isArray(value)) {
        return JSON.stringify(value, null, 2);
    }
    return '';
};

export const validateCustomFieldsInput = (value: unknown): string | undefined => {
    if (value == null || value === '') {
        return undefined;
    }
    if (typeof value === 'object' && !Array.isArray(value)) {
        return undefined;
    }
    if (typeof value !== 'string') {
        return '扩展字段必须是 JSON 对象';
    }
    try {
        const parsed = JSON.parse(value);
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
            return '扩展字段必须是 JSON 对象';
        }
        return undefined;
    } catch {
        return '扩展字段不是有效的 JSON';
    }
};
