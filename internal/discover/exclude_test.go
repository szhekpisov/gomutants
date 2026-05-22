package discover

import (
	"reflect"
	"testing"
)

func TestNewExcluderEmptyReturnsNil(t *testing.T) {
	for _, specs := range [][]string{nil, {}, {"", "  ", "\t"}} {
		// The CLI path trims blanks before this point, but a YAML list can
		// carry empty or whitespace-only entries; they must not produce a
		// live (always-false) pattern.
		e, err := NewExcluder(specs)
		if err != nil {
			t.Fatalf("specs %q: unexpected error %v", specs, err)
		}
		if e != nil {
			t.Errorf("specs %q: want nil Excluder, got %+v", specs, e)
		}
	}
}

func TestNewExcluderSkipsBlanksKeepsLater(t *testing.T) {
	// A blank entry must be skipped (continue), not stop the loop (break):
	// the valid pattern after it has to survive.
	e, err := NewExcluder([]string{"", "vendor/"})
	if err != nil {
		t.Fatal(err)
	}
	if e == nil || len(e.patterns) != 1 {
		t.Fatalf("want 1 pattern after a leading blank, got %+v", e)
	}
	if !e.Match("x/vendor/y.go") {
		t.Error("pattern after blank must still match")
	}
}

func TestNewExcluderInvalidPattern(t *testing.T) {
	e, err := NewExcluder([]string{"valid", "([unclosed"})
	if err == nil {
		t.Fatal("want error for invalid regexp, got nil")
	}
	if e != nil {
		t.Errorf("want nil Excluder on error, got %+v", e)
	}
}

func TestNewExcluderValid(t *testing.T) {
	e, err := NewExcluder([]string{`vendor/`, `_gen\.go$`})
	if err != nil {
		t.Fatal(err)
	}
	if e == nil {
		t.Fatal("want non-nil Excluder")
	}
	if len(e.patterns) != 2 {
		t.Errorf("want 2 compiled patterns, got %d", len(e.patterns))
	}
}

func TestMatchNilReceiver(t *testing.T) {
	var e *Excluder
	if e.Match("anything.go") {
		t.Error("nil Excluder must never match")
	}
}

func TestMatch(t *testing.T) {
	e, err := NewExcluder([]string{`vendor/`, `_gen\.go$`})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want bool
	}{
		{"internal/foo/vendor/x.go", true}, // unanchored: hits mid-path
		{"pkg/types_gen.go", true},         // anchored $ on second pattern
		{"pkg/types_gen.go.bak", false},    // $ prevents trailing-text match
		{"internal/foo/bar.go", false},     // no pattern matches
	}
	for _, tt := range tests {
		if got := e.Match(tt.path); got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestApplyExcludesNilExcluder(t *testing.T) {
	pkgs := []Package{{Dir: "/m/pkg", GoFiles: []string{"a.go", "b.go"}}}
	got, n := ApplyExcludes(pkgs, nil, "/m")
	if n != 0 {
		t.Errorf("want 0 excluded, got %d", n)
	}
	if !reflect.DeepEqual(got, pkgs) {
		t.Errorf("nil Excluder must return pkgs unchanged, got %+v", got)
	}
}

func TestApplyExcludes(t *testing.T) {
	e, err := NewExcluder([]string{`pkg/b\.go$`, `gen/`})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := []Package{
		{
			Dir:         "/m/pkg",
			GoFiles:     []string{"a.go", "b.go"}, // b.go excluded, a.go kept
			TestGoFiles: []string{"b_test.go"},    // never touched
		},
		{
			Dir:     "/m/gen",
			GoFiles: []string{"x.go", "y.go"}, // both under gen/: package emptied, dropped
		},
		{
			Dir:         "/m/empty",
			GoFiles:     nil, // already production-empty: preserved
			TestGoFiles: []string{"z_test.go"},
		},
	}
	got, n := ApplyExcludes(pkgs, e, "/m")
	if n != 3 {
		t.Errorf("want 3 excluded files, got %d", n)
	}
	// The emptied /m/gen package is dropped; /m/pkg and /m/empty remain.
	if len(got) != 2 {
		t.Fatalf("want 2 packages after dropping the emptied one, got %d (%+v)", len(got), got)
	}
	if got[0].Dir != "/m/pkg" || !reflect.DeepEqual(got[0].GoFiles, []string{"a.go"}) {
		t.Errorf("got[0] = %+v, want /m/pkg with GoFiles [a.go]", got[0])
	}
	if !reflect.DeepEqual(got[0].TestGoFiles, []string{"b_test.go"}) {
		t.Errorf("test files must be untouched, got %v", got[0].TestGoFiles)
	}
	// An already-empty package is kept, not mistaken for an emptied one.
	if got[1].Dir != "/m/empty" {
		t.Errorf("got[1].Dir = %q, want /m/empty (already-empty package preserved)", got[1].Dir)
	}
	if !reflect.DeepEqual(got[1].TestGoFiles, []string{"z_test.go"}) {
		t.Errorf("got[1] test files = %v, want [z_test.go]", got[1].TestGoFiles)
	}
	// Input must not be mutated in place.
	if len(pkgs[1].GoFiles) != 2 {
		t.Errorf("ApplyExcludes mutated its input: pkg[1] now %v", pkgs[1].GoFiles)
	}
}

func TestExcludeRelPath(t *testing.T) {
	got := excludeRelPath("/m", "/m/internal/foo", "bar.go")
	if got != "internal/foo/bar.go" {
		t.Errorf("got %q, want internal/foo/bar.go", got)
	}
}

func TestExcludeRelPathFallback(t *testing.T) {
	// A relative moduleRoot against an absolute file path makes filepath.Rel
	// fail; the fallback returns the slash-form absolute path so a pattern
	// still gets a shot at matching.
	got := excludeRelPath("rel/root", "/abs/dir", "f.go")
	if got != "/abs/dir/f.go" {
		t.Errorf("got %q, want /abs/dir/f.go", got)
	}
}
