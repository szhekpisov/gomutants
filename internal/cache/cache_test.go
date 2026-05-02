package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

const (
	testModule  = "github.com/example/proj"
	testVersion = "0.1.0"
)

func TestHashFile_StableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	h1, err := HashFile(p)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	h2, err := HashFile(p)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not stable: %s vs %s", h1, h2)
	}

	if err := os.WriteFile(p, []byte("package x\n\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	h3, err := HashFile(p)
	if err != nil {
		t.Fatalf("hash3: %v", err)
	}
	if h3 == h1 {
		t.Fatalf("hash unchanged after content edit: %s", h3)
	}
}

func TestHashFile_Missing(t *testing.T) {
	if _, err := HashFile(filepath.Join(t.TempDir(), "nope.go")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestHasher_UsesSrcCacheBeforeDisk(t *testing.T) {
	// File on disk has different content than the srcCache entry — the
	// hasher must hash the in-memory bytes, never falling back to disk.
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	mustWrite(t, p, "DISK CONTENT\n")

	memBody := []byte("MEMORY CONTENT\n")
	h := NewHasher(map[string][]byte{p: memBody})
	got, err := h.File(p)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	want, err := HashFile(p) // hashes the disk file
	if err != nil {
		t.Fatalf("disk hash: %v", err)
	}
	if got == want {
		t.Fatalf("hasher used disk content, expected srcCache hit")
	}
}

func TestHashTestFiles_OrderInvariantAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a_test.go")
	b := filepath.Join(dir, "b_test.go")
	mustWrite(t, a, "package x\n")
	mustWrite(t, b, "package x\n")

	h := NewHasher(nil)
	h1, err := h.HashTestFiles([]string{a, b})
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	// Reverse order — must produce the same hash.
	h2, err := NewHasher(nil).HashTestFiles([]string{b, a})
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash depends on input order: %s vs %s", h1, h2)
	}

	// Edit a — hash must change.
	mustWrite(t, a, "package x\n\n")
	h3, err := NewHasher(nil).HashTestFiles([]string{a, b})
	if err != nil {
		t.Fatalf("hash3: %v", err)
	}
	if h3 == h1 {
		t.Fatal("hash unchanged after content edit")
	}
}

func TestHashTestFiles_EmptyAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a_test.go")
	mustWrite(t, a, "package x\n")

	h := NewHasher(nil)
	hEmpty, err := h.HashTestFiles(nil)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	hSingle, err := h.HashTestFiles([]string{a})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	hDup, err := h.HashTestFiles([]string{a, a})
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	if hEmpty == hSingle {
		t.Fatal("empty and single inputs hash to the same value")
	}
	if hSingle != hDup {
		t.Fatalf("duplicates not deduped: %s vs %s", hSingle, hDup)
	}
}

