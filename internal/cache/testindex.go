package cache

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// TestIndex resolves test function names to the absolute paths of the
// _test.go files where they are declared, and lists every _test.go file in
// each known package directory. It is built once per run from a parse of
// every test file in scope.
//
// Cross-package coverage (tests in package B exercising code in package A
// via -coverpkg) means the per-test coverage map can return test names
// whose defining files live outside the mutant's own package directory.
// FilesFor surfaces those, so tests_hash includes every covering test
// file regardless of which package declared it.
type TestIndex struct {
	byName map[string][]string // testName → abs paths of declaring _test.go files
	byDir  map[string][]string // pkgDir → abs paths of every _test.go in dir
}

// BuildTestIndex parses each _test.go file in pkgDirs and indexes
// top-level test/benchmark/example/fuzz function declarations.
//
// Behavior on errors:
//   - A directory that fails to read is skipped (no entries indexed).
//   - A file that fails to parse is recorded in byDir (so fallback hashing
//     still includes it) but contributes no byName entries — its tests are
//     unknown, so any mutant resolving through it falls back to the
//     directory-wide list.
//
// Names that exist in multiple packages map to all declaring files; the
// per-test coverage map collapses package context, so we treat a same-
// named test as potentially covering through any of those files.
func BuildTestIndex(pkgDirs []string) *TestIndex {
	ti := &TestIndex{
		byName: make(map[string][]string),
		byDir:  make(map[string][]string),
	}
	seen := make(map[string]bool)
	for _, dir := range pkgDirs {
		abs, err := filepath.Abs(dir)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true

		entries, err := os.ReadDir(abs)
		if err != nil {
			continue
		}

		var dirFiles []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, "_test.go") {
				continue
			}
			absPath := filepath.Join(abs, name)
			dirFiles = append(dirFiles, absPath)

			fset := token.NewFileSet()
			// SkipObjectResolution: we only need top-level FuncDecl names,
			// no need for the (slow) identifier-resolution pass.
			f, perr := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
			if perr != nil {
				continue
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				if isTestEntryName(fn.Name.Name) {
					ti.byName[fn.Name.Name] = append(ti.byName[fn.Name.Name], absPath)
				}
			}
		}
		if len(dirFiles) > 0 {
			ti.byDir[abs] = dirFiles
		}
	}
	return ti
}

// isTestEntryName reports whether name is a top-level test/benchmark/
// example/fuzz function declaration as recognized by `go test`. We don't
// validate the receiver/parameters — the goal is "did the user touch a
// file that contributes a test entry," not strict conformance.
func isTestEntryName(name string) bool {
	switch {
	case strings.HasPrefix(name, "Test"):
		return true
	case strings.HasPrefix(name, "Benchmark"):
		return true
	case strings.HasPrefix(name, "Example"):
		return true
	case strings.HasPrefix(name, "Fuzz"):
		return true
	}
	return false
}

// FilesFor returns the test files declaring testName, or nil if the name
// is unknown. Multiple files are returned when the same name was declared
// in more than one indexed package.
func (ti *TestIndex) FilesFor(testName string) []string {
	if ti == nil {
		return nil
	}
	return ti.byName[testName]
}

// AllInDir returns every _test.go file in pkgDir (absolute paths). Used
// as the conservative fallback when the per-test coverage map is
// unavailable or has no entry for a mutant.
func (ti *TestIndex) AllInDir(pkgDir string) []string {
	if ti == nil {
		return nil
	}
	return ti.byDir[pkgDir]
}
