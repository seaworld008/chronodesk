import React from 'react'
import {
    Title,
    useNotify,
    usePermissions,
} from 'react-admin'
import { useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
    Alert,
    Box,
    Button,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    Paper,
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TableRow,
    Typography,
} from '@mui/material'
import {
    buildHumanApiRequest,
    humanApiRoutes,
    type PlatformProjectSummary,
} from '@/lib/generated/human-api'
import {
    parsePlatformRole,
    type AccessPermissions,
} from '@/lib/accessControl'
import {
    activeProjectKey,
    clearProjectScopeCache,
} from '@/lib/projectScope'
import {
    apiFetch,
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    InlineDetails,
    ResizableMuiTable,
    type ResizableColumn,
} from '@/components/tables/EnterpriseTable'

const platformProjectColumns: ResizableColumn[] = [
    { key: 'project', defaultWidth: 360, minWidth: 240, maxWidth: 560 },
    { key: 'key', defaultWidth: 160, minWidth: 112, maxWidth: 240 },
    { key: 'status', defaultWidth: 120, minWidth: 96, maxWidth: 180 },
    {
        key: 'actions',
        defaultWidth: 168,
        minWidth: 144,
        maxWidth: 220,
        sticky: 'right',
    },
]

const fetchPlatformProjects = (signal?: AbortSignal) =>
    apiFetch<PlatformProjectSummary[]>(
        humanApiRoutes.listPlatformProjects(),
        {
            method: 'GET',
            signal,
        },
    )

const PlatformProjectGovernancePage = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const navigate = useNavigate()
    const notify = useNotify()
    const queryClient = useQueryClient()
    const isPlatformAdmin =
        parsePlatformRole(permissions?.platform_role) === 'platform_admin'
    const [projects, setProjects] = React.useState<PlatformProjectSummary[]>([])
    const [loading, setLoading] = React.useState(true)
    const [archiving, setArchiving] = React.useState(false)
    const [error, setError] = React.useState('')
    const [projectToArchive, setProjectToArchive] =
        React.useState<PlatformProjectSummary | null>(null)
    const projectToArchiveIsActive =
        projectToArchive !== null &&
        activeProjectKey() === projectToArchive.key

    React.useEffect(() => {
        if (permissionsPending) return
        if (!isPlatformAdmin) {
            setLoading(false)
            return
        }
        const controller = new AbortController()
        setLoading(true)
        setError('')
        void fetchPlatformProjects(controller.signal)
            .then((platformProjects) => {
                if (!controller.signal.aborted) {
                    setProjects(platformProjects)
                }
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
        return () => {
            controller.abort()
        }
    }, [isPlatformAdmin, permissionsPending])

    const archiveProject = async () => {
        if (!projectToArchive || !isPlatformAdmin || archiving) return
        const target = projectToArchive
        if (target.key === 'DEFAULT') return
        const isActiveProject = activeProjectKey() === target.key
        setArchiving(true)
        setError('')
        try {
            const request = buildHumanApiRequest('archivePlatformProject', {
                pathParameters: {
                    projectPublicID: target.public_id,
                },
            })
            const archived = await apiFetch<PlatformProjectSummary>(
                request.path,
                { method: request.method },
            )
            if (
                archived.public_id !== target.public_id ||
                archived.status !== 'archived'
            ) {
                throw new Error('项目归档响应与目标项目不一致')
            }

            setProjectToArchive(null)
            if (isActiveProject) {
                await queryClient.cancelQueries()
                clearProjectScopeCache()
                queryClient.clear()
                notify(`项目“${archived.name}”已归档`, {
                    type: 'success',
                })
                navigate('/', { replace: true })
                return
            }

            try {
                setProjects(await fetchPlatformProjects())
            } catch (refreshError) {
                setError(
                    localizedUnknownErrorMessage(
                        refreshError,
                        '项目已归档，但治理列表刷新失败，请重新进入此页面',
                    ),
                )
            }
            notify(`项目“${archived.name}”已归档`, { type: 'success' })
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

    if (permissionsPending || loading) {
        return (
            <Box
                role="status"
                aria-label="正在加载平台项目"
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

    return (
        <Box
            data-testid="platform-project-governance-page"
            sx={{ p: { xs: 2, md: 3 } }}
        >
            <Title title="平台项目治理" />
            <Typography variant="h4" gutterBottom>
                平台项目治理
            </Typography>
            <Typography color="text.secondary" sx={{ mb: 2 }}>
                此处展示组织内全部项目的窄治理摘要。查看或归档项目不会隐式授予任何项目业务访问或职责。
            </Typography>
            <Alert severity="warning" sx={{ mb: 3 }}>
                项目归档后，该项目的业务访问立即失效。仅当归档当前项目时，系统才会清除当前项目选择与页面缓存；归档其他项目不会改变当前选择。
            </Alert>

            {error && (
                <Alert severity="error" sx={{ mb: 2 }}>
                    {error}
                </Alert>
            )}

            <TableContainer component={Paper} variant="outlined">
                <ResizableMuiTable
                    tableId="platform.projects.governance"
                    columns={platformProjectColumns}
                    aria-label="平台项目治理列表"
                >
                    <TableHead>
                        <TableRow>
                            <TableCell>项目</TableCell>
                            <TableCell>项目键</TableCell>
                            <TableCell>状态</TableCell>
                            <TableCell align="right">平台操作</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {projects.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={4} align="center">
                                    当前没有平台项目
                                </TableCell>
                            </TableRow>
                        ) : (
                            projects.map((project) => (
                                <TableRow key={project.public_id}>
                                    <TableCell>
                                        <InlineDetails
                                            primary={project.name}
                                            secondary={project.public_id}
                                            title={`${project.name} · ${project.public_id}`}
                                        />
                                    </TableCell>
                                    <TableCell>{project.key}</TableCell>
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
                                    <TableCell align="right">
                                        <Button
                                            color="error"
                                            variant="outlined"
                                            disabled={
                                                archiving ||
                                                project.status !== 'active' ||
                                                project.key === 'DEFAULT'
                                            }
                                            data-testid={`archive-platform-project-${project.public_id}`}
                                            onClick={() =>
                                                setProjectToArchive(project)
                                            }
                                        >
                                            归档项目
                                        </Button>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </ResizableMuiTable>
            </TableContainer>

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
                            : '当前项目选择不会改变，治理列表将在归档后刷新。'}
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
