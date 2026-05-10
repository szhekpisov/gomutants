# Performance

Mutation testing on real, third-party Go packages. This page documents the
methodology and the numbers it produced on two targets — a small fast one
(`google/uuid`) and a larger one (`spf13/cobra`) — so you can reproduce
them on your own hardware. Repo-internal benchmarks live in `benchmarks/`;
this page covers external codebases.

## Targets at a glance

| Target | LOC | Packages | Baseline `go test` | Mutants (gomutants OOB on `.`) |
|---|---:|---:|---:|---:|
| google/uuid | ~2.3k | 1 | ~1.0 s | 464 |
| spf13/cobra | ~16.7k | 2 | ~3.0 s | 1706 |

## Environment

| | |
|---|---|
| Host | macOS 26.3.1, Apple M1 Pro (10 cores) |
| Go | go1.25.7 darwin/arm64 (forced via `GOTOOLCHAIN`; see caveat below) |
| gomutants | v0.2.2 |
| gremlins | v0.6.0 (release tarball) |
| Workers | 10 |
| Hyperfine | 3 runs + 1 warmup on uuid; single-run on cobra (8-min cold runs make repeats expensive) |
| Power | AC, no other CPU-bound load |

## Methodology

Two questions worth answering separately:

1. **Engine speed**: given the same set of mutants, how long does the test
   loop take?
2. **Out-of-the-box experience**: what does a user actually wait for when
   they run the tool with defaults?

The first needs a matched operator set. The second deliberately doesn't.

On `google/uuid` each scenario runs through hyperfine (3 measurements + 1
warmup) and the table reports mean ± stddev. On `spf13/cobra` each cold
scenario is a single timed run because each takes minutes; the warm-cache
scenario still uses hyperfine since each run finishes in seconds.

### Cold runs (cache off)

Both targets use the same three command shapes. The argument is `.` for
gremlins (it does not handle `./...` reliably on either target — see
caveats) and matched to `.` for gomutants on cobra so the comparison covers
the same packages. On `google/uuid` (single root package) `./...` and `.`
are equivalent for gomutants.

```bash
# 1. gremlins, 10 workers, default 5 operators
gremlins unleash --workers 10 --timeout-coefficient 20 \
  --silent --output gremlins.json .

# 2. gomutants out-of-box (10 workers, all default operators)
gomutants -workers 10 --cache=off -quiet \
  -output gomutants-oob.json .

# 3. gomutants restricted to gremlins's 5 default operators
gomutants -workers 10 --cache=off -quiet \
  -only=ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,\
INCREMENT_DECREMENT,INVERT_NEGATIVES \
  -output gomutants-l4l.json .
```

`--cache=off` is important: a partial cache hit would skew gomutants
favourably against a tool with no equivalent. Run 3 (like-for-like) is the
clean engine comparison; run 1 and run 2 together describe the user-facing
experience.

### Warm cache

To show the incremental-development behaviour:

```bash
gomutants -workers 10 -output prime.json .                  # primes cache
hyperfine --runs 3 'gomutants -workers 10 -output run.json .'  # warm runs
```

The cache is byte-keyed on source and tests; an unchanged tree skips the
test loop entirely.

### Computing efficacy

Both tools report a `test_efficacy` field, but they compute the denominator
differently. To make the column comparable, this page uses

```
efficacy = KILLED / (KILLED + LIVED + TIMED_OUT)
```

Including `TIMED_OUT` in the denominator avoids treating a timed-out mutant
as a free pass. Gremlins's JSON otherwise reports 100% in scenarios where a
quarter of its mutants timed out, which overstates the result.

## Results: google/uuid

- Pinned commit: `2d3c2a9cc518326daf99a383f07c4d3c44317e4d`
- Source size: ~2.3k LOC, single root package, ~88% line coverage.
- Baseline `go test ./...`: ~1.0–1.2 s.

A small, self-contained package with no external services, no build-tag
gating on the test path, and no `go` directive in `go.mod`. Cold runs
finish in seconds.

### Wall time

