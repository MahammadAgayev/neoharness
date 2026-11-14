import { RecapAwayMs } from '../../shared/model'

// Away tells when the person comes back after RecapAwayMs, for the recap:
// from the window's blur and focus, or from the first key or click after a
// long pause, when the window kept its focus.
export class Away {
  private lastInput = 0
  private blurAt = 0

  blur(now: number): void {
    this.blurAt = now
  }

  // focus and input say whether the person just came back.
  focus(now: number): boolean {
    const back = this.blurAt > 0 && now - this.blurAt >= RecapAwayMs
    this.blurAt = 0
    this.lastInput = now
    return back
  }

  input(now: number): boolean {
    const back = this.lastInput > 0 && now - this.lastInput >= RecapAwayMs
    this.lastInput = now
    return back
  }
}
