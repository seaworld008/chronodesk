import { humanSessionClockNow } from './humanSessionRuntime.ts'

export const humanSessionChannelName = 'chronodesk:human-session:v2'

export type HumanSessionSignOut =
    | {
          scope: 'current_session'
          subject: string
          session_id: string
      }
    | {
          scope: 'all_devices'
          subject: string
      }

export type HumanSessionMetadata =
    | {
          type: 'authenticated'
          subject: string
          session_id: string
          expires_at: number
          issued_at: number
      }
    | ({
          type: 'signed_out'
          issued_at: number
      } & HumanSessionSignOut)

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
    if (value.type === 'signed_out') {
        if (
            !('issued_at' in value) ||
            !positiveNumber(value.issued_at) ||
            !('scope' in value) ||
            !('subject' in value) ||
            !nonEmptyString(value.subject)
        ) {
            return null
        }
        if (
            value.scope === 'current_session' &&
            'session_id' in value &&
            nonEmptyString(value.session_id)
        ) {
            return {
                type: 'signed_out',
                scope: 'current_session',
                subject: value.subject,
                session_id: value.session_id,
                issued_at: value.issued_at,
            }
        }
        if (value.scope === 'all_devices') {
            return {
                type: 'signed_out',
                scope: 'all_devices',
                subject: value.subject,
                issued_at: value.issued_at,
            }
        }
        return null
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
        issued_at: humanSessionClockNow(),
    } satisfies HumanSessionMetadata)
}

export const publishSignedOutHumanSession = (
    signOut: HumanSessionSignOut,
): void => {
    getChannel()?.postMessage({
        type: 'signed_out',
        ...signOut,
        issued_at: humanSessionClockNow(),
    } satisfies HumanSessionMetadata)
}

export const humanSessionSignOutMatchesBinding = (
    metadata: Extract<HumanSessionMetadata, { type: 'signed_out' }>,
    binding: {
        subject: string
        session_id: string
    } | null,
    committedAt: number,
): boolean =>
    binding !== null &&
    binding.subject === metadata.subject &&
    (
        metadata.scope === 'all_devices'
            ? metadata.issued_at >= committedAt
            : binding.session_id === metadata.session_id
    )

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
