package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

func TestEchoTool_returns_input(t *testing.T) {
	ctx := context.Background()
	tl := tool.EchoTool()

	t.Run("echoes JSON args", func(t *testing.T) {
		result, err := tl.Execute(ctx, json.RawMessage(`{"message":"hello"}`))
		require.NoError(t, err)
		assert.Equal(t, `{"message":"hello"}`, result)
	})

	t.Run("echoes empty args as empty object", func(t *testing.T) {
		result, err := tl.Execute(ctx, nil)
		require.NoError(t, err)
		assert.Equal(t, "{}", result)
	})
}

func TestTimeTool_returns_current_time(t *testing.T) {
	ctx := context.Background()
	tl := tool.TimeTool()

	result, err := tl.Execute(ctx, nil)
	require.NoError(t, err)

	parsed, err := time.Parse(time.RFC3339, result)
	require.NoError(t, err, "time tool should return RFC3339 formatted time")

	// Should be within the last minute.
	assert.WithinDuration(t, time.Now(), parsed, time.Minute)
}

func TestWeatherTool_returns_mock_data(t *testing.T) {
	ctx := context.Background()
	tl := tool.WeatherTool()

	t.Run("with city provided", func(t *testing.T) {
		result, err := tl.Execute(ctx, json.RawMessage(`{"city":"Beijing"}`))
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &data))
		assert.Equal(t, "Beijing", data["city"])
		assert.Equal(t, "sunny", data["condition"])
		assert.Equal(t, "celsius", data["unit"])
	})

	t.Run("with city and unit", func(t *testing.T) {
		result, err := tl.Execute(ctx, json.RawMessage(`{"city":"Tokyo","unit":"fahrenheit"}`))
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &data))
		assert.Equal(t, "Tokyo", data["city"])
		assert.Equal(t, "fahrenheit", data["unit"])
	})

	t.Run("missing city returns error", func(t *testing.T) {
		_, err := tl.Execute(ctx, json.RawMessage(`{}`))
		require.Error(t, err)
		assert.True(t, errors.Is(err, tool.ErrInvalidArgs))
	})
}

func TestTool_Schema_is_valid_JSON(t *testing.T) {
	tools := []tool.Tool{
		tool.TimeTool(),
		tool.EchoTool(),
		tool.WeatherTool(),
	}

	for _, tl := range tools {
		t.Run(tl.Name(), func(t *testing.T) {
			schema := tl.Schema()
			var data map[string]any
			require.NoError(t, json.Unmarshal(schema, &data))
			assert.Equal(t, "object", data["type"])
		})
	}
}

func TestRegistry_Register_and_Get(t *testing.T) {
	reg := tool.NewRegistry()

	tl := tool.EchoTool()
	require.NoError(t, reg.Register(tl))

	got, ok := reg.Get("echo")
	require.True(t, ok)
	assert.Equal(t, "echo", got.Name())

	_, ok = reg.Get("nonexistent")
	assert.False(t, ok)
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := tool.NewRegistry()

	require.NoError(t, reg.Register(tool.EchoTool()))
	err := reg.Register(tool.EchoTool())
	require.Error(t, err)
	assert.True(t, errors.Is(err, tool.ErrToolExists))
}

func TestRegistry_List(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(tool.EchoTool()))
	require.NoError(t, reg.Register(tool.TimeTool()))

	tools := reg.List()
	assert.Len(t, tools, 2)

	names := make(map[string]bool)
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	assert.True(t, names["echo"])
	assert.True(t, names["get_current_time"])
}

func TestRegistry_Call(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(tool.EchoTool()))

	result, err := reg.Call(context.Background(), "echo", json.RawMessage(`{"message":"hi"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"message":"hi"}`, result)
}

func TestRegistry_Call_unknown_tool(t *testing.T) {
	reg := tool.NewRegistry()

	_, err := reg.Call(context.Background(), "nonexistent", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, tool.ErrToolNotFound))
}