func TestHashTestFiles_MissingFilePropagatesError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.go")
	if _, err := NewHasher(nil).HashTestFiles([]string{missing}); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCacheString(t *testing.T) {
	var nilC *Cache
	if got := nilC.String(); got != "<nil>" {
		t.Errorf("nil cache String = %q, want \"<nil>\"", got)
	}
	c := &Cache{GoModule: "m", ToolVersion: "v", Entries: []Entry{{}, {}, {}}}
	got := c.String()
	if !strings.Contains(got, "module=m") || !strings.Contains(got, "tool=v") || !strings.Contains(got, "entries=3") {
		t.Errorf("String() = %q, missing module/tool/entries fields", got)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	c := &Cache{
		SchemaVersion: SchemaVersion,
		GoModule:      testModule,
		ToolVersion:   testVersion,
		Entries: []Entry{
			{
				RelFile: "pkg/x.go", Line: 10, Col: 5,
				Type: "ARITHMETIC_BASE", StartOffset: 100,
				Original: "+", Replacement: "-",
				ProdHash: "abc", TestsHash: "def",
				Status: "KILLED", DurationMs: 42,
			},
		},
	}
	if err := Save(c, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := Load(path, testModule, testVersion)
	if len(got.Entries) != 1 || got.Entries[0] != c.Entries[0] {
		t.Fatalf("round-trip mismatch: %+v", got.Entries)
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	c := Load("", testModule, testVersion)
	if c == nil || len(c.Entries) != 0 {
		t.Fatalf("expected empty cache, got %+v", c)
	}
	if c.GoModule != testModule || c.ToolVersion != testVersion {
		t.Fatalf("metadata not stamped: %+v", c)
	}
}

func TestLoad_Missing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nonexistent.json")
	c := Load(p, testModule, testVersion)
	if len(c.Entries) != 0 {
		t.Fatalf("expected empty cache for missing file, got %d entries", len(c.Entries))
	}
}

func TestLoad_GarbageJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	mustWrite(t, p, "{not json")
	c := Load(p, testModule, testVersion)
	if len(c.Entries) != 0 {
		t.Fatal("expected empty cache for garbage")
	}
}

func TestLoad_SchemaVersionMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	mustWrite(t, p, `{"schema_version":99,"go_module":"`+testModule+`","tool_version":"`+testVersion+`","entries":[{"rel_file":"x.go","status":"KILLED"}]}`)
	c := Load(p, testModule, testVersion)
	if len(c.Entries) != 0 {
		t.Fatal("expected empty cache for schema mismatch")
	}
}

func TestLoad_ModuleMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	mustWrite(t, p, `{"schema_version":2,"go_module":"other/mod","tool_version":"`+testVersion+`","entries":[{"rel_file":"x.go","status":"KILLED"}]}`)
	c := Load(p, testModule, testVersion)
	if len(c.Entries) != 0 {
		t.Fatal("expected empty cache for module mismatch")
	}
}

func TestLoad_ToolVersionMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cache.json")
	mustWrite(t, p, `{"schema_version":2,"go_module":"`+testModule+`","tool_version":"0.0.9","entries":[{"rel_file":"x.go","status":"KILLED"}]}`)
	c := Load(p, testModule, testVersion)
	if len(c.Entries) != 0 {
		t.Fatal("expected empty cache for tool-version mismatch")
	}
}

func TestSave_CreatesParentDir(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deeply", "nested", "cache.json")
	c := &Cache{SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion}
	if err := Save(c, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSave_EmptyPathIsNoOp(t *testing.T) {
	if err := Save(&Cache{}, ""); err != nil {
		t.Fatalf("save empty: %v", err)
	}
}

// TestSave_LeavesNoTempFiles asserts the atomic-rename path cleans up
// after itself on the success path: only the target file should remain
// in the directory after Save.
func TestSave_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	c := &Cache{SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion}
	if err := Save(c, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "cache.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only cache.json, got %v", names)
	}
}

// TestSave_RewriteIsAtomic asserts that overwriting an existing cache
// file replaces it cleanly (no partial-write window): the file is
// always parseable as a valid Cache before AND after a Save call.
func TestSave_RewriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	first := &Cache{
		SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion,
		Entries: []Entry{{RelFile: "a.go", Line: 1, Type: "ARITHMETIC_BASE", Status: mutator.StatusKilled.String()}},
	}
	if err := Save(first, path); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	second := &Cache{
		SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion,
		Entries: []Entry{{RelFile: "b.go", Line: 2, Type: "ARITHMETIC_BASE", Status: mutator.StatusLived.String()}},
	}
	if err := Save(second, path); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got := Load(path, testModule, testVersion)
	if len(got.Entries) != 1 || got.Entries[0].RelFile != "b.go" {
		t.Fatalf("rewrite did not replace cleanly: %+v", got.Entries)
	}
}

// lookupCase represents one row of the Lookup hit/miss matrix.
type lookupCase struct {
	name           string
	priorStatus    mutator.MutantStatus
	prodHashMatch  bool
	testsHashMatch bool
	wantHit        bool
}

