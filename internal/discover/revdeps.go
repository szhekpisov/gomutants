package discover

import (
	"context"
	"sort"
	"strings"
)

// resolvePackagesFn indirects ResolvePackages so IntegrationClosure can be
// unit-tested against canned package sets without shelling out to `go list`
// (the real go-list path is covered by ResolvePackages's own tests and the
// end-to-end integration acceptance test).
var resolvePackagesFn = ResolvePackages

// IntegrationClosure computes the reverse-dependency closure of the target
// packages: R = targets ∪ {module-local packages whose imports or test
// imports transitively reach a target}. Only R's tests can possibly kill a
// mutant in a target package, so only R needs its test binaries built and its
// tests run for the cross-package per-test coverage map.
//
// It returns the R import paths (usable directly as `go test` arguments),
// their directories, and the -coverpkg value (the comma-joined target import
// paths) so that tests in importing packages record coverage on the mutated
// target code.
//
// moduleName scopes the import graph to the current module; stdlib and
// external dependencies are never part of the closure.
func IntegrationClosure(ctx context.Context, dir string, targetImportPaths []string, moduleName, tags string) (rPatterns, rDirs []string, coverPkg string, err error) {
	// Reuse ResolvePackages so integration mode shares the single, already
	// vetted `go list` invocation rather than adding another exec call site.
	pkgs, err := resolvePackagesFn(ctx, dir, []string{moduleName + "/..."}, tags)
	if err != nil {
		return nil, nil, "", err
	}

	fwd, dirs := buildImportGraph(pkgs, moduleName)
	r := reverseClosure(targetImportPaths, fwd)
	return r, dirsFor(r, dirs), strings.Join(targetImportPaths, ","), nil
}

// buildImportGraph indexes the listed packages into a module-local forward
// import graph (package → the in-module packages its production OR test code
// imports) and an import-path → directory lookup. Pure so the indexing — and
// the test-import inclusion it depends on — is unit-testable without `go list`.
func buildImportGraph(pkgs []Package, moduleName string) (fwd map[string][]string, dirs map[string]string) {
	fwd = make(map[string][]string, len(pkgs))
	dirs = make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		dirs[p.ImportPath] = p.Dir
		fwd[p.ImportPath] = moduleLocalImports(p, moduleName)
	}
	return fwd, dirs
}

// dirsFor maps import paths to their directories in order, skipping any not
// present in the lookup (a target outside the listed module set has no dir).
func dirsFor(importPaths []string, dirs map[string]string) []string {
	out := make([]string, 0, len(importPaths))
	for _, ip := range importPaths {
		if d, ok := dirs[ip]; ok {
			out = append(out, d)
		}
	}
	return out
}

// moduleLocalImports returns the union of a package's regular, in-test, and
// external-test imports, keeping only those within moduleName. Deduplicated
// so a package imported from both production and test code appears once.
func moduleLocalImports(p Package, moduleName string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, group := range [][]string{p.Imports, p.TestImports, p.XTestImports} {
		for _, imp := range group {
			if !inModule(imp, moduleName) || seen[imp] {
				continue
			}
			seen[imp] = true
			out = append(out, imp)
		}
	}
	return out
}

// inModule reports whether import path imp belongs to module moduleName —
// either the module root package itself or a subpackage. The "/" guard stops
// a sibling module sharing a name prefix (e.g. "example.com/foobar" vs
// "example.com/foo") from being treated as local.
func inModule(imp, moduleName string) bool {
	return imp == moduleName || strings.HasPrefix(imp, moduleName+"/")
}

// reverseClosure returns the set of packages that can reach any target by
// following forward import edges — i.e. the targets plus every (transitive)
// importer of a target — as a sorted, deduplicated slice. fwd maps a package
// to the module-local packages it imports (production + test).
//
// It inverts fwd into an importer graph and runs a BFS seeded with the
// targets. Pure (no I/O) so the traversal is unit-testable against hand-built
// graphs, including the test-import edge that the feature hinges on.
func reverseClosure(targets []string, fwd map[string][]string) []string {
	rev := make(map[string][]string)
	for pkg, deps := range fwd {
		for _, dep := range deps {
			rev[dep] = append(rev[dep], pkg)
		}
	}

	visited := make(map[string]bool, len(targets))
	var queue []string
	for _, t := range targets {
		if !visited[t] {
			visited[t] = true
			queue = append(queue, t)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, importer := range rev[cur] {
			if !visited[importer] {
				visited[importer] = true
				queue = append(queue, importer)
			}
		}
	}

	out := make([]string, 0, len(visited))
	for pkg := range visited {
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}
