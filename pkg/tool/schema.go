package tool

import (
	"encoding/json"
)

// FunctionDefinition represents an OpenAI-compatible function calling format.
// It is used to present a tool's signature to an LLM.
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ParamProp describes a single property in a JSON Schema object.
type ParamProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ParamSchema is a builder for typed parameter definitions that
// produces valid JSON Schema describing a tool's arguments.
//
// Usage:
//
//	schema := tool.NewParamSchema().
//	    AddString("city", "The city name", true).
//	    AddEnum("unit", "Temperature unit", []string{"celsius", "fahrenheit"}, false).
//	    Build()
type ParamSchema struct {
	Type       string               `json:"type"`
	Properties map[string]ParamProp `json:"properties"`
	Required   []string             `json:"required"`
}

// NewParamSchema creates a new parameter schema builder initialized
// with type "object" and an empty properties map.
func NewParamSchema() *ParamSchema {
	return &ParamSchema{
		Type:       "object",
		Properties: make(map[string]ParamProp),
	}
}

// AddString adds a string-typed parameter.
func (p *ParamSchema) AddString(name, desc string, required bool) *ParamSchema {
	p.Properties[name] = ParamProp{Type: "string", Description: desc}
	if required {
		p.Required = append(p.Required, name)
	}
	return p
}

// AddNumber adds a number-typed parameter.
func (p *ParamSchema) AddNumber(name, desc string, required bool) *ParamSchema {
	p.Properties[name] = ParamProp{Type: "number", Description: desc}
	if required {
		p.Required = append(p.Required, name)
	}
	return p
}

// AddBoolean adds a boolean-typed parameter.
func (p *ParamSchema) AddBoolean(name, desc string, required bool) *ParamSchema {
	p.Properties[name] = ParamProp{Type: "boolean", Description: desc}
	if required {
		p.Required = append(p.Required, name)
	}
	return p
}

// AddEnum adds a string-typed parameter restricted to the given values.
func (p *ParamSchema) AddEnum(name, desc string, values []string, required bool) *ParamSchema {
	p.Properties[name] = ParamProp{Type: "string", Description: desc, Enum: values}
	if required {
		p.Required = append(p.Required, name)
	}
	return p
}

// Build marshals the parameter schema into a json.RawMessage.
func (p *ParamSchema) Build() json.RawMessage {
	data, _ := json.Marshal(p)
	return json.RawMessage(data)
}
