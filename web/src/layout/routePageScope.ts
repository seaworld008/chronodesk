import {
    navigationRegistry,
    type NavigationLeafNode,
    type NavigationScope,
} from '@/navigation/navigationRegistry'
import { isNavigationItemActive } from '@/navigation/navigationTreeState'

export type RoutePageScope = NavigationScope | 'account'

export interface RoutePageScopeResolution {
    kind: RoutePageScope
    navigationNodeID: string | null
}

const navigationLeaves: readonly NavigationLeafNode[] =
    navigationRegistry.flatMap((node) =>
        node.kind === 'leaf' ? [node] : node.children,
    )

const matchingPrefixLength = (
    item: NavigationLeafNode,
    pathname: string,
): number => Math.max(
    ...item.activePathPrefixes
        .filter((prefix) =>
            prefix === '/'
                ? pathname === '/'
                : pathname === prefix || pathname.startsWith(`${prefix}/`),
        )
        .map((prefix) => prefix.length),
    -1,
)

export const resolveRoutePageScope = (
    pathname: string,
): RoutePageScopeResolution => {
    const active = navigationLeaves
        .filter((item) => isNavigationItemActive(item, pathname))
        .map((item) => ({
            item,
            prefixLength: matchingPrefixLength(item, pathname),
        }))
        .sort((left, right) => right.prefixLength - left.prefixLength)[0]?.item

    if (!active) {
        return { kind: 'global', navigationNodeID: null }
    }
    return {
        kind: active.placement === 'account' ? 'account' : active.scope,
        navigationNodeID: active.id,
    }
}
