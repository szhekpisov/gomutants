package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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
quiet: true
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
	if !cfg.Quiet {
		t.Error("Quiet should be true")
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

	cfg.ApplyFlags(8, 4, 15, 4.5, 5*time.Second, AdaptiveTimeoutFlag{Set: true, Value: false}, "./pkg/...", "out.json", "BRANCH_IF,BRANCH_ELSE", "ARITHMETIC_BASE", "main", "cache.json", true, true, true)

	if cfg.Workers != 8 {
		t.Errorf("Workers=%d, want 8", cfg.Workers)
	}
	if cfg.TestCPU != 4 {
		t.Errorf("TestCPU=%d, want 4", cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != 15 {
		t.Errorf("TimeoutCoefficient=%d, want 15", cfg.TimeoutCoefficient)
	}
	if cfg.TimeoutMargin != 4.5 {
		t.Errorf("TimeoutMargin=%v, want 4.5", cfg.TimeoutMargin)
	}
	if cfg.TimeoutMin != 5*time.Second {
		t.Errorf("TimeoutMin=%v, want 5s", cfg.TimeoutMin)
	}
	if cfg.AdaptiveTimeoutEnabled() {
		t.Errorf("AdaptiveTimeoutEnabled=true, want false (CLI override)")
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
	if !cfg.Quiet {
		t.Error("Quiet should be true")
	}
}

func TestApplyFlagsZeroValuesNoOverride(t *testing.T) {
	cfg := Default()
	cfg.TestCPU = 7
	orig := cfg

	// Zero/empty values should not override defaults.
	cfg.ApplyFlags(0, 0, 0, 0, 0, AdaptiveTimeoutFlag{}, "", "", "", "", "", "", false, false, false)

	if cfg.Workers != orig.Workers {
		t.Errorf("Workers changed from %d to %d", orig.Workers, cfg.Workers)
	}
	if cfg.TestCPU != orig.TestCPU {
		t.Errorf("TestCPU changed from %d to %d", orig.TestCPU, cfg.TestCPU)
	}
	if cfg.TimeoutCoefficient != orig.TimeoutCoefficient {
		t.Errorf("TimeoutCoefficient changed")
	}
	// Pin the new adaptive-timeout knobs against CONDITIONALS_BOUNDARY
	// (`> 0` → `>= 0`) on their respective ApplyFlags guards. Without
	// these checks a `>= 0` mutation would silently overwrite the
	// default with the caller's zero.
	if cfg.TimeoutMargin != orig.TimeoutMargin {
		t.Errorf("TimeoutMargin changed from %v to %v — CONDITIONALS_BOUNDARY on `> 0` would let zero override the default", orig.TimeoutMargin, cfg.TimeoutMargin)
	}
	if cfg.TimeoutMin != orig.TimeoutMin {
		t.Errorf("TimeoutMin changed from %v to %v", orig.TimeoutMin, cfg.TimeoutMin)
	}
	if cfg.AdaptiveTimeout != orig.AdaptiveTimeout {
		t.Errorf("AdaptiveTimeout pointer changed; AdaptiveTimeoutFlag{Set:false} must be a no-op")
	}
	if cfg.Output != orig.Output {
		t.Errorf("Output changed")
	}
}

// TestAdaptiveTimeoutEnabledNilDefault kills BRANCH_IF on the
// `if c.AdaptiveTimeout == nil { return true }` body. Without that
// early return the function dereferences a nil pointer and panics; the
// recover-then-fail wrapper distinguishes the panic from a clean
// `false` return that some mutations might also produce.
func TestAdaptiveTimeoutEnabledNilDefault(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("AdaptiveTimeoutEnabled panicked on nil pointer — BRANCH_IF on the nil-guard body lets execution fall through to *c.AdaptiveTimeout: %v", r)
		}
	}()
	c := Config{AdaptiveTimeout: nil}
	if !c.AdaptiveTimeoutEnabled() {
		t.Errorf("nil AdaptiveTimeout must default to true")
	}
}

// TestAdaptiveTimeoutEnabledExplicitFalse pins the *c.AdaptiveTimeout
// dereference path so a STATEMENT_REMOVE on the deref still has a
// targeted assertion.
func TestAdaptiveTimeoutEnabledExplicitFalse(t *testing.T) {
	f := false
	c := Config{AdaptiveTimeout: &f}
	if c.AdaptiveTimeoutEnabled() {
		t.Errorf("explicit false must propagate through AdaptiveTimeoutEnabled")
	}
}

// TestDefaultTimeoutMinValue pins DefaultTimeoutMin against
// ARITHMETIC_BASE (`*` → `/` collapses 2*time.Second to 0). Asserting
// the literal value ensures any arithmetic mutation on the constant
// shows up here.
func TestDefaultTimeoutMinValue(t *testing.T) {
	if DefaultTimeoutMin != 2*time.Second {
		t.Errorf("DefaultTimeoutMin = %v, want 2s — ARITHMETIC_BASE on `2 * time.Second` would change this", DefaultTimeoutMin)
	}
}

// TestLoadAppliesDefaultsForZeroAdaptiveFields kills BRANCH_IF and
// CONDITIONALS_NEGATION on the two `if cfg.TimeoutMargin == 0` /
// `cfg.TimeoutMin == 0` defaulting blocks in Load. We write a YAML
// that explicitly sets both to zero and assert the defaults take over.
// Without the guards (or with their condition negated) the user would
// run with TimeoutMargin=0 → per-mutant timeouts always clamp to Min,
// hiding genuine slowdowns.
func TestLoadAppliesDefaultsForZeroAdaptiveFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutants.yml")
	// timeout-min is time.Duration; YAML needs a string-compatible zero.
	// "0s" is the zero duration so the defaulting block in Load() must
	// still kick in (its trigger is the int64 zero, not the YAML token).
	body := []byte("timeout-margin: 0\ntimeout-min: 0s\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TimeoutMargin != DefaultTimeoutMargin {
		t.Errorf("TimeoutMargin=%v, want default %v — BRANCH_IF / CONDITIONALS_NEGATION on the zero-guard would skip the default", cfg.TimeoutMargin, DefaultTimeoutMargin)
	}
	if cfg.TimeoutMin != DefaultTimeoutMin {
		t.Errorf("TimeoutMin=%v, want default %v", cfg.TimeoutMin, DefaultTimeoutMin)
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

// TestLoadExampleFile asserts the in-repo .gomutants.yml.example matches
// the Config struct field-for-field. The production Load() path is
// permissive (yaml.v3 silently ignores unknown keys), so a separate
// strict decode with KnownFields(true) is what actually catches drift —
// e.g. a key removed from Config but still documented in the example,
// or a typo in the example that would silently no-op for users.
//
// Hard-fails (not Skip) on a missing file: the example is committed and
// referenced from the README; a missing file should break CI rather
// than be quietly tolerated.
func TestLoadExampleFile(t *testing.T) {
	path := filepath.Join("..", "..", ".gomutants.yml.example")

	// Permissive Load() must succeed.
	if _, err := Load(path); err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}

	// Strict decode catches keys that aren't on the Config struct.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("strict decode of %s failed — example contains keys absent from Config: %v", path, err)
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
