package hub

import (
	"errors"
	"sync"
	"time"
)

var errTurnTimeout = errors.New("the turn ran too long")

// turnClock calls stop once a turn has run for a time, not counting the time
// it is held: a turn that waits for the person is not wedged.
type turnClock struct {
	mu      sync.Mutex
	left    time.Duration
	started time.Time
	timer   *time.Timer
	fire    func()
}

func newTurnClock(d time.Duration, fire func()) *turnClock {
	c := &turnClock{left: d, fire: fire}
	c.run()
	return c
}

func (c *turnClock) run() {
	c.started = time.Now()
	c.timer = time.AfterFunc(c.left, c.fire)
}

// hold stops the clock while on is true, and starts it again after.
func (c *turnClock) hold(on bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case on && c.timer != nil:
		if c.timer.Stop() {
			c.left -= time.Since(c.started)
		} else {
			c.left = 0 // it fired
		}
		c.timer = nil
	case !on && c.timer == nil && c.left > 0:
		c.run()
	}
}

func (c *turnClock) stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.left = 0
}
