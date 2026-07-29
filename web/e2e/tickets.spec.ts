import { test, expect } from '@playwright/test';
import {
    authenticatePage,
    createTicket,
    deleteTicket,
    E2E_PREFIX,
    loginViaUI,
} from './helpers/testData';

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

    test.beforeAll(async ({ request }) => {
        ticketId = await createTicket(request, `${E2E_PREFIX}基础工单-${Date.now()}`);
    });

    test.afterAll(async ({ request }) => {
        await deleteTicket(request, ticketId);
    });

    test.beforeEach(async ({ page }) => {
        await authenticatePage(page, TEST_USER);
    });

    test('should display ticket list', async ({ page }) => {
        await page.goto('/#/tickets');
        const ticketInfoHeader = page
            .getByRole('columnheader')
            .filter({ hasText: '工单信息' });
        await expect(ticketInfoHeader).toBeVisible({ timeout: 10000 });
    });

    test('should open create ticket form', async ({ page }) => {
        await page.goto('/#/tickets');
        await page.getByRole('link', { name: '创建工单' }).click();
        await expect(page).toHaveURL(/#\/tickets\/create/);
    });

    test('should view ticket details', async ({ page }) => {
        await page.goto('/#/tickets');
        await page.locator('a[href*="/show"]').first().click();
        await expect(page).toHaveURL(/#\/tickets\/\d+\/show/);
    });
});

test.describe('Navigation', () => {
    test.beforeEach(async ({ page }) => {
        await authenticatePage(page, TEST_USER);
    });

    test('should navigate to users page', async ({ page }) => {
        await page.getByRole('menuitem', { name: '用户管理' }).click();
        await expect(page).toHaveURL(/#\/users/);
    });

    test('should navigate to system settings', async ({ page }) => {
        await page.getByRole('menuitem', { name: '系统设置' }).click();
        await expect(page).toHaveURL(/#\/system-settings/);
    });
});
