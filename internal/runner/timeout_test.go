package runner

import (
	"testing"
	"time"

	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/mutator"
)

// newTestMapWithDurations builds a TestMap fixture from raw timing data,
// using only exported APIs that mutate state through public records. We
// use coverage.NewTestMapForTesting (added below) to avoid exporting
// internal map shape from the coverage package.
//
// Falling back to the BuildTestMap pipeline would require spinning up
// real `go test` invocations, which is the opposite of what a unit test
// for the timeout selector should do.
func newTestMapWithDurations(t *testing.T, perTest map[[2]string]time.Duration, coverIndex map[string][]string) *coverage.TestMap {
	t.Helper()
	tm := coverage.NewTestMapForTesting(perTest, coverIndex)
	return tm
}

func TestTimeoutPolicyForAdaptiveDisabled(t *testing.T) {
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: time.Second, Adaptive: false}
	got := p.For(nil, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	if got != 30*time.Second {
		t.Errorf("Adaptive=false should return Global; got %v", got)
	}
}

// Adaptive=false must short-circuit even when the TestMap has timing
// data that would otherwise drive a much shorter per-mutant timeout.
// Drives the Adaptive=false branch with a populated map; if the early
// `if !p.Adaptive` return is elided (BRANCH_IF), the function falls
// through and returns the Min-clamped 1s instead of Global 30s.
func TestTimeoutPolicyForAdaptiveDisabledIgnoresTestMap(t *testing.T) {
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestA"}: 100 * time.Millisecond,
		},
		map[string][]string{
			"f.go:1": {"TestA"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: time.Second, Adaptive: false}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	if got != 30*time.Second {
		t.Errorf("Adaptive=false must ignore TestMap and return Global; got %v", got)
	}
}

func TestTimeoutPolicyForUsesPerTestSum(t *testing.T) {
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestA"}: 100 * time.Millisecond,
			{"p", "TestB"}: 200 * time.Millisecond,
		},
		map[string][]string{
			"f.go:10": {"TestA", "TestB"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: time.Second, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 10})
	want := 900 * time.Millisecond // (100ms+200ms) * 3
	// 900ms is below the 1s floor → clamps to 1s.
	if got != time.Second {
		t.Errorf("got %v; want 1s (per-test sum 300ms × 3 = 900ms, clamped to Min 1s) — got base %v", got, want)
	}
}

func TestTimeoutPolicyForFloorClampsBelowMin(t *testing.T) {
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestA"}: 5 * time.Millisecond,
		},
		map[string][]string{
			"f.go:1": {"TestA"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: 2 * time.Second, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	if got != 2*time.Second {
		t.Errorf("scaled 15ms must clamp up to Min 2s; got %v", got)
	}
}

func TestTimeoutPolicyForCeilingClampsAboveGlobal(t *testing.T) {
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestSlow"}: 60 * time.Second,
		},
		map[string][]string{
			"f.go:1": {"TestSlow"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: time.Second, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	if got != 30*time.Second {
		t.Errorf("scaled 180s must clamp down to Global 30s; got %v", got)
	}
}

func TestTimeoutPolicyForFallsBackToPackageWhenPerTestMissing(t *testing.T) {
	// The covering set lists TestUnseen, which has no recorded duration.
	// Selector must fall back to PackageDuration("p").
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestA"}: 50 * time.Millisecond,
			{"p", "TestB"}: 50 * time.Millisecond,
		},
		map[string][]string{
			"f.go:1": {"TestUnseen"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 4, Min: 0, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	want := 400 * time.Millisecond // pkg sum 100ms × 4
	if got != want {
		t.Errorf("missing per-test entry must trigger pkg fallback; got %v want %v", got, want)
	}
}

func TestTimeoutPolicyForFallsBackToGlobalWhenNoData(t *testing.T) {
	tm := newTestMapWithDurations(t, nil, nil)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 3, Min: time.Second, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "unknown", CoverageFile: "f.go", Line: 1})
	if got != 30*time.Second {
		t.Errorf("no data → Global; got %v", got)
	}
}

func TestTimeoutPolicyForNoCoveringSetUsesPackage(t *testing.T) {
	// No entry in the cover index for the mutant's location → TestsFor
	// returns nil → SumDurationsFor returns (0, false) → package fallback.
	tm := newTestMapWithDurations(t,
		map[[2]string]time.Duration{
			{"p", "TestA"}: 250 * time.Millisecond,
		},
		map[string][]string{
			"other.go:5": {"TestA"},
		},
	)
	p := TimeoutPolicy{Global: 30 * time.Second, Margin: 2, Min: 0, Adaptive: true}
	got := p.For(tm, mutator.Mutant{Pkg: "p", CoverageFile: "f.go", Line: 1})
	want := 500 * time.Millisecond // pkg sum 250ms × 2
	if got != want {
		t.Errorf("uncovered line must use pkg fallback; got %v want %v", got, want)
	}
}
