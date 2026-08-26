import { HttpError } from 'react-admin'
import {
    humanApiRoutes,
    type AuthorizedProjectAccess,
    type ProjectRole,
} from '@/lib/generated/human-api'
import { projectRoleValues } from '@/lib/generated/human-api'
import {
    localizedApiErrorMessage,
    sessionAwareFetch,
} from './apiClient'
import {
    projectAccessInvalidatedEvent,
    projectAccessRefreshRequestedEvent,
    projectScopeChangedEvent,
    signalProjectAccessInvalidated,
    signalProjectInventoryChanged,
    signalProjectScopeChanged,
} from './projectScopeEvents'
import {
    clearStoredProjectSelection,
    legacyActiveProjectStorageKey,
    readStoredProjectSelection,
    writeStoredProjectSelection,
} from './humanSessionStorage'
import {
    readHumanSessionBinding,
    type HumanSessionBinding,
} from './humanTabSession'
import { joinApiUrl } from './apiUrl'

const apiBase = (import.meta.env.VITE_API_URL ?? '/api').toString().replace(/\/$/, '')
export { projectAccessInvalidatedEvent }

export type AuthorizedProject = AuthorizedProjectAccess
export type { ProjectRole }
export { projectRoleValues }

const knownProjectRoles = new Set<string>(projectRoleValues)

export const parseProjectRole = (value: unknown): ProjectRole | null =>
    typeof value === 'string' && knownProjectRoles.has(value)
        ? (value as ProjectRole)
        : null

const projectRoleLabels: Record<ProjectRole, string> = {
    project_admin: '项目管理员',
    manager: '项目经理',
    agent: '项目处理人',
    requester: '项目请求人',
    observer: '项目观察员',
}

export const getProjectRoleLabel = (role: unknown): string => {
    const parsed = parseProjectRole(role)
    return parsed === null ? '未知项目角色' : projectRoleLabels[parsed]
}

export type ProjectCapability =
    | 'view_project'
    | 'create_ticket'
    | 'edit_ticket_safe_fields'
    | 'manage_ticket_workflow'
    | 'assign_ticket'
    | 'delete_ticket'
    | 'write_public_content'
    | 'write_internal_content'
    | 'manage_notifications'
    | 'manage_automation'
    | 'view_integrations'
    | 'manage_integrations'
    | 'view_memberships'
    | 'manage_memberships'
    | 'manage_agents'
    | 'manage_knowledge'

const projectCapabilities: Record<
    ProjectRole,
    ReadonlySet<ProjectCapability>
> = {
    project_admin: new Set([
        'view_project',
        'create_ticket',
        'edit_ticket_safe_fields',
        'manage_ticket_workflow',
        'assign_ticket',
        'delete_ticket',
        'write_public_content',
        'write_internal_content',
        'manage_notifications',
        'manage_automation',
        'view_integrations',
        'manage_integrations',
        'view_memberships',
        'manage_memberships',
        'manage_agents',
        'manage_knowledge',
    ]),
    manager: new Set([
        'view_project',
        'create_ticket',
        'edit_ticket_safe_fields',
        'manage_ticket_workflow',
        'assign_ticket',
        'delete_ticket',
        'write_public_content',
        'write_internal_content',
        'manage_notifications',
        'manage_automation',
        'view_integrations',
        'manage_integrations',
        'view_memberships',
        'manage_knowledge',
    ]),
    agent: new Set([
        'view_project',
        'create_ticket',
        'edit_ticket_safe_fields',
        'manage_ticket_workflow',
        'assign_ticket',
        'write_public_content',
        'write_internal_content',
    ]),
    requester: new Set([
        'view_project',
        'create_ticket',
        'edit_ticket_safe_fields',
        'write_public_content',
    ]),
    observer: new Set(['view_project', 'view_integrations']),
}

export const hasProjectCapability = (
    role: unknown,
    capability: ProjectCapability,
): boolean => {
    const parsed = parseProjectRole(role)
    return parsed !== null && projectCapabilities[parsed].has(capability)
}

