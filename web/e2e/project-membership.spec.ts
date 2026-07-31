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
    version: 1,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T08:00:00Z',
});

type MembershipMockState = {
    memberships: ProjectMembership[];
    upserts: UpsertProjectMembershipRequest[];
    revokedUserIDs: number[];
    membershipReads: number;
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
        membershipReads: 0,
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
            await fulfillJSON(route, {
                code: 0,
                data: state.memberships,
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
            const persisted = existing
                ? {
                    ...existing,
                    role: payload.role,
                    is_active: true,
                    version: existing.version + 1,
                }
                : membership(
                    state.memberships.length + 1,
                    payload.user_id,
                    payload.role,
                );
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
        await page
            .getByRole('menuitem', {
                name: '项目成员管理',
                exact: true,
            })
            .click();
        await expect(page).toHaveURL(/#\/project-memberships$/u);
        await expect(
            page.getByRole('heading', {
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
        await expect(table.getByRole('separator')).toHaveCount(4);
        const userResizeHandle = table.getByRole('separator', {
            name: /调整“?用户”?列宽/u,
        });
        await userResizeHandle.focus();
        await userResizeHandle.press('ArrowRight');
        await expect(userResizeHandle).toHaveAttribute(
            'aria-valuenow',
            '308',
        );
        await expect.poll(() =>
            page.evaluate(() => {
                const raw = localStorage.getItem(
                    'chronodesk.table-columns.v1.projects.memberships',
                );
                return raw
                    ? (JSON.parse(raw) as { user?: unknown }).user
                    : null;
            }),
        ).toBe(308);
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
            await page.getByLabel('用户 ID').fill(String(userID));
            await selectRole(page, role);
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
            });
            await expect(
                table.getByRole('row', {
                    name: new RegExp(String(userID), 'u'),
                }),
            ).toBeVisible();
        }

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
        await expect(page.getByLabel('用户 ID')).toHaveValue(
            String(observerUserID),
        );
        await selectRole(page, 'manager');
        await page
            .getByRole('button', {
                name: '保存成员关系',
                exact: true,
            })
            .click();
        await expect(
            table.getByRole('row', {
                name: new RegExp(
                    `${observerUserID}.*${roleLabels.manager}`,
                    'u',
                ),
            }),
        ).toBeVisible();

        const revokeRow = table.getByRole('row', {
            name: /201/u,
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
                    `/api/projects/${projectA.key}/memberships/201`,
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

        expect(state.upserts).toHaveLength(6);
        expect(state.revokedUserIDs).toEqual([201]);
        expect(state.membershipReads).toBeGreaterThanOrEqual(8);
    });

    for (const role of projectRoleValues.filter(
        (candidate) => candidate !== 'project_admin',
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
                    name: '项目成员管理',
                    exact: true,
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
