package discover

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestReverseClosure_TestImportEdgeIncluded(t *testing.T) {
	// app's *test* imports calc; util is unrelated. The closure of {calc}
	// must include app (it can kill a calc mutant) but not util.
	const (
		calc = "m/calc"
		app  = "m/app"
		util = "m/util"
	)
	fwd := map[string][]string{
		calc: nil,
		app:  {calc}, // edge present only because app's test imports calc
		util: nil,
	}
	got := reverseClosure([]string{calc}, fwd)
	want := []string{app, calc}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverseClosure = %v, want %v", got, want)
	}
}

func TestReverseClosure_Transitive(t *testing.T) {
	// e2e -> app -> calc. Closure of {calc} pulls in both importers.
	const (
		calc = "m/calc"
		app  = "m/app"
		e2e  = "m/e2e"
		zoo  = "m/zoo"
	)
	fwd := map[string][]string{
		calc: nil,
		app:  {calc},
		e2e:  {app},
		zoo:  {util()}, // imports something outside the chain
	}
	got := reverseClosure([]string{calc}, fwd)
	want := []string{app, calc, e2e}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverseClosure = %v, want %v", got, want)
	}
}

func util() string { return "m/util" }

func TestReverseClosure_NoImporters(t *testing.T) {
	const calc = "m/calc"
	fwd := map[string][]string{calc: nil, "m/other": nil}
	got := reverseClosure([]string{calc}, fwd)
	want := []string{calc}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverseClosure = %v, want %v", got, want)
	}
}

func TestReverseClosure_MultipleTargetsDeduped(t *testing.T) {
	const (
		a   = "m/a"
		b   = "m/b"
		app = "m/app"
	)
	fwd := map[string][]string{
		a:   nil,
		b:   nil,
		app: {a, b}, // imports both targets — must appear once
	}
	got := reverseClosure([]string{a, b}, fwd)
	want := []string{a, app, b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverseClosure = %v, want %v", got, want)
	}
}

func TestReverseClosure_CycleTerminates(t *testing.T) {
	// Defensive: a forward cycle must not loop forever.
	const (
		calc = "m/calc"
		a    = "m/a"
		b    = "m/b"
	)
	fwd := map[string][]string{
		calc: nil,
		a:    {calc, b},
		b:    {a},
	}
	got := reverseClosure([]string{calc}, fwd)
	want := []string{a, b, calc}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverseClosure = %v, want %v", got, want)
	}
}

func TestModuleLocalImports_UnionAndFilter(t *testing.T) {
	const mod = "example.com/m"
	p := Package{
		ImportPath:   mod + "/app",
		Imports:      []string{mod + "/calc", "fmt"},
		TestImports:  []string{mod + "/calc", mod + "/testutil", "testing"},
		XTestImports: []string{mod + "/fixtures"},
	}
	got := moduleLocalImports(p, mod)
	want := []string{mod + "/calc", mod + "/testutil", mod + "/fixtures"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("moduleLocalImports = %v, want %v", got, want)
	}
}

func TestInModule(t *testing.T) {
	const mod = "example.com/foo"
	cases := []struct {
		imp  string
		want bool
	}{
		{mod, true},
		{mod + "/bar", true},
		{"example.com/foobar", false}, // prefix collision must not match
		{"fmt", false},
		{"example.com/foo2", false},
	}
	for _, c := range cases {
		if got := inModule(c.imp, mod); got != c.want {
			t.Errorf("inModule(%q, %q) = %v, want %v", c.imp, mod, got, c.want)
		}
	}
}

func TestBuildImportGraph(t *testing.T) {
	const mod = "m"
	pkgs := []Package{
		{ImportPath: mod + "/calc", Dir: "/calc", Imports: []string{"fmt"}},
		{ImportPath: mod + "/app", Dir: "/app", Imports: []string{mod + "/calc", "fmt"}, TestImports: []string{"testing"}},
		{ImportPath: mod + "/e2e", Dir: "/e2e", XTestImports: []string{mod + "/calc"}},
	}
	fwd, dirs := buildImportGraph(pkgs, mod)

	// Production import kept, stdlib filtered out.
	if got := fwd[mod+"/app"]; !reflect.DeepEqual(got, []string{mod + "/calc"}) {
		t.Errorf("fwd[app] = %v, want [m/calc] (fmt/testing filtered)", got)
	}
	// External-test import edge present — the load-bearing case.
	if got := fwd[mod+"/e2e"]; !reflect.DeepEqual(got, []string{mod + "/calc"}) {
		t.Errorf("fwd[e2e] = %v, want [m/calc] (XTestImports edge)", got)
	}
	// A package importing only stdlib has no module-local edges.
	if got := fwd[mod+"/calc"]; len(got) != 0 {
		t.Errorf("fwd[calc] = %v, want empty", got)
	}
	want := map[string]string{mod + "/calc": "/calc", mod + "/app": "/app", mod + "/e2e": "/e2e"}
	if !reflect.DeepEqual(dirs, want) {
		t.Errorf("dirs = %v, want %v", dirs, want)
	}
}

