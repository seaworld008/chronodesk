import { test, expect } from '@playwright/test';
import { apiRequest } from './helpers/api';
import {
    authenticatePage,
    captureSystemConfig,
    e2eRunID,
    getAdminToken,
    restoreSystemConfig,
} from './helpers/testData';
import {
    assertDestructiveE2EAllowed,
    assertGlobalE2EAllowed,
} from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};
const configKey = `security.e2e_${e2eRunID()
    .replace(/[^a-z0-9]+/gu, '_')
    .slice(0, 48)}_password_min_length`;

test.describe('System Settings', () => {
    let configCreated = false;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('系统安全配置 E2E');
        assertGlobalE2EAllowed('系统安全配置 E2E');
        const token = await getAdminToken(request);
        await apiRequest(request, token, '/api/platform/configs', {
            method: 'POST',
            data: {
                key: configKey,
                value: '8',
                value_type: 'int',
                description: 'Playwright 本轮密码长度配置',
                category: 'security',
                // 放在稳定排序的首组，避免其他 E2E 生成的大量安全配置
                // 把本用例目标挤到后续页；跨页草稿另由下方 mock 用例覆盖。
                group: '00-e2e',
                is_required: false,
                is_active: true,
                default_value: '8',
            },
        });
        configCreated = true;
    });

    test.afterAll(async ({ request }) => {
        if (!configCreated) {
            return;
        }
        const token = await getAdminToken(request);
        await apiRequest(
            request,
            token,
            `/api/platform/configs/${encodeURIComponent(configKey)}`,
            { method: 'DELETE' },
        );
    });

    test('UI-017：安全配置使用修改前快照并在 finally 恢复', async ({
        page,
        request,
    }) => {
        const key = configKey;
        const original = await captureSystemConfig(request, 'security', key);
        const numericValue = Number(original.value || '8');
        const updatedValue = String(numericValue + 1);
        const expectedAfterTest = { ...original, value: updatedValue };

        try {
            await authenticatePage(page, TEST_USER);
            const firstPageResponse = page.waitForResponse(
                (response) =>
                    response.request().method() === 'GET' &&
                    new URL(response.url()).pathname ===
                        '/api/platform/configs',
            );
            await page.goto('/#/system-settings');
            await page
                .getByRole('main')
                .getByRole('heading', {
                    name: '平台公共配置',
                    exact: true,
                })
                .waitFor({ timeout: 15_000 });
            const firstPageURL = new URL((await firstPageResponse).url());
            expect(firstPageURL.searchParams.get('page')).toBe('1');
            expect(firstPageURL.searchParams.get('page_size')).toBe('25');
            expect(firstPageURL.searchParams.get('sort_by')).toBe('group');
            expect(firstPageURL.searchParams.get('sort_order')).toBe('asc');
            await page
                .getByRole('tab', { name: '安全策略', exact: true })
                .click();

            const table = page.getByRole('table', {
                name: '系统配置列表',
                exact: true,
            });
            const row = table.getByRole('row', {
                name: new RegExp(key.replaceAll('.', '\\.'), 'u'),
            });
            const valueInput = row.getByRole('spinbutton');
            await expect(valueInput).toHaveValue(String(numericValue));
            await valueInput.fill(updatedValue);
            await page
                .getByRole('tab', { name: '系统信息', exact: true })
                .click();
            await page
                .getByRole('tab', { name: '安全策略', exact: true })
                .click();
            await expect(
                table
                    .getByRole('row', {
                        name: new RegExp(
                            key.replaceAll('.', '\\.'),
                            'u',
                        ),
                    })
                    .getByRole('spinbutton'),
            ).toHaveValue(updatedValue);
            const update = page.waitForResponse(
                (response) =>
                    response.request().method() === 'PUT' &&
                    new URL(response.url()).pathname ===
                        `/api/platform/configs/${key}`,
            );
            await row
                .getByRole('button', {
                    name: `保存配置：${key}`,
                    exact: true,
                })
                .click();
            expect((await update).status()).toBe(200);
            await expect(page.getByText('配置已更新')).toBeVisible({
                timeout: 10_000,
            });
            await expectChineseOperations(page);

            await page
                .getByRole('main')
                .getByRole('button', { name: '刷新', exact: true })
                .click();
            const refreshedRow = table.getByRole('row', {
                name: new RegExp(key.replaceAll('.', '\\.'), 'u'),
            });
            await expect(refreshedRow.getByRole('spinbutton')).toHaveValue(
                updatedValue,
            );

            await refreshedRow
                .getByRole('spinbutton')
                .fill(String(Number(updatedValue) + 1));
            await page
                .getByRole('menuitem', {
                    name: '邮件外发',
                    exact: true,
                })
                .click();
            const leaveDialog = page.getByRole('dialog', {
                name: '存在未保存的平台配置',
                exact: true,
            });
            await expect(leaveDialog).toContainText('1 项修改尚未保存');
            await leaveDialog
                .getByRole('button', {
                    name: '继续编辑',
                    exact: true,
                })
                .click();
            await expect(page).toHaveURL(/#\/system-settings$/u);
            await page
                .getByRole('menuitem', {
                    name: '邮件外发',
                    exact: true,
                })
                .click();
            await page
                .getByRole('dialog', {
                    name: '存在未保存的平台配置',
                    exact: true,
                })
                .getByRole('button', {
                    name: '放弃修改并离开',
                    exact: true,
                })
                .click();
            await expect(page).toHaveURL(/#\/system-settings\/email$/u);
        } finally {
            await restoreSystemConfig(request, original, expectedAfterTest);
        }
    });
});

