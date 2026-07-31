/**
 * Generated from server/internal/humanopenapi/openapi.json.
 * Generator: chronodesk-human-openapi-types@2.0.0.
 * Contract SHA-256: c998ba3ad08fcac6ec1d12390313558bee900ebd8cffb41763b3cee387243fbb.
 * Do not edit by hand; run `npm run generate:human-api`.
 */

export const platformRoleValues = ["platform_admin","security_auditor","emergency_operator","member"] as const
export type PlatformRole = (typeof platformRoleValues)[number]

export const projectRoleValues = ["project_admin","manager","agent","requester","observer"] as const
export type ProjectRole = (typeof projectRoleValues)[number]

export type UserStatus = "active" | "inactive" | "suspended" | "deleted"

export type ProjectStatus = "active" | "archived"

export type PublicUUIDv7 = string

export type LoginRequest = {
    email: string
    password: string
    otp_code?: string
    remember_device?: boolean
    device_name?: string
}

export type RefreshTokenRequest = {
    refresh_token: string
}

export type ForgotPasswordRequest = {
    email: string
}

export type ResetHumanPasswordRequest = {
    token: string
    new_password: string
}

export type LogoutRequest = {
    refresh_token?: string
}

export type UpdateHumanProfileRequest = {
    first_name?: string
    last_name?: string
    phone_number?: string | string
    avatar?: string
    timezone?: string
    language?: "zh-CN" | "en"
}

export type UpsertProjectMembershipRequest = {
    user_id: number
    role: ProjectRole
}

export type CreatePlatformProjectRequest = {
    business_unit_public_id: PublicUUIDv7
    key: string
    name: string
    description?: string
    initial_project_admin_user_ids: Array<number>
    default_queue_key: string
    default_queue_name: string
}

export type CreateAdminUserRequest = {
    username: string
    email: string
    password: string
    phone?: string
    first_name?: string
    last_name?: string
    display_name?: string
    platform_role: PlatformRole
    department?: string
    job_title?: string
    manager_id?: number | null
}

export type UpdateAdminUserRequest = {
    email?: string
    phone?: string | string | null
    first_name?: string
    last_name?: string
    display_name?: string
    avatar?: string
    timezone?: string
    language?: string
    platform_role?: PlatformRole
    status?: UserStatus
    email_verified?: boolean
    department?: string
    job_title?: string
    manager_id?: number | null
}

export type ResetAdminUserPasswordRequest = {
    new_password: string
}

export type HumanUserProfile = {
    id: number
    user_id: number
    first_name: string
    last_name: string
    display_name: string
    avatar: string
    phone: string
    department: string
    position: string
    timezone: string
    language: string
    created_at: string
    updated_at: string
}

export type HumanSessionUser = {
    id: number
    username: string
    email: string
    platform_role: PlatformRole
    status: UserStatus
    email_verified: boolean
    otp_enabled: boolean
    last_login_at: string | null
    profile?: HumanUserProfile
}

export type AuthSession = {
    user: HumanSessionUser
    access_token: string
    refresh_token: string
    expires_in: number
    token_type: "Bearer"
}

export type PlatformProjectSummary = {
    public_id: PublicUUIDv7
    created_at: string
    updated_at: string
    key: string
    name: string
    description: string
    status: ProjectStatus
    business_unit: PlatformBusinessUnitSummary
}

export type PlatformOrganizationSummary = {
    public_id: PublicUUIDv7
    name: string
}

export type PlatformBusinessUnitSummary = {
    public_id: PublicUUIDv7
    key: string
    name: string
    description: string
}

export type ProjectUserOption = {
    id: number
    username: string
    display_name: string
    avatar: string
}

