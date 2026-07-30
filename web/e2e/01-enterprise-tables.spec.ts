import { Buffer } from 'node:buffer';
import {
    expect,
    test,
    type APIRequestContext,
    type Locator,
    type Page,
} from '@playwright/test';
import { apiRequest } from './helpers/api';
import {
    authenticatePage,
    cleanupE2EData,
    createAutomationRule,
    createNotification,
    DEFAULT_ADMIN,
    E2E_MARKER,
    extractData,
    extractItems,
    findUserByEmail,
    getAdminToken,
    projectAPIPath,
    resolveE2EProjectKey,
    resolveE2ETicketCreateConfiguration,
    trackE2EResource,
} from './helpers/testData';
import { waitForPrimaryPage } from './helpers/browserAudit';
import { assertDestructiveE2EAllowed } from './helpers/safety';

const ticketTitle = `${E2E_MARKER}企业表格工单`;
const agentTicketTitle = `${E2E_MARKER}企业表格租约工单`;
const notificationTitle = `${E2E_MARKER}企业表格通知`;
const ruleName = `${E2E_MARKER}企业表格规则`;
const webhookName = `${E2E_MARKER}企业表格 Webhook`;
const principalName = `${E2E_MARKER}企业表格智能体`;

type TicketFixture = {
    id: number;
    ticketNumber: string;
    version: number;
    projectKey: string;
};

type EnterpriseTableCase = {
    name: string;
    path: () => string;
    tableName: string;
    columnName: string;
    defaultWidth: number;
    expectedText?: () => string;
    tabName?: string;
    stickyHeader?: string;
    requireActionButton?: boolean;
    prepareSticky?: (table: Locator) => Promise<void>;
};

let adminUserID = 0;
let ticketFixture: TicketFixture = {
    id: 0,
    ticketNumber: '',
    version: 0,
    projectKey: '',
};
let agentTicketFixture: TicketFixture = {
    id: 0,
    ticketNumber: '',
    version: 0,
    projectKey: '',
};
let agentLeaseID = '';
let automationRuleID = 0;
let commandSequence = 0;

const escapeRegExp = (value: string) =>
    value.replace(/[.*+?^${}()|[\]\\]/gu, '\\$&');

const idempotencyKey = (operation: string) =>
    `e2e-enterprise-table-${process.pid}-${++commandSequence}-${operation}`;

const backendRoot = () => {
    const configured =
        process.env.TEST_API_BASE_URL?.trim() ||
        process.env.VITE_PROXY_TARGET?.trim() ||
        'http://localhost:8081';
    const parsed = new URL(configured);
    parsed.pathname = parsed.pathname.replace(/\/api\/?$/u, '').replace(/\/$/u, '');
    parsed.search = '';
    parsed.hash = '';
    return parsed.toString().replace(/\/$/u, '');
};

