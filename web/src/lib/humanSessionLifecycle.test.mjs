import assert from 'node:assert/strict'
import test from 'node:test'

const calls = []
let tail = Promise.resolve()
const locks = {
    request(name, options, operation) {
        calls.push({ name, options })
        const result = tail.then(operation)
        tail = result.catch(() => undefined)
        return result
    },
}

Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: { locks },
})

const {
    humanSessionLifecycleLockName,
    withHumanSessionLifecycleLock,
} = await import('./humanSessionLifecycle.ts')

test('认证生命周期统一请求命名独占浏览器锁', async () => {
    let releaseFirst
    const firstGate = new Promise((resolve) => {
        releaseFirst = resolve
    })
    let firstStarted = false
    let secondStarted = false
    let active = 0
    let maximumActive = 0

    const first = withHumanSessionLifecycleLock(async () => {
        firstStarted = true
        active += 1
        maximumActive = Math.max(maximumActive, active)
        await firstGate
        active -= 1
        return 'first'
    })
    await Promise.resolve()
    const second = withHumanSessionLifecycleLock(async () => {
        secondStarted = true
        active += 1
        maximumActive = Math.max(maximumActive, active)
        active -= 1
        return 'second'
    })
    await Promise.resolve()

    assert.equal(firstStarted, true)
    assert.equal(secondStarted, false)
    assert.deepEqual(calls, [
        {
            name: humanSessionLifecycleLockName,
            options: { mode: 'exclusive' },
        },
        {
            name: humanSessionLifecycleLockName,
            options: { mode: 'exclusive' },
        },
    ])

    releaseFirst()
    assert.deepEqual(await Promise.all([first, second]), [
        'first',
        'second',
    ])
    assert.equal(maximumActive, 1)
})

test('浏览器缺少跨标签锁时认证生命周期安全失败', async () => {
    Object.defineProperty(globalThis, 'navigator', {
        configurable: true,
        value: {},
    })

    await assert.rejects(
        withHumanSessionLifecycleLock(async () => undefined),
        /不支持安全的多标签页登录协调/u,
    )
})
