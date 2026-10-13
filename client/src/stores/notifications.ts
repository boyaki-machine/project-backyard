/**
 * 自分宛ての未読の通知の数（`GuiDesign.md` 5.16「未読数の取り直し」）。
 *
 * **どの画面にいても未読数を出すために、ここで一元に持つ。** AppShell が `start` を
 * 1回だけ呼ぶ（設定の未確認の監視＝`pending.ts` と同じ型）。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { unreadNotificationCount } from '../api/notifications'

/**
 * 引き直す間隔（ミリ秒）。**設定にしない**（`Design.md` 10.3）。
 */
export const POLL_INTERVAL_MS = 30_000

/** 展開したメニューの行で、数字のまま出す上限（5.16「丸め」） */
export const COUNT_CAP = 999
/** 折りたたみのレールと、スマホの浮遊する ☰ で出す上限。幅が無い */
export const COUNT_CAP_NARROW = 99

/** 未読数を表示の文字列にする。0 は空文字（出さない） */
export function formatUnreadCount(n: number, cap: number = COUNT_CAP): string {
  if (n <= 0) return ''
  return n > cap ? `${cap}+` : String(n)
}

export const useNotificationStore = defineStore('notifications', () => {
  const unread = ref(0)

  let poll: ReturnType<typeof setInterval> | undefined
  let started = false

  async function refresh() {
    try {
      unread.value = (await unreadNotificationCount()).count
    } catch {
      // **引けなくても画面に出さない。** 前の数を出し続ける。通知は付随機能であり、
      // 引けないことで全画面を汚さない（5.16）。
    }
  }

  function schedule() {
    if (poll !== undefined) clearInterval(poll)
    poll = setInterval(() => void refresh(), POLL_INTERVAL_MS)
  }

  /** **タブが隠れている間は引かない。** 見えた瞬間に1回引き、間隔を数え直す */
  function onVisibility() {
    if (document.hidden) {
      if (poll !== undefined) clearInterval(poll)
      poll = undefined
      return
    }
    void refresh()
    schedule()
  }

  /** 監視を始める。**アプリの枠から1回だけ呼ぶ。** */
  function start() {
    if (started) return
    started = true
    document.addEventListener('visibilitychange', onVisibility)
    void refresh()
    if (!document.hidden) schedule()
  }

  function stop() {
    if (poll !== undefined) clearInterval(poll)
    poll = undefined
    started = false
    document.removeEventListener('visibilitychange', onVisibility)
    unread.value = 0
  }

  return { unread, refresh, start, stop }
})
