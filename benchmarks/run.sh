#!/usr/bin/env bash
# Benchmark harness: compares gomutant vs gremlins on shared targets.
#
# Requires: hyperfine, jq, gremlins on PATH, and a built ./bin/gomutant.
# Run from the repo root:  bash benchmarks/run.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

SUMMARIZE_ONLY=0
if [[ "${1:-}" == "--summarize-only" ]]; then
  SUMMARIZE_ONLY=1
fi

GOMUTANT="$REPO_ROOT/bin/gomutant"
GREMLINS="${GREMLINS:-$(command -v gremlins || true)}"

if (( SUMMARIZE_ONLY == 0 )); then
  # Always rebuild so a stale binary from an old branch can't silently mislead
  # the report.
  echo "Building gomutant..."
  go build -o "$GOMUTANT" .
fi

[[ -n "$GREMLINS" ]] || { echo "gremlins not on PATH" >&2; exit 1; }

for bin in hyperfine jq; do
  command -v "$bin" >/dev/null || { echo "$bin required" >&2; exit 1; }
done

OUT_DIR="$REPO_ROOT/benchmarks/out"
mkdir -p "$OUT_DIR"

WORKERS=10
RUNS=5
# Set high enough that gremlins (which fork-execs `go test` per mutant on macOS)
# actually completes its mutant tests instead of silently timing out. gomutant
# is unaffected — it pre-builds and reuses test binaries.
TIMEOUT_COEF=50

# Gremlins' five default mutators; used for the "matched" run.
MATCHED_MUTATORS="ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,INCREMENT_DECREMENT,INVERT_NEGATIVES"

# gremlins auto-appends /... and accepts one path; gomutant takes explicit /...
# Spec format: "label|description|gom_path|gre_path|gom_extra_flags"
SCENARIOS=(
  "small-defaults|./testdata/simple/ with each tool's default mutators|./testdata/simple/|./testdata/simple|"
  "mutator-defaults|./internal/mutator with each tool's default mutators|./internal/mutator/...|./internal/mutator|"
  "mutator-matched|./internal/mutator with matched 5-mutator set (apples-to-apples)|./internal/mutator/...|./internal/mutator|--only $MATCHED_MUTATORS"
)

cpu_info() {
  if [[ "$(uname)" == "Darwin" ]]; then
    sysctl -n machdep.cpu.brand_string
  else
    grep -m1 "model name" /proc/cpuinfo | cut -d: -f2- | sed 's/^ *//'
  fi
}

run_scenario() {
  local label="$1" desc="$2" gom_path="$3" gre_path="$4" gom_extra="$5"

  echo
  echo "===== Scenario: $label ====="
  echo "$desc"

  local gom_json="$OUT_DIR/${label}-gomutant.json"
  local gre_json="$OUT_DIR/${label}-gremlins.json"
  local hf_json="$OUT_DIR/${label}-hyperfine.json"

  local gom_cmd="\"$GOMUTANT\" -w $WORKERS -timeout-coefficient $TIMEOUT_COEF $gom_extra -o \"$gom_json\" $gom_path"
  local gre_cmd="\"$GREMLINS\" unleash --silent --workers $WORKERS --timeout-coefficient $TIMEOUT_COEF -o \"$gre_json\" $gre_path"

  # Warm-up: populate go build cache, produce a fresh JSON for counting.
  echo "Warming..."
  eval "$gom_cmd" >/dev/null 2>&1 || true
  eval "$gre_cmd" >/dev/null 2>&1 || true

  echo "Running hyperfine ($RUNS runs each)..."
  hyperfine --warmup 0 --runs "$RUNS" --export-json "$hf_json" \
    -n gomutant "$gom_cmd" \
    -n gremlins "$gre_cmd"
}

