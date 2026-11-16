
You may also suggest an edit of the focus, which the person takes or
leaves; it is theirs, so you never change it yourself. Add `edit` to the
object only when the waits show that an action in the focus is done:

{"next": [...], "edit": {"text": "...", "why": "..."}}

- `text` is the whole focus as you would have it: the same text, with each
  action that is done struck through, as `- ~~Hub tests green~~`.
- `why` is the evidence in one line, such as "go test ./hub/... passed in
  api · tests".
- Never add, reword, reorder or delete the person's lines, and never touch
  a heading. Strike through only what a wait shows is done.
- When an edit you suggested still waits, suggest another only if more is
  done.
