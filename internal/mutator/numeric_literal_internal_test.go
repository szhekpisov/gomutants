package mutator

import (
	"go/token"
	"testing"
)

// TestMutateNumericLiteralUnsupportedKind exercises the default-fall-through
// at the bottom of mutateNumericLiteral. It exists purely to cover that
// otherwise-unreachable branch — every caller in this package passes
// token.INT or token.FLOAT — and to keep the package at 100% coverage.
func TestMutateNumericLiteralUnsupportedKind(t *testing.T) {
	if got, ok := mutateNumericLiteral("anything", token.STRING, 1); ok || got != "" {
		t.Errorf("mutateNumericLiteral(STRING) = (%q, %v), want (\"\", false)", got, ok)
	}
}