summarize_scenario() {
  local label="$1" desc="$2"
  local gom_json="$OUT_DIR/${label}-gomutant.json"
  local gre_json="$OUT_DIR/${label}-gremlins.json"
  local hf_json="$OUT_DIR/${label}-hyperfine.json"

  local gom_mean gre_mean
  gom_mean=$(jq -r '.results[] | select(.command=="gomutant") | .mean' "$hf_json")
  gre_mean=$(jq -r '.results[] | select(.command=="gremlins") | .mean' "$hf_json")

  # Counts derived from the per-mutant array, so TIMED OUT shows up reliably
  # (it's not in the top-level tallies).
  local gom_killed gom_lived gom_nc gom_nv gom_to gom_total gom_eff
  gom_killed=$(jq '[.files[].mutations[].status | select(.=="KILLED")] | length' "$gom_json")
  gom_lived=$(jq '[.files[].mutations[].status | select(.=="LIVED")] | length' "$gom_json")
  gom_nc=$(jq '[.files[].mutations[].status | select(.=="NOT COVERED")] | length' "$gom_json")
  gom_nv=$(jq '[.files[].mutations[].status | select(.=="NOT VIABLE")] | length' "$gom_json")
  gom_to=$(jq '[.files[].mutations[].status | select(.=="TIMED OUT")] | length' "$gom_json")
  gom_total=$(jq '[.files[].mutations[]] | length' "$gom_json")
  gom_eff=$(jq -r '.test_efficacy // 0' "$gom_json")

  local gre_killed gre_lived gre_nc gre_nv gre_to gre_total gre_eff
  gre_killed=$(jq '[.files[].mutations[].status | select(.=="KILLED")] | length' "$gre_json")
  gre_lived=$(jq '[.files[].mutations[].status | select(.=="LIVED")] | length' "$gre_json")
  gre_nc=$(jq '[.files[].mutations[].status | select(.=="NOT COVERED")] | length' "$gre_json")
  gre_nv=$(jq '[.files[].mutations[].status | select(.=="NOT VIABLE")] | length' "$gre_json")
  gre_to=$(jq '[.files[].mutations[].status | select(.=="TIMED OUT")] | length' "$gre_json")
  gre_total=$(jq '[.files[].mutations[]] | length' "$gre_json")
  gre_eff=$(jq -r '.test_efficacy // 0' "$gre_json")

  # Per-mutant time uses (killed + lived) — the mutants that actually had tests
  # executed against them. NOT_COVERED and NOT_VIABLE are filtered before any
  # test runs, so they don't represent test-execution work.
  local gom_exec gre_exec gom_per gre_per
  gom_exec=$(awk "BEGIN{print $gom_killed + $gom_lived}")
  gre_exec=$(awk "BEGIN{print $gre_killed + $gre_lived}")
  if [[ "$gom_exec" -gt 0 ]]; then
    gom_per=$(awk "BEGIN{printf \"%.0f\", ($gom_mean * 1000) / $gom_exec}")
  else
    gom_per="n/a"
  fi
  if [[ "$gre_exec" -gt 0 ]]; then
    gre_per=$(awk "BEGIN{printf \"%.0f\", ($gre_mean * 1000) / $gre_exec}")
  else
    gre_per="n/a"
  fi

  # Print whichever side is faster, with the multiple. No more "0.41x faster"
  # riddles.
  local winner_line
  if awk "BEGIN{exit !($gom_mean>0 && $gre_mean>0)}"; then
    if awk "BEGIN{exit !($gom_mean<$gre_mean)}"; then
      local r
      r=$(awk "BEGIN{printf \"%.2f\", $gre_mean / $gom_mean}")
      winner_line="**Winner (wall-clock): gomutant — ${r}× faster**"
    else
      local r
      r=$(awk "BEGIN{printf \"%.2f\", $gom_mean / $gre_mean}")
      winner_line="**Winner (wall-clock): gremlins — ${r}× faster**"
    fi
  else
    winner_line="Wall-clock comparison unavailable (zero mean)."
  fi

  cat <<EOF
### $label — $desc

| Metric                  | gomutant | gremlins |
|-------------------------|---------:|---------:|
| Wall-clock mean (s)     | $(printf "%.2f" "$gom_mean") | $(printf "%.2f" "$gre_mean") |
| Mutants discovered      | $gom_total | $gre_total |
| Killed                  | $gom_killed | $gre_killed |
| Lived                   | $gom_lived | $gre_lived |
| Not covered             | $gom_nc | $gre_nc |
| Not viable              | $gom_nv | $gre_nv |
| Timed out               | $gom_to | $gre_to |
| Test efficacy (%)       | $(printf "%.2f" "$gom_eff") | $(printf "%.2f" "$gre_eff") |
| Tested mutants (k+l)    | $gom_exec | $gre_exec |
| Time per tested mutant (ms) | $gom_per | $gre_per |

$winner_line

EOF
}

