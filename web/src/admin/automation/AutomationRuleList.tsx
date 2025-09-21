import React from 'react'
import {
  BooleanField,
  DateField,
  EditButton,
  FunctionField,
  List,
  NumberField,
  ShowButton,
  TextField,
  TopToolbar,
  CreateButton,
  FilterLiveSearch,
} from 'react-admin'
import { Datagrid, BooleanInput, SelectInput } from 'react-admin'
import { Box } from '@mui/material'

const ruleTypeChoices = [
  { id: 'assignment', name: '自动分配' },
  { id: 'classification', name: '自动分类' },
  { id: 'escalation', name: '升级处理' },
  { id: 'sla', name: 'SLA' },
]

const triggerEventChoices = [
  { id: 'ticket.created', name: '工单创建' },
  { id: 'ticket.updated', name: '工单更新' },
  { id: 'ticket.assigned', name: '工单分配' },
  { id: 'ticket.resolved', name: '工单解决' },
  { id: 'ticket.closed', name: '工单关闭' },
  { id: 'scheduled_check', name: '定时检查' },
]

const ListActions = () => (
  <TopToolbar>
    <FilterLiveSearch />
    <CreateButton />
  </TopToolbar>
)

const AutomationRuleList: React.FC = () => (
  <List
    perPage={25}
    sort={{ field: 'priority', order: 'ASC' }}
    actions={<ListActions />}
    filters={[
      <SelectInput
        key="rule_type"
        source="rule_type"
        label="规则类型"
        choices={ruleTypeChoices}
        alwaysOn
      />,
      <SelectInput
        key="trigger_event"
        source="trigger_event"
        label="触发事件"
        choices={triggerEventChoices}
      />,
      <BooleanInput key="active" source="is_active" label="启用" />,
    ]}
  >
    <Datagrid rowClick="show">
      <TextField source="name" label="规则名称" />
      <TextField source="rule_type" label="类型" />
      <TextField source="trigger_event" label="触发事件" />
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
      <ShowButton />
      <EditButton />
    </Datagrid>
  </List>
)

export default AutomationRuleList
