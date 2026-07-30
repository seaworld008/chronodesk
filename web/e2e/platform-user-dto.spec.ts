import { expect, test, type Page } from '@playwright/test';
import type {
    AdminUser,
    CreateAdminUserRequest,
    UpdateAdminUserRequest,
} from '../src/lib/generated/human-api';
import {
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
} from './helpers/mockHumanSession';

const createDTOFields = [
    'username',
    'email',
    'password',
    'phone',
    'first_name',
    'last_name',
    'display_name',
    'platform_role',
    'department',
    'job_title',
    'manager_id',
] as const satisfies readonly (keyof CreateAdminUserRequest)[];

const updateDTOFields = [
    'email',
    'phone',
    'first_name',
    'last_name',
    'display_name',
    'avatar',
    'timezone',
    'language',
    'platform_role',
    'status',
    'email_verified',
    'department',
    'job_title',
    'manager_id',
] as const satisfies readonly (keyof UpdateAdminUserRequest)[];

const platformAdmin = {
    ...defaultMockIdentity,
    id: 7,
    sessionID: 'e2e-platform-user-dto',
    email: 'platform-admin@example.test',
    platformRole: 'platform_admin' as const,
};

const existingUser: AdminUser = {
    id: 81,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T09:00:00Z',
    username: 'strict-user',
    email: 'strict-user@example.test',
    first_name: 'Strict',
    last_name: 'User',
    display_name: '原显示名称',
    avatar: 'https://example.test/avatar.png',
    timezone: 'Asia/Shanghai',
    language: 'zh-CN',
    platform_role: 'member',
    status: 'active',
    email_verified: false,
    phone_verified: false,
    two_factor_enabled: false,
};

type UserMockState = {
    createBodies: Record<string, unknown>[];
    updateBodies: Record<string, unknown>[];
};

const objectBody = (value: unknown): Record<string, unknown> | null =>
    value !== null && typeof value === 'object' && !Array.isArray(value)
        ? (value as Record<string, unknown>)
        : null;

const unexpectedFields = (
    body: Record<string, unknown>,
    allowed: readonly string[],
): string[] => {
    const allowedSet = new Set(allowed);
    return Object.keys(body).filter((field) => !allowedSet.has(field));
};

const installStrictUserBackend = async (
    page: Page,
): Promise<UserMockState> => {
    const state: UserMockState = {
        createBodies: [],
        updateBodies: [],
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [] });
            return;
        }

        if (
            url.pathname === `/api/platform/users/${existingUser.id}` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, { code: 0, data: existingUser });
            return;
        }

        if (
            url.pathname === '/api/platform/users' &&
            request.method() === 'POST'
        ) {
            const body = objectBody(request.postDataJSON());
            if (!body) {
                await fulfillJSON(
                    route,
                    { code: 1, msg: '请求体必须是 JSON 对象' },
                    400,
                );
                return;
            }
            state.createBodies.push(body);
            const extra = unexpectedFields(body, createDTOFields);
            if (
                extra.length > 0 ||
                typeof body.username !== 'string' ||
                typeof body.email !== 'string' ||
                typeof body.password !== 'string' ||
                typeof body.platform_role !== 'string'
            ) {
                await fulfillJSON(
                    route,
                    {
                        code: 1,
                        msg: `CreateAdminUserRequest 严格解码失败：${extra.join(',')}`,
                    },
                    400,
                );
                return;
            }
            await fulfillJSON(
                route,
                {
                    code: 0,
                    data: {
                        ...existingUser,
                        ...body,
                        id: 82,
                        username: body.username,
                        email: body.email,
                    },
                },
                201,
            );
            return;
        }

        if (
            url.pathname === `/api/platform/users/${existingUser.id}` &&
            request.method() === 'PUT'
        ) {
            const body = objectBody(request.postDataJSON());
            if (!body) {
                await fulfillJSON(
                    route,
                    { code: 1, msg: '请求体必须是 JSON 对象' },
                    400,
                );
                return;
            }
            state.updateBodies.push(body);
            const extra = unexpectedFields(body, updateDTOFields);
            if (extra.length > 0) {
                await fulfillJSON(
                    route,
                    {
                        code: 1,
                        msg: `UpdateAdminUserRequest 严格解码失败：${extra.join(',')}`,
                    },
                    400,
                );
                return;
            }
            await fulfillJSON(
                route,
                {
                    code: 0,
                    data: {
                        ...existingUser,
                        ...body,
                    },
                },
                200,
            );
            return;
        }

        if (
            url.pathname === '/api/platform/users' &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [existingUser],
                    total: 1,
                    page: 1,
                    page_size: 25,
                    pages: 1,
                },
            });
            return;
        }

        await fulfillJSON(route, { code: 0, data: [] });
    });

    return state;
};