const createTableTicket = async (
    request: APIRequestContext,
    token: string,
    title: string,
    assignedToID?: number,
): Promise<TicketFixture> => {
    const projectKey = await resolveE2EProjectKey(request, token);
    const configuration = await resolveE2ETicketCreateConfiguration(
        request,
        token,
        projectKey,
    );
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/projects/${encodeURIComponent(projectKey)}/tickets`,
        {
            method: 'POST',
            headers: {
                'Idempotency-Key': idempotencyKey('create-ticket'),
            },
            data: {
                title,
                description: `${title} 自动化测试描述`,
                type: configuration.workClass,
                priority: 'normal',
                source: 'web',
                request_type_version_id:
                    configuration.requestTypeVersionID,
                workflow_version_id: configuration.workflowVersionID,
                ...(assignedToID
                    ? { assigned_to_id: assignedToID }
                    : {}),
            },
        },
    );
    const ticket = extractData<Record<string, unknown>>(response);
    expect(typeof ticket.id).toBe('number');
    expect(typeof ticket.ticket_number).toBe('string');
    expect(typeof ticket.version).toBe('number');
    trackE2EResource('tickets', ticket.id as number);
    return {
        id: ticket.id as number,
        ticketNumber: ticket.ticket_number as string,
        version: ticket.version as number,
        projectKey,
    };
};

const createWebhookFixture = async (
    request: APIRequestContext,
    token: string,
) => {
    const projectKey = await resolveE2EProjectKey(request, token);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(projectKey, 'webhooks'),
        {
            method: 'POST',
            data: {
                name: webhookName,
                description: '企业表格与 Outbox 可视化测试',
                provider: 'custom',
                webhook_url: 'https://chronodesk-enterprise-table.invalid/callback',
                enabled_events: ['io.chronodesk.ticket.created.v1'],
                filter_rules: { transition_statuses: [] },
                message_format: 'json',
                retry_count: 1,
                retry_interval: 60,
                timeout_seconds: 5,
                is_async: true,
                rate_limit: 10,
                rate_limit_window: 60,
                status: 'active',
            },
        },
    );
    const webhook = extractData<Record<string, unknown>>(response);
    expect(typeof webhook.id).toBe('number');
    trackE2EResource('webhooks', webhook.id as number);
};

const provisionAgentLease = async (
    request: APIRequestContext,
    adminToken: string,
    ticket: TicketFixture,
) => {
    const principalResponse = await apiRequest<Record<string, unknown>>(
        request,
        adminToken,
        projectAPIPath(
            ticket.projectKey,
            'admin/agents/service-principals',
        ),
        {
            method: 'POST',
            headers: {
                'Idempotency-Key': idempotencyKey('create-principal'),
            },
            data: {
                name: principalName,
                description: '企业表格真实租约与策略审计测试',
                scopes: ['tickets:read', 'tasks:manage'],
                rate_limit: 120,
                concurrency_limit: 2,
            },
        },
    );
    const principal = extractData<Record<string, unknown>>(principalResponse);
    expect(typeof principal.client_id).toBe('string');
    expect(typeof principal.client_secret).toBe('string');
    const clientID = principal.client_id as string;
    const clientSecret = principal.client_secret as string;
    expect(principal.project_key).toBe(ticket.projectKey);
    trackE2EResource('agentPrincipals', clientID);

    const root = backendRoot();
    const [resourceResponse, authorizationResponse] = await Promise.all([
        request.get(
            `${root}/.well-known/oauth-protected-resource/api/v2`,
        ),
        request.get(`${root}/.well-known/oauth-authorization-server`),
    ]);
    expect(resourceResponse.status(), '读取 Agent REST 资源元数据').toBe(200);
    expect(authorizationResponse.status(), '读取 OAuth 授权元数据').toBe(200);
    const resourceMetadata = (await resourceResponse.json()) as Record<
        string,
        unknown
    >;
    const authorizationMetadata =
        (await authorizationResponse.json()) as Record<string, unknown>;
    expect(typeof resourceMetadata.resource).toBe('string');
    expect(
        new URL(resourceMetadata.resource as string).pathname,
        'Agent REST audience 必须使用 v2',
    ).toBe('/api/v2');
    expect(typeof authorizationMetadata.token_endpoint).toBe('string');

    const tokenResponse = await request.post(
        authorizationMetadata.token_endpoint as string,
        {
            headers: {
                Authorization: `Basic ${Buffer.from(
                    `${clientID}:${clientSecret}`,
                ).toString('base64')}`,
            },
            form: {
                grant_type: 'client_credentials',
                resource: resourceMetadata.resource as string,
                scope: 'tickets:read tasks:manage',
                project_key: ticket.projectKey,
            },
        },
    );
    expect(tokenResponse.status(), '服务主体换取短期访问令牌').toBe(200);
    const tokenPayload = (await tokenResponse.json()) as Record<
        string,
        unknown
    >;
    expect(typeof tokenPayload.access_token).toBe('string');
    expect(tokenPayload.project_key).toBe(ticket.projectKey);

    const claimResponse = await request.post(
        `${root}/api/v2/projects/${encodeURIComponent(ticket.projectKey)}/tickets/${ticket.id}/claim`,
        {
            headers: {
                Authorization: `Bearer ${tokenPayload.access_token as string}`,
                'Content-Type': 'application/json',
                'If-Match': `"v${ticket.version}"`,
                'Idempotency-Key': idempotencyKey('claim-ticket'),
            },
            data: { ttl_seconds: 900 },
        },
    );
    expect(claimResponse.status(), '服务主体领取真实工单租约').toBe(200);
    const claimPayload = (await claimResponse.json()) as Record<
        string,
        unknown
    >;
    const lease = extractData<Record<string, unknown>>(claimPayload);
    expect(typeof lease.lease_id).toBe('string');
    agentLeaseID = lease.lease_id as string;
};

const releaseAgentLease = async (request: APIRequestContext) => {
    if (!agentLeaseID) {
        return;
    }
    const token = await getAdminToken(request);
    const overviewResponse = await apiRequest<Record<string, unknown>>(
        request,
        token,
        projectAPIPath(
            agentTicketFixture.projectKey,
            'admin/agents/agent-control/overview',
        ),
    );
    const overview = extractData<Record<string, unknown>>(overviewResponse);
    const leases = Array.isArray(overview.leases)
        ? (overview.leases as Array<Record<string, unknown>>)
        : [];
    const lease = leases.find((candidate) => candidate.id === agentLeaseID);
    if (!lease) {
        return;
    }
    expect(typeof lease.resource_version).toBe('number');
    await apiRequest(
        request,
        token,
        projectAPIPath(
            agentTicketFixture.projectKey,
            `admin/agents/leases/${agentLeaseID}/force-release`,
        ),
        {
            method: 'POST',
            headers: {
                'If-Match': `"v${lease.resource_version as number}"`,
                'Idempotency-Key': idempotencyKey('release-lease'),
            },
        },
    );
    agentLeaseID = '';
};

const tableCases = (): EnterpriseTableCase[] => [
    {
        name: '工单列表',
        path: () => '/#/tickets',
        tableName: '工单列表',
        columnName: '工单信息',
        defaultWidth: 260,
        expectedText: () => ticketTitle,
        stickyHeader: '操作',
        requireActionButton: true,
    },
    {
        name: '用户列表',
        path: () => '/#/users',
        tableName: '用户列表',
        columnName: '用户',
        defaultWidth: 260,
        expectedText: () => DEFAULT_ADMIN.email,
        stickyHeader: '操作',
        requireActionButton: true,
    },
    {
        name: '通知列表',
        path: () => '/#/notifications',
        tableName: '通知列表',
        columnName: '类型',
        defaultWidth: 164,
        expectedText: () => notificationTitle,
    },
    {
        name: '自动化规则列表',
        path: () => '/#/automation-rules',
        tableName: '自动化规则列表',
        columnName: '规则名称',
        defaultWidth: 280,
        expectedText: () => ruleName,
        stickyHeader: '操作',
        requireActionButton: true,
    },
    {
        name: '自动化日志列表',
        path: () => '/#/automation-logs',
        tableName: '自动化日志列表',
        columnName: 'ID',
        defaultWidth: 88,
        expectedText: () => ruleName,
    },
    {
        name: 'Webhook 配置列表',
        path: () => '/#/webhook-settings',
        tableName: 'Webhook 配置列表',
        columnName: '名称',
        defaultWidth: 280,
        expectedText: () => webhookName,
        stickyHeader: '操作',
        requireActionButton: true,
    },
    {
        name: '系统配置列表',
        path: () => '/#/system-settings/overview',
        tableName: '系统配置列表',
        columnName: '配置项',
        defaultWidth: 260,
        tabName: '安全策略',
        stickyHeader: '操作',
        requireActionButton: true,
        prepareSticky: async (table) => {
            for (const row of await table.getByRole('row').all()) {
                const controls = await row
                    .getByLabel(/^配置“.+”的值$/u)
                    .all();
                for (const control of controls) {
                    if (!(await control.isVisible()) || !(await control.isEnabled())) {
                        continue;
                    }
                    if ((await control.getAttribute('type')) === 'checkbox') {
                        await control.click();
                    } else {
                        const current = await control.inputValue();
                        const numeric = Number(current);
                        await control.fill(
                            Number.isFinite(numeric)
                                ? String(numeric + 1)
                                : `${current}-e2e`,
                        );
                    }
                    return;
                }
            }
            throw new Error('系统配置列表中没有可编辑的真实配置行');
        },
    },
    {
        name: '工单历史列表',
        path: () => `/#/tickets/${ticketFixture.id}/show`,
        tableName: '工单历史列表',
        columnName: '操作',
        defaultWidth: 180,
        expectedText: () => '创建工单',
        tabName: '历史记录',
    },
    {
        name: '该用户创建的工单列表',
        path: () => `/#/users/${adminUserID}/show`,
        tableName: '该用户创建的工单列表',
        columnName: '工单编号',
        defaultWidth: 156,
        expectedText: () => ticketFixture.ticketNumber,
        tabName: '相关工单',
    },
    {
        name: '该用户负责的工单列表',
        path: () => `/#/users/${adminUserID}/show`,
        tableName: '该用户负责的工单列表',
        columnName: '工单编号',
        defaultWidth: 156,
        expectedText: () => ticketFixture.ticketNumber,
        tabName: '相关工单',
    },
    {
        name: '服务主体列表',
        path: () => '/#/agent-control',
        tableName: '服务主体列表',
        columnName: 'AI 智能体',
        defaultWidth: 260,
        expectedText: () => principalName,
        tabName: '服务主体',
        stickyHeader: '操作',
        requireActionButton: true,
    },
    {
        name: '工单租约列表',
        path: () => '/#/agent-control',
        tableName: '工单租约列表',
        columnName: '工单',
        defaultWidth: 160,
        expectedText: () => agentTicketFixture.ticketNumber,
        tabName: '实时租约',
        stickyHeader: '接管',
        requireActionButton: true,
    },
    {
        name: '领域事件列表',
        path: () => '/#/agent-control',
        tableName: '领域事件列表',
        columnName: '时间',
        defaultWidth: 188,
        tabName: '领域事件',
    },
    {
        name: '事件投递列表',
        path: () => '/#/agent-control',
        tableName: '事件投递列表',
        columnName: '事件',
        defaultWidth: 280,
        tabName: '事件投递（Outbox）',
        stickyHeader: '操作',
        requireActionButton: false,
    },
    {
        name: '智能体策略决策审计',
        path: () => '/#/agent-control',
        tableName: '智能体策略决策审计',
        columnName: '时间',
        defaultWidth: 188,
        tabName: '策略审计',
    },
];

