import assert from 'node:assert/strict'
import test from 'node:test'

import {
    KNOWLEDGE_MARKDOWN_MAX_BYTES,
    extractMarkdownHeadings,
    knowledgeMarkdownByteLength,
} from './knowledgeMarkdown.ts'

test('知识 Markdown 目录忽略代码块并稳定处理重复中文标题', () => {
    assert.deepEqual(
        extractMarkdownHeadings(`
# 数据库恢复
## 现象
\`\`\`markdown
## 这不是标题
\`\`\`
## 解决步骤
## 解决步骤
`),
        [
            { id: '数据库恢复', title: '数据库恢复', level: 1 },
            { id: '现象', title: '现象', level: 2 },
            { id: '解决步骤', title: '解决步骤', level: 2 },
            { id: '解决步骤-2', title: '解决步骤', level: 2 },
        ],
    )
})

test('知识 Markdown 使用 UTF-8 字节数校验 128 KiB 上限', () => {
    assert.equal(KNOWLEDGE_MARKDOWN_MAX_BYTES, 131_072)
    assert.equal(knowledgeMarkdownByteLength('abc'), 3)
    assert.equal(knowledgeMarkdownByteLength('知识'), 6)
    assert.equal(
        knowledgeMarkdownByteLength('知'.repeat(43_691)),
        131_073,
    )
})
