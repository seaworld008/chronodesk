import { expect, test, type Page } from '@playwright/test';
import { monitorBrowserHealth } from './helpers/browserAudit';
import {
    authorizedProjectAccess,
    defaultMockIdentity,
    fulfillJSON,
    installMockSession,
    projectA,
} from './helpers/mockHumanSession';

const timestamp = '2026-08-01T08:00:00Z';
const articleID = 'knowledge-article-1';
const versionID = 'knowledge-version-3';
const sourceTicketID = 9001;

const article = {
    id: articleID,
    key: 'database-pool-recovery',
    title: '数据库连接池排障',
    summary: '连接池耗尽时的定位、恢复与验证步骤。',
    status: 'active',
    current_version_id: versionID,
    revision: 3,
    created_at: timestamp,
    updated_at: timestamp,
};

const version = {
    id: versionID,
    article_id: articleID,
    version: 3,
    status: 'published',
    created_by_type: 'human',
    title: article.title,
    mime_type: 'text/markdown',
    size_bytes: 2048,
    content_hash: 'a'.repeat(64),
    virus_scan: 'clean',
    scanned_at: timestamp,
    published_at: timestamp,
    created_at: timestamp,
    updated_at: timestamp,
};

type KnowledgeMockState = {
    draftBodies: Array<Record<string, unknown>>;
    versionDraftBodies: Array<Record<string, unknown>>;
    documentRequests: URL[];
    articleListRequests: URL[];
    attachmentListRequests: URL[];
    attachmentsShrunk: boolean;
    createdDraftArticle?: Record<string, unknown>;
};

