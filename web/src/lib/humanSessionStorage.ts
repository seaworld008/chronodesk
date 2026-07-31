import { signalProjectAccessInvalidated } from './projectScopeEvents'

export const activeProjectStorageKey = 'chronodesk.activeProject'
export const legacyActiveProjectStorageKey = 'chronodesk.activeProjectKey'

export const authenticationStorageKeys = [
    'token',
    'refreshToken',
    'user',
    'tokenExpiresAt',
    'permissions',
] as const

export const clearStoredProjectSelection = (): void => {
    localStorage.removeItem(activeProjectStorageKey)
    localStorage.removeItem(legacyActiveProjectStorageKey)
}

export const clearStoredHumanSession = (): void => {
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
    }
    clearStoredProjectSelection()
    signalProjectAccessInvalidated()
}
