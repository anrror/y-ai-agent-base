// Package tool defines the interface for callable tools.
package tool

import (
	"context"
	"encoding/json"
)

// Tool is a callable function with a JSON Schema describing its parameters.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}
