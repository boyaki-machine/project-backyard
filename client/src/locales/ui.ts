import { i18n } from '../i18n'
import { uiMessages as english } from './en/ui'
import { translate as japanese } from './ja/ui'

export type UiParams = Record<string, string | number | null | undefined>

function interpolate(message: string, params: UiParams): string {
  return message.replace(/\{([^}]+)\}/gu, (match, name: string) => String(params[name] ?? match))
}

/** Translate application-owned UI text. Registered as `$ui` in Vue templates. */
export function uiText(source: string, params: UiParams = {}): string {
  const locale = i18n.global.locale.value
  const message = locale === 'en' ? (english[source] ?? source) : japanese(source)
  return interpolate(message, params)
}

export function uiLocaleTag(): 'ja-JP' | 'en-US' {
  return i18n.global.locale.value === 'en' ? 'en-US' : 'ja-JP'
}

export function uiNumber(value: number): string {
  return value.toLocaleString(uiLocaleTag())
}
