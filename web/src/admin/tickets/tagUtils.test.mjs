import assert from 'node:assert/strict'
import test from 'node:test'

import {
    normalizeTagList,
    normalizeTagsForSubmit,
    validateTagsInput,
} from './tagUtils.ts'

test('标签会去除空白并按大小写不敏感去重', () => {
    assert.deepEqual(
        normalizeTagList(['  Urgent ', 'urgent', '客户', '']),
        ['Urgent', '客户'],
    )
})

test('旧逗号文本只用于读取兼容且提交保持 string 数组', () => {
    assert.deepEqual(normalizeTagsForSubmit(' one, TWO, two '), ['one', 'TWO'])
})

test('标签数量与 Unicode 字符长度给出中文校验', () => {
    assert.equal(
        validateTagsInput(Array.from({ length: 21 }, (_, index) => `tag-${index}`)),
        '标签最多 20 个',
    )
    assert.equal(validateTagsInput(['中'.repeat(50)]), undefined)
    assert.equal(
        validateTagsInput(['中'.repeat(51)]),
        '每个标签不能超过 50 个字符',
    )
    assert.throws(
        () => normalizeTagsForSubmit(['中'.repeat(51)]),
        /每个标签不能超过 50 个字符/,
    )
})
