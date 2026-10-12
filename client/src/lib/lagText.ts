/**
 * 依存のずらしの札（`ApiDesign.md` 9.10.1「リンクの変更を `activity` に記録する」と同じ書式）。
 *
 * 日は `+2d`、秒は `h` / `m` / `s` で日の後ろにそれぞれの符号で続ける（`+10m`・`+1d+1h30m`・
 * `+1d-10m`。`DbDesign.md` 6.6.3。pb-332）。ずらしが無ければ空文字。詳細の関連チケット（5.5）・
 * ガントの線の札（5.14）・タイムラインの線の札（5.15）が同じものを使う。純粋な関数だけを置く。
 */
export function lagText(days: number, seconds = 0): string {
  let out = days ? `${days > 0 ? '+' : ''}${days}d` : ''
  if (seconds) {
    let sec = Math.abs(seconds)
    out += seconds > 0 ? '+' : '-'
    const h = Math.floor(sec / 3600)
    const m = Math.floor((sec % 3600) / 60)
    sec %= 60
    if (h) out += `${h}h`
    if (m) out += `${m}m`
    if (sec) out += `${sec}s`
  }
  return out
}

/** 実効のずらし（ミリ秒）。暦日は 24時間 × 日数として数える（5.14「依存に反する配置」） */
export function lagMs(days: number, seconds = 0): number {
  return days * 86_400_000 + seconds * 1000
}
