import assert from 'node:assert/strict'
import test from 'node:test'

import {
    LatestRequestGate,
    lastPageAfterAppend,
} from './pagedRequestGate.ts'

test('new page request aborts and invalidates the older response', () => {
    const gate = new LatestRequestGate()
    const first = gate.start()
    const second = gate.start()

    assert.equal(first.signal.aborted, true)
    assert.equal(gate.isCurrent(first.token), false)
    assert.equal(second.signal.aborted, false)
    assert.equal(gate.isCurrent(second.token), true)
})

test('unmount abort invalidates the active response', () => {
    const gate = new LatestRequestGate()
    const active = gate.start()

    gate.abort()

    assert.equal(active.signal.aborted, true)
    assert.equal(gate.isCurrent(active.token), false)
})

test('ascending comment append navigates to the page containing the new row', () => {
    assert.equal(lastPageAfterAppend(0), 1)
    assert.equal(lastPageAfterAppend(24), 1)
    assert.equal(lastPageAfterAppend(25), 2)
    assert.equal(lastPageAfterAppend(149), 6)
})
