import { Chip, Tooltip } from '@mui/material'
import {
  InlineDetails,
  TruncatedText,
} from '@/components/tables/EnterpriseTable'
import { StatusChip, type IntegrationColumn } from './IntegrationTable'
import type {
  ConflictSummary,
  ConnectionSummary,
  ConnectorDefinitionSummary,
  DeadLetterSummary,
  DomainEventSummary,
  InboxMessageSummary,
  InboxReceiptSummary,
  MappingSummary,
  OutboxSummary,
  SyncRunSummary,
} from './integrationTypes'

const formatDate = (value?: string) => value
  ? new Date(value).toLocaleString('zh-CN', { hour12: false })
  : '—'

const text = (value: string, fallback = '—') => (
  <TruncatedText title={value || fallback}>{value || fallback}</TruncatedText>
)

export const connectorColumns: IntegrationColumn<ConnectorDefinitionSummary>[] = [
  {
    key: 'connector',
    label: '连接器',
    defaultWidth: 280,
    minWidth: 200,
    maxWidth: 440,
    render: (row) => (
      <InlineDetails
        primary={row.name}
        secondary={row.key}
        title={`${row.name} · ${row.key}`}
      />
    ),
  },
  {
    key: 'kind',
    label: '类型 / 方向',
    defaultWidth: 220,
    minWidth: 160,
    maxWidth: 320,
    render: (row) => (
      <InlineDetails
        primary={row.kind}
        secondary={row.direction}
        title={`${row.kind} · ${row.direction}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'signature',
    label: '签名方案',
    defaultWidth: 180,
    minWidth: 140,
    maxWidth: 280,
    render: (row) => text(row.signature_scheme),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'updated',
    label: '更新时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.updated_at),
  },
]

export const connectionColumns: IntegrationColumn<ConnectionSummary>[] = [
  {
    key: 'connection',
    label: '连接',
    defaultWidth: 300,
    minWidth: 220,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.name}
        secondary={row.key}
        title={`${row.name} · ${row.key}`}
      />
    ),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'credentials',
    label: '配置 / 验证密钥',
    defaultWidth: 200,
    minWidth: 160,
    maxWidth: 300,
    render: (row) => (
      <InlineDetails
        primary={row.has_configuration ? '已配置' : '未配置'}
        secondary={row.has_verification_key ? '密钥已托管' : '无验证密钥'}
        title={`配置：${row.has_configuration ? '已配置' : '未配置'} · 验证密钥：${row.has_verification_key ? '已托管' : '无'}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'health',
    label: '最近健康状态',
    defaultWidth: 260,
    minWidth: 180,
    maxWidth: 420,
    render: (row) => (
      <InlineDetails
        primary={formatDate(row.last_verified_at)}
        secondary={row.last_error_code || '无错误'}
        title={`最近验证：${formatDate(row.last_verified_at)} · ${row.last_error_code || '无错误'}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'updated',
    label: '更新时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.updated_at),
  },
]

export const mappingColumns: IntegrationColumn<MappingSummary>[] = [
  {
    key: 'mapping',
    label: '映射',
    defaultWidth: 260,
    minWidth: 180,
    maxWidth: 420,
    render: (row) => (
      <InlineDetails
        primary={row.key}
        secondary={`v${row.version}`}
        title={`${row.key} · v${row.version}`}
      />
    ),
  },
  {
    key: 'command',
    label: '目标命令',
    defaultWidth: 220,
    minWidth: 160,
    maxWidth: 360,
    render: (row) => text(row.target_command),
  },
  {
    key: 'digest',
    label: '定义摘要',
    defaultWidth: 260,
    minWidth: 180,
    maxWidth: 420,
    render: (row) => text(row.definition_digest),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'updated',
    label: '更新时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.updated_at),
  },
]

export const inboxColumns: IntegrationColumn<InboxMessageSummary>[] = [
  {
    key: 'message',
    label: '外部消息',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.external_message_id}
        secondary={`连接 #${row.connection_id}`}
        title={`${row.external_message_id} · 连接 #${row.connection_id}`}
      />
    ),
  },
  {
    key: 'resource',
    label: '外部资源',
    defaultWidth: 280,
    minWidth: 180,
    maxWidth: 460,
    render: (row) => (
      <InlineDetails
        primary={row.external_resource_type}
        secondary={row.external_resource_id}
        title={`${row.external_resource_type} · ${row.external_resource_id}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 128,
    minWidth: 104,
    maxWidth: 200,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'content',
    label: '内容类型',
    defaultWidth: 200,
    minWidth: 140,
    maxWidth: 320,
    render: (row) => text(row.content_type),
  },
  {
    key: 'received',
    label: '接收时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.received_at),
  },
]

export const receiptColumns: IntegrationColumn<InboxReceiptSummary>[] = [
  {
    key: 'receipt',
    label: '回执',
    defaultWidth: 280,
    minWidth: 180,
    maxWidth: 440,
    render: (row) => (
      <InlineDetails
        primary={row.resource_type}
        secondary={row.resource_id}
        title={`${row.resource_type} · ${row.resource_id}`}
      />
    ),
  },
  {
    key: 'status',
    label: '结果',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'operation',
    label: '操作 / 事件',
    defaultWidth: 320,
    minWidth: 200,
    maxWidth: 520,
    render: (row) => (
      <InlineDetails
        primary={row.operation_id || '无操作 ID'}
        secondary={row.event_id || '无事件 ID'}
        title={`操作：${row.operation_id || '—'} · 事件：${row.event_id || '—'}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'actor',
    label: '操作者',
    defaultWidth: 220,
    minWidth: 160,
    maxWidth: 360,
    render: (row) => text(`${row.actor_type}:${row.actor_id}`),
  },
  {
    key: 'processed',
    label: '处理时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.processed_at),
  },
]

export const syncColumns: IntegrationColumn<SyncRunSummary>[] = [
  {
    key: 'run',
    label: '同步运行',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.run_key}
        secondary={row.direction}
        title={`${row.run_key} · ${row.direction}`}
      />
    ),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'counts',
    label: '处理 / 成功 / 失败 / 冲突',
    defaultWidth: 260,
    minWidth: 200,
    maxWidth: 380,
    render: (row) => text(
      `${row.processed_count} / ${row.succeeded_count} / ${row.failed_count} / ${row.conflict_count}`,
    ),
  },
  {
    key: 'error',
    label: '错误代码',
    defaultWidth: 220,
    minWidth: 160,
    maxWidth: 360,
    render: (row) => text(row.error_code ?? ''),
  },
  {
    key: 'finished',
    label: '完成时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.finished_at),
  },
]

export const conflictColumns: IntegrationColumn<ConflictSummary>[] = [
  {
    key: 'resource',
    label: '冲突资源',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.external_resource_type}
        secondary={row.external_resource_id}
        title={`${row.external_resource_type} · ${row.external_resource_id}`}
      />
    ),
  },
  {
    key: 'type',
    label: '冲突类型',
    defaultWidth: 260,
    minWidth: 180,
    maxWidth: 420,
    render: (row) => text(row.type),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'internal',
    label: '现有 / 新资源',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.existing_internal_resource_id || '—'}
        secondary={row.incoming_internal_resource_id || '—'}
        title={`现有：${row.existing_internal_resource_id || '—'} · 新资源：${row.incoming_internal_resource_id || '—'}`}
        primaryFontWeight={400}
      />
    ),
  },
  {
    key: 'created',
    label: '发现时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.created_at),
  },
]

