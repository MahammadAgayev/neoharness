import { memo, useMemo } from 'react'

import { asJSON, render } from '../lib/markdown'
import { store } from '../store'

// Markdown shows agent text. Links go to the browser, and an artifact's
// name opens the artifact.
export const Markdown = memo(function Markdown(props: { text: string; artifacts?: Set<string>; className?: string }) {
  const html = useMemo(() => render(asJSON(props.text) ?? props.text, props.artifacts), [props.text, props.artifacts])
  return (
    <div
      className={`md ${props.className ?? ''}`}
      onClick={(e) => {
        const el = e.target as HTMLElement
        const a = el.closest('a')
        if (a) {
          e.preventDefault()
          void window.nh.openExternal(a.href)
          return
        }
        const art = el.closest<HTMLElement>('[data-artifact]')
        if (art?.dataset.artifact) void store.openArtifact(art.dataset.artifact)
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
})
