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
	groups := map[string][]string{
		"m/zoo":  {"T"},
		"m/calc": {"T"}, // own
		"m/app":  {"T"},
	}
	got := orderRoutePackages(groups, "m/calc")
	want := []string{"m/calc", "m/app", "m/zoo"}
	if !slices.Equal(got, want) {
		t.Errorf("orderRoutePackages = %v, want %v", got, want)
	}

	// Own package absent from the groups → just the sorted rest.
	got = orderRoutePackages(map[string][]string{"m/app": {"T"}, "m/zoo": {"T"}}, "m/calc")
	want = []string{"m/app", "m/zoo"}
	if !slices.Equal(got, want) {
		t.Errorf("orderRoutePackages (own absent) = %v, want %v", got, want)
	}
}