test.describe('平台用户写入严格 Human DTO', () => {
    test.beforeEach(async ({ page }) => {
        await installMockSession(page, platformAdmin);
    });

    test('创建仅发送 CreateAdminUserRequest，严格 mock 解码后返回 201', async ({
        page,
    }) => {
        const state = await installStrictUserBackend(page);
        await page.goto('/#/users/create');

        await page.getByLabel('用户名').fill('strict-created-user');
        await page
            .getByLabel('邮箱地址')
            .fill('strict-created-user@example.test');

        await page.getByRole('tab', { name: '个人信息' }).click();
        await page.getByLabel('显示名称').fill('严格 DTO 新用户');

        await page.getByRole('tab', { name: '平台职责' }).click();
        await page
            .getByRole('combobox', { name: '平台职责' })
            .click();
        await page
            .getByRole('option', { name: '安全审计员', exact: true })
            .click();

        await page.getByRole('tab', { name: '登录设置' }).click();
        await page.getByLabel('初始密码').fill('StrictPass123!');
        await page.getByLabel('确认密码').fill('StrictPass123!');

        const createResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === '/api/platform/users',
        );
        await page.getByRole('button', { name: '创建用户' }).click();
        expect((await createResponse).status()).toBe(201);

        expect(state.createBodies).toHaveLength(1);
        expect(state.createBodies[0]).toEqual({
            username: 'strict-created-user',
            email: 'strict-created-user@example.test',
            password: 'StrictPass123!',
            display_name: '严格 DTO 新用户',
            platform_role: 'security_auditor',
        });
        expect(state.createBodies[0]).not.toHaveProperty('password_confirm');
        expect(state.createBodies[0]).not.toHaveProperty('status');
        expect(state.createBodies[0]).not.toHaveProperty('timezone');
        expect(state.createBodies[0]).not.toHaveProperty('language');
        expect(state.createBodies[0]).not.toHaveProperty('email_verified');
    });

    test('编辑仅发送 UpdateAdminUserRequest，严格 mock 解码后返回 200', async ({
        page,
    }) => {
        const state = await installStrictUserBackend(page);
        await page.goto(`/#/users/${existingUser.id}`);

        await page
            .getByLabel('邮箱地址')
            .fill('strict-user-updated@example.test');
        await page.getByRole('tab', { name: '个人信息' }).click();
        await page.getByLabel('显示名称').fill('严格 DTO 已更新');

        const updateResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'PUT' &&
                new URL(response.url()).pathname ===
                    `/api/platform/users/${existingUser.id}`,
        );
        await page.getByRole('button', { name: '保存更改' }).click();
        expect((await updateResponse).status()).toBe(200);

        expect(state.updateBodies).toHaveLength(1);
        expect(state.updateBodies[0]).toMatchObject({
            email: 'strict-user-updated@example.test',
            display_name: '严格 DTO 已更新',
            platform_role: 'member',
            status: 'active',
            email_verified: false,
        });
        for (const forbidden of [
            'id',
            'username',
            'created_at',
            'updated_at',
            'password',
            'password_confirm',
            'phone_verified',
            'two_factor_enabled',
        ]) {
            expect(state.updateBodies[0]).not.toHaveProperty(forbidden);
        }
    });
});
