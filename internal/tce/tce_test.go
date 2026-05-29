package tce

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

func TestNormalizeAsm_StripsHeaderAndTrailingWhitespace(t *testing.T) {
	// The `# importpath` header and trailing whitespace are incidental and
	// must not affect the hash; the instruction lines must.
	base := normalizeAsm([]byte("ADD R1,R0\nMOV x,y\n"))
	withHeaderAndWS := normalizeAsm([]byte("# github.com/foo/bar\nADD R1,R0  \nMOV x,y\t\n"))
	if base != withHeaderAndWS {
		t.Errorf("header / trailing whitespace changed the hash:\n base=%s\n  hw=%s", base, withHeaderAndWS)
	}

	// A real instruction difference must change the hash.
	diff := normalizeAsm([]byte("SUB R1,R0\nMOV x,y\n"))
	if base == diff {
		t.Error("ADD vs SUB produced the same hash; normalizeAsm is destroying signal")
	}
}

func TestBuildArgs(t *testing.T) {
	const ip = "example.com/m/pkg"

	ref := buildArgs(ip, "", "")
	want := []string{"build", "-o", os.DevNull, "-gcflags=" + ip + "=-S", ip}
	if !equalArgs(ref, want) {
		t.Errorf("reference args = %v, want %v", ref, want)
	}

	mut := buildArgs(ip, "/tmp/ov.json", "integration")
	wantMut := []string{"build", "-o", os.DevNull, "-gcflags=" + ip + "=-S", "-overlay=/tmp/ov.json", "-tags=integration", ip}
	if !equalArgs(mut, wantMut) {
		t.Errorf("mutant args = %v, want %v", mut, wantMut)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeTempModule writes a one-package module whose `Zero` function the
// compiler folds (x + 0 == x - 0) and whose `Sum` it does not. Returns the
// module dir, the absolute source path, and its bytes.
func writeTempModule(t *testing.T) (dir, srcAbs string, src []byte) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tcetest\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src = []byte("package tcetest\n\nfunc Zero(x int) int { return x + 0 }\n\nfunc Sum(a, b int) int { return a + b }\n")
	srcAbs = filepath.Join(dir, "m.go")
	if err := os.WriteFile(srcAbs, src, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, srcAbs, src
}

// plusMutant builds a Mutant that replaces the `+` inside `marker` with
// `-`, locating it by the byte offset of marker in src.
func plusMutant(t *testing.T, srcAbs string, src []byte, marker string) mutator.Mutant {
	t.Helper()
	i := bytes.Index(src, []byte(marker))
	if i < 0 {
		t.Fatalf("marker %q not found in source", marker)
	}
	plus := i + bytes.IndexByte(src[i:i+len(marker)], '+')
	if src[plus] != '+' {
		t.Fatalf("expected '+' at offset %d, got %q", plus, src[plus])
	}
	return mutator.Mutant{
		Type:        mutator.ArithmeticBase,
		File:        srcAbs,
		Pkg:         "tcetest",
		Original:    "+",
		Replacement: "-",
		StartOffset: plus,
		EndOffset:   plus + 1,
		Status:      mutator.StatusLived,
	}
}

func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
}

func TestDetector_Check_EquivalentVsKillable(t *testing.T) {
	requireGo(t)
	dir, srcAbs, src := writeTempModule(t)
	d := NewDetector(dir, "", map[string][]byte{srcAbs: src})

	tmpSrc := filepath.Join(t.TempDir(), "w.go")
	overlay := filepath.Join(t.TempDir(), "ov.json")

	// x + 0 → x - 0 : the compiler folds both to x, so the assembly is
	// identical → EQUIVALENT.
	eq, err := d.Check(context.Background(), plusMutant(t, srcAbs, src, "x + 0"), tmpSrc, overlay)
	if err != nil {
		t.Fatalf("Check(equivalent): %v", err)
	}
	if !eq {
		t.Error("x+0 → x-0 should be detected as equivalent")
	}

	// a + b → a - b : a real behavior change → assembly differs → NOT
	// equivalent. This is the critical soundness assertion.
	eq, err = d.Check(context.Background(), plusMutant(t, srcAbs, src, "a + b"), tmpSrc, overlay)
	if err != nil {
		t.Fatalf("Check(killable): %v", err)
	}
	if eq {
		t.Error("a+b → a-b must NOT be equivalent (would hide a real test gap)")
	}
}

func TestDetector_Run_FlipsOnlyEquivalentSurvivors(t *testing.T) {
	requireGo(t)
	dir, srcAbs, src := writeTempModule(t)
	d := NewDetector(dir, "", map[string][]byte{srcAbs: src})

	mutants := []mutator.Mutant{
		plusMutant(t, srcAbs, src, "x + 0"), // equivalent → flip
		plusMutant(t, srcAbs, src, "a + b"), // killable → stay LIVED
	}
	// A non-LIVED mutant must be ignored by the pass entirely.
	killed := plusMutant(t, srcAbs, src, "x + 0")
	killed.Status = mutator.StatusKilled
	mutants = append(mutants, killed)

	d.Run(context.Background(), mutants, 2, t.TempDir(), nil)

	if mutants[0].Status != mutator.StatusEquivalent {
		t.Errorf("equivalent survivor not flipped: got %s", mutants[0].Status)
	}
	if mutants[1].Status != mutator.StatusLived {
		t.Errorf("killable survivor wrongly changed: got %s", mutants[1].Status)
	}
	if mutants[2].Status != mutator.StatusKilled {
		t.Errorf("non-LIVED mutant must be untouched: got %s", mutants[2].Status)
	}
}

// --- error paths (no real compile needed; the cheap prep runs first) ---

func TestCheck_SourceNotCached(t *testing.T) {
	d := NewDetector(t.TempDir(), "", map[string][]byte{}) // empty cache
	m := mutator.Mutant{File: "/nope.go", Pkg: "x", StartOffset: 0, EndOffset: 1, Replacement: "-"}
	if _, err := d.Check(context.Background(), m, "", ""); err == nil {
		t.Error("expected error when source is not cached")
	}
}

func TestCheck_PatchOutOfRange(t *testing.T) {
	src := []byte("package p\n")
	d := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	m := mutator.Mutant{File: "/p.go", Pkg: "x", StartOffset: 0, EndOffset: 9999, Replacement: "-"}
	if _, err := d.Check(context.Background(), m, filepath.Join(t.TempDir(), "w.go"), ""); err == nil {
		t.Error("expected error for out-of-range patch offsets")
	}
}

func TestCheck_WriteAndMarshalFailures(t *testing.T) {
	src := []byte("package p\n\nvar X = 1\n")
	d := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	m := mutator.Mutant{File: "/p.go", Pkg: "x", StartOffset: 18, EndOffset: 19, Replacement: "2"}
	tmp := filepath.Join(t.TempDir(), "w.go")
	ov := filepath.Join(t.TempDir(), "ov.json")

	t.Run("tmp source write fails", func(t *testing.T) {
		defer swapWriteFile(failingWrite)()
		if _, err := d.Check(context.Background(), m, tmp, ov); err == nil {
			t.Error("expected error from tmp-source write failure")
		}
	})
	t.Run("overlay marshal fails", func(t *testing.T) {
		defer swapMarshal(func(any) ([]byte, error) { return nil, errInjected })()
		if _, err := d.Check(context.Background(), m, tmp, ov); err == nil {
			t.Error("expected error from overlay marshal failure")
		}
	})
}

func TestCheck_ReferenceCompileError(t *testing.T) {
	src := []byte("package p\n\nvar X = 1\n")
	d := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	m := mutator.Mutant{File: "/p.go", Pkg: "x", StartOffset: 18, EndOffset: 19, Replacement: "2", Status: mutator.StatusLived}
	defer swapExec(failingExec)()

	// Check surfaces the compile error (covers referenceHash + compileHash
	// error branches), and Run leaves such a survivor LIVED.
	if _, err := d.Check(context.Background(), m, filepath.Join(t.TempDir(), "w.go"), filepath.Join(t.TempDir(), "ov.json")); err == nil {
		t.Error("expected compile error to propagate")
	}
	d2 := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	ms := []mutator.Mutant{m}
	d2.Run(context.Background(), ms, 1, t.TempDir(), nil)
	if ms[0].Status != mutator.StatusLived {
		t.Errorf("survivor must stay LIVED when the TCE compile errors, got %s", ms[0].Status)
	}
}

// TestCheck_MutantCompileError covers the compileHash error branch:
// the reference is pre-seeded so referenceHash succeeds, then the mutant
// compile (stubbed) fails.
func TestCheck_MutantCompileError(t *testing.T) {
	src := []byte("package p\n\nvar X = 1\n")
	d := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	seedRef(d, "x", "deadbeef")
	defer swapExec(failingExec)()
	m := mutator.Mutant{File: "/p.go", Pkg: "x", StartOffset: 18, EndOffset: 19, Replacement: "2"}
	if _, err := d.Check(context.Background(), m, filepath.Join(t.TempDir(), "w.go"), filepath.Join(t.TempDir(), "ov.json")); err == nil {
		t.Error("expected mutant compile error to propagate")
	}
}

func TestRun_NoSurvivors(t *testing.T) {
	d := NewDetector(t.TempDir(), "", map[string][]byte{})
	// No LIVED mutants → early return, no compile, no panic.
	d.Run(context.Background(), []mutator.Mutant{{Status: mutator.StatusKilled}}, 0, t.TempDir(), nil)
}

func TestRun_CancelledContext(t *testing.T) {
	src := []byte("package p\n\nvar X = 1\n")
	d := NewDetector(t.TempDir(), "", map[string][]byte{"/p.go": src})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled → goroutine hits the ctx.Err() guard and returns
	ms := []mutator.Mutant{{File: "/p.go", Pkg: "x", StartOffset: 18, EndOffset: 19, Replacement: "2", Status: mutator.StatusLived}}
	d.Run(ctx, ms, 1, t.TempDir(), nil)
	if ms[0].Status != mutator.StatusLived {
		t.Errorf("cancelled run must leave status LIVED, got %s", ms[0].Status)
	}
}

func TestRun_CallbackAndDefaultWorkers(t *testing.T) {
	requireGo(t)
	dir, srcAbs, src := writeTempModule(t)
	dg := NewDetector(dir, "", map[string][]byte{srcAbs: src})
	ms := []mutator.Mutant{plusMutant(t, srcAbs, src, "x + 0")}
	var got int
	// workers=0 exercises the `workers < 1 → 1` guard.
	dg.Run(context.Background(), ms, 0, t.TempDir(), func(mutator.Mutant) { got++ })
	if got != 1 {
		t.Errorf("onResult called %d times, want 1", got)
	}
	if ms[0].Status != mutator.StatusEquivalent {
		t.Errorf("equivalent survivor not flipped under default workers: got %s", ms[0].Status)
	}
}

func TestMutantLess_AllDimensions(t *testing.T) {
	mk := func(pkg, file string, off int) mutator.Mutant {
		return mutator.Mutant{Pkg: pkg, File: file, StartOffset: off}
	}
	cases := []struct {
		name string
		a, b mutator.Mutant
		want bool
	}{
		{"pkg<", mk("a", "f", 0), mk("b", "f", 0), true},
		{"pkg>", mk("b", "f", 0), mk("a", "f", 0), false},
		{"file<", mk("a", "f1", 0), mk("a", "f2", 0), true},
		{"file>", mk("a", "f2", 0), mk("a", "f1", 0), false},
		{"offset<", mk("a", "f", 1), mk("a", "f", 2), true},
		{"offset>=", mk("a", "f", 2), mk("a", "f", 2), false},
	}
	for _, tc := range cases {
		if got := mutantLess(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: mutantLess = %v, want %v", tc.name, got, tc.want)
		}
	}
}

var errInjected = errorString("injected")

type errorString string

func (e errorString) Error() string { return string(e) }

func failingWrite(string, []byte, os.FileMode) error { return errInjected }

func failingExec(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "false") // exits non-zero → cmd.Run errors
}

func swapWriteFile(f func(string, []byte, os.FileMode) error) func() {
	orig := writeFileFunc
	writeFileFunc = f
	return func() { writeFileFunc = orig }
}

func swapMarshal(f func(any) ([]byte, error)) func() {
	orig := marshalFunc
	marshalFunc = f
	return func() { marshalFunc = orig }
}

func swapExec(f func(context.Context, string, ...string) *exec.Cmd) func() {
	orig := execCommandContext
	execCommandContext = f
	return func() { execCommandContext = orig }
}

// seedRef marks a package's reference hash as already computed, so
// referenceHash returns it without compiling.
func seedRef(d *Detector, pkg, hash string) {
	r := &refResult{}
	r.once.Do(func() { r.hash = hash })
	d.refs[pkg] = r
}
