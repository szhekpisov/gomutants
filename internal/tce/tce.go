// Package tce implements Trivial Compiler Equivalence detection: a
// surviving (LIVED) mutant is provably equivalent to the original when
// the Go compiler produces identical generated code for the mutated and
// the original source. Such a mutant can never change behavior, so it can
// never be killed by any test — it's noise, not a test gap.
//
// The check compiles the package twice with assembly emission scoped to
// that package (`go build -gcflags=<importpath>=-S`), once for the
// original (the reference) and once for the mutant (via `-overlay`), and
// compares the normalized assembly. Identical assembly ⇒ identical
// machine code ⇒ provably equivalent.
//
// Soundness: the failure mode is one-sided. Any real difference in the
// generated code (including data symbols and string/numeric constants,
// which `-S` dumps) diverges the hash, so a killable mutant is never
// declared equivalent. A truly-equivalent mutant whose assembly happens to
// differ is simply left LIVED (under-detection, the safe direction).
//
// The verdict is meaningful only for the host build configuration
// (toolchain, GOOS/GOARCH/GOEXPERIMENT) — the same one the tests run
// under — which is why the cache gates equivalence reuse on the Go
// toolchain.
package tce

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"

	"github.com/szhekpisov/gomutants/internal/mutator"
	"github.com/szhekpisov/gomutants/internal/patch"
)

// execCommandContext, writeFileFunc, and marshalFunc are package-level
// indirections to exec.CommandContext, os.WriteFile, and json.Marshal so
// tests can drive the subprocess and I/O failure paths without contriving
// filesystem or PATH state. Mirrors the runner/cache packages.
var (
	execCommandContext = exec.CommandContext
	writeFileFunc      = os.WriteFile
	marshalFunc        = json.Marshal
)

// Detector memoizes per-package reference assembly hashes and checks
// mutants for compiler equivalence. Safe for concurrent use: the
// per-package reference memo is guarded by mu, and each Check writes to
// caller-owned temp files.
type Detector struct {
	projectDir string
	tags       string            // forwarded as -tags= (empty omits)
	srcCache   map[string][]byte // shared, read-only production sources

	mu   sync.Mutex
	refs map[string]*refResult // import path -> memoized reference hash
}

// refResult memoizes one package's reference assembly hash. once ensures
// the reference is compiled exactly once even under concurrent Checks.
type refResult struct {
	once sync.Once
	hash string
	err  error
}

// NewDetector creates a Detector. srcCache is the in-memory production
// source map (absolute path -> bytes) reused from the run; tags mirrors
// the run's --tags so build-constrained packages compile identically to
// how they were tested.
func NewDetector(projectDir, tags string, srcCache map[string][]byte) *Detector {
	return &Detector{
		projectDir: projectDir,
		tags:       tags,
		srcCache:   srcCache,
		refs:       make(map[string]*refResult),
	}
}

// referenceHash returns the normalized reference-assembly hash for
// importPath, compiling the original (unmutated) package once and
// memoizing the result. A sticky error means we never retry a package
// whose reference build fails (e.g. cgo / hand-written .s) — its
// survivors are simply left LIVED.
func (d *Detector) referenceHash(ctx context.Context, importPath string) (string, error) {
	d.mu.Lock()
	r := d.refs[importPath]
	if r == nil {
		r = &refResult{}
		d.refs[importPath] = r
	}
	d.mu.Unlock()

	r.once.Do(func() {
		r.hash, r.err = d.compileHash(ctx, importPath, "")
	})
	return r.hash, r.err
}

// Check reports whether mutant m is equivalent to the original. It applies
// m's byte patch to tmpSrcPath, writes an overlay mapping the original
// source path to that temp file, compiles the package with the same -S
// flag plus -overlay, and compares the normalized assembly to the package
// reference. tmpSrcPath and overlayPath are the caller's stable temp files
// (one set per worker goroutine) so concurrent Checks don't collide.
func (d *Detector) Check(ctx context.Context, m mutator.Mutant, tmpSrcPath, overlayPath string) (bool, error) {
	// Cheap local prep first, so a malformed mutant never pays for the
	// (memoized) reference compile.
	original, ok := d.srcCache[m.File]
	if !ok {
		return false, fmt.Errorf("tce: source not cached: %s", m.File)
	}
	patched, err := patch.Apply(original, m.StartOffset, m.EndOffset, m.Replacement)
	if err != nil {
		return false, err
	}
	if err := writeFileFunc(tmpSrcPath, patched, 0o644); err != nil {
		return false, err
	}
	if err := writeOverlay(overlayPath, m.File, tmpSrcPath); err != nil {
		return false, err
	}
	ref, err := d.referenceHash(ctx, m.Pkg)
	if err != nil {
		return false, err
	}
	h, err := d.compileHash(ctx, m.Pkg, overlayPath)
	if err != nil {
		return false, err
	}
	return h == ref, nil
}