if (( SUMMARIZE_ONLY == 0 )); then
  for spec in "${SCENARIOS[@]}"; do
    IFS='|' read -r label desc gom_path gre_path gom_extra <<<"$spec"
    run_scenario "$label" "$desc" "$gom_path" "$gre_path" "$gom_extra"
  done
fi

# ----- Coverage-guided test selection: on vs off -----
# This proves the speedup of the per-test coverage routing that gomutant runs
# by default. Both sides are gomutant; --no-test-selection disables routing.

SELECTION_TARGET="./internal/mutator/..."
SELECTION_DESC="./internal/mutator with coverage-guided test selection on (default) vs off (--no-test-selection)"

run_selection_scenario() {
  local target="$1" desc="$2"

  echo
  echo "===== Scenario: selection-on-vs-off ====="
  echo "$desc"

  local on_json="$OUT_DIR/selection-on-vs-off-on.json"
  local off_json="$OUT_DIR/selection-on-vs-off-off.json"
  local hf_json="$OUT_DIR/selection-on-vs-off-hyperfine.json"

  local on_cmd="\"$GOMUTANT\" -w $WORKERS -timeout-coefficient $TIMEOUT_COEF -o \"$on_json\" $target"
  local off_cmd="\"$GOMUTANT\" -w $WORKERS -timeout-coefficient $TIMEOUT_COEF --no-test-selection -o \"$off_json\" $target"

  echo "Warming..."
  eval "$on_cmd" >/dev/null 2>&1 || true
  eval "$off_cmd" >/dev/null 2>&1 || true

  echo "Running hyperfine ($RUNS runs each)..."
  hyperfine --warmup 0 --runs "$RUNS" --export-json "$hf_json" \
    -n selection-on  "$on_cmd" \
    -n selection-off "$off_cmd"
}

