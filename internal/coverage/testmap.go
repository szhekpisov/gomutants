package coverage

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// tagsBuildFlag is the `go` build-tags flag prefix; the configured tags
// value is appended to it when non-empty.
const tagsBuildFlag = "-tags="

// Function variables for testing.
var (
	resolvePackagesFunc   = resolvePackages
	listTestsFunc         = listTests
	parseFileFunc         = ParseFile
	compileTestBinaryFunc = compileTestBinary
	runCompiledTestFunc   = runCompiledTest
	statFileFunc          = os.Stat
)

// TestMap maps (file, line) positions to the test functions that cover them.
//
// Concurrency: TestMap is built sequentially in BuildTestMap (the
// receive loop is single-goroutine even though the producers are
// parallel) and is treated as immutable thereafter. The runner's
// per-mutant Worker.Test reads it concurrently across N workers
// without locking — that's safe only because the build phase
// completes before any worker reads. Don't expose new methods that
// mutate TestMap state after BuildTestMap returns, or the lock-free
// read assumption breaks.
type TestMap struct {
	// index maps "file:line" to a set of test function names.
	index map[string]map[string]bool

	// durations is the per-test execution time captured during the
	// per-test coverage build, keyed by (pkg, test). Per-mutant adaptive
	// timeout reads from this directly. Same-named tests in different
	// packages get distinct entries because go test scopes -run within
	// a single package.
	durations map[testKey]time.Duration

	// pkgDurations is the running sum of per-test durations per package,
	// used as the per-package fallback when a mutant has no per-test
	// covering set (e.g. mutated line outside any covered block). Kept
	// alongside durations so package totals don't require a fresh
	// O(n) scan on every Worker.computeTimeout call.
	pkgDurations map[string]time.Duration
}

// testKey identifies a single (pkg, test) timing entry. Using a struct
// instead of "pkg::name" string concatenation avoids the parsing cost
// on every lookup and removes a class of mutation surface (string-key
// off-by-one) that would force the timeout selector into a defensive
// trim path.
type testKey struct {
	pkg, name string
}

type testCoverage struct {
	pkg      string
	testName string
	duration time.Duration
	blocks   []Block
}

// compiledPkg holds a pre-compiled test binary for a package.
type compiledPkg struct {
	binPath    string // Path to compiled test binary.
	importPath string // Package import path.
	dir        string // Package directory (for running the binary).
}

// BuildTestMap enumerates tests in the given packages, compiles each package's
// test binary once, then runs each test function against the compiled binary
// with coverage. Uses parallel workers.
func BuildTestMap(ctx context.Context, projectDir string, packages []string, coverPkg, tags string, tmpDir string, workers int) (*TestMap, error) {
	// 1. Enumerate all test function names.
	tests, err := listTestsFunc(ctx, projectDir, packages, tags)
	if err != nil {
		return nil, fmt.Errorf("listing tests: %w", err)
	}

	// 2. Resolve package patterns to individual packages and compile test binaries.
	resolvedPkgs, err := resolvePackagesFunc(ctx, projectDir, packages, tags)
	if err != nil {
		return nil, fmt.Errorf("resolving packages: %w", err)
	}

	pkgBins := buildPkgBins(ctx, projectDir, tmpDir, coverPkg, tags, resolvedPkgs)

	// 3. Run tests in parallel using compiled binaries.
	work := make(chan testEntry, len(tests))
	results := make(chan testCoverage, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			processWork(ctx, work, pkgBins, tmpDir, workerID, results)
		}(i)
	}

	// Feed work.
	go func() {
		feedWork(ctx, tests, work)
	}()

	// Close results when all workers are done.
	go func() {
		wg.Wait()
		close(results)
	}()

	// 4. Collect and index results.
	tm := &TestMap{
		index:        make(map[string]map[string]bool),
		durations:    make(map[testKey]time.Duration),
		pkgDurations: make(map[string]time.Duration),
	}
	for tc := range results {
		tm.ingestResult(tc)
	}

	return tm, nil
}

