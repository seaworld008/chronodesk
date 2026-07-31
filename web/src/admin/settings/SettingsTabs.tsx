import { Paper, Tab, Tabs } from '@mui/material'
import { useLocation, useNavigate } from 'react-router-dom'
import { usePermissions } from 'react-admin'
import {
    hasPlatformCapability,
    type AccessPermissions,
} from '@/lib/accessControl'

const SettingsTabs = () => {
    const location = useLocation()
    const navigate = useNavigate()
    const { permissions } = usePermissions<AccessPermissions>()
    const activePath = location.pathname === '/system-settings/email'
        ? '/system-settings/email'
        : '/system-settings'

    return (
        <Paper variant="outlined" sx={{ mb: 2 }}>
            <Tabs
                value={activePath}
                onChange={(_, path: string) => navigate(path)}
                aria-label="系统设置二级导航"
                variant="scrollable"
                scrollButtons="auto"
            >
                <Tab value="/system-settings" label="平台公共配置" />
                {hasPlatformCapability(
                    permissions?.platform_role,
                    'manage_email_settings',
                ) && (
                    <Tab value="/system-settings/email" label="平台邮件设置" />
                )}
            </Tabs>
        </Paper>
    )
}

export default SettingsTabs
