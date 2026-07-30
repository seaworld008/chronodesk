import { expect, test } from '@playwright/test'
import { joinApiUrl } from '../src/lib/apiUrl'
import { humanApiRoutes } from '../src/lib/generated/human-api'

test('Human API route helpers preserve the leading slash and normalize joins', () => {
  const route = humanApiRoutes.listProjectTickets(
    { projectKey: 'OPS' },
    {
      page: 2,
      page_size: 25,
      search: '需要 编码',
    },
  )
  expect(route).toBe(
    '/projects/OPS/tickets?page=2&page_size=25&search=%E9%9C%80%E8%A6%81+%E7%BC%96%E7%A0%81',
  )
  const joined = joinApiUrl('/api/', route)
  expect(joined).toBe(
    '/api/projects/OPS/tickets?page=2&page_size=25&search=%E9%9C%80%E8%A6%81+%E7%BC%96%E7%A0%81',
  )
  expect(new URL(joined, 'http://chronodesk.invalid').pathname).not.toContain(
    '//',
  )
})

test('浏览器发出的真实请求路径不包含双斜杠', async ({ page }) => {
  const invalidRequests: string[] = []
  page.on('request', (request) => {
    const pathname = new URL(request.url()).pathname
    if (pathname.includes('//')) invalidRequests.push(pathname)
  })

  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')
  expect(invalidRequests).toEqual([])
})
