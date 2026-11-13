import { autocompletion, completionKeymap, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { markdown } from '@codemirror/lang-markdown'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { EditorView, keymap, placeholder, tooltips } from '@codemirror/view'
import { getCM, vim, Vim } from '@replit/codemirror-vim'
import { useEffect, useRef } from 'react'

import { store, useStore } from '../store'
import { editorKeys, editors, typeahead } from './registry'

// drafts keep what you wrote per thread while the window shows another.
const drafts = new Map<string, string>()
// stashes are Ctrl+S's: the draft put aside until the next send, as in
// Claude Code.
const stashes = new Map<string, string>()

export interface PromptApi {
  focus(insert: boolean): void
  setText(text: string): void
  text(): string
  view: EditorView
}

export const prompts = new Map<number, PromptApi>()

function source(thread: string) {
  return async (ctx: CompletionContext): Promise<CompletionResult | null> => {
    const line = ctx.state.doc.lineAt(ctx.pos)
    const before = line.text.slice(0, ctx.pos - line.from)
    const slash = /^\/([\w:-]*)$/.exec(before)
    const at = /(?:^|\s)@([\w./~-]*)$/.exec(before)
    if (!slash && !at) return null
    const word = (slash ?? at)![1]
    const c = await store.completions(thread, at ? word : '').catch(() => null)
    if (!c) return null
    if (slash) {
      return {
        from: line.from + 1,
        options: c.commands.map((cmd) => ({ label: cmd.replace(/^\//, ''), type: 'keyword', detail: 'command' })),
        validFor: /^[\w:-]*$/,
      }
    }
    return {
      from: ctx.pos - word.length,
      options: [
        ...c.threads.map((t) => ({ label: t, type: 'variable', detail: 'thread', boost: 1 })),
        ...c.files.map((f) => ({ label: f, type: 'text', detail: 'file' })),
      ],
      // A folder with too many files sends only matches: ask again.
      validFor: c.many ? undefined : /^[\w./~-]*$/,
    }
  }
}

const theme = EditorView.theme({
  '&': { background: 'transparent', color: 'var(--text)', fontSize: 'inherit' },
  '&.cm-focused': { outline: 'none' },
  '.cm-content': { fontFamily: 'var(--font-text)', caretColor: 'var(--iris)', padding: '0' },
  '.cm-line': { padding: '0' },
  '.cm-cursor': { borderLeftColor: 'var(--iris)', borderLeftWidth: '2px' },
  '.cm-fat-cursor': { background: 'var(--iris) !important', color: 'var(--base) !important' },
  // Only the editor with focus shows a cursor: one place to type.
  '&:not(.cm-focused) .cm-cursorLayer': { display: 'none' },
  '.cm-placeholder': { color: 'var(--muted)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': { background: 'var(--hl-med) !important' },
  '.cm-tooltip': { background: 'var(--surface)', border: '1px solid var(--overlay)', borderRadius: '8px', overflow: 'hidden' },
  '.cm-tooltip-autocomplete ul li[aria-selected]': { background: 'var(--hl-med)', color: 'var(--text)' },
  '.cm-tooltip-autocomplete ul': { fontFamily: 'var(--font-text) !important', padding: '4px' },
  '.cm-tooltip-autocomplete ul li': { padding: '3px 10px !important', borderRadius: '6px' },
  '.cm-completionDetail': { color: 'var(--muted)', fontStyle: 'normal', marginLeft: '1em' },
})

export function Prompt(props: {
  win: number
  thread: string
  resolved: boolean
  onLeave: () => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const vimOn = useStore((s) => s.settings.vim)
  const vimComp = useRef(new Compartment())
  const { win, thread, onLeave } = props
  const leave = useRef(onLeave)
  leave.current = onLeave

  useEffect(() => {
    const send = (view: EditorView) => {
      const text = view.state.doc.toString()
      if (!text.trim()) return true
      void store.send(thread, text).then((ok) => {
        if (!ok) return
        const back = stashes.get(thread) ?? ''
        stashes.delete(thread)
        view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: back } })
        drafts.set(thread, back)
        if (back) store.say('your stashed draft is back', 'muted')
      })
      return true
    }
    const stash = (view: EditorView) => {
      const text = view.state.doc.toString()
      if (text.trim()) {
        stashes.set(thread, text)
        view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: '' } })
        store.say('draft stashed · it comes back after you send · ⌃S again takes it back', 'muted')
      } else if (stashes.has(thread)) {
        view.dispatch({ changes: { from: 0, to: 0, insert: stashes.get(thread)! } })
        stashes.delete(thread)
      }
      return true
    }
    const takeBack = (view: EditorView) => {
      void store.takeBack(thread).then((text) => {
        if (text === null) {
          leave.current()
          return
        }
        if (text) {
          view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text }, selection: { anchor: text.length } })
        }
      })
      return true
    }
    const newline = (view: EditorView) => {
      view.dispatch(view.state.replaceSelection('\n'))
      return true
    }
    const view = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: drafts.get(thread) ?? '',
        extensions: [
          vimComp.current.of(store.state.settings.vim ? vim({ status: false }) : []),
          Prec.highest(
            keymap.of([
              { key: 'Enter', run: send },
              { key: 'Mod-Enter', run: send },
              { key: 'Shift-Enter', run: newline },
              { key: 'Ctrl-j', run: (v) => (store.state.settings.vim && !getCM(v)?.state.vim?.insertMode ? false : newline(v)) },
              { key: 'Alt-Enter', run: newline },
              // ⌃X ⌃S stashes as ⌃S does: ⌃X alone does nothing here.
              { key: 'Ctrl-x', run: () => true },
              { key: 'Ctrl-s', run: stash },
              {
                key: 'Escape',
                run: (v) => {
                  // Without Vim, Esc is Claude Code's: it stops the turn and
                  // takes your last message back; with nothing to take, it
                  // goes to the transcript.
                  if (store.state.settings.vim) return false
                  return takeBack(v)
                },
              },
            ]),
          ),
          history(),
          keymap.of([...completionKeymap, ...historyKeymap, ...defaultKeymap]),
          autocompletion({ override: [source(thread)], icons: false, activateOnTyping: true }),
          // On the page, so the list can open above a prompt at the bottom.
          tooltips({ parent: document.body }),
          markdown(),
          EditorView.lineWrapping,
          placeholder(props.resolved ? 'resolved · write to bring it back' : `write to ${thread} · / commands · @ threads and files`),
          theme,
          EditorView.updateListener.of((u) => {
            if (u.docChanged) drafts.set(thread, u.state.doc.toString())
            if (u.focusChanged) store.setInsert(u.view.hasFocus && (!store.state.settings.vim || !!getCM(u.view)?.state.vim?.insertMode))
          }),
          EditorView.domEventHandlers({
            focus: () => store.focus(win),
          }),
        ],
      }),
    })
    viewRef.current = view
    editors.add(view)
    const cm = getCM(view)
    const onMode = (e: { mode: string }) => store.setInsert(e.mode === 'insert')
    cm?.on('vim-mode-change', onMode)
    editorKeys.set(view, (key) => {
      const doc = view.state.doc
      const head = view.state.selection.main.head
      switch (key) {
        case '<CR>':
          return send(view)
        case '<Esc>':
          leave.current()
          return true
        case 'k':
        case '<Up>':
          if (doc.lineAt(head).number === 1) {
            leave.current()
            return true
          }
          return false
        case 'u':
          if (doc.length === 0) return takeBack(view)
          return false
      }
      return false
    })
    const api: PromptApi = {
      view,
      focus(insert) {
        view.focus()
        const cm = getCM(view)
        if (insert && cm && store.state.settings.vim && !cm.state.vim?.insertMode) {
          view.dispatch({ selection: { anchor: view.state.doc.length } })
          Vim.handleKey(cm as never, 'a', 'mapping')
        }
        if (insert && typeahead.waiting) {
          const { text: t, send: go } = typeahead
          typeahead.waiting = false
          typeahead.text = ''
          typeahead.send = false
          if (t) view.dispatch(view.state.replaceSelection(t))
          if (go) send(view)
        }
      },
      setText(text) {
        view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text }, selection: { anchor: text.length } })
      },
      text: () => view.state.doc.toString(),
    }
    prompts.set(win, api)
    return () => {
      cm?.off('vim-mode-change', onMode)
      editors.delete(view)
      prompts.delete(win)
      view.destroy()
    }
    // A new thread in the window is a new editor, with that thread's draft.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win, thread])

  useEffect(() => {
    viewRef.current?.dispatch({ effects: vimComp.current.reconfigure(vimOn ? vim({ status: false }) : []) })
  }, [vimOn])

  return <div className="prompt-editor" ref={host} />
}
