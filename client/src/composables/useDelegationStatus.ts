/**
 * 自律実行の状態を、チケット詳細の2か所（実行モードの下の項目と、ユーザ確認待ちの
 * セクション）で共有する（GuiDesign.md 5.5「自律実行」、pb-252）。
 *
 * **読むのは実行モードが「自律実行」のチケットだけ。** 他のチケットでは1本も叩かない。
 * 操作のたびに `reload()` で読み直す——状態・評価・確認待ちは互いに連動して変わる
 * （承認で状態が変わり、再開で確認待ちが消える）ので、手元で組み替えない。
 */
import { ref, watch, type Ref } from 'vue'

import { ApiError } from '../api/client'
import { getDelegationStatus, type DelegationStatus } from '../api/delegation'
import { uiText } from '../locales/ui'

export function delegationErrorText(e: unknown): string {
  if (e instanceof ApiError) {
    const details = e.details.map((d) => d.message).filter((m): m is string => !!m)
    return [e.message, ...details].join(' ')
  }
  return uiText('通信に失敗しました。もう一度試してください')
}

export function useDelegationStatus(
  projectKey: Ref<string>,
  seq: Ref<number>,
  enabled: Ref<boolean>,
) {
  const status = ref<DelegationStatus | null>(null)
  const error = ref<string | null>(null)

  async function reload(): Promise<void> {
    if (!enabled.value) {
      status.value = null
      return
    }
    try {
      status.value = await getDelegationStatus(projectKey.value, seq.value)
      error.value = null
    } catch (e) {
      error.value = delegationErrorText(e)
    }
  }

  watch([projectKey, seq, enabled], () => void reload(), { immediate: true })

  return { status, error, reload }
}
