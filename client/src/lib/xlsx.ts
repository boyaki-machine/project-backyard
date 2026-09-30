/**
 * 最小の xlsx 書き出し（`GuiDesign.md` 5.14「Excel 出力」。pb-223）。
 *
 * **依存ライブラリを足さない。** xlsx は XML（SpreadsheetML）を zip で固めたもので、ガントの
 * 出力に要るのは次だけである——複数のタブ、塗り（単色と斜線）・罫・文字の色と太さ・字下げ・
 * 表示形式（日時）、式、結合、列幅と列の隠し、行の階層（アウトライン）、ウィンドウ枠の固定、
 * 名前付き範囲、条件付き書式（数式）、内部リンク、図形（コネクタと角丸の四角）。
 *
 * **文字列はセルに直接書く**（`inlineStr`）。**書式は同じ組を1つにまとめ**、`cellXfs` の番号で
 * 参照する。条件付き書式の書式は別の表（`dxfs`）に持つ。
 *
 * **zip の圧縮はブラウザの `CompressionStream('deflate-raw')` を使う**（無ければ無圧縮）。
 */

export type Color = string // `#rrggbb`

export interface XFont {
  bold?: boolean
  color?: Color
}

export interface XFill {
  /** `solid` は単色、`lightUp` は右上がりの細い斜線（画面の未着手の斜線に当たる） */
  pattern: 'solid' | 'lightUp'
  fg: Color
  /** 斜線の地の色 */
  bg?: Color
}

export interface XBorderSide {
  style: 'thin' | 'medium' | 'thick' | 'dashed' | 'hair'
  color: Color
}

export interface XBorder {
  left?: XBorderSide
  right?: XBorderSide
  top?: XBorderSide
  bottom?: XBorderSide
}

export interface XStyle {
  font?: XFont
  fill?: XFill
  border?: XBorder
  align?: { h?: 'left' | 'center' | 'right'; v?: 'top' | 'center' | 'bottom'; indent?: number }
  /** 表示形式（`yyyy-mm-dd` など）。日時の値に付ける */
  numFmt?: string
}

export interface XCell {
  /** 値。数は日時のシリアル値にも使う */
  v?: string | number
  /** 式（先頭の `=` を付けない） */
  f?: string
  s?: XStyle
}

export interface XRow {
  /** 列の番号（0始まり）→ セル。空のセルは書かない */
  cells: Map<number, XCell>
  /** 行の高さ（pt） */
  height?: number
  /** 行のグループの深さ（0〜7） */
  outline?: number
}

export interface XCol {
  /** 幅（文字数） */
  width: number
  /** 列の既定の書式（空のセルにも効く） */
  style?: XStyle
  hidden?: boolean
}

/** 条件付き書式の規則。**先に並べたものが優先**される（Excel の優先順位） */
export interface XCondRule {
  formula: string
  style: XStyle
}

/** 図形の位置（0始まりの行・列と、そのセルの左上からのずれ。EMU） */
export interface XAnchor {
  r: number
  c: number
  dr?: number
  dc?: number
}

export type XShape =
  | {
      kind: 'connector'
      from: XAnchor
      to: XAnchor
      color: Color
      /** 線の太さ（EMU） */
      width: number
      dash: 'solid' | 'dash' | 'sysDot' | 'dashDot' | 'lgDashDot'
      /** 終わりの側（後行）の端。`triangle` は矢印、`oval` は丸 */
      end?: 'triangle' | 'oval'
    }
  | { kind: 'roundRect'; from: XAnchor; to: XAnchor; color: Color; width: number; dash: 'solid' | 'dash' }

export interface XSheet {
  name: string
  cols: XCol[]
  rows: XRow[]
  /** 結合（0始まりの行・列の範囲） */
  merges?: { r0: number; c0: number; r1: number; c1: number }[]
  /** 固定する列と行の数 */
  freeze?: { cols: number; rows: number }
  /** 条件付き書式（範囲ごと） */
  cond?: { r0: number; c0: number; r1: number; c1: number; rules: XCondRule[] }[]
  /** 内部リンク（セル → `'タブ'!A1` のような場所） */
  links?: { r: number; c: number; location: string }[]
  shapes?: XShape[]
}

export interface XBook {
  sheets: XSheet[]
  /** 名前付き範囲（名前 → `'タブ'!$A$2:$A$1000`） */
  names?: Record<string, string>
  /** 既定のフォント名 */
  font?: string
}

