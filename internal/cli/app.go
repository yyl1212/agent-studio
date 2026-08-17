package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const helpText = "doctor\ngenerate\nnode init\nnode test\nversion\n"

func Run(_ context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" {
		_, _ = io.WriteString(stdout, helpText)
		return 0
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintf(stdout, "agent-studio %s (%s)\n", agentnode.Version, agentnode.APIVersion)
		return 0
	case "doctor", "generate":
		_, _ = fmt.Fprintf(stderr, "%s is not implemented\n", args[0])
		return 1
	case "node":
		if len(args) < 2 {
			_, _ = io.WriteString(stderr, "node requires init or test\n")
			return 2
		}
		switch args[1] {
		case "init", "test":
			_, _ = fmt.Fprintf(stderr, "node %s is not implemented\n", args[1])
			return 1
		default:
			_, _ = fmt.Fprintf(stderr, "unknown node command %q\n", args[1])
			return 2
		}
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}