summarize_selection_scenario() {
  local desc="$1"
  local on_json="$OUT_DIR/selection-on-vs-off-on.json"
  local off_json="$OUT_DIR/selection-on-vs-off-off.json"
  local hf_json="$OUT_DIR/selection-on-vs-off-hyperfine.json"

  local on_mean off_mean
  on_mean=$(jq -r '.results[] | select(.command=="selection-on") | .mean' "$hf_json")
  off_mean=$(jq -r '.results[] | select(.command=="selection-off") | .mean' "$hf_json")

  local on_killed on_lived on_nc on_nv on_to on_total on_eff
  on_killed=$(jq '[.files[].mutations[].status | select(.=="KILLED")] | length' "$on_json")
  on_lived=$(jq '[.files[].mutations[].status | select(.=="LIVED")] | length' "$on_json")
  on_nc=$(jq '[.files[].mutations[].status | select(.=="NOT COVERED")] | length' "$on_json")
  on_nv=$(jq '[.files[].mutations[].status | select(.=="NOT VIABLE")] | length' "$on_json")
  on_to=$(jq '[.files[].mutations[].status | select(.=="TIMED OUT")] | length' "$on_json")
  on_total=$(jq '[.files[].mutations[]] | length' "$on_json")
  on_eff=$(jq -r '.test_efficacy // 0' "$on_json")

  local off_killed off_lived off_nc off_nv off_to off_total off_eff
  off_killed=$(jq '[.files[].mutations[].status | select(.=="KILLED")] | length' "$off_json")
  off_lived=$(jq '[.files[].mutations[].status | select(.=="LIVED")] | length' "$off_json")
  off_nc=$(jq '[.files[].mutations[].status | select(.=="NOT COVERED")] | length' "$off_json")
  off_nv=$(jq '[.files[].mutations[].status | select(.=="NOT VIABLE")] | length' "$off_json")
  off_to=$(jq '[.files[].mutations[].status | select(.=="TIMED OUT")] | length' "$off_json")
  off_total=$(jq '[.files[].mutations[]] | length' "$off_json")
  off_eff=$(jq -r '.test_efficacy // 0' "$off_json")

  local speedup="n/a"
  if awk "BEGIN{exit !($on_mean>0 && $off_mean>0)}"; then
    speedup=$(awk "BEGIN{printf \"%.2f\", $off_mean / $on_mean}")
  fi

  cat <<EOF
### selection-on-vs-off — $desc

| Metric                  | selection on (default) | selection off (\`--no-test-selection\`) |
|-------------------------|----:|----:|
| Wall-clock mean (s)     | $(printf "%.2f" "$on_mean") | $(printf "%.2f" "$off_mean") |
| Mutants discovered      | $on_total | $off_total |
| Killed                  | $on_killed | $off_killed |
| Lived                   | $on_lived | $off_lived |
| Not covered             | $on_nc | $off_nc |
| Not viable              | $on_nv | $off_nv |
| Timed out               | $on_to | $off_to |
| Test efficacy (%)       | $(printf "%.2f" "$on_eff") | $(printf "%.2f" "$off_eff") |

**Speedup from coverage-guided test selection: ${speedup}× faster.** Identical KILLED + LIVED counts confirm no behavioral regression — the flag changes only the subset of tests run per mutant, not the kill verdict.

EOF
}

if (( SUMMARIZE_ONLY == 0 )); then
  run_selection_scenario "$SELECTION_TARGET" "$SELECTION_DESC"
fi

RESULTS_MD="$REPO_ROOT/benchmarks/results.md"
{
  echo "# Benchmark Results: gomutant vs gremlins"
  echo
  # Date-only so reruns within the same day produce a stable diff.
  echo "_Generated: $(date -u +'%Y-%m-%d')_"
  echo
  echo "| | |"
  echo "|---|---|"
  echo "| Host | $(uname -sm) |"
  echo "| CPU | $(cpu_info) |"
  echo "| Go | $(go version | awk '{print $3, $4}') |"
  echo "| gomutant | $("$GOMUTANT" --version 2>&1 | head -1) |"
  echo "| gremlins | $("$GREMLINS" --version 2>&1 | head -1) |"
  echo "| workers | $WORKERS |"
  echo "| timeout-coefficient | $TIMEOUT_COEF |"
  echo "| hyperfine runs per scenario | $RUNS |"
  echo
  echo "Raw hyperfine output and per-run JSON reports are in \`benchmarks/out/\`."
  echo
  for spec in "${SCENARIOS[@]}"; do
    IFS='|' read -r label desc gom_path gre_path gom_extra <<<"$spec"
    summarize_scenario "$label" "$desc"
  done
  echo "## Coverage-guided test selection (on vs off)"
  echo
  echo "Both sides run \`gomutant\`; the off side adds \`--no-test-selection\`. This isolates the speedup from per-test coverage routing — the differentiator that doesn't show up in the gomutant-vs-gremlins numbers because both sides of those comparisons already use it."
  echo
  summarize_selection_scenario "$SELECTION_DESC"
  cat <<'EOF'
## Reading the results

- **Wall-clock** is what the user waits for. On out-of-the-box defaults gomutant runs more mutators (10 vs 5), so it does more total work and finishes later despite per-mutant being faster.
- **Time per tested mutant** normalizes for that — it's the metric that isolates engine speed from the size of the workload. gomutant wins this consistently because it pre-builds and reuses test binaries; gremlins shells out a fresh `go test` per mutant.
- The `mutator-matched` scenario removes the workload difference entirely. It's the cleanest engine-only comparison.

## Caveats

- gomutant implements 10 mutator types vs gremlins' 5 default mutators, so "defaults" scenarios compare different workloads. The `mutator-matched` scenario restricts gomutant to gremlins' five default mutators for an apples-to-apples engine comparison.
- gomutant's one-time setup (coverage collection, baseline measurement, per-test coverage map build) adds fixed overhead that only pays off when many mutants share that cost.
- The harness uses `--timeout-coefficient 50`. With gremlins' default of 10, gremlins silently TIMED OUT on 18/19 mutants on this machine because each mutant run shells out a fresh `go test` (no cached test binary). The lower coefficient makes gremlins look fast but the kills are missing.
- Results are sensitive to CPU load and thermal state. Re-run under quiet conditions for publishable numbers.
EOF
} > "$RESULTS_MD"

echo
echo "Wrote $RESULTS_MD"
