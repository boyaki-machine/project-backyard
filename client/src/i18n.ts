import { createI18n } from 'vue-i18n'

import { messages, normalizeLocale } from './locales'

/**
 * UI の表示言語。サーバが返す app_user.locale を正とし、未知の値は日本語へ倒す。
 * Composition API に統一し、各部品は useI18n() からメッセージを読む。
 */
export const i18n = createI18n({
  legacy: false,
  locale: 'ja',
  fallbackLocale: 'ja',
  messages,
})

export function setLocale(value: string | null | undefined): void {
  const locale = normalizeLocale(value)
  i18n.global.locale.value = locale
  document.documentElement.lang = locale
}
