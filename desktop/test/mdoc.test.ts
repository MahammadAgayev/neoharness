import fs from 'node:fs'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

import { Fragment, Slice } from 'prosemirror-model'

import { html, MarkdownDoc, normalize, parse, schema, toMarkdown } from '../src/renderer/lib/mdoc'

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
})
