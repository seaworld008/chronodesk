import type {
  IntegrationConflictSummary,
  IntegrationConnectionPage,
  IntegrationConnectionSummary,
  IntegrationConnectorDefinitionSummary,
  IntegrationDeadLetterSummary,
  IntegrationDomainEventCursorPage,
  IntegrationDomainEventSummary,
  IntegrationInboxMessageSummary,
  IntegrationInboxReceiptSummary,
  IntegrationMappingSummary,
  IntegrationOutboxSummary,
  IntegrationOverview as GeneratedIntegrationOverview,
  IntegrationSyncRunSummary,
} from '@/lib/generated/human-api'

// The generic wrappers below are UI-only table adapters. Every wire item and
// overview DTO remains generated from the Human OpenAPI contract.
export type DirectoryPage<T> = Omit<IntegrationConnectionPage, 'items'> & {
  items: T[]
}

export type CursorPage<T> = Omit<
  IntegrationDomainEventCursorPage,
  'items'
> & {
  items: T[]
}

export type ConnectorDefinitionSummary =
  IntegrationConnectorDefinitionSummary
export type ConnectionSummary = IntegrationConnectionSummary
export type MappingSummary = IntegrationMappingSummary
export type InboxMessageSummary = IntegrationInboxMessageSummary
export type InboxReceiptSummary = IntegrationInboxReceiptSummary
export type SyncRunSummary = IntegrationSyncRunSummary
export type ConflictSummary = IntegrationConflictSummary
export type DeadLetterSummary = IntegrationDeadLetterSummary
export type DomainEventSummary = IntegrationDomainEventSummary
export type OutboxSummary = IntegrationOutboxSummary
export type IntegrationOverview = GeneratedIntegrationOverview

export type IntegrationDetail =
  | ConnectorDefinitionSummary
  | ConnectionSummary
  | MappingSummary
  | InboxMessageSummary
  | InboxReceiptSummary
  | SyncRunSummary
  | ConflictSummary
  | DeadLetterSummary
  | DomainEventSummary
  | OutboxSummary
