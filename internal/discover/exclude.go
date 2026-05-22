package discover

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Excluder matches module-relative file paths against a set of compiled
// regexps. A nil *Excluder matches nothing, so callers can treat "no
// patterns configured" and "all patterns missed" the same way without a
// nil check at every call site.
type Excluder struct {
	patterns []*regexp.Regexp
}

// NewExcluder compiles each non-empty spec into a regexp. Whitespace-only
// specs are skipped (splitAndTrim already drops them on the CLI path, but
// YAML lists can carry blanks). When no usable patterns remain it returns
// (nil, nil) so the caller gets a no-op Excluder rather than an empty one.
func NewExcluder(specs []string) (*Excluder, error) {
	patterns := make([]*regexp.Regexp, 0, len(specs))
	for _, s := range specs {
		// Skip blank entries (empty or whitespace-only) without trimming the
		// pattern itself, so a deliberate space inside a regexp survives.
		if strings.TrimSpace(s) == "" {
			continue
		}
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", s, err)
		}
		patterns = append(patterns, re)
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return &Excluder{patterns: patterns}, nil
}

// Match reports whether relPath matches any configured pattern. The match
// is unanchored: a pattern like `vendor/` hits anywhere in the path. A nil
// receiver never matches.
func (e *Excluder) Match(relPath string) bool {
	if e == nil {
		return false
	}
	for _, re := range e.patterns {
		if re.MatchString(relPath) {
			return true
		}
	}
	return false
}

// ApplyExcludes returns pkgs with every production file whose module-relative
// path (slash-separated, relative to moduleRoot) matches the Excluder
// dropped, along with the count of files removed. Test files are left
// untouched — they are never mutated. A nil Excluder matches nothing, so
// every file is kept and the count is 0.
//
// Excluded files are removed before discovery so they are never parsed,
// mutated, or pre-read. A package that exclusion empties of all its
// production files is dropped from the result so it neither inflates the
// reported package count nor wastes a downstream iteration; packages that
// were already production-empty (test-only, asm-only) are preserved
// unchanged.
func ApplyExcludes(pkgs []Package, e *Excluder, moduleRoot string) ([]Package, int) {
	excluded := 0
	out := make([]Package, 0, len(pkgs))
	for _, p := range pkgs {
		kept := make([]string, 0, len(p.GoFiles))
		for _, f := range p.GoFiles {
			rel := excludeRelPath(moduleRoot, p.Dir, f)
			if e.Match(rel) {
				excluded++
				continue
			}
			kept = append(kept, f)
		}
		// Drop only packages that exclusion emptied: they had production
		// files and have none left, so they contribute no mutants. Leave
		// already-empty packages as-is to preserve pre-exclude behavior.
		if len(p.GoFiles) > 0 && len(kept) == 0 {
			continue
		}
		p.GoFiles = kept
		out = append(out, p)
	}
	return out, excluded
}

// excludeRelPath computes the slash-separated module-relative path used for
// matching. On a Rel failure (file outside moduleRoot) it falls back to the
// absolute path so the pattern still gets a chance to match.
func excludeRelPath(moduleRoot, dir, file string) string {
	abs := filepath.Join(dir, file)
	rel, err := filepath.Rel(moduleRoot, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
