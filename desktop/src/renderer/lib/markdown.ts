import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/common'
import { Marked } from 'marked'

// Agents write the markdown, so the HTML is sanitized before it reaches the
// page: no scripts, no handlers, no frames.
const marked = new Marked({
  gfm: true,
  breaks: false,
  renderer: {
    code({ text, lang }) {
      const language = lang && hljs.getLanguage(lang) ? lang : ''
      const html = language ? hljs.highlight(text, { language }).value : escapeHTML(text)
      const label = lang ? `<span class="code-lang">${escapeHTML(lang)}</span>` : ''
      return `<pre class="code">${label}<code class="hljs">${html}</code></pre>`
    },
  },
})

export function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!)
}

// render turns markdown into safe HTML. An inline code span that names an
// artifact becomes a link to it, as <CR> on its name opens it in the TUI.
export function render(md: string, artifacts?: Set<string>): string {
  let html = DOMPurify.sanitize(marked.parse(md, { async: false }) as string, {
    FORBID_TAGS: ['style', 'form', 'input', 'iframe'],
    FORBID_ATTR: ['style'],
  })
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
