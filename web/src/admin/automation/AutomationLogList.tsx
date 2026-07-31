import React, { useMemo } from 'react'
import {
  DateField,
  FilterButton,
  FunctionField,
  List,
  NumberField,
  ReferenceInput,
  TopToolbar,
} from 'react-admin'
import { Chip, Box, Tooltip } from '@mui/material'
import { AutomationLog, AutomationLogActionExecution } from '@/types'
import {
  EnterpriseDatagrid,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import {
  EnterpriseBooleanFilterInput,
  EnterpriseReferenceAutocompleteInput,
} from '@/components/inputs/EnterpriseFilterInputs'
import AutomationTabs from './AutomationTabs'

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
    <FilterButton />
  </TopToolbar>
)

const automationLogColumns: ResizableColumn[] = [
  { key: 'id', defaultWidth: 88, minWidth: 72, maxWidth: 136 },
  { key: 'rule.name', defaultWidth: 240, minWidth: 160, maxWidth: 440 },
  { key: 'ticket.ticket_number', defaultWidth: 160, minWidth: 120, maxWidth: 260 },
  { key: 'column-4', defaultWidth: 112, minWidth: 88, maxWidth: 180 },
  { key: 'executed_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
  { key: 'execution_time', defaultWidth: 132, minWidth: 104, maxWidth: 200 },
  { key: 'column-7', defaultWidth: 360, minWidth: 200, maxWidth: 640 },
  { key: 'column-8', defaultWidth: 280, minWidth: 180, maxWidth: 520 },
]

const actionTypeLabels: Record<string, string> = {
  assign: '分配工单',
  set_priority: '设置优先级',
  set_status: '设置状态',
  add_comment: '添加评论',
  escalate: '升级工单',
  notify: '发送通知',
}

const parseActions = (
  rawActions: AutomationLog['actions_executed'] | undefined,
): AutomationLogActionExecution[] => {
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
          <Chip
            size="small"
            sx={{ mr: 0.5, mb: 0.5 }}
            label={actionTypeLabels[action.type] || '其他动作'}
          />
        </Tooltip>
      ))}
      {hasMore ? '…' : null}
    </Box>
  )
}

const AutomationLogList: React.FC = () => (
  <>
    <AutomationTabs />
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
      >
        <EnterpriseReferenceAutocompleteInput label="规则" optionText="name" />
      </ReferenceInput>,
      <ReferenceInput
        key="ticket_id"
        source="ticket_id"
        reference="tickets"
        label="工单"
      >
        <EnterpriseReferenceAutocompleteInput label="工单" optionText="ticket_number" />
      </ReferenceInput>,
      <EnterpriseBooleanFilterInput key="success" source="success" label="成功" />,
    ]}
  >
    <EnterpriseDatagrid
      tableId="automation.logs"
      columns={automationLogColumns}
      aria-label="自动化日志列表"
      bulkActionButtons={false}
      rowClick={false}
    >
      <NumberField source="id" label="ID" />
      <FunctionField<AutomationLog>
        label="规则"
        sortBy="rule.name"
        render={(record) => (
          <TruncatedText title={record?.rule?.name}>{record?.rule?.name || '—'}</TruncatedText>
        )}
      />
      <FunctionField<AutomationLog>
        source="ticket.ticket_number"
        label="工单"
        render={(record) => (
          <TruncatedText title={record?.ticket?.ticket_number || '—'}>
            {record?.ticket?.ticket_number || '—'}
          </TruncatedText>
        )}
      />
      <FunctionField<AutomationLog> label="结果" render={(record) => <SuccessChip value={record?.success} />} />
      <DateField source="executed_at" label="执行时间" showTime />
      <NumberField source="execution_time" label="耗时（毫秒）" />
      <FunctionField<AutomationLog>
        label="摘要"
        render={(record) => (
          <TruncatedText title={record?.error_message || '执行成功'}>
            {record?.error_message || '执行成功'}
          </TruncatedText>
        )}
      />
      <FunctionField<AutomationLog> label="动作" render={(record) => <ActionsCell record={record} />} />
    </EnterpriseDatagrid>
    </List>
  </>
)

export default AutomationLogList
