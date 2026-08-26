import { useEffect, type ReactNode } from 'react'
import {
    Box,
    Stack,
    Typography,
} from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { markHumanAuthQueryAuthenticated } from '@/lib/authQueryState'
import {
    applyRemoteHumanSignOut,
    bootstrapHumanSession,
} from '@/lib/authProvider'
import { subscribeHumanSessionMetadata } from '@/lib/humanSessionChannel'
import { clearHumanAccessToken } from '@/lib/humanSessionRuntime'
import {
    bindHumanTabSession,
    readHumanSessionBinding,
} from '@/lib/humanTabSession'
import heroAvif from '@/assets/chronodesk-login-orchestration.avif'
import heroJpeg from '@/assets/chronodesk-login-orchestration.jpg'
import ChronoDeskMark from '@/components/brand/ChronoDeskMark'

interface PublicAuthShellProps {
    title: string
    description: string
    children: ReactNode
    contentWidth?: number
    eyebrow?: string
}

const TrustPill = ({ children }: { children: ReactNode }) => (
    <Box
        component="span"
        sx={{
            display: 'inline-flex',
            alignItems: 'center',
            minHeight: 30,
            px: 1.25,
            border: '1px solid rgba(148, 163, 184, 0.24)',
            borderRadius: 999,
            color: 'rgba(226, 232, 240, 0.92)',
            bgcolor: 'rgba(15, 23, 42, 0.34)',
            backdropFilter: 'blur(8px)',
            fontSize: 12,
            fontWeight: 600,
            letterSpacing: '0.04em',
        }}
    >
        {children}
    </Box>
)

