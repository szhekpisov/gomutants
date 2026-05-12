# Benchmark Results: gomutants vs gremlins

_Generated: 2026-05-12_

| | |
|---|---|
| Host | Darwin arm64 |
| CPU | Apple M1 Pro |
| Go | go1.26.1 darwin/arm64 |
| gomutants | gomutants vv0.2.3-0.20260511150126-904647358c17 (commit: 904647358c176a6a95c681384ec08a98a06287dd, built: 2026-05-11T15:01:26Z) |
| gremlins | gremlins version dev darwin/arm64 |
| workers | 10 |
| timeout-coefficient | 50 |
| hyperfine runs per scenario | 5 |

Raw hyperfine output and per-run JSON reports are in `benchmarks/out/`.

### small-defaults — ./testdata/simple/ with each tool's default mutators

| Metric                  | gomutants | gremlins |
|-------------------------|----------:|---------:|
| Wall-clock mean (s)     | 6.39 | 4.49 |
| Mutants discovered      | 36 | 20 |
| Killed                  | 29 | 11 |
| Lived                   | 3 | 3 |
| Not covered             | 0 | 6 |
| Not viable              | 4 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 90.62 | 78.57 |
| Tested mutants (k+l)    | 32 | 14 |
| Time per tested mutant (ms) | 200 | 321 |

**Winner (wall-clock): gremlins — 1.42× faster**

### mutator-defaults — ./internal/mutator with each tool's default mutators

| Metric                  | gomutants | gremlins |
|-------------------------|----------:|---------:|
| Wall-clock mean (s)     | 20.08 | 8.66 |
| Mutants discovered      | 132 | 28 |
| Killed                  | 119 | 28 |
| Lived                   | 0 | 0 |
| Not covered             | 0 | 0 |
| Not viable              | 13 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 100.00 | 100.00 |
| Tested mutants (k+l)    | 119 | 28 |
| Time per tested mutant (ms) | 169 | 309 |

**Winner (wall-clock): gremlins — 2.32× faster**

### mutator-matched — ./internal/mutator with matched 5-mutator set (apples-to-apples)

| Metric                  | gomutants | gremlins |
|-------------------------|----------:|---------:|
| Wall-clock mean (s)     | 6.08 | 8.74 |
| Mutants discovered      | 28 | 28 |
| Killed                  | 28 | 28 |
| Lived                   | 0 | 0 |
| Not covered             | 0 | 0 |
| Not viable              | 0 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 100.00 | 100.00 |
| Tested mutants (k+l)    | 28 | 28 |
| Time per tested mutant (ms) | 217 | 312 |

**Winner (wall-clock): gomutants — 1.44× faster**

## Reading the results

- **Wall-clock** is what the user waits for. On out-of-the-box defaults gomutants runs more mutators (16 vs 5), so it does more total work and finishes later despite per-mutant being faster.
- **Time per tested mutant** normalizes for that — it's the metric that isolates engine speed from the size of the workload. gomutants wins this consistently because it pre-builds and reuses test binaries; gremlins shells out a fresh `go test` per mutant.
- The `mutator-matched` scenario removes the workload difference entirely. It's the cleanest engine-only comparison.

## Caveats

- gomutants implements 16 mutator types vs gremlins' 5 default mutators, so "defaults" scenarios compare different workloads. The `mutator-matched` scenario restricts gomutants to gremlins' five default mutators for an apples-to-apples engine comparison.
- gomutants's one-time setup (coverage collection, baseline measurement, per-test coverage map build) adds fixed overhead that only pays off when many mutants share that cost.
- The harness uses `--timeout-coefficient 50`. With gremlins' default of 10, gremlins silently TIMED OUT on 18/19 mutants on this machine because each mutant run shells out a fresh `go test` (no cached test binary). The lower coefficient makes gremlins look fast but the kills are missing.
- Results are sensitive to CPU load and thermal state. Re-run under quiet conditions for publishable numbers.
