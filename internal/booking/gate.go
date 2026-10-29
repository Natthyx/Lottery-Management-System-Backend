package booking

import (
	"context"
	"sync"
)

// eventGates hands out one in-process FIFO gate per event. A gate is a
// channel with capacity 1: entering sends, leaving receives. Go queues
// blocked senders in arrival order and hands the slot to the oldest one,
// so waiters are served first-come first-served and none can be starved
// by later arrivals. Gates are reference counted and removed when idle.
type eventGates struct {
	mu sync.Mutex
	m  map[int64]*gate
}

type gate struct {
	slot chan struct{}
	refs int
}

func newEventGates() *eventGates {
	return &eventGates{m: make(map[int64]*gate)}
}

// enter blocks until the caller owns the event's gate or ctx is done. On
// success it returns the function that leaves the gate.
func (g *eventGates) enter(ctx context.Context, eventID int64) (leave func(), err error) {
	g.mu.Lock()
	gt := g.m[eventID]
	if gt == nil {
		gt = &gate{slot: make(chan struct{}, 1)}
		g.m[eventID] = gt
	}
	gt.refs++
	g.mu.Unlock()

	select {
	case gt.slot <- struct{}{}:
		return func() {
			<-gt.slot
			g.release(eventID, gt)
		}, nil
	case <-ctx.Done():
		g.release(eventID, gt)
		return nil, ctx.Err()
	}
}

func (g *eventGates) release(eventID int64, gt *gate) {
	g.mu.Lock()
	gt.refs--
	if gt.refs == 0 {
		delete(g.m, eventID)
	}
	g.mu.Unlock()
}

func (g *eventGates) size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.m)
}
