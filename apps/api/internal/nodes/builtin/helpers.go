package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrInvalidConfig         = errors.New("invalid node config")
	ErrRequiredInputMissing  = errors.New("required input missing")
	ErrInputTypeMismatch     = errors.New("input type mismatch")
	ErrInputCardinality      = errors.New("invalid input cardinality")
	ErrInvalidTemplate       = errors.New("invalid template")
	ErrConditionTypeMismatch = errors.New("condition type mismatch")
	ErrEndResultMissing      = errors.New("end result missing")
	ErrEndMultipleResults    = errors.New("end received multiple results")
)

func decodeConfig(raw json.RawMessage, target any) error {
	if err := agentnode.DecodeConfig(raw, target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return nil
}

func exactlyOneInput(inputs map[string][]any, key string) (any, error) {
	values := inputs[key]
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrRequiredInputMissing, key)
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("%w: %s has %d values", ErrInputCardinality, key, len(values))
	}
	return values[0], nil
}

func nodeConfigError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindConfig, "invalid_config", err, nil)
}

func nodeInputError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindInput, "invalid_input", err, nil)
}

func nodeMissingInputError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindInput, "missing_input", err, nil)
}

func nodeExecutionError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindInternal, "execution_failed", err, nil)
}

func nodeCanceledError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindCanceled, "run_canceled", err, nil)
}

func nodeTemporaryError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindTemporary, "upstream_timeout", err, nil)
}

func classifyExternalError(err error) error {
	if errors.Is(err, context.Canceled) {
		return nodeCanceledError(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nodeTemporaryError(err)
	}
	return nodeExecutionError(err)
}
