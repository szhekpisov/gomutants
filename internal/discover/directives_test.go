package discover

import (
	"bytes"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// writeFixture writes src to a temp file under a fresh package dir and
// runs Discover, returning the resulting mutants and the absolute path
// to the source file. The package always contains exactly one .go file.
func writeFixture(t *testing.T, src string) (mutants []mutator.Mutant, path string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs := []Package{{Dir: dir, ImportPath: "example.com/test", GoFiles: []string{"src.go"}}}
	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	return Discover(fset, pkgs, reg.Mutators(), dir, "example.com/test"), path
}

// suppressedTypes returns the set of mutator types in suppressions.
func suppressedTypes(suppressed []Suppression) map[mutator.MutationType]int {
	out := make(map[mutator.MutationType]int)
	for _, s := range suppressed {
		out[s.Mutant.Type]++
	}
	return out
}

// keptTypes returns the set of mutator types in surviving mutants.
func keptTypes(kept []mutator.Mutant) map[mutator.MutationType]int {
	out := make(map[mutator.MutationType]int)
	for _, m := range kept {
		out[m.Type]++
	}
	return out
}

func TestSplitMutatorsAndReason(t *testing.T) {
	cases := []struct {
		in            string
		wantMutators  string
		wantReason    string
		wantOK        bool
	}{
		{"", "", "", true},
		{"ARITHMETIC_BASE", "ARITHMETIC_BASE", "", true},
		{"A,B", "A,B", "", true},
		{`reason="hi"`, "", "hi", true},
		{`A reason="hi"`, "A", "hi", true},
		{`A,B reason="multi word"`, "A,B", "multi word", true},
		{`reason="esc \"q\""`, "", `esc "q"`, true},
		{`reason="unterminated`, "", "", false},
		{`reason=bare`, "", "", false},
		{`reason="ok" trailing`, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			gotM, gotR, gotOK := splitMutatorsAndReason(c.in)
			if gotOK != c.wantOK || gotM != c.wantMutators || gotR != c.wantReason {
				t.Errorf("splitMutatorsAndReason(%q) = (%q,%q,%v), want (%q,%q,%v)",
					c.in, gotM, gotR, gotOK, c.wantMutators, c.wantReason, c.wantOK)
			}
		})
	}
}

func TestParsedirectiveKinds(t *testing.T) {
	cases := []struct {
		text     string
		wantKind directiveKind
		wantOK   bool
	}{
		{"gomutants:disable", directiveSameLine, true},
		{"gomutants:disable-next-line", directiveNextLine, true},
		{"gomutants:disable-func", directiveFunc, true},
		{"gomutants:disable-regexp ^foo", directiveRegexp, true},
		{"gomutants:disable ARITHMETIC_BASE", directiveSameLine, true},
		{"gomutants:disable *", directiveSameLine, true},
		{"gomutants:disable-future-thing", 0, false}, // unknown kind: silently ignored
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			var buf bytes.Buffer
			d, ok := parseDirective(c.text, 1, "x.go", &buf)
			if ok != c.wantOK {
				t.Fatalf("ok=%v, want %v (warn=%q)", ok, c.wantOK, buf.String())
			}
			if ok && d.Kind != c.wantKind {
				t.Errorf("kind=%v, want %v", d.Kind, c.wantKind)
			}
		})
	}
}

func TestParseDirectiveUnknownMutatorWarns(t *testing.T) {
	var buf bytes.Buffer
	d, ok := parseDirective("gomutants:disable ARITHMETIC_BASE,NOT_A_MUTATOR", 5, "x.go", &buf)
	if !ok {
		t.Fatalf("expected directive to parse")
	}
	if !strings.Contains(buf.String(), `unknown mutator "NOT_A_MUTATOR"`) {
		t.Errorf("expected warning about unknown mutator, got %q", buf.String())
	}
	if _, ok := d.Mutators[mutator.ArithmeticBase]; !ok {
		t.Errorf("ARITHMETIC_BASE should still be in the directive's mutator set: %v", d.Mutators)
	}
	if _, ok := d.Mutators["NOT_A_MUTATOR"]; ok {
		t.Errorf("unknown mutator should not be in the directive's mutator set")
	}
}

