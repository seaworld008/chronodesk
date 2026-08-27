export const humanSessionLifecycleLockName =
    'chronodesk:human-session-lifecycle:v1'

export const withHumanSessionLifecycleLock = async <T>(
    operation: () => Promise<T>,
): Promise<T> => {
    if (
        typeof navigator === 'undefined' ||
        navigator.locks === undefined
    ) {
        throw new Error(
            '当前浏览器不支持安全的多标签页登录协调，请升级浏览器后重试',
        )
    }
    return navigator.locks.request(
        humanSessionLifecycleLockName,
        { mode: 'exclusive' },
        operation,
    )
}
