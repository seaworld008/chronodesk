export type MarkdownHeading = {
    id: string
    title: string
    level: number
}

export const KNOWLEDGE_MARKDOWN_MAX_BYTES = 128 * 1024

export const knowledgeMarkdownByteLength = (markdown: string) =>
    new TextEncoder().encode(markdown).byteLength

const baseHeadingID = (title: string, fallbackIndex: number) => {
    const normalized = title
        .normalize('NFKC')
        .trim()
        .toLowerCase()
        .replace(/[^\p{L}\p{N}\s_-]/gu, '')
        .replace(/[\s_]+/gu, '-')
        .replace(/^-+|-+$/gu, '')
    return normalized || `section-${fallbackIndex + 1}`
}

export const uniqueHeadingID = (
    title: string,
    occurrences: Map<string, number>,
    fallbackIndex = 0,
) => {
    const base = baseHeadingID(title, fallbackIndex)
    const count = occurrences.get(base) ?? 0
    occurrences.set(base, count + 1)
    return count === 0 ? base : `${base}-${count + 1}`
}

export const extractMarkdownHeadings = (markdown: string): MarkdownHeading[] => {
    const headings: MarkdownHeading[] = []
    const occurrences = new Map<string, number>()
    let fenced = false
    let fenceMarker = ''
    for (const line of markdown.split(/\r?\n/u)) {
        const fence = line.match(/^\s*(`{3,}|~{3,})/u)?.[1]
        if (fence) {
            if (!fenced) {
                fenced = true
                fenceMarker = fence[0]
            } else if (fence[0] === fenceMarker) {
                fenced = false
                fenceMarker = ''
            }
            continue
        }
        if (fenced) continue
        const match = line.match(/^\s*(#{1,4})\s+(.+?)\s*#*\s*$/u)
        if (!match) continue
        const title = match[2].trim()
        headings.push({
            id: uniqueHeadingID(title, occurrences, headings.length),
            title,
            level: match[1].length,
        })
    }
    return headings
}
