import { test, expect, type Locator } from '@playwright/test';
import {
    authenticatePage,
    cleanupE2EData,
    createNotification,
    deleteNotification,
    ensureRoleAccounts,
    E2E_MARKER,
    extractData,
    getAdminToken,
    projectAPIPath,
    resolveE2EProjectKey,
    trackE2EResource,
    untrackE2EResource,
    type TemporaryRoleAccounts,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Ticket Workflow', () => {
    test.describe.configure({ mode: 'serial' });
    let requesterNotificationId: number | null = null;
    let roleAccounts: TemporaryRoleAccounts;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('工单生命周期与临时角色 E2E');
        roleAccounts = await ensureRoleAccounts(request);
    });

    test.afterEach(async ({ request }) => {
        // Playwright retries serial groups in a fresh worker. Clean any ticket
        // tracked before a failed assertion so a retry never inherits a
        // half-completed lifecycle from the previous worker.
        await cleanupE2EData(request, {
            automationRules: false,
            tickets: true,
            notifications: false,
            users: false,
            emailConfig: false,
        });
    });

    test.afterAll(async ({ request }) => {
        if (requesterNotificationId) {
            await deleteNotification(request, requesterNotificationId);
        }
        await cleanupE2EData(request, {
            automationRules: false,
            notifications: false,
            users: true,
            emailConfig: false,
        });
    });

    test('should create assign resolve close and delete ticket', async ({
        page,
        request,
    }) => {
        test.setTimeout(60_000);
        const title = `${E2E_MARKER}生命周期工单`;
        const description = `${E2E_MARKER}工单描述-用于E2E验证完整流程`;
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const ticketsPath = projectAPIPath(projectKey, 'tickets');
        const ticketListMain = () => page.getByRole('main');
        const ticketRow = () =>
            ticketListMain()
                .getByRole('row')
                .filter({ hasText: title });
        const waitForTicketList = async () => {
            await expect(page).toHaveURL(/#\/tickets(?:\?.*)?$/, {
                timeout: 15_000,
            });
            const searchInput =
                ticketListMain().getByPlaceholder('搜索工单');
            await expect(searchInput).toBeVisible({ timeout: 15_000 });
            return searchInput;
        };
        const openTicketFromList = async () => {
            await page.goto('/#/tickets');
            const searchInput = await waitForTicketList();
            await searchInput.fill(title);
            await searchInput.press('Enter');
            await expect(ticketRow()).toBeVisible({ timeout: 10_000 });
        };
        const openTicketDetailFromList = async () => {
            await openTicketFromList();
            await ticketRow()
                .getByRole('link', { name: '查看', exact: true })
                .click();
            await expect(page).toHaveURL(/#\/tickets\/\d+\/show/);
        };
        const openTicketEditFromDetail = async () => {
            await page
                .getByRole('main')
                .getByRole('link', { name: '编辑', exact: true })
                .click();
            await expect(page).toHaveURL(/#\/tickets\/\d+$/, {
                timeout: 15_000,
            });
            const editForm = page.getByRole('main').locator('form');
            await expect(editForm).toBeVisible({ timeout: 15_000 });
            return editForm;
        };
        const updateTicketStatus = async (
            editForm: Locator,
            ticketID: number,
            statusLabel: '处理中' | '已解决' | '已关闭',
        ) => {
            const statusInput = editForm.getByRole('combobox', {
                name: '状态',
                exact: true,
            });
            await expect(statusInput).toBeVisible({ timeout: 15_000 });
            await statusInput.click();
            await page
                .getByRole('option', { name: statusLabel, exact: true })
                .click();
            const update = page.waitForResponse(
                (response) =>
                    response.request().method() === 'PUT' &&
                    new URL(response.url()).pathname ===
                        `${ticketsPath}/${encodeURIComponent(
                            String(ticketID),
                        )}`,
            );
            await editForm
                .getByRole('button', { name: '保存更改', exact: true })
                .click();
            const updated = await update;
            expect(
                updated.status(),
                `工单更新失败：${await updated.text()}`,
            ).toBe(200);
        };
        const expectStatusInList = async (statusLabel: string) => {
            const row = ticketRow();
            await expect(row).toBeVisible({ timeout: 10000 });
            await expect(row.getByText(statusLabel)).toBeVisible({ timeout: 10000 });
        };

        await authenticatePage(page, TEST_USER);
        await page.goto('/#/tickets');
        await page.getByRole('link', { name: '创建工单' }).click();

        await expect(page).toHaveURL(/#\/tickets\/create/);
        await page.getByLabel('工单标题').fill(title);
        await page.getByLabel('详细描述').fill(description);

        const assigneeInput = page.getByRole('combobox', {
            name: /^分配给/u,
        });
        await assigneeInput.scrollIntoViewIfNeeded();
        await assigneeInput.click();
        await assigneeInput.fill(roleAccounts.agent.username);
        await page
            .getByRole('option', { name: roleAccounts.agent.optionLabel })
            .click();

        const create = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === ticketsPath,
        );
        await page.getByRole('button', { name: '创建工单' }).click();
        const createResponse = await create;
        expect(createResponse.status()).toBe(201);
        const createdTicket = extractData<Record<string, unknown>>(
            await createResponse.json(),
        );
        expect(typeof createdTicket.id).toBe('number');
        trackE2EResource('tickets', createdTicket.id as number);
        await page.waitForURL(/#\/tickets\/\d+\/show/, { timeout: 15000 });

        const createdTicketID = createdTicket.id as number;
        const firstEditForm = await openTicketEditFromDetail();
        await updateTicketStatus(
            firstEditForm,
            createdTicketID,
            '处理中',
        );
        await openTicketFromList();
        await expectStatusInList('处理中');

        await openTicketDetailFromList();
        const resolveEditForm = await openTicketEditFromDetail();
        await updateTicketStatus(
            resolveEditForm,
            createdTicketID,
            '已解决',
        );
        await openTicketFromList();
        await expectStatusInList('已解决');

        await openTicketDetailFromList();
        const closeEditForm = await openTicketEditFromDetail();
        await updateTicketStatus(
            closeEditForm,
            createdTicketID,
            '已关闭',
        );
        await openTicketFromList();
        await expectStatusInList('已关闭');

        await openTicketDetailFromList();
        await page.getByRole('button', { name: '删除', exact: true }).click();
        const confirmDialog = page.getByRole('dialog');
        await expect(confirmDialog).toBeVisible();

        const deleteResponse = page.waitForResponse((response) => {
            const pathname = new URL(response.url()).pathname;
            return response.request().method() === 'DELETE'
                && pathname ===
                    `${ticketsPath}/${encodeURIComponent(
                        String(createdTicket.id),
                    )}`;
        });
        await confirmDialog.getByRole('button', { name: '确认', exact: true }).click();
        const deleted = await deleteResponse;
        expect(
            deleted.request().headers()['if-match'],
            '工单删除必须携带强 ETag 版本前置条件',
        ).toMatch(/^"v[1-9]\d*"$/);
        expect(
            deleted.status(),
            `工单删除失败：${await deleted.text()}`,
        ).toBe(200);
        untrackE2EResource('tickets', createdTicket.id as number);

        // 删除后的 SPA 重定向与列表首次加载分别验证，避免把 React 懒加载
        // 挂载时序误当成删除失败，同时仍要求规范列表路由可独立完整加载。
        await expect(page).toHaveURL(/#\/tickets(?:\?.*)?$/, {
            timeout: 15_000,
        });
        await page.goto('/#/tickets');
        await waitForTicketList();
    });

    test('requester 通知筛选不加载平台用户管理接口', async ({ page, request }) => {
        const platformUserRequests: string[] = [];
        page.on('request', (request) => {
            if (new URL(request.url()).pathname.includes('/platform/users')) {
                platformUserRequests.push(request.url());
            }
        });

        const notificationTitle = `${E2E_MARKER}请求者通知`;
        requesterNotificationId = await createNotification(request, {
            title: notificationTitle,
            content: '用于验证请求者通知筛选不会读取平台用户管理接口',
            recipientEmail: roleAccounts.requester.email,
        });

        await authenticatePage(page, roleAccounts.requester);
        await page.goto('/#/notifications');
        await expect(page.getByText(notificationTitle)).toBeVisible();
        await expect(page.getByPlaceholder('搜索通知')).toBeVisible();

        await page.getByRole('button', { name: '添加筛选条件' }).click();

        await expect(page.getByRole('menuitemcheckbox', { name: '通知类型' })).toBeVisible();
        await expect(page.getByRole('menuitemcheckbox', { name: '接收者' })).toHaveCount(0);
        await expect(page.getByRole('menuitemcheckbox', { name: '发送者' })).toHaveCount(0);
        expect(platformUserRequests).toEqual([]);
    });

    test('requester 项目角色隐藏并拦截 Agent 控制面', async ({ page }) => {
        const agentAdminRequests: string[] = [];
        page.on('request', (request) => {
            if (
                /^\/api\/projects\/[^/]+\/admin\/agents\//.test(
                    new URL(request.url()).pathname,
                )
            ) {
                agentAdminRequests.push(request.url());
            }
        });

        await authenticatePage(page, roleAccounts.requester);

        await expect(
            page.getByRole('menuitem', { name: 'AI 智能体控制' }),
        ).toHaveCount(0);

        await page.goto('/#/agent-control');

        await expect(page).toHaveURL(/#\/?$/);
        await expect(
            page.getByRole('main').getByRole('heading', { name: '工单运营总览' }),
        ).toBeVisible();
        await expect(
            page.getByRole('heading', { name: 'AI 智能体控制中心' }),
        ).toHaveCount(0);
        expect(agentAdminRequests).toEqual([]);
    });
});
