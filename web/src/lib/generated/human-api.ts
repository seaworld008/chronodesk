/**
 * Generated from server/internal/humanopenapi/openapi.json.
 * Generator: chronodesk-human-openapi-types@2.1.0.
 * Contract SHA-256: b8524e8f0b7dfd79a8546f1393254ba63e21b7af4d46167d3eb6124c0fd97aea.
 * Do not edit by hand; run `npm run generate:human-api`.
 */

export const platformRoleValues = ["platform_admin","security_auditor","emergency_operator","member"] as const
export type PlatformRole = (typeof platformRoleValues)[number]

export const projectRoleValues = ["project_admin","manager","agent","requester","observer"] as const
export type ProjectRole = (typeof projectRoleValues)[number]

export type UserStatus = "active" | "inactive" | "suspended" | "deleted"

export type ProjectStatus = "active" | "archived"

export type PublicUUIDv7 = string

export type RegisterHumanRequest = {
    username: string
    email: string
    password: string
    confirm_password: string
    first_name?: string
    last_name?: string
    department?: string
    position?: string
}

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

export type VerifyHumanEmailRequest = {
    token: string
}

export type ResendHumanEmailVerificationRequest = {
    email: string
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
    knowledge_contributor?: boolean
    expected_version: number
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

export type HumanRegistrationResult = {
    user: HumanSessionUser
    access_token: string
    refresh_token: string
    expires_in: number
    token_type: "" | "Bearer"
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
    can_create_knowledge_drafts: boolean
    scope: ProjectScope
    scopes?: Array<string>
}

export type AuthorizedProjectPage = {
    items: Array<AuthorizedProjectAccess>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AuthorizedProjectPageEnvelope = SuccessEnvelope & {
    data: AuthorizedProjectPage
}

export type ProjectMembership = {
    id: number
    project_id: number
    user_id: number
    user?: HumanUserSummary
    role: ProjectRole
    is_active: boolean
    knowledge_contributor: boolean
    version: number
    created_at: string
    updated_at: string
}

export type ProjectMembershipPage = {
    items: Array<ProjectMembership>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ProjectMembershipPageEnvelope = SuccessEnvelope & {
    data: ProjectMembershipPage
}

export type TrustedDevice = {
    id: number
    device_name: string
    last_used_at: string
    last_ip: string
    user_agent: string
    expires_at: string
    revoked: boolean
    created_at: string
    updated_at: string
}

export type TrustedDevicePage = {
    items: Array<TrustedDevice>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TrustedDevicePageEnvelope = SuccessEnvelope & {
    data: TrustedDevicePage
}

export type QueueStatus = "active" | "archived"

export type ProjectQueue = {
    public_id: string
    created_at: string
    updated_at: string
    team_public_id?: string
    team_name?: string
    key: string
    name: string
    description: string
    status: QueueStatus
    is_default: boolean
}

export type ProjectQueuePage = {
    items: Array<ProjectQueue>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ProjectQueuePageEnvelope = SuccessEnvelope & {
    data: ProjectQueuePage
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
    actor_type: ActorType
    actor_id: string
    user_id?: number
    username: string
    platform_role?: PlatformRole
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
    actor_type: ActorType
    actor_id: string
    user_id?: number
    username: string
    platform_role?: PlatformRole
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
    next_cursor: string
    has_more: boolean
}

export const adminAuditExportStateValues = ["queued","processing","completed","failed","expired"] as const
export type AdminAuditExportState = (typeof adminAuditExportStateValues)[number]

export type AdminAuditExport = {
    public_id: PublicUUIDv7
    state: AdminAuditExportState
    requested_at: string
    started_at?: string
    completed_at?: string
    expires_at?: string
    row_count: number
    truncated: boolean
    sha256?: string
    size_bytes: number
    failure_code?: "storage_unavailable" | "query_failed" | "generation_failed" | "lease_lost"
}

export type AdminAuditExportEnvelope = SuccessEnvelope & {
    data: AdminAuditExport
}

export type RegenerateOTPBackupCodesRequest = {
    current_password: string
}

export type OTPBackupCodeRegenerationEnvelope = {
    success: true
    message: string
    data: {
        backup_codes: Array<string>
    }
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

export type HumanRegistrationEnvelope = {
    code: 0
    msg: "注册成功"
    data: HumanRegistrationResult
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

export type HumanTicketSource = "web" | "email" | "phone" | "chat" | "api" | "mobile"

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
    source: HumanTicketSource
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
    source?: HumanTicketSource
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

export type TicketAllowedTransitions = {
    allowed_next_statuses: Array<TicketStatus>
}

export type TicketAllowedTransitionsEnvelope = {
    success: true
    data: TicketAllowedTransitions
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
    customer_email?: string
    customer_phone?: string
    customer_name?: string
    custom_fields: unknown
    view_count: number
    comment_count: number
    rating: number | null
    rating_comment: string
    version: number
    agent_context?: AgentContext
    trust_level: TicketTrustLevel
    created_by_actor?: ActorRef
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
    selected_project_count: number
    selected_projects_truncated: boolean
    summary: WorkbenchDashboardSummary
    daily_trend: Array<WorkbenchDashboardDailyPoint>
    project_breakdown: Array<WorkbenchDashboardProjectBreakdown>
    project_breakdown_truncated: boolean
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
    channel?: "in_app" | "email"
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

export type NotificationTicketSummary = {
    id: number
    ticket_number: string
    title: string
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
    related_ticket: NotificationTicketSummary | null
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
    notification_type: NotificationType
    email_enabled: boolean
    in_app_enabled: boolean
    webhook_enabled: false
    do_not_disturb_start: string | null
    do_not_disturb_end: string | null
    max_daily_count: number
    batch_delivery: false
    batch_interval: 60
}

export type NotificationPreferenceUpdate = {
    notification_type: NotificationType
    email_enabled: boolean
    in_app_enabled: boolean
    webhook_enabled: false
    do_not_disturb_start?: string | null
    do_not_disturb_end?: string | null
    max_daily_count: number
    batch_delivery: false
    batch_interval: 60
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

export type AutomationRuleListItem = {
    id: number
    name: string
    description: string
    rule_type: "assignment" | "classification" | "escalation" | "sla"
    is_active: boolean
    priority: number
    trigger_event: string
    execution_count: number
    last_executed_at?: string
    success_count: number
    failure_count: number
    average_exec_time: number
    created_at: string
    updated_at: string
}

export type AutomationRulePage = {
    items: Array<AutomationRuleListItem>
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
    rule_id: number
    rule?: AutomationRuleLogSummary
    ticket_id: number
    ticket?: AutomationTicketLogSummary
    trigger_event: string
    executed_at: string
    success: boolean
    error_message?: string
    execution_time: number
}

export type AutomationLogPage = {
    items: Array<AutomationLog>
    next_cursor: string
    has_more: boolean
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

export type CleanupLog = {
    id: number
    created_at: string
    task_type: string
    status: "started" | "completed" | "failed"
    start_time: string
    end_time?: string
    duration: string
    records_processed: number
    records_deleted: number
    error_message?: string
    retention_days: number
    cutoff_date: string
    trigger_type: "manual" | "scheduled"
    trigger_by?: number
}

export type CleanupLogPage = {
    items: Array<CleanupLog>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type CleanupLogPageEnvelope = {
    success: true
    data: CleanupLogPage
}

export type CleanupErrorEnvelope = {
    error: string
    message: string
}

export type EmergencyControlSnapshot = {
    global_read_only: boolean
    emergency_stop: boolean
    version: number
    updated_at: string
}

export type UpdateEmergencyControlsRequest = {
    global_read_only?: boolean
    emergency_stop?: boolean
}

export type EmergencyControlEnvelope = {
    code: 0
    msg: string
    data: EmergencyControlSnapshot
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

export type SystemConfigPage = {
    items: Array<SystemConfig>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type SystemConfigListEnvelope = {
    success: true
    message: string
    data: SystemConfigPage
}

export const webhookProviderValues = ["wechat","dingtalk","lark","slack","teams","custom"] as const
export type WebhookProvider = (typeof webhookProviderValues)[number]

export const webhookStatusValues = ["active","inactive","disabled","error"] as const
export type WebhookStatus = (typeof webhookStatusValues)[number]

export type WebhookEventType = "io.chronodesk.ticket.created.v1" | "io.chronodesk.ticket.updated.v1" | "io.chronodesk.ticket.assigned.v1" | "io.chronodesk.ticket.transitioned.v1" | "io.chronodesk.ticket.escalated.v1" | "io.chronodesk.ticket.comment.created.v1" | "io.chronodesk.ticket.attachment.created.v1" | "io.chronodesk.ticket.sla.breached.v1" | "io.chronodesk.ticket.deleted.v1" | "io.chronodesk.automation.notification.requested.v1" | "io.chronodesk.system.alert.v1"

export type WebhookFilterRules = {
    transition_statuses?: Array<TicketStatus>
}

export type CreateWebhookRequest = {
    name: string
    description?: string
    provider: WebhookProvider
    webhook_url: string
    enabled_events?: Array<WebhookEventType>
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
    enabled_events?: Array<WebhookEventType>
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
    webhook_url_masked: string
    has_webhook_url: boolean
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
    enabled_events_list?: Array<WebhookEventType>
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
    resource_version: ResourceVersion
}

export type WebhookEmergencyRevokeResult = {
    config_id: number
    status: "disabled"
    expired_deliveries: number
    in_flight_deliveries: number
    shredded_snapshots: number
    credential_shred_reason: "revoked"
}

export type WebhookEmergencyRevokePreflight = {
    config_id: number
    status: WebhookStatus
    deleted: boolean
    emergency_revoked: boolean
    resource_version: ResourceVersion
}

export type WebhookEmergencyRevokePreflightEnvelope = Envelope & {
    data?: WebhookEmergencyRevokePreflight
}

export type WebhookEmergencyTombstonePage = {
    items: Array<WebhookEmergencyRevokePreflight>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type WebhookEmergencyTombstonePageEnvelope = Envelope & {
    data?: WebhookEmergencyTombstonePage
}

export type WebhookEmergencyRevokeEnvelope = Envelope & {
    data?: WebhookEmergencyRevokeResult
    receipt: Receipt
}

export type WebhookPage = {
    items: Array<WebhookConfig>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type WebhookLog = {
    id: number
    created_at: string
    config_id: number
    event_type: WebhookEventType
    status: "pending" | "success" | "failed"
    response_status?: number
    response_time?: number
    error_message?: string
}

export type WebhookLogPage = {
    items: Array<WebhookLog>
    next_cursor: string
    has_more: boolean
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

export type WebhookLogPageEnvelope = SuccessEnvelope & {
    data: WebhookLogPage
}

export type WebhookTestReceiptEnvelope = SuccessEnvelope & {
    data: WebhookTestReceipt
}

export type AdminOverviewEnvelope = Envelope & {
    data?: AdminOverview
}

export type AdminPrincipalPage = {
    items: Array<AdminServicePrincipalSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AdminPolicyPage = {
    items: Array<AdminAgentPolicy>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AdminLeasePage = {
    items: Array<AdminTicketLeaseSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AdminOutboxPage = {
    items: Array<AdminOutboxDeliverySummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AdminAttachmentPage = {
    items: Array<AdminAttachmentSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AdminDomainEventCursorPage = {
    items: Array<AdminDomainEventSummary>
    next_cursor: string
    has_more: boolean
}

export type AdminPolicyDecisionCursorPage = {
    items: Array<AdminPolicyDecisionSummary>
    next_cursor: string
    has_more: boolean
}

export type AdminPrincipalPageEnvelope = Envelope & {
    data?: AdminPrincipalPage
}

export type AdminPolicyPageEnvelope = Envelope & {
    data?: AdminPolicyPage
}

export type AdminLeasePageEnvelope = Envelope & {
    data?: AdminLeasePage
}

export type AdminOutboxPageEnvelope = Envelope & {
    data?: AdminOutboxPage
}

export type AdminAttachmentPageEnvelope = Envelope & {
    data?: AdminAttachmentPage
}

export type AdminDomainEventCursorEnvelope = Envelope & {
    data?: AdminDomainEventCursorPage
}

export type AdminPolicyDecisionCursorEnvelope = Envelope & {
    data?: AdminPolicyDecisionCursorPage
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
    emergency_stop: boolean
    principal_count: number
    active_principal_count: number
    active_lease_count: number
    failed_outbox_count: number
    recent_event_count: number
    pending_attachment_scan_count: number
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
    grant: AdminPrincipalGrantSummary
}

export type AdminPrincipalGrantSummary = {
    id: number
    project_id: number
    role: ProjectRole
    scopes: Array<AgentScope>
    is_active: boolean
    expires_at: string | null
    created_at: string
}

export type AgentScope = "tickets:read" | "tickets:create" | "tickets:update" | "tickets:assign" | "tickets:transition" | "comments:write" | "attachments:read" | "attachments:write" | "events:subscribe" | "tasks:manage"

export type AdminTicketLeaseSummary = {
    id: string
    ticket_id: number
    ticket_number: string
    holder_actor_type: "human" | "service_principal" | "system"
    holder_actor_id: string
    holder_display_name: string
    acquired_at: string
    expires_at: string
    ticket_version: ResourceVersion
    resource_version: ResourceVersion
}

export type AdminDomainEventSummary = {
    id: string
    created_at: string
    type: string
    subject: string
    actor_type: "human" | "service_principal" | "system"
    actor_id: string
    resource_version: ResourceVersion
    time: string
}

export type AdminOutboxDeliverySummary = {
    id: string
    created_at: string
    event_id: string
    destination_type: "webhook" | "event_stream" | "automation" | "notification" | "sla" | "sla_escalation" | "attachment_upload" | "attachment_cleanup" | "attachment_staging_cleanup" | "a2a_push" | "email" | "other"
    destination_label: string
    status: "pending" | "processing" | "succeeded" | "failed" | "dead" | "expired"
    attempts: number
    next_attempt_at: string
    last_error: string
    expires_at: string | null
    expired_at: string | null
    updated_at: string
    resource_version: ResourceVersion
}

export type AdminAttachmentSummary = {
    id: number
    created_at: string
    ticket_id: number
    original_name: string
    mime_type: string
    file_size: number
    virus_scan: "pending" | "clean" | "infected" | "error"
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
}

export type Problem = {
    type: string
    title: string
    status: number
    detail?: string
    code: "invalid_request" | "invalid_status_transition" | "ticket_form_validation_failed" | "workflow_transition_required" | "trusted_source_not_human_writable" | "precondition_required" | "unauthorized" | "invalid_actor" | "invalid_scope" | "principal_not_found" | "principal_disabled" | "principal_expired" | "invalid_credential" | "credential_expired" | "insufficient_scope" | "policy_denied" | "agent_emergency_stop" | "read_only" | "automation_loop" | "not_found" | "version_conflict" | "webhook_emergency_revoked" | "lease_conflict" | "lease_expired" | "lease_not_owned" | "idempotency_conflict" | "idempotency_in_progress" | "command_scope_mismatch" | "outbox_replay_conflict" | "outbox_replay_expired" | "rate_limited" | "concurrency_limit" | "attachment_rejected" | "attachment_too_large" | "attachment_not_clean" | "invalid_attachment_name" | "service_unavailable" | "internal_error"
    request_id: string
    retryable: boolean
    details?: unknown
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

export type LoginStatus = "success" | "failed" | "blocked" | "suspended" | "expired"

export type LoginMethod = "password" | "password+trusted" | "password+otp" | "password+otp_required"

export type LoginHistoryRecord = {
    id: number
    ip_address: string
    login_time: string
    logout_time: string | null
    login_status: LoginStatus
    login_method: LoginMethod
    failure_reason?: string
    location: string
    device_info: string
    session_duration: string
    is_current_session: boolean
    is_active: boolean
}

export type LoginHistoryPage = {
    items: Array<LoginHistoryRecord>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type LoginHistoryPageEnvelope = {
    code: 0
    msg: string
    data: LoginHistoryPage
}

export type CategoryStatus = "active" | "inactive" | "archived"

export type CategoryType = "general" | "technical" | "business" | "support" | "incident" | "request" | "billing" | "complaint"

export type ProjectCategory = {
    id: number
    name: string
    slug: string
    description: string
    icon: string
    color: string
    type: CategoryType
    status: CategoryStatus
    sort_order: number
    parent_id: number | null
    level: number
    path: string
    is_default: boolean
    is_public: boolean
}

export type ProjectCategoryPage = {
    items: Array<ProjectCategory>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ProjectCategoryPageEnvelope = {
    code: 0
    msg: string
    data: ProjectCategoryPage
}

export type ProjectCategoryEnvelope = {
    code: 0
    msg: string
    data: ProjectCategory
}

export type ProjectAssignee = {
    id: number
    username: string
    first_name: string
    last_name: string
    display_name: string
    role: ProjectRole
}

export type ProjectAssigneePage = {
    items: Array<ProjectAssignee>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ProjectAssigneePageEnvelope = {
    code: 0
    msg: string
    data: ProjectAssigneePage
}

export type ProjectAssigneeEnvelope = {
    code: 0
    msg: string
    data: ProjectAssignee
}

export type EntityKind = "asset" | "device" | "application" | "contract" | "customer" | "location" | "other"

export type TicketRelationType = "parent_of" | "duplicate_of" | "blocks" | "collaborates_with"

export type TicketRelationDirection = "outgoing" | "incoming"

export type TicketEntityLink = {
    id: string
    created_at: string
    ticket_id: number
    kind: EntityKind
    reference_id: string
    display_name: string
    metadata: unknown
}

export type TicketRelation = {
    id: string
    created_at: string
    source_ticket_id: number
    target_ticket_id: number
    direction: TicketRelationDirection
    related_ticket_id: number
    related_ticket_number: string
    related_ticket_title: string
    relation: TicketRelationType
    reason: string
}

export type TicketEntityLinkPage = {
    items: Array<TicketEntityLink>
    total: number
    page: number
    page_size: number
    total_pages: number
    ticket_version: number
}

export type TicketRelationPage = {
    items: Array<TicketRelation>
    total: number
    page: number
    page_size: number
    total_pages: number
    ticket_version: number
}

export type TicketEntityLinkPageEnvelope = {
    success: true
    data: TicketEntityLinkPage
}

export type TicketRelationPageEnvelope = {
    success: true
    data: TicketRelationPage
}

export type AddTicketEntityLinkRequest = {
    expected_version: number
    kind: EntityKind
    reference_id: string
    display_name: string
    metadata?: unknown
}

export type AddTicketRelationRequest = {
    expected_version: number
    target_ticket_id: number
    relation: TicketRelationType
    reason?: string
}

export type AddTicketEntityLinkResult = {
    link: TicketEntityLink
    ticket_version: number
    event_id: string
}

export type AddTicketRelationResult = {
    relation: TicketRelation
    ticket_version: number
    event_id: string
}

export type AddTicketEntityLinkEnvelope = {
    success: true
    data: AddTicketEntityLinkResult
}

export type AddTicketRelationEnvelope = {
    success: true
    data: AddTicketRelationResult
}

export type AgentRunStatus = "queued" | "running" | "waiting_approval" | "succeeded" | "failed" | "cancelled" | "taken_over"

export type ActionRiskLevel = "low" | "medium" | "high" | "critical"

export type ActionProposalStatus = "pending" | "approved" | "rejected" | "executed" | "invalidated" | "expired"

export type ApprovalTaskStatus = "pending" | "approved" | "rejected" | "invalidated" | "expired"

export type HandoffDirection = "human_to_agent" | "agent_to_human" | "queue_to_team"

export type AgentRunSummary = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    status: AgentRunStatus
}

export type AgentRunDetail = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    status: AgentRunStatus
    model_provider: string
    model_name: string
    prompt_version: string
    toolset_version: string
    policy_version: string
    input_summary?: string
    output_summary?: string
    prompt_tokens: number
    completion_tokens: number
    cost_micros: number
    started_at?: string
    finished_at?: string
    termination_reason?: string
}

export type ActionProposalSummary = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    agent_run_id: string
    action_type: string
    risk_level: ActionRiskLevel
    target_ticket_version: number
    status: ActionProposalStatus
    expires_at: string
    executed_at?: string
}

export type ActionProposalDetail = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    agent_run_id: string
    action_type: string
    risk_level: ActionRiskLevel
    target_ticket_version: number
    status: ActionProposalStatus
    expires_at: string
    executed_at?: string
    preview: unknown
}

export type ApprovalTaskSummary = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    proposal_id: string
    target_ticket_version: number
    required_approvals: number
    status: ApprovalTaskStatus
    expires_at: string
    completed_at?: string
}

export type ApprovalTaskDetail = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    updated_at: string
    proposal_id: string
    target_ticket_version: number
    required_approvals: number
    status: ApprovalTaskStatus
    expires_at: string
    completed_at?: string
    approvals_recorded: number
    rejections_recorded: number
}

export type HandoffSummary = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    agent_run_id?: string
    direction: HandoffDirection
}

export type HandoffDetail = {
    id: string
    created_at: string
    ticket_id: number
    ticket_number: string
    ticket_title: string
    agent_run_id?: string
    direction: HandoffDirection
    reason: string
    completed_summary?: string
    missing_information: Array<string>
}

export type AgentRunPage = {
    items: Array<AgentRunSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type AgentRunPageEnvelope = {
    code: 0
    msg: string
    data: AgentRunPage
}

export type ActionProposalPage = {
    items: Array<ActionProposalSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ActionProposalPageEnvelope = {
    code: 0
    msg: string
    data: ActionProposalPage
}

export type ApprovalTaskPage = {
    items: Array<ApprovalTaskSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type ApprovalTaskPageEnvelope = {
    code: 0
    msg: string
    data: ApprovalTaskPage
}

export type HandoffPage = {
    items: Array<HandoffSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type HandoffPageEnvelope = {
    code: 0
    msg: string
    data: HandoffPage
}

export type AgentRunDetailEnvelope = {
    code: 0
    msg: string
    data: AgentRunDetail
}

export type ActionProposalDetailEnvelope = {
    code: 0
    msg: string
    data: ActionProposalDetail
}

export type ApprovalTaskDetailEnvelope = {
    code: 0
    msg: string
    data: ApprovalTaskDetail
}

export type HandoffDetailEnvelope = {
    code: 0
    msg: string
    data: HandoffDetail
}

export type ApprovalDecisionRequest = {
    decision: "approve" | "reject"
    comment?: string
}

export type AgentRunTakeoverRequest = {
    reason: string
    completed_summary?: string
    missing_information?: Array<string>
    evidence_digest: string
}

export type ConfigurationVersionStatus = "draft" | "simulated" | "published"

export type WorkClass = "incident" | "request" | "problem" | "change" | "complaint" | "consultation"

export type IntakeRequestTypeVersion = {
    id: string
    version: number
    status: ConfigurationVersionStatus
    key: string
    name: string
    description: string
    work_class: WorkClass
    json_schema: unknown
    ui_schema: unknown
    published_at?: string
}

export type IntakeWorkflowVersion = {
    id: string
    version: number
    status: ConfigurationVersionStatus
    key: string
    name: string
    description: string
    states: unknown
    transitions: unknown
    published_at?: string
}

export type ProjectIntakeConfiguration = {
    release_id: string
    release_version: number
    request_types: Array<IntakeRequestTypeVersion>
    workflows: Array<IntakeWorkflowVersion>
}

export type ProjectIntakeConfigurationEnvelope = {
    code: 0
    msg: string
    data: ProjectIntakeConfiguration
}

export type SLAConfig = {
    id: number
    created_at: string
    updated_at: string
    name: string
    description: string
    is_active: boolean
    is_default: boolean
    ticket_type?: string
    priority?: string
    category?: string
    assigned_user_id?: number
    response_time: number
    resolution_time: number
    exclude_weekends: boolean
    exclude_holidays: boolean
    applied_count: number
    violation_count: number
    compliance_rate: number
}

export type TimeRange = {
    start: string
    end: string
}

export type WorkingHours = {
    monday: TimeRange
    tuesday: TimeRange
    wednesday: TimeRange
    thursday: TimeRange
    friday: TimeRange
    saturday: TimeRange
    sunday: TimeRange
    timezone?: string
    holidays?: Array<string>
}

export type EscalationRule = {
    trigger_minutes: number
    action: string
    target_user_id?: number
    notify_users?: Array<number>
}

export type SLAConfigRequest = {
    name: string
    description?: string
    is_active?: boolean
    is_default?: boolean
    ticket_type?: string
    priority?: string
    category?: string
    assigned_user_id?: number
    response_time: number
    resolution_time: number
    working_hours?: WorkingHours
    exclude_weekends?: boolean
    exclude_holidays?: boolean
    escalation_rules?: Array<EscalationRule>
}

export type SLAConfigPage = {
    items: Array<SLAConfig>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type SLAConfigEnvelope = {
    success: true
    message: string
    data: SLAConfig
}

export type SLAConfigPageEnvelope = {
    success: true
    message: string
    data: SLAConfigPage
}

export type TicketTemplate = {
    id: number
    created_at: string
    updated_at: string
    name: string
    description: string
    category: string
    is_active: boolean
    title_template: string
    content_template: string
    default_type: string
    default_priority: string
    default_status: string
    assign_to_user_id?: number
    usage_count: number
}

export type CustomField = {
    name: string
    type: "text" | "textarea" | "select" | "checkbox" | "date"
    label: string
    required: boolean
    default_value?: unknown
    options?: Array<string>
}

export type TicketTemplateRequest = {
    name: string
    description?: string
    category: string
    is_active?: boolean
    title_template?: string
    content_template?: string
    default_type?: string
    default_priority?: string
    default_status?: string
    assign_to_user_id?: number
    custom_fields?: Array<CustomField>
}

export type TicketTemplatePage = {
    items: Array<TicketTemplate>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type TicketTemplateEnvelope = {
    success: true
    message: string
    data: TicketTemplate
}

export type TicketTemplatePageEnvelope = {
    success: true
    message: string
    data: TicketTemplatePage
}

export type QuickReply = {
    id: number
    created_at: string
    updated_at: string
    name: string
    category: string
    content: string
    tags: string
    is_public: boolean
    usage_count: number
}

export type QuickReplyRequest = {
    name: string
    category?: string
    content: string
    tags?: string
    is_public?: boolean
}

export type QuickReplyPage = {
    items: Array<QuickReply>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type QuickReplyEnvelope = {
    success: true
    message: string
    data: QuickReply
}

export type QuickReplyPageEnvelope = {
    success: true
    message: string
    data: QuickReplyPage
}

export type KnowledgeArticleStatus = "active" | "archived"

export type KnowledgeVersionStatus = "draft" | "published" | "superseded" | "quarantined"

export type VirusScanStatus = "pending" | "clean" | "infected" | "error"

export type KnowledgeIngestionStatus = "queued" | "parsing" | "indexing" | "completed" | "quarantined" | "failed"

export type KnowledgeArticle = {
    id: string
    key: string
    title: string
    summary: string
    status: KnowledgeArticleStatus
    current_version_id?: string
    revision: number
    created_at: string
    updated_at: string
    has_unpublished_draft?: boolean
    latest_draft_at?: string
    latest_draft_version?: number
}

export type KnowledgeVersion = {
    id: string
    article_id: string
    version: number
    status: KnowledgeVersionStatus
    created_by_type: ActorType
    title: string
    original_file_name: string
    mime_type: string
    size_bytes: number
    content_hash: string
    virus_scan: VirusScanStatus
    scanned_at?: string | null
    page_count: number
    published_at?: string | null
    created_at: string
    updated_at: string
}

export type KnowledgeIngestion = {
    id: string
    article_id: string
    version_id: string
    attempt: number
    status: KnowledgeIngestionStatus
    parser_key: string
    started_at?: string | null
    completed_at?: string | null
    created_at: string
    updated_at: string
}

export type KnowledgeArticlePage = {
    items: Array<KnowledgeArticle>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type KnowledgeArticlePageEnvelope = {
    code: 0
    msg: string
    data: KnowledgeArticlePage
}

export type KnowledgeVersionPage = {
    items: Array<KnowledgeVersion>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type KnowledgeVersionPageEnvelope = {
    code: 0
    msg: string
    data: KnowledgeVersionPage
}

export type KnowledgeIngestionPage = {
    items: Array<KnowledgeIngestion>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type KnowledgeIngestionPageEnvelope = {
    code: 0
    msg: string
    data: KnowledgeIngestionPage
}

export type CreateKnowledgeArticleRequest = {
    key: string
    title: string
    summary?: string
    markdown: string
    source_ticket_id?: number
    source_attachment_ids?: Array<number>
}

export type CreateKnowledgeDraftRequest = {
    title: string
    markdown: string
    source_ticket_id?: number
    source_attachment_ids?: Array<number>
}

export type KnowledgeSource = {
    ordinal: number
    kind: "ticket" | "attachment"
    visibility: "full" | "restricted" | "unavailable"
    reference_label: string
    source_ticket_id?: number
    source_attachment_id?: number
    ticket_number?: string
    ticket_title?: string
    attachment_name?: string
    attachment_hash?: string
}

export type KnowledgeAuthoredResult = {
    article: KnowledgeArticle
    version: KnowledgeVersion
    sources: Array<KnowledgeSource>
    receipt: Receipt
}

export type KnowledgeAuthoredEnvelope = {
    code: 0
    msg: string
    data: KnowledgeAuthoredResult
}

export type KnowledgeDocumentSection = {
    ordinal: number
    heading: string
    level: number
    section_path?: string
    markdown: string
    content_hash: string
}

export type KnowledgeDocument = {
    article: KnowledgeArticle
    version: KnowledgeVersion
    markdown: string
    sections: Array<KnowledgeDocumentSection>
    sources: Array<KnowledgeSource>
}

export type KnowledgeDocumentEnvelope = {
    code: 0
    msg: string
    data: KnowledgeDocument
}

export type KnowledgeVersionEnvelope = {
    code: 0
    msg: string
    data: KnowledgeVersion
}

export type KnowledgeSearchRequest = {
    query: string
    limit?: number
}

export type KnowledgeCitation = {
    id: string
    search_id: string
    article_id: string
    article_key: string
    article_title: string
    version_id: string
    document_version: number
    chunk_id: string
    page_number?: number
    section_path: string
    snippet: string
    content_hash: string
    rank: number
    score: number
}

export type KnowledgeSearchResult = {
    search_id: string
    items: Array<KnowledgeCitation>
}

export type KnowledgeSearchEnvelope = {
    code: 0
    msg: string
    data: KnowledgeSearchResult
}

export type KnowledgeIndexStatus = "rebuild_requested" | "building" | "ready" | "failed"

export type KnowledgeIndexState = {
    id: string
    index_name: string
    generation: number
    desired_generation: number
    status: KnowledgeIndexStatus
    source_digest?: string
    document_count: number
    started_at?: string | null
    completed_at?: string | null
    updated_at: string
}

export type KnowledgeIndexStateEnvelope = {
    code: 0
    msg: string
    data: KnowledgeIndexState
}

export type IntegrationResourceID = string

export type IntegrationConnectorDirection = "inbound" | "outbound" | "bidirectional"

export type IntegrationConnectorDefinitionStatus = "active" | "disabled" | "archived"

export type IntegrationConnectionStatus = "active" | "inactive" | "error" | "archived"

export type IntegrationMappingVersionStatus = "draft" | "published" | "retired"

export type IntegrationInboxMessageStatus = "processing" | "completed" | "conflict" | "dead_letter"

export type IntegrationInboxReceiptStatus = "applied" | "noop"

export type IntegrationSyncDirection = "inbound" | "outbound"

export type IntegrationSyncRunStatus = "pending" | "running" | "succeeded" | "failed" | "conflict" | "cancelled"

export type IntegrationConflictType = "message_identity_reuse" | "external_link_mismatch" | "internal_link_collision"

export type IntegrationConflictStatus = "open" | "resolved" | "ignored"

export type IntegrationConflictResolution = "resolved" | "ignored"

export type IntegrationDeadLetterStatus = "open" | "requeued" | "resolved"

export type IntegrationOutboxDeliveryStatus = "pending" | "processing" | "succeeded" | "failed" | "dead" | "expired"

export type IntegrationConnectorDefinitionSummary = {
    id: IntegrationResourceID
    key: string
    name: string
    description: string
    kind: string
    direction: IntegrationConnectorDirection
    status: IntegrationConnectorDefinitionStatus
    signature_scheme: string
    default_replay_window_seconds: number
    has_configuration_schema: boolean
    has_mapping_schema: boolean
    created_at: string
    updated_at: string
}

export type IntegrationConnectorDefinition = {
    id: IntegrationResourceID
    key: string
    name: string
    description: string
    kind: string
    direction: IntegrationConnectorDirection
    status: IntegrationConnectorDefinitionStatus
    signature_scheme: string
    default_replay_window_seconds: number
    configuration_schema: unknown
    mapping_schema: unknown
    created_at: string
    updated_at: string
}

export type IntegrationConnectionSummary = {
    id: IntegrationResourceID
    key: string
    name: string
    description: string
    status: IntegrationConnectionStatus
    replay_window_seconds: number
    has_configuration: boolean
    has_verification_key: boolean
    last_verified_at?: string
    last_error_at?: string
    last_error_code?: string
    created_at: string
    updated_at: string
}

export type IntegrationMappingSummary = {
    id: IntegrationResourceID
    key: string
    version: number
    status: IntegrationMappingVersionStatus
    target_command: string
    definition_digest: string
    published_at?: string
    published_by?: string
    created_at: string
    updated_at: string
}

export type IntegrationMapping = {
    id: IntegrationResourceID
    connection_id: number
    key: string
    version: number
    status: IntegrationMappingVersionStatus
    source_schema: unknown
    target_command: string
    definition: unknown
    definition_digest: string
    published_at?: string
    published_by?: string
    created_at: string
    updated_at: string
}

export type IntegrationInboxMessageSummary = {
    id: IntegrationResourceID
    connection_id: number
    external_message_id: string
    external_resource_type: string
    external_resource_id: string
    signed_at: string
    received_at: string
    content_type: string
    payload_digest: string
    status: IntegrationInboxMessageStatus
    processed_at?: string
    created_at: string
    updated_at: string
}

export type IntegrationInboxReceiptSummary = {
    id: IntegrationResourceID
    status: IntegrationInboxReceiptStatus
    resource_type: string
    resource_id: string
    resource_version: number
    event_id?: string
    operation_id?: string
    actor_type: ActorType
    actor_id: string
    processed_at: string
    created_at: string
}

export type IntegrationSyncRunSummary = {
    id: IntegrationResourceID
    connection_id: number
    run_key: string
    direction: IntegrationSyncDirection
    status: IntegrationSyncRunStatus
    started_at?: string
    finished_at?: string
    processed_count: number
    succeeded_count: number
    failed_count: number
    conflict_count: number
    error_code?: string
}

export type IntegrationConflictSummary = {
    id: IntegrationResourceID
    connection_id: number
    type: IntegrationConflictType
    status: IntegrationConflictStatus
    external_resource_type: string
    external_resource_id: string
    existing_internal_resource_id?: string
    incoming_internal_resource_id?: string
    resolved_at?: string
    created_at: string
    updated_at: string
}

export type IntegrationDeadLetterSummary = {
    id: IntegrationResourceID
    connection_id: number
    status: IntegrationDeadLetterStatus
    reason_code: string
    attempt_count: number
    next_attempt_at?: string
    resolved_at?: string
    created_at: string
    updated_at: string
}

export type IntegrationDomainEventSummary = {
    id: IntegrationResourceID
    created_at: string
    type: string
    subject: string
    actor_type: ActorType
    actor_id: string
    resource_version: number
    time: string
}

export type IntegrationOutboxSummary = {
    id: IntegrationResourceID
    event_id: IntegrationResourceID
    destination_type: string
    destination_label: string
    status: IntegrationOutboxDeliveryStatus
    attempts: number
    max_attempts: number
    next_attempt_at: string
    last_error?: string
    delivered_at?: string
    expires_at: string | null
    expired_at: string | null
    created_at: string
    updated_at: string
}

export type IntegrationConnectionHealth = {
    id: IntegrationResourceID
    key: string
    name: string
    status: IntegrationConnectionStatus
    last_verified_at?: string
    last_error_at?: string
    last_error_code?: string
    last_run?: IntegrationSyncRunSummary
}

export type IntegrationOverview = {
    connector_definitions: number
    connections: number
    active_connections: number
    error_connections: number
    open_conflicts: number
    open_dead_letters: number
    running_sync_runs: number
    recent_runs: Array<IntegrationSyncRunSummary>
    recent_runs_limit: number
    recent_runs_truncated: boolean
    connection_health: Array<IntegrationConnectionHealth>
    connection_health_limit: number
    connection_health_truncated: boolean
}

export type IntegrationConnectorDefinitionPage = {
    items: Array<IntegrationConnectorDefinitionSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationConnectionPage = {
    items: Array<IntegrationConnectionSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationMappingPage = {
    items: Array<IntegrationMappingSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationInboxMessagePage = {
    items: Array<IntegrationInboxMessageSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationInboxReceiptPage = {
    items: Array<IntegrationInboxReceiptSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationSyncRunPage = {
    items: Array<IntegrationSyncRunSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationConflictPage = {
    items: Array<IntegrationConflictSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationDeadLetterPage = {
    items: Array<IntegrationDeadLetterSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationOutboxPage = {
    items: Array<IntegrationOutboxSummary>
    total: number
    page: number
    page_size: number
    total_pages: number
}

export type IntegrationDomainEventCursorPage = {
    items: Array<IntegrationDomainEventSummary>
    next_cursor: string
    has_more: boolean
}

export type IntegrationConnectorDefinitionPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationConnectorDefinitionPage
}

export type IntegrationConnectorDefinitionEnvelope = {
    code: 0
    msg: string
    data: IntegrationConnectorDefinition
}

export type IntegrationConnectionPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationConnectionPage
}

export type IntegrationConnectionEnvelope = {
    code: 0
    msg: string
    data: IntegrationConnectionSummary
}

export type IntegrationMappingPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationMappingPage
}

export type IntegrationMappingEnvelope = {
    code: 0
    msg: string
    data: IntegrationMapping
}

export type IntegrationInboxMessagePageEnvelope = {
    code: 0
    msg: string
    data: IntegrationInboxMessagePage
}

export type IntegrationInboxReceiptPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationInboxReceiptPage
}

export type IntegrationSyncRunPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationSyncRunPage
}

export type IntegrationConflictPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationConflictPage
}

export type IntegrationConflictEnvelope = {
    code: 0
    msg: string
    data: IntegrationConflictSummary
}

export type IntegrationDeadLetterPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationDeadLetterPage
}

export type IntegrationDomainEventCursorEnvelope = {
    code: 0
    msg: string
    data: IntegrationDomainEventCursorPage
}

export type IntegrationOutboxPageEnvelope = {
    code: 0
    msg: string
    data: IntegrationOutboxPage
}

export type IntegrationOverviewEnvelope = {
    code: 0
    msg: string
    data: IntegrationOverview
}

export type CreateIntegrationConnectorDefinitionRequest = {
    key: string
    name: string
    description?: string
    kind: string
    direction: IntegrationConnectorDirection
    status?: IntegrationConnectorDefinitionStatus
    signature_scheme: string
    default_replay_window_seconds?: number
    configuration_schema?: unknown
    mapping_schema?: unknown
}

export type UpdateIntegrationConnectorDefinitionRequest = {
    name: string
    description: string
    status: IntegrationConnectorDefinitionStatus
    signature_scheme: string
    default_replay_window_seconds: number
    configuration_schema: unknown
    mapping_schema: unknown
    expected_updated_at: string
}

export type CreateIntegrationConnectionRequest = {
    connector_definition_id: IntegrationResourceID
    key: string
    name: string
    description?: string
    status?: IntegrationConnectionStatus
    configuration?: unknown
    verification_key_ref?: string
    replay_window_seconds?: number
}

export type UpdateIntegrationConnectionRequest = {
    name: string
    description: string
    status: IntegrationConnectionStatus
    configuration: unknown
    verification_key_ref: string
    replay_window_seconds: number
    expected_updated_at: string
}

export type CreateIntegrationMappingRequest = {
    key: string
    source_schema?: unknown
    target_command: string
    definition: unknown
}

export type UpdateIntegrationMappingRequest = {
    source_schema?: unknown
    target_command: string
    definition: unknown
    expected_definition_digest: string
    expected_updated_at: string
}

export type PublishIntegrationMappingRequest = {
    expected_definition_digest: string
    expected_updated_at: string
}

export type DryRunIntegrationMappingRequest = {
    payload: unknown
}

export type IntegrationMappingDryRunResult = {
    mapping_version_id: number
    payload_digest: string
    target_command: string
    preview: unknown
    warnings?: Array<string>
}

export type IntegrationMappingDryRunEnvelope = {
    code: 0
    msg: string
    data: IntegrationMappingDryRunResult
}

export type ResolveIntegrationConflictRequest = {
    resolution: IntegrationConflictResolution
    expected_updated_at: string
}

export type ReplayIntegrationDeadLetterRequest = {
    expected_updated_at: string
}

export type IntegrationReplayReceipt = {
    id: IntegrationResourceID
    status: IntegrationInboxReceiptStatus
    resource_type: string
    resource_id: string
    resource_version: number
    event_id: string
    operation_id: string
}

export type IntegrationInboundReplayResult = {
    message_id?: IntegrationResourceID
    status?: IntegrationInboxMessageStatus
    receipt?: IntegrationReplayReceipt
    conflict?: IntegrationConflictSummary
    dead_letter?: IntegrationDeadLetterSummary
    replayed: boolean
}

export type IntegrationInboundReplayEnvelope = {
    code: 0
    msg: string
    data: IntegrationInboundReplayResult
}

export type RegisterHumanOperationPathParameters = Record<string, never>
export type RegisterHumanOperationQuery = Record<string, never>
export type RegisterHumanOperationRequest = RegisterHumanRequest
export type RegisterHumanOperationResponse = HumanRegistrationEnvelope

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

export type VerifyHumanEmailOperationPathParameters = Record<string, never>
export type VerifyHumanEmailOperationQuery = Record<string, never>
export type VerifyHumanEmailOperationRequest = VerifyHumanEmailRequest
export type VerifyHumanEmailOperationResponse = AuthMessageSuccessEnvelope

export type ResendHumanEmailVerificationOperationPathParameters = Record<string, never>
export type ResendHumanEmailVerificationOperationQuery = Record<string, never>
export type ResendHumanEmailVerificationOperationRequest = ResendHumanEmailVerificationRequest
export type ResendHumanEmailVerificationOperationResponse = AuthMessageSuccessEnvelope

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

export type RegenerateOTPBackupCodesOperationPathParameters = Record<string, never>
export type RegenerateOTPBackupCodesOperationQuery = Record<string, never>
export type RegenerateOTPBackupCodesOperationRequest = RegenerateOTPBackupCodesRequest
export type RegenerateOTPBackupCodesOperationResponse = OTPBackupCodeRegenerationEnvelope

export type ListTrustedDevicesOperationPathParameters = Record<string, never>
export type ListTrustedDevicesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "last_used_at" | "expires_at" | "revoked" | "device_name"
    sort_order?: "asc" | "desc"
}
export type ListTrustedDevicesOperationRequest = never
export type ListTrustedDevicesOperationResponse = TrustedDevicePageEnvelope

export type RevokeTrustedDeviceOperationPathParameters = {
    deviceID: number
}
export type RevokeTrustedDeviceOperationQuery = Record<string, never>
export type RevokeTrustedDeviceOperationRequest = never
export type RevokeTrustedDeviceOperationResponse = EmptySuccessEnvelope

export type ListAuthorizedHumanProjectsOperationPathParameters = Record<string, never>
export type ListAuthorizedHumanProjectsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "name" | "key" | "created_at"
    sort_order?: "asc" | "desc"
    search?: string
}
export type ListAuthorizedHumanProjectsOperationRequest = never
export type ListAuthorizedHumanProjectsOperationResponse = AuthorizedProjectPageEnvelope

export type GetAuthorizedProjectContextOperationPathParameters = {
    projectKey: string
}
export type GetAuthorizedProjectContextOperationQuery = Record<string, never>
export type GetAuthorizedProjectContextOperationRequest = never
export type GetAuthorizedProjectContextOperationResponse = SuccessEnvelope & {
    data: AuthorizedProjectAccess
}

export type ListProjectQueuesOperationPathParameters = {
    projectKey: string
}
export type ListProjectQueuesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "name" | "key" | "is_default"
    sort_order?: "asc" | "desc"
}
export type ListProjectQueuesOperationRequest = never
export type ListProjectQueuesOperationResponse = ProjectQueuePageEnvelope

export type ListProjectMembershipsOperationPathParameters = {
    projectKey: string
}
export type ListProjectMembershipsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "role" | "is_active" | "user_id"
    sort_order?: "asc" | "desc"
}
export type ListProjectMembershipsOperationRequest = never
export type ListProjectMembershipsOperationResponse = ProjectMembershipPageEnvelope

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
export type DeactivateProjectMembershipOperationQuery = {
    expected_version: number
}
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

export type CreatePlatformAuditExportOperationPathParameters = Record<string, never>
export type CreatePlatformAuditExportOperationQuery = {
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
    start_time: string
    end_time: string
}
export type CreatePlatformAuditExportOperationRequest = never
export type CreatePlatformAuditExportOperationResponse = AdminAuditExportEnvelope

export type GetPlatformAuditExportOperationPathParameters = {
    auditExportPublicID: string
}
export type GetPlatformAuditExportOperationQuery = Record<string, never>
export type GetPlatformAuditExportOperationRequest = never
export type GetPlatformAuditExportOperationResponse = AdminAuditExportEnvelope

export type DownloadPlatformAuditExportOperationPathParameters = {
    auditExportPublicID: string
}
export type DownloadPlatformAuditExportOperationQuery = Record<string, never>
export type DownloadPlatformAuditExportOperationRequest = never
export type DownloadPlatformAuditExportOperationResponse = string

export type GetPlatformEmergencyControlsOperationPathParameters = Record<string, never>
export type GetPlatformEmergencyControlsOperationQuery = Record<string, never>
export type GetPlatformEmergencyControlsOperationRequest = never
export type GetPlatformEmergencyControlsOperationResponse = EmergencyControlEnvelope

export type UpdatePlatformEmergencyControlsOperationPathParameters = Record<string, never>
export type UpdatePlatformEmergencyControlsOperationQuery = Record<string, never>
export type UpdatePlatformEmergencyControlsOperationRequest = UpdateEmergencyControlsRequest
export type UpdatePlatformEmergencyControlsOperationResponse = EmergencyControlEnvelope

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
    sort_by?: "id" | "ticket_number" | "title" | "status" | "priority" | "due_date" | "created_at" | "updated_at"
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

export type GetProjectTicketAllowedTransitionsOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type GetProjectTicketAllowedTransitionsOperationQuery = Record<string, never>
export type GetProjectTicketAllowedTransitionsOperationRequest = never
export type GetProjectTicketAllowedTransitionsOperationResponse = TicketAllowedTransitionsEnvelope

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
    rule_type?: "assignment" | "classification" | "escalation" | "sla"
    trigger_event?: string
    is_active?: boolean
    search?: string
    page?: number
    page_size?: number
    sort?: "[\"priority\",\"ASC\"]"
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
export type UpdateProjectAutomationRuleOperationResponse = AutomationRuleEnvelope

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
    cursor?: string
    limit?: number
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

export type ListPlatformCleanupLogsOperationPathParameters = Record<string, never>
export type ListPlatformCleanupLogsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "start_time" | "end_time" | "status" | "task_type" | "records_deleted"
    sort_order?: "asc" | "desc"
    task_type?: string
}
export type ListPlatformCleanupLogsOperationRequest = never
export type ListPlatformCleanupLogsOperationResponse = CleanupLogPageEnvelope

export type ListPlatformConfigsOperationPathParameters = Record<string, never>
export type ListPlatformConfigsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "key" | "category" | "group"
    sort_order?: "asc" | "desc"
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
    cursor?: string
    limit?: number
    status?: "pending" | "success" | "failed"
    event_type?: WebhookEventType
}
export type ListProjectWebhookLogsOperationRequest = never
export type ListProjectWebhookLogsOperationResponse = WebhookLogPageEnvelope

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

export type ListAgentServicePrincipalsOperationPathParameters = {
    projectKey: string
}
export type ListAgentServicePrincipalsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "desc"
}
export type ListAgentServicePrincipalsOperationRequest = never
export type ListAgentServicePrincipalsOperationResponse = AdminPrincipalPageEnvelope

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
export type ListServicePrincipalPoliciesV2OperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "priority"
    sort_order?: "desc"
}
export type ListServicePrincipalPoliciesV2OperationRequest = never
export type ListServicePrincipalPoliciesV2OperationResponse = AdminPolicyPageEnvelope

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

export type ListAgentTicketLeasesOperationPathParameters = {
    projectKey: string
}
export type ListAgentTicketLeasesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "expires_at"
    sort_order?: "asc"
}
export type ListAgentTicketLeasesOperationRequest = never
export type ListAgentTicketLeasesOperationResponse = AdminLeasePageEnvelope

export type ForceReleaseTicketLeaseV2OperationPathParameters = {
    projectKey: string
    leaseId: string
}
export type ForceReleaseTicketLeaseV2OperationQuery = Record<string, never>
export type ForceReleaseTicketLeaseV2OperationRequest = never
export type ForceReleaseTicketLeaseV2OperationResponse = AdminTicketLeaseEnvelope

export type ListAgentAttachmentScansOperationPathParameters = {
    projectKey: string
}
export type ListAgentAttachmentScansOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "desc"
}
export type ListAgentAttachmentScansOperationRequest = never
export type ListAgentAttachmentScansOperationResponse = AdminAttachmentPageEnvelope

export type RecordAttachmentVirusScanV2OperationPathParameters = {
    projectKey: string
    attachmentId: number
}
export type RecordAttachmentVirusScanV2OperationQuery = Record<string, never>
export type RecordAttachmentVirusScanV2OperationRequest = AttachmentScanUpdate
export type RecordAttachmentVirusScanV2OperationResponse = AttachmentScanEnvelope

export type ListAgentOutboxDeliveriesOperationPathParameters = {
    projectKey: string
}
export type ListAgentOutboxDeliveriesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "desc"
}
export type ListAgentOutboxDeliveriesOperationRequest = never
export type ListAgentOutboxDeliveriesOperationResponse = AdminOutboxPageEnvelope

export type ReplayOutboxDeliveryV2OperationPathParameters = {
    projectKey: string
    deliveryId: string
}
export type ReplayOutboxDeliveryV2OperationQuery = Record<string, never>
export type ReplayOutboxDeliveryV2OperationRequest = never
export type ReplayOutboxDeliveryV2OperationResponse = ReplayEnvelope

export type ListProjectWebhookEmergencyTombstonesOperationPathParameters = {
    projectKey: string
}
export type ListProjectWebhookEmergencyTombstonesOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectWebhookEmergencyTombstonesOperationRequest = never
export type ListProjectWebhookEmergencyTombstonesOperationResponse = WebhookEmergencyTombstonePageEnvelope

export type GetProjectWebhookEmergencyRevokePreflightOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type GetProjectWebhookEmergencyRevokePreflightOperationQuery = Record<string, never>
export type GetProjectWebhookEmergencyRevokePreflightOperationRequest = never
export type GetProjectWebhookEmergencyRevokePreflightOperationResponse = WebhookEmergencyRevokePreflightEnvelope

export type EmergencyRevokeProjectWebhookOperationPathParameters = {
    projectKey: string
    webhookID: number
}
export type EmergencyRevokeProjectWebhookOperationQuery = Record<string, never>
export type EmergencyRevokeProjectWebhookOperationRequest = never
export type EmergencyRevokeProjectWebhookOperationResponse = WebhookEmergencyRevokeEnvelope

export type ListAgentDomainEventsOperationPathParameters = {
    projectKey: string
}
export type ListAgentDomainEventsOperationQuery = {
    cursor?: string
    limit?: number
}
export type ListAgentDomainEventsOperationRequest = never
export type ListAgentDomainEventsOperationResponse = AdminDomainEventCursorEnvelope

export type ListAgentPolicyDecisionsOperationPathParameters = {
    projectKey: string
}
export type ListAgentPolicyDecisionsOperationQuery = {
    cursor?: string
    limit?: number
}
export type ListAgentPolicyDecisionsOperationRequest = never
export type ListAgentPolicyDecisionsOperationResponse = AdminPolicyDecisionCursorEnvelope

export type ListLoginHistoryOperationPathParameters = Record<string, never>
export type ListLoginHistoryOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "id" | "login_time" | "created_at" | "updated_at" | "ip_address" | "device_type" | "login_method" | "login_status" | "is_active"
    sort_order?: "asc" | "desc"
    status?: LoginStatus
    start_date?: string
    end_date?: string
    ip_address?: string
    device_type?: string
    login_method?: LoginMethod
    session_id?: string
    is_active?: boolean
}
export type ListLoginHistoryOperationRequest = never
export type ListLoginHistoryOperationResponse = LoginHistoryPageEnvelope

export type DeleteLoginHistorySessionOperationPathParameters = {
    loginHistoryID: number
}
export type DeleteLoginHistorySessionOperationQuery = Record<string, never>
export type DeleteLoginHistorySessionOperationRequest = never
export type DeleteLoginHistorySessionOperationResponse = EmptySuccessEnvelope

export type ListProjectCategoriesOperationPathParameters = {
    projectKey: string
}
export type ListProjectCategoriesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "id" | "name" | "slug" | "sort_order" | "status" | "type"
    sort_order?: "asc" | "desc"
    filter?: string
    search?: string
    sort?: string
    status?: CategoryStatus
}
export type ListProjectCategoriesOperationRequest = never
export type ListProjectCategoriesOperationResponse = ProjectCategoryPageEnvelope

export type GetProjectCategoryOperationPathParameters = {
    projectKey: string
    categoryID: number
}
export type GetProjectCategoryOperationQuery = Record<string, never>
export type GetProjectCategoryOperationRequest = never
export type GetProjectCategoryOperationResponse = ProjectCategoryEnvelope

export type ListProjectAssigneesOperationPathParameters = {
    projectKey: string
}
export type ListProjectAssigneesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "id" | "username" | "first_name" | "last_name" | "display_name" | "role"
    sort_order?: "asc" | "desc"
    filter?: string
    search?: string
    sort?: string
}
export type ListProjectAssigneesOperationRequest = never
export type ListProjectAssigneesOperationResponse = ProjectAssigneePageEnvelope

export type GetProjectAssigneeOperationPathParameters = {
    projectKey: string
    assigneeID: number
}
export type GetProjectAssigneeOperationQuery = Record<string, never>
export type GetProjectAssigneeOperationRequest = never
export type GetProjectAssigneeOperationResponse = ProjectAssigneeEnvelope

export type ListProjectTicketEntityLinksOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type ListProjectTicketEntityLinksOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "asc" | "desc"
}
export type ListProjectTicketEntityLinksOperationRequest = never
export type ListProjectTicketEntityLinksOperationResponse = TicketEntityLinkPageEnvelope

export type CreateProjectTicketEntityLinkOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type CreateProjectTicketEntityLinkOperationQuery = Record<string, never>
export type CreateProjectTicketEntityLinkOperationRequest = AddTicketEntityLinkRequest
export type CreateProjectTicketEntityLinkOperationResponse = AddTicketEntityLinkEnvelope

export type ListProjectTicketRelationsOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type ListProjectTicketRelationsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "asc" | "desc"
}
export type ListProjectTicketRelationsOperationRequest = never
export type ListProjectTicketRelationsOperationResponse = TicketRelationPageEnvelope

export type CreateProjectTicketRelationOperationPathParameters = {
    projectKey: string
    ticketID: number
}
export type CreateProjectTicketRelationOperationQuery = Record<string, never>
export type CreateProjectTicketRelationOperationRequest = AddTicketRelationRequest
export type CreateProjectTicketRelationOperationResponse = AddTicketRelationEnvelope

export type ListProjectAgentRunsOperationPathParameters = {
    projectKey: string
}
export type ListProjectAgentRunsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectAgentRunsOperationRequest = never
export type ListProjectAgentRunsOperationResponse = AgentRunPageEnvelope

export type GetProjectAgentRunOperationPathParameters = {
    projectKey: string
    runID: string
}
export type GetProjectAgentRunOperationQuery = Record<string, never>
export type GetProjectAgentRunOperationRequest = never
export type GetProjectAgentRunOperationResponse = AgentRunDetailEnvelope

export type ListProjectActionProposalsOperationPathParameters = {
    projectKey: string
}
export type ListProjectActionProposalsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectActionProposalsOperationRequest = never
export type ListProjectActionProposalsOperationResponse = ActionProposalPageEnvelope

export type GetProjectActionProposalOperationPathParameters = {
    projectKey: string
    proposalID: string
}
export type GetProjectActionProposalOperationQuery = Record<string, never>
export type GetProjectActionProposalOperationRequest = never
export type GetProjectActionProposalOperationResponse = ActionProposalDetailEnvelope

export type ListProjectApprovalTasksOperationPathParameters = {
    projectKey: string
}
export type ListProjectApprovalTasksOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectApprovalTasksOperationRequest = never
export type ListProjectApprovalTasksOperationResponse = ApprovalTaskPageEnvelope

export type GetProjectApprovalTaskOperationPathParameters = {
    projectKey: string
    approvalID: string
}
export type GetProjectApprovalTaskOperationQuery = Record<string, never>
export type GetProjectApprovalTaskOperationRequest = never
export type GetProjectApprovalTaskOperationResponse = ApprovalTaskDetailEnvelope

export type ListProjectHandoffsOperationPathParameters = {
    projectKey: string
}
export type ListProjectHandoffsOperationQuery = {
    page?: number
    page_size?: number
}
export type ListProjectHandoffsOperationRequest = never
export type ListProjectHandoffsOperationResponse = HandoffPageEnvelope

export type GetProjectHandoffOperationPathParameters = {
    projectKey: string
    handoffID: string
}
export type GetProjectHandoffOperationQuery = Record<string, never>
export type GetProjectHandoffOperationRequest = never
export type GetProjectHandoffOperationResponse = HandoffDetailEnvelope

export type DecideProjectAgentApprovalOperationPathParameters = {
    projectKey: string
    approvalID: string
}
export type DecideProjectAgentApprovalOperationQuery = Record<string, never>
export type DecideProjectAgentApprovalOperationRequest = ApprovalDecisionRequest
export type DecideProjectAgentApprovalOperationResponse = ApprovalTaskDetailEnvelope

export type TakeOverProjectAgentRunOperationPathParameters = {
    projectKey: string
    runID: string
}
export type TakeOverProjectAgentRunOperationQuery = Record<string, never>
export type TakeOverProjectAgentRunOperationRequest = AgentRunTakeoverRequest
export type TakeOverProjectAgentRunOperationResponse = HandoffDetailEnvelope

export type GetProjectIntakeConfigurationOperationPathParameters = {
    projectKey: string
}
export type GetProjectIntakeConfigurationOperationQuery = Record<string, never>
export type GetProjectIntakeConfigurationOperationRequest = never
export type GetProjectIntakeConfigurationOperationResponse = ProjectIntakeConfigurationEnvelope

export type ListProjectSLAConfigsOperationPathParameters = {
    projectKey: string
}
export type ListProjectSLAConfigsOperationQuery = {
    page?: number
    page_size?: number
    is_active?: boolean
}
export type ListProjectSLAConfigsOperationRequest = never
export type ListProjectSLAConfigsOperationResponse = SLAConfigPageEnvelope

export type CreateProjectSLAConfigOperationPathParameters = {
    projectKey: string
}
export type CreateProjectSLAConfigOperationQuery = Record<string, never>
export type CreateProjectSLAConfigOperationRequest = SLAConfigRequest
export type CreateProjectSLAConfigOperationResponse = SLAConfigEnvelope

export type ListProjectTicketTemplatesOperationPathParameters = {
    projectKey: string
}
export type ListProjectTicketTemplatesOperationQuery = {
    page?: number
    page_size?: number
    category?: string
    is_active?: boolean
}
export type ListProjectTicketTemplatesOperationRequest = never
export type ListProjectTicketTemplatesOperationResponse = TicketTemplatePageEnvelope

export type CreateProjectTicketTemplateOperationPathParameters = {
    projectKey: string
}
export type CreateProjectTicketTemplateOperationQuery = Record<string, never>
export type CreateProjectTicketTemplateOperationRequest = TicketTemplateRequest
export type CreateProjectTicketTemplateOperationResponse = TicketTemplateEnvelope

export type GetProjectTicketTemplateOperationPathParameters = {
    projectKey: string
    automationConfigID: number
}
export type GetProjectTicketTemplateOperationQuery = Record<string, never>
export type GetProjectTicketTemplateOperationRequest = never
export type GetProjectTicketTemplateOperationResponse = TicketTemplateEnvelope

export type ListProjectQuickRepliesOperationPathParameters = {
    projectKey: string
}
export type ListProjectQuickRepliesOperationQuery = {
    page?: number
    page_size?: number
    category?: string
    keyword?: string
    is_public?: boolean
}
export type ListProjectQuickRepliesOperationRequest = never
export type ListProjectQuickRepliesOperationResponse = QuickReplyPageEnvelope

export type CreateProjectQuickReplyOperationPathParameters = {
    projectKey: string
}
export type CreateProjectQuickReplyOperationQuery = Record<string, never>
export type CreateProjectQuickReplyOperationRequest = QuickReplyRequest
export type CreateProjectQuickReplyOperationResponse = QuickReplyEnvelope

export type UseProjectQuickReplyOperationPathParameters = {
    projectKey: string
    automationConfigID: number
}
export type UseProjectQuickReplyOperationQuery = Record<string, never>
export type UseProjectQuickReplyOperationRequest = never
export type UseProjectQuickReplyOperationResponse = LegacyMessageSuccessEnvelope

export type ListProjectKnowledgeArticlesOperationPathParameters = {
    projectKey: string
}
export type ListProjectKnowledgeArticlesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "key" | "title" | "status"
    sort_order?: "asc" | "desc"
    status?: KnowledgeArticleStatus
    q?: string
    view?: "manage" | "mine"
}
export type ListProjectKnowledgeArticlesOperationRequest = never
export type ListProjectKnowledgeArticlesOperationResponse = KnowledgeArticlePageEnvelope

export type CreateProjectKnowledgeArticleOperationPathParameters = {
    projectKey: string
}
export type CreateProjectKnowledgeArticleOperationQuery = Record<string, never>
export type CreateProjectKnowledgeArticleOperationRequest = CreateKnowledgeArticleRequest
export type CreateProjectKnowledgeArticleOperationResponse = KnowledgeAuthoredEnvelope

export type CreateProjectKnowledgeArticleDraftOperationPathParameters = {
    projectKey: string
    articleID: string
}
export type CreateProjectKnowledgeArticleDraftOperationQuery = Record<string, never>
export type CreateProjectKnowledgeArticleDraftOperationRequest = CreateKnowledgeDraftRequest
export type CreateProjectKnowledgeArticleDraftOperationResponse = KnowledgeAuthoredEnvelope

export type GetProjectKnowledgeArticleDocumentOperationPathParameters = {
    projectKey: string
    articleID: string
}
export type GetProjectKnowledgeArticleDocumentOperationQuery = {
    version_id?: string
    prefer_latest_draft?: boolean
}
export type GetProjectKnowledgeArticleDocumentOperationRequest = never
export type GetProjectKnowledgeArticleDocumentOperationResponse = KnowledgeDocumentEnvelope

export type ListProjectKnowledgeVersionsOperationPathParameters = {
    projectKey: string
    articleID: string
}
export type ListProjectKnowledgeVersionsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "version" | "status"
    sort_order?: "asc" | "desc"
    status?: KnowledgeVersionStatus
    virus_scan?: VirusScanStatus
}
export type ListProjectKnowledgeVersionsOperationRequest = never
export type ListProjectKnowledgeVersionsOperationResponse = KnowledgeVersionPageEnvelope

export type PublishProjectKnowledgeVersionOperationPathParameters = {
    projectKey: string
    versionID: string
}
export type PublishProjectKnowledgeVersionOperationQuery = Record<string, never>
export type PublishProjectKnowledgeVersionOperationRequest = never
export type PublishProjectKnowledgeVersionOperationResponse = KnowledgeVersionEnvelope

export type SearchProjectKnowledgeOperationPathParameters = {
    projectKey: string
}
export type SearchProjectKnowledgeOperationQuery = Record<string, never>
export type SearchProjectKnowledgeOperationRequest = KnowledgeSearchRequest
export type SearchProjectKnowledgeOperationResponse = KnowledgeSearchEnvelope

export type ListProjectKnowledgeIngestionsOperationPathParameters = {
    projectKey: string
}
export type ListProjectKnowledgeIngestionsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "attempt" | "status"
    sort_order?: "asc" | "desc"
    status?: KnowledgeIngestionStatus
    version_id?: string
}
export type ListProjectKnowledgeIngestionsOperationRequest = never
export type ListProjectKnowledgeIngestionsOperationResponse = KnowledgeIngestionPageEnvelope

export type GetProjectKnowledgeIndexStateOperationPathParameters = {
    projectKey: string
}
export type GetProjectKnowledgeIndexStateOperationQuery = Record<string, never>
export type GetProjectKnowledgeIndexStateOperationRequest = never
export type GetProjectKnowledgeIndexStateOperationResponse = KnowledgeIndexStateEnvelope

export type RebuildProjectKnowledgeIndexOperationPathParameters = {
    projectKey: string
}
export type RebuildProjectKnowledgeIndexOperationQuery = Record<string, never>
export type RebuildProjectKnowledgeIndexOperationRequest = never
export type RebuildProjectKnowledgeIndexOperationResponse = KnowledgeIndexStateEnvelope

export type ListMyProjectTicketsOperationPathParameters = {
    projectKey: string
}
export type ListMyProjectTicketsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "asc" | "desc"
    status?: TicketStatus
    priority?: TicketPriority
}
export type ListMyProjectTicketsOperationRequest = never
export type ListMyProjectTicketsOperationResponse = TicketListPageEnvelope

export type ListUnassignedProjectTicketsOperationPathParameters = {
    projectKey: string
}
export type ListUnassignedProjectTicketsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at"
    sort_order?: "asc" | "desc"
    priority?: TicketPriority
    category_id?: number
}
export type ListUnassignedProjectTicketsOperationRequest = never
export type ListUnassignedProjectTicketsOperationResponse = TicketListPageEnvelope

export type ListProjectIntegrationConnectorDefinitionsOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationConnectorDefinitionsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "name" | "status" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationConnectorDefinitionStatus
}
export type ListProjectIntegrationConnectorDefinitionsOperationRequest = never
export type ListProjectIntegrationConnectorDefinitionsOperationResponse = IntegrationConnectorDefinitionPageEnvelope

export type CreateProjectIntegrationConnectorDefinitionOperationPathParameters = {
    projectKey: string
}
export type CreateProjectIntegrationConnectorDefinitionOperationQuery = Record<string, never>
export type CreateProjectIntegrationConnectorDefinitionOperationRequest = CreateIntegrationConnectorDefinitionRequest
export type CreateProjectIntegrationConnectorDefinitionOperationResponse = IntegrationConnectorDefinitionEnvelope

export type UpdateProjectIntegrationConnectorDefinitionOperationPathParameters = {
    projectKey: string
    definitionID: IntegrationResourceID
}
export type UpdateProjectIntegrationConnectorDefinitionOperationQuery = Record<string, never>
export type UpdateProjectIntegrationConnectorDefinitionOperationRequest = UpdateIntegrationConnectorDefinitionRequest
export type UpdateProjectIntegrationConnectorDefinitionOperationResponse = IntegrationConnectorDefinitionEnvelope

export type ListProjectIntegrationConnectionsOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationConnectionsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "name" | "status" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationConnectionStatus
}
export type ListProjectIntegrationConnectionsOperationRequest = never
export type ListProjectIntegrationConnectionsOperationResponse = IntegrationConnectionPageEnvelope

export type CreateProjectIntegrationConnectionOperationPathParameters = {
    projectKey: string
}
export type CreateProjectIntegrationConnectionOperationQuery = Record<string, never>
export type CreateProjectIntegrationConnectionOperationRequest = CreateIntegrationConnectionRequest
export type CreateProjectIntegrationConnectionOperationResponse = IntegrationConnectionEnvelope

export type UpdateProjectIntegrationConnectionOperationPathParameters = {
    projectKey: string
    connectionID: IntegrationResourceID
}
export type UpdateProjectIntegrationConnectionOperationQuery = Record<string, never>
export type UpdateProjectIntegrationConnectionOperationRequest = UpdateIntegrationConnectionRequest
export type UpdateProjectIntegrationConnectionOperationResponse = IntegrationConnectionEnvelope

export type ListProjectIntegrationMappingsOperationPathParameters = {
    projectKey: string
    connectionID: IntegrationResourceID
}
export type ListProjectIntegrationMappingsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "key" | "version" | "status" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationMappingVersionStatus
}
export type ListProjectIntegrationMappingsOperationRequest = never
export type ListProjectIntegrationMappingsOperationResponse = IntegrationMappingPageEnvelope

export type CreateProjectIntegrationMappingOperationPathParameters = {
    projectKey: string
    connectionID: IntegrationResourceID
}
export type CreateProjectIntegrationMappingOperationQuery = Record<string, never>
export type CreateProjectIntegrationMappingOperationRequest = CreateIntegrationMappingRequest
export type CreateProjectIntegrationMappingOperationResponse = IntegrationMappingEnvelope

export type UpdateProjectIntegrationMappingOperationPathParameters = {
    projectKey: string
    mappingID: IntegrationResourceID
}
export type UpdateProjectIntegrationMappingOperationQuery = Record<string, never>
export type UpdateProjectIntegrationMappingOperationRequest = UpdateIntegrationMappingRequest
export type UpdateProjectIntegrationMappingOperationResponse = IntegrationMappingEnvelope

export type DryRunProjectIntegrationMappingOperationPathParameters = {
    projectKey: string
    mappingID: IntegrationResourceID
}
export type DryRunProjectIntegrationMappingOperationQuery = Record<string, never>
export type DryRunProjectIntegrationMappingOperationRequest = DryRunIntegrationMappingRequest
export type DryRunProjectIntegrationMappingOperationResponse = IntegrationMappingDryRunEnvelope

export type PublishProjectIntegrationMappingOperationPathParameters = {
    projectKey: string
    mappingID: IntegrationResourceID
}
export type PublishProjectIntegrationMappingOperationQuery = Record<string, never>
export type PublishProjectIntegrationMappingOperationRequest = PublishIntegrationMappingRequest
export type PublishProjectIntegrationMappingOperationResponse = IntegrationMappingEnvelope

export type GetProjectIntegrationOverviewOperationPathParameters = {
    projectKey: string
}
export type GetProjectIntegrationOverviewOperationQuery = Record<string, never>
export type GetProjectIntegrationOverviewOperationRequest = never
export type GetProjectIntegrationOverviewOperationResponse = IntegrationOverviewEnvelope

export type ListProjectIntegrationInboxMessagesOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationInboxMessagesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "received_at" | "processed_at" | "status" | "created_at" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationInboxMessageStatus
    connection_id?: IntegrationResourceID
}
export type ListProjectIntegrationInboxMessagesOperationRequest = never
export type ListProjectIntegrationInboxMessagesOperationResponse = IntegrationInboxMessagePageEnvelope

export type ListProjectIntegrationInboxReceiptsOperationPathParameters = {
    projectKey: string
    messageID: IntegrationResourceID
}
export type ListProjectIntegrationInboxReceiptsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "processed_at" | "status" | "id"
    sort_order?: "asc" | "desc"
    status?: IntegrationInboxReceiptStatus
}
export type ListProjectIntegrationInboxReceiptsOperationRequest = never
export type ListProjectIntegrationInboxReceiptsOperationResponse = IntegrationInboxReceiptPageEnvelope

export type ListProjectIntegrationSyncRunsOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationSyncRunsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "started_at" | "finished_at" | "status" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationSyncRunStatus
    direction?: IntegrationSyncDirection
    connection_id?: IntegrationResourceID
}
export type ListProjectIntegrationSyncRunsOperationRequest = never
export type ListProjectIntegrationSyncRunsOperationResponse = IntegrationSyncRunPageEnvelope

export type ListProjectIntegrationConflictsOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationConflictsOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "status" | "type" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationConflictStatus
    type?: IntegrationConflictType
}
export type ListProjectIntegrationConflictsOperationRequest = never
export type ListProjectIntegrationConflictsOperationResponse = IntegrationConflictPageEnvelope

export type ResolveProjectIntegrationConflictOperationPathParameters = {
    projectKey: string
    conflictID: IntegrationResourceID
}
export type ResolveProjectIntegrationConflictOperationQuery = Record<string, never>
export type ResolveProjectIntegrationConflictOperationRequest = ResolveIntegrationConflictRequest
export type ResolveProjectIntegrationConflictOperationResponse = IntegrationConflictEnvelope

export type ListProjectIntegrationDeadLettersOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationDeadLettersOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "status" | "attempt_count" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationDeadLetterStatus
}
export type ListProjectIntegrationDeadLettersOperationRequest = never
export type ListProjectIntegrationDeadLettersOperationResponse = IntegrationDeadLetterPageEnvelope