// ingestResult folds one per-test outcome into both the coverage index
// and the duration maps. Extracted so STATEMENT_REMOVE on the duration
// recording is observable without needing a full BuildTestMap pipeline
// to assert on (the receive loop is otherwise invisible to a unit
// test that doesn't drive real `go test` invocations).
func (tm *TestMap) ingestResult(tc testCoverage) {
	tm.addBlocks(tc.testName, tc.blocks)
	tm.recordDuration(tc.pkg, tc.testName, tc.duration)
}

// recordDuration stores a single (pkg, test) timing and updates the
// rolling per-package sum. Extracted so a duplicate observation (same
// test re-run because two packages list the same name into testEntry,
// or future retry logic) accumulates rather than overwrites — matching
// the per-package sum's pre-existing accumulation behavior.
func (tm *TestMap) recordDuration(pkg, name string, d time.Duration) {
	if d <= 0 {
		return
	}
	k := testKey{pkg: pkg, name: name}
	tm.durations[k] += d
	tm.pkgDurations[pkg] += d
}

// addBlocks indexes a test's coverage blocks into the map.
//
// Extracted so the inner line-stepping loop can be unit-tested directly
// with tiny inputs and a tight deadline — calling BuildTestMap to exercise
// it would let mutations like `line++ → line--` allocate gigabytes of
// map entries before any test-side timer can fire.
func (tm *TestMap) addBlocks(testName string, blocks []Block) {
	for _, b := range blocks {
		if b.Count == 0 {
			continue
		}
		for line := b.StartLine; line <= b.EndLine; line++ {
			key := b.File + ":" + fmt.Sprint(line)
			if tm.index[key] == nil {
				tm.index[key] = make(map[string]bool)
			}
			tm.index[key][testName] = true
		}
	}
}

// processWork processes test entries from the work channel.
func processWork(ctx context.Context, work <-chan testEntry, pkgBins map[string]*compiledPkg, tmpDir string, workerID int, results chan<- testCoverage) {
	for test := range work {
		if ctx.Err() != nil {
			return
		}
		cp := pkgBins[test.pkg]
		if cp == nil {
			continue
		}
		profilePath := filepath.Join(tmpDir, fmt.Sprintf("testmap-%d.cov", workerID))
		blocks, dur := runCompiledTestFunc(ctx, cp, test.name, profilePath)
		// Forward the timing even when the test produced no blocks: the
		// mutant covering this test still executes it, so its duration
		// matters for the per-mutant timeout. Without this, a fast unit
		// test that touches no shared coverage line gets a 0 contribution
		// and the package sum understates real wall time.
		if len(blocks) == 0 && dur <= 0 {
			continue
		}
		results <- testCoverage{
			pkg:      test.pkg,
			testName: test.name,
			duration: dur,
			blocks:   blocks,
		}
	}
}

// feedWork sends test entries to the work channel, respecting context cancellation.
func feedWork(ctx context.Context, tests []testEntry, work chan<- testEntry) {
	for _, t := range tests {
		select {
		case work <- t:
		case <-ctx.Done():
			close(work)
			return
		}
	}
	close(work)
}

// buildPkgBins compiles each package's test binary and indexes the results
// by import path. Compile failures (no tests, syntax errors) are skipped
// silently — extracted from BuildTestMap so the skip behavior can be
// tested without driving the whole pipeline.
func buildPkgBins(ctx context.Context, projectDir, tmpDir, coverPkg, tags string, pkgs []resolvedPkg) map[string]*compiledPkg {
	pkgBins := make(map[string]*compiledPkg)
	for _, pkg := range pkgs {
		cp, err := compileTestBinaryFunc(ctx, projectDir, tmpDir, coverPkg, tags, pkg)
		if err != nil {
			// Package may have no tests, fail to compile, or produce no
			// binary; all are non-fatal — skip and keep going.
			continue
		}
		pkgBins[pkg.importPath] = cp
	}
	return pkgBins
}

