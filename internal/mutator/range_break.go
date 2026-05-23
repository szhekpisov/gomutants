package mutator

import (
	"go/ast"
	"go/token"
)

type rangeBreak struct{}

func (m *rangeBreak) Type() MutationType { return RangeBreak }

// Discover emits a mutant for every `for ... range x { ... }` that inserts
// an unconditional `break` as the body's first statement. The result loops
// at most once instead of over every element, exposing tests that pass
// without iterating the full collection.
//
// The mutation is a zero-width insertion just after the body's opening
// brace, so Original is empty and Replacement carries the inserted source
// (with a trailing `;` so the next token isn't accidentally fused onto the
// `break` when the body is on one line).
//
// Bodies that already start with an unlabelled `break` are skipped: their
// post-patch source would be byte-identical to the pre-patch source.
func (m *rangeBreak) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	var out []MutantCandidate
	ast.Inspect(file, func(n ast.Node) bool {
		rng, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		if len(rng.Body.List) > 0 {
			if b, ok := rng.Body.List[0].(*ast.BranchStmt); ok && b.Tok == token.BREAK && b.Label == nil {
				return true
			}
		}
		// Insert immediately after the `{` so the break runs before any
		// existing first statement.
		insertOffset := fset.Position(rng.Body.Lbrace).Offset + 1
		pos := fset.Position(rng.Body.Lbrace)
		out = append(out, MutantCandidate{
			Type:        RangeBreak,
			Pos:         Position{Filename: pos.Filename, Line: pos.Line, Column: pos.Column, Offset: insertOffset},
			Original:    "",
			Replacement: " break;",
			StartOffset: insertOffset,
			EndOffset:   insertOffset,
		})
		return true
	})
	return out
}
