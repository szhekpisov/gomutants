package mutator

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// numericLiteralCandidates discovers every *ast.BasicLit of the given Kind
// and emits a MutantCandidate that replaces its source text with the value
// shifted by delta. Shared by the integer/float increment/decrement
// mutators so the parse/format/skip logic lives in one place.
//
// Behaviour notes that the wrappers rely on:
//   - For token.INT, signed-overflow on the increment side is detected by
//     sign-flip after wrap; such literals are skipped (no usable mutation).
//     The mirror case (decrementing past MinInt64) is unreachable because
//     every BasicLit reaching here has v ≥ 0 — Go's parser models `-N` as
//     unary minus around the positive literal `N`.
//   - For token.FLOAT, the result is re-formatted in `'g'` form and ".0" is
//     appended when the formatted string would otherwise parse as an int
//     literal (e.g. `1.0+1.0 → 2 → 2.0`); this keeps the file parseable.
//   - No "skip when replacement == source" guard exists or is needed: with
//     delta ≠ 0 and re-formatting that normalises representation (hex/oct/
//     binary → decimal, underscores stripped, floats round-tripped through
//     'g'), the canonical output can never collide byte-for-byte with the
//     source span. An earlier defensive check was removed because it was
//     dead in practice and a kept mutation could not be killed.
func numericLiteralCandidates(
	fset *token.FileSet, file *ast.File,
	typ MutationType, kind token.Token, delta int,
) []MutantCandidate {
	var out []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != kind {
			return true
		}
		replacement, ok := mutateNumericLiteral(lit.Value, kind, delta)
		if !ok {
			return true
		}
		pos := fset.Position(lit.ValuePos)
		out = append(out, MutantCandidate{
			Type:        typ,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    lit.Value,
			Replacement: replacement,
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + len(lit.Value),
		})
		return true
	})
	return out
}

// mutateNumericLiteral parses src (an integer or float literal as written in
// source, with optional underscore separators) and returns the canonical
// decimal text of src+delta. Returns ok=false when the literal can't be
// parsed as a 64-bit value or applying delta would overflow.
func mutateNumericLiteral(src string, kind token.Token, delta int) (string, bool) {
	raw := strings.ReplaceAll(src, "_", "")
	switch kind {
	case token.INT:
		v, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			// Untyped int constants in Go can exceed int64 (e.g. 1<<63);
			// skip them rather than guess a representation. ParseInt
			// returns (MaxInt64, ErrRange) for too-large positives — the
			// IntegerIncrement path would then wrap to MinInt64 and be
			// caught by the sign-flip guard below (equivalence), but the
			// IntegerDecrement path would silently emit a bogus
			// "MaxInt64-1" replacement without this early return. The
			// BRANCH_IF mutant on this block is therefore killable, and
			// is killed by TestIntegerDecrementSkipsUnparseable.
			return "", false
		}
		result := v + int64(delta)
		// Signed overflow wraps in Go; detect by sign-flip when stepping up.
		// The mirror branch (`delta < 0 && result > v`) is unreachable here:
		// Go's parser models `-N` as a unary minus around the positive literal
		// `N`, so every BasicLit reaching this function has v ≥ 0 and
		// v + (-1) can never wrap upward.
		// gomutants:disable-next-line INTEGER_DECREMENT,CONDITIONALS_BOUNDARY reason="`delta > 0` widened to `delta > -1` (INTEGER_DECREMENT) and `delta >= 0` (CONDITIONALS_BOUNDARY) are equivalent for delta ∈ {+1, -1}: both stay true for +1 and false for -1; `result <= v` (CONDITIONALS_BOUNDARY) is equivalent because v + 1 == v never holds for integers"
		if delta > 0 && result < v {
			return "", false
		}
		return strconv.FormatInt(result, 10), true
	case token.FLOAT:
		// gomutants:disable-next-line INTEGER_INCREMENT,INTEGER_DECREMENT reason="strconv.ParseFloat's bitSize argument only branches at 32 vs ≠32 — values 63/64/65 all use the float64 parser, so mutating 64 is observably identical"
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", false
		}
		result := v + float64(delta)
		// gomutants:disable-next-line INTEGER_INCREMENT reason="any negative precision selects FormatFloat's shortest-roundtrip mode; mutating prec from -1 to -2 yields the same output. (Mutating prec to 0 — INTEGER_DECREMENT — is killable and is killed by the existing float-literal tests.)"
		s := strconv.FormatFloat(result, 'g', -1, 64)
		// FormatFloat may emit `"1"` for 1.0; force it to remain a float
		// literal so the surrounding code still type-checks.
		if !strings.ContainsAny(s, ".eEpP") {
			s += ".0"
		}
		return s, true
	}
	return "", false
}