// compileTestBinary compiles `pkg`'s test binary into tmpDir and returns
// the compiledPkg metadata. Errors from `go test -c` and from a missing
// output file (a package with no tests produces no binary) are folded
// into the returned error so callers can `continue` on a single check.
func compileTestBinary(ctx context.Context, projectDir, tmpDir, coverPkg, tags string, pkg resolvedPkg) (*compiledPkg, error) {
	binPath := filepath.Join(tmpDir, "testbin-"+sanitize(pkg.importPath)+".test")
	args := []string{"test", "-c", "-o", binPath, "-cover"}
	if coverPkg != "" {
		args = append(args, "-coverpkg="+coverPkg)
	}
	if tags != "" {
		args = append(args, tagsBuildFlag+tags)
	}
	args = append(args, pkg.importPath)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectDir
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go test -c %s: %w", pkg.importPath, err)
	}
	if _, err := statFileFunc(binPath); err != nil {
		return nil, fmt.Errorf("test binary missing for %s: %w", pkg.importPath, err)
	}
	return &compiledPkg{
		binPath:    binPath,
		importPath: pkg.importPath,
		dir:        pkg.dir,
	}, nil
}

// runCompiledTest runs a pre-compiled test binary for a single test with
// coverage and reports the wall-clock duration alongside the parsed blocks.
//
// The duration is reported even on parse failure or non-zero exit: the
// per-mutant adaptive timeout uses these timings, and a flaky test that
// happens to fail during the build phase still represents real work the
// runner will do. Timing only the success path would systematically
// understate timeouts for packages with environment-sensitive tests.
//
// Note on context cancellation: if `ctx` is cancelled mid-run (e.g. an
// upstream SIGINT during the coverage build), the returned duration is
// the partial run-time at cancellation, not the full test cost. That
// can under-record the per-test timing for that one entry; the
// adaptive selector's per-package fallback and the global ceiling
// absorb the impact, but be aware that one cancelled coverage build
// can leave behind tighter-than-real timings until the next clean run.
func runCompiledTest(ctx context.Context, cp *compiledPkg, testName, profilePath string) ([]Block, time.Duration) {
	args := []string{
		fmt.Sprintf("-test.run=^%s$", regexp.QuoteMeta(testName)),
		"-test.coverprofile=" + profilePath,
	}

	cmd := exec.CommandContext(ctx, cp.binPath, args...)
	cmd.Dir = cp.dir

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	if runErr != nil {
		return nil, dur
	}

	profile, err := parseFileFunc(profilePath)
	if err != nil {
		return nil, dur
	}
	return profile.blocks, dur
}

// TestsFor returns the test function names that cover the given position.
// Returns nil if no mapping exists (caller should run all tests).
func (tm *TestMap) TestsFor(file string, line int) []string {
	if tm == nil {
		return nil
	}
	key := file + ":" + fmt.Sprint(line)
	testSet := tm.index[key]
	if len(testSet) == 0 {
		return nil
	}
	tests := make([]string, 0, len(testSet))
	for t := range testSet {
		tests = append(tests, t)
	}
	return tests
}

// SumDurationsFor returns the total observed wall-time of `tests` within
// `pkg` (sum of per-test timings recorded during the coverage build),
// and reports whether every requested test had a recorded timing.
//
// Callers use the returned `complete` flag to decide whether the sum
// is trustworthy: if any test was missing (e.g. it failed during the
// coverage build and produced 0 duration, or the test was added after
// build) the per-package fallback should be used instead.
//
// On a nil TestMap, returns (0, false). Empty tests slice short-circuits
// to (0, false) so the per-package fallback is reached without a misread
// of "all 0 of 0 tests had data → complete=true → use 0 timeout".
func (tm *TestMap) SumDurationsFor(pkg string, tests []string) (time.Duration, bool) {
	if tm == nil || len(tests) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, t := range tests {
		d, ok := tm.durations[testKey{pkg: pkg, name: t}]
		if !ok {
			return 0, false
		}
		total += d
	}
	return total, true
}

