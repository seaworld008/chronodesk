import type { ReactNode } from 'react'
import { Box, Stack, Typography } from '@mui/material'

export interface PageHeaderProps {
    title: string
    description?: ReactNode
    leading?: ReactNode
    action?: ReactNode
    testId?: string
}

const PageHeader = ({
    title,
    description,
    leading,
    action,
    testId = 'page-header',
}: PageHeaderProps) => (
    <Stack
        data-testid={testId}
        direction={{ xs: 'column', md: 'row' }}
        spacing={2}
        sx={{
            alignItems: { xs: 'stretch', md: 'flex-start' },
            justifyContent: 'space-between',
            minWidth: 0,
            width: '100%',
        }}
    >
        <Stack
            direction="row"
            spacing={1.5}
            sx={{
                alignItems: 'flex-start',
                minWidth: 0,
            }}
        >
            {leading && (
                <Box sx={{ flexShrink: 0, pt: 0.25 }}>
                    {leading}
                </Box>
            )}
            <Box sx={{ minWidth: 0 }}>
                <Typography
                    variant="h4"
                    component="h1"
                    sx={{
                        overflowWrap: 'anywhere',
                    }}
                >
                    {title}
                </Typography>
                {description && (
                    <Typography
                        color="text.secondary"
                        sx={{
                            mt: 0.5,
                            overflowWrap: 'anywhere',
                        }}
                    >
                        {description}
                    </Typography>
                )}
            </Box>
        </Stack>
        {action && (
            <Box
                data-testid={`${testId}-action`}
                sx={{
                    alignSelf: { xs: 'stretch', md: 'flex-start' },
                    display: 'flex',
                    flexShrink: 1,
                    flexWrap: 'wrap',
                    gap: 1,
                    justifyContent: { xs: 'flex-start', md: 'flex-end' },
                    minWidth: 0,
                    '& > *': {
                        maxWidth: '100%',
                    },
                }}
            >
                {action}
            </Box>
        )}
    </Stack>
)

export default PageHeader
