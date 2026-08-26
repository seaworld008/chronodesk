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

class MockBroadcastChannel {
    static instances = []

    constructor(name) {
        this.name = name
        this.messages = []
        this.listeners = new Set()
        MockBroadcastChannel.instances.push(this)
    }

    postMessage(message) {
        this.messages.push(message)
    }

    addEventListener(type, listener) {
        if (type === 'message') this.listeners.add(listener)
    }

    removeEventListener(type, listener) {
        if (type === 'message') this.listeners.delete(listener)
    }

    emit(data) {
        for (const listener of this.listeners) {
            listener({ data })
        }
    }
}

globalThis.window = {
    BroadcastChannel: MockBroadcastChannel,
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

local.setItem('token', 'legacy-access-token')
local.setItem('refreshToken', 'legacy-refresh-token')
const {
    clearHumanAccessToken,
    commitHumanAccessToken,
    readHumanAccessToken,
} = await import('./humanSessionRuntime.ts')
const {
    publishAuthenticatedHumanSession,
    publishSignedOutHumanSession,
    subscribeHumanSessionMetadata,
} = await import('./humanSessionChannel.ts')
const {
    clearStoredHumanSession,
    readHumanSessionMetadata,
    writeHumanSessionMetadata,
} = await import('./humanSessionStorage.ts')

test('旧 bearer 只迁移到内存并立即清除浏览器持久化', () => {
    assert.equal(readHumanAccessToken(), 'legacy-access-token')
    assert.equal(local.getItem('token'), null)
    assert.equal(local.getItem('refreshToken'), null)

    commitHumanAccessToken('rotated-access-token')
    assert.equal(readHumanAccessToken(), 'rotated-access-token')
    assert.equal(local.getItem('token'), null)
    assert.equal(local.getItem('refreshToken'), null)

    clearHumanAccessToken()
    assert.equal(readHumanAccessToken(), null)

    local.setItem('token', 'late-persisted-token')
    local.setItem('refreshToken', 'late-persisted-refresh')
    assert.equal(readHumanAccessToken(), null)
    assert.equal(local.getItem('token'), null)
    assert.equal(local.getItem('refreshToken'), null)
})

test('跨标签消息只广播稳定会话元数据，不包含 bearer', () => {
    publishAuthenticatedHumanSession({
        subject: '42',
        session_id: 'session-42',
        expires_at: 4_102_444_800_000,
    })
    publishSignedOutHumanSession()

    const activeChannel = MockBroadcastChannel.instances.at(-1)
    assert.equal(activeChannel.name, 'chronodesk:human-session:v1')
    assert.equal(activeChannel.messages.length, 2)
    assert.deepEqual(
        Object.keys(activeChannel.messages[0]).sort(),
        [
            'expires_at',
            'issued_at',
            'session_id',
            'subject',
            'type',
        ],
    )
    assert.equal('access_token' in activeChannel.messages[0], false)
    assert.equal('refresh_token' in activeChannel.messages[0], false)
})

test('跨标签消息拒绝残缺元数据，只交付完整稳定绑定', () => {
    const received = []
    const unsubscribe = subscribeHumanSessionMetadata((metadata) => {
        received.push(metadata)
    })
    const activeChannel = MockBroadcastChannel.instances.at(-1)

    activeChannel.emit({
        type: 'authenticated',
        subject: '42',
        access_token: 'must-not-be-accepted',
    })
    activeChannel.emit({
        type: 'authenticated',
        subject: '42',
        session_id: 'session-42',
        expires_at: 4_102_444_800_000,
        issued_at: 1,
    })
    unsubscribe()

    assert.deepEqual(received, [
        {
            type: 'authenticated',
            subject: '42',
            session_id: 'session-42',
            expires_at: 4_102_444_800_000,
            issued_at: 1,
        },
    ])
})

test('非敏感会话元数据从旧 localStorage 迁移到当前标签页', () => {
    local.setItem('user', '{"id":42}')

    assert.equal(readHumanSessionMetadata('user'), '{"id":42}')
    assert.equal(local.getItem('user'), null)
    assert.equal(session.getItem('user'), '{"id":42}')

    writeHumanSessionMetadata('tokenExpiresAt', '4102444800000')
    assert.equal(local.getItem('tokenExpiresAt'), null)
    assert.equal(
        session.getItem('tokenExpiresAt'),
        '4102444800000',
    )

    clearStoredHumanSession()
    assert.equal(session.getItem('user'), null)
    assert.equal(session.getItem('tokenExpiresAt'), null)
})

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
