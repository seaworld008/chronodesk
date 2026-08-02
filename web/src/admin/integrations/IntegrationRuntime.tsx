import React from 'react'
import { Title, useNotify, usePermissions } from 'react-admin'
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Paper,
  Stack,
  Tab,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import {
  CheckCircleOutlined as ResolveIcon,
  ReceiptLong as ReceiptIcon,
  Replay as ReplayIcon,
  VisibilityOffOutlined as IgnoreIcon,
} from '@mui/icons-material'
import type { AccessPermissions } from '@/lib/accessControl'
import {
  apiFetch,
  localizedUnknownErrorMessage,
} from '@/lib/apiClient'
import {
  humanApiRoutes,
  type AdminOutboxPage,
  type ReplayIntegrationDeadLetterRequest,
  type ResolveIntegrationConflictRequest,
} from '@/lib/generated/human-api'
import {
  hasProjectCapability,
  parseProjectRole,
} from '@/lib/projectScope'
import {
  conflictColumns,
  connectionColumns,
  connectorColumns,
  deadLetterColumns,
  eventColumns,
  inboxColumns,
  mappingColumns,
  outboxColumns,
  receiptColumns,
  syncColumns,
} from './integrationColumns'
import {
  IntegrationDetailsDrawer,
  IntegrationTable,
} from './IntegrationTable'
import {
  IntegrationHeader,
  IntegrationListToolbar,
  IntegrationModeSwitch,
} from './IntegrationToolbar'
import type {
  ConflictSummary,
  ConnectionSummary,
  ConnectorDefinitionSummary,
  DeadLetterSummary,
  DirectoryPage,
  DomainEventSummary,
  InboxMessageSummary,
  InboxReceiptSummary,
  IntegrationDetail,
  MappingSummary,
  OutboxSummary,
  SyncRunSummary,
} from './integrationTypes'
import {
  useDebouncedValue,
  useIntegrationEvents,
  useIntegrationOverview,
  useIntegrationPage,
  useIntegrationProjectKey,
} from './useIntegrationData'

type RuntimeTab =
  | 'connections'
  | 'mappings'
  | 'inbox'
  | 'conflicts'
  | 'dead-letters'
  | 'events'
  | 'outbox'

type Confirmation =
  | { kind: 'resolve'; row: ConflictSummary }
  | { kind: 'ignore'; row: ConflictSummary }
  | { kind: 'replay'; row: DeadLetterSummary }
  | { kind: 'outbox-replay'; row: OutboxSummary }

const runtimeTabs: { value: RuntimeTab; label: string }[] = [
  { value: 'connections', label: '连接' },
  { value: 'mappings', label: '映射' },
  { value: 'inbox', label: 'Inbox 与同步' },
  { value: 'conflicts', label: '冲突' },
  { value: 'dead-letters', label: '死信' },
  { value: 'events', label: '领域事件' },
  { value: 'outbox', label: 'Outbox' },
]

const statusOptions: Record<RuntimeTab, { value: string; label: string }[]> = {
  connections: [
    { value: 'active', label: '活动' },
    { value: 'inactive', label: '停用' },
    { value: 'error', label: '异常' },
    { value: 'archived', label: '已归档' },
  ],
  mappings: [
    { value: 'draft', label: '草稿' },
    { value: 'published', label: '已发布' },
    { value: 'retired', label: '已退役' },
  ],
  inbox: [
    { value: 'processing', label: '处理中' },
    { value: 'completed', label: '已完成' },
    { value: 'conflict', label: '冲突' },
    { value: 'dead_letter', label: '死信' },
  ],
  conflicts: [
    { value: 'open', label: '待处理' },
    { value: 'resolved', label: '已解决' },
    { value: 'ignored', label: '已忽略' },
  ],
  'dead-letters': [
    { value: 'open', label: '开放' },
    { value: 'requeued', label: '已重排' },
    { value: 'resolved', label: '已解决' },
  ],
  events: [],
  outbox: [
    { value: 'pending', label: '等待投递' },
    { value: 'processing', label: '投递中' },
    { value: 'succeeded', label: '成功' },
    { value: 'failed', label: '失败' },
    { value: 'dead', label: '终止' },
  ],
}

const emptyPage = <T,>(items: T[]): DirectoryPage<T> => ({
  items,
  total: items.length,
  page: 1,
  page_size: 25,
  total_pages: items.length > 0 ? 1 : 0,
})

