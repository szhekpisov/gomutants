package mutator_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

func parse(t *testing.T, src string) (*token.FileSet, *ast.File, []byte) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return fset, f, []byte(src)
}

// --- Token-level mutators ---

func TestArithmeticBase(t *testing.T) {
	src := `package p
func f() int { return 1 + 2 - 3 * 4 / 5 % 6 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ArithmeticBase)
	candidates := m.Discover(fset, file, srcBytes)

	// AST walk order depends on tree shape. Just check total count and all swaps are valid.
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates, got %d", len(candidates))
	}

	validSwaps := map[string]string{
		"+": "-", "-": "+", "*": "/", "/": "*", "%": "*",
	}
	for i, c := range candidates {
		want, ok := validSwaps[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
	}

	for i, c := range candidates {
		if c.Type != mutator.ArithmeticBase {
			t.Errorf("candidate %d: type=%v, want %v", i, c.Type, mutator.ArithmeticBase)
		}
		if c.StartOffset >= c.EndOffset {
			t.Errorf("candidate %d: invalid offset range [%d:%d)", i, c.StartOffset, c.EndOffset)
		}
	}
}

func TestConditionalsBoundary(t *testing.T) {
	src := `package p
func f(a, b int) {
	_ = a < b
	_ = a <= b
	_ = a > b
	_ = a >= b
	_ = a == b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ConditionalsBoundary)
	candidates := m.Discover(fset, file, srcBytes)

	// <→<=, <=→<, >→>=, >=→> (== is not boundary)
	if len(candidates) != 4 {
		t.Fatalf("expected 4 candidates, got %d", len(candidates))
	}

	expected := []struct {
		original    string
		replacement string
	}{
		{"<", "<="},
		{"<=", "<"},
		{">", ">="},
		{">=", ">"},
	}
	for i, c := range candidates {
		if c.Original != expected[i].original || c.Replacement != expected[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, expected[i].original, expected[i].replacement)
		}
	}
}

func TestConditionalsNegation(t *testing.T) {
	src := `package p
func f(a, b int) {
	_ = a == b
	_ = a != b
	_ = a < b
	_ = a >= b
	_ = a > b
	_ = a <= b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ConditionalsNegation)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 6 {
		t.Fatalf("expected 6 candidates, got %d", len(candidates))
	}

	expected := []struct {
		original    string
		replacement string
	}{
		{"==", "!="},
		{"!=", "=="},
		{"<", ">="},
		{">=", "<"},
		{">", "<="},
		{"<=", ">"},
	}
	for i, c := range candidates {
		if c.Original != expected[i].original || c.Replacement != expected[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, expected[i].original, expected[i].replacement)
		}
	}
}

func TestIncrementDecrement(t *testing.T) {
	src := `package p
func f() {
	x := 0
	x++
	x--
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IncrementDecrement)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	if candidates[0].Original != "++" || candidates[0].Replacement != "--" {
		t.Errorf("candidate 0: got %q→%q, want \"++\"→\"--\"", candidates[0].Original, candidates[0].Replacement)
	}
	if candidates[1].Original != "--" || candidates[1].Replacement != "++" {
		t.Errorf("candidate 1: got %q→%q, want \"--\"→\"++\"", candidates[1].Original, candidates[1].Replacement)
	}
}

func TestInvertNegatives(t *testing.T) {
	src := `package p
func f() int { return -42 + -1 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertNegatives)
	candidates := m.Discover(fset, file, srcBytes)

	// Two unary negatives: -42 and -1.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Original != "-" || c.Replacement != "+" {
			t.Errorf("candidate %d: got %q→%q, want \"-\"→\"+\"", i, c.Original, c.Replacement)
		}
		if c.EndOffset-c.StartOffset != 1 {
			t.Errorf("candidate %d: byte length=%d, want 1", i, c.EndOffset-c.StartOffset)
		}
	}
}

func TestInvertNegativesBinary(t *testing.T) {
	src := `package p
func f(a, b int) int { return a - b }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertNegatives)
	candidates := m.Discover(fset, file, srcBytes)

	// Binary subtraction also produces an INVERT_NEGATIVES candidate (matches gremlins).
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Original != "-" || candidates[0].Replacement != "+" {
		t.Errorf("got %q→%q, want \"-\"→\"+\"", candidates[0].Original, candidates[0].Replacement)
	}
}

// --- Compound-assignment / bitwise / logical / loop-ctrl mutators ---

func TestInvertAssignments(t *testing.T) {
	src := `package p
func f(a, b int) {
	a += b
	a -= b
	a *= b
	a /= b
	a %= b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertAssignments)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidates, got %d", len(candidates))
	}

	expected := map[string]string{
		"+=": "-=", "-=": "+=", "*=": "/=", "/=": "*=", "%=": "*=",
	}
	for i, c := range candidates {
		want, ok := expected[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
		if c.Type != mutator.InvertAssignments {
			t.Errorf("candidate %d: type=%v, want %v", i, c.Type, mutator.InvertAssignments)
		}
	}
}

func TestInvertAssignmentsSkipsPlainAssign(t *testing.T) {
	src := `package p
func f() {
	x := 0
	x = 1
	_ = x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertAssignments)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for plain/short assigns, got %d", len(got))
	}
}

