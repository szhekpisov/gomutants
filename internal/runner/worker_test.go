package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/mutator"
)

// TestPackageVarDefaults pins the default values of the worker's
// package-level vars and the maxCapturedOutput const. These literals
// live above any function body and aren't reachable by tests that
// override them — without an explicit pin, ARITHMETIC_BASE and
// INVERT_BITWISE mutants on the literals (e.g. `2 * 1024 * 1024 * 1024`,
// `1 << 20`) are unkillable.
func TestPackageVarDefaults(t *testing.T) {
	if got, want := maxSubprocRSSBytes, int64(2*1024*1024*1024); got != want {
		t.Errorf("maxSubprocRSSBytes = %d, want %d (2 GiB)", got, want)
	}
	if got, want := monitorPollInterval, 1*time.Second; got != want {
		t.Errorf("monitorPollInterval = %v, want %v", got, want)
	}
	if got, want := maxCapturedOutput, 1<<20; got != want {
		t.Errorf("maxCapturedOutput = %d, want %d (1 MiB)", got, want)
	}
}

func TestNewWorker(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{"/src/file.go": []byte("package p\n")}

	w, err := NewWorker(0, dir, 30*time.Second, cache, "/src", nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	if w.id != 0 {
		t.Errorf("id=%d, want 0", w.id)
	}

	// Verify temp files were created.
	if _, err := os.Stat(w.tmpSrcPath); err != nil {
		t.Errorf("tmpSrcPath not created: %v", err)
	}
	if _, err := os.Stat(w.overlayPath); err != nil {
		t.Errorf("overlayPath not created: %v", err)
	}
}

func TestWorkerTestMissingSource(t *testing.T) {
	dir := t.TempDir()
	cache := map[string][]byte{} // Empty cache.

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:   1,
		File: "/nonexistent/file.go",
		Status: mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for missing source", result.Status)
	}
	// Duration must be set even on early return paths.
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestInvalidPatch(t *testing.T) {
	dir := t.TempDir()
	src := []byte("package p\n")
	cache := map[string][]byte{"/src/file.go": src}

	w, err := NewWorker(0, dir, 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID:          1,
		File:        "/src/file.go",
		StartOffset: 100, // Beyond file length.
		EndOffset:   200,
		Replacement: "x",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for invalid patch", result.Status)
	}
	if result.Duration <= 0 {
		t.Errorf("Duration should be > 0 on early-return path, got %v", result.Duration)
	}
}

func TestWorkerTestNotViable(t *testing.T) {
	// Create a small Go project that will fail to compile with the mutation.
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Replace entire file with code that has an undefined symbol (compile error).
	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: 0,
		EndOffset:   len(src),
		Replacement: "package testpkg\n\nfunc Add(a, b int) int {\n\treturn UNDEFINED_SYMBOL\n}\n",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NOT_VIABLE for compile error", result.Status)
	}
}

