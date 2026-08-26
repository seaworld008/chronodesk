import { signalProjectAccessInvalidated } from './projectScopeEvents'
import {
    clearStoredProjectSelection,
} from './projectSelectionStorage'
export {
    activeProjectStorageKey,
    clearStoredProjectSelection,
    legacyActiveProjectStorageKey,
    readStoredProjectSelection,
    writeStoredProjectSelection,
} from './projectSelectionStorage'

export const authenticationStorageKeys = [
    'token',
    'refreshToken',
    'user',
    'tokenExpiresAt',
    'permissions',
] as const

export const clearStoredHumanSession = (): void => {
    for (const key of authenticationStorageKeys) {
        localStorage.removeItem(key)
    }
    clearStoredProjectSelection()
    signalProjectAccessInvalidated()
}
