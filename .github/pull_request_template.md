<!-- PR title must follow: feat: / bug: / fix: / doc: / chore: / test: description -->

## What

<!-- One-sentence summary. Link the issue: Fixes #000 -->

## Why

<!-- Motivation: why is this change needed? -->

## How

<!-- Approach, non-obvious decisions, trade-offs. -->

## Checklist

- [ ] PR title follows convention (`feat:`, `bug:`, `fix:`, `doc:`, `chore:`, `test:`)
- [ ] `go test -race ./...` passes locally
- [ ] `go vet ./...` and `golangci-lint run` clean
- [ ] New/changed behavior covered by tests
- [ ] Coverage threshold met (total ≥ 94%)
- [ ] Mutation efficacy threshold met (`gomutant ./internal/...` ≥ 90%)
- [ ] No new dependencies (or justified)

## Notes for reviewers

<!-- Optional: anything reviewers should pay attention to. -->
