package mutator

import (
	"go/ast"
	"go/token"
)

type floatDecrement struct{}

func (m *floatDecrement) Type() MutationType { return FloatDecrement }

// Discover emits a mutant for every float literal that turns it into the
// value minus 1.0. Output is always in decimal/`'g'` form, with `.0`
// appended when needed to keep the result a float literal.
func (m *floatDecrement) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	return numericLiteralCandidates(fset, file, FloatDecrement, token.FLOAT, -1)
}
