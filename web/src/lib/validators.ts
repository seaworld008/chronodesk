import { ValidatorFunction } from 'ra-core'

const toLength = (value: unknown): number => {
  if (typeof value === 'number') {
    return value.toString().length
  }
  if (typeof value === 'string') {
    return Array.from(value.trim()).length
  }
  if (value === null || value === undefined) {
    return 0
  }
  return Array.from(String(value)).length
}

export const minCharacters = (min: number, message?: string): ValidatorFunction => (value) => {
  if (value === undefined || value === null || value === '') {
    return undefined
  }
  const length = toLength(value)
  if (length < min) {
    return message || `至少需要 ${min} 个字符`
  }
  return undefined
}

export const maxCharacters = (max: number, message?: string): ValidatorFunction => (value) => {
  if (value === undefined || value === null || value === '') {
    return undefined
  }
  const length = toLength(value)
  if (length > max) {
    return message || `不能超过 ${max} 个字符`
  }
  return undefined
}
