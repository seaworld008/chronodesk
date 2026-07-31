import React from 'react'
import {
    HttpError,
    Title,
    useNotify,
    usePermissions,
} from 'react-admin'
import {
    useNavigate,
    useSearchParams,
} from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
    Alert,
    Autocomplete,
    Avatar,
    Box,
    Button,
    Checkbox,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    FormControl,
    InputLabel,
    MenuItem,
    Paper,
    Select,
    Stack,
    Step,
    StepLabel,
    Stepper,
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TablePagination,
    TableRow,
    TableSortLabel,
    TextField,
    Typography,
} from '@mui/material'
import {
    buildHumanApiRequest,
    humanApiRoutes,
    type CreatePlatformProjectRequest,
    type ListPlatformProjectsOperationQuery,
    type PlatformBusinessUnitPage,
    type PlatformBusinessUnitSummary,
    type PlatformProjectPage,
    type PlatformProjectSummary,
    type ProjectCreationContext,
    type ProjectStatus,
    type ProjectUserOption,
} from '@/lib/generated/human-api'
import {
    parsePlatformRole,
    type AccessPermissions,
} from '@/lib/accessControl'
import {
    activeProjectKey,
    clearProjectScopeCache,
    refreshAuthorizedProjectInventory,
} from '@/lib/projectScope'
import {
    apiFetch,
    localizedApiErrorMessage,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    InlineDetails,
    ResizableMuiTable,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'

type GovernanceOrderBy = NonNullable<
    ListPlatformProjectsOperationQuery['order_by']
>
type GovernanceOrder = NonNullable<
    ListPlatformProjectsOperationQuery['order']
>

const platformProjectColumns: ResizableColumn[] = [
    { key: 'project', defaultWidth: 320, minWidth: 240, maxWidth: 520 },
    { key: 'key', defaultWidth: 136, minWidth: 112, maxWidth: 220 },
    { key: 'business-unit', defaultWidth: 220, minWidth: 160, maxWidth: 360 },
    { key: 'status', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
    { key: 'updated-at', defaultWidth: 190, minWidth: 168, maxWidth: 260 },
    {
        key: 'actions',
        defaultWidth: 260,
        minWidth: 220,
        maxWidth: 360,
        sticky: 'right',
    },
]

const emptyProjectPage: PlatformProjectPage = {
    items: [],
    total: 0,
    page: 1,
    page_size: 25,
    total_pages: 0,
}

const fetchPlatformProjects = (
    query: ListPlatformProjectsOperationQuery,
    signal?: AbortSignal,
) =>
    apiFetch<PlatformProjectPage>(
        humanApiRoutes.listPlatformProjects(query),
        {
            method: 'GET',
            signal,
        },
    )

const fetchCreationContext = (
    userSearch: string,
    businessUnitSearch: string,
    signal?: AbortSignal,
) =>
    apiFetch<ProjectCreationContext>(
        humanApiRoutes.getPlatformProjectCreationContext({
            page: 1,
            page_size: 25,
            business_unit_page: 1,
            business_unit_page_size: 25,
            ...(userSearch ? { search: userSearch } : {}),
            ...(businessUnitSearch
                ? { business_unit_search: businessUnitSearch }
                : {}),
        }),
        {
            method: 'GET',
            signal,
        },
    )

const fetchGovernanceBusinessUnits = (
    search: string,
    signal?: AbortSignal,
) =>
    apiFetch<PlatformBusinessUnitPage>(
        humanApiRoutes.listPlatformProjectBusinessUnits({
            page: 1,
            page_size: 25,
            ...(search ? { search } : {}),
        }),
        {
            method: 'GET',
            signal,
        },
    )

const userLabel = (user: ProjectUserOption) =>
    user.display_name || user.username

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const responseData = (value: unknown): unknown =>
    isRecord(value) && 'data' in value ? value.data : value

const matchesArchivedProject = (
    value: unknown,
    target: PlatformProjectSummary,
): boolean => {
    const archived = responseData(value)
    return (
        isRecord(archived) &&
        archived.public_id === target.public_id &&
        archived.status === 'archived'
    )
}

const mergeUserOptions = (
    selected: ProjectUserOption[],
    remote: ProjectUserOption[],
) => {
    const byID = new Map<number, ProjectUserOption>()
    for (const user of [...selected, ...remote]) byID.set(user.id, user)
    return [...byID.values()]
}

const mergeBusinessUnitOptions = (
    selectedPublicID: string,
    current: PlatformBusinessUnitSummary[],
    remote: PlatformBusinessUnitSummary[],
) => {
    const byID = new Map<string, PlatformBusinessUnitSummary>()
    for (const unit of [...current, ...remote]) byID.set(unit.public_id, unit)
    const selected = byID.get(selectedPublicID)
    return [
        ...(selected ? [selected] : []),
        ...[...byID.values()].filter(
            ({ public_id }) => public_id !== selectedPublicID,
        ),
    ]
}

type ProjectCreationWizardProps = {
    context: ProjectCreationContext | null
    open: boolean
    onClose: () => void
    onCreated: (project: PlatformProjectSummary) => Promise<void>
}

const ProjectCreationWizard = ({
    context,
    open,
    onClose,
    onCreated,
}: ProjectCreationWizardProps) => {
    const [activeStep, setActiveStep] = React.useState(0)
    const [name, setName] = React.useState('')
    const [key, setKey] = React.useState('')
    const [description, setDescription] = React.useState('')
    const [businessUnitPublicID, setBusinessUnitPublicID] =
        React.useState('')
    const [businessUnitOptions, setBusinessUnitOptions] =
        React.useState<PlatformBusinessUnitSummary[]>([])
    const [businessUnitSearch, setBusinessUnitSearch] = React.useState('')
    const [businessUnitLoading, setBusinessUnitLoading] =
        React.useState(false)
    const [selectedAdministrators, setSelectedAdministrators] =
        React.useState<ProjectUserOption[]>([])
    const [userOptions, setUserOptions] = React.useState<ProjectUserOption[]>([])
    const [userSearch, setUserSearch] = React.useState('')
    const [userLoading, setUserLoading] = React.useState(false)
    const [defaultQueueKey, setDefaultQueueKey] = React.useState('default')
    const [defaultQueueName, setDefaultQueueName] =
        React.useState('默认队列')
    const [submitting, setSubmitting] = React.useState(false)
    const [error, setError] = React.useState('')
    const initialized = React.useRef(false)

    React.useEffect(() => {
        if (!open) {
            initialized.current = false
            return
        }
        if (!context || initialized.current) return
        initialized.current = true
        setActiveStep(0)
        setName('')
        setKey('')
        setDescription('')
        setBusinessUnitPublicID(
            context.business_units.items[0]?.public_id ?? '',
        )
        setBusinessUnitOptions(context.business_units.items)
        setBusinessUnitSearch('')
        setSelectedAdministrators([context.creator])
        setUserOptions(
            mergeUserOptions([context.creator], context.users.items),
        )
        setUserSearch('')
        setDefaultQueueKey('default')
        setDefaultQueueName('默认队列')
        setError('')
    }, [context, open])

    React.useEffect(() => {
        if (!open || !context) return
        const controller = new AbortController()
        const timer = window.setTimeout(() => {
            setUserLoading(true)
            setBusinessUnitLoading(true)
            void fetchCreationContext(
                userSearch.trim(),
                businessUnitSearch.trim(),
                controller.signal,
            )
                .then((nextContext) => {
                    if (!controller.signal.aborted) {
                        setUserOptions((current) =>
                            mergeUserOptions(
                                selectedAdministrators,
                                mergeUserOptions(
                                    current.filter((user) =>
                                        selectedAdministrators.some(
                                            ({ id }) => id === user.id,
                                        ),
                                    ),
                                    nextContext.users.items,
                                ),
                            ),
                        )
                        setBusinessUnitOptions((current) =>
                            mergeBusinessUnitOptions(
                                businessUnitPublicID,
                                current,
                                nextContext.business_units.items,
                            ),
                        )
                    }
                })
                .catch((requestError: unknown) => {
                    if (!controller.signal.aborted) {
                        setError(
                            localizedUnknownErrorMessage(
                                requestError,
                                '用户候选搜索失败，请稍后重试',
                            ),
                        )
                    }
                })
                .finally(() => {
                    if (!controller.signal.aborted) {
                        setUserLoading(false)
                        setBusinessUnitLoading(false)
                    }
                })
        }, 250)
        return () => {
            window.clearTimeout(timer)
            controller.abort()
        }
    }, [
        businessUnitPublicID,
        businessUnitSearch,
        context,
        open,
        selectedAdministrators,
        userSearch,
    ])

    const validateStep = () => {
        if (activeStep === 0) {
            if (!name.trim()) return '请输入项目名称'
            if (!/^[A-Z][A-Z0-9_-]{0,31}$/u.test(key)) {
                return '项目键须以大写字母开头，仅可包含大写字母、数字、下划线和短横线'
            }
            if (!businessUnitPublicID) return '请选择业务单元'
        }
        if (activeStep === 1 && selectedAdministrators.length === 0) {
            return '至少选择一名初始项目管理员'
        }
        if (
            activeStep === 2 &&
            (!/^[a-z0-9][a-z0-9._-]{0,63}$/u.test(defaultQueueKey) ||
                !defaultQueueName.trim())
        ) {
            return '请填写有效的默认队列键和名称'
        }
        return ''
    }

    const nextStep = () => {
        const validationError = validateStep()
        if (validationError) {
            setError(validationError)
            return
        }
        setError('')
        setActiveStep((current) => Math.min(current + 1, 2))
    }

    const submit = async () => {
        const validationError = validateStep()
        if (validationError || submitting) {
            setError(validationError)
            return
        }
        const payload: CreatePlatformProjectRequest = {
            business_unit_public_id: businessUnitPublicID,
            key,
            name: name.trim(),
            description: description.trim(),
            initial_project_admin_user_ids: selectedAdministrators.map(
                ({ id }) => id,
            ),
            default_queue_key: defaultQueueKey,
            default_queue_name: defaultQueueName.trim(),
        }
        setSubmitting(true)
        setError('')
        try {
            const request = buildHumanApiRequest('createPlatformProject', {
                pathParameters: {},
                body: payload,
            })
            const created = await apiFetch<PlatformProjectSummary>(
                request.path,
                {
                    method: request.method,
                    body: JSON.stringify(request.body),
                },
            )
            await onCreated(created)
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '创建项目失败，请检查项目键与初始管理员后重试',
                ),
            )
        } finally {
            setSubmitting(false)
        }
    }

    return (
        <Dialog
            open={open}
            onClose={() => {
                if (!submitting) onClose()
            }}
            fullWidth
            maxWidth="md"
            aria-labelledby="create-project-wizard-title"
        >
            <DialogTitle id="create-project-wizard-title">
                创建项目
            </DialogTitle>
            <DialogContent>
                {!context ? (
                    <Box
                        role="status"
                        aria-label="正在加载项目创建上下文"
                        sx={{ minHeight: 260, display: 'grid', placeItems: 'center' }}
                    >
                        <CircularProgress />
                    </Box>
                ) : (
                    <>
                        <Alert severity="info" sx={{ mb: 2 }}>
                            组织由服务端从认证上下文解析：{context.organization.name}。
                            平台职责不会自动授予新项目访问。
                        </Alert>
                        <Stepper activeStep={activeStep} sx={{ mb: 3 }}>
                            {['项目信息', '初始管理员', '默认队列'].map(
                                (label) => (
                                    <Step key={label}>
                                        <StepLabel>{label}</StepLabel>
                                    </Step>
                                ),
                            )}
                        </Stepper>
                        {error && (
                            <Alert severity="error" sx={{ mb: 2 }}>
                                {error}
                            </Alert>
                        )}
                        {activeStep === 0 && (
                            <Stack spacing={2}>
                                <TextField
                                    label="项目名称"
                                    value={name}
                                    onChange={(event) =>
                                        setName(event.target.value)
                                    }
                                    slotProps={{
                                        htmlInput: { maxLength: 120 },
                                    }}
                                    required
                                    autoFocus
                                />
                                <TextField
                                    label="项目键"
                                    value={key}
                                    onChange={(event) =>
                                        setKey(event.target.value.toUpperCase())
                                    }
                                    helperText="创建后不可修改；用于工单编号与项目路径。"
                                    slotProps={{
                                        htmlInput: { maxLength: 32 },
                                    }}
                                    required
                                />
                                <TextField
                                    label="项目描述"
                                    value={description}
                                    onChange={(event) =>
                                        setDescription(event.target.value)
                                    }
                                    slotProps={{
                                        htmlInput: { maxLength: 500 },
                                    }}
                                    multiline
                                    minRows={3}
                                />
                                <Autocomplete
                                    options={businessUnitOptions}
                                    value={
                                        businessUnitOptions.find(
                                            ({ public_id }) =>
                                                public_id ===
                                                businessUnitPublicID,
                                        ) ?? null
                                    }
                                    loading={businessUnitLoading}
                                    filterOptions={(options) => options}
                                    isOptionEqualToValue={(option, value) =>
                                        option.public_id === value.public_id
                                    }
                                    getOptionLabel={(unit) =>
                                        `${unit.name}（${unit.key}）`
                                    }
                                    onChange={(_, value) => {
                                        setBusinessUnitPublicID(
                                            value?.public_id ?? '',
                                        )
                                        setBusinessUnitSearch(
                                            value
                                                ? `${value.name}（${value.key}）`
                                                : '',
                                        )
                                    }}
                                    inputValue={businessUnitSearch}
                                    onInputChange={(_, value, reason) => {
                                        if (reason !== 'reset') {
                                            setBusinessUnitSearch(value)
                                        }
                                    }}
                                    renderInput={(params) => (
                                        <TextField
                                            {...params}
                                            label="搜索并选择业务单元"
                                            placeholder="输入业务单元名称或键远程搜索"
                                            required
                                        />
                                    )}
                                />
                            </Stack>
                        )}
                        {activeStep === 1 && (
                            <Stack spacing={2}>
                                <Alert severity="warning">
                                    至少保留一名明确的初始项目管理员。创建者已预选，
                                    但可以移除；移除后创建者不会获得该项目访问。
                                </Alert>
                                <Autocomplete
                                    multiple
                                    disableCloseOnSelect
                                    clearOnEscape
                                    limitTags={3}
                                    options={userOptions}
                                    value={selectedAdministrators}
                                    loading={userLoading}
                                    filterOptions={(options) => options}
                                    isOptionEqualToValue={(option, value) =>
                                        option.id === value.id
                                    }
                                    getOptionLabel={userLabel}
                                    onChange={(_, value) => {
                                        setSelectedAdministrators(value)
                                        setUserSearch('')
                                    }}
                                    inputValue={userSearch}
                                    onInputChange={(_, value, reason) => {
                                        if (reason !== 'reset') {
                                            setUserSearch(value)
                                        }
                                    }}
                                    renderOption={(
                                        props,
                                        option,
                                        { selected },
                                    ) => {
                                        const {
                                            key: optionKey,
                                            ...optionProps
                                        } = props
                                        return (
                                            <Box
                                                component="li"
                                                key={optionKey}
                                                {...optionProps}
                                            >
                                                <Checkbox
                                                    checked={selected}
                                                    tabIndex={-1}
                                                    sx={{ mr: 1 }}
                                                    slotProps={{
                                                        input: {
                                                            'aria-label': `选择 ${userLabel(option)}`,
                                                        },
                                                    }}
                                                />
                                                <Avatar
                                                    src={
                                                        option.avatar ||
                                                        undefined
                                                    }
                                                    sx={{
                                                        width: 28,
                                                        height: 28,
                                                        mr: 1,
                                                    }}
                                                >
                                                    {userLabel(option).slice(
                                                        0,
                                                        1,
                                                    )}
                                                </Avatar>
                                                <Box>
                                                    <Typography variant="body2">
                                                        {userLabel(option)}
                                                    </Typography>
                                                    <Typography
                                                        variant="caption"
                                                        color="text.secondary"
                                                    >
                                                        @{option.username}
                                                    </Typography>
                                                </Box>
                                            </Box>
                                        )
                                    }}
                                    renderValue={(value, getItemProps) =>
                                        value.map((option, index) => {
                                            const {
                                                key: itemKey,
                                                ...itemProps
                                            } = getItemProps({ index })
                                            return (
                                                <Chip
                                                    {...itemProps}
                                                    key={itemKey ?? option.id}
                                                    label={userLabel(option)}
                                                    data-testid={`initial-project-admin-${option.id}`}
                                                />
                                            )
                                        })
                                    }
                                    renderInput={(params) => (
                                        <TextField
                                            {...params}
                                            label="搜索并选择初始项目管理员"
                                            placeholder="输入姓名、用户名或邮箱远程搜索"
                                            required
                                        />
                                    )}
                                />
                                <Typography
                                    variant="body2"
                                    color="text.secondary"
                                    aria-live="polite"
                                >
                                    已选择 {selectedAdministrators.length} 项
                                </Typography>
                            </Stack>
                        )}
                        {activeStep === 2 && (
                            <Stack spacing={2}>
                                <TextField
                                    label="默认队列键"
                                    value={defaultQueueKey}
                                    onChange={(event) =>
                                        setDefaultQueueKey(
                                            event.target.value.toLowerCase(),
                                        )
                                    }
                                    helperText="仅可包含小写字母、数字、点、下划线和短横线。"
                                    required
                                />
                                <TextField
                                    label="默认队列名称"
                                    value={defaultQueueName}
                                    onChange={(event) =>
                                        setDefaultQueueName(event.target.value)
                                    }
                                    required
                                />
                                <Paper variant="outlined" sx={{ p: 2 }}>
                                    <Typography variant="subtitle2">
                                        创建摘要
                                    </Typography>
                                    <Typography color="text.secondary">
                                        {name}（{key}）· 初始项目管理员{' '}
                                        {selectedAdministrators.length} 人
                                    </Typography>
                                </Paper>
                            </Stack>
                        )}
                    </>
                )}
            </DialogContent>
            <DialogActions>
                <Button onClick={onClose} disabled={submitting}>
                    取消
                </Button>
                {activeStep > 0 && (
                    <Button
                        onClick={() => {
                            setError('')
                            setActiveStep((current) => current - 1)
                        }}
                        disabled={submitting}
                    >
                        上一步
                    </Button>
                )}
                {activeStep < 2 ? (
                    <Button
                        variant="contained"
                        onClick={nextStep}
                        disabled={!context || submitting}
                    >
                        下一步
                    </Button>
                ) : (
                    <Button
                        variant="contained"
                        onClick={() => void submit()}
                        disabled={!context || submitting}
                    >
                        {submitting ? '创建中…' : '创建项目'}
                    </Button>
                )}
            </DialogActions>
        </Dialog>
    )
}

