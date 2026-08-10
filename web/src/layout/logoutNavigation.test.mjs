import assert from 'node:assert/strict'
import test from 'node:test'
import { publicLoginHashTarget } from './logoutNavigation.ts'

test('全设备退出只替换 HashRouter 片段', () => {
    assert.equal(publicLoginHashTarget, '#/login')
    assert.equal(publicLoginHashTarget.startsWith('/login'), false)
})
