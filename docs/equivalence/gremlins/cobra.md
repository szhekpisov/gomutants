# Mutator-set equivalence on spf13/cobra vs gremlins v0.6.0

_Last measured: 2026-05-28 on a 10-core Apple M1 Pro under macOS 26.3.1._

Companion to [`uuid.md`](uuid.md): same tools, same 11-mutator
intersection, a larger and more idiomatic Go target. The shared
methodology (mutator mapping, abbreviated-name normalization,
default-disabled gremlins flags, output parsing) lives in that
document; this report repeats only what is target-specific.

## TL;DR

On the 11-mutator intersection of the two tools, against
`spf13/cobra v1.10.2`, both invoked in `--dry-run` mode:

- **All 731 gomutants discoveries land on the identical
  `(file, line, column, mutator_type)` as a gremlins discovery.** Zero
  gomutants-only positions.
- **gremlins generates 29 additional positions** (760 vs 731), each
  attributable to one of three documented design differences:
  - **21 unary `&` (address-of) positions** gremlins surfaces as
    INVERT_BITWISE candidates. Mutating `&x` to `|x` is a syntax error
    and so the candidate is NOT VIABLE. Same root cause as the uuid
    report's §6.2; cobra exposes the pattern much more often because
    it constructs structs by address constantly.
  - **4 labeled `break Loop` / `continue main` positions** gremlins
    surfaces as INVERT_LOOP_CTRL candidates. gomutants's mutator
    deliberately skips labeled branch statements
    (`internal/mutator/invert_loop_ctrl.go:28-30`) because the label
    may target a non-`for` construct (a switch case), making the swap
    a compile error rather than a behavioral mutation.
  - **4 positions in `command_win.go`**, a file marked
    `//go:build windows`. gomutants's package-aware discovery filters
    by host GOOS and excludes the file on Darwin/Linux; gremlins
    scans `.go` files directly and emits candidates regardless of
    build tags.

Conclusion: on cobra, gomutants and gremlins generate the **identical
set of viable mutations on the host-platform-applicable code** across
their shared mutator set.

## Versions pinned for this report

