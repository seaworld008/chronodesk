import React from 'react'
import {
    Title,
    useNotify,
    usePermissions,
} from 'react-admin'
import {
    Alert,
    Autocomplete,
    Avatar,
    Box,
    Button,
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
    TableBody,
    TableCell,
    TableContainer,
    TableHead,
    TableRow,
    TextField,
    Typography,
} from '@mui/material'
import {
    humanApiRoutes,
    type ProjectMembership,
    type ProjectRole,
    type ProjectUserOption,
    type ProjectUserOptionPage,
    type UpsertProjectMembershipRequest,
} from '@/lib/generated/human-api'
import type { AccessPermissions } from '@/lib/accessControl'
import {
    getProjectRoleLabel,
    parseProjectRole,
    projectRoleValues,
    resolveActiveProjectKey,
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

const projectMembershipColumns: ResizableColumn[] = [
    { key: 'user', defaultWidth: 320, minWidth: 240, maxWidth: 520 },
    { key: 'role', defaultWidth: 160, minWidth: 128, maxWidth: 240 },
    { key: 'status', defaultWidth: 112, minWidth: 96, maxWidth: 160 },
    {
        key: 'actions',
        defaultWidth: 260,
        minWidth: 220,
        maxWidth: 360,
        sticky: 'right',
    },
]

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const parseMemberships = (value: unknown): ProjectMembership[] => {
    if (!Array.isArray(value)) {
        throw new Error('项目成员响应格式无效')
    }
    return value.map((item) => {
        if (
            !isRecord(item) ||
            typeof item.id !== 'number' ||
            typeof item.project_id !== 'number' ||
            typeof item.user_id !== 'number' ||
            parseProjectRole(item.role) === null ||
            typeof item.is_active !== 'boolean' ||
            typeof item.version !== 'number' ||
            typeof item.created_at !== 'string' ||
            typeof item.updated_at !== 'string'
        ) {
            throw new Error('项目成员响应包含无效字段')
        }
        return item as ProjectMembership
    })
}

const roleChoices = projectRoleValues.map((role) => ({
    id: role,
    name: getProjectRoleLabel(role),
}))

const userOptionFromMembership = (
    membership: ProjectMembership,
): ProjectUserOption | null => {
    if (!membership.user) return null
    return {
        id: membership.user.id,
        username: membership.user.username,
        display_name: membership.user.display_name,
        avatar: membership.user.avatar,
    }
}

const ProjectMembershipPage = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const notify = useNotify()
    const projectRole = parseProjectRole(permissions?.project_role)
    const isProjectAdmin = projectRole === 'project_admin'
    const canRead = isProjectAdmin || projectRole === 'manager'
    const [memberships, setMemberships] = React.useState<ProjectMembership[]>([])
    const [loading, setLoading] = React.useState(true)
    const [saving, setSaving] = React.useState(false)
    const [error, setError] = React.useState('')
    const [selectedUser, setSelectedUser] =
        React.useState<ProjectUserOption | null>(null)
    const [candidateOptions, setCandidateOptions] =
        React.useState<ProjectUserOption[]>([])
    const [candidateSearch, setCandidateSearch] = React.useState('')
    const [candidateLoading, setCandidateLoading] = React.useState(false)
    const [role, setRole] = React.useState<ProjectRole>('requester')
    const [membershipToRevoke, setMembershipToRevoke] =
        React.useState<ProjectMembership | null>(null)

    const loadMemberships = React.useCallback(async () => {
        if (!canRead) return
        setLoading(true)
        setError('')
        try {
            const path = humanApiRoutes.listProjectMemberships({
                projectKey: await resolveActiveProjectKey(),
            })
            const response = await apiFetch<unknown>(path)
            setMemberships(parseMemberships(response))
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '项目成员加载失败，请稍后重试',
                ),
            )
        } finally {
            setLoading(false)
        }
    }, [canRead])

    React.useEffect(() => {
        if (!permissionsPending && canRead) {
            void loadMemberships()
        } else if (!permissionsPending) {
            setLoading(false)
        }
    }, [canRead, loadMemberships, permissionsPending])

    React.useEffect(() => {
        if (!isProjectAdmin) return
        const controller = new AbortController()
        const timer = window.setTimeout(() => {
            setCandidateLoading(true)
            void resolveActiveProjectKey()
                .then((projectKey) =>
                    apiFetch<ProjectUserOptionPage>(
                        humanApiRoutes.searchProjectMembershipCandidates(
                            { projectKey },
                            {
                                page: 1,
                                page_size: 25,
                                ...(candidateSearch.trim()
                                    ? { search: candidateSearch.trim() }
                                    : {}),
                            },
                        ),
                        {
                            method: 'GET',
                            signal: controller.signal,
                        },
                    ),
                )
                .then((page) => {
                    if (!controller.signal.aborted) {
                        const options = selectedUser
                            ? [
                                selectedUser,
                                ...page.items.filter(
                                    ({ id }) => id !== selectedUser.id,
                                ),
                            ]
                            : page.items
                        setCandidateOptions(options)
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
                        setCandidateLoading(false)
                    }
                })
        }, 250)
        return () => {
            window.clearTimeout(timer)
            controller.abort()
        }
    }, [candidateSearch, isProjectAdmin, selectedUser])

    const saveMembership = async (event: React.FormEvent) => {
        event.preventDefault()
        if (!selectedUser) {
            setError('请通过远程搜索选择用户')
            return
        }
        const payload: UpsertProjectMembershipRequest = {
            user_id: selectedUser.id,
            role,
        }
        setSaving(true)
        setError('')
        try {
            const path = humanApiRoutes.upsertProjectMembership({
                projectKey: await resolveActiveProjectKey(),
            })
            await apiFetch(path, {
                method: 'POST',
                body: JSON.stringify(payload),
            })
            notify('项目成员关系已保存', { type: 'success' })
            setSelectedUser(null)
            setCandidateSearch('')
            setRole('requester')
            await loadMemberships()
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '保存项目成员关系失败',
                ),
            )
        } finally {
            setSaving(false)
        }
    }

    const revokeMembership = async () => {
        if (!membershipToRevoke) return
        setSaving(true)
        setError('')
        try {
            const path = humanApiRoutes.deactivateProjectMembership({
                projectKey: await resolveActiveProjectKey(),
                userID: membershipToRevoke.user_id,
            })
            await apiFetch(path, { method: 'DELETE' })
            notify('项目职责已撤销', { type: 'success' })
            setMembershipToRevoke(null)
            await loadMemberships()
        } catch (requestError) {
            setError(
                localizedUnknownErrorMessage(
                    requestError,
                    '撤销项目职责失败',
                ),
            )
        } finally {
            setSaving(false)
        }
    }

    if (permissionsPending) {
        return (
            <Box sx={{ p: 4, display: 'grid', placeItems: 'center' }}>
                <CircularProgress aria-label="正在校验项目职责" />
            </Box>
        )
    }

    if (!canRead) {
        return (
            <Alert severity="error" data-testid="project-membership-forbidden">
                仅项目管理员或项目经理可查看项目成员。
            </Alert>
        )
    }

    return (
        <Box data-testid="project-membership-page" sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="项目成员管理" />
            <Typography variant="h4" gutterBottom>
                项目成员管理
            </Typography>
            <Typography color="text.secondary" sx={{ mb: 3 }}>
                项目经理可查看成员；只有项目管理员可授予、变更或撤销项目职责。
            </Typography>

            {error && (
                <Alert severity="error" sx={{ mb: 2 }}>
                    {error}
                </Alert>
            )}
            {!isProjectAdmin && (
                <Alert
                    severity="info"
                    sx={{ mb: 2 }}
                    data-testid="project-membership-read-only"
                >
                    当前为只读视图。请联系项目管理员变更成员职责。
                </Alert>
            )}

            {isProjectAdmin && (
                <Paper
                    component="form"
                    onSubmit={saveMembership}
                    variant="outlined"
                    sx={{ p: 3, mb: 3 }}
                >
                    <Typography variant="h6" gutterBottom>
                        授予项目职责
                    </Typography>
                    <Stack
                        direction={{ xs: 'column', md: 'row' }}
                        spacing={2}
                        sx={{ alignItems: { md: 'center' } }}
                    >
                        <Autocomplete
                            options={candidateOptions}
                            value={selectedUser}
                            loading={candidateLoading}
                            filterOptions={(options) => options}
                            isOptionEqualToValue={(option, value) =>
                                option.id === value.id
                            }
                            getOptionLabel={(option) =>
                                option.display_name || option.username
                            }
                            onChange={(_, value) => {
                                setSelectedUser(value)
                                setCandidateSearch(
                                    value
                                        ? value.display_name || value.username
                                        : '',
                                )
                            }}
                            inputValue={candidateSearch}
                            onInputChange={(_, value, reason) => {
                                if (reason !== 'reset') {
                                    setCandidateSearch(value)
                                }
                            }}
                            renderOption={(props, option) => {
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
                                        <Avatar
                                            src={option.avatar || undefined}
                                            sx={{
                                                width: 28,
                                                height: 28,
                                                mr: 1,
                                            }}
                                        >
                                            {(option.display_name ||
                                                option.username).slice(0, 1)}
                                        </Avatar>
                                        <Box>
                                            <Typography variant="body2">
                                                {option.display_name ||
                                                    option.username}
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
                            renderInput={(params) => (
                                <TextField
                                    {...params}
                                    label="搜索用户"
                                    placeholder="输入姓名、用户名或邮箱远程搜索"
                                    required
                                />
                            )}
                            sx={{ minWidth: { md: 320 }, flex: 1 }}
                        />
                        <FormControl sx={{ minWidth: 220 }}>
                            <InputLabel id="project-role-label">
                                项目职责
                            </InputLabel>
                            <Select
                                labelId="project-role-label"
                                label="项目职责"
                                value={role}
                                onChange={(event) =>
                                    setRole(event.target.value as ProjectRole)
                                }
                            >
                                {roleChoices.map((choice) => (
                                    <MenuItem key={choice.id} value={choice.id}>
                                        {choice.name}
                                    </MenuItem>
                                ))}
                            </Select>
                        </FormControl>
                        <Button
                            type="submit"
                            variant="contained"
                            disabled={saving || !selectedUser}
                        >
                            {saving ? '保存中…' : '保存成员关系'}
                        </Button>
                    </Stack>
                </Paper>
            )}

            <TableContainer component={Paper} variant="outlined">
                <ResizableMuiTable
                    tableId="projects.memberships"
                    columns={projectMembershipColumns}
                    aria-label="项目成员列表"
                >
                    <TableHead>
                        <TableRow>
                            <TableCell>用户</TableCell>
                            <TableCell>项目职责</TableCell>
                            <TableCell>状态</TableCell>
                            <TableCell align="right">操作</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {loading ? (
                            <TableRow>
                                <TableCell colSpan={4} align="center">
                                    <CircularProgress
                                        size={24}
                                        aria-label="正在加载项目成员"
                                    />
                                </TableCell>
                            </TableRow>
                        ) : memberships.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={4} align="center">
                                    暂无项目成员
                                </TableCell>
                            </TableRow>
                        ) : (
                            memberships.map((membership) => {
                                const displayName =
                                    membership.user?.display_name ||
                                    membership.user?.username ||
                                    '用户信息不可用'
                                return (
                                    <TableRow key={membership.id}>
                                        <TableCell>
                                            <InlineDetails
                                                primary={displayName}
                                                secondary={
                                                    membership.user
                                                        ? `@${membership.user.username}`
                                                        : '无法加载用户摘要'
                                                }
                                                title={displayName}
                                            />
                                        </TableCell>
                                        <TableCell>
                                            {getProjectRoleLabel(
                                                membership.role,
                                            )}
                                        </TableCell>
                                        <TableCell>
                                            {membership.is_active
                                                ? '有效'
                                                : '已撤销'}
                                        </TableCell>
                                        <TableCell align="right">
                                            {isProjectAdmin ? (
                                                <Stack
                                                    direction="row"
                                                    spacing={1}
                                                    sx={{
                                                        justifyContent:
                                                            'flex-end',
                                                    }}
                                                >
                                                    <Button
                                                        size="small"
                                                        disabled={
                                                            !membership.is_active ||
                                                            !membership.user
                                                        }
                                                        onClick={() => {
                                                            const option =
                                                                userOptionFromMembership(
                                                                    membership,
                                                                )
                                                            setSelectedUser(option)
                                                            setCandidateSearch(
                                                                option
                                                                    ? option.display_name ||
                                                                        option.username
                                                                    : '',
                                                            )
                                                            setRole(
                                                                membership.role,
                                                            )
                                                        }}
                                                    >
                                                        变更职责
                                                    </Button>
                                                    <Button
                                                        size="small"
                                                        color="error"
                                                        disabled={
                                                            !membership.is_active ||
                                                            saving
                                                        }
                                                        onClick={() =>
                                                            setMembershipToRevoke(
                                                                membership,
                                                            )
                                                        }
                                                    >
                                                        撤销项目职责
                                                    </Button>
                                                </Stack>
                                            ) : (
                                                <Typography
                                                    variant="body2"
                                                    color="text.secondary"
                                                >
                                                    只读
                                                </Typography>
                                            )}
                                        </TableCell>
                                    </TableRow>
                                )
                            })
                        )}
                    </TableBody>
                </ResizableMuiTable>
            </TableContainer>

            <Dialog
                open={membershipToRevoke !== null}
                onClose={() => setMembershipToRevoke(null)}
            >
                <DialogTitle>撤销项目职责</DialogTitle>
                <DialogContent>
                    <DialogContentText>
                        撤销后，该用户将立即失去当前项目的访问权限。
                        此操作会保留不可变的成员关系历史。
                    </DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setMembershipToRevoke(null)}>
                        取消
                    </Button>
                    <Button
                        color="error"
                        variant="contained"
                        disabled={saving}
                        onClick={() => void revokeMembership()}
                    >
                        确认撤销
                    </Button>
                </DialogActions>
            </Dialog>
        </Box>
    )
}

export default ProjectMembershipPage
