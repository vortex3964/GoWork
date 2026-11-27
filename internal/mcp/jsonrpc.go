// Package mcp implements a Model Context Protocol client. It speaks JSON-RPC
// 2.0 over a child process (stdio) or HTTP, performs the initialize handshake,
// discovers tools, and invokes them.
//
// The package deliberately has no dependency on the rest of GoWork. A later
// layer adapts discovered tools to the agent's dispatcher.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const jsonRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes, plus the MCP-reserved server error range.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// RPCError is an error object returned by an MCP server. Protocol errors are
// returned as *RPCError values from Client methods so callers can inspect the
// code; tool execution errors are reported through CallResult.IsError instead.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("json-rpc error %d: %s (data: %s)", e.Code, e.Message, truncate(string(e.Data), 512))
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// rpcMessage is the union shape used for both directions. Exactly one of the
// request, response, or notification sets of fields is populated.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type rpcResult struct {
	msg *rpcMessage
	err error
}

// idKey normalizes a JSON-RPC id so responses can be matched to requests.
// Numeric ids (the only kind this client sends) and string ids are both
// supported because servers must echo the id unchanged.
func idKey(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return "s:" + s, true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err == nil {
		return "n:" + n.String(), true
	}
	return "r:" + string(raw), true
}

// decodeRPCMessage parses one JSON-RPC message and rejects frames that are
// neither a request, a notification, nor a response.
func decodeRPCMessage(raw []byte) (*rpcMessage, error) {
	var msg rpcMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("decode json-rpc message: %w", err)
	}
	if msg.JSONRPC != jsonRPCVersion {
		return nil, fmt.Errorf("unsupported json-rpc version %q", msg.JSONRPC)
	}
	if msg.Method != "" {
		return &msg, nil
	}
	if _, ok := idKey(msg.ID); !ok {
		return nil, fmt.Errorf("json-rpc message has neither method nor id")
	}
	if msg.Error == nil && msg.Result == nil {
		return nil, fmt.Errorf("json-rpc response has neither result nor error")
	}
	return &msg, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