func TestParseDirectiveAllUnknownFallsBackToAll(t *testing.T) {
	// If every name is rejected, the directive applies to all mutators
	// rather than silently matching nothing — surfaces the warning while
	// keeping intent ("ignore here") intact.
	var buf bytes.Buffer
	d, ok := parseDirective("gomutants:disable BOGUS,ALSO_BOGUS", 5, "x.go", &buf)
	if !ok {
		t.Fatalf("expected directive to parse")
	}
	if d.Mutators != nil {
		t.Errorf("expected fallback to all-mutators (nil map), got %v", d.Mutators)
	}
}

func TestParseDirectiveMalformedReason(t *testing.T) {
	var buf bytes.Buffer
	_, ok := parseDirective(`gomutants:disable reason="unterminated`, 5, "x.go", &buf)
	if ok {
		t.Fatalf("expected malformed reason to drop directive")
	}
	if !strings.Contains(buf.String(), "malformed reason=") {
		t.Errorf("expected malformed-reason warning, got %q", buf.String())
	}
}

func TestParsedirectiveRegexpMissingPattern(t *testing.T) {
	var buf bytes.Buffer
	_, ok := parseDirective("gomutants:disable-regexp", 5, "x.go", &buf)
	if ok {
		t.Fatalf("expected missing pattern to drop directive")
	}
	if !strings.Contains(buf.String(), "missing pattern") {
		t.Errorf("expected missing-pattern warning, got %q", buf.String())
	}
}

func TestParsedirectiveRegexpInvalidPattern(t *testing.T) {
	var buf bytes.Buffer
	_, ok := parseDirective(`gomutants:disable-regexp [unclosed`, 5, "x.go", &buf)
	if ok {
		t.Fatalf("expected invalid pattern to drop directive")
	}
	if !strings.Contains(buf.String(), "invalid pattern") {
		t.Errorf("expected invalid-pattern warning, got %q", buf.String())
	}
}

func TestParseDirectiveReasonRoundTrips(t *testing.T) {
	d, ok := parseDirective(`gomutants:disable reason="commutative \"add\""`, 5, "x.go", &bytes.Buffer{})
	if !ok {
		t.Fatalf("expected directive to parse")
	}
	if want := `commutative "add"`; d.Reason != want {
		t.Errorf("reason=%q, want %q", d.Reason, want)
	}
}

func TestNextNonCommentLine(t *testing.T) {
	lines := []string{
		"package p", // 1
		"",          // 2
		"// comment",        // 3 (the directive sits here)
		"// another comment",// 4
		"",                  // 5
		"x := a + b",        // 6 — first non-comment, non-blank line
	}
	if got := nextNonCommentLine(lines, 3); got != 6 {
		t.Errorf("nextNonCommentLine(_, 3) = %d, want 6", got)
	}
	// Off the end returns 0 (sentinel for "no following code").
	if got := nextNonCommentLine(lines, 6); got != 0 {
		t.Errorf("nextNonCommentLine off-end = %d, want 0", got)
	}
}

