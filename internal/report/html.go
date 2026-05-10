package report

import (
	"bytes"
	_ "embed"
	"text/template"

	"github.com/szhekpisov/gomutants/internal/mutator"
)

// htmlBundleJS is the vendored mutation-testing-elements UMD bundle. Embedded
// at build time so the produced HTML loads with no network access — see
// assets/README.md for the pinned version and refresh procedure.
//
//go:embed assets/mutation-test-elements.js
var htmlBundleJS string

//go:embed assets/template.html
var htmlTemplateSrc string

// text/template, not html/template: html/template would escape the embedded
// JS bundle and the JSON payload as HTML entities, breaking the page. The
// payloads are placed inside <script> blocks where HTML entity escaping does
// not apply, and json.Marshal already escapes `<`, `>`, `&` to `\uXXXX`,
// which keeps `</script>` substrings inside report data safe.
var htmlTmpl = template.Must(template.New("report").Parse(htmlTemplateSrc))

// WriteHTML writes a self-contained interactive HTML mutation report at path.
// The output bundles the Stryker mutation-testing-elements web component and
// the Stryker v2 report JSON into a single file with no external assets — it
// renders in any browser, including offline (air-gapped CI artifacts).
//
// frameworkVersion is recorded in the embedded report; pass the running
// gomutants version.
func WriteHTML(path string, mutants []mutator.Mutant, projectDir, frameworkVersion string) error {
	rep, err := buildStrykerReport(mutants, projectDir, frameworkVersion)
	if err != nil {
		return err
	}
	data, err := marshalJSON(rep)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := htmlTmpl.Execute(&buf, struct {
		Bundle string
		Report string
	}{
		Bundle: htmlBundleJS,
		Report: string(data),
	}); err != nil {
		return err
	}

	return writeFile(path, buf.Bytes())
}
