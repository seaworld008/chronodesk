import assert from 'node:assert/strict'
import test from 'node:test'

const storage = (initial = {}) => {
    const values = new Map(Object.entries(initial))
    return {
        getItem: (key) => values.get(key) ?? null,
        removeItem: (key) => values.delete(key),
        setItem: (key, value) => values.set(key, String(value)),
        values,
    }
}

const local = storage()
let session = storage()

globalThis.window = {
    dispatchEvent: () => true,
    get localStorage() {
        return local
    },
    get sessionStorage() {
        return session
    },
}

const {
    activeProjectStorageKey,
    clearStoredProjectSelection,
    readStoredProjectSelection,
    writeStoredProjectSelection,
} = await import('./projectSelectionStorage.ts')

test('旧的跨标签项目选择只迁移到当前标签页', () => {
    local.setItem(activeProjectStorageKey, '{"project_key":"OPS"}')

    assert.equal(
        readStoredProjectSelection(),
        '{"project_key":"OPS"}',
    )
    assert.equal(
        session.getItem(activeProjectStorageKey),
        '{"project_key":"OPS"}',
    )
    assert.equal(local.getItem(activeProjectStorageKey), null)
})

test('项目切换只更新当前标签页，不覆盖另一标签页', () => {
    const firstTab = session
    writeStoredProjectSelection('{"project_key":"FIN"}')

    session = storage({
        [activeProjectStorageKey]: '{"project_key":"OPS"}',
    })
    assert.equal(
        readStoredProjectSelection(),
        '{"project_key":"OPS"}',
    )

    session = firstTab
    assert.equal(
        readStoredProjectSelection(),
        '{"project_key":"FIN"}',
    )
})

test('清理项目选择同时移除当前和历史存储', () => {
    local.setItem(activeProjectStorageKey, '{"project_key":"OPS"}')
    session.setItem(activeProjectStorageKey, '{"project_key":"FIN"}')

    clearStoredProjectSelection()

    assert.equal(local.getItem(activeProjectStorageKey), null)
    assert.equal(session.getItem(activeProjectStorageKey), null)
})
