# Benchmarks

Compares gomutant against [go-gremlins/gremlins](https://github.com/go-gremlins/gremlins) on shared Go targets.

## Prerequisites

```bash
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest
brew install hyperfine jq        # macOS
# or: apt-get install hyperfine jq
```

## Running

From the repo root:

```bash
go build -o bin/gomutant .
bash benchmarks/run.sh
```

The script runs three scenarios with hyperfine (5 runs each):

| Scenario | Target | Notes |
|---|---|---|
| `small-defaults` | `./testdata/simple/` | Each tool with its own default mutators. Shows fixed-overhead cost on tiny inputs. |
| `mutator-defaults` | `./internal/mutator` | Each tool with its own default mutators. Real-world out-of-the-box comparison. |
| `mutator-matched` | `./internal/mutator` | gomutant restricted to gremlins' five default mutators (`--only`). Engine-speed comparison on an identical workload. |

Results are written to `benchmarks/results.md`; raw hyperfine JSON and per-tool reports land in `benchmarks/out/` (gitignored).

## Why these targets

- `./testdata/simple/` is a ~70-line package with complete tests — small enough that fixed setup dominates wall-clock time.
- `./internal/mutator` is ~1k lines of AST-mutation logic with fast, deterministic unit tests. Enough mutants to amortize setup, without the heavy `go test` fan-out that `internal/runner` and `internal/coverage` incur.

Running on the whole `./internal/...` tree takes ~9 minutes per tool per run because `internal/runner`'s tests shell out to `go test`; mutation testing that against hundreds of mutants multiplies the cost. For repeatable CI-friendly benchmarks we exclude those packages.

## Caveats

- gomutant ships 10 mutator types; gremlins has 5 enabled by default. `mutator-defaults` compares out-of-the-box behaviour (different workloads); `mutator-matched` compares engine speed on the same mutant set.
- gremlins' `mutants_total` excludes `NOT COVERED` and `TIMED OUT`; the summary uses `sum(mutator_statistics)` to recover a pre-filter discovered-count.
- Wall-clock results are sensitive to background load and thermal state. Rerun under quiet conditions before publishing numbers.
