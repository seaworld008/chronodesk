import { randomUUID } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { monitorBrowserHealth } from './helpers/browserAudit';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
    projectB,
} from './helpers/mockHumanSession';

const timestamp = '2026-08-01T08:00:00Z';
const ticketID = 9001;

type MockOptions = {
    projectRole?: 'project_admin' | 'manager' | 'agent';
    failFirstCollaboration?: boolean;
    failFirstKnowledge?: boolean;
    failFirstEntityLinks?: boolean;
    holdOpsCollaborationSecondPage?: boolean;
    holdOpsKnowledgeSecondPage?: boolean;
};

type MockState = {
    collaborationLists: URL[];
    collaborationDetails: URL[];
    knowledgeLists: URL[];
    knowledgeVersions: URL[];
    relationshipLists: URL[];
    projectSettingsRequests: URL[];
    approvalBodies: Array<Record<string, unknown>>;
    takeoverBodies: Array<Record<string, unknown>>;
    releaseOpsCollaborationSecondPage: () => void;
    releaseOpsKnowledgeSecondPage: () => void;
};

const directory = <T,>(
    items: T[],
    page: number,
    pageSize: number,
    total = 10_000,
) => ({
    items,
    total,
    page,
    page_size: pageSize,
    total_pages: Math.ceil(total / pageSize),
});

const pageParameters = (url: URL) => ({
    page: Number(url.searchParams.get('page') ?? '1'),
    pageSize: Number(url.searchParams.get('page_size') ?? '25'),
});

const ordinalRange = (page: number, pageSize: number) =>
    Array.from(
        { length: pageSize },
        (_, index) => (page - 1) * pageSize + index + 1,
    );

const runItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-run-${ordinal}`,
    created_at: timestamp,
    ticket_id: ticketID,
    ticket_number: `${projectKey}-${ordinal}`,
    ticket_title: `${projectKey} 协作工单 ${ordinal}`,
    updated_at: timestamp,
    status: ordinal === 1 ? 'running' : 'queued',
});

const approvalItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-approval-${ordinal}`,
    created_at: timestamp,
    ticket_id: ticketID,
    ticket_number: `${projectKey}-${ordinal}`,
    ticket_title: `${projectKey} 审批工单 ${ordinal}`,
    updated_at: timestamp,
    proposal_id: `${projectKey}-proposal-${ordinal}`,
    target_ticket_version: 7,
    required_approvals: 2,
    status: ordinal === 1 ? 'pending' : 'approved',
    expires_at: '2026-08-02T08:00:00Z',
});

const proposalItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-proposal-${ordinal}`,
    created_at: timestamp,
    ticket_id: ticketID,
    ticket_number: `${projectKey}-${ordinal}`,
    ticket_title: `${projectKey} 提案工单 ${ordinal}`,
    updated_at: timestamp,
    agent_run_id: `${projectKey}-run-${ordinal}`,
    action_type: 'ticket.assign',
    risk_level: ordinal === 1 ? 'high' : 'low',
    target_ticket_version: 7,
    status: 'pending',
    expires_at: '2026-08-02T08:00:00Z',
});

const handoffItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-handoff-${ordinal}`,
    created_at: timestamp,
    ticket_id: ticketID,
    ticket_number: `${projectKey}-${ordinal}`,
    ticket_title: `${projectKey} 交接工单 ${ordinal}`,
    agent_run_id: `${projectKey}-run-${ordinal}`,
    direction: 'agent_to_human',
});

const articleItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-article-${ordinal}`,
    key: `${projectKey.toLowerCase()}-kb-${ordinal}`,
    title: `${projectKey} 知识文章 ${ordinal}`,
    summary: '分页知识文章',
    status: 'active',
    current_version_id: `${projectKey}-version-${ordinal}-3`,
    revision: 3,
    created_at: timestamp,
    updated_at: timestamp,
});

const versionItem = (
    projectKey: string,
    articleID: string,
    ordinal: number,
) => ({
    id: `${projectKey}-version-${ordinal}`,
    article_id: articleID,
    version: ordinal,
    status: 'published',
    created_by_type: ordinal % 2 === 0 ? 'service_principal' : 'human',
    title: `${projectKey} 知识版本 ${ordinal}`,
    original_file_name: `knowledge-${ordinal}.pdf`,
    mime_type: 'application/pdf',
    size_bytes: 4096,
    content_hash: 'a'.repeat(64),
    virus_scan: 'clean',
    scanned_at: timestamp,
    page_count: 8,
    published_at: timestamp,
    created_at: timestamp,
    updated_at: timestamp,
});

const ingestionItem = (projectKey: string, ordinal: number) => ({
    id: `${projectKey}-ingestion-${ordinal}`,
    article_id: `${projectKey}-article-${ordinal}`,
    version_id: `${projectKey}-version-${ordinal}`,
    attempt: 1,
    status: 'completed',
    parser_key: 'pdf-v2',
    started_at: timestamp,
    completed_at: timestamp,
    created_at: timestamp,
    updated_at: timestamp,
});

const entityLinkItem = (projectKey: string, ordinal: number) => ({
    id: ordinal,
    public_id: `${projectKey}-entity-${ordinal}`,
    kind: ordinal % 2 === 0 ? 'device' : 'asset',
    reference_id: `${projectKey}-ASSET-${ordinal}`,
    display_name: `${projectKey} 关联资产 ${ordinal}`,
    metadata: {},
    created_at: timestamp,
});

const relationItem = (projectKey: string, ordinal: number) => ({
    id: ordinal,
    public_id: `${projectKey}-relation-${ordinal}`,
    relation: ordinal % 2 === 0 ? 'blocks' : 'collaborates_with',
    direction: ordinal % 2 === 0 ? 'incoming' : 'outgoing',
    related_ticket_id: ticketID + ordinal,
    related_ticket_number: `${projectKey}-${ticketID + ordinal}`,
    related_ticket_title: `${projectKey} 关联工单 ${ordinal}`,
    reason: '同一业务事件',
    created_at: timestamp,
});

const ticket = {
    id: ticketID,
    public_id: '00000000-0000-7000-8000-000000009001',
    project_id: projectA.id,
    ticket_number: 'OPS-9001',
    title: '第一阶段关联关系回归工单',
    description: '仅用于无写入的 Playwright mock 回归。',
    type: 'incident',
    priority: 'normal',
    status: 'open',
    source: 'web',
    created_by_id: defaultMockIdentity.id,
    created_by: {
        id: defaultMockIdentity.id,
        username: 'phase-one-reviewer',
        display_name: '第一阶段验收员',
    },
    assigned_to_id: defaultMockIdentity.id,
    assigned_to: {
        id: defaultMockIdentity.id,
        username: 'phase-one-reviewer',
        display_name: '第一阶段验收员',
    },
    version: 7,
    tags: ['回归', '关联'],
    sla_breached: false,
    created_at: timestamp,
    updated_at: timestamp,
};

const installPhaseOneBackend = async (
    page: Page,
    options: MockOptions = {},
): Promise<MockState> => {
    await installMockSession(
        page,
        {
            ...defaultMockIdentity,
            sessionID: `phase-one-${Date.now()}-${randomUUID()}`,
        },
        projectA,
    );
    const accesses = [
        authorizedProjectAccess(
            projectA,
            options.projectRole ?? 'project_admin',
        ),
        authorizedProjectAccess(
            projectB,
            options.projectRole ?? 'project_admin',
        ),
    ];
    let collaborationFailures = 0;
    let knowledgeFailures = 0;
    let entityLinkFailures = 0;
    let releaseCollaboration!: () => void;
    let releaseKnowledge!: () => void;
    const collaborationGate = new Promise<void>((resolve) => {
        releaseCollaboration = resolve;
    });
    const knowledgeGate = new Promise<void>((resolve) => {
        releaseKnowledge = resolve;
    });
    const state: MockState = {
        collaborationLists: [],
        collaborationDetails: [],
        knowledgeLists: [],
        knowledgeVersions: [],
        relationshipLists: [],
        projectSettingsRequests: [],
        approvalBodies: [],
        takeoverBodies: [],
        releaseOpsCollaborationSecondPage: releaseCollaboration,
        releaseOpsKnowledgeSecondPage: releaseKnowledge,
    };

    await page.route('**/api/**', async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const { pathname } = url;

        if (pathname === '/api/auth/me') {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    id: defaultMockIdentity.id,
                    username: 'phase-one-reviewer',
                    email: defaultMockIdentity.email,
                    platform_role: 'member',
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                },
            });
            return;
        }
        if (pathname === '/api/projects') {
            await fulfillJSON(route, { code: 0, msg: 'ok', data: accesses });
            return;
        }
        if (pathname.endsWith('/context')) {
            const projectKey = pathname.split('/')[3];
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: accesses.find(
                    ({ project }) => project.key === projectKey,
                ),
            });
            return;
        }
        if (pathname.endsWith('/notifications/unread-count')) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: { count: 0 },
            });
            return;
        }

        const projectMatch = pathname.match(
            /^\/api\/projects\/(OPS|FIN)\/(.+)$/u,
        );
        if (!projectMatch) {
            await fulfillJSON(route, { code: 0, msg: 'ok', data: [] });
            return;
        }
        const [, projectKey, resource] = projectMatch;

        if (
            resource === 'configuration/intake'
            && request.method() === 'GET'
        ) {
            state.projectSettingsRequests.push(url);
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    release_id: `${projectKey}-release-7`,
                    release_version: 7,
                    request_types: [{
                        id: `${projectKey}-request-type-1`,
                        version: 3,
                        status: 'published',
                        key: 'incident',
                        name: '事件受理',
                        description: '用于生产故障及服务中断。',
                        work_class: 'incident',
                        json_schema: {},
                        ui_schema: {},
                        published_at: timestamp,
                    }],
                    workflows: [{
                        id: `${projectKey}-workflow-1`,
                        version: 4,
                        status: 'published',
                        key: 'standard-resolution',
                        name: '标准处理流程',
                        description: '受理、处理、解决与关闭。',
                        states: [],
                        transitions: [],
                        published_at: timestamp,
                    }],
                },
            });
            return;
        }
        if (resource === 'queues' && request.method() === 'GET') {
            state.projectSettingsRequests.push(url);
            const { page: requestedPage, pageSize } = pageParameters(url);
            const remaining = Math.max(
                0,
                151 - (requestedPage - 1) * pageSize,
            );
            const count = Math.min(pageSize, remaining);
            const items = ordinalRange(requestedPage, pageSize)
                .slice(0, count)
                .map((ordinal) => ({
                    public_id: `${projectKey}-queue-${ordinal}`,
                    created_at: timestamp,
                    updated_at: timestamp,
                    team_public_id: `${projectKey}-team-${ordinal}`,
                    team_name: `支持团队 ${ordinal}`,
                    key: `queue-${ordinal}`,
                    name: `${projectKey} 受理队列 ${ordinal}`,
                    description: '按当前项目隔离的受理队列。',
                    status: 'active',
                    is_default: ordinal === 1,
                }));
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: directory(items, requestedPage, pageSize, 151),
            });
            return;
        }
        if (
            /^admin\/automation\/(sla|templates|quick-replies)$/u
                .test(resource)
            && request.method() === 'GET'
        ) {
            state.projectSettingsRequests.push(url);
            const { page: requestedPage, pageSize } = pageParameters(url);
            await fulfillJSON(route, {
                success: true,
                message: 'ok',
                data: directory([], requestedPage, pageSize, 0),
            });
            return;
        }

        const collaborationMatch = resource.match(
            /^agent-collaboration\/(runs|proposals|approvals|handoffs)(?:\/([^/]+))?(?:\/(decisions|takeover))?$/u,
        );
        if (collaborationMatch) {
            const [, kind, recordID, action] = collaborationMatch;
            if (request.method() === 'POST' && action === 'decisions') {
                state.approvalBodies.push(
                    request.postDataJSON() as Record<string, unknown>,
                );
                await fulfillJSON(route, {
                    code: 0,
                    msg: 'ok',
                    data: { status: 'approved' },
                });
                return;
            }
            if (request.method() === 'POST' && action === 'takeover') {
                state.takeoverBodies.push(
                    request.postDataJSON() as Record<string, unknown>,
                );
                await fulfillJSON(route, {
                    code: 0,
                    msg: 'ok',
                    data: { status: 'taken_over' },
                });
                return;
            }
            if (request.method() === 'GET' && recordID) {
                state.collaborationDetails.push(url);
                const ordinal = Number(recordID.split('-').at(-1) ?? '1');
                const base =
                    kind === 'runs'
                        ? {
                            ...runItem(projectKey, ordinal),
                            model_provider: '受管模型',
                            model_name: '企业推理模型',
                            prompt_version: 'prompt-v3',
                            toolset_version: 'tools-v2',
                            policy_version: 'policy-v5',
                            input_summary: '已脱敏输入摘要',
                            output_summary: '已脱敏输出摘要',
                            prompt_tokens: 120,
                            completion_tokens: 80,
                            cost_micros: 450,
                            started_at: timestamp,
                        }
                        : kind === 'approvals'
                            ? {
                                ...approvalItem(projectKey, ordinal),
                                approvals_recorded: 0,
                                rejections_recorded: 0,
                            }
                            : kind === 'proposals'
                                ? {
                                    ...proposalItem(projectKey, ordinal),
                                    preview: { fields: ['assigned_to_id'] },
                                }
                                : {
                                    ...handoffItem(projectKey, ordinal),
                                    reason: '需要人工判断',
                                    completed_summary: '已完成资料收集',
                                    missing_information: ['客户确认'],
                                };
                await fulfillJSON(route, {
                    code: 0,
                    msg: 'ok',
                    data: base,
                });
                return;
            }
            if (request.method() === 'GET') {
                state.collaborationLists.push(url);
                if (
                    options.failFirstCollaboration
                    && collaborationFailures++ === 0
                ) {
                    await fulfillJSON(
                        route,
                        {
                            code: 'service_unavailable',
                            detail: 'AI 协作列表暂时不可用',
                        },
                        503,
                    );
                    return;
                }
                const { page: requestedPage, pageSize } =
                    pageParameters(url);
                if (
                    options.holdOpsCollaborationSecondPage
                    && projectKey === projectA.key
                    && requestedPage === 2
                ) {
                    await collaborationGate;
                }
                const ordinals = ordinalRange(requestedPage, pageSize);
                const items =
                    kind === 'runs'
                        ? ordinals.map((ordinal) =>
                            runItem(projectKey, ordinal))
                        : kind === 'approvals'
                            ? ordinals.map((ordinal) =>
                                approvalItem(projectKey, ordinal))
                            : kind === 'proposals'
                                ? ordinals.map((ordinal) =>
                                    proposalItem(projectKey, ordinal))
                                : ordinals.map((ordinal) =>
                                    handoffItem(projectKey, ordinal));
                try {
                    await fulfillJSON(route, {
                        code: 0,
                        msg: 'ok',
                        data: directory(
                            items,
                            requestedPage,
                            pageSize,
                        ),
                    });
                } catch {
                    // 项目切换会主动取消旧项目的有界列表请求。
                }
                return;
            }
        }

        if (resource === 'knowledge/index-rebuilds/current') {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    id: `${projectKey}-index`,
                    index_name: `knowledge-${projectKey.toLowerCase()}`,
                    generation: 8,
                    desired_generation: 8,
                    status: 'ready',
                    source_digest: 'b'.repeat(64),
                    document_count: 10_000,
                    completed_at: timestamp,
                    updated_at: timestamp,
                },
            });
            return;
        }
        if (
            resource === 'knowledge/articles'
            && request.method() === 'GET'
        ) {
            state.knowledgeLists.push(url);
            if (options.failFirstKnowledge && knowledgeFailures++ === 0) {
                await fulfillJSON(
                    route,
                    {
                        code: 'service_unavailable',
                        detail: '知识目录暂时不可用',
                    },
                    503,
                );
                return;
            }
            const { page: requestedPage, pageSize } = pageParameters(url);
            if (
                options.holdOpsKnowledgeSecondPage
                && projectKey === projectA.key
                && requestedPage === 2
            ) {
                await knowledgeGate;
            }
            const items = ordinalRange(requestedPage, pageSize).map(
                (ordinal) => articleItem(projectKey, ordinal),
            );
            try {
                await fulfillJSON(route, {
                    code: 0,
                    msg: 'ok',
                    data: directory(items, requestedPage, pageSize),
                });
            } catch {
                // 项目切换会主动取消旧项目的有界列表请求。
            }
            return;
        }
        if (
            resource === 'knowledge/ingestions'
            && request.method() === 'GET'
        ) {
            state.knowledgeLists.push(url);
            const { page: requestedPage, pageSize } = pageParameters(url);
            const items = ordinalRange(requestedPage, pageSize).map(
                (ordinal) => ingestionItem(projectKey, ordinal),
            );
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: directory(items, requestedPage, pageSize),
            });
            return;
        }
        const versionsMatch = resource.match(
            /^knowledge\/articles\/([^/]+)\/versions$/u,
        );
        if (versionsMatch && request.method() === 'GET') {
            state.knowledgeVersions.push(url);
            const { page: requestedPage, pageSize } = pageParameters(url);
            const items = ordinalRange(requestedPage, pageSize).map(
                (ordinal) => versionItem(
                    projectKey,
                    versionsMatch[1],
                    ordinal,
                ),
            );
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: directory(items, requestedPage, pageSize),
            });
            return;
        }

        if (resource === `tickets/${ticketID}`) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    ...ticket,
                    project_id:
                        projectKey === projectA.key
                            ? projectA.id
                            : projectB.id,
                    ticket_number: `${projectKey}-${ticketID}`,
                },
            });
            return;
        }
        const entityLinksMatch = resource.match(
            new RegExp(`^tickets/${ticketID}/entity-links$`, 'u'),
        );
        if (entityLinksMatch && request.method() === 'GET') {
            state.relationshipLists.push(url);
            if (
                options.failFirstEntityLinks
                && entityLinkFailures++ === 0
            ) {
                await fulfillJSON(
                    route,
                    {
                        code: 'service_unavailable',
                        detail: '实体关联暂时不可用',
                    },
                    503,
                );
                return;
            }
            const { page: requestedPage, pageSize } = pageParameters(url);
            const items = ordinalRange(requestedPage, pageSize).map(
                (ordinal) => entityLinkItem(projectKey, ordinal),
            );
            await fulfillJSON(route, {
                success: true,
                data: {
                    ...directory(items, requestedPage, pageSize),
                    ticket_version: 7,
                },
            });
            return;
        }
        const relationsMatch = resource.match(
            new RegExp(`^tickets/${ticketID}/relations$`, 'u'),
        );
        if (relationsMatch && request.method() === 'GET') {
            state.relationshipLists.push(url);
            const { page: requestedPage, pageSize } = pageParameters(url);
            const items = ordinalRange(requestedPage, pageSize).map(
                (ordinal) => relationItem(projectKey, ordinal),
            );
            await fulfillJSON(route, {
                success: true,
                data: {
                    ...directory(items, requestedPage, pageSize),
                    ticket_version: 7,
                },
            });
            return;
        }

        if (resource === 'tickets/stats') {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
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
        await fulfillJSON(route, { code: 0, msg: 'ok', data: [] });
    });

    return state;
};

const expectDefaultPageRequest = (
    url: URL | undefined,
    pathSuffix: string,
) => {
    expect(url?.pathname).toContain(pathSuffix);
    expect(url?.searchParams.get('page')).toBe('1');
    expect(url?.searchParams.get('page_size')).toBe('25');
};

const selectProject = async (page: Page, name: RegExp) => {
    const switcher = page.getByTestId('active-project-switcher');
    await switcher.scrollIntoViewIfNeeded();
    await switcher.click();
    await page.getByRole('option', { name }).click();
};

const switchProjectBySessionContract = async (
    page: Page,
    projectKey: string,
) => {
    await page.evaluate((nextProjectKey) => {
        const storageKey = 'chronodesk.activeProject';
        const raw = sessionStorage.getItem(storageKey);
        if (!raw) throw new Error('缺少当前项目会话绑定');
        const binding = JSON.parse(raw) as {
            subject: string;
            session_id: string;
            project_key: string;
        };
        sessionStorage.setItem(
            storageKey,
            JSON.stringify({
                ...binding,
                project_key: nextProjectKey,
            }),
        );
        window.dispatchEvent(
            new CustomEvent('chronodesk:project-scope-changed', {
                detail: { project_key: nextProjectKey },
            }),
        );
        window.location.hash = '#/';
    }, projectKey);
};

test.describe('第一阶段关键体验（mock）', () => {
    test('AI 人机协作使用服务端分页、键盘详情、清晰多选并取消旧项目请求', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        const state = await installPhaseOneBackend(page, {
            holdOpsCollaborationSecondPage: true,
        });

        await page.goto('/#/agent-collaboration');
        const runsTable = page.getByRole('table', {
            name: 'Agent 运行列表',
            exact: true,
        });
        await expect(runsTable).toBeVisible();
        expectDefaultPageRequest(
            state.collaborationLists.at(-1),
            '/projects/OPS/agent-collaboration/runs',
        );
        await expect(runsTable.getByRole('row')).toHaveCount(26);

        const firstRunRow = runsTable.getByRole('row').nth(1);
        await firstRunRow.focus();
        await expect(firstRunRow).toBeFocused();
        await firstRunRow.press('Enter');
        await expect(
            page.getByRole('heading', {
                name: 'Agent 运行详情',
                exact: true,
            }),
        ).toBeVisible();
        await expect(page.getByText('已脱敏输入摘要')).toBeVisible();
        expect(state.collaborationDetails.at(-1)?.pathname).toContain(
            '/agent-collaboration/runs/OPS-run-1',
        );
        await page.keyboard.press('Escape');

        await firstRunRow
            .getByRole('button', {
                name: '人工接管 Agent 运行',
                exact: true,
            })
            .click();
        const missingInformation = page.getByRole('combobox', {
            name: '逐项添加仍缺失的信息',
            exact: true,
        });
        for (const value of ['客户确认', '影响范围', '回滚窗口', '审批人', '值班组']) {
            await missingInformation.fill(value);
            await missingInformation.press('Enter');
        }
        await missingInformation.press('Tab');
        await expect(page.getByText(/已选择 5 项/u)).toBeVisible();
        await expect(page.getByText('+2', { exact: true })).toBeVisible();
        await page.getByRole('button', { name: '取消', exact: true }).click();

        await page.getByRole('tab', {
            name: '审批任务',
            exact: true,
        }).click();
        const approvalTable = page.getByRole('table', {
            name: '审批任务列表',
            exact: true,
        });
        await expect(approvalTable).toBeVisible();
        await approvalTable
            .getByRole('button', {
                name: '处理 Agent 行动审批',
                exact: true,
            })
            .first()
            .click();
        const decision = page.getByRole('combobox', {
            name: '选择审批决定',
            exact: true,
        });
        await decision.focus();
        await decision.press('ArrowDown');
        await expect(decision).toHaveAttribute('aria-expanded', 'true');
        await expect(
            page.getByRole('option', {
                name: '批准',
                exact: true,
            }),
        ).toHaveAttribute('aria-selected', 'true');
        await page.keyboard.press('Escape');
        await page.getByRole('button', { name: '取消', exact: true }).click();

        await page.getByRole('tab', {
            name: 'Agent 运行',
            exact: true,
        }).click();
        await page.getByRole('combobox', {
            name: '每页记录数',
            exact: true,
        }).click();
        await page.getByRole('option', { name: '100', exact: true }).click();
        await expect(runsTable.getByRole('row')).toHaveCount(101);
        expect(state.collaborationLists.at(-1)?.searchParams.get('page_size'))
            .toBe('100');

        await page.getByRole('button', {
            name: /下一页|next page/iu,
        }).click();
        await expect.poll(() =>
            state.collaborationLists.some((request) =>
                request.pathname.includes('/projects/OPS/')
                && request.searchParams.get('page') === '2',
            ),
        ).toBe(true);
        await switchProjectBySessionContract(page, projectB.key);
        state.releaseOpsCollaborationSecondPage();
        await expect(page).toHaveURL(/#\/$/u);
        await page.goto('/#/agent-collaboration');
        await expect(
            page.getByRole('link', {
                name: '#FIN-1 FIN 协作工单 1',
                exact: true,
            }),
        ).toBeVisible();
        await expect(
            page.getByRole('table', {
                name: 'Agent 运行列表',
                exact: true,
            }).getByRole('link', { name: /OPS 协作工单/u }),
        ).toHaveCount(0);
        expect(state.collaborationLists.at(-1)?.pathname).toContain(
            '/projects/FIN/',
        );
        health.assertClean();
    });

    test('AI 人机协作错误状态可重试且重试仍使用默认有界契约', async ({
        page,
    }) => {
        const state = await installPhaseOneBackend(page, {
            failFirstCollaboration: true,
        });
        await page.goto('/#/agent-collaboration');
        const alert = page.getByRole('alert');
        await expect(alert).toContainText('安全执行保护暂时不可用');
        await alert.getByRole('button', { name: '重试', exact: true }).click();
        await expect(
            page.getByRole('link', {
                name: '#OPS-1 OPS 协作工单 1',
                exact: true,
            }),
        ).toBeVisible();
        expect(state.collaborationLists).toHaveLength(2);
        expectDefaultPageRequest(
            state.collaborationLists.at(-1),
            '/projects/OPS/agent-collaboration/runs',
        );
    });

    test('知识文章和版本独立分页且大目录不堆积 DOM', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        const state = await installPhaseOneBackend(page);
        await page.goto('/#/project-settings/knowledge');
        await expect(page).toHaveURL(/#\/knowledge$/u);

        const articleTable = page.getByRole('table', {
            name: '知识文章列表',
            exact: true,
        });
        await expect(articleTable).toBeVisible();
        expectDefaultPageRequest(
            state.knowledgeLists.at(-1),
            '/projects/OPS/knowledge/articles',
        );
        expect(state.knowledgeLists.at(-1)?.searchParams.get('sort_by'))
            .toBe('updated_at');
        await expect(articleTable.getByRole('row')).toHaveCount(26);

        await page.getByRole('combobox', {
            name: '每页记录数',
            exact: true,
        }).click();
        await page.getByRole('option', { name: '100', exact: true }).click();
        await expect(articleTable.getByRole('row')).toHaveCount(101);
        expect(state.knowledgeLists.at(-1)?.searchParams.get('page_size'))
            .toBe('100');

        await page.getByRole('tab', {
            name: '管理',
            exact: true,
        }).click();
        const firstArticleRow = articleTable.getByRole('row').filter({
            has: page.getByText('OPS 知识文章 1', {
                exact: true,
            }),
        });
        await firstArticleRow.getByRole('button', {
            name: '版本',
            exact: true,
        }).click();
        const versionTable = page.getByRole('table', {
            name: '知识文章版本列表',
            exact: true,
        });
        await expect(versionTable).toBeVisible();
        expectDefaultPageRequest(
            state.knowledgeVersions.at(-1),
            '/knowledge/articles/OPS-article-1/versions',
        );
        await expect(versionTable.getByRole('row')).toHaveCount(26);
        await expect(versionTable.getByText('Agent 草稿').first())
            .toBeVisible();
        await expect(versionTable.getByText('人工维护').first())
            .toBeVisible();
        const versionDrawer = page.locator('.MuiDrawer-paper').filter({
            has: page.getByRole('heading', {
                name: 'OPS 知识文章 1 · 版本管理',
                exact: true,
            }),
        });
        await versionDrawer
            .getByRole('button', { name: /下一页|next page/iu })
            .click();
        await expect.poll(() =>
            state.knowledgeVersions.at(-1)?.searchParams.get('page'),
        ).toBe('2');
        await expect(versionTable.getByText('OPS 知识版本 26')).toBeVisible();
        await page.keyboard.press('Escape');
        health.assertClean();
    });

    test('知识目录错误可重试且项目切换不会回填旧项目第二页', async ({
        page,
    }) => {
        const state = await installPhaseOneBackend(page, {
            failFirstKnowledge: true,
            holdOpsKnowledgeSecondPage: true,
        });
        await page.goto('/#/knowledge');
        const alert = page.getByRole('alert');
        await expect(alert).toContainText('安全执行保护暂时不可用');
        await alert.getByRole('button', { name: '重试', exact: true }).click();
        await expect(
            page.getByLabel('OPS 知识文章 1', { exact: true }),
        ).toBeVisible();

        await page.goto(
            '/#/knowledge?tab=browse&page=2&page_size=25',
        );
        await expect.poll(() =>
            state.knowledgeLists.some((request) =>
                request.pathname.includes('/projects/OPS/')
                && request.searchParams.get('page') === '2',
            ),
        ).toBe(true);
        await selectProject(page, /财务服务.*项目管理员/u);
        state.releaseOpsKnowledgeSecondPage();
        await expect(page).toHaveURL(/#\/$/u);
        await page.goto('/#/knowledge');
        await expect(
            page.getByLabel('FIN 知识文章 1', { exact: true }),
        ).toBeVisible();
        await expect(
            page.getByRole('table', {
                name: '知识文章列表',
                exact: true,
            }).getByLabel(/OPS 知识文章/u),
        ).toHaveCount(0);
        expect(state.knowledgeLists.at(-1)?.pathname).toContain(
            '/projects/FIN/',
        );
    });

    test('工单关联关系默认 25 条、错误可重试且两个目录分别翻页', async ({
        page,
    }) => {
        const state = await installPhaseOneBackend(page, {
            failFirstEntityLinks: true,
        });
        await page.goto(`/#/tickets/${ticketID}/show`);
        await expect(
            page.getByRole('heading', {
                name: ticket.title,
                exact: true,
            }),
        ).toBeVisible();
        await page.getByRole('tab', {
            name: '关联关系',
            exact: true,
        }).click();

        const entityAlert = page.getByRole('alert').filter({
            hasText: '安全执行保护暂时不可用',
        });
        await expect(entityAlert).toBeVisible();
        await entityAlert
            .getByRole('button', { name: '重试', exact: true })
            .click();
        const entityTable = page.getByRole('table', {
            name: '工单实体关联列表',
            exact: true,
        });
        await expect(entityTable.getByRole('row')).toHaveCount(26);
        const entityRequest = state.relationshipLists.findLast((request) =>
            request.pathname.endsWith('/entity-links'),
        );
        expectDefaultPageRequest(entityRequest, '/entity-links');
        expect(entityRequest?.searchParams.get('sort_by')).toBe('created_at');
        expect(entityRequest?.searchParams.get('sort_order')).toBe('desc');

        await page.getByRole('button', {
            name: /下一页|next page/iu,
        }).click();
        await expect(entityTable.getByText('OPS 关联资产 26')).toBeVisible();
        expect(
            state.relationshipLists
                .filter((request) =>
                    request.pathname.endsWith('/entity-links'))
                .at(-1)
                ?.searchParams.get('page'),
        ).toBe('2');

        await page.getByRole('combobox', {
            name: '每页记录数',
            exact: true,
        }).click();
        await page.getByRole('option', { name: '100', exact: true }).click();
        await expect(entityTable.getByRole('row')).toHaveCount(101);

        const relationsTab = page.getByRole('tab', {
            name: /工单关系/u,
        });
        await relationsTab.click();
        await expect(relationsTab).toHaveAttribute('aria-selected', 'true');
        const relationTable = page.getByRole('table', {
            name: '工单关系列表',
            exact: true,
        });
        await expect(relationTable.getByRole('row')).toHaveCount(26);
        const relationRequest = state.relationshipLists.findLast((request) =>
            request.pathname.endsWith('/relations'),
        );
        expectDefaultPageRequest(relationRequest, '/relations');
        await page.getByRole('button', {
            name: /下一页|next page/iu,
        }).click();
        await expect(relationTable.getByText('OPS 关联工单 26')).toBeVisible();
        expect(
            state.relationshipLists
                .filter((request) =>
                    request.pathname.endsWith('/relations'))
                .at(-1)
                ?.searchParams.get('page'),
        ).toBe('2');
    });

    test('manager 获得完整项目设置树、建单发布快照及有界队列目录', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        const state = await installPhaseOneBackend(page, {
            projectRole: 'manager',
        });
        await page.goto('/#/');

        const projectSettings = page.getByRole('menuitem', {
            name: /^项目设置/u,
        });
        await expect(projectSettings).toBeVisible();
        if (
            (await projectSettings.getAttribute('aria-expanded')) !== 'true'
        ) {
            await projectSettings.click();
        }
        const projectSettingsGroup = page.getByRole('group', {
            name: '项目设置导航',
            exact: true,
        });
        const expectedChildren = [
            '基本信息',
            '项目成员',
            '建单配置',
            'SLA 策略',
            '受理队列',
            '工单模板',
            '快捷回复',
            '通知与外发',
        ];
        await expect(
            projectSettingsGroup.getByRole('menuitem'),
        ).toHaveCount(expectedChildren.length);
        for (const child of expectedChildren) {
            await expect(
                projectSettingsGroup.getByRole('menuitem', {
                    name: child,
                    exact: true,
                }),
            ).toBeVisible();
        }

        await projectSettingsGroup.getByRole('menuitem', {
            name: '基本信息',
            exact: true,
        }).click();
        await expect(page).toHaveURL(/#\/project-settings\/basic$/u);
        await expect(
            page.getByRole('main').getByRole('heading', {
                name: '基本信息',
                exact: true,
            }),
        ).toBeVisible();
        await expect(page.getByText(projectA.key, { exact: true }))
            .toBeVisible();

        await projectSettingsGroup.getByRole('menuitem', {
            name: '通知与外发',
            exact: true,
        }).click();
        await expect(page).toHaveURL(
            /#\/project-settings\/notifications$/u,
        );
        await expect(
            page.getByRole('main').getByRole('heading', {
                name: '通知与外发',
                exact: true,
            }),
        ).toBeVisible();
        await expect(
            page.getByText(/邮件服务器属于系统级公共配置/u),
        ).toBeVisible();

        await projectSettingsGroup.getByRole('menuitem', {
            name: '建单配置',
            exact: true,
        }).click();
        await expect(page).toHaveURL(/#\/project-settings\/intake$/u);
        await expect(
            page.getByRole('main').getByRole('heading', {
                name: '建单配置',
                exact: true,
            }),
        ).toBeVisible();
        await expect(page.getByText('Release v7', { exact: true }))
            .toBeVisible();
        await expect(
            page.getByRole('table', {
                name: '已发布请求类型',
                exact: true,
            }).getByText('事件受理', { exact: true }),
        ).toBeVisible();
        await expect(
            page.getByRole('table', {
                name: '已发布工作流',
                exact: true,
            }).getByText('标准处理流程', { exact: true }),
        ).toBeVisible();
        expect(
            state.projectSettingsRequests.at(-1)?.pathname,
        ).toContain('/projects/OPS/configuration/intake');

        await page.goto('/#/project-settings/queues');
        const queueTable = page.getByRole('table', {
            name: '项目队列列表',
            exact: true,
        });
        await expect(queueTable).toBeVisible();
        const firstQueueRequest = state.projectSettingsRequests.find(
            (request) => request.pathname.endsWith('/queues'),
        );
        expectDefaultPageRequest(firstQueueRequest, '/projects/OPS/queues');
        expect(firstQueueRequest?.searchParams.get('sort_by'))
            .toBe('is_default');
        expect(firstQueueRequest?.searchParams.get('sort_order')).toBe('desc');
        await expect(queueTable.getByRole('row')).toHaveCount(26);

        await page.getByRole('button', {
            name: /下一页|next page/iu,
        }).click();
        await expect(
            queueTable.getByText('OPS 受理队列 26', { exact: true }),
        ).toBeVisible();
        expect(
            state.projectSettingsRequests
                .filter((request) => request.pathname.endsWith('/queues'))
                .at(-1)
                ?.searchParams.get('page'),
        ).toBe('2');

        await page.getByRole('combobox', {
            name: '每页数量',
            exact: true,
        }).click();
        await page.getByRole('option', { name: '100', exact: true }).click();
        await expect(queueTable.getByRole('row')).toHaveCount(101);
        const hundredRequest = state.projectSettingsRequests
            .filter((request) => request.pathname.endsWith('/queues'))
            .at(-1);
        expect(hundredRequest?.searchParams.get('page')).toBe('1');
        expect(hundredRequest?.searchParams.get('page_size')).toBe('100');
        health.assertClean();
    });

    test('项目设置旧路径受保护重定向且 agent 无入口也不发设置请求', async ({
        page,
        browser,
    }) => {
        const managerState = await installPhaseOneBackend(page, {
            projectRole: 'manager',
        });
        const redirects = [
            [
                '/automation-sla',
                '/project-settings/sla',
                'SLA 配置',
                '/admin/automation/sla',
            ],
            [
                '/automation-templates',
                '/project-settings/templates',
                '工单模板',
                '/admin/automation/templates',
            ],
            [
                '/automation-quick-replies',
                '/project-settings/quick-replies',
                '快捷回复',
                '/admin/automation/quick-replies',
            ],
        ] as const;
        for (
            const [
                legacyPath,
                canonicalPath,
                heading,
                endpoint,
            ] of redirects
        ) {
            await page.goto(`/#${legacyPath}`);
            await expect(page).toHaveURL(
                new RegExp(`#${canonicalPath.replaceAll('/', '\\/')}$`, 'u'),
            );
            await expect(
                page.getByRole('main').getByRole('heading', {
                    name: heading,
                    exact: true,
                }),
            ).toBeVisible();
            await expect.poll(() =>
                managerState.projectSettingsRequests.some((request) =>
                    request.pathname.endsWith(endpoint),
                ),
            ).toBe(true);
        }

        // A browser context represents one employee browser session. Installing
        // another identity in a sibling tab must now trigger the cross-tab
        // session-replacement guard, so the independent agent persona belongs
        // in its own context.
        const agentContext = await browser.newContext();
        const agentPage = await agentContext.newPage();
        const agentState = await installPhaseOneBackend(agentPage, {
            projectRole: 'agent',
        });
        await agentPage.goto('/#/');
        await expect(
            agentPage.getByRole('menuitem', { name: /^项目设置/u }),
        ).toHaveCount(0);
        for (const child of [
            '基本信息',
            '项目成员',
            '建单配置',
            'SLA 策略',
            '受理队列',
            '工单模板',
            '快捷回复',
            '通知与外发',
        ]) {
            await expect(
                agentPage.getByRole('menuitem', {
                    name: child,
                    exact: true,
                }),
            ).toHaveCount(0);
        }
        for (const guardedPath of [
            '/project-settings/basic',
            '/project-memberships',
            '/project-settings/intake',
            '/project-settings/sla',
            '/project-settings/queues',
            '/project-settings/templates',
            '/project-settings/quick-replies',
            '/project-settings/notifications',
        ]) {
            await agentPage.goto(`/#${guardedPath}`);
            await expect(agentPage).toHaveURL(/#\/?$/u);
            await expect(
                agentPage.getByTestId('project-home'),
            ).toBeVisible();
        }
        await agentPage.goto('/#/project-settings/knowledge');
        await expect(agentPage).toHaveURL(/#\/knowledge$/u);
        await expect(
            agentPage.getByTestId('knowledge-library-page'),
        ).toBeVisible();
        expect(agentState.projectSettingsRequests).toEqual([]);
        await agentContext.close();
    });
});
