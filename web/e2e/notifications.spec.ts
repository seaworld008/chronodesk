import { test, expect } from '@playwright/test';
import {
    authenticatePage,
    createNotification,
    deleteNotification,
    E2E_MARKER,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';

const TEST_USER = {
    email: 'admin@example.com',
    password: 'Admin123!',
};

test.describe('通知中心', () => {
    let notificationId: number | null = null;

    test.beforeAll(() => {
        assertDestructiveE2EAllowed('通知 E2E');
    });

    test.afterAll(async ({ request }) => {
        if (notificationId) {
            await deleteNotification(request, notificationId);
        }
    });

    test('应显示本轮真实创建的通知', async ({ page, request }) => {
        const title = `${E2E_MARKER}通知`;
        const content = `${E2E_MARKER}通知内容`;
        notificationId = await createNotification(request, { title, content });

        await authenticatePage(page, TEST_USER);
        await page.goto('/#/notifications');

        const main = page.getByRole('main');
        const searchInput = main.getByPlaceholder('搜索通知', { exact: true });
        const listRequest = page.waitForResponse((response) => {
            const url = new URL(response.url());
            if (
                url.pathname !== '/api/notifications' ||
                response.request().method() !== 'GET'
            ) {
                return false;
            }
            const rawFilter = url.searchParams.get('filter');
            if (!rawFilter) {
                return false;
            }
            try {
                return (
                    (JSON.parse(rawFilter) as Record<string, unknown>).q ===
                    title
                );
            } catch {
                return false;
            }
        });
        await searchInput.fill(title);
        await searchInput.press('Enter');
        expect((await listRequest).status()).toBe(200);

        const table = main.getByRole('table', {
            name: '通知列表',
            exact: true,
        });
        const notificationRow = table.getByRole('row', {
            name: new RegExp(title, 'u'),
        });
        await expect(notificationRow).toBeVisible({ timeout: 15_000 });
        await expect(
            notificationRow.getByRole('cell', {
                name: new RegExp(title, 'u'),
            }),
        ).toBeVisible();
    });
});
