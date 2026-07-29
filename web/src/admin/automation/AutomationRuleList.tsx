import React from 'react'
import {
  BooleanField,
  DateField,
  EditButton,
  FilterButton,
  FunctionField,
  List,
  NumberField,
  ShowButton,
  TopToolbar,
  CreateButton,
  WrapperField,
} from 'react-admin'
import { Box, Stack } from '@mui/material'
import {
  EnterpriseDatagrid,
  TruncatedText,
  type ResizableColumn,
} from '@/components/tables/EnterpriseTable'
import { EnterpriseFilterLiveSearch } from '@/components/inputs/EnterpriseSearchInput'
import {
  EnterpriseBooleanFilterInput,
  EnterpriseSelectFilterInput,
} from '@/components/inputs/EnterpriseFilterInputs'
import {
  automationTriggerEventChoices,
  automationTriggerEventLabel,
} from './triggerEvents'

const ruleTypeChoices = [
  { id: 'assignment', name: '自动分配' },
  { id: 'classification', name: '自动分类' },
  { id: 'escalation', name: '升级处理' },
  { id: 'sla', name: 'SLA' },
]

const automationRuleColumns: ResizableColumn[] = [
  { key: 'name', defaultWidth: 280, minWidth: 180, maxWidth: 520 },
  { key: 'rule_type', defaultWidth: 144, minWidth: 112, maxWidth: 240 },
  { key: 'trigger_event', defaultWidth: 180, minWidth: 128, maxWidth: 320 },
  { key: 'priority', defaultWidth: 104, minWidth: 80, maxWidth: 160 },
  { key: 'is_active', defaultWidth: 104, minWidth: 80, maxWidth: 160 },
  { key: 'column-6', defaultWidth: 220, minWidth: 160, maxWidth: 360 },
  { key: 'updated_at', defaultWidth: 184, minWidth: 144, maxWidth: 280 },
  { key: 'column-8', defaultWidth: 160, minWidth: 144, maxWidth: 220, sticky: 'right' },
]

const ListActions = () => (
  <TopToolbar>
    <EnterpriseFilterLiveSearch />
    <FilterButton />
    <CreateButton />
  </TopToolbar>
)

const AutomationRuleList: React.FC = () => (
  <List
    perPage={25}
    sort={{ field: 'priority', order: 'ASC' }}
    actions={<ListActions />}
    filters={[
      <EnterpriseSelectFilterInput
        key="rule_type"
        source="rule_type"
        label="规则类型"
        choices={ruleTypeChoices}
        alwaysOn
      />,
      <EnterpriseSelectFilterInput
        key="trigger_event"
        source="trigger_event"
        label="触发事件"
        choices={automationTriggerEventChoices}
      />,
      <EnterpriseBooleanFilterInput key="active" source="is_active" label="启用" />,
    ]}
  >
    <EnterpriseDatagrid
      tableId="automation.rules"
      columns={automationRuleColumns}
      aria-label="自动化规则列表"
      rowClick="show"
    >
      <FunctionField
        label="规则名称"
        sortBy="name"
        render={(record) => (
          <TruncatedText title={record?.name}>{record?.name || '—'}</TruncatedText>
        )}
      />
      <FunctionField
        label="类型"
        sortBy="rule_type"
        render={(record) => (
          <TruncatedText title={`规则类型代码：${record?.rule_type || '—'}`}>
            {ruleTypeChoices.find((choice) => choice.id === record?.rule_type)?.name ?? '未知类型'}
          </TruncatedText>
        )}
      />
      <FunctionField
        label="触发事件"
        sortBy="trigger_event"
        render={(record) => (
          <TruncatedText title={`触发事件代码：${record?.trigger_event || '—'}`}>
            {automationTriggerEventLabel(record?.trigger_event)}
          </TruncatedText>
        )}
      />
      <NumberField source="priority" label="优先级" />
      <BooleanField source="is_active" label="启用" />
      <FunctionField
        label="执行统计"
        render={(record) => (
          <Box component="span">
            成功 {record.success_count ?? 0} 次 / 失败 {record.failure_count ?? 0} 次
          </Box>
        )}
      />
      <DateField source="updated_at" label="更新时间" showTime />
      <WrapperField
        label="操作"
        cellClassName="cd-table-sticky-right"
        headerClassName="cd-table-sticky-right"
      >
        <Stack className="cd-table-actions" direction="row" spacing={0.5}>
          <ShowButton />
          <EditButton />
        </Stack>
      </WrapperField>
    </EnterpriseDatagrid>
  </List>
)

export default AutomationRuleList
