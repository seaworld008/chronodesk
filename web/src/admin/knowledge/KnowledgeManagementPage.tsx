import {
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react'
import {
    Add as AddIcon,
    Article as ArticleIcon,
    Search as SearchIcon,
    Visibility as ViewIcon,
} from '@mui/icons-material'
import {
    Alert,
    Button,
    Card,
    CardActions,
    CardContent,
    Chip,
    CircularProgress,
    FormControl,
    InputLabel,
    MenuItem,
    Paper,
    Select,
    Stack,
    Tab,
    Tabs,
    TextField,
    Typography,
} from '@mui/material'
import { usePermissions } from 'react-admin'
import { useSearchParams } from 'react-router-dom'
import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import type { AccessPermissions } from '@/lib/accessControl'
import {
    parseProjectRole,
    resolveActiveProjectKey,
} from '@/lib/projectScope'
import { KnowledgeArticleTable } from './KnowledgeArticleTable'
import { KnowledgeCreateDialog } from './KnowledgeCreateDialog'
import { KnowledgeDocumentDrawer } from './KnowledgeDocumentDrawer'
import { KnowledgeVersionsDrawer } from './KnowledgeVersionsDrawer'
import {
    listKnowledgeArticles,
    searchKnowledge,
} from './knowledgeApi'
import type {
    CreateKnowledgeDraftResult,
    KnowledgeArticle,
    KnowledgeArticlePage,
    KnowledgeArticleStatus,
    KnowledgeCitation,
    KnowledgeSearchResult,
    KnowledgeTab,
} from './types'

const validPage = (value: string | null) => {
    const parsed = Number(value)
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : 1
}

const validPageSize = (value: string | null) => {
    const parsed = Number(value)
    return parsed === 25 || parsed === 50 || parsed === 100 ? parsed : 25
}

const positiveTicketID = (value: string | null) => {
    const parsed = Number(value)
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined
}

const requestedTab = (value: string | null): KnowledgeTab =>
    value === 'search' || value === 'mine' || value === 'manage'
        ? value
        : 'browse'

const requestedStatus = (
    value: string | null,
): KnowledgeArticleStatus | '' =>
    value === 'active' || value === 'archived' ? value : ''

const articleFromCitation = (citation: KnowledgeCitation): KnowledgeArticle => ({
    id: citation.article_id,
    key: citation.article_key ?? citation.article_id,
    title: citation.article_title ?? `知识片段 #${citation.rank}`,
    summary: citation.snippet,
    status: 'active',
    current_version_id: citation.version_id,
    revision: citation.document_version,
    created_at: '',
    updated_at: '',
})

const KnowledgeManagementPage = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const role = parseProjectRole(permissions?.project_role)
    const canManage = role === 'project_admin' || role === 'manager'
    const canCreateDraft =
        canManage || permissions?.can_create_knowledge_drafts === true
    const [searchParams, setSearchParams] = useSearchParams()
    const pendingSearchParams = useRef(searchParams)
    const desiredTab = requestedTab(searchParams.get('tab'))
    const tab: KnowledgeTab =
        (desiredTab === 'manage' && !canManage)
        || (desiredTab === 'mine' && !canCreateDraft)
            ? 'browse'
            : desiredTab
    const page = validPage(searchParams.get('page'))
    const pageSize = validPageSize(searchParams.get('page_size'))
    const status = requestedStatus(searchParams.get('status'))
    const rawQuery = searchParams.get('q') ?? ''
    const query = rawQuery.slice(0, tab === 'search' ? 2000 : 200)
    const sourceTicketID = positiveTicketID(
        searchParams.get('source_ticket_id'),
    )
    const createRequested = searchParams.get('create') === '1'

    const [projectKey, setProjectKey] = useState('')
    const [queryInput, setQueryInput] = useState(query)
    const [articles, setArticles] =
        useState<KnowledgeArticlePage | null>(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState('')
    const directoryController = useRef<AbortController | null>(null)
    const directorySequence = useRef(0)

    useEffect(() => {
        pendingSearchParams.current = searchParams
    }, [searchParams])

    const [searchResult, setSearchResult] =
        useState<KnowledgeSearchResult | null>(null)
    const [searching, setSearching] = useState(false)
    const [searchError, setSearchError] = useState('')
    const searchController = useRef<AbortController | null>(null)

    const [createOpen, setCreateOpen] = useState(false)
    const [draftArticle, setDraftArticle] =
        useState<KnowledgeArticle | undefined>()
    const [documentArticle, setDocumentArticle] =
        useState<KnowledgeArticle | null>(null)
    const [documentVersionID, setDocumentVersionID] = useState<string>()
    const [documentPreferLatestDraft, setDocumentPreferLatestDraft] =
        useState(false)
    const [versionsArticle, setVersionsArticle] =
        useState<KnowledgeArticle | null>(null)

    const updateQuery = useCallback((
        updates: Partial<{
            tab: KnowledgeTab
            page: number
            pageSize: number
            status: string
            query: string
        }>,
    ) => {
        const next = new URLSearchParams(pendingSearchParams.current)
        if (updates.tab) next.set('tab', updates.tab)
        if (updates.page) next.set('page', String(updates.page))
        if (updates.pageSize) {
            next.set('page_size', String(updates.pageSize))
        }
        if (updates.status !== undefined) {
            if (updates.status) next.set('status', updates.status)
            else next.delete('status')
        }
        if (updates.query !== undefined) {
            if (updates.query) next.set('q', updates.query)
            else next.delete('q')
        }
        pendingSearchParams.current = next
        setSearchParams(next, { replace: true })
    }, [setSearchParams])

    const closeCreate = useCallback(() => {
        setCreateOpen(false)
        setDraftArticle(undefined)
        const next = new URLSearchParams(pendingSearchParams.current)
        next.delete('create')
        next.delete('source_ticket_id')
        pendingSearchParams.current = next
        setSearchParams(next, { replace: true })
    }, [setSearchParams])

    useEffect(() => {
        setQueryInput(query)
    }, [query])

    useEffect(() => {
        if (tab === 'search') return
        const timer = window.setTimeout(() => {
            const normalized = queryInput.trim()
            if (normalized !== query) {
                updateQuery({ query: normalized, page: 1 })
            }
        }, 300)
        return () => window.clearTimeout(timer)
    }, [query, queryInput, tab, updateQuery])

    useEffect(() => {
        if (
            !permissionsPending
            && canCreateDraft
            && createRequested
            && projectKey
        ) {
            setCreateOpen(true)
        }
    }, [
        canCreateDraft,
        createRequested,
        permissionsPending,
        projectKey,
    ])

    const loadArticles = useCallback(async () => {
        if (tab === 'search') return
        directoryController.current?.abort()
        const controller = new AbortController()
        const sequence = directorySequence.current + 1
        directorySequence.current = sequence
        directoryController.current = controller
        setLoading(true)
        setError('')
        try {
            const currentProject = await resolveActiveProjectKey()
            const result = await listKnowledgeArticles(currentProject, {
                page,
                pageSize,
                status: tab === 'manage' ? status || undefined : undefined,
                keyword: query || undefined,
                view: tab === 'manage'
                    ? 'manage'
                    : tab === 'mine'
                        ? 'mine'
                        : undefined,
                signal: controller.signal,
            })
            if (
                controller.signal.aborted
                || directorySequence.current !== sequence
            ) return
            setProjectKey(currentProject)
            setArticles({ ...result, items: result.items ?? [] })
        } catch (loadError) {
            if (
                controller.signal.aborted
                || directorySequence.current !== sequence
            ) return
            setError(localizedUnknownErrorMessage(
                loadError,
                '知识文章加载失败，请稍后重试',
            ))
        } finally {
            if (
                !controller.signal.aborted
                && directorySequence.current === sequence
            ) setLoading(false)
        }
    }, [page, pageSize, query, status, tab])

    useEffect(() => {
        void loadArticles()
        return () => directoryController.current?.abort()
    }, [loadArticles])

    useEffect(() => () => {
        directoryController.current?.abort()
        searchController.current?.abort()
        directorySequence.current += 1
    }, [])

    const runSearch = async () => {
        const normalized = queryInput.trim()
        if (!normalized) return
        searchController.current?.abort()
        const controller = new AbortController()
        searchController.current = controller
        setSearching(true)
        setSearchError('')
        updateQuery({ query: normalized })
        try {
            const currentProject =
                projectKey || await resolveActiveProjectKey()
            const result = await searchKnowledge(
                currentProject,
                normalized,
                controller.signal,
            )
            if (!controller.signal.aborted) {
                setProjectKey(currentProject)
                setSearchResult({
                    ...result,
                    items: result.items ?? [],
                })
            }
        } catch (requestError) {
            if (controller.signal.aborted) return
            setSearchError(localizedUnknownErrorMessage(
                requestError,
                '知识搜索失败，请稍后重试',
            ))
        } finally {
            if (!controller.signal.aborted) setSearching(false)
        }
    }

    const openDocument = (
        article: KnowledgeArticle,
        versionID?: string,
        preferLatestDraft = false,
    ) => {
        setDocumentVersionID(versionID)
        setDocumentPreferLatestDraft(preferLatestDraft)
        setDocumentArticle(article)
    }

    const handleDraftSaved = (
        result: CreateKnowledgeDraftResult,
        published: boolean,
    ) => {
        setArticles((current) => current ? {
            ...current,
            items: [
                result.article,
                ...current.items.filter((item) =>
                    item.id !== result.article.id),
            ].slice(0, current.page_size),
            total: Math.max(current.total, current.items.length + 1),
        } : current)
        if (published) {
            openDocument(result.article, result.version.id)
        } else if (!canManage) {
            updateQuery({
                tab: 'mine',
                page: 1,
                status: '',
            })
            return
        }
        void loadArticles()
    }

    return (
        <PageShell title="知识库" testId="knowledge-library-page">
            <Stack spacing={2}>
                <PageHeader
                    title="知识库"
                    description={
                        projectKey
                            ? `当前项目：${projectKey} · 浏览已授权知识，使用可信来源解决问题。`
                            : '浏览已授权知识，使用可信来源解决问题。'
                    }
                    action={canCreateDraft ? (
                        <Button
                            variant="contained"
                            startIcon={<AddIcon />}
                            disabled={!projectKey}
                            onClick={() => {
                                setDraftArticle(undefined)
                                setCreateOpen(true)
                            }}
                        >
                            沉淀知识
                        </Button>
                    ) : undefined}
                />

                {!permissionsPending && createRequested && !canCreateDraft && (
                    <Alert severity="info">
                        当前成员可以浏览知识，但尚未获得知识草稿贡献授权。
                    </Alert>
                )}

                <Paper>
                    <Tabs
                        value={tab}
                        onChange={(_, value: KnowledgeTab) =>
                            updateQuery({
                                tab: value,
                                page: 1,
                                status: '',
                            })}
                        aria-label="知识库功能"
                    >
                        <Tab value="browse" label="浏览" />
                        <Tab value="search" label="搜索" />
                        {canCreateDraft && (
                            <Tab value="mine" label="我维护的知识" />
                        )}
                        {canManage && <Tab value="manage" label="管理" />}
                    </Tabs>
                </Paper>

                {tab === 'search' ? (
                    <Stack spacing={2}>
                        <Paper
                            component="form"
                            onSubmit={(event) => {
                                event.preventDefault()
                                void runSearch()
                            }}
                            sx={{ p: 2 }}
                        >
                            <Stack
                                direction={{ xs: 'column', sm: 'row' }}
                                spacing={1.5}
                            >
                                <TextField
                                    fullWidth
                                    label="搜索知识正文"
                                    placeholder="描述现象、错误信息或希望解决的问题"
                                    value={queryInput}
                                    onChange={(event) =>
                                        setQueryInput(event.target.value)}
                                    slotProps={{
                                        htmlInput: {
                                            maxLength: 2000,
                                            'aria-label': '搜索知识正文',
                                        },
                                    }}
                                />
                                <Button
                                    type="submit"
                                    variant="contained"
                                    startIcon={<SearchIcon />}
                                    disabled={
                                        searching || !queryInput.trim()
                                    }
                                >
                                    {searching ? '搜索中…' : '搜索'}
                                </Button>
                            </Stack>
                        </Paper>
                        {searchError && (
                            <Alert
                                severity="error"
                                action={
                                    <Button
                                        color="inherit"
                                        onClick={() => void runSearch()}
                                    >
                                        重试
                                    </Button>
                                }
                            >
                                {searchError}
                            </Alert>
                        )}
                        {searching ? (
                            <Stack
                                role="status"
                                aria-label="正在搜索知识"
                                sx={{ alignItems: 'center', py: 6 }}
                            >
                                <CircularProgress size={32} />
                            </Stack>
                        ) : searchResult === null ? (
                            <Paper sx={{ p: 5, textAlign: 'center' }}>
                                <SearchIcon color="disabled" />
                                <Typography color="text.secondary">
                                    输入问题后搜索已发布且有权访问的知识。
                                </Typography>
                            </Paper>
                        ) : searchResult.items.length === 0 ? (
                            <Alert severity="info">
                                没有找到匹配知识。可以调整关键词后重试。
                            </Alert>
                        ) : (
                            <Stack
                                component="section"
                                aria-label="知识搜索结果"
                                spacing={1.5}
                            >
                                {searchResult.items.map((citation) => (
                                    <Card key={citation.id} variant="outlined">
                                        <CardContent>
                                            <Stack spacing={1}>
                                                <Stack
                                                    direction="row"
                                                    spacing={1}
                                                    sx={{
                                                        alignItems: 'center',
                                                        flexWrap: 'wrap',
                                                    }}
                                                >
                                                    <ArticleIcon color="action" />
                                                    <Typography
                                                        component="h3"
                                                        variant="subtitle1"
                                                        sx={{ fontWeight: 600 }}
                                                    >
                                                        {citation.article_title
                                                            ?? `知识片段 #${citation.rank}`}
                                                    </Typography>
                                                    {citation.section_path && (
                                                        <Chip
                                                            size="small"
                                                            label={citation.section_path}
                                                        />
                                                    )}
                                                </Stack>
                                                <Typography
                                                    sx={{ whiteSpace: 'pre-wrap' }}
                                                >
                                                    {citation.snippet}
                                                </Typography>
                                                <Typography
                                                    variant="caption"
                                                    color="text.secondary"
                                                >
                                                    文档版本 v{citation.document_version}
                                                    {citation.page_number
                                                        ? ` · 第 ${citation.page_number} 页`
                                                        : ''}
                                                </Typography>
                                            </Stack>
                                        </CardContent>
                                        <CardActions>
                                            <Button
                                                startIcon={<ViewIcon />}
                                                onClick={() =>
                                                    openDocument(
                                                        articleFromCitation(citation),
                                                        citation.version_id,
                                                    )}
                                            >
                                                查看正文
                                            </Button>
                                        </CardActions>
                                    </Card>
                                ))}
                            </Stack>
                        )}
                    </Stack>
                ) : (
                    <Stack spacing={2}>
                        <Paper sx={{ p: 2 }}>
                            <Stack
                                direction={{ xs: 'column', sm: 'row' }}
                                spacing={2}
                            >
                                <TextField
                                    label="筛选文章"
                                    placeholder="按 Key、标题或摘要"
                                    value={queryInput}
                                    onChange={(event) =>
                                        setQueryInput(event.target.value)}
                                    slotProps={{
                                        htmlInput: {
                                            maxLength: 200,
                                            'aria-label': '筛选知识文章',
                                        },
                                    }}
                                    sx={{ minWidth: { sm: 320 } }}
                                />
                                {tab === 'manage' && (
                                    <FormControl sx={{ minWidth: 180 }}>
                                        <InputLabel id="knowledge-status-filter">
                                            状态
                                        </InputLabel>
                                        <Select
                                            labelId="knowledge-status-filter"
                                            label="状态"
                                            value={status}
                                            onChange={(event) =>
                                                updateQuery({
                                                    status: event.target.value,
                                                    page: 1,
                                                })}
                                        >
                                            <MenuItem value="">全部状态</MenuItem>
                                            <MenuItem value="active">启用</MenuItem>
                                            <MenuItem value="archived">
                                                已归档
                                            </MenuItem>
                                        </Select>
                                    </FormControl>
                                )}
                            </Stack>
                        </Paper>
                        {error && (
                            <Alert
                                severity="error"
                                action={
                                    <Button
                                        color="inherit"
                                        onClick={() => void loadArticles()}
                                    >
                                        重试
                                    </Button>
                                }
                            >
                                {error}
                            </Alert>
                        )}
                        {loading && !articles ? (
                            <Stack
                                role="status"
                                aria-label="正在加载知识文章"
                                sx={{ alignItems: 'center', py: 8 }}
                            >
                                <CircularProgress size={32} />
                            </Stack>
                        ) : (
                            <Paper>
                                <KnowledgeArticleTable
                                    result={articles}
                                    page={page}
                                    pageSize={pageSize}
                                    canCreateVersion={
                                        (tab === 'manage' && canManage)
                                        || (tab === 'mine' && canCreateDraft)
                                    }
                                    canViewVersions={
                                        tab === 'manage' && canManage
                                    }
                                    allowDraftView={tab === 'mine'}
                                    showDraftActivity={tab === 'mine'}
                                    onPageChange={(nextPage) =>
                                        updateQuery({ page: nextPage })}
                                    onPageSizeChange={(nextSize) =>
                                        updateQuery({
                                            page: 1,
                                            pageSize: nextSize,
                                        })}
                                    onView={(article) =>
                                        openDocument(
                                            article,
                                            undefined,
                                            tab === 'mine',
                                        )}
                                    onNewVersion={(article) => {
                                        setDraftArticle(article)
                                        setCreateOpen(true)
                                    }}
                                    onVersions={setVersionsArticle}
                                />
                            </Paper>
                        )}
                    </Stack>
                )}
            </Stack>

            <KnowledgeCreateDialog
                open={createOpen}
                projectKey={projectKey}
                article={draftArticle}
                canPublish={canManage}
                sourceTicketID={sourceTicketID}
                onClose={closeCreate}
                onSaved={handleDraftSaved}
            />
            <KnowledgeDocumentDrawer
                open={documentArticle !== null}
                projectKey={projectKey}
                article={documentArticle}
                versionID={documentVersionID}
                preferLatestDraft={documentPreferLatestDraft}
                onClose={() => {
                    setDocumentArticle(null)
                    setDocumentVersionID(undefined)
                    setDocumentPreferLatestDraft(false)
                }}
            />
            <KnowledgeVersionsDrawer
                open={versionsArticle !== null}
                projectKey={projectKey}
                article={versionsArticle}
                onClose={() => setVersionsArticle(null)}
                onViewDocument={(article, versionID) => {
                    setVersionsArticle(null)
                    openDocument(article, versionID, false)
                }}
                onChanged={() => void loadArticles()}
            />
        </PageShell>
    )
}

export default KnowledgeManagementPage
