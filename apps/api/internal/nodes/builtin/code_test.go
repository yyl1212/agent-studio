package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"agentstudio.local/api/internal/domain"
)

func TestCodeExecutesMainAndLimitsSteps(t *testing.T) {
	node := NewCode(CodeOptions{MaxSteps: 1000, Timeout: time.Second, MaxOutputBytes: 1 << 20})
	okConfig := json.RawMessage(`{"source":"def main(input):\n  return {\"answer\": input[\"n\"] + 1}"}`)
	result, err := node.Execute(context.Background(), domain.NodeRequest{
		Config: okConfig,
		Inputs: map[string][]any{"input": {map[string]any{"n": 1.0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outputs["result"].(map[string]any)["answer"] != float64(2) {
		t.Fatalf("result=%v", result.Outputs)
	}

	loopConfig := json.RawMessage(`{"source":"def main(input):\n  for i in range(1000000):\n    pass\n  return None"}`)
	if _, err := node.Execute(context.Background(), domain.NodeRequest{Config: loopConfig}); !errors.Is(err, ErrCodeStepLimit) {
		t.Fatalf("step limit error=%v", err)
	}
}

func TestCodeRejectsSourceMainTimeoutAndLargeOutput(t *testing.T) {
	tests := []struct {
		name    string
		node    *codeNode
		source  string
		wantErr error
	}{
		{name: "source too large", node: NewCode(CodeOptions{}), source: strings.Repeat("x", 64<<10+1), wantErr: ErrCodeSourceTooLarge},
		{name: "main missing", node: NewCode(CodeOptions{}), source: "value = 1", wantErr: ErrCodeMainMissing},
		{name: "load disabled", node: NewCode(CodeOptions{}), source: "load(\"module.star\", \"value\")\ndef main(input):\n  return value", wantErr: ErrCodeExecution},
		{name: "timeout", node: NewCode(CodeOptions{MaxSteps: 1 << 62, Timeout: time.Millisecond}), source: "def main(input):\n  for i in range(1000000000):\n    pass", wantErr: ErrCodeTimeout},
		{name: "output too large", node: NewCode(CodeOptions{MaxOutputBytes: 16}), source: "def main(input):\n  return \"this output is much too large\"", wantErr: ErrCodeOutputTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := json.Marshal(map[string]string{"source": test.source})
			if err != nil {
				t.Fatal(err)
			}
			_, err = test.node.Execute(context.Background(), domain.NodeRequest{Config: config})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
		})
	}
}
