package directives

// Add has a same-line directive scoped to ARITHMETIC_BASE only.
func Add(a, b int) int {
	return a + b // gomutants:disable ARITHMETIC_BASE reason="commutative"
}

// Sub has a next-line directive (no mutator list = wildcard).
func Sub(a, b int) int {
	// gomutants:disable-next-line
	return a - b
}

// Magic has a function-scope directive.
//
// gomutants:disable-func reason="generated"
func Magic(a, b int) int {
	return a + b
}

// Plain has no directive — its mutants run normally.
func Plain(a, b int) int {
	return a + b
}

// gomutants:disable-regexp ^\s*return\s+a\s*\*\s*b\b reason="ignore the multiply line"
func Mul(a, b int) int {
	return a * b
}
