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
	markBinaryOps := func(expr ast.Expr) {
		ast.Inspect(expr, func(n ast.Node) bool {
			if b, ok := n.(*ast.BinaryExpr); ok {
				ops[b.OpPos] = true
			}
			return true
		})
	}
	markTypeParams := func(tp *ast.FieldList) {
		if tp == nil {
			return
		}
		for _, f := range tp.List {
			markBinaryOps(f.Type)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.InterfaceType:
			for _, f := range t.Methods.List {
				// Empty Names ⇒ embedded type element (a constraint), not a
				// method signature.
				if len(f.Names) == 0 {
					markBinaryOps(f.Type)
				}
			}
		case *ast.TypeSpec:
			markTypeParams(t.TypeParams)
		case *ast.FuncType:
			markTypeParams(t.TypeParams)
		}
		return true
	})
	return ops
}
