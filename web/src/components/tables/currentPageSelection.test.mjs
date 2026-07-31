import assert from 'node:assert/strict'
import test from 'node:test'

import {
    currentPageSelectionState,
    updateCurrentPageSelection,
} from './currentPageSelection.ts'

test('当前页部分选中显示 indeterminate', () => {
    assert.deepEqual(currentPageSelectionState([1, 2, 3], [2, 99]), {
        selectedOnCurrentPage: 1,
        allSelected: false,
        indeterminate: true,
    })
})

test('全选与取消仅改变当前页并保留其他页选择', () => {
    assert.deepEqual(updateCurrentPageSelection([99], [1, 2], true), [99, 1, 2])
    assert.deepEqual(updateCurrentPageSelection([99, 1, 2], [1, 2], false), [99])
})
