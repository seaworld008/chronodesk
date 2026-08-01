import React from 'react'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import {
  humanApiRoutes,
  type ListProjectIntegrationConflictsOperationQuery,
  type ListProjectIntegrationConnectionsOperationQuery,
  type ListProjectIntegrationConnectorDefinitionsOperationQuery,
  type ListProjectIntegrationDeadLettersOperationQuery,
  type ListProjectIntegrationInboxMessagesOperationQuery,
  type ListProjectIntegrationInboxReceiptsOperationQuery,
  type ListProjectIntegrationMappingsOperationQuery,
  type ListProjectIntegrationOutboxDeliveriesOperationQuery,
  type ListProjectIntegrationSyncRunsOperationQuery,
} from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import { projectScopeChangedEvent } from '@/lib/projectScopeEvents'
import type {
  CursorPage,
  DirectoryPage,
  DomainEventSummary,
  IntegrationOverview,
} from './integrationTypes'

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null

const parseDirectoryPage = <T,>(
  value: unknown,
  expectedPage: number,
  expectedPageSize: number,
): DirectoryPage<T> => {
  if (
    !isRecord(value)
    || !Array.isArray(value.items)
    || !Number.isSafeInteger(value.total)
    || Number(value.total) < 0
    || value.page !== expectedPage
    || value.page_size !== expectedPageSize
    || !Number.isSafeInteger(value.total_pages)
    || Number(value.total_pages) < 0
    || value.items.length > expectedPageSize
    || value.total_pages !== (
      value.total === 0
        ? 0
        : Math.ceil(Number(value.total) / expectedPageSize)
    )
  ) {
    throw new Error('集成列表响应格式无效')
  }
  return value as unknown as DirectoryPage<T>
}

const parseEventPage = (
  value: unknown,
): CursorPage<DomainEventSummary> => {
  if (
    !isRecord(value)
    || !Array.isArray(value.items)
    || value.items.length > 25
    || typeof value.next_cursor !== 'string'
    || typeof value.has_more !== 'boolean'
    || (value.has_more && value.next_cursor === '')
  ) {
    throw new Error('领域事件响应格式无效')
  }
  return value as unknown as CursorPage<DomainEventSummary>
}

export const useIntegrationProjectKey = () => {
  const [projectKey, setProjectKey] = React.useState('')
  const [error, setError] = React.useState('')

  React.useEffect(() => {
    let active = true
    void resolveActiveProjectKey()
      .then((key) => {
        if (active) {
          setProjectKey(key)
          setError('')
        }
      })
      .catch((requestError) => {
        if (active) {
          setError(localizedUnknownErrorMessage(
            requestError,
            '无法解析当前项目，请重新选择项目',
          ))
        }
      })
    const handleScopeChange = (event: Event) => {
      const key = event instanceof CustomEvent
        && typeof event.detail?.project_key === 'string'
        ? event.detail.project_key.trim()
        : ''
      setProjectKey(key)
      setError(key ? '' : '无法解析当前项目，请重新选择项目')
    }
    window.addEventListener(projectScopeChangedEvent, handleScopeChange)
    return () => {
      active = false
      window.removeEventListener(projectScopeChangedEvent, handleScopeChange)
    }
  }, [])

  return { projectKey, projectError: error }
}

export const useDebouncedValue = <T,>(value: T, delay = 300) => {
  const [debounced, setDebounced] = React.useState(value)
  React.useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay)
    return () => window.clearTimeout(timer)
  }, [delay, value])
  return debounced
}

export interface IntegrationPageQuery {
  search?: string
  status?: string
  typeField?: string
  typeValue?: string
  connectionId?: string
  sortBy?: string
  sortOrder?: 'asc' | 'desc'
}

export type IntegrationPageResource =
  | 'connector-definitions'
  | 'connections'
  | 'mappings'
  | 'inbox'
  | 'inbox-receipts'
  | 'sync-runs'
  | 'conflicts'
  | 'dead-letters'
  | 'outbox'