// ── 番地と文字 ─────────────────────────────────────────────

/** 0始まりの列の番号を `A`・`Z`・`AA` に直す */
export function colName(c: number): string {
  let s = ''
  let n = c + 1
  while (n > 0) {
    const m = (n - 1) % 26
    s = String.fromCharCode(65 + m) + s
    n = Math.floor((n - 1) / 26)
  }
  return s
}

export function cellRef(r: number, c: number): string {
  return `${colName(c)}${r + 1}`
}

/** XML の文字。**XML 1.0 で書けない制御文字は落とす**（Excel が開けなくなる） */
export function xmlText(s: string): string {
  return s
    // eslint-disable-next-line no-control-regex
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f￾￿]/g, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

const argb = (c: Color) => `FF${c.replace('#', '').toUpperCase()}`
const rgb = (c: Color) => c.replace('#', '').toUpperCase()

/** シート名。Excel の制約（31文字、`[]:*?/\` を含まない）に合わせる */
export function sheetName(s: string): string {
  const t = s.replace(/[[\]:*?/\\]/g, ' ').trim().slice(0, 31)
  return t === '' ? 'Sheet1' : t
}

/** 式の中でタブを指す書き方（`'ガント'`） */
export function sheetRef(name: string): string {
  return `'${sheetName(name).replace(/'/g, "''")}'`
}

/** 1899-12-30 を 0 とする日時のシリアル値。`wall` は壁時計を UTC とみなしたエポックミリ秒 */
export function serial(wall: number): number {
  return wall / 86_400_000 + 25569
}

// ── 書式の表（styles.xml）──────────────────────────────────

class StyleTable {
  private fonts: string[] = []
  private fills: string[] = ['<fill><patternFill patternType="none"/></fill>', '<fill><patternFill patternType="gray125"/></fill>']
  private borders: string[] = ['<border><left/><right/><top/><bottom/><diagonal/></border>']
  private numFmts: string[] = []
  private xfs: string[] = []
  private dxfs: string[] = []
  private index = new Map<string, number>()
  private dxfIndex = new Map<string, number>()

  constructor(private fontName: string) {
    this.fonts.push(this.fontXml({}))
    this.xfs.push('<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>')
    this.index.set('{}', 0)
  }

  private fontXml(f: XFont): string {
    return `<font>${f.bold ? '<b/>' : ''}<sz val="10"/>${f.color ? `<color rgb="${argb(f.color)}"/>` : ''}<name val="${xmlText(this.fontName)}"/><family val="3"/><charset val="128"/></font>`
  }

  private intern(list: string[], xml: string): number {
    const i = list.indexOf(xml)
    if (i >= 0) return i
    list.push(xml)
    return list.length - 1
  }

  private borderXml(b: XBorder, dxf: boolean): string {
    const side = (name: string, s?: XBorderSide) =>
      s ? `<${name} style="${s.style}"><color rgb="${argb(s.color)}"/></${name}>` : dxf ? '' : `<${name}/>`
    return `<border>${side('left', b.left)}${side('right', b.right)}${side('top', b.top)}${side('bottom', b.bottom)}${dxf ? '' : '<diagonal/>'}</border>`
  }

  private numFmtId(code: string): number {
    const i = this.numFmts.indexOf(code)
    if (i >= 0) return 164 + i
    this.numFmts.push(code)
    return 164 + this.numFmts.length - 1
  }

  /** 書式の番号（`s` 属性）。同じ組は同じ番号になる */
  id(st: XStyle | undefined): number {
    if (!st) return 0
    const key = JSON.stringify(st)
    const hit = this.index.get(key)
    if (hit !== undefined) return hit
    const fontId = st.font ? this.intern(this.fonts, this.fontXml(st.font)) : 0
    let fillId = 0
    if (st.fill) {
      const f = st.fill
      fillId = this.intern(
        this.fills,
        `<fill><patternFill patternType="${f.pattern}"><fgColor rgb="${argb(f.fg)}"/><bgColor rgb="${argb(f.bg ?? f.fg)}"/></patternFill></fill>`,
      )
    }
    const borderId = st.border ? this.intern(this.borders, this.borderXml(st.border, false)) : 0
    const numFmtId = st.numFmt ? this.numFmtId(st.numFmt) : 0
    const a = st.align
    const align = a
      ? `<alignment${a.h ? ` horizontal="${a.h}"` : ''}${a.v ? ` vertical="${a.v}"` : ''}${a.indent ? ` indent="${a.indent}"` : ''}/>`
      : ''
    const xf =
      `<xf numFmtId="${numFmtId}" fontId="${fontId}" fillId="${fillId}" borderId="${borderId}" xfId="0"` +
      `${numFmtId ? ' applyNumberFormat="1"' : ''}${fontId ? ' applyFont="1"' : ''}${fillId ? ' applyFill="1"' : ''}${borderId ? ' applyBorder="1"' : ''}${align ? ' applyAlignment="1"' : ''}` +
      (align ? `>${align}</xf>` : '/>')
    this.xfs.push(xf)
    const id = this.xfs.length - 1
    this.index.set(key, id)
    return id
  }

