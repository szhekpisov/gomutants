package coverage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTestMapTestsFor(t *testing.T) {
	tm := &TestMap{
		index: map[string]map[string]bool{
			"file.go:10": {"TestA": true, "TestB": true},
			"file.go:20": {"TestC": true},
		},
	}

	tests := tm.TestsFor("file.go", 10)
	if len(tests) != 2 {
		t.Fatalf("TestsFor(file.go, 10) = %d tests, want 2", len(tests))
	}

	tests = tm.TestsFor("file.go", 20)
	if len(tests) != 1 || tests[0] != "TestC" {
		t.Errorf("TestsFor(file.go, 20) = %v, want [TestC]", tests)
	}

	// No mapping.
	tests = tm.TestsFor("file.go", 99)
	if tests != nil {
		t.Errorf("TestsFor(file.go, 99) = %v, want nil", tests)
	}

	// Nil TestMap.
	var nilTm *TestMap
	tests = nilTm.TestsFor("file.go", 10)
	if tests != nil {
		t.Errorf("nil TestMap.TestsFor = %v, want nil", tests)
	}
}

func TestRunPattern(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"TestA"}, "^(TestA)$"},
		{[]string{"TestA", "TestB"}, "^(TestA|TestB)$"},
		{[]string{"TestSpecial.Name"}, `^(TestSpecial\.Name)$`},
	}
	for _, tc := range tests {
		got := RunPattern(tc.input)
		if got != tc.want {
			t.Errorf("RunPattern(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestProcessWorkContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	work := make(chan testEntry, 2)
	results := make(chan testCoverage, 10)

	// Send work items, then cancel context.
	work <- testEntry{name: "TestA", pkg: "unknown"}
	work <- testEntry{name: "TestB", pkg: "unknown"}
	cancel()
	close(work)

	// No compiled packages — cp will be nil, exercising the nil check.
	processWork(ctx, work, map[string]*compiledPkg{}, t.TempDir(), 0, results)
	close(results)

	// Should complete without hanging.
	for range results {
	}
}

func TestProcessWorkNilPkg(t *testing.T) {
	ctx := context.Background()
	work := make(chan testEntry, 1)
	results := make(chan testCoverage, 1)

	// Package not in pkgBins — cp == nil path.
	work <- testEntry{name: "TestA", pkg: "missing"}
	close(work)

	processWork(ctx, work, map[string]*compiledPkg{}, t.TempDir(), 0, results)
	close(results)

	if len(results) != 0 {
		t.Error("expected no results for nil package")
	}
}

func TestFeedWorkContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	work := make(chan testEntry) // Unbuffered — will block on send.
	tests := []testEntry{{name: "TestA", pkg: "pkg"}}

	// Should not hang — context cancelled means it takes the ctx.Done() path.
	feedWork(ctx, tests, work)

	// Channel should be closed.
	_, ok := <-work
	if ok {
		t.Error("expected work channel to be closed")
	}
}

func TestFeedWorkNormal(t *testing.T) {
	ctx := context.Background()
	work := make(chan testEntry, 3)
	tests := []testEntry{
		{name: "TestA", pkg: "pkg"},
		{name: "TestB", pkg: "pkg"},
	}

	feedWork(ctx, tests, work)

	received := 0
	for range work {
		received++
	}
	if received != 2 {
		t.Errorf("expected 2 test entries, got %d", received)
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/foo/bar", "github_com_foo_bar"},
		{"simple", "simple"},
		{"path/to/pkg", "path_to_pkg"},
		{"with spaces", "with_spaces"},
		{"back\\slash", "back_slash"},
	}
	for _, tc := range tests {
		got := sanitize(tc.input)
		if got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestTestMapAddBlocksLineLoopBounded exercises the inner line-stepping
// loop directly with a tight deadline. Mutating `line++` (e.g. to `--`,
// `+= 0`, etc.) makes the loop unbounded and allocates map entries until
// the test process is RSS-killed within seconds — too fast for a deadline
// wrapped around the whole BuildTestMap pipeline. Hitting addBlocks
// directly with a tiny block lets the deadline fire first.
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
