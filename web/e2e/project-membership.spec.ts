import { expect, test, type Page } from '@playwright/test';
import {
    projectRoleValues,
    type ProjectMembership,
    type ProjectRole,
    type UpsertProjectMembershipRequest,
} from '../src/lib/generated/human-api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

const roleLabels: Record<ProjectRole, string> = {
    project_admin: '项目管理员',
    manager: '项目经理',
    agent: '项目处理人',
    requester: '项目请求人',
    observer: '项目观察员',
};

const membership = (
    id: number,
    userID: number,
    role: ProjectRole,
): ProjectMembership => ({
    id,
    project_id: projectA.id,
    user_id: userID,
    user: {
        id: userID,
        username: `membership-${role}-${userID}`,
        display_name: `${roleLabels[role]} ${userID}`,
        avatar: '',
    },
    role,
    is_active: true,
    knowledge_contributor: false,
    version: 1,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T08:00:00Z',
});

type MembershipMockState = {
    memberships: ProjectMembership[];
    upserts: UpsertProjectMembershipRequest[];
    revokedUserIDs: number[];
    revokeExpectedVersions: number[];
    membershipReads: number;
    membershipQueries: string[];
    candidateReads: number;
    pageTwoDelayMs: number;
    pageTwoFailure: boolean;
};