  /**
   * 条件付き書式の書式の番号。**単色の塗りは `bgColor` に書く**——条件付き書式（dxf）では
   * 単色の色を背景色として読む（Excel の約束事。`fgColor` に書くと色が付かない）。
   */
  dxf(st: XStyle): number {
    const key = JSON.stringify(st)
    const hit = this.dxfIndex.get(key)
    if (hit !== undefined) return hit
    let x = '<dxf>'
    if (st.font) x += `<font>${st.font.bold ? '<b/>' : ''}${st.font.color ? `<color rgb="${argb(st.font.color)}"/>` : ''}</font>`
    if (st.fill) {
      const f = st.fill
      x +=
        f.pattern === 'solid'
          ? `<fill><patternFill patternType="solid"><bgColor rgb="${argb(f.fg)}"/></patternFill></fill>`
          : `<fill><patternFill patternType="${f.pattern}"><fgColor rgb="${argb(f.fg)}"/><bgColor rgb="${argb(f.bg ?? 'ffffff')}"/></patternFill></fill>`
    }
    if (st.border) x += this.borderXml(st.border, true)
    x += '</dxf>'
    this.dxfs.push(x)
    const id = this.dxfs.length - 1
    this.dxfIndex.set(key, id)
    return id
  }

  xml(): string {
    const nf = this.numFmts.map((c, i) => `<numFmt numFmtId="${164 + i}" formatCode="${xmlText(c)}"/>`).join('')
    return (
      '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
      '<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">' +
      (nf ? `<numFmts count="${this.numFmts.length}">${nf}</numFmts>` : '') +
      `<fonts count="${this.fonts.length}">${this.fonts.join('')}</fonts>` +
      `<fills count="${this.fills.length}">${this.fills.join('')}</fills>` +
      `<borders count="${this.borders.length}">${this.borders.join('')}</borders>` +
      '<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>' +
      `<cellXfs count="${this.xfs.length}">${this.xfs.join('')}</cellXfs>` +
      '<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>' +
      `<dxfs count="${this.dxfs.length}">${this.dxfs.join('')}</dxfs>` +
      '</styleSheet>'
    )
  }
}

// ── シート（sheetN.xml）────────────────────────────────────

