package mutator

import (
	"go/ast"
	"go/token"
)

type integerDecrement struct{}

func (m *integerDecrement) Type() MutationType { return IntegerDecrement }

// Discover emits a mutant for every integer literal that turns it into
// the value minus one. The negative-literal case (-1) is unaffected: the
// parser models that as a unary expression around the positive literal 1,
// so this mutator sees `1` and yields `0`, leaving the unary `-` in place.
func (m *integerDecrement) Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate {
	return numericLiteralCandidates(fset, file, IntegerDecrement, token.INT, -1)
}
