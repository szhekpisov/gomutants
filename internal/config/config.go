package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type MutatorConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type Config struct {
	Workers            int `yaml:"workers"`
	TestCPU            int `yaml:"test-cpu"`
	TimeoutCoefficient int `yaml:"timeout-coefficient"`
	// TimeoutMargin scales per-mutant adaptive timeouts (sum of selected
	// per-test durations × this). Default 3.0 — wide enough to absorb GC
	// pauses, scheduler jitter, and mutated-code slowdowns without false
	// TIMED OUT classifications. Only used when AdaptiveTimeout is true.
	TimeoutMargin float64 `yaml:"timeout-margin"`
	// TimeoutMin floors the per-mutant adaptive timeout. Default 2s —
	// covers child-process fork + cold-start cost on tests that measure
	// in single-digit milliseconds. Only used when AdaptiveTimeout is true.
	TimeoutMin time.Duration `yaml:"timeout-min"`
	// AdaptiveTimeout enables per-mutant adaptive timeout selection.
	// Pointer so YAML can distinguish "user opted in/out" from "default"
	// — without it, a YAML `adaptive-timeout: false` is indistinguishable
	// from the zero value during ApplyFlags merging. Use
	// AdaptiveTimeoutEnabled() in callers; that handles the default.
	AdaptiveTimeout *bool    `yaml:"adaptive-timeout"`
	CoverPkg        string   `yaml:"coverpkg"`
	Output          string   `yaml:"output"`
	DryRun          bool     `yaml:"dry-run"`
	Verbose         bool     `yaml:"verbose"`
	Quiet           bool     `yaml:"quiet"`
	Disable         []string `yaml:"disable"`
	Only            []string `yaml:"only"`
	ChangedSince    string   `yaml:"changed-since"`
	Cache           string   `yaml:"cache"`
	// CheckpointInterval is how often completed mutant outcomes are
	// flushed to the cache file mid-run, so a hard kill (OOM, CI timeout,
	// SIGKILL) loses at most this much progress. 0 disables periodic
	// checkpointing — the cache is then written only once, at the end of
	// the run. Negative values are nonsensical and revert to the default.
	CheckpointInterval time.Duration             `yaml:"checkpoint-interval"`
	Mutants            map[string]*MutatorConfig `yaml:"mutants"`
}

// Default values for adaptive-timeout knobs. Exposed as package-level
// constants so the CLI flag descriptions can quote the same numbers.
const (
	DefaultTimeoutMargin = 3.0
	DefaultTimeoutMin    = 2 * time.Second
	// DefaultCheckpointInterval is the default cadence for mid-run cache
	// checkpointing. Cheap relative to per-mutant `go test` cost, and
	// bounds worst-case lost work on a hard kill to ~this duration.
	DefaultCheckpointInterval = 10 * time.Second
)

// AdaptiveTimeoutEnabled returns whether per-mutant adaptive timeout
// selection is in effect. The pointer field allows three states (set to
// true, set to false, unset); when unset the default is true.
func (c *Config) AdaptiveTimeoutEnabled() bool {
	if c.AdaptiveTimeout == nil {
		return true
	}
	return *c.AdaptiveTimeout
}

// DefaultWorkers returns the default worker count: NumCPU. Floored at 1.
// Use --workers / -w to override.
func DefaultWorkers() int {
	return max(1, runtime.NumCPU())
}

