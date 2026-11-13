// Line icons from Lucide (https://lucide.dev, ISC license): one stroke and
// one size everywhere, where text glyphs drew at the font's whim.

const paths: Record<string, string> = {
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  'file-plus':
    '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M9 15h6"/><path d="M12 18v-6"/>',
  diamond:
    '<path d="M2.7 10.3a2.41 2.41 0 0 0 0 3.41l7.59 7.59a2.41 2.41 0 0 0 3.41 0l7.59-7.59a2.41 2.41 0 0 0 0-3.41l-7.59-7.59a2.41 2.41 0 0 0-3.41 0Z"/>',
  plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
  settings: '<path d="M20 7h-9"/><path d="M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>',
  keyboard:
    '<path d="M10 8h.01"/><path d="M12 12h.01"/><path d="M14 8h.01"/><path d="M16 12h.01"/><path d="M18 8h.01"/><path d="M6 8h.01"/><path d="M7 16h10"/><path d="M8 12h.01"/><rect width="20" height="16" x="2" y="4" rx="2"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  'arrow-left': '<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>',
  'arrow-right': '<path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>',
  'panel-left': '<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>',
  'chevron-right': '<path d="m9 18 6-6-6-6"/>',
  'arrow-up-right': '<path d="M7 7h10v10"/><path d="M7 17 17 7"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  hash: '<line x1="4" x2="20" y1="9" y2="9"/><line x1="4" x2="20" y1="15" y2="15"/><line x1="10" x2="8" y1="3" y2="21"/><line x1="16" x2="14" y1="3" y2="21"/>',
  'circle-help': '<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>',
  text: '<path d="M17 6.1H3"/><path d="M21 12.1H3"/><path d="M15.1 18H3"/>',
  // States: idle is a ring, running a dot, waiting a solid diamond.
  idle: '<circle cx="12" cy="12" r="6.5"/>',
  running: '<circle cx="12" cy="12" r="5" fill="currentColor"/>',
  waiting: '<path d="M12 4.5 19.5 12 12 19.5 4.5 12Z" fill="currentColor"/>',
  square: '<rect x="7.5" y="7.5" width="9" height="9" rx="2" fill="currentColor" stroke="none"/>',
}

export function Icon(props: { name: string; size?: number; className?: string }) {
  const size = props.size ?? 15
  return (
    <svg
      className={`icon ${props.className ?? ''}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      dangerouslySetInnerHTML={{ __html: paths[props.name] ?? '' }}
    />
  )
}

const glyphs: Record<string, string> = {
  '○': 'idle',
  '●': 'running',
  '◆': 'waiting',
  '✓': 'check',
  '◇': 'diamond',
  '›': 'chevron-right',
  '≡': 'text',
  '▪': 'square',
  '↗': 'arrow-up-right',
  '✗': 'x',
  '#': 'hash',
  '?': 'circle-help',
}

// Glyph draws the glyph of an item, a state or a toast as its icon; a
// glyph with no icon stays text.
export function Glyph(props: { g: string; size?: number }) {
  const name = glyphs[props.g]
  return name ? <Icon name={name} size={props.size ?? 14} /> : <>{props.g}</>
}
