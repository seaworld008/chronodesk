import type {
  AutomationLog as HumanAutomationLog,
  AutomationRuleAction as HumanAutomationRuleAction,
  AutomationRuleCondition as HumanAutomationRuleCondition,
  AutomationRuleRequest,
} from '@/lib/generated/human-api'

export type AutomationRuleCondition = HumanAutomationRuleCondition
export type AutomationRuleAction = HumanAutomationRuleAction
export type AutomationLogActionExecution = HumanAutomationRuleAction
export type AutomationLog = HumanAutomationLog

export type AutomationRuleFormValues = Omit<
  AutomationRuleRequest,
  'priority' | 'conditions' | 'actions'
> & {
  id?: number
  priority?: number | string
  conditions?: string
  actions?: string
}
