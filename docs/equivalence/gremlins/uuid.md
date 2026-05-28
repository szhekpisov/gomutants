# Mutator-set equivalence on google/uuid vs gremlins v0.6.0

_Last measured: 2026-05-28 on a 10-core Apple M1 Pro under macOS 26.3.1._

This document is a one-time evidentiary check that, on the mutators they
have in common, gomutants and the `github.com/go-gremlins/gremlins`
mutation tester generate the same mutants on `github.com/google/uuid`,
and that every divergence has a documented root cause that either
reflects a documented design choice in gomutants or a NOT-VIABLE
candidate gremlins surfaces and gomutants correctly excludes.

The purpose is to give a reader a reproducible, position-level answer to
"does gomutants do the same job as gremlins?" without re-running the
comparison to trust the answer.

## TL;DR

On the 11-mutator intersection of the two tools, against
`google/uuid v1.6.0`, both invoked in `--dry-run` mode (discovery
without test execution):

- **All 201 gomutants discoveries land on the identical
  `(file, line, column, mutator_type)` as a gremlins discovery.** Zero
  gomutants-only positions.
- **gremlins generates 4 additional positions** (205 vs 201 total).
  Three are the *same* `-1` mutation gomutants generates, but classified
  under `ARITHMETIC_BASE` instead of `INVERT_NEGATIVES`: gremlins's
  token-based scan does not distinguish unary minus from binary
  subtraction. The fourth is `&nu.UUID → |nu.UUID` on `null.go:115`, a
  syntactically invalid mutation gremlins emits because it treats the
  unary address-of `&` as bitwise AND.
- **9 of 11 mutator families have byte-for-byte identical totals.** The
  −4 in the gomutants column is concentrated in two families and is
  fully attributed in §6.

Conclusion: on uuid, gomutants and gremlins generate the **identical
set of viable mutations** across their shared mutator set. The only
real difference is that gremlins's token-level scan ignores the
Go-AST distinction between unary and binary operators, surfacing one
NOT-VIABLE candidate and re-labeling three `-1` mutations under a
different family.

## Versions pinned for this report

