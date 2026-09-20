import enMenu from './en/menu'
import jaMenu from './ja/menu'

export const messages = {
  ja: { menu: jaMenu },
  en: { menu: enMenu },
} as const

export const supportedLocales = ['ja', 'en'] as const
export type SupportedLocale = (typeof supportedLocales)[number]

export function normalizeLocale(value: string | null | undefined): SupportedLocale {
  return supportedLocales.includes(value as SupportedLocale) ? (value as SupportedLocale) : 'ja'
}
