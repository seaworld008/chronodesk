import {
    expect,
    test,
    type Page,
    type Request,
} from '@playwright/test';
import type { ProjectRole } from '../src/lib/generated/human-api';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

const ticketID = 9001;
const internalNote = 'REQUESTER-MUST-NEVER-SEE-INTERNAL-NOTE';
const internalComment = 'REQUESTER-MUST-NEVER-SEE-INTERNAL-COMMENT';
const internalAttachment = 'requester-must-never-see-internal.txt';

const ticket = {
    id: ticketID,
    public_id: '00000000-0000-7000-8000-000000009001',
    project_id: projectA.id,
    ticket_number: `OPS-${ticketID}`,
    title: '项目作用域工作流契约工单',
    description: '用于验证工作流路径与 requester 最小权限的工单。',
    type: 'request',
    priority: 'normal',
    status: 'open',
    source: 'web',
    created_by_id: defaultMockIdentity.id,
    assigned_to_id: 77,
    category_id: null,
    version: 5,
    tags: ['e2e'],
    internal_notes: internalNote,
    sla_breached: false,
    created_at: '2026-07-30T08:00:00Z',
    updated_at: '2026-07-30T09:00:00Z',
};

type RecordedRequest = {
    method: string;
    pathname: string;
    headers: Record<string, string>;
    body: unknown;
};

type TicketMockState = {
    requests: RecordedRequest[];
};

const requestBody = (request: Request): unknown => {
    const contentType = request.headers()['content-type'] ?? '';
    if (contentType.includes('application/json') && request.postData()) {
        return request.postDataJSON();
    }
    return request.postData() ?? undefined;
};

