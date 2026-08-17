package builtin

import "agentstudio.local/api/internal/nodes"

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