func TestWorkerTestKilled(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("wrong")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	// Mutate + to - (test should fail → KILLED).
	plusIdx := 51 // "a + b" — the "+" position
	for i, c := range src {
		if c == '+' && i > 30 { // Skip package line
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusKilled {
		t.Errorf("Status=%v, want KILLED", result.Status)
	}
	if result.Duration == 0 {
		t.Error("Duration should be > 0")
	}
}

func TestWorkerTestLived(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	// This function's test doesn't check the operator, so the mutant survives.
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	testSrc := `package testpkg

import "testing"

func TestAdd(t *testing.T) {
	// Weak test: doesn't verify the result.
	_ = Add(1, 2)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusLived {
		t.Errorf("Status=%v, want LIVED", result.Status)
	}
}

func TestWorkerTestTimeout(t *testing.T) {
	dir := t.TempDir()
	goMod := `module testmod

go 1.26
`
	src := `package testpkg

func Add(a, b int) int {
	return a + b
}
`
	// Test that will run forever.
	testSrc := `package testpkg

import "testing"
import "time"

func TestAdd(t *testing.T) {
	time.Sleep(10 * time.Minute)
}
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "add_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}

	// Very short timeout.
	w, err := NewWorker(0, t.TempDir(), 3*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	m := mutator.Mutant{
		ID:          1,
		File:        filepath.Join(dir, "add.go"),
		Pkg:         "testmod",
		StartOffset: plusIdx,
		EndOffset:   plusIdx + 1,
		Replacement: "-",
		Status:      mutator.StatusPending,
	}

	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusTimedOut {
		t.Errorf("Status=%v, want TIMED_OUT", result.Status)
	}
}

// TestPgroupRSSBytesSelf exercises pgroupRSSBytes against our own process
// group. Kills:
//   - CONDITIONALS_NEGATION on `err != nil` (line 32): mutated `err == nil`
//     would short-circuit to return 0 on the success path.
//   - STATEMENT_REMOVE on `line = strings.TrimSpace(line)` (line 37):
//     without trimming, `ps` output lines (" 12345") fail strconv.ParseInt.
//   - CONDITIONALS_NEGATION on `line == ""` (line 38): mutated `!=` skips
//     every non-empty line, never reaching ParseInt.
//   - CONDITIONALS_NEGATION on `err != nil` (line 42): mutated `==` skips
//     successful parses, total stays 0.
//   - ARITHMETIC_BASE on `n * 1024` (line 45): mutated `n / 1024` produces
//     byte totals roughly 1e6× too small.
//
// We assert the returned value exceeds 1 MiB — real Go test processes use
// tens of MiB, but all the mutations above collapse the result toward 0.
func TestPgroupRSSBytesSelf(t *testing.T) {
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	bytes := pgroupRSSBytes(pgid)
	if bytes < 1<<20 {
		t.Errorf("pgroupRSSBytes(self pgid=%d) = %d bytes, want >= 1 MiB (a running Go test process is typically tens of MiB)", pgid, bytes)
	}
	// Sanity upper bound — catches mutations that inflate the total.
	if bytes > 100*(1<<30) {
		t.Errorf("pgroupRSSBytes(self) = %d bytes, implausibly large", bytes)
	}
}

// TestPgroupRSSBytesInvalidPgid kills the err-path return: passing an
// invalid PGID makes `ps -g` emit either an error or empty output. The
// original returns 0; a mutation that keeps the loop running still
// returns 0 on empty output (no lines to parse), so this specifically
// verifies the call doesn't panic or return garbage.
func TestPgroupRSSBytesInvalidPgid(t *testing.T) {
	// Very large pgid unlikely to exist.
	bytes := pgroupRSSBytes(99999999)
	if bytes < 0 {
		t.Errorf("pgroupRSSBytes(invalid) = %d, want >= 0", bytes)
	}
}

// TestClampPositive directly exercises the d <= 0 boundary that drives
// nonZeroSince. Driving nonZeroSince itself is racy because time.Since on
// a just-captured time.Now() returns a small positive duration on real
// clocks, hiding `<` ↔ `<=` mutations.
func TestClampPositive(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero", 0, time.Nanosecond},
		{"negative", -1 * time.Second, time.Nanosecond},
		{"tiny positive", time.Nanosecond, time.Nanosecond},
		{"normal", 5 * time.Millisecond, 5 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampPositive(c.in); got != c.want {
				t.Errorf("clampPositive(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestPgroupRSSBytesParsing covers the parsing path with a stubbed
// psOutputFunc. Real ps output may or may not have leading whitespace
// depending on column width; injecting a known string lets us pin the
// behavior. Kills:
//   - BRANCH_IF on the err-return (psOutputFunc returns err with non-empty
//     output; original returns 0, the elided body parses the output).
//   - STATEMENT_REMOVE on the inner TrimSpace (now removed since the trim
//     is inlined into ParseInt's argument; mutating the inlined call breaks
//     parses on whitespace-prefixed lines).
//   - REMOVE_SELF_ASSIGNMENTS on `total += n * 1024` (sum vs last-line).
func TestPgroupRSSBytesParsing(t *testing.T) {
	orig := psOutputFunc
	defer func() { psOutputFunc = orig }()

	t.Run("err returns 0 even with output", func(t *testing.T) {
		psOutputFunc = func(int) ([]byte, error) {
			return []byte("12345\n"), errors.New("inject")
		}
		if got := pgroupRSSBytes(0); got != 0 {
			t.Errorf("got %d, want 0 — BRANCH_IF on err-return body lets the parse through", got)
		}
	})

	t.Run("sums all lines", func(t *testing.T) {
		psOutputFunc = func(int) ([]byte, error) {
			return []byte("  100\n  200\n  50\n"), nil
		}
		got := pgroupRSSBytes(0)
		want := int64((100 + 200 + 50) * 1024)
		if got != want {
			t.Errorf("got %d, want %d — REMOVE_SELF_ASSIGNMENTS on `total += n*1024` (or untrimmed-line parse failure) collapses the sum", got, want)
		}
	})

	t.Run("trims leading whitespace", func(t *testing.T) {
		psOutputFunc = func(int) ([]byte, error) {
			return []byte("    7\n"), nil
		}
		got := pgroupRSSBytes(0)
		want := int64(7 * 1024)
		if got != want {
			t.Errorf("got %d, want %d — without TrimSpace, ParseInt rejects whitespace-prefixed lines", got, want)
		}
	})

	t.Run("skips malformed lines", func(t *testing.T) {
		psOutputFunc = func(int) ([]byte, error) {
			return []byte("100\nnotanumber\n200\n"), nil
		}
		got := pgroupRSSBytes(0)
		want := int64((100 + 200) * 1024)
		if got != want {
			t.Errorf("got %d, want %d — malformed line should be skipped, sum should still total valid lines", got, want)
		}
	})
}

// TestNewWorkerWriteFailures kills BRANCH_IF on both write-error returns
// in NewWorker (lines 119 / 122). Stub writeFileFunc to fail at the
// requested call index; the original returns the error, the elided body
// falls through to a successful-looking *Worker.
func TestNewWorkerWriteFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		failCall int32
	}{
		{"first write fails (tmpSrc)", 1},
		{"second write fails (overlay)", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			orig := writeFileFunc
			defer func() { writeFileFunc = orig }()
			var calls atomic.Int32
			writeFileFunc = func(name string, data []byte, perm os.FileMode) error {
				if calls.Add(1) == tt.failCall {
					return errors.New("inject")
				}
				return os.WriteFile(name, data, perm)
			}
			w, err := NewWorker(0, t.TempDir(), time.Second, nil, "/", nil)
			if err == nil {
				t.Errorf("got nil error, want injected failure on call %d (BRANCH_IF on err-return elides early exit, returning %+v)", tt.failCall, w)
			}
		})
	}
}

// TestWorkerTestWriteFailures kills BRANCH_IF on the two write paths
// inside Worker.Test (tmpSrc patched / overlay JSON). Stub writeFileFunc
// so the patched-source write fails on the second sequence of calls
// (NewWorker writes once for each of tmpSrc and overlay first).
func TestWorkerTestWriteFailures(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "f.go")
	src := []byte("package p\nvar X = 1\n")
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name        string
		failOnIndex int32 // call index at which to inject failure (post-NewWorker)
	}{
		{"patched-source write fails", 1},
		{"overlay-JSON write fails", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cache := map[string][]byte{srcPath: src}
			origWrite := writeFileFunc
			defer func() { writeFileFunc = origWrite }()
			// Phase 1: NewWorker sets up the worker with two writes
			// (tmpSrc placeholder, overlay placeholder). Let those through.
			// Phase 2: count Test's writes and fail on the requested index.
			var phase atomic.Int32
			var testCalls atomic.Int32
			writeFileFunc = func(name string, data []byte, perm os.FileMode) error {
				if phase.Load() < 2 {
					phase.Add(1)
					return os.WriteFile(name, data, perm)
				}
				if testCalls.Add(1) == tt.failOnIndex {
					return errors.New("inject")
				}
				return os.WriteFile(name, data, perm)
			}

			w, err := NewWorker(0, t.TempDir(), 5*time.Second, cache, dir, nil)
			if err != nil {
				t.Fatalf("NewWorker: %v", err)
			}

			m := mutator.Mutant{
				ID: 1, File: srcPath, Pkg: "p",
				StartOffset: len(src) - 1, EndOffset: len(src),
				Replacement: "X", Status: mutator.StatusPending,
			}
			start := time.Now()
			result := w.Test(context.Background(), m)
			elapsed := time.Since(start)

			if result.Status != mutator.StatusNotViable {
				t.Errorf("Status=%v, want NotViable — BRANCH_IF on the write-error body falls through to go test", result.Status)
			}
			// Early-return path must still set Duration — STATEMENT_REMOVE
			// on `m.Duration = nonZeroSince(start)` would leave it at zero.
			if result.Duration <= 0 {
				t.Errorf("Duration=%v on early-return path; want > 0 — STATEMENT_REMOVE drops the assignment", result.Duration)
			}
			// Early-return path is essentially instant; falling through
			// would attempt a real `go test` invocation that easily takes
			// hundreds of ms even on a tiny package.
			if elapsed > 200*time.Millisecond {
				t.Errorf("elapsed=%v on early-return path — BRANCH_IF lets execution continue past the write failure", elapsed)
			}
		})
	}
}

// TestShortFlagFromEnv kills CONDITIONALS_NEGATION on the
// `os.Getenv("GOMUTANTS_TEST_SHORT") == "1"` check.
func TestShortFlagFromEnv(t *testing.T) {
	for _, tt := range []struct {
		env  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"true", false},
		{"1", true},
	} {
		t.Run("env="+tt.env, func(t *testing.T) {
			t.Setenv("GOMUTANTS_TEST_SHORT", tt.env)
			if got := shortFlagFromEnv(); got != tt.want {
				t.Errorf("env=%q: got %v, want %v — CONDITIONALS_NEGATION on `==` flips this", tt.env, got, tt.want)
			}
		})
	}
}

