import assert from 'node:assert/strict'
import test from 'node:test'

import {
    auditFiltersFromSearchParams,
    auditFiltersToQuery,
    auditFiltersToSearchParams,
} from './auditExplorerState.ts'

test('audit explorer filters round-trip through the browser URL', () => {
    const source = new URLSearchParams(
        'actor=alice&platform_role=security_auditor&action=platform.user.update' +
            '&method=post&path_prefix=%2Fapi%2Fplatform&status=403&result=error' +
            '&keyword=denied&time_preset=24h&cursor=opaque&limit=50',
    )
    const filters = auditFiltersFromSearchParams(source)
    assert.equal(filters.method, 'POST')
    assert.equal(filters.limit, 50)
    assert.equal(filters.urlError, '')
    assert.equal(
        auditFiltersToSearchParams(filters).toString(),
        'actor=alice&platform_role=security_auditor&action=platform.user.update' +
            '&method=POST&path_prefix=%2Fapi%2Fplatform&status=403&result=error' +
            '&keyword=denied&time_preset=24h&cursor=opaque&limit=50',
    )
    assert.deepEqual(auditFiltersToQuery(filters), {
        limit: 50,
        actor: 'alice',
        platform_role: 'security_auditor',
        action: 'platform.user.update',
        method: 'POST',
        path_prefix: '/api/platform',
        status: 403,
        result: 'error',
        keyword: 'denied',
        time_preset: '24h',
        cursor: 'opaque',
    })
})

test('audit explorer uses bounded defaults and custom ranges', () => {
    const filters = auditFiltersFromSearchParams(
        new URLSearchParams(
            'start_time=2026-07-01T00%3A00%3A00Z' +
                '&end_time=2026-07-31T00%3A00%3A00Z',
        ),
    )
    assert.equal(filters.limit, 25)
    assert.deepEqual(auditFiltersToQuery(filters), {
        limit: 25,
        start_time: '2026-07-01T00:00:00.000Z',
        end_time: '2026-07-31T00:00:00.000Z',
    })
})

test('invalid URL filters are explicit and cannot expand into an unfiltered query', () => {
    for (const query of [
        'platform_role=administrator',
        'method=CONNECT',
        'result=maybe',
        'time_preset=forever',
        'limit=101',
        'limit=not-a-number',
    ]) {
        const filters = auditFiltersFromSearchParams(
            new URLSearchParams(query),
        )
        assert.match(filters.urlError, /URL 中的/u)
        assert.throws(
            () => auditFiltersToQuery(filters),
            /参数无效/u,
        )
        assert.equal(
            auditFiltersToSearchParams(filters).get(
                new URLSearchParams(query).keys().next().value,
            ),
            null,
        )
    }
})

test('unknown duplicate and mutually exclusive URL parameters are rejected', () => {
    for (const query of [
        'role=admin',
        'platform_role=member&platform_role=security_auditor',
        'time_preset=24h&start_time=2026-07-31T00%3A00%3A00Z',
    ]) {
        const filters = auditFiltersFromSearchParams(
            new URLSearchParams(query),
        )
        assert.match(filters.urlError, /参数|不能同时使用/u)
        assert.throws(() => auditFiltersToQuery(filters))
    }
})
