package runner

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/mutator"
)

// routeMap builds a TestMap whose index routes one position to the given
// (pkg, name) references.
func routeMap(fileLine string, refs ...coverage.TestRef) *coverage.TestMap {
	return coverage.NewTestMapForTesting(nil, map[string][]coverage.TestRef{
		fileLine: refs,
	})
}

func lastArg(args []string) string { return args[len(args)-1] }

func runArg(args []string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "-run=") {
			return a, true
		}
	}
	return "", false
}

// Same-package routing yields exactly one invocation against the mutant's
// own package — byte-identical to the pre-integration single-package path.
func TestTestInvocationsSinglePackage(t *testing.T) {
	const calc = "m/calc"
	w := &Worker{testMap: routeMap("f.go:1", coverage.TestRef{Pkg: calc, Name: "TestA"})}
	m := mutator.Mutant{Pkg: calc, CoverageFile: "f.go", Line: 1}

	invs := w.testInvocations(m, false, time.Second)
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1: %v", len(invs), invs)
	}
	if got := lastArg(invs[0]); got != calc {
		t.Errorf("package arg = %q, want %q", got, calc)
	}
	if r, ok := runArg(invs[0]); !ok || r != "-run=^(TestA)$" {
		t.Errorf("-run arg = %q (present=%v), want -run=^(TestA)$", r, ok)
	}
}

// Cross-package routing yields one invocation per covering package, the
// mutant's own package first, each filtered to that package's tests.
func TestTestInvocationsCrossPackageOrdersOwnFirst(t *testing.T) {
	const (
		calc = "m/calc"
		app  = "m/app"
	)
	w := &Worker{testMap: routeMap("f.go:1",
		coverage.TestRef{Pkg: app, Name: "TestApp"},
		coverage.TestRef{Pkg: calc, Name: "TestCalc"},
	)}
	m := mutator.Mutant{Pkg: calc, CoverageFile: "f.go", Line: 1}

	invs := w.testInvocations(m, false, time.Second)
	if len(invs) != 2 {
		t.Fatalf("got %d invocations, want 2: %v", len(invs), invs)
	}
	// Own package (calc) must run first.
	if got := lastArg(invs[0]); got != calc {
		t.Errorf("first invocation package = %q, want %q (own package first)", got, calc)
	}
	if got := lastArg(invs[1]); got != app {
		t.Errorf("second invocation package = %q, want %q", got, app)
	}
	if r, _ := runArg(invs[0]); r != "-run=^(TestCalc)$" {
		t.Errorf("calc invocation -run = %q, want -run=^(TestCalc)$", r)
	}
	if r, _ := runArg(invs[1]); r != "-run=^(TestApp)$" {
		t.Errorf("app invocation -run = %q, want -run=^(TestApp)$", r)
	}
}

// A mutant whose own package has no covering test but an importer does is
// routed to the importer's package — never to its own.
func TestTestInvocationsForeignPackageOnly(t *testing.T) {
	const (
		calc = "m/calc"
		app  = "m/app"
	)
	w := &Worker{testMap: routeMap("f.go:1", coverage.TestRef{Pkg: app, Name: "TestApp"})}
	m := mutator.Mutant{Pkg: calc, CoverageFile: "f.go", Line: 1}

	invs := w.testInvocations(m, false, time.Second)
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1: %v", len(invs), invs)
	}
	if got := lastArg(invs[0]); got != app {
		t.Errorf("package arg = %q, want %q (importer, not own package)", got, app)
	}
}

// No routing info → a single invocation against the mutant's package with no
// -run filter (run the whole package).
func TestTestInvocationsNoRouting(t *testing.T) {
	const calc = "m/calc"
	w := &Worker{testMap: routeMap("other.go:9", coverage.TestRef{Pkg: calc, Name: "TestA"})}
	m := mutator.Mutant{Pkg: calc, CoverageFile: "f.go", Line: 1} // no index entry

	invs := w.testInvocations(m, false, time.Second)
	if len(invs) != 1 {
		t.Fatalf("got %d invocations, want 1: %v", len(invs), invs)
	}
	if got := lastArg(invs[0]); got != calc {
		t.Errorf("package arg = %q, want %q", got, calc)
	}
	if r, ok := runArg(invs[0]); ok {
		t.Errorf("unexpected -run filter %q; whole package should run", r)
	}
}

func TestOrderRoutePackages(t *testing.T) {
	// Go randomizes map iteration order, so a dropped slices.Sort surfaces as
	// a non-sorted result only on some iterations. Using several keys and
	// looping makes the "rest must be sorted" assertion reliably catch it
	// (the odds of the unsorted result accidentally matching every iteration
	// are vanishing).
	groups := map[string][]string{
		"m/calc": {"T"}, // own — must come first regardless of order
		"m/zoo":  {"T"},
		"m/app":  {"T"},
		"m/qux":  {"T"},
		"m/bar":  {"T"},
	}
	want := []string{"m/calc", "m/app", "m/bar", "m/qux", "m/zoo"}
	for range 50 {
		if got := orderRoutePackages(groups, "m/calc"); !slices.Equal(got, want) {
			t.Fatalf("orderRoutePackages = %v, want %v", got, want)
		}
	}

	// Own package absent from the groups → just the sorted rest.
	wantRest := []string{"m/app", "m/bar", "m/zoo"}
	for range 50 {
		got := orderRoutePackages(map[string][]string{"m/zoo": {"T"}, "m/app": {"T"}, "m/bar": {"T"}}, "m/calc")
		if !slices.Equal(got, wantRest) {
			t.Fatalf("orderRoutePackages (own absent) = %v, want %v", got, wantRest)
		}
	}
}
