/**
 * Excel 出力の色（`GuiDesign.md` 8.3.3 / 5.14「Excel 出力」）。
 *
 * **Excel はセルの色に RGB の具体値を要り、OKLCH も透明度も持てない。** そこで、ライトの
 * ベーススケール（8.3.1 / 8.3.2 の hex 表）と、5.14 の `--g-*` を**白の上に重ねた値**を
 * ここに定数として持つ。
 *
 * **この表は写しである。** 画面の色（`styles/tokens.css`）を変えたら、ここも直す。
 * **ずれは e2e が検出する**（`e2e/gantt-excel.spec.ts`）——`CSS_OF` の式をライトで canvas に
 * 塗って読み戻し、各チャンネル ±2 で比べる。配色は常にライト（5.14）。
 */
import type { Hue } from '../../stores/ui'

/** 8.3.1（ブルー）・8.3.2（グリーン）のライトの 12段 */
export const PB_LIGHT: Record<Hue, readonly string[]> = {
  blue: ['#fafcfe', '#f3f7fb', '#e9eff6', '#dde7ef', '#d2dde8', '#c4d2df', '#b3c4d3', '#95aec4', '#2b77b0', '#1368a1', '#39688e', '#102231'],
  green: ['#fafcf9', '#f4f8f3', '#ebf0e8', '#e0e8dd', '#d6dfd1', '#c9d4c4', '#b9c7b2', '#9eb294', '#4e822d', '#3f7318', '#4c6f38', '#172510'],
}

/** 5.14「ガントだけが使う色」のライト。透明度のある色は白の上に重ねた値 */
export const G_LIGHT = {
  sat: '#f2f6fb',
  sun: '#fbf4f5',
  holLine: '#f5e6e6',
  wkend: { blue: '#f4f4f5', green: '#f4f4f4' } as Record<Hue, string>,
  neon: '#0096a6',
  neonAct: '#de6f00',
  dangerText: '#963633',
} as const

/** 透明と混ぜた色（`color-mix(in srgb, c p%, transparent)`）を白の上に重ねる */
export function overWhite(hex: string, p: number): string {
  const n = parseInt(hex.slice(1), 16)
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => Math.round(v * p + 255 * (1 - p)))
  return `#${ch.map((v) => v.toString(16).padStart(2, '0')).join('')}`
}

export interface ExcelPalette {
  /** 文字（強）・文字（弱）・格子の線・見出しの地 */
  text: string
  muted: string
  grid: string
  head: string
  /** 基調色（バーの線・斜線・「今日」の罫・マイルストーン） */
  bar: string
  /** 進行中・レビュー中の塗り（基調色 26%） */
  barFill: string
  doneFill: string
  doneLine: string
  /** 配下の期間の下罫 */
  roll: string
  sat: string
  sun: string
  holLine: string
  wkend: string
  neon: string
  neonAct: string
  danger: string
}

export function paletteOf(hue: Hue): ExcelPalette {
  const pb = PB_LIGHT[hue]
  const s = (n: number) => pb[n - 1]!
  return {
    text: s(12),
    muted: s(11),
    grid: s(6),
    head: s(2),
    bar: s(9),
    barFill: overWhite(s(9), 0.26),
    doneFill: s(3),
    doneLine: s(7),
    roll: s(11),
    sat: G_LIGHT.sat,
    sun: G_LIGHT.sun,
    holLine: G_LIGHT.holLine,
    wkend: G_LIGHT.wkend[hue],
    neon: G_LIGHT.neon,
    neonAct: G_LIGHT.neonAct,
    danger: G_LIGHT.dangerText,
  }
}

/**
 * 表の各色が、画面のどの CSS の式に当たるか（e2e がライトで塗って突き合わせる）。
 * **この対応を試験の側に写さない**——表と式を同じ場所に置き、片方だけ直す事故を防ぐ。
 */
export const CSS_OF: Record<keyof ExcelPalette, string> = {
  text: 'var(--pb-12)',
  muted: 'var(--pb-11)',
  grid: 'var(--pb-6)',
  head: 'var(--pb-2)',
  bar: 'var(--g-bar)',
  barFill: 'var(--g-bar-fill)',
  doneFill: 'var(--g-done-fill)',
  doneLine: 'var(--g-done-line)',
  roll: 'var(--pb-11)',
  sat: 'var(--g-sat)',
  sun: 'var(--g-sun)',
  holLine: 'var(--g-hol-line)',
  wkend: 'var(--g-wkend)',
  neon: 'var(--g-neon)',
  neonAct: 'var(--g-neon-act)',
  danger: 'var(--pb-danger-text)',
}
