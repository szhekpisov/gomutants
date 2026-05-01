package cache

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestBuildTestIndex_IndexesTestFunctions(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a_test.go"), `package x

import "testing"

func TestAlpha(t *testing.T)        {}
func TestBeta(t *testing.T)         {}
func BenchmarkGamma(b *testing.B)   {}
func ExampleDelta()                  {}
func FuzzEpsilon(f *testing.F)      {}
func helperNotATest()                {}
`)
	mustWrite(t, filepath.Join(dir, "x.go"), `package x
func Foo() {}
`)

	ti := BuildTestIndex([]string{dir})

	for _, name := range []string{"TestAlpha", "TestBeta", "BenchmarkGamma", "ExampleDelta", "FuzzEpsilon"} {
		got := ti.FilesFor(name)
		if len(got) != 1 {
			t.Errorf("FilesFor(%q) = %v, want 1 file", name, got)
		}
	}
	if got := ti.FilesFor("helperNotATest"); got != nil {
		t.Errorf("non-test function indexed: %v", got)
	}
	if got := ti.FilesFor("Foo"); got != nil {
		t.Errorf("production function indexed: %v", got)
	}

	all := ti.AllInDir(dir)
	if len(all) != 1 || filepath.Base(all[0]) != "a_test.go" {
		t.Errorf("AllInDir(dir) = %v, want [a_test.go]", all)
	}
}

// TestBuildTestIndex_CrossPackageNameCollision asserts that the same
// test name declared in two packages maps to both files. This is the
// case the per-mutant tests_hash needs to handle: an edit to either
// declaring file must invalidate the cache entry.
func TestBuildTestIndex_CrossPackageNameCollision(t *testing.T) {
	root := t.TempDir()
	pkgA := filepath.Join(root, "a")
	pkgB := filepath.Join(root, "b")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pkgA, "x_test.go"), "package a\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n")
	mustWrite(t, filepath.Join(pkgB, "x_test.go"), "package b\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n")

	ti := BuildTestIndex([]string{pkgA, pkgB})
	got := ti.FilesFor("TestSame")
	if len(got) != 2 {
		t.Fatalf("FilesFor(TestSame) = %v, want 2 entries", got)
	}
	sort.Strings(got)
	wantA := filepath.Join(pkgA, "x_test.go")
	wantB := filepath.Join(pkgB, "x_test.go")
	if got[0] != wantA || got[1] != wantB {
		t.Errorf("FilesFor(TestSame) = %v, want [%s %s]", got, wantA, wantB)
	}
}

// TestBuildTestIndex_MalformedFileRecordedInDirOnly asserts that a
// _test.go that fails to parse still appears in AllInDir — the
// fallback hashing path must include it — but contributes no byName
// entries. Mutants resolving through it then fall back to the
// directory-wide list, which correctly captures the malformed file.
func TestBuildTestIndex_MalformedFileRecordedInDirOnly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good_test.go")
	bad := filepath.Join(dir, "bad_test.go")
	mustWrite(t, good, "package x\nimport \"testing\"\nfunc TestOK(t *testing.T) {}\n")
	mustWrite(t, bad, "package x\nfunc {{{")

	ti := BuildTestIndex([]string{dir})

	if got := ti.FilesFor("TestOK"); len(got) != 1 || got[0] != good {
		t.Errorf("FilesFor(TestOK) = %v, want [%s]", got, good)
	}
	all := ti.AllInDir(dir)
	if len(all) != 2 {
		t.Errorf("AllInDir = %v, want both files (incl. malformed)", all)
	}
}

func TestBuildTestIndex_NilSafe(t *testing.T) {
	var ti *TestIndex
	if got := ti.FilesFor("anything"); got != nil {
		t.Errorf("nil index FilesFor returned %v", got)
	}
	if got := ti.AllInDir("/nope"); got != nil {
		t.Errorf("nil index AllInDir returned %v", got)
	}
}