// TestMakeTestCmdSetpgid kills STATEMENT_REMOVE on `cmd.SysProcAttr =
// &syscall.SysProcAttr{Setpgid: true}`. Without it, the child runs in the
// parent's process group; the RSS monitor would mistakenly include the
// parent and SIGKILL the entire test process.
func TestMakeTestCmdSetpgid(t *testing.T) {
	w := &Worker{projectDir: ".", timeout: time.Second}
	cmd, _, _ := w.makeTestCmd(context.Background(), []string{"version"})
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil — STATEMENT_REMOVE strips process-group isolation")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Errorf("Setpgid=false; want true")
	}
}

// TestMakeTestCmdGOMAXPROCSEnv kills BRANCH_IF, CONDITIONALS_BOUNDARY,
// CONDITIONALS_NEGATION, and STATEMENT_REMOVE on the
// `if w.childGOMAXPROCS > 0 { cmd.Env = append(...) }` block.
func TestMakeTestCmdGOMAXPROCSEnv(t *testing.T) {
	t.Run("zero leaves Env nil", func(t *testing.T) {
		w := &Worker{projectDir: ".", timeout: time.Second, childGOMAXPROCS: 0}
		cmd, _, _ := w.makeTestCmd(context.Background(), []string{"version"})
		if cmd.Env != nil {
			t.Errorf("Env=%v; want nil — CONDITIONALS_BOUNDARY `> 0` → `>= 0` would set env even at zero", cmd.Env)
		}
	})
	t.Run("non-zero sets GOMAXPROCS", func(t *testing.T) {
		w := &Worker{projectDir: "/proj", timeout: time.Second, childGOMAXPROCS: 3}
		cmd, _, _ := w.makeTestCmd(context.Background(), []string{"version"})
		if cmd.Env == nil {
			t.Fatal("Env is nil; want GOMAXPROCS override — BRANCH_IF on the body or STATEMENT_REMOVE on the assignment drops it")
		}
		if !envContains(cmd.Env, "GOMAXPROCS=3") {
			t.Errorf("Env missing GOMAXPROCS=3: %v", cmd.Env)
		}
		if !envContains(cmd.Env, "PWD=/proj") {
			t.Errorf("Env missing PWD=/proj: %v", cmd.Env)
		}
	})
}

