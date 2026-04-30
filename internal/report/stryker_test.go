package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

func TestStrykerStatusMapping(t *testing.T) {
	cases := []struct {
		in   mutator.MutantStatus
		want string
	}{
		{mutator.StatusKilled, "Killed"},
		{mutator.StatusLived, "Survived"},
		{mutator.StatusNotCovered, "NoCoverage"},
		{mutator.StatusNotViable, "CompileError"},
		{mutator.StatusTimedOut, "Timeout"},
		{mutator.StatusPending, "Pending"},
	}
	for _, tc := range cases {
		got := strykerStatus(tc.in)
		if got != tc.want {
			t.Errorf("strykerStatus(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFileIndexLineCol(t *testing.T) {
	idx := newFileIndex([]byte("ab\ncde\nf"))
	cases := []struct {
		off               int
		wantLine, wantCol int
	}{
		{0, 1, 1}, // 'a'
		{1, 1, 2}, // 'b'
		{2, 1, 3}, // '\n'
		{3, 2, 1}, // 'c'
		{5, 2, 3}, // 'e'
		{6, 2, 4}, // '\n' on line 2
		{7, 3, 1}, // 'f'
		{8, 3, 2}, // EOF
		{-1, 1, 1},
		{99, 3, 2},
	}
	for _, tc := range cases {
		gotLine, gotCol := idx.lineCol(tc.off)
		if gotLine != tc.wantLine || gotCol != tc.wantCol {
			t.Errorf("lineCol(%d) = (%d,%d), want (%d,%d)", tc.off, gotLine, gotCol, tc.wantLine, tc.wantCol)
		}
	}
}

// TestWriteStryker_RoundTrip writes a report and re-parses it as the schema
// types to confirm the on-disk JSON is well-formed and carries the data
// downstream consumers (HTML viewer, dashboard) need.
func TestWriteStryker_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.go")
	src := "package x\n\nfunc add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plusOff := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '+' {
			plusOff = i
			break
		}
	}
	plusLine, plusCol := newFileIndex([]byte(src)).lineCol(plusOff)

	mutants := []mutator.Mutant{
		{
			ID:          1,
			Type:        mutator.ArithmeticBase,
			File:        srcPath,
			RelFile:     "src.go",
			Line:        plusLine,
			Col:         plusCol,
			Original:    "+",
			Replacement: "-",
			StartOffset: plusOff,
			EndOffset:   plusOff + 1,
			Status:      mutator.StatusLived,
		},
		{
			ID:          2,
			Type:        mutator.ArithmeticBase,
			File:        srcPath,
			RelFile:     "src.go",
			Line:        plusLine,
			Col:         plusCol,
			Original:    "+",
			Replacement: "*",
			StartOffset: plusOff,
			EndOffset:   plusOff + 1,
			Status:      mutator.StatusKilled,
		},
	}

	out := filepath.Join(dir, "stryker.json")
	if err := WriteStryker(out, mutants, "/proj", "0.1.0"); err != nil {
		t.Fatalf("WriteStryker: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got strykerReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.SchemaVersion != "2" {
		t.Errorf("SchemaVersion=%q, want 2", got.SchemaVersion)
	}
	if got.Framework == nil || got.Framework.Name != "gomutants" || got.Framework.Version != "0.1.0" {
		t.Errorf("Framework=%+v", got.Framework)
	}
	if got.Thresholds.High != 80 || got.Thresholds.Low != 60 {
		t.Errorf("Thresholds=%+v", got.Thresholds)
	}
	if got.ProjectRoot != "/proj" {
		t.Errorf("ProjectRoot=%q", got.ProjectRoot)
	}

	file, ok := got.Files["src.go"]
	if !ok {
		t.Fatalf("missing src.go in files: %+v", got.Files)
	}
	if file.Language != "go" {
		t.Errorf("Language=%q, want go", file.Language)
	}
	if file.Source != src {
		t.Errorf("Source not preserved verbatim")
	}
	if len(file.Mutants) != 2 {
		t.Fatalf("Mutants=%d, want 2", len(file.Mutants))
	}
	if file.Mutants[0].Status != "Survived" {
		t.Errorf("Mutants[0].Status=%q, want Survived", file.Mutants[0].Status)
	}
	if file.Mutants[1].Status != "Killed" {
		t.Errorf("Mutants[1].Status=%q, want Killed", file.Mutants[1].Status)
	}
	if file.Mutants[0].Replacement != "-" || file.Mutants[1].Replacement != "*" {
		t.Errorf("Replacement preservation broken: %+v", file.Mutants)
	}

	// Location end should be derived from byte offsets on the source.
	loc := file.Mutants[0].Location
	if loc.Start.Line != 3 {
		t.Errorf("Start.Line=%d, want 3", loc.Start.Line)
	}
	if loc.End.Line != 3 || loc.End.Column-loc.Start.Column != 1 {
		t.Errorf("End=%+v should be one column past Start=%+v on the same line", loc.End, loc.Start)
	}
}

func TestWriteStryker_MultiLineMutant(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "blk.go")
	src := "if x {\n  doStuff()\n  more()\n}\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// The body spans byte offsets covering "{...\n}" (multi-line).
	startOff := 5         // '{'
	endOff := len(src) - 1 // '}' is at len-2; EndOffset is exclusive.

	mutants := []mutator.Mutant{
		{
			ID: 1, Type: mutator.BranchIf, File: srcPath, RelFile: "blk.go",
			Line: 1, Col: 6,
			Original: "{\n  doStuff()\n  more()\n}", Replacement: "{ _ = 0 }",
			StartOffset: startOff, EndOffset: endOff,
			Status: mutator.StatusLived,
		},
	}

	out := filepath.Join(dir, "s.json")
	if err := WriteStryker(out, mutants, "/p", "0.1.0"); err != nil {
		t.Fatalf("WriteStryker: %v", err)
	}
	data, _ := os.ReadFile(out)
	var got strykerReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	loc := got.Files["blk.go"].Mutants[0].Location
	if loc.End.Line <= loc.Start.Line {
		t.Errorf("multi-line mutant end (%+v) should be on a later line than start (%+v)", loc.End, loc.Start)
	}
}