func TestLookup_HitMissMatrix(t *testing.T) {
	cases := []lookupCase{
		{"killed both match", mutator.StatusKilled, true, true, true},
		{"killed prod mismatch", mutator.StatusKilled, false, true, false},
		{"killed tests mismatch", mutator.StatusKilled, true, false, false},
		{"lived both match", mutator.StatusLived, true, true, true},
		{"lived tests mismatch", mutator.StatusLived, true, false, false},
		// TIMED_OUT now requires tests_hash match — adding a faster
		// killer test could legitimately turn a prior timeout into KILLED.
		{"timed_out both match", mutator.StatusTimedOut, true, true, true},
		{"timed_out tests mismatch", mutator.StatusTimedOut, true, false, false},
		{"timed_out prod mismatch", mutator.StatusTimedOut, false, true, false},
		// NOT_VIABLE is purely a function of mutated source (compile failure),
		// so reuse is gated only on prod_hash.
		{"not_viable prod match", mutator.StatusNotViable, true, false, true},
		{"not_viable prod mismatch", mutator.StatusNotViable, false, false, false},
		{"not_covered never reused", mutator.StatusNotCovered, true, true, false},
		{"pending never reused", mutator.StatusPending, true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runLookupCase(t, tc)
		})
	}
}

func runLookupCase(t *testing.T, tc lookupCase) {
	t.Helper()
	dir := t.TempDir()
	prodPath := filepath.Join(dir, "x.go")
	testPath := filepath.Join(dir, "x_test.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, testPath, "package x\n")

	prodHash, err := HashFile(prodPath)
	if err != nil {
		t.Fatalf("prodHash: %v", err)
	}
	testsHash, err := NewHasher(nil).HashTestFiles([]string{testPath})
	if err != nil {
		t.Fatalf("testsHash: %v", err)
	}
	storedProd := prodHash
	storedTests := testsHash
	if !tc.prodHashMatch {
		storedProd = "stale-prod"
	}
	if !tc.testsHashMatch {
		storedTests = "stale-tests"
	}

	c := &Cache{
		SchemaVersion: SchemaVersion,
		GoModule:      testModule,
		ToolVersion:   testVersion,
		Entries: []Entry{{
			RelFile: "x.go", Line: 1, Col: 1,
			Type: "ARITHMETIC_BASE", StartOffset: 0,
			Original: "+", Replacement: "-",
			ProdHash: storedProd, TestsHash: storedTests,
			Status: tc.priorStatus.String(), DurationMs: 7,
		}},
	}
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: prodPath, RelFile: "x.go",
		Line: 1, Col: 1, StartOffset: 0,
		Original: "+", Replacement: "-",
		Status: mutator.StatusPending,
	}}

	hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor)
	if tc.wantHit {
		if hits != 1 {
			t.Fatalf("expected hit, got %d hits", hits)
		}
		if !mutants[0].FromCache {
			t.Fatal("FromCache not set")
		}
		if mutants[0].Status != tc.priorStatus {
			t.Fatalf("Status=%s, want %s", mutants[0].Status, tc.priorStatus)
		}
		if mutants[0].Duration != 7*time.Millisecond {
			t.Fatalf("Duration=%v, want 7ms", mutants[0].Duration)
		}
	} else {
		if hits != 0 {
			t.Fatalf("expected miss, got %d hits", hits)
		}
		if mutants[0].FromCache {
			t.Fatal("FromCache set on miss")
		}
		if mutants[0].Status != mutator.StatusPending {
			t.Fatalf("Status=%s, want PENDING", mutants[0].Status)
		}
	}
}