function sheetXml(sh: XSheet, styles: StyleTable, hasDrawing: boolean): string {
  const out: string[] = []
  const maxOutline = sh.rows.reduce((m, r) => Math.max(m, r.outline ?? 0), 0)
  out.push(
    '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
      '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">' +
      // 親の行が子の上にある（Excel の既定は子の下に集計行）
      '<sheetPr><outlinePr summaryBelow="0"/></sheetPr>',
  )
  const f = sh.freeze
  if (f && (f.cols > 0 || f.rows > 0)) {
    const pane = f.cols > 0 && f.rows > 0 ? 'bottomRight' : f.rows > 0 ? 'bottomLeft' : 'topRight'
    out.push(
      `<sheetViews><sheetView workbookViewId="0"><pane${f.cols ? ` xSplit="${f.cols}"` : ''}${f.rows ? ` ySplit="${f.rows}"` : ''} topLeftCell="${cellRef(f.rows, f.cols)}" activePane="${pane}" state="frozen"/></sheetView></sheetViews>`,
    )
  }
  out.push(`<sheetFormatPr defaultRowHeight="15"${maxOutline ? ` outlineLevelRow="${maxOutline}"` : ''}/>`)
  if (sh.cols.length > 0) {
    out.push('<cols>')
    sh.cols.forEach((c, i) => {
      const s = c.style ? styles.id(c.style) : 0
      out.push(`<col min="${i + 1}" max="${i + 1}" width="${c.width}" customWidth="1"${s ? ` style="${s}"` : ''}${c.hidden ? ' hidden="1"' : ''}/>`)
    })
    out.push('</cols>')
  }
  out.push('<sheetData>')
  sh.rows.forEach((row, r) => {
    const attrs =
      `${row.height ? ` ht="${row.height}" customHeight="1"` : ''}` +
      `${row.outline ? ` outlineLevel="${Math.min(7, row.outline)}"` : ''}`
    const cols = [...row.cells.keys()].sort((a, b) => a - b)
    if (cols.length === 0 && !attrs) return
    out.push(`<row r="${r + 1}"${attrs}>`)
    for (const c of cols) {
      const cell = row.cells.get(c)!
      // 書式の無いセルは列の書式を引き継ぐ
      const s = cell.s ? styles.id(cell.s) : sh.cols[c]?.style ? styles.id(sh.cols[c]!.style) : 0
      const ref = cellRef(r, c)
      const sa = s ? ` s="${s}"` : ''
      if (cell.f !== undefined) {
        const t = typeof cell.v === 'string' ? ' t="str"' : ''
        const v = cell.v === undefined ? '' : `<v>${xmlText(String(cell.v))}</v>`
        out.push(`<c r="${ref}"${sa}${t}><f>${xmlText(cell.f)}</f>${v}</c>`)
      } else if (cell.v === undefined || cell.v === '') out.push(`<c r="${ref}"${sa}/>`)
      else if (typeof cell.v === 'number') out.push(`<c r="${ref}"${sa}><v>${cell.v}</v></c>`)
      else out.push(`<c r="${ref}"${sa} t="inlineStr"><is><t xml:space="preserve">${xmlText(cell.v)}</t></is></c>`)
    }
    out.push('</row>')
  })
  out.push('</sheetData>')
  // 要素の並びは仕様で決まっている（mergeCells → conditionalFormatting → hyperlinks → pageMargins → drawing）
  if (sh.merges && sh.merges.length > 0) {
    out.push(`<mergeCells count="${sh.merges.length}">`)
    for (const m of sh.merges) out.push(`<mergeCell ref="${cellRef(m.r0, m.c0)}:${cellRef(m.r1, m.c1)}"/>`)
    out.push('</mergeCells>')
  }
  let priority = 1
  for (const cf of sh.cond ?? []) {
    out.push(`<conditionalFormatting sqref="${cellRef(cf.r0, cf.c0)}:${cellRef(cf.r1, cf.c1)}">`)
    for (const rule of cf.rules) {
      out.push(`<cfRule type="expression" dxfId="${styles.dxf(rule.style)}" priority="${priority++}"><formula>${xmlText(rule.formula)}</formula></cfRule>`)
    }
    out.push('</conditionalFormatting>')
  }
  if (sh.links && sh.links.length > 0) {
    out.push('<hyperlinks>')
    for (const l of sh.links) out.push(`<hyperlink ref="${cellRef(l.r, l.c)}" location="${xmlText(l.location)}" display="${xmlText(l.location)}"/>`)
    out.push('</hyperlinks>')
  }
  out.push('<pageMargins left="0.4" right="0.4" top="0.5" bottom="0.5" header="0.3" footer="0.3"/>')
  if (hasDrawing) out.push('<drawing r:id="rId1"/>')
  out.push('</worksheet>')
  return out.join('')
}

// ── 図形（drawingN.xml）────────────────────────────────────

const A = 'http://schemas.openxmlformats.org/drawingml/2006/main'

function marker(tag: string, a: XAnchor): string {
  return `<xdr:${tag}><xdr:col>${a.c}</xdr:col><xdr:colOff>${Math.round(a.dc ?? 0)}</xdr:colOff><xdr:row>${a.r}</xdr:row><xdr:rowOff>${Math.round(a.dr ?? 0)}</xdr:rowOff></xdr:${tag}>`
}

/** 位置を比べるための通し値（行・列が大きいほど右下） */
const key = (a: XAnchor, big: number) => ({ x: a.c * big + (a.dc ?? 0), y: a.r * big + (a.dr ?? 0) })

