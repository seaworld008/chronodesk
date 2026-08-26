let accessToken: string | null = null
let generation = 0

const consumeLegacyPersistedBearer = (): string | null => {
    if (typeof window === 'undefined') return null
    // One-time cutover: adopt the access token already loaded by the legacy
    // application, then remove both bearer tokens from browser persistence.
    const legacyAccessToken = window.localStorage.getItem('token')
    window.localStorage.removeItem('token')
    window.localStorage.removeItem('refreshToken')
    return legacyAccessToken
}

accessToken = consumeLegacyPersistedBearer()

export const readHumanAccessToken = (): string | null => {
    // The cutover is deliberately one-shot. Late writes to legacy keys are
    // purged but never promoted into the live session.
    consumeLegacyPersistedBearer()
    return accessToken
}

export const commitHumanAccessToken = (token: string): number => {
    accessToken = token
    generation += 1
    return generation
}

export const clearHumanAccessToken = (): number => {
    accessToken = null
    generation += 1
    return generation
}

export const humanSessionGeneration = (): number => generation
