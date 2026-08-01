import {
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react'
import {
    Alert,
    Autocomplete,
    Button,
    Checkbox,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Stack,
    Tab,
    Tabs,
    TextField,
    Typography,
} from '@mui/material'
import {
    ConfirmationNumber as TicketIcon,
    Publish as PublishIcon,
    Save as SaveIcon,
} from '@mui/icons-material'
import { Link } from 'react-router-dom'
import { useNotify } from 'react-admin'
import { localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
    createKnowledgeArticleDraft,
    createKnowledgeDraft,
    listKnowledgeSourceAttachments,
    publishKnowledgeVersion,
    searchKnowledgeSourceTickets,
} from './knowledgeApi'
import {
    KNOWLEDGE_MARKDOWN_MAX_BYTES,
    knowledgeMarkdownByteLength,
} from './knowledgeMarkdown'
import { SafeKnowledgeMarkdown } from './SafeKnowledgeMarkdown'
import { KnowledgePublishConfirmDialog } from './KnowledgePublishConfirmDialog'
import type {
    CreateKnowledgeDraftResult,
    KnowledgeArticle,
} from './types'
import type {
    Ticket,
    TicketAttachment,
} from '@/lib/generated/human-api'

export const KNOWLEDGE_MARKDOWN_TEMPLATE = `## 现象

描述用户看到的现象、错误信息和影响范围。

## 适用范围

说明适用的系统、版本、环境或前置条件。

## 原因

记录已经验证的根因；如果仍不确定，请明确标注。

## 解决步骤

1. 写出可重复执行的操作步骤。
2. 标明关键参数和预期结果。

## 验证

说明如何确认问题已经解决，以及需要观察多长时间。

## 避免复发

记录监控、自动化、流程或配置方面的预防措施。
`

const KNOWLEDGE_SOURCE_ATTACHMENT_PAGE_SIZE = 25
const KNOWLEDGE_SOURCE_ATTACHMENT_MAX_SELECTED = 20

const uniqueAttachments = (
    attachments: TicketAttachment[],
): TicketAttachment[] => {
    const seen = new Set<number>()
    return attachments.filter((attachment) => {
        if (seen.has(attachment.id)) return false
        seen.add(attachment.id)
        return true
    })
}

export const KnowledgeCreateDialog = ({
    open,
    projectKey,
    article,
    canPublish,
    sourceTicketID,
    onClose,
    onSaved,
}: {
    open: boolean
    projectKey: string
    article?: KnowledgeArticle
    canPublish: boolean
    sourceTicketID?: number
    onClose: () => void
    onSaved: (result: CreateKnowledgeDraftResult, published: boolean) => void
}) => {
    const notify = useNotify()
    const [key, setKey] = useState('')
    const [title, setTitle] = useState('')
    const [summary, setSummary] = useState('')
    const [sourceTicket, setSourceTicket] = useState<Ticket | null>(null)
    const [sourceTicketInput, setSourceTicketInput] = useState('')
    const [sourceTicketOptions, setSourceTicketOptions] =
        useState<Ticket[]>([])
    const [sourceTicketLoading, setSourceTicketLoading] = useState(false)
    const [sourceTicketError, setSourceTicketError] = useState('')
    const [sourceAttachments, setSourceAttachments] =
        useState<TicketAttachment[]>([])
    const [sourceAttachmentsPage, setSourceAttachmentsPage] = useState(1)
    const [sourceAttachmentsTotal, setSourceAttachmentsTotal] = useState(0)
    const [sourceAttachmentsTotalPages, setSourceAttachmentsTotalPages] =
        useState(0)
    const [selectedSourceAttachments, setSelectedSourceAttachments] =
        useState<TicketAttachment[]>([])
    const [sourceAttachmentsLoading, setSourceAttachmentsLoading] =
        useState(false)
    const [sourceAttachmentsError, setSourceAttachmentsError] = useState('')
    const [
        sourceAttachmentSelectionError,
        setSourceAttachmentSelectionError,
    ] = useState('')
    const [markdown, setMarkdown] = useState(KNOWLEDGE_MARKDOWN_TEMPLATE)
    const [editorTab, setEditorTab] = useState<'edit' | 'preview'>('edit')
    const [saving, setSaving] = useState(false)
    const [publishing, setPublishing] = useState(false)
    const [publishConfirmOpen, setPublishConfirmOpen] = useState(false)
    const [error, setError] = useState('')
    const [draft, setDraft] = useState<CreateKnowledgeDraftResult | null>(null)
    const sourceTicketRequest = useRef<AbortController | null>(null)
    const sourceRequest = useRef<AbortController | null>(null)
    const markdownBytes = knowledgeMarkdownByteLength(markdown)
    const markdownTooLarge =
        markdownBytes > KNOWLEDGE_MARKDOWN_MAX_BYTES

    useEffect(() => {
        if (!open) return
        setKey(article?.key ?? (sourceTicketID ? `ticket-${sourceTicketID}` : ''))
        setTitle(article?.title ?? '')
        setSummary(article?.summary ?? '')
        setSourceTicket(null)
        setSourceTicketInput('')
        setSourceTicketOptions([])
        setSourceTicketLoading(false)
        setSourceTicketError('')
        setSourceAttachments([])
        setSourceAttachmentsPage(1)
        setSourceAttachmentsTotal(0)
        setSourceAttachmentsTotalPages(0)
        setSelectedSourceAttachments([])
        setSourceAttachmentsLoading(false)
        setSourceAttachmentsError('')
        setSourceAttachmentSelectionError('')
        setMarkdown(KNOWLEDGE_MARKDOWN_TEMPLATE)
        setEditorTab('edit')
        setSaving(false)
        setPublishing(false)
        setPublishConfirmOpen(false)
        setError('')
        setDraft(null)
        return () => {
            sourceTicketRequest.current?.abort()
            sourceRequest.current?.abort()
        }
    }, [article, open, sourceTicketID])

    const parsedSourceTicketID = sourceTicketID ?? sourceTicket?.id

    const loadSourceAttachments = useCallback(async () => {
        if (
            !projectKey ||
            parsedSourceTicketID === undefined
        ) return
        sourceRequest.current?.abort()
        const controller = new AbortController()
        sourceRequest.current = controller
        setSourceAttachmentsLoading(true)
        setSourceAttachmentsError('')
        setSourceAttachments([])
        try {
            const result = await listKnowledgeSourceAttachments(
                projectKey,
                parsedSourceTicketID,
                {
                    page: sourceAttachmentsPage,
                    pageSize: KNOWLEDGE_SOURCE_ATTACHMENT_PAGE_SIZE,
                    signal: controller.signal,
                },
            )
            if (controller.signal.aborted) return
            const lastValidPage = Math.max(1, result.totalPages)
            if (sourceAttachmentsPage > lastValidPage) {
                setSourceAttachments([])
                setSourceAttachmentsTotal(result.total)
                setSourceAttachmentsTotalPages(result.totalPages)
                setSourceAttachmentsPage(lastValidPage)
                return
            }
            setSourceAttachments(
                result.items.filter(
                    (attachment) => attachment.virus_scan === 'clean',
                ),
            )
            setSourceAttachmentsTotal(result.total)
            setSourceAttachmentsTotalPages(result.totalPages)
        } catch (requestError) {
            if (controller.signal.aborted) return
            setSourceAttachmentsError(
                localizedUnknownErrorMessage(
                    requestError,
                    '来源附件加载失败，请确认有权查看该工单',
                ),
            )
        } finally {
            if (!controller.signal.aborted) {
                setSourceAttachmentsLoading(false)
            }
        }
    }, [
        parsedSourceTicketID,
        projectKey,
        sourceAttachmentsPage,
    ])

    useEffect(() => {
        if (open && parsedSourceTicketID && projectKey) {
            void loadSourceAttachments()
        }
    }, [loadSourceAttachments, open, parsedSourceTicketID, projectKey])

    useEffect(() => {
        if (
            !open ||
            sourceTicketID !== undefined ||
            draft !== null ||
            !projectKey
        ) {
            sourceTicketRequest.current?.abort()
            setSourceTicketLoading(false)
            return
        }
        const search = sourceTicketInput.trim()
        if (search.length < 2) {
            sourceTicketRequest.current?.abort()
            setSourceTicketOptions(sourceTicket ? [sourceTicket] : [])
            setSourceTicketLoading(false)
            setSourceTicketError('')
            return
        }
        const timer = window.setTimeout(() => {
            sourceTicketRequest.current?.abort()
            const controller = new AbortController()
            sourceTicketRequest.current = controller
            setSourceTicketLoading(true)
            setSourceTicketError('')
            void searchKnowledgeSourceTickets(
                projectKey,
                search,
                controller.signal,
            ).then((result) => {
                if (controller.signal.aborted) return
                const options = [...(result.items ?? [])]
                if (
                    sourceTicket &&
                    !options.some((ticket) => ticket.id === sourceTicket.id)
                ) {
                    options.unshift(sourceTicket)
                }
                setSourceTicketOptions(options)
            }).catch((requestError) => {
                if (controller.signal.aborted) return
                setSourceTicketError(localizedUnknownErrorMessage(
                    requestError,
                    '来源工单搜索失败，请稍后重试',
                ))
            }).finally(() => {
                if (!controller.signal.aborted) {
                    setSourceTicketLoading(false)
                }
            })
        }, 250)
        return () => window.clearTimeout(timer)
    }, [
        draft,
        open,
        projectKey,
        sourceTicket,
        sourceTicketID,
        sourceTicketInput,
    ])

    const valid =
        (article !== undefined ||
            /^[a-z][a-z0-9._-]{0,63}$/u.test(key.trim()))
        && title.trim().length > 0
        && markdown.trim().length > 0
        && !markdownTooLarge

    const saveDraft = async () => {
        if (!valid || !projectKey) return
        setSaving(true)
        setError('')
        try {
            const sourceAttachmentIDs = selectedSourceAttachments.map(
                (attachment) => attachment.id,
            )
            const source = {
                source_ticket_id: parsedSourceTicketID,
                source_attachment_ids:
                    sourceAttachmentIDs.length > 0
                        ? sourceAttachmentIDs
                        : undefined,
            }
            const result = article
                ? await createKnowledgeDraft(projectKey, article.id, {
                    title: title.trim(),
                    markdown,
                    ...source,
                })
                : await createKnowledgeArticleDraft(projectKey, {
                    key: key.trim(),
                    title: title.trim(),
                    summary: summary.trim() || undefined,
                    markdown,
                    ...source,
                })
            setDraft(result)
            notify(
                canPublish
                    ? '知识草稿已保存，尚未发布'
                    : '知识草稿已提交，等待项目管理员或经理复核',
                { type: 'success' },
            )
            onSaved(result, false)
        } catch (saveError) {
            setError(localizedUnknownErrorMessage(
                saveError,
                '知识草稿保存失败，请稍后重试',
            ))
        } finally {
            setSaving(false)
        }
    }

    const publish = async () => {
        if (!draft) return
        setPublishing(true)
        setError('')
        try {
            await publishKnowledgeVersion(projectKey, draft.version.id)
            notify('知识文章已发布', { type: 'success' })
            onSaved(draft, true)
            onClose()
        } catch (publishError) {
            setError(localizedUnknownErrorMessage(
                publishError,
                '知识发布失败；草稿已保留，可稍后重试',
            ))
        } finally {
            setPublishing(false)
            setPublishConfirmOpen(false)
        }
    }

    return (
        <Dialog
            open={open}
            onClose={() => {
                if (!saving && !publishing) onClose()
            }}
            fullWidth
            maxWidth="lg"
            aria-labelledby="create-knowledge-title"
        >
            <DialogTitle id="create-knowledge-title">
                {article ? `更新知识：${article.title}` : '沉淀知识'}
            </DialogTitle>
            <DialogContent dividers>
                <Stack spacing={2.5}>
                    {parsedSourceTicketID && (
                        <Alert severity="info">
                            <Stack
                                direction="row"
                                spacing={1}
                                sx={{ alignItems: 'center', flexWrap: 'wrap' }}
                            >
                                <Typography component="span">
                                    本文将保留来源关系：
                                </Typography>
                                <Chip
                                    size="small"
                                    icon={<TicketIcon />}
                                    label={
                                        sourceTicket
                                            ? `${sourceTicket.ticket_number} · ${sourceTicket.title}`
                                            : `工单 #${parsedSourceTicketID}`
                                    }
                                    component={Link}
                                    clickable
                                    to={`/tickets/${parsedSourceTicketID}/show`}
                                />
                            </Stack>
                        </Alert>
                    )}
                    {error && <Alert severity="error">{error}</Alert>}
                    {draft && (
                        <Alert severity="success">
                            草稿 v{draft.version.version} 已保存。
                            {canPublish
                                ? '发布前仍不会出现在普通知识检索中。'
                                : '项目管理员或经理复核发布前，不会出现在普通知识检索中。'}
                        </Alert>
                    )}
                    <Stack
                        direction={{ xs: 'column', md: 'row' }}
                        spacing={2}
                    >
                        <TextField
                            autoFocus={!article}
                            required={!article}
                            disabled={article !== undefined || draft !== null}
                            label="文章 Key"
                            value={key}
                            onChange={(event) => setKey(event.target.value)}
                            helperText={
                                article
                                    ? '文章 Key 创建后不可修改'
                                    : '以小写字母开头，可使用数字、点、下划线和连字符'
                            }
                            slotProps={{ htmlInput: { maxLength: 64 } }}
                            sx={{ flex: 1 }}
                        />
                        <TextField
                            required
                            disabled={draft !== null}
                            label="标题"
                            value={title}
                            onChange={(event) => setTitle(event.target.value)}
                            slotProps={{ htmlInput: { maxLength: 240 } }}
                            sx={{ flex: 2 }}
                        />
                    </Stack>
                    {!article && (
                        <TextField
                            disabled={draft !== null}
                            label="摘要"
                            value={summary}
                            onChange={(event) => setSummary(event.target.value)}
                            multiline
                            minRows={2}
                            helperText={`${summary.length}/1000`}
                            slotProps={{ htmlInput: { maxLength: 1000 } }}
                        />
                    )}
                    <Stack
                        direction={{ xs: 'column', md: 'row' }}
                        spacing={1.5}
                        sx={{ alignItems: { md: 'flex-start' } }}
                    >
                        {sourceTicketID !== undefined ? (
                            <TextField
                                label="来源工单"
                                value={`工单 #${sourceTicketID}`}
                                disabled
                                helperText="从当前工单沉淀，来源关系不可在此移除"
                                sx={{ flex: 1 }}
                            />
                        ) : (
                            <Autocomplete
                                options={sourceTicketOptions}
                                value={sourceTicket}
                                inputValue={sourceTicketInput}
                                loading={sourceTicketLoading}
                                disabled={draft !== null}
                                filterOptions={(options) => options}
                                getOptionLabel={(ticket) =>
                                    `${ticket.ticket_number} · ${ticket.title}`}
                                isOptionEqualToValue={(left, right) =>
                                    left.id === right.id}
                                onInputChange={(_, value, reason) => {
                                    if (reason !== 'reset') {
                                        setSourceTicketInput(value)
                                    }
                                }}
                                onChange={(_, value) => {
                                    sourceRequest.current?.abort()
                                    setSourceTicket(value)
                                    setSourceTicketInput(
                                        value
                                            ? `${value.ticket_number} · ${value.title}`
                                            : '',
                                    )
                                    setSourceAttachments([])
                                    setSourceAttachmentsPage(1)
                                    setSourceAttachmentsTotal(0)
                                    setSourceAttachmentsTotalPages(0)
                                    setSelectedSourceAttachments([])
                                    setSourceAttachmentsError('')
                                    setSourceAttachmentSelectionError('')
                                }}
                                noOptionsText={
                                    sourceTicketInput.trim().length < 2
                                        ? '输入至少 2 个字符开始搜索'
                                        : '没有匹配且有权查看的工单'
                                }
                                renderInput={(params) => (
                                    <TextField
                                        {...params}
                                        label="来源工单（可选）"
                                        helperText={
                                            sourceTicketError
                                            || '按工单号或标题远程搜索；只返回当前项目内有权查看的工单'
                                        }
                                        error={Boolean(sourceTicketError)}
                                        slotProps={{
                                            ...params.slotProps,
                                            input: {
                                                ...params.slotProps.input,
                                                endAdornment: (
                                                    <>
                                                        {sourceTicketLoading && (
                                                            <CircularProgress
                                                                size={18}
                                                            />
                                                        )}
                                                        {
                                                            params.slotProps.input
                                                                ?.endAdornment
                                                        }
                                                    </>
                                                ),
                                            },
                                        }}
                                    />
                                )}
                                sx={{ flex: 1 }}
                            />
                        )}
                        {parsedSourceTicketID && (
                            <Button
                                variant="outlined"
                                disabled={
                                    draft !== null ||
                                    sourceAttachmentsLoading
                                }
                                onClick={() => {
                                    sourceRequest.current?.abort()
                                    setSourceAttachmentsError('')
                                    void loadSourceAttachments()
                                }}
                                sx={{ mt: { md: 1 } }}
                            >
                                {sourceAttachmentsLoading
                                    ? '加载中…'
                                    : '刷新可用附件'}
                            </Button>
                        )}
                    </Stack>
                    {sourceAttachmentsError && (
                        <Alert severity="error">
                            {sourceAttachmentsError}
                        </Alert>
                    )}
                    {parsedSourceTicketID &&
                        !sourceAttachmentsLoading &&
                        !sourceAttachmentsError &&
                        sourceAttachmentsTotal === 0 && (
                        <Typography color="text.secondary">
                            该工单没有可见附件。
                        </Typography>
                    )}
                    {parsedSourceTicketID && (
                        <Stack spacing={1}>
                            <Autocomplete
                                multiple
                                disableCloseOnSelect
                                limitTags={3}
                                getLimitTagsText={(more) => `+${more}`}
                                disabled={draft !== null}
                                loading={sourceAttachmentsLoading}
                                options={sourceAttachments}
                                value={selectedSourceAttachments}
                                getOptionLabel={(attachment) =>
                                    attachment.original_name}
                                getOptionDisabled={(attachment) =>
                                    selectedSourceAttachments.length
                                        >= KNOWLEDGE_SOURCE_ATTACHMENT_MAX_SELECTED
                                    && !selectedSourceAttachments.some(
                                        (selected) =>
                                            selected.id === attachment.id,
                                    )}
                                isOptionEqualToValue={(left, right) =>
                                    left.id === right.id}
                                onChange={(_, value) => {
                                    const unique = uniqueAttachments(value)
                                    if (
                                        unique.length
                                            > KNOWLEDGE_SOURCE_ATTACHMENT_MAX_SELECTED
                                    ) {
                                        setSourceAttachmentSelectionError(
                                            '来源附件最多选择 20 项，请先移除已有附件',
                                        )
                                        return
                                    }
                                    setSourceAttachmentSelectionError('')
                                    setSelectedSourceAttachments(unique)
                                }}
                                noOptionsText={
                                    sourceAttachmentsLoading
                                        ? '正在加载当前页附件…'
                                        : '当前页没有扫描通过且可用的附件'
                                }
                                renderOption={(props, option, state) => {
                                    const {
                                        key: optionKey,
                                        ...optionProps
                                    } = props
                                    return (
                                        <li
                                            key={optionKey}
                                            {...optionProps}
                                        >
                                            <Checkbox
                                                checked={state.selected}
                                                slotProps={{
                                                    input: {
                                                        'aria-label':
                                                            `选择附件 ${option.original_name}`,
                                                    },
                                                }}
                                            />
                                            {option.original_name}
                                        </li>
                                    )
                                }}
                                renderValue={(values, getItemProps) =>
                                    values.map((attachment, index) => {
                                        const {
                                            key: chipKey,
                                            ...itemProps
                                        } = getItemProps({ index })
                                        return (
                                            <Chip
                                                key={chipKey}
                                                size="small"
                                                label={
                                                    attachment.original_name
                                                }
                                                {...itemProps}
                                            />
                                        )
                                    })}
                                renderInput={(params) => (
                                    <TextField
                                        {...params}
                                        label="来源附件"
                                        error={Boolean(
                                            sourceAttachmentSelectionError,
                                        )}
                                        helperText={
                                            sourceAttachmentSelectionError
                                            || `已选择 ${selectedSourceAttachments.length} 项，最多 20 项`
                                        }
                                        slotProps={{
                                            ...params.slotProps,
                                            input: {
                                                ...params.slotProps.input,
                                                endAdornment: (
                                                    <>
                                                        {
                                                            sourceAttachmentsLoading
                                                            && (
                                                                <CircularProgress
                                                                    size={18}
                                                                />
                                                            )
                                                        }
                                                        {
                                                            params.slotProps
                                                                .input
                                                                ?.endAdornment
                                                        }
                                                    </>
                                                ),
                                            },
                                        }}
                                    />
                                )}
                            />
                            <Stack
                                direction={{
                                    xs: 'column',
                                    sm: 'row',
                                }}
                                spacing={1}
                                sx={{
                                    alignItems: {
                                        sm: 'center',
                                    },
                                    justifyContent: 'space-between',
                                }}
                            >
                                <Typography
                                    color="text.secondary"
                                    variant="body2"
                                    role="status"
                                    aria-live="polite"
                                >
                                    {
                                        `第 ${sourceAttachmentsPage}/${Math.max(
                                            1,
                                            sourceAttachmentsTotalPages,
                                        )} 页 / 共 ${sourceAttachmentsTotal} 条附件`
                                    }
                                    {
                                        ` · 当前页显示 ${sourceAttachments.length} 条扫描通过`
                                    }
                                </Typography>
                                <Stack
                                    direction="row"
                                    spacing={1}
                                    sx={{ justifyContent: 'flex-end' }}
                                >
                                    <Button
                                        size="small"
                                        disabled={
                                            draft !== null
                                            || sourceAttachmentsLoading
                                            || sourceAttachmentsPage <= 1
                                        }
                                        aria-label="上一页来源附件"
                                        onClick={() =>
                                            setSourceAttachmentsPage(
                                                (page) =>
                                                    Math.max(1, page - 1),
                                            )}
                                    >
                                        上一页
                                    </Button>
                                    <Button
                                        size="small"
                                        disabled={
                                            draft !== null
                                            || sourceAttachmentsLoading
                                            || sourceAttachmentsTotalPages === 0
                                            || sourceAttachmentsPage
                                                >= sourceAttachmentsTotalPages
                                        }
                                        aria-label="下一页来源附件"
                                        onClick={() =>
                                            setSourceAttachmentsPage(
                                                (page) => page + 1,
                                            )}
                                    >
                                        下一页
                                    </Button>
                                </Stack>
                            </Stack>
                        </Stack>
                    )}
                    <Tabs
                        value={editorTab}
                        onChange={(_, value: 'edit' | 'preview') =>
                            setEditorTab(value)}
                        aria-label="知识正文编辑模式"
                    >
                        <Tab value="edit" label="编辑 Markdown" />
                        <Tab value="preview" label="安全预览" />
                    </Tabs>
                    {editorTab === 'edit' ? (
                        <TextField
                            required
                            disabled={draft !== null}
                            label="Markdown 正文"
                            value={markdown}
                            onChange={(event) => setMarkdown(event.target.value)}
                            multiline
                            minRows={18}
                            error={markdownTooLarge}
                            helperText={
                                markdownTooLarge
                                    ? `正文为 ${markdownBytes.toLocaleString('zh-CN')} 字节，超过 131,072 字节（128 KiB）上限，请精简后再保存`
                                    : `UTF-8 ${markdownBytes.toLocaleString('zh-CN')} / 131,072 字节（128 KiB）· HTML 和远程图片不会执行或加载`
                            }
                            slotProps={{
                                htmlInput: {
                                    spellCheck: true,
                                },
                            }}
                        />
                    ) : (
                        <Stack
                            role="region"
                            aria-label="知识 Markdown 安全预览"
                            sx={{
                                minHeight: 360,
                                border: 1,
                                borderColor: 'divider',
                                p: 2,
                            }}
                        >
                            <SafeKnowledgeMarkdown markdown={markdown} />
                        </Stack>
                    )}
                </Stack>
            </DialogContent>
            <DialogActions>
                <Button
                    disabled={saving || publishing}
                    onClick={onClose}
                >
                    {draft
                        ? canPublish ? '稍后发布' : '完成'
                        : '取消'}
                </Button>
                {!draft ? (
                    <Button
                        variant="contained"
                        startIcon={<SaveIcon />}
                        disabled={!valid || !projectKey || saving}
                        onClick={() => void saveDraft()}
                    >
                        {saving
                            ? '正在保存…'
                            : article
                              ? '保存新版本草稿'
                              : canPublish
                                ? '保存草稿'
                                : '提交草稿待复核'}
                    </Button>
                ) : canPublish ? (
                    <Button
                        variant="contained"
                        startIcon={<PublishIcon />}
                        disabled={publishing}
                        onClick={() => setPublishConfirmOpen(true)}
                    >
                        {publishing ? '正在发布…' : '发布'}
                    </Button>
                ) : null}
            </DialogActions>
            <KnowledgePublishConfirmDialog
                open={publishConfirmOpen}
                title={draft?.article.title ?? title.trim()}
                busy={publishing}
                onCancel={() => setPublishConfirmOpen(false)}
                onConfirm={() => void publish()}
            />
        </Dialog>
    )
}
