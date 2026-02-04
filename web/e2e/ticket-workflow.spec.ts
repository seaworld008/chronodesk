import { test, expect } from '@playwright/test';
import { cleanupE2EData, ensureRoleAccounts, E2E_PREFIX, loginViaUI } from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Ticket Workflow', () => {
    test.beforeAll(async ({ request }) => {
        await ensureRoleAccounts(request);
    });

    test.afterAll(async ({ request }) => {
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

        await loginViaUI(page, TEST_USER);
        await page.goto('/#/tickets');

        const createLink = page.locator('a[href*="tickets/create"]').first();
        if (await createLink.count()) {
            await createLink.click();
        } else {
            await page.getByLabel('Create').click();
        }

        await expect(page).toHaveURL(/#\/tickets\/create/);
        await page.getByLabel('工单标题').fill(title);
        await page.getByLabel('详细描述').fill(description);

        const assigneeInput = page.locator('input[name="assignee_id"]');
        await assigneeInput.scrollIntoViewIfNeeded();
        await assigneeInput.click();
        await assigneeInput.fill('agent');
        await page.getByRole('option', { name: 'agent (Support Agent)' }).click();

        await page.getByRole('button', { name: '创建工单' }).click();
        await expect(page).toHaveURL(/#\/tickets\/\d+\/show/);

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
        await page.getByRole('button', { name: '删除' }).click();
        const confirmDialog = page.getByRole('dialog');
        try {
            await confirmDialog.waitFor({ state: 'visible', timeout: 3000 });
            await confirmDialog.getByRole('button', { name: '删除' }).click();
        } catch {
            // 删除可能直接跳转到列表，无需确认弹窗
        }
        await expect(page).toHaveURL(/#\/tickets/);
    });
});
