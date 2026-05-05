package discover

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// directivePrefix is the namespace marker every directive comment must
// carry. Defined once so the fast-path byte scan and the per-comment
// parse share one source of truth.
const directivePrefix = "gomutants:"

var directivePrefixBytes = []byte(directivePrefix)

// directiveKind classifies how a `// gomutants:disable*` directive maps
// to source positions.
type directiveKind int

const (
	directiveSameLine directiveKind = iota
	directiveNextLine
	directiveFunc
	directiveRegexp
)

// directive is a parsed `// gomutants:disable*` source annotation.
// Mutators nil/empty means "all mutators". FuncStart/FuncEnd are set
// only for directiveFunc; Regexp only for directiveRegexp.
type directive struct {
	Kind      directiveKind
	Mutators  map[mutator.MutationType]struct{}
	Reason    string
	Line      int
	FuncStart int
	FuncEnd   int
	Regexp    *regexp.Regexp
}

// Suppression records that a mutant was dropped by a directive. Used
// for verbose logging and the aggregate `mutants_suppressed` count.
type Suppression struct {
	Mutant mutator.Mutant
	Reason string
}

// FilterByDirectives drops mutants matched by any `// gomutants:disable*`
// directive in their source file. Returns the surviving mutants and the
// suppressions for reporting. Malformed directives and unknown mutator
// names are written to os.Stderr and the offending directive (or name)
// is dropped.
//
// Not idempotent: warnings are emitted on each call. Call once per run,
// after all other discovery filters have run.
func FilterByDirectives(fset *token.FileSet, mutants []mutator.Mutant) ([]mutator.Mutant, []Suppression, error) {
	return filterByDirectives(fset, mutants, os.Stderr)
}

func filterByDirectives(fset *token.FileSet, mutants []mutator.Mutant, warn io.Writer) ([]mutator.Mutant, []Suppression, error) {
	if len(mutants) == 0 {
		return mutants, nil, nil
	}

	relPathOf := make(map[string]string, len(mutants))
	for _, m := range mutants {
		if _, ok := relPathOf[m.File]; !ok {
			relPathOf[m.File] = m.RelFile
		}
	}

	indexes := make(map[string]*fileIndex, len(relPathOf))
	for path, rel := range relPathOf {
		idx, err := buildFileIndex(fset, path, rel, warn)
		if err != nil {
			return nil, nil, err
		}
		indexes[path] = idx
	}

	kept := make([]mutator.Mutant, 0, len(mutants))
	var suppressed []Suppression
	for _, m := range mutants {
		idx, ok := indexes[m.File]
		if !ok || idx == nil {
			kept = append(kept, m)
			continue
		}
		if d, ok := matchMutant(idx, m); ok {
			suppressed = append(suppressed, Suppression{Mutant: m, Reason: d.Reason})
			continue
		}
		kept = append(kept, m)
	}
	return kept, suppressed, nil
}

// fileIndex holds parsed directives for one source file, organised so
// each mutant lookup is O(1) for line-scoped directives and O(F+R) for
// the small per-file slices of func-scope and regexp directives.
type fileIndex struct {
	sameLine map[int][]directive
	nextLine map[int][]directive
	funcs    []directive
	regexps  []directive
	lines    []string
}

