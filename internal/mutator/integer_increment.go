package mutator

import (
	"go/ast"
	"go/token"
)

type integerIncrement struct{}

func (m *integerIncrement) Type() MutationType { return IntegerIncrement }

// Discover emits a mutant for every integer literal that turns it into
// the value plus one. Imaginary and float literals are ignored because
// the visitor filters on token.INT.
func (m *integerIncrement) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	return numericLiteralCandidates(fset, file, IntegerIncrement, token.INT, 1)
}
