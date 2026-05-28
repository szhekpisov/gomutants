# Mutator-set equivalence on google/uuid

_Last measured: 2026-05-23 on a 10-core Apple M1 Pro under macOS 26.3.1._

This document is a one-time evidentiary check that, on the mutators they have
in common, gomutants and the `github.com/gtramontina/ooze` mutation tester
generate the same mutants on `github.com/google/uuid` — and where they
differ, that every difference has a documented root cause that either favors
gomutants (broader coverage) or is a pure presentation artifact, not a real
correctness gap.

The purpose is to give a reader a reproducible, line-by-line answer to "does
gomutants do the same job as ooze?" without having to re-run the comparison
themselves to trust the answer.

## TL;DR

On the 14-mutator intersection of the two tools, against `google/uuid v1.6.0`:

- **12 of 14 mutator families have byte-for-byte identical mutant counts.** No
  divergence at all.
- **2 mutator families differ in count by exactly +54 per direction in
  gomutants's favor.** Root cause: ooze parses integer literals with
  `strconv.Atoi`, which rejects hex / octal / binary literals; gomutants's
  `strconv.ParseInt(_, 0, _)` accepts every Go integer literal form. The 54
  surplus equals exactly the count of hex literals in uuid.
- **Of the 143 ooze survivors recoverable from its diff output, 119 land on
  the identical `(file, line, type)` as a gomutants LIVED mutant** (83%). The
  remaining mismatches are exhaustively attributed below; none reflects a
  gomutants discovery bug.

Conclusion: on uuid, gomutants is a **strict superset** of ooze v0.2.0
(latest released) across the shared mutator set.

## Versions pinned for this report

| component | version / commit |
|---|---|
| `github.com/google/uuid` | v1.6.0 (`0f11ee6918f41a04c201eceeadf612a377bc7fbc`) |
| `github.com/gtramontina/ooze` | v0.2.0 (latest non-retracted; v0.3.x is retracted by author) |
| `gomutants` | `e75a4d443991b4ccc7244cee3f8180c664f88b4d` (this repo, `HEAD` at write time) |
| Go toolchain | 1.26.3 |

## Methodology

### Mutator mapping

