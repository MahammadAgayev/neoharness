import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/common'
import { Marked } from 'marked'

// Agents write the markdown, so the HTML is sanitized before it reaches the
// page: no scripts, no handlers, no frames.
const marked = new Marked({
  gfm: true,
  // A new line in a message is a new line, as in chat apps and the TUI.
  breaks: true,
  renderer: {
    code({ text, lang }) {
      const language = lang && hljs.getLanguage(lang) ? lang : ''
      const html = language ? hljs.highlight(text, { language }).value : escapeHTML(text)
      const label = lang ? `<span class="code-lang">${escapeHTML(lang)}</span>` : ''
      return `<pre class="code">${label}<button class="copy" data-copy>Copy</button><code class="hljs">${html}</code></pre>`
    },
  },
})

export function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

const alertTitles: Record<string, string> = {
  NOTE: 'Note',
  TIP: 'Tip',
  IMPORTANT: 'Important',
  WARNING: 'Warning',
  CAUTION: 'Caution',
}

// rendered keeps the HTML of the texts shown last, so a chat that opens
// again, or a window that mounts again, does not parse its replies again.
const rendered = new Map<string, string>()
const renderedMax = 4000
const artifactKeys = new WeakMap<Set<string>, string>()

// render turns markdown into safe HTML. An inline code span that names an
// artifact becomes a link to it, as <CR> on its name opens it in the TUI.
export function render(md: string, artifacts?: Set<string>): string {
  let names = ''
  if (artifacts?.size) {
    names = artifactKeys.get(artifacts) ?? [...artifacts].sort().join(' ')
    artifactKeys.set(artifacts, names)
  }
  const key = names + '\0' + md
  const hit = rendered.get(key)
  if (hit !== undefined) {
    rendered.delete(key)
    rendered.set(key, hit)
    return hit
  }
  const html = parse(md, artifacts)
  rendered.set(key, html)
  if (rendered.size > renderedMax) rendered.delete(rendered.keys().next().value!)
  return html
}

// renderBlocks renders markdown a block at a time, each from the cache, so a
// reply that streamed in, or grew, parses only its new blocks.
export function renderBlocks(md: string, artifacts?: Set<string>): string {
  return blocks(md)
    .map((b) => render(b, artifacts))
    .join('')
}

// blocks splits markdown at blank lines, but not inside fenced code, and
// not before an indented line, which goes on the block above it (a list
// item's second paragraph, an indented code block).
export function blocks(md: string): string[] {
  const out: string[] = []
  let cur: string[] = []
  let gap = 0
  let fence = ''
  for (const line of md.split('\n')) {
    const t = line.trim()
    if (fence) {
      cur.push(line)
      if (t.startsWith(fence) && /^[`~]+$/.test(t)) fence = ''
      continue
    }
    if (t === '') {
      gap++
      continue
    }
    if (gap && cur.length && !/^\s/.test(line)) {
      out.push(cur.join('\n'))
      cur = []
    } else if (gap && cur.length) cur.push(...Array(gap).fill(''))
    gap = 0
    cur.push(line)
    const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line)
    if (f) fence = f[1]
  }
  if (cur.length) out.push(cur.join('\n'))
  return out
}

// openFence says a block is fenced code whose closing fence has not come
// yet, as the last block of a reply still being written.
export function openFence(block: string): boolean {
  const lines = block.split('\n')
  const f = /^\s{0,3}(`{3,}|~{3,})/.exec(lines[0])
  if (!f) return false
  return !lines.slice(1).some((l) => l.trim().startsWith(f[1]) && /^[`~]+$/.test(l.trim()))
}

// renderOnce renders markdown that changes as it is written, without the
// cache: the cache would fill with every step of it.
export function renderOnce(md: string, artifacts?: Set<string>): string {
  return parse(md, artifacts)
}

function parse(md: string, artifacts?: Set<string>): string {
  let html = DOMPurify.sanitize(marked.parse(md, { async: false }) as string, {
    // Task lists are disabled checkboxes; no form can send them anywhere.
    FORBID_TAGS: ['style', 'form', 'iframe'],
    FORBID_ATTR: ['style'],
  })
  // GitHub's alerts: a quote that starts with [!NOTE] and the like.
  html = html.replace(
    /<blockquote>\s*<p>\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*/g,
    (_m, kind: string) => `<blockquote class="alert alert-${kind.toLowerCase()}"><p class="alert-title">${alertTitles[kind]}</p><p>`,
  )
  if (artifacts && artifacts.size > 0) {
    html = html.replace(/<code>([a-z0-9][a-z0-9-]{0,39})<\/code>/g, (m, name: string) =>
      artifacts.has(name) ? `<code class="artifact-link" data-artifact="${name}">${name}</code>` : m,
    )
  }
  return html
}

// asJSON shows a reply that is one JSON value as a highlighted block.
export function asJSON(text: string): string | null {
  const t = text.trim()
  if (!(t.startsWith('{') || t.startsWith('['))) return null
  try {
    return '```json\n' + JSON.stringify(JSON.parse(t), null, 2) + '\n```'
  } catch {
    return null
  }
}

// outline is the headings of a markdown text: its line, level and title.
export function outline(md: string): { line: number; level: number; title: string }[] {
  const out: { line: number; level: number; title: string }[] = []
  let fence = false
  md.split('\n').forEach((l, line) => {
    if (/^ {0,3}(`{3,}|~{3,})/.test(l)) fence = !fence
    if (fence) return
    const m = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(l)
    if (m) out.push({ line, level: m[1].length, title: m[2] })
  })
  return out
}