func TestLookup_NonPendingNotTouched(t *testing.T) {
	dir := t.TempDir()
	prodPath := filepath.Join(dir, "x.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, filepath.Join(dir, "x_test.go"), "package x\n")
	prod, _ := HashFile(prodPath)
	tests, _ := NewHasher(nil).HashTestFiles([]string{filepath.Join(dir, "x_test.go")})

	c := &Cache{
		SchemaVersion: SchemaVersion,
		GoModule:      testModule,
		ToolVersion:   testVersion,
		Entries: []Entry{{
			RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
			Original: "+", Replacement: "-",
			ProdHash: prod, TestsHash: tests, Status: "KILLED",
		}},
	}
	// Mutant is already NotCovered (e.g. coverage filter promoted it).
	// Must remain NotCovered — cache reuse must not override.
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: prodPath, RelFile: "x.go",
		Line: 1, Col: 1,
		Original: "+", Replacement: "-",
		Status: mutator.StatusNotCovered,
	}}
	hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor)
	if hits != 0 {
		t.Fatalf("expected 0 hits on non-pending mutant, got %d", hits)
	}
	if mutants[0].Status != mutator.StatusNotCovered {
		t.Fatalf("Status changed: %s", mutants[0].Status)
	}
}

func TestLookup_UnknownIdentityKey(t *testing.T) {
	dir := t.TempDir()
	prodPath := filepath.Join(dir, "x.go")
	mustWrite(t, prodPath, "package x\n")
	prod, _ := HashFile(prodPath)

	c := &Cache{
		Entries: []Entry{{
			RelFile: "other.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
			Original: "+", Replacement: "-",
			ProdHash: prod, Status: "KILLED",
		}},
	}
	mutants := []mutator.Mutant{{
		ID: 1, File: prodPath, RelFile: "x.go", Line: 1, Col: 1,
		Type: mutator.ArithmeticBase, Original: "+", Replacement: "-",
		Status: mutator.StatusPending,
	}}
	if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 0 {
		t.Fatalf("expected miss, got %d", hits)
	}
}

func TestLookup_NilCache(t *testing.T) {
	mutants := []mutator.Mutant{{Status: mutator.StatusPending}}
	var c *Cache
	if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 0 {
		t.Fatalf("nil cache should return 0 hits, got %d", hits)
	}
}

func TestLookup_CorruptedStatusFallsThrough(t *testing.T) {
	dir := t.TempDir()
	prodPath := filepath.Join(dir, "x.go")
	mustWrite(t, prodPath, "package x\n")
	prod, _ := HashFile(prodPath)
	c := &Cache{
		Entries: []Entry{{
			RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
			Original: "+", Replacement: "-",
			ProdHash: prod, Status: "WAT",
		}},
	}
	mutants := []mutator.Mutant{{
		ID: 1, File: prodPath, RelFile: "x.go", Line: 1, Col: 1,
		Type: mutator.ArithmeticBase, Original: "+", Replacement: "-",
		Status: mutator.StatusPending,
	}}
	if hits := c.Lookup(mutants, NewHasher(nil), pkgDirTestFilesFor); hits != 0 {
		t.Fatalf("corrupted status must miss, got %d hits", hits)
	}
}

