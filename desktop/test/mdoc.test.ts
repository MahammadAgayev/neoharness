import fs from 'node:fs'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

import { Fragment, Slice } from 'prosemirror-model'

import { blockKind, html, MarkdownDoc, normalize, parse, schema, toMarkdown } from '../src/renderer/lib/mdoc'

const all = `# Plan

Some **bold**, *em*, ~~gone~~, \`code\` and a [link](https://example.com "t").
A second line, as the agent wrote it.

> [!NOTE]
> A note.

> A quote
> on two lines.

* star list
* with \`code\`

1. one
2. two
   - nested

3) loose

- [ ] open task
- [x] done task

| A | B | C |
|:--|:-:|--:|
| \`x\` | **y** | a \\| b |

\`\`\`go
func main() {}
\`\`\`

~~~
tilde fence
~~~

    indented code

---

Setext
======

Text with a [ref link][r] and <https://auto.link>.

[r]: https://example.com/ref

<kbd>raw html</kbd> & 5 < 6
`

// Texts agents write: the repository's docs, and the real artifacts when
// NEOHARNESS_ARTIFACTS names a folder of them.
function samples(): [string, string][] {
  const root = path.join(__dirname, '..', '..')
  const files = [path.join(root, 'AGENTS.md'), path.join(root, 'README.md'), ...fs.readdirSync(path.join(root, 'docs')).filter((f) => f.endsWith('.md')).map((f) => path.join(root, 'docs', f))]
  const dir = process.env.NEOHARNESS_ARTIFACTS
  if (dir) files.push(...fs.readdirSync(dir).filter((f) => f.endsWith('.md')).map((f) => path.join(dir, f)))
  return [['all', all], ...files.map((f) => [path.basename(f), fs.readFileSync(f, 'utf8')] as [string, string])]
}

describe('markdown documents', () => {
  it.each(samples())('%s goes back byte for byte when nothing changed', (_name, text) => {
    const m = new MarkdownDoc(text)
    expect(m.markdown(m.doc)).toBe(text)
  })

  it.each(samples())('%s reads the same when every block is written again', (_name, text) => {
    expect(html(normalize(text))).toBe(html(text))
  })

  it.each(samples())('%s is stable: a block written twice is written the same', (_name, text) => {
    const once = normalize(text)
    expect(normalize(once)).toBe(once)
  })

  it('keeps the nodes agents write', () => {
    const d = parse(all)
    const types = new Set<string>()
    d.descendants((n) => {
      types.add(n.type.name)
      n.marks.forEach((m) => types.add(m.type.name))
    })
    for (const t of ['heading', 'paragraph', 'blockquote', 'bullet_list', 'ordered_list', 'list_item', 'table', 'table_header', 'table_cell', 'code_block', 'horizontal_rule', 'raw', 'strong', 'em', 'strike', 'code', 'link'])
      expect(types).toContain(t)
    let tasks = 0
    d.descendants((n) => {
      if (n.type.name === 'list_item' && n.attrs.checked !== null) tasks++
    })
    expect(tasks).toBe(2)
  })

  it('writes only the block you edited, and keeps the rest as it was', () => {
    const text = '#  Title  \n\n* a\n* b\n\n\n| x  | y |\n|----|---|\n| 1  | 2 |\n'
    const m = new MarkdownDoc(text)
    // Edit the list: add "c" to its last item.
    const pos = m.doc.child(0).nodeSize + m.doc.child(1).nodeSize - 3
    const doc = m.doc.replace(pos, pos, new Slice(Fragment.from(schema.text('c')), 0, 0))
    expect(m.markdown(doc)).toBe('#  Title  \n\n* a\n* bc\n\n\n| x  | y |\n|----|---|\n| 1  | 2 |\n')
  })

  it('a new block goes a blank line from its neighbours, and an empty line you left goes away', () => {
    const m = new MarkdownDoc('# Launch plan\n\n')
    const task = schema.nodes.bullet_list.create(null, schema.nodes.list_item.create({ checked: false }, schema.nodes.paragraph.create(null, schema.text('write the API'))))
    const doc = schema.nodes.doc.create(null, [m.doc.child(0), task, schema.nodes.paragraph.create()])
    expect(m.markdown(doc)).toBe('# Launch plan\n\n- [ ] write the API\n')
  })

  it('an undo back to the old block gives back its old text', () => {
    const m = new MarkdownDoc('*  spaced  star*\n')
    const again = schema.nodeFromJSON(m.doc.toJSON())
    expect(m.markdown(again)).toBe('*  spaced  star*\n')
  })

  it('writes an edited table, task and alert as GFM', () => {
    expect(toMarkdown(parse('| a | b |\n|:-:|--:|\n| 1 | x\\|y |').child(0))).toBe('| a | b |\n|:---:|---:|\n| 1 | x\\|y |')
    expect(toMarkdown(parse('- [X] done\n- [ ] open').child(0))).toBe('- [x] done\n- [ ] open')
    expect(toMarkdown(parse('> [!WARNING]\n> careful').child(0))).toBe('> [!WARNING]\n> careful')
    expect(toMarkdown(parse('````\n```\n````').child(0))).toBe('````\n```\n````')
  })

  it('a line break in a table cell saves as a space, as a GFM cell is one line', () => {
    const { table, table_row, table_header, table_cell, hard_break } = schema.nodes
    const t = table.create(null, [
      table_row.create(null, [table_header.create(null, schema.text('a')), table_header.create(null, schema.text('b'))]),
      table_row.create(null, [table_cell.create(null, [schema.text('x'), hard_break.create(), schema.text('z')]), table_cell.create(null, schema.text('y'))]),
    ])
    expect(toMarkdown(t)).toBe('| a | b |\n|---|---|\n| x z | y |')
  })
})

describe('blockKind', () => {
  it('names the innermost block at a position, and nothing for a paragraph', () => {
    const doc = parse('# One\n\n### Three\n\npara\n\n> quoted\n\n> [!NOTE]\n> careful\n\n- item\n\n1. first\n\n- [ ] task\n\n```go\nx\n```\n\n| h1 | h2 |\n| - | - |\n| c1 | c2 |\n\n---\n\nend\n')
    const at = (text: string) => {
      let p = -1
      doc.descendants((n, pos) => {
        if (p < 0 && n.isText && n.text!.includes(text)) p = pos
        return p < 0
      })
      return p
    }
    const kinds = ['One', 'Three', 'para', 'quoted', 'careful', 'item', 'first', 'task', 'x', 'h2', 'c2'].map((t) => blockKind(doc, at(t)))
    expect(kinds).toEqual(['heading 1', 'heading 3', '', 'quote', 'note', 'bullet list', 'numbered list', 'task list', 'code · go', 'table · row 1, col 2', 'table · row 2, col 2'])
    let rule = -1
    doc.forEach((n, off) => {
      if (n.type.name === 'horizontal_rule') rule = off
    })
    expect(blockKind(doc, rule)).toBe('rule')
  })
})
