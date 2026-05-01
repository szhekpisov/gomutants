package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cachepkg "github.com/szhekpisov/gomutants/internal/cache"
	"github.com/szhekpisov/gomutants/internal/report"
)

// TestIncrementalCacheColdThenWarm runs the simple testdata twice with
// --cache and asserts the second run reuses every prior outcome.
func TestIncrementalCacheColdThenWarm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := setupTestdataCopy(t, "testdata/simple")

	cachePath := filepath.Join(dir, ".gomutants-cache.json")
	reportPath := filepath.Join(dir, "report.json")

	cold := runInDir(t, dir, []string{
		"-w", "4",
		"-cache", cachePath,
		"-o", reportPath,
		"./...",
	})

	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Sanity: cache contains the expected number of entries
	// (every cacheable terminal status — KILLED, LIVED, NOT_VIABLE,
	// TIMED_OUT — but not NOT_COVERED).
	cacheable := cold.MutantsKilled + cold.MutantsLived + cold.MutantsNotViable +
		(cold.MutantsTotal - cold.MutantsKilled - cold.MutantsLived - cold.MutantsNotCovered - cold.MutantsNotViable)
	loaded := loadCacheFile(t, cachePath)
	if len(loaded.Entries) != cacheable {
		t.Errorf("cache entries=%d, want %d (cacheable terminal statuses)", len(loaded.Entries), cacheable)
	}

	// Warm run: same args, same testdata. Every cacheable mutant should be reused.
	warm := runInDir(t, dir, []string{
		"-w", "4",
		"-cache", cachePath,
		"-o", reportPath,
		"./...",
	})

	if warm.MutantsTotal != cold.MutantsTotal {
		t.Errorf("warm total=%d, want %d", warm.MutantsTotal, cold.MutantsTotal)
	}
	if warm.MutantsKilled != cold.MutantsKilled {
		t.Errorf("warm killed=%d, want %d", warm.MutantsKilled, cold.MutantsKilled)
	}
	if warm.MutantsLived != cold.MutantsLived {
		t.Errorf("warm lived=%d, want %d", warm.MutantsLived, cold.MutantsLived)
	}
	if warm.MutantsCached != cacheable {
		t.Errorf("warm cached=%d, want %d (every cacheable mutant)", warm.MutantsCached, cacheable)
	}
}

// TestIncrementalCacheInvalidatesPerturbedProdFile rewrites a production
// file between runs and asserts the cache invalidates only that file's
// mutants while reusing the rest.
func TestIncrementalCacheInvalidatesPerturbedProdFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := setupTestdataCopy(t, "testdata/simple")
	cachePath := filepath.Join(dir, ".gomutants-cache.json")
	reportPath := filepath.Join(dir, "report.json")

	// Cold run.
	runInDir(t, dir, []string{"-w", "4", "-cache", cachePath, "-o", reportPath, "./..."})

	priorEntries := len(loadCacheFile(t, cachePath).Entries)

	// Append a no-op comment to math.go — every mutant in math.go must
	// invalidate (prod_hash mismatch). There are no other prod files,
	// so this should drop cached count to 0 on the warm run.
	mathPath := filepath.Join(dir, "math.go")
	body, err := os.ReadFile(mathPath)
	if err != nil {
		t.Fatalf("read math.go: %v", err)
	}
	body = append(body, []byte("\n// touched\n")...)
	if err := os.WriteFile(mathPath, body, 0o644); err != nil {
		t.Fatalf("write math.go: %v", err)
	}

	warm := runInDir(t, dir, []string{"-w", "4", "-cache", cachePath, "-o", reportPath, "./..."})

	if warm.MutantsCached != 0 {
		t.Errorf("warm cached=%d, want 0 (every mutant in math.go must invalidate after edit)", warm.MutantsCached)
	}

	// After the warm run, the cache should still hold roughly the same
	// number of entries (recomputed) — the new prod_hash overwrites the
	// stale ones.
	updatedEntries := len(loadCacheFile(t, cachePath).Entries)
	if updatedEntries == 0 {
		t.Error("cache empty after warm run — Update should have repopulated it")
	}
	// Sanity: not strictly equal because new line counts shift mutant
	// positions, but it should be in the same ballpark.
	if updatedEntries < priorEntries/2 {
		t.Errorf("cache shrank dramatically: prior=%d updated=%d", priorEntries, updatedEntries)
	}
}