func buildFileIndex(fset *token.FileSet, path, relPath string, warn io.Writer) (*fileIndex, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !bytes.Contains(src, directivePrefixBytes) {
		return &fileIndex{}, nil
	}
	// ParseComments is required so funcDecl.Doc is populated and we can
	// distinguish a comment that sits on a function declaration from one
	// that floats free in the file.
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		// Discovery already warned for unparseable files; treat as no
		// directives rather than failing the whole run.
		return &fileIndex{}, nil
	}

	funcRangeByDocPos := make(map[token.Pos][2]int)
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Doc == nil || fd.Body == nil {
			continue
		}
		startLine := fset.Position(fd.Body.Lbrace).Line
		endLine := fset.Position(fd.Body.Rbrace).Line
		funcRangeByDocPos[fd.Doc.Pos()] = [2]int{startLine, endLine}
	}

	idx := &fileIndex{
		sameLine: make(map[int][]directive),
		nextLine: make(map[int][]directive),
	}

	type pendingNextLine struct {
		d          directive
		sourceLine int
	}
	var pending []pendingNextLine

	for _, cg := range file.Comments {
		funcRange, isFuncDoc := funcRangeByDocPos[cg.Pos()]
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//") {
				continue
			}
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if !strings.HasPrefix(text, directivePrefix) {
				continue
			}
			line := fset.Position(c.Pos()).Line
			d, parsedOK := parseDirective(text, line, relPath, warn)
			if !parsedOK {
				continue
			}
			switch d.Kind {
			case directiveSameLine:
				idx.sameLine[line] = append(idx.sameLine[line], d)
			case directiveNextLine:
				pending = append(pending, pendingNextLine{d, line})
			case directiveFunc:
				if !isFuncDoc {
					fmt.Fprintf(warn, "gomutants: %s:%d: disable-func not on a function declaration (skipped)\n", relPath, line)
					continue
				}
				d.FuncStart = funcRange[0]
				d.FuncEnd = funcRange[1]
				idx.funcs = append(idx.funcs, d)
			case directiveRegexp:
				idx.regexps = append(idx.regexps, d)
			}
		}
	}

	// Only materialise the line array if we actually need to scan source
	// text — for files with only same-line and disable-func directives,
	// this skips the allocation entirely.
	if len(pending) > 0 || len(idx.regexps) > 0 {
		idx.lines = strings.Split(string(src), "\n")
	}
	for _, p := range pending {
		target := nextNonCommentLine(idx.lines, p.sourceLine)
		if target == 0 {
			fmt.Fprintf(warn, "gomutants: %s:%d: disable-next-line has no following code (skipped)\n", relPath, p.sourceLine)
			continue
		}
		idx.nextLine[target] = append(idx.nextLine[target], p.d)
	}
	return idx, nil
}

func matchMutant(idx *fileIndex, m mutator.Mutant) (directive, bool) {
	for _, d := range idx.sameLine[m.Line] {
		if directiveMatchesType(d, m.Type) {
			return d, true
		}
	}
	for _, d := range idx.nextLine[m.Line] {
		if directiveMatchesType(d, m.Type) {
			return d, true
		}
	}
	for _, d := range idx.funcs {
		if m.Line >= d.FuncStart && m.Line <= d.FuncEnd && directiveMatchesType(d, m.Type) {
			return d, true
		}
	}
	for _, d := range idx.regexps {
		if d.Regexp == nil {
			continue
		}
		if m.Line < 1 || m.Line > len(idx.lines) {
			continue
		}
		if d.Regexp.MatchString(idx.lines[m.Line-1]) && directiveMatchesType(d, m.Type) {
			return d, true
		}
	}
	return directive{}, false
}

// directiveMatchesType reports whether a directive's mutator filter
// applies to the given mutator type. Empty/nil Mutators means "all
// mutators" (the result of either omitting the list or supplying "*").
func directiveMatchesType(d directive, t mutator.MutationType) bool {
	if len(d.Mutators) == 0 {
		return true
	}
	_, ok := d.Mutators[t]
	return ok
}

