package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anrror/y-ai-agent-base/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FuncClient tests
// ---------------------------------------------------------------------------

func TestFuncClient(t *testing.T) {
	t.Run("ListTools with function", func(t *testing.T) {
		c := mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{
					{Name: "hello", Description: "says hello"},
				}, nil
			},
			nil, nil,
		)
		tools, err := c.ListTools(context.Background())
		require.NoError(t, err)
		assert.Len(t, tools, 1)
		assert.Equal(t, "hello", tools[0].Name)
	})

	t.Run("ListTools nil function", func(t *testing.T) {
		c := mcp.NewFuncClient(nil, nil, nil)
		tools, err := c.ListTools(context.Background())
		require.NoError(t, err)
		assert.Nil(t, tools)
	})

	t.Run("CallTool with function", func(t *testing.T) {
		c := mcp.NewFuncClient(nil,
			func(_ context.Context, _ string, _ json.RawMessage) (*mcp.CallResult, error) {
				return &mcp.CallResult{
					Content: []mcp.Content{{Type: "text", Text: "hello world"}},
				}, nil
			},
			nil,
		)
		result, err := c.CallTool(context.Background(), "test", nil)
		require.NoError(t, err)
		assert.Equal(t, "hello world", result.TextContent())
	})

	t.Run("CallTool nil function returns error", func(t *testing.T) {
		c := mcp.NewFuncClient(nil, nil, nil)
		_, err := c.CallTool(context.Background(), "test", nil)
		assert.Error(t, err)
	})

	t.Run("Close nil function is no-op", func(t *testing.T) {
		c := mcp.NewFuncClient(nil, nil, nil)
		assert.NoError(t, c.Close())
	})
}

// ---------------------------------------------------------------------------
// CallResult tests
// ---------------------------------------------------------------------------

func TestCallResult(t *testing.T) {
	t.Run("TextContent concatenates text items", func(t *testing.T) {
		r := &mcp.CallResult{
			Content: []mcp.Content{
				{Type: "text", Text: "line1"},
				{Type: "text", Text: "line2"},
				{Type: "image", Text: "skip"},
			},
		}
		assert.Equal(t, "line1\nline2", r.TextContent())
	})

	t.Run("TextContent empty", func(t *testing.T) {
		r := &mcp.CallResult{}
		assert.Equal(t, "", r.TextContent())
	})

	t.Run("Error returns formatted error", func(t *testing.T) {
		r := &mcp.CallResult{
			IsError: true,
			Content: []mcp.Content{{Type: "text", Text: "permission denied"}},
		}
		err := r.Error()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "permission denied")
	})

	t.Run("Error returns nil when not IsError", func(t *testing.T) {
		r := &mcp.CallResult{Content: []mcp.Content{{Text: "ok"}}}
		assert.NoError(t, r.Error())
	})
}

// ---------------------------------------------------------------------------
// Server tests
// ---------------------------------------------------------------------------

