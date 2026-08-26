export const activeProjectStorageKey = 'chronodesk.activeProject'
export const legacyActiveProjectStorageKey = 'chronodesk.activeProjectKey'

const storageAvailable = (
    storage: 'localStorage' | 'sessionStorage',
): Storage | null => {
    if (typeof window === 'undefined') return null
    return window[storage]
}

export const readStoredProjectSelection = (): string | null => {
    const session = storageAvailable('sessionStorage')
    const local = storageAvailable('localStorage')
    const current = session?.getItem(activeProjectStorageKey) ?? null
    if (current) return current

    // Migrate the historical cross-tab value into a tab-local selection.
    // Keeping the migration read-only for the current tab prevents another
    // tab's later project switch from changing this tab's trusted scope.
    const legacy = local?.getItem(activeProjectStorageKey) ?? null
    if (!legacy || !session) return legacy
    session.setItem(activeProjectStorageKey, legacy)
    local?.removeItem(activeProjectStorageKey)
    return legacy
}

export const writeStoredProjectSelection = (serialized: string): void => {
    const session = storageAvailable('sessionStorage')
    if (!session) {
        throw new Error('当前浏览器不支持标签页级项目会话')
    }
    session.setItem(activeProjectStorageKey, serialized)
    storageAvailable('localStorage')?.removeItem(activeProjectStorageKey)
}

export const clearStoredProjectSelection = (): void => {
    storageAvailable('sessionStorage')?.removeItem(activeProjectStorageKey)
    storageAvailable('sessionStorage')?.removeItem(
        legacyActiveProjectStorageKey,
    )
    storageAvailable('localStorage')?.removeItem(activeProjectStorageKey)
    storageAvailable('localStorage')?.removeItem(
        legacyActiveProjectStorageKey,
    )
}
