import * as React from 'react'
import { Stack, Box, StackProps, useTheme, useMediaQuery } from '@mui/material'

export type RatioRowProps = {
  ratios: number[]
  gap?: number
  breakAt?: 'sm' | 'md' | 'lg'
} & Omit<StackProps, 'direction' | 'spacing'>

export const RatioRow: React.FC<React.PropsWithChildren<RatioRowProps>> = ({
  ratios,
  gap = 2,
  breakAt = 'md',
  children,
  ...stackProps
}) => {
  const theme = useTheme()
  const isDown = useMediaQuery(theme.breakpoints.down(breakAt))
  const items = React.Children.toArray(children)
  const safeRatios = ratios.map((r) => (Number.isFinite(r) && r > 0 ? r : 1))
  const total = safeRatios.reduce((acc, curr) => acc + curr, 0) || 1

  return (
    <Stack
      direction={isDown ? 'column' : 'row'}
      spacing={gap}
      alignItems="stretch"
      {...stackProps}
    >
      {items.map((child, index) => (
        <Box
          key={index}
          sx={{
            flex: isDown ? '1 1 auto' : `${(safeRatios[index] ?? 1) / total} 1 0`,
            minWidth: 0,
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {child}
        </Box>
      ))}
    </Stack>
  )
}
