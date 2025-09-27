import React, { useMemo, useState } from 'react'
import {
  Button,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  FormControl,
  InputLabel,
  MenuItem,
  Stack,
  Alert,
} from '@mui/material'
import Select, { SelectChangeEvent } from '@mui/material/Select'
import { useListContext, useNotify, useUpdateMany } from 'react-admin'

const statusChoices = [
  { id: 'open', name: '待处理' },
  { id: 'in_progress', name: '处理中' },
  { id: 'pending', name: '等待中' },
  { id: 'resolved', name: '已解决' },
  { id: 'closed', name: '已关闭' },
  { id: 'cancelled', name: '已取消' },
]

const priorityChoices = [
  { id: 'low', name: '低' },
  { id: 'normal', name: '普通' },
  { id: 'high', name: '高' },
  { id: 'urgent', name: '紧急' },
  { id: 'critical', name: '严重' },
]

const TicketBulkUpdateButton: React.FC = () => {
  const { selectedIds, onUnselectItems } = useListContext()
  const notify = useNotify()
  const [open, setOpen] = useState(false)
  const [status, setStatus] = useState('')
  const [priority, setPriority] = useState('')
  const [error, setError] = useState<string | null>(null)

  const hasSelection = (selectedIds ?? []).length > 0

  const [updateMany, { isLoading }] = useUpdateMany()

  const handleClose = () => {
    if (!isLoading) {
      setOpen(false)
      setError(null)
    }
  }

  const handleSubmit = async () => {
    const updates: Record<string, unknown> = {}
    if (status) {
      updates.status = status
    }
    if (priority) {
      updates.priority = priority
    }

    if (Object.keys(updates).length === 0) {
      setError('请至少选择一个需要更新的字段')
      return
    }

    try {
      await updateMany(
        'tickets',
        { ids: selectedIds, data: updates },
        {
          onSuccess: () => {
            notify('批量更新已提交', { type: 'success' })
            setOpen(false)
            setError(null)
            setStatus('')
            setPriority('')
            onUnselectItems?.()
          },
          onError: (error) => {
            notify(error?.message || '批量更新失败', { type: 'warning' })
          },
          mutationMode: 'pessimistic',
        }
      )
    } catch (err) {
      const message = err instanceof Error ? err.message : '批量更新失败'
      notify(message, { type: 'warning' })
    }
  }

  const selectionCount = useMemo(() => selectedIds?.length ?? 0, [selectedIds])

  const handleStatusChange = (event: SelectChangeEvent<string>) => {
    setStatus(event.target.value)
    if (error) setError(null)
  }

  const handlePriorityChange = (event: SelectChangeEvent<string>) => {
    setPriority(event.target.value)
    if (error) setError(null)
  }

  return (
    <>
      <Button
        size="small"
        variant="outlined"
        onClick={() => setOpen(true)}
        disabled={!hasSelection}
      >
        批量更新
      </Button>

      <Dialog open={open} onClose={handleClose} fullWidth maxWidth="sm">
        <DialogTitle>批量更新工单</DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ mt: 1 }}>
            <Alert severity="info">
              已选择 {selectionCount} 条工单记录，可在下方选择需要批量更新的字段。
            </Alert>

            <FormControl fullWidth size="small">
              <InputLabel id="bulk-status-label">更新状态</InputLabel>
              <Select
                labelId="bulk-status-label"
                value={status}
                label="更新状态"
                onChange={handleStatusChange}
                displayEmpty
              >
                <MenuItem value="">
                  <em>保持不变</em>
                </MenuItem>
                {statusChoices.map((choice) => (
                  <MenuItem key={choice.id} value={choice.id}>
                    {choice.name}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>

            <FormControl fullWidth size="small">
              <InputLabel id="bulk-priority-label">更新优先级</InputLabel>
              <Select
                labelId="bulk-priority-label"
                value={priority}
                label="更新优先级"
                onChange={handlePriorityChange}
                displayEmpty
              >
                <MenuItem value="">
                  <em>保持不变</em>
                </MenuItem>
                {priorityChoices.map((choice) => (
                  <MenuItem key={choice.id} value={choice.id}>
                    {choice.name}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>

            {error && (
              <Alert severity="warning" onClose={() => setError(null)}>
                {error}
              </Alert>
            )}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={handleClose} disabled={isLoading}>
            取消
          </Button>
          <Button
            onClick={handleSubmit}
            variant="contained"
            disabled={isLoading}
          >
            确认更新
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}

export default TicketBulkUpdateButton