export type ReplayProjectIntegrationDeadLetterOperationPathParameters = {
    projectKey: string
    deadLetterID: IntegrationResourceID
}
export type ReplayProjectIntegrationDeadLetterOperationQuery = Record<string, never>
export type ReplayProjectIntegrationDeadLetterOperationRequest = ReplayIntegrationDeadLetterRequest
export type ReplayProjectIntegrationDeadLetterOperationResponse = IntegrationInboundReplayEnvelope

export type ListProjectIntegrationDomainEventsOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationDomainEventsOperationQuery = {
    cursor?: string
    limit?: number
    event_type?: string
    search?: string
}
export type ListProjectIntegrationDomainEventsOperationRequest = never
export type ListProjectIntegrationDomainEventsOperationResponse = IntegrationDomainEventCursorEnvelope

export type ListProjectIntegrationOutboxDeliveriesOperationPathParameters = {
    projectKey: string
}
export type ListProjectIntegrationOutboxDeliveriesOperationQuery = {
    page?: number
    page_size?: number
    sort_by?: "created_at" | "updated_at" | "status" | "next_attempt_at" | "id"
    sort_order?: "asc" | "desc"
    search?: string
    status?: IntegrationOutboxDeliveryStatus
    destination_type?: string
}
export type ListProjectIntegrationOutboxDeliveriesOperationRequest = never
export type ListProjectIntegrationOutboxDeliveriesOperationResponse = IntegrationOutboxPageEnvelope