const activateTableTab = async (
    page: Page,
    target: EnterpriseTableCase,
) => {
    if (!target.tabName) {
        return;
    }
    const tab = page
        .getByRole('main')
        .getByRole('tab', { name: target.tabName, exact: true });
    await tab.click();
    await expect(tab).toHaveAttribute('aria-selected', 'true');
};

const locateEnterpriseTable = (
    page: Page,
    target: EnterpriseTableCase,
) =>
    page
        .getByRole('main')
        .getByRole('table', { name: target.tableName, exact: true });

const openEnterpriseTable = async (
    page: Page,
    target: EnterpriseTableCase,
) => {
    await page.goto(target.path());
    await waitForPrimaryPage(page);
    await activateTableTab(page, target);
    const table = locateEnterpriseTable(page, target);
    await expect(table).toBeVisible({ timeout: 15_000 });
    if (target.expectedText) {
        await expect(table).toContainText(target.expectedText(), {
            timeout: 15_000,
        });
    }
    return table;
};

const reopenEnterpriseTable = async (
    page: Page,
    target: EnterpriseTableCase,
) => {
    await page.reload();
    await waitForPrimaryPage(page);
    await activateTableTab(page, target);
    const table = locateEnterpriseTable(page, target);
    await expect(table).toBeVisible({ timeout: 15_000 });
    return table;
};

