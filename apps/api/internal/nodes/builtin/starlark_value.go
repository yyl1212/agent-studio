package builtin

import (
	"encoding/json"
	"fmt"
	"reflect"

	"go.starlark.net/starlark"
)

const maxStarlarkValueDepth = 64

func toStarlark(value any, depth int) (starlark.Value, error) {
	if depth > maxStarlarkValueDepth {
		return nil, fmt.Errorf("Starlark input exceeds maximum depth")
	}
	switch typed := value.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(typed), nil
	case string:
		return starlark.String(typed), nil
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return starlark.MakeInt64(integer), nil
		}
		floating, err := typed.Float64()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON number: %w", err)
		}
		return starlark.Float(floating), nil
	case float32:
		return starlark.Float(typed), nil
	case float64:
		return starlark.Float(typed), nil
	case int:
		return starlark.MakeInt(typed), nil
	case int8, int16, int32, int64:
		return starlark.MakeInt64(reflect.ValueOf(typed).Int()), nil
	case uint, uint8, uint16, uint32, uint64:
		return starlark.MakeUint64(reflect.ValueOf(typed).Uint()), nil
	case []any:
		items := make([]starlark.Value, len(typed))
		for index, item := range typed {
			converted, err := toStarlark(item, depth+1)
			if err != nil {
				return nil, err
			}
			items[index] = converted
		}
		return starlark.NewList(items), nil
	case map[string]any:
		dictionary := starlark.NewDict(len(typed))
		for key, item := range typed {
			converted, err := toStarlark(item, depth+1)
			if err != nil {
				return nil, err
			}
			if err := dictionary.SetKey(starlark.String(key), converted); err != nil {
				return nil, err
			}
		}
		return dictionary, nil
	default:
		return nil, fmt.Errorf("unsupported Starlark input type %T", value)
	}
}

func fromStarlark(value starlark.Value, depth int) (any, error) {
	if depth > maxStarlarkValueDepth {
		return nil, fmt.Errorf("Starlark output exceeds maximum depth")
	}
	switch typed := value.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(typed), nil
	case starlark.String:
		return string(typed), nil
	case starlark.Float:
		return float64(typed), nil
	case starlark.Int:
		integer, ok := typed.Int64()
		if !ok {
			return nil, fmt.Errorf("Starlark integer exceeds JSON range")
		}
		return float64(integer), nil
	case *starlark.List:
		items := make([]any, 0, typed.Len())
		iterator := typed.Iterate()
		defer iterator.Done()
		var item starlark.Value
		for iterator.Next(&item) {
			converted, err := fromStarlark(item, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case starlark.Tuple:
		items := make([]any, len(typed))
		for index, item := range typed {
			converted, err := fromStarlark(item, depth+1)
			if err != nil {
				return nil, err
			}
			items[index] = converted
		}
		return items, nil
	case *starlark.Dict:
		result := make(map[string]any, typed.Len())
		for _, item := range typed.Items() {
			key, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("Starlark dictionary keys must be strings")
			}
			converted, err := fromStarlark(item[1], depth+1)
			if err != nil {
				return nil, err
			}
			result[string(key)] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported Starlark output type %s", value.Type())
	}
}
