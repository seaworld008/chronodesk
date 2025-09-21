export interface AutomationRuleSummary {
  id: number
  name: string
  description?: string
  rule_type: string
  trigger_event: string
  priority: number
  is_active: boolean
  success_count?: number
  failure_count?: number
  execution_count?: number
  updated_at?: string
  created_at?: string
}

export interface AutomationTicketSummary {
  id: number
  ticket_number?: string
  title?: string
  status?: string
}

export interface AutomationRuleCondition {
  field: string
  operator: string
  value: unknown
  logic_op?: string
}

export interface AutomationRuleAction {
  type: string
  params?: Record<string, unknown>
}

export interface AutomationLogActionExecution extends AutomationRuleAction {}

export interface AutomationLog {
  id: number
  rule_id: number
  ticket_id: number
  trigger_event: string
  executed_at: string
  success: boolean
  error_message?: string
  execution_time?: number
  actions_executed?: string | AutomationLogActionExecution[]
  rule?: AutomationRuleSummary
  ticket?: AutomationTicketSummary
}

export interface AutomationRuleFormValues {
  id?: number
  name: string
  description?: string
  rule_type: string
  trigger_event: string
  priority?: number | string
  is_active?: boolean
  conditions?: string
  actions?: string
}
