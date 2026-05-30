package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/szhekpisov/gomutants/internal/cache"
	"github.com/szhekpisov/gomutants/internal/config"
	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/discover"
	"github.com/szhekpisov/gomutants/internal/mutator"
	"github.com/szhekpisov/gomutants/internal/report"
	"github.com/szhekpisov/gomutants/internal/runner"
	"github.com/szhekpisov/gomutants/internal/tce"
)

// Sentinel defaults; the effective* helpers upgrade these from build
// info when the corresponding ldflag wasn't injected.
const (
	devVersion   = "dev"
	devCommit    = "none"
	devBuildDate = "unknown"
)

// Overridden at release time via -ldflags '-X main.<field>=...'.
var (
	version   = devVersion
	commit    = devCommit
	buildDate = devBuildDate
)

// effectiveVersion returns the user-visible version. Ldflags-injected
// builds win; otherwise we try Main.Version from build info (set by
// `go install module@vX.Y.Z`); otherwise devVersion.
func effectiveVersion() string {
	if version != devVersion {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

// effectiveCommit returns the source commit. Ldflags win; otherwise
// vcs.revision from build info (populated for `go build` from a git
// tree, but not for `go install module@vX.Y.Z`); otherwise devCommit.
func effectiveCommit() string {
	if commit != devCommit {
		return commit
	}
	if v := vcsSetting("vcs.revision"); v != "" {
		return v
	}
	return commit
}

// effectiveBuildDate returns the build date. Ldflags win; otherwise
// vcs.time from build info; otherwise devBuildDate.
func effectiveBuildDate() string {
	if buildDate != devBuildDate {
		return buildDate
	}
	if v := vcsSetting("vcs.time"); v != "" {
		return v
	}
	return buildDate
}

func vcsSetting(key string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// formatVersion renders the rich `--version` line: name + version,
// then commit and build date when known.
func formatVersion() string {
	return fmt.Sprintf("gomutants v%s (commit: %s, built: %s)\n",
		effectiveVersion(), effectiveCommit(), effectiveBuildDate())
}

// cacheToolVersion is the identifier stamped into the cache's
// `tool_version` field and gated on Load to invalidate stale entries.
//
// We extend the user-visible version with vcs metadata from
// runtime/debug so that local dev builds from different commits don't
// share a key. Without this, two contributors editing mutator code
// against the same constant version string would produce silent stale
// skips for each other (or for themselves across rebuilds).
//
// Format: "<version>" for clean release builds; "<version>+<short>"
// for committed dev builds; "<version>+<short>.dirty" when the working
// tree has uncommitted changes; "<version>+nobuildinfo" when build
// info is unavailable (e.g. `go run`). Computed once via sync.Once
// would be overkill — this runs exactly twice per process at most
// (startup + cache integration).
func cacheToolVersion() string {
	v := effectiveVersion()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v + "+nobuildinfo"
	}
	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return v + "+nobuildinfo"
	}
	short := rev
	if len(short) > 12 {
		short = short[:12]
	}
	if modified {
		return v + "+" + short + ".dirty"
	}
	return v + "+" + short
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "gomutants: %v\n", err)
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}

// exitError carries a specific exit code through run()'s error return so
// main can map it to os.Exit. Matches gremlins's exit-code surface (10
// for efficacy, 11 for mutant coverage) so gremlins-compat scripts keep
// distinguishing the two failure modes.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// stdout is the writer for user-facing output. Tests swap this to capture output.
var stdout io.Writer = os.Stdout

// stderr is the writer for warnings. Tests swap this to capture output.
var stderr io.Writer = os.Stderr

// buildTestMapFunc is the per-test coverage map builder. Swappable so
// tests can drive the warning/skip path without engineering a real
// `go test -list` failure.
var buildTestMapFunc = coverage.BuildTestMap

// runCoverageFunc / measureBaselineFunc / parseProfileFunc / preReadFilesFunc
// indirect through swappable variables so tests can selectively fail
// individual pipeline steps and lock down each err-return wrap.
var (
	runCoverageFunc     = runner.RunCoverage
	measureBaselineFunc = runner.MeasureBaseline
	parseBytesFunc      = coverage.ParseBytes
	preReadFilesFunc    = discover.PreReadFiles
	mkdirTempFunc       = os.MkdirTemp
	getwdFunc           = os.Getwd
	cacheLoadFunc       = cache.Load
	cacheSaveFunc       = cache.Save
	resolveCoverPkgFunc = discover.ResolvePackages
	goVersionFunc       = runGoVersion
)

