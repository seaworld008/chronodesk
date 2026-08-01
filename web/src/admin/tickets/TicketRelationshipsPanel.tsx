import {
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react'
import {
    AddLink as AddLinkIcon,
    DeviceHub as RelationIcon,
    Refresh as RefreshIcon,
} from '@mui/icons-material'
import {
    Alert,
    Autocomplete,
    Box,
    Button,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    FormControl,
    InputLabel,
    MenuItem,
    Select,
    Stack,
    Tab,
    TableBody,
    TableCell,
    TableHead,
    TablePagination,
    TableRow,
    Tabs,
    TextField,
    Tooltip,
    Typography,
} from '@mui/material'
import {
    useGetIdentity,
    useNotify,
    usePermissions,
    useRecordContext,
    useRefresh,
} from 'react-admin'
import {
    ResizableMuiTable,
    TruncatedText,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    humanApiRoutes,
    type AddTicketEntityLinkResult,
    type AddTicketRelationResult,
    type EntityKind,
    type Ticket,
    type TicketEntityLinkPage,
    type TicketPage,
    type TicketRelationPage,
    type TicketRelationType,
} from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import {
    canEditTicket,
    type TicketRolePermissions,
} from './ticketAccess'

const entityKindOptions: Array<{ id: EntityKind; label: string }> = [
    { id: 'asset', label: '资产' },
    { id: 'device', label: '设备' },
    { id: 'application', label: '应用' },
    { id: 'contract', label: '合同' },
    { id: 'customer', label: '客户' },
    { id: 'location', label: '位置' },
    { id: 'other', label: '其他' },
]

const relationOptions: Array<{
    id: TicketRelationType
    label: string
}> = [
    { id: 'parent_of', label: '父工单' },
    { id: 'duplicate_of', label: '重复于' },
    { id: 'blocks', label: '阻塞' },
    { id: 'collaborates_with', label: '协作' },
]

const entityColumns: ResizableColumn[] = [
    { key: 'kind', defaultWidth: 128, minWidth: 104, maxWidth: 200 },
    { key: 'display_name', defaultWidth: 260, minWidth: 160, maxWidth: 520 },
    { key: 'reference_id', defaultWidth: 260, minWidth: 160, maxWidth: 520 },
    { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
]

const relationColumns: ResizableColumn[] = [
    { key: 'relation', defaultWidth: 144, minWidth: 112, maxWidth: 220 },
    { key: 'related_ticket', defaultWidth: 300, minWidth: 180, maxWidth: 560 },
    { key: 'reason', defaultWidth: 360, minWidth: 180, maxWidth: 640 },
    { key: 'created_at', defaultWidth: 190, minWidth: 150, maxWidth: 280 },
]

const dateTime = (value: string) => {
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime())
        ? value
        : parsed.toLocaleString('zh-CN')
}

const entityKindLabel = (kind: EntityKind) =>
    entityKindOptions.find((item) => item.id === kind)?.label ?? kind

const relationLabel = (relation: TicketRelationType) =>
    relationOptions.find((item) => item.id === relation)?.label ?? relation

const ticketLabel = (ticket: Ticket) =>
    `#${ticket.ticket_number} ${ticket.title}`

const entityReferencePattern = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$/u

export const TicketRelationshipsPanel = () => {
    const ticket = useRecordContext<Ticket>()
    const { permissions } = usePermissions<TicketRolePermissions>()
    const { identity } = useGetIdentity()
    const notify = useNotify()
    const refresh = useRefresh()
    const [activeTab, setActiveTab] = useState<'entities' | 'tickets'>('entities')
    const [entityPage, setEntityPage] = useState<TicketEntityLinkPage | null>(null)
    const [relationPage, setRelationPage] = useState<TicketRelationPage | null>(null)
    const [entityPageIndex, setEntityPageIndex] = useState(0)
    const [entityPageSize, setEntityPageSize] = useState(25)
    const [relationPageIndex, setRelationPageIndex] = useState(0)
    const [relationPageSize, setRelationPageSize] = useState(25)
    const [entityLoading, setEntityLoading] = useState(true)
    const [relationLoading, setRelationLoading] = useState(true)
    const [entityError, setEntityError] = useState('')
    const [relationError, setRelationError] = useState('')
    const [resourceVersion, setResourceVersion] = useState(ticket?.version ?? 0)
    const entityController = useRef<AbortController | null>(null)
    const relationController = useRef<AbortController | null>(null)
    const entitySequence = useRef(0)
    const relationSequence = useRef(0)
    const [entityDialogOpen, setEntityDialogOpen] = useState(false)
    const [relationDialogOpen, setRelationDialogOpen] = useState(false)
    const [entityKind, setEntityKind] = useState<EntityKind>('asset')
    const [entityReference, setEntityReference] = useState('')
    const [entityName, setEntityName] = useState('')
    const [relationType, setRelationType] =
        useState<TicketRelationType>('collaborates_with')
    const [relationReason, setRelationReason] = useState('')
    const [ticketSearch, setTicketSearch] = useState('')
    const [ticketOptions, setTicketOptions] = useState<Ticket[]>([])
    const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null)
    const [ticketSearchLoading, setTicketSearchLoading] = useState(false)
    const ticketSearchController = useRef<AbortController | null>(null)
    const [submitting, setSubmitting] = useState(false)

    const canManage = canEditTicket(
        ticket,
        permissions?.project_role,
        identity?.id,
    )

    useEffect(() => {
        setResourceVersion(ticket?.version ?? 0)
    }, [ticket?.id, ticket?.version])

    const relationshipParameters = useCallback(async () => {
        if (!ticket?.id) {
            throw new Error('工单信息尚未加载')
        }
        const projectKey = await resolveActiveProjectKey()
        return { projectKey, ticketID: Number(ticket.id) }
    }, [ticket?.id])

    const loadEntityLinks = useCallback(async () => {
        if (!ticket?.id) return
        entityController.current?.abort()
        const controller = new AbortController()
        const sequence = entitySequence.current + 1
        entitySequence.current = sequence
        entityController.current = controller
        setEntityLoading(true)
        setEntityError('')
        try {
            const path = humanApiRoutes.listProjectTicketEntityLinks(
                await relationshipParameters(),
                {
                    page: entityPageIndex + 1,
                    page_size: entityPageSize,
                    sort_by: 'created_at',
                    sort_order: 'desc',
                },
            )
            const result = await apiFetch<TicketEntityLinkPage>(
                path,
                { signal: controller.signal },
            )
            if (
                controller.signal.aborted
                || entitySequence.current !== sequence
            ) return
            setEntityPage({ ...result, items: result.items ?? [] })
            setResourceVersion(result.ticket_version)
        } catch (error) {
            if (
                controller.signal.aborted
                || entitySequence.current !== sequence
            ) return
            setEntityError(
                localizedUnknownErrorMessage(error, '实体关联加载失败'),
            )
        } finally {
            if (
                !controller.signal.aborted
                && entitySequence.current === sequence
            ) {
                setEntityLoading(false)
            }
        }
    }, [
        entityPageIndex,
        entityPageSize,
        relationshipParameters,
        ticket?.id,
    ])

    const loadTicketRelations = useCallback(async () => {
        if (!ticket?.id) return
        relationController.current?.abort()
        const controller = new AbortController()
        const sequence = relationSequence.current + 1
        relationSequence.current = sequence
        relationController.current = controller
        setRelationLoading(true)
        setRelationError('')
        try {
            const path = humanApiRoutes.listProjectTicketRelations(
                await relationshipParameters(),
                {
                    page: relationPageIndex + 1,
                    page_size: relationPageSize,
                    sort_by: 'created_at',
                    sort_order: 'desc',
                },
            )
            const result = await apiFetch<TicketRelationPage>(
                path,
                { signal: controller.signal },
            )
            if (
                controller.signal.aborted
                || relationSequence.current !== sequence
            ) return
            setRelationPage({ ...result, items: result.items ?? [] })
            setResourceVersion(result.ticket_version)
        } catch (error) {
            if (
                controller.signal.aborted
                || relationSequence.current !== sequence
            ) return
            setRelationError(
                localizedUnknownErrorMessage(error, '工单关系加载失败'),
            )
        } finally {
            if (
                !controller.signal.aborted
                && relationSequence.current === sequence
            ) {
                setRelationLoading(false)
            }
        }
    }, [
        relationPageIndex,
        relationPageSize,
        relationshipParameters,
        ticket?.id,
    ])

    useEffect(() => {
        void loadEntityLinks()
        return () => entityController.current?.abort()
    }, [loadEntityLinks])

    useEffect(() => {
        void loadTicketRelations()
        return () => relationController.current?.abort()
    }, [loadTicketRelations])

    useEffect(() => {
        if (!relationDialogOpen) {
            ticketSearchController.current?.abort()
            setTicketSearchLoading(false)
            return
        }
        const normalizedSearch = ticketSearch.trim()
        if (normalizedSearch.length < 2) {
            ticketSearchController.current?.abort()
            setTicketOptions(selectedTicket ? [selectedTicket] : [])
            setTicketSearchLoading(false)
            return
        }
        const timer = window.setTimeout(() => {
            ticketSearchController.current?.abort()
            const controller = new AbortController()
            ticketSearchController.current = controller
            setTicketSearchLoading(true)
            void resolveActiveProjectKey()
                .then((projectKey) =>
                    apiFetch<TicketPage>(
                        humanApiRoutes.listProjectTickets(
                            { projectKey },
                            {
                                page: 1,
                                page_size: 25,
                                search: normalizedSearch,
                                sort_by: 'updated_at',
                                sort_order: 'desc',
                            },
                        ),
                        { signal: controller.signal },
                    ),
                )
                .then((result) => {
                    if (controller.signal.aborted) return
                    const options = (result.items ?? []).filter(
                        (option) => option.id !== ticket?.id,
                    )
                    if (
                        selectedTicket
                        && !options.some((option) => option.id === selectedTicket.id)
                    ) {
                        options.unshift(selectedTicket)
                    }
                    setTicketOptions(options)
                })
                .catch((error) => {
                    if (controller.signal.aborted) return
                    notify(
                        localizedUnknownErrorMessage(
                            error,
                            '关联工单搜索失败',
                        ),
                        { type: 'error' },
                    )
                })
                .finally(() => {
                    if (!controller.signal.aborted) {
                        setTicketSearchLoading(false)
                    }
                })
        }, 250)
        return () => window.clearTimeout(timer)
    }, [
        notify,
        relationDialogOpen,
        selectedTicket,
        ticket?.id,
        ticketSearch,
    ])

    useEffect(() => () => {
        entityController.current?.abort()
        relationController.current?.abort()
        ticketSearchController.current?.abort()
        entitySequence.current += 1
        relationSequence.current += 1
    }, [])

    const closeEntityDialog = (force = false) => {
        if (submitting && !force) return
        setEntityDialogOpen(false)
        setEntityKind('asset')
        setEntityReference('')
        setEntityName('')
    }

    const closeRelationDialog = (force = false) => {
        if (submitting && !force) return
        setRelationDialogOpen(false)
        setRelationType('collaborates_with')
        setRelationReason('')
        setTicketSearch('')
        setTicketOptions([])
        setSelectedTicket(null)
    }

    const createEntityLink = async () => {
        const reference = entityReference.trim()
        const displayName = entityName.trim()
        if (!entityReferencePattern.test(reference) || !displayName) {
            notify('请填写有效的实体名称和引用标识', { type: 'warning' })
            return
        }
        setSubmitting(true)
        try {
            const parameters = await relationshipParameters()
            const result = await apiFetch<AddTicketEntityLinkResult>(
                humanApiRoutes.createProjectTicketEntityLink(parameters),
                {
                method: 'POST',
                body: JSON.stringify({
                    expected_version: resourceVersion,
                    kind: entityKind,
                    reference_id: reference,
                    display_name: displayName,
                    metadata: {},
                }),
                },
            )
            setResourceVersion(result.ticket_version)
            setEntityPageIndex(0)
            closeEntityDialog(true)
            await loadEntityLinks()
            refresh()
            notify('实体关联已添加', { type: 'success' })
        } catch (error) {
            notify(
                localizedUnknownErrorMessage(error, '实体关联添加失败'),
                { type: 'error' },
            )
        } finally {
            setSubmitting(false)
        }
    }

    const createTicketRelation = async () => {
        if (!selectedTicket) {
            notify('请从搜索结果中选择一个工单', { type: 'warning' })
            return
        }
        setSubmitting(true)
        try {
            const parameters = await relationshipParameters()
            const result = await apiFetch<AddTicketRelationResult>(
                humanApiRoutes.createProjectTicketRelation(parameters),
                {
                method: 'POST',
                body: JSON.stringify({
                    expected_version: resourceVersion,
                    target_ticket_id: selectedTicket.id,
                    relation: relationType,
                    reason: relationReason.trim(),
                }),
                },
            )
            setResourceVersion(result.ticket_version)
            setRelationPageIndex(0)
            closeRelationDialog(true)
            await loadTicketRelations()
            refresh()
            notify('工单关系已添加', { type: 'success' })
        } catch (error) {
            notify(
                localizedUnknownErrorMessage(error, '工单关系添加失败'),
                { type: 'error' },
            )
        } finally {
            setSubmitting(false)
        }
    }

    if (!ticket) return null

    return (
        <Box>
            <Stack
                direction={{ xs: 'column', sm: 'row' }}
                spacing={2}
                sx={{
                    alignItems: { xs: 'stretch', sm: 'center' },
                    justifyContent: 'space-between',
                    mb: 2,
                }}
            >
                <Box>
                    <Typography variant="h6" component="h2">
                        工单关联
                    </Typography>
                    <Typography variant="body2" color="text.secondary">
                        关联记录按项目隔离并独立分页，避免工单详情随数据增长而膨胀。
                    </Typography>
                </Box>
                <Stack direction="row" spacing={1}>
                    <Tooltip title="刷新当前关联列表">
                        <span>
                            <Button
                                startIcon={<RefreshIcon />}
                                disabled={entityLoading || relationLoading}
                                onClick={() => {
                                    if (activeTab === 'entities') {
                                        void loadEntityLinks()
                                    } else {
                                        void loadTicketRelations()
                                    }
                                }}
                            >
                                刷新
                            </Button>
                        </span>
                    </Tooltip>
                    {canManage && (
                        <Button
                            variant="contained"
                            startIcon={
                                activeTab === 'entities'
                                    ? <AddLinkIcon />
                                    : <RelationIcon />
                            }
                            onClick={() => {
                                if (activeTab === 'entities') {
                                    setEntityDialogOpen(true)
                                } else {
                                    setRelationDialogOpen(true)
                                }
                            }}
                        >
                            {activeTab === 'entities'
                                ? '添加实体关联'
                                : '添加工单关系'}
                        </Button>
                    )}
                </Stack>
            </Stack>

            <Tabs
                value={activeTab}
                onChange={(_, value: 'entities' | 'tickets') =>
                    setActiveTab(value)}
                aria-label="工单关联类型"
                sx={{ mb: 2, borderBottom: 1, borderColor: 'divider' }}
            >
                <Tab
                    value="entities"
                    label={`实体关联（${entityPage?.total ?? 0}）`}
                />
                <Tab
                    value="tickets"
                    label={`工单关系（${relationPage?.total ?? 0}）`}
                />
            </Tabs>

            {activeTab === 'entities' && (
                <Box>
                    {entityError && (
                        <Alert
                            severity="error"
                            sx={{ mb: 2 }}
                            action={
                                <Button onClick={() => void loadEntityLinks()}>
                                    重试
                                </Button>
                            }
                        >
                            {entityError}
                        </Alert>
                    )}
                    {entityLoading && !entityPage ? (
                        <Box role="status" sx={{ py: 8, textAlign: 'center' }}>
                            <CircularProgress
                                size={30}
                                aria-label="正在加载实体关联"
                            />
                        </Box>
                    ) : (
                        <Box sx={{ overflowX: 'auto' }}>
                            <ResizableMuiTable
                                tableId="tickets.show.entity-links"
                                columns={entityColumns}
                                size="small"
                                aria-label="工单实体关联列表"
                            >
                                <TableHead>
                                    <TableRow>
                                        <TableCell>类型</TableCell>
                                        <TableCell>名称</TableCell>
                                        <TableCell>引用标识</TableCell>
                                        <TableCell>关联时间</TableCell>
                                    </TableRow>
                                </TableHead>
                                <TableBody>
                                    {(entityPage?.items ?? []).map((item) => (
                                        <TableRow key={item.id} hover>
                                            <TableCell>
                                                <Chip
                                                    size="small"
                                                    variant="outlined"
                                                    label={entityKindLabel(item.kind)}
                                                />
                                            </TableCell>
                                            <TableCell>
                                                <TruncatedText title={item.display_name}>
                                                    {item.display_name}
                                                </TruncatedText>
                                            </TableCell>
                                            <TableCell>
                                                <TruncatedText title={item.reference_id}>
                                                    {item.reference_id}
                                                </TruncatedText>
                                            </TableCell>
                                            <TableCell>
                                                {dateTime(item.created_at)}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                    {(entityPage?.items.length ?? 0) === 0 && (
                                        <TableRow>
                                            <TableCell
                                                colSpan={4}
                                                align="center"
                                                sx={{ py: 6 }}
                                            >
                                                暂无实体关联。
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </ResizableMuiTable>
                            <TablePagination
                                component="div"
                                count={entityPage?.total ?? 0}
                                page={entityPageIndex}
                                onPageChange={(_, nextPage) =>
                                    setEntityPageIndex(nextPage)}
                                rowsPerPage={entityPageSize}
                                onRowsPerPageChange={(event) => {
                                    setEntityPageSize(Number(event.target.value))
                                    setEntityPageIndex(0)
                                }}
                                rowsPerPageOptions={[25, 50, 100]}
                                labelRowsPerPage="每页记录数"
                                labelDisplayedRows={({ from, to, count }) =>
                                    `${from}–${to} / ${count}`}
                                showFirstButton
                                showLastButton
                            />
                        </Box>
                    )}
                </Box>
            )}

            {activeTab === 'tickets' && (
                <Box>
                    {relationError && (
                        <Alert
                            severity="error"
                            sx={{ mb: 2 }}
                            action={
                                <Button onClick={() => void loadTicketRelations()}>
                                    重试
                                </Button>
                            }
                        >
                            {relationError}
                        </Alert>
                    )}
                    {relationLoading && !relationPage ? (
                        <Box role="status" sx={{ py: 8, textAlign: 'center' }}>
                            <CircularProgress
                                size={30}
                                aria-label="正在加载工单关系"
                            />
                        </Box>
                    ) : (
                        <Box sx={{ overflowX: 'auto' }}>
                            <ResizableMuiTable
                                tableId="tickets.show.relations"
                                columns={relationColumns}
                                size="small"
                                aria-label="工单关系列表"
                            >
                                <TableHead>
                                    <TableRow>
                                        <TableCell>关系</TableCell>
                                        <TableCell>目标工单</TableCell>
                                        <TableCell>说明</TableCell>
                                        <TableCell>关联时间</TableCell>
                                    </TableRow>
                                </TableHead>
                                <TableBody>
                                    {(relationPage?.items ?? []).map((item) => (
                                        <TableRow key={item.id} hover>
                                            <TableCell>
                                                <Chip
                                                    size="small"
                                                    color="primary"
                                                    variant="outlined"
                                                    label={relationLabel(item.relation)}
                                                />
                                            </TableCell>
                                            <TableCell>
                                                <Stack
                                                    direction="row"
                                                    spacing={0.75}
                                                    sx={{ alignItems: 'center' }}
                                                >
                                                    <Chip
                                                        size="small"
                                                        variant="outlined"
                                                        label={
                                                            item.direction === 'incoming'
                                                                ? '来自'
                                                                : '指向'
                                                        }
                                                    />
                                                    <TruncatedText
                                                        title={`#${item.related_ticket_number} ${item.related_ticket_title}`}
                                                    >
                                                        #{item.related_ticket_number}{' '}
                                                        {item.related_ticket_title}
                                                    </TruncatedText>
                                                </Stack>
                                            </TableCell>
                                            <TableCell>
                                                <TruncatedText title={item.reason || '—'}>
                                                    {item.reason || '—'}
                                                </TruncatedText>
                                            </TableCell>
                                            <TableCell>
                                                {dateTime(item.created_at)}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                    {(relationPage?.items.length ?? 0) === 0 && (
                                        <TableRow>
                                            <TableCell
                                                colSpan={4}
                                                align="center"
                                                sx={{ py: 6 }}
                                            >
                                                暂无工单关系。
                                            </TableCell>
                                        </TableRow>
                                    )}
                                </TableBody>
                            </ResizableMuiTable>
                            <TablePagination
                                component="div"
                                count={relationPage?.total ?? 0}
                                page={relationPageIndex}
                                onPageChange={(_, nextPage) =>
                                    setRelationPageIndex(nextPage)}
                                rowsPerPage={relationPageSize}
                                onRowsPerPageChange={(event) => {
                                    setRelationPageSize(Number(event.target.value))
                                    setRelationPageIndex(0)
                                }}
                                rowsPerPageOptions={[25, 50, 100]}
                                labelRowsPerPage="每页记录数"
                                labelDisplayedRows={({ from, to, count }) =>
                                    `${from}–${to} / ${count}`}
                                showFirstButton
                                showLastButton
                            />
                        </Box>
                    )}
                </Box>
            )}

            <Dialog
                open={entityDialogOpen}
                onClose={() => closeEntityDialog()}
                fullWidth
                maxWidth="sm"
                aria-labelledby="add-entity-link-title"
            >
                <DialogTitle id="add-entity-link-title">
                    添加实体关联
                </DialogTitle>
                <DialogContent>
                    <Stack spacing={2.5} sx={{ pt: 1 }}>
                        <FormControl fullWidth>
                            <InputLabel id="entity-kind-label">实体类型</InputLabel>
                            <Select
                                labelId="entity-kind-label"
                                value={entityKind}
                                label="实体类型"
                                onChange={(event) =>
                                    setEntityKind(event.target.value as EntityKind)}
                            >
                                {entityKindOptions.map((option) => (
                                    <MenuItem key={option.id} value={option.id}>
                                        {option.label}
                                    </MenuItem>
                                ))}
                            </Select>
                        </FormControl>
                        <TextField
                            autoFocus
                            required
                            label="显示名称"
                            value={entityName}
                            onChange={(event) => setEntityName(event.target.value)}
                            slotProps={{ htmlInput: { maxLength: 255 } }}
                        />
                        <TextField
                            required
                            label="引用标识"
                            value={entityReference}
                            onChange={(event) =>
                                setEntityReference(event.target.value)}
                            helperText="可使用字母、数字、点、下划线、冒号、斜杠和连字符"
                            slotProps={{ htmlInput: { maxLength: 255 } }}
                        />
                    </Stack>
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={submitting}
                        onClick={() => closeEntityDialog()}
                    >
                        取消
                    </Button>
                    <Button
                        variant="contained"
                        disabled={
                            submitting
                            || !entityName.trim()
                            || !entityReferencePattern.test(entityReference.trim())
                        }
                        onClick={() => void createEntityLink()}
                    >
                        {submitting ? '正在添加…' : '确认添加'}
                    </Button>
                </DialogActions>
            </Dialog>

            <Dialog
                open={relationDialogOpen}
                onClose={() => closeRelationDialog()}
                fullWidth
                maxWidth="sm"
                aria-labelledby="add-ticket-relation-title"
            >
                <DialogTitle id="add-ticket-relation-title">
                    添加工单关系
                </DialogTitle>
                <DialogContent>
                    <Stack spacing={2.5} sx={{ pt: 1 }}>
                        <Autocomplete
                            options={ticketOptions}
                            value={selectedTicket}
                            inputValue={ticketSearch}
                            loading={ticketSearchLoading}
                            filterOptions={(options) => options}
                            getOptionLabel={ticketLabel}
                            isOptionEqualToValue={(option, value) =>
                                option.id === value.id}
                            onChange={(_, value) => setSelectedTicket(value)}
                            onInputChange={(_, value, reason) => {
                                if (reason !== 'reset') {
                                    setTicketSearch(value)
                                }
                            }}
                            noOptionsText={
                                ticketSearch.trim().length < 2
                                    ? '输入至少 2 个字符开始搜索'
                                    : '没有匹配的工单'
                            }
                            loadingText="正在搜索工单…"
                            renderInput={(params) => (
                                <TextField
                                    {...params}
                                    autoFocus
                                    required
                                    label="目标工单"
                                    placeholder="按编号或标题搜索"
                                    slotProps={{
                                        ...params.slotProps,
                                        htmlInput: {
                                            ...params.slotProps.htmlInput,
                                            'aria-label': '搜索并选择目标工单',
                                        },
                                    }}
                                />
                            )}
                        />
                        <FormControl fullWidth>
                            <InputLabel id="relation-type-label">
                                关系类型
                            </InputLabel>
                            <Select
                                labelId="relation-type-label"
                                value={relationType}
                                label="关系类型"
                                onChange={(event) =>
                                    setRelationType(
                                        event.target.value as TicketRelationType,
                                    )}
                            >
                                {relationOptions.map((option) => (
                                    <MenuItem key={option.id} value={option.id}>
                                        {option.label}
                                    </MenuItem>
                                ))}
                            </Select>
                        </FormControl>
                        <TextField
                            label="关系说明"
                            value={relationReason}
                            onChange={(event) =>
                                setRelationReason(event.target.value)}
                            multiline
                            minRows={3}
                            slotProps={{ htmlInput: { maxLength: 1000 } }}
                            helperText={`${relationReason.length}/1000`}
                        />
                    </Stack>
                </DialogContent>
                <DialogActions>
                    <Button
                        disabled={submitting}
                        onClick={() => closeRelationDialog()}
                    >
                        取消
                    </Button>
                    <Button
                        variant="contained"
                        disabled={submitting || !selectedTicket}
                        onClick={() => void createTicketRelation()}
                    >
                        {submitting ? '正在添加…' : '确认添加'}
                    </Button>
                </DialogActions>
            </Dialog>
        </Box>
    )
}