func TestUpdate_KeepsUnchangedCarriesNewDropsStale(t *testing.T) {
	root := t.TempDir()
	// Two prod files. file_a.go stays the same; file_b.go is rewritten.
	mustWrite(t, filepath.Join(root, "file_a.go"), "package x\n")
	mustWrite(t, filepath.Join(root, "file_b.go"), "package x\n// v1\n")

	hA, _ := HashFile(filepath.Join(root, "file_a.go"))
	hB, _ := HashFile(filepath.Join(root, "file_b.go"))

	c := &Cache{
		SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion,
		Entries: []Entry{
			{ // prior entry for file_a — file unchanged, should be carried over.
				RelFile: "file_a.go", Line: 1, Col: 1,
				Type: "ARITHMETIC_BASE", Original: "+", Replacement: "-",
				ProdHash: hA, Status: "KILLED",
			},
			{ // prior entry for file_b — about to be rewritten, should drop.
				RelFile: "file_b.go", Line: 1, Col: 1,
				Type: "ARITHMETIC_BASE", Original: "+", Replacement: "-",
				ProdHash: hB, Status: "KILLED",
			},
		},
	}

	// Rewrite file_b so its prior hash no longer matches.
	mustWrite(t, filepath.Join(root, "file_b.go"), "package x\n// v2\n")

	// This run only tested a NEW mutant in file_b (different line/col).
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: filepath.Join(root, "file_b.go"), RelFile: "file_b.go",
		Line: 5, Col: 3, Original: "*", Replacement: "/",
		Status: mutator.StatusLived, Duration: 11 * time.Millisecond,
	}}

	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	if len(c.Entries) != 2 {
		t.Fatalf("expected 2 entries (file_a kept + file_b new), got %d: %+v", len(c.Entries), c.Entries)
	}

	var sawA, sawNewB bool
	for _, e := range c.Entries {
		if e.RelFile == "file_a.go" && e.Status == mutator.StatusKilled.String() {
			sawA = true
		}
		if e.RelFile == "file_b.go" && e.Line == 5 && e.Status == mutator.StatusLived.String() {
			sawNewB = true
			if e.DurationMs != 11 {
				t.Errorf("DurationMs=%d, want 11", e.DurationMs)
			}
		}
		if e.RelFile == "file_b.go" && e.Line == 1 {
			t.Errorf("stale file_b entry not dropped: %+v", e)
		}
	}
	if !sawA {
		t.Error("file_a entry was dropped — should be kept (hash unchanged)")
	}
	if !sawNewB {
		t.Error("new file_b entry not added")
	}
}