const BrandNarrative = () => (
    <Box
        component="section"
        aria-label="ChronoDesk 产品简介"
        data-testid="auth-brand-panel"
        sx={{
            position: { xs: 'relative', md: 'sticky' },
            top: 0,
            minWidth: 0,
            minHeight: {
                xs: 'clamp(148px, 25dvh, 190px)',
                md: '100dvh',
            },
            overflow: 'hidden',
            bgcolor: '#0b1726',
            color: '#ffffff',
            isolation: 'isolate',
            '&::before': {
                position: 'absolute',
                zIndex: 1,
                inset: 0,
                background: {
                    xs: 'linear-gradient(90deg, rgba(7, 17, 31, 0.92) 0%, rgba(7, 17, 31, 0.54) 58%, rgba(7, 17, 31, 0.28) 100%)',
                    md: 'linear-gradient(180deg, rgba(7, 17, 31, 0.52) 0%, rgba(7, 17, 31, 0.04) 44%, rgba(7, 17, 31, 0.2) 100%)',
                },
                content: '""',
                pointerEvents: 'none',
            },
            '&::after': {
                position: 'absolute',
                zIndex: 1,
                inset: 0,
                backgroundImage:
                    'radial-gradient(rgba(255, 255, 255, 0.12) 0.5px, transparent 0.5px)',
                backgroundSize: '5px 5px',
                content: '""',
                opacity: 0.1,
                pointerEvents: 'none',
            },
        }}
    >
        <Box
            component="picture"
            aria-hidden="true"
            sx={{ position: 'absolute', inset: 0 }}
        >
            <source srcSet={heroAvif} type="image/avif" />
            <Box
                component="img"
                src={heroJpeg}
                alt=""
                fetchPriority="high"
                sx={{
                    position: 'absolute',
                    right: { xs: '-7%', md: '-9%' },
                    bottom: { xs: '-48%', sm: '-56%', md: '-2%' },
                    width: { xs: '92%', sm: '82%', md: '116%' },
                    height: { xs: 'auto', md: 'auto' },
                    maxHeight: { md: '72%' },
                    objectFit: 'contain',
                    objectPosition: 'center bottom',
                    opacity: { xs: 0.72, md: 1 },
                    userSelect: 'none',
                }}
            />
        </Box>

        <Stack
            sx={{
                position: 'relative',
                zIndex: 2,
                height: '100%',
                minHeight: 'inherit',
                px: {
                    xs: 2.5,
                    sm: 4,
                    md: 'clamp(40px, 5vw, 80px)',
                },
                pl: {
                    xs:
                        'calc(20px + env(safe-area-inset-left, 0px))',
                    sm:
                        'calc(32px + env(safe-area-inset-left, 0px))',
                    md:
                        'calc(clamp(40px, 5vw, 80px) + env(safe-area-inset-left, 0px))',
                },
                pr: {
                    xs:
                        'calc(20px + env(safe-area-inset-right, 0px))',
                    sm:
                        'calc(32px + env(safe-area-inset-right, 0px))',
                    md:
                        'calc(clamp(40px, 5vw, 80px) + env(safe-area-inset-right, 0px))',
                },
                py: {
                    xs: 2.25,
                    sm: 3,
                    md: 'clamp(40px, 6vh, 72px)',
                },
                pt: {
                    xs: 'calc(18px + env(safe-area-inset-top, 0px))',
                    sm: 'calc(24px + env(safe-area-inset-top, 0px))',
                    md:
                        'calc(clamp(40px, 6vh, 72px) + env(safe-area-inset-top, 0px))',
                },
                pb: {
                    md:
                        'calc(clamp(40px, 6vh, 72px) + env(safe-area-inset-bottom, 0px))',
                },
            }}
        >
            <Stack
                direction="row"
                spacing={1.5}
                sx={{ alignItems: 'center' }}
            >
                <Box
                    data-testid="chronodesk-brand-mark"
                    sx={{
                        display: 'grid',
                        width: 42,
                        height: 42,
                        flex: '0 0 auto',
                        placeItems: 'center',
                        border: '1px solid rgba(255, 255, 255, 0.3)',
                        borderRadius: '12px',
                        background:
                            'linear-gradient(145deg, rgba(255, 255, 255, 0.16), rgba(37, 99, 235, 0.28))',
                        boxShadow:
                            'inset 0 1px 0 rgba(255, 255, 255, 0.24), 0 12px 32px rgba(3, 10, 22, 0.3)',
                    }}
                >
                    <ChronoDeskMark
                        sx={{
                            width: 25,
                            height: 25,
                            color: '#ffffff',
                        }}
                    />
                </Box>
                <Box>
                    <Typography
                        sx={{
                            fontSize: 17,
                            fontWeight: 750,
                            letterSpacing: '-0.01em',
                            lineHeight: 1.1,
                        }}
                    >
                        ChronoDesk
                    </Typography>
                    <Typography
                        sx={{
                            mt: 0.25,
                            color: 'rgba(203, 213, 225, 0.76)',
                            fontSize: 10,
                            fontWeight: 600,
                            letterSpacing: '0.14em',
                        }}
                    >
                        HUMAN + AGENT OPERATIONS
                    </Typography>
                </Box>
            </Stack>

            <Stack
                spacing={2.25}
                sx={{
                    mt: { xs: 2.25, sm: 3, md: 'clamp(58px, 10vh, 112px)' },
                    maxWidth: 610,
                }}
            >
                <Typography
                    component="p"
                    sx={{
                        display: { xs: 'none', md: 'block' },
                        color: '#93c5fd',
                        fontSize: 12,
                        fontWeight: 700,
                        letterSpacing: '0.16em',
                    }}
                >
                    可信工单与任务执行平台
                </Typography>
                <Typography
                    component="p"
                    sx={{
                        maxWidth: 650,
                        fontFamily:
                            '"Iowan Old Style", "Songti SC", "STSong", ui-serif, serif',
                        fontSize: {
                            xs: 22,
                            sm: 28,
                            md: 'clamp(36px, 3.5vw, 50px)',
                        },
                        fontWeight: 600,
                        letterSpacing: '-0.035em',
                        lineHeight: { xs: 1.24, md: 1.16 },
                        textWrap: 'balance',
                    }}
                >
                    让每一次协作，
                    <Box component="span" sx={{ display: { md: 'block' } }}>
                        都有边界、有证据、可接管
                    </Box>
                </Typography>
                <Typography
                    sx={{
                        display: { xs: 'none', md: 'block' },
                        maxWidth: 520,
                        color: 'rgba(203, 213, 225, 0.82)',
                        fontSize: 15,
                        lineHeight: 1.8,
                    }}
                >
                    把人类判断、AI Agent 执行和组织策略收束进同一条
                    可追溯任务链，让自动化始终可控、可审计。
                </Typography>
                <Stack
                    direction="row"
                    spacing={1}
                    useFlexGap
                    sx={{
                        display: { xs: 'none', md: 'flex' },
                        flexWrap: 'wrap',
                        pt: 0.5,
                    }}
                >
                    <TrustPill>项目隔离</TrustPill>
                    <TrustPill>策略受控</TrustPill>
                    <TrustPill>全链路审计</TrustPill>
                </Stack>
            </Stack>

            <Typography
                sx={{
                    display: { xs: 'none', md: 'block' },
                    mt: 'auto',
                    color: 'rgba(148, 163, 184, 0.64)',
                    fontSize: 11,
                    letterSpacing: '0.08em',
                }}
            >
                BUILT FOR RELIABLE SERVICE OPERATIONS
            </Typography>
        </Stack>
    </Box>
)

