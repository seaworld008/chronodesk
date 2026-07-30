import React from 'react'
import {
    Title,
    useNotify,
    usePermissions,
} from 'react-admin'
import {
    Alert,
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
    Table,
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

const ProjectMembershipPage = () => {
    const { permissions, isPending: permissionsPending } =
        usePermissions<AccessPermissions>()
    const notify = useNotify()
    const isProjectAdmin =
        parseProjectRole(permissions?.project_role) === 'project_admin'
    const [memberships, setMemberships] = React.useState<ProjectMembership[]>([])
    const [loading, setLoading] = React.useState(true)
    const [saving, setSaving] = React.useState(false)
    const [error, setError] = React.useState('')
    const [userID, setUserID] = React.useState('')
    const [role, setRole] = React.useState<ProjectRole>('requester')
    const [membershipToRevoke, setMembershipToRevoke] =
        React.useState<ProjectMembership | null>(null)

    const loadMemberships = React.useCallback(async () => {
        if (!isProjectAdmin) return
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
    }, [isProjectAdmin])

    React.useEffect(() => {
        if (!permissionsPending && isProjectAdmin) {
            void loadMemberships()
        } else if (!permissionsPending) {
            setLoading(false)
        }
    }, [isProjectAdmin, loadMemberships, permissionsPending])

    const saveMembership = async (event: React.FormEvent) => {
        event.preventDefault()
        const numericUserID = Number(userID)
        if (!Number.isSafeInteger(numericUserID) || numericUserID <= 0) {
            setError('请输入有效的用户 ID')
            return
        }
        const payload: UpsertProjectMembershipRequest = {
            user_id: numericUserID,
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
            setUserID('')
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

    if (!isProjectAdmin) {
        return (
            <Alert severity="error" data-testid="project-membership-forbidden">
                仅项目管理员可访问项目成员管理。
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
                项目职责仅在当前项目内生效。平台职责不会自动授予任何项目访问权限。
            </Typography>

            {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}

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
                    <TextField
                        label="用户 ID"
                        value={userID}
                        onChange={(event) => setUserID(event.target.value)}
                        inputMode="numeric"
                        required
                    />
                    <FormControl sx={{ minWidth: 220 }}>
                        <InputLabel id="project-role-label">项目职责</InputLabel>
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
                        disabled={saving}
                    >
                        {saving ? '保存中…' : '保存成员关系'}
                    </Button>
                </Stack>
            </Paper>

            <TableContainer component={Paper} variant="outlined">
                <Table aria-label="项目成员列表">
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
                            memberships.map((membership) => (
                                <TableRow key={membership.id}>
                                    <TableCell>
                                        <Typography sx={{ fontWeight: 600 }}>
                                            {membership.user?.display_name ||
                                                membership.user?.username ||
                                                `用户 ${membership.user_id}`}
                                        </Typography>
                                        <Typography
                                            variant="body2"
                                            color="text.secondary"
                                        >
                                            ID {membership.user_id}
                                        </Typography>
                                    </TableCell>
                                    <TableCell>
                                        {getProjectRoleLabel(membership.role)}
                                    </TableCell>
                                    <TableCell>
                                        {membership.is_active
                                            ? '有效'
                                            : '已撤销'}
                                    </TableCell>
                                    <TableCell align="right">
                                        <Stack
                                            direction="row"
                                            spacing={1}
                                            sx={{ justifyContent: 'flex-end' }}
                                        >
                                            <Button
                                                size="small"
                                                disabled={!membership.is_active}
                                                onClick={() => {
                                                    setUserID(
                                                        String(
                                                            membership.user_id,
                                                        ),
                                                    )
                                                    setRole(membership.role)
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
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>
            </TableContainer>

            <Dialog
                open={membershipToRevoke !== null}
                onClose={() => setMembershipToRevoke(null)}
            >
                <DialogTitle>撤销项目职责</DialogTitle>
                <DialogContent>
                    <DialogContentText>
                        撤销后，该用户将立即失去当前项目的访问权限。此操作会保留不可变的成员关系历史。
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
