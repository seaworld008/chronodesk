let boundAccessToken: string | null =
    typeof window === 'undefined'
        ? null
        : window.localStorage.getItem('token')

export const bindHumanTabSession = (accessToken: string | null): void => {
    boundAccessToken = accessToken
}

export const humanTabSessionMatches = (
    accessToken: string | null,
): boolean => boundAccessToken === accessToken