func TestDirsFor(t *testing.T) {
	dirs := map[string]string{"m/a": "/a", "m/b": "/b"}
	got := dirsFor([]string{"m/a", "m/missing", "m/b"}, dirs)
	want := []string{"/a", "/b"} // missing import path skipped, order preserved
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dirsFor = %v, want %v", got, want)
	}
}

// stubResolvePackages swaps resolvePackagesFn for the duration of a test,
// capturing the arguments it was called with and returning canned packages.
func stubResolvePackages(t *testing.T, pkgs []Package, retErr error) *struct {
	dir      string
	patterns []string
	tags     string
} {
	t.Helper()
	captured := &struct {
		dir      string
		patterns []string
		tags     string
	}{}
	orig := resolvePackagesFn
	t.Cleanup(func() { resolvePackagesFn = orig })
	resolvePackagesFn = func(_ context.Context, dir string, patterns []string, tags string) ([]Package, error) {
		captured.dir, captured.patterns, captured.tags = dir, patterns, tags
		return pkgs, retErr
	}
	return captured
}

// TestIntegrationClosure drives the closure against a canned package set
// exercising all three import kinds: app imports calc in production (Imports),
// e2e from an in-package test (TestImports), and ext from an external test
// (XTestImports). util imports none. The closure of {calc} must pull in app,
// e2e, and ext, but not util — and it must query `go list` for `<module>/...`.
func TestIntegrationClosure(t *testing.T) {
	captured := stubResolvePackages(t, []Package{
		{ImportPath: "testmod/calc", Dir: "/m/calc"},
		{ImportPath: "testmod/app", Dir: "/m/app", Imports: []string{"testmod/calc"}},
		{ImportPath: "testmod/e2e", Dir: "/m/e2e", TestImports: []string{"testmod/calc"}},
		{ImportPath: "testmod/ext", Dir: "/m/ext", XTestImports: []string{"testmod/calc"}},
		{ImportPath: "testmod/util", Dir: "/m/util"},
	}, nil)

	rPatterns, rDirs, coverPkg, err := IntegrationClosure(context.Background(), "/m", []string{"testmod/calc"}, "testmod", "tag1")
	if err != nil {
		t.Fatalf("IntegrationClosure: %v", err)
	}

	for _, want := range []string{"testmod/calc", "testmod/app", "testmod/e2e", "testmod/ext"} {
		if !slices.Contains(rPatterns, want) {
			t.Errorf("closure %v missing %q", rPatterns, want)
		}
	}
	if slices.Contains(rPatterns, "testmod/util") {
		t.Errorf("closure %v must not include util (it does not import calc)", rPatterns)
	}
	if coverPkg != "testmod/calc" {
		t.Errorf("coverPkg = %q, want testmod/calc", coverPkg)
	}
	if len(rDirs) != len(rPatterns) {
		t.Errorf("rDirs (%d) and rPatterns (%d) length mismatch", len(rDirs), len(rPatterns))
	}

	// The closure must enumerate the whole module and forward the tags.
	if !reflect.DeepEqual(captured.patterns, []string{"testmod/..."}) {
		t.Errorf("go list patterns = %v, want [testmod/...]", captured.patterns)
	}
	if captured.dir != "/m" || captured.tags != "tag1" {
		t.Errorf("go list dir/tags = %q/%q, want /m/tag1", captured.dir, captured.tags)
	}
}

// TestIntegrationClosureErrorPropagates pins the `go list` failure path: a
// ResolvePackages error must surface rather than being swallowed into an
// empty closure.
func TestIntegrationClosureErrorPropagates(t *testing.T) {
	stubResolvePackages(t, nil, errors.New("go list boom"))
	_, _, _, err := IntegrationClosure(context.Background(), "/m", []string{"testmod/calc"}, "testmod", "")
	if err == nil {
		t.Fatal("expected an error when ResolvePackages fails, got nil")
	}
}
