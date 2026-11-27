// Package lspfake is a small stdio LSP server used by tests in this
// repository. It is not part of the application.
package lspfake

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// Env makes a test binary run this fake server instead of its tests.
	Env = "GOWORK_LSP_FAKE"
	// SleepMethodEnv makes the fake server ignore (and sleep on) a method.
	SleepMethodEnv = "GOWORK_LSP_FAKE_SLEEP_METHOD"
	// ErrorMethodEnv makes the fake server answer a method with a JSON-RPC
	// error.
	ErrorMethodEnv = "GOWORK_LSP_FAKE_ERROR_METHOD"
)

// MaybeRun runs the fake server when Env is set and reports whether it did.
// TestMain implementations call it first, then run the real tests.
func MaybeRun() bool {
	if os.Getenv(Env) != "1" {
		return false
	}
	Run()
	return true
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Run serves LSP requests on stdin/stdout until EOF or an exit notification.
func Run() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	sleepMethod := os.Getenv(SleepMethodEnv)
	errorMethod := os.Getenv(ErrorMethodEnv)
	openedURI := ""

	for {
		body, err := readMessage(in)
		if err != nil {
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if sleepMethod != "" && msg.Method == sleepMethod {
			time.Sleep(10 * time.Second)
			continue
		}

		switch msg.Method {
		case "initialize":
			respond(out, msg.ID, map[string]any{
				"capabilities": map[string]any{
					"textDocumentSync":       1,
					"documentSymbolProvider": true,
					"definitionProvider":     true,
				},
				"serverInfo": map[string]any{"name": "fake-lsp", "version": "1.0.0"},
			})
		case "initialized":
			// Notification: no response.
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			openedURI = params.TextDocument.URI
			notify(out, "textDocument/publishDiagnostics", map[string]any{
				"uri": openedURI,
				"diagnostics": []map[string]any{{
					"range":    fakeRange(1, 2, 1, 8),
					"severity": 1,
					"source":   "fake",
					"message":  "fake diagnostic",
				}},
			})
		case "textDocument/documentSymbol":
			if errorMethod == msg.Method {
				respondError(out, msg.ID, -32601, "fake protocol error")
				continue
			}
			respond(out, msg.ID, []map[string]any{{
				"name":           "main",
				"kind":           12,
				"range":          fakeRange(0, 0, 2, 1),
				"selectionRange": fakeRange(0, 0, 0, 4),
				"children": []map[string]any{{
					"name":           "helper",
					"kind":           6,
					"range":          fakeRange(1, 0, 1, 10),
					"selectionRange": fakeRange(1, 0, 1, 6),
				}},
			}})
		case "textDocument/definition":
			if errorMethod == msg.Method {
				respondError(out, msg.ID, -32602, "fake definition error")
				continue
			}
			uri := openedURI
			if uri == "" {
				uri = "file:///tmp/fake.go"
			}
			respond(out, msg.ID, []map[string]any{{
				"uri":   uri,
				"range": fakeRange(1, 2, 1, 8),
			}})
		case "shutdown":
			respond(out, msg.ID, nil)
		case "exit":
			_ = out.Flush()
			return
		default:
			if len(msg.ID) > 0 {
				respond(out, msg.ID, nil)
			}
		}
	}
}

func fakeRange(startLine, startChar, endLine, endChar int) map[string]any {
	return map[string]any{
		"start": map[string]int{"line": startLine, "character": startChar},
		"end":   map[string]int{"line": endLine, "character": endChar},
	}
}

func respond(w *bufio.Writer, id json.RawMessage, result any) {
	writeMessage(w, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func respondError(w *bufio.Writer, id json.RawMessage, code int, message string) {
	writeMessage(w, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func notify(w *bufio.Writer, method string, params any) {
	writeMessage(w, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func writeMessage(w *bufio.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data))
	_, _ = w.Write(data)
	_ = w.Flush()
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad Content-Length %q", value)
		}
		length = n
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
