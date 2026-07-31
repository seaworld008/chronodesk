export const projectAccessInvalidatedEvent =
    'chronodesk:project-access-invalidated'
export const projectScopeChangedEvent = 'chronodesk:project-scope-changed'
export const sessionInvalidatedEvent = 'chronodesk:session-invalidated'
export const projectAccessRevokedCode = 'project_access_revoked'

const activeProjectStorageKey = 'chronodesk.activeProject'

const projectKeyFromProjectScopedApiPath = (path: string): string | null => {
    let pathname: string
    try {
        pathname = new URL(path, 'http://chronodesk.local').pathname
    } catch {
        return null
    }

    const segments = pathname.split('/').filter(Boolean)
    const projectsIndex = segments.findIndex(
        (segment, index) =>
            segment === 'projects' && segments[index - 1] !== 'platform',
    )
    if (projectsIndex < 0) return null
    const encodedProjectKey = segments[projectsIndex + 1]
    if (!encodedProjectKey) return null

    try {
        const projectKey = decodeURIComponent(encodedProjectKey)
        return projectKey.trim() ? projectKey : null
    } catch {
        return null
    }
}

const storedActiveProjectKey = (): string | null => {
    if (typeof window === 'undefined') return null
    try {
        const serialized = window.localStorage.getItem(activeProjectStorageKey)
        if (!serialized) return null
        const value: unknown = JSON.parse(serialized)
        if (
            typeof value !== 'object' ||
            value === null ||
            !('project_key' in value) ||
            typeof value.project_key !== 'string' ||
            !value.project_key.trim()
        ) {
            return null
        }
        return value.project_key
    } catch {
        return null
    }
}

export const isProjectScopedApiPath = (path: string): boolean =>
    projectKeyFromProjectScopedApiPath(path) !== null

const isProjectAccessRevokedPayload = (value: unknown): boolean =>
    typeof value === 'object' &&
    value !== null &&
    'code' in value &&
    value.code === projectAccessRevokedCode

export const shouldInvalidateActiveProjectAccess = (
    path: string,
    payload: unknown,
): boolean => {
    if (!isProjectAccessRevokedPayload(payload)) return false
    const requestedProjectKey = projectKeyFromProjectScopedApiPath(path)
    const activeProjectKey = storedActiveProjectKey()
    return (
        requestedProjectKey !== null &&
        activeProjectKey !== null &&
        requestedProjectKey === activeProjectKey
    )
}

export const signalProjectAccessInvalidated = (): void => {
    if (typeof window !== 'undefined') {
        window.dispatchEvent(new Event(projectAccessInvalidatedEvent))
    }
}

export const signalProjectScopeChanged = (projectKey: string): void => {
    if (typeof window !== 'undefined') {
        window.dispatchEvent(
            new CustomEvent(projectScopeChangedEvent, {
                detail: { project_key: projectKey },
            }),
        )
    }
}

export const signalSessionInvalidated = (): void => {
    if (typeof window !== 'undefined') {
        window.dispatchEvent(new Event(sessionInvalidatedEvent))
    }
}