export const hasExactProjectRole = (
    role: unknown,
    allowed: readonly ProjectRole[],
): role is ProjectRole => {
    const parsed = parseProjectRole(role)
    return parsed !== null && allowed.includes(parsed)
}

export {
    readHumanSessionBinding,
    type HumanSessionBinding,
} from './humanTabSession'

type ActiveProjectRecord = {
    subject: string
    session_id: string
    project_key: string
    epoch: number
}

export type ProjectScopeSnapshot = Readonly<{
    subject: string
    session_id: string
    project_key: string
    epoch: number
}>

let projectRequest:
    | Promise<AuthorizedProject[]>
    | undefined
let projectRequestBinding: string | undefined
let projectRequestCompletedAt: number | undefined
let projectAccessRefreshRequest:
    | Promise<AuthorizedProject[]>
    | undefined
const activeProjectSelectionListeners = new Set<() => void>()

export const authorizedProjectAccessCacheTtlMs = 30_000

export const subscribeActiveProjectSelection = (
    listener: () => void,
): (() => void) => {
    activeProjectSelectionListeners.add(listener)
    return () => activeProjectSelectionListeners.delete(listener)
}

const notifyActiveProjectSelectionChanged = (): void => {
    for (const listener of activeProjectSelectionListeners) listener()
}

const resetAuthorizedProjectCache = (): void => {
    projectRequest = undefined
    projectRequestBinding = undefined
    projectRequestCompletedAt = undefined
}

