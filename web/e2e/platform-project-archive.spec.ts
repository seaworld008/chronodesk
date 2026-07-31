import { expect, test, type Page } from '@playwright/test';
import type {
    AuthorizedProject,
    AuthorizedProjectAccess,
    PlatformRole,
    PlatformProjectSummary,
} from '../src/lib/generated/human-api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
    projectB,
} from './helpers/mockHumanSession';

type PlatformProjectBackend = {
    archiveRequests: string[];
    authorizedProjectReads: number;
    archived: boolean;
    platformGovernanceRequests: string[];
    platformProjectReads: string[];
    scopedRequests: string[];
};

type PlatformProjectBackendOptions = {
    authorizedProjects?: AuthorizedProjectAccess[];
};

const platformAdmin = {
    ...defaultMockIdentity,
    id: 11,
    sessionID: 'e2e-platform-project-archive',
    email: 'platform-project-admin@example.test',
    platformRole: 'platform_admin' as const,
};

const accessA = authorizedProjectAccess(projectA, 'project_admin');
const accessB = authorizedProjectAccess(projectB, 'observer');
const defaultPlatformProject: PlatformProjectSummary = {
    public_id: '00000000-0000-7000-8000-000000000075',
    key: 'DEFAULT',
    name: '默认项目',
    description: '系统默认项目',
    status: 'active',
};

const platformProjectSummary = (
    project: AuthorizedProject,
    status: PlatformProjectSummary['status'] = 'active',
): PlatformProjectSummary => ({
    public_id: project.public_id,
    key: project.key,
    name: project.name,
    description: project.description,
    status,
});

const emptyTicketStats = (total: number) => ({
    total,
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
});

const installPlatformProjectBackend = async (
    page: Page,
    {
        authorizedProjects = [accessA, accessB],
    }: PlatformProjectBackendOptions = {},
): Promise<PlatformProjectBackend> => {
    const state: PlatformProjectBackend = {
        archiveRequests: [],
        authorizedProjectReads: 0,
        archived: false,
        platformGovernanceRequests: [],
        platformProjectReads: [],
        scopedRequests: [],
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const requestTarget =
            `${request.method()} ${url.pathname}${url.search}`;

        if (url.pathname.startsWith('/api/platform/projects')) {
            state.platformGovernanceRequests.push(requestTarget);
        }

        if (
            url.pathname === '/api/projects' &&
            request.method() === 'GET'
        ) {
            state.authorizedProjectReads += 1;
            await fulfillJSON(route, {
                code: 0,
                data: state.archived
                    ? authorizedProjects.filter(
                        ({ project }) =>
                            project.public_id !== projectA.public_id,
                    )
                    : authorizedProjects,
            });
            return;
        }

        if (
            url.pathname === '/api/platform/projects' &&
            request.method() === 'GET'
        ) {
            state.platformProjectReads.push(requestTarget);
            await fulfillJSON(route, {
                code: 0,
                data: [
                    defaultPlatformProject,
                    platformProjectSummary(
                        projectA,
                        state.archived ? 'archived' : 'active',
                    ),
                    platformProjectSummary(projectB),
                ],
            });
            return;
        }

        const archivePath =
            `/api/platform/projects/${projectA.public_id}/archive`;
        if (
            url.pathname === archivePath &&
            request.method() === 'POST'
        ) {
            state.archiveRequests.push(`${request.method()} ${url.pathname}`);
            state.archived = true;
            await fulfillJSON(
                route,
                {
                    code: 0,
                    msg: '项目已归档',
                    data: platformProjectSummary(projectA, 'archived'),
                },
                200,
            );
            return;
        }

        if (url.pathname.startsWith('/api/projects/')) {
            state.scopedRequests.push(`${request.method()} ${url.pathname}`);
            if (url.pathname.endsWith('/tickets/stats')) {
                const total = url.pathname.includes(`/${projectA.key}/`)
                    ? 111
                    : 222;
                await fulfillJSON(route, {
                    code: 0,
                    data: emptyTicketStats(total),
                });
                return;
            }
            if (url.pathname.includes('/tickets')) {
                await fulfillJSON(route, {
                    code: 0,
                    data: { items: [], total: 0 },
                });
                return;
            }
        }

        await fulfillJSON(route, { code: 1, msg: '未配置的测试路由' }, 404);
    });

    return state;
};