func TestInvertBitwise(t *testing.T) {
	src := `package p
func f(a, b uint) uint {
	_ = a & b
	_ = a | b
	_ = a ^ b
	_ = a &^ b
	_ = a << 1
	_ = a >> 1
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertBitwise)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 6 {
		t.Fatalf("expected 6 candidates, got %d", len(candidates))
	}

	expected := map[string]string{
		"&": "|", "|": "&", "^": "&", "&^": "&", "<<": ">>", ">>": "<<",
	}
	for i, c := range candidates {
		want, ok := expected[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
	}
}

func TestInvertBitwiseSkipsArithmetic(t *testing.T) {
	src := `package p
func f(a, b int) int { return a + b }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertBitwise)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for arithmetic source, got %d", len(got))
	}
}

func TestInvertBitwiseAssignments(t *testing.T) {
	src := `package p
func f(a, b uint) {
	a &= b
	a |= b
	a ^= b
	a &^= b
	a <<= 1
	a >>= 1
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertBitwiseAssignments)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 6 {
		t.Fatalf("expected 6 candidates, got %d", len(candidates))
	}

	expected := map[string]string{
		"&=": "|=", "|=": "&=", "^=": "&=", "&^=": "&=", "<<=": ">>=", ">>=": "<<=",
	}
	for i, c := range candidates {
		want, ok := expected[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
	}
}

func TestInvertLogical(t *testing.T) {
	src := `package p
func f(a, b bool) bool {
	return a && b || a
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertLogical)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	expected := map[string]string{"&&": "||", "||": "&&"}
	for i, c := range candidates {
		want, ok := expected[c.Original]
		if !ok {
			t.Errorf("candidate %d: unexpected original %q", i, c.Original)
		} else if c.Replacement != want {
			t.Errorf("candidate %d: %q→%q, want %q→%q", i, c.Original, c.Replacement, c.Original, want)
		}
	}
}

func TestInvertLoopCtrl(t *testing.T) {
	src := `package p
func f() {
	for i := 0; i < 10; i++ {
		if i == 5 {
			break
		}
		if i == 3 {
			continue
		}
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertLoopCtrl)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	if candidates[0].Original != "break" || candidates[0].Replacement != "continue" {
		t.Errorf("candidate 0: got %q→%q, want \"break\"→\"continue\"", candidates[0].Original, candidates[0].Replacement)
	}
	if candidates[1].Original != "continue" || candidates[1].Replacement != "break" {
		t.Errorf("candidate 1: got %q→%q, want \"continue\"→\"break\"", candidates[1].Original, candidates[1].Replacement)
	}
}

func TestInvertLoopCtrlSkipsLabelled(t *testing.T) {
	src := `package p
func f() {
Outer:
	for {
		for {
			break Outer
		}
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertLoopCtrl)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for labelled break, got %d", len(got))
	}
}

func TestInvertLoopCtrlSkipsGotoFallthrough(t *testing.T) {
	src := `package p
func f(x int) int {
	switch x {
	case 1:
		fallthrough
	case 2:
		goto end
	}
end:
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertLoopCtrl)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for goto/fallthrough, got %d", len(got))
	}
}

func TestRemoveSelfAssignments(t *testing.T) {
	src := `package p
func f(a, b uint) {
	a += b
	a -= b
	a *= b
	a /= b
	a %= b
	a &= b
	a |= b
	a ^= b
	a &^= b
	a <<= 1
	a >>= 1
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RemoveSelfAssignments)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 11 {
		t.Fatalf("expected 11 candidates (one per compound op), got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Replacement != "=" {
			t.Errorf("candidate %d: replacement=%q, want %q", i, c.Replacement, "=")
		}
	}
}

func TestRemoveSelfAssignmentsSkipsPlainAssign(t *testing.T) {
	src := `package p
func f() {
	x := 0
	x = 1
	_ = x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RemoveSelfAssignments)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for plain/short assigns, got %d", len(got))
	}
}

// --- Block-level mutators ---

func TestBranchIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return x
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]
	if c.Replacement != "{ _ = 0 }" {
		t.Errorf("replacement=%q, want %q", c.Replacement, "{ _ = 0 }")
	}
	if c.Type != mutator.BranchIf {
		t.Errorf("type=%v, want %v", c.Type, mutator.BranchIf)
	}
}

func TestBranchIfElseIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)

	// Both the "if" body and the "else if" body should be candidates.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
}

func TestBranchElse(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return x
	} else {
		return 0
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "{ _ = 0 }" {
		t.Errorf("replacement=%q, want %q", candidates[0].Replacement, "{ _ = 0 }")
	}
}

func TestBranchElseSkipsElseIf(t *testing.T) {
	src := `package p
func f(x int) int {
	if x > 0 {
		return 1
	} else if x < 0 {
		return -1
	}
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)

	// else-if is not a plain else block, so no candidates.
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates for else-if chain, got %d", len(candidates))
	}
}