// parseDirective parses a stripped directive body like
// "disable ARITHMETIC_BASE reason=\"foo\"". The leading "gomutants:" has
// already been verified by the caller; this function consumes from the
// kind onward.
func parseDirective(text string, line int, relPath string, warn io.Writer) (directive, bool) {
	rest := strings.TrimPrefix(text, directivePrefix)
	kindStr, rest := splitFirstWord(rest)

	var kind directiveKind
	switch kindStr {
	case "disable":
		kind = directiveSameLine
	case "disable-next-line":
		kind = directiveNextLine
	case "disable-func":
		kind = directiveFunc
	case "disable-regexp":
		kind = directiveRegexp
	default:
		// Unrecognised kind under the gomutants: namespace. Silent so a
		// future kind added in a newer release doesn't break older
		// runs.
		return directive{}, false
	}

	d := directive{Kind: kind, Line: line}

	if kind == directiveRegexp {
		patStr, after := splitFirstWord(rest)
		if patStr == "" {
			fmt.Fprintf(warn, "gomutants: %s:%d: disable-regexp missing pattern (skipped)\n", relPath, line)
			return directive{}, false
		}
		re, err := regexp.Compile(patStr)
		if err != nil {
			fmt.Fprintf(warn, "gomutants: %s:%d: disable-regexp invalid pattern %q: %v (skipped)\n", relPath, line, patStr, err)
			return directive{}, false
		}
		d.Regexp = re
		rest = after
	}

	mutatorsStr, reasonStr, ok := splitMutatorsAndReason(rest)
	if !ok {
		fmt.Fprintf(warn, "gomutants: %s:%d: malformed reason= (skipped)\n", relPath, line)
		return directive{}, false
	}
	d.Reason = reasonStr

	if mutatorsStr != "" && mutatorsStr != "*" {
		d.Mutators = make(map[mutator.MutationType]struct{})
		for name := range strings.SplitSeq(mutatorsStr, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if !isKnownMutator(name) {
				if d.Kind == directiveRegexp {
					fmt.Fprintf(warn, "gomutants: %s:%d: unknown mutator %q in disable-regexp directive (note: patterns cannot contain whitespace; use \\s) (skipped)\n", relPath, line, name)
				} else {
					fmt.Fprintf(warn, "gomutants: %s:%d: unknown mutator %q in directive (skipped)\n", relPath, line, name)
				}
				continue
			}
			d.Mutators[mutator.MutationType(name)] = struct{}{}
		}
		// If every name was rejected, fall back to "all" so the directive isn't a silent no-op.
		if len(d.Mutators) == 0 {
			d.Mutators = nil
		}
	}

	return d, true
}

// splitFirstWord returns the first whitespace-delimited token of s and
// the trimmed remainder. If s contains no whitespace, the whole string
// is returned as the first word and the remainder is empty.
func splitFirstWord(s string) (first, rest string) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i], strings.TrimLeft(s[i:], " \t")
		}
	}
	return s, ""
}

// splitMutatorsAndReason parses an optional mutator list followed by an
// optional `reason="..."`. Returns ok=false if `reason=` is present but
// malformed (e.g., unbalanced quotes, trailing junk).
func splitMutatorsAndReason(s string) (mutators, reason string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", true
	}
	before, tail, found := strings.Cut(s, "reason=")
	if !found {
		return s, "", true
	}
	mutators = strings.TrimSpace(before)
	if !strings.HasPrefix(tail, `"`) {
		return "", "", false
	}
	end := scanQuotedEnd(tail)
	if end < 0 {
		return "", "", false
	}
	val, err := strconv.Unquote(tail[:end])
	if err != nil {
		return "", "", false
	}
	if strings.TrimSpace(tail[end:]) != "" {
		return "", "", false
	}
	return mutators, val, true
}

// scanQuotedEnd returns the index just past the closing quote of a Go
// double-quoted string starting at s[0]=='"'. Returns -1 if the string
// is unterminated.
func scanQuotedEnd(s string) int {
	if len(s) == 0 || s[0] != '"' {
		return -1
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// nextNonCommentLine returns the smallest line number > directiveLine
// whose source line is neither blank nor a `//` comment. Returns 0 if
// every following line is blank or comment-only (i.e. the directive has
// no code to apply to).
func nextNonCommentLine(lines []string, directiveLine int) int {
	for l := directiveLine + 1; l <= len(lines); l++ {
		trimmed := strings.TrimSpace(lines[l-1])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		return l
	}
	return 0
}

// knownMutatorTypes is the canonical set of registered mutator type
// names, validated independent of the user's --only/--disable so that
// directive typos surface even when the user has narrowed their set.
var knownMutatorTypes = func() map[string]struct{} {
	reg := mutator.NewRegistry()
	out := make(map[string]struct{}, len(reg.Mutators()))
	for _, m := range reg.Mutators() {
		out[string(m.Type())] = struct{}{}
	}
	return out
}()

func isKnownMutator(name string) bool {
	_, ok := knownMutatorTypes[name]
	return ok
}
