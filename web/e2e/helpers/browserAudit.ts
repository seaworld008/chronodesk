import { expect, type Page, type Request, type Response } from '@playwright/test';

type BrowserIssue = {
    kind: 'console' | 'pageerror' | 'requestfailed' | 'response';
    page: string;
    detail: string;
};

const isExpectedNavigationAbort = (request: Request) =>
    request.failure()?.errorText.includes('ERR_ABORTED') &&
    (request.resourceType() === 'document' ||
        ['GET', 'HEAD'].includes(request.method()));

const responseDetail = (response: Response) =>
    `${response.status()} ${response.request().method()} ${response.url()}`;

export const monitorBrowserHealth = (page: Page) => {
    const issues: BrowserIssue[] = [];

    page.on('console', (message) => {
        if (message.type() !== 'error' && message.type() !== 'warning') {
            return;
        }
        issues.push({
            kind: 'console',
            page: page.url(),
            detail: `${message.type()}: ${message.text()}`,
        });
    });
    page.on('pageerror', (error) => {
        issues.push({
            kind: 'pageerror',
            page: page.url(),
            detail: error.message,
        });
    });
    page.on('requestfailed', (request) => {
        if (isExpectedNavigationAbort(request)) {
            return;
        }
        issues.push({
            kind: 'requestfailed',
            page: page.url(),
            detail: `${request.method()} ${request.url()}：${
                request.failure()?.errorText ?? '未知网络错误'
            }`,
        });
    });
    page.on('response', (response) => {
        if (response.status() < 400) {
            return;
        }
        const resourceType = response.request().resourceType();
        if (
            ![
                'document',
                'fetch',
                'xhr',
                'script',
                'stylesheet',
                'font',
                'image',
            ].includes(resourceType)
        ) {
            return;
        }
        issues.push({
            kind: 'response',
            page: page.url(),
            detail: responseDetail(response),
        });
    });

    return {
        issues,
        assertClean: () => {
            expect(
                issues,
                issues
                    .map(
                        (issue) =>
                            `[${issue.kind}] ${issue.page || '尚未导航'}：${issue.detail}`,
                    )
                    .join('\n'),
            ).toEqual([]);
        },
    };
};

const englishUserFacingPattern =
    /\b(create|edit|delete|save|cancel|submit|refresh|search|loading|actions?|view|back|next|previous|retry|close|sign\s*in|log\s*out|add|remove|upload|download|no\s+(?:data|results?|records?)|empty|failed?|failure|errors?|success(?:ful)?|invalid|required|unauthorized|forbidden|not\s+found|unavailable|unexpected|timeout|network|server)\b/iu;

const allowedTechnicalTerms =
    /\b(?:ChronoDesk|AI|API|MCP|A2A|OAuth|Webhook|CloudEvent|Agent|SMTP|TLS|SSL|SHA-?256|JSON|HTTP|HTTPS|URL|URI|ID|ETag|Outbox|Scope|Redis|PostgreSQL|WebSocket|OTP|SLA|UTC|Chrome|Playwright|E2E)\b/giu;

const inspectedInteractiveSelector = [
    'button:visible',
    'a:visible',
    '[role="button"]:visible',
    '[role="link"]:visible',
    '[role="menuitem"]:visible',
    '[role="tab"]:visible',
    '[role="alert"]:visible',
    '[role="status"]:visible',
    '[role="dialog"]:visible',
    '[role="columnheader"]:visible',
    '[aria-live]:visible',
    'h1:visible',
    'h2:visible',
    'h3:visible',
    'label:visible',
    'input:visible',
    'textarea:visible',
].join(',');

const containsEnglishUserFacingMessage = (text: string) => {
    const withoutTechnicalTerms = text.replace(allowedTechnicalTerms, ' ');
    if (
        /^(?:https?:\/\/|mailto:)/iu.test(withoutTechnicalTerms.trim()) ||
        /^[\w./:@-]+$/u.test(withoutTechnicalTerms.trim())
    ) {
        return false;
    }
    return englishUserFacingPattern.test(withoutTechnicalTerms);
};

const visibleEnglishOperationTexts = async (page: Page) => {
    const candidates = await page
        .locator(inspectedInteractiveSelector)
        .evaluateAll((elements) =>
            elements.map((element) => {
                const input = element as HTMLInputElement;
                return [
                    element.textContent ?? '',
                    element.getAttribute('aria-label') ?? '',
                    element.getAttribute('title') ?? '',
                    input.placeholder ?? '',
                ]
                    .join(' ')
                    .replace(/\s+/g, ' ')
                    .trim();
            }),
        );

    return [
        ...new Set(candidates.filter(containsEnglishUserFacingMessage)),
    ];
};

export const expectChineseOperations = async (page: Page) => {
    const english = await visibleEnglishOperationTexts(page);
    expect(
        english,
        `发现可见英文操作、反馈或错误提示：${english.join(' | ')}`,
    ).toEqual([]);
};

export const waitForPrimaryPage = async (page: Page) => {
    await expect(page.getByRole('main')).toBeVisible({ timeout: 15_000 });
    await page.waitForLoadState('networkidle', { timeout: 15_000 }).catch(() => {});
    await expect(page.getByLabel('正在加载页面')).toHaveCount(0, {
        timeout: 15_000,
    });
};
