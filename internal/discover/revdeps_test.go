package discover

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

// writeTempModule writes the given file map (relative paths → contents) into a
// fresh temp module directory and returns it. Shared by the closure tests so
// the module-scaffolding boilerplate lives in one place.
func writeTempModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestIntegrationClosure drives the full closure through a real `go list` on a
// synthesized module, exercising all three import kinds: app imports calc in
// production (Imports), e2e from an in-package test (TestImports), and ext from
// an external (`*_test` package) test (XTestImports). util imports none. The
// closure of {calc} must pull in app, e2e, and ext, but not util.
func TestIntegrationClosure(t *testing.T) {
	dir := writeTempModule(t, map[string]string{
		"go.mod":            "module testmod\n\ngo 1.26\n",
		"calc/calc.go":      "package calc\n\nfunc Add(a, b int) int { return a + b }\n",
		"app/app.go":        "package app\n\nimport \"testmod/calc\"\n\nfunc Total() int { return calc.Add(1, 2) }\n",
		"e2e/e2e.go":        "package e2e\n",
		"e2e/e2e_test.go":   "package e2e\n\nimport (\n\t\"testing\"\n\n\t\"testmod/calc\"\n)\n\nfunc TestE2E(t *testing.T) { _ = calc.Add(1, 1) }\n",
		"ext/ext.go":        "package ext\n",
		"ext/ext_x_test.go": "package ext_test\n\nimport (\n\t\"testing\"\n\n\t\"testmod/calc\"\n)\n\nfunc TestExt(t *testing.T) { _ = calc.Add(2, 2) }\n",
		"util/util.go":      "package util\n\nfunc U() int { return 0 }\n",
	})

	rPatterns, rDirs, coverPkg, err := IntegrationClosure(context.Background(), dir, []string{"testmod/calc"}, "testmod", "")
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
	for _, d := range rDirs {
		if !strings.HasPrefix(d, dir) {
			t.Errorf("rDir %q is outside the module dir %q", d, dir)
		}
	}
}

// TestIntegrationClosureForwardsTags pins the `-tags` forwarding: the `tagged`
// package imports calc only from a file behind `//go:build mytag`, so it enters
// calc's closure only when the tag is forwarded. A base file keeps the package
// valid (and out of the closure) without the tag, so `go list` never errors.
func TestIntegrationClosureForwardsTags(t *testing.T) {
	dir := writeTempModule(t, map[string]string{
		"go.mod":           "module testmod\n\ngo 1.26\n",
		"calc/calc.go":     "package calc\n\nfunc Add(a, b int) int { return a + b }\n",
		"tagged/base.go":   "package tagged\n",
		"tagged/tagged.go": "//go:build mytag\n\npackage tagged\n\nimport \"testmod/calc\"\n\nvar _ = calc.Add\n",
	})

	// Without the tag, tagged's calc import is build-excluded → not in closure.
	r, _, _, err := IntegrationClosure(context.Background(), dir, []string{"testmod/calc"}, "testmod", "")
	if err != nil {
		t.Fatalf("IntegrationClosure (no tag): %v", err)
	}
	if slices.Contains(r, "testmod/tagged") {
		t.Errorf("without tag, tagged must not be in closure: %v", r)
	}

	// With tags=mytag, the import is active → tagged enters the closure.
	r, _, _, err = IntegrationClosure(context.Background(), dir, []string{"testmod/calc"}, "testmod", "mytag")
	if err != nil {
		t.Fatalf("IntegrationClosure (tag=mytag): %v", err)
	}
	if !slices.Contains(r, "testmod/tagged") {
		t.Errorf("with tag=mytag, tagged must be in closure: %v", r)
	}
}

// TestIntegrationClosureErrorPropagates pins the `go list` failure path: a
// non-existent working directory makes the command fail to start, and the
// error must surface rather than being swallowed into an empty closure.
func TestIntegrationClosureErrorPropagates(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, _, _, err := IntegrationClosure(context.Background(), missing, []string{"nomod/calc"}, "nomod", "")
	if err == nil {
		t.Fatal("expected an error when `go list` fails, got nil")
	}
}
