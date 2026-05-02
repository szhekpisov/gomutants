package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// Report is the gremlins-compatible JSON report structure.
//
// MutantsCached is gomutants-specific (additive, ignored by gremlins
// consumers). It counts mutants whose status was sourced from the
// incremental-analysis cache rather than this run's go-test invocations.
type Report struct {
	GoModule          string         `json:"go_module"`
	Files             []FileReport   `json:"files"`
	TestEfficacy      float64        `json:"test_efficacy"`
	MutationsCoverage float64        `json:"mutations_coverage"`
	MutantsTotal      int            `json:"mutants_total"`
	MutantsKilled     int            `json:"mutants_killed"`
	MutantsLived      int            `json:"mutants_lived"`
	MutantsNotViable  int            `json:"mutants_not_viable"`
	MutantsNotCovered int            `json:"mutants_not_covered"`
	MutantsCached     int            `json:"mutants_cached,omitempty"`
	ElapsedTime       float64        `json:"elapsed_time"`
	MutatorStatistics map[string]int `json:"mutator_statistics"`
}

// FileReport groups mutations by file.
type FileReport struct {
	FileName  string           `json:"file_name"`
	Mutations []MutationReport `json:"mutations"`
}

// MutationReport is a single mutation entry.
type MutationReport struct {
	Type        string `json:"type"`
	Status      string `json:"status"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Original    string `json:"original,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

// Generate builds a Report from the list of mutants.
func Generate(mutants []mutator.Mutant, goModule string, elapsed time.Duration) *Report {
	r := &Report{
		GoModule:          goModule,
		ElapsedTime:       elapsed.Seconds(),
		MutatorStatistics: make(map[string]int),
	}

	fileMap := make(map[string][]MutationReport)

	for _, m := range mutants {
		r.MutantsTotal++

		if m.FromCache {
			r.MutantsCached++
		}

		switch m.Status {
		case mutator.StatusKilled:
			r.MutantsKilled++
		case mutator.StatusLived:
			r.MutantsLived++
		case mutator.StatusNotViable:
			r.MutantsNotViable++
		case mutator.StatusNotCovered:
			r.MutantsNotCovered++
		}

		// Mutator statistics use lower_snake_case keys.
		statKey := strings.ToLower(string(m.Type))
		if m.Status != mutator.StatusNotCovered {
			r.MutatorStatistics[statKey]++
		}

		mr := MutationReport{
			Type:        string(m.Type),
			Status:      m.Status.String(),
			Line:        m.Line,
			Column:      m.Col,
			Original:    m.Original,
			Replacement: m.Replacement,
		}
		fileMap[m.RelFile] = append(fileMap[m.RelFile], mr)
	}

	// Build file reports in the order files appear.
	seen := make(map[string]bool)
	for _, m := range mutants {
		if seen[m.RelFile] {
			continue
		}
		seen[m.RelFile] = true
		r.Files = append(r.Files, FileReport{
			FileName:  m.RelFile,
			Mutations: fileMap[m.RelFile],
		})
	}

	// Compute efficacy and coverage.
	tested := r.MutantsKilled + r.MutantsLived
	if tested > 0 {
		r.TestEfficacy = float64(r.MutantsKilled) / float64(tested) * 100
	}
	if r.MutantsTotal > 0 {
		r.MutationsCoverage = float64(r.MutantsTotal-r.MutantsNotCovered) / float64(r.MutantsTotal) * 100
	}

	return r
}

// marshalJSON is swappable for testing.
var marshalJSON = json.Marshal

// WriteJSON writes the report to a file.
func WriteJSON(r *Report, path string) error {
	return writeJSONFile(path, r)
}

// writeJSONFile marshals v as JSON and writes it to path, creating parent
// directories as needed. Shared by WriteJSON and WriteStryker.
func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := marshalJSON(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
