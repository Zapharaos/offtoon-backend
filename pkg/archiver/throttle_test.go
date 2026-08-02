package archiver

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestThrottlePacesConcurrentAcquires(t *testing.T) {
	const (
		interval = 50 * time.Millisecond
		callers  = 5
	)
	th := newCDNThrottle(interval)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := th.acquire(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	// The last of N callers cannot start before (N-1)*interval.
	if got, want := time.Since(start), time.Duration(callers-1)*interval; got < want {
		t.Fatalf("acquires finished in %v, expected at least %v (not paced)", got, want)
	}
}

func TestThrottleCooldownBlocksEveryone(t *testing.T) {
	const cooldown = 200 * time.Millisecond
	th := newCDNThrottle(time.Millisecond)

	th.penalize(cooldown)

	start := time.Now()
	if err := th.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := time.Since(start); got < cooldown {
		t.Fatalf("acquire returned after %v, expected to block for %v", got, cooldown)
	}
}

func TestThrottleCooldownAppliesToWaitersAlreadyQueued(t *testing.T) {
	const cooldown = 500 * time.Millisecond
	th := newCDNThrottle(100 * time.Millisecond)

	// Consume the immediately-available slot so the goroutine below is forced to
	// reserve a future one and is still waiting when the 429 lands.
	if err := th.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A caller holding a reservation must still observe a cooldown that lands
	// while it waits — the case a naive implementation misses, letting queued
	// workers slip through and keep the CDN's rate limit engaged.
	done := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		_ = th.acquire(context.Background())
		done <- time.Since(start)
	}()

	time.Sleep(5 * time.Millisecond)
	th.penalize(cooldown)

	select {
	case elapsed := <-done:
		if elapsed < cooldown {
			t.Fatalf("queued waiter slipped through after %v, expected >= %v", elapsed, cooldown)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire never returned")
	}
}

func TestThrottleAcquireRespectsContextCancellation(t *testing.T) {
	th := newCDNThrottle(time.Millisecond)
	th.penalize(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if err := th.acquire(ctx); err == nil {
		t.Fatal("expected acquire to fail on cancelled context")
	}
	if got := time.Since(start); got > time.Second {
		t.Fatalf("acquire took %v to honour cancellation", got)
	}
}

func TestThrottlePenalizeKeepsLongestCooldown(t *testing.T) {
	th := newCDNThrottle(time.Millisecond)

	th.penalize(500 * time.Millisecond)
	th.penalize(50 * time.Millisecond) // must not shorten the existing cooldown

	start := time.Now()
	if err := th.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := time.Since(start); got < 400*time.Millisecond {
		t.Fatalf("shorter penalize truncated the cooldown: waited only %v", got)
	}
}
