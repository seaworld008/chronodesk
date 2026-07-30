export const projectAccessInvalidatedEvent =
    'chronodesk:project-access-invalidated'
export const projectScopeChangedEvent = 'chronodesk:project-scope-changed'
export const sessionInvalidatedEvent = 'chronodesk:session-invalidated'

export const isProjectScopedApiPath = (path: string): boolean =>
    /\/projects\/[^/?#]+(?:[/?#]|$)/.test(path)

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