| # | Tool | Mode | Wall time (mean ± σ) |
|---|---|---|---|
| 1 | gremlins v0.6.0 | 10w, 5 default ops, `--timeout-coefficient=20` | 27.48 ± 0.23 s |
| 2 | gomutants v0.2.2 | out-of-box (10w, all ops, cache off) | 77.35 ± 0.30 s |
| 3 | gomutants v0.2.2 | like-for-like (10w, only the 5 ops, cache off) | 29.66 ± 0.28 s |
| 4 | gomutants v0.2.2 | warm cache (10w, all ops, cache on, 2nd+ run) | **3.22 ± 0.02 s** |

### Mutant outcomes

| # | Total | KILLED | LIVED | NOT_COVERED | TIMED_OUT | NOT_VIABLE | Efficacy |
|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | 123 | 65 | 0 | 26 | **32** | 0 | 67.0% |
| 2 | 464 | 306 | 94 | 34 | 12 | 18 | 76.5% |
| 3 | 120 | 91 | 15 | 11 | **3** | 0 | 85.8% |
| 4 | 464 | 306 | 94 | 34 | 12 | 18 | 76.5% |

### Reading the uuid results

- **Engine throughput is roughly comparable on this small target.** On the
  matched operator set (runs 1 vs 3) the two engines finish within 8% of
  each other — 27.5 s vs 29.7 s for ~120 mutants. Mutant-discovery sets
  differ by ~3% (123 vs 120), which is normal AST-visitor variation, not
  an engine difference.

- **Adaptive timeouts dominate result quality.** Under identical workers
  and operator set, gremlins ran 32 / 123 (26%) of its mutants into the
  `--timeout-coefficient=20` ceiling; gomutants ran 3 / 120 (2.5%).
  Gomutants sizes per-mutant timeouts from the package's per-test
  runtimes plus a margin, so worker contention doesn't push tests past a
  fixed ceiling. With gremlins's default coefficient of 10, every covered
  mutant on this target timed out and the run was unusable; the table
  uses 20 as the lowest setting that yields a workable result.

- **More operators do more work.** Out-of-box gomutants (run 2) does
  ~3.8× the mutation work in ~2.8× the time of the matched gremlins run
  because gomutants ships additional operators — `STATEMENT_REMOVE`,
  `BRANCH_IF`, `INVERT_BITWISE`, etc. — that gremlins doesn't have.
  Whether you want that depends on the project; you can always restrict
  via `-only`.

- **Cache changes the loop.** Run 4 is the same workload as run 2 with
  the cache enabled on a re-run: 3.22 s, ~24× faster. This is the
  relevant number for the inner loop of "edit a test, re-run" — not for
  one-shot CI jobs, which should keep `--cache=off`.

## Results: spf13/cobra

- Pinned commit: `ad460ea8f249db69c943a365fb84f3a59042d54e`
- Source size: ~16.7k LOC across two packages (`cobra`, `cobra/doc`).
- Baseline `go test ./... -short`: ~3.0 s.

A larger target that exercises the engine's per-mutant overhead. The
benchmark targets the root `cobra` package only (`.`), to match what
gremlins can actually consume. A separate "full repo" measurement on
`gomutants ./...` is included for context.

Wall times here are single-run measurements, not hyperfine medians: a
single OOB cold run takes ~7 minutes, so 3 runs + 1 warmup × 4 scenarios
would be 70+ minutes of bench time per matrix. Variance on a workload
this size is small relative to wall time, but treat ±5% as the rough
confidence band on these numbers rather than the ±0.3% hyperfine reports
on uuid.

### Wall time

| # | Tool | Mode | Target | Wall time |
|---|---|---|---|---|
| 1 | gremlins v0.6.0 | 10w, 5 default ops, `--timeout-coefficient=20` | `.` | 129.4 s |
| 2 | gomutants v0.2.2 | out-of-box (10w, all ops, cache off) | `.` | 410.2 s |
| 3 | gomutants v0.2.2 | like-for-like (10w, only the 5 ops, cache off) | `.` | **90.0 s** |
| 4 | gomutants v0.2.2 | warm cache (10w, all ops, cache on, 2nd+ run, hyperfine 3×) | `.` | **2.73 ± 0.11 s** |
| — | gomutants v0.2.2 | full repo, OOB (10w, all ops, cache off) | `./...` | 485.0 s (context only) |

### Mutant outcomes

