import type { UserRole } from '@/lib/accessControl'

export interface User {
  id: number
  username: string
  email: string
  phone?: string
  first_name?: string
  last_name?: string
  display_name?: string
  avatar?: string
  timezone?: string
  language?: string
  role: UserRole
  status: 'active' | 'inactive' | 'suspended' | 'deleted'
  email_verified: boolean
  phone_verified?: boolean
  two_factor_enabled?: boolean
  last_login_at?: string
  department?: string
  job_title?: string
  manager_id?: number
  tickets_created?: number
  tickets_assigned?: number
  tickets_resolved?: number
  created_at: string
  updated_at: string
}

export type TicketStatus =
  | 'open'
  | 'in_progress'
  | 'pending'
  | 'resolved'
  | 'closed'
  | 'cancelled'

export type TicketPriority =
  | 'low'
  | 'normal'
  | 'high'
  | 'urgent'
  | 'critical'

type TicketType =
  | 'incident'
  | 'request'
  | 'problem'
  | 'change'
  | 'complaint'
  | 'consultation'

type TicketSource = 'web' | 'email' | 'phone' | 'chat' | 'api' | 'mobile'

interface Category {
  id: number
  name: string
  description?: string
  color?: string
  icon?: string
  parent_id?: number | null
  parent?: Category
  children?: Category[]
  is_active: boolean
  sort_order?: number
  created_at: string
  updated_at: string
}

interface Comment {
  id: number
  ticket_id: number
  user_id: number
  content: string
  comment_type?: 'public' | 'private' | 'internal'
  is_internal?: boolean
  author?: User
  created_at: string
  updated_at: string
}

export interface Ticket {
  id: number
  version: number
  ticket_number?: string
  title: string
  description: string
  type?: TicketType
  priority: TicketPriority
  status: TicketStatus
  source?: TicketSource
  created_by_id?: number
  created_by?: User
  assigned_to_id?: number | null
  assigned_to?: User
  category_id?: number | null
  category?: Category
  subcategory_id?: number | null
  subcategory?: Category
  tags?: string[]
  due_date?: string
  resolved_at?: string
  closed_at?: string
  first_reply_at?: string
  sla_breached?: boolean
  is_overdue?: boolean
  sla_due_date?: string
  response_time?: number
  resolution_time?: number
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  custom_fields?: Record<string, unknown>
  comments?: Comment[]
  created_at: string
  updated_at: string
}

export interface CreateTicketRequest {
  title: string
  description: string
  type: TicketType
  priority: TicketPriority
  source?: TicketSource
  assigned_to_id?: number
  category_id?: number
  subcategory_id?: number
  tags?: string[]
  due_date?: string
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  custom_fields?: Record<string, unknown>
}

export interface UpdateTicketRequest {
  title?: string
  description?: string
  type?: TicketType
  priority?: TicketPriority
  status?: TicketStatus
  source?: TicketSource
  assigned_to_id?: number
  category_id?: number
  subcategory_id?: number
  tags?: string[]
  due_date?: string
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  custom_fields?: Record<string, unknown>
}

export * from './automation'
