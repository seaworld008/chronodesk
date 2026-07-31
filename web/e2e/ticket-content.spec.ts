import { Buffer } from 'node:buffer';
import { expect, test } from '@playwright/test';
import {
    authenticatePage,
    createTicket,
    deleteTicket,
    E2E_MARKER,
    getAdminToken,
    markE2EAttachmentClean,
    projectAPIPath,
    resolveE2EProjectKey,
} from './helpers/testData';
import { assertDestructiveE2EAllowed } from './helpers/safety';
import { expectChineseOperations } from './helpers/browserAudit';

const ticketTitle = `${E2E_MARKER}评论附件工单`;
const publicComment = `${E2E_MARKER}公开评论`;
const internalComment = `${E2E_MARKER}内部处理记录`;
const fileName = `${E2E_MARKER}small.txt`;

test.describe('工单评论与附件真实交互', () => {
    let ticketID = 0;

    test.beforeAll(async ({ request }) => {
        assertDestructiveE2EAllowed('工单评论附件 E2E');
        ticketID = await createTicket(request, ticketTitle);
    });

    test.afterAll(async ({ request }) => {
        if (ticketID > 0) {
            await deleteTicket(request, ticketID);
        }
    });

    test('CNT-001 CNT-002 CNT-006 CNT-009 UI-011：评论、上传、扫描与下载', async ({
        page,
        request,
    }) => {
        const token = await getAdminToken(request);
        const projectKey = await resolveE2EProjectKey(request, token);
        const ticketPath = projectAPIPath(
            projectKey,
            `tickets/${ticketID}`,
        );
        const commentsPath = `${ticketPath}/comments`;
        const attachmentsPath = `${ticketPath}/attachments`;
        await authenticatePage(page);
        await page.goto(`/#/tickets/${ticketID}/show`);
        const main = page.getByRole('main');
        const commentsListResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'GET' &&
                new URL(response.url()).pathname === commentsPath,
        );
        const attachmentsListResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'GET' &&
                new URL(response.url()).pathname === attachmentsPath,
        );
        await main
            .getByRole('tab', { name: '评论历史', exact: true })
            .click();
        expect((await commentsListResponse).status()).toBe(200);
        expect((await attachmentsListResponse).status()).toBe(200);
        const commentFormRegion = main.getByRole('region', {
            name: '添加工单评论',
            exact: true,
        });
        await expect(commentFormRegion).toBeVisible({
            timeout: 15_000,
        });

        await commentFormRegion
            .getByLabel(/^评论内容/u)
            .fill(publicComment);
        const publicResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === commentsPath,
        );
        await commentFormRegion
            .getByRole('button', { name: '添加评论', exact: true })
            .click();
        expect((await publicResponse).status()).toBe(201);
        await expect(
            page.getByText('评论已添加', { exact: true }),
        ).toBeVisible();
        const commentRecordRegion = main.getByRole('region', {
            name: '工单评论记录',
            exact: true,
        });
        const publicItem = commentRecordRegion
            .getByRole('listitem')
            .filter({ hasText: publicComment });
        await expect(publicItem).toContainText('公开');

        await commentFormRegion
            .getByLabel('可见性', { exact: true })
            .click();
        await page.getByRole('option', { name: '内部评论' }).click();
        await commentFormRegion
            .getByLabel(/^评论内容/u)
            .fill(internalComment);
        const internalResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === commentsPath,
        );
        await commentFormRegion
            .getByRole('button', { name: '添加评论', exact: true })
            .click();
        expect((await internalResponse).status()).toBe(201);
        const internalItem = commentRecordRegion
            .getByRole('listitem')
            .filter({ hasText: internalComment });
        await expect(internalItem).toContainText('内部');

        const attachmentRegion = main.getByRole('region', {
            name: '工单附件',
            exact: true,
        });
        await attachmentRegion.getByLabel('选择附件').setInputFiles({
            name: fileName,
            mimeType: 'text/plain',
            buffer: Buffer.from('ChronoDesk Playwright E2E attachment\n', 'utf8'),
        });
        await expect(
            attachmentRegion.getByText(fileName, { exact: true }),
        ).toBeVisible();
        const uploadResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'POST' &&
                new URL(response.url()).pathname === attachmentsPath,
        );
        await attachmentRegion
            .getByRole('button', { name: '上传附件', exact: true })
            .click();
        expect((await uploadResponse).status()).toBe(202);
        await expect(
            page.getByText('附件已上传，等待安全扫描', { exact: true }),
        ).toBeVisible();
        await expectChineseOperations(page);

        const attachmentItem = attachmentRegion
            .getByRole('listitem')
            .filter({ hasText: fileName });
        await expect(attachmentItem).toContainText('待安全扫描');
        await expect(attachmentItem).toContainText('SHA-256');
        await expect(
            attachmentItem.getByRole('button', { name: '下载' }),
        ).toBeDisabled();

        const attachmentID = await markE2EAttachmentClean(request, fileName);
        await page.reload();
        await main
            .getByRole('tab', { name: '评论历史', exact: true })
            .click();
        const cleanAttachment = attachmentRegion
            .getByRole('listitem')
            .filter({ hasText: fileName });
        await expect(cleanAttachment).toContainText('扫描通过', {
            timeout: 15_000,
        });
        const downloadButton = cleanAttachment.getByRole('button', {
            name: '下载',
        });
        await expect(downloadButton).toBeEnabled();

        const downloadPromise = page.waitForEvent('download');
        const downloadResponse = page.waitForResponse(
            (response) =>
                response.request().method() === 'GET' &&
                new URL(response.url()).pathname ===
                    `${attachmentsPath}/${attachmentID}/content`,
        );
        await downloadButton.click();
        const [download, downloaded] = await Promise.all([
            downloadPromise,
            downloadResponse,
        ]);
        expect(downloaded.status()).toBe(200);
        expect(download.suggestedFilename()).toBe(fileName);
    });
});
