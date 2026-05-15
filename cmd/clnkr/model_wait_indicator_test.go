package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clnkr-ai/clnkr"
)

func TestModelWaitIndicatorDisabledWritesNothing(t *testing.T) {
	var out bytes.Buffer
	_ = testModelWaitIndicator(false, &out, time.Millisecond, time.Millisecond, func() time.Time { return time.Unix(0, 0) })

	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
}

func TestModelWaitIndicatorShortWaitBeforeDelayWritesNothing(t *testing.T) {
	var out bytes.Buffer
	indicator := testModelWaitIndicator(true, &out, time.Hour, time.Millisecond, func() time.Time { return time.Unix(0, 0) })

	indicator.Start()
	indicator.Stop()

	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
}

func TestModelWaitIndicatorRendersAfterDelayAndClearsOnStop(t *testing.T) {
	out := &guardedBuffer{}
	now := time.Unix(10, 0)
	indicator := testModelWaitIndicator(true, out, time.Millisecond, time.Hour, func() time.Time { return now })

	indicator.Start()
	waitForOutput(t, out, "waiting for model")
	indicator.Stop()

	got := out.String()
	if !strings.Contains(got, "\r[clnkr] ╵[-_-]╵ waiting for model... 0s") {
		t.Fatalf("output = %q, want first render", got)
	}
	if !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("output = %q, want terminal clear suffix", got)
	}
}

func TestModelWaitIndicatorRendersFixedDotsAndElapsedSeconds(t *testing.T) {
	start := time.Unix(20, 0)
	now := &testClock{now: start}
	out := &guardedBuffer{}
	indicator := testModelWaitIndicator(true, out, time.Millisecond, time.Millisecond, now.Now)

	indicator.Start()
	waitForOutput(t, out, "0s")
	now.Set(start.Add(2 * time.Second))
	waitForOutput(t, out, "2s")
	indicator.Stop()

	got := out.String()
	if !strings.Contains(got, "╵[-_-]╵ waiting for model... 0s") {
		t.Fatalf("output = %q, want first approved frame", got)
	}
	if !strings.Contains(got, "╶[o_o]╴ waiting for model    2s") && !strings.Contains(got, "╷[O_O]╷ waiting for model.   2s") && !strings.Contains(got, "╴[o_o]╶ waiting for model..  2s") {
		t.Fatalf("output = %q, want later approved frame with elapsed seconds", got)
	}
}

func TestModelWaitIndicatorRepeatedStartStopIsRaceSafe(t *testing.T) {
	var out bytes.Buffer
	indicator := testModelWaitIndicator(true, &out, time.Millisecond, time.Millisecond, func() time.Time { return time.Now() })

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			indicator.Start()
			indicator.Start()
			indicator.Stop()
			indicator.Stop()
		}()
	}
	wg.Wait()
	indicator.Stop()
}

func TestModelWaitIndicatorStopPreventsFutureWrites(t *testing.T) {
	out := &guardedBuffer{}
	indicator := testModelWaitIndicator(true, out, time.Millisecond, time.Millisecond, func() time.Time { return time.Now() })

	indicator.Start()
	waitForGuardedOutput(t, out)
	indicator.Stop()
	before := out.String()
	time.Sleep(5 * time.Millisecond)
	after := out.String()

	if after != before {
		t.Fatalf("output changed after Stop returned:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestModelWaitIndicatorStartDuringStopDoesNotCreateSecondRenderer(t *testing.T) {
	out := newBlockingClearBuffer()
	indicator := testModelWaitIndicator(true, out, time.Millisecond, time.Hour, func() time.Time { return time.Now() })

	indicator.Start()
	waitForOutput(t, out, "waiting for model")
	out.BlockClear()
	stopped := make(chan struct{})
	go func() {
		indicator.Stop()
		close(stopped)
	}()
	out.WaitForClear(t)
	indicator.Start()
	out.UnblockClear()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Stop")
	}
	before := out.String()
	time.Sleep(5 * time.Millisecond)
	if after := out.String(); after != before {
		t.Fatalf("output changed after Stop returned:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestUpdateModelWaitForAgentEventStartsAndStops(t *testing.T) {
	out := &guardedBuffer{}
	indicator := testModelWaitIndicator(true, out, time.Millisecond, time.Hour, func() time.Time { return time.Unix(0, 0) })

	updateModelWaitForAgentEvent(indicator, clnkr.EventDebug{Message: modelWaitQueryDebugMessage})
	waitForOutput(t, out, "waiting for model")
	updateModelWaitForAgentEvent(indicator, clnkr.EventResponse{Turn: &clnkr.DoneTurn{}})

	if !strings.HasSuffix(out.String(), "\r\x1b[2K") {
		t.Fatalf("output = %q, want clear after response event", out.String())
	}
}

type guardedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func testModelWaitIndicator(enabled bool, out interface{ Write([]byte) (int, error) }, delay, tick time.Duration, now func() time.Time) *modelWaitIndicator {
	if !enabled {
		return nil
	}
	return &modelWaitIndicator{out: out, delay: delay, tick: tick, now: now}
}

func (b *guardedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *guardedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *guardedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

type blockingClearBuffer struct {
	guardedBuffer
	blockClear chan struct{}
	clearSeen  chan struct{}
	once       sync.Once
}

func newBlockingClearBuffer() *blockingClearBuffer {
	return &blockingClearBuffer{}
}

func (b *blockingClearBuffer) BlockClear() {
	b.blockClear = make(chan struct{})
	b.clearSeen = make(chan struct{})
}

func (b *blockingClearBuffer) Write(p []byte) (int, error) {
	if string(p) == "\r\x1b[2K" && b.blockClear != nil {
		b.once.Do(func() { close(b.clearSeen) })
		<-b.blockClear
	}
	return b.guardedBuffer.Write(p)
}

func (b *blockingClearBuffer) WaitForClear(t *testing.T) {
	t.Helper()
	select {
	case <-b.clearSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clear")
	}
}

func (b *blockingClearBuffer) UnblockClear() {
	close(b.blockClear)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type stringer interface {
	String() string
}

func waitForOutput(t *testing.T, out stringer, want string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("output = %q, want substring %q", out.String(), want)
}

func waitForGuardedOutput(t *testing.T, out *guardedBuffer) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if out.String() != "" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for indicator output")
}