function drawingXml(shapes: XShape[]): string {
  const out: string[] = [
    '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>',
    `<xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing" xmlns:a="${A}">`,
  ]
  const BIG = 1e9
  shapes.forEach((sh, i) => {
    const id = i + 2
    const p = key(sh.from, BIG)
    const q = key(sh.to, BIG)
    // twoCellAnchor は左上から右下へ。**線の向きは反転（flip）で表す**——始まりが右なら flipH、下なら flipV
    const flipH = q.x < p.x
    const flipV = q.y < p.y
    const tl: XAnchor = { c: flipH ? sh.to.c : sh.from.c, dc: flipH ? sh.to.dc : sh.from.dc, r: flipV ? sh.to.r : sh.from.r, dr: flipV ? sh.to.dr : sh.from.dr }
    const br: XAnchor = { c: flipH ? sh.from.c : sh.to.c, dc: flipH ? sh.from.dc : sh.to.dc, r: flipV ? sh.from.r : sh.to.r, dr: flipV ? sh.from.dr : sh.to.dr }
    const line = `<a:ln w="${sh.width}"><a:solidFill><a:srgbClr val="${rgb(sh.color)}"/></a:solidFill><a:prstDash val="${sh.dash}"/>${sh.kind === 'connector' && sh.end ? `<a:tailEnd type="${sh.end}" w="sm" len="sm"/>` : ''}</a:ln>`
    const xfrm = `<a:xfrm${flipH ? ' flipH="1"' : ''}${flipV ? ' flipV="1"' : ''}><a:off x="0" y="0"/><a:ext cx="0" cy="0"/></a:xfrm>`
    out.push(`<xdr:twoCellAnchor >${marker('from', tl)}${marker('to', br)}`)
    if (sh.kind === 'connector') {
      out.push(
        `<xdr:cxnSp macro=""><xdr:nvCxnSpPr><xdr:cNvPr id="${id}" name="dep ${id}"/><xdr:cNvCxnSpPr/></xdr:nvCxnSpPr>` +
          `<xdr:spPr>${xfrm}<a:prstGeom prst="bentConnector3"><a:avLst/></a:prstGeom><a:noFill/>${line}</xdr:spPr></xdr:cxnSp>`,
      )
    } else {
      out.push(
        `<xdr:sp macro="" textlink=""><xdr:nvSpPr><xdr:cNvPr id="${id}" name="blocks ${id}"/><xdr:cNvSpPr/></xdr:nvSpPr>` +
          `<xdr:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/></a:xfrm><a:prstGeom prst="roundRect"><a:avLst/></a:prstGeom><a:noFill/>${line}</xdr:spPr></xdr:sp>`,
      )
    }
    out.push('<xdr:clientData/></xdr:twoCellAnchor>')
  })
  out.push('</xdr:wsDr>')
  return out.join('')
}

// ── zip ────────────────────────────────────────────────────

let crcTable: Uint32Array | null = null

export function crc32(data: Uint8Array): number {
  if (!crcTable) {
    crcTable = new Uint32Array(256)
    for (let n = 0; n < 256; n++) {
      let c = n
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
      crcTable[n] = c >>> 0
    }
  }
  let crc = 0xffffffff
  for (let i = 0; i < data.length; i++) crc = crcTable[(crc ^ data[i]!) & 0xff]! ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

async function deflateRaw(data: Uint8Array): Promise<Uint8Array | null> {
  if (typeof CompressionStream !== 'function') return null
  try {
    const stream = new Blob([data as BlobPart]).stream().pipeThrough(new CompressionStream('deflate-raw' as CompressionFormat))
    return new Uint8Array(await new Response(stream).arrayBuffer())
  } catch {
    return null
  }
}

/** zip に固める。**名前は ASCII に限る**（xlsx の部品名はすべて ASCII） */
export async function zip(files: { name: string; data: Uint8Array }[]): Promise<Blob> {
  const parts: BlobPart[] = []
  const central: Uint8Array[] = []
  let offset = 0
  const enc = new TextEncoder()
  for (const f of files) {
    const name = enc.encode(f.name)
    const crc = crc32(f.data)
    const deflated = await deflateRaw(f.data)
    const method = deflated ? 8 : 0
    const body = deflated ?? f.data
    const head = new DataView(new ArrayBuffer(30))
    head.setUint32(0, 0x04034b50, true)
    head.setUint16(4, 20, true)
    head.setUint16(6, 0, true)
    head.setUint16(8, method, true)
    head.setUint16(10, 0, true) // 時刻
    head.setUint16(12, 0x21, true) // 1980-01-01
    head.setUint32(14, crc, true)
    head.setUint32(18, body.length, true)
    head.setUint32(22, f.data.length, true)
    head.setUint16(26, name.length, true)
    head.setUint16(28, 0, true)
    parts.push(head.buffer, name as BlobPart, body as BlobPart)
    const cd = new DataView(new ArrayBuffer(46))
    cd.setUint32(0, 0x02014b50, true)
    cd.setUint16(4, 20, true)
    cd.setUint16(6, 20, true)
    cd.setUint16(8, 0, true)
    cd.setUint16(10, method, true)
    cd.setUint16(12, 0, true)
    cd.setUint16(14, 0x21, true)
    cd.setUint32(16, crc, true)
    cd.setUint32(20, body.length, true)
    cd.setUint32(24, f.data.length, true)
    cd.setUint16(28, name.length, true)
    cd.setUint32(42, offset, true)
    const entry = new Uint8Array(46 + name.length)
    entry.set(new Uint8Array(cd.buffer), 0)
    entry.set(name, 46)
    central.push(entry)
    offset += 30 + name.length + body.length
  }
  const cdSize = central.reduce((n, e) => n + e.length, 0)
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, files.length, true)
  end.setUint16(10, files.length, true)
  end.setUint32(12, cdSize, true)
  end.setUint32(16, offset, true)
  return new Blob([...parts, ...(central as BlobPart[]), end.buffer], {
    type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  })
}

