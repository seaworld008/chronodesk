import assert from 'node:assert/strict'
import test from 'node:test'
import { createServer } from 'vite'

const vite = await createServer({
  appType: 'custom',
  server: { hmr: false, middlewareMode: true },
})
const sidebar = await vite.ssrLoadModule('/src/layout/sidebarWidth.ts')

test.after(async () => {
  await vite.close()
})

test('侧栏宽度限制在可用范围并拒绝非法持久化值', () => {
  assert.equal(sidebar.clampSidebarWidth(100), sidebar.sidebarMinWidth)
  assert.equal(sidebar.clampSidebarWidth(999), sidebar.sidebarMaxWidth)
  assert.equal(
    sidebar.clampSidebarWidth(Number.NaN),
    sidebar.sidebarDefaultWidth,
  )

  const values = new Map()
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  }
  assert.equal(
    sidebar.loadSidebarWidth(storage, 'alice'),
    sidebar.sidebarDefaultWidth,
  )
  values.set(sidebar.sidebarWidthStorageKey('alice'), 'not-a-number')
  assert.equal(
    sidebar.loadSidebarWidth(storage, 'alice'),
    sidebar.sidebarDefaultWidth,
  )
  values.set(sidebar.sidebarWidthStorageKey('alice'), '999')
  assert.equal(
    sidebar.loadSidebarWidth(storage, 'alice'),
    sidebar.sidebarMaxWidth,
  )
})

test('宽度偏好按账号隔离并在写入时归一化', () => {
  const values = new Map()
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  }
  sidebar.saveSidebarWidth(storage, 'alice', 287.6)
  sidebar.saveSidebarWidth(storage, 'bob', 260)

  assert.equal(sidebar.loadSidebarWidth(storage, 'alice'), 288)
  assert.equal(sidebar.loadSidebarWidth(storage, 'bob'), 260)
  assert.notEqual(
    sidebar.sidebarWidthStorageKey('alice'),
    sidebar.sidebarWidthStorageKey('bob'),
  )
})

test('键盘调整支持方向键与 Home End 边界', () => {
  assert.equal(sidebar.keyboardSidebarWidth(240, 'ArrowLeft'), 232)
  assert.equal(sidebar.keyboardSidebarWidth(240, 'ArrowRight'), 248)
  assert.equal(sidebar.keyboardSidebarWidth(240, 'ArrowLeft', true), 216)
  assert.equal(sidebar.keyboardSidebarWidth(240, 'ArrowRight', true), 264)
  assert.equal(
    sidebar.keyboardSidebarWidth(240, 'Home'),
    sidebar.sidebarMinWidth,
  )
  assert.equal(
    sidebar.keyboardSidebarWidth(240, 'End'),
    sidebar.sidebarMaxWidth,
  )
  assert.equal(sidebar.keyboardSidebarWidth(240, 'Enter'), null)
  assert.equal(
    sidebar.keyboardSidebarWidth(sidebar.sidebarMinWidth, 'ArrowLeft'),
    sidebar.sidebarMinWidth,
  )
  assert.equal(
    sidebar.keyboardSidebarWidth(sidebar.sidebarMaxWidth, 'ArrowRight'),
    sidebar.sidebarMaxWidth,
  )
})
