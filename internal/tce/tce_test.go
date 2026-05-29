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
