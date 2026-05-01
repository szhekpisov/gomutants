// Package cache implements PIT-style incremental analysis: per-mutant
// outcomes are persisted keyed by content hashes of the production file
// and the test files that cover the mutant. On the next run, mutants
// whose hashes still match are skipped.
//
// The cache is a single JSON file (default .gomutants-cache.json), opt-in
// via the --cache flag. NOT_COVERED is intentionally not cached — coverage
// is recomputed every run by discover.FilterByCoverage, which keeps "no
// longer covered" reclassifications correct.
//
// tests_hash is computed from the union of test files identified by the
// TestFilesForFn callback (typically backed by the per-test coverage map
// + TestIndex). This handles cross-package -coverpkg correctly: tests in
// package B that exercise code in package A invalidate A's mutants when
// edited.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// SchemaVersion is the on-disk format version. Bump when Entry shape or
// any hashing algorithm changes so old cache files are silently
// discarded rather than producing wrong skips.
//
//	v1: package-dir tests_hash (replaced — undercounted cross-package coverage).
//	v2: per-mutant tests_hash via TestFilesForFn; TIMED_OUT now gated on tests_hash.
const SchemaVersion = 2

// Cache is the on-disk artifact. Entries are keyed by mutant identity
// (rel_file, line, col, type, start_offset, original, replacement).
type Cache struct {
	SchemaVersion int     `json:"schema_version"`
	GoModule      string  `json:"go_module"`
	ToolVersion   string  `json:"tool_version"`
	Entries       []Entry `json:"entries"`
}

