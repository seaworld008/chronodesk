import { useCallback, useEffect, useMemo, useState } from 'react'
import {
    Alert,
    Box,
    Button,
    Chip,
    CircularProgress,
    Divider,
    Drawer,
    IconButton,
    Stack,
    Typography,
} from '@mui/material'
import {
    AttachFile as AttachmentIcon,
    Close as CloseIcon,
    ConfirmationNumber as TicketIcon,
    LinkOff as UnavailableIcon,
    LockOutlined as RestrictedIcon,
} from '@mui/icons-material'
import { Link } from 'react-router-dom'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import { getKnowledgeDocument } from './knowledgeApi'
import { extractMarkdownHeadings } from './knowledgeMarkdown'
import { SafeKnowledgeMarkdown } from './SafeKnowledgeMarkdown'
import type {
    KnowledgeArticle,
    KnowledgeDocument,
    KnowledgeSource,
} from './types'

const sourceKey = (source: KnowledgeSource) =>
    `${source.kind}:${source.ordinal}`

export const KnowledgeDocumentDrawer = ({
    open,
    projectKey,
    article,
    versionID,
    preferLatestDraft = false,
    onClose,
}: {
    open: boolean
    projectKey: string
    article: KnowledgeArticle | null
    versionID?: string
    preferLatestDraft?: boolean
    onClose: () => void
}) => {
    const [documentData, setDocumentData] =
        useState<KnowledgeDocument | null>(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState('')
    const [reloadToken, setReloadToken] = useState(0)

    useEffect(() => {
        if (!open || !article || !projectKey) return
        if (
            !versionID
            && !preferLatestDraft
            && !article.current_version_id
        ) {
            setDocumentData(null)
            setError('这篇文章还没有已发布版本')
            setLoading(false)
            return
        }
        const controller = new AbortController()
        setLoading(true)
        setError('')
        setDocumentData(null)
        void getKnowledgeDocument(
            projectKey,
            article.id,
            {
                versionID,
                preferLatestDraft,
                signal: controller.signal,
            },
        ).then((result) => {
            if (!controller.signal.aborted) setDocumentData(result)
        }).catch((loadError) => {
            if (controller.signal.aborted) return
            setError(localizedUnknownErrorMessage(
                loadError,
                '知识正文加载失败，请稍后重试',
            ))
        }).finally(() => {
            if (!controller.signal.aborted) setLoading(false)
        })
        return () => controller.abort()
    }, [
        article,
        open,
        preferLatestDraft,
        projectKey,
        reloadToken,
        versionID,
    ])

    const markdown = documentData?.markdown
        || documentData?.sections.map((section) => section.markdown).join('\n\n')
        || ''
    const headings = useMemo(
        () => extractMarkdownHeadings(markdown),
        [markdown],
    )
    const sources = useMemo(() => {
        const values = documentData?.sources ?? []
        return [...new Map(values.map((source) => [
            sourceKey(source),
            source,
        ])).values()]
    }, [documentData?.sources])
    const ticketSources = useMemo(
        () => [...new Map(sources
            .filter((source) =>
                source.visibility === 'full'
                && source.source_ticket_id !== undefined)
            .map((source) => [
                source.source_ticket_id,
                source,
            ])).values()],
        [sources],
    )
    const scrollToHeading = useCallback((id: string) => {
        const target = window.document.getElementById(id)
        target?.scrollIntoView({ behavior: 'smooth', block: 'start' })
        target?.focus({ preventScroll: true })
    }, [])

    return (
        <Drawer
            anchor="right"
            open={open}
            onClose={onClose}
            slotProps={{
                paper: {
                    'aria-labelledby': 'knowledge-document-title',
                    sx: {
                        width: { xs: '100%', lg: 'min(1120px, 92vw)' },
                    },
                },
            }}
        >
            <Stack sx={{ height: '100%' }}>
                <Stack
                    direction="row"
                    spacing={2}
                    sx={{
                        alignItems: 'flex-start',
                        justifyContent: 'space-between',
                        p: 2.5,
                    }}
                >
                    <Box sx={{ minWidth: 0 }}>
                        <Typography
                            id="knowledge-document-title"
                            component="h2"
                            variant="h6"
                        >
                            {documentData?.article.title ?? article?.title ?? '知识正文'}
                        </Typography>
                        <Typography variant="body2" color="text.secondary">
                            {documentData
                                ? `${documentData.article.key} · v${documentData.version.version}`
                                : article?.key}
                        </Typography>
                    </Box>
                    <IconButton
                        aria-label="关闭知识正文"
                        onClick={onClose}
                    >
                        <CloseIcon />
                    </IconButton>
                </Stack>
                <Divider />

                {loading ? (
                    <Stack
                        role="status"
                        aria-label="正在加载知识正文"
                        spacing={1}
                        sx={{ alignItems: 'center', py: 8 }}
                    >
                        <CircularProgress size={32} />
                        <Typography color="text.secondary">
                            正在加载正文…
                        </Typography>
                    </Stack>
                ) : error ? (
                    <Alert
                        severity="warning"
                        sx={{ m: 2.5 }}
                        action={
                            article?.current_version_id
                            || versionID
                            || preferLatestDraft ? (
                                <Button
                                    color="inherit"
                                    onClick={() => setReloadToken((value) => value + 1)}
                                >
                                    重试
                                </Button>
                            ) : undefined
                        }
                    >
                        {error}
                    </Alert>
                ) : documentData ? (
                    <Box
                        sx={{
                            display: 'grid',
                            gridTemplateColumns: {
                                xs: '1fr',
                                md: '240px minmax(0, 1fr)',
                            },
                            minHeight: 0,
                            flex: 1,
                        }}
                    >
                        <Box
                            component="nav"
                            aria-label="知识正文目录"
                            sx={{
                                borderRight: { md: 1 },
                                borderBottom: { xs: 1, md: 0 },
                                borderColor: 'divider',
                                p: 2,
                                overflowY: 'auto',
                            }}
                        >
                            <Typography
                                variant="overline"
                                color="text.secondary"
                            >
                                本文目录
                            </Typography>
                            {headings.length === 0 ? (
                                <Typography variant="body2" color="text.secondary">
                                    正文暂无分节标题
                                </Typography>
                            ) : (
                                <Stack spacing={0.5}>
                                    {headings.map((heading) => (
                                        <Button
                                            key={heading.id}
                                            size="small"
                                            color="inherit"
                                            onClick={() => scrollToHeading(heading.id)}
                                            sx={{
                                                justifyContent: 'flex-start',
                                                pl: Math.min(
                                                    1 + (heading.level - 1) * 1.25,
                                                    5,
                                                ),
                                                textAlign: 'left',
                                            }}
                                        >
                                            {heading.title}
                                        </Button>
                                    ))}
                                </Stack>
                            )}
                        </Box>
                        <Box sx={{ p: { xs: 2, md: 3 }, overflowY: 'auto' }}>
                            {sources.length > 0 && (
                                <Box
                                    component="section"
                                    aria-label="知识来源"
                                    sx={{ mb: 3 }}
                                >
                                    <Typography
                                        variant="subtitle2"
                                        sx={{ mb: 1 }}
                                    >
                                        来源
                                    </Typography>
                                    <Stack
                                        direction="row"
                                        spacing={1}
                                        useFlexGap
                                        sx={{ flexWrap: 'wrap' }}
                                    >
                                        {ticketSources.map((source) => (
                                            <Chip
                                                key={`ticket-${source.source_ticket_id}`}
                                                icon={<TicketIcon />}
                                                label={[
                                                    source.ticket_number,
                                                    source.ticket_title,
                                                ].filter(Boolean).join(' ')
                                                    || source.reference_label}
                                                component={Link}
                                                clickable
                                                to={`/tickets/${source.source_ticket_id}/show`}
                                            />
                                        ))}
                                        {sources
                                            .filter((source) =>
                                                source.visibility === 'full' &&
                                                source.source_attachment_id
                                                && source.attachment_name)
                                            .map((source) => (
                                                <Chip
                                                    key={`attachment-${sourceKey(source)}`}
                                                    icon={<AttachmentIcon />}
                                                    variant="outlined"
                                                    label={`附件：${source.attachment_name}`}
                                                />
                                            ))}
                                        {sources
                                            .filter((source) =>
                                                source.visibility !== 'full')
                                            .map((source) => (
                                                <Chip
                                                    key={`protected-${sourceKey(source)}`}
                                                    icon={
                                                        source.visibility === 'restricted'
                                                            ? <RestrictedIcon />
                                                            : <UnavailableIcon />
                                                    }
                                                    color={
                                                        source.visibility === 'restricted'
                                                            ? 'warning'
                                                            : 'default'
                                                    }
                                                    variant="outlined"
                                                    label={source.reference_label}
                                                    title={
                                                        source.visibility === 'restricted'
                                                            ? '当前账号无权查看该来源详情'
                                                            : '来源已删除、不可用或不再满足安全要求'
                                                    }
                                                />
                                            ))}
                                    </Stack>
                                </Box>
                            )}
                            <SafeKnowledgeMarkdown markdown={markdown} />
                        </Box>
                    </Box>
                ) : null}
            </Stack>
        </Drawer>
    )
}