// ── ブック ─────────────────────────────────────────────────

/** ブックを xlsx の Blob にする */
export async function buildXlsx(book: XBook): Promise<Blob> {
  const styles = new StyleTable(book.font ?? 'Calibri')
  const enc = new TextEncoder()
  const x = (s: string) => enc.encode(s)
  const head = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
  const R = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
  const files: { name: string; data: Uint8Array }[] = []
  const overrides: string[] = []
  let drawingNo = 0

  book.sheets.forEach((sh, i) => {
    const n = i + 1
    const hasDrawing = (sh.shapes?.length ?? 0) > 0
    files.push({ name: `xl/worksheets/sheet${n}.xml`, data: x(sheetXml(sh, styles, hasDrawing)) })
    overrides.push(`<Override PartName="/xl/worksheets/sheet${n}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`)
    if (hasDrawing) {
      drawingNo++
      files.push({
        name: `xl/worksheets/_rels/sheet${n}.xml.rels`,
        data: x(`${head}<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="${R}/drawing" Target="../drawings/drawing${drawingNo}.xml"/></Relationships>`),
      })
      files.push({ name: `xl/drawings/drawing${drawingNo}.xml`, data: x(drawingXml(sh.shapes!)) })
      overrides.push(`<Override PartName="/xl/drawings/drawing${drawingNo}.xml" ContentType="application/vnd.openxmlformats-officedocument.drawing+xml"/>`)
    }
  })

  const names = Object.entries(book.names ?? {})
    .map(([k, v]) => `<definedName name="${xmlText(k)}">${xmlText(v)}</definedName>`)
    .join('')
  const workbook =
    head +
    `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="${R}">` +
    `<sheets>${book.sheets.map((sh, i) => `<sheet name="${xmlText(sheetName(sh.name))}" sheetId="${i + 1}" r:id="rId${i + 1}"/>`).join('')}</sheets>` +
    (names ? `<definedNames>${names}</definedNames>` : '') +
    // 式の値は開いたときに計算させる（書き出しは値を持たない）
    '<calcPr calcId="191029" fullCalcOnLoad="1"/>' +
    '</workbook>'
  const wbRels =
    head +
    '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">' +
    book.sheets.map((_, i) => `<Relationship Id="rId${i + 1}" Type="${R}/worksheet" Target="worksheets/sheet${i + 1}.xml"/>`).join('') +
    `<Relationship Id="rId${book.sheets.length + 1}" Type="${R}/styles" Target="styles.xml"/>` +
    '</Relationships>'

  return zip([
    {
      name: '[Content_Types].xml',
      data: x(
        head +
          '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">' +
          '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>' +
          '<Default Extension="xml" ContentType="application/xml"/>' +
          '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>' +
          overrides.join('') +
          '<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>' +
          '</Types>',
      ),
    },
    {
      name: '_rels/.rels',
      data: x(`${head}<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="${R}/officeDocument" Target="xl/workbook.xml"/></Relationships>`),
    },
    { name: 'xl/workbook.xml', data: x(workbook) },
    { name: 'xl/_rels/workbook.xml.rels', data: x(wbRels) },
    ...files,
    // 書式の表は、シートを書き終えてから（書きながら育てるため）
    { name: 'xl/styles.xml', data: x(styles.xml()) },
  ])
}