const installKnowledgeBackend = async (
    page: Page,
    projectRole:
        | 'project_admin'
        | 'manager'
        | 'agent'
        | 'requester'
        | 'observer',
    canCreateKnowledgeDrafts =
        projectRole === 'project_admin' || projectRole === 'manager',
): Promise<KnowledgeMockState> => {
    const identity = {
        ...defaultMockIdentity,
        sessionID: `knowledge-${projectRole}-${Date.now()}-${Math.random()}`,
    };
    await installMockSession(page, identity, projectA);
    const access = authorizedProjectAccess(
        projectA,
        projectRole,
        canCreateKnowledgeDrafts,
    );
    const state: KnowledgeMockState = {
        draftBodies: [],
        versionDraftBodies: [],
        documentRequests: [],
        articleListRequests: [],
        attachmentListRequests: [],
        attachmentsShrunk: false,
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
                    id: identity.id,
                    username: 'knowledge-reviewer',
                    email: identity.email,
                    platform_role: identity.platformRole,
                    status: 'active',
                    email_verified: true,
                    otp_enabled: false,
                },
            });
            return;
        }
        if (pathname === '/api/projects') {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: [access],
            });
            return;
        }
        if (pathname === `/api/projects/${projectA.key}/context`) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: access,
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
        if (
            pathname === `/api/projects/${projectA.key}/tickets`
            && request.method() === 'GET'
        ) {
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    items: [{
                        id: sourceTicketID,
                        ticket_number: 'OPS-9001',
                        title: '数据库连接池耗尽',
                    }],
                    total: 1,
                    page: 1,
                    page_size: 25,
                    total_pages: 1,
                },
            });
            return;
        }
        if (
            pathname
                === `/api/projects/${projectA.key}/tickets/${sourceTicketID}/attachments`
            && request.method() === 'GET'
        ) {
            state.attachmentListRequests.push(url);
            const requestedPage = Number(
                url.searchParams.get('page') ?? '1',
            );
            const requestedPageSize = Number(
                url.searchParams.get('page_size') ?? '25',
            );
            const pageOneAttachments = Array.from(
                { length: 25 },
                (_, index) => ({
                    id: index === 0 ? 71 : 100 + index,
                    ticket_id: sourceTicketID,
                    original_name: index === 0
                        ? '连接池监控.md'
                        : index === 24
                          ? '未通过扫描.exe'
                          : `排障附件-${index + 1}.md`,
                    file_name: index === 0
                        ? 'pool-monitor.md'
                        : `troubleshooting-${index + 1}.md`,
                    file_size: 1024 + index,
                    mime_type: index === 24
                        ? 'application/octet-stream'
                        : 'text/markdown',
                    file_type: index === 24 ? 'other' : 'document',
                    extension: index === 24 ? 'exe' : 'md',
                    is_public: true,
                    hash: String(index + 1).padStart(64, 'b'),
                    virus_scan: index === 24 ? 'infected' : 'clean',
                    created_at: timestamp,
                    updated_at: timestamp,
                }),
            );
            const pageTwoAttachments = [{
                id: 72,
                ticket_id: sourceTicketID,
                original_name: '连接池恢复步骤.md',
                file_name: 'pool-recovery.md',
                file_size: 2048,
                mime_type: 'text/markdown',
                file_type: 'document',
                extension: 'md',
                is_public: true,
                hash: 'c'.repeat(64),
                virus_scan: 'clean',
                created_at: timestamp,
                updated_at: timestamp,
            }];
            const responseItems = state.attachmentsShrunk
                ? requestedPage === 1
                    ? pageOneAttachments.slice(0, 24)
                    : []
                : requestedPage === 2
                  ? pageTwoAttachments
                  : pageOneAttachments;
            await fulfillJSON(route, {
                success: true,
                data: responseItems,
                total: state.attachmentsShrunk ? 24 : 26,
                page: requestedPage,
                page_size: requestedPageSize,
                total_pages: state.attachmentsShrunk ? 1 : 2,
            });
            return;
        }

        const articlesPath =
            `/api/projects/${projectA.key}/knowledge/articles`;
        if (pathname === articlesPath && request.method() === 'GET') {
            state.articleListRequests.push(url);
            const items = url.searchParams.get('view') === 'mine'
                ? state.createdDraftArticle
                    ? [state.createdDraftArticle]
                    : []
                : [article];
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    items,
                    total: items.length,
                    page: Number(url.searchParams.get('page') ?? '1'),
                    page_size: Number(
                        url.searchParams.get('page_size') ?? '25',
                    ),
                    total_pages: 1,
                },
            });
            return;
        }
        if (pathname === articlesPath && request.method() === 'POST') {
            const draftBody =
                request.postDataJSON() as Record<string, unknown>;
            state.draftBodies.push(draftBody);
            const createdDraftArticle = {
                ...article,
                id: 'knowledge-article-draft',
                key: String(draftBody.key ?? 'ticket-9001'),
                title: String(
                    draftBody.title ?? '从工单沉淀的连接池知识',
                ),
                summary: String(
                    draftBody.summary ?? '由工单整理，等待独立发布。',
                ),
                current_version_id: null,
                revision: 1,
                has_unpublished_draft: true,
                latest_draft_at: timestamp,
                latest_draft_version: 1,
            };
            state.createdDraftArticle = createdDraftArticle;
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    article: createdDraftArticle,
                    version: {
                        ...version,
                        id: 'knowledge-version-draft',
                        article_id: 'knowledge-article-draft',
                        version: 1,
                        status: 'draft',
                        title: createdDraftArticle.title,
                        published_at: null,
                    },
                    sources: [{
                        ordinal: 1,
                        kind: 'ticket',
                        visibility: 'full',
                        reference_label: '工单 OPS-9001',
                        source_ticket_id: sourceTicketID,
                        ticket_number: 'OPS-9001',
                        ticket_title: '数据库连接池耗尽',
                    }],
                    receipt: {
                        operation_id: 'knowledge-operation-1',
                        resource_id: 'knowledge-article-draft',
                        resource_version: 1,
                        event_id: 'knowledge-event-1',
                        changed_fields: ['article', 'version', 'sources'],
                    },
                },
            });
            return;
        }
        const draftPathMatch = pathname.match(
            new RegExp(`^${articlesPath}/([^/]+)/drafts$`, 'u'),
        );
        if (draftPathMatch && request.method() === 'POST') {
            state.versionDraftBodies.push(
                request.postDataJSON() as Record<string, unknown>,
            );
            const targetArticle = draftPathMatch[1] === articleID
                ? article
                : state.createdDraftArticle ?? article;
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    article: targetArticle,
                    version: {
                        ...version,
                        id: 'knowledge-version-4-draft',
                        version: 4,
                        status: 'draft',
                        published_at: null,
                    },
                    sources: [],
                    receipt: {
                        operation_id: 'knowledge-operation-version-4',
                        resource_id: articleID,
                        resource_version: 4,
                        event_id: 'knowledge-event-version-4',
                        changed_fields: ['version'],
                    },
                },
            });
            return;
        }

        const documentPathMatch = pathname.match(
            new RegExp(`^${articlesPath}/([^/]+)/document$`, 'u'),
        );
        if (documentPathMatch && request.method() === 'GET') {
            state.documentRequests.push(url);
            const isPersonalDraft =
                documentPathMatch[1] === 'knowledge-article-draft';
            const documentArticle = isPersonalDraft
                ? state.createdDraftArticle ?? article
                : article;
            const documentVersion = isPersonalDraft
                ? {
                    ...version,
                    id: 'knowledge-version-draft',
                    article_id: 'knowledge-article-draft',
                    version: 1,
                    status: 'draft',
                    title: documentArticle.title,
                    published_at: null,
                }
                : version;
            await fulfillJSON(route, {
                code: 0,
                msg: 'ok',
                data: {
                    article: documentArticle,
                    version: documentVersion,
                    markdown: isPersonalDraft
                        ? `# ${documentArticle.title}

## 最新草稿

这是普通贡献者刚提交、等待复核的最新草稿。`
                        : `# 数据库连接池排障

## 现象

连接请求持续超时，但数据库本身仍可访问。

## 解决步骤

1. 确认等待队列和活跃连接数。
2. 逐步恢复流量并观察错误率。

[官方说明](https://example.com/guide)

![不得加载的远程图片](https://example.invalid/remote.png)

<script>window.__knowledgeInjected = true</script>`,
                    sections: isPersonalDraft ? [{
                        ordinal: 1,
                        heading: '最新草稿',
                        level: 2,
                        markdown: '## 最新草稿\n\n这是普通贡献者刚提交、等待复核的最新草稿。',
                    }] : [
                        {
                            ordinal: 1,
                            heading: '现象',
                            level: 2,
                            markdown: '## 现象\n\n连接请求持续超时。',
                        },
                        {
                            ordinal: 2,
                            heading: '解决步骤',
                            level: 2,
                            markdown: '## 解决步骤\n\n1. 确认等待队列。',
                        },
                    ],
                    sources: isPersonalDraft ? [] : [
                        {
                            ordinal: 1,
                            kind: 'ticket',
                            visibility: 'full',
                            reference_label: '工单 OPS-9001',
                            source_ticket_id: sourceTicketID,
                            ticket_number: 'OPS-9001',
                            ticket_title: '数据库连接池耗尽',
                        },
                        {
                            ordinal: 2,
                            kind: 'attachment',
                            visibility: 'full',
                            reference_label: '附件 连接池监控.md',
                            source_ticket_id: sourceTicketID,
                            source_attachment_id: 71,
                            ticket_number: 'OPS-9001',
                            ticket_title: '数据库连接池耗尽',
                            attachment_name: '连接池监控.md',
                            attachment_hash: 'b'.repeat(64),
                        },
                        {
                            ordinal: 3,
                            kind: 'ticket',
                            visibility: 'restricted',
                            reference_label: '受限工单来源',
                        },
                        {
                            ordinal: 4,
                            kind: 'attachment',
                            visibility: 'unavailable',
                            reference_label: '附件来源已不可用',
                        },
                    ],
                },
            });
            return;
        }

        await fulfillJSON(route, { code: 0, msg: 'ok', data: [] });
    });

    return state;
};