// phaseDurationDisplay rounds a duration to 100ms precision for display
// in phase-done lines. Extracted so the rounding constant is testable
// without parsing fmt output: ARITHMETIC mutations on `100*time.Millisecond`
// (e.g. `*` → `/`, which collapses to 0 and disables rounding) surface as
// observable changes in the returned Duration.
func phaseDurationDisplay(d time.Duration) time.Duration {
	return d.Round(100 * time.Millisecond)
}

func run(ctx context.Context, args []string) error {
	// Strip "unleash" for gremlins CLI compat.
	if len(args) > 0 && args[0] == "unleash" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("gomutants", flag.ContinueOnError)

	var (
		workers            int
		testCPU            int
		timeoutCoefficient int
		timeoutMargin      float64
		timeoutMin         time.Duration
		adaptiveTimeout    config.AdaptiveTimeoutFlag
		detectEquivalent   config.DetectEquivalentFlag
		checkpointInterval config.CheckpointIntervalFlag
		coverPkg           string
		tags               string
		output             string
		configPath         string
		disable            string
		only               string
		excludeFiles       string
		changedSince       string
		cachePath          string
		annotations        string
		strykerOutput      string
		htmlOutput         string
		thresholdEfficacy  float64
		thresholdMcover    float64
		dryRun             bool
		verbose            bool
		quiet              bool
		showVersion        bool
	)

	fs.IntVar(&workers, "workers", 0, "parallel workers (default: NumCPU)")
	fs.IntVar(&workers, "w", 0, "parallel workers (shorthand)")
	fs.IntVar(&testCPU, "test-cpu", 0, "value passed to inner go test -cpu per mutant (0 omits the flag; go test then uses GOMAXPROCS)")
	fs.IntVar(&timeoutCoefficient, "timeout-coefficient", 0, "multiply baseline test time for the global timeout ceiling (default: 10)")
	fs.Float64Var(&timeoutMargin, "timeout-margin", 0, fmt.Sprintf("scale per-test sums into the per-mutant adaptive timeout (default: %g)", config.DefaultTimeoutMargin))
	fs.DurationVar(&timeoutMin, "timeout-min", 0, fmt.Sprintf("floor for the per-mutant adaptive timeout (default: %s)", config.DefaultTimeoutMin))
	// BoolFunc lets us distinguish "user set --adaptive-timeout=false"
	// from "user did not pass the flag" — the merge layer in
	// (*Config).ApplyFlags relies on the .Set bit to override YAML.
	fs.BoolFunc("adaptive-timeout", "use per-test durations to size each mutant's timeout (default: true; pass =false to disable)", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("--adaptive-timeout: %w", err)
		}
		adaptiveTimeout = config.AdaptiveTimeoutFlag{Set: true, Value: v}
		return nil
	})
	// Opt-in TCE pass. BoolFunc (like --adaptive-timeout) so ApplyFlags can
	// tell "not provided" from an explicit value via the .Set bit.
	fs.BoolFunc("detect-equivalent", "after testing, compile each surviving mutant with -gcflags=-S and mark it EQUIVALENT when the generated assembly is identical to the original (Trivial Compiler Equivalence; default false, adds one package compile per survivor)", func(s string) error {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("--detect-equivalent: %w", err)
		}
		detectEquivalent = config.DetectEquivalentFlag{Set: true, Value: v}
		return nil
	})
	// fs.Func (like the BoolFunc above) lets us distinguish "user set
	// --checkpoint-interval" from "user did not pass the flag", which
	// (*Config).ApplyFlags needs because 0 is a valid value (disable) and
	// can't be told apart from the unset zero value otherwise.
	fs.Func("checkpoint-interval", fmt.Sprintf("how often to flush completed mutant outcomes to the cache mid-run so a hard kill (OOM, CI timeout, SIGKILL) loses at most this much progress; 0 disables (default: %s)", config.DefaultCheckpointInterval), func(s string) error {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("--checkpoint-interval: %w", err)
		}
		if d < 0 {
			return fmt.Errorf("--checkpoint-interval must be >= 0, got %s", d)
		}
		checkpointInterval = config.CheckpointIntervalFlag{Set: true, Value: d}
		return nil
	})
	fs.StringVar(&coverPkg, "coverpkg", "", "coverage package pattern")
	fs.StringVar(&tags, "tags", "", "comma-separated build tags forwarded as -tags to the inner go list/go test (gremlins-compat)")
	fs.StringVar(&output, "output", "", "JSON report path")
	fs.StringVar(&output, "o", "", "JSON report path (shorthand)")
	fs.StringVar(&configPath, "config", ".gomutants.yml", "config file path")
	fs.StringVar(&disable, "disable", "", "comma-separated mutator types to disable")
	fs.StringVar(&only, "only", "", "comma-separated mutator types to run (disables all others)")
	fs.StringVar(&excludeFiles, "exclude-files", "", "comma-separated regexps; skip mutating production files whose module-relative path matches any (e.g. \"vendor/,_gen\\\\.go$\")")
	fs.StringVar(&changedSince, "changed-since", "", "only test mutants on lines changed vs git ref (e.g. main, HEAD~1)")
	fs.StringVar(&cachePath, "cache", "", "path to incremental-analysis cache file; skips mutants whose source and tests are byte-identical to the cached run. Default .gomutants-cache.json. Pass --cache=off to disable")
	fs.StringVar(&annotations, "annotations", "", "emit annotations for surviving mutants (values: github)")
	fs.StringVar(&strykerOutput, "stryker-output", "", "also write a Stryker mutation-testing-elements report at this path (HTML viewer / dashboard)")
	fs.StringVar(&htmlOutput, "html-output", "", "also write a self-contained interactive HTML mutation report at this path (Stryker mutation-testing-elements viewer, no network deps)")
	fs.Float64Var(&thresholdEfficacy, "threshold-efficacy", 0, "minimum test efficacy %% (KILLED/(KILLED+LIVED)); exit 10 if not met. 0 disables (gremlins-compat)")
	fs.Float64Var(&thresholdMcover, "threshold-mcover", 0, "minimum mutant coverage %% ((KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)); exit 11 if not met. 0 disables (gremlins-compat)")
	fs.BoolVar(&dryRun, "dry-run", false, "list mutants without testing")
	fs.BoolVar(&verbose, "verbose", false, "show each mutant as tested")
	fs.BoolVar(&verbose, "v", false, "verbose (shorthand)")
	fs.BoolVar(&quiet, "quiet", false, "suppress header, phase lines, and per-mutant progress; only the final summary prints (warnings still go to stderr)")
	fs.BoolVar(&quiet, "q", false, "quiet (shorthand)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if testCPU < 0 {
		return fmt.Errorf("--test-cpu must be >= 0, got %d", testCPU)
	}

	if quiet && verbose {
		return fmt.Errorf("--quiet and --verbose cannot be used together")
	}

	switch annotations {
	case "", "github":
	default:
		return fmt.Errorf("--annotations=%q not recognized (supported: github)", annotations)
	}

	if showVersion {
		fmt.Fprint(stdout, formatVersion())
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.ApplyFlags(config.Flags{
		Workers:            workers,
		TestCPU:            testCPU,
		TimeoutCoefficient: timeoutCoefficient,
		TimeoutMargin:      timeoutMargin,
		TimeoutMin:         timeoutMin,
		AdaptiveTimeout:    adaptiveTimeout,
		DetectEquivalent:   detectEquivalent,
		CheckpointInterval: checkpointInterval,
		CoverPkg:           coverPkg,
		Tags:               tags,
		Output:             output,
		Disable:            disable,
		Only:               only,
		ExcludeFiles:       excludeFiles,
		ChangedSince:       changedSince,
		Cache:              cachePath,
		DryRun:             dryRun,
		Verbose:            verbose,
		Quiet:              quiet,
	})
	cfg.ResolveCache()

	// Periodic checkpointing rides on the cache file; with --cache=off
	// there is nothing to flush. Warn rather than silently ignore so a
	// user who set --checkpoint-interval isn't misled about durability.
	if cfg.Cache == "" && checkpointInterval.Set {
		fmt.Fprintln(stderr, "gomutants: --checkpoint-interval ignored: --cache is off")
	}

	packages := fs.Args()
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	// Determine project directory (current working directory).
	projectDir, err := getwdFunc()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Read go module name from go.mod.
	goModule, err := readModuleName(projectDir)
	if err != nil {
		return err
	}

	// Load the cache early so the coverage phase can short-circuit on a
	// matching profile key. The per-mutant Lookup runs later off the same
	// *Cache; load failures fall through to a nil cache, which the rest
	// of the pipeline treats as "no cache at all". A single Hasher is
	// reused across the coverage-key calc and Lookup so per-file sha256s
	// are memoized only once.
	var (
		loadedCache  *cache.Cache
		hasher       *cache.Hasher
		testFilesFor cache.TestFilesForFn
		// goToolchain fingerprints the project's `go` (its `go version`
		// string). It joins the cache metadata gate because EQUIVALENT
		// verdicts are decided by the compiler, and feeds the coverage-key
		// toolchain dimension below. Computed once, only when caching is on.
		goToolchain string
	)
	if cfg.Cache != "" {
		goToolchain = goVersionFunc(ctx)
		loadedCache = cacheLoadFunc(cfg.Cache, goModule, cacheToolVersion(), cfg.Tags, goToolchain)
		// Hasher is created before discovery's PreReadFiles so the
		// coverage-key calc can use it. SetSrcCache is called once
		// the in-memory source map exists (after step 6), so
		// per-mutant Lookup's prodHash calls reuse already-loaded bytes.
		hasher = cache.NewHasher(nil)
	}

	// Get enabled mutators. Validate names first so a typo in --only /
	// --disable (or in the config file) surfaces as a stderr warning
	// instead of a silent filter miss. EnabledMutators already ignores
	// unknown names; warning is purely additive.
	reg := mutator.NewRegistry()
	for _, n := range reg.UnknownNames(cfg.Only) {
		fmt.Fprintf(stderr, "gomutants: unknown mutator %q in --only (ignored)\n", n)
	}
	for _, n := range reg.UnknownNames(cfg.Disable) {
		fmt.Fprintf(stderr, "gomutants: unknown mutator %q in --disable (ignored)\n", n)
	}
	enabledMutators := reg.EnabledMutators(cfg.Only, cfg.Disable)

	term := report.NewTerminal(stdout, 0, cfg.Verbose, cfg.Quiet)
	term.Header(effectiveVersion(), fmt.Sprintf("%v", packages), cfg.Workers, len(enabledMutators))

	// 1. Resolve packages.
	term.Phase("Resolving packages...")
	pkgs, err := discover.ResolvePackages(ctx, projectDir, packages, cfg.Tags)
	if err != nil {
		return err
	}
	excluder, err := discover.NewExcluder(cfg.ExcludeFiles)
	if err != nil {
		return fmt.Errorf("--exclude-files: %w", err)
	}
	pkgs, excludedFiles := discover.ApplyExcludes(pkgs, excluder, projectDir)
	resolveMsg := fmt.Sprintf("done (%d packages)", len(pkgs))
	if excludedFiles > 0 {
		resolveMsg = fmt.Sprintf("done (%d packages, %d files excluded)", len(pkgs), excludedFiles)
	}
	term.PhaseDone(resolveMsg)

	// 2. Create temp directory.
	tmpDir, err := mkdirTempFunc("", "gomutants-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// 3. Collect coverage. With --cache enabled, the profile is memoized
	// under a content-hash key that fingerprints every input that can
	// change `go test -coverprofile` output (sources, go.mod/sum, toolchain,
	// env, -coverpkg). A key match parses the cached profile in-process and
	// skips the multi-second `go test` invocation.
	term.Phase("Collecting coverage...")
	coverStart := time.Now()

	var (
		profile        *coverage.Profile
		profileBytes   []byte
		coverageKey    string
		coverFromCache bool
	)
	if loadedCache != nil {
		// Hash failures (unreadable file/dir) fall through to a fresh
		// coverage run rather than aborting — same conservative policy
		// as the per-mutant Lookup path.
		if pkgDirs, derr := coveragePkgDirs(ctx, projectDir, pkgs, cfg.CoverPkg, cfg.Tags); derr == nil {
			toolchain := fmt.Sprintf("gomutants/%s|go/%s", runtime.Version(), goToolchain)
			if k, herr := hasher.HashCoverageInputs(pkgDirs, projectDir, cfg.CoverPkg, cfg.Tags, toolchain, captureCoverageEnv()); herr == nil {
				coverageKey = k
			}
		}
		if coverageKey != "" && coverageKey == loadedCache.CoverageKey && loadedCache.CoverageProfile != "" {
			if p, perr := parseBytesFunc([]byte(loadedCache.CoverageProfile)); perr == nil {
				profile = p
				profileBytes = []byte(loadedCache.CoverageProfile)
				coverFromCache = true
			}
		}
	}

	if profile == nil {
		profilePath, rerr := runCoverageFunc(ctx, projectDir, packages, cfg.CoverPkg, cfg.Tags, tmpDir)
		if rerr != nil {
			return rerr
		}
		// Read the profile bytes once and reuse them for parsing + cache
		// persistence. Avoids a second file read at cache-write time.
		bs, rerr := os.ReadFile(profilePath)
		if rerr != nil {
			return fmt.Errorf("reading coverage profile: %w", rerr)
		}
		p, perr := parseBytesFunc(bs)
		if perr != nil {
			return perr
		}
		profile = p
		profileBytes = bs
	}

	coverSuffix := ""
	if coverFromCache {
		coverSuffix = ", cached"
	}
	term.PhaseDone(fmt.Sprintf("done (%s%s)", phaseDurationDisplay(time.Since(coverStart)), coverSuffix))

	// 4. Measure baseline test duration.
	term.Phase("Measuring baseline...")
	baseline, err := measureBaselineFunc(ctx, projectDir, packages, cfg.Tags)
	if err != nil {
		return err
	}
	testTimeout := baseline * time.Duration(cfg.TimeoutCoefficient)
	// Distinguish the displayed value: with adaptive sizing, testTimeout
	// is the upper-bound ceiling, not the deadline every mutant gets.
	// Without this, users see "timeout: 4m" and assume each mutant has
	// 4 minutes; in reality fast packages get sub-second deadlines.
	timeoutLabel := "timeout"
	if cfg.AdaptiveTimeoutEnabled() {
		timeoutLabel = "ceiling"
	}
	term.PhaseDone(fmt.Sprintf("done (%s, %s: %s)", phaseDurationDisplay(baseline), timeoutLabel, testTimeout.Round(time.Second)))

	// 5. Discover mutants.
	term.Phase("Discovering mutants...")
	fset := token.NewFileSet()
	discovered := discover.Discover(fset, pkgs, enabledMutators, projectDir, goModule)
	mutants := discovered.Mutants
	if cfg.ChangedSince != "" {
		gitRoot, err := discover.GitRoot(ctx, projectDir)
		if err != nil {
			return fmt.Errorf("--changed-since requires a git repository: %w", err)
		}
		ranges, err := discover.RunGitDiff(ctx, projectDir, cfg.ChangedSince)
		if err != nil {
			return err
		}
		mutants = discover.FilterByDiff(mutants, ranges, gitRoot)
	}
	discover.FilterByCoverage(mutants, profile, pkgs, goModule)

	mutants, suppressed, err := discover.FilterByDirectivesWithCache(fset, mutants, discovered.Files)
	if err != nil {
		return fmt.Errorf("applying directives: %w", err)
	}
	if cfg.Verbose {
		for _, s := range suppressed {
			reason := s.Reason
			if reason == "" {
				reason = "no reason"
			}
			fmt.Fprintf(stderr, "suppressed %s at %s:%d (%s)\n",
				s.Mutant.Type, s.Mutant.RelFile, s.Mutant.Line, reason)
		}
	}

	pendingCount := 0
	notCoveredCount := 0
	for _, m := range mutants {
		switch m.Status {
		case mutator.StatusPending:
			pendingCount++
		case mutator.StatusNotCovered:
			notCoveredCount++
		}
	}
	term.PhaseDone(fmt.Sprintf("%d found (%d not covered, %d to test)", len(mutants), notCoveredCount, pendingCount))

	if cfg.DryRun {
		for _, m := range mutants {
			fmt.Fprintf(stdout, "[%s] %s:%d:%d  %s → %s  (%s)\n",
				m.Status.String(), m.RelFile, m.Line, m.Col,
				m.Original, m.Replacement, m.Type)
		}
		return nil
	}

	// 6. Pre-read source files.
	srcCache, err := preReadFilesFunc(pkgs)
	if err != nil {
		return fmt.Errorf("pre-reading source files: %w", err)
	}
	if hasher != nil {
		// Hasher was created early (before PreReadFiles) for the
		// coverage-key calc; attach the in-memory source map now so
		// per-mutant Lookup's prodHash calls skip disk reads. Files
		// hashed during the coverage-key phase remain in the hasher's
		// internal memo, so this only affects newly seen paths.
		hasher.SetSrcCache(srcCache)
	}

	// 7. Build per-test coverage map.
	term.Phase("Building per-test coverage map...")
	testMap, err := buildTestMapFunc(ctx, projectDir, packages, cfg.CoverPkg, cfg.Tags, tmpDir, cfg.Workers)
	if err != nil {
		// Non-fatal: fall back to running all tests per mutant.
		fmt.Fprintf(stderr, "warning: per-test coverage map failed: %v\n", err)
		testMap = nil
		term.PhaseDone("skipped (will run all tests per mutant)")
	} else {
		term.PhaseDone("done")
	}

	// 7a. Apply incremental-analysis cache (opt-in via --cache). Hits
	// flip the mutant from Pending to its prior terminal status, which
	// makes the runner's Pending-only filter naturally skip them.
	// loadedCache + hasher were created at module-read time so the
	// coverage phase could already consult them — here we just build
	// the test-files resolver and run the lookup.
	if loadedCache != nil {
		// TestIndex is built from every package's directory so cross-
		// package coverage (tests in pkg B exercising code in pkg A
		// via -coverpkg) resolves correctly.
		pkgDirs := make([]string, 0, len(pkgs))
		for _, p := range pkgs {
			pkgDirs = append(pkgDirs, p.Dir)
		}
		testIndex := cache.BuildTestIndex(pkgDirs)

		// Resolver: prefer the per-test coverage map's covering set,
		// mapped through the index to defining files. When the map is
		// nil, has no entry for this mutant, or none of the covering
		// names resolve to a known file, fall back to every _test.go
		// in the mutant's package directory — sound but coarser.
		testFilesFor = func(m mutator.Mutant) []string {
			pkgDir := filepath.Dir(m.File)
			if testMap == nil {
				return testIndex.AllInDir(pkgDir)
			}
			names := testMap.TestsFor(m.CoverageFile, m.Line)
			if len(names) == 0 {
				return testIndex.AllInDir(pkgDir)
			}
			seen := make(map[string]bool, len(names))
			var files []string
			for _, n := range names {
				for _, f := range testIndex.FilesFor(n) {
					if !seen[f] {
						seen[f] = true
						files = append(files, f)
					}
				}
			}
			if len(files) == 0 {
				return testIndex.AllInDir(pkgDir)
			}
			return files
		}

		if hits := loadedCache.Lookup(mutants, hasher, testFilesFor); hits > 0 {
			pendingCount -= hits
			// When equivalence detection is off this run, a cached EQUIVALENT
			// must not surface — report the survivor honestly as LIVED. The
			// reuse already validated prod+tests hashes (EQUIVALENT needs a
			// tests hash), so a LIVED reading is sound.
			if !cfg.DetectEquivalentEnabled() {
				for i := range mutants {
					if mutants[i].Status == mutator.StatusEquivalent {
						mutants[i].Status = mutator.StatusLived
					}
				}
			}
			if !cfg.Quiet {
				fmt.Fprintf(stdout, "Cache: %d mutant outcomes reused from %s\n", hits, cfg.Cache)
			}
		}
	}

	// 8. Run mutation testing. pool.Run mutates the slice in place.
	// TimeoutPolicy resolves per-mutant deadlines from the per-test
	// durations recorded on the testMap, falling back to the global
	// baseline×coefficient ceiling. testTimeout stays the absolute cap.
	policy := runner.TimeoutPolicy{
		Global:   testTimeout,
		Margin:   cfg.TimeoutMargin,
		Min:      cfg.TimeoutMin,
		Adaptive: cfg.AdaptiveTimeoutEnabled(),
	}
	term2 := report.NewTerminal(stdout, pendingCount, cfg.Verbose, cfg.Quiet)
	// Idle "(compiling)" heartbeat so the TTY doesn't sit silent during
	// the first per-package go-test compile (no OnResult until the first
	// mutant completes). First OnResult auto-stops it; the defer covers
	// the all-cached / zero-pending paths where OnResult never fires.
	term2.StartHeartbeat()
	defer term2.StopHeartbeat()

	// Stamp the coverage memo once, before the run loop: the profile and
	// its key are fixed for the whole run, so there's no reason to
	// re-serialize the (potentially large) profile on every checkpoint.
	// An empty coverageKey means hashing failed earlier and we silently
	// fell back to a fresh run; don't poison the cache with a missing key.
	if loadedCache != nil && coverageKey != "" && len(profileBytes) > 0 {
		loadedCache.CoverageKey = coverageKey
		loadedCache.CoverageProfile = string(profileBytes)
	}

	// 8a. checkpoint flushes completed mutant outcomes to the cache file,
	// throttled to cfg.CheckpointInterval. cache.Update only emits
	// terminal-status mutants, so flushing a partially-complete slice
	// mid-run is safe — pending mutants are simply omitted. No locking
	// needed: pool.Run invokes onResult from a single goroutine, so the
	// mutants-slice reads here are serialized with the collector's writes.
	// Write failures are non-fatal — a stale cache only costs speed.
	var lastCheckpoint time.Time
	checkpoint := func(force bool) {
		if loadedCache == nil {
			return
		}
		if !force && cfg.CheckpointInterval <= 0 {
			return
		}
		if !force && time.Since(lastCheckpoint) < cfg.CheckpointInterval {
			return
		}
		loadedCache.Update(mutants, hasher, projectDir, testFilesFor)
		if err := cacheSaveFunc(loadedCache, cfg.Cache); err != nil {
			fmt.Fprintf(stderr, "warning: writing cache to %s: %v\n", cfg.Cache, err)
			return // leave lastCheckpoint stale so the next onResult retries
		}
		lastCheckpoint = time.Now()
	}

	pool := runner.NewPool(cfg.Workers, runner.ExecOpts{TestCPU: cfg.TestCPU, Tags: cfg.Tags}, policy, tmpDir, srcCache, projectDir, testMap)
	// Seed lastCheckpoint so the first periodic checkpoint fires one full
	// interval into the run, not on the very first mutant.
	lastCheckpoint = time.Now()
	pool.Run(ctx, mutants, func(m mutator.Mutant) {
		term2.OnResult(m)
		checkpoint(false)
	})

	// 8b. Trivial Compiler Equivalence pass (opt-in). Recompile each
	// surviving (LIVED) mutant with package-scoped `-gcflags=-S` and
	// reclassify it as EQUIVALENT when the assembly matches the original —
	// a compiler-proven non-gap. Runs before the final checkpoint so the
	// verdicts are cached. Cached EQUIVALENT survivors stay EQUIVALENT and
	// are skipped (their Status is no longer LIVED).
	if cfg.DetectEquivalentEnabled() {
		term.Phase("Detecting equivalent mutants...")
		det := tce.NewDetector(projectDir, cfg.Tags, srcCache)
		equiv := det.Run(ctx, mutants, cfg.Workers, tmpDir, nil)
		term.PhaseDone(fmt.Sprintf("%d equivalent", equiv))
	}

	// Final flush. force=true bypasses the throttle and the disable
	// switch, so even --checkpoint-interval=0 still writes the cache once.
	checkpoint(true)

	// 9. Generate report.
	totalElapsed := time.Since(coverStart)
	r := report.Generate(mutants, goModule, totalElapsed, len(suppressed))
	term2.Summary(r)

	if err := report.WriteJSON(r, cfg.Output); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	if !cfg.Quiet {
		fmt.Fprintf(stdout, "Report: %s\n", cfg.Output)
	}

	if strykerOutput != "" {
		if err := report.WriteStryker(strykerOutput, mutants, projectDir, effectiveVersion()); err != nil {
			return fmt.Errorf("writing Stryker report: %w", err)
		}
		if !cfg.Quiet {
			fmt.Fprintf(stdout, "Stryker report: %s\n", strykerOutput)
		}
	}

	if htmlOutput != "" {
		if err := report.WriteHTML(htmlOutput, mutants, projectDir, effectiveVersion()); err != nil {
			return fmt.Errorf("writing HTML report: %w", err)
		}
		fmt.Fprintf(stdout, "HTML report: %s\n", htmlOutput)
	}

	if annotations == "github" {
		if err := report.WriteGitHubAnnotations(stdout, r); err != nil {
			return fmt.Errorf("writing annotations: %w", err)
		}
	}

	// Threshold gates. Exit codes 10/11 match gremlins's surface so scripts
	// that distinguish the two failure modes keep working. Mutant coverage
	// uses the gremlins formula (KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED);
	// r.MutationsCoverage in the JSON uses a different denominator and is
	// kept as-is for backward-compat with existing report consumers.
	//
	// We deviate from gremlins on two points: a gate is *skipped* (with a
	// stderr note) when its denominator is zero — empty discovery is almost
	// always a config issue, not a test-quality issue, and reporting "0.00%
	// below 80.00%" hides that. Error messages always include both
	// percentages so a single read shows the full state.
	// EQUIVALENT mutants are neither KILLED nor LIVED, so they fall out of
	// both gates' denominators here — a compiler-proven non-gap shouldn't
	// move efficacy or mutant coverage in either direction.
	tested := r.MutantsKilled + r.MutantsLived
	mcoverDenom := tested + r.MutantsNotCovered
	mcover := 0.0
	if mcoverDenom > 0 {
		mcover = float64(tested) / float64(mcoverDenom) * 100
	}

	if thresholdEfficacy > 0 {
		if tested == 0 {
			fmt.Fprintln(stderr, "gomutants: no testable mutants discovered; --threshold-efficacy not evaluated")
		} else if r.TestEfficacy < thresholdEfficacy {
			return &exitError{
				code: 10,
				msg:  fmt.Sprintf("test efficacy %.2f%% below --threshold-efficacy=%.2f%% (mutant coverage: %.2f%%)", r.TestEfficacy, thresholdEfficacy, mcover),
			}
		}
	}
	if thresholdMcover > 0 {
		if mcoverDenom == 0 {
			fmt.Fprintln(stderr, "gomutants: no covered or testable mutants discovered; --threshold-mcover not evaluated")
		} else if mcover < thresholdMcover {
			return &exitError{
				code: 11,
				msg:  fmt.Sprintf("mutant coverage %.2f%% below --threshold-mcover=%.2f%% (test efficacy: %.2f%%)", mcover, thresholdMcover, r.TestEfficacy),
			}
		}
	}
	return nil
}

// runGoVersion shells out to `go version` once so the coverage cache key
// can fingerprint the toolchain that actually compiles the tests
// (independent of runtime.Version() for the gomutants binary itself).
// Swappable via goVersionFunc for tests that want deterministic output.
//
// Returns a sentinel on failure so the "go binary unavailable" mode is
// distinct from any conceivable successful empty output — collapsing
// both into "" would let the cache survive a toolchain swap if the
// other inputs happen to stay constant.
func runGoVersion(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "go", "version")
	out, err := cmd.Output()
	if err != nil {
		return "go-version-unavailable"
	}
	return strings.TrimSpace(string(out))
}

