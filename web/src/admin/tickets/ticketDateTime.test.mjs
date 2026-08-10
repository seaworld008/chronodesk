import assert from 'node:assert/strict'
import test from 'node:test'

import { normalizeTicketDateTimeForSubmit } from './ticketDateTime.ts'

test('datetime-local values are serialized as RFC3339 instants', () => {
    process.env.TZ = 'Asia/Shanghai'
    const actual = normalizeTicketDateTimeForSubmit('2026-08-03T17:00')
    assert.equal(actual, '2026-08-03T09:00:00.000Z')
})

test('existing RFC3339 values stay the same instant and blank clears', () => {
    assert.equal(
        normalizeTicketDateTimeForSubmit('2026-08-03T09:00:00.000Z'),
        '2026-08-03T09:00:00.000Z',
    )
    assert.equal(normalizeTicketDateTimeForSubmit('  '), null)
    assert.equal(normalizeTicketDateTimeForSubmit(null), null)
})

test('invalid input remains available for server-side rejection', () => {
    assert.equal(
        normalizeTicketDateTimeForSubmit('not-a-date'),
        'not-a-date',
    )
})
