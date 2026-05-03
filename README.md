# gomutants

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/szhekpisov/gomutants/badge)](https://scorecard.dev/viewer/?uri=github.com/szhekpisov/gomutants)
[![Go Report Card](https://goreportcard.com/badge/github.com/szhekpisov/gomutants)](https://goreportcard.com/report/github.com/szhekpisov/gomutants)
[![Go Reference](https://pkg.go.dev/badge/github.com/szhekpisov/gomutants.svg)](https://pkg.go.dev/github.com/szhekpisov/gomutants)
[![codecov](https://codecov.io/gh/szhekpisov/gomutant/graph/badge.svg?token=XNXMEJDGV2)](https://codecov.io/gh/szhekpisov/gomutant)
[![Mutation testing badge](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fbadge-api.stryker-mutator.io%2Fgithub.com%2Fszhekpisov%2Fgomutants%2Fmain)](https://dashboard.stryker-mutator.io/reports/github.com/szhekpisov/gomutants/main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Security & Static Analysis](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml)

`gomutants` is a mutation-testing tool for Go that's designed to gate CI on a per-PR basis. It supports generics, ships block-level mutators alongside the standard token-level set, runs only the tests whose coverage actually touches each mutant, and treats `--changed-since <ref>` as a first-class mode rather than an afterthought. Output is gremlins-compatible JSON, so existing scripts keep working.

## Quick start

```bash
go install github.com/szhekpisov/gomutants@v0.1.0

# Run on the whole module.
gomutants ./...

# Run only on lines this PR changes — typically under a minute on a hosted runner.
gomutants --changed-since origin/main ./...

# Drop-in for gremlins users:
gomutants unleash ./...
```

Requires Go 1.26 or later.

## Table of Contents

- [Why use gomutants?](#why-use-gomutants)
- [Why shouldn't I use gomutants?](#why-shouldnt-i-use-gomutants)
- [How it compares](#how-it-compares)
- [Install](#install)
- [Usage](#usage)
  - [GitHub Action](#github-action)
  - [Stryker-format reports](#stryker-format-reports)
  - [CLI reference](#cli-reference)
  - [Configuration](#configuration)
- [Mutators](#mutators)
- [JSON report](#json-report)
- [How it works](#how-it-works)
- [Benchmarks](#benchmarks)
- [Contributing](#contributing)
- [License](#license)

## Why use gomutants?

PR-scoped mutation testing on a CI gate is a different workload than batch mutation testing on a developer's laptop, and the existing Go tools were built for the latter. gomutants is built around the former, with these specific consequences:

**1. Discovery is conservative, not generous.** Compile-failing mutants are reported as `NOT_VIABLE` and excluded from the kill count — they don't silently inflate efficacy. Address-of `&` is recognised at the AST level and skipped (mutating it as bitwise AND would never compile). Unary `-` is emitted by exactly one mutator (`InvertNegatives`), not also by `ArithmeticBase`, so there are no duplicate mutants on the same byte. The upshot: `test_efficacy = killed / (killed + lived)` is a number you can gate on.

**2. `--changed-since` is the headline mode, not a flag tucked in the back.** It runs `git diff --unified=0 <ref>` and keeps only mutants whose line falls inside an added/modified range. Combined with the per-test coverage map, **a typical PR's mutation job drops from minutes to under a minute** — fast enough to gate every pull request. This repo's own PR job uses `--changed-since` and gates on "no LIVED mutant on changed lines"; the post-merge job runs the full tree against an absolute efficacy floor. See [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml).

**3. Per-test coverage routing.** For each mutant, gomutants runs only the tests whose coverage touches the mutated line — not the entire test suite. This is built from a per-test coverage map computed once per run by compiling each test binary one time and replaying it with `-test.run=<one>` per test. When the change is on a line covered by 3 of your 400 tests, you run those 3.

**4. Speed comes from doing less, not from cutting corners.** Per-mutant time is within noise of gremlins (1.79s vs 1.81s on the diffyml workload — see [Benchmarks](#benchmarks)). The wall-clock difference comes from cache-locality engineering: each child runs with `GOMAXPROCS=NumCPU/workers` so a 10-worker run on a 10-core box doesn't oversubscribe 100×; mutants are dispatched in `(Pkg, File, Offset)` order so the first mutant in a package pays the cold compile and subsequent ones reuse the build cache (a 17% wall-clock reduction by itself); `-vet=off` on the inner `go test` is a 17–39% per-mutant reduction on representative packages.

**5. Byte-level patching, not AST rewriting.** Mutations are applied as byte patches through `go test -overlay`. Type parameters, instantiations, and the rest of Go's syntax surface — generics included — survive intact. The original source tree is never modified.

**6. OOM-safe by construction.** Each `go test` child runs in its own process group with a 2 GiB RSS cap. A mutation that flips a loop bound or allocation size — and there are many — can balloon a test binary to tens of gigabytes within seconds; gomutants `SIGKILL`s the entire process group on cap breach and classifies the mutant as `TIMED_OUT` instead of taking the whole job down. Output is capped at 1 MiB per stream so a panic-loop mutant can't fill the runner disk.

**7. 16 mutators, including block-level.** Beyond the standard token-level operators, gomutants ships `BRANCH_IF` / `BRANCH_ELSE` / `BRANCH_CASE`, `EXPRESSION_REMOVE`, and `STATEMENT_REMOVE` — block-level mutators that reshape statements and branches and surface the weak-assertion test gaps that token-level mutation misses. Full catalog under [Mutators](#mutators).

**8. Stryker Dashboard support out of the box.** `--stryker-output report.json` writes a [mutation-testing-elements v2](https://github.com/stryker-mutator/mutation-testing-elements) report alongside the gremlins-format JSON — feeds the interactive HTML viewer and the Stryker Dashboard score badge.

## Why shouldn't I use gomutants?

A handful of cases where gomutants is the wrong choice:

- **You run mutation testing manually, once in a while.** [gremlins](https://github.com/go-gremlins/gremlins) is simpler, well-supported, and **faster on small workloads** — gomutants's one-time setup (coverage collection, baseline measurement, per-test coverage map build) is fixed overhead that only pays off when many mutants share that cost. On `./testdata/simple/` (34 mutants) gremlins is 1.31× faster wall-clock; on a single small package with default mutators, 2.43× faster. See [`benchmarks/results.md`](benchmarks/results.md) for the full breakdown.
- **Your test suite is thin.** Mutation testing is leverage on top of an existing test suite. If line coverage is below ~70%, fixing that is a higher-value use of time than gating on mutation efficacy.
- **You're on Go &lt; 1.26.** gomutants doesn't ship a build for older toolchains.
- **You don't want a CI gate.** The `--changed-since` PR-scope is gomutants's centerpiece. Without it you're paying complexity for features you won't use.

## How it compares

| Feature | gomutants | [gremlins](https://github.com/go-gremlins/gremlins) | [go-mutesting](https://github.com/zimmski/go-mutesting) |
|---|---|---|---|
| Mutators (default set) | 16 | 5 | 6 |
| Block-level mutators | yes | no | no |
| Generics support | yes (byte-patching) | partial[^1] | no |
| `--changed-since <ref>` | first-class | no | no |
| Per-test coverage routing | yes | no | no |
| `NOT_VIABLE` classification | yes | no[^2] | partial |
| OOM-safe subprocess control | 2 GiB RSS cap, process group | no | no |
| gremlins-compatible JSON | yes | (native) | no |
| Stryker dashboard format | yes | no | no |
| Per-mutant timeout | yes | yes | yes |
| Active maintenance | yes | yes | minimal |

[^1]: gremlins uses AST rewriting; some generic constructs round-trip incorrectly.
[^2]: Compile-failing mutants are silently dropped, so they don't appear in the report at all — they neither contribute to the kill count nor surface as a separate category.

## Install

```bash
go install github.com/szhekpisov/gomutants@v0.1.0
```

Requires Go 1.26 or later. No platform-specific build steps; the tool is a pure-Go binary that shells out to the user's `go` toolchain.

## Usage

```bash
# Default: run on all packages with NumCPU workers.
gomutants ./...

# Faster CI: only mutants on lines this PR changes.
gomutants --changed-since origin/main ./...

# Local exploration: see what would be tested without running.
gomutants --dry-run ./...

# Verbose stream of every mutant as it completes.
gomutants -v ./...

# Limit to specific mutators (or exclude some).
gomutants --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...
gomutants --disable BRANCH_IF,BRANCH_ELSE ./...

# Tune for memory-tight runners.
gomutants --workers=2 ./...

# Give each go test more CPU lanes (paired with low --workers).
gomutants --workers=1 --test-cpu=8 ./...

# Custom output path; coverage scope; raised timeout.
gomutants -o report.json --coverpkg ./pkg/mypackage/... \
         --timeout-coefficient 15 ./...
```

`gomutants unleash ./...` is accepted unchanged for gremlins-compat scripts.

### GitHub Action

Surface surviving mutants as inline annotations on the PR diff:

```yaml
- uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6
  with:
    fetch-depth: 0  # required so --changed-since can reach the base ref
- uses: szhekpisov/gomutants@v0.1.0
  with:
    args: --changed-since origin/${{ github.base_ref }} ./...
```

Each LIVED mutant on a changed line is emitted as a `::warning file=...,line=...::` workflow command, which GitHub renders inline on the "Files changed" view. The action fails if any LIVED is reported (override with `fail-on-lived: false`).

| Input | Default | Description |
|---|---|---|
| `args` | _required_ | Arguments forwarded to `gomutants`. The action appends `--annotations=github` automatically. |
| `version` | `latest` | gomutants version to install. With `version: latest` the action keeps a pre-installed binary on PATH; with any pinned tag/branch/SHA it always re-installs so what runs matches what was requested. |
| `threshold-efficacy` | `100` | Minimum test efficacy `%` (`KILLED/(KILLED+LIVED)`). Below threshold → exit 10. Default `100` fails the step on any LIVED mutant; set to `""` to disable. |
| `threshold-mcover` | _empty_ | Minimum mutant coverage `%` (`(KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)`). Below threshold → exit 11. Empty disables. |
| `working-directory` | `.` | Directory containing `go.mod`. |
| `cache` | `.gomutants-cache.json` | Path to the incremental-analysis cache file. Set to `off` to disable. Pair with [`actions/cache`](https://github.com/actions/cache) to persist across CI runs. |

**Security:** the `args` input is splatted into a shell command, and `version` is interpolated into `go install …@<version>`. Don't pipe untrusted strings (PR titles, branch names) into either. For supply-chain hardening, pin `version` to a specific commit SHA rather than `latest`.

See [`action.yml`](action.yml) for the full composite definition.

### Stryker-format reports

```bash
gomutants --stryker-output stryker-report.json ./...
```

Writes a [mutation-testing-elements v2](https://github.com/stryker-mutator/mutation-testing-elements) report alongside the gremlins-format JSON. The same file feeds:

- the [`<mutation-test-report-app>`](https://www.npmjs.com/package/mutation-testing-elements) web component, which renders an interactive HTML view when embedded in a page with `src="stryker-report.json"`.
- the [Stryker Dashboard](https://stryker-mutator.io/docs/General/dashboard/), which hosts the report and serves a mutation-score badge:

```bash
curl -X PUT \
  -H "X-Api-Key: $STRYKER_DASHBOARD_KEY" \
  -H "Content-Type: application/json" \
  --data @stryker-report.json \
  "https://dashboard.stryker-mutator.io/api/reports/github.com/<org>/<repo>/<branch-or-sha>"
```

Once registered on `dashboard.stryker-mutator.io`, your project gets a `mutationScoreBadge` URL you can drop in this README — the same surface PIT, Stryker (JS/.NET/Scala), and Infection PHP plug into.

### CLI reference

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--workers` | `-w` | NumCPU | Parallel workers |
| `--test-cpu` | | 0 (omit) | Value passed to inner `go test -cpu` per mutant; 0 lets go test use `GOMAXPROCS` |
| `--timeout-coefficient` | | 10 | Multiplier applied to baseline test time for the per-mutant timeout |
| `--coverpkg` | | | Coverage package pattern (forwarded to `go test -coverpkg`) |
| `--output` | `-o` | `mutation-report.json` | JSON report path |
| `--config` | | `.gomutants.yml` | Config file path |
| `--disable` | | | Comma-separated mutator types to disable |
| `--only` | | | Comma-separated mutator types to run (disables all others) |
| `--changed-since` | | | Only test mutants on lines changed vs git ref (e.g. `main`, `HEAD~1`); requires a git repo |
| `--cache` | | `.gomutants-cache.json` | Path to incremental-analysis cache file. Skips mutants whose source and tests are byte-identical to the cached run. Pass `--cache=off` to disable. |
| `--annotations` | | | Emit annotations for LIVED mutants. Supported: `github` (workflow-command warnings on stdout). |
| `--stryker-output` | | | Also write a [Stryker mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) report at this path (for the HTML viewer and Stryker Dashboard). |
| `--threshold-efficacy` | | 0 | Minimum test efficacy (KILLED/(KILLED+LIVED)). Below threshold → exit 10 (gremlins-compat). 0 disables. |
| `--threshold-mcover` | | 0 | Minimum mutant coverage ((KILLED+LIVED)/(KILLED+LIVED+NOT_COVERED)). Below threshold → exit 11 (gremlins-compat). 0 disables. |
| `--dry-run` | | false | List mutants without testing |
| `--verbose` | `-v` | false | Stream each mutant as tested |
| `--version` | | | Print version and exit |

### Configuration

`.gomutants.yml` in the project root:

```yaml
workers: 10
test-cpu: 0           # 0 = let go test use GOMAXPROCS
timeout-coefficient: 10
coverpkg: "./pkg/mypackage/..."
output: mutation-report.json
changed-since: ""     # set to e.g. "main" to scope runs by default
cache: ""             # path to incremental-analysis cache; "" = .gomutants-cache.json, "off" = disabled
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

Three performance optimizations layered on top:

- **`GOMAXPROCS=NumCPU/workers` per child.** Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100×. With it, each child compiles + tests within its share.
- **Sort pending mutants by `(Pkg, File, Offset)` before dispatch.** The first mutant in a package pays the cold compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.
- **`-vet=off` on the inner `go test`.** Vet runs in the user's CI on clean source; re-running it for every mutant is wasted work. Measured 17–39% per-mutant wall-clock reduction on representative packages.

## Benchmarks

Headline numbers, measured on [diffyml](https://github.com/szhekpisov/diffyml) with a matched 11-mutator set on an Apple M1 Pro 10-core, fresh full-pipeline run:

| Workers | gomutants | gremlins |
|---|---:|---:|
| 1 | 1134 s | 1848 s |
| 5 | **342 s** | 410 s |

Per-mutant time is essentially identical (1.79s vs 1.81s); the wall-clock difference comes from cache-locality engineering (described in [How it works](#how-it-works)) and a tighter mutant set (the conservative-discovery rules emit fewer duplicates and fewer compile-failing mutants). On the diffyml workload that's 1030 mutants discovered, 94% efficacy.

**Where gomutants loses.** On small workloads — single small packages, default-mutator runs over a few dozen mutants — gremlins is faster (1.31× on `./testdata/simple/`, 2.43× on a single-package default-mutator run). gomutants's one-time setup cost (coverage collection, baseline measurement, per-test coverage map build) only amortizes when there are many mutants to share it across. The `mutator-matched` apples-to-apples scenario at the same scale flips back: 1.29× faster for gomutants. See [`benchmarks/results.md`](benchmarks/results.md) for full per-scenario tables, methodology, hyperfine output, and caveats; reproduce with `bash benchmarks/run.sh`.

### Self-efficacy (gomutants on itself)

gomutants kills **100%** of mutants in its `./internal/...` library code (every package at 100% efficacy). Statement coverage is also 100%. The CI gate fails on any surviving mutant on changed lines per PR and on the full `./internal/...` tree post-merge — drift surfaces on the merge that introduces it.

The `main` package is excluded from mutation testing. Its mutants exercise the integration test suite (which forks gomutants subprocesses to test mutated overlays), each taking minutes; running them in CI under the same gate isn't tractable, and most surviving mutants are output-formatting drift the integration tests intentionally don't pin.

## Contributing

Found a bug or have a feature request? [Open an issue](https://github.com/szhekpisov/gomutants/issues/new).

## License

[MIT](LICENSE)

---

If you find this project useful, please consider giving it a ⭐ — it helps others discover it.
