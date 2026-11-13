// Color schemes. Every color in the app is a CSS variable of the scheme, so a
// new scheme is one more entry here. The names of the colors are the TUI's
// (internal/tui/theme.go): Love only for errors and things that need the
// person, Iris for "you type here".

export interface Palette {
  dark: boolean
  base: string
  surface: string
  overlay: string
  muted: string
  subtle: string
  text: string
  love: string
  gold: string
  rose: string
  pine: string
  foam: string
  iris: string
  hlLow: string
  hlMed: string
  hlHigh: string
}

// Rosé Pine, https://rosepinetheme.com/palette
export const schemes: Record<string, Palette> = {
  'rose-pine-moon': {
    dark: true,
    base: '#232136',
    surface: '#2a273f',
    overlay: '#393552',
    muted: '#6e6a86',
    subtle: '#908caa',
    text: '#e0def4',
    love: '#eb6f92',
    gold: '#f6c177',
    rose: '#ea9a97',
    pine: '#3e8fb0',
    foam: '#9ccfd8',
    iris: '#c4a7e7',
    hlLow: '#2a283e',
    hlMed: '#44415a',
    hlHigh: '#56526e',
  },
  'rose-pine': {
    dark: true,
    base: '#191724',
    surface: '#1f1d2e',
    overlay: '#26233a',
    muted: '#6e6a86',
    subtle: '#908caa',
    text: '#e0def4',
    love: '#eb6f92',
    gold: '#f6c177',
    rose: '#ebbcba',
    pine: '#31748f',
    foam: '#9ccfd8',
    iris: '#c4a7e7',
    hlLow: '#21202e',
    hlMed: '#403d52',
    hlHigh: '#524f67',
  },
  'rose-pine-dawn': {
    dark: false,
    base: '#faf4ed',
    surface: '#fffaf3',
    overlay: '#f2e9e1',
    muted: '#9893a5',
    subtle: '#797593',
    text: '#575279',
    love: '#b4637a',
    gold: '#ea9d34',
    rose: '#d7827e',
    pine: '#286983',
    foam: '#56949f',
    iris: '#907aa9',
    hlLow: '#f4ede8',
    hlMed: '#dfdad9',
    hlHigh: '#cecacd',
  },
}

// "system" follows the macOS appearance: the first scheme for dark, the
// second for light.
export const systemPair: [string, string] = ['rose-pine-moon', 'rose-pine-dawn']

export const schemeNames = ['system', ...Object.keys(schemes)]

export function resolveScheme(name: string, systemDark: boolean): Palette {
  if (name === 'system' || !schemes[name]) return schemes[systemPair[systemDark ? 0 : 1]]
  return schemes[name]
}

type RGB = [number, number, number]

const rgb = (hex: string): RGB => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as RGB
const hex = (c: RGB): string => '#' + c.map((v) => Math.round(v).toString(16).padStart(2, '0')).join('')
const mix = (a: string, b: string, p: number): string => {
  const x = rgb(a)
  const y = rgb(b)
  return hex(x.map((v, i) => v * p + y[i] * (1 - p)) as RGB)
}

function luminance(c: string): number {
  const [r, g, b] = rgb(c).map((v) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

// contrast is the WCAG ratio of two colors, from 1 to 21.
export function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((m, n) => n - m)
  return (x + 0.05) / (y + 0.05)
}

// legible takes c toward black or white, a step at a time, until it reads at
// ratio on every background. The hue stays the scheme's.
function legible(c: string, ink: string, bgs: string[], ratio: number): string {
  for (let p = 1; p >= 0; p -= 0.02) {
    const m = mix(c, ink, p)
    if (bgs.every((b) => contrast(m, b) >= ratio)) return m
  }
  return ink
}

// The contrast of each level of text. Rosé Pine keeps its text soft for code
// in a terminal; a window of prose and labels needs darker ink, and no text
// below 4.5, the WCAG minimum, even the muted one.
export const ratios = { text: 12, subtle: 6.5, muted: 4.6 }

export function colors(p: Palette): Record<string, string> {
  const ink = p.dark ? '#ffffff' : '#000000'
  // The shell (title bar, sidebar) sits under the panel the work is on; in
  // the light schemes only a little darker, so the edges stay calm.
  const shell = p.dark ? mix(p.base, '#000000', 0.7) : mix(p.overlay, p.surface, 0.55)
  const panel = p.dark ? p.base : p.surface
  const bgs = [shell, panel, p.surface]
  return {
    '--base': p.base,
    '--surface': p.surface,
    '--overlay': p.overlay,
    '--shell': shell,
    '--panel': panel,
    '--muted': legible(mix(p.muted, p.subtle, 0.55), ink, bgs, ratios.muted),
    '--subtle': legible(p.subtle, ink, bgs, ratios.subtle),
    '--text': legible(p.text, ink, bgs, ratios.text),
    // The accents color text too: a state, a link, a heading.
    '--love': legible(p.love, ink, bgs, ratios.muted),
    '--gold': legible(p.gold, ink, bgs, ratios.muted),
    '--rose': legible(p.rose, ink, bgs, ratios.muted),
    '--pine': legible(p.pine, ink, bgs, ratios.muted),
    '--foam': legible(p.foam, ink, bgs, ratios.muted),
    '--iris': legible(p.iris, ink, bgs, ratios.muted),
    '--hl-low': p.hlLow,
    '--hl-med': p.hlMed,
    '--hl-high': p.hlHigh,
  }
}

export function applyScheme(p: Palette, root: HTMLElement): void {
  for (const [k, v] of Object.entries(colors(p))) root.style.setProperty(k, v)
  root.style.colorScheme = p.dark ? 'dark' : 'light'
}
