import React from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Drawer,
  IconButton,
  Stack,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TablePagination,
  TableRow,
  Tooltip,
  Typography,
} from '@mui/material'
import { Close as CloseIcon, Refresh as RefreshIcon } from '@mui/icons-material'
import {
  ResizableMuiTable,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import type { DirectoryPage, IntegrationDetail } from './integrationTypes'

export interface IntegrationColumn<T> extends ResizableColumn {
  label: string
  render: (row: T) => React.ReactNode
}

interface IntegrationTableProps<T extends { id: string }> {
  tableId: string
  ariaLabel: string
  columns: IntegrationColumn<T>[]
  page: DirectoryPage<T> | null
  pageIndex: number
  pageSize: number
  loading: boolean
  error: string
  emptyMessage: string
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onRetry: () => void
  onOpenDetails: (row: T) => void
  renderActions?: (row: T) => React.ReactNode
  pagination?: boolean
  footer?: React.ReactNode
}

export const IntegrationTable = <T extends { id: string }>({
  tableId,
  ariaLabel,
  columns,
  page,
  pageIndex,
  pageSize,
  loading,
  error,
  emptyMessage,
  onPageChange,
  onPageSizeChange,
  onRetry,
  onOpenDetails,
  renderActions,
  pagination = true,
  footer,
}: IntegrationTableProps<T>) => {
  const tableColumns = React.useMemo(
    () => renderActions
      ? [
        ...columns,
        {
          key: 'actions',
          defaultWidth: 120,
          minWidth: 96,
          maxWidth: 180,
          sticky: 'right' as const,
        },
      ]
      : columns,
    [columns, renderActions],
  )

  if (error) {
    return (
      <Alert
        severity="error"
        action={
          <Button color="inherit" size="small" onClick={onRetry}>
            重试
          </Button>
        }
      >
        {error}
      </Alert>
    )
  }

  return (
    <Box aria-busy={loading}>
      {loading && !page ? (
        <Stack
          role="status"
          spacing={1}
          sx={{ alignItems: 'center', py: 8 }}
        >
          <CircularProgress size={28} />
          <Typography color="text.secondary">正在加载数据…</Typography>
        </Stack>
      ) : (
        <>
          <TableContainer sx={{ overflowX: 'auto' }}>
            <ResizableMuiTable
              tableId={tableId}
              columns={tableColumns}
              size="small"
              aria-label={ariaLabel}
            >
              <TableHead>
                <TableRow>
                  {columns.map((column) => (
                    <TableCell key={column.key}>{column.label}</TableCell>
                  ))}
                  {renderActions && <TableCell align="right">操作</TableCell>}
                </TableRow>
              </TableHead>
              <TableBody>
                {(page?.items ?? []).map((row) => (
                  <TableRow
                    key={row.id}
                    hover
                    tabIndex={0}
                    aria-label={`查看 ${row.id} 详情`}
                    onClick={() => onOpenDetails(row)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault()
                        onOpenDetails(row)
                      }
                    }}
                    sx={{ cursor: 'pointer' }}
                  >
                    {columns.map((column) => (
                      <TableCell key={column.key}>
                        {column.render(row)}
                      </TableCell>
                    ))}
                    {renderActions && (
                      <TableCell
                        align="right"
                        onClick={(event) => event.stopPropagation()}
                        onKeyDown={(event) => event.stopPropagation()}
                      >
                        {renderActions(row)}
                      </TableCell>
                    )}
                  </TableRow>
                ))}
                {(page?.items.length ?? 0) === 0 && (
                  <TableRow>
                    <TableCell
                      colSpan={tableColumns.length}
                      align="center"
                      sx={{ py: 7, color: 'text.secondary' }}
                    >
                      {emptyMessage}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </ResizableMuiTable>
          </TableContainer>
          <Stack direction="row" sx={{ alignItems: 'center', minHeight: 52 }}>
            {loading && (
              <CircularProgress
                size={18}
                sx={{ ml: 2 }}
                aria-label="正在刷新当前页"
              />
            )}
            {footer}
            {pagination && (
              <TablePagination
                component="div"
                count={page?.total ?? 0}
                page={pageIndex}
                rowsPerPage={pageSize}
                rowsPerPageOptions={[25, 50, 100]}
                onPageChange={(_, nextPage) => onPageChange(nextPage)}
                onRowsPerPageChange={(event) =>
                  onPageSizeChange(Number(event.target.value))}
                labelRowsPerPage="每页"
                labelDisplayedRows={({ from, to, count }) =>
                  `${from}–${to} / ${count}`}
                sx={{ ml: 'auto' }}
              />
            )}
          </Stack>
        </>
      )}
    </Box>
  )
}

const detailLabels: Record<string, string> = {
  id: '资源 ID',
  key: '标识',
  name: '名称',
  description: '说明',
  kind: '类型',
  direction: '方向',
  status: '状态',
  signature_scheme: '签名方案',
  default_replay_window_seconds: '默认重放窗口（秒）',
  replay_window_seconds: '重放窗口（秒）',
  external_message_id: '外部消息 ID',
  external_resource_type: '外部资源类型',
  external_resource_id: '外部资源 ID',
  resource_type: '资源类型',
  resource_id: '资源 ID',
  resource_version: '资源版本',
  event_id: '事件 ID',
  operation_id: '操作 ID',
  actor_type: '操作者类型',
  actor_id: '操作者',
  target_command: '目标命令',
  definition_digest: '定义摘要',
  payload_digest: '内容摘要',
  reason_code: '原因代码',
  error_code: '错误代码',
  last_error: '最近错误',
  attempts: '已尝试',
  max_attempts: '最大尝试',
  created_at: '创建时间',
  updated_at: '更新时间',
  received_at: '接收时间',
  processed_at: '处理时间',
  delivered_at: '投递时间',
}

const detailValue = (value: unknown) => {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  return String(value)
}

export const IntegrationDetailsDrawer = ({
  title,
  detail,
  onClose,
}: {
  title: string
  detail: IntegrationDetail | null
  onClose: () => void
}) => (
  <Drawer
    anchor="right"
    open={detail !== null}
    onClose={onClose}
    slotProps={{
      paper: {
        sx: { width: { xs: '100%', sm: 480 }, maxWidth: '100%' },
      },
    }}
  >
    <Stack
      direction="row"
      sx={{ alignItems: 'center', justifyContent: 'space-between', p: 2 }}
    >
      <Typography variant="h6">{title}</Typography>
      <Tooltip title="关闭详情">
        <IconButton onClick={onClose} aria-label="关闭详情">
          <CloseIcon />
        </IconButton>
      </Tooltip>
    </Stack>
    <Stack spacing={0} sx={{ px: 2, pb: 3 }}>
      {detail && Object.entries(detail).map(([key, value]) => (
        <Stack
          key={key}
          direction={{ xs: 'column', sm: 'row' }}
          spacing={1}
          sx={{
            py: 1.25,
            borderTop: 1,
            borderColor: 'divider',
            minWidth: 0,
          }}
        >
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ width: { sm: 144 }, flexShrink: 0 }}
          >
            {detailLabels[key] ?? key}
          </Typography>
          <TruncatedText title={detailValue(value)}>
            {detailValue(value)}
          </TruncatedText>
        </Stack>
      ))}
    </Stack>
  </Drawer>
)

export const RefreshButton = ({
  loading,
  onClick,
}: {
  loading: boolean
  onClick: () => void
}) => (
  <Tooltip title="刷新当前视图">
    <span>
      <IconButton
        onClick={onClick}
        disabled={loading}
        aria-label="刷新当前视图"
      >
        <RefreshIcon />
      </IconButton>
    </span>
  </Tooltip>
)

export const StatusChip = ({ value }: { value: string }) => {
  const success = ['active', 'completed', 'applied', 'succeeded', 'published', 'resolved']
  const warning = ['processing', 'pending', 'draft', 'requeued', 'inactive']
  const color = success.includes(value)
    ? 'success'
    : warning.includes(value)
      ? 'warning'
      : ['error', 'failed', 'dead', 'dead_letter', 'conflict'].includes(value)
        ? 'error'
        : 'default'
  return <Chip size="small" label={value} color={color} variant="outlined" />
}