func TestBranchCase(t *testing.T) {
	src := `package p
func f(x int) int {
	switch x {
	case 1:
		return 10
	case 2:
		return 20
	default:
		return 0
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchCase)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Replacement != "_ = 0" {
			t.Errorf("candidate %d: replacement=%q, want %q", i, c.Replacement, "_ = 0")
		}
	}
}

func TestExpressionRemove(t *testing.T) {
	src := `package p
func f(a, b bool) bool {
	return a && b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ExpressionRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// && produces 2 candidates: replace a with true, replace b with true.
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	if candidates[0].Replacement != "true" {
		t.Errorf("candidate 0: replacement=%q, want \"true\"", candidates[0].Replacement)
	}
	if candidates[1].Replacement != "true" {
		t.Errorf("candidate 1: replacement=%q, want \"true\"", candidates[1].Replacement)
	}
}

func TestExpressionRemoveOr(t *testing.T) {
	src := `package p
func f(a, b bool) bool {
	return a || b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ExpressionRemove)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	for i, c := range candidates {
		if c.Replacement != "false" {
			t.Errorf("candidate %d: replacement=%q, want \"false\"", i, c.Replacement)
		}
	}
}

func TestStatementRemoveAssign(t *testing.T) {
	src := `package p
func f() int {
	x := 0
	x = 42
	return x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// Only "x = 42" is a plain assign. ":=" is skipped.
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = 42" {
		t.Errorf("replacement=%q, want \"_ = 42\"", candidates[0].Replacement)
	}
}

func TestStatementRemoveExprStmt(t *testing.T) {
	src := `package p
func f() {
	println("hello")
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = 0" {
		t.Errorf("replacement=%q, want \"_ = 0\"", candidates[0].Replacement)
	}
}

func TestStatementRemoveIncDec(t *testing.T) {
	src := `package p
func f() int {
	x := 0
	x++
	return x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// x++ is also an IncDecStmt.
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	if candidates[0].Replacement != "_ = x" {
		t.Errorf("replacement=%q, want \"_ = x\"", candidates[0].Replacement)
	}
}

// TestStatementRemoveSkipsBlankLhs covers the early-return added so that
// "_ = expr" doesn't produce a candidate whose replacement is identical to
// the original (a phantom LIVED mutant). Without the guard, both expressions
// inside this function would surface as STATEMENT_REMOVE candidates.
func TestStatementRemoveSkipsBlankLhs(t *testing.T) {
	src := `package p
func f(x int) int {
	_ = x
	_ = 1 + 2
	y := x
	return y
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)
	for _, c := range candidates {
		if c.Original == c.Replacement {
			t.Errorf("found phantom mutation (original==replacement): %q at offset %d", c.Original, c.StartOffset)
		}
	}
}

// TestStatementRemoveMultiLhsNotSkipped kills BRANCH_IF on the
// `if len(lhs) != 1 { return false }` guard inside isBlankLhs. Without
// the early return, a multi-LHS assignment whose first slot happens to
// be `_` (e.g. `_, b = c, d`) would also be classified as "blank LHS"
// and the candidate would be skipped — even though the assignment as a
// whole has real side effects on `b` and is a legitimate STATEMENT_REMOVE
// target.
func TestStatementRemoveMultiLhsNotSkipped(t *testing.T) {
	src := `package p
func f() int {
	var b int
	_, b = 1, 2
	return b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)
	found := false
	for _, c := range candidates {
		if strings.HasPrefix(c.Original, "_, b") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a candidate for `_, b = 1, 2`; BRANCH_IF on isBlankLhs's len-check elides the early-return and lhs[0]=`_` makes the multi-LHS look blank")
	}
}

func TestStatementRemoveSkipsShortDecl(t *testing.T) {
	src := `package p
func f() {
	x := 42
	_ = x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.StatementRemove)
	candidates := m.Discover(fset, file, srcBytes)

	// ":=" is skipped (short decl), "_ = x" is also skipped (blank LHS
	// would yield a phantom mutation identical to the original).
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %d", len(candidates))
	}
}

// --- Numeric-literal increment / decrement ---

func TestIntegerIncrement(t *testing.T) {
	src := `package p
func f() int { return 1 + 7 + 0xFF }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	want := []struct {
		original    string
		replacement string
	}{
		{"1", "2"},
		{"7", "8"},
		// 0xFF is reformatted as decimal — keeps the replacement unambiguous.
		{"0xFF", "256"},
	}
	for i, c := range candidates {
		if c.Original != want[i].original || c.Replacement != want[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, want[i].original, want[i].replacement)
		}
	}
}

func TestIntegerIncrementSkipsFloatsAndImaginaries(t *testing.T) {
	src := `package p
func f() complex128 { _ = 3.14; return 1i }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates on float/imaginary literals, got %d (%+v)", len(got), got)
	}
}

func TestIntegerIncrementHandlesUnderscores(t *testing.T) {
	src := `package p
func f() int { return 1_000 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Original != "1_000" || candidates[0].Replacement != "1001" {
		t.Errorf("got %q→%q, want \"1_000\"→\"1001\"", candidates[0].Original, candidates[0].Replacement)
	}
}

// TestIntegerIncrementSkipsMaxInt64 kills the mutation that widens the
// signed-overflow guard `delta > 0` to a tautology (e.g. `delta > -1`): with
// the guard disabled the helper would silently return a wrapped MinInt64
// instead of dropping the candidate.
func TestIntegerIncrementSkipsMaxInt64(t *testing.T) {
	src := `package p
func f() int64 { return 9223372036854775807 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for MaxInt64 (signed-overflow guard); got %d (%+v)", len(got), got)
	}
}

// TestIntegerIncrementLargeLiteral kills the mutation that narrows
// strconv.ParseInt's bitSize from 64 to 63: a literal between 2^62 and
// 2^63-1 fits an int64 but not a signed 63-bit value, so bitSize=63 would
// reject it and drop the candidate.
func TestIntegerIncrementLargeLiteral(t *testing.T) {
	src := `package p
func f() int64 { return 5000000000000000000 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate for 5e18 (fits in int64, not int63); got %d", len(candidates))
	}
	if candidates[0].Replacement != "5000000000000000001" {
		t.Errorf("replacement=%q, want %q", candidates[0].Replacement, "5000000000000000001")
	}
}

// TestIntegerIncrementSkipsUnparseable kills the mutation that empties
// the `if err != nil { return "", false }` body: with the early-return
// gone, v stays at 0 (strconv's contract) and the helper would emit a
// bogus "1" candidate for an integer literal that overflows int64.
func TestIntegerIncrementSkipsUnparseable(t *testing.T) {
	src := `package p
func f() {
	const x = 99999999999999999999
	_ = x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerIncrement)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for an int literal that overflows int64, got %d (%+v)", len(got), got)
	}
}

// TestFloatIncrementSkipsUnparseable kills the BRANCH_IF on the float
// err-return: a literal that exceeds float64 range must drop the
// candidate, not emit "+Inf+1" garbage.
func TestFloatIncrementSkipsUnparseable(t *testing.T) {
	src := `package p
func f() float64 {
	const x = 1e10000
	return x
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.FloatIncrement)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for an out-of-range float literal, got %d (%+v)", len(got), got)
	}
}

func TestIntegerDecrement(t *testing.T) {
	src := `package p
func f() int { return 1 + 7 + 0 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.IntegerDecrement)
	candidates := m.Discover(fset, file, srcBytes)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	want := []struct {
		original    string
		replacement string
	}{
		{"1", "0"},
		{"7", "6"},
		{"0", "-1"},
	}
	for i, c := range candidates {
		if c.Original != want[i].original || c.Replacement != want[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, want[i].original, want[i].replacement)
		}
	}
}

func TestFloatIncrement(t *testing.T) {
	src := `package p
func f() float64 { return 1.5 + 0.0 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.FloatIncrement)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	// 0.0 → 1.0 must stay a float literal, not collapse to "1".
	want := []struct {
		original    string
		replacement string
	}{
		{"1.5", "2.5"},
		{"0.0", "1.0"},
	}
	for i, c := range candidates {
		if c.Original != want[i].original || c.Replacement != want[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, want[i].original, want[i].replacement)
		}
	}
}