const PlatformProjectGovernancePage = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const navigate = useNavigate()
    const notify = useNotify()
    const queryClient = useQueryClient()
    const [searchParams, setSearchParams] = useSearchParams()
    const isPlatformAdmin =
        parsePlatformRole(permissions?.platform_role) === 'platform_admin'
    const [projectPage, setProjectPage] =
        React.useState<PlatformProjectPage>(emptyProjectPage)
    const [creationContext, setCreationContext] =
        React.useState<ProjectCreationContext | null>(null)
    const [page, setPage] = React.useState(1)
    const [pageSize, setPageSize] = React.useState(25)
    const [search, setSearch] = React.useState('')
    const [status, setStatus] = React.useState<ProjectStatus | ''>('')
    const [businessUnitPublicID, setBusinessUnitPublicID] =
        React.useState('')
    const [governanceBusinessUnits, setGovernanceBusinessUnits] =
        React.useState<PlatformBusinessUnitSummary[]>([])
    const [
        governanceBusinessUnitSearch,
        setGovernanceBusinessUnitSearch,
    ] = React.useState('')
    const [governanceBusinessUnitLoading, setGovernanceBusinessUnitLoading] =
        React.useState(false)
    const [orderBy, setOrderBy] = React.useState<GovernanceOrderBy>('name')
    const [order, setOrder] = React.useState<GovernanceOrder>('asc')
    const [refreshVersion, setRefreshVersion] = React.useState(0)
    const [loading, setLoading] = React.useState(true)
    const [archiving, setArchiving] = React.useState(false)
    const [error, setError] = React.useState('')
    const [projectToArchive, setProjectToArchive] =
        React.useState<PlatformProjectSummary | null>(null)
    const [projectToInspect, setProjectToInspect] =
        React.useState<PlatformProjectSummary | null>(null)
    const createWizardOpen = searchParams.get('create') === '1'
    const projectToArchiveIsActive =
        projectToArchive !== null &&
        activeProjectKey() === projectToArchive.key

    React.useEffect(() => {
        if (permissionsPending || !isPlatformAdmin) return
        const controller = new AbortController()
        void fetchCreationContext('', '', controller.signal)
            .then((value) => {
                if (!controller.signal.aborted) {
                    setCreationContext(value)
                }
            })
            .catch((requestError: unknown) => {
                if (!controller.signal.aborted) {
                    setError(
                        localizedUnknownErrorMessage(
                            requestError,
                            '项目创建选项加载失败，请稍后重试',
                        ),
                    )
                }
            })
        return () => controller.abort()
    }, [isPlatformAdmin, permissionsPending])

    React.useEffect(() => {
        if (permissionsPending || !isPlatformAdmin) return
        const controller = new AbortController()
        const timer = window.setTimeout(() => {
            setGovernanceBusinessUnitLoading(true)
            void fetchGovernanceBusinessUnits(
                governanceBusinessUnitSearch.trim(),
                controller.signal,
            )
                .then((value) => {
                    if (!controller.signal.aborted) {
                        setGovernanceBusinessUnits((current) =>
                            mergeBusinessUnitOptions(
                                businessUnitPublicID,
                                current,
                                value.items,
                            ),
                        )
                    }
                })
                .catch((requestError: unknown) => {
                    if (!controller.signal.aborted) {
                        setError(
                            localizedUnknownErrorMessage(
                                requestError,
                                '业务单元搜索失败，请稍后重试',
                            ),
                        )
                    }
                })
                .finally(() => {
                    if (!controller.signal.aborted) {
                        setGovernanceBusinessUnitLoading(false)
                    }
                })
        }, 250)
        return () => {
            window.clearTimeout(timer)
            controller.abort()
        }
    }, [
        businessUnitPublicID,
        governanceBusinessUnitSearch,
        isPlatformAdmin,
        permissionsPending,
    ])

    React.useEffect(() => {
        if (permissionsPending || !isPlatformAdmin) {
            setLoading(false)
            return
        }
        const controller = new AbortController()
        const timer = window.setTimeout(() => {
            setLoading(true)
            setError('')
            void fetchPlatformProjects(
                {
                    page,
                    page_size: pageSize,
                    ...(search.trim() ? { search: search.trim() } : {}),
                    ...(status ? { status } : {}),
                    ...(businessUnitPublicID
                        ? { business_unit_public_id: businessUnitPublicID }
                        : {}),
                    order_by: orderBy,
                    order,
                },
                controller.signal,
            )
                .then((value) => {
                    if (controller.signal.aborted) return
                    const lastPage = Math.max(1, value.total_pages)
                    if (page > lastPage) {
                        setProjectPage({
                            ...value,
                            items: [],
                            page: lastPage,
                        })
                        setPage(lastPage)
                        return
                    }
                    setProjectPage(value)
                })
                .catch((requestError: unknown) => {
                    if (!controller.signal.aborted) {
                        setError(
                            localizedUnknownErrorMessage(
                                requestError,
                                '平台项目加载失败，请稍后重试',
                            ),
                        )
                    }
                })
                .finally(() => {
                    if (!controller.signal.aborted) setLoading(false)
                })
        }, 250)
        return () => {
            window.clearTimeout(timer)
            controller.abort()
        }
    }, [
        businessUnitPublicID,
        isPlatformAdmin,
        order,
        orderBy,
        page,
        pageSize,
        permissionsPending,
        refreshVersion,
        search,
        status,
    ])

    const openCreationWizard = () => {
        const next = new URLSearchParams(searchParams)
        next.set('create', '1')
        setSearchParams(next, { replace: true })
    }

    const closeCreationWizard = () => {
        const next = new URLSearchParams(searchParams)
        next.delete('create')
        setSearchParams(next, { replace: true })
    }

    const refreshAuthorizedProjects = async (
        clearSelection: boolean,
    ): Promise<boolean> => {
        if (clearSelection) {
            clearProjectScopeCache()
            return true
        }
        try {
            await refreshAuthorizedProjectInventory()
            return true
        } catch {
            return false
        }
    }

    const handleCreated = async (created: PlatformProjectSummary) => {
        const inventoryRefreshed = await refreshAuthorizedProjects(false)
        closeCreationWizard()
        setRefreshVersion((current) => current + 1)
        notify(`项目“${created.name}”创建成功`, { type: 'success' })
        if (!inventoryRefreshed) {
            notify('项目已创建，但授权项目清单刷新失败，请稍后手动刷新', {
                type: 'warning',
            })
        }
    }

    const archiveProject = async () => {
        if (!projectToArchive || !isPlatformAdmin || archiving) return
        const target = projectToArchive
        if (target.key === 'DEFAULT' || target.status !== 'active') return
        const isActiveProject = activeProjectKey() === target.key
        setArchiving(true)
        setError('')
        try {
            const request = buildHumanApiRequest('archivePlatformProject', {
                pathParameters: {
                    projectPublicID: target.public_id,
                },
            })
            const response = await apiFetch<Response>(
                request.path,
                {
                    method: request.method,
                    rawResponse: true,
                },
            )
            const payload: unknown = await response.json().catch(() => null)
            if (!response.ok) {
                throw new HttpError(
                    localizedApiErrorMessage(payload, response.status),
                    response.status,
                    payload,
                )
            }
            const responseMatches = matchesArchivedProject(payload, target)
            setProjectToArchive(null)
            const inventoryRefreshed =
                await refreshAuthorizedProjects(isActiveProject)
            setRefreshVersion((current) => current + 1)
            if (responseMatches) {
                notify(`项目“${target.name}”已归档`, { type: 'success' })
            } else {
                notify(
                    `项目“${target.name}”的归档请求已成功提交，但响应校验异常，请刷新确认状态`,
                    { type: 'warning' },
                )
            }
            if (!inventoryRefreshed) {
                notify('项目已归档，但授权项目清单刷新失败，请稍后手动刷新', {
                    type: 'warning',
                })
            }
            if (isActiveProject) {
                await queryClient.cancelQueries()
                queryClient.clear()
                navigate('/', { replace: true })
            }
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '归档项目失败，请稍后重试',
                ),
            )
            setProjectToArchive(null)
        } finally {
            setArchiving(false)
        }
    }

    const toggleSort = (field: GovernanceOrderBy) => {
        if (orderBy === field) {
            setOrder((current) => current === 'asc' ? 'desc' : 'asc')
        } else {
            setOrderBy(field)
            setOrder('asc')
        }
        setPage(1)
    }

    if (permissionsPending) {
        return (
            <Box
                role="status"
                aria-label="正在校验平台项目权限"
                sx={{ display: 'grid', minHeight: 280, placeItems: 'center' }}
            >
                <CircularProgress size={32} />
            </Box>
        )
    }

    if (!isPlatformAdmin) {
        return (
            <Alert severity="error" data-testid="platform-project-forbidden">
                仅平台管理员可访问平台项目治理。
            </Alert>
        )
    }

    const sortableHeader = (
        label: string,
        field: GovernanceOrderBy,
    ) => (
        <TableSortLabel
            active={orderBy === field}
            direction={orderBy === field ? order : 'asc'}
            onClick={() => toggleSort(field)}
        >
            {label}
        </TableSortLabel>
    )

    return (
        <Box
            data-testid="platform-project-governance-page"
            sx={{ p: { xs: 2, md: 3 } }}
        >
            <Title title="平台项目治理" />
            <Stack
                direction={{ xs: 'column', sm: 'row' }}
                spacing={2}
                sx={{ justifyContent: 'space-between', mb: 1 }}
            >
                <Box>
                    <Typography variant="h4" gutterBottom>
                        平台项目治理
                    </Typography>
                    <Typography color="text.secondary">
                        搜索、筛选和归档组织内项目不会隐式授予任何项目职责。
                    </Typography>
                </Box>
                <Button
                    variant="contained"
                    onClick={openCreationWizard}
                    data-testid="create-platform-project"
                >
                    创建项目
                </Button>
            </Stack>

            <Alert severity="warning" sx={{ my: 2 }}>
                项目归档会立即撤销业务访问，当前版本不支持恢复。
            </Alert>
            {error && (
                <Alert severity="error" sx={{ mb: 2 }}>
                    {error}
                </Alert>
            )}

            <Paper variant="outlined" sx={{ p: 2, mb: 2 }}>
                <Stack
                    direction={{ xs: 'column', md: 'row' }}
                    spacing={2}
                    sx={{ alignItems: { md: 'center' } }}
                >
                    <TextField
                        label="搜索项目"
                        value={search}
                        onChange={(event) => {
                            setSearch(event.target.value)
                            setPage(1)
                        }}
                        slotProps={{
                            htmlInput: { maxLength: 100 },
                        }}
                        sx={{ minWidth: 260 }}
                    />
                    <FormControl sx={{ minWidth: 160 }}>
                        <InputLabel id="project-status-filter-label">
                            状态
                        </InputLabel>
                        <Select
                            labelId="project-status-filter-label"
                            label="状态"
                            value={status}
                            onChange={(event) => {
                                setStatus(event.target.value as ProjectStatus | '')
                                setPage(1)
                            }}
                        >
                            <MenuItem value="">全部状态</MenuItem>
                            <MenuItem value="active">运行中</MenuItem>
                            <MenuItem value="archived">已归档</MenuItem>
                        </Select>
                    </FormControl>
                    <Autocomplete
                        options={governanceBusinessUnits}
                        value={
                            governanceBusinessUnits.find(
                                ({ public_id }) =>
                                    public_id === businessUnitPublicID,
                            ) ?? null
                        }
                        loading={governanceBusinessUnitLoading}
                        filterOptions={(options) => options}
                        isOptionEqualToValue={(option, value) =>
                            option.public_id === value.public_id
                        }
                        getOptionLabel={(unit) => unit.name}
                        onChange={(_, value) => {
                            setBusinessUnitPublicID(value?.public_id ?? '')
                            setGovernanceBusinessUnitSearch(
                                value?.name ?? '',
                            )
                            setPage(1)
                        }}
                        inputValue={governanceBusinessUnitSearch}
                        onInputChange={(_, value, reason) => {
                            if (reason !== 'reset') {
                                setGovernanceBusinessUnitSearch(value)
                            }
                        }}
                        renderInput={(params) => (
                            <TextField
                                {...params}
                                label="业务单元"
                                placeholder="全部或远程搜索"
                            />
                        )}
                        sx={{ minWidth: 220 }}
                    />
                </Stack>
            </Paper>

            <TableContainer component={Paper} variant="outlined">
                <ResizableMuiTable
                    tableId="platform.projects.governance"
                    columns={platformProjectColumns}
                    aria-label="平台项目治理列表"
                >
                    <TableHead>
                        <TableRow>
                            <TableCell>{sortableHeader('项目', 'name')}</TableCell>
                            <TableCell>{sortableHeader('项目键', 'key')}</TableCell>
                            <TableCell>
                                {sortableHeader('业务单元', 'business_unit')}
                            </TableCell>
                            <TableCell>{sortableHeader('状态', 'status')}</TableCell>
                            <TableCell>
                                {sortableHeader('更新时间', 'updated_at')}
                            </TableCell>
                            <TableCell align="right">平台操作</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={6} align="center">
                                    <CircularProgress
                                        size={24}
                                        aria-label="正在加载平台项目"
                                    />
                                </TableCell>
                            </TableRow>
                        ) : projectPage.items.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={6} align="center">
                                    当前筛选条件下没有项目
                                </TableCell>
                            </TableRow>
                        ) : (
                            projectPage.items.map((project) => (
                                <TableRow key={project.public_id}>
                                    <TableCell>
                                        <InlineDetails
                                            primary={project.name}
                                            secondary={`${project.description || '暂无描述'} · ${project.public_id}`}
                                            title={`${project.name} · ${project.description}`}
                                        />
                                    </TableCell>
                                    <TableCell>{project.key}</TableCell>
                                    <TableCell>
                                        <InlineDetails
                                            primary={project.business_unit.name}
                                            secondary={project.business_unit.key}
                                            title={`${project.business_unit.name} · ${project.business_unit.key}`}
                                        />
                                    </TableCell>
                                    <TableCell>
                                        <Chip
                                            size="small"
                                            label={
                                                project.status === 'active'
                                                    ? '运行中'
                                                    : '已归档'
                                            }
                                            color={
                                                project.status === 'active'
                                                    ? 'success'
                                                    : 'default'
                                            }
                                        />
                                    </TableCell>
                                    <TableCell>
                                        {new Date(project.updated_at)
                                            .toLocaleString('zh-CN')}
                                    </TableCell>
                                    <TableCell align="right">
                                        <Stack
                                            direction="row"
                                            spacing={1}
                                            sx={{
                                                alignItems: 'center',
                                                justifyContent: 'flex-end',
                                            }}
                                        >
                                            <Button
                                                size="small"
                                                onClick={() =>
                                                    setProjectToInspect(project)
                                                }
                                            >
                                                查看详情
                                            </Button>
                                            {project.status === 'archived' ? (
                                                <Typography
                                                    variant="body2"
                                                    color="text.secondary"
                                                >
                                                    已归档，当前版本暂不支持恢复
                                                </Typography>
                                            ) : (
                                                <Button
                                                    size="small"
                                                    color="error"
                                                    variant="outlined"
                                                    disabled={
                                                        archiving ||
                                                        project.key === 'DEFAULT'
                                                    }
                                                    data-testid={`archive-platform-project-${project.public_id}`}
                                                    onClick={() =>
                                                        setProjectToArchive(project)
                                                    }
                                                >
                                                    归档项目
                                                </Button>
                                            )}
                                        </Stack>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </ResizableMuiTable>
                <TablePagination
                    component="div"
                    count={projectPage.total}
                    page={Math.max(0, projectPage.page - 1)}
                    onPageChange={(_, nextPage) => setPage(nextPage + 1)}
                    rowsPerPage={projectPage.page_size}
                    onRowsPerPageChange={(event) => {
                        setPageSize(Number(event.target.value))
                        setPage(1)
                    }}
                    rowsPerPageOptions={[25, 50, 100]}
                    labelRowsPerPage="每页"
                />
            </TableContainer>

            <ProjectCreationWizard
                context={creationContext}
                open={createWizardOpen}
                onClose={closeCreationWizard}
                onCreated={handleCreated}
            />

            <Dialog
                open={projectToInspect !== null}
                onClose={() => setProjectToInspect(null)}
                aria-labelledby="platform-project-details-title"
            >
                <DialogTitle id="platform-project-details-title">
                    项目详情
                </DialogTitle>
                <DialogContent>
                    <Stack spacing={1}>
                        <Typography>
                            名称：{projectToInspect?.name}
                        </Typography>
                        <Typography>
                            项目键：{projectToInspect?.key}（不可修改）
                        </Typography>
                        <Typography>
                            业务单元：{projectToInspect?.business_unit.name}
                        </Typography>
                        <Typography>
                            描述：{projectToInspect?.description || '暂无'}
                        </Typography>
                        {projectToInspect?.status === 'archived' && (
                            <Alert severity="info">
                                已归档，当前版本暂不支持恢复
                            </Alert>
                        )}
                    </Stack>
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setProjectToInspect(null)}>
                        关闭
                    </Button>
                </DialogActions>
            </Dialog>

            <Dialog
                open={projectToArchive !== null}
                onClose={() => {
                    if (!archiving) setProjectToArchive(null)
                }}
                aria-labelledby="archive-platform-project-title"
            >
                <DialogTitle id="archive-platform-project-title">
                    确认归档项目
                </DialogTitle>
                <DialogContent>
                    <DialogContentText>
                        确认归档“{projectToArchive?.name}”（
                        {projectToArchive?.key}
                        ）吗？归档后将立即撤销该项目的业务访问。
                        {projectToArchiveIsActive
                            ? '这是当前项目，系统将清除当前项目与页面缓存。'
                            : '当前项目选择不会改变，但授权项目清单会立即刷新。'}
                    </DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button
                        onClick={() => setProjectToArchive(null)}
                        disabled={archiving}
                    >
                        取消
                    </Button>
                    <Button
                        color="error"
                        variant="contained"
                        onClick={() => void archiveProject()}
                        disabled={archiving}
                    >
                        {archiving ? '归档中…' : '确认归档'}
                    </Button>
                </DialogActions>
            </Dialog>
        </Box>
    )
}

export default PlatformProjectGovernancePage