const integrationPageRoute = (
  projectKey: string,
  resource: IntegrationPageResource,
  resourceID: string,
  query: IntegrationPageQuery,
  page: number,
  pageSize: number,
) => {
  const common = {
    page,
    page_size: pageSize,
    sort_by: query.sortBy ?? 'created_at',
    sort_order: query.sortOrder ?? 'desc',
    search: query.search?.trim() || undefined,
    status: query.status || undefined,
  }
  switch (resource) {
    case 'connector-definitions':
      return humanApiRoutes.listProjectIntegrationConnectorDefinitions(
        { projectKey },
        common as ListProjectIntegrationConnectorDefinitionsOperationQuery,
      )
    case 'connections':
      return humanApiRoutes.listProjectIntegrationConnections(
        { projectKey },
        common as ListProjectIntegrationConnectionsOperationQuery,
      )
    case 'mappings':
      return humanApiRoutes.listProjectIntegrationMappings(
        { projectKey, connectionID: resourceID },
        common as ListProjectIntegrationMappingsOperationQuery,
      )
    case 'inbox':
      return humanApiRoutes.listProjectIntegrationInboxMessages(
        { projectKey },
        {
          ...common,
          connection_id: query.connectionId || undefined,
        } as ListProjectIntegrationInboxMessagesOperationQuery,
      )
    case 'inbox-receipts':
      return humanApiRoutes.listProjectIntegrationInboxReceipts(
        { projectKey, messageID: resourceID },
        {
          page,
          page_size: pageSize,
          sort_by: query.sortBy ?? 'created_at',
          sort_order: query.sortOrder ?? 'desc',
          status: query.status || undefined,
        } as ListProjectIntegrationInboxReceiptsOperationQuery,
      )
    case 'sync-runs':
      return humanApiRoutes.listProjectIntegrationSyncRuns(
        { projectKey },
        {
          ...common,
          direction: query.typeField === 'direction'
            ? query.typeValue || undefined
            : undefined,
          connection_id: query.connectionId || undefined,
        } as ListProjectIntegrationSyncRunsOperationQuery,
      )
    case 'conflicts':
      return humanApiRoutes.listProjectIntegrationConflicts(
        { projectKey },
        {
          ...common,
          type: query.typeField === 'type'
            ? query.typeValue || undefined
            : undefined,
        } as ListProjectIntegrationConflictsOperationQuery,
      )
    case 'dead-letters':
      return humanApiRoutes.listProjectIntegrationDeadLetters(
        { projectKey },
        common as ListProjectIntegrationDeadLettersOperationQuery,
      )
    case 'outbox':
      return humanApiRoutes.listProjectIntegrationOutboxDeliveries(
        { projectKey },
        {
          ...common,
          destination_type: query.typeField === 'destination_type'
            ? query.typeValue || undefined
            : undefined,
        } as ListProjectIntegrationOutboxDeliveriesOperationQuery,
      )
  }
}

export const useIntegrationPage = <T,>(
  projectKey: string,
  resource: IntegrationPageResource,
  query: IntegrationPageQuery,
  enabled = true,
  resourceID = '',
) => {
  const [data, setData] = React.useState<DirectoryPage<T> | null>(null)
  const [page, setPage] = React.useState(0)
  const [pageSize, setPageSize] = React.useState(25)
  const [loading, setLoading] = React.useState(false)
  const [error, setError] = React.useState('')
  const [refreshVersion, setRefreshVersion] = React.useState(0)
  const filterSignature = JSON.stringify(query)
  const requestIdentity =
    `${projectKey}\u0000${resource}\u0000${resourceID}\u0000${filterSignature}`
  const renderedIdentityRef = React.useRef(requestIdentity)
  const identityChanged = renderedIdentityRef.current !== requestIdentity
  const requestPage = identityChanged ? 0 : page
  const controllerRef = React.useRef<AbortController | null>(null)

  React.useEffect(() => {
    renderedIdentityRef.current = requestIdentity
    setPage(0)
    setData(null)
  }, [requestIdentity])

  React.useEffect(() => {
    if (!enabled || !projectKey) {
      controllerRef.current?.abort()
      setLoading(false)
      return
    }
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setLoading(true)
    setError('')
    const path = integrationPageRoute(
      projectKey,
      resource,
      resourceID,
      query,
      requestPage + 1,
      pageSize,
    )
    void apiFetch<unknown>(path, { signal: controller.signal })
      .then((response) => {
        if (!controller.signal.aborted) {
          const parsed = parseDirectoryPage<T>(
            response,
            requestPage + 1,
            pageSize,
          )
          setData(parsed)
          if (
            parsed.total_pages > 0
            && requestPage + 1 > parsed.total_pages
          ) setPage(parsed.total_pages - 1)
        }
      })
      .catch((requestError) => {
        if (!controller.signal.aborted) {
          setError(localizedUnknownErrorMessage(
            requestError,
            '集成列表加载失败，请稍后重试',
          ))
        }
      })
      .finally(() => {
        if (
          !controller.signal.aborted
          && controllerRef.current === controller
        ) setLoading(false)
      })
    return () => controller.abort()
  }, [
    enabled,
    filterSignature,
    page,
    pageSize,
    projectKey,
    query,
    refreshVersion,
    requestIdentity,
    requestPage,
    resource,
    resourceID,
  ])

  return {
    data,
    page,
    pageSize,
    loading,
    error,
    setPage,
    setPageSize: (next: number) => {
      setPage(0)
      setPageSize(next)
    },
    refresh: () => setRefreshVersion((value) => value + 1),
  }
}

