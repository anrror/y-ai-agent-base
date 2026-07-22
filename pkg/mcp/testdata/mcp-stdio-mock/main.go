package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type request struct {
	ID     int              `json:"id"`
	Method string           `json:"method"`
	Params json.RawMessage  `json:"params,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			writeResponse(req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
				"serverInfo": map[string]any{
					"name":    "mock-mcp-server",
					"version": "1.0.0",
				},
			})

		case "notifications/initialized":
			// No response to notifications.

		case "tools/list":
			writeResponse(req.ID, map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"description": "Echoes back the input",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{"type": "string"},
							},
						},
					},
					{
						"name":        "add",
						"description": "Adds two numbers",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"a": map[string]any{"type": "number"},
								"b": map[string]any{"type": "number"},
							},
						},
					},
				},
			})

		case "tools/call":
			var params struct {
				Name      string           `json:"name"`
				Arguments json.RawMessage  `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeError(req.ID, -32602, "Invalid params")
				continue
			}

			switch params.Name {
			case "echo":
				writeResponse(req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": string(params.Arguments)},
					},
					"isError": false,
				})
			case "add":
				var args struct {
					A float64 `json:"a"`
					B float64 `json:"b"`
				}
				if err := json.Unmarshal(params.Arguments, &args); err != nil {
					writeError(req.ID, -32602, "Invalid arguments")
					continue
				}
				result := fmt.Sprintf("%.0f", args.A+args.B)
				writeResponse(req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": result},
					},
					"isError": false,
				})
			default:
				writeResponse(req.ID, map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "unknown tool: " + params.Name},
					},
					"isError": true,
				})
			}

		case "shutdown":
			writeResponse(req.ID, map[string]any{})
			os.Exit(0)

		default:
			writeError(req.ID, -32601, "Method not found")
		}
	}
}

func writeResponse(id int, result any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

func writeError(id int, code int, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}
