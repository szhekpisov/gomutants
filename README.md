# gomutant

> Mutation testing for Go that's faster than the alternatives, more accurate, and doesn't lie about its kill rate.

A drop-in replacement for [go-gremlins](https://github.com/go-gremlins/gremlins) — same `unleash` subcommand, same JSON report shape — built around three premises:

1. **The numbers should be true.** Mutations that fail to compile aren't kills. Address-of `&` isn't bitwise AND. Unary `-` isn't both `InvertNegatives` and `ArithmeticBase` at the same time.
2. **Speed comes from doing less.** Mutating only changed lines, running only the tests that cover each mutant, sharing a hot build cache across consecutive mutants in the same package.
3. **The CI workflow is the point.** First-class `--changed-since` mode, gremlins-compatible JSON, memory-safe subprocess control — designed for `pull_request` jobs, not just local exploration.

```bash
go install github.com/szhekpisov/gomutant@latest
gomutant ./...
```

---

## Why gomutant

### 1.20× faster than gremlins, doing strictly less work

Measured on [diffyml](https://github.com/szhekpisov/diffyml), matched 11-mutator set, M1 Pro 10-core, fresh full-pipeline run:

| Workers | gomutant | gremlins | speedup |
|---|---:|---:|---:|
| 1 | 1134 s | 1848 s | **1.63×** |
| 5 (`NumCPU/2`) | **342 s** | 410 s | **1.20×** |

Per-mutant time is essentially identical (1.79s vs 1.81s) — the speedup is entirely from a tighter mutant set and cache-locality engineering, not faster compilation.

### Honest counts, not inflated ones

On the same diffyml run, **gremlins reports 1168 mutants and 95% efficacy. gomutant reports 1030 mutants and an honest 94%.**

The 138-mutant gap isn't gomutant missing things — it's gremlins counting:

- Mutations on **address-of `&`** as if it were bitwise AND. They never compile. Gremlins counts them as `KILLED`.
- **Double-counted unary `-`** (emitted by both `InvertNegatives` and `ArithmeticBase`). The duplicate fires once, also fails to compile, also `KILLED`.

gomutant rejects the bogus mutations at discovery and labels real compile failures as `NOT_VIABLE` — separate from kills. **Compile errors are not test passes.**

### Run only the tests that matter

For each mutant, gomutant runs **only the tests whose coverage touches the mutated line** — not the entire test suite. This is built from a per-test coverage map computed once per run by compiling each test binary one time and replaying it with `-test.run=<one>` per test.

When the change is on a line covered by 3 of your 400 tests, you run those 3 — not all 400.

### PR-scoped mutation testing as a first-class mode

```bash
# Only test mutants on lines this PR touches vs main:
gomutant --changed-since main ./...
gomutant --changed-since HEAD~1 ./...
```

`--changed-since` runs `git diff --unified=0 <ref>` and keeps only mutants whose line falls inside an added/modified range. Combined with the per-test coverage map, **a typical PR's mutation job drops from minutes to under a minute** — fast enough to gate every pull request. (This very repo's PR job takes ~1 min on a hosted runner.)

This repo's own CI does exactly this: PR job uses `--changed-since` and gates on "no LIVED mutant on changed lines"; post-merge job runs the full tree against an absolute efficacy floor. See [`.github/workflows/mutation.yml`](.github/workflows/mutation.yml).

### Mutators that find bugs the others miss

Five mutators unique to gomutant (vs. token-only tools) target real test-gap classes:

| Mutator | What it catches |
|---|---|
| `BRANCH_IF` / `BRANCH_ELSE` / `BRANCH_CASE` | Tests that exercise the branch but never assert on its effect |
| `EXPRESSION_REMOVE` | Tests that pass when one side of an `&&` / `\|\|` is hard-coded `true` / `false` |
| `STATEMENT_REMOVE` | Tests that don't notice when a statement's side effect disappears |

These find weak assertions that operator-only mutators (the gremlins set) silently approve.

### Generics, no source-tree copies, OOM-safe

- **Generics support.** Byte-level patching, not AST-rewriting — preserves type parameters, instantiations, all of Go's syntax surface.
- **`go test -overlay`** for every mutant. Each worker owns one stable temp file and one stable overlay JSON. The original source tree is never modified.
- **2 GiB per-subprocess RSS cap.** A mutation that flips a loop bound or allocation size can balloon the test binary to tens of gigabytes within seconds. gomutant monitors process-group RSS and `SIGKILL`s the entire tree on cap breach — classified as `TIMED_OUT`, not as a runaway that takes the whole job down.
- **Output capped at 1 MiB per stream.** A panic-loop mutant can't fill the runner disk.

---

## Quick start

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

---

## Configuration

`.gomutant.yml` in the project root:

```yaml
workers: 10
test-cpu: 0           # 0 = let go test use GOMAXPROCS
timeout-coefficient: 10
coverpkg: "./pkg/mypackage/..."
output: mutation-report.json
changed-since: ""     # set to e.g. "main" to scope runs by default
disable: []
only: []
```

Priority: built-in defaults < config file < CLI flags.

## CLI reference

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
| `--version` | | | Print version and exit |

---

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

### Block-level (gomutant-only — finds gaps token mutators miss)

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

---

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

`test_efficacy = killed / (killed + lived)` — **excludes** `not_viable`, `not_covered`, and `timed_out`. Compile errors and timeouts don't masquerade as test successes. That's why the number is honest.

---

## How it works

1. **Resolve packages** via `go list -json`.
2. **Collect coverage** with `go test -coverprofile`. Mutants on uncovered lines are filtered upfront as `NOT_COVERED`.
3. **Measure baseline test time** to set a sane per-mutant timeout (multiplied by `--timeout-coefficient`).
4. **Discover mutants** by walking the AST and emitting byte-level patches. Address-of `&` and unary `-` are detected and skipped at this step (no double-counting, no bogus compile failures).
5. **Build per-test coverage map.** Test binaries are compiled once; each test runs in isolation with `-test.run=<one>` to record the lines it covers.
6. **Test mutants** in parallel:
   - Each worker owns a stable temp source file + overlay JSON.
   - Mutations are applied as byte-level patches; the original tree is never written to.
   - The mutant's covered tests are looked up; only those run via `go test -overlay -run=<regex>`.
   - Each `go test` child runs in its own process group with a 2 GiB RSS cap; output is capped at 1 MiB per stream.

Two performance optimizations layered on top:

- **`GOMAXPROCS=NumCPU/workers` per child.** Without this, `--workers=10` on a 10-core box would have each child also assume 10 cores, oversubscribing 100×. With it, each child compiles + tests within its share.
- **Sort pending mutants by `(Pkg, File, Offset)` before dispatch.** The first mutant in a package pays the cold compile; subsequent ones reuse the build cache for deps and stdlib. This sort alone was a 17% wall-clock reduction.

---

## Benchmarks

Headline numbers were given above. Reproduce with `bash benchmarks/run.sh`. Per-scenario detail in [`benchmarks/results.md`](benchmarks/results.md).

The `workers=5` win is composed of:

- A correct mutant set (1030 vs gremlins' 1168 — 138 of theirs never compile).
- `GOMAXPROCS` capping per child to avoid CPU oversubscription.
- `(Pkg, File, Offset)` dispatch order to keep the build cache hot.

`NumCPU/2` was the historical default before this benchmark; gomutant now defaults to `NumCPU` because the per-child `GOMAXPROCS` cap eliminates the oversubscription failure mode.

### Self-efficacy (gomutant on itself)

gomutant kills **69.32%** of mutants in its own test suite (664 mutants across 8 packages, v0.1.0). Coverage is 97% — most lived mutants are real test gaps, not blind spots. Per-package breakdown in [`testdata/golden/self-efficacy.txt`](testdata/golden/self-efficacy.txt). The `internal/...` subset (excluding `main`) clears 88.03%, which is the gate this repo's CI enforces post-merge.

---

## License

[MIT](LICENSE)