// captureCoverageEnv returns an allowlisted snapshot of go-related env
// vars that can change the coverage profile. Restricted to a small set
// so that irrelevant churn (GOPROXY rotation, GOCACHE path changes)
// doesn't invalidate the cache. New entries must actually affect what
// `go test -coverprofile` produces.
func captureCoverageEnv() string {
	keys := []string{"GOEXPERIMENT", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"}
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s|", k, os.Getenv(k))
	}
	return b.String()
}

// coveragePkgDirs returns the set of package directories whose source
// could land in the coverage profile. With cfg.CoverPkg unset (or
// matching the target patterns), this is exactly `pkgs`. With a
// broader -coverpkg pattern, we resolve it separately so the hash
// covers every package that go test will instrument.
func coveragePkgDirs(ctx context.Context, projectDir string, pkgs []discover.Package, coverPkg, tags string) ([]string, error) {
	dirs := make([]string, 0, len(pkgs))
	seen := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		if !seen[p.Dir] {
			seen[p.Dir] = true
			dirs = append(dirs, p.Dir)
		}
	}
	if coverPkg == "" {
		return dirs, nil
	}
	expanded, err := resolveCoverPkgFunc(ctx, projectDir, []string{coverPkg}, tags)
	if err != nil {
		return nil, err
	}
	for _, p := range expanded {
		if !seen[p.Dir] {
			seen[p.Dir] = true
			dirs = append(dirs, p.Dir)
		}
	}
	return dirs, nil
}

// readModuleName extracts the module path from go.mod by scanning for
// the first `module <path>` line. Tolerates tabs and multiple spaces
// between the directive and the path, and ignores inline comments
// (`module foo // comment`) — fields[1] is always the path for a
// well-formed go.mod. Prefer golang.org/x/mod/modfile if parsing ever
// needs to handle deprecation markers, the rare `module ( ... )` block
// form, or quoted module paths.
func readModuleName(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	// bufio.Scanner caps lines at 64 KiB by default. A pathological
	// go.mod with a longer-than-cap line before the module directive
	// would surface as bufio.ErrTooLong here — we propagate it rather
	// than silently returning "module name not found", which would
	// mislead the user about what's wrong.
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("scanning go.mod: %w", err)
	}
	return "", fmt.Errorf("module name not found in go.mod")
}
