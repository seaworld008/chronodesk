export const joinApiUrl = (base: string, path: string): string => {
  const normalizedBase = base.replace(/\/+$/, '')
  const normalizedPath = path.replace(/^\/+/, '')
  if (normalizedBase === '') return `/${normalizedPath}`
  if (normalizedPath === '') return normalizedBase
  return `${normalizedBase}/${normalizedPath}`
}
