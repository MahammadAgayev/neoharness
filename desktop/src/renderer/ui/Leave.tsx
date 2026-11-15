import { useEffect, useRef, type ReactNode } from 'react'

// leaveMs is how long a thing takes to leave: --t-leave, or 0 when the
// person asks for less motion.
function leaveMs(): number {
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return 0
  return parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--t-leave')) || 0
}

// Leave lets what it holds leave softly instead of vanishing in one frame.
// React unmounts at once; the node it takes out is put back where it was,
// marked .leaving to fade, and taken out for good when the fade ends. React
// owns that node no more, so nothing in it can act while it fades.
// Without a className it adds no box of its own (display: contents).
export function Leave(props: { children: ReactNode; className?: string }) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const mo = new MutationObserver((records) => {
      const ms = leaveMs()
      if (!ms) return
      for (const r of records) {
        r.removedNodes.forEach((n) => {
          if (!(n instanceof HTMLElement) || n.classList.contains('leaving')) return
          n.classList.add('leaving')
          const next = r.nextSibling?.parentNode === el ? r.nextSibling : null
          el.insertBefore(n, next)
          setTimeout(() => n.remove(), ms)
        })
      }
    })
    mo.observe(el, { childList: true })
    return () => mo.disconnect()
  }, [])
  return (
    <div ref={box} className={props.className ?? 'leave'}>
      {props.children}
    </div>
  )
}
