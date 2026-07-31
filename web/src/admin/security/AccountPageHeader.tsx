import type { ReactNode } from 'react'
import { Box, Stack, Typography } from '@mui/material'

interface AccountPageHeaderProps {
    title: string
    description: string
    action?: ReactNode
}

const AccountPageHeader = ({
    title,
    description,
    action,
}: AccountPageHeaderProps) => (
    <Stack
        data-testid="account-page-header"
        direction={{ xs: 'column', md: 'row' }}
        spacing={2}
        sx={{
            alignItems: { xs: 'stretch', md: 'flex-start' },
            justifyContent: 'space-between',
        }}
    >
        <Box sx={{ minWidth: 0 }}>
            <Typography variant="h4" component="h1" gutterBottom>
                {title}
            </Typography>
            <Typography color="text.secondary">
                {description}
            </Typography>
        </Box>
        {action && (
            <Box
                data-testid="account-page-header-action"
                sx={{
                    flexShrink: 0,
                    alignSelf: { xs: 'stretch', md: 'flex-start' },
                }}
            >
                {action}
            </Box>
        )}
    </Stack>
)

export default AccountPageHeader
