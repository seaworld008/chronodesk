export const humanSessionChannelName = 'chronodesk:human-session:v1'

export type HumanSessionMetadata =
    | {
          type: 'authenticated'
          subject: string
          session_id: string
          expires_at: number
          issued_at: number
      }
    | {
          type: 'signed_out'
          issued_at: number
      }

type HumanSessionMetadataListener = (
    metadata: HumanSessionMetadata,
) => void

let channel: BroadcastChannel | null = null

const positiveNumber = (value: unknown): value is number =>
    typeof value === 'number' && Number.isFinite(value) && value > 0

const nonEmptyString = (value: unknown): value is string =>
    typeof value === 'string' && value.length > 0

const parseHumanSessionMetadata = (
    value: unknown,
): HumanSessionMetadata | null => {
    if (typeof value !== 'object' || value === null || !('type' in value)) {
        return null
    }
    if (
        value.type === 'signed_out' &&
        'issued_at' in value &&
        positiveNumber(value.issued_at)
    ) {
        return {
            type: 'signed_out',
            issued_at: value.issued_at,
        }
    }
    if (
        value.type === 'authenticated' &&
        'subject' in value &&
        nonEmptyString(value.subject) &&
        'session_id' in value &&
        nonEmptyString(value.session_id) &&
        'expires_at' in value &&
        positiveNumber(value.expires_at) &&
        'issued_at' in value &&
        positiveNumber(value.issued_at)
    ) {
        return {
            type: 'authenticated',
            subject: value.subject,
            session_id: value.session_id,
            expires_at: value.expires_at,
            issued_at: value.issued_at,
        }
    }
    return null
}

const getChannel = (): BroadcastChannel | null => {
    if (
        typeof window === 'undefined' ||
        typeof window.BroadcastChannel === 'undefined'
    ) {
        return null
    }
    channel ??= new window.BroadcastChannel(humanSessionChannelName)
    return channel
}

export const publishAuthenticatedHumanSession = (
    metadata: Omit<
        Extract<HumanSessionMetadata, { type: 'authenticated' }>,
        'type' | 'issued_at'
    >,
): void => {
    getChannel()?.postMessage({
        type: 'authenticated',
        ...metadata,
        issued_at: Date.now(),
    } satisfies HumanSessionMetadata)
}

export const publishSignedOutHumanSession = (): void => {
    getChannel()?.postMessage({
        type: 'signed_out',
        issued_at: Date.now(),
    } satisfies HumanSessionMetadata)
}

export const subscribeHumanSessionMetadata = (
    listener: HumanSessionMetadataListener,
): (() => void) => {
    const activeChannel = getChannel()
    if (!activeChannel) return () => undefined

    const handleMessage = (event: MessageEvent<unknown>) => {
        const metadata = parseHumanSessionMetadata(event.data)
        if (metadata) listener(metadata)
    }
    activeChannel.addEventListener('message', handleMessage)
    return () => {
        activeChannel.removeEventListener('message', handleMessage)
    }
}
