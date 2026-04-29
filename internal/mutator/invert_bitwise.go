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
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
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
