/**
 * ロールの表示名（`DbDesign.md` 7.3 の `role.display_name`）。
 *
 * **同じものは全画面で同じ表記にする**（`GuiDesign.md` 5.6）。画面ごとに
 * 「アドミン」と「アドミニストレータ」を使い分けない。正本はDBのシードで、
 * ここはその写しである。
 *
 * **`GET /roles`（`ApiDesign.md` 7.1、手順14）を実装したらこのファイルごと捨てる。**
 * それまでは API がロールをキーでしか返さないため、画面が対応表を持つ。
 */

/** システムロール（`app_user.system_role`。`DbDesign.md` 6.2 の CHECK 制約） */
const SYSTEM_ROLE_LABELS: Record<string, string> = {
  operator: 'オペレータ',
  administrator: 'アドミニストレータ',
}

/** プロジェクトロール（`project_member.role`。`DbDesign.md` 7.3 のシード） */
const PROJECT_ROLE_LABELS: Record<string, string> = {
  project_admin: 'プロジェクト管理者',
  project_member: 'メンバー',
  project_viewer: '閲覧者',
}

/**
 * 未知のキーはそのまま返す。
 *
 * 対応表はDBのシードの写しであり、片方だけ増えることがありうる
 * （カスタムロールは Phase 3）。**表示できるものを表示する**ほうが、
 * 空欄になって行の意味が読めなくなるより良い。
 */
function label(table: Record<string, string>, key: string): string {
  return table[key] ?? key
}

export function systemRoleLabel(key: string): string {
  return label(SYSTEM_ROLE_LABELS, key)
}

export function projectRoleLabel(key: string): string {
  return label(PROJECT_ROLE_LABELS, key)
}

/**
 * 一覧のロール列（`GuiDesign.md` 5.6）。
 *
 * **エージェントは `system_role` が `null` になる**（`ApiDesign.md` 6.1）ので、
 * 列には種別をそのまま出す。「—」にすると、ロールを持たないのか未設定なのかが
 * 読み取れない。
 */
export function userRoleLabel(kind: string, systemRole: string | null): string {
  if (systemRole !== null) return systemRoleLabel(systemRole)
  return kind === 'agent' ? 'エージェント' : '—'
}