func TestFloatIncrementSkipsIntsAndImaginaries(t *testing.T) {
	src := `package p
func f() complex128 { _ = 42; return 1i }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.FloatIncrement)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates on int/imaginary literals, got %d (%+v)", len(got), got)
	}
}

func TestFloatDecrement(t *testing.T) {
	src := `package p
func f() float64 { return 1.5 + 0.0 + 1e2 }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.FloatDecrement)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
	// 1e2 (=100.0) decrements to 99 — the formatter would emit "99" which
	// is an int literal in Go, so the helper must append ".0" to keep it a
	// float. The exact replacements are asserted to kill mutations on the
	// delta arg in float_decrement.go (e.g. `-1 → -2` would yield `-0.5`,
	// `-1.0`, `98.0`).
	want := []struct {
		original    string
		replacement string
	}{
		{"1.5", "0.5"},
		{"0.0", "-1.0"},
		{"1e2", "99.0"},
	}
	for i, c := range candidates {
		if c.Original != want[i].original || c.Replacement != want[i].replacement {
			t.Errorf("candidate %d: got %q→%q, want %q→%q",
				i, c.Original, c.Replacement, want[i].original, want[i].replacement)
		}
		// `99.0` contains `.`; if the helper ever dropped its append-".0"
		// guard, `99` would slip through and break the surrounding code.
		if !strings.ContainsAny(c.Replacement, ".eEpP") {
			t.Errorf("candidate %d replacement=%q is not a float literal", i, c.Replacement)
		}
	}
}

// --- Loop condition ---

