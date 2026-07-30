import { expect, test } from '@playwright/test';
import {
    platformRoleValues,
    type PlatformRole,
    type StandardErrorEnvelope,
} from '../src/lib/generated/human-api';
import { apiRequest } from './helpers/api';
import {
    authenticatePage,
    cleanupE2EData,
    E2E_ACCOUNT_STEM,
    E2E_MARKER,
    DEFAULT_PASSWORD,
    extractData,
    extractItems,
    findUserByEmail,
    getAdminToken,
    trackE2EResource,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const accountStem = E2E_ACCOUNT_STEM;

const platformRoleLabels: Record<PlatformRole, string> = {
    platform_admin: '平台管理员',
    security_auditor: '安全审计员',
    emergency_operator: '紧急运维员',
    member: '普通成员',
};

const roleCases = platformRoleValues.map((platformRole) => ({
    platformRole,
    label: platformRoleLabels[platformRole],
}));

test.describe('平台用户管理角色与最后平台管理员保护', () => {
    test.describe.configure({ mode: 'serial' });

    test.beforeAll(() => {
        assertDestructiveE2EAllowed('平台用户管理角色 E2E');
    });

    test.afterAll(async ({ request }) => {
        await cleanupE2EData(request, {
            automationRules: false,
            tickets: false,
            notifications: false,
            emailConfig: false,
        });
    });

    test('RBAC-011 UI-013：最后一个活跃平台管理员不能被降级且提示中文', async ({
        page,
        request,
    }) => {
        const token = await getAdminToken(request);
        const response = await apiRequest<Record<string, unknown>>(
            request,
            token,
            '/api/platform/users?platform_role=platform_admin&status=active&page=1&page_size=100',
        );
        const activeAdmins = extractItems<Record<string, unknown>>(response);
        test.skip(
            activeAdmins.length !== 1,
            `当前环境有 ${activeAdmins.length} 个活跃平台管理员，不能安全执行最后平台管理员真实变更测试`,
        );

        const soleAdmin = activeAdmins[0];
        expect(typeof soleAdmin.id).toBe('number');
        await authenticatePage(page);
        await page.goto(`/#/users/${soleAdmin.id}`);
        await expect(page).toHaveURL(
            new RegExp(`#/users/${soleAdmin.id}$`),
        );
        await page
            .getByRole('tab', { name: '平台职责', exact: true })
            .click();

        await page
            .getByRole('combobox', { name: '平台职责', exact: true })
            .click();
        await page
            .getByRole('option', { name: '普通成员', exact: true })
            .click();
        const update = page.waitForResponse(
            (candidate) =>
                candidate.request().method() === 'PUT' &&
                new URL(candidate.url()).pathname ===
                    `/api/platform/users/${soleAdmin.id}`,
        );
        await page.getByRole('button', { name: '保存更改' }).click();
        const updateResponse = await update;
        expect(updateResponse.status()).toBe(409);
        const updateError =
            (await updateResponse.json()) as StandardErrorEnvelope;
        expect(updateError.code).toBe(1);
        expect(updateError.msg).toBe(
            '不能停用或降级最后一个活跃平台管理员',
        );
        await expect(
            page.getByText('不能停用或降级最后一个活跃平台管理员'),
        ).toBeVisible({ timeout: 10_000 });
        await expectChineseOperations(page);

        const persisted = await apiRequest<Record<string, unknown>>(
            request,
            token,
            `/api/platform/users/${soleAdmin.id}`,
        );
        const persistedUser = (persisted.data ?? persisted) as Record<
            string,
            unknown
        >;
        expect(persistedUser.platform_role).toBe('platform_admin');
        expect(persistedUser.status).toBe('active');
    });

    test('RBAC-010 UI-013：创建并编辑四种平台角色', async ({
        page,
        request,
    }) => {
        await authenticatePage(page);
        const token = await getAdminToken(request);

        for (const roleCase of roleCases) {
            await test.step(roleCase.label, async () => {
                const username = `${accountStem}_${roleCase.platformRole}`;
                const email = `${username}@example.test`;
                const displayName = `${E2E_MARKER}${roleCase.label}-已编辑`;

                await page
                    .getByRole('menuitem', {
                        name: '平台用户管理',
                        exact: true,
                    })
                    .click();
                await expect(page).toHaveURL(/#\/users$/);
                await page
                    .getByRole('link', {
                        name: '创建用户',
                        exact: true,
                    })
                    .click();
                await expect(page).toHaveURL(/#\/users\/create$/);
                await page
                    .getByRole('tab', { name: '基本信息', exact: true })
                    .click();
                const usernameInput = page.getByRole('textbox', {
                    name: '用户名',
                    exact: true,
                });
                await expect(usernameInput).toBeVisible({
                    timeout: 15_000,
                });
                await usernameInput.fill(username);
                await page
                    .getByRole('textbox', {
                        name: '邮箱地址',
                        exact: true,
                    })
                    .fill(email);

                await page
                    .getByRole('tab', { name: '平台职责', exact: true })
                    .click();
                await page
                    .getByRole('combobox', {
                        name: '平台职责',
                        exact: true,
                    })
                    .click();
                await page
                    .getByRole('option', { name: roleCase.label, exact: true })
                    .click();

                await page.getByRole('tab', { name: '登录设置' }).click();
                await page.getByLabel('初始密码').fill(DEFAULT_PASSWORD);
                await page.getByLabel('确认密码').fill(DEFAULT_PASSWORD);

                const create = page.waitForResponse(
                    (candidate) =>
                        candidate.request().method() === 'POST' &&
                        new URL(candidate.url()).pathname ===
                            '/api/platform/users',
                );
                await page.getByRole('button', { name: '创建用户' }).click();
                const createResponse = await create;
                expect(createResponse.status()).toBe(201);
                const createPayload = extractData<Record<string, unknown>>(
                    await createResponse.json(),
                );
                expect(typeof createPayload.id).toBe('number');
                trackE2EResource('users', createPayload.id as number);
                await page.waitForURL(/#\/users\/\d+\/show/, {
                    timeout: 15_000,
                });

                const created = await findUserByEmail(request, token, email);
                expect(created?.platform_role).toBe(roleCase.platformRole);
                expect(typeof created?.id).toBe('number');
                expect(created?.id).toBe(createPayload.id);

                await page.goto(`/#/users/${created!.id}`);
                await page.getByRole('tab', { name: '个人信息' }).click();
                await page.getByLabel('显示名称').fill(displayName);
                const update = page.waitForResponse(
                    (candidate) =>
                        candidate.request().method() === 'PUT' &&
                        new URL(candidate.url()).pathname ===
                            `/api/platform/users/${created!.id}`,
                );
                await page.getByRole('button', { name: '保存更改' }).click();
                expect((await update).status()).toBe(200);

                const edited = await findUserByEmail(request, token, email);
                expect(edited?.display_name).toBe(displayName);
                expect(edited?.platform_role).toBe(roleCase.platformRole);
            });
        }
    });
});
