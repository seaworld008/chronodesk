import { signalProjectAccessInvalidated } from './projectScopeEvents.ts'
import {
    clearStoredProjectSelection,
} from './projectSelectionStorage.ts'
export {
    activeProjectStorageKey,
    clearStoredProjectSelection,
    legacyActiveProjectStorageKey,
    readStoredProjectSelection,
    writeStoredProjectSelection,
} from './projectSelectionStorage.ts'

export const authenticationStorageKeys = [
    'token',
    'refreshToken',
    'user',
    'tokenExpiresAt',
    'permissions',
] as const

export type HumanSessionMetadataStorageKey =
    | 'user'
    | 'tokenExpiresAt'

export const readHumanSessionMetadata = (
    key: HumanSessionMetadataStorageKey,
): string | null => {
    if (typeof window === 'undefined') return null
    const currentValue = window.sessionStorage.getItem(key)
    if (currentValue !== null) return currentValue

    const legacyValue = window.localStorage.getItem(key)
    if (legacyValue !== null) {
        window.sessionStorage.setItem(key, legacyValue)
        window.localStorage.removeItem(key)
    }
    return legacyValue
}

export const writeHumanSessionMetadata = (
    key: HumanSessionMetadataStorageKey,
    value: string,
): void => {
    if (typeof window === 'undefined') return
    window.localStorage.removeItem(key)
    window.sessionStorage.setItem(key, value)
}

export const clearStoredHumanSession = (): void => {
    if (typeof window === 'undefined') return
    for (const key of authenticationStorageKeys) {
        window.localStorage.removeItem(key)
        window.sessionStorage.removeItem(key)
    }
    clearStoredProjectSelection()
    signalProjectAccessInvalidated()
}
