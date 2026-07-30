export type CrossProjectWorkbenchView = 'todo' | 'created' | 'assigned'

export type CrossProjectTicketStatus =
  | 'open'
  | 'in_progress'
  | 'pending'
  | 'resolved'
  | 'closed'
  | 'cancelled'

export type CrossProjectTicketPriority =
  | 'low'
  | 'normal'
  | 'high'
  | 'urgent'
  | 'critical'

export interface CrossProjectWorkbenchTicket {
  id: number
  public_id: string
  project_id: number
  project_key: string
  project_name: string
  ticket_number: string
  title: string
  type: string
  priority: CrossProjectTicketPriority
  status: CrossProjectTicketStatus
  created_by_id?: number
  assigned_to_id?: number
  assigned_to_name?: string
  due_date?: string
  sla_due_date?: string
  sla_breached: boolean
  version: number
  created_at: string
  updated_at: string
}

export interface CrossProjectWorkbenchPage {
  items: CrossProjectWorkbenchTicket[]
  total: number
  page: number
  page_size: number
  total_pages: number
  view: CrossProjectWorkbenchView
}
