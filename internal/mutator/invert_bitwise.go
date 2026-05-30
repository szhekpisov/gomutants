package mutator

import (
	"go/ast"
	"go/token"
)

type invertBitwise struct{}

func (i *invertBitwise) Type() MutationType { return InvertBitwise }

// invertBitwiseSwaps covers the bitwise binary operators. XOR and AND_NOT
// fall back to AND because XOR ↔ OR / AND_NOT ↔ OR don't carry the same
// semantic flip, while → AND most reliably surfaces a wrong result.
var invertBitwiseSwaps = map[token.Token]token.Token{
	token.AND:     token.OR,
	token.OR:      token.AND,
	token.XOR:     token.AND,
	token.AND_NOT: token.AND,
	token.SHL:     token.SHR,
	token.SHR:     token.SHL,
}

func (i *invertBitwise) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	// A union operator in a generic type constraint (`~int | ~string`,
	// `int | string`) parses to the same *ast.BinaryExpr{Op: token.OR} as a
	// value-level bitwise OR — it is type syntax, not runtime logic, and
	// swapping it only ever yields uncompilable code. Collect the positions
	// of every such union first so the discovery walk below can skip them.
	// Constraints live in three places: interface type elements, and the
	// type-parameter list of a generic type or func declaration.
	constraintOps := constraintBinaryOps(file)

	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		if constraintOps[bin.OpPos] {
			return true
		}
		replacement, ok := invertBitwiseSwaps[bin.Op]
		if !ok {
			return true
		}
		pos := fset.Position(bin.OpPos)
		original := bin.Op.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        InvertBitwise,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    original,
			Replacement: repl,
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + len(original),
		})
		return true
	})
	return candidates
}

// constraintBinaryOps returns the set of OpPos for every binary operator that
// appears in a generic type-constraint position (an interface type element or
// a type-parameter constraint), so the bitwise mutator can leave them alone.
// In practice the only such operator is the union `|`, but any binary operator
// reached through a type-constraint expression is type syntax, not runtime
// logic — so the operator kind is irrelevant and recording all of them is both
// simpler and strictly safer than singling out token.OR.
func constraintBinaryOps(file *ast.File) map[token.Pos]bool {
	ops := make(map[token.Pos]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.InterfaceType:
			recordInterfaceOps(t, ops)
		case *ast.TypeSpec:
			recordTypeParamOps(t.TypeParams, ops)
		case *ast.FuncType:
			recordTypeParamOps(t.TypeParams, ops)
		}
		return true
	})
	return ops
}

// recordBinaryOps records the OpPos of every binary operator within expr.
func recordBinaryOps(expr ast.Expr, ops map[token.Pos]bool) {
	ast.Inspect(expr, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok {
			ops[b.OpPos] = true
		}
		return true
	})
}

// recordInterfaceOps records binary-operator positions in the embedded type
// elements of an interface (its constraint unions), skipping method
// signatures.
func recordInterfaceOps(it *ast.InterfaceType, ops map[token.Pos]bool) {
	for _, f := range it.Methods.List {
		// Empty Names ⇒ embedded type element (a constraint), not a method.
		if len(f.Names) == 0 {
			recordBinaryOps(f.Type, ops)
		}
	}
}

// recordTypeParamOps records binary-operator positions in a type-parameter
// constraint list (e.g. the `int | string` in `[T int | string]`).
func recordTypeParamOps(tp *ast.FieldList, ops map[token.Pos]bool) {
	if tp == nil {
		return
	}
	for _, f := range tp.List {
		recordBinaryOps(f.Type, ops)
	}
}
