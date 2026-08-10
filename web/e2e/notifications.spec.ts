import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import type {
    NotificationPreferenceUpdate,
    ProjectRole,
} from '../src/lib/generated/human-api';
import { monitorBrowserHealth } from './helpers/browserAudit';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';
import {
    authenticatePage,
    createNotification,
    deleteNotification,
    E2E_MARKER,
    getAdminToken,
    projectAPIPath,
    resolveE2EProjectKey,
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
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const notificationsPath = projectAPIPath(
            projectKey,
            'notifications',
        );
        notificationId = await createNotification(request, { title, content });

        await authenticatePage(page, TEST_USER);
        await page.goto('/#/notifications');

        const main = page.getByRole('main');
        const searchInput = main.getByPlaceholder('搜索通知', { exact: true });
        const listRequest = page.waitForResponse((response) => {
            const url = new URL(response.url());
            if (
                url.pathname !== notificationsPath ||
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

    test('旧全局通知路由应直接返回 404', async ({ request }) => {
        const token = await getAdminToken(request);
        const headers = {
            Authorization: `Bearer ${token}`,
            'Content-Type': 'application/json',
        };
        const legacyList = await request.get('/api/notifications', {
            headers,
        });
        expect(legacyList.status()).toBe(404);

    });
});

type NotificationCenterMockState = {
    deleteIDs: number[];
    markAllReadCount: number;
    markReadIDs: number[];
    preferenceUpdates: NotificationPreferenceUpdate[][];
};

const notificationTimestamp = '2026-08-02T08:00:00Z';

const installNotificationCenterBackend = async (
    page: Page,
    projectRole: ProjectRole,
    empty = false,
): Promise<NotificationCenterMockState> => {
    const state: NotificationCenterMockState = {
        deleteIDs: [],
        markAllReadCount: 0,
        markReadIDs: [],
        preferenceUpdates: [],
    };
    const access = authorizedProjectAccess(projectA, projectRole);
    const notificationsPath =
        `/api/projects/${projectA.key}/notifications`;
    let notifications = empty ? [] : [
        {
            id: 701,
            created_at: notificationTimestamp,
            updated_at: notificationTimestamp,
            type: 'ticket_assigned',
            title: '待处理分配通知',
            content: '请处理新分配的工单',
            priority: 'high',
            channel: 'in_app',
            recipient: {
                id: defaultMockIdentity.id,
                username: 'notification-employee',
                display_name: '通知员工',
            },
            recipient_id: defaultMockIdentity.id,
            sender: null,
            related_type: 'ticket',
            related_id: 501,
            related_ticket_id: null,
            is_read: false,
            read_at: null,
            is_sent: true,
            sent_at: notificationTimestamp,
            is_delivered: true,
            delivered_at: notificationTimestamp,
            action_url: '',
            scheduled_at: null,
            expires_at: null,
            metadata: null,
            delivery_status: 'delivered',
        },
        {
            id: 702,
            created_at: notificationTimestamp,
            updated_at: notificationTimestamp,
            type: 'system_alert',
            title: '系统安全提醒',
            content: '请检查当前项目的安全设置',
            priority: 'urgent',
            channel: 'in_app',
            recipient: {
                id: defaultMockIdentity.id,
                username: 'notification-employee',
                display_name: '通知员工',
            },
            recipient_id: defaultMockIdentity.id,
            sender: null,
            related_type: '',
            related_id: null,
            related_ticket_id: null,
            is_read: false,
            read_at: null,
            is_sent: true,
            sent_at: notificationTimestamp,
            is_delivered: true,
            delivered_at: notificationTimestamp,
            action_url: '',
            scheduled_at: null,
            expires_at: null,
            metadata: null,
            delivery_status: 'delivered',
        },
        {
            id: 703,
            created_at: notificationTimestamp,
            updated_at: notificationTimestamp,
            type: 'ticket_created',
            title: '他人项目通知',
            content: '该通知属于同项目的另一名员工',
            priority: 'normal',
            channel: 'in_app',
            recipient: {
                id: 99,
                username: 'another-employee',
                display_name: '其他员工',
            },
            recipient_id: 99,
            sender: null,
            related_type: 'ticket',
            related_id: 503,
            related_ticket_id: null,
            is_read: false,
            read_at: null,
            is_sent: true,
            sent_at: notificationTimestamp,
            is_delivered: true,
            delivered_at: notificationTimestamp,
            action_url: '',
            scheduled_at: null,
            expires_at: null,
            metadata: null,
            delivery_status: 'delivered',
        },
    ];

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (
            url.pathname === notificationsPath &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: notifications,
                    total: notifications.length,
                    page: 1,
                    page_size: 25,
                    total_pages: notifications.length > 0 ? 1 : 0,
                },
            });
            return;
        }
        if (
            url.pathname === `${notificationsPath}/unread-count` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                count: notifications.filter(
                    ({ is_read, recipient_id }) =>
                        !is_read &&
                        recipient_id === defaultMockIdentity.id,
                ).length,
            });
            return;
        }
        if (
            url.pathname === `${notificationsPath}/read-all` &&
            request.method() === 'PUT'
        ) {
            state.markAllReadCount += 1;
            notifications = notifications.map((notification) =>
                notification.recipient_id === defaultMockIdentity.id
                    ? {
                          ...notification,
                          is_read: true,
                          read_at: notificationTimestamp,
                      }
                    : notification,
            );
            await fulfillJSON(route, { message: '全部标记已读成功' });
            return;
        }
        const readMatch = url.pathname.match(
            new RegExp(`^${notificationsPath}/(\\d+)/read$`, 'u'),
        );
        if (readMatch && request.method() === 'PUT') {
            const notificationID = Number(readMatch[1]);
            state.markReadIDs.push(notificationID);
            notifications = notifications.map((notification) =>
                notification.id === notificationID
                    ? {
                          ...notification,
                          is_read: true,
                          read_at: notificationTimestamp,
                      }
                    : notification,
            );
            await fulfillJSON(route, { message: '标记已读成功' });
            return;
        }
        const deleteMatch = url.pathname.match(
            new RegExp(`^${notificationsPath}/(\\d+)$`, 'u'),
        );
        if (deleteMatch && request.method() === 'DELETE') {
            const notificationID = Number(deleteMatch[1]);
            state.deleteIDs.push(notificationID);
            notifications = notifications.filter(
                ({ id }) => id !== notificationID,
            );
            await fulfillJSON(route, { message: '删除通知成功' });
            return;
        }
        if (
            url.pathname === '/api/notification-preferences' &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: [
                    {
                        notification_type: 'ticket_assigned',
                        email_enabled: false,
                        in_app_enabled: true,
                        webhook_enabled: false,
                        do_not_disturb_start: null,
                        do_not_disturb_end: null,
                        max_daily_count: 25,
                        batch_delivery: false,
                        batch_interval: 60,
                    },
                ],
            });
            return;
        }
        if (
            url.pathname === '/api/notification-preferences' &&
            request.method() === 'PUT'
        ) {
            const body = request.postDataJSON() as {
                preferences: NotificationPreferenceUpdate[];
            };
            state.preferenceUpdates.push(body.preferences);
            await fulfillJSON(route, { message: '更新成功' });
            return;
        }
        if (
            url.pathname ===
            `/api/projects/${projectA.key}/tickets/stats`
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    total: 0,
                    open: 0,
                    in_progress: 0,
                    pending: 0,
                    resolved: 0,
                    overdue: 0,
                    sla_breached: 0,
                    my_tickets: 0,
                    unassigned: 0,
                    high_priority: 0,
                    escalated: 0,
                },
            });
            return;
        }

        await fulfillJSON(route, { code: 0, data: [] });
    });

    return state;
};

