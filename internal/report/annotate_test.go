package report

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteGitHubAnnotations_OnlyLived(t *testing.T) {
	r := &Report{
		Files: []FileReport{
			{
				FileName: "a.go",
				Mutations: []MutationReport{
					{Type: "ARITHMETIC_BASE", Status: "KILLED", Line: 1, Column: 1, Original: "+", Replacement: "-"},
					{Type: "CONDITIONALS_NEGATION", Status: "LIVED", Line: 12, Column: 4, Original: "==", Replacement: "!="},
					{Type: "BRANCH_IF", Status: "NOT COVERED", Line: 20, Column: 2},
					{Type: "INVERT_LOGICAL", Status: "TIMED OUT", Line: 30, Column: 8, Original: "&&", Replacement: "||"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteGitHubAnnotations(&buf, r); err != nil {
		t.Fatalf("WriteGitHubAnnotations: %v", err)
	}

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 annotation (only LIVED), got %d:\n%s", len(lines), out)
	}
	want := "::warning file=a.go,line=12,col=4::Mutant LIVED — CONDITIONALS_NEGATION (== → !=)"
	if lines[0] != want {
		t.Errorf("annotation mismatch:\n got: %q\nwant: %q", lines[0], want)
	}
}

func TestWriteGitHubAnnotations_FallsBackWhenOriginalMissing(t *testing.T) {
	r := &Report{
		Files: []FileReport{{
			FileName: "a.go",
			Mutations: []MutationReport{
				{Type: "STATEMENT_REMOVE", Status: "LIVED", Line: 5, Column: 1},
			},
		}},
	}
	var buf bytes.Buffer
	if err := WriteGitHubAnnotations(&buf, r); err != nil {
		t.Fatalf("WriteGitHubAnnotations: %v", err)
	}
	got := strings.TrimRight(buf.String(), "\n")
	want := "::warning file=a.go,line=5,col=1::Mutant LIVED — STATEMENT_REMOVE"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeProperty(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a.go", "a.go"},
		{"path/with,comma.go", "path/with%2Ccomma.go"},
		{"C:\\file.go", "C%3A\\file.go"},
		{"weird\nname.go", "weird%0Aname.go"},
		{"100%done.go", "100%25done.go"},
	}
	for _, tc := range cases {
		got := escapeProperty(tc.in)
		if got != tc.want {
			t.Errorf("escapeProperty(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEscapeMessage(t *testing.T) {
	// Message escapes a smaller set: only `%`, `\r`, `\n`. Colons and commas
	// are legal in messages.
	cases := []struct{ in, want string }{
		{"a == b, want a != b", "a == b, want a != b"},
		{"line1\nline2", "line1%0Aline2"},
		{"50%", "50%25"},
		{"colon: kept", "colon: kept"},
	}
	for _, tc := range cases {
		got := escapeMessage(tc.in)
		if got != tc.want {
			t.Errorf("escapeMessage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteGitHubAnnotations_EmptyReport(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteGitHubAnnotations(&buf, &Report{}); err != nil {
		t.Fatalf("WriteGitHubAnnotations: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty Report should produce no output, got: %q", buf.String())
	}
}

func TestWriteGitHubAnnotations_EscapesNewlinesInOriginal(t *testing.T) {
	// Block-level mutators may have multi-line Original strings (e.g. an if body).
	r := &Report{
		Files: []FileReport{{
			FileName: "a.go",
			Mutations: []MutationReport{
				{Type: "BRANCH_IF", Status: "LIVED", Line: 10, Column: 1,
					Original: "doStuff()\nmore()", Replacement: "_ = 0"},
			},
		}},
	}
	var buf bytes.Buffer
	if err := WriteGitHubAnnotations(&buf, r); err != nil {
		t.Fatalf("WriteGitHubAnnotations: %v", err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("expected exactly one trailing newline (multi-line Original must be escaped), got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "%0A") {
		t.Errorf("expected newline in Original to be encoded as %%0A, got: %q", buf.String())
	}
}
