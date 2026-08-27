let accessToken: string | null = null
let generation = 0
let committedAt = 0
const staleHumanSessionResponses = new WeakSet<object>()

export type HumanAccessTokenSnapshot = Readonly<{
    accessToken: string
    generation: number
}>

export const humanSessionClockNow = (): number => {
    if (
        typeof performance !== 'undefined' &&
        Number.isFinite(performance.timeOrigin) &&
        Number.isFinite(performance.now())
    ) {
        return performance.timeOrigin + performance.now()
    }
    return Date.now()
}

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
if (accessToken !== null) {
    committedAt = humanSessionClockNow()
}

export const readHumanAccessToken = (): string | null => {
    // The cutover is deliberately one-shot. Late writes to legacy keys are
    // purged but never promoted into the live session.
    consumeLegacyPersistedBearer()
    return accessToken
}

export const commitHumanAccessToken = (token: string): number => {
    accessToken = token
    generation += 1
    committedAt = humanSessionClockNow()
    return generation
}

export const clearHumanAccessToken = (): number => {
    accessToken = null
    generation += 1
    committedAt = 0
    return generation
}

export const humanSessionGeneration = (): number => generation

export const humanSessionCommittedAt = (): number => committedAt

export const captureHumanAccessTokenSnapshot = (
    token: string | null,
): HumanAccessTokenSnapshot | null =>
    token === null || token.length === 0
        ? null
        : {
              accessToken: token,
              generation,
          }

export const humanAccessTokenSnapshotIsCurrent = (
    snapshot: HumanAccessTokenSnapshot | null,
): boolean =>
    snapshot !== null &&
    generation === snapshot.generation &&
    accessToken === snapshot.accessToken

export const markStaleHumanSessionResponse = <T extends object>(
    error: T,
): T => {
    staleHumanSessionResponses.add(error)
    return error
}

export const isStaleHumanSessionResponse = (
    error: unknown,
): boolean =>
    typeof error === 'object' &&
    error !== null &&
    staleHumanSessionResponses.has(error)
