import type { ReactNode } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Paper,
  Stack,
  TableContainer,
  TablePagination,
} from '@mui/material'
import {
  ResizableMuiTable,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import PageHeader from '@/components/layout/PageHeader'
import PageShell from '@/components/layout/PageShell'

interface AutomationDirectoryLayoutProps {
  title: string
  description: string
  action: ReactNode
  filters: ReactNode
  tableID: string
  tableLabel: string
  columns: ResizableColumn[]
  loading: boolean
  error: string
  empty: boolean
  emptyMessage: string
  total: number
  page: number
  pageSize: number
  onRetry: () => void
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  children: ReactNode
  overlays?: ReactNode
}

const AutomationDirectoryLayout = ({
  title,
  description,
  action,
  filters,
  tableID,
  tableLabel,
  columns,
  loading,
  error,
  empty,
  emptyMessage,
  total,
  page,
  pageSize,
  onRetry,
  onPageChange,
  onPageSizeChange,
  children,
  overlays,
}: AutomationDirectoryLayoutProps) => (
  <PageShell title={title} testId={tableID}>
    <Stack spacing={2}>
      <PageHeader
        title={title}
        description={description}
        action={action}
      />
      {filters}
      {error && (
        <Alert
          severity="error"
          role="alert"
          action={
            <Button size="small" onClick={onRetry}>
              重试
            </Button>
          }
        >
          {error}
        </Alert>
      )}
      {loading && (
        <Box
          role="status"
          sx={{ display: 'grid', minHeight: 220, placeItems: 'center' }}
        >
          <CircularProgress aria-label={`正在加载${title}`} />
        </Box>
      )}
      {!loading && !error && empty && (
        <Alert severity="info">{emptyMessage}</Alert>
      )}
      {!error && !empty && (
        <Paper>
          <TableContainer>
            <ResizableMuiTable
              tableId={tableID}
              columns={columns}
              size="small"
              aria-label={tableLabel}
            >
              {children}
            </ResizableMuiTable>
          </TableContainer>
          <TablePagination
            component="div"
            count={total}
            page={page}
            rowsPerPage={pageSize}
            rowsPerPageOptions={[25, 50, 100]}
            onPageChange={(_, nextPage) => onPageChange(nextPage)}
            onRowsPerPageChange={(event) =>
              onPageSizeChange(Number(event.target.value))
            }
            labelRowsPerPage="每页数量"
            labelDisplayedRows={({ from, to, count }) =>
              `${from}–${to} / ${count}`
            }
            slotProps={{
              select: {
                inputProps: {
                  'aria-label': `${title}每页数量`,
                },
              },
            }}
          />
        </Paper>
      )}
    </Stack>
    {overlays}
  </PageShell>
)

export default AutomationDirectoryLayout
