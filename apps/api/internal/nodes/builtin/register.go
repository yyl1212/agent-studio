package builtin

import (
	"agentstudio.local/api/internal/modelprovider"
	"agentstudio.local/api/internal/nodes"
)

func RegisterCore(registry *nodes.Registry) error {
	for _, node := range []nodes.NodeType{
		NewStart(),
		NewTemplate(),
		NewCondition(),
		NewEnd(),
	} {
		if err := registry.Register(node); err != nil {
			return err
		}
	}
	return nil
}

func RegisterLLM(registry *nodes.Registry, provider modelprovider.Provider, defaultModel string) error {
	return registry.Register(NewLLM(provider, defaultModel))
}
