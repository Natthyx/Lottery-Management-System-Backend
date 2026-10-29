package booking

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestEventGates_ServesWaitersInArrivalOrder(t *testing.T) {
	g := newEventGates()
	leave, err := g.enter(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := g.enter(context.Background(), 1)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			l()
		}(i)
		// Let goroutine i register and block on the gate before starting
		// i+1 (refs is bumped just before the blocking send).
		waitFor(t, func() bool { return g.refs(1) == i+2 })
		time.Sleep(5 * time.Millisecond)
	}
	leave()
	wg.Wait()

	for i, got := range order {
		if got != i {
			t.Fatalf("waiters not served FIFO: %v", order)
		}
	}
	if g.size() != 0 {
		t.Fatalf("idle gate not removed, %d left", g.size())
	}
}

func TestEventGates_TimesOutAndCleansUp(t *testing.T) {
	g := newEventGates()
	leave, _ := g.enter(context.Background(), 7)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.enter(ctx, 7); err == nil {
		t.Fatal("expected timeout while gate is held")
	}
	leave()
	if g.size() != 0 {
		t.Fatalf("gate leaked after timeout, %d left", g.size())
	}
}

func TestEventGates_DifferentEventsDoNotBlock(t *testing.T) {
	g := newEventGates()
	leave1, _ := g.enter(context.Background(), 1)
	defer leave1()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	leave2, err := g.enter(ctx, 2)
	if err != nil {
		t.Fatalf("event 2 blocked by event 1: %v", err)
	}
	leave2()
}

func (g *eventGates) refs(eventID int64) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if gt := g.m[eventID]; gt != nil {
		return gt.refs
	}
	return 0
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
