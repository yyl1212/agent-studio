package nodepackage

import "sort"

type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Package  string   `json:"package,omitempty"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
}

func SortDiagnostics(input []Diagnostic) []Diagnostic {
	output := append([]Diagnostic(nil), input...)
	sort.Slice(output, func(left, right int) bool {
		if output[left].Severity != output[right].Severity {
			return output[left].Severity < output[right].Severity
		}
		if output[left].Code != output[right].Code {
			return output[left].Code < output[right].Code
		}
		if output[left].Package != output[right].Package {
			return output[left].Package < output[right].Package
		}
		if output[left].Path != output[right].Path {
			return output[left].Path < output[right].Path
		}
		return output[left].Message < output[right].Message
	})
	if output == nil {
		return []Diagnostic{}
	}
	return output
}

func HasErrors(input []Diagnostic) bool {
	for _, diagnostic := range input {
		if diagnostic.Severity == SeverityError {
			return true
		}
	}
	return false
}
