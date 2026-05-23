package mutator

import (
	"go/ast"
	"go/token"
)

type loopCondition struct{}

func (m *loopCondition) Type() MutationType { return LoopCondition }

// Discover emits a mutant for every `for ...; cond; ... { }` that replaces
// the condition with the literal `false`. Three constructs are deliberately
// skipped:
//   - infinite loops (`for { }`) — they have no Cond to mutate;
//   - `for ... range x { }` — handled separately by RangeBreak;
//   - conditions whose source text is already `false`, which would yield a
//     byte-identical patch (a phantom LIVED mutant no test can kill).
func (m *loopCondition) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var out []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.ForStmt)
		if !ok || forStmt.Cond == nil {
			return true
		}
		startOffset := fset.Position(forStmt.Cond.Pos()).Offset
		endOffset := fset.Position(forStmt.Cond.End()).Offset
		original := string(src[startOffset:endOffset])
		if original == "false" {
			return true
		}
		pos := fset.Position(forStmt.Cond.Pos())
		out = append(out, MutantCandidate{
			Type:        LoopCondition,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: startOffset},
			Original:    original,
			Replacement: "false",
			StartOffset: startOffset,
			EndOffset:   endOffset,
		})
		return true
	})
	return out
}
