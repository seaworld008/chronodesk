import { test, expect } from '@playwright/test';
import {
    authenticatePage,
    createTicket,
    deleteTicket,
    E2E_MARKER,
    loginViaUI,
} from './helpers/testData';
import { monitorBrowserHealth } from './helpers/browserAudit';
import { assertDestructiveE2EAllowed } from './helpers/safety';

/**
 * ChronoDesk Ticket Workflow E2E Tests
 * 
 * These tests cover the critical paths for the ticket management system.
 * Requires the backend and frontend to be running.
 */

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Authentication', () => {
    test('should allow user to login', async ({ page }) => {
        await loginViaUI(page, TEST_USER);
    });
});

test.describe('Ticket Management', () => {
    let ticketId: number;
    const ticketTitle = `${E2E_MARKER}基础工单`;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('基础工单管理 E2E');
        ticketId = await createTicket(request, ticketTitle);
    });

    test.afterAll(async ({ request }) => {
        await deleteTicket(request, ticketId);
    });

    test.beforeEach(async ({ page }) => {
        await authenticatePage(page, TEST_USER);
    });

    test('should display ticket list', async ({ page }) => {
        await page.goto('/#/tickets');
        const table = page.getByRole('table', {
            name: '工单列表',
            exact: true,
        });
        const ticketInfoHeader = table.getByRole('columnheader', {
            name: /^“工单信息”/u,
        });
        await expect(ticketInfoHeader).toBeVisible({ timeout: 10000 });
        const search = page.getByPlaceholder('搜索工单');
        await search.fill(ticketTitle);
        await search.press('Enter');
        const createdRow = page.getByRole('row', {
            name: new RegExp(ticketTitle),
        });
        await expect(createdRow).toBeVisible({ timeout: 10_000 });
        await expect(
            createdRow.getByRole('cell', {
                name: new RegExp(ticketTitle, 'u'),
            }),
        ).toBeVisible();
    });

    test('should open create ticket form', async ({ page }) => {
        await page.goto('/#/tickets');
        await page.getByRole('link', { name: '创建工单' }).click();
        await expect(page).toHaveURL(/#\/tickets\/create/);
    });

    test('should view ticket details', async ({ page }) => {
        const health = monitorBrowserHealth(page);
        await page.goto('/#/tickets');
        const search = page.getByPlaceholder('搜索工单');
        await search.fill(ticketTitle);
        await search.press('Enter');
        const createdRow = page.getByRole('row', {
            name: new RegExp(ticketTitle),
        });
        await expect(createdRow).toBeVisible({ timeout: 10_000 });
        await createdRow
            .getByRole('link', { name: '查看', exact: true })
            .click();
        await expect(page).toHaveURL(
            new RegExp(`#\\/tickets\\/${ticketId}\\/show$`),
        );
        await page.getByRole('button', { name: '删除', exact: true }).click();
        const confirmation = page.getByRole('dialog', {
            name: new RegExp(`删除工单 ${ticketTitle}`, 'u'),
        });
        await expect(confirmation).toBeVisible();
        await expect(
            confirmation.getByRole('button', { name: '确认' }),
        ).toBeFocused();
        await confirmation.getByRole('button', { name: '取消' }).click();
        await expect(confirmation).toBeHidden();
        health.assertClean();
    });
});

test.describe('Navigation', () => {
    test.beforeEach(async ({ page }) => {
        await authenticatePage(page, TEST_USER);
    });

    test('should navigate to users page', async ({ page }) => {
        const governance = page.getByRole('button', { name: /^治理中心/ });
        if ((await governance.getAttribute('aria-expanded')) !== 'true') {
            await governance.click();
        }
        await page
            .getByRole('menuitem', { name: '平台身份与访问' })
            .click();
        await expect(page).toHaveURL(/#\/users/);
    });

    test('should navigate to system settings', async ({ page }) => {
        const settings = page.getByRole('button', { name: /^系统设置/ });
        if ((await settings.getAttribute('aria-expanded')) !== 'true') {
            await settings.click();
        }
        await page.getByRole('menuitem', { name: '邮件外发' }).click();
        await expect(page).toHaveURL(/#\/system-settings\/email/);
    });
});
