package discover

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

// LineRange is a closed interval [Start, End] of 1-indexed line numbers.
type LineRange struct {
	Start int
	End   int
}

// Contains reports whether line is in [Start, End] inclusive.
func (r LineRange) Contains(line int) bool {
	return line >= r.Start && line <= r.End
}

// GitRoot returns the absolute path to the git working-tree root for dir.
func GitRoot(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w\n%s", err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// RunGitDiff executes `git diff --unified=0 <ref>` in dir and returns the
// changed line ranges per file (paths relative to the git root). Lines
// only deleted at a position (count=0) produce no range — there is nothing
// to mutate. Renames use the new (b/) path.
func RunGitDiff(ctx context.Context, dir, ref string) (map[string][]LineRange, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--unified=0", "--no-color", "--no-ext-diff", ref)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff %s: %w\n%s", ref, err, stderr.String())
	}
	return ParseUnifiedDiff(&stdout)
}

// ParseUnifiedDiff reads a unified diff (as produced by `git diff
// --unified=0`) and returns the changed line ranges per file. Paths are
// taken from the `+++ b/<path>` line; deleted files (`+++ /dev/null`)
// contribute no ranges. Hunk headers carry `+newstart[,newcount]`; we emit
// [newstart, newstart+newcount-1] for each newcount > 0.
func ParseUnifiedDiff(r io.Reader) (map[string][]LineRange, error) {
	ranges := make(map[string][]LineRange)
	var current string

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ ")
			// Strip trailing tab-prefixed metadata (timestamps).
			if i := strings.IndexByte(path, '\t'); i >= 0 {
				path = path[:i]
			}
			if path == "/dev/null" {
				current = ""
				continue
			}
			current = stripDiffPrefix(path)
		case strings.HasPrefix(line, "@@"):
			if current == "" {
				continue
			}
			rng, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			ranges[current] = append(ranges[current], rng)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading diff: %w", err)
	}
	return ranges, nil
}

// stripDiffPrefix removes the standard "a/" or "b/" prefix that git adds
// to diff paths. If diff.noprefix is set the path is already plain.
func stripDiffPrefix(p string) string {
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseHunkHeader extracts the +start[,count] range from a unified-diff
// hunk header like "@@ -1,2 +3,4 @@ ctx". Returns ok=false if the header
// is malformed or the new-side count is zero (deletion-only hunk).
func parseHunkHeader(line string) (LineRange, bool) {
	_, after, found := strings.Cut(line, "+")
	if !found {
		return LineRange{}, false
	}
	tok, _, found := strings.Cut(after, " ")
	if !found {
		return LineRange{}, false
	}
	startStr, countStr, hasCount := strings.Cut(tok, ",")
	start, err := strconv.Atoi(startStr)
	if err != nil || start <= 0 {
		return LineRange{}, false
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countStr)
		if err != nil {
			return LineRange{}, false
		}
	}
	if count <= 0 {
		return LineRange{}, false
	}
	return LineRange{Start: start, End: start + count - 1}, true
}

// FilterByDiff returns the subset of mutants whose (file, line) falls
// inside one of the changed ranges. Paths in ranges are relative to
// gitRoot; mutant File paths are absolute.
func FilterByDiff(mutants []mutator.Mutant, ranges map[string][]LineRange, gitRoot string) []mutator.Mutant {
	if len(ranges) == 0 {
		return nil
	}
	out := mutants[:0]
	for _, m := range mutants {
		rel, err := filepath.Rel(gitRoot, m.File)
		if err != nil {
			continue
		}
		// On Windows, filepath.Rel uses backslashes; git always uses forward slashes.
		rel = filepath.ToSlash(rel)
		hunks, ok := ranges[rel]
		if !ok {
			continue
		}
		for _, r := range hunks {
			if r.Contains(m.Line) {
				out = append(out, m)
				break
			}
		}
	}
	// Renumber IDs so downstream output stays compact.
	for i := range out {
		out[i].ID = i + 1
	}
	return out
}
