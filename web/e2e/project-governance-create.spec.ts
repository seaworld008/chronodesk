import { expect, test, type Page } from '@playwright/test';
import type {
    CreatePlatformProjectRequest,
    PlatformProjectSummary,
} from '../src/lib/generated/human-api';
import {
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
} from './helpers/mockHumanSession';

const organization = {
    public_id: '00000000-0000-7000-8000-000000000081',
    name: 'ChronoDesk',
};
const businessUnit = {
    public_id: '00000000-0000-7000-8000-000000000082',
    key: 'OPS',
    name: '运营中心',
    description: '运营业务单元',
};
const creator = {
    id: 31,
    username: 'platform-creator',
    display_name: '平台创建者',
    avatar: '',
};
const explicitAdministrator = {
    id: 88,
    username: 'explicit-project-admin',
    display_name: '明确项目管理员',
    avatar: '',
};
const createdProject: PlatformProjectSummary = {
    public_id: '00000000-0000-7000-8000-000000000083',
    created_at: '2026-07-31T10:00:00Z',
    updated_at: '2026-07-31T10:00:00Z',
    key: 'NEW_PROJ',
    name: '新项目',
    description: '由向导创建',
    status: 'active',
    business_unit: businessUnit,
};

type CreateBackendState = {
    authorizedProjectReads: number;
    creationContextReads: URLSearchParams[];
    createPayloads: CreatePlatformProjectRequest[];
    created: boolean;
};

const installCreateBackend = async (
    page: Page,
): Promise<CreateBackendState> => {
    const state: CreateBackendState = {
        authorizedProjectReads: 0,
        creationContextReads: [],
        createPayloads: [],
        created: false,
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());

        if (
            url.pathname === '/api/projects' &&
            request.method() === 'GET'
        ) {
            state.authorizedProjectReads += 1;
            // The creator is deliberately removed from the explicit initial
            // administrator set and therefore keeps zero project access.
            await fulfillJSON(route, { code: 0, data: [] });
            return;
        }

        if (
            url.pathname === '/api/platform/project-business-units' &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    items: [businessUnit],
                    total: 1,
                    page: 1,
                    page_size: 25,
                    total_pages: 1,
                },
            });
            return;
        }

        if (
            url.pathname === '/api/platform/project-creation-context' &&
            request.method() === 'GET'
        ) {
            state.creationContextReads.push(new URLSearchParams(url.search));
            const userSearch = url.searchParams.get('search') ?? '';
            const unitSearch =
                url.searchParams.get('business_unit_search') ?? '';
            const users = userSearch.includes('明确')
                ? [explicitAdministrator]
                : [creator];
            const units = !unitSearch ||
                businessUnit.name.includes(unitSearch) ||
                businessUnit.key.includes(unitSearch)
                ? [businessUnit]
                : [];
            await fulfillJSON(route, {
                code: 0,
                data: {
                    organization,
                    business_units: {
                        items: units,
                        total: units.length,
                        page: 1,
                        page_size: 25,
                        total_pages: units.length > 0 ? 1 : 0,
                    },
                    creator,
                    users: {
                        items: users,
                        total: users.length,
                        page: 1,
                        page_size: 25,
                        total_pages: users.length > 0 ? 1 : 0,
                    },
                },
            });
            return;
        }

        if (
            url.pathname === '/api/platform/projects' &&
            request.method() === 'GET'
        ) {
            const items = state.created ? [createdProject] : [];
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
            url.pathname === '/api/platform/projects' &&
            request.method() === 'POST'
        ) {
            const payload =
                request.postDataJSON() as CreatePlatformProjectRequest;
            state.createPayloads.push(payload);
            state.created = true;
            await fulfillJSON(
                route,
                {
                    code: 0,
                    msg: '项目创建成功',
                    data: createdProject,
                },
                201,
            );
            return;
        }

        await fulfillJSON(route, { code: 1, msg: '未配置的测试路由' }, 404);
    });

    return state;
};