func envContains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

// TestWorkerTestStartFailureClassifiesNotViable kills BRANCH_IF on the
// `if err := cmd.Start(); err != nil` body. Stub execCommandContext to
// return a Cmd whose Path is bogus so Start fails. With the body elided,
// Getpgid runs against a nil cmd.Process and panics; the original returns
// NotViable cleanly. Also asserts the diagnostic Fprintf surfaces in
// stderr (kills STATEMENT_REMOVE on the log line).
func TestWorkerTestStartFailureClassifiesNotViable(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "f.go")
	src := []byte("package p\nvar X = 1\n")
	if err := os.WriteFile(srcPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cache := map[string][]byte{srcPath: src}

	orig := execCommandContext
	defer func() { execCommandContext = orig }()
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Path that Start() will fail to exec.
		return exec.CommandContext(ctx, "/this/path/does/not/exist/zzz")
	}

	w, err := NewWorker(0, t.TempDir(), 5*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID: 1, File: srcPath, Pkg: "p",
		StartOffset: len(src) - 1, EndOffset: len(src),
		Replacement: "X", Status: mutator.StatusPending,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Worker.Test panicked on cmd.Start failure: %v — BRANCH_IF on the err-return body elides the early exit and Getpgid(nil.Pid) panics", r)
		}
	}()
	var result mutator.Mutant
	captured := captureStderr(t, func() {
		result = w.Test(context.Background(), m)
	})
	if result.Status != mutator.StatusNotViable {
		t.Errorf("Status=%v, want NotViable on cmd.Start failure", result.Status)
	}
	if result.Duration <= 0 {
		t.Errorf("Duration=%v, want > 0", result.Duration)
	}
	if !strings.Contains(captured, "cmd.Start failed") {
		t.Errorf("stderr missing the cmd.Start diagnostic; got: %q — STATEMENT_REMOVE on the Fprintf elides the log", captured)
	}
}

// TestWorkerTestGetpgidFallback kills CONDITIONALS_NEGATION and BRANCH_IF
// on the `if err != nil { pgid = cmd.Process.Pid }` fallback. Stubbing
// syscallGetpgidFunc to fail forces the body to fire; we then assert that
// the kill the RSS monitor issues went to a non-zero pid (the original
// fallback). The body-elided mutant leaves pgid at Getpgid's zero return,
// so killPgroup gets called with -0 = 0; the negation mutant flips the
// branch and the body runs on success, leaving pgid as cmd.Process.Pid
// either way — but with the body elided we observe the difference.
func TestWorkerTestGetpgidFallback(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}
	plusIdx := strings.IndexByte(string(src), '+')

	origCap := maxSubprocRSSBytes
	origPoll := monitorPollInterval
	origPS := psOutputFunc
	origKill := syscallKillFunc
	origGetpgid := syscallGetpgidFunc
	defer func() {
		maxSubprocRSSBytes = origCap
		monitorPollInterval = origPoll
		psOutputFunc = origPS
		syscallKillFunc = origKill
		syscallGetpgidFunc = origGetpgid
	}()
	maxSubprocRSSBytes = 1024
	monitorPollInterval = 50 * time.Millisecond
	psOutputFunc = func(int) ([]byte, error) { return []byte("999999\n"), nil }

	// Force the Getpgid path to fail so the fallback body must execute.
	syscallGetpgidFunc = func(int) (int, error) {
		return 0, errors.New("inject")
	}

	var killedPid atomic.Int32
	syscallKillFunc = func(pid int, _ syscall.Signal) error {
		// Capture the first non-zero kill pid we see (later polls may also
		// fire before the goroutine returns).
		if killedPid.Load() == 0 {
			killedPid.Store(int32(pid))
		}
		return nil
	}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID: 1, File: srcPath, Pkg: "testmod",
		StartOffset: plusIdx, EndOffset: plusIdx + 1,
		Replacement: "-", Status: mutator.StatusPending,
	}
	w.Test(context.Background(), m)

	got := killedPid.Load()
	if got == 0 {
		t.Fatalf("syscallKillFunc never invoked")
	}
	// killPgroup negates pgid, so a nonzero result here means the fallback
	// `pgid = cmd.Process.Pid` did run. Body elision (BRANCH_IF) leaves
	// pgid=0, killPgroup sends -0 == 0; mutating `!=` to `==` makes the
	// body fire on the success path with the same value, but the success
	// path is gated out by our Getpgid stub.
	if got == 0 {
		t.Errorf("kill targeted pgid=0 — BRANCH_IF on the fallback elides `pgid = cmd.Process.Pid`")
	}
}

