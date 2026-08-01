import { useCallback, useEffect, useRef, useState } from 'react'
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
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TablePagination,
    TableRow,
    Typography,
} from '@mui/material'
import {
    Close as CloseIcon,
    Publish as PublishIcon,
    Visibility as ViewIcon,
} from '@mui/icons-material'
import { useNotify } from 'react-admin'
import {
    ResizableMuiTable,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
    listKnowledgeVersions,
    publishKnowledgeVersion,
} from './knowledgeApi'
import { KnowledgePublishConfirmDialog } from './KnowledgePublishConfirmDialog'
import type {
    KnowledgeArticle,
    KnowledgeVersion,
    KnowledgeVersionPage,
} from './types'

const columns: ResizableColumn[] = [
    { key: 'version', defaultWidth: 96, minWidth: 80, maxWidth: 144 },
    { key: 'title', defaultWidth: 260, minWidth: 180, maxWidth: 520 },
    { key: 'source', defaultWidth: 240, minWidth: 160, maxWidth: 480 },
    { key: 'author_type', defaultWidth: 128, minWidth: 112, maxWidth: 180 },
    { key: 'status', defaultWidth: 128, minWidth: 104, maxWidth: 180 },
    { key: 'created_at', defaultWidth: 180, minWidth: 144, maxWidth: 260 },
    { key: 'actions', defaultWidth: 200, minWidth: 176, maxWidth: 260 },
]

const statusLabel: Record<string, string> = {
    draft: '草稿',
    published: '已发布',
    superseded: '已替代',
    quarantined: '已隔离',
}

const authorTypeLabel: Record<KnowledgeVersion['created_by_type'], string> = {
    human: '人工维护',
    service_principal: 'Agent 草稿',
    system: '系统生成',
}

