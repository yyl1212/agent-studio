package nodepackage

import (
	"reflect"
	"testing"
)

func TestAddRegistrationCopiesAndSortsManifest(t *testing.T) {
	original := Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata: Metadata{
			Name: "example.com/nodes", DisplayName: "Example Nodes", Description: "",
			License: "Apache-2.0", Repository: "https://example.com/nodes",
		},
		Compatibility: Compatibility{
			NodeAPI: "agent-studio.dev/v1alpha1",
			Runtime: RuntimeRange{MinVersion: "v0.2.0", MaxVersionExclusive: "v0.4.0"},
		},
		Registrations: []Registration{{
			Package: "example.com/nodes/extensions/zeta",
			Nodes:   []NodeRef{{Type: "example.zeta", Version: "1.0.0"}},
		}},
	}
	before := cloneManifestFixture(original)

	updated, err := AddRegistration(original, Registration{
		Package: "example.com/nodes/extensions/echo",
		Nodes: []NodeRef{
			{Type: "example.search", Version: "1.0.0"},
			{Type: "example.echo", Version: "1.0.0"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("input mutated\nbefore=%+v\nafter=%+v", before, original)
	}
	if len(updated.Registrations) != 2 || updated.Registrations[0].Package != "example.com/nodes/extensions/echo" {
		t.Fatalf("registrations=%+v", updated.Registrations)
	}
	if got := updated.Registrations[0].Nodes; len(got) != 2 || got[0].Type != "example.echo" || got[1].Type != "example.search" {
		t.Fatalf("nodes=%+v", got)
	}

	updated.Registrations[1].Nodes[0].Type = "mutated"
	if original.Registrations[0].Nodes[0].Type != "example.zeta" {
		t.Fatalf("nested input aliased: %+v", original)
	}
}

func TestSortDiagnosticsCopiesAndUsesStableFields(t *testing.T) {
	input := []Diagnostic{
		{Severity: SeverityWarning, Code: "B", Package: "z", Message: "warning"},
		{Severity: SeverityError, Code: "B", Package: "a", Message: "second"},
		{Severity: SeverityError, Code: "A", Package: "z", Message: "first"},
	}
	before := append([]Diagnostic(nil), input...)

	sorted := SortDiagnostics(input)
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("input mutated: %+v", input)
	}
	if got := []string{sorted[0].Code, sorted[1].Package, string(sorted[2].Severity)}; !reflect.DeepEqual(got, []string{"A", "a", "warning"}) {
		t.Fatalf("order=%+v", sorted)
	}
	if !HasErrors(sorted) || HasErrors([]Diagnostic{{Severity: SeverityWarning}}) {
		t.Fatalf("HasErrors returned wrong result for %+v", sorted)
	}
}

func cloneManifestFixture(input Manifest) Manifest {
	output := input
	output.Registrations = append([]Registration(nil), input.Registrations...)
	for index := range output.Registrations {
		output.Registrations[index].Nodes = append([]NodeRef(nil), input.Registrations[index].Nodes...)
	}
	return output
}
