package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// timeTool returns the current time as a formatted string.
type timeTool struct{}

func (t *timeTool) Name() string        { return "get_current_time" }
func (t *timeTool) Description() string { return "Returns the current date and time" }
func (t *timeTool) Schema() json.RawMessage {
	return NewParamSchema().Build()
}

func (t *timeTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return time.Now().Format(time.RFC3339), nil
}

// echoTool echoes the provided arguments back as a JSON string.
type echoTool struct{}

func (e *echoTool) Name() string        { return "echo" }
func (e *echoTool) Description() string { return "Echoes back the provided arguments" }
func (e *echoTool) Schema() json.RawMessage {
	return NewParamSchema().
		AddString("message", "The message to echo", true).
		Build()
}

func (e *echoTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		return "{}", nil
	}
	return string(args), nil
}

// weatherTool returns mock weather data for a given city.
type weatherTool struct{}

func (w *weatherTool) Name() string        { return "get_weather" }
func (w *weatherTool) Description() string { return "Returns mock weather information for a city" }
func (w *weatherTool) Schema() json.RawMessage {
	return NewParamSchema().
		AddString("city", "The city name", true).
		AddEnum("unit", "Temperature unit", []string{"celsius", "fahrenheit"}, false).
		Build()
}

func (w *weatherTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var params struct {
		City string `json:"city"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	if params.City == "" {
		return "", fmt.Errorf("%w: city is required", ErrInvalidArgs)
	}

	unit := params.Unit
	if unit == "" {
		unit = "celsius"
	}
	temp := 22.0
	if unit == "fahrenheit" {
		temp = 71.6
	}

	result := map[string]any{
		"city":        params.City,
		"temperature": temp,
		"unit":        unit,
		"condition":   "sunny",
		"humidity":    45,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("weather marshal: %w", err)
	}
	return string(data), nil
}

// TimeTool returns a pre-built time tool instance.
func TimeTool() Tool { return &timeTool{} }

// EchoTool returns a pre-built echo tool instance.
func EchoTool() Tool { return &echoTool{} }

// WeatherTool returns a pre-built mock weather tool instance.
func WeatherTool() Tool { return &weatherTool{} }
