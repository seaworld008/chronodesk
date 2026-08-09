import { HttpError } from 'react-admin'
import {
  shouldRefreshActiveProjectAccessAfterForbidden,
  shouldInvalidateActiveProjectAccess,
  signalProjectAccessInvalidated,
  signalProjectAccessRefreshRequested,
  signalSessionInvalidated,
  signalSessionReplaced,
} from './projectScopeEvents'
import { joinApiUrl } from './apiUrl'
import {
  adoptHumanTabSessionRotation,
  humanTabSessionMatches,
  readCommittedHumanTabSessionToken,
} from './humanTabSession'

export type ApiOptions = RequestInit & { rawResponse?: boolean }

const API_BASE = (import.meta.env.VITE_API_URL ?? '/api').toString().replace(/\/$/, '')

const toUrl = (path: string) => joinApiUrl(API_BASE, path)

const requestPath = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input
  if (input instanceof URL) return input.toString()
  return input.url
}

export const sessionAwareFetch = async (
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> => {
  const requestHeaders = new Headers(
    init?.headers ??
      (input instanceof Request ? input.headers : undefined),
  )
  const authorization = requestHeaders.get('Authorization')
  if (authorization?.startsWith('Bearer ')) {
    const accessToken = authorization.slice('Bearer '.length)
    const committedAccessToken = readCommittedHumanTabSessionToken()
    if (
      committedAccessToken !== accessToken ||
      (
        !humanTabSessionMatches(accessToken) &&
        !adoptHumanTabSessionRotation(accessToken)
      )
    ) {
      signalSessionReplaced()
      throw new Error('登录账号已在其他标签页发生变化，请刷新后继续')
    }
  }
  const response = await fetch(input, init)
  const path = requestPath(input)
  if (response.status === 401) {
    signalSessionInvalidated()
  } else if (response.status === 403) {
    const payload = await response.clone().json().catch(() => null)
    if (shouldInvalidateActiveProjectAccess(path, payload)) {
      signalProjectAccessInvalidated()
    } else if (shouldRefreshActiveProjectAccessAfterForbidden(path)) {
      signalProjectAccessRefreshRequested()
    }
  }
  return response
}

type JsonRecord = Record<string, unknown>

const isJsonRecord = (value: unknown): value is JsonRecord => typeof value === 'object' && value !== null

const problemMessages: Record<string, string> = {
  invalid_request: '请求内容无效，请检查后重试',
  invalid_actor: '操作身份无效，请重新认证后重试',
  invalid_scope: '请求的权限范围无效',
  unauthorized: '登录状态已失效，请重新登录',
  project_access_revoked: '当前项目访问权限已失效',
  insufficient_scope: '当前凭据缺少执行此操作所需的权限范围',
  scope_not_granted: '当前服务主体未被授予所需权限范围',
  scope_allowed: '权限范围校验通过',
  explicit_allow: '显式允许策略已授权本次操作',
  explicit_deny: '显式拒绝策略阻止了本次操作',
  explicit_allow_required: '该风险操作需要配置显式允许策略',
  policy_denied: '安全策略拒绝了本次操作',
  principal_not_found: '服务主体不存在',
  principal_disabled: '服务主体已停用',
  principal_expired: '服务主体已过期',
  invalid_credential: '智能体凭据无效或已被撤销',
  credential_expired: '智能体凭据已过期',
  global_emergency_stop: '智能体全局紧急停止已启用',
  agent_emergency_stop: '智能体紧急停止已启用',
  global_read_only: '智能体全局只读模式已启用',
  principal_read_only: '该智能体当前处于只读模式',
  read_only: '当前处于只读模式，写操作已被拒绝',
  not_found: '请求的资源不存在或当前账号无权访问',
  precondition_required: '数据版本信息缺失，请刷新后重试',
  version_conflict: '数据已被其他操作更新，请刷新后重试',
  lease_conflict: '工单租约已失效或由其他智能体持有',
  lease_expired: '工单租约已过期，请重新领取',
  lease_not_owned: '当前智能体不是该工单租约的持有者',
  idempotency_conflict: '相同幂等键正在处理，或已用于不同请求',
  idempotency_in_progress: '相同幂等请求仍在处理中，请稍后查询结果',
  command_scope_mismatch: '幂等键已用于其他命令范围',
  rate_limited: '操作过于频繁，请稍后重试',
  concurrency_limit: '智能体并发任务数已达到上限',
  execution_guard_unavailable: '智能体安全执行保护暂时不可用',
  service_unavailable: '安全执行保护暂时不可用，请稍后重试',
  automation_loop: '检测到异常自动化循环，操作已停止',
  outbox_replay_conflict: '该投递当前无法回放，请刷新状态后重试',
  attachment_rejected: '附件未通过安全校验，无法继续处理',
  ticket_configuration_unavailable: '当前项目没有完整的已发布建单配置',
  request_type_version_required: '请选择当前项目已发布的请求类型',
  ticket_form_validation_failed: '提交内容不符合所选请求类型的表单规则',
  internal_error: '服务暂时不可用，请稍后重试',
}

export const containsChineseText = (value: string) => /[\u3400-\u9fff]/u.test(value)

export const localizedUnknownErrorMessage = (
  error: unknown,
  fallback = '操作失败，请检查网络后重试',
) => {
  if (error instanceof DOMException && error.name === 'AbortError') {
    return '请求已取消'
  }
  if (error instanceof Error && containsChineseText(error.message)) {
    return error.message
  }
  return fallback
}

export const localizedApiErrorMessage = (
  payload: unknown,
  status: number,
  fallback = '请求未能完成，请检查输入后重试',
) => {
  const record = isJsonRecord(payload) ? payload : null
  const code =
    typeof record?.code === 'string'
      ? record.code
      : typeof record?.reason_code === 'string'
        ? record.reason_code
        : ''
  if (code && problemMessages[code]) return problemMessages[code]

  const candidate = record
    ? [record.detail, record.msg, record.message].find((value) => typeof value === 'string')
    : undefined
  if (typeof candidate === 'string' && containsChineseText(candidate)) return candidate

  if (status === 401) return '登录状态已失效，请重新登录'
  if (status === 403) return '当前账号无权执行此操作'
  if (status === 404) return '请求的资源不存在'
  if (status === 409) return '数据状态已发生变化，请刷新后重试'
  if (status === 429) return '操作过于频繁，请稍后重试'
  if (status >= 500) return '服务暂时不可用，请稍后重试'
  return fallback
}

const extractDataFromEnvelope = <T>(payload: JsonRecord): T | undefined => {
  if (typeof payload.code === 'number') {
    if (payload.code !== 0) {
      const status = typeof payload.status === 'number' ? payload.status : 400
      throw new Error(localizedApiErrorMessage(payload, status, '操作失败，请检查后重试'))
    }
    return (payload.data as T | undefined) ?? (payload as unknown as T)
  }

  if (typeof payload.success === 'boolean') {
    if (!payload.success) {
      const status = typeof payload.status === 'number' ? payload.status : 400
      throw new Error(localizedApiErrorMessage(payload, status, '操作失败，请检查后重试'))
    }
    return (payload.data as T | undefined) ?? (payload as unknown as T)
  }

  if ('data' in payload && 'meta' in payload && isJsonRecord(payload.meta)) {
    return payload.data as T
  }

  return payload as unknown as T
}

export async function apiFetch<T = unknown>(path: string, options: ApiOptions = {}): Promise<T> {
  const token = localStorage.getItem('token')
  const headers = new Headers(options.headers ?? {})
  headers.set('Accept', 'application/json')
  if (!(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }

  let response: Response
  try {
    response = await sessionAwareFetch(toUrl(path), {
      ...options,
      headers,
    })
  } catch (error) {
    throw new Error(localizedUnknownErrorMessage(error, '网络连接失败，请检查网络后重试'))
  }

  if (options.rawResponse) {
    return response as unknown as T
  }

  const text = await response.text()
  let parsed: unknown = null

  if (text) {
    try {
      parsed = JSON.parse(text)
    } catch (error) {
      throw new Error('返回结果解析失败')
    }
  }

  if (!response.ok) {
    throw new HttpError(
      localizedApiErrorMessage(parsed, response.status),
      response.status,
      parsed,
    )
  }

  if (isJsonRecord(parsed)) {
    return extractDataFromEnvelope<T>(parsed) ?? (undefined as T)
  }

  return parsed as T
}

export { API_BASE }