func Default() Config {
	return Config{
		Workers:            DefaultWorkers(),
		TimeoutCoefficient: 10,
		TimeoutMargin:      DefaultTimeoutMargin,
		TimeoutMin:         DefaultTimeoutMin,
		CheckpointInterval: DefaultCheckpointInterval,
		Output:             "mutation-report.json",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Preserve defaults for zero-value fields.
	if cfg.Workers == 0 {
		cfg.Workers = DefaultWorkers()
	}
	if cfg.TimeoutCoefficient == 0 {
		cfg.TimeoutCoefficient = 10
	}
	// Treat negative as nonsensical and revert to the default. ApplyFlags
	// already screens out non-positive CLI values; doing the same here
	// closes the YAML side. A negative Margin or Min would silently
	// collapse adaptive selection to the floor or to a negative scaled
	// value (later max'd up to Min) — never useful, never what the user
	// meant.
	if cfg.TimeoutMargin <= 0 {
		cfg.TimeoutMargin = DefaultTimeoutMargin
	}
	if cfg.TimeoutMin <= 0 {
		cfg.TimeoutMin = DefaultTimeoutMin
	}
	// Only negative values revert to the default — 0 is a meaningful
	// "disable periodic checkpointing" choice and must survive unmarshal.
	if cfg.CheckpointInterval < 0 {
		cfg.CheckpointInterval = DefaultCheckpointInterval
	}
	if cfg.Output == "" {
		cfg.Output = "mutation-report.json"
	}

	return cfg, nil
}

// ResolveCache materializes the cache path from the loaded config:
// "off" disables caching (Cache=""), an empty Cache enables it at the
// default path, and any other value passes through. Call after Load and
// ApplyFlags so YAML and CLI inputs are merged before the default
// kicks in.
func (c *Config) ResolveCache() {
	switch c.Cache {
	case "off":
		c.Cache = ""
	case "":
		c.Cache = ".gomutants-cache.json"
	}
}

// AdaptiveTimeoutFlag captures the `--adaptive-timeout` CLI flag value.
// Used as a parameter to ApplyFlags so the CLI layer can express three
// states ("set to true", "set to false", "not provided") that a plain
// bool cannot.
type AdaptiveTimeoutFlag struct {
	Set   bool
	Value bool
}

// CheckpointIntervalFlag captures the `--checkpoint-interval` CLI flag
// value. Like AdaptiveTimeoutFlag, it carries a Set bit so ApplyFlags can
// tell "not provided" from an explicit `--checkpoint-interval=0`; a plain
// duration can't, because 0 is both the zero value and a valid choice.
type CheckpointIntervalFlag struct {
	Set   bool
	Value time.Duration
}

// Flags bundles the CLI flag values consumed by ApplyFlags. Bundling
// them keeps the per-flag merge semantics ("non-zero / non-empty / Set=true
// overrides YAML") at the call site explicit by named field, which a
// positional signature can't sanely express past a handful of args.
type Flags struct {
	Workers            int
	TestCPU            int
	TimeoutCoefficient int
	TimeoutMargin      float64
	TimeoutMin         time.Duration
	AdaptiveTimeout    AdaptiveTimeoutFlag
	CheckpointInterval CheckpointIntervalFlag
	CoverPkg           string
	Output             string
	Disable            string
	Only               string
	ChangedSince       string
	Cache              string
	DryRun             bool
	Verbose            bool
	Quiet              bool
}

// ApplyFlags merges CLI-provided flag values into c, with CLI winning
// over the YAML-loaded defaults already present. Grouped into three
// per-kind helpers so each method stays under the linter's cognitive
// complexity threshold.
func (c *Config) ApplyFlags(f Flags) {
	c.applyNumericFlags(f)
	c.applyStringFlags(f)
	c.applyToggleFlags(f)
}

func (c *Config) applyNumericFlags(f Flags) {
	if f.Workers > 0 {
		c.Workers = f.Workers
	}
	if f.TestCPU > 0 {
		c.TestCPU = f.TestCPU
	}
	if f.TimeoutCoefficient > 0 {
		c.TimeoutCoefficient = f.TimeoutCoefficient
	}
	if f.TimeoutMargin > 0 {
		c.TimeoutMargin = f.TimeoutMargin
	}
	if f.TimeoutMin > 0 {
		c.TimeoutMin = f.TimeoutMin
	}
	if f.AdaptiveTimeout.Set {
		v := f.AdaptiveTimeout.Value
		c.AdaptiveTimeout = &v
	}
	if f.CheckpointInterval.Set {
		c.CheckpointInterval = f.CheckpointInterval.Value
	}
}

func (c *Config) applyStringFlags(f Flags) {
	if f.CoverPkg != "" {
		c.CoverPkg = f.CoverPkg
	}
	if f.Output != "" {
		c.Output = f.Output
	}
	if f.Disable != "" {
		c.Disable = splitAndTrim(f.Disable)
	}
	if f.Only != "" {
		c.Only = splitAndTrim(f.Only)
	}
	if f.ChangedSince != "" {
		c.ChangedSince = f.ChangedSince
	}
	if f.Cache != "" {
		c.Cache = f.Cache
	}
}

func (c *Config) applyToggleFlags(f Flags) {
	if f.DryRun {
		c.DryRun = true
	}
	if f.Verbose {
		c.Verbose = true
	}
	if f.Quiet {
		c.Quiet = true
	}
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
