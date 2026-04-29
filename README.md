# gomutant

A mutation testing tool for Go. Drop-in replacement for [go-gremlins](https://github.com/go-gremlins/gremlins) with more mutators, generics support, more accurate mutant discovery, and competitive speed via `go test -overlay` + per-test coverage mapping.

## Features

- **16 mutation types** — token-level and block-level (see below); 5 of them (`BRANCH_IF/ELSE/CASE`, `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`) catch test gaps gremlins doesn't surface
- **Accurate discovery** — distinguishes address-of `&` from bitwise AND, doesn't double-count unary `-`, classifies compile-failure mutations as `NOT_VIABLE` instead of inflating the kill count
- **Generics support** — byte-level patching preserves all Go syntax
- **Per-test coverage mapping** — compiles test binaries once, runs only the tests that cover each mutant
- **Parallel** — worker pool with `go test -overlay` (no source tree copies); each child capped at `GOMAXPROCS=NumCPU/workers` to avoid CPU oversubscription
- **Gremlins-compatible** — accepts the `unleash` subcommand; JSON report shape matches
- **Configurable** — YAML config file + CLI flags

## Installation

```bash
go install github.com/szhekpisov/gomutant@latest
```

## Usage

```bash
# Run on all packages
gomutant ./...

# Run on specific packages
gomutant ./pkg/mypackage/...

# Dry run — list mutants without testing
gomutant --dry-run ./...

# Verbose output — show each mutant as tested
gomutant -v ./...

# Run only specific mutator types
gomutant --only ARITHMETIC_BASE,CONDITIONALS_NEGATION ./...

# Disable specific mutator types
gomutant --disable BRANCH_IF,BRANCH_ELSE ./...

# Custom worker count and timeout
gomutant -w 8 --timeout-coefficient 15 ./...

# Specify coverage package pattern
gomutant --coverpkg ./pkg/mypackage/... ./...

# Custom output path
gomutant -o report.json ./...
```

### Gremlins compatibility

The `unleash` subcommand is accepted and ignored for CLI compatibility:

```bash
gomutant unleash ./...
```

## CLI Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--workers` | `-w` | NumCPU | Parallel workers |
| `--timeout-coefficient` | | 10 | Multiply baseline test time for per-mutant timeout |
| `--coverpkg` | | | Coverage package pattern (passed to `go test -coverpkg`) |
| `--output` | `-o` | `mutation-report.json` | JSON report path |
| `--config` | | `.gomutant.yml` | Config file path |
| `--disable` | | | Comma-separated mutator types to disable |
| `--only` | | | Comma-separated mutator types to run (disables all others) |
| `--dry-run` | | false | List mutants without testing |
| `--verbose` | `-v` | false | Show each mutant as tested |
| `--version` | | | Print version and exit |

## Configuration

Create a `.gomutant.yml` in your project root:

```yaml
workers: 10
timeout-coefficient: 10
coverpkg: "./pkg/mypackage/..."
output: mutation-report.json
```

Priority: defaults < config file < CLI flags.

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

## JSON Report

Output is compatible with the gremlins JSON format:

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

## Mutant Statuses

| Status | Meaning |
|--------|---------|
| KILLED | Test failed — mutant detected |
| LIVED | Tests passed — mutant survived (test gap) |
| NOT COVERED | No test covers this code |
| NOT VIABLE | Mutant causes compile error |
| TIMED OUT | Test execution exceeded timeout |

## How It Works

1. **Resolve packages** — `go list -json` to find source files
2. **Collect coverage** — `go test -coverprofile` to identify covered code
3. **Measure baseline** — run tests once to establish timeout threshold
4. **Discover mutants** — parse AST, apply mutators, filter by coverage
5. **Build test map** — compile test binaries once, map tests to covered lines
6. **Test mutants** — parallel workers apply mutations via `go test -overlay`

Each worker owns a stable temp file. Mutations are applied as byte-level patches on the original source, written to the temp file, and injected via Go's overlay mechanism. The original source tree is never modified.

## Benchmarks

Latest measurement on [diffyml](https://github.com/szhekpisov/diffyml) (matched 11-mutator set, M1 Pro 10-core, fresh full-pipeline run):

| Workers | gomutant | gremlins |
|---|---:|---:|
| 1 | 1134 s | 1848 s |
| 5 (`NumCPU/2`) | **342 s** | 410 s |

At workers=5, **gomutant is ~1.20× faster wall-clock** than gremlins on this workload, and ~1.6× faster sequentially. Per-mutant time is essentially identical (1.79s vs 1.81s) — gomutant's wall-clock win comes from doing strictly less work: it discovers 1030 real mutants while gremlins reports 1168, and the 138-mutant gap is bogus mutations on address-of `&` (mutated as bitwise AND) and unary `-` (double-counted as both `InvertNegatives` and `ArithmeticBase`) that gremlins silently classifies as `KILLED`.

The workers=5 number reflects two optimizations layered on the original engine: capping each child `go test`'s `GOMAXPROCS` to avoid CPU oversubscription, and sorting pending mutants by `(Pkg, File, Offset)` before dispatch so the per-package build cache stays hot across consecutive mutants. The sort alone was a 17% wall-clock reduction — found via an autoresearch loop after several flag-tuning hypotheses (`-p` cap, per-worker `GOTMPDIR`, `-trimpath`, `GOMAXPROCS=1`, separate cache pre-warm) turned out not to help. NumCPU/2 was the historical default before this benchmark — gomutant now defaults to NumCPU.

What gomutant adds beyond raw speed:

- **Discovers a tighter, more accurate mutant set.** On the same diffyml run, gremlins reports 1168 mutants but ~138 are bogus mutations on address-of `&` and unary `-` that always fail to compile and get silently counted as `KILLED`, inflating its 95% efficacy. gomutant correctly skips those, reports 1030 real mutants and an honest 94% efficacy.
- **Real `NOT_VIABLE` classification.** Mutations that cause compile failure (e.g. `%` → `*` on a float) are reported separately, not folded into kills.
- **More mutator coverage on test-gap-finding constructs.** The block-level mutators (`BRANCH_IF/ELSE/CASE`, `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`) flag a class of weak tests that token-only tools miss.

Reproduce with `bash benchmarks/run.sh`. Per-scenario detail in [`benchmarks/results.md`](benchmarks/results.md).

### Self-efficacy

gomutant kills **69.32%** of mutants in its own test suite (664 mutants across 8 packages). Coverage is high (97% of mutants are exercised by tests), but several packages — especially `main` (39.56%) and `internal/report` (61.70%) — still have meaningful test gaps that we plan to close. Per-package breakdown: [`testdata/golden/self-efficacy.txt`](testdata/golden/self-efficacy.txt).

## License

[MIT](LICENSE)
