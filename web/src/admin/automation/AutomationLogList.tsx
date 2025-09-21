import React, { useMemo } from 'react'
import {
  BooleanInput,
  DateField,
  FunctionField,
  List,
  NumberField,
  ReferenceInput,
  TextField,
  TopToolbar,
  Datagrid,
  FilterLiveSearch,
} from 'react-admin'
import { Chip, Box, Tooltip } from '@mui/material'
import { AutomationLog, AutomationLogActionExecution } from '@/types'

const SuccessChip: React.FC<{ value?: boolean }> = ({ value }) => (
  <Chip
    size="small"
    label={value ? '成功' : '失败'}
    color={value ? 'success' : 'error'}
    sx={{ fontSize: '0.75rem' }}
  />
)

const LogListActions = () => (
  <TopToolbar>
    <FilterLiveSearch />
  </TopToolbar>
)

const parseActions = (rawActions: AutomationLog['actions_executed']): AutomationLogActionExecution[] => {
  if (!rawActions) {
    return []
  }

  if (Array.isArray(rawActions)) {
    return rawActions
  }

  try {
    const parsed = JSON.parse(rawActions)
    return Array.isArray(parsed) ? (parsed as AutomationLogActionExecution[]) : []
  } catch (error) {
    return []
  }
}

const ActionsCell: React.FC<{ record?: AutomationLog }> = ({ record }) => {
  const actions = useMemo(() => parseActions(record?.actions_executed), [record?.actions_executed])

  if (!record || actions.length === 0) {
    return <>—</>
  }

  const visibleActions = actions.slice(0, 2)
  const hasMore = actions.length > visibleActions.length

  return (
    <Box>
      {visibleActions.map((action, index) => (
        <Tooltip key={`${record.id}-action-${index}`} title={JSON.stringify(action, null, 2)} placement="top" arrow>
          <Chip size="small" sx={{ mr: 0.5, mb: 0.5 }} label={action.type || '动作'} />
        </Tooltip>
      ))}
      {hasMore ? '…' : null}
    </Box>
  )
}

const AutomationLogList: React.FC = () => (
  <List
    perPage={25}
    sort={{ field: 'executed_at', order: 'DESC' }}
    actions={<LogListActions />}
    filters={[
      <ReferenceInput
        key="rule_id"
        source="rule_id"
        reference="automation-rules"
        label="规则"
        alwaysOn
      />,
      <ReferenceInput
        key="ticket_id"
        source="ticket_id"
        reference="tickets"
        label="工单"
      />,
      <BooleanInput key="success" source="success" label="成功" />,
    ]}
  >
    <Datagrid bulkActionButtons={false} rowClick={false}>
      <NumberField source="id" label="ID" />
      <TextField source="rule.name" label="规则" />
      <TextField source="ticket.ticket_number" label="工单" />
      <FunctionField<AutomationLog> label="结果" render={(record) => <SuccessChip value={record?.success} />} />
      <DateField source="executed_at" label="执行时间" showTime />
      <NumberField source="execution_time" label="耗时(ms)" />
      <FunctionField<AutomationLog> label="摘要" render={(record) => record?.error_message || '执行成功'} />
      <FunctionField<AutomationLog> label="动作" render={(record) => <ActionsCell record={record} />} />
    </Datagrid>
  </List>
)

export default AutomationLogList
