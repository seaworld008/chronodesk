import { HttpError } from 'react-admin'
import { localizedApiErrorMessage } from './apiClient'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').toString().replace(/\/$/, '')
const activeProjectStorageKey = 'chronodesk.activeProjectKey'

export interface AuthorizedProject {
    project: {
        id: number
        public_id: string
        key: string
        name: string
        description?: string
        business_unit_id: number
        organization_id: number
        status: 'active' | 'archived'
    }
    role: 'project_admin' | 'manager' | 'agent' | 'requester' | 'observer'
    scope: {
        organization_id: number
        project_id: number
    }
}

let projectRequest: Promise<AuthorizedProject[]> | undefined

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const responseData = (value: unknown): unknown => {
    if (isRecord(value) && 'data' in value) return value.data
    return value
}

export const loadAuthorizedProjects = async (
    force = false,
): Promise<AuthorizedProject[]> => {
    if (!force && projectRequest) return projectRequest

    projectRequest = (async () => {
        const token = localStorage.getItem('token')
        if (!token) return []
        const response = await fetch(`${apiBase}/projects`, {
            headers: {
                Accept: 'application/json',
                Authorization: `Bearer ${token}`,
            },
        })
        const body: unknown = await response.json().catch(() => ({}))
        if (!response.ok) {
            throw new HttpError(
                localizedApiErrorMessage(body, response.status),
                response.status,
                body,
            )
        }
        const payload = responseData(body)
        if (!Array.isArray(payload)) {
            throw new HttpError('授权项目响应格式无效', 502, body)
        }
        return payload as AuthorizedProject[]
    })()

    try {
        return await projectRequest
    } catch (error) {
        projectRequest = undefined
        throw error
    }
}

export const activeProjectKey = (): string | undefined => {
    const value = localStorage.getItem(activeProjectStorageKey)?.trim()
    return value || undefined
}

export const resolveActiveProjectKey = async (): Promise<string> => {
    const projects = await loadAuthorizedProjects()
    const stored = activeProjectKey()
    const selected =
        projects.find(({ project }) => project.key === stored) ??
        projects[0]
    if (!selected) {
        throw new HttpError('当前账号没有可访问的项目', 403, {
            code: 'project_access_required',
        })
    }
    if (selected.project.key !== stored) {
        localStorage.setItem(activeProjectStorageKey, selected.project.key)
    }
    return selected.project.key
}

export const setActiveProjectKey = (
    projectKey: string,
    projects: AuthorizedProject[],
): void => {
    if (!projects.some(({ project }) => project.key === projectKey)) {
        throw new Error('不能切换到未授权项目')
    }
    localStorage.setItem(activeProjectStorageKey, projectKey)
}

export const projectResourcePath = async (resourcePath: string): Promise<string> => {
    const projectKey = await resolveActiveProjectKey()
    return `projects/${encodeURIComponent(projectKey)}/${resourcePath.replace(/^\/+/, '')}`
}

export const clearProjectScopeCache = (): void => {
    projectRequest = undefined
}