if (typeof window !== 'undefined') {
    window.addEventListener(projectAccessInvalidatedEvent, () => {
        resetAuthorizedProjectCache()
    })
    window.addEventListener(projectScopeChangedEvent, () => {
        resetAuthorizedProjectCache()
    })
    window.addEventListener(projectAccessRefreshRequestedEvent, () => {
        if (projectAccessRefreshRequest) return
        resetAuthorizedProjectCache()
        const refreshRequest = loadAuthorizedProjects(true)
        projectAccessRefreshRequest = refreshRequest
        void refreshRequest
            .then(() => {
                signalProjectInventoryChanged()
            })
            .catch(() => undefined)
            .finally(() => {
                if (projectAccessRefreshRequest === refreshRequest) {
                    projectAccessRefreshRequest = undefined
                }
            })
    })
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
    typeof value === 'object' && value !== null

const positiveInteger = (value: unknown): value is number =>
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value > 0

const nonNegativeInteger = (value: unknown): value is number =>
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value >= 0

const nonEmptyString = (value: unknown): value is string =>
    typeof value === 'string' && value.length > 0

const bindingKey = (binding: HumanSessionBinding): string =>
    `${binding.subject}\u0000${binding.session_id}`

const sameBinding = (
    left: Pick<HumanSessionBinding, 'subject' | 'session_id'>,
    right: Pick<HumanSessionBinding, 'subject' | 'session_id'>,
): boolean =>
    left.subject === right.subject &&
    left.session_id === right.session_id

const parseActiveProjectRecord = (value: unknown): ActiveProjectRecord | null => {
    if (
        !isRecord(value) ||
        !nonEmptyString(value.subject) ||
        !nonEmptyString(value.session_id) ||
        !nonEmptyString(value.project_key) ||
        (
            value.epoch !== undefined &&
            !positiveInteger(value.epoch)
        )
    ) {
        return null
    }
    return {
        subject: value.subject,
        session_id: value.session_id,
        project_key: value.project_key,
        epoch: value.epoch === undefined ? 1 : value.epoch,
    }
}

const readActiveProjectRecord = (): ActiveProjectRecord | null => {
    const serialized = readStoredProjectSelection()
    if (!serialized) return null
    try {
        return parseActiveProjectRecord(JSON.parse(serialized))
    } catch {
        return null
    }
}

const parseAuthorizedProject = (value: unknown): AuthorizedProject | null => {
    if (!isRecord(value)) return null
    const project = value.project
    const scope = value.scope
    const projectRole = parseProjectRole(value.project_role)
    if (
        !isRecord(project) ||
        !positiveInteger(project.id) ||
        !nonEmptyString(project.public_id) ||
        !nonEmptyString(project.key) ||
        !nonEmptyString(project.name) ||
        typeof project.description !== 'string' ||
        !positiveInteger(project.business_unit_id) ||
        !positiveInteger(project.organization_id) ||
        (project.status !== 'active' && project.status !== 'archived') ||
        !isRecord(scope) ||
        !positiveInteger(scope.organization_id) ||
        !positiveInteger(scope.project_id) ||
        scope.organization_id !== project.organization_id ||
        scope.project_id !== project.id ||
        projectRole === null
    ) {
        return null
    }
    const canCreateKnowledgeDrafts =
        typeof value.can_create_knowledge_drafts === 'boolean'
            ? value.can_create_knowledge_drafts
            : projectRole === 'project_admin' || projectRole === 'manager'
    return {
        ...(value as AuthorizedProject),
        can_create_knowledge_drafts: canCreateKnowledgeDrafts,
    }
}

const responseData = (value: unknown): unknown => {
    if (isRecord(value) && 'data' in value) return value.data
    return value
}

const authorizedProjectPageSize = 100
const maxAuthorizedProjectInventory = 500

type AuthorizedProjectPage = {
    items: AuthorizedProject[]
    total: number
    page: number
    page_size: number
    total_pages: number
}

const parseAuthorizedProjectItems = (
    value: unknown,
    body: unknown,
): AuthorizedProject[] => {
    if (!Array.isArray(value)) {
        throw new HttpError('授权项目响应格式无效', 502, body)
    }
    const projects = value.map(parseAuthorizedProject)
    if (projects.some((project) => project === null)) {
        throw new HttpError('授权项目响应包含无效项目角色或范围', 502, body)
    }
    return projects as AuthorizedProject[]
}

const parseAuthorizedProjectPage = (
    value: unknown,
    expectedPage: number,
    body: unknown,
): AuthorizedProjectPage | AuthorizedProject[] => {
    // Keep the prior array response readable for one protected release while
    // every deployed server moves to the bounded page envelope.
    if (Array.isArray(value)) {
        if (expectedPage !== 1) {
            throw new HttpError('授权项目分页响应格式发生变化', 502, body)
        }
        return parseAuthorizedProjectItems(value, body)
    }
    if (
        !isRecord(value) ||
        !Array.isArray(value.items) ||
        !nonNegativeInteger(value.total) ||
        !positiveInteger(value.page) ||
        !positiveInteger(value.page_size) ||
        !nonNegativeInteger(value.total_pages) ||
        value.page !== expectedPage ||
        value.page_size !== authorizedProjectPageSize ||
        value.total_pages !== (
            value.total === 0
                ? 0
                : Math.ceil(value.total / authorizedProjectPageSize)
        )
    ) {
        throw new HttpError('授权项目分页响应格式无效', 502, body)
    }
    return {
        items: parseAuthorizedProjectItems(value.items, body),
        total: value.total,
        page: value.page,
        page_size: value.page_size,
        total_pages: value.total_pages,
    }
}

const authorizedProjectPagePath = (page: number): string => {
    const parameters = new URLSearchParams({
        page: String(page),
        page_size: String(authorizedProjectPageSize),
        sort_by: 'name',
        sort_order: 'asc',
    })
    return `${humanApiRoutes.listAuthorizedHumanProjects()}?${parameters}`
}

export const loadAuthorizedProjects = async (
    force = false,
): Promise<AuthorizedProject[]> => {
    const binding = readHumanSessionBinding()
    if (!binding) {
        resetAuthorizedProjectCache()
        clearStoredProjectSelection()
        localStorage.removeItem(legacyActiveProjectStorageKey)
        throw new HttpError('登录会话无效，请重新登录', 401, {
            code: 'invalid_human_session',
        })
    }
    const currentBindingKey = bindingKey(binding)
    if (
        !force &&
        projectRequest &&
        projectRequestBinding === currentBindingKey &&
        (
            projectRequestCompletedAt === undefined ||
            Date.now() - projectRequestCompletedAt <
                authorizedProjectAccessCacheTtlMs
        )
    ) {
        return projectRequest
    }

    projectRequestBinding = currentBindingKey
    projectRequestCompletedAt = undefined
    const request = (async () => {
        const token = localStorage.getItem('token')
        const projects: AuthorizedProject[] = []
        const seenPublicIDs = new Set<string>()
        const seenKeys = new Set<string>()
        let expectedTotal: number | null = null
        let totalPages = 1
        for (let page = 1; page <= totalPages; page += 1) {
            const response = await sessionAwareFetch(
                joinApiUrl(apiBase, authorizedProjectPagePath(page)),
                {
                    headers: {
                        Accept: 'application/json',
                        Authorization: `Bearer ${token}`,
                    },
                },
            )
            const body: unknown = await response.json().catch(() => ({}))
            if (!response.ok) {
                throw new HttpError(
                    localizedApiErrorMessage(body, response.status),
                    response.status,
                    body,
                )
            }
            const payload = parseAuthorizedProjectPage(
                responseData(body),
                page,
                body,
            )
            if (Array.isArray(payload)) return payload
            if (page === 1) {
                expectedTotal = payload.total
                totalPages = payload.total_pages
                if (expectedTotal > maxAuthorizedProjectInventory) {
                    throw new HttpError(
                        `授权项目超过 ${maxAuthorizedProjectInventory} 个，请联系平台管理员拆分职责范围`,
                        409,
                        { code: 'authorized_project_inventory_limit' },
                    )
                }
            } else if (
                payload.total !== expectedTotal ||
                payload.total_pages !== totalPages
            ) {
                throw new HttpError(
                    '授权项目在分页读取期间发生变化，请重试',
                    409,
                    { code: 'authorized_project_inventory_changed' },
                )
            }
            for (const project of payload.items) {
                const publicID = project.project.public_id
                const key = project.project.key
                if (seenPublicIDs.has(publicID) || seenKeys.has(key)) {
                    throw new HttpError(
                        '授权项目分页响应包含重复项目',
                        502,
                        body,
                    )
                }
                seenPublicIDs.add(publicID)
                seenKeys.add(key)
                projects.push(project)
            }
        }
        if (projects.length !== expectedTotal) {
            throw new HttpError(
                '授权项目分页响应数量不一致，请重试',
                409,
                { code: 'authorized_project_inventory_changed' },
            )
        }
        const latestBinding = readHumanSessionBinding()
        if (!latestBinding || !sameBinding(binding, latestBinding)) {
            throw new HttpError('登录账号已变化，已拒绝复用旧项目缓存', 401, {
                code: 'project_cache_subject_mismatch',
            })
        }
        return projects
    })()
    projectRequest = request

    try {
        const projects = await request
        if (projectRequest === request) {
            projectRequestCompletedAt = Date.now()
        }
        return projects
    } catch (error) {
        if (projectRequest === request) {
            resetAuthorizedProjectCache()
        }
        throw error
    }
}

export const refreshAuthorizedProjectInventory = async (): Promise<
    AuthorizedProject[]
> => {
    const projects = await loadAuthorizedProjects(true)
    signalProjectInventoryChanged()
    return projects
}

export const activeProjectKey = (): string | undefined => {
    localStorage.removeItem(legacyActiveProjectStorageKey)
    const binding = readHumanSessionBinding()
    const stored = readActiveProjectRecord()
    if (!binding || !stored || !sameBinding(binding, stored)) {
        clearStoredProjectSelection()
        return undefined
    }
    return stored.project_key
}

export const captureProjectScopeSnapshot = (): ProjectScopeSnapshot => {
    const binding = readHumanSessionBinding()
    const stored = readActiveProjectRecord()
    if (!binding || !stored || !sameBinding(binding, stored)) {
        clearStoredProjectSelection()
        throw new HttpError('项目范围已变化，请刷新页面后重试', 409, {
            code: 'project_scope_changed',
        })
    }
    return Object.freeze({
        subject: binding.subject,
        session_id: binding.session_id,
        project_key: stored.project_key,
        epoch: stored.epoch,
    })
}

export const assertProjectScopeSnapshot = (
    snapshot: ProjectScopeSnapshot,
): void => {
    const current = captureProjectScopeSnapshot()
    if (
        current.subject !== snapshot.subject ||
        current.session_id !== snapshot.session_id ||
        current.project_key !== snapshot.project_key ||
        current.epoch !== snapshot.epoch
    ) {
        throw new HttpError('项目范围已变化，请刷新页面后重试', 409, {
            code: 'project_scope_changed',
        })
    }
}

export const resolveActiveProjectAccess = async (): Promise<AuthorizedProject> => {
    const projects = await loadAuthorizedProjects()
    const stored = activeProjectKey()
    if (!stored) {
        throw new HttpError(
            projects.length === 0
                ? '当前账号没有可访问的项目'
                : '请选择要进入的项目',
            projects.length === 0 ? 403 : 409,
            {
                code:
                    projects.length === 0
                        ? 'project_access_required'
                        : 'active_project_required',
            },
        )
    }
    const selected = projects.find(({ project }) => project.key === stored)
    if (!selected) {
        // Keep this resolver side-effect free. A cached or in-flight inventory
        // can become stale while the user switches projects; only explicit
        // revocation and the latest AppBar inventory reconciliation may remove
        // the stored selection.
        throw new HttpError('当前账号没有可访问的项目', 403, {
            code: 'active_project_access_lost',
        })
    }
    return selected
}

export const resolveActiveProjectKey = async (): Promise<string> =>
    (await resolveActiveProjectAccess()).project.key

export const setActiveProjectKey = (
    projectKey: string,
    projects: AuthorizedProject[],
): void => {
    const binding = readHumanSessionBinding()
    if (
        !binding ||
        !projects.some(({ project }) => project.key === projectKey)
    ) {
        throw new Error('不能切换到未授权项目')
    }
    const record: ActiveProjectRecord = {
        subject: binding.subject,
        session_id: binding.session_id,
        project_key: projectKey,
        epoch: 1,
    }
    const previous = readActiveProjectRecord()
    const previousProjectKey =
        previous && sameBinding(binding, previous)
            ? previous.project_key
            : undefined
    record.epoch =
        previous && sameBinding(binding, previous)
            ? previous.epoch + (previous.project_key === projectKey ? 0 : 1)
            : 1
    writeStoredProjectSelection(JSON.stringify(record))
    if (previousProjectKey !== projectKey) {
        notifyActiveProjectSelectionChanged()
        signalProjectScopeChanged(projectKey)
    }
}

export const clearActiveProjectSelection = (): void => {
    clearStoredProjectSelection()
    notifyActiveProjectSelectionChanged()
}

export const projectResourcePath = async (
    resourcePath: string,
    snapshot?: ProjectScopeSnapshot,
): Promise<string> => {
    const scope = snapshot ?? captureProjectScopeSnapshot()
    const access = await resolveActiveProjectAccess()
    assertProjectScopeSnapshot(scope)
    if (access.project.key !== scope.project_key) {
        throw new HttpError('项目范围已变化，请刷新页面后重试', 409, {
            code: 'project_scope_changed',
        })
    }
    return `projects/${encodeURIComponent(scope.project_key)}/${resourcePath.replace(/^\/+/, '')}`
}

export const invalidateProjectAccessCache = (): void => {
    invalidateAuthorizedProjectCache()
    signalProjectAccessInvalidated()
}

export const invalidateAuthorizedProjectCache = (): void => {
    resetAuthorizedProjectCache()
}

export const clearProjectScopeCache = (): void => {
    invalidateProjectAccessCache()
    clearActiveProjectSelection()
}