| component | version / commit |
|---|---|
| `github.com/google/uuid` | v1.6.0 (`0f11ee6918f41a04c201eceeadf612a377bc7fbc`) |
| `github.com/go-gremlins/gremlins` | v0.6.0 (released 2025-12-06; latest at write time) |
| `gomutants` | `997e1f2dfef0998f5669ed02ce636a5883b9b89b` (this repo's `HEAD` at write time) |
| Go toolchain | 1.26.3 (gomutants build); 1.26.1 (gremlins binary) |

## Methodology

### Mutator mapping

Both tools name their mutator types from the same canonical set, except
for two abbreviations gremlins uses internally. The full intersection
is 11 mutators:

| canonical name | gremlins CLI flag | gremlins per-mutation `type` field |
|---|---|---|
| ARITHMETIC_BASE | `--arithmetic-base` | `ARITHMETIC_BASE` |
| CONDITIONALS_BOUNDARY | `--conditionals-boundary` | `CONDITIONALS_BOUNDARY` |
| CONDITIONALS_NEGATION | `--conditionals-negation` | `CONDITIONALS_NEGATION` |
| INCREMENT_DECREMENT | `--increment-decrement` | `INCREMENT_DECREMENT` |
| INVERT_ASSIGNMENTS | `--invert-assignments` | `INVERT_ASSIGNMENTS` |
| INVERT_BITWISE | `--invert-bitwise` | `INVERT_BITWISE` |
| INVERT_BITWISE_ASSIGNMENTS | `--invert-bwassign` | **`INVERT_BWASSIGN`** |
| INVERT_LOGICAL | `--invert-logical` | `INVERT_LOGICAL` |
| INVERT_LOOP_CTRL | `--invert-loopctrl` | **`INVERT_LOOPCTRL`** |
| INVERT_NEGATIVES | `--invert-negatives` | `INVERT_NEGATIVES` |
| REMOVE_SELF_ASSIGNMENTS | `--remove-self-assignments` | `REMOVE_SELF_ASSIGNMENTS` |

The two bold rows are normalized in the comparator:
`INVERT_BWASSIGN` → `INVERT_BITWISE_ASSIGNMENTS`, `INVERT_LOOPCTRL` →
`INVERT_LOOP_CTRL`. The abbreviations only appear in gremlins's
per-mutation `type` field; the `OutputResult.MutatorStatistics` struct
JSON tags use the canonical full names, so the abbreviation is internal
inconsistency in gremlins itself, not a tool divergence.

Mutators outside this intersection were excluded from both invocations:

- **gremlins-only**: none. Every gremlins v0.6.0 mutator is in
  gomutants.
- **gomutants-only**: `BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE`,
  `EXPRESSION_REMOVE`, `STATEMENT_REMOVE`, `INTEGER_INCREMENT`,
  `INTEGER_DECREMENT`, `FLOAT_INCREMENT`, `FLOAT_DECREMENT`,
  `LOOP_CONDITION`, `RANGE_BREAK` (11 mutators).

Gremlins ships with **6 of the 11 intersection mutators disabled by
default** (`internal/configuration/mutantenabled.go` in gremlins
v0.6.0): `InvertAssignments`, `InvertBitwise`,
`InvertBitwiseAssignments`, `InvertLogical`, `InvertLoopCtrl`,
`RemoveSelfAssignments`. Running gremlins with default flags would
produce a misleading 96-mutant gap that has nothing to do with discovery
behavior. The reproduction recipe below enables all 11 explicitly.

### Reproduction

The whole comparison runs from a clean shell with Go ≥ 1.22 and
Python 3. Working directory is parameterizable; the examples use
`/tmp/eq-gremlins`.

```bash
set -euo pipefail
GOMUTANTS_REPO=${GOMUTANTS_REPO:-$PWD}
mkdir -p /tmp/eq-gremlins/bin && cd /tmp/eq-gremlins
go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
git clone --depth 1 --branch v1.6.0 https://github.com/google/uuid.git uuid-gremlins
git clone --depth 1 --branch v1.6.0 https://github.com/google/uuid.git uuid-gomutants
git -C "$GOMUTANTS_REPO" rev-parse HEAD
( cd "$GOMUTANTS_REPO" && go build -o /tmp/eq-gremlins/bin/gomutants . )
```

**Run gremlins** with all 11 intersection mutators explicitly enabled,
in `--dry-run` mode so test execution is skipped:

```bash
cd /tmp/eq-gremlins/uuid-gremlins
gremlins unleash --dry-run --output /tmp/eq-gremlins/gremlins.json \
  --arithmetic-base=true --conditionals-boundary=true --conditionals-negation=true \
  --increment-decrement=true --invert-assignments=true --invert-bitwise=true \
  --invert-bwassign=true --invert-logical=true --invert-loopctrl=true \
  --invert-negatives=true --remove-self-assignments=true .
```

Note the trailing `.` rather than `./...`: see §7 for why `./...`
silently produces "No results to report" in gremlins v0.6.0.

**Run gomutants** restricted to the same 11-mutator intersection, also
in `--dry-run`:

```bash
cd /tmp/eq-gremlins/uuid-gomutants
/tmp/eq-gremlins/bin/gomutants --dry-run \
  --only ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,\
INCREMENT_DECREMENT,INVERT_ASSIGNMENTS,INVERT_BITWISE,\
INVERT_BITWISE_ASSIGNMENTS,INVERT_LOGICAL,INVERT_LOOP_CTRL,\
INVERT_NEGATIVES,REMOVE_SELF_ASSIGNMENTS \
  > /tmp/eq-gremlins/gomutants.txt
```

`--output` is intentionally omitted: gomutants's `--dry-run` mode writes
the discovered mutants to stdout and ignores `--output`
(`main.go:555-562`). See §7.

### Normalization

Both tools produce one record per discovered mutant containing at least
`(file, line, column, mutator_type)`:

- **Gremlins** writes a JSON document with
  `files[].mutations[]`. Each entry has `file_name`, `line`, `column`,
  `type`. The `file_name` is prefixed with the Go module path
  (`github.com/google/uuid/time.go`); the prefix is stripped to match
  gomutants's repo-relative paths.
- **Gomutants** in `--dry-run` prints
  `[STATUS] file:line:col  original → replacement  (TYPE)` lines to
  stdout. A single regex extracts the four comparison fields per line.

The two mutator-name abbreviations from gremlins
(`INVERT_BWASSIGN`, `INVERT_LOOPCTRL`) are mapped to the canonical
full names before comparison.

## Results

### §5.1 Top-line counts

| metric | gomutants | gremlins |
|---|---:|---:|
| Mutants discovered (total) | 201 | 205 |
| RUNNABLE / PENDING | 187 | 172 |
| NOT COVERED | 14 | 33 |

The NOT COVERED gap reflects different per-line coverage thresholds for
classifying "no test exercises this line". It does not affect *which*
mutants are discovered; only how they're labeled. The comparison key in
§5.4 includes every discovered mutant regardless of status.

### §5.2 Per-mutator total parity

Where Δ ≠ 0, the divergence is investigated in §6.

| canonical type | gomutants | gremlins | Δ (gm − gr) |
|---|---:|---:|---:|
| ARITHMETIC_BASE | 39 | 42 | **−3** |
| CONDITIONALS_BOUNDARY | 7 | 7 | 0 |
| CONDITIONALS_NEGATION | 64 | 64 | 0 |
| INCREMENT_DECREMENT | 0 | 0 | 0 |
| INVERT_ASSIGNMENTS | 4 | 4 | 0 |
| INVERT_BITWISE | 55 | 56 | **−1** |
| INVERT_BITWISE_ASSIGNMENTS | 3 | 3 | 0 |
| INVERT_LOGICAL | 14 | 14 | 0 |
| INVERT_LOOP_CTRL | 0 | 0 | 0 |
| INVERT_NEGATIVES | 8 | 8 | 0 |
| REMOVE_SELF_ASSIGNMENTS | 7 | 7 | 0 |
| **total** | **201** | **205** | **−4** |

9 of 11 families: byte-identical totals. The −4 is concentrated in two
families and is fully explained by §6.1 and §6.2.

### §5.3 Per-(file, type) bucket disagreements

Across all 14 mutated files, only two `(file, type)` buckets disagree:

| file | type | gomutants | gremlins | Δ |
|---|---|---:|---:|---:|
| null.go | INVERT_BITWISE | 0 | 1 | −1 |
| time.go | ARITHMETIC_BASE | 11 | 14 | −3 |
| **sum** | | | | **−4** |

Every other bucket in every file matches exactly.

### §5.4 Identity match

Comparing `(file, line, column, mutator_type)` tuples across all
discovered mutants on both sides:

| set | size |
|---|---:|
| gomutants discoveries | 201 |
| gremlins discoveries | 205 |
| **Matched on `(file, line, column, type)`** | **201** |
| gomutants-only | **0** |
| gremlins-only | 4 |

100% of gomutants's discoveries match a gremlins discovery on the exact
`(file, line, column, type)` tuple. The 4 gremlins-only positions are
attributed in Appendix A.1.

## §6 Root cause analysis

### §6.1 Unary minus classified as ARITHMETIC_BASE

Three of the 4 gremlins-only positions are on identical Go source: a
literal `-1` used as a function-call argument or in a comparison.

| position | source | gremlins family | gomutants family | mutation produced |
|---|---|---|---|---|
| time.go:56:20 | `setClockSequence(-1)` | ARITHMETIC_BASE | INVERT_NEGATIVES | `-` → `+` |
| time.go:84:20 | `setClockSequence(-1)` | ARITHMETIC_BASE | INVERT_NEGATIVES | `-` → `+` |
| time.go:98:12 | `if seq == -1 {` | ARITHMETIC_BASE | INVERT_NEGATIVES | `-` → `+` |

The mutation itself is identical in both tools: `-` → `+` on the `-1`
literal. The disagreement is only the mutator-family label.

The root cause is the scanner model:

- **Gremlins** is token-based: any `token.SUB` token is a candidate for
  the ARITHMETIC_BASE virus, regardless of whether Go's parser would
  classify it as a binary subtraction or a unary minus prefix on an
  integer literal.
- **Gomutants** is AST-based
  (`internal/mutator/arithmetic_base.go` walks `*ast.BinaryExpr` only):
  unary minus is `*ast.UnaryExpr{Op: token.SUB}` and falls under the
  INVERT_NEGATIVES mutator. The same source position therefore yields
  the same `(file, line, column, original → replacement)` mutation, but
  under a different family name.

On a `(file, line, col, original, replacement)` tuple set comparison
these three are identical mutations.

### §6.2 Unary address-of classified as INVERT_BITWISE (NOT VIABLE)

The fourth gremlins-only position is `null.go:115:30`:

```go
err := json.Unmarshal(data, &nu.UUID)
//                          ^ column 30
```

The `&` here is Go's unary address-of operator
(`*ast.UnaryExpr{Op: token.AND}`), not the bitwise-AND binary operator.
Gremlins's token scanner emits this position as an INVERT_BITWISE
candidate (`&` → `|`); the resulting source
`json.Unmarshal(data, |nu.UUID)` is a syntax error because `|` is not a
valid unary operator in Go. The mutant is NOT VIABLE: it cannot
compile, so the mutation can never affect program behavior.

Gomutants's AST scan only considers `*ast.BinaryExpr{Op: token.AND}`
for INVERT_BITWISE and so excludes the address-of position. This is
the only viable-vs-NOT-VIABLE difference between the two discovery
sets.

### §6.3 Default-disabled mutators (not a divergence)

For completeness: gremlins v0.6.0 disables 6 of the 11 intersection
mutators by default. If a user ran gremlins with default flags against
uuid, they would see 109 mutants (the 5 default-enabled families) and
infer a much larger gap. The reproduction recipe in §3 enables all 11
explicitly to expose the real intersection.

## §7 Caveats and tool limitations

- **gomutants `--dry-run` writes to stdout, not JSON.** `--output` is
  ignored under `--dry-run` (`main.go:555-562`). gremlins's
  `--dry-run --output FILE` writes a structured JSON report. For this
  comparison gomutants's stdout is parsed with a regex; aligning
  gomutants to also emit JSON under `--dry-run` would simplify any
  future re-runs of this comparison.
- **`gremlins unleash ./...` produces "No results to report"** in
  v0.6.0; passing the explicit path `.` works. The cause is in
  gremlins's diff-package interaction; not investigated further here.
- **gremlins's per-mutation `type` field uses two abbreviated names**
  (`INVERT_LOOPCTRL`, `INVERT_BWASSIGN`) while
  `OutputResult.MutatorStatistics` JSON tags use the canonical full
  names. The abbreviations are normalized in the comparator. This is
  internal inconsistency in gremlins, not a comparison artifact.
- **Coverage-aware NOT COVERED counts differ.** Both tools run
  `go test -coverprofile` first; the per-line classification thresholds
  differ. This affects the RUNNABLE/NOT-COVERED split (187/14 vs
  172/33), not whether a mutant was discovered.
- **Column position is reliable.** Both tools report 1-based column at
  the AST or token start. All 201 matched tuples agreed on column to
  the byte.

## Appendix A — Divergent positions, fully attributed

### A.1 gremlins reports a mutant, gomutants does not

| file | line | col | gremlins type | mutation | attribution |
|---|---:|---:|---|---|---|
| null.go | 115 | 30 | INVERT_BITWISE | `&` → `\|` on `&nu.UUID` | §6.2 unary address-of; NOT VIABLE candidate |
| time.go | 56 | 20 | ARITHMETIC_BASE | `-` → `+` on `-1` literal | §6.1 same mutation, classified INVERT_NEGATIVES by gomutants |
| time.go | 84 | 20 | ARITHMETIC_BASE | `-` → `+` on `-1` literal | §6.1 same mutation, classified INVERT_NEGATIVES by gomutants |
| time.go | 98 | 12 | ARITHMETIC_BASE | `-` → `+` on `-1` literal | §6.1 same mutation, classified INVERT_NEGATIVES by gomutants |

Total: 4 positions. 3 are family reclassifications (the same
`(file, line, col, original → replacement)` mutation is generated by
both tools, but under different mutator-family labels). 1 is a
NOT-VIABLE candidate gomutants's AST scan correctly excludes.

### A.2 gomutants reports a mutant, gremlins does not

(empty)

## Appendix B — How to reproduce in CI

This comparison is not currently wired into CI. The end-to-end check is:

1. Run `gremlins unleash --dry-run --output gr.json` with all 11
   intersection flags from §3 against `github.com/google/uuid@v1.6.0`.
2. Run `gomutants --dry-run --only <11-mutator list>` against the same
   uuid checkout; capture stdout.
3. Parse both outputs, normalize gremlins's two abbreviated names, and
   assert that every gomutants `(file, line, column, type)` tuple is in
   the gremlins tuple set, and that every gremlins-only tuple matches
   either an §6.1 unary-minus reclassification or the §6.2
   address-of NOT-VIABLE pattern.

The reference comparator is short enough to live outside the repo (a
JSON-load + regex + tuple-set diff). The gomutants JSON shape is
stable (`internal/report/json.go`).
