import type {
    NavigationGroupNode,
    NavigationLeafNode,
} from './navigationRegistry'

const validLeaf: NavigationLeafNode = {
    kind: 'leaf',
    id: 'valid',
    label: '有效叶子',
    icon: 'home',
    order: 1,
    scope: 'global',
    capability: null,
    roles: null,
    placement: 'sidebar',
    path: '/valid',
    activePathPrefixes: ['/valid'],
}

const invalidLeaf: NavigationLeafNode = {
    ...validLeaf,
    // @ts-expect-error leaf 在类型层禁止 children
    children: [],
}

const validGroup: NavigationGroupNode = {
    kind: 'group',
    id: 'valid-group',
    label: '有效分组',
    icon: 'home',
    order: 1,
    scope: 'global',
    capability: null,
    roles: null,
    placement: 'sidebar',
    path: null,
    children: [validLeaf],
}

const invalidNestedGroup: NavigationGroupNode = {
    ...validGroup,
    // @ts-expect-error 当前 UX 最多两层，group children 只能是 leaf
    children: [validGroup],
}

void invalidLeaf
void invalidNestedGroup
