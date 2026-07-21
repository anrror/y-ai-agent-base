package tool

import (
	"context"
	"encoding/json"
)

// toolFunc adapts a plain function into the Tool interface.
type toolFunc struct {
	name        string
	description string
	schema      json.RawMessage
	fn          func(context.Context, json.RawMessage) (string, error)
}

func (t *toolFunc) Name() string            { return t.name }
func (t *toolFunc) Description() string     { return t.description }
func (t *toolFunc) Schema() json.RawMessage { return t.schema }
func (t *toolFunc) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.fn(ctx, args)
}

// FromFunction creates a Tool from a name, description, function, and
// an optional parameter schema. If no schema is provided, an empty
// object schema is used.
//
// Usage:
//
//	multiply := tool.FromFunction("multiply", "Multiply two numbers",
//	    func(ctx context.Context, args json.RawMessage) (string, error) {
//	        // implementation
//	    },
//	    tool.NewParamSchema().
//	        AddNumber("a", "First operand", true).
//	        AddNumber("b", "Second operand", true).
//	        Build(),
//	)
func FromFunction(
	name string,
	description string,
	fn func(context.Context, json.RawMessage) (string, error),
	schema ...json.RawMessage,
) Tool {
	var s json.RawMessage
	if len(schema) > 0 && len(schema[0]) > 0 {
		s = schema[0]
	} else {
		s = NewParamSchema().Build()
	}
	return &toolFunc{
		name:        name,
		description: description,
		schema:      s,
		fn:          fn,
	}
}
