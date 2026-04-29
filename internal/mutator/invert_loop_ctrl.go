package mutator

import (
	"go/ast"
	"go/token"
)

type invertLoopCtrl struct{}

func (i *invertLoopCtrl) Type() MutationType { return InvertLoopCtrl }

var invertLoopCtrlSwaps = map[token.Token]token.Token{
	token.BREAK:    token.CONTINUE,
	token.CONTINUE: token.BREAK,
}

func (i *invertLoopCtrl) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var candidates []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		replacement, ok := invertLoopCtrlSwaps[stmt.Tok]
		if !ok {
			return true
		}
		// Skip labelled break/continue: swapping `break L` with `continue L`
		// can target a label that isn't a continuable construct, producing
		// a compile-error mutation rather than a behavioral one.
		if stmt.Label != nil {
			return true
		}
		pos := fset.Position(stmt.TokPos)
		original := stmt.Tok.String()
		repl := replacement.String()
		candidates = append(candidates, MutantCandidate{
			Type:        InvertLoopCtrl,
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
