package coverage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
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

// TestAddBlocksContinuesPastCount0 kills INVERT_LOOP_CTRL on the `continue`
// in addBlocks (testmap.go:132). Mutated to `break`, hitting any Count==0
// block aborts the entire block walk so later Count>0 blocks never make
// it into the index.
func TestAddBlocksContinuesPastCount0(t *testing.T) {
	tm := &TestMap{index: make(map[string]map[string]bool)}
	tm.addBlocks("TestX", []Block{
		// Count==0 first — must be skipped, not break.
		{File: "f.go", StartLine: 1, EndLine: 1, Count: 0},
		// Count>0 covering line 10 — must be indexed.
		{File: "f.go", StartLine: 10, EndLine: 10, Count: 1},
	})
	if _, ok := tm.index["f.go:10"]; !ok {
		t.Errorf("index missing f.go:10 — addBlocks must continue past a Count==0 block; got index keys %v", keysOf(tm.index))
	}
}

func keysOf(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestProcessWorkReturnsImmediatelyOnCancelledCtx kills BRANCH_IF on the
// `if ctx.Err() != nil { return }` body in processWork (testmap.go:147).
// With the body elided, the loop falls through to runCompiledTestFunc.
// Stub it and assert no calls happen when ctx is cancelled before dispatch.
func TestProcessWorkReturnsImmediatelyOnCancelledCtx(t *testing.T) {
	orig := runCompiledTestFunc
	defer func() { runCompiledTestFunc = orig }()
	var calls atomic.Int32
	runCompiledTestFunc = func(_ context.Context, _ *compiledPkg, _, _ string) []Block {
		calls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	work := make(chan testEntry, 1)
	results := make(chan testCoverage, 1)
	work <- testEntry{name: "TestA", pkg: "pkg1"}
	close(work)

	pkgBins := map[string]*compiledPkg{
		"pkg1": {binPath: "x", importPath: "pkg1", dir: t.TempDir()},
	}
	processWork(ctx, work, pkgBins, t.TempDir(), 0, results)
	close(results)

	if got := calls.Load(); got != 0 {
		t.Errorf("runCompiledTestFunc called %d times after ctx cancel — BRANCH_IF on the ctx-return body lets execution fall through", got)
	}
}

// TestProcessWorkContinuesPastEmptyBlocks kills INVERT_LOOP_CTRL on the
// `continue` for `len(blocks) == 0` (testmap.go:138). Mutated to `break`,
// a single empty-blocks test ends the worker before later non-empty tests
// run, so their results never reach the channel.
func TestProcessWorkContinuesPastEmptyBlocks(t *testing.T) {
	orig := runCompiledTestFunc
	defer func() { runCompiledTestFunc = orig }()
	runCompiledTestFunc = func(_ context.Context, _ *compiledPkg, testName, _ string) []Block {
		if testName == "TestEmpty" {
			return nil
		}
		return []Block{{File: "f.go", StartLine: 1, EndLine: 1, Count: 1}}
	}

	work := make(chan testEntry, 2)
	results := make(chan testCoverage, 2)
	work <- testEntry{name: "TestEmpty", pkg: "pkg1"}
	work <- testEntry{name: "TestNonEmpty", pkg: "pkg1"}
	close(work)

	pkgBins := map[string]*compiledPkg{
		"pkg1": {binPath: "x", importPath: "pkg1", dir: t.TempDir()},
	}
	processWork(context.Background(), work, pkgBins, t.TempDir(), 0, results)
	close(results)

	count := 0
	for range results {
		count++
	}
	if count != 1 {
		t.Errorf("got %d results, want 1 — INVERT_LOOP_CTRL turns the empty-blocks `continue` into `break`, killing the next test", count)
	}
}

// TestProcessWorkContinuesPastNilCp kills INVERT_LOOP_CTRL on the
// `continue` for `cp == nil` (testmap.go:152). Mutated to `break`, a single
// missing-pkg test stops the worker — later valid tests never run.
func TestProcessWorkContinuesPastNilCp(t *testing.T) {
	orig := runCompiledTestFunc
	defer func() { runCompiledTestFunc = orig }()
	var calls atomic.Int32
	runCompiledTestFunc = func(_ context.Context, cp *compiledPkg, _, _ string) []Block {
		calls.Add(1)
		return nil
	}

	work := make(chan testEntry, 2)
	results := make(chan testCoverage, 1)
	work <- testEntry{name: "TestUnknownPkg", pkg: "missing"}
	work <- testEntry{name: "TestKnownPkg", pkg: "pkg1"}
	close(work)

	pkgBins := map[string]*compiledPkg{
		"pkg1": {binPath: "x", importPath: "pkg1", dir: t.TempDir()},
	}
	processWork(context.Background(), work, pkgBins, t.TempDir(), 0, results)
	close(results)

	if got := calls.Load(); got != 1 {
		t.Errorf("runCompiledTestFunc called %d times, want 1 — INVERT_LOOP_CTRL turns the cp==nil `continue` into `break`, killing later tests", got)
	}
}

// TestProcessWorkSkipsEmptyBlocks kills CONDITIONALS_NEGATION on the
// `if len(blocks) == 0` guard in processWork (post-refactor). Without the
// guard, every test result — including those with no coverage blocks —
// gets sent to the results channel, polluting the map with empty entries.
func TestProcessWorkSkipsEmptyBlocks(t *testing.T) {
	orig := runCompiledTestFunc
	defer func() { runCompiledTestFunc = orig }()
	runCompiledTestFunc = func(_ context.Context, _ *compiledPkg, _, _ string) []Block {
		return nil // simulate test that produced no coverage blocks
	}

	work := make(chan testEntry, 1)
	results := make(chan testCoverage, 1)
	work <- testEntry{name: "TestNoCoverage", pkg: "pkg1"}
	close(work)

	pkgBins := map[string]*compiledPkg{
		"pkg1": {binPath: "x", importPath: "pkg1", dir: t.TempDir()},
	}
	processWork(context.Background(), work, pkgBins, t.TempDir(), 0, results)
	close(results)

	count := 0
	for range results {
		count++
	}
	if count != 0 {
		t.Errorf("got %d results, want 0 — empty-block tests must not produce a result entry", count)
	}
}

// TestBuildPkgBinsSkipsCompileFailure kills BRANCH_IF on the
// `if err != nil { continue }` body in buildPkgBins. Without the continue,
// pkgBins[pkg.importPath] = cp executes with cp==nil, leaking a nil entry
// into the map. Through processWork the difference is invisible (nil entry
// vs missing key both fail the cp==nil guard); inspecting pkgBins directly
// is what makes the mutant observable.
func TestBuildPkgBinsSkipsCompileFailure(t *testing.T) {
	orig := compileTestBinaryFunc
	defer func() { compileTestBinaryFunc = orig }()
	compileTestBinaryFunc = func(_ context.Context, _, _, _ string, pkg resolvedPkg) (*compiledPkg, error) {
		if pkg.importPath == "fail" {
			return nil, errors.New("compile failed")
		}
		return &compiledPkg{importPath: pkg.importPath, binPath: "x", dir: pkg.dir}, nil
	}

	bins := buildPkgBins(context.Background(), "", "", "", []resolvedPkg{
		{importPath: "fail"},
		{importPath: "ok"},
	})

	if _, ok := bins["fail"]; ok {
		t.Errorf("pkgBins should not contain failed pkg — BRANCH_IF on the err-continue body lets nil entries through; got bins=%v", bins)
	}
	if _, ok := bins["ok"]; !ok {
		t.Errorf("pkgBins should contain ok pkg, got bins=%v", bins)
	}
}

// TestCompileTestBinaryReturnsErrOnRunFailure kills BRANCH_IF on the
// `if err := cmd.Run(); err != nil { return nil, ... }` body in
// compileTestBinary. We pre-stage a stale file at the expected output
// path; `go test -c` against a bogus package fails, so under the original
// the function short-circuits to error. Under mutation it falls through to
// statFileFunc, which finds the stale file and returns *compiledPkg, nil.
func TestCompileTestBinaryReturnsErrOnRunFailure(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := resolvedPkg{importPath: "definitely.not.a.real/pkg/zzz", dir: t.TempDir()}
	binPath := filepath.Join(tmpDir, "testbin-"+sanitize(pkg.importPath)+".test")
	if err := os.WriteFile(binPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	cp, err := compileTestBinary(context.Background(), pkg.dir, tmpDir, "", pkg)
	if err == nil {
		t.Errorf("expected error from cmd.Run failure, got nil with cp=%+v — BRANCH_IF on the run-error return lets a stale binary masquerade as success", cp)
	}
	if err != nil && !strings.Contains(err.Error(), "go test -c") {
		t.Errorf("error should wrap `go test -c` failure, got: %v", err)
	}
}

// TestCompileTestBinaryReturnsErrOnMissingBinary kills BRANCH_IF on the
// `if _, err := statFileFunc(binPath); err != nil { return nil, ... }`
// body in compileTestBinary. Stub statFileFunc to fail; under the original
// the missing-binary path returns an error. Under mutation execution falls
// through to the &compiledPkg{} return.
func TestCompileTestBinaryReturnsErrOnMissingBinary(t *testing.T) {
	dir := setupTestProject(t)
	tmpDir := t.TempDir()

	orig := statFileFunc
	defer func() { statFileFunc = orig }()
	statFileFunc = func(string) (os.FileInfo, error) {
		return nil, errors.New("injected missing-binary")
	}

	cp, err := compileTestBinary(context.Background(), dir, tmpDir, "", resolvedPkg{importPath: "testmod", dir: dir})
	if err == nil {
		t.Errorf("expected error when statFileFunc fails, got nil with cp=%+v — BRANCH_IF on the missing-binary return elides the early exit", cp)
	}
	if err != nil && !strings.Contains(err.Error(), "test binary missing") {
		t.Errorf("error should wrap missing-binary failure, got: %v", err)
	}
}

// TestRunCompiledTestUsesPkgDirAsCwd kills STATEMENT_REMOVE on the
// `cmd.Dir = cp.dir` line. The compiled test binary opens
// "./testdata/sample.txt" — a path resolved against the process cwd.
// Under the original, cwd is set to cp.dir so the open succeeds and the
// test passes (coverage blocks recorded). Under mutation cmd.Dir stays
// empty so the binary runs from the test process's cwd; the file isn't
// there, the test fails, cmd.Run reports non-zero, and we get nil blocks.
func TestRunCompiledTestUsesPkgDirAsCwd(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module cwdmod\n\ngo 1.26\n",
		"lib.go": "package cwdmod\n\nfunc Touch() {}\n",
		"lib_test.go": `package cwdmod
import (
	"os"
	"testing"
)
func TestNeedsCwd(t *testing.T) {
	Touch()
	if _, err := os.Stat("testdata/sample.txt"); err != nil {
		t.Fatalf("relative path resolved against wrong cwd: %v", err)
	}
}
`,
		"testdata/sample.txt": "ok\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tmpDir := t.TempDir()
	pkg := resolvedPkg{importPath: "cwdmod", dir: dir}
	cp, err := compileTestBinary(context.Background(), dir, tmpDir, "", pkg)
	if err != nil {
		t.Fatalf("compileTestBinary: %v", err)
	}

	profilePath := filepath.Join(tmpDir, "cwd.cov")
	blocks := runCompiledTest(context.Background(), cp, "TestNeedsCwd", profilePath)
	if len(blocks) == 0 {
		t.Errorf("expected coverage blocks; the test fails when cwd != cp.dir, so STATEMENT_REMOVE on `cmd.Dir = cp.dir` would zero this out")
	}
}

// TestBuildTestMapContinuesPastFailedCompile kills INVERT_LOOP_CTRL on the
// `continue` after a compile failure in BuildTestMap. Stub
// compileTestBinaryFunc to fail for the first package and succeed for the
// second; assert the second package's tests still drive runCompiledTestFunc.
func TestBuildTestMapContinuesPastFailedCompile(t *testing.T) {
	origCompile := compileTestBinaryFunc
	origResolve := resolvePackagesFunc
	origList := listTestsFunc
	origRun := runCompiledTestFunc
	defer func() {
		compileTestBinaryFunc = origCompile
		resolvePackagesFunc = origResolve
		listTestsFunc = origList
		runCompiledTestFunc = origRun
	}()

	resolvePackagesFunc = func(_ context.Context, _ string, _ []string) ([]resolvedPkg, error) {
		return []resolvedPkg{
			{importPath: "pkg.fail", dir: t.TempDir()},
			{importPath: "pkg.ok", dir: t.TempDir()},
		}, nil
	}
	listTestsFunc = func(_ context.Context, _ string, _ []string) ([]testEntry, error) {
		return []testEntry{{name: "TestA", pkg: "pkg.ok"}}, nil
	}
	compileTestBinaryFunc = func(_ context.Context, _, _, _ string, pkg resolvedPkg) (*compiledPkg, error) {
		if pkg.importPath == "pkg.fail" {
			return nil, errors.New("compile failed")
		}
		return &compiledPkg{binPath: "x", importPath: pkg.importPath, dir: pkg.dir}, nil
	}
	var ranTests int32
	runCompiledTestFunc = func(_ context.Context, cp *compiledPkg, _, _ string) []Block {
		atomic.AddInt32(&ranTests, 1)
		if cp.importPath != "pkg.ok" {
			t.Errorf("runCompiledTestFunc invoked with wrong pkg %q", cp.importPath)
		}
		return nil
	}

	_, err := BuildTestMap(context.Background(), t.TempDir(), []string{"./..."}, "", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("BuildTestMap: %v", err)
	}
	if got := atomic.LoadInt32(&ranTests); got != 1 {
		t.Errorf("runCompiledTestFunc called %d times, want 1 — INVERT_LOOP_CTRL turns the compile-failure `continue` into `break`, dropping pkg.ok", got)
	}
}

// TestParseListTestsOutputContinuesPastOk kills INVERT_LOOP_CTRL on the
// `continue` for ok-prefixed/empty lines (testmap.go:253). Mutated to
// `break`, an "ok" status line in the middle of `go test -list` output
// would terminate the scan and drop later test names.
func TestParseListTestsOutputContinuesPastOk(t *testing.T) {
	in := strings.NewReader("TestA\nok  \tpkg\t0.001s\nTestB\n")
	got := parseListTestsOutput(in, "pkg")
	if len(got) != 2 || got[0].name != "TestA" || got[1].name != "TestB" {
		t.Errorf("got %+v, want [TestA, TestB] — INVERT_LOOP_CTRL on the ok-line `continue` turns later test discovery into break", got)
	}
}

// TestParseListTestsOutputSkipsEmptyLine kills EXPRESSION_REMOVE on the
// `line == ""` operand of the skip guard. With it replaced by `false`,
// empty lines are no longer skipped and surface as testEntry{Name: ""}.
func TestParseListTestsOutputSkipsEmptyLine(t *testing.T) {
	in := strings.NewReader("TestA\n\nTestB\n")
	got := parseListTestsOutput(in, "pkg")
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (empty line skipped): %+v", len(got), got)
	}
	for _, e := range got {
		if e.name == "" {
			t.Errorf("empty test name leaked through — EXPRESSION_REMOVE replaces `line == \"\"` with false, which lets blank lines fall through")
		}
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

