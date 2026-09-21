import { defineStore } from 'pinia'
import { uiText } from '../locales/ui'
import { computed, ref } from 'vue'

import { getPermissions, getRoles, type Permission, type Role, type RoleScope } from '../api/roles'

/**
 * ロールと権限のカタログ（`ApiDesign.md` 7.1 / 7.2）。
 *
 * **正本は `DbDesign.md` 7.2 / 7.3 のシードで、画面は対応表を持たない**
 * （`GuiDesign.md` 5.6）。手順14 より前は `lib/roles.ts` が表示名の写しを
 * 持っていたが、`GET /roles` の実装にあわせて廃止した。
 *
 * **表示名は全画面で同じでなければならない**（5.6「同じものは全画面で同じ表記に
 * する」）。1つのストアに集約するのはそのためである。
 *
 * 取得できる範囲は呼び出し元の権限で変わる（7.1）。`project` は認証済みなら
 * 誰でも読めるが、`system` と全件は `user.manage` を要する。**画面は自分が
 * 必要とする範囲を渡す**こと。
 */
export const useRolesStore = defineStore('roles', () => {
  const roles = ref<Role[]>([])
  const permissions = ref<Permission[]>([])

  /**
   * いま持っている範囲。`null` は未取得。
   *
   * `all` はどの要求も満たす。`project` しか読めない利用者が `all` を
   * 要求することは無い（画面が範囲を決めて渡すため）。
   */
  const loadedScope = ref<RoleScope | null>(null)
  const permissionsLoaded = ref(false)

  /** 取得に失敗した理由。画面は表示名の代わりにキーを出して先へ進む */
  const error = ref<string | null>(null)

  // 同じ範囲を同時に要求されたときに2回叩かないためのフラグ。
  let rolesInFlight: Promise<void> | null = null
  let permissionsInFlight: Promise<void> | null = null

  function satisfies(want: RoleScope): boolean {
    if (loadedScope.value === null) return false
    if (loadedScope.value === 'all') return true
    return loadedScope.value === want
  }

  /**
   * ロールを1度だけ取得する。
   *
   * **失敗しても例外を投げない。** 表示名が引けないことは画面を止める理由に
   * ならず、キーをそのまま出せば行の意味は読める（廃止した `lib/roles.ts` の
   * 「表示できるものを表示する」方針を引き継ぐ）。
   */
  async function ensureRoles(scope: RoleScope = 'all'): Promise<void> {
    if (satisfies(scope)) return
    if (rolesInFlight) return rolesInFlight

    rolesInFlight = (async () => {
      try {
        const res = await getRoles(scope)
        roles.value = res.items
        loadedScope.value = scope
        error.value = null
      } catch (e) {
        error.value = e instanceof Error ? e.message : uiText('ロールを取得できませんでした')
      } finally {
        rolesInFlight = null
      }
    })()
    return rolesInFlight
  }

  /**
   * 権限カタログを1度だけ取得する（`user.manage` が要る）。
   *
   * 消費者は `GuiDesign.md` 5.6.3 の権限マトリクスだけである。**こちらは
   * 失敗を握りつぶさない**——マトリクスは権限カタログそのものが本体であり、
   * 空の表を出しても意味が無い（6.2 のエラー状態を出す）。
   */
  async function ensurePermissions(): Promise<void> {
    if (permissionsLoaded.value) return
    if (permissionsInFlight) return permissionsInFlight

    permissionsInFlight = (async () => {
      try {
        const res = await getPermissions()
        permissions.value = res.items
        permissionsLoaded.value = true
      } finally {
        permissionsInFlight = null
      }
    })()
    return permissionsInFlight
  }

  /** `role.sort_order` の順は API が保証する。画面で並べ替えない */
  const systemRoles = computed(() => roles.value.filter((r) => r.scope === 'system'))
  const projectRoles = computed(() => roles.value.filter((r) => r.scope === 'project'))

  /**
   * 表示名を引く。**未知のキーはそのまま返す。**
   *
   * カタログはシードで増えうる（カスタムロールは Phase 3）。空欄にすると
   * 行の意味が読めなくなるので、表示できるものを表示する。
   */
  function roleLabel(key: string): string {
    return roles.value.find((r) => r.key === key)?.display_name ?? key
  }

  /** 選択肢の下に添える説明（`role.description`）。無ければ空文字 */
  function roleDescription(key: string): string {
    return roles.value.find((r) => r.key === key)?.description ?? ''
  }

  /**
   * 一覧のロール列（`GuiDesign.md` 5.6）。
   *
   * **エージェントは `system_role` が `null` になる**（`ApiDesign.md` 6.1）ので、
   * 列には種別をそのまま出す。「—」にすると、ロールを持たないのか未設定なのかが
   * 読み取れない。**この文字列は画面が作っており DBに無い**ため、一覧の検索
   * （`q`）では当たらない。
   */
  function userRoleLabel(kind: string, systemRole: string | null | undefined): string {
    if (systemRole != null) return roleLabel(systemRole)
    return kind === 'agent' ? uiText('エージェント') : '—'
  }

  return {
    roles,
    permissions,
    systemRoles,
    projectRoles,
    error,
    ensureRoles,
    ensurePermissions,
    roleLabel,
    roleDescription,
    userRoleLabel,
  }
})
