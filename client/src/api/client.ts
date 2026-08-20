/**
 * REST API の呼び出し口（`ApiDesign.md` 2章）。
 *
 * 型は `docs/openapi.yaml` から生成する（`npm run gen:api`。`Design.md` 3.3 が
 * 「コード生成は TypeScript クライアントのみ」と定める）。**生成するのは型だけ**で、
 * 呼び出しそのものはこの薄いラッパが持つ。2.4 の CSRF・2.5 のエラー形式・
 * Cookie の送出といった共通規約を1か所に集めるため。
 *
 * フィールドは snake_case のまま扱う（2.2）。変換層を挟まない。
 */
import type { components } from './schema'

/** API のベースパス（`ApiDesign.md` 2.1） */
const BASE_PATH = '/api/v1'

/** CSRF の Cookie とヘッダ（`ApiDesign.md` 2.4） */
const CSRF_COOKIE = 'pb_csrf'
const CSRF_HEADER = 'X-PB-CSRF'

/** 安全なメソッド。これ以外は CSRF トークンを付ける（サーバ側 5b と同じ裏返しの判定） */
const SAFE_METHODS = ['GET', 'HEAD', 'OPTIONS', 'TRACE']

export type ErrorCode = components['schemas']['ErrorCode']
export type ErrorDetail = components['schemas']['ErrorDetail']

/**
 * 2.5 のエラー形式を載せた例外。
 *
 * **`message` はサーバが返した日本語をそのまま使う。** フロントで文言を
 * 組み立てない（`ApiDesign.md` 2.5、`CLAUDE.md` の命名・形式の規約）。
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | 'network_error'
  readonly details: ErrorDetail[]
  readonly retryAfterSec?: number
  readonly requestId?: string

  constructor(init: {
    status: number
    code: ErrorCode | 'network_error'
    message: string
    details?: ErrorDetail[]
    retryAfterSec?: number
    requestId?: string
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.details = init.details ?? []
    this.retryAfterSec = init.retryAfterSec
    this.requestId = init.requestId
  }

  /** 入力欄に紐づけるための索引（2.5 の details） */
  detailFor(field: string): ErrorDetail | undefined {
    return this.details.find((d) => d.field === field)
  }
}

/**
 * 401 を受けたときに呼ぶ処理。セッションの失効を1か所で扱うために置く。
 *
 * ストアやルータをこのモジュールから import すると循環するため、
 * 起動時に `main.ts` から登録する。
 */
type UnauthorizedHandler = () => void
let onUnauthorized: UnauthorizedHandler = () => {}

export function setUnauthorizedHandler(handler: UnauthorizedHandler): void {
  onUnauthorized = handler
}

export interface RequestOptions {
  /**
   * 401 を「セッション失効」として扱わない。
   *
   * 起動時の `GET /me`（未ログインなら 401 が正常）で使う。ここで共通処理を
   * 走らせると、ログイン画面へ来ただけで失効の扱いになる。
   */
  allowUnauthenticated?: boolean

  /**
   * このリクエストにだけ載せるヘッダ。
   *
   * 今の用途は楽観ロックの `If-Match`（`ApiDesign.md` 2.8）ひとつである。
   * **`Content-Type` と `X-PB-CSRF` は共通処理が決めるので、ここで上書きしない**
   * （呼び出し側ごとに CSRF の付け方が変わると 2.4 の規約が崩れる）。
   */
  headers?: Record<string, string>
}

/** Cookie を読む。`pb_csrf` は HttpOnly ではない（2.4 の double submit のため） */
function readCookie(name: string): string | null {
  const prefix = `${name}=`
  for (const part of document.cookie.split('; ')) {
    if (part.startsWith(prefix)) return decodeURIComponent(part.slice(prefix.length))
  }
  return null
}

/** 応答本文から 2.5 のエラーを取り出す。形が違えばコードだけ埋めた既定を返す */
async function toApiError(res: Response): Promise<ApiError> {
  let body: components['schemas']['Error'] | undefined
  try {
    body = (await res.json()) as components['schemas']['Error']
  } catch {
    body = undefined
  }

  const err = body?.error
  if (!err || typeof err.message !== 'string') {
    // サーバが 2.5 の形式で返せなかった場合（プロキシが挟まる等）。
    // ここでしか文言を作らない。
    return new ApiError({
      status: res.status,
      code: 'internal_error',
      message: `サーバでエラーが発生しました（HTTP ${res.status}）`,
    })
  }
  return new ApiError({
    status: res.status,
    code: err.code,
    message: err.message,
    details: err.details,
    retryAfterSec: err.retry_after_sec,
    requestId: err.request_id,
  })
}

/**
 * API を1回呼ぶ。
 *
 * - Cookie を必ず送る（同一オリジン。`ApiDesign.md` 2.3）
 * - 状態変更系には `pb_csrf` の値を `X-PB-CSRF` に載せる（2.4）
 * - 失敗は必ず `ApiError` になる。呼び出し側で `res.ok` を見ない
 */
async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json; charset=utf-8'

  if (!SAFE_METHODS.includes(method)) {
    const csrf = readCookie(CSRF_COOKIE)
    if (csrf) headers[CSRF_HEADER] = csrf
  }

  // 呼び出し側のヘッダは最後に載せる。ただし共通規約（Content-Type・CSRF）は
  // 上で決めた値を残すため、既にあるキーは触らない。
  for (const [k, v] of Object.entries(options.headers ?? {})) {
    if (!(k in headers)) headers[k] = v
  }

  let res: Response
  try {
    res = await fetch(BASE_PATH + path, {
      method,
      headers,
      // 同一オリジンで配信する（Design.md 3.4）。既定でも送られるが明示する。
      credentials: 'same-origin',
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    // サーバまで届かなかった場合。2.5 の応答が無いので、ここだけは
    // 画面に出す文言をフロントが持つ。
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: 'サーバに接続できませんでした。ネットワークの状態を確認してください',
    })
  }

  if (res.status === 401 && !options.allowUnauthenticated) onUnauthorized()

  if (!res.ok) throw await toApiError(res)

  // 204 No Content（ログアウト）と本文なしの応答。
  if (res.status === 204 || res.headers.get('Content-Length') === '0') {
    return undefined as T
  }
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string, options?: RequestOptions) => request<T>('GET', path, undefined, options),
  post: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('POST', path, body, options),
  patch: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PATCH', path, body, options),
  put: <T>(path: string, body?: unknown, options?: RequestOptions) =>
    request<T>('PUT', path, body, options),
  // `delete` は予約語なので名前を変える。呼び出し側は `api.del(...)` と書く
  del: <T>(path: string, options?: RequestOptions) =>
    request<T>('DELETE', path, undefined, options),
}
