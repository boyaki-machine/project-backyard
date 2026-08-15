import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

/**
 * 画面の見た目に関する設定（GuiDesign.md 7.1 の ui ストア）。
 *
 * テーマとベース色相（8.11）に加えて、メニューの折りたたみ状態（2.3.3）を持つ。
 * 集中モード（2.3.2）は Phase 2 のため持たない。
 *
 * 保存先は localStorage のみ。ユーザー設定（app_user）への保存は
 * /me の API と画面（手順17）で対応する。
 */

/** テーマの選択値。既定は「システムに従う」（GuiDesign.md 8.11） */
export type ThemePreference = 'system' | 'light' | 'dark'

/** ベース色相。既定はブルー（GuiDesign.md 8.3） */
export type Hue = 'blue' | 'green'

const THEME_KEY = 'pb.theme'
const HUE_KEY = 'pb.hue'
const MENU_KEY = 'pb.menu_collapsed'

const DARK_QUERY = '(prefers-color-scheme: dark)'

/**
 * ブレークポイント（GuiDesign.md 2.4）。
 *
 * - 1280px 以上：展開が既定
 * - 768〜1279px：折りたたみが既定
 * - 768px 未満：メニューは非表示。☰ を左上に浮遊表示する（この幅でのみ案B）
 */
const WIDE_QUERY = '(min-width: 1280px)'
const NARROW_QUERY = '(max-width: 767px)'

function readTheme(): ThemePreference {
  const v = localStorage.getItem(THEME_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

function readHue(): Hue {
  const v = localStorage.getItem(HUE_KEY)
  return v === 'green' || v === 'blue' ? v : 'blue'
}

/** 利用者が明示的に選んだ折りたたみ状態。未選択なら null で、幅から決める */
function readMenuCollapsed(): boolean | null {
  const v = localStorage.getItem(MENU_KEY)
  if (v === 'true') return true
  if (v === 'false') return false
  return null
}

export const useUiStore = defineStore('ui', () => {
  const theme = ref<ThemePreference>('system')
  const hue = ref<Hue>('blue')

  /** null は「利用者が選んでいない」。この間は幅の変化に追随する（2.4） */
  const menuChoice = ref<boolean | null>(null)
  /** 画面幅が 1280px 以上か */
  const wide = ref(true)
  /** 画面幅が 768px 未満か。この幅ではメニューをオーバーレイにする（2.4） */
  const narrow = ref(false)
  /** 768px 未満でオーバーレイを開いているか。リロードで解除する（保存しない） */
  const overlayOpen = ref(false)

  /** メニューを折りたたんで表示するか（アイコンレールのみ 56px） */
  const menuCollapsed = computed(() => menuChoice.value ?? !wide.value)

  /** 折りたたみ／展開を切り替える。`[`（9.1）とトグルボタン（2.3）から呼ぶ */
  function toggleMenu() {
    if (narrow.value) {
      overlayOpen.value = !overlayOpen.value
      return
    }
    const next = !menuCollapsed.value
    menuChoice.value = next
    localStorage.setItem(MENU_KEY, String(next))
  }

  function closeOverlay() {
    overlayOpen.value = false
  }

  /** system を実際の light / dark へ解決する */
  function resolvedTheme(): 'light' | 'dark' {
    if (theme.value !== 'system') return theme.value
    return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
  }

  /**
   * <html> の属性へ反映する。
   * tokens.css は [data-theme="dark"] と [data-hue="green"] だけを見ており、
   * 差し替えは属性の付け替えで完結する（再読み込みを要しない。GuiDesign.md 8.11）。
   */
  function apply() {
    const root = document.documentElement
    root.dataset.theme = resolvedTheme()
    root.dataset.hue = hue.value
  }

  function setTheme(next: ThemePreference) {
    theme.value = next
    localStorage.setItem(THEME_KEY, next)
    apply()
  }

  function setHue(next: Hue) {
    hue.value = next
    localStorage.setItem(HUE_KEY, next)
    apply()
  }

  /** 起動時に一度だけ呼ぶ（main.ts） */
  function init() {
    theme.value = readTheme()
    hue.value = readHue()
    apply()

    // 「システムに従う」を選んでいる間は、OS 側の切替に追随する。
    window.matchMedia(DARK_QUERY).addEventListener('change', () => {
      if (theme.value === 'system') apply()
    })

    menuChoice.value = readMenuCollapsed()

    const wideMq = window.matchMedia(WIDE_QUERY)
    const narrowMq = window.matchMedia(NARROW_QUERY)
    wide.value = wideMq.matches
    narrow.value = narrowMq.matches
    wideMq.addEventListener('change', (e) => {
      wide.value = e.matches
    })
    narrowMq.addEventListener('change', (e) => {
      narrow.value = e.matches
      if (!e.matches) overlayOpen.value = false
    })
  }

  return {
    theme,
    hue,
    resolvedTheme,
    setTheme,
    setHue,
    menuCollapsed,
    narrow,
    overlayOpen,
    toggleMenu,
    closeOverlay,
    init,
  }
})
