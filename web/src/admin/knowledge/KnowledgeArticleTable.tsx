import {
    Box,
    Button,
    Chip,
    Stack,
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TablePagination,
    TableRow,
} from '@mui/material'
import {
    NoteAdd as NewVersionIcon,
    History as VersionsIcon,
    Visibility as ViewIcon,
} from '@mui/icons-material'
import {
    ResizableMuiTable,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import type { KnowledgeArticle, KnowledgeArticlePage } from './types'

const columns: ResizableColumn[] = [
    { key: 'key', defaultWidth: 190, minWidth: 136, maxWidth: 340 },
    { key: 'title', defaultWidth: 260, minWidth: 180, maxWidth: 480 },
    { key: 'summary', defaultWidth: 380, minWidth: 220, maxWidth: 720 },
    { key: 'status', defaultWidth: 112, minWidth: 96, maxWidth: 160 },
    { key: 'updated_at', defaultWidth: 180, minWidth: 144, maxWidth: 260 },
    { key: 'actions', defaultWidth: 320, minWidth: 280, maxWidth: 400 },
]

export const KnowledgeArticleTable = ({
    result,
    page,
    pageSize,
    canCreateVersion,
    canViewVersions,
    allowDraftView,
    showDraftActivity,
    onPageChange,
    onPageSizeChange,
    onView,
    onNewVersion,
    onVersions,
}: {
    result: KnowledgeArticlePage | null
    page: number
    pageSize: number
    canCreateVersion: boolean
    canViewVersions: boolean
    allowDraftView: boolean
    showDraftActivity: boolean
    onPageChange: (page: number) => void
    onPageSizeChange: (pageSize: number) => void
    onView: (article: KnowledgeArticle) => void
    onNewVersion: (article: KnowledgeArticle) => void
    onVersions: (article: KnowledgeArticle) => void
}) => (
    <Box>
        <TableContainer>
            <ResizableMuiTable
                tableId="knowledge.articles"
                columns={columns}
                size="small"
                aria-label="知识文章列表"
            >
                <TableHead>
                    <TableRow>
                        <TableCell>Key</TableCell>
                        <TableCell>标题</TableCell>
                        <TableCell>摘要</TableCell>
                        <TableCell>状态</TableCell>
                        <TableCell>
                            {showDraftActivity
                                ? '最新草稿活动'
                                : '更新时间'}
                        </TableCell>
                        <TableCell>操作</TableCell>
                    </TableRow>
                </TableHead>
                <TableBody>
                    {(result?.items ?? []).map((article) => {
                        const archived = article.status === 'archived'
                        const unavailableReason = archived
                            ? '已归档文章暂不支持查看或创建新版本'
                            : undefined
                        return (
                        <TableRow key={article.id} hover>
                            <TableCell>
                                <TruncatedText title={article.key}>
                                    {article.key}
                                </TruncatedText>
                            </TableCell>
                            <TableCell>
                                <TruncatedText title={article.title}>
                                    {article.title}
                                </TruncatedText>
                            </TableCell>
                            <TableCell>
                                <TruncatedText title={article.summary || '—'}>
                                    {article.summary || '—'}
                                </TruncatedText>
                            </TableCell>
                            <TableCell>
                                <Stack direction="row" spacing={0.75}>
                                    <Chip
                                        size="small"
                                        color={
                                            article.status === 'active'
                                                ? 'success'
                                                : 'default'
                                        }
                                        variant="outlined"
                                        label={
                                            article.status === 'active'
                                                ? '启用'
                                                : '已归档'
                                        }
                                    />
                                    {article.has_unpublished_draft && (
                                        <Chip
                                            size="small"
                                            color="warning"
                                            variant="outlined"
                                            label="待复核"
                                            aria-label={
                                                article.latest_draft_version
                                                    ? `待复核草稿版本 ${article.latest_draft_version}`
                                                    : '有待复核草稿'
                                            }
                                        />
                                    )}
                                </Stack>
                            </TableCell>
                            <TableCell>
                                {showDraftActivity
                                    ? article.latest_draft_at
                                        ? new Date(
                                            article.latest_draft_at,
                                        ).toLocaleString('zh-CN')
                                        : '—'
                                    : new Date(
                                        article.updated_at,
                                    ).toLocaleString('zh-CN')}
                            </TableCell>
                            <TableCell>
                                <Stack direction="row" spacing={1}>
                                    <Box
                                        component="span"
                                        title={unavailableReason}
                                    >
                                        <Button
                                            size="small"
                                            startIcon={<ViewIcon />}
                                            disabled={
                                                archived ||
                                                (!article.current_version_id
                                                    && !allowDraftView)
                                            }
                                            onClick={() => onView(article)}
                                        >
                                            查看
                                        </Button>
                                    </Box>
                                    {canCreateVersion && (
                                        <Box
                                            component="span"
                                            title={unavailableReason}
                                        >
                                            <Button
                                                size="small"
                                                startIcon={<NewVersionIcon />}
                                                disabled={archived}
                                                onClick={() =>
                                                    onNewVersion(article)}
                                            >
                                                新版本
                                            </Button>
                                        </Box>
                                    )}
                                    {canViewVersions && (
                                        <Box
                                            component="span"
                                            title={unavailableReason}
                                        >
                                            <Button
                                                size="small"
                                                startIcon={<VersionsIcon />}
                                                disabled={archived}
                                                onClick={() =>
                                                    onVersions(article)}
                                            >
                                                版本
                                            </Button>
                                        </Box>
                                    )}
                                </Stack>
                            </TableCell>
                        </TableRow>
                        )
                    })}
                    {(result?.items.length ?? 0) === 0 && (
                        <TableRow>
                            <TableCell
                                colSpan={6}
                                align="center"
                                sx={{ py: 7 }}
                            >
                                暂无可见知识文章
                            </TableCell>
                        </TableRow>
                    )}
                </TableBody>
            </ResizableMuiTable>
        </TableContainer>
        <TablePagination
            component="div"
            count={result?.total ?? 0}
            page={page - 1}
            rowsPerPage={pageSize}
            rowsPerPageOptions={[25, 50, 100]}
            onPageChange={(_, nextPage) => onPageChange(nextPage + 1)}
            onRowsPerPageChange={(event) =>
                onPageSizeChange(Number(event.target.value))}
            labelRowsPerPage="每页记录数"
            labelDisplayedRows={({ from, to, count }) =>
                `${from}–${to} / ${count}`}
            showFirstButton
            showLastButton
        />
    </Box>
)
