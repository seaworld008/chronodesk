import { test, expect } from '@playwright/test';
import {
    authenticatePage,
    cleanupE2EData,
    createNotification,
    deleteNotification,
    ensureRoleAccounts,
    E2E_PREFIX,
} from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

const CUSTOMER_USER = {
    email: 'customer@example.com',
    password: 'Admin123!',
};

test.describe('Ticket Workflow', () => {
    test.describe.configure({ mode: 'serial' });
    let customerNotificationId: number | null = null;

    test.beforeAll(async ({ request }) => {
        await ensureRoleAccounts(request);
    });

    test.afterAll(async ({ request }) => {
        if (customerNotificationId) {
            await deleteNotification(request, customerNotificationId);
        }
        await cleanupE2EData(request, {
            automationRules: false,
            notifications: false,
            users: false,
            emailConfig: false,
        });
    });

    test('should create assign resolve close and delete ticket', async ({ page }) => {
        const title = `${E2E_PREFIX}工单-${Date.now()}`;
        const description = `${E2E_PREFIX}工单描述-用于E2E验证完整流程`;
        const openTicketFromList = async () => {
            await page.goto('/#/tickets');
            const searchInput = page.getByPlaceholder('搜索工单');
            await searchInput.fill(title);
            await searchInput.press('Enter');
            await expect(page.getByText(title)).toBeVisible({ timeout: 10000 });
        };
        const openTicketDetailFromList = async () => {
            await openTicketFromList();
            await page.getByText(title).click();
            await expect(page).toHaveURL(/#\/tickets\/\d+\/show/);
        };
        const expectStatusInList = async (statusLabel: string) => {
            const row = page.getByRole('row', { name: new RegExp(title) });
            await expect(row).toBeVisible({ timeout: 10000 });
            await expect(row.getByText(statusLabel)).toBeVisible({ timeout: 10000 });
        };

        await authenticatePage(page, TEST_USER);
        await page.goto('/#/tickets');
        await page.getByRole('link', { name: '创建工单' }).click();

        await expect(page).toHaveURL(/#\/tickets\/create/);
        await page.getByLabel('工单标题').fill(title);
        await page.getByLabel('详细描述').fill(description);

        const assigneeInput = page.locator('input[name="assigned_to_id"]');
        await assigneeInput.scrollIntoViewIfNeeded();
        await assigneeInput.click();
        await assigneeInput.fill('agent');
        await page.getByRole('option', { name: 'agent (Support Agent)' }).click();

        await page.getByRole('button', { name: '创建工单' }).click();
        await page.waitForURL(/#\/tickets\/\d+\/show/, { timeout: 15000 });

        await page.getByRole('link', { name: '编辑' }).click();
        await expect(page).toHaveURL(/#\/tickets\/\d+$/);

        await page.getByLabel('状态').click();
        await page.getByRole('option', { name: '已解决' }).click();
        await page.getByRole('button', { name: '保存更改' }).click();
        await openTicketFromList();
        await expectStatusInList('已解决');

        await openTicketDetailFromList();
        await page.getByRole('link', { name: '编辑' }).click();
        await page.getByLabel('状态').click();
        await page.getByRole('option', { name: '已关闭' }).click();
        await page.getByRole('button', { name: '保存更改' }).click();
        await openTicketFromList();
        await expectStatusInList('已关闭');

        await openTicketDetailFromList();
        await page.getByRole('button', { name: '删除', exact: true }).click();
        const confirmDialog = page.getByRole('dialog');
        await expect(confirmDialog).toBeVisible();

        const deleteResponse = page.waitForResponse((response) => {
            const pathname = new URL(response.url()).pathname;
            return response.request().method() === 'DELETE'
                && /^\/api\/tickets\/\d+$/.test(pathname)
                && response.ok();
        });
        await confirmDialog.getByRole('button', { name: '确认', exact: true }).click();
        await deleteResponse;

        await expect(page).toHaveURL(/#\/tickets(?:\?.*)?$/, { timeout: 15000 });
        await expect(page.getByPlaceholder('搜索工单')).toBeVisible();
    });

    test('should not load user-management filters for customer notifications', async ({ page, request }) => {
        const adminUserRequests: string[] = [];
        page.on('request', (request) => {
            if (new URL(request.url()).pathname.includes('/admin/users')) {
                adminUserRequests.push(request.url());
            }
        });

        const notificationTitle = `${E2E_PREFIX}客户通知-${Date.now()}`;
        customerNotificationId = await createNotification(request, {
            title: notificationTitle,
            content: '用于验证客户通知筛选不会读取用户管理接口',
            recipientEmail: CUSTOMER_USER.email,
        });

        await authenticatePage(page, CUSTOMER_USER);
        await page.goto('/#/notifications');
        await expect(page.getByText(notificationTitle)).toBeVisible();
        await expect(page.getByPlaceholder('搜索通知')).toBeVisible();

        await page.getByRole('button', { name: '添加筛选条件' }).click();

        await expect(page.getByRole('menuitemcheckbox', { name: '通知类型' })).toBeVisible();
        await expect(page.getByRole('menuitemcheckbox', { name: '接收者' })).toHaveCount(0);
        await expect(page.getByRole('menuitemcheckbox', { name: '发送者' })).toHaveCount(0);
        expect(adminUserRequests).toEqual([]);
    });

    test('should hide and guard agent control from customer role', async ({ page }) => {
        const agentAdminRequests: string[] = [];
        page.on('request', (request) => {
            if (new URL(request.url()).pathname.includes('/v1/admin/')) {
                agentAdminRequests.push(request.url());
            }
        });

        await authenticatePage(page, CUSTOMER_USER);

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
