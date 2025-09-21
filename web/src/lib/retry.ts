import { AxiosError } from 'axios'

export interface RetryConfig {
  retries: number
  retryDelay: number
  retryCondition?: (error: AxiosError) => boolean
  onRetry?: (error: AxiosError, retryCount: number) => void
}

const defaultRetryConfig: RetryConfig = {
  retries: 3,
  retryDelay: 1000,
  retryCondition: (error: AxiosError) => {
    // 默认重试条件：网络错误或 5xx 服务器错误
    return (
      !error.response ||
      (error.response.status >= 500 && error.response.status <= 599) ||
      error.code === 'ECONNABORTED' ||
      error.code === 'NETWORK_ERROR'
    )
  },
}

/**
 * 指数退避延迟计算
 * @param retryCount 重试次数
 * @param baseDelay 基础延迟时间（毫秒）
 * @returns 延迟时间（毫秒）
 */
export const calculateExponentialBackoff = (
  retryCount: number,
  baseDelay: number = 1000
): number => {
  const jitter = Math.random() * 0.1 * baseDelay // 添加 10% 的随机抖动
  return Math.min(baseDelay * Math.pow(2, retryCount) + jitter, 30000) // 最大延迟 30 秒
}

/**
 * 重试函数
 * @param fn 要重试的异步函数
 * @param config 重试配置
 * @returns Promise
 */
export const withRetry = async <T>(
  fn: () => Promise<T>,
  config: Partial<RetryConfig> = {}
): Promise<T> => {
  const finalConfig = { ...defaultRetryConfig, ...config }
  let lastError: unknown

  for (let attempt = 0; attempt <= finalConfig.retries; attempt++) {
    try {
      return await fn()
    } catch (error) {
      lastError = error
      
      // 如果是最后一次尝试，直接抛出错误
      if (attempt === finalConfig.retries) {
        throw error
      }
      
      // 检查是否应该重试
      if (finalConfig.retryCondition && !finalConfig.retryCondition(error as AxiosError)) {
        throw error
      }
      
      // 调用重试回调
      if (finalConfig.onRetry) {
        finalConfig.onRetry(error as AxiosError, attempt + 1)
      }
      
      // 等待指定时间后重试
      const delay = calculateExponentialBackoff(attempt, finalConfig.retryDelay)
      await new Promise(resolve => setTimeout(resolve, delay))
      
      console.warn(`Request failed, retrying in ${delay}ms (attempt ${attempt + 1}/${finalConfig.retries})`, error)
    }
  }
  
  throw lastError
}

/**
 * 创建带重试功能的 API 调用函数
 * @param apiCall 原始 API 调用函数
 * @param retryConfig 重试配置
 * @returns 带重试功能的 API 调用函数
 */
export const createRetryableApiCall = <Args extends unknown[], Result>(
  apiCall: (...args: Args) => Promise<Result>,
  retryConfig?: Partial<RetryConfig>
): ((...args: Args) => Promise<Result>) => {
  return (...args: Args) => withRetry(() => apiCall(...args), retryConfig)
}

/**
 * 批量请求重试器
 * @param requests 请求数组
 * @param config 重试配置
 * @returns Promise<Array<T | Error>>
 */
export const retryBatch = async <T>(
  requests: Array<() => Promise<T>>,
  config: Partial<RetryConfig> = {}
): Promise<Array<T | Error>> => {
  const results = await Promise.allSettled(
    requests.map(request => withRetry(request, config))
  )
  
  return results.map(result => 
    result.status === 'fulfilled' ? result.value : result.reason
  )
}

/**
 * 并发限制的批量重试器
 * @param requests 请求数组
 * @param concurrency 并发数量
 * @param config 重试配置
 * @returns Promise<Array<T | Error>>
 */
export const retryBatchWithConcurrency = async <T>(
  requests: Array<() => Promise<T>>,
  concurrency: number = 5,
  config: Partial<RetryConfig> = {}
): Promise<Array<T | Error>> => {
  const results: Array<T | Error> = []
  
  for (let i = 0; i < requests.length; i += concurrency) {
    const batch = requests.slice(i, i + concurrency)
    const batchResults = await retryBatch(batch, config)
    results.push(...batchResults)
  }
  
  return results
}
