// Windows register a handler for the keys that act on what they show: j k in
// a chat move between its blocks, in an artifact they scroll. The keymap
// sends such keys to the window in focus.

import type { EditorView } from '@codemirror/view'

export type WinAction =
  | 'down'
  | 'up'
  | 'top'
  | 'bottom'
  | 'halfDown'
  | 'halfUp'
  | 'pageDown'
  | 'pageUp'
  | 'nextMsg'
  | 'prevMsg'
  | 'enter'
  | 'fold'
  | 'todo'
  | 'yank'
  | 'insert'
  | 'append'
  | 'stopHere'
  | 'takeBack'
  | 'put'
  | 'link'
  | 'splitRight'
  | 'splitAbove'
  | 'delete'
  | 'search'
  | 'searchNext'
  | 'searchPrev'
  | 'visual'
  | 'escape'
  | 'nextHunk'
  | 'prevHunk'
  | 'takeHunk'

export type WinHandler = (a: WinAction, count: number) => boolean

export const handlers = new Map<number, WinHandler>()

// editors are the CodeMirror views on screen, so the keymap can tell when
// one takes the keys and in which Vim mode it is.
export const editors = new Set<EditorView>()

// DocKeys is a rich-text document on screen (an artifact) with its own Vim
// layer: the keymap sends it keys as it does to a CodeMirror editor.
export interface DocKeys {
  hasFocus(): boolean
  insert(): boolean
  busy(): boolean
  key(key: string): boolean
  leaveInsert(): void
  deleteWord(): void
}

export const docs = new Set<DocKeys>()

// lastYank is what y yanked last; p puts it in the prompt.
export const yank = { text: '' }

// editorKeys are an editor's own keys in Normal mode, before Vim's: the
// prompt leaves on <Esc> and on k at its top, and u with nothing written
// takes your last message back. insert says Vim is in Insert mode.
export const editorKeys = new WeakMap<EditorView, (key: string, insert: boolean) => boolean>()

// typeahead holds what is typed while a prompt is on its way (after :open
// or :new), so no key is lost before the editor takes focus.
export const typeahead = { waiting: false, text: '', send: false }