export type ProjectUserOptionPage = {
    items: Array<ProjectUserOption>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type PlatformBusinessUnitPage = {
    items: Array<PlatformBusinessUnitSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ProjectCreationContext = {
    organization: PlatformOrganizationSummary
    business_units: PlatformBusinessUnitPage
    creator: ProjectUserOption
    users: ProjectUserOptionPage
}

export type PlatformProjectPage = {
    items: Array<PlatformProjectSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AuthorizedProject = {
    id: number
    public_id: string
    created_at: string
    updated_at: string
    organization_id: number
    business_unit_id: number
    key: string
    name: string
    description: string
    status: ProjectStatus
}

export type ProjectScope = {
    organization_id: number
    project_id: number
}

export type AuthorizedProjectAccess = {
    project: AuthorizedProject
    project_role: ProjectRole
    scope: ProjectScope
    scopes?: Array<string>
}

export type ProjectMembership = {
    id: number
    project_id: number
    user_id: number
    user?: HumanUserSummary
    role: ProjectRole
    is_active: boolean
    version: number
    created_at: string
    updated_at: string
}

export type HumanUserSummary = {
    id: number
    username: string
    display_name: string
    avatar: string
}

export type AdminUser = {
    id: number
    created_at: string
    updated_at: string
    username: string
    email: string
    phone: string
    first_name: string
    last_name: string
    display_name: string
    avatar: string
    timezone: string
    language: string
    platform_role: PlatformRole
    status: UserStatus
    email_verified: boolean
    phone_verified: boolean
    two_factor_enabled: boolean
    last_login_at: string | null
    department: string
    job_title: string
    manager_id: number | null
    tickets_created: number
    tickets_assigned: number
    tickets_resolved: number
}

export type AdminUserPage = {
    items: Array<AdminUser>
    total: number
    page: number
    page_size: number
    pages: number
}

export type AdminUserStats = {
    total_users: number
    active_users: number
    users_by_platform_role: {
        platform_admin?: number
        security_auditor?: number
        emergency_operator?: number
        member?: number
    }
    new_users_this_week: number
}

export type AdminAuditLog = {
    id: number
    created_at: string
    user_id?: number
    username: string
    platform_role: PlatformRole
    action: string
    action_code?: string
    resource_type?: string
    resource_public_id?: string
    method: string
    path: string
    status_code: number
    masked_ip: string
    latency_ms: number
    result: string
}

export type AdminAuditLogDetail = {
    id: number
    created_at: string
    user_id?: number
    username: string
    platform_role: PlatformRole
    action: string
    action_code?: string
    resource_type?: string
    resource_public_id?: string
    method: string
    path: string
    status_code: number
    masked_ip: string
    latency_ms: number
    result: string
    query: string
    user_agent: string
    notes: string
    request_id?: string
    trace_id?: string
    correlation_id?: string
}

export type AdminAuditLogPage = {
    items: Array<AdminAuditLog>
    total: number
    page: number
    limit: number
    next_cursor?: string
}

export type SuccessEnvelope = {
    code: 0
    msg: string
}

export type PlatformProjectSummaryEnvelope = {
    code: 0
    msg: string
    data: PlatformProjectSummary
}

export type PlatformProjectPageEnvelope = {
    code: 0
    msg: string
    data: PlatformProjectPage
}

export type PlatformBusinessUnitPageEnvelope = {
    code: 0
    msg: string
    data: PlatformBusinessUnitPage
}

export type ProjectCreationContextEnvelope = {
    code: 0
    msg: string
    data: ProjectCreationContext
}

export type ProjectUserOptionPageEnvelope = {
    code: 0
    msg: string
    data: ProjectUserOptionPage
}

export type StandardErrorEnvelope = {
    code: number
    msg: string
    data?: unknown
}

export type AuthErrorEnvelope = {
    error: string
    message?: string
    code?: string
}

export type CodedErrorEnvelope = {
    code: string
    msg: string
}

export type RecoveryErrorEnvelope = {
    success: false
    error: {
        code: "internal_error"
        message: string
        request_id?: string
    }
}

export type ErrorEnvelope = StandardErrorEnvelope | AuthErrorEnvelope | CodedErrorEnvelope | RecoveryErrorEnvelope

export type AuthSessionEnvelope = SuccessEnvelope & {
    data: AuthSession
}

export type AuthSessionSuccessEnvelope = {
    success: true
    message: "登录令牌刷新成功"
    data: AuthSession
}

export type HumanSessionUserSuccessEnvelope = {
    success: true
    message?: string
    data: HumanSessionUser
}

export type AuthMessageSuccessEnvelope = {
    success: true
    message: string
}

export type ProjectMembershipEnvelope = SuccessEnvelope & {
    data: ProjectMembership
}

export type AdminUserEnvelope = SuccessEnvelope & {
    data: AdminUser
}

export type AdminUserStatsEnvelope = SuccessEnvelope & {
    data: AdminUserStats
}

export type EmptySuccessEnvelope = SuccessEnvelope & {
    data: null
}

export type AdminAuditLogPageEnvelope = SuccessEnvelope & {
    data: AdminAuditLogPage
}

export type AdminAuditLogDetailEnvelope = SuccessEnvelope & {
    data: AdminAuditLogDetail
}

export const ticketStatusValues = ["open","in_progress","pending","resolved","closed","cancelled"] as const
export type TicketStatus = (typeof ticketStatusValues)[number]

export const ticketPriorityValues = ["low","normal","high","urgent","critical"] as const
export type TicketPriority = (typeof ticketPriorityValues)[number]

export const ticketTypeValues = ["incident","request","problem","change","complaint","consultation"] as const
export type TicketType = (typeof ticketTypeValues)[number]

export const ticketSourceValues = ["web","email","phone","chat","api","mobile","agent"] as const
export type TicketSource = (typeof ticketSourceValues)[number]

export type TicketTrustLevel = "untrusted" | "verified" | "trusted" | "system"

export type ActorType = "human" | "service_principal" | "system"

export type ActorRef = {
    type: ActorType
    id: string
}

export type AgentContext = {
    goal?: string
    constraints?: Array<string>
    acceptance_criteria?: Array<string>
    missing_information?: Array<string>
    related_resources?: Array<string>
}

export type TicketCategory = {
    id: number
    name: string
    description?: string
    color?: string
    icon?: string
    parent_id?: number | null
    is_active?: boolean
    sort_order?: number
    created_at?: string
    updated_at?: string
}

export type CreateTicketRequest = {
    title: string
    description: string
    type: TicketType
    priority: TicketPriority
    status?: TicketStatus
    source: TicketSource
    request_type_version_id: string
    workflow_version_id?: string
    assigned_to_id?: number | null
    category_id?: number | null
    subcategory_id?: number | null
    tags?: Array<string>
    due_date?: string | null
    customer_email?: string
    customer_phone?: string
    customer_name?: string
    custom_fields?: unknown
    agent_context?: AgentContext
}

export type UpdateTicketRequest = {
    title?: string | null
    description?: string | null
    type?: TicketType
    priority?: TicketPriority
    status?: TicketStatus
    source?: TicketSource
    assigned_to_id?: number | null
    category_id?: number | null
    subcategory_id?: number | null
    tags?: Array<string>
    due_date?: string | null
    customer_email?: string | null
    customer_phone?: string | null
    customer_name?: string | null
    internal_notes?: string | null
    rating?: number | null
    rating_comment?: string | null
    custom_fields?: unknown | null
    agent_context?: AgentContext
}

export type AssignTicketRequest = {
    assigned_to_id: number
    comment?: string
}

export type TransferTicketRequest = {
    assigned_to_id: number
    department?: string
    comment?: string
    transfer_reason?: string
}

export type EscalateTicketRequest = {
    reason: string
    escalate_to_id: number
    comment?: string
}

export type UpdateTicketStatusRequest = {
    status: TicketStatus
    comment?: string
    resolution_notes?: string
}

export type TicketWorkflowEnvelope = {
    success: true
    data: Ticket
    message: string
}

export type HumanServicePrincipalSummary = {
    id: string
    name: string
}

export type TicketHistoryAction = "create" | "update" | "status_change" | "priority_change" | "assign" | "unassign" | "comment" | "attachment" | "close" | "reopen" | "escalate" | "merge" | "split" | "transfer" | "resolve" | "reject" | "approve" | "system"

export type TicketHistoryProvenance = "domain_event" | "pre_event" | "imported"

export type TicketHistory = {
    id: number
    created_at: string
    ticket_id: number
    user?: HumanUserSummary
    actor: ActorRef
    service_principal?: HumanServicePrincipalSummary
    event_id?: string | null
    resource_version: number
    provenance: TicketHistoryProvenance
    action: TicketHistoryAction
    description: string
    details: unknown
    field_name: string
    old_value: string
    new_value: string
    comment_id: number | null
    attachment_id: number | null
    duration: number | null
    scheduled_at: string | null
    completed_at: string | null
    is_visible: boolean
    is_system: boolean
    is_automated: boolean
    is_important: boolean
    metadata: unknown
}

export type TicketHistoryListEnvelope = {
    success: true
    data: Array<TicketHistory>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketCommentType = "public" | "internal" | "system"

export type CreateTicketCommentRequest = {
    content: string
    content_type?: "text" | "markdown"
    type?: "public" | "internal"
    parent_id?: number | null
    time_spent?: number | null
    billable_time?: number | null
    work_type?: string
}

export type TicketComment = {
    id: number
    created_at: string
    updated_at?: string
    ticket_id: number
    organization_id?: number
    project_id?: number
    user?: HumanUserSummary
    actor?: ActorRef
    service_principal?: HumanServicePrincipalSummary
    content: string
    content_type: "text" | "html" | "markdown"
    type: TicketCommentType
    metadata?: unknown
    is_edited: boolean
    edited_at?: string | null
    is_deleted?: boolean
    deleted_by?: HumanUserSummary
    parent_id?: number | null
    replies?: Array<TicketComment>
    reply_count: number
    time_spent?: number | null
    billable_time?: number | null
    work_type?: string
    notification_sent?: boolean
    is_helpful?: boolean | null
    helpful_count?: number
    unhelpful_count?: number
}

export type TicketCommentListEnvelope = {
    success: true
    data: Array<TicketComment>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketCommentEnvelope = {
    success: true
    data: TicketComment
    receipt: Receipt
}

export type TicketAttachmentType = "image" | "document" | "video" | "audio" | "archive" | "other"

export type TicketAttachmentVirusScan = "pending" | "clean" | "infected" | "error"

export type UploadTicketAttachmentRequest = {
    file: string
    visibility?: "public" | "internal"
    is_public?: boolean
    comment_id?: number
    description?: string
}

export type TicketAttachment = {
    id: number
    created_at: string
    updated_at: string
    ticket_id: number
    organization_id?: number
    project_id?: number
    comment_id?: number | null
    uploaded_by?: number | null
    actor_type?: ActorType
    actor_id?: string
    service_principal_id?: string | null
    file_name?: string
    original_name: string
    file_size: number
    mime_type: string
    file_type: TicketAttachmentType
    extension: string
    is_public: boolean
    download_count?: number
    hash?: string
    virus_scan: TicketAttachmentVirusScan
    scan_details?: string
    scanned_at?: string | null
    description?: string
    width?: number
    height?: number
    page_count?: number
}

export type TicketAttachmentListEnvelope = {
    success: true
    data: Array<TicketAttachment>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketAttachmentEnvelope = {
    success: true
    data: TicketAttachment
    receipt: Receipt
}

export type Ticket = {
    id: number
    public_id: string
    created_at: string
    updated_at: string
    organization_id: number
    project_id: number
    queue_id: number
    request_type_version_id: string
    workflow_version_id: string
    ticket_number: string
    title: string
    description: string
    type: TicketType
    priority: TicketPriority
    status: TicketStatus
    source: TicketSource
    created_by_id?: number
    created_by?: HumanUserSummary
    assigned_to_id?: number
    assigned_to?: HumanUserSummary
    category_id?: number
    category?: TicketCategory
    subcategory_id?: number
    subcategory?: TicketCategory
    tags: Array<string>
    due_date: string | null
    resolved_at: string | null
    closed_at: string | null
    first_reply_at: string | null
    sla_breached: boolean
    sla_due_date: string | null
    response_time: number | null
    resolution_time: number | null
    customer_email: string
    customer_phone: string
    customer_name: string
    custom_fields: unknown
    view_count: number
    comment_count: number
    rating: number | null
    rating_comment: string
    version: number
    agent_context: AgentContext
    trust_level: TicketTrustLevel
    created_by_actor: ActorRef
    assigned_to_actor?: ActorRef
    is_overdue: boolean
    is_escalated: boolean
}

export type TicketPage = {
    items: Array<Ticket>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketEnvelope = SuccessEnvelope & {
    data: Ticket
}

export type TicketListPageEnvelope = {
    success: true
    data: Array<Ticket>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketPageEnvelope = SuccessEnvelope & {
    data: TicketPage
}

export const crossProjectWorkbenchViewValues = ["todo","created","assigned"] as const
export type CrossProjectWorkbenchView = (typeof crossProjectWorkbenchViewValues)[number]

export type CrossProjectWorkbenchTicket = {
    id: number
    public_id: string
    project_id: number
    project_key: string
    project_name: string
    ticket_number: string
    title: string
    type: TicketType
    priority: TicketPriority
    status: TicketStatus
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

export type CrossProjectWorkbenchPage = {
    items: Array<CrossProjectWorkbenchTicket>
    total: number
    page: number
    page_size: number
    total_pages: number
    view: CrossProjectWorkbenchView
}

export type CrossProjectWorkbenchPageEnvelope = SuccessEnvelope & {
    data: CrossProjectWorkbenchPage
}

export type WorkbenchDashboardProject = {
    key: string
    name: string
}

export type WorkbenchDashboardStatusCounts = {
    open: number
    in_progress: number
    pending: number
    resolved: number
    closed: number
    cancelled: number
}

export type WorkbenchDashboardPriorityCounts = {
    low: number
    normal: number
    high: number
    urgent: number
    critical: number
}

export type WorkbenchDashboardAssignmentCounts = {
    assigned: number
    unassigned: number
    human: number
    service_principal: number
}

export type WorkbenchDashboardSummary = {
    total: number
    status: WorkbenchDashboardStatusCounts
    priority: WorkbenchDashboardPriorityCounts
    sla_breached: number
    overdue: number
    assignment: WorkbenchDashboardAssignmentCounts
}

export type WorkbenchDashboardDailyPoint = {
    date: string
    created: number
}

export type WorkbenchDashboardProjectBreakdown = {
    project_key: string
    project_name: string
    total: number
    sla_breached: number
    overdue: number
}

export type WorkbenchDashboard = {
    generated_at: string
    days: 7 | 30 | 90
    selected_projects: Array<WorkbenchDashboardProject>
    summary: WorkbenchDashboardSummary
    daily_trend: Array<WorkbenchDashboardDailyPoint>
    project_breakdown: Array<WorkbenchDashboardProjectBreakdown>
}

export type WorkbenchDashboardEnvelope = SuccessEnvelope & {
    data: WorkbenchDashboard
}

export const notificationTypeValues = ["ticket_assigned","ticket_status_changed","ticket_commented","ticket_created","ticket_overdue","ticket_resolved","ticket_closed","system_maintenance","user_mention","system_alert"] as const
export type NotificationType = (typeof notificationTypeValues)[number]

export const notificationPriorityValues = ["low","normal","high","urgent"] as const
export type NotificationPriority = (typeof notificationPriorityValues)[number]

export const notificationChannelValues = ["in_app","email","webhook","websocket"] as const
export type NotificationChannel = (typeof notificationChannelValues)[number]

export type CreateNotificationRequest = {
    type: NotificationType
    title: string
    content: string
    priority?: NotificationPriority
    channel?: NotificationChannel
    recipient_id: number
    sender_id?: number | null
    related_type?: string
    related_id?: number | null
    related_ticket_id?: number | null
    action_url?: string
    scheduled_at?: string | null
    expires_at?: string | null
    metadata?: unknown
}

export type Notification = {
    id: number
    created_at: string
    updated_at: string
    type: NotificationType
    title: string
    content: string
    priority: NotificationPriority
    channel: NotificationChannel
    recipient?: HumanUserSummary
    sender?: HumanUserSummary
    related_type: string
    related_id: number | null
    related_ticket?: Ticket
    is_read: boolean
    read_at: string | null
    is_sent: boolean
    sent_at: string | null
    is_delivered: boolean
    delivered_at: string | null
    action_url: string
    scheduled_at: string | null
    expires_at: string | null
    metadata: unknown
    delivery_status: string
}

export type NotificationPage = {
    items: Array<Notification>
    page: number
    page_size: number
    total: number
    total_pages: number
}

export type NotificationPreference = {
    id: number
    created_at: string
    updated_at: string
    user_id: number
    notification_type: NotificationType
    email_enabled: boolean
    in_app_enabled: boolean
    webhook_enabled: boolean
    do_not_disturb_start: string | null
    do_not_disturb_end: string | null
    max_daily_count: number
    batch_delivery: boolean
    batch_interval: number
}

export type NotificationPreferenceUpdate = {
    notification_type: NotificationType
    email_enabled: boolean
    in_app_enabled: boolean
    webhook_enabled: boolean
    do_not_disturb_start?: string | null
    do_not_disturb_end?: string | null
    max_daily_count: number
    batch_delivery: boolean
    batch_interval: number
}

export type UpdateNotificationPreferencesRequest = {
    preferences: Array<NotificationPreferenceUpdate>
}

export type NotificationPageEnvelope = SuccessEnvelope & {
    data: NotificationPage
}

export type NotificationEnvelope = SuccessEnvelope & {
    data: Notification
}

export type NotificationPreferencesEnvelope = SuccessEnvelope & {
    data: Array<NotificationPreference>
}

export type UnreadNotificationCount = {
    count: number
}

export type MessageEnvelope = {
    message: string
}

export type SimpleErrorEnvelope = {
    error: string
}

export type AutomationRuleCondition = {
    field: string
    operator: string
    value: string | number | number | boolean | unknown | Array<unknown> | null
    logic_op?: string
}

export type AutomationRuleAction = {
    type: string
    params?: unknown
}

export type AutomationRuleRequest = {
    name: string
    description?: string
    rule_type: "assignment" | "classification" | "escalation" | "sla"
    is_active?: boolean
    priority?: number
    trigger_event: string
    conditions?: Array<AutomationRuleCondition>
    actions?: Array<AutomationRuleAction>
}

export type AutomationRule = {
    id: number
    created_at: string
    updated_at: string
    organization_id: number
    project_id: number
    name: string
    description: string
    rule_type: string
    is_active: boolean
    priority: number
    trigger_event: string
    conditions: string
    actions: string
    execution_count: number
    last_executed_at?: string
    success_count: number
    failure_count: number
    average_exec_time: number
    created_by: number
    updated_by?: number
}

export type AutomationRulePage = {
    rules: Array<AutomationRule>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AutomationRuleLogSummary = {
    id: number
    name: string
    description: string
    rule_type: string
    trigger_event: string
    priority: number
    is_active: boolean
    success_count: number
    failure_count: number
    execution_count: number
    created_at: string
    updated_at: string
}

export type AutomationTicketLogSummary = {
    id: number
    ticket_number: string
    title: string
    status: TicketStatus
}

export type AutomationLog = {
    id: number
    created_at: string
    organization_id: number
    project_id: number
    rule_id: number
    rule?: AutomationRuleLogSummary
    ticket_id: number
    ticket?: AutomationTicketLogSummary
    trigger_event: string
    executed_at: string
    success: boolean
    error_message?: string
    execution_time: number
    actions_executed: string
    changes: string
}

export type AutomationLogPage = {
    logs: Array<AutomationLog>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AutomationRuleEnvelope = {
    success: true
    message: string
    data: AutomationRule
}

export type AutomationRulePageEnvelope = {
    success: true
    message: string
    data: AutomationRulePage
}

export type AutomationLogPageEnvelope = {
    success: true
    message: string
    data: AutomationLogPage
}

export type LegacyMessageSuccessEnvelope = {
    success: true
    message: string
}

export type LegacyErrorEnvelope = {
    success: false
    message: string
    error?: string
}

export type EmailConfig = {
    id: number
    created_at: string
    updated_at: string
    email_verification_enabled: boolean
    smtp_host: string
    smtp_port: number
    smtp_username: string
    smtp_use_tls: boolean
    smtp_use_ssl: boolean
    from_email: string
    from_name: string
    welcome_email_subject: string
    welcome_email_template: string
    otp_email_subject: string
    otp_email_template: string
    is_active: boolean
    is_configured: boolean
    can_send_email: boolean
    updated_by_id: number | null
}

export type UpdateEmailConfigRequest = {
    email_verification_enabled?: boolean
    smtp_host?: string
    smtp_port?: number
    smtp_username?: string
    smtp_password?: string
    smtp_use_tls?: boolean
    smtp_use_ssl?: boolean
    from_email?: string
    from_name?: string
    welcome_email_subject?: string
    welcome_email_template?: string
    otp_email_subject?: string
    otp_email_template?: string
    skip_smtp_test?: boolean
}

export type TestEmailRequest = {
    to_email: string
    subject: string
    content: string
}

export type EmailConfigEnvelope = SuccessEnvelope & {
    data: EmailConfig
}

export type SystemConfig = {
    id: number
    created_at: string
    updated_at: string
    key: string
    value: string
    value_type: "string" | "int" | "bool" | "json"
    description: string
    category: string
    group: string
    is_required: boolean
    is_active: boolean
    default_value: string
    min_value?: number
    max_value?: number
    valid_values?: string
    updated_by?: number
    version: number
}

export type UpdateSystemConfigRequest = {
    value: string
    value_type: "string" | "int" | "bool" | "json"
    description: string
    category: string
    group: string
}

export type SystemConfigUpdateEnvelope = {
    success: true
    message: string
    data: UpdateSystemConfigRequest
}

export type SystemConfigListEnvelope = {
    success: true
    message: string
    data: Array<SystemConfig>
}

export const webhookProviderValues = ["wechat","dingtalk","lark","slack","teams","custom"] as const
export type WebhookProvider = (typeof webhookProviderValues)[number]

export const webhookStatusValues = ["active","inactive","disabled","error"] as const
export type WebhookStatus = (typeof webhookStatusValues)[number]

export type WebhookFilterRules = {
    transition_statuses?: Array<TicketStatus>
}

export type CreateWebhookRequest = {
    name: string
    description?: string
    provider: WebhookProvider
    webhook_url: string
    enabled_events?: Array<string>
    message_template?: string
    message_format?: string
    filter_rules?: WebhookFilterRules
    retry_count?: number
    retry_interval?: number
    timeout_seconds?: number
    is_async?: boolean
    rate_limit?: number
    rate_limit_window?: number
    secret?: string
    access_token?: string
}

export type UpdateWebhookRequest = {
    name?: string
    description?: string
    provider?: WebhookProvider
    webhook_url?: string
    enabled_events?: Array<string>
    message_template?: string
    message_format?: string
    filter_rules?: WebhookFilterRules
    retry_count?: number
    retry_interval?: number
    timeout_seconds?: number
    is_async?: boolean
    rate_limit?: number
    rate_limit_window?: number
    secret?: string
    secret_overlap_seconds?: number
    access_token?: string
    status?: WebhookStatus
}

export type WebhookConfig = {
    name: string
    description: string
    provider: WebhookProvider
    webhook_url: string
    enabled_events: string
    message_template: string
    message_format: string
    filter_rules: string
    retry_count: number
    retry_interval: number
    timeout_seconds: number
    is_async: boolean
    rate_limit: number
    rate_limit_window: number
    id: number
    created_at: string
    updated_at: string
    organization_id: number
    project_id: number
    status: WebhookStatus
    previous_secret_expires_at?: string
    enabled_events_list?: Array<string>
    filter_rules_obj?: WebhookFilterRules
    last_triggered_at?: string
    last_success_at?: string
    last_error_at?: string
    last_error: string
    total_sent: number
    total_success: number
    total_failed: number
    created_by: number
    updated_by?: number
}

export type WebhookPage = {
    items: Array<WebhookConfig>
    total: number
    page: number
    size: number
}

export type WebhookTestReceipt = {
    operation_id: string
    event_id: string
    delivery_id: string
    snapshot_id: string
    config_id: number
    configuration_version: string
    status: "queued"
    queued: true
    delivered: false
}

export type WebhookEnvelope = SuccessEnvelope & {
    data: WebhookConfig
}

export type WebhookPageEnvelope = SuccessEnvelope & {
    data: WebhookPage
}

export type WebhookTestReceiptEnvelope = SuccessEnvelope & {
    data: WebhookTestReceipt
}

export type AdminOverviewEnvelope = Envelope & {
    data?: AdminOverview
}

export type Envelope = {
    data: unknown
    meta: Meta
    receipt?: Receipt
}

export type Meta = {
    request_id: string
    next_cursor?: string
    has_more?: boolean
}

export type Receipt = {
    operation_id: string
    resource_id: string
    resource_version: ResourceVersion
    event_id: string
    changed_fields: Array<string>
    policy_decision_id?: string
}

export type ResourceVersion = number

export type AdminOverview = {
    global_read_only: boolean
    global_read_only_version: ResourceVersion
    emergency_stop: boolean
    emergency_stop_version: ResourceVersion
    principals: Array<AdminServicePrincipalSummary>
    leases: Array<AdminTicketLeaseSummary>
    events: Array<AdminDomainEventSummary>
    outbox: Array<AdminOutboxDeliverySummary>
    attachments: Array<AdminAttachmentSummary>
    policy_decisions: Array<AdminPolicyDecisionSummary>
}

export type AdminServicePrincipalSummary = {
    id: string
    client_id: string
    name: string
    description: string
    status: "active" | "inactive" | "revoked"
    scopes: Array<AgentScope>
    rate_limit: number
    concurrency_limit: number
    last_used_at: string | null
    expires_at: string | null
    created_at: string
    read_only: boolean
    emergency_disabled: boolean
    resource_version: ResourceVersion
}

export type AgentScope = "tickets:read" | "tickets:create" | "tickets:update" | "tickets:assign" | "tickets:transition" | "comments:write" | "attachments:read" | "attachments:write" | "events:subscribe" | "tasks:manage"

export type AdminTicketLeaseSummary = {
    id: string
    ticket_id: number
    ticket_number: string
    principal_name: string
    acquired_at: string
    expires_at: string
    ticket_version: ResourceVersion
    resource_version: ResourceVersion
}

export type AdminDomainEventSummary = {
    id: string
    type: string
    subject: string
    actor_type: "human" | "service_principal" | "system"
    actor_id: string
    resource_version: ResourceVersion
    time: string
}

export type AdminOutboxDeliverySummary = {
    id: string
    event_id: string
    destination: string
    status: "pending" | "processing" | "succeeded" | "failed" | "dead"
    attempts: number
    next_attempt_at: string
    last_error: string
    updated_at: string
    resource_version: ResourceVersion
}

export type AdminAttachmentSummary = {
    id: number
    ticket_id: number
    original_name: string
    mime_type: string
    file_size: number
    virus_scan: "pending" | "clean" | "infected" | "error"
    scan_details: string
    scanned_at: string | null
    updated_at: string
    resource_version: ResourceVersion
}

export type AdminPolicyDecisionSummary = {
    id: string
    created_at: string
    actor_type: "human" | "service_principal" | "system"
    actor_id: string
    credential_id: string
    scope: string
    action: string
    resource_type: string
    resource_id: string
    allowed: boolean
    reason_code: string
    matched_policy_id: string
    source_protocol: string
    request_digest: string
}

export type Problem = {
    type: string
    title: string
    status: number
    detail?: string
    code: "invalid_request" | "precondition_required" | "unauthorized" | "invalid_actor" | "invalid_scope" | "principal_not_found" | "principal_disabled" | "principal_expired" | "invalid_credential" | "credential_expired" | "insufficient_scope" | "policy_denied" | "agent_emergency_stop" | "read_only" | "automation_loop" | "not_found" | "version_conflict" | "lease_conflict" | "lease_expired" | "lease_not_owned" | "idempotency_conflict" | "idempotency_in_progress" | "command_scope_mismatch" | "outbox_replay_conflict" | "rate_limited" | "concurrency_limit" | "attachment_rejected" | "attachment_too_large" | "attachment_not_clean" | "invalid_attachment_name" | "service_unavailable" | "internal_error"
    request_id: string
    retryable: boolean
}

export type ServicePrincipalCreate = {
    name: string
    description?: string
    scopes: Array<AgentScope>
    rate_limit?: number
    concurrency_limit?: number
    expires_at?: string | null
}

export type IssuedCredentialEnvelope = Envelope & {
    data?: IssuedCredential
    receipt: Receipt
}

export type IssuedCredential = {
    client_id: string
    client_secret: string
    expires_at: string
    project_key?: ProjectKey
}

export type ProjectKey = string

export type ServicePrincipalControl = {
    status?: "active" | "inactive" | "revoked"
    read_only?: boolean
    emergency_disabled?: boolean
}

export type ServicePrincipalEnvelope = Envelope & {
    data?: ServicePrincipal
    receipt: Receipt
}

export type ServicePrincipal = {
    id: string
    created_at: string
    updated_at: string
    deleted_at?: string | null
    name: string
    description: string
    status: "active" | "inactive" | "revoked"
    scopes: Array<AgentScope>
    rate_limit_per_minute: number
    concurrent_limit: number
    expires_at?: string | null
    read_only: boolean
    emergency_disabled: boolean
    last_used_at?: string | null
    created_by_id?: number | null
}

export type CredentialRevocationEnvelope = Envelope & {
    data?: CredentialRevocationResult
    receipt: Receipt
}

export type CredentialRevocationResult = {
    revoked: true
}

export type AgentPolicyListEnvelope = Envelope & {
    data?: Array<AdminAgentPolicy>
}

export type AdminAgentPolicy = {
    id: string
    created_at: string
    updated_at: string
    service_principal_id: string
    name: string
    effect: "allow" | "deny"
    scope: AgentPolicyScope
    action: string
    resource_type: string
    resource_id: string
    conditions: AgentPolicyConditions
    priority: number
    is_active: boolean
    expires_at: string | null
    resource_version: ResourceVersion
}

export type AgentPolicyScope = AgentScope | "*"

export type AgentPolicyConditions = { [key: string]: PolicyConditionValue } | null

export type PolicyConditionValue = string | number | boolean | null | Array<PolicyConditionValue> | { [key: string]: PolicyConditionValue }

export type AgentPolicyCreate = {
    name?: string
    effect: "allow" | "deny"
    scope: AgentPolicyScope
    action?: string
    resource_type?: string
    resource_id?: string
    conditions?: AgentPolicyConditions
    priority?: number
    expires_at?: string | null
}

export type AgentPolicyEnvelope = Envelope & {
    data?: AgentPolicy
    receipt: Receipt
}

export type AgentPolicy = {
    id: string
    created_at: string
    updated_at: string
    service_principal_id: string
    name: string
    effect: "allow" | "deny"
    scope: AgentPolicyScope
    action: string
    resource_type: string
    resource_id: string
    conditions: AgentPolicyConditions
    priority: number
    is_active: boolean
    expires_at?: string | null
}

export type PolicyDisableEnvelope = Envelope & {
    data?: PolicyDisableResult
    receipt: Receipt
}

export type PolicyDisableResult = {
    disabled: true
}

export type AdminTicketLeaseEnvelope = Envelope & {
    data?: AdminReleasedTicketLease
    receipt: Receipt
}

export type AdminReleasedTicketLease = {
    lease_id: string
    created_at: string
    updated_at: string
    ticket_id: number
    holder_actor_type: "human" | "service_principal" | "system"
    holder_actor_id: string
    ticket_version: ResourceVersion
    expires_at: string
    last_heartbeat_at: string
    released_at: string
    release_reason: string
}

export type AttachmentScanUpdate = {
    status: "clean" | "infected" | "error"
    details?: string
}

export type AttachmentScanEnvelope = Envelope & {
    data?: {
        attachment_id: number
        status: "clean" | "infected" | "error"
    }
    receipt: Receipt
}

export type ReplayEnvelope = Envelope & {
    data?: {
        replayed: true
    }
    receipt: Receipt
}

export type CreateHumanSessionOperationPathParameters = Record<string, never>
export type CreateHumanSessionOperationQuery = Record<string, never>
export type CreateHumanSessionOperationRequest = LoginRequest
export type CreateHumanSessionOperationResponse = AuthSessionEnvelope

export type RefreshHumanSessionOperationPathParameters = Record<string, never>
export type RefreshHumanSessionOperationQuery = Record<string, never>
export type RefreshHumanSessionOperationRequest = RefreshTokenRequest
export type RefreshHumanSessionOperationResponse = AuthSessionSuccessEnvelope

export type RequestHumanPasswordResetOperationPathParameters = Record<string, never>
export type RequestHumanPasswordResetOperationQuery = Record<string, never>
export type RequestHumanPasswordResetOperationRequest = ForgotPasswordRequest
export type RequestHumanPasswordResetOperationResponse = AuthMessageSuccessEnvelope

export type ResetHumanPasswordOperationPathParameters = Record<string, never>
export type ResetHumanPasswordOperationQuery = Record<string, never>
export type ResetHumanPasswordOperationRequest = ResetHumanPasswordRequest
export type ResetHumanPasswordOperationResponse = AuthMessageSuccessEnvelope

export type DeleteHumanSessionOperationPathParameters = Record<string, never>
export type DeleteHumanSessionOperationQuery = Record<string, never>
export type DeleteHumanSessionOperationRequest = LogoutRequest
export type DeleteHumanSessionOperationResponse = AuthMessageSuccessEnvelope

export type DeleteAllHumanSessionsOperationPathParameters = Record<string, never>
export type DeleteAllHumanSessionsOperationQuery = Record<string, never>
export type DeleteAllHumanSessionsOperationRequest = never
export type DeleteAllHumanSessionsOperationResponse = AuthMessageSuccessEnvelope

export type GetHumanSessionUserOperationPathParameters = Record<string, never>
export type GetHumanSessionUserOperationQuery = Record<string, never>
export type GetHumanSessionUserOperationRequest = never
export type GetHumanSessionUserOperationResponse = HumanSessionUserSuccessEnvelope

export type UpdateHumanProfileOperationPathParameters = Record<string, never>
export type UpdateHumanProfileOperationQuery = Record<string, never>
export type UpdateHumanProfileOperationRequest = UpdateHumanProfileRequest
export type UpdateHumanProfileOperationResponse = AuthMessageSuccessEnvelope

export type ListAuthorizedHumanProjectsOperationPathParameters = Record<string, never>
export type ListAuthorizedHumanProjectsOperationQuery = Record<string, never>
export type ListAuthorizedHumanProjectsOperationRequest = never
export type ListAuthorizedHumanProjectsOperationResponse = SuccessEnvelope & {
    data?: Array<AuthorizedProjectAccess>
}

export type GetAuthorizedProjectContextOperationPathParameters = {
    projectKey: string
}
export type GetAuthorizedProjectContextOperationQuery = Record<string, never>
export type GetAuthorizedProjectContextOperationRequest = never
export type GetAuthorizedProjectContextOperationResponse = SuccessEnvelope & {
    data: AuthorizedProjectAccess
}

export type ListProjectMembershipsOperationPathParameters = {
    projectKey: string
}
export type ListProjectMembershipsOperationQuery = Record<string, never>
export type ListProjectMembershipsOperationRequest = never
export type ListProjectMembershipsOperationResponse = SuccessEnvelope & {
    data?: Array<ProjectMembership>
}

export type UpsertProjectMembershipOperationPathParameters = {
    projectKey: string
}
export type UpsertProjectMembershipOperationQuery = Record<string, never>
export type UpsertProjectMembershipOperationRequest = UpsertProjectMembershipRequest
export type UpsertProjectMembershipOperationResponse = ProjectMembershipEnvelope

export type SearchProjectMembershipCandidatesOperationPathParameters = {
    projectKey: string
}
export type SearchProjectMembershipCandidatesOperationQuery = {
    page?: number
    page_size?: number
    search?: string
}
export type SearchProjectMembershipCandidatesOperationRequest = never
export type SearchProjectMembershipCandidatesOperationResponse = ProjectUserOptionPageEnvelope

export type DeactivateProjectMembershipOperationPathParameters = {
    projectKey: string
    userID: number
}
export type DeactivateProjectMembershipOperationQuery = Record<string, never>
export type DeactivateProjectMembershipOperationRequest = never
export type DeactivateProjectMembershipOperationResponse = ProjectMembershipEnvelope

export type ListPlatformProjectsOperationPathParameters = Record<string, never>
export type ListPlatformProjectsOperationQuery = {
    page?: number
    page_size?: number
    search?: string
    status?: ProjectStatus
    business_unit_public_id?: PublicUUIDv7
    order_by?: "name" | "key" | "status" | "business_unit" | "created_at" | "updated_at"
    order?: "asc" | "desc"
}
export type ListPlatformProjectsOperationRequest = never
export type ListPlatformProjectsOperationResponse = PlatformProjectPageEnvelope

export type CreatePlatformProjectOperationPathParameters = Record<string, never>
export type CreatePlatformProjectOperationQuery = Record<string, never>
export type CreatePlatformProjectOperationRequest = CreatePlatformProjectRequest
export type CreatePlatformProjectOperationResponse = PlatformProjectSummaryEnvelope

export type GetPlatformProjectCreationContextOperationPathParameters = Record<string, never>
export type GetPlatformProjectCreationContextOperationQuery = {
    page?: number
    page_size?: number
    search?: string
    business_unit_page?: number
    business_unit_page_size?: number
    business_unit_search?: string
}
export type GetPlatformProjectCreationContextOperationRequest = never
export type GetPlatformProjectCreationContextOperationResponse = ProjectCreationContextEnvelope

export type ListPlatformProjectBusinessUnitsOperationPathParameters = Record<string, never>
export type ListPlatformProjectBusinessUnitsOperationQuery = {
    page?: number
    page_size?: number
    search?: string
}
export type ListPlatformProjectBusinessUnitsOperationRequest = never
export type ListPlatformProjectBusinessUnitsOperationResponse = PlatformBusinessUnitPageEnvelope

export type ArchivePlatformProjectOperationPathParameters = {
    projectPublicID: string
}
export type ArchivePlatformProjectOperationQuery = Record<string, never>
export type ArchivePlatformProjectOperationRequest = never
export type ArchivePlatformProjectOperationResponse = PlatformProjectSummaryEnvelope

export type ListPlatformUsersOperationPathParameters = Record<string, never>
export type ListPlatformUsersOperationQuery = {
    page?: number
    page_size?: number
    platform_role?: PlatformRole
    status?: UserStatus
    search?: string
    order_by?: "id" | "username" | "email" | "created_at" | "updated_at" | "last_login_at"
    order?: "asc" | "desc"
}
export type ListPlatformUsersOperationRequest = never
export type ListPlatformUsersOperationResponse = SuccessEnvelope & {
    data?: AdminUserPage
}

export type CreatePlatformUserOperationPathParameters = Record<string, never>
export type CreatePlatformUserOperationQuery = Record<string, never>
export type CreatePlatformUserOperationRequest = CreateAdminUserRequest
export type CreatePlatformUserOperationResponse = AdminUserEnvelope

export type GetPlatformUserStatsOperationPathParameters = Record<string, never>
export type GetPlatformUserStatsOperationQuery = Record<string, never>
export type GetPlatformUserStatsOperationRequest = never
export type GetPlatformUserStatsOperationResponse = AdminUserStatsEnvelope

export type GetPlatformUserOperationPathParameters = {
    userID: number
}
export type GetPlatformUserOperationQuery = Record<string, never>
export type GetPlatformUserOperationRequest = never
export type GetPlatformUserOperationResponse = AdminUserEnvelope

export type UpdatePlatformUserOperationPathParameters = {
    userID: number
}
export type UpdatePlatformUserOperationQuery = Record<string, never>
export type UpdatePlatformUserOperationRequest = UpdateAdminUserRequest
export type UpdatePlatformUserOperationResponse = AdminUserEnvelope

export type DeletePlatformUserOperationPathParameters = {
    userID: number
}
export type DeletePlatformUserOperationQuery = Record<string, never>
export type DeletePlatformUserOperationRequest = never
export type DeletePlatformUserOperationResponse = EmptySuccessEnvelope

export type ResetPlatformUserPasswordOperationPathParameters = {
    userID: number
}
export type ResetPlatformUserPasswordOperationQuery = Record<string, never>
export type ResetPlatformUserPasswordOperationRequest = ResetAdminUserPasswordRequest
export type ResetPlatformUserPasswordOperationResponse = EmptySuccessEnvelope

export type ListPlatformAuditLogsOperationPathParameters = Record<string, never>
export type ListPlatformAuditLogsOperationQuery = {
    user_id?: number
    actor?: string
    platform_role?: PlatformRole
    action?: string
    method?: "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS"
    path?: string
    path_prefix?: string
    status?: number
    keyword?: string
    result?: "pending" | "success" | "error"
    time_preset?: "1h" | "24h" | "7d" | "30d"
    start_time?: string
    end_time?: string
    page?: number
    limit?: number
    cursor?: string
}
export type ListPlatformAuditLogsOperationRequest = never
export type ListPlatformAuditLogsOperationResponse = AdminAuditLogPageEnvelope

export type GetPlatformAuditLogDetailOperationPathParameters = {
    auditLogID: number
}
export type GetPlatformAuditLogDetailOperationQuery = Record<string, never>
export type GetPlatformAuditLogDetailOperationRequest = never
export type GetPlatformAuditLogDetailOperationResponse = AdminAuditLogDetailEnvelope

export type GetWorkbenchDashboardOperationPathParameters = Record<string, never>
export type GetWorkbenchDashboardOperationQuery = {
    project_keys?: Array<string>
    days?: 7 | 30 | 90
}
export type GetWorkbenchDashboardOperationRequest = never
export type GetWorkbenchDashboardOperationResponse = WorkbenchDashboardEnvelope

export type ListCrossProjectWorkbenchTicketsOperationPathParameters = Record<string, never>
export type ListCrossProjectWorkbenchTicketsOperationQuery = {
    view?: CrossProjectWorkbenchView
    page?: number
    page_size?: number
}
export type ListCrossProjectWorkbenchTicketsOperationRequest = never
export type ListCrossProjectWorkbenchTicketsOperationResponse = CrossProjectWorkbenchPageEnvelope

export type ListProjectTicketsOperationPathParameters = {
    projectKey: string
}
export type ListProjectTicketsOperationQuery = {
    page?: number
    page_size?: number
    status?: string
    priority?: string
    type?: TicketType
    source?: TicketSource
    assigned_to?: number
    created_by?: number
    search?: string
    sort_by?: string
    sort_order?: "asc" | "desc"
    sla_breached?: boolean
    is_overdue?: boolean
    unassigned?: boolean
    assigned_to_me?: boolean
    filter?: string
}
export type ListProjectTicketsOperationRequest = never
export type ListProjectTicketsOperationResponse = TicketPageEnvelope

export type CreateProjectTicketOperationPathParameters = {
    projectKey: string
}
export type CreateProjectTicketOperationQuery = Record<string, never>
export type CreateProjectTicketOperationRequest = CreateTicketRequest
export type CreateProjectTicketOperationResponse = TicketEnvelope

export type ListProjectOverdueTicketsOperationPathParameters = {
    projectKey: string
}
export type ListProjectOverdueTicketsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectOverdueTicketsOperationRequest = never
export type ListProjectOverdueTicketsOperationResponse = TicketListPageEnvelope

export type ListProjectSLABreachedTicketsOperationPathParameters = {
    projectKey: string
}
export type ListProjectSLABreachedTicketsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectSLABreachedTicketsOperationRequest = never
export type ListProjectSLABreachedTicketsOperationResponse = TicketListPageEnvelope

export type GetProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type GetProjectTicketOperationQuery = Record<string, never>
export type GetProjectTicketOperationRequest = never
export type GetProjectTicketOperationResponse = TicketEnvelope

export type UpdateProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type UpdateProjectTicketOperationQuery = Record<string, never>
export type UpdateProjectTicketOperationRequest = UpdateTicketRequest
export type UpdateProjectTicketOperationResponse = TicketEnvelope

export type DeleteProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type DeleteProjectTicketOperationQuery = Record<string, never>
export type DeleteProjectTicketOperationRequest = never
export type DeleteProjectTicketOperationResponse = EmptySuccessEnvelope

export type AssignProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type AssignProjectTicketOperationQuery = Record<string, never>
export type AssignProjectTicketOperationRequest = AssignTicketRequest
export type AssignProjectTicketOperationResponse = TicketWorkflowEnvelope

export type TransferProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type TransferProjectTicketOperationQuery = Record<string, never>
export type TransferProjectTicketOperationRequest = TransferTicketRequest
export type TransferProjectTicketOperationResponse = TicketWorkflowEnvelope

export type EscalateProjectTicketOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type EscalateProjectTicketOperationQuery = Record<string, never>
export type EscalateProjectTicketOperationRequest = EscalateTicketRequest
export type EscalateProjectTicketOperationResponse = TicketWorkflowEnvelope

export type UpdateProjectTicketStatusOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type UpdateProjectTicketStatusOperationQuery = Record<string, never>
export type UpdateProjectTicketStatusOperationRequest = UpdateTicketStatusRequest
export type UpdateProjectTicketStatusOperationResponse = TicketWorkflowEnvelope

export type ListProjectTicketHistoryOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type ListProjectTicketHistoryOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectTicketHistoryOperationRequest = never
export type ListProjectTicketHistoryOperationResponse = TicketHistoryListEnvelope

export type ListProjectTicketCommentsOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type ListProjectTicketCommentsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectTicketCommentsOperationRequest = never
export type ListProjectTicketCommentsOperationResponse = TicketCommentListEnvelope

export type CreateProjectTicketCommentOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type CreateProjectTicketCommentOperationQuery = Record<string, never>
export type CreateProjectTicketCommentOperationRequest = CreateTicketCommentRequest
export type CreateProjectTicketCommentOperationResponse = TicketCommentEnvelope

export type ListProjectTicketCommentRepliesOperationPathParameters = {
    projectKey: string
    ticketID: number
    commentID: number
}
export type ListProjectTicketCommentRepliesOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectTicketCommentRepliesOperationRequest = never
export type ListProjectTicketCommentRepliesOperationResponse = TicketCommentListEnvelope

export type ListProjectTicketAttachmentsOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type ListProjectTicketAttachmentsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectTicketAttachmentsOperationRequest = never
export type ListProjectTicketAttachmentsOperationResponse = TicketAttachmentListEnvelope

export type UploadProjectTicketAttachmentOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type UploadProjectTicketAttachmentOperationQuery = Record<string, never>
export type UploadProjectTicketAttachmentOperationRequest = UploadTicketAttachmentRequest
export type UploadProjectTicketAttachmentOperationResponse = TicketAttachmentEnvelope

export type DownloadProjectTicketAttachmentOperationPathParameters = {
    projectKey: string
    ticketID: number
    attachmentID: number
}
export type DownloadProjectTicketAttachmentOperationQuery = Record<string, never>
export type DownloadProjectTicketAttachmentOperationRequest = never
export type DownloadProjectTicketAttachmentOperationResponse = string

export type ListProjectNotificationsOperationPathParameters = {
    projectKey: string
}
export type ListProjectNotificationsOperationQuery = {
    page?: number
    page_size?: number
    sort?: string
    filter?: string
}
export type ListProjectNotificationsOperationRequest = never
export type ListProjectNotificationsOperationResponse = NotificationPageEnvelope

export type CreateProjectNotificationOperationPathParameters = {
    projectKey: string
}
export type CreateProjectNotificationOperationQuery = Record<string, never>
export type CreateProjectNotificationOperationRequest = CreateNotificationRequest
export type CreateProjectNotificationOperationResponse = NotificationEnvelope

export type DeleteProjectNotificationOperationPathParameters = {
    projectKey: string
    notificationID: number
}
export type DeleteProjectNotificationOperationQuery = Record<string, never>
export type DeleteProjectNotificationOperationRequest = never
export type DeleteProjectNotificationOperationResponse = MessageEnvelope

export type MarkProjectNotificationReadOperationPathParameters = {
    projectKey: string
    notificationID: number
}
export type MarkProjectNotificationReadOperationQuery = Record<string, never>
export type MarkProjectNotificationReadOperationRequest = never
export type MarkProjectNotificationReadOperationResponse = MessageEnvelope

export type MarkAllProjectNotificationsReadOperationPathParameters = {
    projectKey: string
}
export type MarkAllProjectNotificationsReadOperationQuery = Record<string, never>
export type MarkAllProjectNotificationsReadOperationRequest = never
export type MarkAllProjectNotificationsReadOperationResponse = MessageEnvelope

export type GetProjectUnreadNotificationCountOperationPathParameters = {
    projectKey: string
}
export type GetProjectUnreadNotificationCountOperationQuery = Record<string, never>
export type GetProjectUnreadNotificationCountOperationRequest = never
export type GetProjectUnreadNotificationCountOperationResponse = UnreadNotificationCount

export type GetHumanNotificationPreferencesOperationPathParameters = Record<string, never>
export type GetHumanNotificationPreferencesOperationQuery = Record<string, never>
export type GetHumanNotificationPreferencesOperationRequest = never
export type GetHumanNotificationPreferencesOperationResponse = NotificationPreferencesEnvelope

export type UpdateHumanNotificationPreferencesOperationPathParameters = Record<string, never>
export type UpdateHumanNotificationPreferencesOperationQuery = Record<string, never>
export type UpdateHumanNotificationPreferencesOperationRequest = UpdateNotificationPreferencesRequest
export type UpdateHumanNotificationPreferencesOperationResponse = MessageEnvelope

export type ListProjectAutomationRulesOperationPathParameters = {
    projectKey: string
}
export type ListProjectAutomationRulesOperationQuery = {
    rule_type?: string
    trigger_event?: string
    is_active?: boolean
    search?: string
    page?: number
    page_size?: number
}
export type ListProjectAutomationRulesOperationRequest = never
export type ListProjectAutomationRulesOperationResponse = AutomationRulePageEnvelope

export type CreateProjectAutomationRuleOperationPathParameters = {
    projectKey: string
}
export type CreateProjectAutomationRuleOperationQuery = Record<string, never>
export type CreateProjectAutomationRuleOperationRequest = AutomationRuleRequest
export type CreateProjectAutomationRuleOperationResponse = AutomationRuleEnvelope

export type GetProjectAutomationRuleOperationPathParameters = {
    projectKey: string
    ruleID: number
}
export type GetProjectAutomationRuleOperationQuery = Record<string, never>
export type GetProjectAutomationRuleOperationRequest = never
export type GetProjectAutomationRuleOperationResponse = AutomationRuleEnvelope

export type UpdateProjectAutomationRuleOperationPathParameters = {
    projectKey: string
    ruleID: number
}
export type UpdateProjectAutomationRuleOperationQuery = Record<string, never>
export type UpdateProjectAutomationRuleOperationRequest = AutomationRuleRequest
export type UpdateProjectAutomationRuleOperationResponse = LegacyMessageSuccessEnvelope

export type DeleteProjectAutomationRuleOperationPathParameters = {
    projectKey: string
    ruleID: number
}
export type DeleteProjectAutomationRuleOperationQuery = Record<string, never>
export type DeleteProjectAutomationRuleOperationRequest = never
export type DeleteProjectAutomationRuleOperationResponse = LegacyMessageSuccessEnvelope

export type ListProjectAutomationLogsOperationPathParameters = {
    projectKey: string
}
export type ListProjectAutomationLogsOperationQuery = {
    rule_id?: number
    ticket_id?: number
    success?: boolean
    page?: number
    page_size?: number
}
export type ListProjectAutomationLogsOperationRequest = never
export type ListProjectAutomationLogsOperationResponse = AutomationLogPageEnvelope

export type GetPlatformEmailConfigOperationPathParameters = Record<string, never>
export type GetPlatformEmailConfigOperationQuery = Record<string, never>
export type GetPlatformEmailConfigOperationRequest = never
export type GetPlatformEmailConfigOperationResponse = EmailConfigEnvelope

export type UpdatePlatformEmailConfigOperationPathParameters = Record<string, never>
export type UpdatePlatformEmailConfigOperationQuery = Record<string, never>
export type UpdatePlatformEmailConfigOperationRequest = UpdateEmailConfigRequest
export type UpdatePlatformEmailConfigOperationResponse = EmailConfigEnvelope

export type TestPlatformEmailConfigOperationPathParameters = Record<string, never>
export type TestPlatformEmailConfigOperationQuery = Record<string, never>
export type TestPlatformEmailConfigOperationRequest = TestEmailRequest
export type TestPlatformEmailConfigOperationResponse = EmptySuccessEnvelope

export type ListPlatformConfigsOperationPathParameters = Record<string, never>
export type ListPlatformConfigsOperationQuery = {
    category?: string
}
export type ListPlatformConfigsOperationRequest = never
export type ListPlatformConfigsOperationResponse = SystemConfigListEnvelope

export type UpdatePlatformConfigOperationPathParameters = {
    configKey: string
}
export type UpdatePlatformConfigOperationQuery = Record<string, never>
export type UpdatePlatformConfigOperationRequest = UpdateSystemConfigRequest
export type UpdatePlatformConfigOperationResponse = SystemConfigUpdateEnvelope

export type ListProjectWebhooksOperationPathParameters = {
    projectKey: string
}
export type ListProjectWebhooksOperationQuery = {
    page?: number
    page_size?: number
    provider?: WebhookProvider
    status?: WebhookStatus
}
export type ListProjectWebhooksOperationRequest = never
export type ListProjectWebhooksOperationResponse = WebhookPageEnvelope

export type CreateProjectWebhookOperationPathParameters = {
    projectKey: string
}
export type CreateProjectWebhookOperationQuery = Record<string, never>
export type CreateProjectWebhookOperationRequest = CreateWebhookRequest
export type CreateProjectWebhookOperationResponse = WebhookEnvelope

export type GetProjectWebhookOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type GetProjectWebhookOperationQuery = Record<string, never>
export type GetProjectWebhookOperationRequest = never
export type GetProjectWebhookOperationResponse = WebhookEnvelope

export type UpdateProjectWebhookOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type UpdateProjectWebhookOperationQuery = Record<string, never>
export type UpdateProjectWebhookOperationRequest = UpdateWebhookRequest
export type UpdateProjectWebhookOperationResponse = WebhookEnvelope

export type DeleteProjectWebhookOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type DeleteProjectWebhookOperationQuery = Record<string, never>
export type DeleteProjectWebhookOperationRequest = never
export type DeleteProjectWebhookOperationResponse = EmptySuccessEnvelope

export type QueueProjectWebhookTestOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type QueueProjectWebhookTestOperationQuery = Record<string, never>
export type QueueProjectWebhookTestOperationRequest = never
export type QueueProjectWebhookTestOperationResponse = WebhookTestReceiptEnvelope

export type ListProjectWebhookLogsOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type ListProjectWebhookLogsOperationQuery = {
    page?: number
    page_size?: number
    status?: string
}
export type ListProjectWebhookLogsOperationRequest = never
export type ListProjectWebhookLogsOperationResponse = {
    code: 0
    msg: string
    data: {
        items: Array<{
            id: number
            created_at: string
            config_id: number
            event_type: string
            status: string
            response_status?: number
            response_time?: number
            error_message?: string
        }>
        total: number
        page: number
        size: number
    }
}

export type GetProjectWebhookStatsOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type GetProjectWebhookStatsOperationQuery = {
    days?: number
}
export type GetProjectWebhookStatsOperationRequest = never
export type GetProjectWebhookStatsOperationResponse = {
    code: 0
    msg: string
    data: {
        summary: {
            total_sent: number
            total_success: number
            total_failed: number
        }
        daily_stats: Array<{
            date: string
            sent: number
            success: number
            failed: number
        }>
        period: string
    }
}

export type GetAgentControlOverviewV2OperationPathParameters = {
    projectKey: string
}
export type GetAgentControlOverviewV2OperationQuery = Record<string, never>
export type GetAgentControlOverviewV2OperationRequest = never
export type GetAgentControlOverviewV2OperationResponse = AdminOverviewEnvelope

export type CreateServicePrincipalV2OperationPathParameters = {
    projectKey: string
}
export type CreateServicePrincipalV2OperationQuery = Record<string, never>
export type CreateServicePrincipalV2OperationRequest = ServicePrincipalCreate
export type CreateServicePrincipalV2OperationResponse = IssuedCredentialEnvelope

export type SetServicePrincipalStatusV2OperationPathParameters = {
    projectKey: string
    principalId: string
}
export type SetServicePrincipalStatusV2OperationQuery = Record<string, never>
export type SetServicePrincipalStatusV2OperationRequest = ServicePrincipalControl
export type SetServicePrincipalStatusV2OperationResponse = ServicePrincipalEnvelope

export type RotateServicePrincipalCredentialV2OperationPathParameters = {
    projectKey: string
    principalId: string
}
export type RotateServicePrincipalCredentialV2OperationQuery = Record<string, never>
export type RotateServicePrincipalCredentialV2OperationRequest = never
export type RotateServicePrincipalCredentialV2OperationResponse = IssuedCredentialEnvelope

export type RevokeServicePrincipalCredentialV2OperationPathParameters = {
    projectKey: string
    principalId: string
    credentialId: string
}
export type RevokeServicePrincipalCredentialV2OperationQuery = Record<string, never>
export type RevokeServicePrincipalCredentialV2OperationRequest = never
export type RevokeServicePrincipalCredentialV2OperationResponse = CredentialRevocationEnvelope

export type ListServicePrincipalPoliciesV2OperationPathParameters = {
    projectKey: string
    principalId: string
}
export type ListServicePrincipalPoliciesV2OperationQuery = Record<string, never>
export type ListServicePrincipalPoliciesV2OperationRequest = never
export type ListServicePrincipalPoliciesV2OperationResponse = AgentPolicyListEnvelope

export type CreateServicePrincipalPolicyV2OperationPathParameters = {
    projectKey: string
    principalId: string
}
export type CreateServicePrincipalPolicyV2OperationQuery = Record<string, never>
export type CreateServicePrincipalPolicyV2OperationRequest = AgentPolicyCreate
export type CreateServicePrincipalPolicyV2OperationResponse = AgentPolicyEnvelope

export type DisableServicePrincipalPolicyV2OperationPathParameters = {
    projectKey: string
    principalId: string
    policyId: string
}
export type DisableServicePrincipalPolicyV2OperationQuery = Record<string, never>
export type DisableServicePrincipalPolicyV2OperationRequest = never
export type DisableServicePrincipalPolicyV2OperationResponse = PolicyDisableEnvelope

export type ForceReleaseTicketLeaseV2OperationPathParameters = {
    projectKey: string
    leaseId: string
}
export type ForceReleaseTicketLeaseV2OperationQuery = Record<string, never>
export type ForceReleaseTicketLeaseV2OperationRequest = never
export type ForceReleaseTicketLeaseV2OperationResponse = AdminTicketLeaseEnvelope

export type RecordAttachmentVirusScanV2OperationPathParameters = {
    projectKey: string
    attachmentId: number
}
export type RecordAttachmentVirusScanV2OperationQuery = Record<string, never>
export type RecordAttachmentVirusScanV2OperationRequest = AttachmentScanUpdate
export type RecordAttachmentVirusScanV2OperationResponse = AttachmentScanEnvelope

export type ReplayOutboxDeliveryV2OperationPathParameters = {
    projectKey: string
    deliveryId: string
}
export type ReplayOutboxDeliveryV2OperationQuery = Record<string, never>
export type ReplayOutboxDeliveryV2OperationRequest = never
export type ReplayOutboxDeliveryV2OperationResponse = ReplayEnvelope

export const humanApiOperations = {
    createHumanSession: {
        method: "POST",
        path: "/auth/login",
        successStatus: 200,
        requestBody: "required",
    },
    refreshHumanSession: {
        method: "POST",
        path: "/auth/refresh",
        successStatus: 200,
        requestBody: "required",
    },
    requestHumanPasswordReset: {
        method: "POST",
        path: "/auth/forgot-password",
        successStatus: 200,
        requestBody: "required",
    },
    resetHumanPassword: {
        method: "POST",
        path: "/auth/reset-password",
        successStatus: 200,
        requestBody: "required",
    },
    deleteHumanSession: {
        method: "POST",
        path: "/auth/logout",
        successStatus: 200,
        requestBody: "optional",
    },
    deleteAllHumanSessions: {
        method: "POST",
        path: "/auth/logout-all",
        successStatus: 200,
        requestBody: "none",
    },
    getHumanSessionUser: {
        method: "GET",
        path: "/auth/me",
        successStatus: 200,
        requestBody: "none",
    },
    updateHumanProfile: {
        method: "PUT",
        path: "/auth/profile",
        successStatus: 200,
        requestBody: "required",
    },
    listAuthorizedHumanProjects: {
        method: "GET",
        path: "/projects",
        successStatus: 200,
        requestBody: "none",
    },
    getAuthorizedProjectContext: {
        method: "GET",
        path: "/projects/{projectKey}/context",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectMemberships: {
        method: "GET",
        path: "/projects/{projectKey}/memberships",
        successStatus: 200,
        requestBody: "none",
    },
    upsertProjectMembership: {
        method: "POST",
        path: "/projects/{projectKey}/memberships",
        successStatus: 200,
        requestBody: "required",
    },
    searchProjectMembershipCandidates: {
        method: "GET",
        path: "/projects/{projectKey}/membership-candidates",
        successStatus: 200,
        requestBody: "none",
    },
    deactivateProjectMembership: {
        method: "DELETE",
        path: "/projects/{projectKey}/memberships/{userID}",
        successStatus: 200,
        requestBody: "none",
    },
    listPlatformProjects: {
        method: "GET",
        path: "/platform/projects",
        successStatus: 200,
        requestBody: "none",
    },
    createPlatformProject: {
        method: "POST",
        path: "/platform/projects",
        successStatus: 201,
        requestBody: "required",
    },
    getPlatformProjectCreationContext: {
        method: "GET",
        path: "/platform/project-creation-context",
        successStatus: 200,
        requestBody: "none",
    },
    listPlatformProjectBusinessUnits: {
        method: "GET",
        path: "/platform/project-business-units",
        successStatus: 200,
        requestBody: "none",
    },
    archivePlatformProject: {
        method: "POST",
        path: "/platform/projects/{projectPublicID}/archive",
        successStatus: 200,
        requestBody: "none",
    },
    listPlatformUsers: {
        method: "GET",
        path: "/platform/users",
        successStatus: 200,
        requestBody: "none",
    },
    createPlatformUser: {
        method: "POST",
        path: "/platform/users",
        successStatus: 201,
        requestBody: "required",
    },
    getPlatformUserStats: {
        method: "GET",
        path: "/platform/users/stats",
        successStatus: 200,
        requestBody: "none",
    },
    getPlatformUser: {
        method: "GET",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "none",
    },
    updatePlatformUser: {
        method: "PUT",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "required",
    },
    deletePlatformUser: {
        method: "DELETE",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "none",
    },
    resetPlatformUserPassword: {
        method: "POST",
        path: "/platform/users/{userID}/reset-password",
        successStatus: 200,
        requestBody: "required",
    },
    listPlatformAuditLogs: {
        method: "GET",
        path: "/platform/audit-logs",
        successStatus: 200,
        requestBody: "none",
    },
    getPlatformAuditLogDetail: {
        method: "GET",
        path: "/platform/audit-logs/{auditLogID}",
        successStatus: 200,
        requestBody: "none",
    },
    getWorkbenchDashboard: {
        method: "GET",
        path: "/workbench/dashboard",
        successStatus: 200,
        requestBody: "none",
    },
    listCrossProjectWorkbenchTickets: {
        method: "GET",
        path: "/workbench/tickets",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets",
        successStatus: 200,
        requestBody: "none",
    },
    createProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets",
        successStatus: 201,
        requestBody: "required",
    },
    listProjectOverdueTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/overdue",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectSLABreachedTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/sla-breach",
        successStatus: 200,
        requestBody: "none",
    },
    getProjectTicket: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "none",
    },
    updateProjectTicket: {
        method: "PUT",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "required",
    },
    deleteProjectTicket: {
        method: "DELETE",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "none",
    },
    assignProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/assign",
        successStatus: 200,
        requestBody: "required",
    },
    transferProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/transfer",
        successStatus: 200,
        requestBody: "required",
    },
    escalateProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/escalate",
        successStatus: 200,
        requestBody: "required",
    },
    updateProjectTicketStatus: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/status",
        successStatus: 200,
        requestBody: "required",
    },
    listProjectTicketHistory: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/history",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectTicketComments: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments",
        successStatus: 200,
        requestBody: "none",
    },
    createProjectTicketComment: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments",
        successStatus: 201,
        requestBody: "required",
    },
    listProjectTicketCommentReplies: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectTicketAttachments: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments",
        successStatus: 200,
        requestBody: "none",
    },
    uploadProjectTicketAttachment: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments",
        successStatus: 202,
        requestBody: "required",
    },
    downloadProjectTicketAttachment: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments/{attachmentID}/content",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectNotifications: {
        method: "GET",
        path: "/projects/{projectKey}/notifications",
        successStatus: 200,
        requestBody: "none",
    },
    createProjectNotification: {
        method: "POST",
        path: "/projects/{projectKey}/notifications",
        successStatus: 201,
        requestBody: "required",
    },
    deleteProjectNotification: {
        method: "DELETE",
        path: "/projects/{projectKey}/notifications/{notificationID}",
        successStatus: 200,
        requestBody: "none",
    },
    markProjectNotificationRead: {
        method: "PUT",
        path: "/projects/{projectKey}/notifications/{notificationID}/read",
        successStatus: 200,
        requestBody: "none",
    },
    markAllProjectNotificationsRead: {
        method: "PUT",
        path: "/projects/{projectKey}/notifications/read-all",
        successStatus: 200,
        requestBody: "none",
    },
    getProjectUnreadNotificationCount: {
        method: "GET",
        path: "/projects/{projectKey}/notifications/unread-count",
        successStatus: 200,
        requestBody: "none",
    },
    getHumanNotificationPreferences: {
        method: "GET",
        path: "/notification-preferences",
        successStatus: 200,
        requestBody: "none",
    },
    updateHumanNotificationPreferences: {
        method: "PUT",
        path: "/notification-preferences",
        successStatus: 200,
        requestBody: "required",
    },
    listProjectAutomationRules: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/rules",
        successStatus: 200,
        requestBody: "none",
    },
    createProjectAutomationRule: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/rules",
        successStatus: 201,
        requestBody: "required",
    },
    getProjectAutomationRule: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "none",
    },
    updateProjectAutomationRule: {
        method: "PUT",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "required",
    },
    deleteProjectAutomationRule: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "none",
    },
    listProjectAutomationLogs: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/logs",
        successStatus: 200,
        requestBody: "none",
    },
    getPlatformEmailConfig: {
        method: "GET",
        path: "/platform/email-config",
        successStatus: 200,
        requestBody: "none",
    },
    updatePlatformEmailConfig: {
        method: "PUT",
        path: "/platform/email-config",
        successStatus: 200,
        requestBody: "required",
    },
    testPlatformEmailConfig: {
        method: "POST",
        path: "/platform/email-config/test",
        successStatus: 200,
        requestBody: "required",
    },
    listPlatformConfigs: {
        method: "GET",
        path: "/platform/configs",
        successStatus: 200,
        requestBody: "none",
    },
    updatePlatformConfig: {
        method: "PUT",
        path: "/platform/configs/{configKey}",
        successStatus: 200,
        requestBody: "required",
    },
    listProjectWebhooks: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks",
        successStatus: 200,
        requestBody: "none",
    },
    createProjectWebhook: {
        method: "POST",
        path: "/projects/{projectKey}/webhooks",
        successStatus: 200,
        requestBody: "required",
    },
    getProjectWebhook: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "none",
    },
    updateProjectWebhook: {
        method: "PUT",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "required",
    },
    deleteProjectWebhook: {
        method: "DELETE",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "none",
    },
    queueProjectWebhookTest: {
        method: "POST",
        path: "/projects/{projectKey}/webhooks/{webhookID}/test",
        successStatus: 202,
        requestBody: "none",
    },
    listProjectWebhookLogs: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}/logs",
        successStatus: 200,
        requestBody: "none",
    },
    getProjectWebhookStats: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}/stats",
        successStatus: 200,
        requestBody: "none",
    },
    getAgentControlOverviewV2: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/agent-control/overview",
        successStatus: 200,
        requestBody: "none",
    },
    createServicePrincipalV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals",
        successStatus: 201,
        requestBody: "required",
    },
    setServicePrincipalStatusV2: {
        method: "PUT",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/status",
        successStatus: 200,
        requestBody: "required",
    },
    rotateServicePrincipalCredentialV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/rotate",
        successStatus: 200,
        requestBody: "none",
    },
    revokeServicePrincipalCredentialV2: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/{credentialId}",
        successStatus: 200,
        requestBody: "none",
    },
    listServicePrincipalPoliciesV2: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
        successStatus: 200,
        requestBody: "none",
    },
    createServicePrincipalPolicyV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
        successStatus: 201,
        requestBody: "required",
    },
    disableServicePrincipalPolicyV2: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies/{policyId}",
        successStatus: 200,
        requestBody: "none",
    },
    forceReleaseTicketLeaseV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/leases/{leaseId}/force-release",
        successStatus: 200,
        requestBody: "none",
    },
    recordAttachmentVirusScanV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/attachments/{attachmentId}/scan",
        successStatus: 200,
        requestBody: "required",
    },
    replayOutboxDeliveryV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/outbox/{deliveryId}/replay",
        successStatus: 202,
        requestBody: "none",
    },
} as const