// Entry is one cached mutant outcome.
type Entry struct {
	RelFile     string `json:"rel_file"`
	Line        int    `json:"line"`
	Col         int    `json:"col"`
	Type        string `json:"type"`
	StartOffset int    `json:"start_offset"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	ProdHash    string `json:"prod_hash"`
	TestsHash   string `json:"tests_hash"`
	Status      string `json:"status"`
	DurationMs  int64  `json:"duration_ms"`
}

// key returns the identity tuple used for cache lookups.
func (e Entry) key() entryKey {
	return entryKey{
		RelFile:     e.RelFile,
		Line:        e.Line,
		Col:         e.Col,
		Type:        e.Type,
		StartOffset: e.StartOffset,
		Original:    e.Original,
		Replacement: e.Replacement,
	}
}

type entryKey struct {
	RelFile     string
	Line        int
	Col         int
	Type        string
	StartOffset int
	Original    string
	Replacement string
}

func mutantKey(m mutator.Mutant) entryKey {
	return entryKey{
		RelFile:     m.RelFile,
		Line:        m.Line,
		Col:         m.Col,
		Type:        string(m.Type),
		StartOffset: m.StartOffset,
		Original:    m.Original,
		Replacement: m.Replacement,
	}
}

// HashFile returns the hex-encoded sha256 of the file at absPath.
func HashFile(absPath string) (string, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hasher memoizes per-file hashes within a single run. Not safe for
// concurrent use — the pipeline calls Lookup/Update sequentially.
type Hasher struct {
	files    map[string]string // absPath → hex sha256
	srcCache map[string][]byte // optional in-memory source files (from discover.PreReadFiles)
}

// NewHasher returns an empty per-run hasher. If srcCache is non-nil, it
// is used as a fast path for File() so production sources already read
// into memory by the discovery phase aren't re-read from disk.
func NewHasher(srcCache map[string][]byte) *Hasher {
	return &Hasher{
		files:    make(map[string]string),
		srcCache: srcCache,
	}
}

// File returns the hash of absPath, computing it on first call.
func (h *Hasher) File(absPath string) (string, error) {
	if v, ok := h.files[absPath]; ok {
		return v, nil
	}
	if data, ok := h.srcCache[absPath]; ok {
		sum := sha256.Sum256(data)
		v := hex.EncodeToString(sum[:])
		h.files[absPath] = v
		return v, nil
	}
	v, err := HashFile(absPath)
	if err != nil {
		return "", err
	}
	h.files[absPath] = v
	return v, nil
}

// HashTestFiles returns a stable hex-encoded sha256 over the union of
// the test files in absPaths. Inputs are sorted and de-duplicated, so
// any iteration order produces the same hash. The returned digest mixes
// each file's basename + per-file content hash, with NUL separators so
// concatenation can't alias one file boundary into another.
//
// An empty (or nil) input returns the hash of the empty string. A read
// error on any file is propagated — callers should treat this as a
// cache miss for that mutant.
func (h *Hasher) HashTestFiles(absPaths []string) (string, error) {
	if len(absPaths) == 0 {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), nil
	}
	sorted := append([]string(nil), absPaths...)
	sort.Strings(sorted)
	uniq := sorted[:0]
	for i, p := range sorted {
		if i > 0 && p == sorted[i-1] {
			continue
		}
		uniq = append(uniq, p)
	}

	hh := sha256.New()
	for _, p := range uniq {
		// Filename + content hash + NUL separators. Filename catches
		// add/remove (even of an empty file); per-file hash catches
		// content edits; NULs prevent boundary aliasing across files.
		hh.Write([]byte(filepath.Base(p)))
		hh.Write([]byte{0})
		fileHex, err := h.File(p)
		if err != nil {
			return "", err
		}
		hh.Write([]byte(fileHex))
		hh.Write([]byte{0})
	}
	return hex.EncodeToString(hh.Sum(nil)), nil
}

// Load reads the cache from path. Returns an empty (but valid) Cache
// stamped with the caller's identity if the file doesn't exist, fails
// to parse, or has a mismatched schema/module/tool version. Callers
// should treat the returned Cache as authoritative regardless of error
// — Load never returns nil.
func Load(path, goModule, toolVersion string) *Cache {
	empty := &Cache{
		SchemaVersion: SchemaVersion,
		GoModule:      goModule,
		ToolVersion:   toolVersion,
	}
	if path == "" {
		return empty
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return empty
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return empty
	}
	// Mutator definitions can change between tool versions, so stale
	// entries can silently produce wrong skips. Pessimistic invalidation
	// on any metadata mismatch is the safe default.
	if c.SchemaVersion != SchemaVersion || c.GoModule != goModule || c.ToolVersion != toolVersion {
		return empty
	}
	return &c
}

// Save writes c to path, creating parent directories as needed. The
// write is atomic within the target's filesystem: serialization goes
// to a temp file in the same directory, then os.Rename swaps it into
// place. A crash before the rename leaves the prior cache file
// untouched; a crash after leaves the new one fully written. Either
// way the file on disk parses successfully on the next Load.
func Save(c *Cache, path string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Temp file must live in the same directory as the target so
	// os.Rename stays atomic — cross-filesystem renames degrade to
	// copy+unlink on some platforms.
	tmp, err := os.CreateTemp(dir, ".gomutants-cache-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Cleared after a successful rename; otherwise the deferred Remove
	// cleans up any temp file left by an error path below.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := json.NewEncoder(tmp).Encode(c); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

// TestFilesForFn resolves a mutant to the set of absolute test-file
// paths whose contents gate cache reuse for that mutant. The integrator
// typically wires this through the per-test coverage map + TestIndex,
// falling back to "all _test.go in the mutant's package directory" when
// coverage information is unavailable.
type TestFilesForFn func(m mutator.Mutant) []string

// Lookup applies cache hits to the mutants slice in place. For each
// pending mutant whose identity key + content hashes match a cached
// entry, sets Status, Duration, and FromCache so the runner's
// Pending-only filter naturally skips it. Returns the number of hits.
//
// Skip rules:
//
//	prior=KILLED      + prod_hash match + tests_hash match → reuse
//	prior=LIVED       + prod_hash match + tests_hash match → reuse
//	prior=TIMED_OUT   + prod_hash match + tests_hash match → reuse
//	prior=NOT_VIABLE  + prod_hash match (compile failure — tests irrelevant) → reuse
//	otherwise → leave Pending
//
// TIMED_OUT is gated on tests because adding a faster killer test could
// legitimately turn a prior timeout into KILLED on the next run; without
// the gate we'd silently skip a now-killable mutant. NOT_VIABLE is the
// only outcome that depends purely on the mutated source (it failed to
// compile), so it's safe to reuse on prod_hash alone.
//
// Hash failures (unreadable file/dir) are silently treated as a miss
// for that mutant so a transient I/O error never produces a wrong skip.
func (c *Cache) Lookup(mutants []mutator.Mutant, h *Hasher, testFilesFor TestFilesForFn) int {
	if c == nil || len(c.Entries) == 0 {
		return 0
	}
	idx := make(map[entryKey]Entry, len(c.Entries))
	for _, e := range c.Entries {
		idx[e.key()] = e
	}
	hits := 0
	for i := range mutants {
		m := &mutants[i]
		if m.Status != mutator.StatusPending {
			continue
		}
		entry, ok := idx[mutantKey(*m)]
		if !ok {
			continue
		}
		status := parseStatus(entry.Status)
		if !canReuse(status) {
			continue
		}

		prodHash, err := h.File(m.File)
		if err != nil || prodHash != entry.ProdHash {
			continue
		}
		if needsTestsHash(status) {
			testsHash, err := h.HashTestFiles(testFilesFor(*m))
			if err != nil || testsHash != entry.TestsHash {
				continue
			}
		}

		m.Status = status
		m.Duration = time.Duration(entry.DurationMs) * time.Millisecond
		m.FromCache = true
		hits++
	}
	return hits
}

// canReuse reports whether a status is one we cache + reuse on the next
// run. PENDING / NOT_COVERED are not reusable — the first means the
// prior run never finished; the second is recomputed every run from the
// fresh coverage profile.
func canReuse(s mutator.MutantStatus) bool {
	switch s {
	case mutator.StatusKilled,
		mutator.StatusLived,
		mutator.StatusTimedOut,
		mutator.StatusNotViable:
		return true
	}
	return false
}

// needsTestsHash reports whether a cacheable status's reuse depends on
// the test files matching, in addition to the production file. Only
// NOT_VIABLE is purely a function of the mutated source (compile
// failure), so its reuse is safe on prod_hash alone.
func needsTestsHash(s mutator.MutantStatus) bool {
	switch s {
	case mutator.StatusKilled, mutator.StatusLived, mutator.StatusTimedOut:
		return true
	}
	return false
}

// parseStatus maps an on-disk status string back to a MutantStatus.
// Unknown values fall through to Pending so a corrupted cache entry
// can never silently produce a terminal status.
func parseStatus(s string) mutator.MutantStatus {
	switch s {
	case mutator.StatusKilled.String():
		return mutator.StatusKilled
	case mutator.StatusLived.String():
		return mutator.StatusLived
	case mutator.StatusTimedOut.String():
		return mutator.StatusTimedOut
	case mutator.StatusNotViable.String():
		return mutator.StatusNotViable
	}
	return mutator.StatusPending
}

// Update merges this run's results into c and drops entries for files
// whose prod_hash no longer matches the current file content. Entries
// for files outside this run's mutant set (e.g. excluded by
// --changed-since) are preserved when their file still exists with
// matching content.
//
// projectDir lets us resolve a stored RelFile back to an absolute path
// for re-hashing prior entries.
func (c *Cache) Update(mutants []mutator.Mutant, h *Hasher, projectDir string, testFilesFor TestFilesForFn) {
	if c == nil {
		return
	}

	// 1. Build new entries from this run for any mutant with a
	//    cacheable terminal status. Cache hits (FromCache=true) keep
	//    the same content, just re-emitted.
	newByKey := make(map[entryKey]Entry, len(mutants)+len(c.Entries))
	for _, m := range mutants {
		if !canReuse(m.Status) {
			continue
		}
		prodHash, err := h.File(m.File)
		if err != nil {
			continue
		}
		entry := Entry{
			RelFile:     m.RelFile,
			Line:        m.Line,
			Col:         m.Col,
			Type:        string(m.Type),
			StartOffset: m.StartOffset,
			Original:    m.Original,
			Replacement: m.Replacement,
			ProdHash:    prodHash,
			Status:      m.Status.String(),
			DurationMs:  m.Duration.Milliseconds(),
		}
		// tests_hash is only meaningful for statuses where it gates
		// reuse. Stamp it for KILLED/LIVED/TIMED_OUT so future
		// Lookups can compare; NOT_VIABLE leaves it empty.
		if needsTestsHash(m.Status) {
			testsHash, err := h.HashTestFiles(testFilesFor(m))
			if err == nil {
				entry.TestsHash = testsHash
			}
		}
		newByKey[entry.key()] = entry
	}

	// 2. Carry over prior entries whose file still hashes the same and
	//    that this run did not overwrite.
	for _, prior := range c.Entries {
		if _, overwritten := newByKey[prior.key()]; overwritten {
			continue
		}
		abs := filepath.Join(projectDir, prior.RelFile)
		curHash, err := h.File(abs)
		if err != nil {
			continue
		}
		if curHash != prior.ProdHash {
			continue
		}
		newByKey[prior.key()] = prior
	}

	// 3. Emit entries in deterministic order so the on-disk file
	//    diffs cleanly between runs.
	merged := make([]Entry, 0, len(newByKey))
	for _, e := range newByKey {
		merged = append(merged, e)
	}
	sort.Slice(merged, func(i, j int) bool {
		a, b := merged[i], merged[j]
		if a.RelFile != b.RelFile {
			return a.RelFile < b.RelFile
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Col != b.Col {
			return a.Col < b.Col
		}
		if a.StartOffset != b.StartOffset {
			return a.StartOffset < b.StartOffset
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		// Tie-break on Original/Replacement so two mutants sharing the
		// same (file, line, col, offset, type) — identity-key collision
		// avoidance only — emit in a deterministic order across runs.
		if a.Original != b.Original {
			return a.Original < b.Original
		}
		return a.Replacement < b.Replacement
	})
	c.Entries = merged
}

// String returns a short human-readable summary of the cache state for
// debug output.
func (c *Cache) String() string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Cache{module=%s tool=%s entries=%d}",
		c.GoModule, c.ToolVersion, len(c.Entries))
}
