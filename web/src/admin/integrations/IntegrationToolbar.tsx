import {
  Box,
  Chip,
  FormControl,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Typography,
} from '@mui/material'
import { RefreshButton } from './IntegrationTable'
import type { IntegrationOverview } from './integrationTypes'

export const IntegrationHeader = ({
  projectKey,
  overview,
}: {
  projectKey: string
  overview: IntegrationOverview | null
}) => {
  const metrics = [
    ['连接', overview?.connections ?? '—'],
    ['活动连接', overview?.active_connections ?? '—'],
    ['异常连接', overview?.error_connections ?? '—'],
    ['待处理冲突', overview?.open_conflicts ?? '—'],
    ['开放死信', overview?.open_dead_letters ?? '—'],
  ] as const
  return (
    <Stack spacing={2}>
      <Stack
        direction={{ xs: 'column', md: 'row' }}
        spacing={1.5}
        sx={{ justifyContent: 'space-between', alignItems: { md: 'center' } }}
      >
        <Box>
          <Typography variant="h4">集成中心</Typography>
          <Typography color="text.secondary" sx={{ mt: 0.5 }}>
            统一管理外部连接、字段映射、Inbox、安全同步与事件投递。
          </Typography>
        </Box>
        <Chip
          label={`当前项目：${projectKey || '正在解析'}`}
          color="primary"
          variant="outlined"
          aria-label={`当前项目 ${projectKey || '正在解析'}`}
        />
      </Stack>
      <Stack
        direction="row"
        spacing={1}
        sx={{ overflowX: 'auto', pb: 0.5 }}
      >
        {metrics.map(([label, value]) => (
          <Paper
            key={label}
            variant="outlined"
            sx={{ px: 2, py: 1.25, minWidth: 136, flex: '0 0 auto' }}
          >
            <Typography variant="caption" color="text.secondary">
              {label}
            </Typography>
            <Typography variant="h6">{value}</Typography>
          </Paper>
        ))}
      </Stack>
    </Stack>
  )
}

export interface SelectOption {
  value: string
  label: string
}

export const IntegrationListToolbar = ({
  search,
  onSearchChange,
  status,
  onStatusChange,
  statuses,
  statusLabel = '状态',
  secondaryLabel,
  secondaryValue,
  onSecondaryChange,
  secondaryOptions = [],
  loading,
  onRefresh,
}: {
  search: string
  onSearchChange: (value: string) => void
  status: string
  onStatusChange: (value: string) => void
  statuses: SelectOption[]
  statusLabel?: string
  secondaryLabel?: string
  secondaryValue?: string
  onSecondaryChange?: (value: string) => void
  secondaryOptions?: SelectOption[]
  loading: boolean
  onRefresh: () => void
}) => (
  <Stack
    direction={{ xs: 'column', md: 'row' }}
    spacing={1.5}
    sx={{ alignItems: { md: 'center' }, mb: 2 }}
  >
    <TextField
      label="搜索"
      value={search}
      onChange={(event) => onSearchChange(event.target.value)}
      size="small"
      type="search"
      slotProps={{ htmlInput: { maxLength: 200 } }}
      sx={{ minWidth: { md: 280 } }}
    />
    {statuses.length > 0 && (
      <FormControl size="small" sx={{ minWidth: 160 }}>
        <InputLabel id="integration-status-filter-label">
          {statusLabel}
        </InputLabel>
        <Select
          labelId="integration-status-filter-label"
          label={statusLabel}
          value={status}
          onChange={(event) => onStatusChange(event.target.value)}
        >
          <MenuItem value="">全部{statusLabel}</MenuItem>
          {statuses.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>
    )}
    {secondaryLabel && onSecondaryChange && (
      <FormControl size="small" sx={{ minWidth: 180 }}>
        <InputLabel id="integration-secondary-filter-label">
          {secondaryLabel}
        </InputLabel>
        <Select
          labelId="integration-secondary-filter-label"
          label={secondaryLabel}
          value={secondaryValue ?? ''}
          onChange={(event) => onSecondaryChange(event.target.value)}
        >
          <MenuItem value="">全部{secondaryLabel}</MenuItem>
          {secondaryOptions.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>
    )}
    <Box sx={{ flex: 1 }} />
    <RefreshButton loading={loading} onClick={onRefresh} />
  </Stack>
)

export const IntegrationModeSwitch = ({
  value,
  onChange,
  options,
  label,
}: {
  value: string
  onChange: (value: string) => void
  options: SelectOption[]
  label: string
}) => (
  <ToggleButtonGroup
    exclusive
    size="small"
    value={value}
    onChange={(_, next) => {
      if (typeof next === 'string') onChange(next)
    }}
    aria-label={label}
    sx={{ mb: 2 }}
  >
    {options.map((option) => (
      <ToggleButton key={option.value} value={option.value}>
        {option.label}
      </ToggleButton>
    ))}
  </ToggleButtonGroup>
)
