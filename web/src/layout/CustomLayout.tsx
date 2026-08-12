import * as React from 'react'
import {
  Layout as RaLayout,
  Sidebar as RaSidebar,
  useSidebarState,
  type LayoutProps,
  type SidebarProps,
} from 'react-admin'
import {
  Box,
  Drawer,
  GlobalStyles,
  useMediaQuery,
  type Theme,
} from '@mui/material'
import { readHumanSessionBinding } from '@/lib/humanTabSession'
import {
  clampSidebarWidth,
  keyboardSidebarWidth,
  loadSidebarWidth,
  saveSidebarWidth,
  sidebarClosedWidth,
  sidebarDefaultWidth,
  sidebarMaxWidth,
  sidebarMinWidth,
} from '@/layout/sidebarWidth'

const primaryNavigationID = 'chronodesk-primary-navigation'

const currentSidebarSubject = (): string => {
  if (typeof window === 'undefined') return 'anonymous'
  return readHumanSessionBinding()?.subject ?? 'anonymous'
}

const resolveToolbarMinHeight = (theme: Theme): number => {
  const toolbar = theme.mixins.toolbar
  if (typeof toolbar.minHeight === 'number') {
    return toolbar.minHeight
  }

  const breakpointKey = theme.breakpoints.up('sm')
  const responsiveConfig = (toolbar as Record<string, { minHeight?: number } | undefined>)[breakpointKey]

  if (responsiveConfig && typeof responsiveConfig.minHeight === 'number') {
    return responsiveConfig.minHeight
  }

  return 64
}