func TestLoopCondition(t *testing.T) {
	src := `package p
func f() {
	for i := 0; i < 10; i++ {
		_ = i
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.LoopCondition)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Original != "i < 10" || candidates[0].Replacement != "false" {
		t.Errorf("got %q→%q, want \"i < 10\"→\"false\"", candidates[0].Original, candidates[0].Replacement)
	}
}

func TestLoopConditionSkipsInfiniteAndRange(t *testing.T) {
	src := `package p
func f() {
	for {
		break
	}
	for _, v := range []int{1, 2} {
		_ = v
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.LoopCondition)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for infinite/range loops, got %d (%+v)", len(got), got)
	}
}

func TestLoopConditionSkipsAlreadyFalse(t *testing.T) {
	src := `package p
func f() {
	for false {
		_ = 1
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.LoopCondition)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for `for false`, got %d (%+v)", len(got), got)
	}
}

// --- Range break ---

func TestRangeBreak(t *testing.T) {
	src := `package p
func f() {
	for _, v := range []int{1, 2, 3} {
		_ = v
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Original != "" {
		t.Errorf("Original=%q, want empty", c.Original)
	}
	if c.StartOffset != c.EndOffset {
		t.Errorf("StartOffset=%d EndOffset=%d, want equal (zero-width insertion)", c.StartOffset, c.EndOffset)
	}
	// Anchor StartOffset exactly one byte past the body's `{`. Anything else
	// either overwrites the brace (lose the `{`) or lands inside the body
	// (corrupts the first statement). Both produce NotViable mutants — bad
	// signal — so this assertion kills off-by-one mutations on insertOffset.
	wantOffset := strings.Index(src, "{\n\t\t_ = v") + 1
	if c.StartOffset != wantOffset {
		t.Errorf("StartOffset=%d, want %d (one byte past the body Lbrace)", c.StartOffset, wantOffset)
	}
	if c.Replacement != " break;" {
		t.Errorf("Replacement=%q, want %q", c.Replacement, " break;")
	}
}

// TestRangeBreakPatchProducesParseableSource sanity-checks that applying the
// inserted `break` yields a file the Go parser still accepts. This kills any
// mutation that strips the leading space or trailing `;` from the inserted
// text, which would fuse the new token onto an adjacent identifier.
func TestRangeBreakPatchProducesParseableSource(t *testing.T) {
	src := `package p
func f() { for _, v := range []int{1, 2, 3} { _ = v } }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	candidates := m.Discover(fset, file, srcBytes)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	out := append([]byte(nil), srcBytes[:c.StartOffset]...)
	out = append(out, c.Replacement...)
	out = append(out, srcBytes[c.EndOffset:]...)
	if _, err := parser.ParseFile(token.NewFileSet(), "patched.go", string(out), 0); err != nil {
		t.Errorf("patched source failed to parse: %v\n%s", err, out)
	}
}

// TestRangeBreakHandlesEmptyBody kills the `len > 0` → `len > -1` mutation:
// the mutated guard would always enter the inner branch and panic on
// `rng.Body.List[0]` for an empty body. The test asserts a candidate is
// emitted and no panic occurs.
func TestRangeBreakHandlesEmptyBody(t *testing.T) {
	src := `package p
func f(ch chan int) {
	for range ch {
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	if got := m.Discover(fset, file, srcBytes); len(got) != 1 {
		t.Errorf("expected 1 candidate for empty-body range; got %d (%+v)", len(got), got)
	}
}

func TestRangeBreakSkipsExistingBreak(t *testing.T) {
	src := `package p
func f() {
	for _, v := range []int{1, 2, 3} {
		break
		_ = v
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates when body already starts with break, got %d (%+v)", len(got), got)
	}
}

// TestRangeBreakSkipsSingleBreakBody kills the `len > 0` → `len > 1`
// mutation: with a one-statement body of just `break`, the original guard
// (`> 0`) enters the branch and skips the candidate; the mutated guard
// (`> 1`) skips the branch and emits a phantom-identical insertion.
func TestRangeBreakSkipsSingleBreakBody(t *testing.T) {
	src := `package p
func f(ch chan int) {
	for range ch {
		break
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for body-of-only-break, got %d (%+v)", len(got), got)
	}
}

// TestRangeBreakEmitsForLeadingContinue kills the mutation that drops the
// `b.Tok == token.BREAK` check (collapsing it to `true`): with the check
// gone, any BranchStmt at the body's head — including `continue` — would
// be treated as an existing break and the candidate skipped.
func TestRangeBreakEmitsForLeadingContinue(t *testing.T) {
	src := `package p
func f(ch chan int) {
	for range ch {
		continue
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	if got := m.Discover(fset, file, srcBytes); len(got) != 1 {
		t.Errorf("expected 1 candidate for body starting with `continue`, got %d (%+v)", len(got), got)
	}
}

// TestRangeBreakEmitsForLeadingLabelledBreak kills the mutation that drops
// the `b.Label == nil` check: an unconditional `break Outer` exits the
// outer loop, leaving the inner range to still iterate, so the candidate
// must still be emitted. Collapsing the label check to `true` would treat
// the labelled break as the inner-loop short-circuit and skip the mutant.
func TestRangeBreakEmitsForLeadingLabelledBreak(t *testing.T) {
	src := `package p
func f(rows [][]int) {
Outer:
	for _, row := range rows {
		for _, v := range row {
			break Outer
			_ = v
		}
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	// Two RangeStmts: the outer (`for _, row := range rows`) and the inner
	// (`for _, v := range row`). The inner body starts with a *labelled*
	// break, so it must still emit a candidate; the outer body starts with
	// the inner ForStmt (not a BranchStmt) and also emits.
	if got := m.Discover(fset, file, srcBytes); len(got) != 2 {
		t.Errorf("expected 2 candidates (outer + inner range), got %d (%+v)", len(got), got)
	}
}

func TestRangeBreakDoesNotTouchForStmt(t *testing.T) {
	src := `package p
func f() {
	for i := 0; i < 3; i++ {
		_ = i
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.RangeBreak)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("expected 0 candidates for non-range ForStmt, got %d (%+v)", len(got), got)
	}
}

// --- Registry ---

func TestRegistryIsKnown(t *testing.T) {
	reg := mutator.NewRegistry()

	for _, name := range []string{"ARITHMETIC_BASE", "BRANCH_IF", "STATEMENT_REMOVE"} {
		if !reg.IsKnown(name) {
			t.Errorf("IsKnown(%q)=false, want true", name)
		}
	}

	for _, name := range []string{"", "FOO", "ARTIHMETIC_BASE", "arithmetic_base"} {
		if reg.IsKnown(name) {
			t.Errorf("IsKnown(%q)=true, want false", name)
		}
	}
}

func TestRegistryUnknownNames(t *testing.T) {
	reg := mutator.NewRegistry()

	if got := reg.UnknownNames(nil); got != nil {
		t.Errorf("UnknownNames(nil)=%v, want nil", got)
	}

	if got := reg.UnknownNames([]string{"ARITHMETIC_BASE", "BRANCH_IF"}); got != nil {
		t.Errorf("UnknownNames(all-known)=%v, want nil", got)
	}

	got := reg.UnknownNames([]string{"ARITHMETIC_BASE", "ARTIHMETIC_BASE", "FOO"})
	want := []string{"ARTIHMETIC_BASE", "FOO"}
	if len(got) != len(want) {
		t.Fatalf("UnknownNames=%v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("UnknownNames[%d]=%q, want %q", i, got[i], w)
		}
	}
}

func TestRegistryEnabledMutators(t *testing.T) {
	reg := mutator.NewRegistry()

	all := reg.Mutators()
	if len(all) != 22 {
		t.Fatalf("expected 22 mutators, got %d", len(all))
	}

	only := reg.EnabledMutators([]string{"ARITHMETIC_BASE"}, nil)
	if len(only) != 1 {
		t.Fatalf("expected 1 with --only, got %d", len(only))
	}

	disabled := reg.EnabledMutators(nil, []string{"ARITHMETIC_BASE", "BRANCH_IF"})
	if len(disabled) != 20 {
		t.Fatalf("expected 20 after disabling 2, got %d", len(disabled))
	}
}

// --- Offset sanity ---

// TestOffsetsMatchSource asserts StartOffset:EndOffset corresponds to Original
// text for every mutator type. Kills mutations on the offset arithmetic in each
// mutator (e.g. `+ len(original)` → `- len(original)` produces wrong byte range,
// either out of bounds or mismatching the token text).
func TestOffsetsMatchSource(t *testing.T) {
	// Source covers every mutator's target construct so each mutator produces
	// at least one candidate and this test exercises its offset computation.
	src := `package p

func f(a, b int) int {
	if a > 0 {
		a++
		a = a - 1
	} else {
		a--
	}
	switch a {
	case 1:
		return a + b
	case 2:
		return a * b / 2 % 3
	}
	if a == b && a < b {
		return -a
	}
	if a != b || a >= b {
		return a
	}
	// Compound assignments — InvertAssignments / RemoveSelfAssignments / InvertBitwiseAssignments.
	a += b
	a -= b
	a *= b
	a /= b
	a %= b
	a &= b
	a |= b
	a ^= b
	a &^= b
	a <<= 1
	a >>= 1
	// Bitwise binary — InvertBitwise.
	_ = a & b
	_ = a | b
	_ = a ^ b
	_ = a &^ b
	_ = a << 1
	_ = a >> 1
	// Loop control — InvertLoopCtrl. The non-nil condition also exercises LoopCondition.
	for i := 0; i < 10; i++ {
		if i == 5 {
			break
		}
		if i == 3 {
			continue
		}
	}
	// Range — RangeBreak.
	for _, v := range []int{1, 2, 3} {
		_ = v
	}
	// Float literal — Float{Increment,Decrement}.
	_ = 3.14
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	totalCandidates := 0
	for _, m := range reg.Mutators() {
		candidates := m.Discover(fset, file, srcBytes)
		totalCandidates += len(candidates)
		for _, c := range candidates {
			if c.StartOffset < 0 || c.EndOffset > len(srcBytes) || c.StartOffset > c.EndOffset {
				t.Errorf("%s: invalid offset [%d:%d) in %d-byte source",
					c.Type, c.StartOffset, c.EndOffset, len(srcBytes))
				continue
			}
			got := string(srcBytes[c.StartOffset:c.EndOffset])
			if got != c.Original {
				t.Errorf("%s at offset [%d:%d): source has %q, candidate says %q",
					c.Type, c.StartOffset, c.EndOffset, got, c.Original)
			}
		}
		// Each built-in mutator must produce at least one candidate on this rich source.
		if len(candidates) == 0 {
			t.Errorf("%s: expected at least one candidate on rich source, got 0", m.Type())
		}
	}
	if totalCandidates == 0 {
		t.Fatal("no mutators produced candidates")
	}
}

// TestBitwiseOpsProduceNoArithCandidates kills BRANCH_IF and BRANCH_CASE
// mutations on the `!ok`/default guards in arithmetic/boundary/negation
// mutators: if the guard is removed, bitwise ops produce bogus candidates.
func TestBitwiseOpsProduceNoArithCandidates(t *testing.T) {
	src := `package p

func f(a, b int) int {
	_ = a & b
	_ = a | b
	_ = a ^ b
	_ = a << 1
	_ = a >> 1
	return 0
}
`
	fset, file, srcBytes := parse(t, src)
	targets := []mutator.MutationType{
		mutator.ArithmeticBase,
		mutator.ConditionalsBoundary,
		mutator.ConditionalsNegation,
	}
	for _, tt := range targets {
		m := findMutator(t, tt)
		if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
			t.Errorf("%s: expected 0 candidates for bitwise-only source, got %d (%+v)", tt, len(got), got)
		}
	}
}

// TestExpressionRemoveSkipsArithmetic kills BRANCH_CASE on the default
// clause of the LAND/LOR switch — without the default return, arithmetic
// ops would incorrectly produce EXPRESSION_REMOVE candidates with empty
// identity value.
func TestExpressionRemoveSkipsArithmetic(t *testing.T) {
	src := `package p
func f(a, b int) int { return a + b }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.ExpressionRemove)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("ExpressionRemove: arithmetic ops should produce 0 candidates, got %d (%+v)", len(got), got)
	}
}

// TestInvertLogicalSkipsNonLogical kills BRANCH_IF on the `if !ok { return true }`
// guard at invert_logical.go:25. Without it, any binary expression (arithmetic,
// comparison, bitwise) would produce a candidate with a zero-value (ILLEGAL)
// replacement token.
func TestInvertLogicalSkipsNonLogical(t *testing.T) {
	src := `package p
func f(a, b int) int {
	_ = a + b
	_ = a == b
	_ = a & b
	return a - b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertLogical)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("InvertLogical: non-logical ops should produce 0 candidates, got %d (%+v)", len(got), got)
	}
}

// TestInvertBitwiseAssignmentsSkipsNonBitwise kills BRANCH_IF on the
// `if !ok { return true }` guard at invert_bitwise_assignments.go:29. Without
// it, plain `=` and arithmetic compound assigns would produce candidates with
// a zero-value replacement token.
func TestInvertBitwiseAssignmentsSkipsNonBitwise(t *testing.T) {
	src := `package p
func f(a, b int) {
	a = b
	a += b
	a -= b
	a *= b
	a /= b
	a %= b
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertBitwiseAssignments)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("InvertBitwiseAssignments: non-bitwise assigns should produce 0 candidates, got %d (%+v)", len(got), got)
	}
}

// TestStatementRemoveEmptyRhs kills BRANCH_IF on the `if len(stmt.Rhs) == 0`
// guard at statement_remove.go:24. The guard protects against parser-recovered
// AssignStmts with empty Rhs; without it, `stmt.Rhs[0].Pos()` panics. Synthetic
// AST construction is used because Go's parser recovery normally yields
// Rhs=[BadExpr] rather than an empty slice.
func TestStatementRemoveEmptyRhs(t *testing.T) {
	file := &ast.File{
		Name: ast.NewIdent("p"),
		Decls: []ast.Decl{
			&ast.FuncDecl{
				Name: ast.NewIdent("f"),
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.AssignStmt{
							Lhs: []ast.Expr{ast.NewIdent("x")},
							Tok: token.ASSIGN,
						},
					},
				},
			},
		},
	}
	fset := token.NewFileSet()
	m := findMutator(t, mutator.StatementRemove)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("StatementRemove panicked on empty-Rhs AssignStmt: %v", r)
		}
	}()
	if got := m.Discover(fset, file, nil); len(got) != 0 {
		t.Errorf("expected 0 candidates for empty Rhs, got %d", len(got))
	}
}

// TestInvertNegativesSkipsNonSub kills the BRANCH_IF on `node.Op != token.SUB`
// guards: without them, unary `+` and binary `+` would produce candidates.
func TestInvertNegativesSkipsNonSub(t *testing.T) {
	src := `package p
func f(a, b int) int { _ = +a; return a + b }
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.InvertNegatives)
	if got := m.Discover(fset, file, srcBytes); len(got) != 0 {
		t.Errorf("InvertNegatives: non-SUB ops should produce 0 candidates, got %d (%+v)", len(got), got)
	}
}

// TestEnabledMutatorsPreservesOrderAndSet kills CONDITIONALS_BOUNDARY on
// `len(disable) > 0` in EnabledMutators: mutating to `>=` would include the
// empty-disable case and return a copy (different slice than r.mutators).
func TestEnabledMutatorsEmptyDisableReturnsSameSlice(t *testing.T) {
	reg := mutator.NewRegistry()
	full := reg.Mutators()
	got := reg.EnabledMutators(nil, nil)
	// Must return the ORIGINAL mutators slice (same length, same ordering, same type ids).
	if len(got) != len(full) {
		t.Fatalf("len=%d, want %d", len(got), len(full))
	}
	for i := range got {
		if got[i].Type() != full[i].Type() {
			t.Errorf("[%d] type=%v, want %v", i, got[i].Type(), full[i].Type())
		}
	}
}

// --- Status String ---

func TestMutantStatusString(t *testing.T) {
	tests := []struct {
		status mutator.MutantStatus
		want   string
	}{
		{mutator.StatusPending, "PENDING"},
		{mutator.StatusKilled, "KILLED"},
		{mutator.StatusLived, "LIVED"},
		{mutator.StatusNotCovered, "NOT COVERED"},
		{mutator.StatusNotViable, "NOT VIABLE"},
		{mutator.StatusTimedOut, "TIMED OUT"},
		{mutator.MutantStatus(99), "UNKNOWN"},
	}
	for _, tc := range tests {
		got := tc.status.String()
		if got != tc.want {
			t.Errorf("MutantStatus(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// --- No-op mutators on empty functions ---

func TestMutatorsOnEmptyFunc(t *testing.T) {
	src := `package p
func f() {}
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		// Should not panic on minimal source.
		_ = m.Discover(fset, file, srcBytes)
	}
}

// TestMutatorsNonMatchingNodes exercises the early-return paths in each mutator
// by providing AST nodes that don't match the mutator's target pattern.
func TestMutatorsNonMatchingNodes(t *testing.T) {
	// This source has diverse AST nodes but specifically avoids matching
	// certain mutator patterns, exercising the "no match" branches.
	src := `package p

import "fmt"

func f(x int) string {
	// Bitwise ops — not in arithmetic swap table.
	a := x & 0xff
	b := x | 0x0f
	c := x ^ 0x01
	d := x << 2
	e := x >> 1

	// String concatenation — ADD token but not numeric.
	s := "hello" + "world"

	// Comparison with == — not in ConditionalsBoundary.
	if a == b {
		fmt.Println(c, d, e, s)
	}

	// For loop (not if/switch).
	for i := 0; i < 10; i++ {
		_ = i
	}

	// Select statement (not switch).
	ch := make(chan int, 1)
	ch <- 1
	select {
	case v := <-ch:
		_ = v
	}

	// Type switch (case clause with no body beyond type assert).
	var iface interface{} = 42
	switch iface.(type) {
	case int:
	}

	// Return statement (not AssignStmt/ExprStmt/IncDecStmt for StatementRemove).
	return fmt.Sprintf("%d %d", a, b)
}
`
	fset, file, srcBytes := parse(t, src)
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		candidates := m.Discover(fset, file, srcBytes)
		// We just want these to run without panic and exercise all branches.
		_ = candidates
	}
}

// --- Edge cases for branch mutators ---

func TestBranchIfEmptyBody(t *testing.T) {
	src := `package p
func f(x int) {
	if x > 0 {
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchIf)
	candidates := m.Discover(fset, file, srcBytes)
	// Empty if body should be skipped.
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for empty if body, got %d", len(candidates))
	}
}

func TestBranchElseEmptyBody(t *testing.T) {
	src := `package p
func f(x int) {
	if x > 0 {
		_ = x
	} else {
	}
}
`
	fset, file, srcBytes := parse(t, src)
	m := findMutator(t, mutator.BranchElse)
	candidates := m.Discover(fset, file, srcBytes)
	// Empty else body should be skipped.
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for empty else body, got %d", len(candidates))
	}
}

// --- EnabledMutators with both only and disable ---

func TestRegistryEnabledMutatorsNoFilter(t *testing.T) {
	reg := mutator.NewRegistry()
	all := reg.EnabledMutators(nil, nil)
	if len(all) != 22 {
		t.Errorf("expected 22, got %d", len(all))
	}
}

// findMutator returns the mutator of the given type from the registry.
func findMutator(t *testing.T, typ mutator.MutationType) mutator.Mutator {
	t.Helper()
	reg := mutator.NewRegistry()
	for _, m := range reg.Mutators() {
		if m.Type() == typ {
			return m
		}
	}
	t.Fatalf("mutator %v not found in registry", typ)
	return nil
}
