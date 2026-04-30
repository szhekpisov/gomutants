# Benchmark Results: gomutants vs gremlins

_Generated: 2026-04-28_

| | |
|---|---|
| Host | Darwin arm64 |
| CPU | Apple M1 Pro |
| Go | go1.26.1 darwin/arm64 |
| gomutants | gomutants v0.1.0 |
| gremlins | gremlins version dev darwin/arm64 |
| workers | 10 |
| timeout-coefficient | 50 |
| hyperfine runs per scenario | 5 |

Raw hyperfine output and per-run JSON reports are in `benchmarks/out/`.

### small-defaults — ./testdata/simple/ with each tool's default mutators

| Metric                  | gomutants | gremlins |
|-------------------------|---------:|---------:|
| Wall-clock mean (s)     | 6.13 | 4.69 |
| Mutants discovered      | 34 | 20 |
| Killed                  | 21 | 11 |
| Lived                   | 3 | 3 |
| Not covered             | 6 | 6 |
| Not viable              | 4 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 87.50 | 78.57 |
| Tested mutants (k+l)    | 24 | 14 |
| Time per tested mutant (ms) | 255 | 335 |

**Winner (wall-clock): gremlins — 1.31× faster**

### mutator-defaults — ./internal/mutator with each tool's default mutators

| Metric                  | gomutants | gremlins |
|-------------------------|---------:|---------:|
| Wall-clock mean (s)     | 14.65 | 6.03 |
| Mutants discovered      | 90 | 19 |
| Killed                  | 79 | 19 |
| Lived                   | 1 | 0 |
| Not covered             | 0 | 0 |
| Not viable              | 10 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 98.75 | 100.00 |
| Tested mutants (k+l)    | 80 | 19 |
| Time per tested mutant (ms) | 183 | 317 |

**Winner (wall-clock): gremlins — 2.43× faster**

### mutator-matched — ./internal/mutator with matched 5-mutator set (apples-to-apples)

| Metric                  | gomutants | gremlins |
|-------------------------|---------:|---------:|
| Wall-clock mean (s)     | 4.64 | 5.98 |
| Mutants discovered      | 19 | 19 |
| Killed                  | 19 | 19 |
| Lived                   | 0 | 0 |
| Not covered             | 0 | 0 |
| Not viable              | 0 | 0 |
| Timed out               | 0 | 0 |
| Test efficacy (%)       | 100.00 | 100.00 |
| Tested mutants (k+l)    | 19 | 19 |
| Time per tested mutant (ms) | 244 | 315 |

**Winner (wall-clock): gomutants — 1.29× faster**

## Reading the results

- **Wall-clock** is what the user waits for. On out-of-the-box defaults gomutants runs more mutators (10 vs 5), so it does more total work and finishes later despite per-mutant being faster.
- **Time per tested mutant** normalizes for that — it's the metric that isolates engine speed from the size of the workload. gomutants wins this consistently because it pre-builds and reuses test binaries; gremlins shells out a fresh `go test` per mutant.
- The `mutator-matched` scenario removes the workload difference entirely. It's the cleanest engine-only comparison.

## Caveats

- gomutants implements 10 mutator types vs gremlins' 5 default mutators, so "defaults" scenarios compare different workloads. The `mutator-matched` scenario restricts gomutants to gremlins' five default mutators for an apples-to-apples engine comparison.
- gomutants's one-time setup (coverage collection, baseline measurement, per-test coverage map build) adds fixed overhead that only pays off when many mutants share that cost.
- The harness uses `--timeout-coefficient 50`. With gremlins' default of 10, gremlins silently TIMED OUT on 18/19 mutants on this machine because each mutant run shells out a fresh `go test` (no cached test binary). The lower coefficient makes gremlins look fast but the kills are missing.
- Results are sensitive to CPU load and thermal state. Re-run under quiet conditions for publishable numbers.