// TestKillPgroupSendsNegativePgid kills INVERT_NEGATIVES on `-pgid`. The
// negative pgid is what makes syscall.Kill target the entire process
// group; with `+pgid` only the leader gets the signal, leaving children
// alive — defeating the RSS-runaway containment.
func TestKillPgroupSendsNegativePgid(t *testing.T) {
	orig := syscallKillFunc
	defer func() { syscallKillFunc = orig }()
	var got int
	syscallKillFunc = func(pid int, sig syscall.Signal) error {
		got = pid
		return nil
	}
	killPgroup(123)
	if got != -123 {
		t.Errorf("syscallKillFunc called with pid=%d, want -123 — INVERT_NEGATIVES on -pgid flips the sign", got)
	}
}

// TestWorkerTestRSSKillsRunaway kills BRANCH_IF on the
// `if pgroupRSSBytes(pgid) > maxSubprocRSSBytes` body and STATEMENT_REMOVE
// on `killPgroup(pgid)`. Stubbing syscallKillFunc lets us assert the kill
// was actually issued without sending a real signal that would tear down
// the test process tree.
func TestWorkerTestRSSKillsRunaway(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}
	plusIdx := strings.IndexByte(string(src), '+')

	origCap := maxSubprocRSSBytes
	origPoll := monitorPollInterval
	origPS := psOutputFunc
	origKill := syscallKillFunc
	defer func() {
		maxSubprocRSSBytes = origCap
		monitorPollInterval = origPoll
		psOutputFunc = origPS
		syscallKillFunc = origKill
	}()
	maxSubprocRSSBytes = 1024 // 1 KB — well below any real process
	monitorPollInterval = 50 * time.Millisecond

	psOutputFunc = func(int) ([]byte, error) {
		// Way above the tiny cap so the kill path always fires.
		return []byte("999999\n"), nil
	}
	var killCalls atomic.Int32
	syscallKillFunc = func(pid int, sig syscall.Signal) error {
		killCalls.Add(1)
		// Don't actually kill — let the cmd run to natural completion.
		return nil
	}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID: 1, File: srcPath, Pkg: "testmod",
		StartOffset: plusIdx, EndOffset: plusIdx + 1,
		Replacement: "-", Status: mutator.StatusPending,
	}
	result := w.Test(context.Background(), m)
	if result.Status != mutator.StatusTimedOut {
		t.Errorf("Status=%v, want TimedOut — BRANCH_IF on `pgroupRSSBytes > cap` body skips the kill", result.Status)
	}
	if killCalls.Load() == 0 {
		t.Errorf("syscallKillFunc never invoked — STATEMENT_REMOVE on killPgroup elides the kill call")
	}
}

// TestWorkerTestRSSExactlyAtCapDoesNotKill kills CONDITIONALS_BOUNDARY on
// `pgroupRSSBytes(pgid) > maxSubprocRSSBytes`. With ps reporting exactly
// the cap, the original `>` evaluates to false and no kill fires; the
// boundary mutant `>=` would trigger the kill on the equality.
func TestWorkerTestRSSExactlyAtCapDoesNotKill(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}
	plusIdx := strings.IndexByte(string(src), '+')

	origCap := maxSubprocRSSBytes
	origPoll := monitorPollInterval
	origPS := psOutputFunc
	origKill := syscallKillFunc
	defer func() {
		maxSubprocRSSBytes = origCap
		monitorPollInterval = origPoll
		psOutputFunc = origPS
		syscallKillFunc = origKill
	}()
	maxSubprocRSSBytes = 1024
	monitorPollInterval = 50 * time.Millisecond

	// ps RSS column is in KB, multiplied by 1024 inside pgroupRSSBytes.
	// "1\n" ⇒ total = 1024 bytes, exactly equal to cap.
	psOutputFunc = func(int) ([]byte, error) { return []byte("1\n"), nil }
	var killCalls atomic.Int32
	syscallKillFunc = func(int, syscall.Signal) error {
		killCalls.Add(1)
		return nil
	}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID: 1, File: srcPath, Pkg: "testmod",
		StartOffset: plusIdx, EndOffset: plusIdx + 1,
		Replacement: "-", Status: mutator.StatusPending,
	}
	result := w.Test(context.Background(), m)
	// The mutated `+`→`-` makes TestAdd fail, so original status is Killed.
	// Boundary mutation `>=` would trigger the RSS kill at the equality
	// boundary, classifying the result as TimedOut instead.
	if result.Status == mutator.StatusTimedOut {
		t.Errorf("RSS == cap should not trigger kill (`>` boundary); CONDITIONALS_BOUNDARY mutation `>=` triggers here")
	}
	if killCalls.Load() != 0 {
		t.Errorf("syscallKillFunc called %d times — boundary `>=` lets equality fire", killCalls.Load())
	}
}