export const KnowledgeVersionsDrawer = ({
    open,
    projectKey,
    article,
    onClose,
    onViewDocument,
    onChanged,
}: {
    open: boolean
    projectKey: string
    article: KnowledgeArticle | null
    onClose: () => void
    onViewDocument: (article: KnowledgeArticle, versionID: string) => void
    onChanged: () => void
}) => {
    const notify = useNotify()
    const [page, setPage] = useState(0)
    const [pageSize, setPageSize] = useState(25)
    const [result, setResult] = useState<KnowledgeVersionPage | null>(null)
    const [loading, setLoading] = useState(false)
    const [error, setError] = useState('')
    const [publishingID, setPublishingID] = useState('')
    const [publishCandidate, setPublishCandidate] =
        useState<KnowledgeVersion | null>(null)
    const controller = useRef<AbortController | null>(null)

    const load = useCallback(async (
        nextPage: number,
        nextPageSize: number,
    ) => {
        if (!open || !article || !projectKey) return
        controller.current?.abort()
        const request = new AbortController()
        controller.current = request
        setLoading(true)
        setError('')
        try {
            const response = await listKnowledgeVersions(
                projectKey,
                article.id,
                {
                    page: nextPage + 1,
                    pageSize: nextPageSize,
                    signal: request.signal,
                },
            )
            if (!request.signal.aborted) {
                setResult({ ...response, items: response.items ?? [] })
            }
        } catch (loadError) {
            if (request.signal.aborted) return
            setError(localizedUnknownErrorMessage(
                loadError,
                '知识版本加载失败，请稍后重试',
            ))
        } finally {
            if (!request.signal.aborted) setLoading(false)
        }
    }, [article, open, projectKey])

    useEffect(() => {
        if (!open) return
        setPage(0)
        setResult(null)
        void load(0, pageSize)
        return () => controller.current?.abort()
    }, [article?.id, load, open, pageSize, projectKey])

    const publish = async (version: KnowledgeVersion) => {
        setPublishingID(version.id)
        try {
            await publishKnowledgeVersion(projectKey, version.id)
            notify('知识版本已发布', { type: 'success' })
            await load(page, pageSize)
            onChanged()
        } catch (publishError) {
            notify(localizedUnknownErrorMessage(
                publishError,
                '知识版本发布失败；草稿已保留',
            ), { type: 'error' })
        } finally {
            setPublishingID('')
            setPublishCandidate(null)
        }
    }

    return (
        <Drawer
            anchor="right"
            open={open}
            onClose={onClose}
            aria-labelledby="knowledge-versions-title"
            slotProps={{
                paper: {
                    sx: {
                        boxSizing: 'border-box',
                        width: { xs: '100%', lg: 'min(980px, 92vw)' },
                    },
                },
            }}
        >
            <Stack
                direction="row"
                spacing={2}
                sx={{
                    alignItems: 'flex-start',
                    justifyContent: 'space-between',
                    p: 2.5,
                }}
            >
                <Box>
                    <Typography
                        id="knowledge-versions-title"
                        component="h2"
                        variant="h6"
                    >
                        {article?.title ?? '知识文章'} · 版本管理
                    </Typography>
                    <Typography variant="body2" color="text.secondary">
                        草稿保存与发布彼此独立；发布版本不可直接修改。
                    </Typography>
                </Box>
                <IconButton aria-label="关闭版本管理" onClick={onClose}>
                    <CloseIcon />
                </IconButton>
            </Stack>
            <Divider />
            {error && (
                <Alert
                    severity="error"
                    sx={{ m: 2 }}
                    action={
                        <Button
                            color="inherit"
                            onClick={() => void load(page, pageSize)}
                        >
                            重试
                        </Button>
                    }
                >
                    {error}
                </Alert>
            )}
            {loading && !result ? (
                <Stack
                    role="status"
                    aria-label="正在加载知识版本"
                    sx={{ alignItems: 'center', py: 8 }}
                >
                    <CircularProgress size={32} />
                </Stack>
            ) : (
                <Box sx={{ p: 2 }}>
                    <TableContainer>
                        <ResizableMuiTable
                            tableId="knowledge.article-versions"
                            columns={columns}
                            size="small"
                            aria-label="知识文章版本列表"
                        >
                            <TableHead>
                                <TableRow>
                                    <TableCell>版本</TableCell>
                                    <TableCell>标题</TableCell>
                                    <TableCell>内容来源</TableCell>
                                    <TableCell>创建方式</TableCell>
                                    <TableCell>状态</TableCell>
                                    <TableCell>创建时间</TableCell>
                                    <TableCell>操作</TableCell>
                                </TableRow>
                            </TableHead>
                            <TableBody>
                                {(result?.items ?? []).map((version) => (
                                    <TableRow key={version.id} hover>
                                        <TableCell>v{version.version}</TableCell>
                                        <TableCell>
                                            <TruncatedText title={version.title}>
                                                {version.title}
                                            </TruncatedText>
                                        </TableCell>
                                        <TableCell>
                                            <TruncatedText
                                                title={
                                                    version.original_file_name
                                                    ?? 'Markdown 正文'
                                                }
                                            >
                                                {version.original_file_name
                                                    ?? 'Markdown 正文'}
                                            </TruncatedText>
                                        </TableCell>
                                        <TableCell>
                                            <Chip
                                                size="small"
                                                variant="outlined"
                                                color={
                                                    version.created_by_type
                                                    === 'service_principal'
                                                        ? 'info'
                                                        : 'default'
                                                }
                                                label={
                                                    authorTypeLabel[
                                                        version.created_by_type
                                                    ]
                                                }
                                            />
                                        </TableCell>
                                        <TableCell>
                                            <Chip
                                                size="small"
                                                color={
                                                    version.status === 'published'
                                                        ? 'success'
                                                        : version.status === 'draft'
                                                          ? 'warning'
                                                          : 'default'
                                                }
                                                variant="outlined"
                                                label={
                                                    statusLabel[version.status]
                                                    ?? version.status
                                                }
                                            />
                                        </TableCell>
                                        <TableCell>
                                            {new Date(
                                                version.created_at,
                                            ).toLocaleString('zh-CN')}
                                        </TableCell>
                                        <TableCell>
                                            <Stack direction="row" spacing={1}>
                                                <Button
                                                    size="small"
                                                    startIcon={<ViewIcon />}
                                                    onClick={() => {
                                                        if (article) {
                                                            onViewDocument(
                                                                article,
                                                                version.id,
                                                            )
                                                        }
                                                    }}
                                                >
                                                    查看
                                                </Button>
                                                {version.status === 'draft' && (
                                                    <Button
                                                        size="small"
                                                        startIcon={<PublishIcon />}
                                                        disabled={
                                                            publishingID
                                                            === version.id
                                                        }
                                                        onClick={() =>
                                                            setPublishCandidate(
                                                                version,
                                                            )}
                                                    >
                                                        发布
                                                    </Button>
                                                )}
                                            </Stack>
                                        </TableCell>
                                    </TableRow>
                                ))}
                                {(result?.items.length ?? 0) === 0 && (
                                    <TableRow>
                                        <TableCell
                                            colSpan={7}
                                            align="center"
                                            sx={{ py: 7 }}
                                        >
                                            暂无知识版本
                                        </TableCell>
                                    </TableRow>
                                )}
                            </TableBody>
                        </ResizableMuiTable>
                    </TableContainer>
                    <TablePagination
                        component="div"
                        count={result?.total ?? 0}
                        page={page}
                        rowsPerPage={pageSize}
                        rowsPerPageOptions={[25, 50, 100]}
                        onPageChange={(_, nextPage) => {
                            setPage(nextPage)
                            void load(nextPage, pageSize)
                        }}
                        onRowsPerPageChange={(event) => {
                            const nextSize = Number(event.target.value)
                            setPage(0)
                            setPageSize(nextSize)
                        }}
                        labelRowsPerPage="每页记录数"
                        labelDisplayedRows={({ from, to, count }) =>
                            `${from}–${to} / ${count}`}
                    />
                </Box>
            )}
            <KnowledgePublishConfirmDialog
                open={publishCandidate !== null}
                title={publishCandidate?.title ?? article?.title ?? ''}
                busy={Boolean(publishingID)}
                onCancel={() => setPublishCandidate(null)}
                onConfirm={() => {
                    if (publishCandidate) {
                        void publish(publishCandidate)
                    }
                }}
            />
        </Drawer>
    )
}
