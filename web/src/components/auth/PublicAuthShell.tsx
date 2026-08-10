import { useEffect, type ReactNode } from 'react'
import { Box, Card, CardContent, Stack, Typography } from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import {
    humanSessionStorageCommitKey,
    markHumanAuthQueryAuthenticated,
} from '@/lib/authQueryState'
import { hasCompleteAuthenticationState } from '@/lib/authProvider'

interface PublicAuthShellProps {
    title: string
    description: string
    children: ReactNode
}

const PublicAuthShell = ({
    title,
    description,
    children,
}: PublicAuthShellProps) => {
    const queryClient = useQueryClient()

    useEffect(() => {
        const handleAuthenticationStorage = (event: StorageEvent) => {
            if (
                event.key !== humanSessionStorageCommitKey ||
                event.newValue === null ||
                !hasCompleteAuthenticationState()
            ) {
                return
            }
            markHumanAuthQueryAuthenticated(queryClient)
        }
        window.addEventListener('storage', handleAuthenticationStorage)
        return () => {
            window.removeEventListener(
                'storage',
                handleAuthenticationStorage,
            )
        }
    }, [queryClient])

    return (
        <Box
            component="main"
            sx={{
                alignItems: 'center',
                background:
                    'linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%)',
                display: 'flex',
                justifyContent: 'center',
                minHeight: '100vh',
                px: 2,
                py: 4,
            }}
        >
            <Card
                sx={{
                    backdropFilter: 'blur(20px)',
                    backgroundColor: 'rgba(255, 255, 255, 0.94)',
                    border: '1px solid rgba(255, 255, 255, 0.5)',
                    borderRadius: 4,
                    boxShadow: '0 8px 32px rgba(0, 0, 0, 0.08)',
                    maxWidth: 480,
                    width: '100%',
                }}
            >
                <CardContent sx={{ p: { xs: 3, sm: 5 } }}>
                    <Stack spacing={3}>
                        <Stack spacing={1} sx={{ textAlign: 'center' }}>
                            <Box
                                aria-hidden="true"
                                sx={{
                                    alignItems: 'center',
                                    background:
                                        'linear-gradient(135deg, #2563eb 0%, #4f46e5 100%)',
                                    borderRadius: 2,
                                    boxShadow:
                                        '0 4px 12px rgba(37, 99, 235, 0.3)',
                                    color: 'white',
                                    display: 'flex',
                                    fontSize: 24,
                                    fontWeight: 700,
                                    height: 48,
                                    justifyContent: 'center',
                                    mx: 'auto',
                                    width: 48,
                                }}
                            >
                                T
                            </Box>
                            <Typography
                                component="h1"
                                sx={{ fontWeight: 700 }}
                                variant="h5"
                            >
                                {title}
                            </Typography>
                            <Typography color="text.secondary" variant="body2">
                                {description}
                            </Typography>
                        </Stack>
                        {children}
                    </Stack>
                </CardContent>
            </Card>
        </Box>
    )
}

export default PublicAuthShell
