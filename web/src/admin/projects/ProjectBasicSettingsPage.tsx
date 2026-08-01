import React from 'react'
import { Title } from 'react-admin'
import {
    Alert,
    Box,
    Chip,
    CircularProgress,
    Divider,
    Paper,
    Stack,
    Typography,
} from '@mui/material'

import {
    localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
    getProjectRoleLabel,
    resolveActiveProjectAccess,
} from '@/lib/projectScope'
import type {
    AuthorizedProject as AuthorizedProjectSummary,
} from '@/lib/generated/human-api'
import { projectScopeChangedEvent } from '@/lib/projectScopeEvents'

const ProjectBasicSettingsPage = () => {
    const [project, setProject] =
        React.useState<AuthorizedProjectSummary | null>(null)
    const [role, setRole] = React.useState('')
    const [loading, setLoading] = React.useState(true)
    const [error, setError] = React.useState('')
    const [scopeVersion, setScopeVersion] = React.useState(0)

    React.useEffect(() => {
        const reload = () => setScopeVersion((current) => current + 1)
        window.addEventListener(projectScopeChangedEvent, reload)
        return () => window.removeEventListener(projectScopeChangedEvent, reload)
    }, [])

    React.useEffect(() => {
        let active = true
        setLoading(true)
        setError('')
        void resolveActiveProjectAccess()
            .then((access) => {
                if (!active) return
                setProject(access.project)
                setRole(getProjectRoleLabel(access.project_role))
            })
            .catch((requestError: unknown) => {
                if (!active) return
                setError(
                    localizedUnknownErrorMessage(
                        requestError,
                        '项目基本信息加载失败，请稍后重试',
                    ),
                )
            })
            .finally(() => {
                if (active) setLoading(false)
            })
        return () => {
            active = false
        }
    }, [scopeVersion])

    return (
        <Box sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="项目基本信息" />
            <Typography variant="h4" gutterBottom>
                基本信息
            </Typography>
            <Typography color="text.secondary" sx={{ mb: 2 }}>
                当前项目的身份、用途和生命周期边界。Project Key 创建后不可修改。
            </Typography>

            {loading && (
                <Box
                    role="status"
                    aria-label="正在加载项目基本信息"
                    sx={{ display: 'grid', minHeight: 240, placeItems: 'center' }}
                >
                    <CircularProgress size={32} />
                </Box>
            )}
            {error && <Alert severity="error">{error}</Alert>}
            {!loading && !error && project && (
                <Paper variant="outlined" sx={{ p: 3, maxWidth: 760 }}>
                    <Stack spacing={2}>
                        <Box>
                            <Typography variant="overline" color="text.secondary">
                                项目名称
                            </Typography>
                            <Typography variant="h6">{project.name}</Typography>
                        </Box>
                        <Divider />
                        <Stack
                            direction={{ xs: 'column', sm: 'row' }}
                            spacing={4}
                        >
                            <Box sx={{ minWidth: 180 }}>
                                <Typography variant="overline" color="text.secondary">
                                    Project Key
                                </Typography>
                                <Typography sx={{ fontFamily: 'monospace' }}>
                                    {project.key}
                                </Typography>
                            </Box>
                            <Box>
                                <Typography variant="overline" color="text.secondary">
                                    状态
                                </Typography>
                                <Box>
                                    <Chip
                                        size="small"
                                        color={
                                            project.status === 'active'
                                                ? 'success'
                                                : 'default'
                                        }
                                        label={
                                            project.status === 'active'
                                                ? '运行中'
                                                : '已归档'
                                        }
                                    />
                                </Box>
                            </Box>
                            <Box>
                                <Typography variant="overline" color="text.secondary">
                                    我的项目职责
                                </Typography>
                                <Typography>{role}</Typography>
                            </Box>
                        </Stack>
                        <Divider />
                        <Box>
                            <Typography variant="overline" color="text.secondary">
                                项目说明
                            </Typography>
                            <Typography sx={{ whiteSpace: 'pre-wrap' }}>
                                {project.description || '暂未填写项目说明'}
                            </Typography>
                        </Box>
                        {project.status !== 'active' && (
                            <Alert severity="info">
                                项目已归档，当前版本暂不支持恢复。
                            </Alert>
                        )}
                    </Stack>
                </Paper>
            )}
        </Box>
    )
}

export default ProjectBasicSettingsPage