func TestServer(t *testing.T) {
	t.Run("NewServer sets name", func(t *testing.T) {
		s := mcp.NewServer("test", mcp.NewFuncClient(nil, nil, nil))
		assert.Equal(t, "test", s.Name)
	})

	t.Run("NewServer defaults name", func(t *testing.T) {
		s := mcp.NewServer("", nil)
		assert.Equal(t, "mcp", s.Name)
	})

	t.Run("Validate passes with valid server", func(t *testing.T) {
		s := mcp.NewServer("valid", mcp.NewFuncClient(nil, nil, nil))
		assert.NoError(t, s.Validate())
	})

	t.Run("Validate fails on empty name", func(t *testing.T) {
		s := &mcp.Server{Name: "", Client: mcp.NewFuncClient(nil, nil, nil)}
		assert.Error(t, s.Validate())
	})

	t.Run("Validate fails on nil client", func(t *testing.T) {
		s := &mcp.Server{Name: "test", Client: nil}
		assert.Error(t, s.Validate())
	})

	t.Run("Close calls client close", func(t *testing.T) {
		closed := false
		c := mcp.NewFuncClient(nil, nil, func() error { closed = true; return nil })
		s := mcp.NewServer("test", c)
		require.NoError(t, s.Close())
		assert.True(t, closed)
	})
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry(t *testing.T) {
	t.Run("Add and Get", func(t *testing.T) {
		r := mcp.NewRegistry()
		s := mcp.NewServer("alpha", mcp.NewFuncClient(nil, nil, nil))
		require.NoError(t, r.Add(s))
		assert.Equal(t, s, r.Get("alpha"))
	})

	t.Run("Add duplicate returns error", func(t *testing.T) {
		r := mcp.NewRegistry()
		s := mcp.NewServer("dup", mcp.NewFuncClient(nil, nil, nil))
		require.NoError(t, r.Add(s))
		assert.Error(t, r.Add(s))
	})

	t.Run("Add nil server returns error", func(t *testing.T) {
		r := mcp.NewRegistry()
		assert.Error(t, r.Add(nil))
	})

	t.Run("Get non-existent returns nil", func(t *testing.T) {
		r := mcp.NewRegistry()
		assert.Nil(t, r.Get("nope"))
	})

	t.Run("List returns all servers", func(t *testing.T) {
		r := mcp.NewRegistry()
		r.Add(mcp.NewServer("a", mcp.NewFuncClient(nil, nil, nil)))
		r.Add(mcp.NewServer("b", mcp.NewFuncClient(nil, nil, nil)))
		assert.Len(t, r.List(), 2)
	})

	t.Run("Remove", func(t *testing.T) {
		r := mcp.NewRegistry()
		r.Add(mcp.NewServer("x", mcp.NewFuncClient(nil, nil, nil)))
		assert.True(t, r.Remove("x"))
		assert.False(t, r.Remove("x")) // already removed
		assert.Nil(t, r.Get("x"))
	})

	t.Run("ServerNames", func(t *testing.T) {
		r := mcp.NewRegistry()
		r.Add(mcp.NewServer("a", mcp.NewFuncClient(nil, nil, nil)))
		r.Add(mcp.NewServer("b", mcp.NewFuncClient(nil, nil, nil)))
		names := r.ServerNames()
		assert.ElementsMatch(t, []string{"a", "b"}, names)
	})

	t.Run("Close clears registry", func(t *testing.T) {
		r := mcp.NewRegistry()
		r.Add(mcp.NewServer("a", mcp.NewFuncClient(nil, nil, nil)))
		require.NoError(t, r.Close())
		assert.Equal(t, 0, r.Len())
	})

	t.Run("Close collects errors", func(t *testing.T) {
		errClient := mcp.NewFuncClient(nil, nil, func() error { return errors.New("boom") })
		r := mcp.NewRegistry()
		r.Add(mcp.NewServer("err", errClient))
		err := r.Close()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
}

// ---------------------------------------------------------------------------
// ToolAdapter tests
// ---------------------------------------------------------------------------

func TestToolAdapter(t *testing.T) {
	t.Run("Name is fully qualified", func(t *testing.T) {
		info := &mcp.ToolInfo{Name: "read_file", Description: "read a file"}
		client := mcp.NewFuncClient(nil, nil, nil)
		a := mcp.NewToolAdapter("fs", info, client)
		assert.Equal(t, "fs/read_file", a.Name())
	})

	t.Run("Description from info", func(t *testing.T) {
		info := &mcp.ToolInfo{Name: "t", Description: "test tool"}
		client := mcp.NewFuncClient(nil, nil, nil)
		a := mcp.NewToolAdapter("srv", info, client)
		assert.Equal(t, "test tool", a.Description())
	})

	t.Run("Schema from info", func(t *testing.T) {
		schema := json.RawMessage(`{"type":"object"}`)
		info := &mcp.ToolInfo{Name: "t", InputSchema: schema}
		a := mcp.NewToolAdapter("srv", info, mcp.NewFuncClient(nil, nil, nil))
		assert.Equal(t, schema, a.Schema())
	})

	t.Run("Execute delegates to client", func(t *testing.T) {
		info := &mcp.ToolInfo{Name: "echo"}
		client := mcp.NewFuncClient(nil,
			func(_ context.Context, name string, _ json.RawMessage) (*mcp.CallResult, error) {
				return &mcp.CallResult{
					Content: []mcp.Content{{Type: "text", Text: "echo: " + name}},
				}, nil
			},
			nil,
		)
		a := mcp.NewToolAdapter("srv", info, client)
		result, err := a.Execute(context.Background(), json.RawMessage(`{"msg":"hi"}`))
		require.NoError(t, err)
		assert.Equal(t, "echo: echo", result)
	})

	t.Run("Execute propagates error result", func(t *testing.T) {
		info := &mcp.ToolInfo{Name: "fail"}
		client := mcp.NewFuncClient(nil,
			func(_ context.Context, _ string, _ json.RawMessage) (*mcp.CallResult, error) {
				return &mcp.CallResult{
					IsError: true,
					Content: []mcp.Content{{Type: "text", Text: "something broke"}},
				}, nil
			},
			nil,
		)
		a := mcp.NewToolAdapter("srv", info, client)
		_, err := a.Execute(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "something broke")
	})

	t.Run("Execute propagates transport error", func(t *testing.T) {
		info := &mcp.ToolInfo{Name: "crash"}
		client := mcp.NewFuncClient(nil,
			func(_ context.Context, _ string, _ json.RawMessage) (*mcp.CallResult, error) {
				return nil, errors.New("connection refused")
			},
			nil,
		)
		a := mcp.NewToolAdapter("srv", info, client)
		_, err := a.Execute(context.Background(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})
}

// ---------------------------------------------------------------------------
// ServerTools and ResolveTools tests
// ---------------------------------------------------------------------------

func TestServerTools(t *testing.T) {
	t.Run("returns tools from server", func(t *testing.T) {
		client := mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{
					{Name: "a", Description: "tool A"},
					{Name: "b", Description: "tool B"},
				}, nil
			},
			nil, nil,
		)
		server := mcp.NewServer("test", client)
		tools, err := mcp.ServerTools(server)
		require.NoError(t, err)
		require.Len(t, tools, 2)
		assert.Equal(t, "test/a", tools[0].Name())
		assert.Equal(t, "test/b", tools[1].Name())
	})

	t.Run("nil client returns empty", func(t *testing.T) {
		server := mcp.NewServer("empty", nil)
		tools, err := mcp.ServerTools(server)
		require.NoError(t, err)
		assert.Nil(t, tools)
	})
}

func TestResolveTools(t *testing.T) {
	t.Run("selects specific servers", func(t *testing.T) {
		reg := mcp.NewRegistry()
		reg.Add(mcp.NewServer("a", mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{{Name: "tool1"}}, nil
			}, nil, nil,
		)))
		reg.Add(mcp.NewServer("b", mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{{Name: "tool2"}}, nil
			}, nil, nil,
		)))

		tools, err := mcp.ResolveTools(reg, []string{"a"})
		require.NoError(t, err)
		require.Len(t, tools, 1)
		assert.Equal(t, "a/tool1", tools[0].Name())
	})

	t.Run("empty server list uses all", func(t *testing.T) {
		reg := mcp.NewRegistry()
		reg.Add(mcp.NewServer("a", mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{{Name: "x"}}, nil
			}, nil, nil,
		)))
		reg.Add(mcp.NewServer("b", mcp.NewFuncClient(
			func(_ context.Context) ([]*mcp.ToolInfo, error) {
				return []*mcp.ToolInfo{{Name: "y"}}, nil
			}, nil, nil,
		)))

		tools, err := mcp.ResolveTools(reg, nil)
		require.NoError(t, err)
		assert.Len(t, tools, 2)
	})

	t.Run("nil registry returns empty", func(t *testing.T) {
		tools, err := mcp.ResolveTools(nil, []string{"a"})
		require.NoError(t, err)
		assert.Nil(t, tools)
	})

	t.Run("unknown server returns error", func(t *testing.T) {
		reg := mcp.NewRegistry()
		_, err := mcp.ResolveTools(reg, []string{"nonexistent"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent")
	})
}

// ---------------------------------------------------------------------------
// ToolFullName test
// ---------------------------------------------------------------------------

func TestToolFullName(t *testing.T) {
	assert.Equal(t, "fs/read_file", mcp.ToolFullName("fs", "read_file"))
}
