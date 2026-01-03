import { test, expect } from '@playwright/test';

/**
 * ChronoDesk Ticket Workflow E2E Tests
 * 
 * These tests cover the critical paths for the ticket management system.
 * Requires the backend and frontend to be running.
 */

// Test data
const TEST_USER = {
    username: 'admin',
    password: 'admin123',
};

test.describe('Authentication', () => {
    test('should allow user to login', async ({ page }) => {
        await page.goto('/login');

        // Fill login form
        await page.fill('input[name="username"]', TEST_USER.username);
        await page.fill('input[name="password"]', TEST_USER.password);

        // Submit and wait for navigation
        await page.click('button[type="submit"]');

        // Verify we're on the dashboard after login
        await expect(page).toHaveURL(/.*\/(tickets|admin)/);
    });
});

test.describe('Ticket Management', () => {
    test.beforeEach(async ({ page }) => {
        // Login before each test
        await page.goto('/login');
        await page.fill('input[name="username"]', TEST_USER.username);
        await page.fill('input[name="password"]', TEST_USER.password);
        await page.click('button[type="submit"]');
        await page.waitForURL(/.*\/(tickets|admin)/, { timeout: 10000 });
    });

    test('should display ticket list', async ({ page }) => {
        await page.goto('/admin/tickets');

        // Verify ticket list is visible
        await expect(page.locator('[data-testid="ticket-list"]').or(page.locator('.RaList'))).toBeVisible({ timeout: 10000 });
    });

    test('should open create ticket form', async ({ page }) => {
        await page.goto('/admin/tickets');

        // Click create button
        await page.click('[aria-label="Create"]').or(page.click('a[href*="create"]'));

        // Verify we're on create page
        await expect(page).toHaveURL(/.*create/);
    });

    test('should view ticket details', async ({ page }) => {
        await page.goto('/admin/tickets');

        // Click on first ticket row
        await page.click('tr[role="row"]:has(td)');

        // Verify we're viewing ticket details
        await expect(page.locator('[data-testid="ticket-show"]').or(page.locator('.RaShow'))).toBeVisible({ timeout: 10000 });
    });
});

test.describe('Navigation', () => {
    test.beforeEach(async ({ page }) => {
        await page.goto('/login');
        await page.fill('input[name="username"]', TEST_USER.username);
        await page.fill('input[name="password"]', TEST_USER.password);
        await page.click('button[type="submit"]');
        await page.waitForURL(/.*\/(tickets|admin)/);
    });

    test('should navigate to users page', async ({ page }) => {
        await page.click('text=用户管理').or(page.click('a[href*="users"]'));
        await expect(page).toHaveURL(/.*users/);
    });

    test('should navigate to system settings', async ({ page }) => {
        await page.click('text=系统设置').or(page.click('a[href*="settings"]'));
        await expect(page).toHaveURL(/.*settings/);
    });
});
