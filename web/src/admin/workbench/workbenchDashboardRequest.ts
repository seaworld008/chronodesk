export type DashboardDays = 7 | 30 | 90

const projectKeyPattern = /^[A-Z][A-Z0-9_-]{0,31}$/u
const allowedQueryKeys = new Set(['days', 'project_keys'])

export type WorkbenchDashboardURLRequest = {
  days: DashboardDays | null
  projectKeys: string[] | null
  requestKey: string | null
  error: string
}

export const parseWorkbenchDashboardURLRequest = (
  serializedSearch: string,
): WorkbenchDashboardURLRequest => {
  const search = new URLSearchParams(serializedSearch)
  for (const key of search.keys()) {
    if (!allowedQueryKeys.has(key)) {
      return invalidDashboardRequest('链接包含不支持的查询参数')
    }
  }

  const rawDays = search.getAll('days')
  let days: DashboardDays = 30
  if (rawDays.length > 0) {
    if (
      rawDays.length !== 1 ||
      !['7', '30', '90'].includes(rawDays[0])
    ) {
      return invalidDashboardRequest('统计周期链接无效，请选择 7、30 或 90 天')
    }
    days = Number(rawDays[0]) as DashboardDays
  }

  const rawProjectKeys = search.getAll('project_keys')
  if (
    rawProjectKeys.some((key) =>
      key.length === 0 ||
      key !== key.trim() ||
      !projectKeyPattern.test(key),
    )
  ) {
    return invalidDashboardRequest('项目筛选链接无效，请重新选择项目', days)
  }
  const uniqueProjectKeys = new Set(rawProjectKeys)
  if (
    uniqueProjectKeys.size !== rawProjectKeys.length ||
    rawProjectKeys.length > 500
  ) {
    return invalidDashboardRequest('项目筛选链接包含重复项或超出安全上限', days)
  }

  const projectKeys = rawProjectKeys.length === 0
    ? null
    : [...rawProjectKeys].sort()
  return {
    days,
    projectKeys,
    requestKey: JSON.stringify({
      days,
      project_keys: projectKeys ?? '*',
    }),
    error: '',
  }
}

const invalidDashboardRequest = (
  error: string,
  days: DashboardDays | null = null,
): WorkbenchDashboardURLRequest => ({
  days,
  projectKeys: null,
  requestKey: null,
  error,
})
