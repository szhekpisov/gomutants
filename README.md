# gomutant

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/szhekpisov/gomutant/badge)](https://scorecard.dev/viewer/?uri=github.com/szhekpisov/gomutant)
[![Go Report Card](https://goreportcard.com/badge/github.com/szhekpisov/gomutant)](https://goreportcard.com/report/github.com/szhekpisov/gomutant)
[![Go Reference](https://pkg.go.dev/badge/github.com/szhekpisov/gomutant.svg)](https://pkg.go.dev/github.com/szhekpisov/gomutant)
[![codecov](https://codecov.io/gh/szhekpisov/gomutant/branch/main/graph/badge.svg)](https://codecov.io/gh/szhekpisov/gomutant)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Security & Static Analysis](https://github.com/szhekpisov/gomutant/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/szhekpisov/gomutant/actions/workflows/security.yml)

> Mutation testing for Go: more mutators, generics support, per-test coverage routing, and PR-scoped runs as a first-class CI workflow.

A drop-in replacement for [go-gremlins](https://github.com/go-gremlins/gremlins) — same `unleash` subcommand, same JSON report shape — built around three premises:

1. **Discovery is conservative.** Compile failures are reported as `NOT_VIABLE`, separate from kills. Address-of `&` is distinguished from bitwise AND, and unary `-` is emitted by exactly one mutator.
2. **Speed comes from doing less.** Mutating only changed lines, running only the tests that cover each mutant, sharing a hot build cache across consecutive mutants in the same package.
3. **The CI workflow is the point.** First-class `--changed-since` mode, gremlins-compatible JSON, memory-safe subprocess control — designed for `pull_request` jobs, not just local exploration.

## Table of Contents

- [Background](#background)
- [Install](#install)
- [Usage](#usage)
  - [CLI reference](#cli-reference)
  - [Configuration](#configuration)
- [Mutators](#mutators)
- [JSON report](#json-report)
- [How it works](#how-it-works)
- [Benchmarks](#benchmarks)
- [Contributing](#contributing)
- [License](#license)

## Background

### Performance

Measured on [diffyml](https://github.com/szhekpisov/diffyml), matched 11-mutator set, M1 Pro 10-core, fresh full-pipeline run:

| Workers | gomutant | gremlins |
|---|---:|---:|
| 1 | 1134 s | 1848 s |
| 5 (`NumCPU/2`) | **342 s** | 410 s |

Per-mutant time is essentially identical (1.79s vs 1.81s); the wall-clock difference comes from cache-locality engineering and a tighter mutant set (see "Accurate discovery" below). Reproduce with `bash benchmarks/run.sh`.

### Accurate discovery

A few mutation patterns the AST walker handles deliberately:

- **Address-of `&`** is recognised and skipped — mutating it as bitwise AND would always fail to compile, so it's not emitted at all.
- **Unary `-`** is emitted by `InvertNegatives` only, not also by `ArithmeticBase` — no duplicates on the same byte.
- **Compile-failing mutants** are classified as `NOT_VIABLE` and excluded from the kill count; `test_efficacy` is `killed / (killed + lived)`.

Net effect on the diffyml benchmark: 1030 mutants discovered, 94% efficacy.

### Run only the tests that matter

For each mutant, gomutant runs **only the tests whose coverage touches the mutated line** — not the entire test suite. This is built from a per-test coverage map computed once per run by compiling each test binary one time and replaying it with `-test.run=<one>` per test. When the change is on a line covered by 3 of your 400 tests, you run those 3 — not all 400.

**This is on by default.** The `selection-on-vs-off` benchmark scenario quantifies the win on this repo's own `./internal/mutator` package; reproduce with `bash benchmarks/run.sh`. To turn the routing off (debugging, comparison, or a suite where the upfront map cost outweighs the savings) pass `--no-test-selection`.

### PR-scoped mutation testing as a first-class mode

```bash
gomutant --changed-since main ./...
gomutant --changed-since HEAD~1 ./...
```

`--changed-since` runs `git diff --unified=0 <ref>` and keeps only mutants whose line falls inside an added/modified range. Combined with the per-test coverage map, **a typical PR's mutation job drops from minutes to under a minute** — fast enough to gate every pull request. (This very repo's PR job takes ~1 min on a hosted runner.)

This repo's own CI does exactly this: PR job uses `--changed-since` and gates on "no LIVED mutant on changed lines"; post-merge job runs the full tree against an absolute efficacy floor. See [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml).

### Block-level mutators

Beyond the standard token-level operators, gomutant ships block-level mutators (`BRANCH_IF` / `BRANCH_ELSE` / `BRANCH_CASE`, `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`) that reshape statements and branches to surface weak-assertion test gaps. See [Mutators](#mutators) for the full 16-mutator catalog.

### Generics, no source-tree copies, OOM-safe

- **Generics support.** Byte-level patching, not AST-rewriting — preserves type parameters, instantiations, all of Go's syntax surface.
- **`go test -overlay`** for every mutant. Each worker owns one stable temp file and one stable overlay JSON. The original source tree is never modified.
- **2 GiB per-subprocess RSS cap.** A mutation that flips a loop bound or allocation size can balloon the test binary to tens of gigabytes within seconds. gomutant monitors process-group RSS and `SIGKILL`s the entire tree on cap breach — classified as `TIMED_OUT`, not as a runaway that takes the whole job down.
- **Output capped at 1 MiB per stream.** A panic-loop mutant can't fill the runner disk.

## Install

```bash
go install github.com/szhekpisov/gomutant@latest
```

Requires Go 1.26 or later.

## Usage

```bash
# Default: run on all packages with NumCPU workers.
gomutant ./...

# Faster CI: only mutants on lines this PR changes.
gomutant --changed-since origin/main ./...

# Local exploration: see what would be tested without running.
gomutant --dry-run ./...

# Verbose stream of every mutant as it completes.
gomutant -v ./...

# Limit to specific mutators (or exclude some).
gomutant --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...
gomutant --disable BRANCH_IF,BRANCH_ELSE ./...

# Tune for memory-tight runners.
gomutant --workers=2 ./...

# Give each go test more CPU lanes (paired with low --workers).
gomutant --workers=1 --test-cpu=8 ./...

# Custom output path; coverage scope; raised timeout.
gomutant -o report.json --coverpkg ./pkg/mypackage/... \
         --timeout-coefficient 15 ./...
```

`gomutant unleash ./...` is accepted unchanged for gremlins-compat scripts.

### CLI reference

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--workers` | `-w` | NumCPU | Parallel workers |
| `--test-cpu` | | 0 (omit) | Value passed to inner `go test -cpu` per mutant; 0 lets go test use `GOMAXPROCS` |
| `--timeout-coefficient` | | 10 | Multiplier applied to baseline test time for the per-mutant timeout |
| `--coverpkg` | | | Coverage package pattern (forwarded to `go test -coverpkg`) |
| `--output` | `-o` | `mutation-report.json` | JSON report path |
| `--config` | | `.gomutant.yml` | Config file path |
| `--disable` | | | Comma-separated mutator types to disable |
| `--only` | | | Comma-separated mutator types to run (disables all others) |
| `--changed-since` | | | Only test mutants on lines changed vs git ref (e.g. `main`, `HEAD~1`); requires a git repo |
| `--dry-run` | | false | List mutants without testing |
| `--verbose` | `-v` | false | Stream each mutant as tested |
| `--no-test-selection` | | false | Disable per-test coverage routing; run the full package's tests for every mutant |
| `--version` | | | Print version and exit |

### Configuration

`.gomutant.yml` in the project root:

```yaml
workers: 10
test-cpu: 0                # 0 = let go test use GOMAXPROCS
timeout-coefficient: 10
coverpkg: "./pkg/mypackage/..."
output: mutation-report.json
changed-since: ""          # set to e.g. "main" to scope runs by default
no-test-selection: false   # set true to disable per-test coverage routing
disable: []
only: []
```

Priority: built-in defaults < config file < CLI flags.

## Mutators

### Token-level

| Type | Description | Example |
|------|-------------|---------|
| `ARITHMETIC_BASE` | Swap arithmetic operators | `+` <-> `-`, `*` <-> `/`, `%` <-> `*` |
| `CONDITIONALS_BOUNDARY` | Relax/tighten boundaries | `<` <-> `<=`, `>` <-> `>=` |
| `CONDITIONALS_NEGATION` | Negate comparisons | `==` <-> `!=`, `<` <-> `>=`, `>` <-> `<=` |
| `INCREMENT_DECREMENT` | Swap increment/decrement | `++` <-> `--` |
| `INVERT_NEGATIVES` | Invert negation | `-x` -> `+x`, `a - b` -> `a + b` |
| `INVERT_ASSIGNMENTS` | Swap arithmetic compound assignments | `+=` <-> `-=`, `*=` <-> `/=`, `%=` -> `*=` |
| `INVERT_BITWISE` | Swap bitwise binary operators | `&` <-> `\|`, `^` -> `&`, `<<` <-> `>>` |
| `INVERT_BITWISE_ASSIGNMENTS` | Swap bitwise compound assignments | `&=` <-> `\|=`, `^=` -> `&=`, `<<=` <-> `>>=` |
| `INVERT_LOGICAL` | Swap logical operators | `&&` <-> `\|\|` |
| `INVERT_LOOP_CTRL` | Swap loop control | `break` <-> `continue` |
| `REMOVE_SELF_ASSIGNMENTS` | Drop op from compound assignment | `x += y` -> `x = y` |

### Block-level

| Type | Description | Example |
|------|-------------|---------|
| `BRANCH_IF` | Empty if/else-if body | `if x { doStuff() }` -> `if x { _ = 0 }` |
| `BRANCH_ELSE` | Empty else body | `else { doStuff() }` -> `else { _ = 0 }` |
| `BRANCH_CASE` | Empty case body | `case 1: doStuff()` -> `case 1: _ = 0` |
| `EXPRESSION_REMOVE` | Remove boolean operand | `a && b` -> `true && b` / `a && true` |
| `STATEMENT_REMOVE` | Remove statement effect | `x = expr` -> `_ = expr`, `f()` -> `_ = 0` |

### Mutant statuses

| Status | Meaning |
|--------|---------|
| KILLED | Test failed — mutant detected |
| LIVED | Tests passed — **test gap** |
| NOT COVERED | No test covers the mutated line |
| NOT VIABLE | Mutation causes a compile error (filtered, not counted as a kill) |
| TIMED OUT | Test execution exceeded the per-mutant timeout |

## JSON report

Compatible with the gremlins JSON format:

```json
{
  "go_module": "github.com/example/project",
  "test_efficacy": 100,
  "mutations_coverage": 97.16,
  "mutants_total": 792,
  "mutants_killed": 772,
  "mutants_lived": 0,
  "mutants_not_viable": 0,
  "mutants_not_covered": 20,
  "elapsed_time": 159.84,
  "files": [...]
}
```

`test_efficacy = killed / (killed + lived)` — excludes `not_viable`, `not_covered`, and `timed_out`.

## How it works

1. **Resolve packages** via `go list -json`.
2. **Collect coverage** with `go test -coverprofile`. Mutants on uncovered lines are filtered upfront as `NOT_COVERED`.
3. **Measure baseline test time** to set a sane per-mutant timeout (multiplied by `--timeout-coefficient`).
4. **Discover mutants** by walking the AST and emitting byte-level patches. Address-of `&` is recognised and skipped; unary `-` is emitted by exactly one mutator.
5. **Build per-test coverage map.** Test binaries are compiled once; each test runs in isolation with `-test.run=<one>` to record the lines it covers.
6. **Test mutants** in parallel:
   - Each worker owns a stable temp source file + overlay JSON.
   - Mutations are applied as byte-level patches; the original tree is never written to.
   - The mutant's covered tests are looked up; only those run via `go test -overlay -run=<regex>`.
   - Each `go test` child runs in its own process group with a 2 GiB RSS cap; output is capped at 1 MiB per stream.

Two performance optimizations layered on top:

- **`GOMAXPROCS=NumCPU/workers` per child.** Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100×. With it, each child compiles + tests within its share.
- **Sort pending mutants by `(Pkg, File, Offset)` before dispatch.** The first mutant in a package pays the cold compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.

## Benchmarks

Headline numbers are in [Background](#background). Reproduce with `bash benchmarks/run.sh`. Per-scenario detail in [`benchmarks/results.md`](benchmarks/results.md).

The `workers=5` wall-clock is shaped by three things layered on the engine:

- The conservative discovery rules described above (1030 mutants on the diffyml workload).
- `GOMAXPROCS` capping per child to avoid CPU oversubscription.
- `(Pkg, File, Offset)` dispatch order to keep the build cache hot across consecutive mutants in the same package.

`NumCPU/2` was the historical default before this benchmark; gomutant now defaults to `NumCPU` because the per-child `GOMAXPROCS` cap eliminates the oversubscription failure mode.

### Self-efficacy (gomutant on itself)

gomutant kills **69.32%** of mutants in its own test suite (664 mutants across 8 packages, v0.1.0). Coverage is 97% — most lived mutants are real test gaps, not blind spots. Per-package breakdown in [`testdata/golden/self-efficacy.txt`](testdata/golden/self-efficacy.txt). The `internal/...` subset (excluding `main`) clears 88.03%, which is the gate this repo's CI enforces post-merge.

## Contributing

Found a bug or have a feature request? [Open an issue](https://github.com/szhekpisov/gomutant/issues/new).

## License

[MIT](LICENSE)
