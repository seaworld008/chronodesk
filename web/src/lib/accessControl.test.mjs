import assert from 'node:assert/strict'
import test from 'node:test'

import {
    getPlatformRoleLabel,
    hasPlatformCapability,
    parsePlatformRole,
    platformRoleChoices,
} from './accessControl.ts'

test('平台角色仅接受契约中的四个精确值', () => {
    for (const role of [
        'platform_admin',
        'security_auditor',
        'emergency_operator',
        'member',
    ]) {
        assert.equal(parsePlatformRole(role), role)
    }
    for (const role of [
        'platform_owner',
        'security-reviewer',
        ' Platform_admin',
        'PLATFORM_ADMIN',
        '',
        null,
        undefined,
    ]) {
        assert.equal(parsePlatformRole(role), null)
    }
})

test('平台能力按职责精确授权并对未知值关闭', () => {
    assert.equal(
        hasPlatformCapability('platform_admin', 'manage_platform_users'),
        true,
    )
    assert.equal(
        hasPlatformCapability('security_auditor', 'view_platform_audit'),
        true,
    )
    assert.equal(
        hasPlatformCapability(
            'security_auditor',
            'manage_platform_settings',
        ),
        false,
    )
    assert.equal(
        hasPlatformCapability(
            'emergency_operator',
            'operate_emergency_controls',
        ),
        true,
    )
    assert.equal(
        hasPlatformCapability('member', 'manage_platform_users'),
        false,
    )
    assert.equal(
        hasPlatformCapability('unknown', 'view_platform_audit'),
        false,
    )
})

test('平台职责中文标签和选择项保持统一', () => {
    assert.deepEqual(platformRoleChoices, [
        { id: 'platform_admin', name: '平台管理员' },
        { id: 'security_auditor', name: '安全审计员' },
        { id: 'emergency_operator', name: '紧急运维员' },
        { id: 'member', name: '普通成员' },
    ])
    assert.equal(getPlatformRoleLabel('not-a-role'), '未知平台角色')
})