func TestFilterNextLineAtEOFWarns(t *testing.T) {
	// Directive sits on the last meaningful line with no code after it —
	// should emit a warning and not suppress anything.
	src := `package p

func F(a, b int) int { return a + b }

// gomutants:disable-next-line
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	var warn bytes.Buffer
	kept, suppressed, err := filterByDirectives(fset, mutants, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 0 {
		t.Errorf("expected no suppressions; got %d", len(suppressed))
	}
	if len(kept) != len(mutants) {
		t.Errorf("expected all mutants to survive; got kept=%d in=%d", len(kept), len(mutants))
	}
	if !strings.Contains(warn.String(), "no following code") {
		t.Errorf("expected EOF warning; got %q", warn.String())
	}
}

func TestParsedirectiveRegexpUnknownMutatorHintsWhitespace(t *testing.T) {
	// `disable-regexp foo bar` — `bar` becomes the mutator name; the
	// warning should hint that whitespace is unsupported in patterns.
	var buf bytes.Buffer
	_, ok := parseDirective("gomutants:disable-regexp foo bar", 5, "x.go", &buf)
	if !ok {
		t.Fatalf("expected directive to parse; warn=%q", buf.String())
	}
	if !strings.Contains(buf.String(), `unknown mutator "bar"`) {
		t.Errorf("expected unknown-mutator warning; got %q", buf.String())
	}
	if !strings.Contains(buf.String(), `cannot contain whitespace`) {
		t.Errorf("expected whitespace hint in regex-context warning; got %q", buf.String())
	}
}

func TestFilterSameLine(t *testing.T) {
	src := `package p

func F(a, b int) int {
	return a + b // gomutants:disable ARITHMETIC_BASE reason="commutative"
}
`
	mutants, _ := writeFixture(t, src)
	hadArithmetic := false
	for _, m := range mutants {
		if m.Type == mutator.ArithmeticBase {
			hadArithmetic = true
		}
	}
	if !hadArithmetic {
		t.Fatal("fixture didn't produce any ARITHMETIC_BASE mutant; check src")
	}

	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	if got := suppressedTypes(suppressed)[mutator.ArithmeticBase]; got == 0 {
		t.Errorf("expected ARITHMETIC_BASE to be suppressed; got %v", suppressedTypes(suppressed))
	}
	if got := keptTypes(kept)[mutator.ArithmeticBase]; got != 0 {
		t.Errorf("ARITHMETIC_BASE should be removed from kept; got %d", got)
	}
	if len(suppressed) > 0 && suppressed[0].Reason != "commutative" {
		t.Errorf("reason=%q, want commutative", suppressed[0].Reason)
	}
}

func TestFilterNextLine(t *testing.T) {
	src := `package p

func F(a, b int) int {
	// gomutants:disable-next-line
	return a + b
}
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) == 0 {
		t.Fatal("expected at least one suppressed mutant on the line after the directive")
	}
	for _, m := range kept {
		if m.Line == 5 {
			t.Errorf("mutant on line 5 (%s) was not suppressed", m.Type)
		}
	}
}

func TestFilterNextLineSkipsCommentLines(t *testing.T) {
	// disable-next-line on line 3; line 4 is also a comment; line 5 is the actual code.
	src := `package p

// gomutants:disable-next-line
// the comment we want to skip
func F(a, b int) int { return a + b }
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) == 0 {
		t.Fatal("expected directive to skip past comment-only lines and suppress code on line 5")
	}
	for _, m := range kept {
		if m.Line == 5 {
			t.Errorf("mutant on line 5 (%s) was not suppressed", m.Type)
		}
	}
}

func TestFilterFunc(t *testing.T) {
	src := `package p

// gomutants:disable-func reason="generated"
func F(a, b int) int {
	if a > b {
		return a + b
	}
	return a - b
}

func G(a, b int) int {
	return a * b
}
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}

	// All mutants in F (lines 4..9) should be suppressed; G's stay.
	for _, s := range suppressed {
		if s.Mutant.Line < 4 || s.Mutant.Line > 9 {
			t.Errorf("suppressed mutant outside F's body: line=%d type=%s", s.Mutant.Line, s.Mutant.Type)
		}
	}
	gKept := false
	for _, m := range kept {
		if m.Line == 12 && m.Type == mutator.ArithmeticBase {
			gKept = true
		}
		if m.Line >= 4 && m.Line <= 9 {
			t.Errorf("F-body mutant survived disable-func: line=%d type=%s", m.Line, m.Type)
		}
	}
	if !gKept {
		t.Errorf("G's ARITHMETIC_BASE on line 12 should have survived")
	}
}

