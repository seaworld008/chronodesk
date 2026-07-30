import { expect, test, type Page } from '@playwright/test';
import type {
    AuthorizedProjectAccess,
    ProjectRole,
} from '../src/lib/generated/human-api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
    projectB,
} from './helpers/mockHumanSession';

type ObservedRequest = {
    method: string;
    pathname: string;
    body: unknown;
};

type MockState = {
    accesses: AuthorizedProjectAccess[];
    deniedProjectKeys: Set<string>;
    unauthorizedPaths: Set<string>;
    requests: ObservedRequest[];
};

const ticketID = 9001;

const ticketForProject = (
    projectKey: string,
    projectID: number,
) => ({
    id: ticketID,
    public_id:
        projectKey === projectA.key
            ? '00000000-0000-7000-8000-000000009001'
            : '00000000-0000-7000-8000-000000019001',
    project_id: projectID,
    ticket_number: `${projectKey}-${ticketID}`,
    title:
        projectKey === projectA.key
            ? 'A 项目同 ID 工单'
            : 'B 项目同 ID 工单',
    description: `${projectKey} 项目的独立工单内容`,
    type: 'request',
    priority: 'normal',
    status: 'open',
    source: 'web',
    created_by_id: defaultMockIdentity.id,
    assigned_to_id: null,
    version: projectKey === projectA.key ? 3 : 7,
    tags: [],
    internal_notes: `${projectKey} internal only`,
    sla_breached: false,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T09:00:00Z',
});

const ticketList = (projectKey: string, projectID: number) => ({
    code: 0,
    data: {
        items: [ticketForProject(projectKey, projectID)],
        total: 1,
        page: 1,
        page_size: 25,
        total_pages: 1,
    },
});

const mockProjectBackend = async (
    page: Page,
    accesses: AuthorizedProjectAccess[],
): Promise<MockState> => {
    const state: MockState = {
        accesses,
        deniedProjectKeys: new Set(),
        unauthorizedPaths: new Set(),
        requests: [],
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const body = request.postData()
            ? request.postDataJSON()
            : undefined;
        state.requests.push({
            method: request.method(),
            pathname: url.pathname,
            body,
        });

        if (state.unauthorizedPaths.has(url.pathname)) {
            await fulfillJSON(
                route,
                {
                    code: 'unauthorized',
                    msg: '登录状态已失效',
                },
                401,
            );
            return;
        }

        if (url.pathname === '/api/projects') {
            if (!request.headers().authorization) {
                await fulfillJSON(
                    route,
                    { code: 'unauthorized', msg: '登录状态已失效' },
                    401,
                );
                return;
            }
            await fulfillJSON(route, { code: 0, data: state.accesses });
            return;
        }

        const projectMatch = url.pathname.match(
            /^\/api\/projects\/([^/]+)\/(.+)$/u,
        );
        if (projectMatch) {
            const projectKey = decodeURIComponent(projectMatch[1]);
            const access = state.accesses.find(
                (candidate) => candidate.project.key === projectKey,
            );
            if (!access || state.deniedProjectKeys.has(projectKey)) {
                await fulfillJSON(
                    route,
                    {
                        code: 'project_access_denied',
                        msg: '无权访问该项目',
                    },
                    403,
                );
                return;
            }

            const suffix = projectMatch[2];
            const ticket = ticketForProject(
                projectKey,
                access.project.id,
            );
            if (suffix === `tickets/${ticketID}`) {
                await fulfillJSON(route, { code: 0, data: ticket });
                return;
            }
            if (suffix === 'tickets/stats') {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        total: 1,
                        open: 1,
                        in_progress: 0,
                        pending: 0,
                        resolved: 0,
                        overdue: 0,
                        sla_breached: 0,
                        my_tickets: 0,
                        unassigned: 1,
                        high_priority: 0,
                        escalated: 0,
                    },
                });
                return;
            }
            if (suffix === 'tickets' || suffix === 'tickets/my-tickets') {
                await fulfillJSON(
                    route,
                    ticketList(projectKey, access.project.id),
                );
                return;
            }
            if (
                suffix === `tickets/${ticketID}/comments` ||
                suffix === `tickets/${ticketID}/attachments` ||
                suffix === `tickets/${ticketID}/history`
            ) {
                await fulfillJSON(route, { code: 0, data: [] });
                return;
            }
            if (suffix === 'categories' || suffix === 'assignees') {
                await fulfillJSON(route, { code: 0, data: [] });
                return;
            }
            if (suffix === 'admin/agents/agent-control/overview') {
                await fulfillJSON(route, {
                    code: 0,
                    data: {
                        global_read_only: false,
                        emergency_stop: false,
                        principals: [],
                        leases: [],
                        events: [],
                        outbox: [],
                        policy_decisions: [],
                    },
                });
                return;
            }
            await fulfillJSON(route, { code: 0, data: [] });
            return;
        }

        if (url.pathname === '/api/workbench/tickets') {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [],
                    total: 0,
                    page: 1,
                    page_size: 20,
                    total_pages: 0,
                },
            });
            return;
        }

        await fulfillJSON(route, { code: 0, data: [] });
    });

    return state;
};

