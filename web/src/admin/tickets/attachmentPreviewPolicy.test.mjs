import assert from 'node:assert/strict'
import test from 'node:test'

import {
    ATTACHMENT_PREVIEW_LIMITS,
    getAttachmentPreviewDecision,
    isPreviewResponseMimeCompatible,
    normalizeAttachmentMimeType,
} from './attachmentPreviewPolicy.ts'

const attachment = (overrides = {}) => ({
    id: 1,
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    ticket_id: 1,
    original_name: 'evidence.txt',
    file_size: 128,
    mime_type: 'text/plain',
    file_type: 'document',
    extension: '.txt',
    is_public: false,
    virus_scan: 'clean',
    ...overrides,
})

test('只有扫描通过且在大小上限内的白名单类型可预览', () => {
    assert.equal(
        getAttachmentPreviewDecision(
            attachment({ virus_scan: 'pending' }),
        ).eligible,
        false,
    )
    assert.equal(
        getAttachmentPreviewDecision(
            attachment({
                mime_type: 'image/png',
                file_type: 'image',
                extension: '.png',
                file_size: ATTACHMENT_PREVIEW_LIMITS.image,
            }),
        ).eligible,
        true,
    )
    assert.match(
        getAttachmentPreviewDecision(
            attachment({
                mime_type: 'image/png',
                file_size: ATTACHMENT_PREVIEW_LIMITS.image + 1,
            }),
        ).reason,
        /仅支持下载/u,
    )
})

test('SVG、HTML、Office 与未知二进制文件 fail closed', () => {
    for (const mime_type of [
        'image/svg+xml',
        'text/html',
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
        'application/octet-stream',
    ]) {
        const decision = getAttachmentPreviewDecision(
            attachment({ mime_type }),
        )
        assert.equal(decision.eligible, false)
        assert.match(decision.reason, /仅支持下载/u)
    }
})

test('Markdown 只允许安全文本 MIME 并按 1 MiB 限制', () => {
    const markdown = getAttachmentPreviewDecision(
        attachment({
            original_name: 'runbook.md',
            extension: '.md',
            file_size: ATTACHMENT_PREVIEW_LIMITS.text,
        }),
    )
    assert.equal(markdown.eligible, true)
    assert.equal(markdown.kind, 'markdown')
    assert.equal(
        getAttachmentPreviewDecision(
            attachment({
                original_name: 'runbook.md',
                extension: '.md',
                mime_type: 'application/octet-stream',
            }),
        ).eligible,
        false,
    )
})

test('历史附件缺少 extension 时从安全文件名回退且不使页面崩溃', () => {
    const legacyMarkdown = getAttachmentPreviewDecision(
        attachment({
            original_name: 'legacy-runbook.md',
            extension: undefined,
        }),
    )
    assert.equal(legacyMarkdown.eligible, true)
    assert.equal(legacyMarkdown.kind, 'markdown')

    const unnamed = getAttachmentPreviewDecision(
        attachment({
            original_name: undefined,
            extension: undefined,
        }),
    )
    assert.equal(unnamed.eligible, true)
    assert.equal(unnamed.kind, 'text')
})

test('响应 MIME 必须与元数据预览类型兼容', () => {
    const image = getAttachmentPreviewDecision(
        attachment({ mime_type: 'image/png' }),
    )
    assert.equal(
        isPreviewResponseMimeCompatible(image, 'image/png; charset=binary'),
        true,
    )
    assert.equal(isPreviewResponseMimeCompatible(image, 'text/html'), false)

    const markdown = getAttachmentPreviewDecision(
        attachment({ original_name: 'notes.md', extension: '.md' }),
    )
    assert.equal(isPreviewResponseMimeCompatible(markdown, 'text/markdown'), true)
    assert.equal(isPreviewResponseMimeCompatible(markdown, null), false)
    assert.equal(normalizeAttachmentMimeType(' Text/Plain; charset=UTF-8 '), 'text/plain')
})