export interface HumanApiOperationTypes {
    createHumanSession: {
        pathParameters: CreateHumanSessionOperationPathParameters
        query: CreateHumanSessionOperationQuery
        request: CreateHumanSessionOperationRequest
        response: CreateHumanSessionOperationResponse
    }
    refreshHumanSession: {
        pathParameters: RefreshHumanSessionOperationPathParameters
        query: RefreshHumanSessionOperationQuery
        request: RefreshHumanSessionOperationRequest
        response: RefreshHumanSessionOperationResponse
    }
    requestHumanPasswordReset: {
        pathParameters: RequestHumanPasswordResetOperationPathParameters
        query: RequestHumanPasswordResetOperationQuery
        request: RequestHumanPasswordResetOperationRequest
        response: RequestHumanPasswordResetOperationResponse
    }
    resetHumanPassword: {
        pathParameters: ResetHumanPasswordOperationPathParameters
        query: ResetHumanPasswordOperationQuery
        request: ResetHumanPasswordOperationRequest
        response: ResetHumanPasswordOperationResponse
    }
    deleteHumanSession: {
        pathParameters: DeleteHumanSessionOperationPathParameters
        query: DeleteHumanSessionOperationQuery
        request: DeleteHumanSessionOperationRequest
        response: DeleteHumanSessionOperationResponse
    }
    deleteAllHumanSessions: {
        pathParameters: DeleteAllHumanSessionsOperationPathParameters
        query: DeleteAllHumanSessionsOperationQuery
        request: DeleteAllHumanSessionsOperationRequest
        response: DeleteAllHumanSessionsOperationResponse
    }
    getHumanSessionUser: {
        pathParameters: GetHumanSessionUserOperationPathParameters
        query: GetHumanSessionUserOperationQuery
        request: GetHumanSessionUserOperationRequest
        response: GetHumanSessionUserOperationResponse
    }
    updateHumanProfile: {
        pathParameters: UpdateHumanProfileOperationPathParameters
        query: UpdateHumanProfileOperationQuery
        request: UpdateHumanProfileOperationRequest
        response: UpdateHumanProfileOperationResponse
    }
    listAuthorizedHumanProjects: {
        pathParameters: ListAuthorizedHumanProjectsOperationPathParameters
        query: ListAuthorizedHumanProjectsOperationQuery
        request: ListAuthorizedHumanProjectsOperationRequest
        response: ListAuthorizedHumanProjectsOperationResponse
    }
    getAuthorizedProjectContext: {
        pathParameters: GetAuthorizedProjectContextOperationPathParameters
        query: GetAuthorizedProjectContextOperationQuery
        request: GetAuthorizedProjectContextOperationRequest
        response: GetAuthorizedProjectContextOperationResponse
    }
    listProjectMemberships: {
        pathParameters: ListProjectMembershipsOperationPathParameters
        query: ListProjectMembershipsOperationQuery
        request: ListProjectMembershipsOperationRequest
        response: ListProjectMembershipsOperationResponse
    }
    upsertProjectMembership: {
        pathParameters: UpsertProjectMembershipOperationPathParameters
        query: UpsertProjectMembershipOperationQuery
        request: UpsertProjectMembershipOperationRequest
        response: UpsertProjectMembershipOperationResponse
    }
    searchProjectMembershipCandidates: {
        pathParameters: SearchProjectMembershipCandidatesOperationPathParameters
        query: SearchProjectMembershipCandidatesOperationQuery
        request: SearchProjectMembershipCandidatesOperationRequest
        response: SearchProjectMembershipCandidatesOperationResponse
    }
    deactivateProjectMembership: {
        pathParameters: DeactivateProjectMembershipOperationPathParameters
        query: DeactivateProjectMembershipOperationQuery
        request: DeactivateProjectMembershipOperationRequest
        response: DeactivateProjectMembershipOperationResponse
    }
    listPlatformProjects: {
        pathParameters: ListPlatformProjectsOperationPathParameters
        query: ListPlatformProjectsOperationQuery
        request: ListPlatformProjectsOperationRequest
        response: ListPlatformProjectsOperationResponse
    }
    createPlatformProject: {
        pathParameters: CreatePlatformProjectOperationPathParameters
        query: CreatePlatformProjectOperationQuery
        request: CreatePlatformProjectOperationRequest
        response: CreatePlatformProjectOperationResponse
    }
    getPlatformProjectCreationContext: {
        pathParameters: GetPlatformProjectCreationContextOperationPathParameters
        query: GetPlatformProjectCreationContextOperationQuery
        request: GetPlatformProjectCreationContextOperationRequest
        response: GetPlatformProjectCreationContextOperationResponse
    }
    listPlatformProjectBusinessUnits: {
        pathParameters: ListPlatformProjectBusinessUnitsOperationPathParameters
        query: ListPlatformProjectBusinessUnitsOperationQuery
        request: ListPlatformProjectBusinessUnitsOperationRequest
        response: ListPlatformProjectBusinessUnitsOperationResponse
    }
    archivePlatformProject: {
        pathParameters: ArchivePlatformProjectOperationPathParameters
        query: ArchivePlatformProjectOperationQuery
        request: ArchivePlatformProjectOperationRequest
        response: ArchivePlatformProjectOperationResponse
    }
    listPlatformUsers: {
        pathParameters: ListPlatformUsersOperationPathParameters
        query: ListPlatformUsersOperationQuery
        request: ListPlatformUsersOperationRequest
        response: ListPlatformUsersOperationResponse
    }
    createPlatformUser: {
        pathParameters: CreatePlatformUserOperationPathParameters
        query: CreatePlatformUserOperationQuery
        request: CreatePlatformUserOperationRequest
        response: CreatePlatformUserOperationResponse
    }
    getPlatformUserStats: {
        pathParameters: GetPlatformUserStatsOperationPathParameters
        query: GetPlatformUserStatsOperationQuery
        request: GetPlatformUserStatsOperationRequest
        response: GetPlatformUserStatsOperationResponse
    }
    getPlatformUser: {
        pathParameters: GetPlatformUserOperationPathParameters
        query: GetPlatformUserOperationQuery
        request: GetPlatformUserOperationRequest
        response: GetPlatformUserOperationResponse
    }
    updatePlatformUser: {
        pathParameters: UpdatePlatformUserOperationPathParameters
        query: UpdatePlatformUserOperationQuery
        request: UpdatePlatformUserOperationRequest
        response: UpdatePlatformUserOperationResponse
    }
    deletePlatformUser: {
        pathParameters: DeletePlatformUserOperationPathParameters
        query: DeletePlatformUserOperationQuery
        request: DeletePlatformUserOperationRequest
        response: DeletePlatformUserOperationResponse
    }
    resetPlatformUserPassword: {
        pathParameters: ResetPlatformUserPasswordOperationPathParameters
        query: ResetPlatformUserPasswordOperationQuery
        request: ResetPlatformUserPasswordOperationRequest
        response: ResetPlatformUserPasswordOperationResponse
    }
    listPlatformAuditLogs: {
        pathParameters: ListPlatformAuditLogsOperationPathParameters
        query: ListPlatformAuditLogsOperationQuery
        request: ListPlatformAuditLogsOperationRequest
        response: ListPlatformAuditLogsOperationResponse
    }
    getPlatformAuditLogDetail: {
        pathParameters: GetPlatformAuditLogDetailOperationPathParameters
        query: GetPlatformAuditLogDetailOperationQuery
        request: GetPlatformAuditLogDetailOperationRequest
        response: GetPlatformAuditLogDetailOperationResponse
    }
    getWorkbenchDashboard: {
        pathParameters: GetWorkbenchDashboardOperationPathParameters
        query: GetWorkbenchDashboardOperationQuery
        request: GetWorkbenchDashboardOperationRequest
        response: GetWorkbenchDashboardOperationResponse
    }
    listCrossProjectWorkbenchTickets: {
        pathParameters: ListCrossProjectWorkbenchTicketsOperationPathParameters
        query: ListCrossProjectWorkbenchTicketsOperationQuery
        request: ListCrossProjectWorkbenchTicketsOperationRequest
        response: ListCrossProjectWorkbenchTicketsOperationResponse
    }
    listProjectTickets: {
        pathParameters: ListProjectTicketsOperationPathParameters
        query: ListProjectTicketsOperationQuery
        request: ListProjectTicketsOperationRequest
        response: ListProjectTicketsOperationResponse
    }
    createProjectTicket: {
        pathParameters: CreateProjectTicketOperationPathParameters
        query: CreateProjectTicketOperationQuery
        request: CreateProjectTicketOperationRequest
        response: CreateProjectTicketOperationResponse
    }
    listProjectOverdueTickets: {
        pathParameters: ListProjectOverdueTicketsOperationPathParameters
        query: ListProjectOverdueTicketsOperationQuery
        request: ListProjectOverdueTicketsOperationRequest
        response: ListProjectOverdueTicketsOperationResponse
    }
    listProjectSLABreachedTickets: {
        pathParameters: ListProjectSLABreachedTicketsOperationPathParameters
        query: ListProjectSLABreachedTicketsOperationQuery
        request: ListProjectSLABreachedTicketsOperationRequest
        response: ListProjectSLABreachedTicketsOperationResponse
    }
    getProjectTicket: {
        pathParameters: GetProjectTicketOperationPathParameters
        query: GetProjectTicketOperationQuery
        request: GetProjectTicketOperationRequest
        response: GetProjectTicketOperationResponse
    }
    updateProjectTicket: {
        pathParameters: UpdateProjectTicketOperationPathParameters
        query: UpdateProjectTicketOperationQuery
        request: UpdateProjectTicketOperationRequest
        response: UpdateProjectTicketOperationResponse
    }
    deleteProjectTicket: {
        pathParameters: DeleteProjectTicketOperationPathParameters
        query: DeleteProjectTicketOperationQuery
        request: DeleteProjectTicketOperationRequest
        response: DeleteProjectTicketOperationResponse
    }
    assignProjectTicket: {
        pathParameters: AssignProjectTicketOperationPathParameters
        query: AssignProjectTicketOperationQuery
        request: AssignProjectTicketOperationRequest
        response: AssignProjectTicketOperationResponse
    }
    transferProjectTicket: {
        pathParameters: TransferProjectTicketOperationPathParameters
        query: TransferProjectTicketOperationQuery
        request: TransferProjectTicketOperationRequest
        response: TransferProjectTicketOperationResponse
    }
    escalateProjectTicket: {
        pathParameters: EscalateProjectTicketOperationPathParameters
        query: EscalateProjectTicketOperationQuery
        request: EscalateProjectTicketOperationRequest
        response: EscalateProjectTicketOperationResponse
    }
    updateProjectTicketStatus: {
        pathParameters: UpdateProjectTicketStatusOperationPathParameters
        query: UpdateProjectTicketStatusOperationQuery
        request: UpdateProjectTicketStatusOperationRequest
        response: UpdateProjectTicketStatusOperationResponse
    }
    listProjectTicketHistory: {
        pathParameters: ListProjectTicketHistoryOperationPathParameters
        query: ListProjectTicketHistoryOperationQuery
        request: ListProjectTicketHistoryOperationRequest
        response: ListProjectTicketHistoryOperationResponse
    }
    listProjectTicketComments: {
        pathParameters: ListProjectTicketCommentsOperationPathParameters
        query: ListProjectTicketCommentsOperationQuery
        request: ListProjectTicketCommentsOperationRequest
        response: ListProjectTicketCommentsOperationResponse
    }
    createProjectTicketComment: {
        pathParameters: CreateProjectTicketCommentOperationPathParameters
        query: CreateProjectTicketCommentOperationQuery
        request: CreateProjectTicketCommentOperationRequest
        response: CreateProjectTicketCommentOperationResponse
    }
    listProjectTicketCommentReplies: {
        pathParameters: ListProjectTicketCommentRepliesOperationPathParameters
        query: ListProjectTicketCommentRepliesOperationQuery
        request: ListProjectTicketCommentRepliesOperationRequest
        response: ListProjectTicketCommentRepliesOperationResponse
    }
    listProjectTicketAttachments: {
        pathParameters: ListProjectTicketAttachmentsOperationPathParameters
        query: ListProjectTicketAttachmentsOperationQuery
        request: ListProjectTicketAttachmentsOperationRequest
        response: ListProjectTicketAttachmentsOperationResponse
    }
    uploadProjectTicketAttachment: {
        pathParameters: UploadProjectTicketAttachmentOperationPathParameters
        query: UploadProjectTicketAttachmentOperationQuery
        request: UploadProjectTicketAttachmentOperationRequest
        response: UploadProjectTicketAttachmentOperationResponse
    }
    downloadProjectTicketAttachment: {
        pathParameters: DownloadProjectTicketAttachmentOperationPathParameters
        query: DownloadProjectTicketAttachmentOperationQuery
        request: DownloadProjectTicketAttachmentOperationRequest
        response: DownloadProjectTicketAttachmentOperationResponse
    }
    listProjectNotifications: {
        pathParameters: ListProjectNotificationsOperationPathParameters
        query: ListProjectNotificationsOperationQuery
        request: ListProjectNotificationsOperationRequest
        response: ListProjectNotificationsOperationResponse
    }
    createProjectNotification: {
        pathParameters: CreateProjectNotificationOperationPathParameters
        query: CreateProjectNotificationOperationQuery
        request: CreateProjectNotificationOperationRequest
        response: CreateProjectNotificationOperationResponse
    }
    deleteProjectNotification: {
        pathParameters: DeleteProjectNotificationOperationPathParameters
        query: DeleteProjectNotificationOperationQuery
        request: DeleteProjectNotificationOperationRequest
        response: DeleteProjectNotificationOperationResponse
    }
    markProjectNotificationRead: {
        pathParameters: MarkProjectNotificationReadOperationPathParameters
        query: MarkProjectNotificationReadOperationQuery
        request: MarkProjectNotificationReadOperationRequest
        response: MarkProjectNotificationReadOperationResponse
    }
    markAllProjectNotificationsRead: {
        pathParameters: MarkAllProjectNotificationsReadOperationPathParameters
        query: MarkAllProjectNotificationsReadOperationQuery
        request: MarkAllProjectNotificationsReadOperationRequest
        response: MarkAllProjectNotificationsReadOperationResponse
    }
    getProjectUnreadNotificationCount: {
        pathParameters: GetProjectUnreadNotificationCountOperationPathParameters
        query: GetProjectUnreadNotificationCountOperationQuery
        request: GetProjectUnreadNotificationCountOperationRequest
        response: GetProjectUnreadNotificationCountOperationResponse
    }
    getHumanNotificationPreferences: {
        pathParameters: GetHumanNotificationPreferencesOperationPathParameters
        query: GetHumanNotificationPreferencesOperationQuery
        request: GetHumanNotificationPreferencesOperationRequest
        response: GetHumanNotificationPreferencesOperationResponse
    }
    updateHumanNotificationPreferences: {
        pathParameters: UpdateHumanNotificationPreferencesOperationPathParameters
        query: UpdateHumanNotificationPreferencesOperationQuery
        request: UpdateHumanNotificationPreferencesOperationRequest
        response: UpdateHumanNotificationPreferencesOperationResponse
    }
    listProjectAutomationRules: {
        pathParameters: ListProjectAutomationRulesOperationPathParameters
        query: ListProjectAutomationRulesOperationQuery
        request: ListProjectAutomationRulesOperationRequest
        response: ListProjectAutomationRulesOperationResponse
    }
    createProjectAutomationRule: {
        pathParameters: CreateProjectAutomationRuleOperationPathParameters
        query: CreateProjectAutomationRuleOperationQuery
        request: CreateProjectAutomationRuleOperationRequest
        response: CreateProjectAutomationRuleOperationResponse
    }
    getProjectAutomationRule: {
        pathParameters: GetProjectAutomationRuleOperationPathParameters
        query: GetProjectAutomationRuleOperationQuery
        request: GetProjectAutomationRuleOperationRequest
        response: GetProjectAutomationRuleOperationResponse
    }
    updateProjectAutomationRule: {
        pathParameters: UpdateProjectAutomationRuleOperationPathParameters
        query: UpdateProjectAutomationRuleOperationQuery
        request: UpdateProjectAutomationRuleOperationRequest
        response: UpdateProjectAutomationRuleOperationResponse
    }
    deleteProjectAutomationRule: {
        pathParameters: DeleteProjectAutomationRuleOperationPathParameters
        query: DeleteProjectAutomationRuleOperationQuery
        request: DeleteProjectAutomationRuleOperationRequest
        response: DeleteProjectAutomationRuleOperationResponse
    }
    listProjectAutomationLogs: {
        pathParameters: ListProjectAutomationLogsOperationPathParameters
        query: ListProjectAutomationLogsOperationQuery
        request: ListProjectAutomationLogsOperationRequest
        response: ListProjectAutomationLogsOperationResponse
    }
    getPlatformEmailConfig: {
        pathParameters: GetPlatformEmailConfigOperationPathParameters
        query: GetPlatformEmailConfigOperationQuery
        request: GetPlatformEmailConfigOperationRequest
        response: GetPlatformEmailConfigOperationResponse
    }
    updatePlatformEmailConfig: {
        pathParameters: UpdatePlatformEmailConfigOperationPathParameters
        query: UpdatePlatformEmailConfigOperationQuery
        request: UpdatePlatformEmailConfigOperationRequest
        response: UpdatePlatformEmailConfigOperationResponse
    }
    testPlatformEmailConfig: {
        pathParameters: TestPlatformEmailConfigOperationPathParameters
        query: TestPlatformEmailConfigOperationQuery
        request: TestPlatformEmailConfigOperationRequest
        response: TestPlatformEmailConfigOperationResponse
    }
    listPlatformConfigs: {
        pathParameters: ListPlatformConfigsOperationPathParameters
        query: ListPlatformConfigsOperationQuery
        request: ListPlatformConfigsOperationRequest
        response: ListPlatformConfigsOperationResponse
    }
    updatePlatformConfig: {
        pathParameters: UpdatePlatformConfigOperationPathParameters
        query: UpdatePlatformConfigOperationQuery
        request: UpdatePlatformConfigOperationRequest
        response: UpdatePlatformConfigOperationResponse
    }
    listProjectWebhooks: {
        pathParameters: ListProjectWebhooksOperationPathParameters
        query: ListProjectWebhooksOperationQuery
        request: ListProjectWebhooksOperationRequest
        response: ListProjectWebhooksOperationResponse
    }
    createProjectWebhook: {
        pathParameters: CreateProjectWebhookOperationPathParameters
        query: CreateProjectWebhookOperationQuery
        request: CreateProjectWebhookOperationRequest
        response: CreateProjectWebhookOperationResponse
    }
    getProjectWebhook: {
        pathParameters: GetProjectWebhookOperationPathParameters
        query: GetProjectWebhookOperationQuery
        request: GetProjectWebhookOperationRequest
        response: GetProjectWebhookOperationResponse
    }
    updateProjectWebhook: {
        pathParameters: UpdateProjectWebhookOperationPathParameters
        query: UpdateProjectWebhookOperationQuery
        request: UpdateProjectWebhookOperationRequest
        response: UpdateProjectWebhookOperationResponse
    }
    deleteProjectWebhook: {
        pathParameters: DeleteProjectWebhookOperationPathParameters
        query: DeleteProjectWebhookOperationQuery
        request: DeleteProjectWebhookOperationRequest
        response: DeleteProjectWebhookOperationResponse
    }
    queueProjectWebhookTest: {
        pathParameters: QueueProjectWebhookTestOperationPathParameters
        query: QueueProjectWebhookTestOperationQuery
        request: QueueProjectWebhookTestOperationRequest
        response: QueueProjectWebhookTestOperationResponse
    }
    listProjectWebhookLogs: {
        pathParameters: ListProjectWebhookLogsOperationPathParameters
        query: ListProjectWebhookLogsOperationQuery
        request: ListProjectWebhookLogsOperationRequest
        response: ListProjectWebhookLogsOperationResponse
    }
    getProjectWebhookStats: {
        pathParameters: GetProjectWebhookStatsOperationPathParameters
        query: GetProjectWebhookStatsOperationQuery
        request: GetProjectWebhookStatsOperationRequest
        response: GetProjectWebhookStatsOperationResponse
    }
    getAgentControlOverviewV2: {
        pathParameters: GetAgentControlOverviewV2OperationPathParameters
        query: GetAgentControlOverviewV2OperationQuery
        request: GetAgentControlOverviewV2OperationRequest
        response: GetAgentControlOverviewV2OperationResponse
    }
    createServicePrincipalV2: {
        pathParameters: CreateServicePrincipalV2OperationPathParameters
        query: CreateServicePrincipalV2OperationQuery
        request: CreateServicePrincipalV2OperationRequest
        response: CreateServicePrincipalV2OperationResponse
    }
    setServicePrincipalStatusV2: {
        pathParameters: SetServicePrincipalStatusV2OperationPathParameters
        query: SetServicePrincipalStatusV2OperationQuery
        request: SetServicePrincipalStatusV2OperationRequest
        response: SetServicePrincipalStatusV2OperationResponse
    }
    rotateServicePrincipalCredentialV2: {
        pathParameters: RotateServicePrincipalCredentialV2OperationPathParameters
        query: RotateServicePrincipalCredentialV2OperationQuery
        request: RotateServicePrincipalCredentialV2OperationRequest
        response: RotateServicePrincipalCredentialV2OperationResponse
    }
    revokeServicePrincipalCredentialV2: {
        pathParameters: RevokeServicePrincipalCredentialV2OperationPathParameters
        query: RevokeServicePrincipalCredentialV2OperationQuery
        request: RevokeServicePrincipalCredentialV2OperationRequest
        response: RevokeServicePrincipalCredentialV2OperationResponse
    }
    listServicePrincipalPoliciesV2: {
        pathParameters: ListServicePrincipalPoliciesV2OperationPathParameters
        query: ListServicePrincipalPoliciesV2OperationQuery
        request: ListServicePrincipalPoliciesV2OperationRequest
        response: ListServicePrincipalPoliciesV2OperationResponse
    }
    createServicePrincipalPolicyV2: {
        pathParameters: CreateServicePrincipalPolicyV2OperationPathParameters
        query: CreateServicePrincipalPolicyV2OperationQuery
        request: CreateServicePrincipalPolicyV2OperationRequest
        response: CreateServicePrincipalPolicyV2OperationResponse
    }
    disableServicePrincipalPolicyV2: {
        pathParameters: DisableServicePrincipalPolicyV2OperationPathParameters
        query: DisableServicePrincipalPolicyV2OperationQuery
        request: DisableServicePrincipalPolicyV2OperationRequest
        response: DisableServicePrincipalPolicyV2OperationResponse
    }
    forceReleaseTicketLeaseV2: {
        pathParameters: ForceReleaseTicketLeaseV2OperationPathParameters
        query: ForceReleaseTicketLeaseV2OperationQuery
        request: ForceReleaseTicketLeaseV2OperationRequest
        response: ForceReleaseTicketLeaseV2OperationResponse
    }
    recordAttachmentVirusScanV2: {
        pathParameters: RecordAttachmentVirusScanV2OperationPathParameters
        query: RecordAttachmentVirusScanV2OperationQuery
        request: RecordAttachmentVirusScanV2OperationRequest
        response: RecordAttachmentVirusScanV2OperationResponse
    }
    replayOutboxDeliveryV2: {
        pathParameters: ReplayOutboxDeliveryV2OperationPathParameters
        query: ReplayOutboxDeliveryV2OperationQuery
        request: ReplayOutboxDeliveryV2OperationRequest
        response: ReplayOutboxDeliveryV2OperationResponse
    }
}

