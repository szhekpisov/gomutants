package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/szhekpisov/gomutants/internal/config"
	"github.com/szhekpisov/gomutants/internal/coverage"
	"github.com/szhekpisov/gomutants/internal/discover"
	"github.com/szhekpisov/gomutants/internal/mutator"
	"github.com/szhekpisov/gomutants/internal/report"
	"github.com/szhekpisov/gomutants/internal/runner"
)

const version = "0.1.0"

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
		coverPkg           string
		output             string
		configPath         string
		disable            string
		only               string
		changedSince       string
		annotations        string
		strykerOutput      string
		thresholdEfficacy  float64
		thresholdMcover    float64
		dryRun             bool
		verbose            bool
		showVersion        bool
	)

	fs.IntVar(&workers, "workers", 0, "parallel workers (default: NumCPU)")
	fs.IntVar(&workers, "w", 0, "parallel workers (shorthand)")
	fs.IntVar(&testCPU, "test-cpu", 0, "value passed to inner go test -cpu per mutant (0 omits the flag; go test then uses GOMAXPROCS)")
	fs.IntVar(&timeoutCoefficient, "timeout-coefficient", 0, "multiply baseline test time (default: 10)")
	fs.StringVar(&coverPkg, "coverpkg", "", "coverage package pattern")
	fs.StringVar(&output, "output", "", "JSON report path")
	fs.StringVar(&output, "o", "", "JSON report path (shorthand)")
	fs.StringVar(&configPath, "config", ".gomutants.yml", "config file path")
	fs.StringVar(&disable, "disable", "", "comma-separated mutator types to disable")
	fs.StringVar(&only, "only", "", "comma-separated mutator types to run (disables all others)")
	fs.StringVar(&changedSince, "changed-since", "", "only test mutants on lines changed vs git ref (e.g. main, HEAD~1)")
	fs.StringVar(&annotations, "annotations", "", "emit annotations for surviving mutants (values: github)")
	fs.StringVar(&strykerOutput, "stryker-output", "", "also write a Stryker mutation-testing-elements report at this path (HTML viewer / dashboard)")
	fs.Float64Var(&thresholdEfficacy, "threshold-efficacy", 0, "minimum test efficacy %% (KILLED/(KILLED+LIVED)); exit 10 if not met. 0 disables (gremlins-compat)")
	fs.Float64Var(&thresholdMcover, "threshold-mcover", 0, "minimum mutant coverage %% ((KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)); exit 11 if not met. 0 disables (gremlins-compat)")
	fs.BoolVar(&dryRun, "dry-run", false, "list mutants without testing")
	fs.BoolVar(&verbose, "verbose", false, "show each mutant as tested")
	fs.BoolVar(&verbose, "v", false, "verbose (shorthand)")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if testCPU < 0 {
		return fmt.Errorf("--test-cpu must be >= 0, got %d", testCPU)
	}

	switch annotations {
	case "", "github":
	default:
		return fmt.Errorf("--annotations=%q not recognized (supported: github)", annotations)
	}

	if showVersion {
		fmt.Fprintf(stdout, "gomutants v%s\n", version)
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.ApplyFlags(workers, testCPU, timeoutCoefficient, coverPkg, output, disable, only, changedSince, dryRun, verbose)

	packages := fs.Args()
	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	// Determine project directory (current working directory).
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Read go module name from go.mod.
	goModule, err := readModuleName(projectDir)
	if err != nil {
		return err
	}

	// Get enabled mutators.
	reg := mutator.NewRegistry()
	enabledMutators := reg.EnabledMutators(cfg.Only, cfg.Disable)

	term := report.NewTerminal(stdout, 0, cfg.Verbose)
	term.Header(version, fmt.Sprintf("%v", packages), cfg.Workers, len(enabledMutators))

	// 1. Resolve packages.
	term.Phase("Resolving packages...")
	pkgs, err := discover.ResolvePackages(ctx, projectDir, packages)
	if err != nil {
		return err
	}
	term.PhaseDone(fmt.Sprintf("done (%d packages)", len(pkgs)))

	// 2. Create temp directory.
	tmpDir, err := os.MkdirTemp("", "gomutants-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// 3. Collect coverage.
	term.Phase("Collecting coverage...")
	coverStart := time.Now()
	profilePath, err := runner.RunCoverage(ctx, projectDir, packages, cfg.CoverPkg, tmpDir)
	if err != nil {
		return err
	}
	profile, err := coverage.ParseFile(profilePath)
	if err != nil {
		return err
	}
	term.PhaseDone(fmt.Sprintf("done (%s)", time.Since(coverStart).Round(100*time.Millisecond)))

	// 4. Measure baseline test duration.
	term.Phase("Measuring baseline...")
	baseline, err := runner.MeasureBaseline(ctx, projectDir, packages)
	if err != nil {
		return err
	}
	testTimeout := baseline * time.Duration(cfg.TimeoutCoefficient)
	term.PhaseDone(fmt.Sprintf("done (%s, timeout: %s)", baseline.Round(100*time.Millisecond), testTimeout.Round(time.Second)))

	// 5. Discover mutants.
	term.Phase("Discovering mutants...")
	fset := token.NewFileSet()
	mutants := discover.Discover(fset, pkgs, enabledMutators, projectDir, goModule)
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
	srcCache, err := discover.PreReadFiles(pkgs)
	if err != nil {
		return fmt.Errorf("pre-reading source files: %w", err)
	}

	// 7. Build per-test coverage map.
	term.Phase("Building per-test coverage map...")
	testMap, err := coverage.BuildTestMap(ctx, projectDir, packages, cfg.CoverPkg, tmpDir, cfg.Workers)
	if err != nil {
		// Non-fatal: fall back to running all tests per mutant.
		fmt.Fprintf(stderr, "warning: per-test coverage map failed: %v\n", err)
		testMap = nil
		term.PhaseDone("skipped (will run all tests per mutant)")
	} else {
		term.PhaseDone("done")
	}

	// 8. Run mutation testing.
	term2 := report.NewTerminal(stdout, pendingCount, cfg.Verbose)
	pool := runner.NewPool(cfg.Workers, cfg.TestCPU, testTimeout, tmpDir, srcCache, projectDir, testMap)
	mutants = pool.Run(ctx, mutants, term2.OnResult)

	// 9. Generate report.
	totalElapsed := time.Since(coverStart)
	r := report.Generate(mutants, goModule, totalElapsed)
	term2.Summary(r)

	if err := report.WriteJSON(r, cfg.Output); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	fmt.Fprintf(stdout, "Report: %s\n", cfg.Output)

	if strykerOutput != "" {
		if err := report.WriteStryker(strykerOutput, mutants, projectDir, version); err != nil {
			return fmt.Errorf("writing Stryker report: %w", err)
		}
		fmt.Fprintf(stdout, "Stryker report: %s\n", strykerOutput)
	}

	if annotations == "github" {
		if err := report.WriteGitHubAnnotations(stdout, r); err != nil {
			return fmt.Errorf("writing annotations: %w", err)
		}
	}

	// Threshold gates (gremlins-compat). Exit codes 10/11 match gremlins so
	// scripts that distinguished the two failure modes keep working. Default
	// 0 disables each gate, also matching gremlins.
	if thresholdEfficacy > 0 && r.TestEfficacy < thresholdEfficacy {
		return &exitError{
			code: 10,
			msg:  fmt.Sprintf("test efficacy %.2f%% below --threshold-efficacy=%.2f%%", r.TestEfficacy, thresholdEfficacy),
		}
	}
	if thresholdMcover > 0 {
		// Compute mutant coverage with the gremlins formula
		// (KILLED+LIVED) / (KILLED+LIVED+NOT_COVERED). r.MutationsCoverage
		// in the JSON uses a slightly different denominator (it folds in
		// NOT_VIABLE/TIMED_OUT) which we keep for backward-compat with the
		// existing report field.
		denom := r.MutantsKilled + r.MutantsLived + r.MutantsNotCovered
		mcover := 0.0
		if denom > 0 {
			mcover = float64(r.MutantsKilled+r.MutantsLived) / float64(denom) * 100
		}
		if mcover < thresholdMcover {
			return &exitError{
				code: 11,
				msg:  fmt.Sprintf("mutant coverage %.2f%% below --threshold-mcover=%.2f%%", mcover, thresholdMcover),
			}
		}
	}
	return nil
}

func readModuleName(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	for _, line := range splitLines(data) {
		// `module foo` — tolerate arbitrary whitespace (tabs, multiple
		// spaces) between the directive and the path. Inline comments
		// (`module foo // comment`) yield extra fields after fields[1]
		// which we ignore — fields[1] is always the module path for a
		// well-formed go.mod. Prefer golang.org/x/mod/modfile if parsing
		// ever needs to handle `go.mod` deprecation markers or version
		// directives.
		fields := strings.Fields(string(line))
		if len(fields) >= 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("module name not found in go.mod")
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := 0
		for i < len(data) && data[i] != '\n' {
			i++
		}
		lines = append(lines, data[:i])
		if i < len(data) {
			i++ // skip \n
		}
		data = data[i:]
	}
	return lines
}
