package agentnode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func DecodeConfig(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return NewError(ErrorKindConfig, "invalid_config", err, nil)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return NewError(ErrorKindConfig, "invalid_config", err, nil)
	}
	return nil
}

func MustSchema(raw string) json.RawMessage {
	if !json.Valid([]byte(raw)) {
		panic("agentnode: schema is not valid JSON")
	}
	return append(json.RawMessage(nil), raw...)
}
