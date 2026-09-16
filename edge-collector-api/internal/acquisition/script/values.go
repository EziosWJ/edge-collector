package script

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"

	"go.starlark.net/starlark"
)

// starlarkToJSONValue is deliberately narrower than the full Starlark value
// model. It is the only path by which state and event payloads become runtime
// data, so host objects, functions, sets, floats, bytes, and cycles are
// rejected here.
func starlarkToJSONValue(value starlark.Value, active map[uintptr]struct{}) (any, error) {
	switch value := value.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(value), nil
	case starlark.Int:
		if integer, ok := value.Int64(); ok {
			return integer, nil
		}
		return value.BigInt(), nil
	case starlark.String:
		return string(value), nil
	case *starlark.List:
		leave, err := enterReference(value, active)
		if err != nil {
			return nil, err
		}
		defer leave()
		items := make([]any, value.Len())
		for i := range items {
			item, err := starlarkToJSONValue(value.Index(i), active)
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i, err)
			}
			items[i] = item
		}
		return items, nil
	case starlark.Tuple:
		items := make([]any, len(value))
		for i, item := range value {
			converted, err := starlarkToJSONValue(item, active)
			if err != nil {
				return nil, fmt.Errorf("tuple item %d: %w", i, err)
			}
			items[i] = converted
		}
		return items, nil
	case *starlark.Dict:
		leave, err := enterReference(value, active)
		if err != nil {
			return nil, err
		}
		defer leave()
		items := value.Items()
		object := make(map[string]any, len(items))
		for _, item := range items {
			key, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("dict key has type %s, want string", item[0].Type())
			}
			converted, err := starlarkToJSONValue(item[1], active)
			if err != nil {
				return nil, fmt.Errorf("dict key %q: %w", key, err)
			}
			object[string(key)] = converted
		}
		return object, nil
	default:
		return nil, fmt.Errorf("value of type %s is not JSON-compatible", value.Type())
	}
}

func enterReference(value starlark.Value, active map[uintptr]struct{}) (func(), error) {
	identity := reflect.ValueOf(value)
	if identity.Kind() != reflect.Pointer {
		return func() {}, nil
	}
	pointer := identity.Pointer()
	if _, exists := active[pointer]; exists {
		return nil, fmt.Errorf("cyclic value is not JSON-compatible")
	}
	active[pointer] = struct{}{}
	return func() { delete(active, pointer) }, nil
}

func jsonSize(value any) (int, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return len(encoded), nil
}

func cloneJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, string, int64:
		return value, nil
	case *big.Int:
		if value == nil {
			return (*big.Int)(nil), nil
		}
		return new(big.Int).Set(value), nil
	case []any:
		items := make([]any, len(value))
		for i, item := range value {
			cloned, err := cloneJSONValue(item)
			if err != nil {
				return nil, err
			}
			items[i] = cloned
		}
		return items, nil
	case map[string]any:
		object := make(map[string]any, len(value))
		for key, item := range value {
			cloned, err := cloneJSONValue(item)
			if err != nil {
				return nil, err
			}
			object[key] = cloned
		}
		return object, nil
	default:
		return nil, fmt.Errorf("unsupported stored value %T", value)
	}
}

func cloneState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return make(map[string]any)
	}
	cloned, err := cloneJSONValue(state)
	if err != nil {
		panic(fmt.Sprintf("invalid internal script state: %v", err))
	}
	return cloned.(map[string]any)
}

func cloneEvents(events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]Event, len(events))
	for i, event := range events {
		payload, err := cloneJSONValue(event.Payload)
		if err != nil {
			panic(fmt.Sprintf("invalid internal script event: %v", err))
		}
		cloned[i] = event
		cloned[i].Payload = payload
	}
	return cloned
}

func toStarlarkValue(value any) (starlark.Value, error) {
	switch value := value.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(value), nil
	case int64:
		return starlark.MakeInt64(value), nil
	case *big.Int:
		if value == nil {
			return starlark.None, nil
		}
		return starlark.MakeBigInt(value), nil
	case string:
		return starlark.String(value), nil
	case []any:
		items := make([]starlark.Value, len(value))
		for i, item := range value {
			converted, err := toStarlarkValue(item)
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return starlark.NewList(items), nil
	case map[string]any:
		dict := starlark.NewDict(len(value))
		for key, item := range value {
			converted, err := toStarlarkValue(item)
			if err != nil {
				return nil, err
			}
			if err := dict.SetKey(starlark.String(key), converted); err != nil {
				return nil, err
			}
		}
		return dict, nil
	default:
		return nil, fmt.Errorf("unsupported stored value %T", value)
	}
}
