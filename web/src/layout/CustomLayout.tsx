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
          },
          '.RaLayout-content': {
            minHeight: `calc(100vh - ${minHeightValue}px)`,
            display: 'flex',
            flexDirection: 'column',
            padding: theme.spacing(0),
            backgroundColor: 'transparent',
          },
        }
      }}
    />
    <RaLayout {...props} />
  </>
)