const actualDataRows = async (table: Locator) => {
    const result: Locator[] = [];
    for (const row of await table.getByRole('row').all()) {
        if ((await row.getByRole('cell').count()) > 1) {
            result.push(row);
        }
    }
    return result;
};

const interactiveControls = async (scope: Locator) => [
    ...(await scope.getByRole('button').all()),
    ...(await scope.getByRole('link').all()),
];

const expectActualSingleLineRows = async (
    table: Locator,
    tableName: string,
) => {
    const resizeHandles = table.getByRole('separator');
    expect(
        await resizeHandles.count(),
        `${tableName} 必须为可配置列提供列宽调整手柄`,
    ).toBeGreaterThan(0);
    for (const handle of await resizeHandles.all()) {
        await expect(handle).toBeVisible();
        await expect(handle).toHaveAttribute('aria-orientation', 'vertical');
        await expect(handle).toHaveAttribute('aria-valuenow', /^\d+$/u);
        await expect(handle).toHaveAccessibleName(/^调整“?.+列宽/u);
    }

    const rows = await actualDataRows(table);
    expect(
        rows.length,
        `${tableName} 必须使用真实数据行验证，空态行不能算通过`,
    ).toBeGreaterThan(0);

    const violations: Array<Record<string, unknown>> = [];
    for (const [rowIndex, row] of rows.entries()) {
        const rowBox = await row.boundingBox();
        if (!rowBox || rowBox.height > 64) {
            violations.push({
                row: rowIndex + 1,
                kind: 'row-height',
                height: rowBox?.height ?? null,
            });
        }
        const cellViolations = await row
            .getByRole('cell')
            .evaluateAll((cells) =>
                cells.flatMap((cell, cellIndex) => {
                    if (cell.classList.contains('cd-table-cell-multiline')) {
                        return [];
                    }
                    const style = getComputedStyle(cell);
                    const textRects: DOMRect[] = [];
                    const walker = document.createTreeWalker(
                        cell,
                        NodeFilter.SHOW_TEXT,
                    );
                    let node = walker.nextNode();
                    while (node) {
                        if ((node.textContent ?? '').trim()) {
                            const parent = node.parentElement;
                            const parentStyle = parent
                                ? getComputedStyle(parent)
                                : null;
                            if (
                                parentStyle?.display !== 'none' &&
                                parentStyle?.visibility !== 'hidden'
                            ) {
                                const range = document.createRange();
                                range.selectNodeContents(node);
                                for (const rect of range.getClientRects()) {
                                    if (rect.width > 0.5 && rect.height > 0.5) {
                                        textRects.push(rect);
                                    }
                                }
                            }
                        }
                        node = walker.nextNode();
                    }

                    const lineTops: number[] = [];
                    for (const rect of textRects.sort(
                        (left, right) => left.top - right.top,
                    )) {
                        if (
                            lineTops.every(
                                (knownTop) =>
                                    Math.abs(knownTop - rect.top) > 6,
                            )
                        ) {
                            lineTops.push(rect.top);
                        }
                    }
                    const cellBox = cell.getBoundingClientRect();
                    const reasons: string[] = [];
                    if (style.whiteSpace !== 'nowrap') {
                        reasons.push(`white-space=${style.whiteSpace}`);
                    }
                    if (lineTops.length > 1) {
                        reasons.push(`text-lines=${lineTops.length}`);
                    }
                    if (cellBox.height > 64) {
                        reasons.push(`cell-height=${cellBox.height}`);
                    }
                    return reasons.length > 0
                        ? [{ cell: cellIndex + 1, reasons }]
                        : [];
                }),
            );
        for (const violation of cellViolations) {
            violations.push({
                row: rowIndex + 1,
                kind: 'cell-layout',
                ...violation,
            });
        }
    }

    expect(
        violations,
        `${tableName} 存在真实换行或超高数据行：${JSON.stringify(
            violations,
        )}`,
    ).toEqual([]);
};

