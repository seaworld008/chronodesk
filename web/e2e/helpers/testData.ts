import type { APIRequestContext, Page } from '@playwright/test';
import {
    apiRequest,
    loginSession,
    type AuthSession,
    type Credentials,
} from './api';

export const E2E_PREFIX = 'E2E-';
export const DEFAULT_PASSWORD = 'Admin123!';

export const DEFAULT_ADMIN: Credentials = {
    email: 'admin@example.com',
    password: DEFAULT_PASSWORD,
};

const ROLE_ACCOUNTS = [
    {
        email: 'admin.manager@example.com',
        username: 'admin_manager',
        role: 'admin',
        first_name: 'Admin',
        last_name: 'Manager',
        department: 'IT',
        job_title: 'System Admin',
    },
    {
        email: 'agent@example.com',
        username: 'agent',
        role: 'agent',
        first_name: 'Support',
        last_name: 'Agent',
        department: 'Support',
        job_title: 'Support Agent',
    },
    {
        email: 'customer@example.com',
        username: 'customer',
        role: 'customer',
        first_name: 'Demo',
        last_name: 'Customer',
        department: 'Customer',
        job_title: 'Customer',
    },
] as const;

const RESERVED_EMAILS = new Set([
    DEFAULT_ADMIN.email,
    ...ROLE_ACCOUNTS.map((account) => account.email),
]);

const authSessions = new Map<string, Promise<AuthSession>>();

const extractItems = <T>(payload: unknown): T[] => {
    if (!payload || typeof payload !== 'object') {
        return [];
    }
    const data = (payload as Record<string, unknown>).data as Record<string, unknown> | undefined;
    if (data?.items && Array.isArray(data.items)) {
        return data.items as T[];
    }
    if (data?.rules && Array.isArray(data.rules)) {
        return data.rules as T[];
    }
    if (data?.logs && Array.isArray(data.logs)) {
        return data.logs as T[];
    }
    if (Array.isArray(payload)) {
        return payload as T[];
    }
    return [];
};

const getAuthSession = (
    request: APIRequestContext,
    credentials: Credentials,
): Promise<AuthSession> => {
    const key = credentials.email.toLowerCase();
    const existing = authSessions.get(key);
    if (existing) {
        return existing;
    }

    const pending = loginSession(request, credentials).catch((error) => {
        authSessions.delete(key);
        throw error;
    });
    authSessions.set(key, pending);
    return pending;
};

const getAdminToken = async (request: APIRequestContext) =>
    (await getAuthSession(request, DEFAULT_ADMIN)).access_token;

export const authenticatePage = async (
    page: Page,
    credentials: Credentials = DEFAULT_ADMIN,
) => {
    const session = await getAuthSession(page.request, credentials);
    await page.addInitScript((auth) => {
        localStorage.setItem('token', auth.access_token);
        if (auth.refresh_token) {
            localStorage.setItem('refreshToken', auth.refresh_token);
        }
        if (auth.user) {
            localStorage.setItem('user', JSON.stringify(auth.user));
        }
        if (auth.permissions) {
            localStorage.setItem('permissions', JSON.stringify(auth.permissions));
        }
        if (auth.expires_in) {
            localStorage.setItem(
                'tokenExpiresAt',
                String(Date.now() + auth.expires_in * 1000),
            );
        }
    }, session);
    await page.goto('/#/');
    await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
};

export const loginViaUI = async (page: Page, credentials: Credentials = DEFAULT_ADMIN) => {
    await page.goto('/#/login');
    await page.getByLabel('邮箱').fill(credentials.email);
    await page.getByLabel('密码').fill(credentials.password);
    await page.getByRole('button', { name: '登录系统' }).click();
    await page.getByRole('menuitem', { name: '工单管理' }).waitFor({ timeout: 15000 });
};

