package generated

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"github.com/yyl1212/agent-studio/sdk/go/nodepackage"
)

type recordingRegistrar struct {
	types    []string
	safeties map[string]agentnode.ExecutionSafety
}

func (registrar *recordingRegistrar) Register(node agentnode.Node) error {
	definition := node.Definition()
	key := definition.Type + "@" + definition.Version
	registrar.types = append(registrar.types, key)
	if registrar.safeties == nil {
		registrar.safeties = make(map[string]agentnode.ExecutionSafety)
	}
	registrar.safeties[key] = definition.ExecutionSafety
	return nil
}

func (registrar *recordingRegistrar) RegisterPackage(_ nodepackage.RuntimeRecord, register func(agentnode.Registrar) error) error {
	return register(registrar)
}

func TestRegisterNodesUsesGeneratedOrder(t *testing.T) {
	registrar := &recordingRegistrar{}
	if err := RegisterNodes(registrar); err != nil {
		t.Fatal(err)
	}
	want := []string{"extension.echo@1.0.0", "extension.retriever@1.0.0", "extension.webhook@1.0.0"}
	if !reflect.DeepEqual(registrar.types, want) {
		t.Fatalf("types=%v want=%v", registrar.types, want)
	}
}

func TestRegisterNodesDeclaresExactExecutionSafety(t *testing.T) {
	registrar := &recordingRegistrar{}
	if err := RegisterNodes(registrar); err != nil {
		t.Fatal(err)
	}
	want := map[string]agentnode.ExecutionSafety{
		"extension.echo@1.0.0":      agentnode.ExecutionSafetyPure,
		"extension.retriever@1.0.0": agentnode.ExecutionSafetyPure,
		"extension.webhook@1.0.0":   agentnode.ExecutionSafetySideEffect,
	}
	if !reflect.DeepEqual(registrar.safeties, want) {
		t.Fatalf("execution safeties=%v, want %v", registrar.safeties, want)
	}
}

func TestRegisterNodesAddsOfficialExtensions(t *testing.T) {
	registry := nodes.NewRegistry()
	if err := RegisterNodes(registry); err != nil {
		t.Fatal(err)
	}
	echo, err := registry.Get("extension.echo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	result, err := echo.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"prefix":"回答："}`),
		Inputs: map[string][]any{"text": {"你好"}},
	})
	if err != nil || result.Outputs["text"] != "回答：你好" {
		t.Fatalf("result=%v err=%v", result, err)
	}

	retriever, err := registry.Get("extension.retriever", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	retrieverPorts, err := retriever.Resolve(json.RawMessage(`{"documents":[{"id":"doc-1","text":"hello world"}],"topK":1}`))
	if err != nil || len(retrieverPorts.Inputs) != 1 || len(retrieverPorts.Outputs) != 1 {
		t.Fatalf("retriever ports=%+v err=%v", retrieverPorts, err)
	}

	webhook, err := registry.Get("extension.webhook", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	webhookPorts, err := webhook.Resolve(json.RawMessage(`{"path":"hooks/run"}`))
	if err != nil || len(webhookPorts.Inputs) != 1 || len(webhookPorts.Outputs) != 2 {
		t.Fatalf("webhook ports=%+v err=%v", webhookPorts, err)
	}
	wantCapabilities := []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}
	if got := webhook.Definition().Capabilities; !reflect.DeepEqual(got, wantCapabilities) {
		t.Fatalf("webhook capabilities=%v want=%v", got, wantCapabilities)
	}
}