export const useIntegrationEvents = (
  projectKey: string,
  search: string,
  eventType: string,
  enabled: boolean,
) => {
  const [data, setData] = React.useState<CursorPage<DomainEventSummary> | null>(null)
  const [cursor, setCursor] = React.useState('')
  const [history, setHistory] = React.useState<string[]>([])
  const [loading, setLoading] = React.useState(false)
  const [error, setError] = React.useState('')
  const [refreshVersion, setRefreshVersion] = React.useState(0)
  const requestIdentity = `${projectKey}\u0000${search}\u0000${eventType}`
  const renderedIdentityRef = React.useRef(requestIdentity)
  const identityChanged = renderedIdentityRef.current !== requestIdentity
  const requestCursor = identityChanged ? '' : cursor

  React.useEffect(() => {
    renderedIdentityRef.current = requestIdentity
    setCursor('')
    setHistory([])
    setData(null)
  }, [requestIdentity])

  React.useEffect(() => {
    if (!enabled || !projectKey) return
    const controller = new AbortController()
    setLoading(true)
    setError('')
    const path = humanApiRoutes.listProjectIntegrationDomainEvents(
      { projectKey },
      {
        cursor: requestCursor || undefined,
        limit: 25,
        event_type: eventType || undefined,
        search: search.trim() || undefined,
      },
    )
    void apiFetch<unknown>(path, {
      signal: controller.signal,
    })
      .then((response) => {
        if (!controller.signal.aborted) setData(parseEventPage(response))
      })
      .catch((requestError) => {
        if (!controller.signal.aborted) {
          setError(localizedUnknownErrorMessage(
            requestError,
            '领域事件加载失败，请稍后重试',
          ))
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [
    cursor,
    enabled,
    eventType,
    projectKey,
    refreshVersion,
    requestCursor,
    requestIdentity,
    search,
  ])

  return {
    data,
    loading,
    error,
    previous: () => {
      const next = [...history]
      setCursor(next.pop() ?? '')
      setHistory(next)
    },
    next: () => {
      if (!data?.next_cursor) return
      setHistory((current) => [...current, cursor])
      setCursor(data.next_cursor)
    },
    canPrevious: history.length > 0,
    refresh: () => setRefreshVersion((value) => value + 1),
  }
}

export const useIntegrationOverview = (projectKey: string) => {
  const [data, setData] = React.useState<IntegrationOverview | null>(null)
  React.useEffect(() => {
    if (!projectKey) return
    const controller = new AbortController()
    void apiFetch<IntegrationOverview>(
      humanApiRoutes.getProjectIntegrationOverview({ projectKey }),
      { signal: controller.signal },
    ).then((response) => {
      if (!controller.signal.aborted) setData(response)
    }).catch(() => {
      if (!controller.signal.aborted) setData(null)
    })
    return () => controller.abort()
  }, [projectKey])
  return data
}
