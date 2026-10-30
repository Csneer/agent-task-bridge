package blackbox

import (
	"context"
	"sync"
	"time"
)

// Clock is a controlled test-host clock; it is not a production RPC.
type Clock struct {
	mu    sync.RWMutex
	value time.Time
}

func NewClock(now time.Time) *Clock      { return &Clock{value: now} }
func (c *Clock) Now() time.Time          { c.mu.RLock(); defer c.mu.RUnlock(); return c.value }
func (c *Clock) Set(now time.Time)       { c.mu.Lock(); c.value = now; c.mu.Unlock() }
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.value = c.value.Add(d); c.mu.Unlock() }

// Gate pauses a single matching request outside the daemon write transaction.
// Await makes race ordering observable rather than relying on sleeps.
type Gate struct {
	Point, Method, Key string
	once               sync.Once
	releaseOnce        sync.Once
	arrived            chan struct{}
	release            chan struct{}
}

func NewGate(point, method, key string) *Gate {
	return &Gate{Point: point, Method: method, Key: key, arrived: make(chan struct{}), release: make(chan struct{})}
}
func (g *Gate) Hook(point, method, key string) {
	if point != g.Point || method != g.Method || (g.Key != "" && key != g.Key) {
		return
	}
	matched := false
	g.once.Do(func() { matched = true; close(g.arrived) })
	if matched {
		<-g.release
	}
}
func (g *Gate) Await(ctx context.Context) error {
	select {
	case <-g.arrived:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (g *Gate) Release() { g.releaseOnce.Do(func() { close(g.release) }) }
