package builtin

import (
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func RuntimeRecord(info buildinfo.Info) nodepackage.RuntimeRecord {
	coreNodes := []nodepackage.NodeRef{
		{Type: "start", Version: "1"}, {Type: "template", Version: "1"},
		{Type: "condition", Version: "1"}, {Type: "end", Version: "1"},
		{Type: "llm", Version: "1"}, {Type: "llm", Version: "2"},
		{Type: "http", Version: "1"}, {Type: "code", Version: "1"},
	}
	coreNodes = append(coreNodes, durableE2ENodeRefs()...)
	return nodepackage.RuntimeRecord{
		Summary: nodepackage.Summary{
			Name: "agent-studio.dev/core", DisplayName: "Agent Studio Core", Version: info.Version,
			License: "Apache-2.0", Repository: "https://github.com/yyl1212/agent-studio", Source: nodepackage.SourceBuiltin,
		},
		Nodes: coreNodes,
	}
}

func RegisterCore(registrar agentnode.Registrar) error {
	for _, node := range []agentnode.Node{
		NewStart(),
		NewTemplate(),
		NewCondition(),
		NewEnd(),
	} {
		if err := registrar.Register(node); err != nil {
			return err
		}
	}
	return nil
}

func RegisterLLM(registrar agentnode.Registrar, provider modelprovider.Provider, defaultModel string) error {
	for _, node := range []agentnode.Node{NewLLM(provider, defaultModel), NewLLMV2(provider, defaultModel)} {
		if err := registrar.Register(node); err != nil {
			return err
		}
	}
	return nil
}

func RegisterIntegrationNodes(registrar agentnode.Registrar, httpOptions HTTPOptions) error {
	if err := registrar.Register(NewHTTP(httpOptions)); err != nil {
		return err
	}
	if err := registrar.Register(NewCode(CodeOptions{
		MaxSteps:       100000,
		Timeout:        2 * time.Second,
		MaxOutputBytes: 1 << 20,
	})); err != nil {
		return err
	}
	for _, node := range durableE2ENodes() {
		if err := registrar.Register(node); err != nil {
			return err
		}
	}
	return nil
}
