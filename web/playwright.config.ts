import { defineConfig, devices } from '@playwright/test';
import {
    assertDestructiveE2EAllowed,
    isLoopbackE2E,
    testBaseURL,
} from './e2e/helpers/safety';

const configuredBaseURL = testBaseURL();
if (
    !['http:', 'https:'].includes(configuredBaseURL.protocol) ||
    configuredBaseURL.username ||
    configuredBaseURL.password ||
    configuredBaseURL.search ||
    configuredBaseURL.hash ||
    configuredBaseURL.pathname.replace(/\/+$/u, '') !== ''
) {
    throw new Error(
        'TEST_BASE_URL 必须是无凭据、路径、查询参数和片段的 http(s) origin。',
    );
}
assertDestructiveE2EAllowed('启动 Playwright 写测试会话');

const isPublishingCI =
    process.env.CI === 'true' || process.env.CI === '1';
const isEphemeralPublishingCI =
    isPublishingCI &&
    isLoopbackE2E() &&
    process.env.CHRONODESK_EPHEMERAL_E2E === '1';
const shouldStartLocalServer =
    isLoopbackE2E() &&
    configuredBaseURL.port === '3000';

/**
 * ChronoDesk E2E Test Configuration
 * @see https://playwright.dev/docs/test-configuration
 */
export default defineConfig({
    testDir: './e2e',
    // E2E 场景会修改共享的管理员账号、系统配置和测试数据。
    // 只有 CI 的一次性回环环境允许文件级并行；共享、本地和远端环境固定单
    // worker，避免并行登录限流或清理其他会话的数据。单文件内部仍保持串行。
    fullyParallel: false,
    forbidOnly: isPublishingCI,
    retries: isPublishingCI ? 2 : 0,
    workers: isEphemeralPublishingCI ? 3 : 1,
    // 发布 CI 只输出凭据安全的行报告，不生成可上传的浏览器状态快照。
    reporter: isPublishingCI
        ? 'line'
        : [['html', { open: 'never' }]],
    // Keep visual baselines isolated by browser project and operating system.
    // Contributors can update their local baseline without overwriting the
    // canonical Linux baseline exercised by CI.
    snapshotPathTemplate:
        '{testDir}/{testFilePath}-snapshots/{arg}{-projectName}{-snapshotSuffix}{ext}',

    use: {
        // Base URL for all tests
        baseURL: configuredBaseURL.origin,

        // Trace/video can capture Authorization headers and browser storage.
        trace: 'off',
        video: 'off',

        // CI failure pages can contain passwords, OTPs, backup codes or secrets.
        screenshot: isPublishingCI ? 'off' : 'only-on-failure',
    },

    projects: [
        {
            name: 'chromium',
            use: { ...devices['Desktop Chrome'] },
        },
    ],

    // Local dev server
    webServer: shouldStartLocalServer
        ? {
              command: 'npm run dev',
              url: configuredBaseURL.origin,
              reuseExistingServer:
                  process.env.PLAYWRIGHT_REUSE_SERVER === '1' ||
                  !isPublishingCI,
              timeout: 120 * 1000,
          }
        : undefined,
});