export type HumanApiOperationId = keyof HumanApiOperationTypes

export type HumanApiPathParameters<Operation extends HumanApiOperationId> =
    HumanApiOperationTypes[Operation]["pathParameters"]
export type HumanApiQuery<Operation extends HumanApiOperationId> =
    HumanApiOperationTypes[Operation]["query"]
export type HumanApiRequest<Operation extends HumanApiOperationId> =
    HumanApiOperationTypes[Operation]["request"]
export type HumanApiResponse<Operation extends HumanApiOperationId> =
    HumanApiOperationTypes[Operation]["response"]

type HumanApiRequestBodyOption<Operation extends HumanApiOperationId> =
    (typeof humanApiOperations)[Operation]["requestBody"] extends "required"
        ? { body: HumanApiRequest<Operation> }
        : (typeof humanApiOperations)[Operation]["requestBody"] extends "optional"
            ? { body?: HumanApiRequest<Operation> }
            : { body?: never }

export type HumanApiRequestOptions<Operation extends HumanApiOperationId> = {
    pathParameters: HumanApiPathParameters<Operation>
    query?: HumanApiQuery<Operation>
} & HumanApiRequestBodyOption<Operation>

export type HumanApiClientRequest<Operation extends HumanApiOperationId> = {
    operationId: Operation
    method: (typeof humanApiOperations)[Operation]["method"]
    path: string
    body?: HumanApiRequest<Operation>
}

