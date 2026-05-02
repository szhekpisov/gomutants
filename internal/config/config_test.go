package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want %d", cfg.Workers, DefaultWorkers())
	}
	if cfg.TimeoutCoefficient != 10 {
		t.Errorf("TimeoutCoefficient=%d, want 10", cfg.TimeoutCoefficient)
	}
	if cfg.Output != "mutation-report.json" {
		t.Errorf("Output=%q, want %q", cfg.Output, "mutation-report.json")
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load("/nonexistent/.gomutants.yml")
	if err != nil {
		t.Fatalf("Load of missing file should not error: %v", err)
	}
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want default %d", cfg.Workers, DefaultWorkers())
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")

	yaml := `workers: 4
test-cpu: 2
timeout-coefficient: 20
coverpkg: "./pkg/..."
output: report.json
dry-run: true
verbose: true
disable:
  - BRANCH_IF
only:
  - ARITHMETIC_BASE
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Workers != 4 {
		t.Errorf("Workers=%d, want 4", cfg.Workers)
	}
	if cfg.TestCPU != 2 {
		t.Errorf("TestCPU=%d, want 2", cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != 20 {
		t.Errorf("TimeoutCoefficient=%d, want 20", cfg.TimeoutCoefficient)
	}
	if cfg.CoverPkg != "./pkg/..." {
		t.Errorf("CoverPkg=%q, want %q", cfg.CoverPkg, "./pkg/...")
	}
	if cfg.Output != "report.json" {
		t.Errorf("Output=%q, want %q", cfg.Output, "report.json")
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true")
	}
	if !cfg.Verbose {
		t.Error("Verbose should be true")
	}
	if len(cfg.Disable) != 1 || cfg.Disable[0] != "BRANCH_IF" {
		t.Errorf("Disable=%v, want [BRANCH_IF]", cfg.Disable)
	}
	if len(cfg.Only) != 1 || cfg.Only[0] != "ARITHMETIC_BASE" {
		t.Errorf("Only=%v, want [ARITHMETIC_BASE]", cfg.Only)
	}
}

func TestLoadZeroValuesGetDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	// Explicitly set fields to zero — should fall back to defaults.
	yaml := "workers: 0\ntimeout-coefficient: 0\noutput: \"\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workers != DefaultWorkers() {
		t.Errorf("Workers=%d, want default %d", cfg.Workers, DefaultWorkers())
	}
	if cfg.TimeoutCoefficient != 10 {
		t.Errorf("TimeoutCoefficient=%d, want default 10", cfg.TimeoutCoefficient)
	}
	if cfg.Output != "mutation-report.json" {
		t.Errorf("Output=%q, want default", cfg.Output)
	}
}

func TestLoadReadError(t *testing.T) {
	// Use a directory path as config file — will cause a read error (not IsNotExist).
	dir := t.TempDir()
	_, err := Load(dir) // dir exists but is a directory, not a file.
	if err == nil {
		t.Fatal("expected error when reading a directory as config file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	if err := os.WriteFile(path, []byte("{{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestApplyFlags(t *testing.T) {
	cfg := Default()

	cfg.ApplyFlags(8, 4, 15, "./pkg/...", "out.json", "BRANCH_IF,BRANCH_ELSE", "ARITHMETIC_BASE", "main", "cache.json", true, true)

	if cfg.Workers != 8 {
		t.Errorf("Workers=%d, want 8", cfg.Workers)
	}
	if cfg.TestCPU != 4 {
		t.Errorf("TestCPU=%d, want 4", cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != 15 {
		t.Errorf("TimeoutCoefficient=%d, want 15", cfg.TimeoutCoefficient)
	}
	if cfg.CoverPkg != "./pkg/..." {
		t.Errorf("CoverPkg=%q", cfg.CoverPkg)
	}
	if cfg.Output != "out.json" {
		t.Errorf("Output=%q", cfg.Output)
	}
	if len(cfg.Disable) != 2 {
		t.Errorf("Disable=%v, want 2 entries", cfg.Disable)
	}
	if len(cfg.Only) != 1 || cfg.Only[0] != "ARITHMETIC_BASE" {
		t.Errorf("Only=%v", cfg.Only)
	}
	if cfg.ChangedSince != "main" {
		t.Errorf("ChangedSince=%q, want main", cfg.ChangedSince)
	}
	if cfg.Cache != "cache.json" {
		t.Errorf("Cache=%q, want cache.json", cfg.Cache)
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true")
	}
	if !cfg.Verbose {
		t.Error("Verbose should be true")
	}
}

func TestApplyFlagsZeroValuesNoOverride(t *testing.T) {
	cfg := Default()
	cfg.TestCPU = 7
	orig := cfg

	// Zero/empty values should not override defaults.
	cfg.ApplyFlags(0, 0, 0, "", "", "", "", "", "", false, false)

	if cfg.Workers != orig.Workers {
		t.Errorf("Workers changed from %d to %d", orig.Workers, cfg.Workers)
	}
	if cfg.TestCPU != orig.TestCPU {
		t.Errorf("TestCPU changed from %d to %d", orig.TestCPU, cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != orig.TimeoutCoefficient {
		t.Errorf("TimeoutCoefficient changed")
	}
	if cfg.Output != orig.Output {
		t.Errorf("Output changed")
	}
}

func TestResolveCache(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty resolves to default path", "", ".gomutants-cache.json"},
		{"off disables", "off", ""},
		{"explicit path passes through", "/tmp/x.json", "/tmp/x.json"},
		{"relative path passes through", ".cache.json", ".cache.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Cache: tc.in}
			c.ResolveCache()
			if c.Cache != tc.want {
				t.Errorf("Cache=%q, want %q", c.Cache, tc.want)
			}
		})
	}
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}}, // Kills STATEMENT_REMOVE on TrimSpace.
		{"single", []string{"single"}},
		{"  spaced  ", []string{"spaced"}}, // Explicit trimming check.
		{"", nil},
		{",,,", nil},
	}
	for _, tc := range tests {
		got := splitAndTrim(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("splitAndTrim(%q) = %v (len %d), want %v (len %d)",
				tc.input, got, len(got), tc.want, len(tc.want))
			continue
		}
		for i, g := range got {
			if g != tc.want[i] {
				t.Errorf("splitAndTrim(%q)[%d] = %q, want %q", tc.input, i, g, tc.want[i])
			}
		}
	}
}
