// Package clock provides a Clock interface and a RealClock implementation
// for abstracting time operations, plus a FakeClock for deterministic
// testing without real wall-clock waits.
package clock

import (
	"sync"
	"time"
)

// Ticker is the interface satisfied by both real and fake tickers.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Clock is the interface for time operations, allowing injection of a
// fake clock in tests.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
	After(d time.Duration) <-chan time.Time
}

// RealClock uses real system time. The zero value is ready to use.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) NewTicker(d time.Duration) Ticker       { return &realTicker{ticker: time.NewTicker(d)} }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type realTicker struct {
	ticker *time.Ticker
}

func (t *realTicker) C() <-chan time.Time { return t.ticker.C }
func (t *realTicker) Stop()               { t.ticker.Stop() }

// FakeClock is a manually-controlled clock for deterministic tests.
type FakeClock struct {
	now     time.Time
	tickers []*fakeTicker
	after   []*fakeAfter
	mu      sync.Mutex
}

// NewFakeClock returns a FakeClock set to the given time.
func NewFakeClock(now time.Time) *FakeClock {
	return &FakeClock{now: now}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and fires any tickers / after
// channels whose deadlines have elapsed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)

	// Fire tickers whose period has elapsed.
	for _, t := range c.tickers {
		if t.stopped {
			continue // skip stopped tickers
		}
		if !c.now.Before(t.next) {
			select {
			case t.ch <- c.now:
			default:
			}
			t.next = t.next.Add(t.period)
		}
	}

	// Fire after channels whose deadline has passed.
	for _, a := range c.after {
		if !a.fired && !c.now.Before(a.deadline) {
			a.fired = true
			a.ch <- c.now
		}
	}
}

// NewTicker creates a ticker that will fire when Advance crosses its period.
func (c *FakeClock) NewTicker(d time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTicker{
		ch:     make(chan time.Time, 1),
		period: d,
		next:   c.now.Add(d),
	}
	c.tickers = append(c.tickers, ft)
	return ft
}

// After creates a channel that receives the current time after d has
// elapsed on the fake clock.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	fa := &fakeAfter{
		ch:       make(chan time.Time, 1),
		deadline: c.now.Add(d),
	}
	c.after = append(c.after, fa)
	return fa.ch
}

type fakeTicker struct {
	ch      chan time.Time
	period  time.Duration
	next    time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time {
	if t.stopped {
		return nil // nil channel blocks forever in select (Go convention)
	}
	return t.ch
}

func (t *fakeTicker) Stop() { t.stopped = true }

type fakeAfter struct {
	ch       chan time.Time
	deadline time.Time
	fired    bool
}