export const humanApiRoute = <Operation extends HumanApiOperationId>(
    operationId: Operation,
    pathParameters: HumanApiPathParameters<Operation>,
    query: HumanApiQuery<Operation> = {} as HumanApiQuery<Operation>,
): string => {
    const operation = humanApiOperations[operationId]
    const parameters = pathParameters as Record<string, string | number>
    const route = operation.path.replace(/\{([^}]+)\}/g, (_, name: string) => {
        const value = parameters[name]
        if (value === undefined || value === null || String(value) === "") {
            throw new Error(`Missing Human API path parameter ${name}`)
        }
        return encodeURIComponent(String(value))
    })
    const search = new URLSearchParams()
    for (const [name, rawValue] of Object.entries(query)) {
        if (rawValue === undefined || rawValue === null || rawValue === "") continue
        const values = Array.isArray(rawValue) ? rawValue : [rawValue]
        for (const value of values) search.append(name, String(value))
    }
    const encoded = search.toString()
    return encoded === "" ? route : `${route}?${encoded}`
}

export const buildHumanApiRequest = <Operation extends HumanApiOperationId>(
    operationId: Operation,
    options: HumanApiRequestOptions<Operation>,
): HumanApiClientRequest<Operation> => {
    const operation = humanApiOperations[operationId]
    const candidate = options as HumanApiRequestOptions<Operation> & {
        body?: HumanApiRequest<Operation>
    }
    return {
        operationId,
        method: operation.method,
        path: humanApiRoute(
            operationId,
            options.pathParameters,
            options.query,
        ),
        ...("body" in candidate ? { body: candidate.body } : {}),
    }
}