func TestUpdate_StatusFiltering(t *testing.T) {
	root := t.TempDir()
	prodPath := filepath.Join(root, "x.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, filepath.Join(root, "x_test.go"), "package x\n")

	c := &Cache{SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion}

	mutants := []mutator.Mutant{
		// NOT_COVERED — must NOT be cached (recomputed each run).
		mkMutant(prodPath, 1, mutator.StatusNotCovered),
		// PENDING — must NOT be cached (incomplete run).
		mkMutant(prodPath, 2, mutator.StatusPending),
		// KILLED — cached.
		mkMutant(prodPath, 3, mutator.StatusKilled),
		// LIVED — cached.
		mkMutant(prodPath, 4, mutator.StatusLived),
		// TIMED_OUT — cached.
		mkMutant(prodPath, 5, mutator.StatusTimedOut),
		// NOT_VIABLE — cached.
		mkMutant(prodPath, 6, mutator.StatusNotViable),
	}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	if len(c.Entries) != 4 {
		t.Fatalf("expected 4 cacheable entries, got %d: %+v", len(c.Entries), c.Entries)
	}

	for _, e := range c.Entries {
		if e.Status == mutator.StatusPending.String() || e.Status == mutator.StatusNotCovered.String() {
			t.Errorf("non-cacheable status %q persisted: %+v", e.Status, e)
		}
	}
}

// TestUpdate_TestsHashStampedWhenGated asserts that Update writes
// tests_hash for exactly the statuses whose reuse is gated on it
// (KILLED/LIVED/TIMED_OUT) and leaves it empty for NOT_VIABLE (which
// reuses on prod_hash alone — a compile failure is purely a function of
// the mutated source).
func TestUpdate_TestsHashStampedWhenGated(t *testing.T) {
	root := t.TempDir()
	prodPath := filepath.Join(root, "x.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, filepath.Join(root, "x_test.go"), "package x\n")

	c := &Cache{SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion}
	mutants := []mutator.Mutant{
		mkMutant(prodPath, 1, mutator.StatusKilled),
		mkMutant(prodPath, 2, mutator.StatusLived),
		mkMutant(prodPath, 3, mutator.StatusTimedOut),
		mkMutant(prodPath, 4, mutator.StatusNotViable),
	}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	for _, e := range c.Entries {
		switch e.Status {
		case mutator.StatusKilled.String(),
			mutator.StatusLived.String(),
			mutator.StatusTimedOut.String():
			if e.TestsHash == "" {
				t.Errorf("%s entry missing tests_hash: %+v", e.Status, e)
			}
		case mutator.StatusNotViable.String():
			if e.TestsHash != "" {
				t.Errorf("NOT_VIABLE entry has tests_hash %q (should be empty): %+v", e.TestsHash, e)
			}
		}
	}
}

func TestUpdate_OverwritesPriorEntryAtSameKey(t *testing.T) {
	root := t.TempDir()
	prodPath := filepath.Join(root, "x.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, filepath.Join(root, "x_test.go"), "package x\n")
	prod, _ := HashFile(prodPath)

	c := &Cache{
		SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion,
		Entries: []Entry{{
			RelFile: "x.go", Line: 1, Col: 1, Type: "ARITHMETIC_BASE",
			Original: "+", Replacement: "-",
			ProdHash: prod, TestsHash: "old-tests", Status: "LIVED", DurationMs: 1,
		}},
	}
	mutants := []mutator.Mutant{{
		ID: 1, Type: mutator.ArithmeticBase,
		File: prodPath, RelFile: "x.go", Line: 1, Col: 1,
		Original: "+", Replacement: "-",
		Status: mutator.StatusKilled, Duration: 99 * time.Millisecond,
	}}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	if len(c.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(c.Entries))
	}
	e := c.Entries[0]
	if e.Status != mutator.StatusKilled.String() || e.DurationMs != 99 {
		t.Errorf("entry not overwritten: %+v", e)
	}
	if e.TestsHash == "old-tests" {
		t.Errorf("tests_hash not refreshed: %+v", e)
	}
}

func TestUpdate_DeterministicOrder(t *testing.T) {
	root := t.TempDir()
	prodPath := filepath.Join(root, "x.go")
	mustWrite(t, prodPath, "package x\n")
	mustWrite(t, filepath.Join(root, "x_test.go"), "package x\n")

	c := &Cache{SchemaVersion: SchemaVersion, GoModule: testModule, ToolVersion: testVersion}
	mutants := []mutator.Mutant{
		mkMutantAt(prodPath, "x.go", 5, 1, mutator.StatusKilled),
		mkMutantAt(prodPath, "x.go", 1, 1, mutator.StatusKilled),
		mkMutantAt(prodPath, "x.go", 3, 1, mutator.StatusKilled),
	}
	c.Update(mutants, NewHasher(nil), root, pkgDirTestFilesFor)

	for i := 1; i < len(c.Entries); i++ {
		if c.Entries[i-1].Line >= c.Entries[i].Line {
			t.Errorf("entries not sorted by line: %+v", c.Entries)
		}
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// pkgDirTestFilesFor is the conservative resolver used by unit tests:
// every _test.go file in the mutant's package directory. Mirrors what
// the production resolver does when no per-test coverage map is
// available — exercises the same code path without requiring tests to
// stand up a TestIndex.
func pkgDirTestFilesFor(m mutator.Mutant) []string {
	dir := filepath.Dir(m.File)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".go" && len(e.Name()) > len("_test.go") &&
			e.Name()[len(e.Name())-len("_test.go"):] == "_test.go" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files
}

func mkMutant(prodPath string, line int, status mutator.MutantStatus) mutator.Mutant {
	return mkMutantAt(prodPath, filepath.Base(prodPath), line, 1, status)
}

func mkMutantAt(prodPath, relFile string, line, col int, status mutator.MutantStatus) mutator.Mutant {
	return mutator.Mutant{
		ID:          line, // unique per call
		Type:        mutator.ArithmeticBase,
		File:        prodPath,
		RelFile:     relFile,
		Line:        line,
		Col:         col,
		StartOffset: line * 10,
		Original:    "+",
		Replacement: "-",
		Status:      status,
		Duration:    1 * time.Millisecond,
	}
}
