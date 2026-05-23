package mutator

import (
	"go/ast"
	"go/token"
)

type floatIncrement struct{}

func (m *floatIncrement) Type() MutationType { return FloatIncrement }

// Discover emits a mutant for every float literal that turns it into the
// value plus 1.0. Mutating the literal — not the surrounding expression —
// means a `-3.14` source span becomes `-4.14`, which moves the expression
// value the other way; that's accepted because the goal is to detect tests
// that don't depend on the precise numeric.
func (m *floatIncrement) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	return numericLiteralCandidates(fset, file, FloatIncrement, token.FLOAT, 1)
}