// TestIncrementalCacheInvalidatesPerturbedTestFile touches a test file
// and asserts that mutants whose status depended on test content
// (KILLED, LIVED) are invalidated, while NOT_VIABLE / TIMED_OUT
// (which depend only on prod) remain cached.
func TestIncrementalCacheInvalidatesPerturbedTestFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := setupTestdataCopy(t, "testdata/simple")
	cachePath := filepath.Join(dir, ".gomutants-cache.json")
	reportPath := filepath.Join(dir, "report.json")

	cold := runInDir(t, dir, []string{"-w", "4", "-cache", cachePath, "-o", reportPath, "./..."})

	// Touch math_test.go — appending whitespace changes the file hash
	// without changing test semantics.
	testPath := filepath.Join(dir, "math_test.go")
	body, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatalf("read math_test.go: %v", err)
	}
	body = append(body, []byte("\n")...)
	if err := os.WriteFile(testPath, body, 0o644); err != nil {
		t.Fatalf("write math_test.go: %v", err)
	}

	warm := runInDir(t, dir, []string{"-w", "4", "-cache", cachePath, "-o", reportPath, "./..."})

	// KILLED + LIVED entries must invalidate (their tests_hash changed).
	// NOT_VIABLE entries must stay cached (test content is irrelevant).
	// TIMED_OUT entries (if any) must also stay cached.
	killedAndLived := cold.MutantsKilled + cold.MutantsLived
	if warm.MutantsCached >= killedAndLived+cold.MutantsNotViable {
		// MutantsCached should be < total cacheable — at least the
		// killed+lived ones must drop.
		t.Errorf("warm cached=%d, expected < %d (killed+lived must invalidate)",
			warm.MutantsCached, killedAndLived+cold.MutantsNotViable)
	}
	if warm.MutantsCached < cold.MutantsNotViable {
		t.Errorf("warm cached=%d, want >= %d (NOT_VIABLE must stay cached)",
			warm.MutantsCached, cold.MutantsNotViable)
	}
}

// runInDir invokes run() with args, chdir'd into dir for the duration
// of the call. Returns the parsed JSON report.
func runInDir(t *testing.T, dir string, args []string) *report.Report {
	t.Helper()
	t.Chdir(dir)

	if err := run(context.Background(), args); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Find the -o argument to read the report.
	var outPath string
	for i, a := range args {
		if (a == "-o" || a == "-output") && i+1 < len(args) {
			outPath = args[i+1]
			break
		}
	}
	if outPath == "" {
		t.Fatalf("no -o flag in args: %v", args)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read report %s: %v", outPath, err)
	}
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	return &r
}

// setupTestdataCopy copies srcRel's files into a tempdir (one level deep)
// alongside a synthesized go.mod so that the dir is a self-contained
// module rooted at the temp directory. Returns the absolute tempdir.
func setupTestdataCopy(t *testing.T, srcRel string) string {
	t.Helper()
	dst := t.TempDir()

	srcAbs, err := filepath.Abs(srcRel)
	if err != nil {
		t.Fatalf("abs %s: %v", srcRel, err)
	}
	entries, err := os.ReadDir(srcAbs)
	if err != nil {
		t.Fatalf("read testdata %s: %v", srcAbs, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(srcAbs, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	// Synthesize a minimal go.mod so the tempdir is its own module.
	gomod := "module example.com/simple\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dst, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dst
}

func loadCacheFile(t *testing.T, path string) *cachepkg.Cache {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var c cachepkg.Cache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	return &c
}
