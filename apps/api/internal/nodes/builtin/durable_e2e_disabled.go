//go:build !durablee2e

package builtin

import (
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func durableE2ENodeRefs() []nodepackage.NodeRef { return nil }

func durableE2ENodes() []agentnode.Node { return nil }