const ResponsiveSidebar: React.FC<SidebarProps> = ({
  appBarAlwaysOn: _appBarAlwaysOn,
  children,
  closedSize: _closedSize,
  size: _size,
  ...props
}) => {
  const useTemporaryDrawer = useMediaQuery<Theme>((theme) =>
    theme.breakpoints.down('md'),
  )
  const [open, setOpen] = useSidebarState()
  const temporaryModeNormalized = React.useRef(false)
  const desktopOpen = React.useRef(open)
  const subject = currentSidebarSubject()
  const [preferredWidth, setPreferredWidth] = React.useState(() =>
    typeof window === 'undefined'
      ? sidebarDefaultWidth
      : loadSidebarWidth(window.localStorage, subject),
  )
  const [resizing, setResizing] = React.useState(false)
  const resizeCleanupRef = React.useRef<(() => void) | null>(null)

  React.useEffect(() => {
    if (useTemporaryDrawer && !temporaryModeNormalized.current) {
      temporaryModeNormalized.current = true
      desktopOpen.current = open
      setOpen(false)
    } else if (!useTemporaryDrawer && temporaryModeNormalized.current) {
      temporaryModeNormalized.current = false
      setOpen(desktopOpen.current)
    } else if (!useTemporaryDrawer) {
      desktopOpen.current = open
    }
  }, [open, setOpen, useTemporaryDrawer])

  React.useEffect(() => {
    setPreferredWidth(
      loadSidebarWidth(window.localStorage, subject),
    )
  }, [subject])

  React.useEffect(
    () => () => resizeCleanupRef.current?.(),
    [],
  )

  React.useEffect(() => {
    if (!open || useTemporaryDrawer) {
      resizeCleanupRef.current?.()
    }
  }, [open, useTemporaryDrawer])

  const temporaryOpen = temporaryModeNormalized.current ? open : false
  const renderedWidth = open ? preferredWidth : sidebarClosedWidth

  const persistWidth = React.useCallback((width: number) => {
    const next = clampSidebarWidth(width)
    setPreferredWidth(next)
    saveSidebarWidth(window.localStorage, subject, next)
  }, [subject])

  const startResize = React.useCallback(
    (event: React.PointerEvent<HTMLElement>) => {
      if (!open || (event.pointerType === 'mouse' && event.button !== 0)) {
        return
      }
      event.stopPropagation()
      resizeCleanupRef.current?.()

      const startX = event.clientX
      const startWidth = preferredWidth
      let latestX = startX
      let animationFrame: number | null = null

      const applyLatestWidth = () => {
        animationFrame = null
        setPreferredWidth(
          clampSidebarWidth(startWidth + latestX - startX),
        )
      }
      const handlePointerMove = (pointerEvent: PointerEvent) => {
        latestX = pointerEvent.clientX
        if (animationFrame === null) {
          animationFrame = window.requestAnimationFrame(applyLatestWidth)
        }
      }
      const detach = () => {
        window.removeEventListener('pointermove', handlePointerMove)
        window.removeEventListener('pointerup', finishResize)
        window.removeEventListener('pointercancel', cancelResize)
        window.removeEventListener('keydown', handleEscape)
        if (animationFrame !== null) {
          window.cancelAnimationFrame(animationFrame)
        }
        document.body.classList.remove('cd-sidebar-is-resizing')
        resizeCleanupRef.current = null
      }
      const finishResize = (pointerEvent: PointerEvent) => {
        latestX = pointerEvent.clientX
        const next = clampSidebarWidth(startWidth + latestX - startX)
        detach()
        setResizing(false)
        persistWidth(next)
      }
      const cancelResize = () => {
        detach()
        setResizing(false)
        setPreferredWidth(startWidth)
      }
      const handleEscape = (keyboardEvent: KeyboardEvent) => {
        if (keyboardEvent.key !== 'Escape') return
        keyboardEvent.preventDefault()
        cancelResize()
      }

      resizeCleanupRef.current = cancelResize
      setResizing(true)
      document.body.classList.add('cd-sidebar-is-resizing')
      window.addEventListener('pointermove', handlePointerMove)
      window.addEventListener('pointerup', finishResize)
      window.addEventListener('pointercancel', cancelResize)
      window.addEventListener('keydown', handleEscape)
    },
    [open, persistWidth, preferredWidth],
  )

  const handleResizeKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLElement>) => {
      const next = keyboardSidebarWidth(
        preferredWidth,
        event.key,
        event.shiftKey,
      )
      if (next === null) return
      event.preventDefault()
      event.stopPropagation()
      persistWidth(next)
    },
    [persistWidth, preferredWidth],
  )

  if (!useTemporaryDrawer) {
    return (
      <Box
        component="aside"
        aria-label="主导航侧栏"
        data-testid="desktop-sidebar"
        data-sidebar-state={open ? 'open' : 'closed'}
        data-sidebar-width={renderedWidth}
        data-resizing={resizing || undefined}
        sx={(theme) => ({
          '--chronodesk-sidebar-width': `${renderedWidth}px`,
          position: 'relative',
          zIndex: theme.zIndex.drawer,
          flex: `0 0 var(--chronodesk-sidebar-width)`,
          width: 'var(--chronodesk-sidebar-width)',
          minWidth: 'var(--chronodesk-sidebar-width)',
          maxWidth: 'var(--chronodesk-sidebar-width)',
          transition: resizing
            ? 'none'
            : theme.transitions.create(
              ['width', 'min-width', 'max-width', 'flex-basis'],
              {
                easing: theme.transitions.easing.sharp,
                duration: theme.transitions.duration.leavingScreen,
              },
            ),
          '& .RaSidebar-root, & .RaSidebar-paper, & .RaSidebar-fixed, & .RaMenu-open, & .RaMenu-closed':
            {
              width: 'var(--chronodesk-sidebar-width) !important',
              minWidth: 'var(--chronodesk-sidebar-width) !important',
              maxWidth: 'var(--chronodesk-sidebar-width) !important',
            },
          '& .RaSidebar-paper, & .RaSidebar-fixed, & .RaMenu-open, & .RaMenu-closed':
            {
              transition: resizing ? 'none !important' : undefined,
            },
        })}
      >
        <RaSidebar
          {...props}
          id={primaryNavigationID}
          appBarAlwaysOn={_appBarAlwaysOn}
          closedSize={_closedSize}
          size={_size}
        >
          {children}
        </RaSidebar>
        {open ? (
          <Box
            component="span"
            role="separator"
            aria-label="调整主导航宽度"
            aria-orientation="vertical"
            aria-controls={primaryNavigationID}
            aria-valuemin={sidebarMinWidth}
            aria-valuemax={sidebarMaxWidth}
            aria-valuenow={preferredWidth}
            aria-valuetext={`${preferredWidth} 像素`}
            data-testid="sidebar-resize-handle"
            data-resizing={resizing || undefined}
            tabIndex={0}
            title="拖动调整宽度，双击恢复默认；聚焦后可用方向键、Home 和 End 调整"
            onPointerDown={startResize}
            onDoubleClick={(event) => {
              event.preventDefault()
              event.stopPropagation()
              persistWidth(sidebarDefaultWidth)
            }}
            onKeyDown={handleResizeKeyDown}
            sx={(theme) => ({
              position: 'absolute',
              zIndex: theme.zIndex.drawer + 2,
              top: 0,
              right: -6,
              bottom: 0,
              width: 12,
              cursor: 'col-resize',
              touchAction: 'none',
              '&::after': {
                position: 'absolute',
                top: 4,
                right: '5px',
                bottom: 4,
                width: '1px',
                borderRadius: 999,
                backgroundColor: theme.palette.divider,
                content: '""',
                transition:
                  'background-color 120ms ease, width 120ms ease',
              },
              '&:hover::after, &:focus-visible::after, &[data-resizing]::after':
                {
                  width: '2px',
                  backgroundColor: theme.palette.primary.main,
                },
              '&:focus-visible': {
                outline: `2px solid ${theme.palette.primary.light}`,
                outlineOffset: -2,
              },
            })}
          />
        ) : null}
      </Box>
    )
  }

  return (
    <Drawer
      {...props}
      className={[
        'ChronoDeskSidebar-temporary',
        props.className,
      ].filter(Boolean).join(' ')}
      id={primaryNavigationID}
      data-testid="mobile-sidebar"
      variant="temporary"
      open={temporaryOpen}
      onClose={() => setOpen(false)}
      classes={{
        root: 'RaSidebar-root',
        paper: 'RaSidebar-paper',
        modal: 'RaSidebar-modal',
      }}
      sx={{
        '& .MuiDrawer-paper': {
          bgcolor: 'background.paper',
          boxSizing: 'border-box',
          width: 240,
        },
      }}
    >
      {children}
    </Drawer>
  )
}

