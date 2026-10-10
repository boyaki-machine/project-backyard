// Mask parser-recognized comments without joining text or changing line numbers.
// This is for inspecting Vue source, not for sanitizing HTML for display.
export function templateWithoutComments(block) {
  if (!block) return ''
  const chars = block.content.split('')
  function visit(node) {
    if (node.type === 3) {
      const start = node.loc.start.offset - block.loc.start.offset
      const end = node.loc.end.offset - block.loc.start.offset
      for (let i = start; i < end; i++) {
        if (chars[i] !== '\n' && chars[i] !== '\r') chars[i] = ' '
      }
    }
    for (const child of node.children ?? []) visit(child)
  }
  visit(block.ast)
  return chars.join('')
}
