import { defineConfig, devices } from '@playwright/test';

/**
 * ChronoDesk E2E Test Configuration
 * @see https://playwright.dev/docs/test-configuration
 */
export default defineConfig({
    testDir: './e2e',
    // E2E 场景会修改共享的管理员账号、系统配置和测试数据。
    // 固定单 worker 可避免并行登录触发认证限流，也避免清理阶段互相删除数据。
    fullyParallel: false,
    forbidOnly: !!process.env.CI,
    retries: process.env.CI ? 2 : 0,
    workers: 1,
    reporter: 'html',

    use: {
        // Base URL for all tests
        baseURL: process.env.TEST_BASE_URL || 'http://localhost:3000',

        // Collect trace on retry
        trace: 'on-first-retry',

        // Screenshots on failure
        screenshot: 'only-on-failure',
    },

    projects: [
        {
            name: 'chromium',
            use: { ...devices['Desktop Chrome'] },
        },
    ],

    // Local dev server
    webServer: {
        command: 'npm run dev',
        url: 'http://localhost:3000',
        reuseExistingServer:
            process.env.PLAYWRIGHT_REUSE_SERVER === '1' || !process.env.CI,
        timeout: 120 * 1000,
    },
});
