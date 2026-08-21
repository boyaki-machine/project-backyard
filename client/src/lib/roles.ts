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
 * 選択肢として出すロール（`DbDesign.md` 7.3 の `sort_order` の順）。
 *
 * **並びも表示名もDBのシードが正本**である。手順14 で `GET /roles` を
 * 実装したら、この配列ごと応答に置き換わる。
 */
export interface RoleChoice {
  key: string
  label: string
  /** `role.description`。選択肢の下に添えて、選ぶ前に違いが読めるようにする */
  description: string
}

/** システムロール（`sort_order` 10 / 20） */
export const SYSTEM_ROLES: RoleChoice[] = [
  {
    key: 'operator',
    label: 'オペレータ',
    description: 'プロジェクトとチケットの閲覧・編集ができます',
  },
  {
    key: 'administrator',
    label: 'アドミニストレータ',
    description: 'ユーザー管理・システム設定を含む全操作ができます',
  },
]

/** プロジェクトロール（`sort_order` 30 / 40 / 50） */
export const PROJECT_ROLES: RoleChoice[] = [
  {
    key: 'project_admin',
    label: 'プロジェクト管理者',
    description: '当該プロジェクトの全操作と承認ができます',
  },
  {
    key: 'project_member',
    label: 'メンバー',
    description: '当該プロジェクトのチケットを作成・編集できます',
  },
  {
    key: 'project_viewer',
    label: '閲覧者',
    description: '当該プロジェクトを閲覧のみできます',
  },
]

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
