import { Paper, Tab, Tabs } from '@mui/material'
import { useLocation, useNavigate } from 'react-router-dom'

const automationSections = [
    { path: '/automation-rules', label: '规则' },
    { path: '/automation-logs', label: '执行日志' },
] as const

const AutomationTabs = () => {
    const location = useLocation()
    const navigate = useNavigate()
    const activePath = location.pathname.startsWith('/automation-logs')
        ? '/automation-logs'
        : '/automation-rules'

    return (
        <Paper variant="outlined" sx={{ mb: 2 }}>
            <Tabs
                value={activePath}
                onChange={(_, path: string) => navigate(path)}
                aria-label="自动化二级导航"
                variant="scrollable"
                scrollButtons="auto"
            >
                {automationSections.map((section) => (
                    <Tab
                        key={section.path}
                        value={section.path}
                        label={section.label}
                    />
                ))}
            </Tabs>
        </Paper>
    )
}

export default AutomationTabs