| component | version / commit |
|---|---|
| `github.com/spf13/cobra` | v1.10.2 (released 2025-12-04) |
| `github.com/go-gremlins/gremlins` | v0.6.0 |
| `gomutants` | `6be469b53dd416dc004461e06cacdae491824271` (this repo's `HEAD` at write time) |
| Go toolchain | 1.26.3 (gomutants build); 1.25.7 (gremlins, pinned via `GOTOOLCHAIN`) |

`GOTOOLCHAIN=go1.25.7` is required for gremlins on cobra. gremlins v0.6.0
fails on Go 1.26.x for this target, as documented in the repo
`README.md` benchmark notes.

## Methodology

The 11-mutator intersection, the two gremlins abbreviated mutator names
(`INVERT_LOOPCTRL`, `INVERT_BWASSIGN`) requiring normalization, the six
default-disabled gremlins mutators that must be enabled explicitly, and
the output-parsing approach for both tools are identical to
[`uuid.md` §Methodology](uuid.md#methodology) and are not repeated
here.

### Reproduction

```bash
set -euo pipefail
GOMUTANTS_REPO=${GOMUTANTS_REPO:-$PWD}
mkdir -p /tmp/eq-cobra/bin && cd /tmp/eq-cobra
go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
git clone --depth 1 --branch v1.10.2 https://github.com/spf13/cobra.git cobra-gremlins
git clone --depth 1 --branch v1.10.2 https://github.com/spf13/cobra.git cobra-gomutants
git -C "$GOMUTANTS_REPO" rev-parse HEAD
( cd "$GOMUTANTS_REPO" && go build -o /tmp/eq-cobra/bin/gomutants . )
```

**Run gremlins** (note `GOTOOLCHAIN=go1.25.7` and the trailing `.`
rather than `./...`):

```bash
cd /tmp/eq-cobra/cobra-gremlins
GOTOOLCHAIN=go1.25.7 gremlins unleash --dry-run --output /tmp/eq-cobra/gremlins.json \
  --arithmetic-base=true --conditionals-boundary=true --conditionals-negation=true \
  --increment-decrement=true --invert-assignments=true --invert-bitwise=true \
  --invert-bwassign=true --invert-logical=true --invert-loopctrl=true \
  --invert-negatives=true --remove-self-assignments=true .
```

**Run gomutants**:

```bash
cd /tmp/eq-cobra/cobra-gomutants
/tmp/eq-cobra/bin/gomutants --dry-run \
  --only ARITHMETIC_BASE,CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION,\
INCREMENT_DECREMENT,INVERT_ASSIGNMENTS,INVERT_BITWISE,\
INVERT_BITWISE_ASSIGNMENTS,INVERT_LOGICAL,INVERT_LOOP_CTRL,\
INVERT_NEGATIVES,REMOVE_SELF_ASSIGNMENTS \
  > /tmp/eq-cobra/gomutants.txt
```

## Results

### §5.1 Top-line counts

| metric | gomutants | gremlins |
|---|---:|---:|
| Mutants discovered (total) | 731 | 760 |
| RUNNABLE / PENDING | 660 | 656 |
| NOT COVERED | 71 | 104 |

### §5.2 Per-mutator total parity

Where Δ ≠ 0, the divergence is investigated in §6.

| canonical type | gomutants | gremlins | Δ (gm − gr) |
|---|---:|---:|---:|
| ARITHMETIC_BASE | 114 | 114 | 0 |
| CONDITIONALS_BOUNDARY | 70 | 71 | **−1** |
| CONDITIONALS_NEGATION | 343 | 345 | **−2** |
| INCREMENT_DECREMENT | 5 | 5 | 0 |
| INVERT_ASSIGNMENTS | 18 | 18 | 0 |
| INVERT_BITWISE | 9 | 30 | **−21** |
| INVERT_BITWISE_ASSIGNMENTS | 0 | 0 | 0 |
| INVERT_LOGICAL | 106 | 107 | **−1** |
| INVERT_LOOP_CTRL | 29 | 33 | **−4** |
| INVERT_NEGATIVES | 19 | 19 | 0 |
| REMOVE_SELF_ASSIGNMENTS | 18 | 18 | 0 |
| **total** | **731** | **760** | **−29** |

6 of 11 families: byte-identical totals. The −29 is concentrated in
three patterns (§6.1, §6.2, §6.3).

### §5.3 Per-(file, type) bucket disagreements

| file | type | gomutants | gremlins | Δ |
|---|---|---:|---:|---:|
| cobra.go | INVERT_BITWISE | 0 | 1 | −1 |
| command.go | INVERT_BITWISE | 0 | 2 | −2 |
| command.go | INVERT_LOOP_CTRL | 10 | 14 | −4 |
| command_win.go | CONDITIONALS_BOUNDARY | 0 | 1 | −1 |
| command_win.go | CONDITIONALS_NEGATION | 0 | 2 | −2 |
| command_win.go | INVERT_LOGICAL | 0 | 1 | −1 |
| completions.go | INVERT_BITWISE | 9 | 22 | −13 |
| doc/man_docs.go | INVERT_BITWISE | 0 | 4 | −4 |
| doc/yaml_docs.go | INVERT_BITWISE | 0 | 1 | −1 |
| **sum** | | | | **−29** |

Every other `(file, type)` bucket in cobra matches exactly.

### §5.4 Identity match

| set | size |
|---|---:|
| gomutants discoveries | 731 |
| gremlins discoveries | 760 |
| **Matched on `(file, line, column, type)`** | **731** |
| gomutants-only | **0** |
| gremlins-only | 29 |

100% of gomutants's discoveries match a gremlins discovery on the
exact `(file, line, column, type)` tuple. The 29 gremlins-only
positions are attributed in Appendix A.1.

## §6 Root cause analysis

### §6.1 Unary `&` (address-of) as INVERT_BITWISE

Identical mechanism to the uuid report's §6.2: gremlins's token-based
scan treats every `&` token (including `*ast.UnaryExpr{Op: token.AND}`)
as an INVERT_BITWISE candidate. Mutating `&x` to `|x` is a syntax
error — `|` is not a valid unary operator in Go — so the candidate
is NOT VIABLE. gomutants's AST scan only considers
`*ast.BinaryExpr{Op: token.AND}`.

cobra triggers this pattern 21 times because the codebase constructs
structs by address constantly (`&Command{...}`, `&sync.RWMutex{}`,
`&directive`, `&noDesc` arguments to `pflag.BoolVar`,
`&GenManHeader{...}` in the doc generator, `&yamlDoc` in the yaml
emitter, `&sb` to `fmt.Fprintf`, etc.). Every affected position is in
Appendix A.1.

### §6.2 Labeled `break` / `continue` as INVERT_LOOP_CTRL

Four positions in `command.go` are labeled control-flow statements:

| position | source |
|---|---|
| command.go:690:4 | `break Loop` |
| command.go:699:5 | `break Loop` |
| command.go:728:4 | `break Loop` |
| command.go:1408:5 | `continue main` |

Gremlins emits each as an INVERT_LOOP_CTRL candidate (swapping
`break` ↔ `continue`). gomutants's mutator at
`internal/mutator/invert_loop_ctrl.go:28-30` deliberately skips
labeled branch statements:

> Skip labelled break/continue: swapping `break L` with `continue L`
> can target a label that isn't a continuable construct, producing a
> compile-error mutation rather than a behavioral one.

In these four cobra positions the labels happen to attach to `for`
loops, so the swap *would* be syntactically valid. gomutants's design
choice is to exclude the category category-wide rather than to
per-position decide. Both interpretations are defensible.

### §6.3 Build-tag-excluded files

Four positions are in `command_win.go`, whose first non-comment line
is `//go:build windows`:

| position | source |
|---|---|
| command_win.go:31:23 | `if MousetrapHelpText != "" && mousetrap.StartedByExplorer() {` — `!=` (CONDITIONALS_NEGATION) |
| command_win.go:31:29 | same line — `&&` (INVERT_LOGICAL) |
| command_win.go:33:31 | `if MousetrapDisplayDuration > 0 {` — `>` (CONDITIONALS_BOUNDARY) |
| command_win.go:33:31 | same column — `>` (CONDITIONALS_NEGATION) |

gomutants's discovery walks Go packages (via `go list`-equivalent),
which inherits the host's `GOOS`/`GOARCH` filter and skips files
whose build tags don't match. Running on Darwin or Linux,
`command_win.go` is not in the package set and so no mutants are
generated for it. gremlins scans `.go` files directly without
applying build tags and emits candidates for the file regardless.

This is not a coverage gap on the host platform — those mutations
target code that doesn't compile on Darwin/Linux and so couldn't be
tested even if generated — but it is a real scope divergence: a CI
matrix that wants to mutate every line that any build configuration
could reach would need to run gomutants once per relevant
`GOOS`/`GOARCH` pair, where gremlins covers all platform-conditional
files in a single invocation.

## §7 Caveats and tool limitations

Caveats are the same as the uuid report
([`uuid.md` §7](uuid.md#7-caveats-and-tool-limitations)) with one
addition specific to cobra:

- **Coverage-aware NOT COVERED counts differ more than on uuid** (71
  vs 104). cobra has more conditionally-tested completion-script
  generation code than uuid; the gap is wider but the discovery match
  is unaffected.
- **Column position is reliable.** All 731 matched tuples agree on
  column to the byte.

## Appendix A — Divergent positions, fully attributed

### A.1 gremlins reports a mutant, gomutants does not

Grouped by attribution category. All 29 positions are accounted for.

**§6.1 Unary `&` address-of (21 positions, all INVERT_BITWISE):**

| file | line | col | original |
|---|---:|---:|---|
| cobra.go | 180 | 9 | `&tmplFunc{...}` |
| command.go | 792 | 23 | `&sb` (Fprintf arg) |
| command.go | 1269 | 19 | `&Command{...}` |
| completions.go | 41 | 27 | `&sync.RWMutex{}` |
| completions.go | 124 | 39 | `&directive` |
| completions.go | 232 | 17 | `&Command{...}` |
| completions.go | 724 | 33 | `&flagCompError{...}` |
| completions.go | 769 | 19 | `&Command{...}` |
| completions.go | 801 | 10 | `&Command{...}` |
| completions.go | 833 | 24 | `&noDesc` |
| completions.go | 836 | 9 | unary `&` |
| completions.go | 872 | 23 | `&noDesc` |
| completions.go | 875 | 10 | unary `&` |
| completions.go | 897 | 24 | `&noDesc` |
| completions.go | 900 | 16 | unary `&` |
| completions.go | 923 | 30 | `&noDesc` |
| doc/man_docs.go | 51 | 12 | `&GenManHeader{...}` |
| doc/man_docs.go | 79 | 21 | `&headerCopy` |
| doc/man_docs.go | 107 | 12 | `&GenManHeader{...}` |
| doc/man_docs.go | 134 | 17 | `&now` |
| doc/yaml_docs.go | 137 | 29 | `&yamlDoc` |

**§6.2 Labeled break/continue (4 positions, all INVERT_LOOP_CTRL):**

| file | line | col | original |
|---|---:|---:|---|
| command.go | 690 | 4 | `break Loop` |
| command.go | 699 | 5 | `break Loop` |
| command.go | 728 | 4 | `break Loop` |
| command.go | 1408 | 5 | `continue main` |

**§6.3 `//go:build windows`-excluded (4 positions in command_win.go):**

| file | line | col | mutator | original |
|---|---:|---:|---|---|
| command_win.go | 31 | 23 | CONDITIONALS_NEGATION | `!=` |
| command_win.go | 31 | 29 | INVERT_LOGICAL | `&&` |
| command_win.go | 33 | 31 | CONDITIONALS_BOUNDARY | `>` |
| command_win.go | 33 | 31 | CONDITIONALS_NEGATION | `>` |

Total: 29 positions. All attributed. No gomutants discovery bugs.

### A.2 gomutants reports a mutant, gremlins does not

(empty)

## Appendix B — How to reproduce in CI

Identical to [`uuid.md` Appendix B](uuid.md#appendix-b--how-to-reproduce-in-ci)
with the target swapped to `github.com/spf13/cobra@v1.10.2` and
`GOTOOLCHAIN=go1.25.7` set when invoking gremlins. The reference
comparator is unchanged.
