export type ApiOptions = RequestInit & { rawResponse?: boolean }

const API_BASE = (import.meta.env.VITE_API_URL ?? '/api').toString().replace(/\/$/, '')

const toUrl = (path: string) => `${API_BASE}${path.startsWith('/') ? path : `/${path}`}`

type JsonRecord = Record<string, unknown>

const isJsonRecord = (value: unknown): value is JsonRecord => typeof value === 'object' && value !== null

const extractDataFromEnvelope = <T>(payload: JsonRecord): T | undefined => {
  if (typeof payload.code === 'number') {
    if (payload.code !== 0) {
      const message = typeof payload.msg === 'string' ? payload.msg : '操作失败'
      throw new Error(message)
    }
    return (payload.data as T | undefined) ?? (payload as unknown as T)
  }

  if (typeof payload.success === 'boolean') {
    if (!payload.success) {
      const message = typeof payload.message === 'string' ? payload.message : '操作失败'
      throw new Error(message)
    }
    return (payload.data as T | undefined) ?? (payload as unknown as T)
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

  const response = await fetch(toUrl(path), {
    ...options,
    headers,
  })

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
    const message = isJsonRecord(parsed)
      ? (parsed.msg as string) || (parsed.message as string) || response.statusText || '请求失败'
      : response.statusText || '请求失败'
    throw new Error(message)
  }

  if (isJsonRecord(parsed)) {
    return extractDataFromEnvelope<T>(parsed) ?? (undefined as T)
  }

  return parsed as T
}

export { API_BASE }
