import type {
    NavigationGroupNode,
    NavigationLeafNode,
    NavigationNode,
} from './navigationRegistry'

export type NavigationGroupState = Record<string, boolean>

export interface NavigationSessionBinding {
    subject: string
    session_id: string
}

export interface NavigationStateStorage {
    getItem(key: string): string | null
    setItem(key: string, value: string): void
}

export const navigationStateStorageVersion = 2
const storagePrefix =
    `chronodesk.navigation-groups.v${navigationStateStorageVersion}`

export const navigationStateStorageKey = (
    binding: NavigationSessionBinding,
): string =>
    `${storagePrefix}.${encodeURIComponent(binding.subject)}.${encodeURIComponent(binding.session_id)}`

export const validNavigationGroupIDs = (
    nodes: readonly NavigationNode[],
): string[] => nodes
    .filter((node): node is NavigationGroupNode => node.kind === 'group')
    .map((group) => group.id)

export const findActiveNavigationGroupID = (
    nodes: readonly NavigationNode[],
    pathname: string,
): string | null => {
    const candidates = nodes.flatMap((node) =>
        node.kind === 'leaf'
            ? []
            : node.children.flatMap((item) =>
            item.activePathPrefixes
                .filter((prefix) =>
                    prefix === '/'
                        ? pathname === '/'
                        : pathname === prefix || pathname.startsWith(`${prefix}/`),
                )
                .map((prefix) => ({ groupID: node.id, prefix })),
            ),
    )
    candidates.sort((left, right) => right.prefix.length - left.prefix.length)
    return candidates[0]?.groupID ?? null
}

export const isNavigationItemActive = (
    item: NavigationLeafNode,
    pathname: string,
): boolean =>
    item.activePathPrefixes.some((prefix) =>
        prefix === '/'
            ? pathname === '/'
            : pathname === prefix || pathname.startsWith(`${prefix}/`),
    )

export const loadNavigationGroupState = (
    storage: NavigationStateStorage,
    binding: NavigationSessionBinding,
    validGroupIDs: readonly string[],
): NavigationGroupState => {
    const result: NavigationGroupState = {}
    try {
        const serialized = storage.getItem(navigationStateStorageKey(binding))
        if (!serialized) return result
        const parsed: unknown = JSON.parse(serialized)
        if (typeof parsed !== 'object' || parsed === null) return result
        for (const groupID of validGroupIDs) {
            const value = (parsed as Record<string, unknown>)[groupID]
            if (typeof value === 'boolean') result[groupID] = value
        }
    } catch {
        // Corrupt or unavailable preference storage must not block navigation.
    }
    return result
}

export const saveNavigationGroupState = (
    storage: NavigationStateStorage,
    binding: NavigationSessionBinding,
    state: NavigationGroupState,
    validGroupIDs: readonly string[],
): void => {
    const sanitized = Object.fromEntries(
        validGroupIDs
            .filter((groupID) => typeof state[groupID] === 'boolean')
            .map((groupID) => [groupID, state[groupID]]),
    )
    try {
        storage.setItem(
            navigationStateStorageKey(binding),
            JSON.stringify(sanitized),
        )
    } catch {
        // Persistence is a progressive enhancement.
    }
}

export const expandActiveNavigationGroup = (
    state: NavigationGroupState,
    activeGroupID: string | null,
): NavigationGroupState =>
    activeGroupID === null || state[activeGroupID]
        ? state
        : { ...state, [activeGroupID]: true }

export const toggleNavigationGroup = (
    state: NavigationGroupState,
    groupID: string,
    forcedOpenGroupID: string | null = null,
): NavigationGroupState => ({
    ...state,
    [groupID]: groupID === forcedOpenGroupID ? true : !state[groupID],
})

export const isNavigationToggleKey = (key: string): boolean =>
    key === 'Enter' || key === ' '
