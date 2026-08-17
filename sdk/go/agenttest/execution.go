package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const defaultMaxOutputBytes = 1 << 20

type ExecutionCase struct {
	Name          string
	Request       agentnode.Request
	WantOutputs   map[string]any
	WantErrorKind *agentnode.ErrorKind
}

func validateExecutionCase(node agentnode.Node, execution ExecutionCase, maxOutputBytes int) error {
	ports, err := node.Resolve(execution.Request.Config)
	if err != nil {
		return fmt.Errorf("resolve ports: %w", err)
	}
	if err := validatePorts("resolved output", ports.Outputs); err != nil {
		return err
	}
	result, err := node.Execute(context.Background(), execution.Request)
	if execution.WantErrorKind != nil {
		if err == nil {
			return fmt.Errorf("expected error kind %q, got nil", *execution.WantErrorKind)
		}
		if kind := agentnode.KindOf(err); kind != *execution.WantErrorKind {
			return fmt.Errorf("error kind %q, want %q: %w", kind, *execution.WantErrorKind, err)
		}
		if len(result.Outputs) > 0 || len(result.ActivePorts) > 0 {
			return fmt.Errorf("execute returned outputs or active ports together with an error")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	if err := validateResult(result, ports.Outputs, maxOutputBytes); err != nil {
		return err
	}
	if !reflect.DeepEqual(result.Outputs, execution.WantOutputs) {
		return fmt.Errorf("outputs %#v, want %#v", result.Outputs, execution.WantOutputs)
	}
	return nil
}

func validateResult(result agentnode.Result, outputs []agentnode.Port, maxOutputBytes int) error {
	declared := make(map[string]struct{}, len(outputs))
	for _, port := range outputs {
		declared[port.Key] = struct{}{}
	}
	for key := range result.Outputs {
		if _, exists := declared[key]; !exists {
			return fmt.Errorf("result contains undeclared output %q", key)
		}
	}
	active := make(map[string]struct{}, len(result.ActivePorts))
	for _, key := range result.ActivePorts {
		if _, exists := declared[key]; !exists {
			return fmt.Errorf("result contains undeclared active port %q", key)
		}
		if _, exists := active[key]; exists {
			return fmt.Errorf("result contains duplicate active port %q", key)
		}
		active[key] = struct{}{}
	}
	encoded, err := json.Marshal(result.Outputs)
	if err != nil {
		return fmt.Errorf("encode outputs as JSON: %w", err)
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = defaultMaxOutputBytes
	}
	if len(encoded) > maxOutputBytes {
		return fmt.Errorf("encoded outputs are %d bytes, maximum is %d", len(encoded), maxOutputBytes)
	}
	return nil
}
