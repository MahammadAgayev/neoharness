// Fuzzy match as the TUI pickers do (tui/picker.go): the query's
// letters in order, more for runs and for word starts, less for long text.

export function fuzzyScore(text: string, q: string): number | null {
  let score = 0
  let qi = 0
  let run = 0
  for (let i = 0; i < text.length && qi < q.length; i++) {
    if (text[i] === q[qi]) {
      run++
      score += 1 + run * 2
      if (i === 0 || !/\p{L}/u.test(text[i - 1])) score += 3
      qi++
    } else {
      run = 0
    }
  }
  return qi === q.length ? score - Math.floor(text.length / 10) : null
}

export function fuzzy<T extends { label: string; detail?: string }>(items: T[], q: string): T[] {
  if (!q) return items
  const ql = q.toLowerCase()
  const scored: { it: T; s: number }[] = []
  for (const it of items) {
    const s = fuzzyScore(`${it.label} ${it.detail ?? ''}`.toLowerCase(), ql)
    if (s !== null) scored.push({ it, s })
  }
  // Array.prototype.sort is stable, so equal scores keep their order.
  return scored.sort((a, b) => b.s - a.s).map((x) => x.it)
}

// grep finds q in lines of text, smart case: a query with an upper-case
// letter matches case exactly. It returns the line and a snippet that starts
// a little before the match.
export function grep(text: string, q: string): { line: number; snippet: string }[] {
  if (!q.trim()) return []
  const fold = q.toLowerCase() === q
  const out: { line: number; snippet: string }[] = []
  text.split('\n').forEach((l, line) => {
    const at = (fold ? l.toLowerCase() : l).indexOf(q)
    if (at < 0) return
    let snippet = l.trim()
    if (at > 24) {
      const cut = l.lastIndexOf(' ', at - 12)
      snippet = '…' + l.slice(Math.max(cut, 0)).trim()
    }
    out.push({ line, snippet })
  })
  return out
}
