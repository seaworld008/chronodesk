import type { QueryClient } from '@tanstack/react-query'

export const humanAuthCheckQueryKey = [
    'auth',
    'checkAuth',
    {},
] as const

export const humanSessionStorageCommitKey = 'tokenExpiresAt'

export const markHumanAuthQueryAuthenticated = (
    queryClient: QueryClient,
): void => {
    void queryClient.cancelQueries({
        queryKey: ['auth', 'checkAuth'],
    })
    queryClient.setQueryData(humanAuthCheckQueryKey, true)
}
