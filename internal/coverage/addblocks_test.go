package coverage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// File deliberately named so it sorts before testmap_integration_test.go
// in alphabetical filename order. Go's test runner registers tests in
// that order, so this fast deadline test runs first; with -failfast (which
// gomutant always passes), a runaway-loop mutation on addBlocks's inner
// line counter trips here in milliseconds — before the slower BuildTestMap
// integration tests can run them long enough to be RSS-killed and
// classified as TIMED OUT instead of KILLED.

// TestTestMapAddBlocksLineLoopBounded exercises the inner line-stepping
// loop directly with a tight deadline. Mutating `line++` (e.g. to `--`)
// makes the loop unbounded and allocates map entries until the test
// process is RSS-killed within seconds — too fast for a deadline wrapped
// around the whole BuildTestMap pipeline.
func TestTestMapAddBlocksLineLoopBounded(t *testing.T) {
	tm := &TestMap{index: make(map[string]map[string]bool)}
	blocks := []Block{
		{File: "a.go", StartLine: 1, EndLine: 3, Count: 1},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tm.addBlocks("TestX", blocks)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("addBlocks did not return within 100ms — line-counter mutation likely makes the loop unbounded")
	}

	// Block covers lines 1–3 inclusive. Endpoints + interior must all be
	// mapped; absence of any pins the boundary correctly.
	for _, line := range []int{1, 2, 3} {
		key := fmt.Sprintf("a.go:%d", line)
		if tm.index[key] == nil {
			t.Errorf("line %d not indexed (key %q)", line, key)
		}
	}
}

// TestFeedWorkClosesWorkOnCancel pins both close(work) calls in feedWork.
// Calling feedWork directly with a pre-cancelled context makes the close
// path non-flaky: regardless of which select branch the random scheduler
// picks (send into the buffer, or ctx.Done), the function MUST close work
// on return — the integration test that drives feedWork through
// BuildTestMap can't guarantee this because the buffer is sized to fit
// every test and Go's select is non-deterministic.
func TestFeedWorkClosesWorkOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []testEntry{{name: "T1", pkg: "p"}, {name: "T2", pkg: "p"}}
	// Unbuffered: send blocks immediately, so the select inside feedWork
	// has only ctx.Done() ready and picks it deterministically — no Go
	// select randomness to make the test flaky.
	work := make(chan testEntry)

	done := make(chan struct{})
	go func() {
		defer close(done)
		feedWork(ctx, tests, work)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("feedWork did not return within 500ms")
	}

	// After feedWork returns, work must be closed. If the close on
	// ctx.Done is mutated to a no-op, this receive blocks until the
	// deadline (channel open, no senders).
	select {
	case _, ok := <-work:
		if ok {
			t.Fatal("got an entry from a supposedly-cancelled feedWork")
		}
		// ok=false means closed — pass.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("feedWork returned without closing the work channel — close(work) on ctx.Done was likely mutated to a no-op")
	}
}

// TestFeedWorkClosesWorkAfterAllSent pins the post-loop close(work) at
// the end of feedWork. Same idea as the cancel-path test, but with a
// healthy context so the loop runs to completion.
func TestFeedWorkClosesWorkAfterAllSent(t *testing.T) {
	tests := []testEntry{{name: "T1", pkg: "p"}, {name: "T2", pkg: "p"}}
	work := make(chan testEntry, len(tests))

	done := make(chan struct{})
	go func() {
		defer close(done)
		feedWork(context.Background(), tests, work)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("feedWork did not return within 500ms")
	}

	got := 0
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-work:
			if !ok {
				if got != len(tests) {
					t.Errorf("got %d entries before close, want %d", got, len(tests))
				}
				return
			}
			got++
		case <-deadline:
			t.Fatal("feedWork returned without closing the work channel after all sends")
		}
	}
}
