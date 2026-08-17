package builtin

import (
	"bytes"
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
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
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

func nodeExecutionError(err error) error {
	return agentnode.NewError(agentnode.ErrorKindInternal, "execution_failed", err, nil)
}