test.describe('平台项目创建向导', () => {
    test('零 Membership 的 platform_admin 可移除创建者并显式选择初始管理员', async ({
        page,
    }) => {
        await installMockSession(page, {
            ...defaultMockIdentity,
            id: creator.id,
            sessionID: 'e2e-project-create-explicit-admin',
            email: 'project-create@example.test',
            platformRole: 'platform_admin',
        });
        const state = await installCreateBackend(page);

        await page.goto('/#/');
        await expect(page.getByTestId('platform-home')).toBeVisible();
        await expect.poll(
            () => state.authorizedProjectReads,
        ).toBeGreaterThan(0);
        const authorizedReadsBeforeCreate = state.authorizedProjectReads;

        await page
            .getByRole('button', { name: '创建项目', exact: true })
            .click();
        await expect(page).toHaveURL(
            /#\/platform\/projects\?create=1$/u,
        );
        const wizard = page.getByRole('dialog', { name: '创建项目' });
        await expect(wizard).toContainText(
            `组织由服务端从认证上下文解析：${organization.name}`,
        );

        await wizard.getByLabel('项目名称').fill(createdProject.name);
        await wizard.getByLabel('项目键').fill('new_proj');
        await expect(wizard.getByLabel('项目键')).toHaveValue('NEW_PROJ');
        await expect(wizard).toContainText('创建后不可修改');
        await wizard.getByLabel('项目描述').fill(createdProject.description);
        await wizard
            .getByRole('button', { name: '下一步', exact: true })
            .click();

        const creatorChip = wizard.getByTestId(
            `initial-project-admin-${creator.id}`,
        );
        await expect(creatorChip).toBeVisible();
        await expect(wizard).toContainText('已选择 1 项');
        await creatorChip.getByTestId('CancelIcon').click();
        await expect(wizard).toContainText('已选择 0 项');

        await wizard
            .getByLabel('搜索并选择初始项目管理员')
            .fill('明确');
        await page
            .getByRole('option', {
                name: new RegExp(explicitAdministrator.display_name, 'u'),
            })
            .click();
        await expect(
            wizard.getByTestId(
                `initial-project-admin-${explicitAdministrator.id}`,
            ),
        ).toBeVisible();
        await expect(wizard).toContainText('已选择 1 项');
        await wizard
            .getByRole('button', { name: '下一步', exact: true })
            .click();

        await expect(wizard.getByLabel('默认队列键')).toHaveValue('default');
        await expect(wizard.getByLabel('默认队列名称')).toHaveValue(
            '默认队列',
        );
        const createRequest = page.waitForRequest(
            (request) =>
                request.method() === 'POST' &&
                new URL(request.url()).pathname ===
                    '/api/platform/projects',
        );
        await wizard
            .getByRole('button', { name: '创建项目', exact: true })
            .click();
        await createRequest;

        expect(state.createPayloads).toEqual([
            {
                business_unit_public_id: businessUnit.public_id,
                key: createdProject.key,
                name: createdProject.name,
                description: createdProject.description,
                initial_project_admin_user_ids: [
                    explicitAdministrator.id,
                ],
                default_queue_key: 'default',
                default_queue_name: '默认队列',
            },
        ]);
        expect(
            Object.hasOwn(state.createPayloads[0], 'organization_id'),
        ).toBe(false);
        expect(
            Object.hasOwn(state.createPayloads[0], 'business_unit_id'),
        ).toBe(false);
        await expect(wizard).toHaveCount(0);
        await expect(
            page.getByRole('row').filter({ hasText: createdProject.public_id }),
        ).toBeVisible();
        await expect.poll(
            () => state.authorizedProjectReads,
        ).toBeGreaterThan(authorizedReadsBeforeCreate);
        expect(
            state.creationContextReads.some(
                (query) =>
                    query.get('page_size') === '25' &&
                    query.get('business_unit_page_size') === '25',
            ),
        ).toBe(true);
    });
});
