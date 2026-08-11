import type { ApiResponse } from './types'

/** CSRF token,仅存 React 内存状态 */
let csrfToken: string | null = null

/** 全局 401 回调(由 AuthProvider 注册,避免循环 import) */
let unauthorizedHandler: (() => void) | null = null

/** 注册全局 401 回调 */
export function registerUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

/** 设置内存 CSRF token(由 AuthProvider 管理) */
export function setCSRFToken(token: string | null): void {
  csrfToken = token
}

/** ApiError 携带 HTTP 状态与稳定错误码 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
  signal?: AbortSignal
}

/**
 * 唯一的 fetch 入口。
 *
 * 统一设置 Accept、JSON Content-Type 与 credentials: same-origin;
 * 非 GET/HEAD/OPTIONS 自动携带 X-CSRF-Token;401 触发 onUnauthorized 回调。
 */
export async function request<T>(
  path: string,
  init?: RequestOptions,
  onUnauthorized?: () => void,
): Promise<T> {
  const headers = new Headers(init?.headers)
  headers.set('Accept', 'application/json')
  headers.set('Content-Type', 'application/json')

  const method = (init?.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  let body: BodyInit | undefined
  if (init?.body !== undefined) {
    body = typeof init.body === 'string' ? init.body : JSON.stringify(init.body)
  }

  let resp: Response
  try {
    resp = await fetch(path, {
      ...init,
      method,
      headers,
      body,
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError(0, 'NETWORK_ERROR', '网络连接失败，请检查服务状态')
  }

  let payload: ApiResponse<T>
  try {
    payload = (await resp.json()) as ApiResponse<T>
  } catch {
    throw new ApiError(resp.status, 'INVALID_RESPONSE', '网络连接失败，请检查服务状态')
  }

  // 只有管理会话失效才退出后台。iCloud 上游登录失败、OTP 错误等也可能
  // 返回 401，但这些错误应留在当前弹窗中显示，不能误清管理员会话。
  if (resp.status === 401 && payload.success === false && payload.code === 'AUTH_REQUIRED') {
    onUnauthorized?.()
    unauthorizedHandler?.()
  }

  if (!resp.ok || payload.success === false) {
    throw new ApiError(
      resp.status,
      payload.code ?? 'INTERNAL_ERROR',
      payload.message ?? '请求失败',
    )
  }
  return payload.data as T
}
