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

	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/mutator"
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
		return mutantLess(mutants[pending[a]], mutants[pending[b]])
	})

	work := make(chan int, len(pending))
	results := make(chan mutator.Mutant, len(pending))

	workers := p.createWorkers()

	// If no worker could be created, abort cleanly rather than deadlocking
	// on a feeder blocked forever sending into `work` with no readers.
	if len(workers) == 0 {
		fmt.Fprintln(os.Stderr, "gomutants: no workers could be started; skipping mutation run")
		return mutants
	}

	var wg sync.WaitGroup
	for _, w := range workers {
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

// createWorkers spins up p.workers workers, logs and skips ones that fail
// to construct, and applies the per-worker GOMAXPROCS cap. Extracted from
// Run so the loop can be unit-tested directly: with a stub
// newWorkerFunc, the test sees how many workers were created and what
// childGOMAXPROCS each ended up with — neither of which is observable
// through the pool's mutant return value.
func (p *Pool) createWorkers() []*Worker {
	workers := make([]*Worker, 0, p.workers)
	cap := childGOMAXPROCSFor(p.workers)
	for i := range p.workers {
		w, err := newWorkerFunc(i, p.tmpDir, p.timeout, p.srcCache, p.projectDir, p.testMap)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gomutants: NewWorker %d failed: %v\n", i, err)
			continue
		}
		w.childGOMAXPROCS = cap
		w.testCPU = p.testCPU
		workers = append(workers, w)
	}
	return workers
}

// newWorkerFunc is the constructor used by createWorkers; swappable for tests.
var newWorkerFunc = NewWorker

// mutantLess orders mutants by (Pkg, File, StartOffset) using paired `<`/`>`
// comparisons so each `<` mutation flips behavior on the equality path. A
// guarded `if a != b { return a < b }` would render `<` ↔ `<=` mutations
// equivalent because the guard rules out the only case where the boundary
// matters.
func mutantLess(a, b mutator.Mutant) bool {
	if a.Pkg < b.Pkg {
		return true
	}
	if a.Pkg > b.Pkg {
		return false
	}
	if a.File < b.File {
		return true
	}
	if a.File > b.File {
		return false
	}
	return a.StartOffset < b.StartOffset
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