const openMockNotificationCenter = async (
    page: Page,
    projectRole: ProjectRole,
) => {
    await installMockSession(
        page,
        {
            ...defaultMockIdentity,
            sessionID: `e2e-notification-center-${projectRole}`,
        },
        projectA,
    );
    const state = await installNotificationCenterBackend(page, projectRole);
    await page.goto('/#/notifications');
    await expect(
        page.getByRole('table', { name: '通知列表', exact: true }),
    ).toBeVisible();
    return state;
};

test.describe('通知中心员工操作', () => {
    test('零通知员工仍可进入通知偏好', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-empty-notification-center',
            },
            projectA,
        );
        await installNotificationCenterBackend(page, 'observer', true);
        await page.goto('/#/notifications');

        await expect(page.getByText('暂无通知', { exact: true }))
            .toBeVisible();
        await page
            .getByRole('button', {
                name: '通知偏好',
                exact: true,
            })
            .click();
        await expect(
            page.getByRole('dialog', {
                name: '通知偏好',
                exact: true,
            }),
        ).toBeVisible();
    });

    test('项目管理员可逐条或全部标为已读，并经确认删除通知', async ({
        page,
    }) => {
        const browserHealth = monitorBrowserHealth(page);
        const state = await openMockNotificationCenter(
            page,
            'project_admin',
        );

        const assignmentRow = page.getByRole('row', {
            name: /待处理分配通知/u,
        });
        await expect(
            page.getByRole('button', {
                name: '将“他人项目通知”标为已读',
                exact: true,
            }),
        ).toHaveCount(0);
        await assignmentRow
            .getByRole('button', {
                name: '将“待处理分配通知”标为已读',
                exact: true,
            })
            .click();
        await expect.poll(() => state.markReadIDs).toEqual([701]);
        await expect(
            assignmentRow.getByRole('button', {
                name: '将“待处理分配通知”标为已读',
                exact: true,
            }),
        ).toHaveCount(0);

        await page
            .getByRole('button', {
                name: '全部标为已读',
                exact: true,
            })
            .click();
        await expect.poll(() => state.markAllReadCount).toBe(1);
        await expect(
            page.getByRole('button', {
                name: '将“系统安全提醒”标为已读',
                exact: true,
            }),
        ).toHaveCount(0);

        await assignmentRow
            .getByRole('button', {
                name: '删除通知“待处理分配通知”',
                exact: true,
            })
            .click();
        const confirmation = page.getByRole('dialog', {
            name: '确认删除通知',
            exact: true,
        });
        await expect(confirmation).toContainText(
            '删除后无法恢复。即将删除“待处理分配通知”',
        );
        await confirmation
            .getByRole('button', { name: '取消', exact: true })
            .click();
        expect(state.deleteIDs).toEqual([]);

        await assignmentRow
            .getByRole('button', {
                name: '删除通知“待处理分配通知”',
                exact: true,
            })
            .click();
        await confirmation
            .getByRole('button', {
                name: '确认删除',
                exact: true,
            })
            .click();
        await expect.poll(() => state.deleteIDs).toEqual([701]);
        await expect(assignmentRow).toHaveCount(0);
        browserHealth.assertClean();
    });

    test('非项目管理员不显示删除操作，但仍可管理自己的已读状态', async ({
        page,
    }) => {
        const state = await openMockNotificationCenter(page, 'manager');
        await expect(
            page.getByRole('button', {
                name: /删除通知/u,
            }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('button', {
                name: '将“他人项目通知”标为已读',
                exact: true,
            }),
        ).toHaveCount(0);
        await page
            .getByRole('button', {
                name: '将“待处理分配通知”标为已读',
                exact: true,
            })
            .click();
        await expect.poll(() => state.markReadIDs).toEqual([701]);
    });

    test('普通观察员可查看和严格更新自己的通知偏好', async ({
        page,
    }) => {
        const browserHealth = monitorBrowserHealth(page);
        const state = await openMockNotificationCenter(page, 'observer');

        await page
            .getByRole('button', {
                name: '通知偏好',
                exact: true,
            })
            .click();
        const dialog = page.getByRole('dialog', {
            name: '通知偏好',
            exact: true,
        });
        await expect(dialog).toContainText(
            '这些偏好属于当前登录员工，并在其所有项目中生效',
        );

        const assignedEmail = dialog.getByRole('switch', {
            name: '工单分配邮件通知',
            exact: true,
        });
        await expect(assignedEmail).not.toBeChecked();
        const accessibilityScan = await new AxeBuilder({ page })
            .include('[role="dialog"]')
            .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
            .analyze();
        expect(
            accessibilityScan.violations.filter(
                ({ impact }) =>
                    impact === 'critical' || impact === 'serious',
            ),
        ).toEqual([]);
        await assignedEmail.check();
        await dialog.getByLabel('工单分配每项目每日上限').fill('75');
        await dialog
            .getByLabel('工单分配免打扰开始')
            .fill('2026-08-03T09:00');
        await dialog
            .getByRole('button', {
                name: '保存偏好',
                exact: true,
            })
            .click();
        await expect(dialog.getByRole('alert')).toContainText(
            '工单分配的免打扰开始和结束时间必须同时填写',
        );
        expect(state.preferenceUpdates).toEqual([]);

        await dialog
            .getByLabel('工单分配免打扰结束')
            .fill('2026-08-03T17:00');
        await dialog
            .getByRole('button', {
                name: '保存偏好',
                exact: true,
            })
            .click();
        await expect.poll(() => state.preferenceUpdates.length).toBe(1);
        await expect(dialog).toHaveCount(0);
        await expect(page.getByText('通知偏好已保存')).toBeVisible();

        const update = state.preferenceUpdates[0];
        expect(update).toHaveLength(10);
        const assigned = update.find(
            ({ notification_type }) =>
                notification_type === 'ticket_assigned',
        );
        expect(assigned).toMatchObject({
            notification_type: 'ticket_assigned',
            email_enabled: true,
            in_app_enabled: true,
            webhook_enabled: false,
            max_daily_count: 75,
            batch_delivery: false,
            batch_interval: 60,
        });
        expect(assigned?.do_not_disturb_start).toMatch(
            /^2026-08-03T/u,
        );
        expect(assigned?.do_not_disturb_end).toMatch(
            /^2026-08-03T/u,
        );
        for (const preference of update) {
            expect(Object.keys(preference).sort()).toEqual([
                'batch_delivery',
                'batch_interval',
                'do_not_disturb_end',
                'do_not_disturb_start',
                'email_enabled',
                'in_app_enabled',
                'max_daily_count',
                'notification_type',
                'webhook_enabled',
            ]);
        }
        browserHealth.assertClean();
    });
});