// TestWorkerTestMonitorGoroutineExits kills STATEMENT_REMOVE on
// `close(monitorDone)`. With RSS well below the cap the kill path never
// fires, so the goroutine relies on `<-monitorDone` to exit. Without the
// close, it keeps polling ps after Worker.Test returns.
func TestWorkerTestMonitorGoroutineExits(t *testing.T) {
	dir := setupTestProject(t)
	srcPath := filepath.Join(dir, "add.go")
	src, _ := os.ReadFile(srcPath)
	cache := map[string][]byte{srcPath: src}
	plusIdx := strings.IndexByte(string(src), '+')

	origPoll := monitorPollInterval
	origPS := psOutputFunc
	defer func() {
		monitorPollInterval = origPoll
		psOutputFunc = origPS
	}()
	monitorPollInterval = 50 * time.Millisecond

	var psCalls atomic.Int32
	psOutputFunc = func(int) ([]byte, error) {
		psCalls.Add(1)
		return []byte("0\n"), nil // far below cap; no kill
	}

	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	m := mutator.Mutant{
		ID: 1, File: srcPath, Pkg: "testmod",
		StartOffset: plusIdx, EndOffset: plusIdx + 1,
		Replacement: "-", Status: mutator.StatusPending,
	}
	w.Test(context.Background(), m)

	atReturn := psCalls.Load()
	time.Sleep(4 * monitorPollInterval)
	if growth := psCalls.Load() - atReturn; growth > 1 {
		t.Errorf("ps polled %d more times after Test returned — STATEMENT_REMOVE on close(monitorDone) leaks the goroutine", growth)
	}
}

// TestNonZeroSinceSleep kills CONDITIONALS_NEGATION on `d <= 0` (line 60):
// mutated `d > 0` takes the Nanosecond branch on every normal call, so
// the returned duration would be exactly 1 ns even after a real sleep.
func TestNonZeroSinceSleep(t *testing.T) {
	start := time.Now()
	time.Sleep(5 * time.Millisecond)
	d := nonZeroSince(start)
	if d < 5*time.Millisecond {
		t.Errorf("nonZeroSince after 5ms sleep = %v, want >= 5ms (mutation returns 1ns)", d)
	}
}

// TestNonZeroSinceFuture kills the BRANCH_IF on `{ return time.Nanosecond }`:
// a start time in the future yields d <= 0 from time.Since. The original
// returns time.Nanosecond (>0) so callers can use 0 as a "never set"
// sentinel. Under BRANCH_IF the body is elided and 0 or negative leaks out.
func TestNonZeroSinceFuture(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	d := nonZeroSince(future)
	if d <= 0 {
		t.Errorf("nonZeroSince(future) = %v, want > 0 (sentinel positive duration)", d)
	}
}

// TestCappedBufferCapsAtMax kills ARITHMETIC_BASE and INVERT_NEGATIVES on
// `maxCapturedOutput - len(c.buf)` (line 78): mutated `+` makes remaining
// always large, so the buffer grows past its cap. We write 2× the cap and
// assert the stored bytes don't exceed the cap.
func TestCappedBufferCapsAtMax(t *testing.T) {
	var c cappedBuffer
	chunk := make([]byte, 64*1024) // 64 KiB chunks
	for range 40 {                 // 40 * 64 KiB = 2.5 MiB, well above 1 MiB cap
		n, _ := c.Write(chunk)
		if n != len(chunk) {
			t.Errorf("Write returned n=%d, want %d (must report full length to satisfy io.Writer)", n, len(chunk))
		}
	}
	if len(c.buf) > maxCapturedOutput {
		t.Errorf("buf grew to %d bytes, exceeds cap %d — capping arithmetic broken", len(c.buf), maxCapturedOutput)
	}
	// Must have captured at least something up to the cap.
	if len(c.buf) == 0 {
		t.Errorf("buf is empty after writes — cap check too aggressive")
	}
}

// TestCappedBufferPartialFinalWrite kills patterns that mishandle the
// "final write exceeds remaining" branch. After writing cap-1 bytes, a
// second write of 10 bytes should fill to exactly the cap (1 byte taken
// from the second chunk).
func TestCappedBufferPartialFinalWrite(t *testing.T) {
	var c cappedBuffer
	first := make([]byte, maxCapturedOutput-1)
	c.Write(first)
	if len(c.buf) != maxCapturedOutput-1 {
		t.Fatalf("after first write: len=%d, want %d", len(c.buf), maxCapturedOutput-1)
	}
	// Second write: 10 bytes, but only 1 byte of remaining capacity.
	n, _ := c.Write([]byte("0123456789"))
	if n != 10 {
		t.Errorf("Write n=%d, want 10 (must report full input length)", n)
	}
	if len(c.buf) != maxCapturedOutput {
		t.Errorf("after partial write: len=%d, want %d (cap)", len(c.buf), maxCapturedOutput)
	}
}