const expectTicketCapabilities = async (
    page: Page,
    projectRole: ProjectRole,
) => {
    const main = page.getByRole('main');
    const canManage = projectRole === 'project_admin';
    if (canManage) {
        await expect(
            main.getByRole('link', { name: '编辑', exact: true }),
        ).toBeVisible();
        await expect(
            main.getByRole('button', { name: '删除', exact: true }),
        ).toBeVisible();
        await expect(
            main.getByRole('button', { name: '分配工单', exact: true }),
        ).toBeVisible();
        return;
    }
    await expect(
        main.getByRole('link', { name: '编辑', exact: true }),
    ).toHaveCount(0);
    await expect(
        main.getByRole('button', { name: '删除', exact: true }),
    ).toHaveCount(0);
    await expect(
        main.getByRole('button', { name: '分配工单', exact: true }),
    ).toHaveCount(0);
};

test.describe('项目资源缓存与会话失效隔离', () => {
    test('A/B 同 numeric ticket ID：撤销 A 后当前 URL fail closed，绝不触碰 B 同 ID 工单', async ({
        page,
    }) => {
        await installMockSession(page, defaultMockIdentity, projectA);
        const backend = await mockProjectBackend(page, [
            authorizedProjectAccess(projectA, 'project_admin'),
            authorizedProjectAccess(projectB, 'observer'),
        ]);

        await page.goto(`/#/tickets/${ticketID}/show`);
        await expect(
            page.getByRole('heading', {
                name: 'A 项目同 ID 工单',
                exact: true,
            }),
        ).toBeVisible();
        await expectTicketCapabilities(page, 'project_admin');

        backend.accesses = [
            authorizedProjectAccess(projectB, 'observer'),
        ];
        backend.deniedProjectKeys.add(projectA.key);
        const requestOffset = backend.requests.length;
        const deniedResponse = page.waitForResponse((response) =>
            response.status() === 403 &&
            new URL(response.url()).pathname ===
                `/api/projects/${projectA.key}/tickets/${ticketID}`,
        );
        await page
            .getByRole('banner')
            .getByRole('button', { name: '刷新', exact: true })
            .click();
        expect(
            (await (await deniedResponse).json() as { code?: unknown }).code,
        ).toBe('project_access_denied');

        await expect(page).toHaveURL(/#\/?$/u);
        await expect(
            page.getByRole('heading', {
                name: '请选择要进入的项目',
                exact: true,
            }),
        ).toBeVisible();
        await expect(
            page.getByText('A 项目同 ID 工单', { exact: true }),
        ).toHaveCount(0);
        const requestsAfterRevocation =
            backend.requests.slice(requestOffset);
        expect(
            requestsAfterRevocation.some(
                ({ pathname }) =>
                    pathname ===
                    `/api/projects/${projectB.key}/tickets/${ticketID}`,
            ),
        ).toBe(false);
        expect(
            requestsAfterRevocation.some(
                ({ method, pathname }) =>
                    method !== 'GET' &&
                    pathname.startsWith(
                        `/api/projects/${projectB.key}/tickets/${ticketID}`,
                    ),
            ),
        ).toBe(false);
    });

    for (const roleSwitch of [
        {
            name: 'A project_admin → B observer',
            startProject: projectA,
            targetProject: projectB,
            startRole: 'project_admin',
            targetRole: 'observer',
        },
        {
            name: 'B observer → A project_admin',
            startProject: projectB,
            targetProject: projectA,
            startRole: 'observer',
            targetRole: 'project_admin',
        },
    ] as const) {
        test(`${roleSwitch.name} 清除 permissions/query/resource cache`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                defaultMockIdentity,
                roleSwitch.startProject,
            );
            const backend = await mockProjectBackend(page, [
                authorizedProjectAccess(projectA, 'project_admin'),
                authorizedProjectAccess(projectB, 'observer'),
            ]);

            await page.goto(`/#/tickets/${ticketID}/show`);
            await expect(
                page.getByRole('heading', {
                    name:
                        roleSwitch.startProject.key === projectA.key
                            ? 'A 项目同 ID 工单'
                            : 'B 项目同 ID 工单',
                    exact: true,
                }),
            ).toBeVisible();
            await expectTicketCapabilities(page, roleSwitch.startRole);

            const targetDetailPath =
                `/api/projects/${roleSwitch.targetProject.key}` +
                `/tickets/${ticketID}`;
            const targetRequest = page.waitForRequest((request) =>
                request.method() === 'GET' &&
                new URL(request.url()).pathname === targetDetailPath,
            );
            await page.getByTestId('active-project-switcher').click();
            await page
                .getByRole('option', {
                    name: new RegExp(
                        `^${roleSwitch.targetProject.name} · `,
                        'u',
                    ),
                })
                .click();
            await page.goto(`/#/tickets/${ticketID}/show`);
            await targetRequest;

            await expect(
                page.getByRole('heading', {
                    name:
                        roleSwitch.targetProject.key === projectA.key
                            ? 'A 项目同 ID 工单'
                            : 'B 项目同 ID 工单',
                    exact: true,
                }),
            ).toBeVisible();
            await expectTicketCapabilities(page, roleSwitch.targetRole);
            await expect(
                page.getByText(
                    roleSwitch.startProject.key === projectA.key
                        ? 'A 项目同 ID 工单'
                        : 'B 项目同 ID 工单',
                    { exact: true },
                ),
            ).toHaveCount(0);
            expect(
                backend.requests.some(
                    ({ method, pathname }) =>
                        method === 'GET' &&
                        pathname === targetDetailPath,
                ),
            ).toBe(true);
        });
    }

    test('direct apiFetch 401 触发退出、登录跳转与全部会话/项目缓存清理', async ({
        page,
    }) => {
        await installMockSession(page, defaultMockIdentity, projectA);
        const backend = await mockProjectBackend(page, [
            authorizedProjectAccess(projectA, 'project_admin'),
        ]);

        await page.goto('/#/');
        await expect(page.getByTestId('project-home')).toBeVisible();
        const overviewPath =
            `/api/projects/${projectA.key}` +
            '/admin/agents/agent-control/overview';
        backend.unauthorizedPaths.add(overviewPath);
        const unauthorizedResponse = page.waitForResponse((response) =>
            response.status() === 401 &&
            new URL(response.url()).pathname === overviewPath,
        );

        await page.goto('/#/agent-control');
        expect(
            (await (await unauthorizedResponse).json() as {
                code?: unknown;
            }).code,
        ).toBe('unauthorized');
        await expect(page).toHaveURL(/#\/login$/u);
        await expect(page.getByLabel('邮箱')).toBeVisible();

        const storage = await page.evaluate(() => ({
            token: localStorage.getItem('token'),
            refreshToken: localStorage.getItem('refreshToken'),
            user: localStorage.getItem('user'),
            tokenExpiresAt: localStorage.getItem('tokenExpiresAt'),
            permissions: localStorage.getItem('permissions'),
            project: localStorage.getItem('chronodesk.activeProject'),
        }));
        expect(storage).toEqual({
            token: null,
            refreshToken: null,
            user: null,
            tokenExpiresAt: null,
            permissions: null,
            project: null,
        });
    });
});
