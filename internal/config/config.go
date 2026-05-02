package config

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type MutatorConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type Config struct {
	Workers            int                       `yaml:"workers"`
	TestCPU            int                       `yaml:"test-cpu"`
	TimeoutCoefficient int                       `yaml:"timeout-coefficient"`
	CoverPkg           string                    `yaml:"coverpkg"`
	Output             string                    `yaml:"output"`
	DryRun             bool                      `yaml:"dry-run"`
	Verbose            bool                      `yaml:"verbose"`
	Disable            []string                  `yaml:"disable"`
	Only               []string                  `yaml:"only"`
	ChangedSince       string                    `yaml:"changed-since"`
	Cache              string                    `yaml:"cache"`
	Mutants            map[string]*MutatorConfig `yaml:"mutants"`
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

func (c *Config) ApplyFlags(workers, testCPU, timeoutCoefficient int, coverPkg, output, disable, only, changedSince, cache string, dryRun, verbose bool) {
	if workers > 0 {
		c.Workers = workers
	}
	if testCPU > 0 {
		c.TestCPU = testCPU
	}
	if timeoutCoefficient > 0 {
		c.TimeoutCoefficient = timeoutCoefficient
	}
	if coverPkg != "" {
		c.CoverPkg = coverPkg
	}
	if output != "" {
		c.Output = output
	}
	if disable != "" {
		c.Disable = splitAndTrim(disable)
	}
	if only != "" {
		c.Only = splitAndTrim(only)
	}
	if changedSince != "" {
		c.ChangedSince = changedSince
	}
	if cache != "" {
		c.Cache = cache
	}
	if dryRun {
		c.DryRun = true
	}
	if verbose {
		c.Verbose = true
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
