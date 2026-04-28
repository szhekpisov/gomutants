package mutator

import (
	"go/ast"
	"go/token"
)

type removeSelfAssignments struct{}

func (r *removeSelfAssignments) Type() MutationType { return RemoveSelfAssignments }

// removeSelfAssignmentsTargets is the set of compound assignment tokens
// (`x op= y`). The mutation drops the op so the statement becomes plain
// assignment (`x = y`), discarding the previous value of x.
var removeSelfAssignmentsTargets = map[token.Token]bool{
	token.ADD_ASSIGN:     true,
	token.SUB_ASSIGN:     true,
	token.MUL_ASSIGN:     true,
	token.QUO_ASSIGN:     true,
	token.REM_ASSIGN:     true,
	token.AND_ASSIGN:     true,
	token.OR_ASSIGN:      true,
	token.XOR_ASSIGN:     true,
	token.AND_NOT_ASSIGN: true,
	token.SHL_ASSIGN:     true,
	token.SHR_ASSIGN:     true,
}

func (r *removeSelfAssignments) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if !removeSelfAssignmentsTargets[stmt.Tok] {
			return true
		}
		pos := fset.Position(stmt.TokPos)
		original := stmt.Tok.String()
		candidates = append(candidates, MutantCandidate{
			Type:        RemoveSelfAssignments,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: pos.Offset},
			Original:    original,
			Replacement: "=",
			StartOffset: pos.Offset,
			EndOffset:   pos.Offset + len(original),
		})
		return true
	})
	return candidates
}