// TestCappedBufferWriteAtCap kills mutations on the `remaining > 0` guard:
// once buf is at the cap, further writes must be no-ops but still return
// the input length (to satisfy the io.Writer contract).
func TestCappedBufferWriteAtCap(t *testing.T) {
	var c cappedBuffer
	c.buf = make([]byte, maxCapturedOutput)
	before := len(c.buf)
	n, err := c.Write([]byte("extra"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write n=%d, want 5", n)
	}
	if len(c.buf) != before {
		t.Errorf("buf grew past cap: len=%d, was %d", len(c.buf), before)
	}
}

// TestCappedBufferString kills trivial mutations on the String() accessor
// (e.g., STATEMENT_REMOVE on the return) by exercising it on real data.
func TestCappedBufferString(t *testing.T) {
	var c cappedBuffer
	c.Write([]byte("hello"))
	if got := c.String(); got != "hello" {
		t.Errorf("String() = %q, want %q", got, "hello")
	}
}

// TestWorkerTestParentCtxCancel verifies that a parent-context
// cancellation (Ctrl-C, upstream deadline) is NOT classified as Killed.
// The worker should preserve the incoming Status (Pending) + zero
// Duration so the pool surfaces the mutant as not tested.
//
// Cost: ~300-500 ms per run — the inner test binary sleeps until the
// parent ctx fires. Keep this in mind when adding similar patterns.
func TestWorkerTestParentCtxCancel(t *testing.T) {
	dir := t.TempDir()
	goMod := "module testmod\n\ngo 1.26\n"
	src := "package testpkg\n\nfunc Add(a, b int) int { return a + b }\n"
	testSrc := "package testpkg\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(30 * time.Second) }\n"

	for name, body := range map[string]string{
		"go.mod": goMod, "add.go": src, "add_test.go": testSrc,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cache := map[string][]byte{filepath.Join(dir, "add.go"): []byte(src)}
	w, err := NewWorker(0, t.TempDir(), 30*time.Second, cache, dir, nil)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	plusIdx := 0
	for i, c := range src {
		if c == '+' && i > 30 {
			plusIdx = i
			break
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := mutator.Mutant{
		ID: 1, File: filepath.Join(dir, "add.go"), Pkg: "testmod",
		StartOffset: plusIdx, EndOffset: plusIdx + 1, Replacement: "-",
		Status: mutator.StatusPending,
	}

	// Cancel mid-run: the test binary above sleeps 30s, so parent-ctx
	// cancellation fires before the test returns naturally.
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	result := w.Test(ctx, m)
	if result.Status != mutator.StatusPending {
		t.Errorf("Status=%v, want Pending — parent-ctx cancel must not produce a terminal classification", result.Status)
	}
	// Invariant: Pending ⇒ Duration==0. Otherwise the report shows a
	// "not tested" mutant with an execution time, which is misleading.
	if result.Duration != 0 {
		t.Errorf("Duration=%v on cancelled (Pending) mutant, want 0", result.Duration)
	}
}

// TestBuildTestArgsShortFlag kills CONDITIONALS_NEGATION / BRANCH_IF on
// the GOMUTANTS_TEST_SHORT gate: passing short=true must add "-short" to
// the command line; short=false must omit it. We assert both directions.
func TestBuildTestArgsShortFlag(t *testing.T) {
	w := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{Pkg: "mymod"}

	withShort := w.buildTestArgs(m, true)
	if !containsStr(withShort, "-short") {
		t.Errorf("short=true: args %v missing -short", withShort)
	}
	withoutShort := w.buildTestArgs(m, false)
	if containsStr(withoutShort, "-short") {
		t.Errorf("short=false: args %v should not contain -short", withoutShort)
	}
}

// TestBuildTestArgsTestCPU kills BRANCH_IF / CONDITIONALS_BOUNDARY on the
// `if w.testCPU > 0` gate. With testCPU=2 the args must include `-cpu=2`;
// with testCPU=0 the `-cpu=` arg must be absent (let go test default to
// GOMAXPROCS, matching gremlins).
func TestBuildTestArgsTestCPU(t *testing.T) {
	m := mutator.Mutant{Pkg: "mymod"}

	wOn := &Worker{testCPU: 2, timeout: time.Second, overlayPath: "/tmp/o.json"}
	argsOn := wOn.buildTestArgs(m, false)
	if !containsStr(argsOn, "-cpu=2") {
		t.Errorf("testCPU=2: args %v missing -cpu=2", argsOn)
	}

	wOff := &Worker{testCPU: 0, timeout: time.Second, overlayPath: "/tmp/o.json"}
	argsOff := wOff.buildTestArgs(m, false)
	if anyHasPrefix(argsOff, "-cpu=") {
		t.Errorf("testCPU=0: args %v should not contain -cpu=", argsOff)
	}
}

// TestBuildTestArgsPackageArgLast kills STATEMENT_REMOVE on
// `args = append(args, m.Pkg)`: removing that line leaves the command
// without a package target. Asserting that the package shows up as the
// final positional arg catches both the removal and any reordering.
func TestBuildTestArgsPackageArgLast(t *testing.T) {
	w := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{Pkg: "example.com/mod/sub"}
	args := w.buildTestArgs(m, false)
	if len(args) == 0 || args[len(args)-1] != "example.com/mod/sub" {
		t.Errorf("last arg = %q, want package import path; full args: %v",
			args[len(args)-1], args)
	}
	// Also: -timeout, -overlay, -failfast, -count=1, -vet=off must all be present.
	for _, want := range []string{"-failfast", "-count=1", "-vet=off"} {
		if !containsStr(args, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	if !anyHasPrefix(args, "-overlay=") {
		t.Errorf("args missing -overlay=…: %v", args)
	}
	if !anyHasPrefix(args, "-timeout=") {
		t.Errorf("args missing -timeout=…: %v", args)
	}
}

// TestBuildTestArgsWithTestMap kills CONDITIONALS_NEGATION / BRANCH_IF on
// `if w.testMap != nil`. With a non-nil map that actually contains the
// mutant's (file, line), the command line must include `-run=<regex>`.
// With no map, no -run should appear. Under either mutation, the -run
// flag would be either missing (when it should appear) or leak (via a
// nil-deref panic in the negation case).
func TestBuildTestArgsWithTestMap(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("go.mod", "module testmod\n\ngo 1.26\n")
	mustWrite("add.go", "package testmod\n\nfunc Add(a, b int) int { return a + b }\n")
	mustWrite("add_test.go", "package testmod\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal(\"wrong\") } }\n")

	tm, err := coverage.BuildTestMap(context.Background(), dir, []string{"testmod"}, "", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}

	wWith := &Worker{testMap: tm, timeout: time.Second, overlayPath: "/tmp/o.json"}
	wWithout := &Worker{timeout: time.Second, overlayPath: "/tmp/o.json"}
	m := mutator.Mutant{
		CoverageFile: "testmod/add.go",
		Line:         3,
		Pkg:          "testmod",
	}
	// With map: -run=<pattern> must appear.
	argsWith := wWith.buildTestArgs(m, false)
	if !anyHasPrefix(argsWith, "-run=") {
		t.Errorf("testMap non-nil with matching entry: expected -run= in %v", argsWith)
	}
	// Without map: -run= must not appear.
	argsWithout := wWithout.buildTestArgs(m, false)
	if anyHasPrefix(argsWithout, "-run=") {
		t.Errorf("testMap nil: -run= must be absent, got %v", argsWithout)
	}
	// With map but no matches for this (file, line): -run= must not appear.
	// Kills CONDITIONALS_BOUNDARY on `len(tests) > 0` — mutated `>= 0` would
	// always enter the branch and append -run= with an empty pattern.
	mMiss := mutator.Mutant{CoverageFile: "unknown/file.go", Line: 9999, Pkg: "testmod"}
	argsMiss := wWith.buildTestArgs(mMiss, false)
	if anyHasPrefix(argsMiss, "-run=") {
		t.Errorf("testMap non-nil but no matches: -run= must be absent (len(tests)>0 guard), got %v", argsMiss)
	}
}

// TestClassifyTestOutcome covers every branch of the classifier.
// Kills BRANCH_IF on the memKilled short-circuit, the runErr==nil
// Lived return, the DeadlineExceeded arm, and both EXPRESSION_REMOVE
// mutations on the `compileErrorRe && ([build failed] || [setup failed])`
// predicate.
func TestClassifyTestOutcome(t *testing.T) {
	anyErr := errors.New("exit status 1")
	tests := []struct {
		name       string
		runErr     error
		memKilled  bool
		testCtxErr error
		stdout     string
		stderr     string
		want       mutator.MutantStatus
	}{
		{"memkilled beats everything", anyErr, true, context.DeadlineExceeded, "", "", mutator.StatusTimedOut},
		// memKilled with otherwise-clean outcome: if the BRANCH_IF on the
		// memKilled early return is elided, execution falls through to
		// `runErr == nil → Lived`. Asserting TimedOut here kills that
		// mutation.
		{"memkilled alone still wins", nil, true, nil, "", "", mutator.StatusTimedOut},
		{"success => lived", nil, false, nil, "", "", mutator.StatusLived},
		{"timeout before classify", anyErr, false, context.DeadlineExceeded, "", "", mutator.StatusTimedOut},
		{"compile failure => not viable", anyErr, false, nil,
			"FAIL\ttestmod [build failed]\n", "worker-0.go:5:2: undefined: Foo\n", mutator.StatusNotViable},
		{"setup failure => not viable", anyErr, false, nil,
			"FAIL\ttestmod [setup failed]\n", "worker-0.go:5:2: cannot use\n", mutator.StatusNotViable},
		{"stderr compile regex but no [build failed] in stdout => killed", anyErr, false, nil,
			"--- FAIL: TestX\nadd_test.go:7: wrong\n", "worker-0.go:5:2: undefined\n", mutator.StatusKilled},
		{"[build failed] in stdout but no compile regex in stderr => killed", anyErr, false, nil,
			"FAIL [build failed]\n", "", mutator.StatusKilled},
		{"normal test failure => killed", anyErr, false, nil,
			"--- FAIL: TestAdd\n", "add_test.go:7: Add(1,2) != 3\n", mutator.StatusKilled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyTestOutcome(tc.runErr, tc.memKilled, tc.testCtxErr, tc.stdout, tc.stderr)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Test helpers.
func containsStr(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
func anyHasPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

func TestCompileErrorRegex(t *testing.T) {
	tests := []struct {
		input string
		match bool
	}{
		{"./file.go:10:5: undefined: foo", true},
		{"main.go:1:1: expected declaration", true},
		{"FAIL\ttestmod\t0.001s", false},
		{"ok  \ttestmod\t0.001s", false},
	}
	for _, tc := range tests {
		if got := compileErrorRe.MatchString(tc.input); got != tc.match {
			t.Errorf("compileErrorRe.Match(%q) = %v, want %v", tc.input, got, tc.match)
		}
	}
}
