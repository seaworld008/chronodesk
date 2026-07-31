import * as React from 'react'
import {
  Layout as RaLayout,
  Sidebar as RaSidebar,
  useSidebarState,
  type LayoutProps,
  type SidebarProps,
} from 'react-admin'
import {
  Drawer,
  GlobalStyles,
  useMediaQuery,
  type Theme,
} from '@mui/material'

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

  React.useEffect(() => {
    if (useTemporaryDrawer && !temporaryModeNormalized.current) {
      temporaryModeNormalized.current = true
      setOpen(false)
    } else if (!useTemporaryDrawer) {
      temporaryModeNormalized.current = false
    }
  }, [setOpen, useTemporaryDrawer])
  const temporaryOpen = temporaryModeNormalized.current ? open : false

  if (!useTemporaryDrawer) {
    return (
      <RaSidebar
        {...props}
        appBarAlwaysOn={_appBarAlwaysOn}
        closedSize={_closedSize}
        size={_size}
      >
        {children}
      </RaSidebar>
    )
  }

  return (
    <Drawer
      {...props}
      className={[
        'ChronoDeskSidebar-temporary',
        props.className,
      ].filter(Boolean).join(' ')}
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
            backgroundColor: theme.palette.background.paper,
          },
          '.RaSidebar-paper, .RaSidebar-root .MuiDrawer-paper': {
            zIndex: theme.zIndex.drawer,
            maxHeight: '100dvh',
            overflowX: 'hidden',
            overflowY: 'auto',
            overscrollBehavior: 'contain',
            scrollbarGutter: 'stable',
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
        }
      }}
    />
    <RaLayout {...props} sidebar={ResponsiveSidebar} />
  </>
)
