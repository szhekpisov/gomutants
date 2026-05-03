Gomutants is a mutation testing tool for Go, supporting diff-scoped runs, incremental caching, per-test coverage routing, and block-level mutators. It's a near drop-in for [gremlins](https://github.com/go-gremlins/gremlins) — same `unleash` command, same gremlins-compatible JSON output, same threshold exit codes — so existing CI scripts keep working.

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/szhekpisov/gomutants/badge)](https://scorecard.dev/viewer/?uri=github.com/szhekpisov/gomutants)
[![Go Report Card](https://goreportcard.com/badge/github.com/szhekpisov/gomutants)](https://goreportcard.com/report/github.com/szhekpisov/gomutants)
[![Go Reference](https://pkg.go.dev/badge/github.com/szhekpisov/gomutants.svg)](https://pkg.go.dev/github.com/szhekpisov/gomutants)
[![codecov](https://codecov.io/gh/szhekpisov/gomutant/graph/badge.svg?token=XNXMEJDGV2)](https://codecov.io/gh/szhekpisov/gomutant)
[![Mutation testing badge](https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fbadge-api.stryker-mutator.io%2Fgithub.com%2Fszhekpisov%2Fgomutants%2Fmain)](https://dashboard.stryker-mutator.io/reports/github.com/szhekpisov/gomutants/main)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Security & Static Analysis](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/szhekpisov/gomutants/actions/workflows/security.yml)

Licensed under [MIT](LICENSE).


### Documentation quick links

* [Quick start](#quick-start)
* [Quick examples comparing tools](#quick-examples-comparing-tools)
* [Why should I use gomutants?](#why-should-i-use-gomutants)
* [Why shouldn't I use gomutants?](#why-shouldnt-i-use-gomutants)
* [Is it really faster than gremlins?](#is-it-really-faster-than-gremlins)
* [Feature comparison](#feature-comparison)
* [Installation](#installation)
* [Building](#building)
* [Running tests](#running-tests)
* [GitHub Action](#github-action)
* [Stryker-format reports](#stryker-format-reports)
* [CLI reference](#cli-reference)
* [Configuration](#configuration)
* [Mutators](#mutators)
* [JSON report](#json-report)
* [How it works](#how-it-works)
* [Self-efficacy](#self-efficacy-gomutants-on-itself)


### Quick start

```
$ go install github.com/szhekpisov/gomutants@v0.1.0

# Run on the whole module.
$ gomutants ./...

# Run only on lines this PR changes.
$ gomutants --changed-since origin/main ./...

# Near drop-in for gremlins users:
$ gomutants unleash ./...
```


### Quick examples comparing tools

The headline workload is [diffyml](https://github.com/szhekpisov/diffyml), run on an Apple M1 Pro 10-core with the matched 5-mutator set (gremlins' defaults enabled on both sides), `workers=5`, `--timeout-coefficient=50`, and gomutants's per-mutant cache disabled (`--cache=off`) so every run measures actual mutation work. Hyperfine reports the mean of three runs after a warmup pass:

| Tool | Workers | Mean wall-clock | Tested mutants | Per-mutant time |
| ---- | :-----: | --------------: | -------------: | --------------: |
| **gomutants** | 5 | **244 s ± 41** | 773 | **316 ms** |
| [gremlins](https://github.com/go-gremlins/gremlins) | 5 | 295 s ± 65 | 542 | 545 ms |

**gomutants is ~20% faster wall-clock and ~1.7× faster per tested mutant.** It also generates ~40% more viable mutants from the same five operators (byte-level patching emits patches the AST rewriter doesn't); both tools land on the same efficacy (99.87% vs 99.82%), so the wall-clock lead would be larger if they tested the same mutant population. The wall-clock difference comes from cache-locality engineering (see [How it works](#how-it-works)) and gomutants's per-test coverage routing. Note: `--cache=off` is set so a partial cache hit doesn't skew the comparison; on a typical CI re-run with the cache enabled (gomutants's default), gomutants is faster still.

Beware of small workloads though. gomutants's one-time setup cost (coverage collection, baseline measurement, per-test coverage map build) only amortizes when there are many mutants to share it across:

| Workload | Tool | Time |
| -------- | ---- | ---: |
| `./testdata/simple/` (34 mutants, default mutators) | [gremlins](https://github.com/go-gremlins/gremlins) | **9.6 s** (1.00x) |
| `./testdata/simple/` (34 mutants, default mutators) | gomutants | 12.6 s (1.31x) |
| Single small package, default mutators | [gremlins](https://github.com/go-gremlins/gremlins) | **2.5 s** (1.00x) |
| Single small package, default mutators | gomutants | 6.1 s (2.43x) |
| Single small package, mutator-matched | gomutants | **1.9 s** (1.00x) |
| Single small package, mutator-matched | [gremlins](https://github.com/go-gremlins/gremlins) | 2.5 s (1.29x) |

The `--changed-since <ref>` flag scopes a run to mutants on lines added or modified since the given ref. Use `gomutants --changed-since origin/main ./...` to gate every pull request without re-running the full mutation suite on untouched code.

See [`benchmarks/results.md`](benchmarks/results.md) for the full per-scenario breakdown, methodology, hyperfine output, and caveats; reproduce with `bash benchmarks/run.sh`.


### Why should I use gomutants?

gomutants is built for PR-scoped mutation testing as a CI gate. The consequences:

* **Discovery is conservative.** Compile-failing mutants are reported as `NOT_VIABLE` and excluded from the kill count — they don't silently inflate efficacy. `test_efficacy = killed / (killed + lived)` is a number you can gate on.
* **`--changed-since` is the headline mode.** It runs `git diff --unified=0 <ref>` and keeps only mutants on added/modified lines — fast enough to gate every PR without re-testing untouched code. This repo's PR job uses it to gate on "no LIVED mutant on changed lines"; the post-merge job runs the full tree. See [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml).
* **Per-test coverage routing.** Each mutant runs only the tests whose coverage touches the mutated line — not the whole suite. On top of that: cache-locality dispatch (mutants sorted by `(Pkg, File, Offset)` so subsequent ones reuse the build cache, –17% wall-clock); per-child `GOMAXPROCS=NumCPU/workers` to avoid oversubscription; `-vet=off` on the inner `go test` (–17 to –39% per mutant). End-to-end on diffyml: ~20% wall-clock advantage over gremlins.
* **Byte-level patching, not AST rewriting.** Mutations apply as byte patches through `go test -overlay`. Generics and the rest of Go's syntax surface survive intact; the original source tree is never modified.
* **OOM-safe by construction.** Each `go test` child runs in its own process group with a 2 GiB RSS cap; gomutants `SIGKILL`s the group on breach and reports `TIMED_OUT` instead of taking the whole job down. Output is capped at 1 MiB per stream.
* **16 mutators, including block-level.** Beyond the token-level operators, gomutants ships `BRANCH_IF`/`BRANCH_ELSE`/`BRANCH_CASE`, `EXPRESSION_REMOVE`, and `STATEMENT_REMOVE` — block-level mutators that surface weak-assertion test gaps that token-level mutation misses. Full catalog under [Mutators](#mutators).


### Why shouldn't I use gomutants?

A handful of cases where gomutants is the wrong choice:

* **You run mutation testing manually, once in a while.** [gremlins](https://github.com/go-gremlins/gremlins) is simpler, well-supported, and faster on small workloads — gomutants's one-time setup (coverage collection, baseline measurement, per-test coverage map build) is fixed overhead that only pays off when many mutants share that cost. See [Quick examples comparing tools](#quick-examples-comparing-tools).
* **Your test suite is thin.** Mutation testing is leverage on top of an existing test suite. If line coverage is below ~70%, fixing that is a higher-value use of time than gating on mutation efficacy.
* **You're on Go < 1.26.** gomutants doesn't ship a build for older toolchains.
* **You don't want a CI gate.** The `--changed-since` PR-scope is gomutants's centerpiece. Without it you're paying complexity for features you won't use.


### Is it really faster than gremlins?

On the workloads gomutants is built for — full-module runs with many mutants, and PR-scoped runs that touch a small fraction of the tree — yes. On small one-off runs, no; see [the small-workload table above](#quick-examples-comparing-tools).

Summarizing, gomutants is fast at scale because:

* **Per-test coverage routing.** A mutant on a line covered by 3 of 400 tests runs those 3, not all 400. The coverage map is built once at startup (each test binary compiled once, replayed with `-test.run=<one>` per test) and reused for every mutant in that package.
* **Cache-locality dispatch.** Pending mutants are sorted by `(Pkg, File, Offset)` before dispatch. The first mutant in a package pays the cold `go test` compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.
* **Bounded child concurrency.** Each `go test` child runs with `GOMAXPROCS=NumCPU/workers`. Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100× and burning all the gain to context switching.
* **`-vet=off` on the inner `go test`.** Vet runs in your CI on clean source; re-running it for every mutant is wasted work. Measured 17–39% per-mutant reduction on representative packages.
* **Incremental analysis cache.** With `--cache` (on by default), mutants whose source byte range and the surrounding tests are byte-identical to a prior run are skipped and their previous classifications reused. CI runs that touch one file pay for that file only.
* **Discovery emits fewer wasted mutants.** Conservative AST checks (skipping address-of `&`, deduplicating unary `-`) and the byte-level patcher mean fewer compile-failing mutants reach the test stage in the first place.

PR-scoped runs are faster than gremlins for a different reason: gremlins has no equivalent mode, so the equivalent gremlins workflow runs the full module on every PR. `--changed-since` is what makes mutation testing tractable as a per-PR gate.


### Feature comparison

| Feature | gomutants | [gremlins](https://github.com/go-gremlins/gremlins) | [go-mutesting](https://github.com/zimmski/go-mutesting) |
|---|---|---|---|
| Mutators (default set) | 16 | 5 | 6 |
| Block-level mutators | yes | no | no |
| Generics support | yes (byte-patching) | partial[^1] | no |
| `--changed-since <ref>` | first-class | no | no |
| Per-test coverage routing | yes | no | no |
| Incremental cache | yes (on by default) | no | no |
| `NOT_VIABLE` classification | yes | no[^2] | partial |
| OOM-safe subprocess control | 2 GiB RSS cap, process group | no | no |
| gremlins-compatible JSON | yes | (native) | no |
| Stryker dashboard format | yes | no | no |
| Per-mutant timeout | yes | yes | yes |
| Active maintenance | yes | yes | minimal |

[^1]: gremlins uses AST rewriting; some generic constructs round-trip incorrectly.
[^2]: Compile-failing mutants are silently dropped, so they don't appear in the report at all — they neither contribute to the kill count nor surface as a separate category.


### Installation

Gomutants can be installed with `go install`:

```
$ go install github.com/szhekpisov/gomutants@v0.1.0
```

The minimum supported version of Go for gomutants is **1.26**, both for building gomutants itself and for the project under test (gomutants shells out to `go test` in your project's toolchain).

If you use **GitHub Actions**, gomutants is published as a composite action — see [GitHub Action](#github-action) below for the full configuration.

There are no platform-specific build steps; gomutants is a pure-Go binary that shells out to your `go` toolchain. macOS and Linux on amd64/arm64 are tested in CI; Windows works wherever `go` does, though it isn't covered by automated tests.


### Building

gomutants is written in Go, so you'll need a [Go installation](https://go.dev/dl/) in order to compile it. gomutants compiles with Go 1.26 or newer.

To build gomutants:

```
$ git clone https://github.com/szhekpisov/gomutants
$ cd gomutants
$ go build ./...
$ ./gomutants --version
```


### Running tests

gomutants has a unit-test suite alongside an integration-test suite that forks gomutants subprocesses to test mutated overlays end-to-end. To run the full suite:

```
$ go test ./...
```

The integration tests are tagged separately and take noticeably longer; the standard CI matrix runs them on every PR. See [`.github/workflows/test.yml`](.github/workflows/test.yml) for the exact invocation.

To run gomutants on itself (mutation-testing the mutation-tester), use the workflow in [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml) or replicate per-package locally with `gomutants ./internal/<pkg>/`. The `main` package is excluded — see [Self-efficacy](#self-efficacy-gomutants-on-itself).


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

**Security note:** the `args` input is splatted into a shell command, and `version` is interpolated into `go install …@<version>`. Don't pipe untrusted strings (PR titles, branch names) into either. For supply-chain hardening, pin `version` to a specific commit SHA rather than `latest`.

See [`action.yml`](action.yml) for the full composite definition.


### Stryker-format reports

```
$ gomutants --stryker-output stryker-report.json ./...
```

Writes a [mutation-testing-elements v2](https://github.com/stryker-mutator/mutation-testing-elements) report alongside the gremlins-format JSON. The same file feeds:

* the [`<mutation-test-report-app>`](https://www.npmjs.com/package/mutation-testing-elements) web component, which renders an interactive HTML view when embedded in a page with `src="stryker-report.json"`.
* the [Stryker Dashboard](https://stryker-mutator.io/docs/General/dashboard/), which hosts the report and serves a mutation-score badge:

```
$ curl -X PUT \
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

Common invocations:

```
# Default: run on all packages with NumCPU workers.
$ gomutants ./...

# Faster CI: only mutants on lines this PR changes.
$ gomutants --changed-since origin/main ./...

# Local exploration: see what would be tested without running.
$ gomutants --dry-run ./...

# Verbose stream of every mutant as it completes.
$ gomutants -v ./...

# Limit to specific mutators (or exclude some).
$ gomutants --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...
$ gomutants --disable BRANCH_IF,BRANCH_ELSE ./...

# Tune for memory-tight runners.
$ gomutants --workers=2 ./...

# Give each go test more CPU lanes (paired with low --workers).
$ gomutants --workers=1 --test-cpu=8 ./...

# Custom output path; coverage scope; raised timeout.
$ gomutants -o report.json --coverpkg ./pkg/mypackage/... \
           --timeout-coefficient 15 ./...
```

`gomutants unleash ./...` is accepted unchanged for gremlins-compat scripts.


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


### Mutators

Token-level:

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

Block-level:

| Type | Description | Example |
|------|-------------|---------|
| `BRANCH_IF` | Empty if/else-if body | `if x { doStuff() }` -> `if x { _ = 0 }` |
| `BRANCH_ELSE` | Empty else body | `else { doStuff() }` -> `else { _ = 0 }` |
| `BRANCH_CASE` | Empty case body | `case 1: doStuff()` -> `case 1: _ = 0` |
| `EXPRESSION_REMOVE` | Remove boolean operand | `a && b` -> `true && b` / `a && true` |
| `STATEMENT_REMOVE` | Remove statement effect | `x = expr` -> `_ = expr`, `f()` -> `_ = 0` |

Mutant statuses:

| Status | Meaning |
|--------|---------|
| KILLED | Test failed — mutant detected |
| LIVED | Tests passed — **test gap** |
| NOT COVERED | No test covers the mutated line |
| NOT VIABLE | Mutation causes a compile error (filtered, not counted as a kill) |
| TIMED OUT | Test execution exceeded the per-mutant timeout |


### JSON report

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


### How it works

1. **Resolve packages** via `go list -json`.
2. **Collect coverage** with `go test -coverprofile`. Mutants on uncovered lines are filtered upfront as `NOT_COVERED`.
3. **Measure baseline test time** to set a sane per-mutant timeout (multiplied by `--timeout-coefficient`).
4. **Discover mutants** by walking the AST and emitting byte-level patches. Address-of `&` is recognised and skipped; unary `-` is emitted by exactly one mutator.
5. **Build per-test coverage map.** Test binaries are compiled once; each test runs in isolation with `-test.run=<one>` to record the lines it covers.
6. **Test mutants** in parallel:
   * Each worker owns a stable temp source file + overlay JSON.
   * Mutations are applied as byte-level patches; the original tree is never written to.
   * The mutant's covered tests are looked up; only those run via `go test -overlay -run=<regex>`.
   * Each `go test` child runs in its own process group with a 2 GiB RSS cap; output is capped at 1 MiB per stream.

Three performance optimizations layered on top:

* **`GOMAXPROCS=NumCPU/workers` per child.** Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100×. With it, each child compiles + tests within its share.
* **Sort pending mutants by `(Pkg, File, Offset)` before dispatch.** The first mutant in a package pays the cold compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.
* **`-vet=off` on the inner `go test`.** Vet runs in the user's CI on clean source; re-running it for every mutant is wasted work. Measured 17–39% per-mutant wall-clock reduction on representative packages.


### Self-efficacy (gomutants on itself)

gomutants kills **100%** of mutants in its `./internal/...` library code (every package at 100% efficacy). Statement coverage is also 100%. The CI gate fails on any surviving mutant on changed lines per PR, and on the full `./internal/...` tree post-merge — drift surfaces on the merge that introduces it.

The `main` package is excluded from mutation testing. Its mutants exercise the integration test suite (which forks gomutants subprocesses to test mutated overlays), each taking minutes; running them in CI under the same gate isn't tractable, and most surviving mutants are output-formatting drift the integration tests intentionally don't pin.


### Contributing

Found a bug or have a feature request? [Open an issue](https://github.com/szhekpisov/gomutants/issues/new).


### License

[MIT](LICENSE).

---

If you find this project useful, please consider giving it a ⭐ — it helps others discover it.