| # | Total | KILLED | LIVED | NOT_COVERED | TIMED_OUT | NOT_VIABLE | Efficacy |
|---|---:|---:|---:|---:|---:|---:|---:|
| 1 | 556 | 402 | 88 | 65 | 1 | 0 | 81.9% |
| 2 | 1706 | 1210 | 192 | 184 | 8 | 112 | 85.8% |
| 3 | 461 | 344 | 39 | 48 | 0 | 30 | 89.8% |
| 4 | 1706 | 1208 | 193 | 184 | 9 | 112 | 85.7% |

### Reading the cobra results

- **Engine ordering flips on bigger packages.** On the like-for-like row
  (1 vs 3), gomutants is **1.44× faster** in wall-clock — 90.0 s vs
  129.4 s — opposite of uuid where gremlins narrowly led. Per-mutant cost
  for gremlins on cobra is ~264 ms (KILLED+LIVED only; ignores
  NOT_COVERED); for gomutants it's ~235 ms. The gap comes from gomutants
  pre-building and reusing the test binary while gremlins fork-execs a
  fresh `go test` (with full compile) per mutant. That compile overhead
  amortizes badly on cobra's larger package.

- **Mutant counts diverge more than on uuid (556 vs 461).** Most of the
  delta is gomutants's pre-filtering: 30 mutants flagged `NOT_VIABLE`
  (won't compile or are syntactic no-ops) that gremlins doesn't filter
  and instead either skips silently or runs and reports as KILLED. The
  remainder (17) is AST-visitor coverage variation. Both KILLED counts
  represent real test work, so the wall-time comparison is still fair.

- **Timeouts are no longer the headline.** Cobra's slower test suite
  means worker contention doesn't push individual mutant runs past the
  20× ceiling — only 1 timeout for gremlins, 0 for gomutants L4L. The
  uuid timeout pattern is specific to fast-test packages where 10
  workers oversubscribe the CPU.

- **Cache lands hard on bigger workloads.** Run 4: 2.73 s vs the 410 s
  cold OOB — **~150× faster**. The full mutant set (1706) is unchanged,
  but the cache is byte-keyed on source and tests, so an unchanged tree
  skips the test loop entirely.

- **Out-of-box: 7 minutes vs 2 minutes.** OOB gomutants takes ~3.2× the
  wall time of OOB gremlins on the same root package, but discovers
  ~3.1× the mutants (1706 vs 556) by running 16 operator types vs 5.
  Throughput per mutant tested is essentially equal between OOB and L4L
  for gomutants (~290 ms vs ~235 ms); the wall-time delta is purely
  workload size. Restrict with `-only` if you don't want it.

## Why warm-cache time doesn't track project size

Cobra's warm-cache run (2.73 s) is *faster* than uuid's (3.22 s), despite
cobra having ~3.7× more mutants and ~7× more LOC. That looks wrong until
you measure where the seconds actually go.

On a warm-cache run, gomutants's per-mutant work is genuinely free — every
mutant short-circuits to its cached status. The wall time is almost
entirely the pre-flight Go toolchain calls that run *before* the cache
lookup:

| Stage | uuid | cobra |
|---|---:|---:|
| `go list -json .` (package metadata) | 0.20 s | 0.09 s |
| `go test -count=1 -coverprofile` (fresh coverage) | 1.24 s | 0.91 s |
| `go test -count=1 .` (baseline timing for adaptive timeout) | 1.44 s | 1.51 s |
| `go test -list . .` (enumerate test fns) | 0.41 s | 0.46 s |
| `go list -f` | 0.05 s | 0.05 s |
| `go test -c -cover` (pre-build test binary) | 0.30 s | 0.35 s |

These were measured under a `go` wrapper, so the per-call durations are
inflated by the wrapper overhead and do not sum to the observed wall time
(some of these calls run in parallel). The ordering of relative cost is
what matters.

The dominant cost is **`go test -count=1 -coverprofile`** — gomutants
always re-collects a fresh coverage profile before consulting the cache,
and `-count=1` defeats Go's test result cache. That command's runtime is
set by the *test suite's wall-clock floor*, not the project's size.
Direct measurement, no wrapper:

```
uuid  go test -count=1 -cover .   real 1.24s   user 1.02s   sys 0.49s
cobra go test -count=1 -cover .   real 0.91s   user 0.67s   sys 0.69s
```

uuid's 59 tests run slower than cobra's 260 because uuid has
wall-clock-gated tests — `TestClockSeqRace`, `TestGetTime`,
`TestNewV6FromTimeGeneratesUniqueUUIDs`, `TestMonotonicTimeNow` — that
have small real-time floors which don't shrink with parallelism. Cobra's
tests are pure CPU-bound command-parsing unit tests that scale across
all 10 cores and finish in 0.91 s despite running ~4× more functions.

So warm-cache wall time is currently a function of test-suite character,
not project size. A future enhancement worth tracking: memoize the
coverage profile on the cache so warm-cache no-op runs skip the
`-count=1` step entirely. That would drop both targets below ~1 s.

## Caveats

- **Gremlins v0.6.0 silently produces zero mutants on Go 1.26.x** (the
  current homebrew default at time of writing). It runs `go test -cover`,
  reports a sub-second elapsed time, and exits with "No results to
  report." Forcing `GOTOOLCHAIN=go1.25.7` produces real numbers. This page
  uses 1.25.7 for both tools so they're benchmarked on the same toolchain;
  gomutants runs fine on 1.26 as well.
- **`./...` vs `.`**: gremlins on this build prints "no results" silently
  when given `./...` on either target and works only with `.`. The
  benchmarks use `.` throughout for gremlins. For uuid that's the entire
  module (one package); for cobra it's the root package only and the
  `cobra/doc` subpackage is excluded — the matched gomutants runs exclude
  it too. A separate gomutants `./...` row on the cobra table shows the
  full-repo number for context.
- **Wall time is sensitive to thermal state and background load.** These
  numbers were taken on AC power with no other CPU-bound work; rerun under
  the same conditions before drawing conclusions.
- **Cobra results are single-run.** Cold gomutants OOB on cobra is ~7
  minutes per run; a 3-runs × 4-scenarios hyperfine matrix would be
  >70 minutes of bench time, which isn't worth it for a workload where
  variance is small relative to wall time. The warm-cache row uses
  hyperfine 3× because each run finishes in seconds. Treat the cobra cold
  numbers as ±5% rather than the ±0.3% hyperfine reports on uuid.
- **Coverage of external targets.** This page covers `google/uuid` and
  `spf13/cobra`. The in-repo `benchmarks/` harness covers two other
  targets (`./testdata/simple/` and `./internal/mutator`) and will give a
  different picture on different code.

## Reproducing

```bash
# Pin a Go that gremlins works with
go install golang.org/dl/go1.25.7@latest && go1.25.7 download
export GOTOOLCHAIN=go1.25.7

# gomutants from this repo:
go build -o ~/bin/gomutants .
# gremlins v0.6.0 release: download from go-gremlins/gremlins releases.

# --- google/uuid ---
git clone https://github.com/google/uuid /tmp/uuid && cd /tmp/uuid
git checkout 2d3c2a9cc518326daf99a383f07c4d3c44317e4d

hyperfine --warmup 1 --runs 3 \
  --prepare 'rm -f .gomutants-cache.json' \
  -n gremlins 'gremlins unleash --workers 10 --timeout-coefficient 20 --silent -o /tmp/g.json .' \
  -n gom-oob  'gomutants -workers 10 --cache=off -quiet -o /tmp/m1.json .' \
  -n gom-l4l  'gomutants -workers 10 --cache=off -quiet -only=ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,INCREMENT_DECREMENT,INVERT_NEGATIVES -o /tmp/m2.json .'

# --- spf13/cobra (single-run; each cold takes minutes) ---
git clone https://github.com/spf13/cobra /tmp/cobra && cd /tmp/cobra
git checkout ad460ea8f249db69c943a365fb84f3a59042d54e

rm -f .gomutants-cache.json
time gremlins unleash --workers 10 --timeout-coefficient 20 --silent -o /tmp/c-grem.json .
rm -f .gomutants-cache.json
time gomutants -workers 10 --cache=off -quiet -o /tmp/c-oob.json .
rm -f .gomutants-cache.json
time gomutants -workers 10 --cache=off -quiet -only=ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,INCREMENT_DECREMENT,INVERT_NEGATIVES -o /tmp/c-l4l.json .

# Warm-cache run (prime once, then hyperfine):
rm -f .gomutants-cache.json
gomutants -workers 10 -quiet -o /tmp/c-prime.json . >/dev/null
hyperfine --warmup 0 --runs 3 'gomutants -workers 10 -quiet -o /tmp/c-warm.json .'
```
