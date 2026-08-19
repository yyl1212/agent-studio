package nodes

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestRegisterPackageCommitsOnlyExactNodeSet(t *testing.T) {
	registry := NewRegistry()
	record := testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"})
	err := registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		return registrar.Register(publicSDKNode{})
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := registry.PackageFor("example.public", "1.0.0")
	if !ok || summary.Name != "example.com/nodes" {
		t.Fatalf("summary=%+v ok=%t", summary, ok)
	}
	catalog := registry.Catalog()
	if len(catalog) != 1 || catalog[0].Definition.Type != "example.public" || catalog[0].Package.Name != "example.com/nodes" {
		t.Fatalf("catalog=%+v", catalog)
	}
}

func TestRegisterPackageFailuresAreAtomic(t *testing.T) {
	wantCallbackErr := errors.New("registration failed")
	for _, test := range []struct {
		name      string
		record    nodepackage.RuntimeRecord
		prepare   func(*Registry)
		register  func(agentnode.Registrar) error
		wantError error
	}{
		{
			name: "callback error", record: testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"}),
			register: func(registrar agentnode.Registrar) error {
				if err := registrar.Register(publicSDKNode{}); err != nil {
					return err
				}
				return wantCallbackErr
			}, wantError: wantCallbackErr,
		},
		{
			name: "missing node", record: testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"}),
			register: func(agentnode.Registrar) error { return nil }, wantError: ErrPackageNodeSetMismatch,
		},
		{
			name: "extra node", record: testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"}),
			register: func(registrar agentnode.Registrar) error {
				if err := registrar.Register(publicSDKNode{}); err != nil {
					return err
				}
				return registrar.Register(fakeNode{kind: "extra", version: "1"})
			}, wantError: ErrPackageNodeSetMismatch,
		},
		{
			name: "wrong version", record: testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "2.0.0"}),
			register:  func(registrar agentnode.Registrar) error { return registrar.Register(publicSDKNode{}) },
			wantError: ErrPackageNodeSetMismatch,
		},
		{
			name: "schema compilation", record: testRuntimeRecord(nodepackage.NodeRef{Type: "bad", Version: "1"}),
			register: func(registrar agentnode.Registrar) error {
				return registrar.Register(fakeNode{kind: "bad", schema: []byte(`{"type":`)})
			},
		},
		{
			name: "main registry conflict", record: testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"}),
			prepare: func(registry *Registry) {
				if err := registry.Register(publicSDKNode{}); err != nil {
					t.Fatal(err)
				}
			},
			register:  func(registrar agentnode.Registrar) error { return registrar.Register(publicSDKNode{}) },
			wantError: ErrPackageNodeDuplicate,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if test.prepare != nil {
				test.prepare(registry)
			}
			before := registry.Definitions()
			err := registry.RegisterPackage(test.record, test.register)
			if err == nil {
				t.Fatal("expected registration failure")
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v want=%v", err, test.wantError)
			}
			if after := registry.Definitions(); !reflect.DeepEqual(after, before) {
				t.Fatalf("registry changed\nbefore=%+v\nafter=%+v", before, after)
			}
			if len(registry.Catalog()) != 0 {
				t.Fatalf("package mappings changed: %+v", registry.Catalog())
			}
		})
	}
}

func TestPackageCatalogAndInputsAreIsolated(t *testing.T) {
	registry := NewRegistry()
	record := testRuntimeRecord(nodepackage.NodeRef{Type: "example.public", Version: "1.0.0"})
	if err := registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		return registrar.Register(publicSDKNode{})
	}); err != nil {
		t.Fatal(err)
	}
	record.Nodes[0].Type = "mutated"
	record.Summary.Name = "mutated"
	first := registry.Catalog()
	first[0].Definition.Outputs[0].Key = "mutated"
	first[0].Package.Name = "mutated"
	second := registry.Catalog()
	if second[0].Definition.Outputs[0].Key != "result" || second[0].Package.Name != "example.com/nodes" {
		t.Fatalf("catalog leaked mutable state: %+v", second)
	}

	if err := registry.Register(fakeNode{kind: "direct", version: "1"}); err != nil {
		t.Fatal(err)
	}
	if len(registry.Catalog()) != 1 {
		t.Fatalf("direct registration must not appear in package catalog: %+v", registry.Catalog())
	}
	if _, ok := registry.PackageFor("direct", "1"); ok {
		t.Fatal("direct registration unexpectedly has package metadata")
	}
}

func testRuntimeRecord(nodes ...nodepackage.NodeRef) nodepackage.RuntimeRecord {
	return nodepackage.RuntimeRecord{
		Summary: nodepackage.Summary{
			Name: "example.com/nodes", DisplayName: "Example Nodes", Source: nodepackage.SourceModule,
		},
		Nodes: nodes,
	}
}