func TestFilterFuncOnNonFuncWarns(t *testing.T) {
	src := `package p

// gomutants:disable-func
var x = 1 + 2
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	var warn bytes.Buffer
	kept, suppressed, err := filterByDirectives(fset, mutants, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 0 {
		t.Errorf("disable-func not on a FuncDecl should not suppress anything; got %d", len(suppressed))
	}
	if !strings.Contains(warn.String(), "not on a function declaration") {
		t.Errorf("expected misplaced-disable-func warning; got %q", warn.String())
	}
	if len(kept) != len(mutants) {
		t.Errorf("nothing should have been filtered; got kept=%d in=%d", len(kept), len(mutants))
	}
}

func TestFilterRegexp(t *testing.T) {
	src := `package p

// gomutants:disable-regexp ^\s*log\.

import "log"

func F(a, b int) int {
	log.Printf("a+b = %d", a+b)
	return a + b
}
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	// The log.Printf line should be suppressed; the bare `return a + b` should not.
	logSuppressed := false
	for _, s := range suppressed {
		if strings.Contains(s.Mutant.Original, "+") && s.Mutant.Line == 8 {
			logSuppressed = true
		}
	}
	if !logSuppressed {
		t.Errorf("expected mutants on log.Printf line to be suppressed; got %v", suppressed)
	}
	returnSurvived := false
	for _, m := range kept {
		if m.Line == 9 && m.Type == mutator.ArithmeticBase {
			returnSurvived = true
		}
	}
	if !returnSurvived {
		t.Errorf("ARITHMETIC_BASE on the return line should not be suppressed; kept=%v", kept)
	}
}

func TestFilterMutatorTypeScoping(t *testing.T) {
	src := `package p

func F(a, b int, c bool) int {
	if c { return a + b } // gomutants:disable ARITHMETIC_BASE
	return a - b
}
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	// On line 4: ARITHMETIC_BASE should be suppressed; BRANCH_IF (and any
	// other non-arithmetic mutators on the same line) should survive.
	for _, s := range suppressed {
		if s.Mutant.Line == 4 && s.Mutant.Type != mutator.ArithmeticBase {
			t.Errorf("non-ARITHMETIC mutant suppressed on line 4: %s", s.Mutant.Type)
		}
	}
	branchIfKept := false
	for _, m := range kept {
		if m.Line == 4 && m.Type == mutator.BranchIf {
			branchIfKept = true
		}
	}
	if !branchIfKept {
		t.Errorf("BRANCH_IF on line 4 should have survived (directive only named ARITHMETIC_BASE)")
	}
}

func TestFilterWildcardSuppressesAll(t *testing.T) {
	src := `package p

func F(a, b int, c bool) int {
	if c { return a + b } // gomutants:disable *
	return a - b
}
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, _, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range kept {
		if m.Line == 4 {
			t.Errorf("wildcard should suppress every mutant on line 4; got survivor %s", m.Type)
		}
	}
}

func TestFilterMultiFileIsolation(t *testing.T) {
	dir := t.TempDir()
	srcA := `package p

func A(a, b int) int { return a + b } // gomutants:disable ARITHMETIC_BASE
`
	srcB := `package p

func B(a, b int) int { return a + b }
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(srcA), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(srcB), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs := []Package{{Dir: dir, ImportPath: "example.com/test", GoFiles: []string{"a.go", "b.go"}}}
	reg := mutator.NewRegistry()
	fset := token.NewFileSet()
	mutants := Discover(fset, pkgs, reg.Mutators(), dir, "example.com/test")

	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range suppressed {
		if filepath.Base(s.Mutant.File) != "a.go" {
			t.Errorf("directive in a.go leaked into %s", s.Mutant.File)
		}
	}
	bArithmeticKept := false
	for _, m := range kept {
		if filepath.Base(m.File) == "b.go" && m.Type == mutator.ArithmeticBase {
			bArithmeticKept = true
		}
	}
	if !bArithmeticKept {
		t.Errorf("b.go's ARITHMETIC_BASE should have survived")
	}
}

func TestFilterEmpty(t *testing.T) {
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kept != nil || suppressed != nil {
		t.Errorf("empty input should yield empty output; got kept=%v suppressed=%v", kept, suppressed)
	}
}

func TestFilterNoDirectivesKeepsAll(t *testing.T) {
	src := `package p

func F(a, b int) int { return a + b }
`
	mutants, _ := writeFixture(t, src)
	fset := token.NewFileSet()
	kept, suppressed, err := FilterByDirectives(fset, mutants)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 0 {
		t.Errorf("expected zero suppressed; got %d", len(suppressed))
	}
	if len(kept) != len(mutants) {
		t.Errorf("expected all mutants to be kept; got kept=%d in=%d", len(kept), len(mutants))
	}
}
