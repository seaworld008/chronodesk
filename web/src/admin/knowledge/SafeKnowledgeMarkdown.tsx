import { Children, createElement, type ReactNode } from 'react'
import { Box } from '@mui/material'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { uniqueHeadingID } from './knowledgeMarkdown'

const ALLOWED_ELEMENTS = [
    'a',
    'blockquote',
    'br',
    'code',
    'del',
    'em',
    'h1',
    'h2',
    'h3',
    'h4',
    'hr',
    'li',
    'ol',
    'p',
    'pre',
    'strong',
    'table',
    'tbody',
    'td',
    'th',
    'thead',
    'tr',
    'ul',
]

const safeLink = (url: string) => {
    const trimmed = url.trim()
    if (trimmed.startsWith('#')) return trimmed
    try {
        const parsed = new URL(trimmed)
        return ['http:', 'https:', 'mailto:'].includes(parsed.protocol)
            ? trimmed
            : ''
    } catch {
        return ''
    }
}

const textFromChildren = (children: ReactNode) =>
    Children.toArray(children).join('').trim()

export const SafeKnowledgeMarkdown = ({
    markdown,
    compact = false,
}: {
    markdown: string
    compact?: boolean
}) => {
    const occurrences = new Map<string, number>()
    let headingIndex = 0
    const heading = (level: 1 | 2 | 3 | 4) =>
        ({ children }: { children?: ReactNode }) => {
            const title = textFromChildren(children)
            const id = uniqueHeadingID(title, occurrences, headingIndex)
            headingIndex += 1
            return createElement(`h${level}`, { id, tabIndex: -1 }, children)
        }

    return (
        <Box
            data-testid="safe-knowledge-markdown"
            sx={{
                overflowWrap: 'anywhere',
                '& h1': { fontSize: compact ? '1.25rem' : '1.75rem', mt: 0 },
                '& h2': { fontSize: compact ? '1.1rem' : '1.35rem', mt: 3 },
                '& h3, & h4': { fontSize: '1rem', mt: 2.5 },
                '& p': { lineHeight: 1.75 },
                '& pre': {
                    overflowX: 'auto',
                    p: 1.5,
                    bgcolor: 'action.hover',
                },
                '& code': { fontFamily: 'monospace' },
                '& table': {
                    borderCollapse: 'collapse',
                    width: '100%',
                },
                '& th, & td': {
                    border: 1,
                    borderColor: 'divider',
                    p: 1,
                    textAlign: 'left',
                },
            }}
        >
            <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                skipHtml
                allowedElements={ALLOWED_ELEMENTS}
                urlTransform={safeLink}
                components={{
                    h1: heading(1),
                    h2: heading(2),
                    h3: heading(3),
                    h4: heading(4),
                    a: ({ children, href, title }) => (
                        <a
                            href={href}
                            title={title}
                            target="_blank"
                            rel="noopener noreferrer nofollow"
                        >
                            {children}
                        </a>
                    ),
                }}
            >
                {markdown}
            </ReactMarkdown>
        </Box>
    )
}
