import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { apiFetch, localizedUnknownErrorMessage } from '@/lib/apiClient'
import { humanApiRoutes } from '@/lib/generated/human-api'
import { resolveActiveProjectKey } from '@/lib/projectScope'
import { projectScopeChangedEvent } from '@/lib/projectScopeEvents'

export interface AutomationDirectoryPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

type AutomationDirectoryFilter = Record<
  string,
  string | boolean | undefined
>

const requestPath = async (
    endpoint: string,
    query?: URLSearchParams,
) => {
  const projectKey = await resolveActiveProjectKey()
  const page = Number(query?.get('page') ?? 1)
  const pageSize = Number(query?.get('page_size') ?? 25)
  const optionalBoolean = (name: string) => {
    const value = query?.get(name)
    return value === 'true' ? true : value === 'false' ? false : undefined
  }
  switch (endpoint) {
    case 'sla':
      return humanApiRoutes.listProjectSLAConfigs(
        { projectKey },
        {
          page,
          page_size: pageSize,
          is_active: optionalBoolean('is_active'),
        },
      )
    case 'templates':
      return humanApiRoutes.listProjectTicketTemplates(
        { projectKey },
        {
          page,
          page_size: pageSize,
          category: query?.get('category') || undefined,
          is_active: optionalBoolean('is_active'),
        },
      )
    case 'quick-replies':
      return humanApiRoutes.listProjectQuickReplies(
        { projectKey },
        {
          page,
          page_size: pageSize,
          category: query?.get('category') || undefined,
          keyword: query?.get('keyword') || undefined,
          is_public: optionalBoolean('is_public'),
        },
      )
    default:
      throw new Error('不支持的自动化目录')
  }
}

export const automationDirectoryCommandPath = async (
  endpoint: string,
) => {
  const projectKey = await resolveActiveProjectKey()
  switch (endpoint) {
    case 'sla':
      return humanApiRoutes.createProjectSLAConfig({ projectKey })
    case 'templates':
      return humanApiRoutes.createProjectTicketTemplate({ projectKey })
    case 'quick-replies':
      return humanApiRoutes.createProjectQuickReply({ projectKey })
    default: {
      const match = /^quick-replies\/([1-9][0-9]*)\/use$/u.exec(endpoint)
      if (!match) throw new Error('不支持的自动化命令')
      return humanApiRoutes.useProjectQuickReply({
        projectKey,
        automationConfigID: Number(match[1]),
      })
    }
  }
}

const isAborted = (error: unknown, signal: AbortSignal) =>
  signal.aborted ||
  (error instanceof DOMException && error.name === 'AbortError')

export const useAutomationDirectory = <T>(
  endpoint: string,
  appliedFilters: AutomationDirectoryFilter,
  errorFallback: string,
) => {
  const [page, setPage] = useState(0)
  const [pageSize, setPageSize] = useState(25)
  const [result, setResult] = useState<AutomationDirectoryPage<T> | null>(
    null,
  )
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [scopeVersion, setScopeVersion] = useState(0)
  const [reloadVersion, setReloadVersion] = useState(0)
  const controller = useRef<AbortController | null>(null)
  const sequence = useRef(0)

  const filterKey = useMemo(
    () => JSON.stringify(appliedFilters),
    [appliedFilters],
  )
  const requestIdentity = `${filterKey}:${reloadVersion}:${scopeVersion}`

  const load = useCallback(async () => {
    // Reading this versioned identity makes retries and scope changes explicit
    // inputs to the cancellable request without leaking either into the URL.
    void requestIdentity
    controller.current?.abort()
    const currentController = new AbortController()
    const currentSequence = sequence.current + 1
    sequence.current = currentSequence
    controller.current = currentController
    setLoading(true)
    setError('')
    try {
      const query = new URLSearchParams({
        page: String(page + 1),
        page_size: String(pageSize),
      })
      Object.entries(appliedFilters).forEach(([key, value]) => {
        if (value !== undefined && value !== '') {
          query.set(key, String(value))
        }
      })
      const path = await requestPath(endpoint, query)
      if (
        currentController.signal.aborted ||
        sequence.current !== currentSequence
      ) return
      const response = await apiFetch<AutomationDirectoryPage<T>>(path, {
        signal: currentController.signal,
      })
      if (
        currentController.signal.aborted ||
        sequence.current !== currentSequence
      ) return
      if (response.total_pages > 0 && page + 1 > response.total_pages) {
        setPage(response.total_pages - 1)
        return
      }
      setResult(response)
    } catch (loadError: unknown) {
      if (
        isAborted(loadError, currentController.signal) ||
        sequence.current !== currentSequence
      ) return
      setResult(null)
      setError(localizedUnknownErrorMessage(loadError, errorFallback))
    } finally {
      if (
        !currentController.signal.aborted &&
        sequence.current === currentSequence
      ) {
        setLoading(false)
      }
    }
  }, [
    appliedFilters,
    endpoint,
    errorFallback,
    page,
    pageSize,
    requestIdentity,
  ])

  useEffect(() => {
    void load()
    return () => controller.current?.abort()
  }, [load])

  useEffect(() => {
    const handleProjectScopeChanged = () => {
      controller.current?.abort()
      sequence.current += 1
      setResult(null)
      setError('')
      setPage(0)
      setScopeVersion((version) => version + 1)
    }
    window.addEventListener(
      projectScopeChangedEvent,
      handleProjectScopeChanged,
    )
    return () => {
      controller.current?.abort()
      sequence.current += 1
      window.removeEventListener(
        projectScopeChangedEvent,
        handleProjectScopeChanged,
      )
    }
  }, [])

  return {
    result,
    loading,
    error,
    page,
    pageSize,
    setPage,
    setPageSize: (nextPageSize: number) => {
      setPage(0)
      setPageSize(nextPageSize)
    },
    resetPage: () => setPage(0),
    retry: () => setReloadVersion((version) => version + 1),
    reload: () => setReloadVersion((version) => version + 1),
  }
}
