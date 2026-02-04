import { test, expect } from '@playwright/test';
import {
    createNotification,
    deleteNotification,
    E2E_PREFIX,
    loginViaUI,
} from './helpers/testData';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('Notifications', () => {
    let notificationId: number | null = null;

    test.afterAll(async ({ request }) => {
        if (notificationId) {
            await deleteNotification(request, notificationId);
        }
    });

    test('should display created notification', async ({ page, request }) => {
        const title = `${E2E_PREFIX}通知-${Date.now()}`;
        const content = `${E2E_PREFIX}通知内容-${Date.now()}`;
        notificationId = await createNotification(request, { title, content });

        await loginViaUI(page, TEST_USER);
        await page.goto('/#/notifications');

        const searchInput = page.getByPlaceholder('搜索通知');
        const listRequest = page.waitForResponse((response) =>
            response.url().includes('/api/notifications') && response.request().method() === 'GET',
        );
        await searchInput.fill(title);
        await searchInput.press('Enter');
        await listRequest;

        await expect(page.getByText(title)).toBeVisible({ timeout: 15000 });
    });
});
