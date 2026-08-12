export interface SidebarWidthStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export const sidebarClosedWidth = 56
export const sidebarDefaultWidth = 240
export const sidebarMinWidth = 216
export const sidebarMaxWidth = 360
export const sidebarKeyboardStep = 8
export const sidebarKeyboardLargeStep = 24

const sidebarWidthStorageVersion = 1
const sidebarWidthStoragePrefix =
  `chronodesk.sidebar-width.v${sidebarWidthStorageVersion}`

export const sidebarWidthStorageKey = (subject: string): string =>
  `${sidebarWidthStoragePrefix}.${encodeURIComponent(subject)}`

export const clampSidebarWidth = (value: number): number => {
  if (!Number.isFinite(value)) return sidebarDefaultWidth
  return Math.min(
    sidebarMaxWidth,
    Math.max(sidebarMinWidth, Math.round(value)),
  )
}

export const loadSidebarWidth = (
  storage: SidebarWidthStorage,
  subject: string,
): number => {
  try {
    const serialized = storage.getItem(sidebarWidthStorageKey(subject))
    if (serialized === null || serialized.trim() === '') {
      return sidebarDefaultWidth
    }
    const parsed = Number(serialized)
    if (!Number.isFinite(parsed)) return sidebarDefaultWidth
    return clampSidebarWidth(parsed)
  } catch {
    return sidebarDefaultWidth
  }
}

export const saveSidebarWidth = (
  storage: SidebarWidthStorage,
  subject: string,
  width: number,
): void => {
  try {
    storage.setItem(
      sidebarWidthStorageKey(subject),
      String(clampSidebarWidth(width)),
    )
  } catch {
    // Layout persistence is a progressive enhancement.
  }
}

export const keyboardSidebarWidth = (
  width: number,
  key: string,
  largeStep = false,
): number | null => {
  const step = largeStep
    ? sidebarKeyboardLargeStep
    : sidebarKeyboardStep

  switch (key) {
    case 'ArrowLeft':
      return clampSidebarWidth(width - step)
    case 'ArrowRight':
      return clampSidebarWidth(width + step)
    case 'Home':
      return sidebarMinWidth
    case 'End':
      return sidebarMaxWidth
    default:
      return null
  }
}