const installMembershipBackend = async (
    page: Page,
    projectRole: ProjectRole,
): Promise<MembershipMockState> => {
    const state: MembershipMockState = {
        memberships: projectRoleValues.map((role, index) =>
            membership(index + 1, 101 + index, role),
        ),
        upserts: [],
        revokedUserIDs: [],
        revokeExpectedVersions: [],
        membershipReads: 0,
        membershipQueries: [],
        candidateReads: 0,
        pageTwoDelayMs: 0,
        pageTwoFailure: false,
    };
    const access = authorizedProjectAccess(projectA, projectRole);
    const collectionPath =
        `/api/projects/${projectA.key}/memberships`;

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }
        if (
            url.pathname === collectionPath &&
            request.method() === 'GET'
        ) {
            state.membershipReads += 1;
            state.membershipQueries.push(url.search);
            const pageNumber = Number(url.searchParams.get('page') ?? '1');
            const pageSize = Number(
                url.searchParams.get('page_size') ?? '25',
            );
            const start = (pageNumber - 1) * pageSize;
            const items = state.memberships.slice(start, start + pageSize);
            if (pageNumber === 2 && state.pageTwoDelayMs > 0) {
                await new Promise((resolve) =>
                    setTimeout(resolve, state.pageTwoDelayMs),
                );
            }
            if (pageNumber === 2 && state.pageTwoFailure) {
                await route.fulfill({
                    status: 503,
                    contentType: 'application/json',
                    body: JSON.stringify({
                        code: 'service_unavailable',
                        msg: '第二页暂时不可用',
                    }),
                });
                return;
            }
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items,
                    total: state.memberships.length,
                    page: pageNumber,
                    page_size: pageSize,
                    total_pages: state.memberships.length === 0
                        ? 0
                        : Math.ceil(state.memberships.length / pageSize),
                },
            });
            return;
        }
        if (
            url.pathname ===
                `/api/projects/${projectA.key}/membership-candidates` &&
            request.method() === 'GET'
        ) {
            state.candidateReads += 1;
            const rawSearch = url.searchParams.get('search') ?? '';
            const numericID = Number(rawSearch.replace(/\D/gu, ''));
            const items = Number.isSafeInteger(numericID) && numericID > 0
                ? [{
                    id: numericID,
                    username: `candidate-${numericID}`,
                    display_name: `候选用户 ${numericID}`,
                    avatar: '',
                }]
                : state.memberships
                    .filter(({ user }) => user)
                    .slice(0, 25)
                    .map(({ user }) => ({
                        id: user!.id,
                        username: user!.username,
                        display_name: user!.display_name,
                        avatar: user!.avatar,
                    }));
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items,
                    total: items.length,
                    page: 1,
                    page_size: 25,
                    total_pages: items.length > 0 ? 1 : 0,
                },
            });
            return;
        }
        if (
            url.pathname === collectionPath &&
            request.method() === 'POST'
        ) {
            const payload =
                request.postDataJSON() as UpsertProjectMembershipRequest;
            state.upserts.push(payload);
            const existing = state.memberships.find(
                (item) => item.user_id === payload.user_id,
            );
            if (
                (existing && payload.expected_version !== existing.version) ||
                (!existing && payload.expected_version !== 0)
            ) {
                await fulfillJSON(
                    route,
                    {
                        code: 409,
                        msg: '成员关系已被其他操作更新，请刷新成员列表后重试',
                    },
                    409,
                );
                return;
            }
            const persisted = existing
                ? {
                    ...existing,
                    role: payload.role,
                    is_active: true,
                    knowledge_contributor:
                        payload.knowledge_contributor ?? false,
                    version: existing.version + 1,
                }
                : membership(
                    state.memberships.length + 1,
                    payload.user_id,
                    payload.role,
                );
            persisted.knowledge_contributor =
                payload.knowledge_contributor ?? false;
            state.memberships = existing
                ? state.memberships.map((item) =>
                    item.user_id === payload.user_id
                        ? persisted
                        : item,
                )
                : [...state.memberships, persisted];
            await fulfillJSON(route, {
                code: 0,
                data: persisted,
            });
            return;
        }

        const revokeMatch = url.pathname.match(
            new RegExp(
                `^${collectionPath}/([1-9][0-9]*)$`,
                'u',
            ),
        );
        if (revokeMatch && request.method() === 'DELETE') {
            const userID = Number(revokeMatch[1]);
            state.revokedUserIDs.push(userID);
            const expectedVersion = Number(
                url.searchParams.get('expected_version'),
            );
            state.revokeExpectedVersions.push(expectedVersion);
            const existing = state.memberships.find(
                (item) => item.user_id === userID,
            );
            if (!existing) {
                await fulfillJSON(
                    route,
                    { code: 1, msg: '成员关系不存在' },
                    404,
                );
                return;
            }
            if (expectedVersion !== existing.version) {
                await fulfillJSON(
                    route,
                    {
                        code: 409,
                        msg: '成员关系已被其他操作更新，请刷新成员列表后重试',
                    },
                    409,
                );
                return;
            }
            const revoked = {
                ...existing,
                is_active: false,
                version: existing.version + 1,
            };
            state.memberships = state.memberships.map((item) =>
                item.user_id === userID ? revoked : item,
            );
            await fulfillJSON(route, { code: 0, data: revoked });
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

const selectRole = async (
    page: Page,
    role: ProjectRole,
): Promise<void> => {
    await page
        .getByRole('combobox', { name: '项目职责' })
        .click();
    await page
        .getByRole('option', {
            name: roleLabels[role],
            exact: true,
        })
        .click();
};

test.describe('项目成员管理五角色 UI', () => {
    test('project_admin 可查看、授予、变更与撤销全部五种职责', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-admin',
            },
            projectA,
        );
        const state = await installMembershipBackend(
            page,
            'project_admin',
        );

        await page.goto('/#/');
        const projectSettings = page.getByRole('menuitem', {
            name: /^项目设置/u,
        });
        if (
            (await projectSettings.getAttribute('aria-expanded')) !== 'true'
        ) {
            await projectSettings.click();
        }
        await page
            .getByRole('menuitem', {
                name: '项目成员',
                exact: true,
            })
            .click();
        await expect(page).toHaveURL(/#\/project-memberships$/u);
        await expect(
            page.getByTestId('project-membership-page').getByRole('heading', {
                name: '项目成员管理',
                exact: true,
            }),
        ).toBeVisible();

        const table = page.getByRole('table', {
            name: '项目成员列表',
            exact: true,
        });
        for (const role of projectRoleValues) {
            await expect(
                table.getByRole('row', {
                    name: new RegExp(roleLabels[role], 'u'),
                }),
            ).toBeVisible();
        }
        await expect(table.getByRole('separator')).toHaveCount(5);
        const userResizeHandle = table.getByRole('separator', {
            name: /调整“?用户”?列宽/u,
        });
        await userResizeHandle.focus();
        await userResizeHandle.press('ArrowRight');
        await expect(userResizeHandle).toHaveAttribute(
            'aria-valuenow',
            '328',
        );
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = localStorage.getItem(
                    'chronodesk.table-columns.v1.42.projects.memberships',
                );
                return raw
                    ? (JSON.parse(raw) as { user?: unknown }).user
                    : null;
            }),
        ).toBe(328);
        const firstMembershipCells = table
            .getByRole('row')
            .filter({ hasText: roleLabels.project_admin })
            .first()
            .getByRole('cell');
        for (const cell of await firstMembershipCells.all()) {
            expect(
                await cell.evaluate(
                    (element) => getComputedStyle(element).whiteSpace,
                ),
            ).toBe('nowrap');
        }

        for (const [index, role] of projectRoleValues.entries()) {
            const userID = 201 + index;
            const userSearch = page.getByLabel('搜索用户');
            await userSearch.fill(`候选 ${userID}`);
            await page
                .getByRole('option', {
                    name: new RegExp(`候选用户 ${userID}`, 'u'),
                })
                .click();
            await selectRole(page, role);
            if (role === 'agent') {
                await page.getByLabel('允许创建知识草稿').check();
            }
            const saveRequest = page.waitForRequest(
                (request) =>
                    request.method() === 'POST' &&
                    new URL(request.url()).pathname ===
                        `/api/projects/${projectA.key}/memberships`,
            );
            await page
                .getByRole('button', {
                    name: '保存成员关系',
                    exact: true,
                })
                .click();
            expect((await saveRequest).postDataJSON()).toEqual({
                user_id: userID,
                role,
                knowledge_contributor: role === 'agent',
                expected_version: 0,
            });
            await expect(
                table.getByRole('row', {
                    name: new RegExp(String(userID), 'u'),
                }),
            ).toBeVisible();
        }
        const contributorUserID =
            201 + projectRoleValues.indexOf('agent');
        await expect(
            table.getByRole('row', {
                name: new RegExp(
                    `${contributorUserID}.*可创建草稿`,
                    'u',
                ),
            }),
        ).toBeVisible();

        const observerUserID =
            201 + projectRoleValues.indexOf('observer');
        const observerRow = table.getByRole('row', {
            name: new RegExp(String(observerUserID), 'u'),
        });
        await observerRow
            .getByRole('button', {
                name: '变更职责',
                exact: true,
            })
            .click();
        await expect(page.getByLabel('搜索用户')).toHaveValue(
            `${roleLabels.observer} ${observerUserID}`,
        );
        await selectRole(page, 'manager');
        const updateRequest = page.waitForRequest(
            (request) =>
                request.method() === 'POST' &&
                new URL(request.url()).pathname ===
                    `/api/projects/${projectA.key}/memberships`,
        );
        await page
            .getByRole('button', {
                name: '保存成员关系',
                exact: true,
            })
            .click();
        expect((await updateRequest).postDataJSON()).toEqual({
            user_id: observerUserID,
            role: 'manager',
            knowledge_contributor: false,
            expected_version: 1,
        });
        await expect(
            table.getByRole('row', {
                name: new RegExp(
                    `${observerUserID}.*${roleLabels.manager}`,
                    'u',
                ),
            }),
        ).toBeVisible();

        const revokeRow = table.getByRole('row', {
            name: new RegExp(String(contributorUserID), 'u'),
        });
        await revokeRow
            .getByRole('button', {
                name: '撤销项目职责',
                exact: true,
            })
            .click();
        const revokeRequest = page.waitForRequest(
            (request) =>
                request.method() === 'DELETE' &&
                new URL(request.url()).pathname ===
                    `/api/projects/${projectA.key}/memberships/${contributorUserID}`,
        );
        await page
            .getByRole('dialog')
            .getByRole('button', {
                name: '确认撤销',
                exact: true,
            })
            .click();
        await revokeRequest;
        await expect(revokeRow.getByText('已撤销', { exact: true })).toBeVisible();
        await expect(
            revokeRow.getByText('随职责撤销', { exact: true }),
        ).toBeVisible();
        await expect(
            revokeRow.getByText('可创建草稿', { exact: true }),
        ).toHaveCount(0);

        expect(state.upserts).toHaveLength(6);
        expect(state.revokedUserIDs).toEqual([contributorUserID]);
        expect(state.revokeExpectedVersions).toEqual([1]);
        expect(state.membershipReads).toBeGreaterThanOrEqual(8);
        expect(state.candidateReads).toBeGreaterThan(0);
    });

    test('旧成员表单冲突后刷新且不覆盖新授予的知识贡献权限', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-stale-upsert',
            },
            projectA,
        );
        const state = await installMembershipBackend(
            page,
            'project_admin',
        );
        await page.goto('/#/project-memberships');

        const observerUserID = 105;
        const table = page.getByRole('table', {
            name: '项目成员列表',
            exact: true,
        });
        const observerRow = table.getByRole('row', {
            name: new RegExp(String(observerUserID), 'u'),
        });
        await observerRow
            .getByRole('button', {
                name: '变更职责',
                exact: true,
            })
            .click();

        state.memberships = state.memberships.map((item) =>
            item.user_id === observerUserID
                ? {
                    ...item,
                    knowledge_contributor: true,
                    version: 2,
                }
                : item,
        );
        const readsBeforeConflict = state.membershipReads;
        await page
            .getByRole('button', {
                name: '保存成员关系',
                exact: true,
            })
            .click();

        await expect(
            page.getByRole('alert').filter({
                hasText: '成员关系已被其他操作更新，请刷新成员列表后重试',
            }),
        ).toBeVisible();
        await expect.poll(() => state.membershipReads).toBeGreaterThan(
            readsBeforeConflict,
        );
        const persisted = state.memberships.find(
            ({ user_id: userID }) => userID === observerUserID,
        );
        expect(persisted).toMatchObject({
            role: 'observer',
            knowledge_contributor: true,
            version: 2,
        });
        expect(state.upserts.at(-1)).toMatchObject({
            user_id: observerUserID,
            expected_version: 1,
            knowledge_contributor: false,
        });
        await expect(observerRow.getByText('可创建草稿')).toBeVisible();
    });

    test('旧页面不能撤销已经更新的成员职责', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-stale-revoke',
            },
            projectA,
        );
        const state = await installMembershipBackend(
            page,
            'project_admin',
        );
        await page.goto('/#/project-memberships');

        const targetUserID = 103;
        const table = page.getByRole('table', {
            name: '项目成员列表',
            exact: true,
        });
        const targetRow = table.getByRole('row', {
            name: new RegExp(String(targetUserID), 'u'),
        });
        await targetRow
            .getByRole('button', {
                name: '撤销项目职责',
                exact: true,
            })
            .click();
        state.memberships = state.memberships.map((item) =>
            item.user_id === targetUserID
                ? {
                    ...item,
                    knowledge_contributor: true,
                    version: 2,
                }
                : item,
        );
        const readsBeforeConflict = state.membershipReads;
        await page
            .getByRole('dialog')
            .getByRole('button', {
                name: '确认撤销',
                exact: true,
            })
            .click();

        await expect(
            page.getByRole('alert').filter({
                hasText: '成员关系已被其他操作更新，请刷新成员列表后重试',
            }),
        ).toBeVisible();
        await expect(
            page.getByRole('dialog', { name: '撤销项目职责' }),
        ).toHaveCount(0);
        await expect.poll(() => state.membershipReads).toBeGreaterThan(
            readsBeforeConflict,
        );
        expect(state.revokeExpectedVersions).toEqual([1]);
        expect(
            state.memberships.find(
                ({ user_id: userID }) => userID === targetUserID,
            ),
        ).toMatchObject({
            is_active: true,
            knowledge_contributor: true,
            version: 2,
        });
        await expect(targetRow.getByText('有效', { exact: true })).toBeVisible();
    });

    test('manager 可查看成员但没有候选搜索或成员写操作', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-manager',
            },
            projectA,
        );
        const state = await installMembershipBackend(page, 'manager');
        await page.goto('/#/');
        const projectSettings = page.getByRole('menuitem', {
            name: /^项目设置/u,
        });
        if (
            (await projectSettings.getAttribute('aria-expanded')) !== 'true'
        ) {
            await projectSettings.click();
        }
        await page
            .getByRole('menuitem', {
                name: '项目成员',
                exact: true,
            })
            .click();
        await expect(page).toHaveURL(/#\/project-memberships$/u);
        await expect(
            page.getByTestId('project-membership-read-only'),
        ).toBeVisible();
        await expect(
            page.getByRole('table', { name: '项目成员列表' }),
        ).toBeVisible();
        await expect(page.getByLabel('搜索用户')).toHaveCount(0);
        await expect(
            page.getByRole('button', { name: '变更职责', exact: true }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('button', {
                name: '撤销项目职责',
                exact: true,
            }),
        ).toHaveCount(0);
        expect(state.membershipReads).toBeGreaterThan(0);
        expect(state.candidateReads).toBe(0);
    });

    test('成员目录使用有界分页并取消过期翻页请求', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-pagination',
            },
            projectA,
        );
        const state = await installMembershipBackend(
            page,
            'project_admin',
        );
        state.memberships = Array.from({ length: 30 }, (_, index) =>
            membership(
                index + 1,
                1001 + index,
                'observer',
            ),
        );
        state.pageTwoDelayMs = 250;

        await page.goto('/#/project-memberships');
        const table = page.getByRole('table', {
            name: '项目成员列表',
            exact: true,
        });
        await expect(table.getByText('项目观察员 1001')).toBeVisible();
        expect(state.membershipQueries[0]).toContain('page=1');
        expect(state.membershipQueries[0]).toContain('page_size=25');
        expect(state.membershipQueries[0]).toContain('sort_by=is_active');
        expect(state.membershipQueries[0]).toContain('sort_order=desc');

        await page
            .getByRole('button', { name: /下一页|next page/iu })
            .click();
        await expect.poll(() => state.membershipQueries).toContainEqual(
            expect.stringContaining('page=2'),
        );
        await page
            .getByRole('combobox', {
                name: '每页行数',
                exact: true,
            })
            .click();
        await page.getByRole('option', { name: '50' }).click();

        await expect(table.getByText('项目观察员 1001')).toBeVisible();
        await expect(table.getByText('项目观察员 1030')).toBeVisible();
        await expect(
            page.getByText('请求已取消', { exact: true }),
        ).toHaveCount(0);
        expect(state.membershipQueries).toContainEqual(
            expect.stringContaining('page_size=50'),
        );
    });

    test('成员目录翻页失败不把上一页成员冒充为新页', async ({ page }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-project-membership-page-failure',
            },
            projectA,
        );
        const state = await installMembershipBackend(
            page,
            'project_admin',
        );
        state.memberships = Array.from({ length: 30 }, (_, index) =>
            membership(index + 1, 1001 + index, 'observer'),
        );

        await page.goto('/#/project-memberships');
        const table = page.getByRole('table', {
            name: '项目成员列表',
            exact: true,
        });
        await expect(table.getByText('项目观察员 1001')).toBeVisible();
        await table
            .getByRole('button', {
                name: '变更职责',
                exact: true,
            })
            .first()
            .click();
        await expect(
            page.getByRole('button', {
                name: '保存成员关系',
                exact: true,
            }),
        ).toBeEnabled();

        state.pageTwoFailure = true;
        await page
            .getByRole('button', { name: /下一页|next page/iu })
            .click();
        await expect(page.getByRole('alert')).toContainText(
            '安全执行保护暂时不可用',
        );
        await expect(table.getByText('项目观察员 1001')).toHaveCount(0);
        await expect(
            table.getByRole('button', {
                name: '撤销项目职责',
                exact: true,
            }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('button', {
                name: '保存成员关系',
                exact: true,
            }),
        ).toBeDisabled();

        state.pageTwoFailure = false;
        await page
            .getByRole('button', { name: '重试', exact: true })
            .click();
        await expect(table.getByText('项目观察员 1026')).toBeVisible();
    });

    for (const role of projectRoleValues.filter(
        (candidate) =>
            candidate !== 'project_admin' && candidate !== 'manager',
    )) {
        test(`${role} 无项目成员管理菜单且直接路由不发 memberships 请求`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                {
                    ...defaultMockIdentity,
                    sessionID: `e2e-project-membership-${role}`,
                },
                projectA,
            );
            const state = await installMembershipBackend(page, role);
            await page.goto('/#/');

            await expect(
                page.getByRole('menuitem', {
                    name: /^项目设置/u,
                }),
            ).toHaveCount(0);
            await page.goto('/#/project-memberships');
            await expect(page).toHaveURL(/#\/?$/u);
            await expect(
                page.getByTestId('project-membership-page'),
            ).toHaveCount(0);
            expect(state.membershipReads).toBe(0);
        });
    }
});
