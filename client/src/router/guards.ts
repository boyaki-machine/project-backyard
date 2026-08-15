/**
 * ルーターガード（`GuiDesign.md` 7.2）。
 *
 *   1. 未認証 かつ 保護ルート        → /login?redirect=<元のパス>
 *   2. :key のプロジェクトに到達できない → /404（403 ではない）
 *   3. meta.permission を持たない      → /403
 *   4. 通過
 *
 * **判定2を判定3より先に置く。** 逆にすると非メンバーのプロジェクトで
 * 「権限が無い」が先に当たって 403 になり、存在を隠す意味が失われる。
 * サーバ側 `RequireProjectPermission` も同じ順序である（`Design.md` 6.4.5）。
 *
 * ここでの制御は利便性のためのものであり、セキュリティ境界ではない（7.3）。
 */
import type { NavigationGuardWithThis, RouteLocationNormalized } from 'vue-router'

import { useAuthStore } from '../stores/auth'

/** ログイン後に戻る先。未認証で保護ページへ来たときに保持する（`GuiDesign.md` 5.1） */
export const REDIRECT_QUERY = 'redirect'

export const authGuard: NavigationGuardWithThis<undefined> = async (to) => {
  const auth = useAuthStore()

  // Cookie は HttpOnly のため、GET /me を1度通すまで認証状態が分からない。
  await auth.restore()

  if (!auth.isAuthenticated) {
    if (to.meta.public === true) return true
    return { path: '/login', query: { [REDIRECT_QUERY]: to.fullPath } }
  }

  // 認証済みでログイン画面へ来たら初期画面へ送る（`GuiDesign.md` 3.1）。
  if (to.path === '/login') return { path: '/projects' }

  const key = projectKey(to)
  if (key !== null && !auth.canReachProject(key)) return { path: '/404' }

  const permission = to.meta.permission
  if (permission !== undefined) {
    const granted = key !== null ? auth.canInProject(key, permission) : auth.can(permission)
    if (!granted) return { path: '/403' }
  }

  return true
}

/** ルートの `:key`（`GuiDesign.md` 3.2 の `/p/:key/...`）を取り出す */
function projectKey(to: RouteLocationNormalized): string | null {
  const key = to.params.key
  if (typeof key === 'string' && key !== '') return key
  return null
}

/**
 * `?redirect=` の値を遷移先として使ってよいか。
 *
 * 同一オリジン内の絶対パスだけを許す。`//example.com` は
 * プロトコル相対URLとして外部サイトへ飛ぶため弾く。
 */
export function safeRedirect(value: unknown): string | null {
  if (typeof value !== 'string') return null
  if (!value.startsWith('/') || value.startsWith('//')) return null
  return value
}
