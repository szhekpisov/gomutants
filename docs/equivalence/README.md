# Mutator-set equivalence reports

This directory holds one-time evidentiary comparisons between gomutants and
other Go mutation testers. Each report answers, for one external tool and
one target codebase: on the mutators the two tools have in common, do they
generate the same mutants — and where they don't, is every difference
attributable to a documented root cause that doesn't reflect a gomutants
discovery bug?

A report's conclusion is one of:

- **Strict superset.** gomutants generates every mutant the other tool does,
  plus additional mutants from documented scope differences.
- **Equivalent.** Identical mutant sets on the shared mutator families.
- **Gap.** A mutant the other tool generates is genuinely missing from
  gomutants. (No report has reached this conclusion to date.)

## Reports

| external tool | target | result | report |
|---|---|---|---|
| ooze v0.2.0 | google/uuid v1.6.0 | strict superset | [ooze/uuid.md](ooze/uuid.md) |

## Report structure

Each report follows the same skeleton so they stay directly comparable:

1. **TL;DR** — top-line outcome in 3–5 bullets.
2. **Versions pinned** — exact commits / module versions for the target,
   the external tool, gomutants, and the Go toolchain.
3. **Methodology** — mutator mapping (which gomutants `MutationType`
   corresponds to which external mutator), reproduction recipe, and any
   output normalization needed.
4. **Results** — top-line counts, per-mutator total parity,
   per-`(file, mutator)` bucket parity, and survivor-identity match on
   `(file, line, mutator)`.
5. **Root cause analysis** — every divergence, attributed.
6. **Caveats** — limitations of the comparison (e.g. information the
   external tool doesn't expose).
7. **Appendices** — exhaustive position-level attribution of every
   divergent survivor.

## Layout

Reports are filed by `<tool>/<target>.md`. Tool-first because each
comparison's intersection mutator set, reproduction recipe, and
output-parsing quirks are tool-specific.
