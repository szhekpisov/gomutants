package mutator

import (
	"go/ast"
	"go/token"
)

// Mutator discovers mutation candidates in a parsed Go source file.
type Mutator interface {
	Type() MutationType
	Discover(fset *token.FileSet, file *ast.File, src []byte) []MutantCandidate
}

// Registry holds all registered mutators.
type Registry struct {
	mutators []Mutator
}

// NewRegistry creates a registry with all built-in mutators.
func NewRegistry() *Registry {
	return &Registry{
		mutators: []Mutator{
			&arithmeticBase{},
			&conditionalsBoundary{},
			&conditionalsNegation{},
			&incrementDecrement{},
			&invertNegatives{},
			&invertAssignments{},
			&invertBitwise{},
			&invertBitwiseAssignments{},
			&invertLogical{},
			&invertLoopCtrl{},
			&removeSelfAssignments{},
			&branchIf{},
			&branchElse{},
			&branchCase{},
			&expressionRemove{},
			&statementRemove{},
		},
	}
}

// Mutators returns all registered mutators.
func (r *Registry) Mutators() []Mutator {
	return r.mutators
}

// UnknownNames returns the subset of names that don't match any
// registered mutator type. Used by callers that accept user-supplied
// mutator lists (--only / --disable, config file) to surface typos
// before silently filtering them out.
func (r *Registry) UnknownNames(names []string) []string {
	known := make(map[string]struct{}, len(r.mutators))
	for _, m := range r.mutators {
		known[string(m.Type())] = struct{}{}
	}
	var unknown []string
	for _, n := range names {
		if _, ok := known[n]; !ok {
			unknown = append(unknown, n)
		}
	}
	return unknown
}

// EnabledMutators returns mutators filtered by the given only/disable lists.
// If only is non-empty, only those types are included.
// Otherwise, disabled types are excluded.
func (r *Registry) EnabledMutators(only, disable []string) []Mutator {
	if len(only) > 0 {
		set := make(map[string]bool, len(only))
		for _, t := range only {
			set[t] = true
		}
		var out []Mutator
		for _, m := range r.mutators {
			if set[string(m.Type())] {
				out = append(out, m)
			}
		}
		return out
	}

	set := make(map[string]bool, len(disable))
	for _, t := range disable {
		set[t] = true
	}
	var out []Mutator
	for _, m := range r.mutators {
		if !set[string(m.Type())] {
			out = append(out, m)
		}
	}
	return out
}
