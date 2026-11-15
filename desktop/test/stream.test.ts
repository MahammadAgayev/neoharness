import { describe, expect, it } from 'vitest'

import { blocks, openFence } from '../src/renderer/lib/markdown'

describe('blocks', () => {
  it('splits at blank lines', () => {
    expect(blocks('# Plan\n\nFirst part.\nSame part.\n\n\n- a\n- b')).toEqual(['# Plan', 'First part.\nSame part.', '- a\n- b'])
  })

  it('keeps fenced code whole, blank lines and all', () => {
    expect(blocks('Run:\n\n```sh\ngo vet\n\ngo test\n```\n\nDone.')).toEqual(['Run:', '```sh\ngo vet\n\ngo test\n```', 'Done.'])
  })

  it('keeps an indented line on the block above it', () => {
    expect(blocks('1. Step\n\n   More of the step.\n\n2. Next')).toEqual(['1. Step\n\n   More of the step.', '2. Next'])
  })

  it('leaves a code block that is still being written open at the end', () => {
    const bs = blocks('Here:\n\n```go\nfunc main() {\n\n')
    expect(bs).toEqual(['Here:', '```go\nfunc main() {\n\n'])
    expect(openFence(bs[1])).toBe(true)
    expect(openFence('```go\nx\n```')).toBe(false)
    expect(openFence('text')).toBe(false)
  })
})