const PublicAuthShell = ({
    title,
    description,
    children,
    contentWidth = 480,
    eyebrow = '安全工作区',
}: PublicAuthShellProps) => {
    const queryClient = useQueryClient()

    useEffect(() => {
        return subscribeHumanSessionMetadata((metadata) => {
            void queryClient.cancelQueries({
                queryKey: ['auth', 'checkAuth'],
            })
            queryClient.removeQueries({
                queryKey: ['auth', 'checkAuth'],
            })
            if (metadata.type === 'signed_out') {
                applyRemoteHumanSignOut()
                return
            }
            const binding = readHumanSessionBinding()
            if (
                binding !== null &&
                binding.subject === metadata.subject &&
                binding.session_id === metadata.session_id
            ) {
                markHumanAuthQueryAuthenticated(queryClient)
                return
            }
            clearHumanAccessToken()
            bindHumanTabSession(null)
            void bootstrapHumanSession()
                .then(() => {
                    markHumanAuthQueryAuthenticated(queryClient)
                })
                .catch(() => undefined)
        })
    }, [queryClient])

    return (
        <Box
            component="main"
            data-testid="public-auth-shell"
            sx={{
                display: 'grid',
                gridTemplateColumns: {
                    xs: 'minmax(0, 1fr)',
                    md: 'minmax(0, 1.18fr) minmax(460px, 0.82fr)',
                    xl: 'minmax(0, 1.32fr) minmax(500px, 0.78fr)',
                },
                minWidth: 0,
                minHeight: '100dvh',
                bgcolor: '#f8fafc',
            }}
        >
            <BrandNarrative />

            <Box
                component="section"
                aria-label={title}
                data-testid="auth-workspace"
                sx={{
                    display: 'flex',
                    minWidth: 0,
                    minHeight: { md: '100dvh' },
                    alignItems: 'center',
                    bgcolor: '#f8fafc',
                    backgroundImage:
                        'linear-gradient(rgba(255, 255, 255, 0.72), rgba(248, 250, 252, 0.96))',
                }}
            >
                <Stack
                    spacing={{ xs: 2.75, sm: 3 }}
                    sx={{
                        width: '100%',
                        maxWidth: contentWidth,
                        mx: 'auto',
                        px: { xs: 2.5, sm: 4, lg: 5 },
                        pl: {
                            xs:
                                'calc(20px + env(safe-area-inset-left, 0px))',
                            sm:
                                'calc(32px + env(safe-area-inset-left, 0px))',
                            md:
                                'calc(32px + env(safe-area-inset-left, 0px))',
                            lg:
                                'calc(40px + env(safe-area-inset-left, 0px))',
                        },
                        pr: {
                            xs:
                                'calc(20px + env(safe-area-inset-right, 0px))',
                            sm:
                                'calc(32px + env(safe-area-inset-right, 0px))',
                            md:
                                'calc(32px + env(safe-area-inset-right, 0px))',
                            lg:
                                'calc(40px + env(safe-area-inset-right, 0px))',
                        },
                        py: {
                            xs: 3.25,
                            sm: 5,
                            md: 'clamp(38px, 6vh, 64px)',
                        },
                        pt: {
                            md:
                                'calc(clamp(38px, 6vh, 64px) + env(safe-area-inset-top, 0px))',
                        },
                        pb: {
                            xs:
                                'calc(26px + env(safe-area-inset-bottom, 0px))',
                            sm:
                                'calc(40px + env(safe-area-inset-bottom, 0px))',
                            md:
                                'calc(clamp(38px, 6vh, 64px) + env(safe-area-inset-bottom, 0px))',
                        },
                        '& .MuiOutlinedInput-root': {
                            borderRadius: '10px',
                            bgcolor: '#ffffff',
                            transition:
                                'box-shadow 140ms ease, border-color 140ms ease',
                            '&:hover .MuiOutlinedInput-notchedOutline': {
                                borderColor: '#94a3b8',
                            },
                            '&.Mui-focused': {
                                boxShadow:
                                    '0 0 0 3px rgba(37, 99, 235, 0.1)',
                            },
                        },
                        '& .MuiButton-contained': {
                            minHeight: 48,
                            borderRadius: '10px',
                            boxShadow:
                                '0 8px 22px rgba(37, 99, 235, 0.18)',
                            fontWeight: 700,
                            textTransform: 'none',
                        },
                        '& .MuiAlert-root': {
                            borderRadius: '10px',
                        },
                    }}
                >
                    <Stack spacing={1.15}>
                        <Typography
                            sx={{
                                color: '#245f94',
                                fontSize: 11,
                                fontWeight: 750,
                                letterSpacing: '0.14em',
                            }}
                        >
                            {eyebrow}
                        </Typography>
                        <Typography
                            component="h1"
                            sx={{
                                color: '#16222e',
                                fontSize: {
                                    xs: 28,
                                    sm: 32,
                                },
                                fontWeight: 750,
                                letterSpacing: '-0.025em',
                                lineHeight: 1.25,
                            }}
                        >
                            {title}
                        </Typography>
                        <Typography
                            sx={{
                                color: '#64748b',
                                fontSize: 14,
                                lineHeight: 1.7,
                            }}
                        >
                            {description}
                        </Typography>
                    </Stack>

                    {children}

                    <Typography
                        sx={{
                            pt: 0.25,
                            color: '#64748b',
                            fontSize: 11,
                            lineHeight: 1.6,
                            textAlign: 'center',
                        }}
                    >
                        会话与操作受组织策略保护，并保留必要审计记录
                    </Typography>
                </Stack>
            </Box>
        </Box>
    )
}

export default PublicAuthShell
