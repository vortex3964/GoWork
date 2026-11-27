package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	fakeServerEnv      = "GOWORK_LSP_FAKE"
	fakeSleepMethodEnv = "GOWORK_LSP_FAKE_SLEEP_METHOD"
)

// TestMain doubles as the fake language-server entry point. Client tests
// re-exec this test binary with fakeServerEnv set; the helper then speaks LSP
// over stdin/stdout instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) == "1" {
		runFakeServer()
		return
	}
	os.Exit(m.Run())
}

type fakeRPCMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func runFakeServer() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	sleepMethod := os.Getenv(fakeSleepMethodEnv)
	openedURI := ""

	for {
		body, err := fakeReadMessage(in)
		if err != nil {
			return
		}
		var msg fakeRPCMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if sleepMethod != "" && msg.Method == sleepMethod {
			time.Sleep(10 * time.Second)
			continue
		}

		switch msg.Method {
		case "initialize":
			fakeRespond(out, msg.ID, map[string]any{
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
			fakeNotify(out, "textDocument/publishDiagnostics", map[string]any{
				"uri": openedURI,
				"diagnostics": []map[string]any{{
					"range":    fakeRange(1, 2, 1, 8),
					"severity": 1,
					"source":   "fake",
					"message":  "fake diagnostic",
				}},
			})
		case "textDocument/documentSymbol":
			fakeRespond(out, msg.ID, []map[string]any{{
				"name":           "main",
				"kind":           12,
				"range":          fakeRange(0, 0, 2, 1),
				"selectionRange": fakeRange(0, 0, 0, 4),
			}})
		case "textDocument/definition":
			uri := openedURI
			if uri == "" {
				uri = "file:///tmp/fake.go"
			}
			fakeRespond(out, msg.ID, []map[string]any{{
				"uri":   uri,
				"range": fakeRange(1, 2, 1, 8),
			}})
		case "shutdown":
			fakeRespond(out, msg.ID, nil)
		case "exit":
			_ = out.Flush()
			return
		default:
			if len(msg.ID) > 0 {
				fakeRespond(out, msg.ID, nil)
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

func fakeRespond(w *bufio.Writer, id json.RawMessage, result any) {
	fakeWriteMessage(w, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func fakeNotify(w *bufio.Writer, method string, params any) {
	fakeWriteMessage(w, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func fakeWriteMessage(w *bufio.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data))
	_, _ = w.Write(data)
	_ = w.Flush()
}

func fakeReadMessage(r *bufio.Reader) ([]byte, error) {
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
