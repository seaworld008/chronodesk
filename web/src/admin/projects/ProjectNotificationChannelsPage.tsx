import { Title, usePermissions } from 'react-admin'
import { useNavigate } from 'react-router-dom'
import {
    Alert,
    Box,
    Button,
    Card,
    CardActions,
    CardContent,
    Chip,
    Stack,
    Typography,
} from '@mui/material'

import {
    hasProjectCapability,
    parseProjectRole,
} from '@/lib/projectScope'
import type { AccessPermissions } from '@/lib/accessControl'

const ProjectNotificationChannelsPage = () => {
    const navigate = useNavigate()
    const { permissions } = usePermissions<AccessPermissions>()
    const role = parseProjectRole(permissions?.project_role)
    const canManageIntegrations = role !== null &&
        hasProjectCapability(role, 'manage_integrations')
    const canViewIntegrations = role !== null &&
        hasProjectCapability(role, 'view_integrations')

    return (
        <Box sx={{ p: { xs: 2, md: 3 } }}>
            <Title title="项目通知渠道" />
            <Typography variant="h4" gutterBottom>
                通知与外发
            </Typography>
            <Typography color="text.secondary" sx={{ mb: 2 }}>
                这里说明项目通知的职责边界；渠道密钥和投递日志仍在各自的专用页面管理，避免重复配置。
            </Typography>
            <Alert severity="info" sx={{ mb: 2 }}>
                邮件服务器属于系统级公共配置；Webhook 和投递监控属于当前项目集成。项目设置不会复制或覆盖平台密钥。
            </Alert>
            <Stack
                direction={{ xs: 'column', lg: 'row' }}
                spacing={2}
                sx={{ alignItems: 'stretch' }}
            >
                <Card variant="outlined" sx={{ flex: 1 }}>
                    <CardContent>
                        <Chip size="small" color="success" label="默认启用" />
                        <Typography variant="h6" sx={{ mt: 1 }}>
                            站内通知
                        </Typography>
                        <Typography color="text.secondary">
                            工单分配、状态变化和 SLA 事件写入收件人的通知中心，并按收件人分页读取。
                        </Typography>
                    </CardContent>
                </Card>
                <Card variant="outlined" sx={{ flex: 1 }}>
                    <CardContent>
                        <Chip size="small" label="继承系统配置" />
                        <Typography variant="h6" sx={{ mt: 1 }}>
                            邮件外发
                        </Typography>
                        <Typography color="text.secondary">
                            项目只消费平台批准的邮件通道，不读取 SMTP 凭据；个人接收偏好由用户本人管理。
                        </Typography>
                    </CardContent>
                </Card>
                <Card variant="outlined" sx={{ flex: 1 }}>
                    <CardContent>
                        <Chip size="small" color="primary" label="项目级" />
                        <Typography variant="h6" sx={{ mt: 1 }}>
                            Webhook 与事件投递
                        </Typography>
                        <Typography color="text.secondary">
                            事件订阅、签名密钥、失败重试和 Outbox 监控都绑定当前项目作用域。
                        </Typography>
                    </CardContent>
                    {(canManageIntegrations || canViewIntegrations) && (
                        <CardActions>
                            {canManageIntegrations && (
                                <Button onClick={() => navigate('/webhook-settings')}>
                                    管理 Webhook
                                </Button>
                            )}
                            {canViewIntegrations && (
                                <Button onClick={() => navigate('/integration-runtime')}>
                                    查看投递监控
                                </Button>
                            )}
                        </CardActions>
                    )}
                </Card>
            </Stack>
        </Box>
    )
}

export default ProjectNotificationChannelsPage