const scrollTableToFarRight = async (table: Locator) =>
    table.evaluate((root) => {
        let container = root.parentElement;
        while (container) {
            const overflowX = getComputedStyle(container).overflowX;
            if (overflowX === 'auto' || overflowX === 'scroll') {
                container.scrollLeft = container.scrollWidth;
                const rect = container.getBoundingClientRect();
                const clientRight = rect.left + container.clientWidth;
                // `scrollbar-gutter: stable` reduces clientWidth even when the
                // reserved gutter is paintable. Sticky `right: 0` aligns to
                // the scroll container's visual border edge, not clientRight.
                const rawVisibleRight = rect.right;
                let visibleRight = Math.min(
                    rawVisibleRight,
                    document.documentElement.clientWidth,
                );
                let clippingAncestor = container.parentElement;
                while (clippingAncestor) {
                    const ancestorStyle = getComputedStyle(clippingAncestor);
                    if (
                        /hidden|clip|auto|scroll/u.test(
                            ancestorStyle.overflowX,
                        )
                    ) {
                        visibleRight = Math.min(
                            visibleRight,
                            clippingAncestor.getBoundingClientRect().right,
                        );
                    }
                    clippingAncestor = clippingAncestor.parentElement;
                }
                return {
                    overflowX,
                    scrollLeft: container.scrollLeft,
                    scrollWidth: container.scrollWidth,
                    clientWidth: container.clientWidth,
                    clientRight,
                    rawVisibleRight,
                    visibleRight,
                    documentWidth: document.documentElement.scrollWidth,
                    viewportWidth: document.documentElement.clientWidth,
                };
            }
            container = container.parentElement;
        }
        return null;
    });