// compileHash runs `go build` with package-scoped `-S` (plus -overlay when
// overlayPath is non-empty) and returns the normalized assembly hash.
func (d *Detector) compileHash(ctx context.Context, importPath, overlayPath string) (string, error) {
	cmd := execCommandContext(ctx, "go", buildArgs(importPath, overlayPath, d.tags)...)
	cmd.Dir = d.projectDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// gomutants:disable-next-line STATEMENT_REMOVE reason="a nil cmd.Stdout is already connected to the null device by os/exec, and `go build -S` emits assembly to stderr (stdout stays empty), so dropping this discard is observably identical"
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tce: go build -S %s: %w\n%s", importPath, err, stderr.String())
	}
	return normalizeAsm(stderr.Bytes()), nil
}

// buildArgs assembles the `go build` argv. `-S` is emitted to stderr and
// scoped to importPath so dependencies/stdlib produce no assembly noise.
// `-o os.DevNull` discards any output binary (a main package would
// otherwise litter the working directory).
func buildArgs(importPath, overlayPath, tags string) []string {
	args := []string{"build", "-o", os.DevNull, "-gcflags=" + importPath + "=-S"}
	if overlayPath != "" {
		args = append(args, "-overlay="+overlayPath)
	}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	return append(args, importPath)
}

// normalizeAsm hashes the `-S` assembly after dropping `# <importpath>`
// package-header lines and trailing whitespace. Both are constant between
// the original and the mutant for a given package, so the stripping is
// purely defensive against incidental churn; the file:line refs, symbol
// hashes, and object byte dumps that encode real differences are kept.
func normalizeAsm(b []byte) string {
	h := sha256.New()
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		if len(line) > 0 && line[0] == '#' {
			continue
		}
		line = bytes.TrimRight(line, " \t\r")
		h.Write(line)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// overlay is the JSON structure for `go build -overlay`, mirroring the
// shape the runner uses for `go test -overlay`.
type overlay struct {
	Replace map[string]string `json:"Replace"`
}

func writeOverlay(path, srcAbs, tmpSrc string) error {
	b, err := marshalFunc(overlay{Replace: map[string]string{srcAbs: tmpSrc}})
	if err != nil {
		return err
	}
	return writeFileFunc(path, b, 0o644)
}

// Run checks every StatusLived mutant for compiler equivalence in
// parallel, flipping matches to StatusEquivalent in place. Mutants that
// error or aren't equivalent stay LIVED. onResult (may be nil) is invoked
// once per checked mutant, serialized so a non-concurrent terminal sink is
// safe. Honors ctx cancellation.
func (d *Detector) Run(ctx context.Context, mutants []mutator.Mutant, workers int, tmpDir string, onResult func(mutator.Mutant)) {
	var lived []int
	for i := range mutants {
		if mutants[i].Status == mutator.StatusLived {
			lived = append(lived, i)
		}
	}
	// gomutants:disable-next-line BRANCH_IF reason="fast-path optimisation; with no survivors the loop below feeds an empty work channel, so spawning workers and returning is observably identical to returning early"
	if len(lived) == 0 {
		return
	}
	// Sort by (Pkg, File, StartOffset) so consecutive survivors share the
	// per-package reference compile and the dependency build cache.
	sort.SliceStable(lived, func(a, b int) bool {
		return mutantLess(mutants[lived[a]], mutants[lived[b]])
	})

	// gomutants:disable-next-line CONDITIONALS_BOUNDARY reason="`< → <=` is provably equivalent: at workers==1 the body sets workers=1 (a no-op), and for every other value the predicate is unchanged, so all inputs yield the same worker count"
	if workers < 1 {
		workers = 1
	}
	work := make(chan int, len(lived))
	var wg sync.WaitGroup
	var outMu sync.Mutex // serializes onResult (and its mutant read)
	for w := range workers {
		tmpSrc := filepath.Join(tmpDir, fmt.Sprintf("tce-%d.go", w))
		overlayPath := filepath.Join(tmpDir, fmt.Sprintf("tce-overlay-%d.json", w))
		wg.Add(1)
		go func(tmpSrc, overlayPath string) {
			defer wg.Done()
			for idx := range work {
				if ctx.Err() != nil {
					return
				}
				if eq, err := d.Check(ctx, mutants[idx], tmpSrc, overlayPath); err == nil && eq {
					mutants[idx].Status = mutator.StatusEquivalent
				}
				if onResult != nil {
					outMu.Lock()
					onResult(mutants[idx])
					outMu.Unlock()
				}
			}
		}(tmpSrc, overlayPath)
	}
	for _, idx := range lived {
		work <- idx // buffered to len(lived); never blocks
	}
	close(work)
	wg.Wait()
}

// mutantLess orders mutants by (Pkg, File, StartOffset). Paired `<`/`>`
// comparisons keep each boundary observable to the mutation tester.
func mutantLess(a, b mutator.Mutant) bool {
	if a.Pkg < b.Pkg {
		return true
	}
	if a.Pkg > b.Pkg {
		return false
	}
	if a.File < b.File {
		return true
	}
	if a.File > b.File {
		return false
	}
	return a.StartOffset < b.StartOffset
}
