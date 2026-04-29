package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/szhekpisov/gomutant/internal/coverage"
	"github.com/szhekpisov/gomutant/internal/mutator"
)

// ResultCallback is called for each completed mutant.
type ResultCallback func(m mutator.Mutant)

// Pool coordinates parallel mutation testing.
type Pool struct {
	workers    int
	testCPU    int
	timeout    time.Duration
	tmpDir     string
	srcCache   map[string][]byte
	projectDir string
	testMap    *coverage.TestMap
}

// childGOMAXPROCSFor returns the GOMAXPROCS cap for each child `go test`
// when running with the given outer worker count. Goal: spread NumCPU cores
// across workers so the host doesn't oversubscribe (50 threads on a 10-core
// machine when each worker spawns a parallel-compiling subprocess).
// Returns 0 (= inherit) when there's only one worker — no contention to fix.
func childGOMAXPROCSFor(workers int) int {
	if workers <= 1 {
		return 0
	}
	return max(1, runtime.NumCPU()/workers)
}

// NewPool creates a worker pool. testCPU == 0 means "don't pass -cpu to the
// inner go test" (let go test default to GOMAXPROCS).
func NewPool(workers, testCPU int, timeout time.Duration, tmpDir string, srcCache map[string][]byte, projectDir string, testMap *coverage.TestMap) *Pool {
	return &Pool{
		workers:    workers,
		testCPU:    testCPU,
		timeout:    timeout,
		tmpDir:     tmpDir,
		srcCache:   srcCache,
		projectDir: projectDir,
		testMap:    testMap,
	}
}

// Run tests all pending mutants in parallel, calling onResult for each completion.
func (p *Pool) Run(ctx context.Context, mutants []mutator.Mutant, onResult ResultCallback) []mutator.Mutant {
	// Filter to only pending mutants.
	var pending []int
	for i, m := range mutants {
		if m.Status == mutator.StatusPending {
			pending = append(pending, i)
		}
	}

	if len(pending) == 0 {
		return mutants
	}

	// EXP4: sort pending mutants by (Pkg, File, StartOffset) so consecutive
	// mutants on the work channel target the same package + file. The first
	// mutant in a package pays the cold compile; subsequent ones in that
	// package hit the build cache for deps + stdlib.
	sort.SliceStable(pending, func(a, b int) bool {
		ma, mb := mutants[pending[a]], mutants[pending[b]]
		if ma.Pkg != mb.Pkg {
			return ma.Pkg < mb.Pkg
		}
		if ma.File != mb.File {
			return ma.File < mb.File
		}
		return ma.StartOffset < mb.StartOffset
	})

	work := make(chan int, len(pending))
	results := make(chan mutator.Mutant, len(pending))

	var wg sync.WaitGroup

	// Start workers.
	workersStarted := 0
	for i := range p.workers {
		w, err := NewWorker(i, p.tmpDir, p.timeout, p.srcCache, p.projectDir, p.testMap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gomutant: NewWorker %d failed: %v\n", i, err)
			continue
		}
		w.childGOMAXPROCS = childGOMAXPROCSFor(p.workers)
		w.testCPU = p.testCPU
		workersStarted++
		wg.Add(1)
		go func(w *Worker) {
			defer wg.Done()
			for idx := range work {
				if ctx.Err() != nil {
					return
				}
				result := w.Test(ctx, mutants[idx])
				results <- result
			}
		}(w)
	}

	// If no worker could be created, abort cleanly rather than deadlocking
	// on a feeder blocked forever sending into `work` with no readers.
	if workersStarted == 0 {
		fmt.Fprintln(os.Stderr, "gomutant: no workers could be started; skipping mutation run")
		return mutants
	}

	// Feed work.
	go func() {
		defer close(work)
		for _, idx := range pending {
			select {
			case work <- idx:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Collect results.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Build ID → index lookup so we don't rely on IDs being a dense
	// 1-based contiguous range. Future filter steps that drop mutants
	// before Pool won't silently corrupt results with an off-by-one.
	idToIdx := make(map[int]int, len(mutants))
	for i, m := range mutants {
		idToIdx[m.ID] = i
	}

	for result := range results {
		if idx, ok := idToIdx[result.ID]; ok {
			mutants[idx] = result
		}
		if onResult != nil {
			onResult(result)
		}
	}

	return mutants
}

// MeasureBaseline runs the test suite once to determine baseline duration.
func MeasureBaseline(ctx context.Context, projectDir string, packages []string) (time.Duration, error) {
	args := append([]string{"test", "-count=1"}, packages...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectDir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = nil

	start := time.Now()
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("baseline test failed: %w\n%s", err, stderr.String())
	}
	return time.Since(start), nil
}

// RunCoverage runs go test with coverage and returns the profile path.
func RunCoverage(ctx context.Context, projectDir string, packages []string, coverPkg string, tmpDir string) (string, error) {
	profilePath := tmpDir + "/coverage.out"

	args := []string{"test", "-count=1", "-coverprofile=" + profilePath}
	if coverPkg != "" {
		args = append(args, "-coverpkg="+coverPkg)
	}
	args = append(args, packages...)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = projectDir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stderr // Show test output.

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("coverage run failed: %w\n%s", err, stderr.String())
	}

	return profilePath, nil
}