const formatResourceVersion = (version: number) => `"v${version}"`

const newIdempotencyKey = () => {
  if (
    typeof crypto !== 'undefined'
    && typeof crypto.randomUUID === 'function'
  ) return crypto.randomUUID()
  return `integration-outbox-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

const findAdminOutboxDelivery = async (
  projectKey: string,
  deliveryID: string,
  signal: AbortSignal,
) => {
  let page = 1
  while (true) {
    const result = await apiFetch<AdminOutboxPage>(
      humanApiRoutes.listAgentOutboxDeliveries(
        { projectKey },
        {
          page,
          page_size: 100,
          sort_by: 'created_at',
          sort_order: 'desc',
        },
      ),
      { signal },
    )
    const delivery = result.items.find((item) => item.id === deliveryID)
    if (delivery) return delivery
    if (
      result.items.length === 0
      || !Number.isSafeInteger(result.total_pages)
      || page >= result.total_pages
    ) return null
    page += 1
  }
}

const IntegrationRuntime = () => {
  const notify = useNotify()
  const { permissions } = usePermissions<AccessPermissions>()
  const role = parseProjectRole(permissions?.project_role)
  const canManage = role === 'project_admin' || role === 'manager'
  const canReplayOutbox = role === 'project_admin'
    && hasProjectCapability(role, 'manage_agents')
  const { projectKey, projectError } = useIntegrationProjectKey()
  const overview = useIntegrationOverview(projectKey)
  const [tab, setTab] = React.useState<RuntimeTab>('connections')
  const [connectionMode, setConnectionMode] = React.useState('instances')
  const [inboxMode, setInboxMode] = React.useState('messages')
  const [search, setSearch] = React.useState('')
  const [status, setStatus] = React.useState('')
  const [secondary, setSecondary] = React.useState('')
  const [connectionSearch, setConnectionSearch] = React.useState('')
  const [selectedConnection, setSelectedConnection] =
    React.useState<ConnectionSummary | null>(null)
  const [detail, setDetail] = React.useState<{
    title: string
    value: IntegrationDetail
  } | null>(null)
  const [receiptMessage, setReceiptMessage] =
    React.useState<InboxMessageSummary | null>(null)
  const [confirmation, setConfirmation] =
    React.useState<Confirmation | null>(null)
  const [submitting, setSubmitting] = React.useState(false)
  const mutationController = React.useRef<AbortController | null>(null)
  const projectKeyRef = React.useRef(projectKey)
  projectKeyRef.current = projectKey
  const debouncedSearch = useDebouncedValue(search)
  const debouncedConnectionSearch = useDebouncedValue(connectionSearch)

  React.useEffect(() => () => mutationController.current?.abort(), [])
  React.useEffect(() => {
    mutationController.current?.abort()
    setSearch('')
    setStatus('')
    setSecondary('')
    setDetail(null)
    setReceiptMessage(null)
    setConfirmation(null)
    setSelectedConnection(null)
    setConnectionSearch('')
    setSubmitting(false)
  }, [projectKey, tab])

  const basicQuery = React.useMemo(
    () => ({ search: debouncedSearch, status }),
    [debouncedSearch, status],
  )
  const inboxQuery = React.useMemo(
    () => ({
      ...basicQuery,
      sortBy: 'received_at',
      sortOrder: 'desc' as const,
    }),
    [basicQuery],
  )
  const conflictQuery = React.useMemo(
    () => ({
      ...basicQuery,
      typeField: 'type',
      typeValue: secondary,
    }),
    [basicQuery, secondary],
  )
  const outboxQuery = React.useMemo(
    () => ({
      ...basicQuery,
      typeField: 'destination_type',
      typeValue: secondary,
    }),
    [basicQuery, secondary],
  )
  const syncQuery = React.useMemo(
    () => ({
      ...basicQuery,
      typeField: 'direction',
      typeValue: secondary,
    }),
    [basicQuery, secondary],
  )
  const connectionOptionQuery = React.useMemo(
    () => ({ search: debouncedConnectionSearch }),
    [debouncedConnectionSearch],
  )

  const connections = useIntegrationPage<ConnectionSummary>(
    projectKey,
    'connections',
    basicQuery,
    tab === 'connections' && connectionMode === 'instances',
  )
  const connectors = useIntegrationPage<ConnectorDefinitionSummary>(
    projectKey,
    'connector-definitions',
    basicQuery,
    tab === 'connections' && connectionMode === 'definitions',
  )
  const connectionOptions = useIntegrationPage<ConnectionSummary>(
    projectKey,
    'connections',
    connectionOptionQuery,
    tab === 'mappings',
  )
  const mappings = useIntegrationPage<MappingSummary>(
    projectKey,
    'mappings',
    basicQuery,
    tab === 'mappings' && selectedConnection !== null,
    selectedConnection?.id ?? '',
  )
  const inbox = useIntegrationPage<InboxMessageSummary>(
    projectKey,
    'inbox',
    inboxQuery,
    tab === 'inbox' && inboxMode === 'messages',
  )
  const syncRuns = useIntegrationPage<SyncRunSummary>(
    projectKey,
    'sync-runs',
    syncQuery,
    tab === 'inbox' && inboxMode === 'sync',
  )
  const conflicts = useIntegrationPage<ConflictSummary>(
    projectKey,
    'conflicts',
    conflictQuery,
    tab === 'conflicts',
  )
  const deadLetters = useIntegrationPage<DeadLetterSummary>(
    projectKey,
    'dead-letters',
    basicQuery,
    tab === 'dead-letters',
  )
  const outbox = useIntegrationPage<OutboxSummary>(
    projectKey,
    'outbox',
    outboxQuery,
    tab === 'outbox',
  )
  const events = useIntegrationEvents(
    projectKey,
    debouncedSearch,
    '',
    tab === 'events',
  )
  const receipts = useIntegrationPage<InboxReceiptSummary>(
    projectKey,
    'inbox-receipts',
    React.useMemo(() => ({}), []),
    receiptMessage !== null,
    receiptMessage?.id ?? '',
  )

  React.useEffect(() => {
    if (!selectedConnection && connectionOptions.data?.items[0]) {
      setSelectedConnection(connectionOptions.data.items[0])
    }
  }, [connectionOptions.data, selectedConnection])

  const openDetails = React.useCallback(
    (title: string, value: IntegrationDetail) => setDetail({ title, value }),
    [],
  )

  const performConfirmedAction = async () => {
    if (
      !confirmation
      || !projectKey
      || !canManage
      || (confirmation.kind === 'outbox-replay' && !canReplayOutbox)
    ) return
    mutationController.current?.abort()
    const controller = new AbortController()
    mutationController.current = controller
    const capturedProjectKey = projectKey
    setSubmitting(true)
    try {
      if (confirmation.kind === 'outbox-replay') {
        const delivery = await findAdminOutboxDelivery(
          capturedProjectKey,
          confirmation.row.id,
          controller.signal,
        )
        if (!delivery) {
          throw new Error('无法获取投递的最新版本，请刷新列表后重试')
        }
        if (delivery.status !== 'failed' && delivery.status !== 'dead') {
          throw new Error('该投递状态已发生变化，请刷新列表后重试')
        }
        await apiFetch(
          humanApiRoutes.replayOutboxDeliveryV2({
            projectKey: capturedProjectKey,
            deliveryId: delivery.id,
          }),
          {
            method: 'POST',
            headers: {
              'Idempotency-Key': newIdempotencyKey(),
              'If-Match': formatResourceVersion(delivery.resource_version),
            },
            signal: controller.signal,
          },
        )
        if (projectKeyRef.current === capturedProjectKey) {
          notify('事件投递已重新排队', { type: 'success' })
          outbox.refresh()
        }
      } else if (confirmation.kind === 'replay') {
        const body: ReplayIntegrationDeadLetterRequest = {
          expected_updated_at: confirmation.row.updated_at,
        }
        await apiFetch(
          humanApiRoutes.replayProjectIntegrationDeadLetter({
            projectKey: capturedProjectKey,
            deadLetterID: confirmation.row.id,
          }),
          {
            method: 'POST',
            body: JSON.stringify(body),
            signal: controller.signal,
          },
        )
        if (projectKeyRef.current === capturedProjectKey) {
          notify('死信已重新进入处理队列', { type: 'success' })
          deadLetters.refresh()
        }
      } else {
        const body: ResolveIntegrationConflictRequest = {
          resolution: confirmation.kind === 'resolve'
            ? 'resolved'
            : 'ignored',
          expected_updated_at: confirmation.row.updated_at,
        }
        await apiFetch(
          humanApiRoutes.resolveProjectIntegrationConflict({
            projectKey: capturedProjectKey,
            conflictID: confirmation.row.id,
          }),
          {
            method: 'POST',
            body: JSON.stringify(body),
            signal: controller.signal,
          },
        )
        if (projectKeyRef.current === capturedProjectKey) {
          notify(
            confirmation.kind === 'resolve' ? '冲突已解决' : '冲突已忽略',
            { type: 'success' },
          )
          conflicts.refresh()
        }
      }
      if (projectKeyRef.current === capturedProjectKey) setConfirmation(null)
    } catch (requestError) {
      if (!controller.signal.aborted) {
        notify(localizedUnknownErrorMessage(
          requestError,
          '集成恢复操作失败，请刷新后重试',
        ), { type: 'error' })
      }
    } finally {
      if (!controller.signal.aborted) setSubmitting(false)
    }
  }

  const secondaryConfig = tab === 'conflicts'
    ? {
      label: '冲突类型',
      options: [
        { value: 'message_identity_reuse', label: '消息身份复用' },
        { value: 'external_link_mismatch', label: '外部链接不匹配' },
        { value: 'internal_link_collision', label: '内部链接冲突' },
      ],
    }
    : tab === 'outbox'
      ? {
        label: '投递类型',
        options: [
          { value: 'webhook', label: 'Webhook' },
          { value: 'notification', label: '项目通知' },
          { value: 'automation', label: '自动化' },
          { value: 'email', label: '邮件' },
          { value: 'mcp', label: 'MCP' },
          { value: 'a2a', label: 'A2A' },
        ],
      }
      : tab === 'inbox' && inboxMode === 'sync'
        ? {
          label: '同步方向',
          options: [
            { value: 'inbound', label: '入站' },
            { value: 'outbound', label: '出站' },
          ],
        }
        : null

  const activeLoading = {
    connections: connectionMode === 'instances'
      ? connections.loading
      : connectors.loading,
    mappings: mappings.loading || connectionOptions.loading,
    inbox: inboxMode === 'messages' ? inbox.loading : syncRuns.loading,
    conflicts: conflicts.loading,
    'dead-letters': deadLetters.loading,
    events: events.loading,
    outbox: outbox.loading,
  }[tab]
  const refreshActive = {
    connections: connectionMode === 'instances'
      ? connections.refresh
      : connectors.refresh,
    mappings: mappings.refresh,
    inbox: inboxMode === 'messages' ? inbox.refresh : syncRuns.refresh,
    conflicts: conflicts.refresh,
    'dead-letters': deadLetters.refresh,
    events: events.refresh,
    outbox: outbox.refresh,
  }[tab]

  return (
    <>
      <Title title="集成中心" />
      <Box sx={{ p: { xs: 2, md: 3 } }}>
        <IntegrationHeader projectKey={projectKey} overview={overview} />
        {projectError && <Alert severity="error" sx={{ mt: 2 }}>{projectError}</Alert>}
        <Paper variant="outlined" sx={{ mt: 3, minWidth: 0 }}>
          <Tabs
            value={tab}
            onChange={(_, value: RuntimeTab) => setTab(value)}
            variant="scrollable"
            scrollButtons="auto"
            aria-label="集成中心功能"
            sx={{ borderBottom: 1, borderColor: 'divider' }}
          >
            {runtimeTabs.map((item) => (
              <Tab key={item.value} value={item.value} label={item.label} />
            ))}
          </Tabs>
          <Box sx={{ p: { xs: 1.5, md: 2 } }}>
            {tab === 'connections' && (
              <IntegrationModeSwitch
                label="连接视图"
                value={connectionMode}
                onChange={(value) => {
                  setConnectionMode(value)
                  setStatus('')
                }}
                options={[
                  { value: 'instances', label: '连接实例' },
                  { value: 'definitions', label: '连接器定义' },
                ]}
              />
            )}
            {tab === 'inbox' && (
              <IntegrationModeSwitch
                label="Inbox 与同步视图"
                value={inboxMode}
                onChange={(value) => {
                  setInboxMode(value)
                  setStatus('')
                  setSecondary('')
                }}
                options={[
                  { value: 'messages', label: 'Inbox 消息' },
                  { value: 'sync', label: '同步运行' },
                ]}
              />
            )}
            {tab !== 'mappings' && (
              <IntegrationListToolbar
                search={search}
                onSearchChange={setSearch}
                status={status}
                onStatusChange={setStatus}
                statuses={tab === 'connections' && connectionMode === 'definitions'
                  ? [
                    { value: 'active', label: '活动' },
                    { value: 'disabled', label: '停用' },
                    { value: 'archived', label: '已归档' },
                  ]
                  : tab === 'inbox' && inboxMode === 'sync'
                    ? [
                      { value: 'pending', label: '等待' },
                      { value: 'running', label: '运行中' },
                      { value: 'succeeded', label: '成功' },
                      { value: 'failed', label: '失败' },
                      { value: 'conflict', label: '冲突' },
                      { value: 'cancelled', label: '已取消' },
                    ]
                    : statusOptions[tab]}
                secondaryLabel={secondaryConfig?.label}
                secondaryValue={secondary}
                onSecondaryChange={secondaryConfig ? setSecondary : undefined}
                secondaryOptions={secondaryConfig?.options}
                loading={activeLoading}
                onRefresh={refreshActive}
              />
            )}

            {tab === 'connections' && connectionMode === 'instances' && (
              <IntegrationTable
                tableId="integration.connections"
                ariaLabel="连接实例列表"
                columns={connectionColumns}
                page={connections.data}
                pageIndex={connections.page}
                pageSize={connections.pageSize}
                loading={connections.loading}
                error={connections.error}
                emptyMessage="当前筛选下没有连接实例。"
                onPageChange={connections.setPage}
                onPageSizeChange={connections.setPageSize}
                onRetry={connections.refresh}
                onOpenDetails={(row) => openDetails('连接详情', row)}
              />
            )}
            {tab === 'connections' && connectionMode === 'definitions' && (
              <IntegrationTable
                tableId="integration.connector-definitions"
                ariaLabel="连接器定义列表"
                columns={connectorColumns}
                page={connectors.data}
                pageIndex={connectors.page}
                pageSize={connectors.pageSize}
                loading={connectors.loading}
                error={connectors.error}
                emptyMessage="当前筛选下没有连接器定义。"
                onPageChange={connectors.setPage}
                onPageSizeChange={connectors.setPageSize}
                onRetry={connectors.refresh}
                onOpenDetails={(row) => openDetails('连接器定义详情', row)}
              />
            )}

            {tab === 'mappings' && (
              <>
                <Stack
                  direction={{ xs: 'column', md: 'row' }}
                  spacing={1.5}
                  sx={{ mb: 1.5, alignItems: { md: 'center' } }}
                >
                  <Autocomplete
                    value={selectedConnection}
                    options={connectionOptions.data?.items ?? []}
                    loading={connectionOptions.loading}
                    filterOptions={(options) => options}
                    getOptionLabel={(option) => `${option.name}（${option.key}）`}
                    isOptionEqualToValue={(option, value) => option.id === value.id}
                    onInputChange={(_, value) => setConnectionSearch(value)}
                    onChange={(_, value) => setSelectedConnection(value)}
                    renderInput={(params) => (
                      <TextField
                        {...params}
                        label="选择连接"
                        size="small"
                        slotProps={{
                          inputLabel: params.slotProps.inputLabel,
                          input: params.slotProps.input,
                          htmlInput: {
                            ...params.slotProps.htmlInput,
                            maxLength: 200,
                          },
                        }}
                      />
                    )}
                    sx={{ minWidth: { md: 360 } }}
                  />
                </Stack>
                <IntegrationListToolbar
                  search={search}
                  onSearchChange={setSearch}
                  status={status}
                  onStatusChange={setStatus}
                  statuses={statusOptions.mappings}
                  loading={mappings.loading}
                  onRefresh={mappings.refresh}
                />
                {!selectedConnection ? (
                  <Alert severity="info">请先选择一个连接查看映射版本。</Alert>
                ) : (
                  <IntegrationTable
                    tableId="integration.mappings"
                    ariaLabel="映射版本列表"
                    columns={mappingColumns}
                    page={mappings.data}
                    pageIndex={mappings.page}
                    pageSize={mappings.pageSize}
                    loading={mappings.loading}
                    error={mappings.error}
                    emptyMessage="该连接暂无映射版本。"
                    onPageChange={mappings.setPage}
                    onPageSizeChange={mappings.setPageSize}
                    onRetry={mappings.refresh}
                    onOpenDetails={(row) => openDetails('映射详情', row)}
                  />
                )}
              </>
            )}

            {tab === 'inbox' && inboxMode === 'messages' && (
              <IntegrationTable
                tableId="integration.inbox"
                ariaLabel="Inbox 消息列表"
                columns={inboxColumns}
                page={inbox.data}
                pageIndex={inbox.page}
                pageSize={inbox.pageSize}
                loading={inbox.loading}
                error={inbox.error}
                emptyMessage="当前筛选下没有 Inbox 消息。"
                onPageChange={inbox.setPage}
                onPageSizeChange={inbox.setPageSize}
                onRetry={inbox.refresh}
                onOpenDetails={(row) => openDetails('Inbox 消息详情', row)}
                renderActions={(row) => (
                  <Tooltip title="查看处理回执">
                    <IconButton
                      size="small"
                      aria-label={`查看消息 ${row.external_message_id} 的处理回执`}
                      onClick={() => setReceiptMessage(row)}
                    >
                      <ReceiptIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                )}
              />
            )}
            {tab === 'inbox' && inboxMode === 'sync' && (
              <IntegrationTable
                tableId="integration.sync-runs"
                ariaLabel="同步运行列表"
                columns={syncColumns}
                page={syncRuns.data}
                pageIndex={syncRuns.page}
                pageSize={syncRuns.pageSize}
                loading={syncRuns.loading}
                error={syncRuns.error}
                emptyMessage="当前筛选下没有同步运行记录。"
                onPageChange={syncRuns.setPage}
                onPageSizeChange={syncRuns.setPageSize}
                onRetry={syncRuns.refresh}
                onOpenDetails={(row) => openDetails('同步运行详情', row)}
              />
            )}

            {tab === 'conflicts' && (
              <IntegrationTable
                tableId="integration.conflicts"
                ariaLabel="集成冲突列表"
                columns={conflictColumns}
                page={conflicts.data}
                pageIndex={conflicts.page}
                pageSize={conflicts.pageSize}
                loading={conflicts.loading}
                error={conflicts.error}
                emptyMessage="当前筛选下没有集成冲突。"
                onPageChange={conflicts.setPage}
                onPageSizeChange={conflicts.setPageSize}
                onRetry={conflicts.refresh}
                onOpenDetails={(row) => openDetails('冲突详情', row)}
                renderActions={canManage ? (row) => row.status === 'open' && (
                  <Stack direction="row" spacing={0.5} sx={{ justifyContent: 'flex-end' }}>
                    <Tooltip title="标记为已解决">
                      <IconButton
                        size="small"
                        aria-label={`解决冲突 ${row.id}`}
                        onClick={() => setConfirmation({ kind: 'resolve', row })}
                      >
                        <ResolveIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    <Tooltip title="忽略此冲突">
                      <IconButton
                        size="small"
                        aria-label={`忽略冲突 ${row.id}`}
                        onClick={() => setConfirmation({ kind: 'ignore', row })}
                      >
                        <IgnoreIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </Stack>
                ) : undefined}
              />
            )}

            {tab === 'dead-letters' && (
              <IntegrationTable
                tableId="integration.dead-letters"
                ariaLabel="集成死信列表"
                columns={deadLetterColumns}
                page={deadLetters.data}
                pageIndex={deadLetters.page}
                pageSize={deadLetters.pageSize}
                loading={deadLetters.loading}
                error={deadLetters.error}
                emptyMessage="当前筛选下没有死信。"
                onPageChange={deadLetters.setPage}
                onPageSizeChange={deadLetters.setPageSize}
                onRetry={deadLetters.refresh}
                onOpenDetails={(row) => openDetails('死信详情', row)}
                renderActions={canManage ? (row) => row.status === 'open' && (
                  <Tooltip title="重新处理">
                    <IconButton
                      size="small"
                      aria-label={`重新处理死信 ${row.id}`}
                      onClick={() => setConfirmation({ kind: 'replay', row })}
                    >
                      <ReplayIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                ) : undefined}
              />
            )}

            {tab === 'events' && (
              <IntegrationTable
                tableId="integration.domain-events"
                ariaLabel="领域事件列表"
                columns={eventColumns}
                page={events.data ? emptyPage<DomainEventSummary>(events.data.items) : null}
                pageIndex={0}
                pageSize={25}
                loading={events.loading}
                error={events.error}
                emptyMessage="当前筛选下没有领域事件。"
                onPageChange={() => undefined}
                onPageSizeChange={() => undefined}
                onRetry={events.refresh}
                onOpenDetails={(row) => openDetails('领域事件详情', row)}
                pagination={false}
                footer={(
                  <Stack
                    direction="row"
                    spacing={1}
                    sx={{ ml: 'auto', px: 1 }}
                  >
                    <Button
                      disabled={!events.canPrevious || events.loading}
                      onClick={events.previous}
                    >
                      上一页
                    </Button>
                    <Button
                      disabled={!events.data?.has_more || events.loading}
                      onClick={events.next}
                    >
                      下一页
                    </Button>
                  </Stack>
                )}
              />
            )}

            {tab === 'outbox' && (
              <IntegrationTable
                tableId="integration.outbox"
                ariaLabel="Outbox 投递列表"
                columns={outboxColumns}
                page={outbox.data}
                pageIndex={outbox.page}
                pageSize={outbox.pageSize}
                loading={outbox.loading}
                error={outbox.error}
                emptyMessage="当前筛选下没有 Outbox 投递记录。"
                onPageChange={outbox.setPage}
                onPageSizeChange={outbox.setPageSize}
                onRetry={outbox.refresh}
                onOpenDetails={(row) => openDetails('Outbox 投递详情', row)}
                renderActions={canReplayOutbox ? (row) => (
                  row.status === 'failed' || row.status === 'dead'
                ) && (
                  <Tooltip title="重新投递">
                    <IconButton
                      size="small"
                      aria-label={`重新投递 ${row.id}`}
                      onClick={() => setConfirmation({
                        kind: 'outbox-replay',
                        row,
                      })}
                    >
                      <ReplayIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                ) : undefined}
              />
            )}
          </Box>
        </Paper>
      </Box>

      <IntegrationDetailsDrawer
        title={detail?.title ?? '集成详情'}
        detail={detail?.value ?? null}
        onClose={() => setDetail(null)}
      />

      <Dialog
        open={receiptMessage !== null}
        onClose={() => setReceiptMessage(null)}
        fullWidth
        maxWidth="lg"
        aria-labelledby="integration-receipts-title"
      >
        <DialogTitle id="integration-receipts-title">
          Inbox 处理回执
        </DialogTitle>
        <DialogContent dividers>
          <Typography color="text.secondary" sx={{ mb: 2 }}>
            外部消息：{receiptMessage?.external_message_id ?? '—'}
          </Typography>
          <IntegrationTable
            tableId="integration.inbox-receipts"
            ariaLabel="Inbox 处理回执列表"
            columns={receiptColumns}
            page={receipts.data}
            pageIndex={receipts.page}
            pageSize={receipts.pageSize}
            loading={receipts.loading}
            error={receipts.error}
            emptyMessage="该消息暂无处理回执。"
            onPageChange={receipts.setPage}
            onPageSizeChange={receipts.setPageSize}
            onRetry={receipts.refresh}
            onOpenDetails={(row) => {
              setReceiptMessage(null)
              openDetails('Inbox 回执详情', row)
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setReceiptMessage(null)}>关闭</Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={confirmation !== null}
        onClose={() => {
          if (!submitting) setConfirmation(null)
        }}
        aria-labelledby="integration-confirmation-title"
        aria-describedby="integration-confirmation-description"
      >
        <DialogTitle id="integration-confirmation-title">
          {confirmation?.kind === 'outbox-replay'
            ? '确认重新投递'
            : confirmation?.kind === 'replay'
            ? '确认重新处理死信'
            : confirmation?.kind === 'ignore'
              ? '确认忽略冲突'
              : '确认解决冲突'}
        </DialogTitle>
        <DialogContent>
          <Typography id="integration-confirmation-description">
            {confirmation?.kind === 'outbox-replay'
              ? '该事件投递将重新进入队列。接收端必须按事件 ID 去重，以免产生重复副作用。操作会保留审计记录。'
              : '此操作会写入当前项目的集成运行状态，并保留审计记录。请确认当前筛选和资源标识无误。'}
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button disabled={submitting} onClick={() => setConfirmation(null)}>
            取消
          </Button>
          <Button
            variant="contained"
            disabled={submitting}
            onClick={() => void performConfirmedAction()}
          >
            {submitting
              ? '正在提交…'
              : confirmation?.kind === 'outbox-replay'
                ? '确认重新投递'
                : '确认'}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}

export default IntegrationRuntime