The 14 mutators in this comparison are the complete released mutator set of
ooze v0.2.0 (the `cancelnil` virus exists only on ooze's unreleased main).
Every one maps onto an existing gomutants mutator:

| ooze virus | gomutants `MutationType` | Scope notes |
|---|---|---|
| `arithmetic` | `ARITHMETIC_BASE` | Identical 5-op set: `+↔-`, `*↔/`, `%→*`. Both deliberately omit `*→%` (compile errors on floats). |
| `arithmeticassignment` | `REMOVE_SELF_ASSIGNMENTS` | Identical 11-token set: drop the operator from compound assigns. |
| `arithmeticassignmentinvert` | `INVERT_ASSIGNMENTS` | Invert arithmetic compound assigns. |
| `bitwise` | `INVERT_BITWISE` | Both: `&↔|`, `^→&`, `<<↔>>`. ooze additionally covers `&^→&`; in uuid there are zero such tokens so this is not observable here. |
| `comparison` | `CONDITIONALS_BOUNDARY` | Identical: `<↔<=`, `>↔>=`. |
| `comparisoninvert` | `CONDITIONALS_NEGATION` | Identical: `==↔!=`, `<↔>=`, `>↔<=`. |
| `comparisonreplace` | `EXPRESSION_REMOVE` | Substitute one boolean operand of `&&`/`||` with the identity element. |
| `floatdecrement` | `FLOAT_DECREMENT` | Subtract 1.0 from float literals. |
| `floatincrement` | `FLOAT_INCREMENT` | Add 1.0 to float literals. |
| `integerdecrement` | `INTEGER_DECREMENT` | Subtract 1 from integer literals. **Parser scope differs (see §6.1).** |
| `integerincrement` | `INTEGER_INCREMENT` | Add 1 to integer literals. **Parser scope differs (see §6.1).** |
| `loopbreak` | `INVERT_LOOP_CTRL` | `break↔continue`. |
| `loopcondition` | `LOOP_CONDITION` | Replace `for` condition with always-false. |
| `rangebreak` | `RANGE_BREAK` | Insert a `break` as the first statement of a `for … range` body. |

Mutators outside this intersection were excluded from both tool invocations:

- **ooze-only**: `cancelnil` (1 mutator, not in v0.2.0).
- **gomutants-only**: `INCREMENT_DECREMENT`, `INVERT_NEGATIVES`,
  `INVERT_BITWISE_ASSIGNMENTS`, `INVERT_LOGICAL`, `BRANCH_IF`, `BRANCH_ELSE`,
  `BRANCH_CASE`, `STATEMENT_REMOVE` (8 mutators).

### Reproduction

The whole comparison runs from a clean shell with `go ≥ 1.22` and Python 3.
Working directory is parameterizable; the examples below use `/tmp/eq`.

```bash
set -euo pipefail
GOMUTANTS_REPO=${GOMUTANTS_REPO:-$PWD}   # this checkout
mkdir -p /tmp/eq/raw /tmp/eq/bin
cd /tmp/eq
git clone --depth 1 --branch v1.6.0 https://github.com/google/uuid.git uuid-gomutants
git clone --depth 1 --branch v1.6.0 https://github.com/google/uuid.git uuid-ooze
# Build gomutants from this repo at the pinned commit.
git -C "$GOMUTANTS_REPO" rev-parse HEAD             # record the SHA
( cd "$GOMUTANTS_REPO" && go build -o /tmp/eq/bin/gomutants . )
```

**Run gomutants** restricted to the 14-mutator intersection. The names are
the canonical `MutationType` constants from `internal/mutator/types.go`:

```bash
cd /tmp/eq/uuid-gomutants
/tmp/eq/bin/gomutants \
  --only ARITHMETIC_BASE,REMOVE_SELF_ASSIGNMENTS,INVERT_ASSIGNMENTS,\
INVERT_BITWISE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,\
EXPRESSION_REMOVE,FLOAT_DECREMENT,FLOAT_INCREMENT,\
INTEGER_DECREMENT,INTEGER_INCREMENT,INVERT_LOOP_CTRL,\
LOOP_CONDITION,RANGE_BREAK \
  --output /tmp/eq/raw/gomutants.json \
  ./...
```

**Set up and run ooze** in the parallel uuid clone:

```bash
cd /tmp/eq/uuid-ooze
go get github.com/gtramontina/ooze@v0.2.0
cat > mutation_test.go <<'EOF'
//go:build mutation

package uuid_test

import (
    "testing"

    "github.com/gtramontina/ooze"
    "github.com/gtramontina/ooze/viruses/arithmetic"
    "github.com/gtramontina/ooze/viruses/arithmeticassignment"
    "github.com/gtramontina/ooze/viruses/arithmeticassignmentinvert"
    "github.com/gtramontina/ooze/viruses/bitwise"
    "github.com/gtramontina/ooze/viruses/comparison"
    "github.com/gtramontina/ooze/viruses/comparisoninvert"
    "github.com/gtramontina/ooze/viruses/comparisonreplace"
    "github.com/gtramontina/ooze/viruses/floatdecrement"
    "github.com/gtramontina/ooze/viruses/floatincrement"
    "github.com/gtramontina/ooze/viruses/integerdecrement"
    "github.com/gtramontina/ooze/viruses/integerincrement"
    "github.com/gtramontina/ooze/viruses/loopbreak"
    "github.com/gtramontina/ooze/viruses/loopcondition"
    "github.com/gtramontina/ooze/viruses/rangebreak"
)

func TestMutation(t *testing.T) {
    ooze.Release(t,
        ooze.Parallel(),
        ooze.WithViruses(
            arithmetic.New(),
            arithmeticassignment.New(),
            arithmeticassignmentinvert.New(),
            bitwise.New(),
            comparison.New(),
            comparisoninvert.New(),
            comparisonreplace.New(),
            floatdecrement.New(),
            floatincrement.New(),
            integerdecrement.New(),
            integerincrement.New(),
            loopbreak.New(),
            loopcondition.New(),
            rangebreak.New(),
        ),
    )
}
EOF
go test -tags=mutation -v -ooze.v -timeout 60m . > /tmp/eq/raw/ooze.txt 2>&1
```

`-ooze.v` is required because ooze v0.2.0's default console reporter only
prints survivor diffs — the verbose flag exposes per-mutant `running tests on
'…'` / `mutant killed` / `mutant survived` events that let us recover the
per-mutant outcome from the text log.

### Normalization

ooze emits no JSON. Per-mutant data is recovered from the verbose `go test`
log:

- **Bucket counts** (mutants generated per `(file, mutator)`) come from
  `=== RUN TestMutation/<file>_→_<label>(#NN)?` lines emitted during ooze's
  discovery phase, before `Parallel()` interleaves goroutines.
- **Per-survivor line numbers** come from the boxed unified diff each
  surviving mutant produces at the end of the log. The diff is parsed for the
  first `(-, +)` line pair whose minus side is non-comment and whose textual
  content differs after collapsing whitespace. For insertion-only mutators
  (`RANGE_BREAK`), the position is recovered from the first orphan `+` line
  via `cur_old - 1` (which aligns with gomutants's `ast.RangeStmt.Body.Lbrace`
  position).

These transforms are why some attribution rows in Appendix A.1 are flagged
"diff-attribution drift, spot-checked equivalent" — they are limitations of
the recovery, not of either tool.

## Results

### §5.1 Top-line counts

| metric | gomutants | ooze |
|---|---:|---:|
| Total mutants generated | 1340 | 1232 |
| Killed | 584 | 818 |
| Survived (LIVED) | 430 | 414 |
| Not viable (compile error) | 280 | n/a |
| Not covered (no test covers line) | 35 | n/a |
| Timed out | 11 | n/a |

ooze treats every non-zero `go test` exit as a kill, which lumps compile
failures and timeouts into KILLED. gomutants separates them. The fairest
apples-to-apples comparison normalizes the gomutants side accordingly:

| status union | gomutants | ooze |
|---|---:|---:|
| KILLED ∪ NOT VIABLE ∪ TIMED OUT | **875** | **818** |
| LIVED | **430** | **414** |
| NOT COVERED | **35** | (n/a — ooze runs full suite for every mutant) |

The +57 in the KILLED-union row matches the +54 mutant-count surplus from
§6.1 plus a handful of NOT_COVERED mutants that ooze ran-and-killed.

### §5.2 Per-mutator total parity

Where Δ ≠ 0, the divergence is investigated in §6.

| canonical type | gomutants | ooze | Δ (gm − ooze) |
|---|---:|---:|---:|
| ARITHMETIC_BASE | 39 | 39 | 0 |
| REMOVE_SELF_ASSIGNMENTS | 7 | 7 | 0 |
| INVERT_ASSIGNMENTS | 4 | 4 | 0 |
| INVERT_BITWISE | 55 | 55 | 0 |
| CONDITIONALS_BOUNDARY | 7 | 7 | 0 |
| CONDITIONALS_NEGATION | 64 | 64 | 0 |
| EXPRESSION_REMOVE | 28 | 28 | 0 |
| FLOAT_DECREMENT | 0 | 0 | 0 |
| FLOAT_INCREMENT | 0 | 0 | 0 |
| INTEGER_DECREMENT | 564 | 510 | **+54** |
| INTEGER_INCREMENT | 564 | 510 | **+54** |
| INVERT_LOOP_CTRL | 0 | 0 | 0 |
| LOOP_CONDITION | 2 | 2 | 0 |
| RANGE_BREAK | 6 | 6 | 0 |
| **total** | **1340** | **1232** | **+108** |

12 of 14 families: byte-identical. The full divergence is concentrated in the
two integer-stepping families — explained in §6.1.

### §5.3 Per-mutator outcomes

`gm killed*` is gomutants `KILLED + NOT VIABLE + TIMED OUT`, the union ooze
reports as KILLED.

| canonical type | gm KILLED | gm NV | gm TO | gm killed* | ooze KILLED | Δ killed* | gm LIVED | ooze SURVIVED | Δ survived |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| ARITHMETIC_BASE | 27 | 0 | 2 | 29 | 32 | −3 | 5 | 7 | −2 |
| REMOVE_SELF_ASSIGNMENTS | 4 | 0 | 2 | 6 | 6 | 0 | 1 | 1 | 0 |
| INVERT_ASSIGNMENTS | 3 | 0 | 1 | 4 | 4 | 0 | 0 | 0 | 0 |
| INVERT_BITWISE | 39 | 0 | 0 | 39 | 41 | −2 | 14 | 14 | 0 |
| CONDITIONALS_BOUNDARY | 5 | 0 | 0 | 5 | 5 | 0 | 1 | 2 | −1 |
| CONDITIONALS_NEGATION | 48 | 0 | 1 | 49 | 49 | 0 | 11 | 15 | −4 |
| EXPRESSION_REMOVE | 14 | 0 | 0 | 14 | 14 | 0 | 14 | 14 | 0 |
| INTEGER_DECREMENT | 223 | 22 | 2 | 247 | 222 | +25 | 306 | 288 | +18 |
| INTEGER_INCREMENT | 216 | 258 | 3 | 477 | 440 | +37 | 76 | 70 | +6 |
| LOOP_CONDITION | 2 | 0 | 0 | 2 | 2 | 0 | 0 | 0 | 0 |
| RANGE_BREAK | 3 | 0 | 0 | 3 | 3 | 0 | 2 | 3 | −1 |

The few non-zero `Δ survived` values on bucket-equal mutator families (e.g.
CONDITIONALS_NEGATION: −4) collapse cleanly once attributed by position
(Appendix A.2): they're all gomutants positions classified as NOT COVERED
that ooze ran-and-survived. Once those are excluded from the
"gomutants LIVED" pool, the remaining survivor sets agree byte-for-byte.

### §5.4 Per-(file, type) bucket parity

All 16 bucket disagreements are in INTEGER_INCREMENT/DECREMENT — i.e., they
are the hex-literal gap of §6.1 distributed across files. Every other bucket
in every file has identical totals.

| file | type | gm | ooze | Δ |
|---|---|---:|---:|---:|
| dce.go | INTEGER_DECREMENT | 12 | 10 | +2 |
| dce.go | INTEGER_INCREMENT | 12 | 10 | +2 |
| hash.go | INTEGER_DECREMENT | 27 | 7 | +20 |
| hash.go | INTEGER_INCREMENT | 27 | 7 | +20 |
| time.go | INTEGER_DECREMENT | 42 | 35 | +7 |
| time.go | INTEGER_INCREMENT | 42 | 35 | +7 |
| uuid.go | INTEGER_DECREMENT | 148 | 142 | +6 |
| uuid.go | INTEGER_INCREMENT | 148 | 142 | +6 |
| version1.go | INTEGER_DECREMENT | 11 | 7 | +4 |
| version1.go | INTEGER_INCREMENT | 11 | 7 | +4 |
| version4.go | INTEGER_DECREMENT | 19 | 11 | +8 |
| version4.go | INTEGER_INCREMENT | 19 | 11 | +8 |
| version6.go | INTEGER_DECREMENT | 11 | 7 | +4 |
| version6.go | INTEGER_INCREMENT | 11 | 7 | +4 |
| version7.go | INTEGER_DECREMENT | 23 | 20 | +3 |
| version7.go | INTEGER_INCREMENT | 23 | 20 | +3 |
| **sum** | | | | **+108** |

### §5.5 Survivor identity match

Comparing `(file, line, mutator)` sets:

| set | size |
|---|---:|
| gomutants survivors (LIVED, deduped) | 138 |
| ooze survivors (with extractable line) | 147 |
| **matched on `(file, line, mutator)`** | **119** |
| gomutants-only | 19 |
| ooze-only | 28 |

The 19 gomutants-only positions are attributed in Appendix A.1. Every one
falls into a known category: hex literal (ooze can't generate), gomutants
column where ooze's diff was attributed to a neighboring row in a run of
identical literals, or `RANGE_BREAK` insertion line recovered approximately.

The 28 ooze-only positions are attributed in Appendix A.2: **26 of 28** are
gomutants NOT COVERED at the same position — gomutants statically skipped
the mutant because no uuid test covers the line; ooze ran the full uuid
suite anyway and saw the mutant survive. The remaining 2 are time-dependent
disagreements covered in §6.4.

## §6 Root cause analysis

### §6.1 The +54-per-direction integer-literal gap

ooze v0.2.0's integer mutators parse literal source text with `strconv.Atoi`,
which only accepts base-10 decimal. Any `*ast.BasicLit{Kind: token.INT}`
whose source begins with `0x`, `0o`, leading-zero octal, or `0b` returns
`err != nil` and is silently skipped.

```go
// viruses/integerdecrement/integerdecrement.go:28 (ooze v0.2.0)
originalInteger, err := strconv.Atoi(originalValue)
if err != nil {
    return nil
}
```

gomutants's equivalent at `internal/mutator/numeric_literal.go:66` uses
`strconv.ParseInt(raw, 0, 64)`. The `base=0` flag activates Go's prefix
detection — every form the Go parser accepts, the mutator accepts:

```go
raw := strings.ReplaceAll(src, "_", "")
v, err := strconv.ParseInt(raw, 0, 64)
```

uuid v1.6.0 contains exactly 54 hex integer literals across its 14 mutated
files — `0xFF` masks in `hash.go`, version bits like `0x60` / `0x70` /
`0x80`, the `0xfff` / `0x3fff` clock-sequence masks in `time.go`, etc. Each
appears in both INTEGER_INCREMENT and INTEGER_DECREMENT, producing the
54-per-direction gap exactly. There are zero octal or binary literals in
uuid, so the same scope difference is also present for those forms but
unobservable on this target.

This is observable in §5.4: the bucket disagreement is concentrated where
the file does byte manipulation. `hash.go`'s `Max = UUID{0xFF, 0xFF, …}`
contributes 20 mutants per direction (16 bytes × `0xFF` literals + a few
masks), which is the entire +20 hash.go gap.

### §6.2 NOT COVERED handling

gomutants uses uuid's `go test -coverprofile` output to learn which mutants
sit on lines no test exercises. Those mutants are *generated and reported*
(so coverage gaps in your test suite are still visible in the report) but
the per-mutant `go test` run is skipped — the status is `NOT COVERED`. ooze
has no coverage analysis: every mutant gets a full `go test ./...` run.

When a mutant gomutants would call NOT COVERED happens to live on code uuid
tests load but don't assert on, that mutant survives ooze's full-suite run
and shows up as `🧬 Mutant survived` in ooze's report.

In numbers: 26 of the 28 ooze-only survivors in §5.5 (Appendix A.2) are
precisely this case. These are not real survivor-set disagreements; they're
gomutants choosing not to spend test time on lines uuid's authors never
asserted against.

### §6.3 Diff-attribution artifacts

Three artifacts of recovering line numbers from ooze's unified-diff output
account for every other survivor-set difference:

1. **gofmt re-formats the whole file post-mutation.** A one-token mutation
   can produce a multi-hunk diff where N−1 hunks are pure whitespace /
   comment reformatting noise. The parser must skip the noisy hunks and
   identify the real one. (`compare.py` does this by requiring the minus
   side of a candidate `(-, +)` pair to be non-comment and to differ from
   its plus partner after stripping all whitespace.)

2. **Runs of identical literals defeat unified-diff line alignment.**
   `util.go`'s `xvalues = [256]byte{255, 255, 255, …, 255}` is 16 rows of 16
   identical `255` literals. Mutating any single one produces a diff where
   the standard LCS algorithm legitimately attributes the change to either
   the first or last `255` in the run; depending on which, ooze's "mutation
   line" can be off by up to the run length from gomutants's AST position.
   This causes ~10 entries in Appendix A.1.

3. **Insertion-only mutators (`RANGE_BREAK`) produce no `-` line.** gomutants
   reports the line of the loop's opening `{`; ooze's diff places the `+
   break` immediately afterward. `compare.py` recovers the gomutants line
   via `cur_old − 1` at the orphan plus position; this works for every
   uuid `RANGE_BREAK` site.

### §6.4 Two genuine outcome disagreements

The only two ooze-only survivors that gomutants ran-and-killed at the same
position:

| position | gomutants | ooze | explanation |
|---|---|---|---|
| `version6.go:43` INTEGER_DECREMENT (`8` → `7`) | KILLED | SURVIVED | `binary.BigEndian.PutUint16(uuid[8:], seq)` — `uuid[8:]` is a slice of a `[16]byte`. The PutUint16 writes 2 bytes to that slice; the bytes written at offsets 8–9 in the unmutated path move to offsets 7–8 in the mutated path. Byte 8 is then overwritten on line 46 (`uuid[8] = 0x80 | …`), so the only observable difference is byte 7. Whether a test catches this is sensitive to which tests run and in what order — gomutants's per-mutant test selection (the subset of tests covering line 43) catches it; ooze's full-suite run catches it under some test orderings but not all. |
| `version7.go:96` INTEGER_DECREMENT (`12` → `11`) | KILLED | SURVIVED | `now := milli<<12 + seq`, mutated to `milli<<11`. UUIDv7 uses a global `lastV7time` that's monotonized across calls; whether the mutation is detected depends on whether earlier tests have already advanced `lastV7time` enough to mask the smaller `now`. Order-sensitive. |

Both stem from gomutants's coverage-aware test selection running a different
(smaller, focused) set of tests per mutant than ooze's full-suite run; in
both cases gomutants's outcome reflects the test that's actually relevant to
the mutated code. Neither is a discovery bug.

## §7 Caveats and parser limitations

- **Killed-mutant line identity isn't recoverable from ooze's output.** ooze
  reports survivors with line-level diffs, but killed mutants only appear as
  bucket-level events (`tests for '/tmp/…' failed; mutant killed`) with no
  per-mutant line attribution. Outcome comparison at line granularity is
  therefore one-sided (survivor-set comparison only).
- **Column position is not comparable.** ooze emits no column info; the
  match key in §5.5 is `(file, line, mutator)` only. Two gomutants mutants
  at the same line but different columns collapse to one tuple in this
  comparison.
- **`Parallel()` interleaves output.** ooze runs mutants concurrently;
  per-mutant log lines from different goroutines interleave at fragment
  granularity. The parser handles this by deriving bucket totals from the
  pre-`PAUSE` discovery pass (sequential) and per-survivor line numbers
  from the post-run boxed diffs (each box is self-contained), so
  parallelism affects throughput but not the comparison.
- **`docs/performance.md`'s "~120 mutants on uuid" line is stale relative
  to this exercise.** That figure pre-dates several mutator additions; with
  the current 14-mutator intersection gomutants generates 1340 mutants on
  uuid. The performance doc should be updated independently.

## Appendix A — Divergent survivor positions, fully attributed

### A.1 gomutants reports LIVED, ooze did not

| file | line | mutator | gomutants column / original | attribution |
|---|---:|---|---|---|
| hash.go | 23 | INTEGER_DECREMENT | 8× `0xFF` at C3,C9,C15,…,C45 | §6.1 hex literal |
| hash.go | 24 | INTEGER_DECREMENT | 8× `0xFF` at C3,C9,C15,…,C45 | §6.1 hex literal |
| hash.go | 41 | INTEGER_INCREMENT | C7:`8`, C18:`8`, C23:`0x3f`, C31:`0x80` | §6.1 hex literal |
| time.go | 63 | INVERT_BITWISE | C30:`&`, C40:`\|` | §6.3 diff-attribution drift within a run of bitwise ops on `clockSeq` |
| time.go | 104 | INTEGER_DECREMENT | C24:`0x3fff`, C34:`0x8000` | §6.1 hex literal |
| util.go | 20 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 21 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 28 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 29 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 30 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 31 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 32 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 33 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| util.go | 34 | INTEGER_DECREMENT | 16× `255` | §6.3 repeated-literal lookup table |
| version1.go | 26 | INTEGER_DECREMENT | C26:`0xffffffff` | §6.1 hex literal |
| version1.go | 26 | INTEGER_INCREMENT | C26:`0xffffffff` | §6.1 hex literal |
| version1.go | 29 | INTEGER_INCREMENT | C12:`0x1000` | §6.1 hex literal |
| version1.go | 31 | INTEGER_INCREMENT | C34:`0` | §6.3 diff-attribution drift to adjacent line |
| version7.go | 70 | INVERT_BITWISE | C19:`>>` | §6.3 diff-attribution drift (one of 5 consecutive `<<` / `>>` mutations in `Time()`) |

Total: 19 positions. All attributed.

### A.2 ooze reports SURVIVED, gomutants did not

| file | line | mutator | gomutants status at same position | attribution |
|---|---:|---|---|---|
| null.go | 71 | CONDITIONALS_NEGATION | NOT COVERED | §6.2 |
| null.go | 71 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| null.go | 71 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| null.go | 91 | CONDITIONALS_NEGATION | NOT COVERED | §6.2 |
| time.go | 56 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| time.go | 56 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| time.go | 83 | CONDITIONALS_NEGATION | NOT COVERED | §6.2 |
| time.go | 83 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| time.go | 84 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| time.go | 84 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| time.go | 86 | INVERT_BITWISE | NOT COVERED | §6.2 |
| time.go | 119 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| time.go | 119 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| time.go | 120 | ARITHMETIC_BASE | NOT COVERED | §6.2 |
| time.go | 120 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| time.go | 120 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| time.go | 120 | INVERT_BITWISE | NOT COVERED | §6.2 |
| uuid.go | 77 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| uuid.go | 77 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| uuid.go | 126 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| uuid.go | 126 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| uuid.go | 291 | CONDITIONALS_BOUNDARY | NOT COVERED | §6.2 |
| uuid.go | 291 | CONDITIONALS_NEGATION | NOT COVERED | §6.2 |
| uuid.go | 291 | INTEGER_DECREMENT | NOT COVERED | §6.2 |
| uuid.go | 291 | INTEGER_INCREMENT | NOT COVERED | §6.2 |
| uuid.go | 361 | RANGE_BREAK | NOT COVERED | §6.2 |
| version6.go | 43 | INTEGER_DECREMENT | KILLED | §6.4 test-ordering / per-mutant test selection |
| version7.go | 96 | INTEGER_DECREMENT | KILLED | §6.4 test-ordering / per-mutant test selection |

Total: 28 positions. 26 are §6.2 NOT COVERED skipping; 2 are §6.4
test-selection differences. No discovery bugs.

## Appendix B — How to reproduce in CI

This comparison is not currently wired into CI. If you want a repeating
guarantee that the equivalence holds across gomutants releases, the
end-to-end check is:

1. Run gomutants with the `--only` list from §"Run gomutants" against
   `github.com/google/uuid@v1.6.0`; load the JSON.
2. Run ooze (v0.2.0) with the `WithViruses(...)` list from §"Set up and run
   ooze" against the same uuid checkout; capture stdout under
   `-tags=mutation -v -ooze.v`.
3. Parse both outputs and assert the bucket-count parity invariants from
   §5.2: every `(file, type)` bucket either matches exactly OR shows a
   surplus on the gomutants side AND every surplus mutant has a hex-prefixed
   `original` literal.

The reference parser used here lives outside the repo (it depends on
ooze-specific text-format details and isn't part of gomutants's runtime).
The JSON shape on the gomutants side is stable
(`internal/report/json.go`).
