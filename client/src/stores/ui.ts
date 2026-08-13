import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * 画面の見た目に関する設定（GuiDesign.md 7.1 の ui ストア）。
 *
 * 手順7で持つのはテーマとベース色相の2つだけである（GuiDesign.md 8.11）。
 * メニューの折りたたみ状態（2.3.3）は、メニュー本体を作る手順8で足す。
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

const DARK_QUERY = '(prefers-color-scheme: dark)'

function readTheme(): ThemePreference {
  const v = localStorage.getItem(THEME_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

function readHue(): Hue {
  const v = localStorage.getItem(HUE_KEY)
  return v === 'green' || v === 'blue' ? v : 'blue'
}

export const useUiStore = defineStore('ui', () => {
  const theme = ref<ThemePreference>('system')
  const hue = ref<Hue>('blue')

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
  }

  return { theme, hue, resolvedTheme, setTheme, setHue, init }
})
