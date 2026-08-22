/**
 * 現在のアクターと実効権限（`GuiDesign.md` 7.1 の auth ストア）。
 *
 * 構築元は `GET /api/v1/me` の応答ただ1つである（`Design.md` 6.4.4）。
 * ログイン応答も同じ形を返すため（`ApiDesign.md` 3.1）、そのまま流し込める。
 *
 * **ここでの判定は利便性のためのものであり、セキュリティ境界ではない**
 * （`GuiDesign.md` 7.3）。同じ判定はサーバ側でも必ず行われる。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import * as authApi from '../api/auth'
import type { Session, SessionProject } from '../api/auth'
import { ApiError } from '../api/client'
import { useUiStore } from './ui'

export const useAuthStore = defineStore('auth', () => {
  /** null は未認証。`GET /me` を試すまでは分からない（Cookie は HttpOnly のため読めない） */
  const session = ref<Session | null>(null)

  /** 起動時の復元が済んだか。ルーターガードはこれが立つまで判定できない */
  const loaded = ref(false)

  /** 復元中の呼び出しを1本にまとめる（ガードは遷移のたびに走る） */
  let restoring: Promise<void> | null = null

  const isAuthenticated = computed(() => session.value !== null)
  const actor = computed(() => session.value?.actor ?? null)

  /** システムロール由来の実効権限（`ApiDesign.md` 4.1） */
  const permissions = computed(() => session.value?.permissions ?? [])

  /** 所属プロジェクトと、そこでの実効権限 */
  const projects = computed<SessionProject[]>(() => session.value?.projects ?? [])

  const isAdministrator = computed(() => actor.value?.system_role === 'administrator')

  /**
   * 要パスワード変更か（`ApiDesign.md` 3.1）。
   *
   * 立っている間はルーターガードが `/me` から出さない（`GuiDesign.md` 5.8）。
   * **未認証のときは false。** ログイン画面まで巻き戻す判定はガード側の
   * 別の分岐が持っており、ここで真を返すと `/login` へも行けなくなる。
   */
  const mustChangePassword = computed(() => actor.value?.must_change_password === true)

  function projectByKey(key: string): SessionProject | undefined {
    return projects.value.find((p) => p.key === key)
  }

  /**
   * システムロール層の権限を持つか。
   *
   * プロジェクト配下の画面では `canInProject` を使う（`Design.md` 6.4.1 の三層構造）。
   */
  function can(permission: string): boolean {
    return permissions.value.includes(permission)
  }

  /** 当該プロジェクトでの実効権限を持つか（システムロール ∪ プロジェクトロール） */
  function canInProject(key: string, permission: string): boolean {
    const p = projectByKey(key)
    if (p) return p.permissions.includes(permission)
    // 非メンバーのアドミニストレータはシステムロール層だけで判定する。
    return can(permission)
  }

  /**
   * そのプロジェクトに到達してよいか（`GuiDesign.md` 7.2 手順2）。
   *
   * サーバ側 `RequireProjectPermission` と同じ規則にする（`Design.md` 6.4.5）。
   * 非メンバーかつ非アドミニストレータなら 404 相当であり、**403 にはしない**
   * （存在そのものを漏らさないため）。
   */
  function canReachProject(key: string): boolean {
    return isAdministrator.value || projectByKey(key) !== undefined
  }

  /**
   * セッションを差し替える。
   *
   * **`PATCH /me` の応答をそのまま渡せる**（`ApiDesign.md` 4.2 が `GET /me` と
   * 同一構造を返す）。表示名やテーマを変えた直後に、メニューのアバター名や
   * 権限の出し分けが古い値のまま残ることを防ぐ。
   *
   * **見た目の設定をここで `ui` ストアへ流す**（`GuiDesign.md` 8.11）。
   * セッションが入ってくる口はログイン・復元・再取得・`PATCH /me` の4つ
   * あり、そのすべてでサーバ側の値を正としたい。呼び出し側に任せると、
   * どれか1つで書き忘れて「別の端末で変えたテーマが効かない」が起きる。
   */
  function setSession(next: Session): void {
    session.value = next
    loaded.value = true
    useUiStore().syncFromServer(next.actor.theme, next.actor.hue)
  }

  /** ストアを未認証の状態へ戻す。画面遷移はしない（呼び出し側の責務） */
  function clear(): void {
    session.value = null
    loaded.value = true
  }

  /**
   * 起動時・リロード時にセッションを復元する。
   *
   * Cookie は HttpOnly のため、フロントは `GET /me` を叩くまで認証状態を
   * 知り得ない。401 は「未ログイン」であって異常ではない。
   */
  function restore(): Promise<void> {
    if (loaded.value) return Promise.resolve()
    if (restoring) return restoring

    restoring = authApi
      .me()
      .then((s) => setSession(s))
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) {
          clear()
          return
        }
        // 通信不能やサーバエラー。認証済みとして扱えないので未認証に倒す。
        // ログイン画面まで進めば、そこで同じエラーが利用者に見える。
        clear()
      })
      .finally(() => {
        restoring = null
      })
    return restoring
  }

  /**
   * `GET /me` を取り直す。
   *
   * `restore` と違い、復元済みでも必ず問い合わせる。プロジェクトを作ると
   * 作成者が `project_admin` として `project_member` に入る（`ApiDesign.md` 5.3）
   * ため、ログイン時点の写しのままでは所属プロジェクトが古いままになる
   * （メニューの切替（`GuiDesign.md` 4.4）とプロジェクト配下の画面の判定に効く）。
   */
  async function refresh(): Promise<void> {
    setSession(await authApi.me())
  }

  /** ログイン。応答は `GET /me` と同じ内容なので、そのままストアになる */
  async function login(email: string, password: string): Promise<void> {
    setSession(await authApi.login(email, password))
  }

  /**
   * ログアウト。
   *
   * サーバ側が失敗しても画面側の状態は必ず捨てる。ログアウトしたつもりの
   * 利用者に、権限を持ったままの画面を見せない。
   */
  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } finally {
      clear()
    }
  }

  return {
    session,
    loaded,
    isAuthenticated,
    actor,
    permissions,
    projects,
    isAdministrator,
    mustChangePassword,
    projectByKey,
    can,
    canInProject,
    canReachProject,
    restore,
    refresh,
    login,
    logout,
    setSession,
    clear,
  }
})
