import assert from 'node:assert/strict'
import test from 'node:test'

import { focusMainContent } from './skipNavigation.ts'

test('跳过导航将焦点留在主要内容并在离开时清理临时 tabindex', () => {
    let blurHandler
    const attributes = new Map()
    const mainContent = {
        addEventListener(type, handler, options) {
            assert.equal(type, 'blur')
            assert.deepEqual(options, { once: true })
            blurHandler = handler
        },
        focus() {
            this.focused = true
        },
        removeAttribute(name) {
            attributes.delete(name)
        },
        setAttribute(name, value) {
            attributes.set(name, value)
        },
    }
    const previousDocument = globalThis.document
    globalThis.document = {
        getElementById(id) {
            assert.equal(id, 'main-content')
            return mainContent
        },
    }

    try {
        focusMainContent()
        assert.equal(mainContent.focused, true)
        assert.equal(attributes.get('tabindex'), '-1')

        blurHandler()
        assert.equal(attributes.has('tabindex'), false)
    } finally {
        if (previousDocument === undefined) {
            delete globalThis.document
        } else {
            globalThis.document = previousDocument
        }
    }
})
