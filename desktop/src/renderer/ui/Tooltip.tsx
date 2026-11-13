import { useEffect, useState } from 'react'

// Tooltip shows the data-tip of what the mouse rests on, and its keys from
// data-keys, sooner than the native title does.
export function Tooltip() {
  const [tip, setTip] = useState<{ text: string; keys: string; x: number; y: number } | null>(null)
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null
    const hide = () => {
      if (timer) clearTimeout(timer)
      setTip(null)
    }
    const over = (e: MouseEvent) => {
      const el = (e.target as Element).closest<HTMLElement>('[data-tip]')
      hide()
      if (!el) return
      timer = setTimeout(() => {
        const r = el.getBoundingClientRect()
        setTip({ text: el.dataset.tip ?? '', keys: el.dataset.keys ?? '', x: r.left + r.width / 2, y: r.bottom + 6 })
      }, 450)
    }
    document.addEventListener('mouseover', over)
    document.addEventListener('mousedown', hide)
    document.addEventListener('keydown', hide)
    return () => {
      hide()
      document.removeEventListener('mouseover', over)
      document.removeEventListener('mousedown', hide)
      document.removeEventListener('keydown', hide)
    }
  }, [])
  if (!tip) return null
  return (
    <div className="tooltip" style={{ left: Math.min(Math.max(tip.x, 90), window.innerWidth - 90), top: tip.y }}>
      {tip.text}
      {tip.keys && <kbd>{tip.keys}</kbd>}
    </div>
  )
}
