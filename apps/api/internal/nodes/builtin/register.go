package builtin

import (
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

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
	return registrar.Register(NewLLM(provider, defaultModel))
}

func RegisterIntegrationNodes(registrar agentnode.Registrar, httpOptions HTTPOptions) error {
	if err := registrar.Register(NewHTTP(httpOptions)); err != nil {
		return err
	}
	return registrar.Register(NewCode(CodeOptions{
		MaxSteps:       100000,
		Timeout:        2 * time.Second,
		MaxOutputBytes: 1 << 20,
	}))
}
