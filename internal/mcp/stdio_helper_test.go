package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// TestStdioHelperProcess is not a real test. It turns the test binary into a
// fake MCP server when the parent test starts it with GO_WANT_MCP_HELPER=1.
func TestStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	serveFakeStdio(os.Stdin, os.Stdout, os.Stderr, os.Getenv("MCP_HELPER_SCENARIO"))
	os.Exit(0)
}

func serveFakeStdio(in io.Reader, out, errOut io.Writer, scenario string) {
	enc := json.NewEncoder(out)
	reader := bufio.NewReader(in)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if json.Unmarshal(line, &msg) == nil && msg.Method != "" {
				if !handleFakeStdioRequest(enc, errOut, scenario, msg) {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func handleFakeStdioRequest(enc *json.Encoder, errOut io.Writer, scenario string, msg rpcMessage) bool {
	writeResult := func(result any) {
		_ = enc.Encode(rpcResponse{JSONRPC: jsonRPCVersion, ID: msg.ID, Result: result})
	}
	writeError := func(code int, message string) {
		_ = enc.Encode(rpcResponse{JSONRPC: jsonRPCVersion, ID: msg.ID, Error: &RPCError{Code: code, Message: message}})
	}

	switch msg.Method {
	case "initialize":
		switch scenario {
		case "init-error":
			writeError(-32000, "initialize rejected")
			return true
		case "silent-init":
			return true
		case "crash-start":
			fmt.Fprintln(errOut, "fake server: missing credentials")
			time.Sleep(50 * time.Millisecond)
			return false
		}

		version := os.Getenv("MCP_HELPER_PROTOCOL")
		if scenario == "unsupported-version" {
			version = "1900-01-01"
		}
		if version == "" {
			version = ProtocolVersion20250618
		}
		writeResult(InitializeResult{
			ProtocolVersion: version,
			Capabilities: ServerCapabilities{
				Tools: &ToolsCapability{ListChanged: true},
			},
			ServerInfo:   ServerInfo{Name: "fake-server", Version: "1.2.3"},
			Instructions: "Use the echo tool.",
		})
		return true

	case "notifications/initialized":
		return true

	case "ping":
		writeResult(map[string]any{})
		return true

	case "tools/list":
		if scenario == "list-error" {
			writeError(CodeInternalError, "tool registry unavailable")
			return true
		}

		var params struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		switch params.Cursor {
		case "":
			writeResult(map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"title":       "Echo",
						"description": "  Echo text back  ",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text": map[string]any{"type": "string"},
							},
							"required": []string{"text"},
						},
					},
					{
						"name":        "fail",
						"description": "Always fails",
						"inputSchema": map[string]any{"type": "object"},
					},
				},
				"nextCursor": "page-2",
			})
		case "page-2":
			writeResult(map[string]any{
				"tools": []map[string]any{
					{
						"name":        "image",
						"description": "Returns an image",
					},
					{
						"name":        "resource",
						"description": "Returns an embedded resource",
						"inputSchema": map[string]any{"type": "object"},
					},
				},
			})
		default:
			writeError(CodeInvalidParams, "unknown cursor")
		}
		return true

	case "tools/call":
		if scenario == "call-error" {
			writeError(CodeInternalError, "tool call failed at protocol level")
			return true
		}

		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			writeError(CodeInvalidParams, "invalid call params")
			return true
		}

		switch params.Name {
		case "echo":
			writeResult(map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("echo: %v", params.Arguments["text"]),
				}},
				"isError": false,
			})
		case "fail":
			writeResult(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "tool execution failed"}},
				"isError": true,
			})
		case "image":
			writeResult(map[string]any{
				"content": []map[string]any{
					{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
					{"type": "text", "text": "caption"},
				},
			})
		case "resource":
			writeResult(map[string]any{
				"content": []map[string]any{{
					"type": "resource",
					"resource": map[string]any{
						"uri":      "file:///tmp/fake.txt",
						"mimeType": "text/plain",
						"text":     "file body",
					},
				}},
			})
		case "structured":
			writeResult(map[string]any{
				"structuredContent": map[string]any{"answer": 42},
			})
		case "exit":
			return false
		default:
			writeError(CodeInvalidParams, "unknown tool: "+params.Name)
		}
		return true

	default:
		if _, isRequest := idKey(msg.ID); isRequest {
			writeError(CodeMethodNotFound, "method not found: "+msg.Method)
		}
		return true
	}
}