export const humanApiOperations = {
    registerHuman: {
        method: "POST",
        path: "/auth/register",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    createHumanSession: {
        method: "POST",
        path: "/auth/login",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    refreshHumanSession: {
        method: "POST",
        path: "/auth/refresh",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    requestHumanPasswordReset: {
        method: "POST",
        path: "/auth/forgot-password",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    resetHumanPassword: {
        method: "POST",
        path: "/auth/reset-password",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    verifyHumanEmail: {
        method: "POST",
        path: "/auth/verify-email",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    resendHumanEmailVerification: {
        method: "POST",
        path: "/auth/resend-verification",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    deleteHumanSession: {
        method: "POST",
        path: "/auth/logout",
        successStatus: 200,
        requestBody: "optional",
        listStrategy: null,
    },
    deleteAllHumanSessions: {
        method: "POST",
        path: "/auth/logout-all",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    getHumanSessionUser: {
        method: "GET",
        path: "/auth/me",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    updateHumanProfile: {
        method: "PUT",
        path: "/auth/profile",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    regenerateOTPBackupCodes: {
        method: "POST",
        path: "/auth/otp/backup-codes",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    listTrustedDevices: {
        method: "GET",
        path: "/user/trusted-devices",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    revokeTrustedDevice: {
        method: "DELETE",
        path: "/user/trusted-devices/{deviceID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listAuthorizedHumanProjects: {
        method: "GET",
        path: "/projects",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getAuthorizedProjectContext: {
        method: "GET",
        path: "/projects/{projectKey}/context",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectQueues: {
        method: "GET",
        path: "/projects/{projectKey}/queues",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectMemberships: {
        method: "GET",
        path: "/projects/{projectKey}/memberships",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    upsertProjectMembership: {
        method: "POST",
        path: "/projects/{projectKey}/memberships",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    searchProjectMembershipCandidates: {
        method: "GET",
        path: "/projects/{projectKey}/membership-candidates",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    deactivateProjectMembership: {
        method: "DELETE",
        path: "/projects/{projectKey}/memberships/{userID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listPlatformProjects: {
        method: "GET",
        path: "/platform/projects",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createPlatformProject: {
        method: "POST",
        path: "/platform/projects",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    getPlatformProjectCreationContext: {
        method: "GET",
        path: "/platform/project-creation-context",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listPlatformProjectBusinessUnits: {
        method: "GET",
        path: "/platform/project-business-units",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    archivePlatformProject: {
        method: "POST",
        path: "/platform/projects/{projectPublicID}/archive",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listPlatformUsers: {
        method: "GET",
        path: "/platform/users",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createPlatformUser: {
        method: "POST",
        path: "/platform/users",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    getPlatformUserStats: {
        method: "GET",
        path: "/platform/users/stats",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    getPlatformUser: {
        method: "GET",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    updatePlatformUser: {
        method: "PUT",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    deletePlatformUser: {
        method: "DELETE",
        path: "/platform/users/{userID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    resetPlatformUserPassword: {
        method: "POST",
        path: "/platform/users/{userID}/reset-password",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listPlatformAuditLogs: {
        method: "GET",
        path: "/platform/audit-logs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    getPlatformAuditLogDetail: {
        method: "GET",
        path: "/platform/audit-logs/{auditLogID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    createPlatformAuditExport: {
        method: "POST",
        path: "/platform/audit-exports",
        successStatus: 202,
        requestBody: "none",
        listStrategy: null,
    },
    getPlatformAuditExport: {
        method: "GET",
        path: "/platform/audit-exports/{auditExportPublicID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    downloadPlatformAuditExport: {
        method: "GET",
        path: "/platform/audit-exports/{auditExportPublicID}/download",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    getPlatformEmergencyControls: {
        method: "GET",
        path: "/platform/emergency-controls",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    updatePlatformEmergencyControls: {
        method: "PUT",
        path: "/platform/emergency-controls",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    getWorkbenchDashboard: {
        method: "GET",
        path: "/workbench/dashboard",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listCrossProjectWorkbenchTickets: {
        method: "GET",
        path: "/workbench/tickets",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    listProjectOverdueTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/overdue",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectSLABreachedTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/sla-breach",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectTicket: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    updateProjectTicket: {
        method: "PUT",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    deleteProjectTicket: {
        method: "DELETE",
        path: "/projects/{projectKey}/tickets/{ticketID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    assignProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/assign",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    transferProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/transfer",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    escalateProjectTicket: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/escalate",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    updateProjectTicketStatus: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/status",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    getProjectTicketAllowedTransitions: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/transitions",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectTicketHistory: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/history",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectTicketComments: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectTicketComment: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    listProjectTicketCommentReplies: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/comments/{commentID}/replies",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectTicketAttachments: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    uploadProjectTicketAttachment: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments",
        successStatus: 202,
        requestBody: "required",
        listStrategy: "bounded",
    },
    downloadProjectTicketAttachment: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/attachments/{attachmentID}/content",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectNotifications: {
        method: "GET",
        path: "/projects/{projectKey}/notifications",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectNotification: {
        method: "POST",
        path: "/projects/{projectKey}/notifications",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    deleteProjectNotification: {
        method: "DELETE",
        path: "/projects/{projectKey}/notifications/{notificationID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    markProjectNotificationRead: {
        method: "PUT",
        path: "/projects/{projectKey}/notifications/{notificationID}/read",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    markAllProjectNotificationsRead: {
        method: "PUT",
        path: "/projects/{projectKey}/notifications/read-all",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    getProjectUnreadNotificationCount: {
        method: "GET",
        path: "/projects/{projectKey}/notifications/unread-count",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    getHumanNotificationPreferences: {
        method: "GET",
        path: "/notification-preferences",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    updateHumanNotificationPreferences: {
        method: "PUT",
        path: "/notification-preferences",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectAutomationRules: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/rules",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectAutomationRule: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/rules",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    getProjectAutomationRule: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    updateProjectAutomationRule: {
        method: "PUT",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    deleteProjectAutomationRule: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/automation/rules/{ruleID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectAutomationLogs: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/logs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    getPlatformEmailConfig: {
        method: "GET",
        path: "/platform/email-config",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    updatePlatformEmailConfig: {
        method: "PUT",
        path: "/platform/email-config",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    testPlatformEmailConfig: {
        method: "POST",
        path: "/platform/email-config/test",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listPlatformCleanupLogs: {
        method: "GET",
        path: "/platform/system/cleanup/logs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listPlatformConfigs: {
        method: "GET",
        path: "/platform/configs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    updatePlatformConfig: {
        method: "PUT",
        path: "/platform/configs/{configKey}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectWebhooks: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectWebhook: {
        method: "POST",
        path: "/projects/{projectKey}/webhooks",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    getProjectWebhook: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    updateProjectWebhook: {
        method: "PUT",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    deleteProjectWebhook: {
        method: "DELETE",
        path: "/projects/{projectKey}/webhooks/{webhookID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    queueProjectWebhookTest: {
        method: "POST",
        path: "/projects/{projectKey}/webhooks/{webhookID}/test",
        successStatus: 202,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectWebhookLogs: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}/logs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    getProjectWebhookStats: {
        method: "GET",
        path: "/projects/{projectKey}/webhooks/{webhookID}/stats",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    getAgentControlOverviewV2: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/agent-control/overview",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listAgentServicePrincipals: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/service-principals",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createServicePrincipalV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    setServicePrincipalStatusV2: {
        method: "PUT",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/status",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    rotateServicePrincipalCredentialV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/rotate",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    revokeServicePrincipalCredentialV2: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/credentials/{credentialId}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listServicePrincipalPoliciesV2: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createServicePrincipalPolicyV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    disableServicePrincipalPolicyV2: {
        method: "DELETE",
        path: "/projects/{projectKey}/admin/agents/service-principals/{principalId}/policies/{policyId}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listAgentTicketLeases: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/leases",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    forceReleaseTicketLeaseV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/leases/{leaseId}/force-release",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listAgentAttachmentScans: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/attachments",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    recordAttachmentVirusScanV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/attachments/{attachmentId}/scan",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    listAgentOutboxDeliveries: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/outbox",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    replayOutboxDeliveryV2: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/outbox/{deliveryId}/replay",
        successStatus: 202,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectWebhookEmergencyTombstones: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/webhooks/tombstones",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectWebhookEmergencyRevokePreflight: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    emergencyRevokeProjectWebhook: {
        method: "POST",
        path: "/projects/{projectKey}/admin/agents/webhooks/{webhookID}/emergency-revoke",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listAgentDomainEvents: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/events",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    listAgentPolicyDecisions: {
        method: "GET",
        path: "/projects/{projectKey}/admin/agents/policy-decisions",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    listLoginHistory: {
        method: "GET",
        path: "/user/login-history",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    deleteLoginHistorySession: {
        method: "DELETE",
        path: "/user/login-history/{loginHistoryID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectCategories: {
        method: "GET",
        path: "/projects/{projectKey}/categories",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectCategory: {
        method: "GET",
        path: "/projects/{projectKey}/categories/{categoryID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectAssignees: {
        method: "GET",
        path: "/projects/{projectKey}/assignees",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectAssignee: {
        method: "GET",
        path: "/projects/{projectKey}/assignees/{assigneeID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectTicketEntityLinks: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/entity-links",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectTicketEntityLink: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/entity-links",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectTicketRelations: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/{ticketID}/relations",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectTicketRelation: {
        method: "POST",
        path: "/projects/{projectKey}/tickets/{ticketID}/relations",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectAgentRuns: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/runs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectAgentRun: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/runs/{runID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectActionProposals: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/proposals",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectActionProposal: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/proposals/{proposalID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectApprovalTasks: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/approvals",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectApprovalTask: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/approvals/{approvalID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectHandoffs: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/handoffs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectHandoff: {
        method: "GET",
        path: "/projects/{projectKey}/agent-collaboration/handoffs/{handoffID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    decideProjectAgentApproval: {
        method: "POST",
        path: "/projects/{projectKey}/agent-collaboration/approvals/{approvalID}/decisions",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    takeOverProjectAgentRun: {
        method: "POST",
        path: "/projects/{projectKey}/agent-collaboration/runs/{runID}/takeover",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    getProjectIntakeConfiguration: {
        method: "GET",
        path: "/projects/{projectKey}/configuration/intake",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectSLAConfigs: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/sla",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectSLAConfig: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/sla",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectTicketTemplates: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/templates",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectTicketTemplate: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/templates",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    getProjectTicketTemplate: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/templates/{automationConfigID}",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectQuickReplies: {
        method: "GET",
        path: "/projects/{projectKey}/admin/automation/quick-replies",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectQuickReply: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/quick-replies",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    useProjectQuickReply: {
        method: "POST",
        path: "/projects/{projectKey}/admin/automation/quick-replies/{automationConfigID}/use",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    listProjectKnowledgeArticles: {
        method: "GET",
        path: "/projects/{projectKey}/knowledge/articles",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectKnowledgeArticle: {
        method: "POST",
        path: "/projects/{projectKey}/knowledge/articles",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    createProjectKnowledgeArticleDraft: {
        method: "POST",
        path: "/projects/{projectKey}/knowledge/articles/{articleID}/drafts",
        successStatus: 201,
        requestBody: "required",
        listStrategy: "bounded",
    },
    getProjectKnowledgeArticleDocument: {
        method: "GET",
        path: "/projects/{projectKey}/knowledge/articles/{articleID}/document",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectKnowledgeVersions: {
        method: "GET",
        path: "/projects/{projectKey}/knowledge/articles/{articleID}/versions",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    publishProjectKnowledgeVersion: {
        method: "POST",
        path: "/projects/{projectKey}/knowledge/versions/{versionID}/publication",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    searchProjectKnowledge: {
        method: "POST",
        path: "/projects/{projectKey}/knowledge/searches",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    listProjectKnowledgeIngestions: {
        method: "GET",
        path: "/projects/{projectKey}/knowledge/ingestions",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    getProjectKnowledgeIndexState: {
        method: "GET",
        path: "/projects/{projectKey}/knowledge/index-rebuilds/current",
        successStatus: 200,
        requestBody: "none",
        listStrategy: null,
    },
    rebuildProjectKnowledgeIndex: {
        method: "POST",
        path: "/projects/{projectKey}/knowledge/index-rebuilds",
        successStatus: 202,
        requestBody: "none",
        listStrategy: null,
    },
    listMyProjectTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/my-tickets",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listUnassignedProjectTickets: {
        method: "GET",
        path: "/projects/{projectKey}/tickets/unassigned",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectIntegrationConnectorDefinitions: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/connector-definitions",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectIntegrationConnectorDefinition: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/connector-definitions",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    updateProjectIntegrationConnectorDefinition: {
        method: "PUT",
        path: "/projects/{projectKey}/integrations/connector-definitions/{definitionID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectIntegrationConnections: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/connections",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectIntegrationConnection: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/connections",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    updateProjectIntegrationConnection: {
        method: "PUT",
        path: "/projects/{projectKey}/integrations/connections/{connectionID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectIntegrationMappings: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    createProjectIntegrationMapping: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/connections/{connectionID}/mappings",
        successStatus: 201,
        requestBody: "required",
        listStrategy: null,
    },
    updateProjectIntegrationMapping: {
        method: "PUT",
        path: "/projects/{projectKey}/integrations/mappings/{mappingID}",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    dryRunProjectIntegrationMapping: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/mappings/{mappingID}/dry-runs",
        successStatus: 200,
        requestBody: "required",
        listStrategy: "bounded",
    },
    publishProjectIntegrationMapping: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/mappings/{mappingID}/publication",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    getProjectIntegrationOverview: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/overview",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "bounded",
    },
    listProjectIntegrationInboxMessages: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/inbox",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectIntegrationInboxReceipts: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/inbox/{messageID}/receipts",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectIntegrationSyncRuns: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/sync-runs",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    listProjectIntegrationConflicts: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/conflicts",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    resolveProjectIntegrationConflict: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/conflicts/{conflictID}/resolution",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectIntegrationDeadLetters: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/dead-letters",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
    replayProjectIntegrationDeadLetter: {
        method: "POST",
        path: "/projects/{projectKey}/integrations/dead-letters/{deadLetterID}/replays",
        successStatus: 200,
        requestBody: "required",
        listStrategy: null,
    },
    listProjectIntegrationDomainEvents: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/domain-events",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "cursor",
    },
    listProjectIntegrationOutboxDeliveries: {
        method: "GET",
        path: "/projects/{projectKey}/integrations/outbox",
        successStatus: 200,
        requestBody: "none",
        listStrategy: "page",
    },
} as const

export interface HumanApiOperationTypes {
    registerHuman: {
        pathParameters: RegisterHumanOperationPathParameters
        query: RegisterHumanOperationQuery
        request: RegisterHumanOperationRequest
        response: RegisterHumanOperationResponse
    }
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
    verifyHumanEmail: {
        pathParameters: VerifyHumanEmailOperationPathParameters
        query: VerifyHumanEmailOperationQuery
        request: VerifyHumanEmailOperationRequest
        response: VerifyHumanEmailOperationResponse
    }
    resendHumanEmailVerification: {
        pathParameters: ResendHumanEmailVerificationOperationPathParameters
        query: ResendHumanEmailVerificationOperationQuery
        request: ResendHumanEmailVerificationOperationRequest
        response: ResendHumanEmailVerificationOperationResponse
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
    regenerateOTPBackupCodes: {
        pathParameters: RegenerateOTPBackupCodesOperationPathParameters
        query: RegenerateOTPBackupCodesOperationQuery
        request: RegenerateOTPBackupCodesOperationRequest
        response: RegenerateOTPBackupCodesOperationResponse
    }
    listTrustedDevices: {
        pathParameters: ListTrustedDevicesOperationPathParameters
        query: ListTrustedDevicesOperationQuery
        request: ListTrustedDevicesOperationRequest
        response: ListTrustedDevicesOperationResponse
    }
    revokeTrustedDevice: {
        pathParameters: RevokeTrustedDeviceOperationPathParameters
        query: RevokeTrustedDeviceOperationQuery
        request: RevokeTrustedDeviceOperationRequest
        response: RevokeTrustedDeviceOperationResponse
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
    listProjectQueues: {
        pathParameters: ListProjectQueuesOperationPathParameters
        query: ListProjectQueuesOperationQuery
        request: ListProjectQueuesOperationRequest
        response: ListProjectQueuesOperationResponse
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
    createPlatformAuditExport: {
        pathParameters: CreatePlatformAuditExportOperationPathParameters
        query: CreatePlatformAuditExportOperationQuery
        request: CreatePlatformAuditExportOperationRequest
        response: CreatePlatformAuditExportOperationResponse
    }
    getPlatformAuditExport: {
        pathParameters: GetPlatformAuditExportOperationPathParameters
        query: GetPlatformAuditExportOperationQuery
        request: GetPlatformAuditExportOperationRequest
        response: GetPlatformAuditExportOperationResponse
    }
    downloadPlatformAuditExport: {
        pathParameters: DownloadPlatformAuditExportOperationPathParameters
        query: DownloadPlatformAuditExportOperationQuery
        request: DownloadPlatformAuditExportOperationRequest
        response: DownloadPlatformAuditExportOperationResponse
    }
    getPlatformEmergencyControls: {
        pathParameters: GetPlatformEmergencyControlsOperationPathParameters
        query: GetPlatformEmergencyControlsOperationQuery
        request: GetPlatformEmergencyControlsOperationRequest
        response: GetPlatformEmergencyControlsOperationResponse
    }
    updatePlatformEmergencyControls: {
        pathParameters: UpdatePlatformEmergencyControlsOperationPathParameters
        query: UpdatePlatformEmergencyControlsOperationQuery
        request: UpdatePlatformEmergencyControlsOperationRequest
        response: UpdatePlatformEmergencyControlsOperationResponse
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
    getProjectTicketAllowedTransitions: {
        pathParameters: GetProjectTicketAllowedTransitionsOperationPathParameters
        query: GetProjectTicketAllowedTransitionsOperationQuery
        request: GetProjectTicketAllowedTransitionsOperationRequest
        response: GetProjectTicketAllowedTransitionsOperationResponse
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
    listPlatformCleanupLogs: {
        pathParameters: ListPlatformCleanupLogsOperationPathParameters
        query: ListPlatformCleanupLogsOperationQuery
        request: ListPlatformCleanupLogsOperationRequest
        response: ListPlatformCleanupLogsOperationResponse
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
    listAgentServicePrincipals: {
        pathParameters: ListAgentServicePrincipalsOperationPathParameters
        query: ListAgentServicePrincipalsOperationQuery
        request: ListAgentServicePrincipalsOperationRequest
        response: ListAgentServicePrincipalsOperationResponse
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
    listAgentTicketLeases: {
        pathParameters: ListAgentTicketLeasesOperationPathParameters
        query: ListAgentTicketLeasesOperationQuery
        request: ListAgentTicketLeasesOperationRequest
        response: ListAgentTicketLeasesOperationResponse
    }
    forceReleaseTicketLeaseV2: {
        pathParameters: ForceReleaseTicketLeaseV2OperationPathParameters
        query: ForceReleaseTicketLeaseV2OperationQuery
        request: ForceReleaseTicketLeaseV2OperationRequest
        response: ForceReleaseTicketLeaseV2OperationResponse
    }
    listAgentAttachmentScans: {
        pathParameters: ListAgentAttachmentScansOperationPathParameters
        query: ListAgentAttachmentScansOperationQuery
        request: ListAgentAttachmentScansOperationRequest
        response: ListAgentAttachmentScansOperationResponse
    }
    recordAttachmentVirusScanV2: {
        pathParameters: RecordAttachmentVirusScanV2OperationPathParameters
        query: RecordAttachmentVirusScanV2OperationQuery
        request: RecordAttachmentVirusScanV2OperationRequest
        response: RecordAttachmentVirusScanV2OperationResponse
    }
    listAgentOutboxDeliveries: {
        pathParameters: ListAgentOutboxDeliveriesOperationPathParameters
        query: ListAgentOutboxDeliveriesOperationQuery
        request: ListAgentOutboxDeliveriesOperationRequest
        response: ListAgentOutboxDeliveriesOperationResponse
    }
    replayOutboxDeliveryV2: {
        pathParameters: ReplayOutboxDeliveryV2OperationPathParameters
        query: ReplayOutboxDeliveryV2OperationQuery
        request: ReplayOutboxDeliveryV2OperationRequest
        response: ReplayOutboxDeliveryV2OperationResponse
    }
    listProjectWebhookEmergencyTombstones: {
        pathParameters: ListProjectWebhookEmergencyTombstonesOperationPathParameters
        query: ListProjectWebhookEmergencyTombstonesOperationQuery
        request: ListProjectWebhookEmergencyTombstonesOperationRequest
        response: ListProjectWebhookEmergencyTombstonesOperationResponse
    }
    getProjectWebhookEmergencyRevokePreflight: {
        pathParameters: GetProjectWebhookEmergencyRevokePreflightOperationPathParameters
        query: GetProjectWebhookEmergencyRevokePreflightOperationQuery
        request: GetProjectWebhookEmergencyRevokePreflightOperationRequest
        response: GetProjectWebhookEmergencyRevokePreflightOperationResponse
    }
    emergencyRevokeProjectWebhook: {
        pathParameters: EmergencyRevokeProjectWebhookOperationPathParameters
        query: EmergencyRevokeProjectWebhookOperationQuery
        request: EmergencyRevokeProjectWebhookOperationRequest
        response: EmergencyRevokeProjectWebhookOperationResponse
    }
    listAgentDomainEvents: {
        pathParameters: ListAgentDomainEventsOperationPathParameters
        query: ListAgentDomainEventsOperationQuery
        request: ListAgentDomainEventsOperationRequest
        response: ListAgentDomainEventsOperationResponse
    }
    listAgentPolicyDecisions: {
        pathParameters: ListAgentPolicyDecisionsOperationPathParameters
        query: ListAgentPolicyDecisionsOperationQuery
        request: ListAgentPolicyDecisionsOperationRequest
        response: ListAgentPolicyDecisionsOperationResponse
    }
    listLoginHistory: {
        pathParameters: ListLoginHistoryOperationPathParameters
        query: ListLoginHistoryOperationQuery
        request: ListLoginHistoryOperationRequest
        response: ListLoginHistoryOperationResponse
    }
    deleteLoginHistorySession: {
        pathParameters: DeleteLoginHistorySessionOperationPathParameters
        query: DeleteLoginHistorySessionOperationQuery
        request: DeleteLoginHistorySessionOperationRequest
        response: DeleteLoginHistorySessionOperationResponse
    }
    listProjectCategories: {
        pathParameters: ListProjectCategoriesOperationPathParameters
        query: ListProjectCategoriesOperationQuery
        request: ListProjectCategoriesOperationRequest
        response: ListProjectCategoriesOperationResponse
    }
    getProjectCategory: {
        pathParameters: GetProjectCategoryOperationPathParameters
        query: GetProjectCategoryOperationQuery
        request: GetProjectCategoryOperationRequest
        response: GetProjectCategoryOperationResponse
    }
    listProjectAssignees: {
        pathParameters: ListProjectAssigneesOperationPathParameters
        query: ListProjectAssigneesOperationQuery
        request: ListProjectAssigneesOperationRequest
        response: ListProjectAssigneesOperationResponse
    }
    getProjectAssignee: {
        pathParameters: GetProjectAssigneeOperationPathParameters
        query: GetProjectAssigneeOperationQuery
        request: GetProjectAssigneeOperationRequest
        response: GetProjectAssigneeOperationResponse
    }
    listProjectTicketEntityLinks: {
        pathParameters: ListProjectTicketEntityLinksOperationPathParameters
        query: ListProjectTicketEntityLinksOperationQuery
        request: ListProjectTicketEntityLinksOperationRequest
        response: ListProjectTicketEntityLinksOperationResponse
    }
    createProjectTicketEntityLink: {
        pathParameters: CreateProjectTicketEntityLinkOperationPathParameters
        query: CreateProjectTicketEntityLinkOperationQuery
        request: CreateProjectTicketEntityLinkOperationRequest
        response: CreateProjectTicketEntityLinkOperationResponse
    }
    listProjectTicketRelations: {
        pathParameters: ListProjectTicketRelationsOperationPathParameters
        query: ListProjectTicketRelationsOperationQuery
        request: ListProjectTicketRelationsOperationRequest
        response: ListProjectTicketRelationsOperationResponse
    }
    createProjectTicketRelation: {
        pathParameters: CreateProjectTicketRelationOperationPathParameters
        query: CreateProjectTicketRelationOperationQuery
        request: CreateProjectTicketRelationOperationRequest
        response: CreateProjectTicketRelationOperationResponse
    }
    listProjectAgentRuns: {
        pathParameters: ListProjectAgentRunsOperationPathParameters
        query: ListProjectAgentRunsOperationQuery
        request: ListProjectAgentRunsOperationRequest
        response: ListProjectAgentRunsOperationResponse
    }
    getProjectAgentRun: {
        pathParameters: GetProjectAgentRunOperationPathParameters
        query: GetProjectAgentRunOperationQuery
        request: GetProjectAgentRunOperationRequest
        response: GetProjectAgentRunOperationResponse
    }
    listProjectActionProposals: {
        pathParameters: ListProjectActionProposalsOperationPathParameters
        query: ListProjectActionProposalsOperationQuery
        request: ListProjectActionProposalsOperationRequest
        response: ListProjectActionProposalsOperationResponse
    }
    getProjectActionProposal: {
        pathParameters: GetProjectActionProposalOperationPathParameters
        query: GetProjectActionProposalOperationQuery
        request: GetProjectActionProposalOperationRequest
        response: GetProjectActionProposalOperationResponse
    }
    listProjectApprovalTasks: {
        pathParameters: ListProjectApprovalTasksOperationPathParameters
        query: ListProjectApprovalTasksOperationQuery
        request: ListProjectApprovalTasksOperationRequest
        response: ListProjectApprovalTasksOperationResponse
    }
    getProjectApprovalTask: {
        pathParameters: GetProjectApprovalTaskOperationPathParameters
        query: GetProjectApprovalTaskOperationQuery
        request: GetProjectApprovalTaskOperationRequest
        response: GetProjectApprovalTaskOperationResponse
    }
    listProjectHandoffs: {
        pathParameters: ListProjectHandoffsOperationPathParameters
        query: ListProjectHandoffsOperationQuery
        request: ListProjectHandoffsOperationRequest
        response: ListProjectHandoffsOperationResponse
    }
    getProjectHandoff: {
        pathParameters: GetProjectHandoffOperationPathParameters
        query: GetProjectHandoffOperationQuery
        request: GetProjectHandoffOperationRequest
        response: GetProjectHandoffOperationResponse
    }
    decideProjectAgentApproval: {
        pathParameters: DecideProjectAgentApprovalOperationPathParameters
        query: DecideProjectAgentApprovalOperationQuery
        request: DecideProjectAgentApprovalOperationRequest
        response: DecideProjectAgentApprovalOperationResponse
    }
    takeOverProjectAgentRun: {
        pathParameters: TakeOverProjectAgentRunOperationPathParameters
        query: TakeOverProjectAgentRunOperationQuery
        request: TakeOverProjectAgentRunOperationRequest
        response: TakeOverProjectAgentRunOperationResponse
    }
    getProjectIntakeConfiguration: {
        pathParameters: GetProjectIntakeConfigurationOperationPathParameters
        query: GetProjectIntakeConfigurationOperationQuery
        request: GetProjectIntakeConfigurationOperationRequest
        response: GetProjectIntakeConfigurationOperationResponse
    }
    listProjectSLAConfigs: {
        pathParameters: ListProjectSLAConfigsOperationPathParameters
        query: ListProjectSLAConfigsOperationQuery
        request: ListProjectSLAConfigsOperationRequest
        response: ListProjectSLAConfigsOperationResponse
    }
    createProjectSLAConfig: {
        pathParameters: CreateProjectSLAConfigOperationPathParameters
        query: CreateProjectSLAConfigOperationQuery
        request: CreateProjectSLAConfigOperationRequest
        response: CreateProjectSLAConfigOperationResponse
    }
    listProjectTicketTemplates: {
        pathParameters: ListProjectTicketTemplatesOperationPathParameters
        query: ListProjectTicketTemplatesOperationQuery
        request: ListProjectTicketTemplatesOperationRequest
        response: ListProjectTicketTemplatesOperationResponse
    }
    createProjectTicketTemplate: {
        pathParameters: CreateProjectTicketTemplateOperationPathParameters
        query: CreateProjectTicketTemplateOperationQuery
        request: CreateProjectTicketTemplateOperationRequest
        response: CreateProjectTicketTemplateOperationResponse
    }
    getProjectTicketTemplate: {
        pathParameters: GetProjectTicketTemplateOperationPathParameters
        query: GetProjectTicketTemplateOperationQuery
        request: GetProjectTicketTemplateOperationRequest
        response: GetProjectTicketTemplateOperationResponse
    }
    listProjectQuickReplies: {
        pathParameters: ListProjectQuickRepliesOperationPathParameters
        query: ListProjectQuickRepliesOperationQuery
        request: ListProjectQuickRepliesOperationRequest
        response: ListProjectQuickRepliesOperationResponse
    }
    createProjectQuickReply: {
        pathParameters: CreateProjectQuickReplyOperationPathParameters
        query: CreateProjectQuickReplyOperationQuery
        request: CreateProjectQuickReplyOperationRequest
        response: CreateProjectQuickReplyOperationResponse
    }
    useProjectQuickReply: {
        pathParameters: UseProjectQuickReplyOperationPathParameters
        query: UseProjectQuickReplyOperationQuery
        request: UseProjectQuickReplyOperationRequest
        response: UseProjectQuickReplyOperationResponse
    }
    listProjectKnowledgeArticles: {
        pathParameters: ListProjectKnowledgeArticlesOperationPathParameters
        query: ListProjectKnowledgeArticlesOperationQuery
        request: ListProjectKnowledgeArticlesOperationRequest
        response: ListProjectKnowledgeArticlesOperationResponse
    }
    createProjectKnowledgeArticle: {
        pathParameters: CreateProjectKnowledgeArticleOperationPathParameters
        query: CreateProjectKnowledgeArticleOperationQuery
        request: CreateProjectKnowledgeArticleOperationRequest
        response: CreateProjectKnowledgeArticleOperationResponse
    }
    createProjectKnowledgeArticleDraft: {
        pathParameters: CreateProjectKnowledgeArticleDraftOperationPathParameters
        query: CreateProjectKnowledgeArticleDraftOperationQuery
        request: CreateProjectKnowledgeArticleDraftOperationRequest
        response: CreateProjectKnowledgeArticleDraftOperationResponse
    }
    getProjectKnowledgeArticleDocument: {
        pathParameters: GetProjectKnowledgeArticleDocumentOperationPathParameters
        query: GetProjectKnowledgeArticleDocumentOperationQuery
        request: GetProjectKnowledgeArticleDocumentOperationRequest
        response: GetProjectKnowledgeArticleDocumentOperationResponse
    }
    listProjectKnowledgeVersions: {
        pathParameters: ListProjectKnowledgeVersionsOperationPathParameters
        query: ListProjectKnowledgeVersionsOperationQuery
        request: ListProjectKnowledgeVersionsOperationRequest
        response: ListProjectKnowledgeVersionsOperationResponse
    }
    publishProjectKnowledgeVersion: {
        pathParameters: PublishProjectKnowledgeVersionOperationPathParameters
        query: PublishProjectKnowledgeVersionOperationQuery
        request: PublishProjectKnowledgeVersionOperationRequest
        response: PublishProjectKnowledgeVersionOperationResponse
    }
    searchProjectKnowledge: {
        pathParameters: SearchProjectKnowledgeOperationPathParameters
        query: SearchProjectKnowledgeOperationQuery
        request: SearchProjectKnowledgeOperationRequest
        response: SearchProjectKnowledgeOperationResponse
    }
    listProjectKnowledgeIngestions: {
        pathParameters: ListProjectKnowledgeIngestionsOperationPathParameters
        query: ListProjectKnowledgeIngestionsOperationQuery
        request: ListProjectKnowledgeIngestionsOperationRequest
        response: ListProjectKnowledgeIngestionsOperationResponse
    }
    getProjectKnowledgeIndexState: {
        pathParameters: GetProjectKnowledgeIndexStateOperationPathParameters
        query: GetProjectKnowledgeIndexStateOperationQuery
        request: GetProjectKnowledgeIndexStateOperationRequest
        response: GetProjectKnowledgeIndexStateOperationResponse
    }
    rebuildProjectKnowledgeIndex: {
        pathParameters: RebuildProjectKnowledgeIndexOperationPathParameters
        query: RebuildProjectKnowledgeIndexOperationQuery
        request: RebuildProjectKnowledgeIndexOperationRequest
        response: RebuildProjectKnowledgeIndexOperationResponse
    }
    listMyProjectTickets: {
        pathParameters: ListMyProjectTicketsOperationPathParameters
        query: ListMyProjectTicketsOperationQuery
        request: ListMyProjectTicketsOperationRequest
        response: ListMyProjectTicketsOperationResponse
    }
    listUnassignedProjectTickets: {
        pathParameters: ListUnassignedProjectTicketsOperationPathParameters
        query: ListUnassignedProjectTicketsOperationQuery
        request: ListUnassignedProjectTicketsOperationRequest
        response: ListUnassignedProjectTicketsOperationResponse
    }
    listProjectIntegrationConnectorDefinitions: {
        pathParameters: ListProjectIntegrationConnectorDefinitionsOperationPathParameters
        query: ListProjectIntegrationConnectorDefinitionsOperationQuery
        request: ListProjectIntegrationConnectorDefinitionsOperationRequest
        response: ListProjectIntegrationConnectorDefinitionsOperationResponse
    }
    createProjectIntegrationConnectorDefinition: {
        pathParameters: CreateProjectIntegrationConnectorDefinitionOperationPathParameters
        query: CreateProjectIntegrationConnectorDefinitionOperationQuery
        request: CreateProjectIntegrationConnectorDefinitionOperationRequest
        response: CreateProjectIntegrationConnectorDefinitionOperationResponse
    }
    updateProjectIntegrationConnectorDefinition: {
        pathParameters: UpdateProjectIntegrationConnectorDefinitionOperationPathParameters
        query: UpdateProjectIntegrationConnectorDefinitionOperationQuery
        request: UpdateProjectIntegrationConnectorDefinitionOperationRequest
        response: UpdateProjectIntegrationConnectorDefinitionOperationResponse
    }
    listProjectIntegrationConnections: {
        pathParameters: ListProjectIntegrationConnectionsOperationPathParameters
        query: ListProjectIntegrationConnectionsOperationQuery
        request: ListProjectIntegrationConnectionsOperationRequest
        response: ListProjectIntegrationConnectionsOperationResponse
    }
    createProjectIntegrationConnection: {
        pathParameters: CreateProjectIntegrationConnectionOperationPathParameters
        query: CreateProjectIntegrationConnectionOperationQuery
        request: CreateProjectIntegrationConnectionOperationRequest
        response: CreateProjectIntegrationConnectionOperationResponse
    }
    updateProjectIntegrationConnection: {
        pathParameters: UpdateProjectIntegrationConnectionOperationPathParameters
        query: UpdateProjectIntegrationConnectionOperationQuery
        request: UpdateProjectIntegrationConnectionOperationRequest
        response: UpdateProjectIntegrationConnectionOperationResponse
    }
    listProjectIntegrationMappings: {
        pathParameters: ListProjectIntegrationMappingsOperationPathParameters
        query: ListProjectIntegrationMappingsOperationQuery
        request: ListProjectIntegrationMappingsOperationRequest
        response: ListProjectIntegrationMappingsOperationResponse
    }
    createProjectIntegrationMapping: {
        pathParameters: CreateProjectIntegrationMappingOperationPathParameters
        query: CreateProjectIntegrationMappingOperationQuery
        request: CreateProjectIntegrationMappingOperationRequest
        response: CreateProjectIntegrationMappingOperationResponse
    }
    updateProjectIntegrationMapping: {
        pathParameters: UpdateProjectIntegrationMappingOperationPathParameters
        query: UpdateProjectIntegrationMappingOperationQuery
        request: UpdateProjectIntegrationMappingOperationRequest
        response: UpdateProjectIntegrationMappingOperationResponse
    }
    dryRunProjectIntegrationMapping: {
        pathParameters: DryRunProjectIntegrationMappingOperationPathParameters
        query: DryRunProjectIntegrationMappingOperationQuery
        request: DryRunProjectIntegrationMappingOperationRequest
        response: DryRunProjectIntegrationMappingOperationResponse
    }
    publishProjectIntegrationMapping: {
        pathParameters: PublishProjectIntegrationMappingOperationPathParameters
        query: PublishProjectIntegrationMappingOperationQuery
        request: PublishProjectIntegrationMappingOperationRequest
        response: PublishProjectIntegrationMappingOperationResponse
    }
    getProjectIntegrationOverview: {
        pathParameters: GetProjectIntegrationOverviewOperationPathParameters
        query: GetProjectIntegrationOverviewOperationQuery
        request: GetProjectIntegrationOverviewOperationRequest
        response: GetProjectIntegrationOverviewOperationResponse
    }
    listProjectIntegrationInboxMessages: {
        pathParameters: ListProjectIntegrationInboxMessagesOperationPathParameters
        query: ListProjectIntegrationInboxMessagesOperationQuery
        request: ListProjectIntegrationInboxMessagesOperationRequest
        response: ListProjectIntegrationInboxMessagesOperationResponse
    }
    listProjectIntegrationInboxReceipts: {
        pathParameters: ListProjectIntegrationInboxReceiptsOperationPathParameters
        query: ListProjectIntegrationInboxReceiptsOperationQuery
        request: ListProjectIntegrationInboxReceiptsOperationRequest
        response: ListProjectIntegrationInboxReceiptsOperationResponse
    }
    listProjectIntegrationSyncRuns: {
        pathParameters: ListProjectIntegrationSyncRunsOperationPathParameters
        query: ListProjectIntegrationSyncRunsOperationQuery
        request: ListProjectIntegrationSyncRunsOperationRequest
        response: ListProjectIntegrationSyncRunsOperationResponse
    }
    listProjectIntegrationConflicts: {
        pathParameters: ListProjectIntegrationConflictsOperationPathParameters
        query: ListProjectIntegrationConflictsOperationQuery
        request: ListProjectIntegrationConflictsOperationRequest
        response: ListProjectIntegrationConflictsOperationResponse
    }
    resolveProjectIntegrationConflict: {
        pathParameters: ResolveProjectIntegrationConflictOperationPathParameters
        query: ResolveProjectIntegrationConflictOperationQuery
        request: ResolveProjectIntegrationConflictOperationRequest
        response: ResolveProjectIntegrationConflictOperationResponse
    }
    listProjectIntegrationDeadLetters: {
        pathParameters: ListProjectIntegrationDeadLettersOperationPathParameters
        query: ListProjectIntegrationDeadLettersOperationQuery
        request: ListProjectIntegrationDeadLettersOperationRequest
        response: ListProjectIntegrationDeadLettersOperationResponse
    }
    replayProjectIntegrationDeadLetter: {
        pathParameters: ReplayProjectIntegrationDeadLetterOperationPathParameters
        query: ReplayProjectIntegrationDeadLetterOperationQuery
        request: ReplayProjectIntegrationDeadLetterOperationRequest
        response: ReplayProjectIntegrationDeadLetterOperationResponse
    }
    listProjectIntegrationDomainEvents: {
        pathParameters: ListProjectIntegrationDomainEventsOperationPathParameters
        query: ListProjectIntegrationDomainEventsOperationQuery
        request: ListProjectIntegrationDomainEventsOperationRequest
        response: ListProjectIntegrationDomainEventsOperationResponse
    }
    listProjectIntegrationOutboxDeliveries: {
        pathParameters: ListProjectIntegrationOutboxDeliveriesOperationPathParameters
        query: ListProjectIntegrationOutboxDeliveriesOperationQuery
        request: ListProjectIntegrationOutboxDeliveriesOperationRequest
        response: ListProjectIntegrationOutboxDeliveriesOperationResponse
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
    registerHuman: (query: RegisterHumanOperationQuery = {}) =>
        humanApiRoute("registerHuman", {}, query),
    createHumanSession: (query: CreateHumanSessionOperationQuery = {}) =>
        humanApiRoute("createHumanSession", {}, query),
    refreshHumanSession: (query: RefreshHumanSessionOperationQuery = {}) =>
        humanApiRoute("refreshHumanSession", {}, query),
    requestHumanPasswordReset: (query: RequestHumanPasswordResetOperationQuery = {}) =>
        humanApiRoute("requestHumanPasswordReset", {}, query),
    resetHumanPassword: (query: ResetHumanPasswordOperationQuery = {}) =>
        humanApiRoute("resetHumanPassword", {}, query),
    verifyHumanEmail: (query: VerifyHumanEmailOperationQuery = {}) =>
        humanApiRoute("verifyHumanEmail", {}, query),
    resendHumanEmailVerification: (query: ResendHumanEmailVerificationOperationQuery = {}) =>
        humanApiRoute("resendHumanEmailVerification", {}, query),
    deleteHumanSession: (query: DeleteHumanSessionOperationQuery = {}) =>
        humanApiRoute("deleteHumanSession", {}, query),
    deleteAllHumanSessions: (query: DeleteAllHumanSessionsOperationQuery = {}) =>
        humanApiRoute("deleteAllHumanSessions", {}, query),
    getHumanSessionUser: (query: GetHumanSessionUserOperationQuery = {}) =>
        humanApiRoute("getHumanSessionUser", {}, query),
    updateHumanProfile: (query: UpdateHumanProfileOperationQuery = {}) =>
        humanApiRoute("updateHumanProfile", {}, query),
    regenerateOTPBackupCodes: (query: RegenerateOTPBackupCodesOperationQuery = {}) =>
        humanApiRoute("regenerateOTPBackupCodes", {}, query),
    listTrustedDevices: (query: ListTrustedDevicesOperationQuery = {}) =>
        humanApiRoute("listTrustedDevices", {}, query),
    revokeTrustedDevice: (pathParameters: RevokeTrustedDeviceOperationPathParameters, query: RevokeTrustedDeviceOperationQuery = {}) =>
        humanApiRoute("revokeTrustedDevice", pathParameters, query),
    listAuthorizedHumanProjects: (query: ListAuthorizedHumanProjectsOperationQuery = {}) =>
        humanApiRoute("listAuthorizedHumanProjects", {}, query),
    getAuthorizedProjectContext: (pathParameters: GetAuthorizedProjectContextOperationPathParameters, query: GetAuthorizedProjectContextOperationQuery = {}) =>
        humanApiRoute("getAuthorizedProjectContext", pathParameters, query),
    listProjectQueues: (pathParameters: ListProjectQueuesOperationPathParameters, query: ListProjectQueuesOperationQuery = {}) =>
        humanApiRoute("listProjectQueues", pathParameters, query),
    listProjectMemberships: (pathParameters: ListProjectMembershipsOperationPathParameters, query: ListProjectMembershipsOperationQuery = {}) =>
        humanApiRoute("listProjectMemberships", pathParameters, query),
    upsertProjectMembership: (pathParameters: UpsertProjectMembershipOperationPathParameters, query: UpsertProjectMembershipOperationQuery = {}) =>
        humanApiRoute("upsertProjectMembership", pathParameters, query),
    searchProjectMembershipCandidates: (pathParameters: SearchProjectMembershipCandidatesOperationPathParameters, query: SearchProjectMembershipCandidatesOperationQuery = {}) =>
        humanApiRoute("searchProjectMembershipCandidates", pathParameters, query),
    deactivateProjectMembership: (pathParameters: DeactivateProjectMembershipOperationPathParameters, query: DeactivateProjectMembershipOperationQuery) =>
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
    createPlatformAuditExport: (query: CreatePlatformAuditExportOperationQuery) =>
        humanApiRoute("createPlatformAuditExport", {}, query),
    getPlatformAuditExport: (pathParameters: GetPlatformAuditExportOperationPathParameters, query: GetPlatformAuditExportOperationQuery = {}) =>
        humanApiRoute("getPlatformAuditExport", pathParameters, query),
    downloadPlatformAuditExport: (pathParameters: DownloadPlatformAuditExportOperationPathParameters, query: DownloadPlatformAuditExportOperationQuery = {}) =>
        humanApiRoute("downloadPlatformAuditExport", pathParameters, query),
    getPlatformEmergencyControls: (query: GetPlatformEmergencyControlsOperationQuery = {}) =>
        humanApiRoute("getPlatformEmergencyControls", {}, query),
    updatePlatformEmergencyControls: (query: UpdatePlatformEmergencyControlsOperationQuery = {}) =>
        humanApiRoute("updatePlatformEmergencyControls", {}, query),
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
    getProjectTicketAllowedTransitions: (pathParameters: GetProjectTicketAllowedTransitionsOperationPathParameters, query: GetProjectTicketAllowedTransitionsOperationQuery = {}) =>
        humanApiRoute("getProjectTicketAllowedTransitions", pathParameters, query),
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
    listPlatformCleanupLogs: (query: ListPlatformCleanupLogsOperationQuery = {}) =>
        humanApiRoute("listPlatformCleanupLogs", {}, query),
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
    listAgentServicePrincipals: (pathParameters: ListAgentServicePrincipalsOperationPathParameters, query: ListAgentServicePrincipalsOperationQuery = {}) =>
        humanApiRoute("listAgentServicePrincipals", pathParameters, query),
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
    listAgentTicketLeases: (pathParameters: ListAgentTicketLeasesOperationPathParameters, query: ListAgentTicketLeasesOperationQuery = {}) =>
        humanApiRoute("listAgentTicketLeases", pathParameters, query),
    forceReleaseTicketLeaseV2: (pathParameters: ForceReleaseTicketLeaseV2OperationPathParameters, query: ForceReleaseTicketLeaseV2OperationQuery = {}) =>
        humanApiRoute("forceReleaseTicketLeaseV2", pathParameters, query),
    listAgentAttachmentScans: (pathParameters: ListAgentAttachmentScansOperationPathParameters, query: ListAgentAttachmentScansOperationQuery = {}) =>
        humanApiRoute("listAgentAttachmentScans", pathParameters, query),
    recordAttachmentVirusScanV2: (pathParameters: RecordAttachmentVirusScanV2OperationPathParameters, query: RecordAttachmentVirusScanV2OperationQuery = {}) =>
        humanApiRoute("recordAttachmentVirusScanV2", pathParameters, query),
    listAgentOutboxDeliveries: (pathParameters: ListAgentOutboxDeliveriesOperationPathParameters, query: ListAgentOutboxDeliveriesOperationQuery = {}) =>
        humanApiRoute("listAgentOutboxDeliveries", pathParameters, query),
    replayOutboxDeliveryV2: (pathParameters: ReplayOutboxDeliveryV2OperationPathParameters, query: ReplayOutboxDeliveryV2OperationQuery = {}) =>
        humanApiRoute("replayOutboxDeliveryV2", pathParameters, query),
    listProjectWebhookEmergencyTombstones: (pathParameters: ListProjectWebhookEmergencyTombstonesOperationPathParameters, query: ListProjectWebhookEmergencyTombstonesOperationQuery = {}) =>
        humanApiRoute("listProjectWebhookEmergencyTombstones", pathParameters, query),
    getProjectWebhookEmergencyRevokePreflight: (pathParameters: GetProjectWebhookEmergencyRevokePreflightOperationPathParameters, query: GetProjectWebhookEmergencyRevokePreflightOperationQuery = {}) =>
        humanApiRoute("getProjectWebhookEmergencyRevokePreflight", pathParameters, query),
    emergencyRevokeProjectWebhook: (pathParameters: EmergencyRevokeProjectWebhookOperationPathParameters, query: EmergencyRevokeProjectWebhookOperationQuery = {}) =>
        humanApiRoute("emergencyRevokeProjectWebhook", pathParameters, query),
    listAgentDomainEvents: (pathParameters: ListAgentDomainEventsOperationPathParameters, query: ListAgentDomainEventsOperationQuery = {}) =>
        humanApiRoute("listAgentDomainEvents", pathParameters, query),
    listAgentPolicyDecisions: (pathParameters: ListAgentPolicyDecisionsOperationPathParameters, query: ListAgentPolicyDecisionsOperationQuery = {}) =>
        humanApiRoute("listAgentPolicyDecisions", pathParameters, query),
    listLoginHistory: (query: ListLoginHistoryOperationQuery = {}) =>
        humanApiRoute("listLoginHistory", {}, query),
    deleteLoginHistorySession: (pathParameters: DeleteLoginHistorySessionOperationPathParameters, query: DeleteLoginHistorySessionOperationQuery = {}) =>
        humanApiRoute("deleteLoginHistorySession", pathParameters, query),
    listProjectCategories: (pathParameters: ListProjectCategoriesOperationPathParameters, query: ListProjectCategoriesOperationQuery = {}) =>
        humanApiRoute("listProjectCategories", pathParameters, query),
    getProjectCategory: (pathParameters: GetProjectCategoryOperationPathParameters, query: GetProjectCategoryOperationQuery = {}) =>
        humanApiRoute("getProjectCategory", pathParameters, query),
    listProjectAssignees: (pathParameters: ListProjectAssigneesOperationPathParameters, query: ListProjectAssigneesOperationQuery = {}) =>
        humanApiRoute("listProjectAssignees", pathParameters, query),
    getProjectAssignee: (pathParameters: GetProjectAssigneeOperationPathParameters, query: GetProjectAssigneeOperationQuery = {}) =>
        humanApiRoute("getProjectAssignee", pathParameters, query),
    listProjectTicketEntityLinks: (pathParameters: ListProjectTicketEntityLinksOperationPathParameters, query: ListProjectTicketEntityLinksOperationQuery = {}) =>
        humanApiRoute("listProjectTicketEntityLinks", pathParameters, query),
    createProjectTicketEntityLink: (pathParameters: CreateProjectTicketEntityLinkOperationPathParameters, query: CreateProjectTicketEntityLinkOperationQuery = {}) =>
        humanApiRoute("createProjectTicketEntityLink", pathParameters, query),
    listProjectTicketRelations: (pathParameters: ListProjectTicketRelationsOperationPathParameters, query: ListProjectTicketRelationsOperationQuery = {}) =>
        humanApiRoute("listProjectTicketRelations", pathParameters, query),
    createProjectTicketRelation: (pathParameters: CreateProjectTicketRelationOperationPathParameters, query: CreateProjectTicketRelationOperationQuery = {}) =>
        humanApiRoute("createProjectTicketRelation", pathParameters, query),
    listProjectAgentRuns: (pathParameters: ListProjectAgentRunsOperationPathParameters, query: ListProjectAgentRunsOperationQuery = {}) =>
        humanApiRoute("listProjectAgentRuns", pathParameters, query),
    getProjectAgentRun: (pathParameters: GetProjectAgentRunOperationPathParameters, query: GetProjectAgentRunOperationQuery = {}) =>
        humanApiRoute("getProjectAgentRun", pathParameters, query),
    listProjectActionProposals: (pathParameters: ListProjectActionProposalsOperationPathParameters, query: ListProjectActionProposalsOperationQuery = {}) =>
        humanApiRoute("listProjectActionProposals", pathParameters, query),
    getProjectActionProposal: (pathParameters: GetProjectActionProposalOperationPathParameters, query: GetProjectActionProposalOperationQuery = {}) =>
        humanApiRoute("getProjectActionProposal", pathParameters, query),
    listProjectApprovalTasks: (pathParameters: ListProjectApprovalTasksOperationPathParameters, query: ListProjectApprovalTasksOperationQuery = {}) =>
        humanApiRoute("listProjectApprovalTasks", pathParameters, query),
    getProjectApprovalTask: (pathParameters: GetProjectApprovalTaskOperationPathParameters, query: GetProjectApprovalTaskOperationQuery = {}) =>
        humanApiRoute("getProjectApprovalTask", pathParameters, query),
    listProjectHandoffs: (pathParameters: ListProjectHandoffsOperationPathParameters, query: ListProjectHandoffsOperationQuery = {}) =>
        humanApiRoute("listProjectHandoffs", pathParameters, query),
    getProjectHandoff: (pathParameters: GetProjectHandoffOperationPathParameters, query: GetProjectHandoffOperationQuery = {}) =>
        humanApiRoute("getProjectHandoff", pathParameters, query),
    decideProjectAgentApproval: (pathParameters: DecideProjectAgentApprovalOperationPathParameters, query: DecideProjectAgentApprovalOperationQuery = {}) =>
        humanApiRoute("decideProjectAgentApproval", pathParameters, query),
    takeOverProjectAgentRun: (pathParameters: TakeOverProjectAgentRunOperationPathParameters, query: TakeOverProjectAgentRunOperationQuery = {}) =>
        humanApiRoute("takeOverProjectAgentRun", pathParameters, query),
    getProjectIntakeConfiguration: (pathParameters: GetProjectIntakeConfigurationOperationPathParameters, query: GetProjectIntakeConfigurationOperationQuery = {}) =>
        humanApiRoute("getProjectIntakeConfiguration", pathParameters, query),
    listProjectSLAConfigs: (pathParameters: ListProjectSLAConfigsOperationPathParameters, query: ListProjectSLAConfigsOperationQuery = {}) =>
        humanApiRoute("listProjectSLAConfigs", pathParameters, query),
    createProjectSLAConfig: (pathParameters: CreateProjectSLAConfigOperationPathParameters, query: CreateProjectSLAConfigOperationQuery = {}) =>
        humanApiRoute("createProjectSLAConfig", pathParameters, query),
    listProjectTicketTemplates: (pathParameters: ListProjectTicketTemplatesOperationPathParameters, query: ListProjectTicketTemplatesOperationQuery = {}) =>
        humanApiRoute("listProjectTicketTemplates", pathParameters, query),
    createProjectTicketTemplate: (pathParameters: CreateProjectTicketTemplateOperationPathParameters, query: CreateProjectTicketTemplateOperationQuery = {}) =>
        humanApiRoute("createProjectTicketTemplate", pathParameters, query),
    getProjectTicketTemplate: (pathParameters: GetProjectTicketTemplateOperationPathParameters, query: GetProjectTicketTemplateOperationQuery = {}) =>
        humanApiRoute("getProjectTicketTemplate", pathParameters, query),
    listProjectQuickReplies: (pathParameters: ListProjectQuickRepliesOperationPathParameters, query: ListProjectQuickRepliesOperationQuery = {}) =>
        humanApiRoute("listProjectQuickReplies", pathParameters, query),
    createProjectQuickReply: (pathParameters: CreateProjectQuickReplyOperationPathParameters, query: CreateProjectQuickReplyOperationQuery = {}) =>
        humanApiRoute("createProjectQuickReply", pathParameters, query),
    useProjectQuickReply: (pathParameters: UseProjectQuickReplyOperationPathParameters, query: UseProjectQuickReplyOperationQuery = {}) =>
        humanApiRoute("useProjectQuickReply", pathParameters, query),
    listProjectKnowledgeArticles: (pathParameters: ListProjectKnowledgeArticlesOperationPathParameters, query: ListProjectKnowledgeArticlesOperationQuery = {}) =>
        humanApiRoute("listProjectKnowledgeArticles", pathParameters, query),
    createProjectKnowledgeArticle: (pathParameters: CreateProjectKnowledgeArticleOperationPathParameters, query: CreateProjectKnowledgeArticleOperationQuery = {}) =>
        humanApiRoute("createProjectKnowledgeArticle", pathParameters, query),
    createProjectKnowledgeArticleDraft: (pathParameters: CreateProjectKnowledgeArticleDraftOperationPathParameters, query: CreateProjectKnowledgeArticleDraftOperationQuery = {}) =>
        humanApiRoute("createProjectKnowledgeArticleDraft", pathParameters, query),
    getProjectKnowledgeArticleDocument: (pathParameters: GetProjectKnowledgeArticleDocumentOperationPathParameters, query: GetProjectKnowledgeArticleDocumentOperationQuery = {}) =>
        humanApiRoute("getProjectKnowledgeArticleDocument", pathParameters, query),
    listProjectKnowledgeVersions: (pathParameters: ListProjectKnowledgeVersionsOperationPathParameters, query: ListProjectKnowledgeVersionsOperationQuery = {}) =>
        humanApiRoute("listProjectKnowledgeVersions", pathParameters, query),
    publishProjectKnowledgeVersion: (pathParameters: PublishProjectKnowledgeVersionOperationPathParameters, query: PublishProjectKnowledgeVersionOperationQuery = {}) =>
        humanApiRoute("publishProjectKnowledgeVersion", pathParameters, query),
    searchProjectKnowledge: (pathParameters: SearchProjectKnowledgeOperationPathParameters, query: SearchProjectKnowledgeOperationQuery = {}) =>
        humanApiRoute("searchProjectKnowledge", pathParameters, query),
    listProjectKnowledgeIngestions: (pathParameters: ListProjectKnowledgeIngestionsOperationPathParameters, query: ListProjectKnowledgeIngestionsOperationQuery = {}) =>
        humanApiRoute("listProjectKnowledgeIngestions", pathParameters, query),
    getProjectKnowledgeIndexState: (pathParameters: GetProjectKnowledgeIndexStateOperationPathParameters, query: GetProjectKnowledgeIndexStateOperationQuery = {}) =>
        humanApiRoute("getProjectKnowledgeIndexState", pathParameters, query),
    rebuildProjectKnowledgeIndex: (pathParameters: RebuildProjectKnowledgeIndexOperationPathParameters, query: RebuildProjectKnowledgeIndexOperationQuery = {}) =>
        humanApiRoute("rebuildProjectKnowledgeIndex", pathParameters, query),
    listMyProjectTickets: (pathParameters: ListMyProjectTicketsOperationPathParameters, query: ListMyProjectTicketsOperationQuery = {}) =>
        humanApiRoute("listMyProjectTickets", pathParameters, query),
    listUnassignedProjectTickets: (pathParameters: ListUnassignedProjectTicketsOperationPathParameters, query: ListUnassignedProjectTicketsOperationQuery = {}) =>
        humanApiRoute("listUnassignedProjectTickets", pathParameters, query),
    listProjectIntegrationConnectorDefinitions: (pathParameters: ListProjectIntegrationConnectorDefinitionsOperationPathParameters, query: ListProjectIntegrationConnectorDefinitionsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationConnectorDefinitions", pathParameters, query),
    createProjectIntegrationConnectorDefinition: (pathParameters: CreateProjectIntegrationConnectorDefinitionOperationPathParameters, query: CreateProjectIntegrationConnectorDefinitionOperationQuery = {}) =>
        humanApiRoute("createProjectIntegrationConnectorDefinition", pathParameters, query),
    updateProjectIntegrationConnectorDefinition: (pathParameters: UpdateProjectIntegrationConnectorDefinitionOperationPathParameters, query: UpdateProjectIntegrationConnectorDefinitionOperationQuery = {}) =>
        humanApiRoute("updateProjectIntegrationConnectorDefinition", pathParameters, query),
    listProjectIntegrationConnections: (pathParameters: ListProjectIntegrationConnectionsOperationPathParameters, query: ListProjectIntegrationConnectionsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationConnections", pathParameters, query),
    createProjectIntegrationConnection: (pathParameters: CreateProjectIntegrationConnectionOperationPathParameters, query: CreateProjectIntegrationConnectionOperationQuery = {}) =>
        humanApiRoute("createProjectIntegrationConnection", pathParameters, query),
    updateProjectIntegrationConnection: (pathParameters: UpdateProjectIntegrationConnectionOperationPathParameters, query: UpdateProjectIntegrationConnectionOperationQuery = {}) =>
        humanApiRoute("updateProjectIntegrationConnection", pathParameters, query),
    listProjectIntegrationMappings: (pathParameters: ListProjectIntegrationMappingsOperationPathParameters, query: ListProjectIntegrationMappingsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationMappings", pathParameters, query),
    createProjectIntegrationMapping: (pathParameters: CreateProjectIntegrationMappingOperationPathParameters, query: CreateProjectIntegrationMappingOperationQuery = {}) =>
        humanApiRoute("createProjectIntegrationMapping", pathParameters, query),
    updateProjectIntegrationMapping: (pathParameters: UpdateProjectIntegrationMappingOperationPathParameters, query: UpdateProjectIntegrationMappingOperationQuery = {}) =>
        humanApiRoute("updateProjectIntegrationMapping", pathParameters, query),
    dryRunProjectIntegrationMapping: (pathParameters: DryRunProjectIntegrationMappingOperationPathParameters, query: DryRunProjectIntegrationMappingOperationQuery = {}) =>
        humanApiRoute("dryRunProjectIntegrationMapping", pathParameters, query),
    publishProjectIntegrationMapping: (pathParameters: PublishProjectIntegrationMappingOperationPathParameters, query: PublishProjectIntegrationMappingOperationQuery = {}) =>
        humanApiRoute("publishProjectIntegrationMapping", pathParameters, query),
    getProjectIntegrationOverview: (pathParameters: GetProjectIntegrationOverviewOperationPathParameters, query: GetProjectIntegrationOverviewOperationQuery = {}) =>
        humanApiRoute("getProjectIntegrationOverview", pathParameters, query),
    listProjectIntegrationInboxMessages: (pathParameters: ListProjectIntegrationInboxMessagesOperationPathParameters, query: ListProjectIntegrationInboxMessagesOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationInboxMessages", pathParameters, query),
    listProjectIntegrationInboxReceipts: (pathParameters: ListProjectIntegrationInboxReceiptsOperationPathParameters, query: ListProjectIntegrationInboxReceiptsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationInboxReceipts", pathParameters, query),
    listProjectIntegrationSyncRuns: (pathParameters: ListProjectIntegrationSyncRunsOperationPathParameters, query: ListProjectIntegrationSyncRunsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationSyncRuns", pathParameters, query),
    listProjectIntegrationConflicts: (pathParameters: ListProjectIntegrationConflictsOperationPathParameters, query: ListProjectIntegrationConflictsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationConflicts", pathParameters, query),
    resolveProjectIntegrationConflict: (pathParameters: ResolveProjectIntegrationConflictOperationPathParameters, query: ResolveProjectIntegrationConflictOperationQuery = {}) =>
        humanApiRoute("resolveProjectIntegrationConflict", pathParameters, query),
    listProjectIntegrationDeadLetters: (pathParameters: ListProjectIntegrationDeadLettersOperationPathParameters, query: ListProjectIntegrationDeadLettersOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationDeadLetters", pathParameters, query),
    replayProjectIntegrationDeadLetter: (pathParameters: ReplayProjectIntegrationDeadLetterOperationPathParameters, query: ReplayProjectIntegrationDeadLetterOperationQuery = {}) =>
        humanApiRoute("replayProjectIntegrationDeadLetter", pathParameters, query),
    listProjectIntegrationDomainEvents: (pathParameters: ListProjectIntegrationDomainEventsOperationPathParameters, query: ListProjectIntegrationDomainEventsOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationDomainEvents", pathParameters, query),
    listProjectIntegrationOutboxDeliveries: (pathParameters: ListProjectIntegrationOutboxDeliveriesOperationPathParameters, query: ListProjectIntegrationOutboxDeliveriesOperationQuery = {}) =>
        humanApiRoute("listProjectIntegrationOutboxDeliveries", pathParameters, query),
} as const