const expectStickyActionColumn = async (
    table: Locator,
    target: EnterpriseTableCase,
) => {
    expect(target.stickyHeader).toBeTruthy();
    await target.prepareSticky?.(table);

    const header = table.getByRole('columnheader', {
        name: new RegExp(
            `^[“"]?${escapeRegExp(target.stickyHeader!)}`,
            'u',
        ),
    });
    await expect(header).toHaveCount(1);
    const headerCellIndex = await header.evaluate(
        (element) => (element as HTMLTableCellElement).cellIndex,
    );

    let actionCell: Locator | undefined;
    let actionableButton: Locator | undefined;
    for (const row of await actualDataRows(table)) {
        const cells = await row.getByRole('cell').all();
        const candidate = cells[headerCellIndex];
        if (!candidate) {
            continue;
        }
        const buttons = await interactiveControls(candidate);
        const enabledButtons: Locator[] = [];
        for (const button of buttons) {
            if ((await button.isVisible()) && (await button.isEnabled())) {
                enabledButtons.push(button);
            }
        }
        if (
            !target.requireActionButton ||
            enabledButtons.length > 0
        ) {
            actionCell = candidate;
            actionableButton = enabledButtons[0];
            break;
        }
    }

    expect(actionCell, `${target.name} 找不到 sticky 操作数据单元格`).toBeDefined();
    if (target.requireActionButton) {
        expect(
            actionableButton,
            `${target.name} 的 sticky 操作列没有可操作按钮`,
        ).toBeDefined();
    }

    const metrics = await scrollTableToFarRight(table);
    expect(metrics, `${target.name} 必须具有表格内部横向滚动容器`).not.toBeNull();
    expect(metrics!.overflowX).toMatch(/auto|scroll/u);
    expect(metrics!.scrollWidth).toBeGreaterThan(metrics!.clientWidth);
    expect(metrics!.scrollLeft).toBeGreaterThan(0);
    expect(metrics!.documentWidth).toBeLessThanOrEqual(
        metrics!.viewportWidth + 1,
    );

    const [headerBox, cellBox] = await Promise.all([
        header.boundingBox(),
        actionCell!.boundingBox(),
    ]);
    expect(headerBox).not.toBeNull();
    expect(cellBox).not.toBeNull();
    expect(
        Math.abs(
            headerBox!.x + headerBox!.width - metrics!.visibleRight,
        ),
        `${target.name} 的 sticky 表头必须贴住滚动容器右边：${JSON.stringify({
            metrics,
            headerBox,
            cellBox,
        })}`,
    ).toBeLessThanOrEqual(2);
    expect(
        Math.abs(cellBox!.x + cellBox!.width - metrics!.visibleRight),
        `${target.name} 的 sticky 数据单元格必须贴住滚动容器右边`,
    ).toBeLessThanOrEqual(2);
    expect(
        await actionCell!.evaluate((element) => {
            const box = element.getBoundingClientRect();
            const hit = document.elementFromPoint(
                box.right - Math.min(8, box.width / 2),
                box.top + box.height / 2,
            );
            return (
                hit === element ||
                (hit !== null && element.contains(hit))
            );
        }),
        `${target.name} 的 sticky 操作列不能被其他列遮挡`,
    ).toBe(true);

    for (const button of await interactiveControls(actionCell!)) {
        if (await button.isVisible()) {
            await expect(button).toHaveAccessibleName(/\S/u);
        }
    }
    if (actionableButton) {
        await actionableButton.click({ trial: true });
    }
};

