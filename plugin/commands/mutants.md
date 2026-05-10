---
description: Run gomutants on changed code and propose tests for surviving mutants
argument-hint: [packages... | --since <ref>]
allowed-tools: Bash(gomutants *), Bash(go run *), Bash(git *), Bash(jq *), Bash(cat *), Bash(which *), Read, Glob
---

You are running `gomutants` (Go mutation testing) and using the report to find test gaps in the user's project.

## Step 0 — locate gomutants

Check whether `gomutants` is on PATH (`which gomutants`).

- If found, use `gomutants` directly.
- If missing, fall back to `go run github.com/szhekpisov/gomutants@latest`. Tell the user once that you are using the fallback and that they can install the binary with:
  - `go install github.com/szhekpisov/gomutants@latest`, or
  - downloading a release from https://github.com/szhekpisov/gomutants/releases.

In the rest of these instructions, `<gomutants>` means whichever of the two you picked.

## Step 1 — pick a scope

Parse `$ARGUMENTS`:

- If it contains one or more package patterns (e.g. `./internal/foo`, `./...`), use them as positional args.
- If it contains `--since <ref>` (e.g. `--since main`, `--since HEAD~1`), pass `-changed-since <ref>` and default packages to `./...`.
- If empty, default to `-changed-since main ./...`. If the repo has no `main` branch, fall back to `./...` and tell me.

Run from the repo root (the directory containing `go.mod`). If the user invoked you from a subdirectory of a Go module, walk up to the module root first.

## Step 2 — run gomutants

```
<gomutants> -quiet -output /tmp/gomutants-report.json [scope from step 1]
```

Notes:
- `-quiet` suppresses progress output; the JSON file has everything needed.
- Do **not** pass `-dry-run` — real KILLED/LIVED status is required.
- Exit codes 10 / 11 mean the efficacy / coverage thresholds were not met. The report still wrote, so continue.
- If the run is taking visibly long on `./...`, narrow to the package with the most changed files and tell the user you did so.

## Step 3 — extract surviving mutants

Read `/tmp/gomutants-report.json`. Schema:

```
{
  "files": [
    {
      "file_name": "...",
      "mutations": [
        {
          "type": "...",
          "status": "LIVED|KILLED|NOT_COVERED|NOT_VIABLE|TIMED_OUT",
          "line": N,
          "column": N,
          "original": "...",
          "replacement": "..."
        }
      ]
    }
  ]
}
```

Filter to `status == "LIVED"`. Note the `NOT_COVERED` count per file separately as a secondary signal — those mutants no test even exercises.

## Step 4 — propose tests

For up to ~10 surviving mutants (prioritise files with the most survivors):

1. Read the source file around `line` to understand what the mutation changes. The `original` → `replacement` diff is the key (e.g. removing a `defer`, flipping `<` to `<=`, dropping a statement).
2. Use `Glob` to find the corresponding `*_test.go` and skim existing test names so suggestions don't collide.
3. Output one block per mutant:

   ```
   ### <file>:<line>  —  <type>   (status: LIVED)
   `<original>`  →  `<replacement>`

   **Why it survived:** <one sentence — what existing tests fail to assert>

   **Kill it:**
   ```go
   func TestXxx_<short_name>(t *testing.T) {
       // ...
   }
   ```
   ```

## Step 5 — wrap up

End with a one-line summary:
`N surviving mutants across M files; proposed K new tests.`

Do **not** edit any files — proposals only. If the user wants them applied, they will ask.