// PackageDuration returns the sum of per-test durations recorded for
// `pkg` during the coverage build. Used as the per-package fallback
// when a mutant has no resolvable per-test covering set.
//
// Returns 0 for unknown packages or a nil TestMap; callers treat 0 as
// "no per-package signal" and degrade further to the global timeout.
func (tm *TestMap) PackageDuration(pkg string) time.Duration {
	if tm == nil {
		return 0
	}
	return tm.pkgDurations[pkg]
}

// NewTestMapForTesting constructs a TestMap directly from raw timing
// data and a "file:line" → tests cover index. Exposed only because the
// runner-package timeout selector needs to be exercised against
// hand-built fixtures without spinning up `go test` to populate a real
// map. Production code must use BuildTestMap.
//
// perTest: keys are [pkg, name] pairs; the helper aggregates them into
// the internal (pkg, name)→duration map and the rolling per-package
// totals so PackageDuration matches what BuildTestMap would produce.
func NewTestMapForTesting(perTest map[[2]string]time.Duration, coverIndex map[string][]string) *TestMap {
	tm := &TestMap{
		index:        make(map[string]map[string]bool),
		durations:    make(map[testKey]time.Duration),
		pkgDurations: make(map[string]time.Duration),
	}
	for k, d := range perTest {
		tm.recordDuration(k[0], k[1], d)
	}
	for fileLine, names := range coverIndex {
		set := make(map[string]bool, len(names))
		for _, n := range names {
			set[n] = true
		}
		tm.index[fileLine] = set
	}
	return tm
}

// RunPattern returns a -run regex pattern that matches exactly the given tests.
func RunPattern(tests []string) string {
	if len(tests) == 0 {
		return ""
	}
	escaped := make([]string, len(tests))
	for i, t := range tests {
		escaped[i] = regexp.QuoteMeta(t)
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

type testEntry struct {
	name string
	pkg  string
}

func listTests(ctx context.Context, projectDir string, packages []string, tags string) ([]testEntry, error) {
	var allTests []testEntry

	for _, pkg := range packages {
		args := []string{"test", "-list", "."}
		if tags != "" {
			args = append(args, tagsBuildFlag+tags)
		}
		args = append(args, pkg)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = projectDir

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("go test -list for %s: %w\n%s", pkg, err, stderr.String())
		}

		allTests = append(allTests, parseListTestsOutput(&stdout, pkg)...)
	}

	return allTests, nil
}

// parseListTestsOutput parses the stdout of `go test -list .`. Each non-empty,
// non-"ok"-prefixed line is one test name. Extracted so the line-filter can
// be exercised directly from a string reader — driving it through listTests
// would require a real `go test` invocation.
func parseListTestsOutput(r io.Reader, pkg string) []testEntry {
	var tests []testEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "ok") {
			continue
		}
		tests = append(tests, testEntry{name: line, pkg: pkg})
	}
	return tests
}

type resolvedPkg struct {
	importPath string
	dir        string
}

func resolvePackages(ctx context.Context, projectDir string, patterns []string, tags string) ([]resolvedPkg, error) {
	args := []string{"list", "-f", "{{.ImportPath}}\t{{.Dir}}"}
	if tags != "" {
		args = append(args, tagsBuildFlag+tags)
	}
	args = append(args, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}

	var pkgs []resolvedPkg
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) == 2 {
			pkgs = append(pkgs, resolvedPkg{importPath: parts[0], dir: parts[1]})
		}
	}
	return pkgs, nil
}

// sanitize makes a test name safe for use as a filename.
func sanitize(s string) string {
	return strings.NewReplacer("/", "_", " ", "_", "\\", "_", ".", "_").Replace(s)
}