func TestRegistry_Call_context_canceled(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(tool.EchoTool()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := reg.Call(ctx, "echo", json.RawMessage(`{"message":"hi"}`))
	require.Error(t, err)
}

func TestRegistry_Call_context_deadline(t *testing.T) {
	reg := tool.NewRegistry()
	require.NoError(t, reg.Register(tool.EchoTool()))

	ctx, cancel := context.WithTimeout(context.Background(), -time.Nanosecond)
	defer cancel()

	_, err := reg.Call(ctx, "echo", json.RawMessage(`{"message":"hi"}`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, tool.ErrToolCallTimeout))
}

func TestRegistry_ConcurrentRegister(t *testing.T) {
	reg := tool.NewRegistry()
	var wg sync.WaitGroup

	names := []string{"a", "b", "c", "d", "e"}
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			_ = reg.Register(tool.FromFunction(n, "test tool", func(_ context.Context, _ json.RawMessage) (string, error) {
				return n, nil
			}))
		}(name)
	}

	wg.Wait()

	registered := reg.List()
	assert.Len(t, registered, len(names))
}

func TestFromFunction_no_schema(t *testing.T) {
	tl := tool.FromFunction("hello", "Says hello", func(_ context.Context, _ json.RawMessage) (string, error) {
		return "world", nil
	})

	assert.Equal(t, "hello", tl.Name())
	assert.Equal(t, "Says hello", tl.Description())

	var schema map[string]any
	require.NoError(t, json.Unmarshal(tl.Schema(), &schema))
	assert.Equal(t, "object", schema["type"])

	result, err := tl.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "world", result)
}

func TestFromFunction_with_schema(t *testing.T) {
	schema := tool.NewParamSchema().
		AddNumber("a", "First number", true).
		AddNumber("b", "Second number", true).
		Build()

	tl := tool.FromFunction(
		"add", "Adds two numbers",
		func(_ context.Context, args json.RawMessage) (string, error) {
			var params struct {
				A float64 `json:"a"`
				B float64 `json:"b"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("unmarshal args: %w", err)
			}
			sum := params.A + params.B
			data, _ := json.Marshal(map[string]float64{"result": sum})
			return string(data), nil
		},
		schema,
	)

	result, err := tl.Execute(context.Background(), json.RawMessage(`{"a":3,"b":4}`))
	require.NoError(t, err)

	var out map[string]float64
	require.NoError(t, json.Unmarshal([]byte(result), &out))
	assert.InDelta(t, 7.0, out["result"], 0.001)
}

func TestParamSchema_Builder(t *testing.T) {
	schema := tool.NewParamSchema().
		AddString("name", "User name", true).
		AddNumber("age", "User age", false).
		AddBoolean("active", "Is active", false).
		AddEnum("role", "User role", []string{"admin", "user"}, true).
		Build()

	var data map[string]any
	require.NoError(t, json.Unmarshal(schema, &data))

	assert.Equal(t, "object", data["type"])

	props, ok := data["properties"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, props, "name")
	assert.Contains(t, props, "age")
	assert.Contains(t, props, "active")
	assert.Contains(t, props, "role")

	required, ok := data["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "name")
	assert.Contains(t, required, "role")
	assert.NotContains(t, required, "age")

	// Verify enum.
	roleProp, ok := props["role"].(map[string]any)
	require.True(t, ok)
	enumVals, ok := roleProp["enum"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"admin", "user"}, enumVals)
}

func TestFunctionDefinition_JSON(t *testing.T) {
	schema := tool.NewParamSchema().
		AddString("location", "City name", true).
		Build()

	fd := tool.FunctionDefinition{
		Name:        "get_weather",
		Description: "Get weather for a city",
		Parameters:  schema,
	}

	data, err := json.Marshal(fd)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "get_weather", parsed["name"])
	assert.Equal(t, "Get weather for a city", parsed["description"])

	params, ok := parsed["parameters"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", params["type"])
}

func TestTool_Name_and_Description(t *testing.T) {
	tl := tool.EchoTool()
	assert.Equal(t, "echo", tl.Name())
	assert.Equal(t, "Echoes back the provided arguments", tl.Description())

	wt := tool.WeatherTool()
	assert.Equal(t, "get_weather", wt.Name())
	assert.Contains(t, wt.Description(), "weather")
}