const installTicketBackend = async (
    page: Page,
    projectRole: ProjectRole,
): Promise<TicketMockState> => {
    const state: TicketMockState = { requests: [] };
    const access = authorizedProjectAccess(projectA, projectRole);

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        state.requests.push({
            method: request.method(),
            pathname: url.pathname,
            headers: request.headers(),
            body: requestBody(request),
        });

        if (url.pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, data: [access] });
            return;
        }

        const ticketPath = `/api/projects/${projectA.key}/tickets/${ticketID}`;
        if (
            url.pathname === ticketPath &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, { code: 0, data: ticket });
            return;
        }
        if (
            url.pathname === ticketPath &&
            request.method() === 'PUT'
        ) {
            const body = requestBody(request);
            await fulfillJSON(route, {
                code: 0,
                data: {
                    ...ticket,
                    ...(body && typeof body === 'object' ? body : {}),
                    version: ticket.version + 1,
                },
            });
            return;
        }

        if (
            url.pathname ===
                `/api/projects/${projectA.key}/assignees`
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: [
                    {
                        id: 88,
                        username: 'target-agent',
                        first_name: 'Target',
                        last_name: 'Agent',
                    },
                ],
            });
            return;
        }

        if (
            url.pathname === `${ticketPath}/comments` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: [
                    {
                        id: 1,
                        content: '公开评论仍然可见',
                        type: 'public',
                        created_at: '2026-07-30T08:30:00Z',
                        actor: { type: 'human', id: '42' },
                    },
                    {
                        id: 2,
                        content: internalComment,
                        type: 'internal',
                        created_at: '2026-07-30T08:35:00Z',
                        actor: { type: 'human', id: '7' },
                    },
                ],
            });
            return;
        }

        if (
            url.pathname === `${ticketPath}/comments` &&
            request.method() === 'POST'
        ) {
            await fulfillJSON(
                route,
                {
                    code: 0,
                    data: { id: 3 },
                    receipt: { resource_version: 6 },
                },
                201,
                { ETag: '"v6"' },
            );
            return;
        }

        if (
            url.pathname === `${ticketPath}/attachments` &&
            request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: [
                    {
                        id: 11,
                        original_name: 'public-guide.txt',
                        file_size: 12,
                        mime_type: 'text/plain',
                        virus_scan: 'clean',
                        is_public: true,
                        created_at: '2026-07-30T08:40:00Z',
                    },
                    {
                        id: 12,
                        original_name: internalAttachment,
                        file_size: 14,
                        mime_type: 'text/plain',
                        virus_scan: 'clean',
                        is_public: false,
                        created_at: '2026-07-30T08:45:00Z',
                    },
                ],
            });
            return;
        }

        if (
            url.pathname === `${ticketPath}/attachments` &&
            request.method() === 'POST'
        ) {
            await fulfillJSON(
                route,
                {
                    code: 0,
                    data: { id: 13 },
                    receipt: { resource_version: 7 },
                },
                201,
                { ETag: '"v7"' },
            );
            return;
        }

        if (
            url.pathname.startsWith(`${ticketPath}/`) &&
            request.method() === 'POST'
        ) {
            await fulfillJSON(route, {
                code: 0,
                data: {
                    ...ticket,
                    version: ticket.version + 1,
                },
                receipt: { resource_version: ticket.version + 1 },
            });
            return;
        }

        if (
            url.pathname ===
                `/api/projects/${projectA.key}/tickets/stats`
        ) {
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
                    my_tickets: 1,
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

const workflowCases = [
    {
        name: 'assign',
        button: '重新分配',
        confirm: '确认重新分配',
        suffix: 'assign',
        prepare: async (page: Page) => {
            const dialog = page.getByRole('dialog');
            await dialog
                .getByRole('combobox', { name: '分配给' })
                .click();
            await page
                .getByRole('option', { name: /target-agent/u })
                .click();
        },
    },
    {
        name: 'transfer',
        button: '转移工单',
        confirm: '确认转移工单',
        suffix: 'transfer',
        prepare: async (page: Page) => {
            const dialog = page.getByRole('dialog');
            await dialog.getByRole('combobox').first().click();
            await page
                .getByRole('option', { name: '技术支持', exact: true })
                .click();
            await dialog
                .getByRole('combobox', { name: '转移给' })
                .click();
            await page
                .getByRole('option', { name: 'target-agent', exact: true })
                .click();
        },
    },
    {
        name: 'escalate',
        button: '升级工单',
        confirm: '确认升级工单',
        suffix: 'escalate',
        prepare: async (page: Page) => {
            const dialog = page.getByRole('dialog');
            await dialog
                .getByRole('textbox', { name: '升级原因' })
                .fill('需要项目管理员协同');
            await dialog
                .getByRole('combobox', { name: '升级给' })
                .click();
            await page
                .getByRole('option', { name: 'target-agent', exact: true })
                .click();
        },
    },
    {
        name: 'status',
        button: '状态变更',
        confirm: '确认状态变更',
        suffix: 'status',
        prepare: async (page: Page) => {
            const dialog = page.getByRole('dialog');
            await dialog.getByRole('combobox').click();
            await page
                .getByRole('option', { name: '处理中', exact: true })
                .click();
        },
    },
] as const;

test.describe('工单 Human UI 项目作用域与 requester 最小权限', () => {
    for (const workflowCase of workflowCases) {
        test(`${workflowCase.name} 仅请求 /api/projects/{key}/tickets/...`, async ({
            page,
        }) => {
            await installMockSession(
                page,
                {
                    ...defaultMockIdentity,
                    sessionID: `e2e-workflow-${workflowCase.name}`,
                },
                projectA,
            );
            const state = await installTicketBackend(
                page,
                'project_admin',
            );
            await page.goto(`/#/tickets/${ticketID}/show`);

            await page
                .getByRole('main')
                .getByRole('button', {
                    name: workflowCase.button,
                    exact: true,
                })
                .click();
            await workflowCase.prepare(page);

            const expectedPath =
                `/api/projects/${projectA.key}/tickets/` +
                `${ticketID}/${workflowCase.suffix}`;
            const operationRequest = page.waitForRequest(
                (request) =>
                    request.method() === 'POST' &&
                    new URL(request.url()).pathname === expectedPath,
            );
            await page
                .getByRole('dialog')
                .getByRole('button', {
                    name: workflowCase.confirm,
                    exact: true,
                })
                .click();
            const captured = await operationRequest;

            expect(new URL(captured.url()).pathname).toBe(expectedPath);
            expect(captured.headers()['if-match']).toBe('"v5"');
            expect(
                state.requests.filter(
                    (request) =>
                        request.method === 'POST' &&
                        /^\/api\/tickets(?:\/|$)/u.test(request.pathname),
                ),
            ).toEqual([]);
        });
    }

    test('requester 编辑只发安全字段，详情无工作流/内部内容且公开评论附件可用', async ({
        page,
    }) => {
        await installMockSession(
            page,
            {
                ...defaultMockIdentity,
                sessionID: 'e2e-requester-ticket-contract',
            },
            projectA,
        );
        const state = await installTicketBackend(page, 'requester');

        await page.goto(`/#/tickets/${ticketID}`);
        await expect(page.getByLabel('状态')).toHaveCount(0);

        await page.getByRole('tab', { name: '分类', exact: true }).click();
        await expect(page.getByLabel('分配给')).toHaveCount(0);

        await page
            .getByRole('tab', { name: '附加信息', exact: true })
            .click();
        await expect(page.getByLabel('内部备注')).toHaveCount(0);

        await page.getByRole('tab', { name: '基本信息', exact: true }).click();
        await page
            .getByLabel('工单标题')
            .fill('requester 更新后的安全标题');
        const updateRequest = page.waitForRequest(
            (request) =>
                request.method() === 'PUT' &&
                new URL(request.url()).pathname ===
                    `/api/projects/${projectA.key}/tickets/${ticketID}`,
        );
        await page.getByRole('button', { name: '保存更改' }).click();
        const update = await updateRequest;
        const updateBody = update.postDataJSON() as Record<string, unknown>;
        expect(updateBody.title).toBe('requester 更新后的安全标题');
        expect(updateBody).not.toHaveProperty('status');
        expect(updateBody).not.toHaveProperty('assigned_to_id');
        expect(updateBody).not.toHaveProperty('internal_notes');

        await page.goto(`/#/tickets/${ticketID}/show`);
        const main = page.getByRole('main');
        for (const action of [
            '分配工单',
            '重新分配',
            '转移工单',
            '升级工单',
            '状态变更',
        ]) {
            await expect(
                main.getByRole('button', {
                    name: action,
                    exact: true,
                }),
            ).toHaveCount(0);
        }

        await page
            .getByRole('tab', { name: '附加信息', exact: true })
            .click();
        await expect(page.getByText(internalNote, { exact: true })).toHaveCount(
            0,
        );
        await expect(page.getByText('内部备注', { exact: true })).toHaveCount(
            0,
        );

        await page
            .getByRole('tab', { name: '评论历史', exact: true })
            .click();
        await expect(
            page.getByText('公开评论仍然可见', { exact: true }),
        ).toBeVisible();
        await expect(
            page.getByText(internalComment, { exact: true }),
        ).toHaveCount(0);
        await expect(
            page.getByText(internalAttachment, { exact: true }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('combobox', { name: '可见性' }),
        ).toHaveCount(0);
        await expect(
            page.getByText('公开评论', { exact: true }),
        ).toBeVisible();
        await expect(
            page.getByText('公开附件', { exact: true }),
        ).toBeVisible();

        await page
            .getByRole('textbox', { name: '评论内容' })
            .fill('requester 的公开评论');
        const commentRequest = page.waitForRequest(
            (request) =>
                request.method() === 'POST' &&
                new URL(request.url()).pathname ===
                    `/api/projects/${projectA.key}/tickets/` +
                        `${ticketID}/comments`,
        );
        await page
            .getByRole('button', { name: '添加评论', exact: true })
            .click();
        const comment = await commentRequest;
        expect(comment.postDataJSON()).toEqual({
            content: 'requester 的公开评论',
            content_type: 'text',
            type: 'public',
        });

        await page.locator('input[type="file"]').setInputFiles({
            name: 'requester-public.txt',
            mimeType: 'text/plain',
            buffer: Buffer.from('public attachment'),
        });
        const attachmentRequest = page.waitForRequest(
            (request) =>
                request.method() === 'POST' &&
                new URL(request.url()).pathname ===
                    `/api/projects/${projectA.key}/tickets/` +
                        `${ticketID}/attachments`,
        );
        await page
            .getByRole('button', { name: '上传附件', exact: true })
            .click();
        const attachment = await attachmentRequest;
        expect(attachment.postData()).toContain('name="visibility"');
        expect(attachment.postData()).toContain('public');
        expect(attachment.postData()).not.toContain('internal');

        expect(
            state.requests.filter(
                (request) =>
                    request.method !== 'GET' &&
                    /^\/api\/tickets(?:\/|$)/u.test(request.pathname),
            ),
        ).toEqual([]);
    });
});