test.describe('System Settings 分页草稿 mock', () => {
    test('mock：跨页跨分类保留草稿并保存全部修改', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                platformRole: 'platform_admin',
                sessionID: 'e2e-system-config-page',
            },
            projectA,
        );
        const configs = [
            ...Array.from({ length: 30 }, (_, index) => ({
                id: index + 1,
                created_at: '2026-07-31T08:00:00Z',
                updated_at: '2026-07-31T08:00:00Z',
                key: `system.mock.${String(index + 1).padStart(3, '0')}`,
                value: `value-${index + 1}`,
                value_type: 'string',
                description: `系统配置 ${index + 1}`,
                category: 'system',
                group: 'mock',
                is_required: false,
                is_active: true,
                default_value: '',
                valid_values: '',
                version: 1,
            })),
            {
                id: 1001,
                created_at: '2026-07-31T08:00:00Z',
                updated_at: '2026-07-31T08:00:00Z',
                key: 'security.mock.001',
                value: 'security-before',
                value_type: 'string',
                description: '安全配置',
                category: 'security',
                group: 'mock',
                is_required: false,
                is_active: true,
                default_value: '',
                valid_values: '',
                version: 1,
            },
        ];
        const listQueries: string[] = [];
        const updates: Array<Record<string, unknown>> = [];
        await page.route('**/api/**', async (route) => {
            const request = route.request();
            const url = new URL(request.url());
            if (url.pathname === '/api/projects') {
                await fulfillJSON(route, {
                    code: 0,
                    data: [
                        authorizedProjectAccess(
                            projectA,
                            'project_admin',
                        ),
                    ],
                });
                return;
            }
            if (
                url.pathname === '/api/platform/configs' &&
                request.method() === 'GET'
            ) {
                listQueries.push(url.search);
                const category =
                    url.searchParams.get('category') ?? 'system';
                const pageNumber = Number(
                    url.searchParams.get('page') ?? '1',
                );
                const pageSize = Number(
                    url.searchParams.get('page_size') ?? '25',
                );
                const matching = configs.filter(
                    (config) => config.category === category,
                );
                const start = (pageNumber - 1) * pageSize;
                await fulfillJSON(route, {
                    success: true,
                    data: {
                        items: matching.slice(start, start + pageSize),
                        total: matching.length,
                        page: pageNumber,
                        page_size: pageSize,
                        total_pages: matching.length === 0
                            ? 0
                            : Math.ceil(matching.length / pageSize),
                    },
                });
                return;
            }
            const updateMatch = url.pathname.match(
                /^\/api\/platform\/configs\/(.+)$/u,
            );
            if (updateMatch && request.method() === 'PUT') {
                const key = decodeURIComponent(updateMatch[1]);
                const payload =
                    request.postDataJSON() as Record<string, unknown>;
                updates.push({ key, ...payload });
                const existing = configs.find(
                    (config) => config.key === key,
                );
                if (existing && typeof payload.value === 'string') {
                    existing.value = payload.value;
                }
                await fulfillJSON(route, {
                    success: true,
                    data: payload,
                });
                return;
            }
            await fulfillJSON(route, { code: 0, data: [] });
        });

        await page.goto('/#/system-settings');
        const systemInput = page.getByLabel(
            '配置“system.mock.001”的值',
        );
        await expect(systemInput).toHaveValue('value-1');
        await systemInput.fill('system-draft');
        await page
            .getByRole('button', { name: /下一页|next page/iu })
            .click();
        await expect(
            page.getByLabel('配置“system.mock.026”的值'),
        ).toBeVisible();
        await page
            .getByRole('button', { name: /上一页|previous page/iu })
            .click();
        await expect(systemInput).toHaveValue('system-draft');

        await page
            .getByRole('tab', { name: '安全策略', exact: true })
            .click();
        await page
            .getByLabel('配置“security.mock.001”的值')
            .fill('security-draft');
        await page
            .getByRole('button', {
                name: '保存全部 (2)',
                exact: true,
            })
            .click();
        await expect(
            page.getByRole('button', {
                name: '保存全部 (0)',
                exact: true,
            }),
        ).toBeDisabled();
        expect(updates).toEqual(
            expect.arrayContaining([
                expect.objectContaining({
                    key: 'system.mock.001',
                    value: 'system-draft',
                }),
                expect.objectContaining({
                    key: 'security.mock.001',
                    value: 'security-draft',
                }),
            ]),
        );
        expect(listQueries.every((query) =>
            query.includes('page_size=25') &&
            query.includes('sort_by=group') &&
            query.includes('sort_order=asc'),
        )).toBe(true);
    });
});