test.describe('项目知识库（mock）', () => {
    test('所有项目角色都从项目运营进入知识库，普通角色只浏览和搜索', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        await installKnowledgeBackend(page, 'agent');

        await page.goto('/#/project-settings/knowledge');
        await expect(page).toHaveURL(/#\/knowledge$/u);
        await expect(page.getByTestId('knowledge-library-page')).toBeVisible();

        const projectOperations = page.getByRole('group', {
            name: '项目运营导航',
            exact: true,
        });
        await expect(
            projectOperations.getByRole('menuitem', {
                name: '知识库',
                exact: true,
            }),
        ).toHaveAttribute('aria-current', 'page');
        await expect(
            page.getByRole('tab', { name: '浏览', exact: true }),
        ).toBeVisible();
        await expect(
            page.getByRole('tab', { name: '搜索', exact: true }),
        ).toBeVisible();
        await expect(
            page.getByRole('tab', { name: '管理', exact: true }),
        ).toHaveCount(0);
        await expect(
            page.getByRole('button', { name: '沉淀知识', exact: true }),
        ).toHaveCount(0);
        health.assertClean();
    });

    test('普通项目成员获独立授权后可提交和修订自己的草稿，但不能发布', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        const state = await installKnowledgeBackend(page, 'agent', true);

        await page.goto('/#/knowledge');
        await expect(
            page.getByRole('tab', { name: '我维护的知识', exact: true }),
        ).toBeVisible();
        await expect(
            page.getByRole('tab', { name: '管理', exact: true }),
        ).toHaveCount(0);
        await page.getByRole('button', {
            name: '沉淀知识',
            exact: true,
        }).click();

        let dialog = page.getByRole('dialog', { name: '沉淀知识' });
        await dialog.getByLabel('文章 Key').fill('agent-pitfall');
        await dialog.getByLabel('标题').fill('普通成员提交的排障经验');
        await dialog.getByLabel('Markdown 正文').fill(`## 现象

某个边界配置会导致请求失败。

## 解决步骤

1. 核对配置。
2. 在测试环境验证。`);
        await dialog.getByRole('button', {
            name: '提交草稿待复核',
            exact: true,
        }).click();

        await expect.poll(() => state.draftBodies.length).toBe(1);
        expect(state.draftBodies[0]).toMatchObject({
            key: 'agent-pitfall',
            title: '普通成员提交的排障经验',
        });
        expect(state.draftBodies[0]).not.toHaveProperty(
            'grant_project_access',
        );
        await expect(dialog.getByText(/项目管理员或经理复核发布前/u))
            .toBeVisible();
        await expect(
            dialog.getByRole('button', { name: '发布', exact: true }),
        ).toHaveCount(0);
        await dialog.getByRole('button', {
            name: '完成',
            exact: true,
        }).click();

        await expect(page).toHaveURL(/tab=mine/u);
        await expect.poll(() =>
            state.articleListRequests.some(
                (request) => request.searchParams.get('view') === 'mine',
            ),
        ).toBe(true);
        const table = page.getByRole('table', {
            name: '知识文章列表',
            exact: true,
        });
        await expect(
            table.getByText('普通成员提交的排障经验'),
        ).toBeVisible();
        await expect(
            table.getByText('最新草稿活动', { exact: true }),
        ).toBeVisible();
        await expect(table.getByText('待复核', { exact: true })).toBeVisible();
        await table.getByRole('button', {
            name: '查看',
            exact: true,
        }).click();
        const draftDrawer = page.getByRole('dialog', {
            name: '普通成员提交的排障经验',
            exact: true,
        });
        await expect(
            draftDrawer.getByText(
                '这是普通贡献者刚提交、等待复核的最新草稿。',
            ),
        ).toBeVisible();
        await expect.poll(() => state.documentRequests.length).toBe(1);
        expect(
            state.documentRequests[0].searchParams.get(
                'prefer_latest_draft',
            ),
        ).toBe('true');
        await draftDrawer.getByRole('button', {
            name: '关闭知识正文',
            exact: true,
        }).click();

        await table.getByRole('button', {
            name: '新版本',
            exact: true,
        }).click();

        dialog = page.getByRole('dialog', {
            name: '更新知识：普通成员提交的排障经验',
            exact: true,
        });
        await dialog.getByLabel('Markdown 正文').fill(`## 补充

复核前补充一条验证步骤。`);
        await dialog.getByRole('button', {
            name: '保存新版本草稿',
            exact: true,
        }).click();
        await expect.poll(() => state.versionDraftBodies.length).toBe(1);
        await expect(
            dialog.getByRole('button', { name: '发布', exact: true }),
        ).toHaveCount(0);
        health.assertClean();
    });

    test('已归档知识在个人维护视图中明确只读', async ({ page }) => {
        const health = monitorBrowserHealth(page);
        const state = await installKnowledgeBackend(page, 'requester', true);
        state.createdDraftArticle = {
            ...article,
            id: 'knowledge-article-archived',
            key: 'archived-pitfall',
            title: '已归档排障经验',
            status: 'archived',
            current_version_id: null,
            has_unpublished_draft: true,
            latest_draft_at: timestamp,
            latest_draft_version: 2,
        };

        await page.goto('/#/knowledge?tab=mine');
        const table = page.getByRole('table', {
            name: '知识文章列表',
            exact: true,
        });
        await expect(table.getByText('已归档排障经验')).toBeVisible();
        await expect(
            table.getByRole('button', { name: '查看', exact: true }),
        ).toBeDisabled();
        await expect(
            table.getByRole('button', { name: '新版本', exact: true }),
        ).toBeDisabled();
        health.assertClean();
    });

    test('管理员可从工单原子保存草稿，正文按字节限流并安全查看来源', async ({
        page,
    }) => {
        const health = monitorBrowserHealth(page);
        const state = await installKnowledgeBackend(page, 'project_admin');

        await page.goto(
            `/#/knowledge?source_ticket_id=${sourceTicketID}&create=1`,
        );
        const dialog = page.getByRole('dialog', { name: '沉淀知识' });
        await expect(dialog).toBeVisible();
        await expect(
            dialog.getByRole('link', { name: `工单 #${sourceTicketID}` }),
        ).toHaveAttribute('href', `#/tickets/${sourceTicketID}/show`);

        await dialog.getByLabel('标题').fill('从工单沉淀的连接池知识');
        const markdownEditor = dialog.getByLabel('Markdown 正文');
        await markdownEditor.fill('知'.repeat(43_691));
        await expect(
            dialog.getByText(
                /正文为 131,073 字节，超过 131,072 字节（128 KiB）上限/u,
            ),
        ).toBeVisible();
        await expect(
            dialog.getByRole('button', { name: '保存草稿', exact: true }),
        ).toBeDisabled();

        await markdownEditor.fill(`## 现象

连接请求持续超时。

## 解决步骤

1. 核对连接池等待队列。
2. 逐步恢复流量。`);
        await expect(
            dialog.getByText(/UTF-8 \d+ \/ 131,072 字节（128 KiB）/u),
        ).toBeVisible();
        const sourceAttachments = dialog.getByRole('combobox', {
            name: '来源附件',
            exact: true,
        });
        await expect(sourceAttachments).toBeVisible();
        await expect.poll(() =>
            state.attachmentListRequests.length,
        ).toBeGreaterThan(0);
        expect(
            state.attachmentListRequests[0].searchParams.get('page'),
        ).toBe('1');
        expect(
            state.attachmentListRequests[0].searchParams.get('page_size'),
        ).toBe('25');
        await expect(
            dialog.getByText(
                '第 1/2 页 / 共 26 条附件 · 当前页显示 24 条扫描通过',
                { exact: true },
            ),
        ).toBeVisible();
        await sourceAttachments.click();
        await page.getByRole('option', {
            name: /连接池监控\.md/u,
        }).click();
        await expect(dialog.getByText('已选择 1 项，最多 20 项')).toBeVisible();
        await expect(
            page.getByRole('option', { name: /未通过扫描\.exe/u }),
        ).toHaveCount(0);
        await page.keyboard.press('Escape');
        await dialog.getByRole('button', {
            name: '下一页来源附件',
            exact: true,
        }).click();
        await expect.poll(() =>
            state.attachmentListRequests.some(
                (requestURL) =>
                    requestURL.searchParams.get('page') === '2',
            ),
        ).toBe(true);
        await expect(
            dialog.getByText(
                /第 2\/2 页 \/ 共 26 条附件 · 当前页显示 1\s*条扫描通过/u,
            ),
        ).toBeVisible();
        await sourceAttachments.click();
        await page.getByRole('option', {
            name: /连接池恢复步骤\.md/u,
        }).click();
        await expect(dialog.getByText('已选择 2 项，最多 20 项')).toBeVisible();
        await page.keyboard.press('Escape');
        state.attachmentsShrunk = true;
        await dialog.getByRole('button', {
            name: '刷新可用附件',
            exact: true,
        }).click();
        await expect.poll(() =>
            state.attachmentListRequests.filter(
                (requestURL) =>
                    requestURL.searchParams.get('page') === '1',
            ).length,
        ).toBeGreaterThan(1);
        await expect(
            dialog.getByText(
                /第 1\/1 页 \/ 共 24 条附件 · 当前页显示 24\s*条扫描通过/u,
            ),
        ).toBeVisible();
        await expect(dialog.getByText('已选择 2 项，最多 20 项')).toBeVisible();
        await dialog.getByRole('button', {
            name: '保存草稿',
            exact: true,
        }).click();

        await expect.poll(() => state.draftBodies.length).toBe(1);
        expect(state.draftBodies[0]).toMatchObject({
            key: 'ticket-9001',
            title: '从工单沉淀的连接池知识',
            source_ticket_id: sourceTicketID,
            source_attachment_ids: [71, 72],
        });
        expect(state.draftBodies[0]).not.toHaveProperty(
            'grant_project_access',
        );
        await expect(
            dialog.getByText(/草稿 v1 已保存/u),
        ).toBeVisible();
        await expect(
            dialog.getByRole('button', { name: '发布', exact: true }),
        ).toBeVisible();
        await dialog.getByRole('button', {
            name: '发布',
            exact: true,
        }).click();
        const publishConfirm = page.getByRole('dialog', {
            name: '确认发布知识',
            exact: true,
        });
        await expect(publishConfirm).toContainText(
            '对当前项目所有成员可见',
        );
        await expect(publishConfirm).toContainText('AI Agent');
        await publishConfirm.getByRole('button', {
            name: '取消',
            exact: true,
        }).click();
        await dialog.getByRole('button', {
            name: '稍后发布',
            exact: true,
        }).click();

        const articleTable = page.getByRole('table', {
            name: '知识文章列表',
            exact: true,
        });
        await articleTable.getByRole('button', {
            name: '查看',
            exact: true,
        }).click();
        const drawer = page.getByRole('dialog', {
            name: article.title,
            exact: true,
        });
        await expect(drawer).toBeVisible();
        await expect(
            drawer.getByRole('heading', {
                name: article.title,
                exact: true,
            }).first(),
        ).toBeVisible();
        await expect.poll(() => state.documentRequests.length).toBe(1);
        expect(
            state.documentRequests[0].searchParams.get('version_id'),
        ).toBeNull();

        const tableOfContents = page.getByRole('navigation', {
            name: '知识正文目录',
            exact: true,
        });
        await expect(
            tableOfContents.getByRole('button', {
                name: '现象',
                exact: true,
            }),
        ).toBeVisible();
        await expect(
            tableOfContents.getByRole('button', {
                name: '解决步骤',
                exact: true,
            }),
        ).toBeVisible();
        await expect(
            page.getByRole('link', {
                name: /OPS-9001 数据库连接池耗尽/u,
            }),
        ).toHaveAttribute('href', `#/tickets/${sourceTicketID}/show`);
        await expect(page.getByText('附件：连接池监控.md')).toBeVisible();
        await expect(
            page.getByTitle('当前账号无权查看该来源详情'),
        ).toContainText('受限工单来源');
        await expect(
            page.getByTitle('来源已删除、不可用或不再满足安全要求'),
        ).toContainText('附件来源已不可用');

        const safeMarkdown = page.getByTestId('safe-knowledge-markdown');
        await expect(safeMarkdown).toContainText('连接请求持续超时');
        await expect(
            safeMarkdown.getByRole('link', { name: '官方说明' }),
        ).toHaveAttribute('rel', 'noopener noreferrer nofollow');
        await expect(
            safeMarkdown.locator('img, script, iframe'),
        ).toHaveCount(0);
        expect(
            await page.evaluate(() =>
                (window as Window & { __knowledgeInjected?: boolean })
                    .__knowledgeInjected),
        ).toBeUndefined();
        health.assertClean();
    });

    test('管理员可从文章列表创建后续版本草稿', async ({ page }) => {
        const health = monitorBrowserHealth(page);
        const state = await installKnowledgeBackend(page, 'manager');

        await page.goto('/#/knowledge?tab=manage');
        const articleTable = page.getByRole('table', {
            name: '知识文章列表',
            exact: true,
        });
        await articleTable.getByRole('button', {
            name: '新版本',
            exact: true,
        }).click();

        const dialog = page.getByRole('dialog', {
            name: `更新知识：${article.title}`,
            exact: true,
        });
        await expect(dialog.getByLabel('文章 Key')).toBeDisabled();
        await expect(dialog.getByLabel('文章 Key')).toHaveValue(article.key);
        await dialog.getByLabel('来源工单（可选）').fill('OPS-9');
        await page.getByRole('option', {
            name: /OPS-9001 · 数据库连接池耗尽/u,
        }).click();
        const sourceAttachments = dialog.getByRole('combobox', {
            name: '来源附件',
            exact: true,
        });
        await expect(sourceAttachments).toBeVisible();
        await sourceAttachments.click();
        await page.getByRole('option', {
            name: /连接池监控\.md/u,
        }).click();
        await dialog.getByLabel('Markdown 正文').fill(`## 现象

连接池配置在新版本中发生变化。

## 解决步骤

1. 复核新参数。
2. 逐步恢复流量。`);
        await dialog.getByRole('button', {
            name: '保存新版本草稿',
            exact: true,
        }).click();

        await expect.poll(() => state.versionDraftBodies.length).toBe(1);
        expect(state.versionDraftBodies[0]).toMatchObject({
            title: article.title,
            source_ticket_id: sourceTicketID,
            source_attachment_ids: [71],
        });
        expect(state.versionDraftBodies[0]).not.toHaveProperty('key');
        expect(state.versionDraftBodies[0]).not.toHaveProperty('summary');
        await expect(dialog.getByText(/草稿 v4 已保存/u)).toBeVisible();
        health.assertClean();
    });
});
