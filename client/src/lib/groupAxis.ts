/**
 * グループ化の軸を視点（バックログ・ガント）のあいだで共有する補い（`GuiDesign.md` 4.1.1。pb-220）。
 *
 * **正本は各視点の URL の `group` である。** ここはブラウザに覚えた軸を返すだけで、
 * **URL に `group` が無い視点を開いたとき**にだけ使う。共有された URL は今までどおり
 * 同じ画面を再現する。
 *
 * **保存するのは利用者が軸を選んだときだけ**である。URL の変化を監視して保存すると、
 * プロジェクトを切り替えた瞬間（`group` の無い URL へ移る）に、新しいプロジェクトの
 * 覚えた軸を「なし」で上書きしてしまう。**「なし」（`''`）も軸の1つとして覚える。**
 */

const STORAGE_KEY = 'pb.group_axis'

function readAll(): Record<string, unknown> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    const parsed: unknown = raw === null ? {} : JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, unknown>) : {}
  } catch {
    // 壊れた値やプライベートモードでも画面は開く。軸は失われてよい情報である
    return {}
  }
}

/** 覚えた軸。**覚えていなければ `null`**（「なし」を覚えているときの `''` と分ける） */
export function loadGroupAxis(projectKey: string): string | null {
  const v = readAll()[projectKey]
  return typeof v === 'string' ? v : null
}

export function saveGroupAxis(projectKey: string, axis: string): void {
  const all = readAll()
  all[projectKey] = axis
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(all))
  } catch {
    // 保存できなくても、いま開いている視点の軸は URL が持っている
  }
}

/**
 * URL に `group` が無いとき、補うべき軸を返す。**補わなくてよければ `null`**。
 *
 * `allowed` に無い値（古い版が覚えた軸など）は補わない。「なし」は URL に書かない
 * （`group` の無い URL がすでに「なし」を表す）ので、`''` を覚えていても `null` を返す。
 */
export function groupAxisToRestore(
  projectKey: string,
  query: Record<string, unknown>,
  allowed: readonly string[],
): string | null {
  if (typeof query.group === 'string') return null
  const stored = loadGroupAxis(projectKey)
  if (stored === null || stored === '' || !allowed.includes(stored)) return null
  return stored
}