export const humanApiRoutes = {
    createHumanSession: (query: CreateHumanSessionOperationQuery = {}) =>
        humanApiRoute("createHumanSession", {}, query),
    refreshHumanSession: (query: RefreshHumanSessionOperationQuery = {}) =>
        humanApiRoute("refreshHumanSession", {}, query),
    requestHumanPasswordReset: (query: RequestHumanPasswordResetOperationQuery = {}) =>
        humanApiRoute("requestHumanPasswordReset", {}, query),
    resetHumanPassword: (query: ResetHumanPasswordOperationQuery = {}) =>
        humanApiRoute("resetHumanPassword", {}, query),
    deleteHumanSession: (query: DeleteHumanSessionOperationQuery = {}) =>
        humanApiRoute("deleteHumanSession", {}, query),
    deleteAllHumanSessions: (query: DeleteAllHumanSessionsOperationQuery = {}) =>
        humanApiRoute("deleteAllHumanSessions", {}, query),
    getHumanSessionUser: (query: GetHumanSessionUserOperationQuery = {}) =>
        humanApiRoute("getHumanSessionUser", {}, query),
    updateHumanProfile: (query: UpdateHumanProfileOperationQuery = {}) =>
        humanApiRoute("updateHumanProfile", {}, query),
    listAuthorizedHumanProjects: (query: ListAuthorizedHumanProjectsOperationQuery = {}) =>
        humanApiRoute("listAuthorizedHumanProjects", {}, query),
    getAuthorizedProjectContext: (pathParameters: GetAuthorizedProjectContextOperationPathParameters, query: GetAuthorizedProjectContextOperationQuery = {}) =>
        humanApiRoute("getAuthorizedProjectContext", pathParameters, query),
    listProjectMemberships: (pathParameters: ListProjectMembershipsOperationPathParameters, query: ListProjectMembershipsOperationQuery = {}) =>
        humanApiRoute("listProjectMemberships", pathParameters, query),
    upsertProjectMembership: (pathParameters: UpsertProjectMembershipOperationPathParameters, query: UpsertProjectMembershipOperationQuery = {}) =>
        humanApiRoute("upsertProjectMembership", pathParameters, query),
    searchProjectMembershipCandidates: (pathParameters: SearchProjectMembershipCandidatesOperationPathParameters, query: SearchProjectMembershipCandidatesOperationQuery = {}) =>
        humanApiRoute("searchProjectMembershipCandidates", pathParameters, query),
    deactivateProjectMembership: (pathParameters: DeactivateProjectMembershipOperationPathParameters, query: DeactivateProjectMembershipOperationQuery = {}) =>
        humanApiRoute("deactivateProjectMembership", pathParameters, query),
    listPlatformProjects: (query: ListPlatformProjectsOperationQuery = {}) =>
        humanApiRoute("listPlatformProjects", {}, query),
    createPlatformProject: (query: CreatePlatformProjectOperationQuery = {}) =>
        humanApiRoute("createPlatformProject", {}, query),
    getPlatformProjectCreationContext: (query: GetPlatformProjectCreationContextOperationQuery = {}) =>
        humanApiRoute("getPlatformProjectCreationContext", {}, query),
    listPlatformProjectBusinessUnits: (query: ListPlatformProjectBusinessUnitsOperationQuery = {}) =>
        humanApiRoute("listPlatformProjectBusinessUnits", {}, query),
    archivePlatformProject: (pathParameters: ArchivePlatformProjectOperationPathParameters, query: ArchivePlatformProjectOperationQuery = {}) =>
        humanApiRoute("archivePlatformProject", pathParameters, query),
    listPlatformUsers: (query: ListPlatformUsersOperationQuery = {}) =>
        humanApiRoute("listPlatformUsers", {}, query),
    createPlatformUser: (query: CreatePlatformUserOperationQuery = {}) =>
        humanApiRoute("createPlatformUser", {}, query),
    getPlatformUserStats: (query: GetPlatformUserStatsOperationQuery = {}) =>
        humanApiRoute("getPlatformUserStats", {}, query),
    getPlatformUser: (pathParameters: GetPlatformUserOperationPathParameters, query: GetPlatformUserOperationQuery = {}) =>
        humanApiRoute("getPlatformUser", pathParameters, query),
    updatePlatformUser: (pathParameters: UpdatePlatformUserOperationPathParameters, query: UpdatePlatformUserOperationQuery = {}) =>
        humanApiRoute("updatePlatformUser", pathParameters, query),
    deletePlatformUser: (pathParameters: DeletePlatformUserOperationPathParameters, query: DeletePlatformUserOperationQuery = {}) =>
        humanApiRoute("deletePlatformUser", pathParameters, query),
    resetPlatformUserPassword: (pathParameters: ResetPlatformUserPasswordOperationPathParameters, query: ResetPlatformUserPasswordOperationQuery = {}) =>
        humanApiRoute("resetPlatformUserPassword", pathParameters, query),
    listPlatformAuditLogs: (query: ListPlatformAuditLogsOperationQuery = {}) =>
        humanApiRoute("listPlatformAuditLogs", {}, query),
    getPlatformAuditLogDetail: (pathParameters: GetPlatformAuditLogDetailOperationPathParameters, query: GetPlatformAuditLogDetailOperationQuery = {}) =>
        humanApiRoute("getPlatformAuditLogDetail", pathParameters, query),
    getWorkbenchDashboard: (query: GetWorkbenchDashboardOperationQuery = {}) =>
        humanApiRoute("getWorkbenchDashboard", {}, query),
    listCrossProjectWorkbenchTickets: (query: ListCrossProjectWorkbenchTicketsOperationQuery = {}) =>
        humanApiRoute("listCrossProjectWorkbenchTickets", {}, query),
    listProjectTickets: (pathParameters: ListProjectTicketsOperationPathParameters, query: ListProjectTicketsOperationQuery = {}) =>
        humanApiRoute("listProjectTickets", pathParameters, query),
    createProjectTicket: (pathParameters: CreateProjectTicketOperationPathParameters, query: CreateProjectTicketOperationQuery = {}) =>
        humanApiRoute("createProjectTicket", pathParameters, query),
    listProjectOverdueTickets: (pathParameters: ListProjectOverdueTicketsOperationPathParameters, query: ListProjectOverdueTicketsOperationQuery = {}) =>
        humanApiRoute("listProjectOverdueTickets", pathParameters, query),
    listProjectSLABreachedTickets: (pathParameters: ListProjectSLABreachedTicketsOperationPathParameters, query: ListProjectSLABreachedTicketsOperationQuery = {}) =>
        humanApiRoute("listProjectSLABreachedTickets", pathParameters, query),
    getProjectTicket: (pathParameters: GetProjectTicketOperationPathParameters, query: GetProjectTicketOperationQuery = {}) =>
        humanApiRoute("getProjectTicket", pathParameters, query),
    updateProjectTicket: (pathParameters: UpdateProjectTicketOperationPathParameters, query: UpdateProjectTicketOperationQuery = {}) =>
        humanApiRoute("updateProjectTicket", pathParameters, query),
    deleteProjectTicket: (pathParameters: DeleteProjectTicketOperationPathParameters, query: DeleteProjectTicketOperationQuery = {}) =>
        humanApiRoute("deleteProjectTicket", pathParameters, query),
    assignProjectTicket: (pathParameters: AssignProjectTicketOperationPathParameters, query: AssignProjectTicketOperationQuery = {}) =>
        humanApiRoute("assignProjectTicket", pathParameters, query),
    transferProjectTicket: (pathParameters: TransferProjectTicketOperationPathParameters, query: TransferProjectTicketOperationQuery = {}) =>
        humanApiRoute("transferProjectTicket", pathParameters, query),
    escalateProjectTicket: (pathParameters: EscalateProjectTicketOperationPathParameters, query: EscalateProjectTicketOperationQuery = {}) =>
        humanApiRoute("escalateProjectTicket", pathParameters, query),
    updateProjectTicketStatus: (pathParameters: UpdateProjectTicketStatusOperationPathParameters, query: UpdateProjectTicketStatusOperationQuery = {}) =>
        humanApiRoute("updateProjectTicketStatus", pathParameters, query),
    listProjectTicketHistory: (pathParameters: ListProjectTicketHistoryOperationPathParameters, query: ListProjectTicketHistoryOperationQuery = {}) =>
        humanApiRoute("listProjectTicketHistory", pathParameters, query),
    listProjectTicketComments: (pathParameters: ListProjectTicketCommentsOperationPathParameters, query: ListProjectTicketCommentsOperationQuery = {}) =>
        humanApiRoute("listProjectTicketComments", pathParameters, query),
    createProjectTicketComment: (pathParameters: CreateProjectTicketCommentOperationPathParameters, query: CreateProjectTicketCommentOperationQuery = {}) =>
        humanApiRoute("createProjectTicketComment", pathParameters, query),
    listProjectTicketCommentReplies: (pathParameters: ListProjectTicketCommentRepliesOperationPathParameters, query: ListProjectTicketCommentRepliesOperationQuery = {}) =>
        humanApiRoute("listProjectTicketCommentReplies", pathParameters, query),
    listProjectTicketAttachments: (pathParameters: ListProjectTicketAttachmentsOperationPathParameters, query: ListProjectTicketAttachmentsOperationQuery = {}) =>
        humanApiRoute("listProjectTicketAttachments", pathParameters, query),
    uploadProjectTicketAttachment: (pathParameters: UploadProjectTicketAttachmentOperationPathParameters, query: UploadProjectTicketAttachmentOperationQuery = {}) =>
        humanApiRoute("uploadProjectTicketAttachment", pathParameters, query),
    downloadProjectTicketAttachment: (pathParameters: DownloadProjectTicketAttachmentOperationPathParameters, query: DownloadProjectTicketAttachmentOperationQuery = {}) =>
        humanApiRoute("downloadProjectTicketAttachment", pathParameters, query),
    listProjectNotifications: (pathParameters: ListProjectNotificationsOperationPathParameters, query: ListProjectNotificationsOperationQuery = {}) =>
        humanApiRoute("listProjectNotifications", pathParameters, query),
    createProjectNotification: (pathParameters: CreateProjectNotificationOperationPathParameters, query: CreateProjectNotificationOperationQuery = {}) =>
        humanApiRoute("createProjectNotification", pathParameters, query),
    deleteProjectNotification: (pathParameters: DeleteProjectNotificationOperationPathParameters, query: DeleteProjectNotificationOperationQuery = {}) =>
        humanApiRoute("deleteProjectNotification", pathParameters, query),
    markProjectNotificationRead: (pathParameters: MarkProjectNotificationReadOperationPathParameters, query: MarkProjectNotificationReadOperationQuery = {}) =>
        humanApiRoute("markProjectNotificationRead", pathParameters, query),
    markAllProjectNotificationsRead: (pathParameters: MarkAllProjectNotificationsReadOperationPathParameters, query: MarkAllProjectNotificationsReadOperationQuery = {}) =>
        humanApiRoute("markAllProjectNotificationsRead", pathParameters, query),
    getProjectUnreadNotificationCount: (pathParameters: GetProjectUnreadNotificationCountOperationPathParameters, query: GetProjectUnreadNotificationCountOperationQuery = {}) =>
        humanApiRoute("getProjectUnreadNotificationCount", pathParameters, query),
    getHumanNotificationPreferences: (query: GetHumanNotificationPreferencesOperationQuery = {}) =>
        humanApiRoute("getHumanNotificationPreferences", {}, query),
    updateHumanNotificationPreferences: (query: UpdateHumanNotificationPreferencesOperationQuery = {}) =>
        humanApiRoute("updateHumanNotificationPreferences", {}, query),
    listProjectAutomationRules: (pathParameters: ListProjectAutomationRulesOperationPathParameters, query: ListProjectAutomationRulesOperationQuery = {}) =>
        humanApiRoute("listProjectAutomationRules", pathParameters, query),
    createProjectAutomationRule: (pathParameters: CreateProjectAutomationRuleOperationPathParameters, query: CreateProjectAutomationRuleOperationQuery = {}) =>
        humanApiRoute("createProjectAutomationRule", pathParameters, query),
    getProjectAutomationRule: (pathParameters: GetProjectAutomationRuleOperationPathParameters, query: GetProjectAutomationRuleOperationQuery = {}) =>
        humanApiRoute("getProjectAutomationRule", pathParameters, query),
    updateProjectAutomationRule: (pathParameters: UpdateProjectAutomationRuleOperationPathParameters, query: UpdateProjectAutomationRuleOperationQuery = {}) =>
        humanApiRoute("updateProjectAutomationRule", pathParameters, query),
    deleteProjectAutomationRule: (pathParameters: DeleteProjectAutomationRuleOperationPathParameters, query: DeleteProjectAutomationRuleOperationQuery = {}) =>
        humanApiRoute("deleteProjectAutomationRule", pathParameters, query),
    listProjectAutomationLogs: (pathParameters: ListProjectAutomationLogsOperationPathParameters, query: ListProjectAutomationLogsOperationQuery = {}) =>
        humanApiRoute("listProjectAutomationLogs", pathParameters, query),
    getPlatformEmailConfig: (query: GetPlatformEmailConfigOperationQuery = {}) =>
        humanApiRoute("getPlatformEmailConfig", {}, query),
    updatePlatformEmailConfig: (query: UpdatePlatformEmailConfigOperationQuery = {}) =>
        humanApiRoute("updatePlatformEmailConfig", {}, query),
    testPlatformEmailConfig: (query: TestPlatformEmailConfigOperationQuery = {}) =>
        humanApiRoute("testPlatformEmailConfig", {}, query),
    listPlatformConfigs: (query: ListPlatformConfigsOperationQuery = {}) =>
        humanApiRoute("listPlatformConfigs", {}, query),
    updatePlatformConfig: (pathParameters: UpdatePlatformConfigOperationPathParameters, query: UpdatePlatformConfigOperationQuery = {}) =>
        humanApiRoute("updatePlatformConfig", pathParameters, query),
    listProjectWebhooks: (pathParameters: ListProjectWebhooksOperationPathParameters, query: ListProjectWebhooksOperationQuery = {}) =>
        humanApiRoute("listProjectWebhooks", pathParameters, query),
    createProjectWebhook: (pathParameters: CreateProjectWebhookOperationPathParameters, query: CreateProjectWebhookOperationQuery = {}) =>
        humanApiRoute("createProjectWebhook", pathParameters, query),
    getProjectWebhook: (pathParameters: GetProjectWebhookOperationPathParameters, query: GetProjectWebhookOperationQuery = {}) =>
        humanApiRoute("getProjectWebhook", pathParameters, query),
    updateProjectWebhook: (pathParameters: UpdateProjectWebhookOperationPathParameters, query: UpdateProjectWebhookOperationQuery = {}) =>
        humanApiRoute("updateProjectWebhook", pathParameters, query),
    deleteProjectWebhook: (pathParameters: DeleteProjectWebhookOperationPathParameters, query: DeleteProjectWebhookOperationQuery = {}) =>
        humanApiRoute("deleteProjectWebhook", pathParameters, query),
    queueProjectWebhookTest: (pathParameters: QueueProjectWebhookTestOperationPathParameters, query: QueueProjectWebhookTestOperationQuery = {}) =>
        humanApiRoute("queueProjectWebhookTest", pathParameters, query),
    listProjectWebhookLogs: (pathParameters: ListProjectWebhookLogsOperationPathParameters, query: ListProjectWebhookLogsOperationQuery = {}) =>
        humanApiRoute("listProjectWebhookLogs", pathParameters, query),
    getProjectWebhookStats: (pathParameters: GetProjectWebhookStatsOperationPathParameters, query: GetProjectWebhookStatsOperationQuery = {}) =>
        humanApiRoute("getProjectWebhookStats", pathParameters, query),
    getAgentControlOverviewV2: (pathParameters: GetAgentControlOverviewV2OperationPathParameters, query: GetAgentControlOverviewV2OperationQuery = {}) =>
        humanApiRoute("getAgentControlOverviewV2", pathParameters, query),
    createServicePrincipalV2: (pathParameters: CreateServicePrincipalV2OperationPathParameters, query: CreateServicePrincipalV2OperationQuery = {}) =>
        humanApiRoute("createServicePrincipalV2", pathParameters, query),
    setServicePrincipalStatusV2: (pathParameters: SetServicePrincipalStatusV2OperationPathParameters, query: SetServicePrincipalStatusV2OperationQuery = {}) =>
        humanApiRoute("setServicePrincipalStatusV2", pathParameters, query),
    rotateServicePrincipalCredentialV2: (pathParameters: RotateServicePrincipalCredentialV2OperationPathParameters, query: RotateServicePrincipalCredentialV2OperationQuery = {}) =>
        humanApiRoute("rotateServicePrincipalCredentialV2", pathParameters, query),
    revokeServicePrincipalCredentialV2: (pathParameters: RevokeServicePrincipalCredentialV2OperationPathParameters, query: RevokeServicePrincipalCredentialV2OperationQuery = {}) =>
        humanApiRoute("revokeServicePrincipalCredentialV2", pathParameters, query),
    listServicePrincipalPoliciesV2: (pathParameters: ListServicePrincipalPoliciesV2OperationPathParameters, query: ListServicePrincipalPoliciesV2OperationQuery = {}) =>
        humanApiRoute("listServicePrincipalPoliciesV2", pathParameters, query),
    createServicePrincipalPolicyV2: (pathParameters: CreateServicePrincipalPolicyV2OperationPathParameters, query: CreateServicePrincipalPolicyV2OperationQuery = {}) =>
        humanApiRoute("createServicePrincipalPolicyV2", pathParameters, query),
    disableServicePrincipalPolicyV2: (pathParameters: DisableServicePrincipalPolicyV2OperationPathParameters, query: DisableServicePrincipalPolicyV2OperationQuery = {}) =>
        humanApiRoute("disableServicePrincipalPolicyV2", pathParameters, query),
    forceReleaseTicketLeaseV2: (pathParameters: ForceReleaseTicketLeaseV2OperationPathParameters, query: ForceReleaseTicketLeaseV2OperationQuery = {}) =>
        humanApiRoute("forceReleaseTicketLeaseV2", pathParameters, query),
    recordAttachmentVirusScanV2: (pathParameters: RecordAttachmentVirusScanV2OperationPathParameters, query: RecordAttachmentVirusScanV2OperationQuery = {}) =>
        humanApiRoute("recordAttachmentVirusScanV2", pathParameters, query),
    replayOutboxDeliveryV2: (pathParameters: ReplayOutboxDeliveryV2OperationPathParameters, query: ReplayOutboxDeliveryV2OperationQuery = {}) =>
        humanApiRoute("replayOutboxDeliveryV2", pathParameters, query),
} as const
