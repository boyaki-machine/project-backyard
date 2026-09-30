/**
 * 最小の xlsx 書き出し（`GuiDesign.md` 5.14「Excel 出力」。pb-223）。
 *
 * **依存ライブラリを足さない。** xlsx は XML（SpreadsheetML）を zip で固めたもので、ガントの
 * 出力に要るのは、1枚のシート・塗り（単色と斜線）・罫・文字の色と太さ・字下げ・結合・
 * 列幅と列の既定の書式・行の階層（アウトライン）・ウィンドウ枠の固定だけである。
 *
 * **文字列はセルに直接書く**（`inlineStr`）。共有文字列の表を作らないので、書き出しが
 * 1回の走査で済む。**書式は同じ組を1つにまとめ**、`cellXfs` の番号で参照する。
 *
 * **zip の圧縮はブラウザの `CompressionStream('deflate-raw')` を使う**（無ければ無圧縮）。
 * 圧縮のコードを持ち込まない。
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
}

export interface XCell {
  v?: string | number
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
}

export interface XSheet {
  name: string
  cols: XCol[]
  rows: XRow[]
  /** 結合（0始まりの行・列の範囲） */
  merges: { r0: number; c0: number; r1: number; c1: number }[]
  /** 固定する列と行の数 */
  freeze?: { cols: number; rows: number }
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

/** シート名。Excel の制約（31文字、`[]:*?/\` を含まない）に合わせる */
export function sheetName(s: string): string {
  const t = s.replace(/[[\]:*?/\\]/g, ' ').trim().slice(0, 31)
  return t === '' ? 'Sheet1' : t
}

// ── 書式の表（styles.xml）──────────────────────────────────

class StyleTable {
  private fonts: string[] = []
  private fills: string[] = ['<fill><patternFill patternType="none"/></fill>', '<fill><patternFill patternType="gray125"/></fill>']
  private borders: string[] = ['<border><left/><right/><top/><bottom/><diagonal/></border>']
  private xfs: string[] = []
  private index = new Map<string, number>()

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
    let borderId = 0
    if (st.border) {
      const side = (name: string, b?: XBorderSide) =>
        b ? `<${name} style="${b.style}"><color rgb="${argb(b.color)}"/></${name}>` : `<${name}/>`
      const b = st.border
      borderId = this.intern(this.borders, `<border>${side('left', b.left)}${side('right', b.right)}${side('top', b.top)}${side('bottom', b.bottom)}<diagonal/></border>`)
    }
    const a = st.align
    const align = a
      ? `<alignment${a.h ? ` horizontal="${a.h}"` : ''}${a.v ? ` vertical="${a.v}"` : ''}${a.indent ? ` indent="${a.indent}"` : ''}/>`
      : ''
    const xf =
      `<xf numFmtId="0" fontId="${fontId}" fillId="${fillId}" borderId="${borderId}" xfId="0"` +
      `${fontId ? ' applyFont="1"' : ''}${fillId ? ' applyFill="1"' : ''}${borderId ? ' applyBorder="1"' : ''}${align ? ' applyAlignment="1"' : ''}` +
      (align ? `>${align}</xf>` : '/>')
    this.xfs.push(xf)
    const id = this.xfs.length - 1
    this.index.set(key, id)
    return id
  }

  xml(): string {
    return (
      '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' +
      '<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">' +
      `<fonts count="${this.fonts.length}">${this.fonts.join('')}</fonts>` +
      `<fills count="${this.fills.length}">${this.fills.join('')}</fills>` +
      `<borders count="${this.borders.length}">${this.borders.join('')}</borders>` +
      '<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>' +
      `<cellXfs count="${this.xfs.length}">${this.xfs.join('')}</cellXfs>` +
      '<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>' +
      '</styleSheet>'
    )
  }
}

// ── シート（sheet1.xml）────────────────────────────────────

function sheetXml(sh: XSheet, styles: StyleTable): string {
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
      out.push(`<col min="${i + 1}" max="${i + 1}" width="${c.width}" customWidth="1"${s ? ` style="${s}"` : ''}/>`)
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
      // 値の無いセルは、列の書式を上書きする書式があるときだけ書く
      const s = cell.s ? styles.id(cell.s) : sh.cols[c]?.style ? styles.id(sh.cols[c]!.style) : 0
      const ref = cellRef(r, c)
      const sa = s ? ` s="${s}"` : ''
      if (cell.v === undefined || cell.v === '') out.push(`<c r="${ref}"${sa}/>`)
      else if (typeof cell.v === 'number') out.push(`<c r="${ref}"${sa}><v>${cell.v}</v></c>`)
      else out.push(`<c r="${ref}"${sa} t="inlineStr"><is><t xml:space="preserve">${xmlText(cell.v)}</t></is></c>`)
    }
    out.push('</row>')
  })
  out.push('</sheetData>')
  if (sh.merges.length > 0) {
    out.push(`<mergeCells count="${sh.merges.length}">`)
    for (const m of sh.merges) out.push(`<mergeCell ref="${cellRef(m.r0, m.c0)}:${cellRef(m.r1, m.c1)}"/>`)
    out.push('</mergeCells>')
  }
  out.push('<pageMargins left="0.4" right="0.4" top="0.5" bottom="0.5" header="0.3" footer="0.3"/></worksheet>')
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

/** シート1枚のブックを xlsx の Blob にする */
export async function buildXlsx(sh: XSheet): Promise<Blob> {
  const styles = new StyleTable(sh.font ?? 'Calibri')
  const sheet = sheetXml(sh, styles) // 書式の表は、シートを書きながら育てる
  const enc = new TextEncoder()
  const x = (s: string) => enc.encode(s)
  const head = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
  return zip([
    {
      name: '[Content_Types].xml',
      data: x(
        head +
          '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">' +
          '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>' +
          '<Default Extension="xml" ContentType="application/xml"/>' +
          '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>' +
          '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>' +
          '<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>' +
          '</Types>',
      ),
    },
    {
      name: '_rels/.rels',
      data: x(
        head +
          '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">' +
          '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>' +
          '</Relationships>',
      ),
    },
    {
      name: 'xl/workbook.xml',
      data: x(
        head +
          '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">' +
          `<sheets><sheet name="${xmlText(sheetName(sh.name))}" sheetId="1" r:id="rId1"/></sheets>` +
          '</workbook>',
      ),
    },
    {
      name: 'xl/_rels/workbook.xml.rels',
      data: x(
        head +
          '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">' +
          '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>' +
          '<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>' +
          '</Relationships>',
      ),
    },
    { name: 'xl/worksheets/sheet1.xml', data: x(sheet) },
    { name: 'xl/styles.xml', data: x(styles.xml()) },
  ])
}