test.describe('平台项目归档浏览器闭环', () => {
    test('platform_admin 归档当前项目后清空项目与运行时缓存并显式重选', async ({
        page,
    }) => {
        await installMockSession(page, platformAdmin, projectA);
        const backend = await installPlatformProjectBackend(page);

        await page.goto('/#/');
        await expect(page.getByTestId('project-home')).toBeVisible();
        await expect.poll(() =>
            backend.scopedRequests.some((request) =>
                request.includes(`/api/projects/${projectA.key}/tickets/stats`),
            ),
        ).toBe(true);

        await page
            .getByRole('menuitem', {
                name: '平台项目治理',
                exact: true,
            })
            .click();
        await expect(page).toHaveURL(/#\/platform\/projects$/u);
        await expect(
            page.getByRole('heading', {
                name: '平台项目治理',
                exact: true,
            }),
        ).toBeVisible();
        await expect.poll(
            () => backend.platformProjectReads.length,
        ).toBeGreaterThan(0);
        expect(new Set(backend.platformProjectReads)).toEqual(
            new Set(['GET /api/platform/projects']),
        );
        const governanceTable = page.getByRole('table', {
            name: '平台项目治理列表',
            exact: true,
        });
        await expect(governanceTable.getByRole('separator')).toHaveCount(4);
        const projectResizeHandle = governanceTable.getByRole(
            'separator',
            { name: /调整“?项目”?列宽/u },
        );
        await projectResizeHandle.focus();
        await projectResizeHandle.press('ArrowRight');
        await expect(projectResizeHandle).toHaveAttribute(
            'aria-valuenow',
            '368',
        );
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = localStorage.getItem(
                    'chronodesk.table-columns.v1.platform.projects.governance',
                );
                return raw
                    ? (JSON.parse(raw) as { project?: unknown }).project
                    : null;
            }),
        ).toBe(368);
        const defaultProjectRow = governanceTable
            .getByRole('row')
            .filter({ hasText: defaultPlatformProject.public_id });
        await expect(
            defaultProjectRow.getByRole('button', {
                name: '归档项目',
                exact: true,
            }),
        ).toBeDisabled();

        await page
            .getByTestId(`archive-platform-project-${projectA.public_id}`)
            .click();
        const dialog = page.getByRole('dialog', { name: '确认归档项目' });
        await expect(dialog).toContainText(projectA.name);
        await expect(dialog).toContainText(projectA.key);

        const archiveResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    `/api/platform/projects/${projectA.public_id}/archive`,
        );
        await dialog
            .getByRole('button', { name: '确认归档', exact: true })
            .click();
        expect((await archiveResponse).status()).toBe(200);

        await expect(page).toHaveURL(/#\/$/u);
        await expect(
            page.getByTestId('active-project-selection-required'),
        ).toBeVisible();
        await expect(
            page.getByTestId(`select-project-${projectB.key}`),
        ).toBeVisible();
        await expect(
            page.getByTestId(`select-project-${projectA.key}`),
        ).toHaveCount(0);

        expect(backend.archiveRequests).toEqual([
            `POST /api/platform/projects/${projectA.public_id}/archive`,
        ]);
        await expect.poll(() => backend.authorizedProjectReads).toBeGreaterThan(
            1,
        );
        await expect.poll(() =>
            page.evaluate(
                () => localStorage.getItem('chronodesk.activeProject'),
            ),
        ).toBeNull();

        await page
            .getByTestId(`select-project-${projectB.key}`)
            .click();
        await expect(page.getByTestId('project-home')).toBeVisible();
        await expect.poll(() =>
            backend.scopedRequests.some((request) =>
                request.includes(`/api/projects/${projectB.key}/tickets/stats`),
            ),
        ).toBe(true);
        await expect(
            page.getByRole('menuitem', {
                name: '项目成员管理',
                exact: true,
            }),
        ).toHaveCount(0);
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = localStorage.getItem(
                    'chronodesk.activeProject',
                );
                return raw ? JSON.parse(raw).project_key : null;
            }),
        ).toBe(projectB.key);
    });

    test('0 Membership 的 platform_admin 可从平台清单归档非当前项目并刷新治理列表', async ({
        page,
    }) => {
        await installMockSession(page, {
            ...platformAdmin,
            id: 12,
            sessionID: 'e2e-platform-project-zero-memberships',
            email: 'platform-project-zero-memberships@example.test',
        });
        const backend = await installPlatformProjectBackend(page, {
            authorizedProjects: [],
        });

        await page.goto('/#/');
        await expect(page.getByTestId('platform-home')).toBeVisible();
        await expect.poll(
            () => backend.authorizedProjectReads,
        ).toBeGreaterThan(0);
        const authorizedReadsBeforeGovernance =
            backend.authorizedProjectReads;

        await page
            .getByRole('menuitem', {
                name: '平台项目治理',
                exact: true,
            })
            .click();
        await expect(page).toHaveURL(/#\/platform\/projects$/u);
        await expect(
            page.getByRole('heading', {
                name: '平台项目治理',
                exact: true,
            }),
        ).toBeVisible();
        await expect(page.getByText(projectA.public_id)).toBeVisible();
        await expect(page.getByText(projectB.public_id)).toBeVisible();
        await expect.poll(
            () => backend.platformProjectReads.length,
        ).toBeGreaterThan(0);
        expect(new Set(backend.platformProjectReads)).toEqual(
            new Set(['GET /api/platform/projects']),
        );
        expect(backend.authorizedProjectReads).toBe(
            authorizedReadsBeforeGovernance,
        );

        const platformReadsBeforeArchive =
            backend.platformProjectReads.length;
        const projectRow = page
            .getByRole('row')
            .filter({ hasText: projectA.public_id });
        await projectRow
            .getByRole('button', { name: '归档项目', exact: true })
            .click();
        const dialog = page.getByRole('dialog', {
            name: '确认归档项目',
        });
        await expect(dialog).toContainText('当前项目选择不会改变');

        const archiveResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname ===
                    `/api/platform/projects/${projectA.public_id}/archive`,
        );
        await dialog
            .getByRole('button', { name: '确认归档', exact: true })
            .click();
        expect((await archiveResponse).status()).toBe(200);

        await expect(page).toHaveURL(/#\/platform\/projects$/u);
        await expect(projectRow.getByText('已归档', { exact: true }))
            .toBeVisible();
        await expect(
            projectRow.getByRole('button', {
                name: '归档项目',
                exact: true,
            }),
        ).toBeDisabled();
        await expect.poll(
            () => backend.platformProjectReads.length,
        ).toBeGreaterThan(platformReadsBeforeArchive);
        expect(new Set(backend.platformProjectReads)).toEqual(
            new Set(['GET /api/platform/projects']),
        );
        expect(backend.archiveRequests).toEqual([
            `POST /api/platform/projects/${projectA.public_id}/archive`,
        ]);
        expect(backend.authorizedProjectReads).toBe(
            authorizedReadsBeforeGovernance,
        );
        await expect.poll(() =>
            page.evaluate(
                () => localStorage.getItem('chronodesk.activeProject'),
            ),
        ).toBeNull();
    });

    for (const platformRole of [
        'security_auditor',
        'emergency_operator',
        'member',
    ] as const satisfies readonly PlatformRole[]) {
        test(`${platformRole} 无治理入口，直达治理路由也不发平台治理请求`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                {
                    ...defaultMockIdentity,
                    id: 20,
                    sessionID: `e2e-platform-project-${platformRole}`,
                    email: `${platformRole}@example.test`,
                    platformRole,
                },
                projectA,
            );
            const backend = await installPlatformProjectBackend(page);

            await page.goto('/#/');
            await expect(
                page.getByRole('menuitem', {
                    name: '平台项目治理',
                    exact: true,
                }),
            ).toHaveCount(0);

            await page.goto('/#/platform/projects');
            await expect(page).toHaveURL(/#\/$/u);
            await expect(
                page.getByRole('button', { name: '归档项目', exact: true }),
            ).toHaveCount(0);
            expect(backend.archiveRequests).toEqual([]);
            expect(backend.platformGovernanceRequests).toEqual([]);
        });
    }
});
