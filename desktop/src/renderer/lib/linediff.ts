// Line diff, as internal/linediff: the common start and end are cut first,
// then the longest common run of lines matches the middle.

export interface Op {
  kind: ' ' | '-' | '+'
  text: string
}

// maxCells bounds the table. Past it, the changed middle shows as removed
// and added as a whole.
const maxCells = 4_000_000

export function lines(a: string[], b: string[]): Op[] {
  let p = 0
  while (p < a.length && p < b.length && a[p] === b[p]) p++
  let s = 0
  while (s < a.length - p && s < b.length - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++
  const out: Op[] = a.slice(0, p).map((text) => ({ kind: ' ', text }))
  const ma = a.slice(p, a.length - s)
  const mb = b.slice(p, b.length - s)
  const n = ma.length
  const m = mb.length
  if ((n + 1) * (m + 1) > maxCells) {
    for (const text of ma) out.push({ kind: '-', text })
    for (const text of mb) out.push({ kind: '+', text })
  } else {
    const lcs = new Int32Array((n + 1) * (m + 1))
    const at = (i: number, j: number) => i * (m + 1) + j
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[at(i, j)] = ma[i] === mb[j] ? lcs[at(i + 1, j + 1)] + 1 : Math.max(lcs[at(i + 1, j)], lcs[at(i, j + 1)])
      }
    }
    let i = 0
    let j = 0
    while (i < n || j < m) {
      if (i < n && j < m && ma[i] === mb[j]) {
        out.push({ kind: ' ', text: ma[i] })
        i++
        j++
      } else if (i < n && (j === m || lcs[at(i + 1, j)] >= lcs[at(i, j + 1)])) {
        out.push({ kind: '-', text: ma[i++] })
      } else {
        out.push({ kind: '+', text: mb[j++] })
      }
    }
  }
  for (const text of a.slice(a.length - s)) out.push({ kind: ' ', text })
  return out
}
