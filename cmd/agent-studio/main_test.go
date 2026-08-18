package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/yyl1212/agent-studio/internal/cli"
)

func TestCLIIsAvailableToProductionConsumers(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
