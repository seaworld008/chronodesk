// 用户相关类型
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
  role: 'admin' | 'agent' | 'customer' | 'supervisor'
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

// 认证相关类型
export interface LoginRequest {
  email: string
  password: string
  otp_code?: string
}

export interface RegisterRequest {
  username: string
  email: string
  password: string
  confirm_password?: string
}

export interface VerifyOTPRequest {
  email: string
  otp: string
}

export interface UpdateProfileRequest {
  first_name?: string
  last_name?: string
  phone_number?: string
  avatar?: string
  timezone?: string
  language?: string
}

export interface AuthResponse {
  access_token: string
  refresh_token: string
  user: User
  expires_in: number
  token_type: string
}

// 工单相关类型  
export type TicketStatus = 'open' | 'in_progress' | 'pending' | 'resolved' | 'closed' | 'cancelled'
export type TicketPriority = 'low' | 'normal' | 'medium' | 'high' | 'urgent' | 'critical'
export type TicketType = 'incident' | 'request' | 'problem' | 'change' | 'complaint' | 'consultation' | 'bug' | 'feature'
export type TicketSource = 'web' | 'email' | 'phone' | 'chat' | 'api' | 'mobile'
export type UserRole = 'admin' | 'agent' | 'customer' | 'supervisor'

// 分类相关类型
export interface Category {
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

export interface Ticket {
  id: number
  ticket_number?: string
  title: string
  description: string
  type?: TicketType
  priority: TicketPriority
  status: TicketStatus
  source?: TicketSource
  created_by_id: number
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
  sla_due_date?: string
  response_time?: number
  resolution_time?: number
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  attachments?: string[]
  custom_fields?: Record<string, unknown>
  comments?: Comment[]
  created_at: string
  updated_at: string
  // 兼容性字段，用于TicketForm组件
  createdAt: string
  updatedAt: string
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
  attachments?: string[]
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
  attachments?: string[]
  custom_fields?: Record<string, unknown>
}

// 评论相关类型
export interface Comment {
  id: number
  ticket_id: number
  user_id: number
  content: string
  comment_type?: 'public' | 'private' | 'internal'
  is_internal?: boolean
  author?: User
  attachments?: Attachment[]
  created_at: string
  updated_at: string
}

export interface CreateCommentRequest {
  ticket_id: number
  content: string
  comment_type?: 'public' | 'private' | 'internal'
  is_internal?: boolean
}

// API 响应类型
export interface ApiResponse<T = unknown> {
  code: number
  msg: string
  data: T
}

export interface PaginatedResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

// 查询参数类型
export interface TicketQueryParams {
  page?: number
  page_size?: number
  status?: TicketStatus
  priority?: TicketPriority
  type?: TicketType
  assigned_to_id?: number
  created_by_id?: number
  category_id?: number
  search?: string
  sort_by?: 'created_at' | 'updated_at' | 'priority' | 'status' | 'title'
  sort_order?: 'asc' | 'desc'
}

// 工单搜索参数
export interface TicketSearchParams {
  query?: string
  page?: number
  limit?: number
  sort_by?: 'created_at' | 'updated_at' | 'priority' | 'status' | 'title'
  sort_order?: 'asc' | 'desc'
  filters?: TicketFilters
}

// 工单过滤器
export interface TicketFilters {
  status?: TicketStatus[]
  priority?: TicketPriority[]
  type?: TicketType[]
  category?: number[]
  assignee?: number[]
  reporter?: number[]
  date_range?: {
    start: string
    end: string
  }
  tags?: string[]
}

// 工单列表响应
export interface TicketListResponse {
  items: Ticket[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

export * from './automation'

// 创建工单数据
export interface CreateTicketData {
  title: string
  description: string
  type?: TicketType
  priority: TicketPriority
  category_id?: number
  subcategory_id?: number
  due_date?: string
  tags?: string[]
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  attachments?: File[]
  custom_fields?: Record<string, unknown>
}

// 更新工单数据
export interface UpdateTicketData {
  title?: string
  description?: string
  type?: TicketType
  status?: TicketStatus
  priority?: TicketPriority
  category_id?: number
  subcategory_id?: number
  assigned_to_id?: number
  due_date?: string
  tags?: string[]
  customer_email?: string
  customer_phone?: string
  customer_name?: string
  attachments?: File[]
  custom_fields?: Record<string, unknown>
}

// 工单统计
export interface TicketStats {
  total: number
  open: number
  in_progress: number
  pending: number
  resolved: number
  closed: number
  cancelled?: number
  by_priority: {
    low: number
    normal: number
    high: number
    urgent: number
    critical: number
  }
  by_type?: {
    incident: number
    request: number
    problem: number
    change: number
    complaint: number
    consultation: number
  }
  by_category: Record<string, number>
}

// 工单历史记录类型
export type HistoryAction = 
  | 'create'
  | 'update'
  | 'status_change'
  | 'priority_change'
  | 'assign'
  | 'unassign'
  | 'comment'
  | 'attachment'
  | 'close'
  | 'reopen'
  | 'escalate'
  | 'merge'
  | 'split'
  | 'transfer'
  | 'resolve'
  | 'reject'
  | 'approve'
  | 'system'

export interface TicketHistory {
  id: number
  ticket_id: number
  user?: User
  action: HistoryAction
  description: string
  details?: Record<string, unknown>
  field_name?: string
  old_value?: string
  new_value?: string
  comment_id?: number
  attachment_id?: number
  duration?: number
  scheduled_at?: string
  completed_at?: string
  is_visible: boolean
  is_system: boolean
  is_automated: boolean
  is_important: boolean
  metadata?: Record<string, unknown>
  created_at: string
}

export interface HistoryFilter {
  ticket_id?: number
  user_id?: number
  actions?: HistoryAction[]
  field_name?: string
  is_visible?: boolean
  is_system?: boolean
  is_automated?: boolean
  is_important?: boolean
  date_from?: string
  date_to?: string
  page?: number
  limit?: number
  order_by?: string
  order_dir?: 'asc' | 'desc'
}

// 附件类型
export interface Attachment {
  id: number
  filename: string
  original_filename: string
  file_path: string
  file_size: number
  mime_type: string
  file_hash?: string
  ticket_id?: number
  comment_id?: number
  uploaded_by?: number
  uploader?: User
  created_at: string
  updated_at: string
}

// 邮箱配置相关类型
export interface EmailConfig {
  id: number
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password?: string
  smtp_from_email: string
  smtp_from_name: string
  smtp_encryption?: 'none' | 'tls' | 'ssl'
  email_verification_enabled: boolean
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface EmailConfigUpdateRequest {
  smtp_host?: string
  smtp_port?: number
  smtp_username?: string
  smtp_password?: string
  smtp_from_email?: string
  smtp_from_name?: string
  smtp_encryption?: 'none' | 'tls' | 'ssl'
  email_verification_enabled?: boolean
  is_active?: boolean
}

export interface EmailTestRequest {
  to_email: string
  smtp_host?: string
  smtp_port?: number
  smtp_username?: string
  smtp_password?: string
  smtp_from_email?: string
  smtp_from_name?: string
  smtp_encryption?: 'none' | 'tls' | 'ssl'
}

// Notification related types
export enum NotificationType {
  TICKET_ASSIGNED = 'ticket_assigned',
  TICKET_STATUS_CHANGED = 'ticket_status_changed',
  TICKET_COMMENTED = 'ticket_commented',
  TICKET_CREATED = 'ticket_created',
  TICKET_OVERDUE = 'ticket_overdue',
  TICKET_RESOLVED = 'ticket_resolved',
  TICKET_CLOSED = 'ticket_closed',
  SYSTEM_MAINTENANCE = 'system_maintenance',
  USER_MENTION = 'user_mention',
  SYSTEM_ALERT = 'system_alert',
}

export enum NotificationPriority {
  LOW = 'low',
  NORMAL = 'normal',
  HIGH = 'high',
  URGENT = 'urgent',
}

export enum NotificationChannel {
  IN_APP = 'in_app',
  EMAIL = 'email',
  WEBHOOK = 'webhook',
  WEBSOCKET = 'websocket',
}

export interface Notification {
  id: number
  created_at: string
  updated_at: string
  type: NotificationType
  title: string
  content: string
  priority: NotificationPriority
  channel: NotificationChannel
  recipient?: User
  sender?: User
  related_type: string
  related_id?: number
  related_ticket?: Ticket
  is_read: boolean
  read_at?: string
  is_sent: boolean
  sent_at?: string
  is_delivered: boolean
  delivered_at?: string
  action_url: string
  scheduled_at?: string
  expires_at?: string
  metadata?: Record<string, unknown>
  delivery_status: string
}

export interface NotificationFilter {
  recipient_id?: number
  sender_id?: number
  types?: NotificationType[]
  priorities?: NotificationPriority[]
  channels?: NotificationChannel[]
  is_read?: boolean
  is_sent?: boolean
  is_delivered?: boolean
  related_type?: string
  related_id?: number
  related_ticket_id?: number
  created_after?: string
  created_before?: string
  limit?: number
  offset?: number
  order_by?: string
  order_dir?: 'asc' | 'desc'
}

export interface NotificationPreference {
  id: number
  created_at: string
  updated_at: string
  user_id: number
  notification_type: NotificationType
  email_enabled: boolean
  in_app_enabled: boolean
  webhook_enabled: boolean
  do_not_disturb_start?: string
  do_not_disturb_end?: string
  max_daily_count: number
  batch_delivery: boolean
  batch_interval: number
}

export interface CreateNotificationRequest {
  type: NotificationType
  title: string
  content: string
  priority?: NotificationPriority
  channel?: NotificationChannel
  recipient_id: number
  sender_id?: number
  related_type?: string
  related_id?: number
  related_ticket_id?: number
  action_url?: string
  scheduled_at?: string
  expires_at?: string
  metadata?: Record<string, unknown>
}
