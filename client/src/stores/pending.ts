/**
 * 確認を待っている設定変更の監視（`ApiDesign.md` 11.9、`GuiDesign.md` 2.6）。pb-107。
 *
 * **どの画面にいても未確認を出すために、ここで一元に持つ。** 改訂前は
 * アプリケーション設定ページの中だけで持っていたため、**設定を変えたあと
 * 別の画面へ移ると確認ボタンがどこにも無い状態で期限が過ぎた**（stg での
 * 利用者の指摘、2026-09-12）。
 *
 * **`system.settings` を持つ人だけが引く。** 一般利用者に「いま管理者が設定を
 * 変えている」を見せる必要はなく、確認を押す権限も無い。
 */
import { defineStore } from 'pinia'
import { uiText } from '../locales/ui'
import { computed, ref } from 'vue'

import * as settingsApi from '../api/settings'
import { useAuthStore } from './auth'

/**
 * 引き直す間隔（ミリ秒）。
 *
 * **設定にしない**（`Design.md` 10.3）。300秒の窓に対して30秒なら、
 * **他の管理者の変更に最悪30秒遅れて気づく。**
 */
const POLL_INTERVAL_MS = 30_000

/** 残りがこれを切ったら赤にする（`GuiDesign.md` 8.4 の段階） */
export const URGENT_SECONDS = 60

export const usePendingStore = defineStore('pending', () => {
  const auth = useAuthStore()

  const pending = ref<settingsApi.PendingConfirmation | null>(null)
  const confirming = ref(false)
  /** 押せなかった理由。**サーバの文面をそのまま出す**（11.8） */
  const error = ref<string | null>(null)

  /** いまの時刻。**残り時間を秒で出すために毎秒進める。** */
  const now = ref(Date.now())

  let poll: ReturnType<typeof setInterval> | undefined
  let tick: ReturnType<typeof setInterval> | undefined

  /** 期限までの残り秒。**0 になると元の設定へ戻る。** */
  const remainSeconds = computed(() => {
    if (pending.value === null) return 0
    const ms = new Date(pending.value.expires_at).getTime() - now.value
    return Math.max(0, Math.floor(ms / 1000))
  })

  /** 残りが少ないか。**枠線と数字を赤にする合図**（8.4） */
  const urgent = computed(() => pending.value !== null && remainSeconds.value <= URGENT_SECONDS)

  /** 変えたのが自分か。**文言を分ける**（pb-107） */
  const changedByMe = computed(
    () => pending.value?.changed_by?.id !== undefined && pending.value.changed_by.id === auth.actor?.id,
  )

  /** 監視できる立場か。**権限が無ければ引かない**（403 になる） */
  const watchable = computed(() => auth.can('system.settings'))

  async function refresh() {
    if (!watchable.value) {
      pending.value = null
      return
    }
    try {
      const res = await settingsApi.getPendingSettings()
      pending.value = res.pending_confirmation
    } catch {
      // **引けなくても画面を壊さない。** 未確認の監視は付随機能であり、
      // ここでエラーを出すと**締め出しの手当てが全画面を汚す。**
    }
  }

  /** 「アクセスできました」を押す（11.8） */
  async function confirm() {
    confirming.value = true
    error.value = null
    try {
      await settingsApi.confirmSettings()
      pending.value = null
    } catch (e: unknown) {
      // **サーバの文面をそのまま出す。** 確認の条件はキーごとに違い、
      // 画面が組み立て直すと片方が古くなる（11.8）。
      error.value = e instanceof Error ? e.message : uiText('確認できませんでした')
      await refresh()
    } finally {
      confirming.value = false
    }
  }

  /** 監視を始める。**アプリの枠から1回だけ呼ぶ。** */
  function start() {
    if (poll !== undefined) return
    void refresh()
    poll = setInterval(() => void refresh(), POLL_INTERVAL_MS)
    tick = setInterval(() => {
      now.value = Date.now()
      // **期限を過ぎたら引き直す。** サーバ側が戻しているので、画面も追う。
      if (pending.value !== null && remainSeconds.value === 0) void refresh()
    }, 1000)
  }

  function stop() {
    if (poll !== undefined) clearInterval(poll)
    if (tick !== undefined) clearInterval(tick)
    poll = undefined
    tick = undefined
    pending.value = null
  }

  return {
    pending,
    confirming,
    error,
    remainSeconds,
    urgent,
    changedByMe,
    watchable,
    refresh,
    confirm,
    start,
    stop,
  }
})
