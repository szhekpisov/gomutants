package mutator

import (
	"go/ast"
	"go/token"
)

type invertBitwiseAssignments struct{}

func (i *invertBitwiseAssignments) Type() MutationType { return InvertBitwiseAssignments }

var invertBitwiseAssignSwaps = map[token.Token]token.Token{
	token.AND_ASSIGN:     token.OR_ASSIGN,
	token.OR_ASSIGN:      token.AND_ASSIGN,
	token.XOR_ASSIGN:     token.AND_ASSIGN,
	token.AND_NOT_ASSIGN: token.AND_ASSIGN,
	token.SHL_ASSIGN:     token.SHR_ASSIGN,
	token.SHR_ASSIGN:     token.SHL_ASSIGN,
}

func (i *invertBitwiseAssignments) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		replacement, ok := invertBitwiseAssignSwaps[stmt.Tok]
		if !ok {
			return true
		}
		pos := fset.Position(stmt.TokPos)
		original := stmt.Tok.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        InvertBitwiseAssignments,
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