export const deadLetterColumns: IntegrationColumn<DeadLetterSummary>[] = [
  {
    key: 'letter',
    label: '死信',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => (
      <InlineDetails
        primary={row.reason_code}
        secondary={`连接 #${row.connection_id}`}
        title={`${row.reason_code} · 连接 #${row.connection_id}`}
      />
    ),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'attempts',
    label: '尝试次数',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => row.attempt_count,
  },
  {
    key: 'retry',
    label: '下次尝试',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.next_attempt_at),
  },
  {
    key: 'created',
    label: '创建时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.created_at),
  },
]

export const eventColumns: IntegrationColumn<DomainEventSummary>[] = [
  {
    key: 'time',
    label: '时间',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.time),
  },
  {
    key: 'type',
    label: '事件类型',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 520,
    render: (row) => text(row.type),
  },
  {
    key: 'subject',
    label: '主题',
    defaultWidth: 320,
    minWidth: 200,
    maxWidth: 560,
    render: (row) => text(row.subject),
  },
  {
    key: 'actor',
    label: '操作者',
    defaultWidth: 240,
    minWidth: 160,
    maxWidth: 400,
    render: (row) => text(`${row.actor_type}:${row.actor_id}`),
  },
  {
    key: 'version',
    label: '版本',
    defaultWidth: 100,
    minWidth: 80,
    maxWidth: 160,
    render: (row) => `v${row.resource_version}`,
  },
]

export const outboxColumns: IntegrationColumn<OutboxSummary>[] = [
  {
    key: 'event',
    label: '事件',
    defaultWidth: 300,
    minWidth: 200,
    maxWidth: 480,
    render: (row) => text(row.event_id),
  },
  {
    key: 'destination',
    label: '投递目标',
    defaultWidth: 220,
    minWidth: 160,
    maxWidth: 360,
    render: (row) => (
      <Tooltip title={`类型代码：${row.destination_type}`}>
        <Chip size="small" label={row.destination_label} variant="outlined" />
      </Tooltip>
    ),
  },
  {
    key: 'status',
    label: '状态',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => <StatusChip value={row.status} />,
  },
  {
    key: 'attempts',
    label: '尝试',
    defaultWidth: 120,
    minWidth: 96,
    maxWidth: 180,
    render: (row) => `${row.attempts} / ${row.max_attempts}`,
  },
  {
    key: 'retry',
    label: '下次尝试',
    defaultWidth: 188,
    minWidth: 160,
    maxWidth: 260,
    render: (row) => formatDate(row.next_attempt_at),
  },
  {
    key: 'error',
    label: '最近错误',
    defaultWidth: 340,
    minWidth: 220,
    maxWidth: 600,
    render: (row) => text(row.last_error ?? ''),
  },
]

export { formatDate }
