package mutator

import (
	"go/ast"
	"go/token"
)

type invertAssignments struct{}

func (i *invertAssignments) Type() MutationType { return InvertAssignments }

// invertAssignmentsSwaps mirrors the arithmetic-base swaps but on compound
// assignment tokens. REM_ASSIGN → MUL_ASSIGN matches `%` → `*` from the
// non-assigning arithmetic mutator (see arithmetic.go for rationale on the
// asymmetry).
var invertAssignmentsSwaps = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.SUB_ASSIGN,
	token.SUB_ASSIGN: token.ADD_ASSIGN,
	token.MUL_ASSIGN: token.QUO_ASSIGN,
	token.QUO_ASSIGN: token.MUL_ASSIGN,
	token.REM_ASSIGN: token.MUL_ASSIGN,
}

func (i *invertAssignments) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		replacement, ok := invertAssignmentsSwaps[stmt.Tok]
		if !ok {
			return true
		}
		pos := fset.Position(stmt.TokPos)
		original := stmt.Tok.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        InvertAssignments,
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
