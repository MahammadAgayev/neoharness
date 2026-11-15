import { memo, useMemo } from 'react'

import { asJSON, renderBlocks } from '../lib/markdown'
import { store } from '../store'

// copied puts text on the clipboard and says so on the button for a moment.
export function copied(button: HTMLElement, text: string): void {
  window.nh.copy(text)
  button.textContent = 'Copied'
  setTimeout(() => (button.textContent = 'Copy'), 1200)
}

// mdClick is a click in agent text: links go to the browser, and an
// artifact's name opens the artifact.
export function mdClick(e: React.MouseEvent): void {
  const el = e.target as HTMLElement
  const copy = el.closest<HTMLElement>('[data-copy]')
  if (copy) {
    copied(copy, copy.parentElement?.querySelector('code')?.textContent ?? '')
    return
  }
  const a = el.closest('a')
  if (a) {
    e.preventDefault()
    void window.nh.openExternal(a.href)
    return
  }
  const art = el.closest<HTMLElement>('[data-artifact]')
  if (art?.dataset.artifact) void store.openArtifact(art.dataset.artifact)
}

// Markdown shows agent text.
export const Markdown = memo(function Markdown(props: { text: string; artifacts?: Set<string>; className?: string }) {
  const html = useMemo(() => renderBlocks(asJSON(props.text) ?? props.text, props.artifacts), [props.text, props.artifacts])
  return <div className={`md ${props.className ?? ''}`} onClick={mdClick} dangerouslySetInnerHTML={{ __html: html }} />
})
