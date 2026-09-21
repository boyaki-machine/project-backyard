import fs from 'node:fs'
import path from 'node:path'
import ts from 'typescript'
import { parse as parseSfc } from '@vue/compiler-sfc'

const sourceRoot = path.resolve('src')
const japanese = /[ぁ-んァ-ヶ一-龠]/u
const ignored = new Set([
  path.join(sourceRoot, 'api', 'schema.d.ts'),
  path.join(sourceRoot, 'locales', 'en', 'ui.ts'),
])
const errors = []
const usedKeys = new Set()

const englishSource = fs.readFileSync(path.join(sourceRoot, 'locales', 'en', 'ui.ts'), 'utf8')
const englishAst = ts.createSourceFile('ui.ts', englishSource, ts.ScriptTarget.Latest, true)
const englishKeys = new Set()
const englishErrors = []
const placeholders = (value) => [...value.matchAll(/\{([^}]+)\}/gu)].map((match) => match[1]).sort().join(',')
function collectEnglishKeys(node) {
  if (ts.isPropertyAssignment(node) && ts.isStringLiteral(node.name)) {
    englishKeys.add(node.name.text)
    if (ts.isStringLiteral(node.initializer)) {
      if (japanese.test(node.initializer.text)) {
        englishErrors.push(`English message still contains Japanese: ${node.name.text}`)
      }
      if (placeholders(node.name.text) !== placeholders(node.initializer.text)) {
        englishErrors.push(`placeholder mismatch: ${node.name.text}`)
      }
    }
  }
  ts.forEachChild(node, collectEnglishKeys)
}
collectEnglishKeys(englishAst)

function filesBelow(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(dir, entry.name)
    if (entry.isDirectory()) return filesBelow(target)
    return /\.(ts|vue)$/u.test(entry.name) ? [target] : []
  })
}

function lineAt(text, offset) {
  return text.slice(0, offset).split('\n').length
}

function record(file, line, text) {
  errors.push(`${path.relative(process.cwd(), file)}:${line}: ${text.trim()}`)
}

function checkScript(file, source, lineOffset = 0) {
  const parsed = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const literalKinds = new Set([
    ts.SyntaxKind.StringLiteral,
    ts.SyntaxKind.NoSubstitutionTemplateLiteral,
    ts.SyntaxKind.TemplateHead,
    ts.SyntaxKind.TemplateMiddle,
    ts.SyntaxKind.TemplateTail,
  ])

  function visit(node) {
    if (literalKinds.has(node.kind) && japanese.test(node.text ?? '')) {
      const call = node.parent
      const callee = ts.isCallExpression(call) ? call.expression.getText(parsed) : ''
      if (ts.isCallExpression(call) && call.arguments[0] === node && callee === 'uiText') {
        usedKeys.add(node.text)
        return
      }
      let declaration = node.parent
      while (declaration && !ts.isVariableDeclaration(declaration)) declaration = declaration.parent
      if (
        ts.isVariableDeclaration(declaration) &&
        ts.isIdentifier(declaration.name) &&
        declaration.name.text.endsWith('Sources')
      ) {
        usedKeys.add(node.text)
        return
      }
      const position = parsed.getLineAndCharacterOfPosition(node.getStart(parsed))
      record(file, lineOffset + position.line + 1, node.text)
    }
    ts.forEachChild(node, visit)
  }
  visit(parsed)
}

for (const file of filesBelow(sourceRoot)) {
  if (ignored.has(file) || file.includes(`${path.sep}locales${path.sep}ja${path.sep}`)) continue
  const source = fs.readFileSync(file, 'utf8')

  if (file.endsWith('.ts')) {
    checkScript(file, source)
    continue
  }

  const template = parseSfc(source, { filename: file }).descriptor.template
  if (template) {
    const withoutComments = template.content
      .replace(/<!--[\s\S]*?-->/gu, '')
      .replace(/\$ui\(\s*(?:'((?:\\.|[^'\\])*)'|"((?:\\.|[^"\\])*)")/gu, (_match, single, double) => {
        const key = (single ?? double).replace(/\\(['"\\])/gu, '$1')
        usedKeys.add(key)
        return '$ui('
      })
    withoutComments.split('\n').forEach((line, index) => {
      if (japanese.test(line)) {
        record(file, lineAt(source, template.loc.start.offset) + index, line)
      }
    })
  }

  for (const script of source.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/gu)) {
    const start = script.index + script[0].indexOf(script[1])
    checkScript(file, script[1], lineAt(source, start) - 1)
  }
}

for (const key of usedKeys) {
  if (!englishKeys.has(key)) errors.push(`src/locales/en/ui.ts: missing English message for ${key}`)
}
errors.push(...englishErrors.map((error) => `src/locales/en/ui.ts: ${error}`))

if (process.argv.includes('--missing-keys')) {
  console.log(JSON.stringify([...usedKeys].filter((key) => !englishKeys.has(key)), null, 2))
  process.exit(0)
}

if (errors.length > 0) {
  console.error('NG: locales/ja 以外に日本語のUI候補が残っています')
  errors.forEach((error) => console.error(`  ${error}`))
  process.exit(1)
}

console.log('OK: locales/ja 以外に日本語のUIリテラルはありません')
