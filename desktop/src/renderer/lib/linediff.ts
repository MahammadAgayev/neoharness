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

// Hunk is a change between mine, lines mFrom..mTo, and theirs, lines
// tFrom..tTo; at is the index of its first op.
export interface Hunk {
  mFrom: number
  mTo: number
  tFrom: number
  tTo: number
  at: number
}

export function hunks(ops: Op[]): Hunk[] {
  const out: Hunk[] = []
  let i = 0
  let j = 0
  for (let k = 0; k < ops.length; ) {
    if (ops[k].kind === ' ') {
      i++
      j++
      k++
      continue
    }
    const h: Hunk = { mFrom: i, mTo: i, tFrom: j, tTo: j, at: k }
    for (; k < ops.length && ops[k].kind !== ' '; k++) {
      if (ops[k].kind === '-') i++
      else j++
    }
    h.mTo = i
    h.tTo = j
    out.push(h)
  }
  return out
}

// take is do: the change of h comes from theirs into mine.
export function take(mine: string[], theirs: string[], h: Hunk): string[] {
  return [...mine.slice(0, h.mFrom), ...theirs.slice(h.tFrom, h.tTo), ...mine.slice(h.mTo)]
}
