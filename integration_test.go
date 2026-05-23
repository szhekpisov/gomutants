package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/szhekpisov/gomutants/internal/report"
)

func TestIntegrationSimple(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir, _ := os.Getwd()
	outPath := filepath.Join(t.TempDir(), "report.json")

	err := run(context.Background(), []string{
		"-w", "4",
		"-o", outPath,
		"./testdata/simple/",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	r := loadReport(t, outPath)

	// Expected: 51 total, 0 not covered (all positions in tested
	// files are testable thanks to the FilterByCoverage relaxation
	// that runs uninstrumented positions in tested files), 51 tested.
	if r.MutantsTotal != 51 {
		t.Errorf("total=%d, want 51", r.MutantsTotal)
	}
	if r.MutantsNotCovered != 0 {
		t.Errorf("not_covered=%d, want 0", r.MutantsNotCovered)
	}

	// All mutants should be either killed, lived, or not viable.
	tested := r.MutantsKilled + r.MutantsLived + r.MutantsNotViable
	if tested != 51 {
		t.Errorf("tested=%d (killed=%d lived=%d not_viable=%d), want 51 total",
			tested, r.MutantsKilled, r.MutantsLived, r.MutantsNotViable)
	}

	// Efficacy should be > 80% (strong tests).
	if r.TestEfficacy < 80 {
		t.Errorf("efficacy=%.1f%%, want >= 80%%", r.TestEfficacy)
	}

	_ = dir
}

func TestIntegrationUntested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	outPath := filepath.Join(t.TempDir(), "report.json")

	err := run(context.Background(), []string{
		"-w", "4",
		"-o", outPath,
		"./testdata/untested/",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	r := loadReport(t, outPath)

	// Expected: 12 total, 6 not covered (IsEven has no test;
	// its two literals each emit IntegerIncrement+IntegerDecrement
	// alongside the original ARITHMETIC_BASE / CONDITIONALS_NEGATION).
	if r.MutantsTotal != 12 {
		t.Errorf("total=%d, want 12", r.MutantsTotal)
	}
	if r.MutantsNotCovered != 6 {
		t.Errorf("not_covered=%d, want 6", r.MutantsNotCovered)
	}

	// Weak tests — some mutants should survive.
	if r.MutantsLived == 0 {
		t.Error("expected some lived mutants with weak tests")
	}

	// Efficacy should be < 100% (intentionally weak tests).
	if r.TestEfficacy >= 100 {
		t.Errorf("efficacy=%.1f%%, want < 100%% with weak tests", r.TestEfficacy)
	}
}

func TestIntegrationDirectives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	outPath := filepath.Join(t.TempDir(), "report.json")

	err := run(context.Background(), []string{
		"-w", "4",
		"--cache=off",
		"-o", outPath,
		"./testdata/directives/",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	r := loadReport(t, outPath)

	// Five mutants are suppressed across the four directive forms:
	// 1× same-line on Add (scoped to ARITHMETIC_BASE);
	// 2× next-line on Sub (wildcard suppresses ARITHMETIC + INVERT_NEGATIVES);
	// 1× disable-func on Magic body;
	// 1× disable-regexp on Mul's `return a * b`.
	if r.MutantsSuppressed != 5 {
		t.Errorf("MutantsSuppressed=%d, want 5", r.MutantsSuppressed)
	}

	// Suppressed mutants must not roll into MutantsTotal.
	if r.MutantsTotal != 1 {
		t.Errorf("MutantsTotal=%d, want 1 (only Plain's `+` survives suppression)", r.MutantsTotal)
	}
	if r.MutantsKilled != 1 {
		t.Errorf("MutantsKilled=%d, want 1", r.MutantsKilled)
	}

	// No suppressed mutant should appear in Files[].Mutations.
	for _, f := range r.Files {
		for _, m := range f.Mutations {
			// Sub's `a - b` is on line 11; Magic's `a + b` is on line 18;
			// Mul's `a * b` is on line 28. Add's line-5 ARITHMETIC_BASE
			// is suppressed but a non-arithmetic mutator on the same line
			// (if any) would be allowed — the fixture has none.
			if m.Line == 5 && m.Type == "ARITHMETIC_BASE" {
				t.Errorf("Add line 5 ARITHMETIC_BASE should be suppressed: %+v", m)
			}
			if m.Line == 11 {
				t.Errorf("Sub line 11 should be suppressed: %+v", m)
			}
			if m.Line == 18 {
				t.Errorf("Magic line 18 (inside disable-func) should be suppressed: %+v", m)
			}
			if m.Line == 28 {
				t.Errorf("Mul line 28 (regexp match) should be suppressed: %+v", m)
			}
		}
	}
}

func loadReport(t *testing.T, path string) *report.Report {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading report: %v", err)
	}
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("parsing report: %v", err)
	}
	return &r
}