export const CustomLayout: React.FC<LayoutProps> = (props) => (
  <>
    <GlobalStyles
      styles={(theme) => {
        const minHeightValue = resolveToolbarMinHeight(theme)

        return {
          '.RaLayout-root': {
            minWidth: 0,
            maxWidth: '100%',
            overflowX: 'clip',
          },
          '.RaLayout-appFrame': {
            minWidth: 0,
            maxWidth: '100%',
            width: '100%',
          },
          '.RaLayout-content, .RaLayout-contentWithSidebar': {
            maxWidth: 'none',
            minWidth: 0,
            width: '100%',
          },
          '.RaLayout-contentWithSidebar': {
            position: 'relative',
            display: 'flex',
            alignItems: 'stretch',
            overflow: 'hidden',
          },
          '.RaSidebar-root': {
            position: 'relative',
            zIndex: theme.zIndex.drawer,
            flexShrink: 0,
          },
          '.RaSidebar-root.ChronoDeskSidebar-temporary': {
            position: 'fixed',
            inset: 0,
          },
          '.RaSidebar-fixed': {
            zIndex: theme.zIndex.drawer,
            width: 'inherit',
            maxWidth: 'inherit',
            maxHeight: `calc(100dvh - ${minHeightValue}px)`,
            boxSizing: 'border-box',
            overflowX: 'hidden',
            overflowY: 'auto',
            overscrollBehavior: 'contain',
            scrollbarGutter: 'stable',
            scrollPaddingBlock: theme.spacing(1),
            paddingBottom: theme.spacing(1),
            backgroundColor: theme.palette.background.paper,
          },
          '.RaSidebar-paper, .RaSidebar-root .MuiDrawer-paper': {
            zIndex: theme.zIndex.drawer,
            maxHeight: '100dvh',
            overflowX: 'hidden',
            overflowY: 'auto',
            overscrollBehavior: 'contain',
            scrollbarGutter: 'stable',
            scrollPaddingBlock: theme.spacing(1),
            paddingBottom: theme.spacing(1),
          },
          '.RaLayout-content': {
            position: 'relative',
            zIndex: 0,
            minHeight: `calc(100vh - ${minHeightValue}px)`,
            display: 'flex',
            flexDirection: 'column',
            overflowX: 'hidden',
            padding: `${theme.spacing(0)} !important`,
            boxSizing: 'border-box',
            backgroundColor: 'transparent',
          },
          '.cd-sidebar-is-resizing, .cd-sidebar-is-resizing *': {
            cursor: 'col-resize !important',
            userSelect: 'none !important',
          },
        }
      }}
    />
    <RaLayout {...props} sidebar={ResponsiveSidebar} />
  </>
)