const findUserByEmail = async (request: APIRequestContext, token: string, email: string) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/admin/users?search=${encodeURIComponent(email)}&page=1&page_size=50`,
    );
    const users = extractItems<Record<string, unknown>>(response);
    return users.find((user) => user.email === email);
};

export const ensureRoleAccounts = async (request: APIRequestContext) => {
    const token = await getAdminToken(request);

    for (const account of ROLE_ACCOUNTS) {
        const existing = await findUserByEmail(request, token, account.email);
        if (existing) {
            if (typeof existing.id !== 'number') {
                throw new Error(`角色测试账号缺少有效 ID: ${account.email}`);
            }
            await apiRequest(request, token, `/api/admin/users/${existing.id}`, {
                method: 'PUT',
                data: {
                    role: account.role,
                    status: 'active',
                    first_name: account.first_name,
                    last_name: account.last_name,
                    department: account.department,
                    job_title: account.job_title,
                },
            });
            await apiRequest(request, token, `/api/admin/users/${existing.id}/reset-password`, {
                method: 'POST',
                data: { new_password: DEFAULT_PASSWORD },
            });
            continue;
        }
        await apiRequest(request, token, '/api/admin/users', {
            method: 'POST',
            data: {
                username: account.username,
                email: account.email,
                password: DEFAULT_PASSWORD,
                role: account.role,
                first_name: account.first_name,
                last_name: account.last_name,
                department: account.department,
                job_title: account.job_title,
            },
        });
    }
};

export const createNotification = async (
    request: APIRequestContext,
    options: { title: string; content: string; recipientEmail?: string },
) => {
    const token = await getAdminToken(request);
    const recipientEmail = options.recipientEmail ?? DEFAULT_ADMIN.email;
    const recipient = await findUserByEmail(request, token, recipientEmail);
    if (!recipient) {
        throw new Error(`未找到通知接收者: ${recipientEmail}`);
    }

    const response = await apiRequest<Record<string, unknown>>(request, token, '/api/admin/notifications', {
        method: 'POST',
        data: {
            type: 'system_alert',
            title: options.title,
            content: options.content,
            priority: 'normal',
            channel: 'in_app',
            recipient_id: recipient.id,
        },
    });

    const data = (response.data as Record<string, unknown>) ?? {};
    return data.id as number;
};

export const deleteNotification = async (request: APIRequestContext, id: number) => {
    const token = await getAdminToken(request);
    await apiRequest(request, token, `/api/admin/notifications/${id}`, { method: 'DELETE' });
};

export const createTicket = async (request: APIRequestContext, title: string) => {
    const token = await getAdminToken(request);
    const response = await apiRequest<Record<string, unknown>>(request, token, '/api/tickets', {
        method: 'POST',
        data: {
            title,
            description: `${title} 自动化测试描述`,
            type: 'request',
            priority: 'normal',
            source: 'web',
        },
    });
    const data = (response.data as Record<string, unknown>) ?? {};
    return data.id as number;
};

export const deleteTicket = async (request: APIRequestContext, id: number) => {
    const token = await getAdminToken(request);
    await apiRequest(request, token, `/api/tickets/${id}`, { method: 'DELETE' });
};

export const deleteTicketByTitle = async (request: APIRequestContext, title: string) => {
    const token = await getAdminToken(request);
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/tickets?search=${encodeURIComponent(title)}&page=1&page_size=20`,
    );
    const tickets = extractItems<Record<string, unknown>>(response);
    const target = tickets.find((ticket) => ticket.title === title);
    if (!target) {
        return;
    }
    await apiRequest(request, token, `/api/tickets/${target.id}`, { method: 'DELETE' });
};

const deleteAutomationRules = async (request: APIRequestContext, token: string) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/admin/automation/rules?search=${encodeURIComponent(E2E_PREFIX)}&page=1&page_size=100`,
    );
    const rules = extractItems<Record<string, unknown>>(response);
    for (const rule of rules) {
        if (typeof rule.name === 'string' && rule.name.startsWith(E2E_PREFIX)) {
            await apiRequest(request, token, `/api/admin/automation/rules/${rule.id}`, { method: 'DELETE' });
        }
    }
};

const deleteTickets = async (request: APIRequestContext, token: string) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/tickets?search=${encodeURIComponent(E2E_PREFIX)}&page=1&page_size=100`,
    );
    const tickets = extractItems<Record<string, unknown>>(response);
    for (const ticket of tickets) {
        if (typeof ticket.title === 'string' && ticket.title.startsWith(E2E_PREFIX)) {
            await apiRequest(request, token, `/api/tickets/${ticket.id}`, { method: 'DELETE' });
        }
    }
};

const deleteNotifications = async (request: APIRequestContext, token: string) => {
    const filter = encodeURIComponent(JSON.stringify({ q: E2E_PREFIX }));
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/notifications?filter=${filter}&page=1&page_size=100`,
    );
    const notifications = extractItems<Record<string, unknown>>(response);
    for (const notification of notifications) {
        if (typeof notification.title === 'string' && notification.title.startsWith(E2E_PREFIX)) {
            await apiRequest(request, token, `/api/admin/notifications/${notification.id}`, { method: 'DELETE' });
        }
    }
};

const deleteTestUsers = async (request: APIRequestContext, token: string) => {
    const response = await apiRequest<Record<string, unknown>>(
        request,
        token,
        `/api/admin/users?search=e2e_&page=1&page_size=100`,
    );
    const users = extractItems<Record<string, unknown>>(response);

    for (const user of users) {
        const email = String(user.email ?? '');
        if (!email || RESERVED_EMAILS.has(email)) {
            continue;
        }
        if (!email.startsWith('e2e_') && !email.startsWith('test_')) {
            continue;
        }
        await apiRequest(request, token, `/api/admin/users/${user.id}`, { method: 'DELETE' });
    }
};

const resetEmailConfig = async (request: APIRequestContext, token: string) => {
    await apiRequest(request, token, '/api/admin/email-config', {
        method: 'PUT',
        data: {
            email_verification_enabled: false,
            smtp_host: '',
            smtp_port: 587,
            smtp_username: '',
            smtp_password: '',
            smtp_use_tls: true,
            smtp_use_ssl: false,
            from_email: '',
            from_name: '工单系统',
            skip_smtp_test: true,
        },
    });
};

type CleanupOptions = {
    automationRules?: boolean;
    tickets?: boolean;
    notifications?: boolean;
    users?: boolean;
    emailConfig?: boolean;
};

const defaultCleanupOptions: Required<CleanupOptions> = {
    automationRules: true,
    tickets: true,
    notifications: true,
    users: true,
    emailConfig: true,
};

export const cleanupE2EData = async (request: APIRequestContext, options: CleanupOptions = {}) => {
    const token = await getAdminToken(request);
    const config = { ...defaultCleanupOptions, ...options };

    if (config.automationRules) {
        await deleteAutomationRules(request, token);
    }
    if (config.tickets) {
        await deleteTickets(request, token);
    }
    if (config.notifications) {
        await deleteNotifications(request, token);
    }
    if (config.users) {
        await deleteTestUsers(request, token);
    }
    if (config.emailConfig) {
        await resetEmailConfig(request, token);
    }
};
