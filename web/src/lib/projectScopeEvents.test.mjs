import assert from 'node:assert/strict'
import test from 'node:test'

import {
    shouldInvalidateActiveProjectAccess,
    shouldRefreshActiveProjectAccessAfterForbidden,
} from './projectScopeEvents.ts'

const activeProjectStorageKey = 'chronodesk.activeProject'
const storedValues = new Map()

globalThis.window = {
    localStorage: {
        getItem: (key) => storedValues.get(key) ?? null,
    },
}

const selectActiveProject = (projectKey) => {
    storedValues.set(
        activeProjectStorageKey,
        JSON.stringify({ project_key: projectKey }),
    )
}

test('知识接口 403 仅为当前项目请求 ProjectAccess 软刷新', () => {
    selectActiveProject('OPS')

    assert.equal(
        shouldRefreshActiveProjectAccessAfterForbidden(
            '/api/projects/OPS/knowledge/articles',
        ),
        true,
    )
    assert.equal(
        shouldRefreshActiveProjectAccessAfterForbidden(
            'https://chronodesk.test/api/projects/OPS/knowledge/articles/1/drafts',
        ),
        true,
    )
    assert.equal(
        shouldRefreshActiveProjectAccessAfterForbidden(
            '/api/projects/FIN/knowledge/articles',
        ),
        false,
    )
    assert.equal(
        shouldRefreshActiveProjectAccessAfterForbidden(
            '/api/projects/OPS/tickets',
        ),
        false,
    )
    assert.equal(
        shouldRefreshActiveProjectAccessAfterForbidden(
            '/api/platform/projects/OPS/knowledge/articles',
        ),
        false,
    )
})

test('项目访问撤销仍使用显式硬失效代码', () => {
    selectActiveProject('OPS')

    assert.equal(
        shouldInvalidateActiveProjectAccess(
            '/api/projects/OPS/knowledge/articles',
            { code: 'project_access_revoked' },
        ),
        true,
    )
    assert.equal(
        shouldInvalidateActiveProjectAccess(
            '/api/projects/OPS/knowledge/articles',
            { code: 403, msg: '当前成员未获知识草稿贡献授权' },
        ),
        false,
    )
})