const resizeHandle = (
    table: Locator,
    columnName: string,
) =>
    table.getByRole('separator', {
        name: new RegExp(
            `^调整“${escapeRegExp(columnName)}”列宽`,
            'u',
        ),
    });

test.describe('企业级列表表格', () => {
    test.describe.configure({ mode: 'serial' });

    test.beforeAll(async ({ request }) => {
        test.setTimeout(120_000);
        assertDestructiveE2EAllowed('企业列表真实数据 E2E');
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const admin = await findUserByEmail(
            request,
            token,
            DEFAULT_ADMIN.email,
        );
        expect(typeof admin?.id).toBe('number');
        adminUserID = admin!.id as number;

        await createWebhookFixture(request, token);
        await createNotification(request, {
            title: notificationTitle,
            content: `${notificationTitle} 内容`,
        });
        automationRuleID = await createAutomationRule(request, ruleName, true);
        ticketFixture = await createTableTicket(
            request,
            token,
            ticketTitle,
            adminUserID,
        );
        agentTicketFixture = await createTableTicket(
            request,
            token,
            agentTicketTitle,
        );

        await expect
            .poll(
                async () => {
                    const response = await apiRequest<Record<string, unknown>>(
                        request,
                        token,
                        `${projectAPIPath(
                            projectKey,
                            'admin/automation/logs',
                        )}?page=1&page_size=100`,
                    );
                    return extractItems<Record<string, unknown>>(response).some(
                        (item) =>
                            item.rule_id === automationRuleID ||
                            (item.rule as Record<string, unknown> | undefined)
                                ?.name === ruleName,
                    );
                },
                {
                    message: '等待 E2E 自动化执行日志生成',
                    timeout: 30_000,
                },
            )
            .toBe(true);

        await provisionAgentLease(request, token, agentTicketFixture);
        await expect
            .poll(
                async () => {
                    const response = await apiRequest<Record<string, unknown>>(
                        request,
                        token,
                        projectAPIPath(
                            agentTicketFixture.projectKey,
                            'admin/agents/agent-control/overview',
                        ),
                    );
                    const overview =
                        extractData<Record<string, unknown>>(response);
                    return (
                        Array.isArray(overview.leases) &&
                        overview.leases.length > 0 &&
                        Array.isArray(overview.events) &&
                        overview.events.length > 0 &&
                        Array.isArray(overview.outbox) &&
                        overview.outbox.length > 0 &&
                        Array.isArray(overview.policy_decisions) &&
                        overview.policy_decisions.length > 0
                    );
                },
                {
                    message: '等待 Agent 控制面五张表的真实数据',
                    timeout: 30_000,
                },
            )
            .toBe(true);
    });

    test.afterAll(async ({ request }) => {
        test.setTimeout(120_000);
        try {
            await releaseAgentLease(request);
        } finally {
            await cleanupE2EData(request, {
                users: false,
                emailConfig: false,
                webhooks: true,
                agentControl: true,
            });
        }
    });

    test('UI-004 UI-006 UI-028：全部 15 张企业表用真实数据验证实际单行布局', async ({
        page,
    }) => {
        test.setTimeout(180_000);
        await authenticatePage(page);

        const cases = tableCases();
        expect(cases).toHaveLength(15);
        for (const target of cases) {
            await test.step(target.name, async () => {
                const table = await openEnterpriseTable(page, target);
                await expectActualSingleLineRows(table, target.tableName);
            });
        }
    });

    test('UI-007 UI-029：窄视口横向滚动后 sticky 右操作列仍贴边、命中且按钮有名称', async ({
        page,
    }) => {
        test.setTimeout(150_000);
        await page.setViewportSize({ width: 820, height: 720 });
        await authenticatePage(page);

        const stickyCases = tableCases().filter(
            (target) => target.stickyHeader,
        );
        expect(stickyCases).toHaveLength(8);
        for (const target of stickyCases) {
            await test.step(target.name, async () => {
                const table = await openEnterpriseTable(page, target);
                await expectStickyActionColumn(table, target);
            });
        }
    });

    test('UI-005 UI-026：全部 15 张表的键盘列宽可持久化且双击复位也持久化', async ({
        page,
    }) => {
        test.setTimeout(300_000);
        await page.setViewportSize({ width: 1280, height: 800 });
        await authenticatePage(page);

        for (const target of tableCases()) {
            await test.step(target.name, async () => {
                let table = await openEnterpriseTable(page, target);
                let handle = resizeHandle(table, target.columnName);
                await expect(handle).toHaveCount(1);
                await handle.dblclick();
                await expect(handle).toHaveAttribute(
                    'aria-valuenow',
                    String(target.defaultWidth),
                );

                await handle.focus();
                await handle.press('ArrowRight');
                await expect(handle).toHaveAttribute(
                    'aria-valuenow',
                    String(target.defaultWidth + 8),
                );

                table = await reopenEnterpriseTable(page, target);
                handle = resizeHandle(table, target.columnName);
                await expect(handle).toHaveAttribute(
                    'aria-valuenow',
                    String(target.defaultWidth + 8),
                );

                await handle.dblclick();
                await expect(handle).toHaveAttribute(
                    'aria-valuenow',
                    String(target.defaultWidth),
                );

                table = await reopenEnterpriseTable(page, target);
                await expect(
                    resizeHandle(table, target.columnName),
                ).toHaveAttribute(
                    'aria-valuenow',
                    String(target.defaultWidth),
                );
            });
        }
    });

    test('UI-029：操作按钮、系统配置控件和 Webhook switch 都有稳定可访问名称', async ({
        page,
    }) => {
        test.setTimeout(90_000);
        await authenticatePage(page);

        let table = await openEnterpriseTable(page, tableCases()[0]);
        const ticketRow = table.getByRole('row', {
            name: new RegExp(escapeRegExp(ticketTitle), 'u'),
        });
        for (const name of [
            '在详情页分配工单',
            '在详情页升级工单',
            '在详情页变更状态',
        ]) {
            await expect(
                ticketRow.getByRole('button', { name, exact: true }),
            ).toBeVisible();
        }
        for (const name of ['查看', '编辑']) {
            await expect(
                ticketRow.getByRole('link', { name, exact: true }),
            ).toBeVisible();
        }

        const systemCase = tableCases().find(
            (target) => target.tableName === '系统配置列表',
        )!;
        table = await openEnterpriseTable(page, systemCase);
        const configControls = await table
            .getByLabel(/^配置“.+”的值$/u)
            .all();
        expect(configControls.length).toBeGreaterThan(0);
        for (const control of configControls) {
            await expect(control).toHaveAccessibleName(
                /^配置“.+”的值$/u,
            );
        }

        const webhookCase = tableCases().find(
            (target) => target.tableName === 'Webhook 配置列表',
        )!;
        table = await openEnterpriseTable(page, webhookCase);
        const webhookRow = table.getByRole('row', {
            name: new RegExp(escapeRegExp(webhookName), 'u'),
        });
        for (const name of [
            `测试 Webhook：${webhookName}`,
            `编辑 Webhook：${webhookName}`,
            `删除 Webhook：${webhookName}`,
        ]) {
            await expect(
                webhookRow.getByRole('button', { name, exact: true }),
            ).toBeVisible();
        }
        await page
            .getByRole('main')
            .getByRole('button', { name: '新增 Webhook', exact: true })
            .click();
        const webhookDialog = page.getByRole('dialog', {
            name: '新增 Webhook',
            exact: true,
        });
        await expect(
            webhookDialog.getByLabel('异步发送', { exact: true }),
        ).toBeVisible();
        await webhookDialog
            .getByRole('button', { name: '取消', exact: true })
            .click();

        await page.goto('/#/agent-control');
        await waitForPrimaryPage(page);
        await expect(
            page
                .getByRole('main')
                .getByLabel('智能体全局紧急停止', { exact: true }),
        ).toBeVisible();
        await expect(
            page
                .getByRole('main')
                .getByLabel('智能体全局只读模式', { exact: true }),
        ).toBeVisible();
    });
});
