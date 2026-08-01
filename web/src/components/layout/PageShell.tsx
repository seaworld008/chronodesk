import type { PropsWithChildren } from 'react'
import { Box, type SxProps, type Theme } from '@mui/material'
import { Title } from 'react-admin'

export interface PageShellProps extends PropsWithChildren {
    title: string
    maxWidth?: number | string | false
    testId?: string
    sx?: SxProps<Theme>
}

const PageShell = ({
    title,
    maxWidth = false,
    testId = 'page-shell',
    sx,
    children,
}: PageShellProps) => (
    <Box
        data-testid={testId}
        sx={[
            {
                boxSizing: 'border-box',
                minWidth: 0,
                p: { xs: 2, sm: 2.5, md: 3 },
                width: '100%',
            },
            ...(Array.isArray(sx) ? sx : [sx]),
        ]}
    >
        <Title title={title} />
        <Box
            sx={{
                boxSizing: 'border-box',
                maxWidth: maxWidth === false ? 'none' : maxWidth,
                minWidth: 0,
                mx: maxWidth === false ? 0 : 'auto',
                width: '100%',
            }}
        >
            {children}
        </Box>
    </Box>
)

export default PageShell