func TestWriteStryker_GroupsByFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	mutants := []mutator.Mutant{
		{ID: 1, Type: mutator.ArithmeticBase, File: filepath.Join(dir, "a.go"), RelFile: "a.go", Line: 1, Col: 1, Status: mutator.StatusKilled},
		{ID: 2, Type: mutator.ArithmeticBase, File: filepath.Join(dir, "b.go"), RelFile: "b.go", Line: 1, Col: 1, Status: mutator.StatusLived},
		{ID: 3, Type: mutator.ArithmeticBase, File: filepath.Join(dir, "a.go"), RelFile: "a.go", Line: 1, Col: 2, Status: mutator.StatusKilled},
	}
	out := filepath.Join(dir, "s.json")
	if err := WriteStryker(out, mutants, "/p", "0.1.0"); err != nil {
		t.Fatalf("WriteStryker: %v", err)
	}
	data, _ := os.ReadFile(out)
	var got strykerReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.Files["a.go"].Mutants) != 2 {
		t.Errorf("a.go mutants=%d, want 2", len(got.Files["a.go"].Mutants))
	}
	if len(got.Files["b.go"].Mutants) != 1 {
		t.Errorf("b.go mutants=%d, want 1", len(got.Files["b.go"].Mutants))
	}
}

func TestWriteStryker_PropagatesReadError(t *testing.T) {
	dir := t.TempDir()
	mutants := []mutator.Mutant{
		{ID: 1, Type: mutator.ArithmeticBase,
			File: filepath.Join(dir, "does-not-exist.go"), RelFile: "does-not-exist.go",
			Line: 1, Col: 1, Status: mutator.StatusKilled},
	}
	err := WriteStryker(filepath.Join(dir, "out.json"), mutants, "/p", "0.1.0")
	if err == nil {
		t.Fatal("expected error when mutant references unreadable file")
	}
	if !strings.Contains(err.Error(), "does-not-exist.go") {
		t.Errorf("error %q should mention the missing file", err)
	}
}

func TestWriteStryker_Empty(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "s.json")
	if err := WriteStryker(out, nil, "/p", "0.1.0"); err != nil {
		t.Fatalf("WriteStryker: %v", err)
	}
	data, _ := os.ReadFile(out)
	var got strykerReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SchemaVersion != "2" {
		t.Errorf("SchemaVersion=%q", got.SchemaVersion)
	}
	if len(got.Files) != 0 {
		t.Errorf("Files should be empty, got %d", len(got.Files))
	}
}
