import { test, expect } from '@playwright/test';

/**
 * ChronoDesk Ticket Workflow E2E Tests
 * 
 * These tests cover the critical paths for the ticket management system.
 * Requires the backend and frontend to be running.
 */

// Test data
const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Authentication', () => {
    test('should allow user to login', async ({ page }) => {
        await page.goto('/#/login');

        await page.getByLabel('邮箱').fill(TEST_USER.email);
        await page.getByLabel('密码').fill(TEST_USER.password);

        await page.getByRole('button', { name: '登录系统' }).click();
        await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
    });
});

test.describe('Ticket Management', () => {
    test.beforeEach(async ({ page }) => {
        // Login before each test
        await page.goto('/#/login');
        await page.getByLabel('邮箱').fill(TEST_USER.email);
        await page.getByLabel('密码').fill(TEST_USER.password);
        await page.getByRole('button', { name: '登录系统' }).click();
        await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
    });

    test('should display ticket list', async ({ page }) => {
        await page.goto('/#/tickets');
        await expect(page.getByRole('columnheader', { name: '工单信息' })).toBeVisible({ timeout: 10000 });
    });

    test('should open create ticket form', async ({ page }) => {
        await page.goto('/#/tickets');
        const createLink = page.locator('a[href*="tickets/create"]').first();
        if (await createLink.count()) {
            await createLink.click();
        } else {
            await page.getByLabel('Create').click();
        }
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
        await page.goto('/#/login');
        await page.getByLabel('邮箱').fill(TEST_USER.email);
        await page.getByLabel('密码').fill(TEST_USER.password);
        await page.getByRole('button', { name: '登录系统' }).click();
        await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
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
