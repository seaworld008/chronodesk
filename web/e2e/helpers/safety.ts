import type { Page, Route } from '@playwright/test';

const DEFAULT_BASE_URL = 'http://localhost:3000';
const REMOTE_E2E_ENV = 'CHRONODESK_ALLOW_REMOTE_E2E';
const OWNERSHIP_PREFIX_ENV = 'CHRONODESK_E2E_OWNERSHIP_PREFIX';
const EPHEMERAL_E2E_ENV = 'CHRONODESK_EPHEMERAL_E2E';
const OWNERSHIP_PREFIX_PATTERN =
    /^e2e-[a-z0-9][a-z0-9._-]{4,58}[a-z0-9]$/u;

export const testBaseURL = () =>
    new URL(process.env.TEST_BASE_URL || DEFAULT_BASE_URL);

export const isLoopbackE2E = () => {
    const hostname = testBaseURL().hostname.replace(/^\[|\]$/g, '').toLowerCase();
    return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1';
};

const isRemoteE2EExplicitlyAllowed = () =>
    process.env[REMOTE_E2E_ENV] === '1';

const validatedOwnershipPrefix = () => {
    const prefix = (process.env[OWNERSHIP_PREFIX_ENV] ?? '')
        .trim()
        .toLowerCase();
    if (!prefix) {
        return null;
    }
    if (!OWNERSHIP_PREFIX_PATTERN.test(prefix)) {
        throw new Error(
            `${OWNERSHIP_PREFIX_ENV} 必须以 e2e- 开头，且为 10-64 位` +
                '小写字母、数字、点、下划线或连字符。',
        );
    }
    return prefix;
};

export const assertDestructiveE2EAllowed = (operation: string) => {
    const ownershipPrefix = validatedOwnershipPrefix();
    if (
        isLoopbackE2E() ||
        (isRemoteE2EExplicitlyAllowed() && ownershipPrefix !== null)
    ) {
        return;
    }

    throw new Error(
        `拒绝在非回环环境执行破坏性 E2E 操作“${operation}”。` +
            `目标为 ${testBaseURL().origin}；如已确认这是隔离测试环境，请显式设置 ` +
            `${REMOTE_E2E_ENV}=1 与 ` +
            `${OWNERSHIP_PREFIX_ENV}=e2e-<唯一所有者>。`,
    );
};

export const assertGlobalE2EAllowed = (operation: string) => {
    // 即使是回环环境也拒绝格式错误的 ownership prefix，避免操作者误以为
    // 该值在切换到远端后仍然有效。
    validatedOwnershipPrefix();
    if (
        isLoopbackE2E() &&
        process.env[EPHEMERAL_E2E_ENV] === '1'
    ) {
        return;
    }
    throw new Error(
        `拒绝执行全局 E2E 操作“${operation}”。` +
            `仅回环一次性环境可设置 ${EPHEMERAL_E2E_ENV}=1 后运行。`,
    );
};

export const assertSecretMutationIsRestorable = (
    operation: string,
    touchesUnrecoverableSecret: boolean,
) => {
    if (!touchesUnrecoverableSecret) {
        return;
    }
    assertGlobalE2EAllowed(operation);
    // 本地环境仍须使用本轮唯一 marker，避免与其他测试凭据混淆。
    console.warn(`本地隔离秘密测试：${operation}`);
};

const isSafeBrowserRequest = (route: Route) => {
    const request = route.request();
    if (['GET', 'HEAD', 'OPTIONS'].includes(request.method())) {
        return true;
    }

    const pathname = new URL(request.url()).pathname;
    if (
        pathname === '/api/auth/refresh' ||
        pathname === '/api/auth/logout'
    ) {
        return true;
    }
    if (pathname !== '/api/auth/login') {
        return false;
    }

    const payload = request.postDataJSON() as
        | { remember_device?: boolean }
        | null;
    return payload?.remember_device !== true;
};

/**
 * 防御性拦截所有浏览器侧持久化写入。测试文件仍应在破坏性场景开头显式调用
 * assertDestructiveE2EAllowed，路由拦截用于防止后续新增用例漏加守卫。
 */
export const installBrowserMutationGuard = async (page: Page) => {
    await page.route('**/api/**', async (route) => {
        if (isSafeBrowserRequest(route)) {
            await route.continue();
            return;
        }
        assertDestructiveE2EAllowed(
            `${route.request().method()} ${new URL(route.request().url()).pathname}`,
        );
        await route.continue();
    });
};
