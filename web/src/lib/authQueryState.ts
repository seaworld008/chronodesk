import type { QueryClient } from '@tanstack/react-query'

export const humanAuthCheckQueryKey = [
    'auth',
    'checkAuth',
    {},
] as const

export const humanSessionStorageCommitKey = 'token'

export const markHumanAuthQueryAuthenticated = (
    queryClient: QueryClient,
): void => {
    void queryClient.cancelQueries({
        queryKey: ['auth', 'checkAuth'],
    })
    queryClient.setQueryData(humanAuthCheckQueryKey, true)
}
