import { expect, test } from '@playwright/test';
import {
    authenticatePage,
    captureAgentGlobalControls,
    cleanupTrackedAgentPrincipals,
    E2E_MARKER,
    extractData,
    restoreAgentGlobalControls,
    trackE2EResource,
    type AgentGlobalControlSnapshot,
} from './helpers/testData';
import {
    assertDestructiveE2EAllowed,
    assertGlobalE2EAllowed,
} from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const principalName = `${E2E_MARKER}工单协作智能体`;
const principalRowName = new RegExp(
    principalName.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&'),
    'u',
);

test.describe('AI 智能体控制中心', () => {
    test.describe.configure({ mode: 'serial' });
    let globalControlsBeforeTest: AgentGlobalControlSnapshot;

    test.beforeEach(async ({ request }) => {
        assertDestructiveE2EAllowed('Agent 控制中心写操作 E2E');
        globalControlsBeforeTest = await captureAgentGlobalControls(request);
    });

    test.afterEach(async ({ request }) => {
        await restoreAgentGlobalControls(request, globalControlsBeforeTest);
    });

    test.afterAll(async ({ request }) => {
        await cleanupTrackedAgentPrincipals(request);
    });

    test('AGT-009 UI-020 UI-021：页面完整且全局只读、紧急停止均可安全恢复', async ({
        page,
        request,
    }) => {
        assertGlobalE2EAllowed('Agent 全局控制 E2E');
        try {
            await authenticatePage(page);
            await page.goto('/#/agent-control');
            const main = page.getByRole('main');
            await expect(
                main.getByRole('heading', {
                    name: 'AI 智能体控制中心',
                    exact: true,
                }),
            ).toBeVisible({ timeout: 15_000 });

            for (const helper of [
                '可签发令牌的服务主体',
                '正在处理的工单',
                '最近一页领域事件',
                '需要关注的事件投递记录',
            ] as const) {
                await expect(
                    main.getByText(helper, { exact: true }),
                ).toBeVisible();
            }

            const tabs = [
                ['服务主体', '服务主体列表'],
                ['实时租约', '工单租约列表'],
                ['领域事件', '领域事件列表'],
                ['事件投递（Outbox）', '事件投递列表'],
                ['策略审计', '智能体策略决策审计'],
            ] as const;
            for (const [tab, table] of tabs) {
                await main.getByRole('tab', { name: tab, exact: true }).click();
                await expect(
                    main.getByRole('table', { name: table, exact: true }),
                ).toBeVisible();
            }
            await main
                .getByRole('tab', { name: '服务主体', exact: true })
                .click();

            const readOnly = main.getByLabel('智能体全局只读模式', {
                exact: true,
            });
            await expect(readOnly).toBeChecked({
                checked: globalControlsBeforeTest.global_read_only,
            });
            const setReadOnly = async (enabled: boolean) => {
                await readOnly.click();
                const confirmation = page.getByRole('dialog', {
                    name: enabled
                        ? '确认开启全局只读'
                        : '确认恢复智能体写操作',
                });
                if (enabled) {
                    await expect(confirmation).toContainText(
                        '所有智能体写操作都会被策略层拒绝',
                    );
                }
                const response = page.waitForResponse(
                    (candidate) =>
                        candidate.request().method() === 'PUT' &&
                        new URL(candidate.url()).pathname ===
                            '/api/v1/admin/agent-control/read-only',
                );
                await confirmation
                    .getByRole('button', {
                        name: enabled ? '开启全局只读' : '恢复写操作',
                    })
                    .click();
                expect((await response).status()).toBe(200);
                await expect(readOnly).toBeChecked({ checked: enabled });
                await expect(
                    page.getByText(
                        enabled
                            ? '智能体全局只读模式已开启'
                            : '智能体写操作已恢复',
                        { exact: true },
                    ),
                ).toBeVisible();
            };
            await setReadOnly(!globalControlsBeforeTest.global_read_only);
            await setReadOnly(globalControlsBeforeTest.global_read_only);

            const emergencyStop = main.getByLabel('智能体全局紧急停止', {
                exact: true,
            });
            await expect(emergencyStop).toBeChecked({
                checked: globalControlsBeforeTest.emergency_stop,
            });
            const setEmergencyStop = async (enabled: boolean) => {
                await emergencyStop.click();
                const confirmation = page.getByRole('dialog', {
                    name: enabled
                        ? '确认全局紧急停止'
                        : '确认解除全局紧急停止',
                });
                if (enabled) {
                    await expect(confirmation).toContainText(
                        '所有智能体请求会立即被拒绝',
                    );
                }
                const response = page.waitForResponse(
                    (candidate) =>
                        candidate.request().method() === 'PUT' &&
                        new URL(candidate.url()).pathname ===
                            '/api/v1/admin/agent-control/emergency-stop',
                );
                await confirmation
                    .getByRole('button', {
                        name: enabled
                            ? '立即停止全部智能体'
                            : '解除紧急停止',
                    })
                    .click();
                expect((await response).status()).toBe(200);
                await expect(emergencyStop).toBeChecked({ checked: enabled });
                await expect(
                    page.getByText(
                        enabled
                            ? '智能体全局紧急停止已启用'
                            : '智能体全局紧急停止已解除',
                        { exact: true },
                    ),
                ).toBeVisible();
            };
            await setEmergencyStop(!globalControlsBeforeTest.emergency_stop);
            await setEmergencyStop(globalControlsBeforeTest.emergency_stop);
            await expectChineseOperations(page);
        } finally {
            await restoreAgentGlobalControls(
                request,
                globalControlsBeforeTest,
            );
        }
    });

    test('AGT-001 AGT-002 AGT-008 UI-020：服务主体、策略、凭据与单体熔断', async ({
        page,
    }) => {
        await authenticatePage(page);
        await page.goto('/#/agent-control');
        const main = page.getByRole('main');
        await expect(
            main.getByRole('heading', {
                name: 'AI 智能体控制中心',
                exact: true,
            }),
        ).toBeVisible({ timeout: 15_000 });

        await main
            .getByRole('button', { name: '新建智能体', exact: true })
            .click();
        let dialog = page.getByRole('dialog', {
            name: '新建 AI 智能体服务主体',
        });
        await dialog.getByLabel('名称').fill(principalName);
        await dialog
            .getByLabel('说明')
            .fill('Playwright E2E 最小权限服务主体');
        await dialog.getByLabel('权限范围（Scope）').click();
        await page
            .getByRole('option', { name: '更新工单（tickets:update）' })
            .click();
        await page.keyboard.press('Escape');
        const create = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    '/api/v1/admin/service-principals',
        );
        await dialog
            .getByRole('button', { name: '创建并签发凭据' })
            .click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(201);
        const createdPrincipal = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof createdPrincipal.client_id).toBe('string');
        trackE2EResource(
            'agentPrincipals',
            createdPrincipal.client_id as string,
        );

        dialog = page.getByRole('dialog', { name: '保存一次性凭据' });
        const firstSecret = await dialog
            .getByRole('textbox', {
                name: '客户端密钥',
                exact: true,
            })
            .inputValue();
        expect(firstSecret.length).toBeGreaterThan(20);
        await dialog.getByRole('button', { name: '我已安全保存' }).click();
        await expect(dialog).toHaveCount(0);

        const principalTable = main.getByRole('table', {
            name: '服务主体列表',
            exact: true,
        });
        const row = principalTable.getByRole('row', {
            name: principalRowName,
        });
        await expect(row).toBeVisible({ timeout: 15_000 });
        await expect(row).toContainText('读取工单');
        await expect(row).toContainText('更新工单');

        await row
            .getByRole('button', { name: `管理 ${principalName} 的策略` })
            .click();
        dialog = page.getByRole('dialog', {
            name: `${principalName} · 权限范围策略`,
        });
        const activePolicyButtons = dialog.getByRole('button', {
            name: '停用',
            exact: true,
        });
        const initialActivePolicyCount = await activePolicyButtons.count();
        await dialog.getByLabel('操作（可选）').fill('ticket.update');
        let createPolicy = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/api\/v1\/admin\/service-principals\/[^/]+\/policies$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await dialog.getByRole('button', { name: '新增策略' }).click();
        expect((await createPolicy).status()).toBe(201);
        await expect(dialog).toContainText('更新工单');
        await expect(activePolicyButtons).toHaveCount(
            initialActivePolicyCount + 1,
        );

        await dialog.getByLabel('效果').click();
        await page.getByRole('option', { name: '允许', exact: true }).click();
        await dialog.getByLabel('权限范围（Scope）').click();
        await page
            .getByRole('option', {
                name: '流转工单状态（tickets:transition）',
            })
            .click();
        await dialog
            .getByLabel('操作（可选）')
            .fill('ticket.transition');
        await dialog.getByRole('button', { name: '新增策略' }).click();
        let confirmation = page.getByRole('dialog', {
            name: '确认新增允许策略',
        });
        createPolicy = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/api\/v1\/admin\/service-principals\/[^/]+\/policies$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await confirmation
            .getByRole('button', { name: '确认授予权限' })
            .click();
        expect((await createPolicy).status()).toBe(201);
        await expect(dialog).toContainText('流转工单状态');
        await expect(activePolicyButtons).toHaveCount(
            initialActivePolicyCount + 2,
        );
        await dialog.getByRole('button', { name: '关闭' }).click();

        await row
            .getByRole('button', { name: `轮换 ${principalName} 的凭据` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认轮换智能体凭据',
        });
        const rotate = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                /\/credentials\/rotate$/.test(
                    new URL(response.url()).pathname,
                ),
        );
        await confirmation
            .getByRole('button', { name: '轮换并撤销旧凭据' })
            .click();
        expect((await rotate).status()).toBe(200);
        dialog = page.getByRole('dialog', { name: '保存一次性凭据' });
        const rotatedSecret = await dialog
            .getByRole('textbox', {
                name: '客户端密钥',
                exact: true,
            })
            .inputValue();
        expect(rotatedSecret.length).toBeGreaterThan(20);
        expect(rotatedSecret === firstSecret).toBe(false);
        await dialog.getByRole('button', { name: '我已安全保存' }).click();

        await row
            .getByRole('button', { name: `启用 ${principalName} 的熔断` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认立即熔断智能体',
        });
        await confirmation
            .getByRole('button', { name: '立即熔断' })
            .click();
        await expect(
            row.getByText('已熔断', { exact: true }),
        ).toBeVisible({ timeout: 10_000 });

        await row
            .getByRole('button', { name: `解除 ${principalName} 的熔断` })
            .click();
        confirmation = page.getByRole('dialog', {
            name: '确认解除智能体熔断',
        });
        await confirmation
            .getByRole('button', { name: '解除熔断' })
            .click();
        await expect(
            row.getByText('已熔断', { exact: true }),
        ).toHaveCount(0, {
            timeout: 10_000,
        });

        await row.getByRole('button', { name: '停用', exact: true }).click();
        confirmation = page.getByRole('dialog', {
            name: '确认停用智能体',
        });
        await confirmation
            .getByRole('button', { name: '确认停用' })
            .click();
        await expect(
            row.getByText('停用', { exact: true }),
        ).toBeVisible({ timeout: 10_000 });
        await expectChineseOperations(page);
    });
});
