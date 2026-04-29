package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutant/internal/mutator"
)

func TestParseUnifiedDiffSingleHunk(t *testing.T) {
	in := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -10,0 +11,2 @@ ctx
+added line
+another
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"foo.go": {{Start: 11, End: 12}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffSingleLineNoCount(t *testing.T) {
	// `+11` with no count means count=1.
	in := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -10 +11 @@
-old
+new
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"x.go": {{Start: 11, End: 11}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffMultipleHunks(t *testing.T) {
	in := `diff --git a/pkg/a.go b/pkg/a.go
--- a/pkg/a.go
+++ b/pkg/a.go
@@ -5,0 +6,1 @@
+x
@@ -20,1 +21,3 @@
-y
+a
+b
+c
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"pkg/a.go": {{Start: 6, End: 6}, {Start: 21, End: 23}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffDeletionOnlyHunkSkipped(t *testing.T) {
	// `+10,0` → deletion at position 10; nothing to mutate.
	in := `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -10,2 +10,0 @@
-gone
-also gone
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no ranges (deletion-only), got %v", got)
	}
}

func TestParseUnifiedDiffDeletedFileSkipped(t *testing.T) {
	in := `diff --git a/dead.go b/dead.go
deleted file mode 100644
--- a/dead.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package x
-
-func F() {}
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("deleted file should produce no ranges, got %v", got)
	}
}

func TestParseUnifiedDiffNewFile(t *testing.T) {
	in := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package x
+
+func F() {}
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"new.go": {{Start: 1, End: 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffRenameUsesNewPath(t *testing.T) {
	in := `diff --git a/old.go b/sub/new.go
similarity index 90%
rename from old.go
rename to sub/new.go
--- a/old.go
+++ b/sub/new.go
@@ -3 +3 @@
-old
+new
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"sub/new.go": {{Start: 3, End: 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffMultipleFiles(t *testing.T) {
	in := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-x
+y
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -5,0 +6,2 @@
+m
+n
`
	got, err := ParseUnifiedDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]LineRange{
		"a.go": {{Start: 1, End: 1}},
		"b.go": {{Start: 6, End: 7}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseUnifiedDiffEmpty(t *testing.T) {
	got, err := ParseUnifiedDiff(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty diff should yield no ranges, got %v", got)
	}
}

func TestParseHunkHeaderMalformed(t *testing.T) {
	cases := []string{
		"@@ no plus marker @@",
		"@@ -1,2 +abc,def @@",   // non-numeric start
		"@@ -1,2 +1,abc @@",     // non-numeric count
		"@@ -1,2 +0,1 @@",       // start <= 0
	}
	for _, c := range cases {
		if _, ok := parseHunkHeader(c); ok {
			t.Errorf("parseHunkHeader(%q) should have returned ok=false", c)
		}
	}
}

func TestStripDiffPrefix(t *testing.T) {
	cases := map[string]string{
		"a/foo.go":     "foo.go",
		"b/sub/foo.go": "sub/foo.go",
		"foo.go":       "foo.go", // diff.noprefix
		"":             "",
	}
	for in, want := range cases {
		if got := stripDiffPrefix(in); got != want {
			t.Errorf("stripDiffPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineRangeContains(t *testing.T) {
	r := LineRange{Start: 5, End: 10}
	cases := map[int]bool{
		4:  false,
		5:  true,
		7:  true,
		10: true,
		11: false,
	}
	for line, want := range cases {
		if got := r.Contains(line); got != want {
			t.Errorf("Contains(%d) = %v, want %v", line, got, want)
		}
	}
}

func TestFilterByDiffKeepsInRange(t *testing.T) {
	gitRoot := "/repo"
	ranges := map[string][]LineRange{
		"pkg/a.go": {{Start: 10, End: 12}, {Start: 20, End: 20}},
	}
	mutants := []mutator.Mutant{
		{ID: 1, File: "/repo/pkg/a.go", Line: 9},  // before
		{ID: 2, File: "/repo/pkg/a.go", Line: 10}, // start of range
		{ID: 3, File: "/repo/pkg/a.go", Line: 12}, // end of range
		{ID: 4, File: "/repo/pkg/a.go", Line: 13}, // gap
		{ID: 5, File: "/repo/pkg/a.go", Line: 20}, // second range
		{ID: 6, File: "/repo/pkg/b.go", Line: 10}, // file not in diff
	}
	got := FilterByDiff(mutants, ranges, gitRoot)
	if len(got) != 3 {
		t.Fatalf("expected 3 mutants kept, got %d: %+v", len(got), got)
	}
	wantLines := []int{10, 12, 20}
	for i, m := range got {
		if m.Line != wantLines[i] {
			t.Errorf("mutant[%d].Line = %d, want %d", i, m.Line, wantLines[i])
		}
		if m.ID != i+1 {
			t.Errorf("mutant[%d].ID = %d, want %d (renumbered)", i, m.ID, i+1)
		}
	}
}

func TestFilterByDiffEmptyRangesReturnsNone(t *testing.T) {
	mutants := []mutator.Mutant{
		{ID: 1, File: "/repo/a.go", Line: 1},
	}
	got := FilterByDiff(mutants, map[string][]LineRange{}, "/repo")
	if len(got) != 0 {
		t.Errorf("expected 0 mutants when ranges is empty, got %d", len(got))
	}
}

func TestFilterByDiffPathOutsideGitRoot(t *testing.T) {
	// File path outside git root: filepath.Rel succeeds but produces "../..."
	// which won't match any diff entry. Mutant is dropped.
	ranges := map[string][]LineRange{
		"a.go": {{Start: 1, End: 1}},
	}
	mutants := []mutator.Mutant{
		{ID: 1, File: "/elsewhere/a.go", Line: 1},
	}
	got := FilterByDiff(mutants, ranges, "/repo")
	if len(got) != 0 {
		t.Errorf("expected 0 mutants outside git root, got %d", len(got))
	}
}

// TestRunGitDiffIntegration constructs a tiny git repo, makes a commit, edits
// a file in the working tree, then runs RunGitDiff against HEAD.
func TestRunGitDiffIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	ctx := context.Background()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("package p\nfunc F() int { return 1 + 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	// Edit line 2 in working tree.
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("package p\nfunc F() int { return 3 + 4 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RunGitDiff(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("RunGitDiff: %v", err)
	}
	if len(got["f.go"]) == 0 {
		t.Fatalf("expected ranges for f.go, got %v", got)
	}
	if got["f.go"][0].Start != 2 {
		t.Errorf("expected change on line 2, got %v", got["f.go"][0])
	}

	// GitRoot should resolve to dir (canonicalized — macOS /private/ etc.).
	root, err := GitRoot(ctx, dir)
	if err != nil {
		t.Fatalf("GitRoot: %v", err)
	}
	if !strings.HasSuffix(root, filepath.Base(dir)) {
		t.Errorf("GitRoot=%q, expected suffix %q", root, filepath.Base(dir))
	}
}

func TestGitRootNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Walk up could land in a real repo; create a sentinel `.git` *file*
	// (not dir) to make git fail decisively. Actually simpler: run from
	// a path that has an empty parent. Use a temp dir with no .git.
	// On most CI temp dirs aren't inside a git repo, but locally the
	// developer's home might be — guard accordingly.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Skip("temp dir is inside a git repo (developer machine); skipping negative case")
	}
	if _, err := GitRoot(context.Background(), dir); err == nil {
		t.Error("expected error when not in a git repo")
	}
}
