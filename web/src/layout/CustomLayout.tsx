import * as React from 'react'
import { Layout as RaLayout, LayoutProps } from 'react-admin'
import { GlobalStyles, type Theme } from '@mui/material'

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

export const CustomLayout: React.FC<LayoutProps> = (props) => (
  <>
    <GlobalStyles
      styles={(theme) => {
        const minHeightValue = resolveToolbarMinHeight(theme)

        return {
          '.RaLayout-content, .RaLayout-contentWithSidebar': {
            maxWidth: 'none',
            minWidth: 0,
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
          '.RaSidebar-fixed': {
            zIndex: theme.zIndex.drawer,
            width: 'inherit',
            maxWidth: 'inherit',
            boxSizing: 'border-box',
            overflowX: 'hidden',
            overflowY: 'auto',
            backgroundColor: theme.palette.background.paper,
          },
          '.RaSidebar-paper': {
            zIndex: theme.zIndex.drawer,
            overflow: 'visible',
          },
          '.RaLayout-content': {
            position: 'relative',
            zIndex: 0,
            minHeight: `calc(100vh - ${minHeightValue}px)`,
            display: 'flex',
            flexDirection: 'column',
            overflowX: 'hidden',
            padding: theme.spacing(0),
            backgroundColor: 'transparent',
          },
        }
      }}
    />
    <RaLayout {...props} />
  </>
)
