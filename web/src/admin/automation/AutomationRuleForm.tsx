import React from 'react'
import { BooleanInput, NumberInput, SelectInput, TextInput, SimpleForm } from 'react-admin'
import BackButton from '../common/BackButton'
import { AutomationRuleAction, AutomationRuleCondition, AutomationRuleFormValues } from '@/types'

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

const formatJsonText = (value?: string) => {
  if (!value) return ''
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch (error) {
    return value
  }
}

const parseJsonText = (value: string) => value

const parseJsonArray = <T,>(text?: string): T[] => {
  if (!text || !text.trim()) {
    return []
  }

  try {
    const parsed = JSON.parse(text)
    if (Array.isArray(parsed)) {
      return parsed as T[]
    }
  } catch (error) {
    // fall through to error throw below
  }

  throw new Error('条件/动作需要有效的 JSON 数组')
}

const normalizePriority = (value: AutomationRuleFormValues['priority']): number => {
  if (typeof value === 'number') {
    return value
  }
  if (typeof value === 'string') {
    const numeric = Number(value)
    if (!Number.isNaN(numeric)) {
      return numeric
    }
  }
  return 1
}

// eslint-disable-next-line react-refresh/only-export-components
export const buildTransform = () => (data: AutomationRuleFormValues) => ({
  name: data.name,
  description: data.description,
  rule_type: data.rule_type,
  trigger_event: data.trigger_event,
  priority: normalizePriority(data.priority),
  is_active: data.is_active ?? true,
  conditions: parseJsonArray<AutomationRuleCondition>(data.conditions),
  actions: parseJsonArray<AutomationRuleAction>(data.actions),
})

const AutomationRuleForm: React.FC<{ toolbar?: React.ReactElement }> = ({ toolbar }) => (
  <SimpleForm toolbar={toolbar}>
    <BackButton fallbackPath="/automation-rules" />
    <TextInput source="name" label="名称" required fullWidth />
    <TextInput source="description" label="描述" multiline fullWidth />
    <SelectInput source="rule_type" label="规则类型" choices={ruleTypeChoices} required fullWidth />
    <SelectInput source="trigger_event" label="触发事件" choices={triggerEventChoices} required fullWidth />
    <NumberInput source="priority" label="优先级" defaultValue={1} min={1} />
    <BooleanInput source="is_active" label="启用" defaultValue={true} />
    <TextInput
      source="conditions"
      label="条件 (JSON数组)"
      multiline
      fullWidth
      minRows={4}
      parse={parseJsonText}
      format={formatJsonText}
      helperText={'例如: [{"field":"priority","operator":"eq","value":"high"}]'}
    />
    <TextInput
      source="actions"
      label="动作 (JSON数组)"
      multiline
      fullWidth
      minRows={4}
      parse={parseJsonText}
      format={formatJsonText}
      helperText={'例如: [{"type":"assign","params":{"user_id":1}}]'}
    />
  </SimpleForm>
)

export default AutomationRuleForm
