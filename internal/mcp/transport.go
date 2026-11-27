package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotStarted is returned when the client has not completed Start.
	ErrNotStarted = errors.New("mcp: client not started")
	// ErrClosed is returned after Close or when an operation races shutdown.
	ErrClosed = errors.New("mcp: client closed")
	// ErrTimeout identifies a request that exceeded its configured deadline.
	ErrTimeout = errors.New("mcp: request timed out")
	// ErrAlreadyStarted is returned when Start is called twice.
	ErrAlreadyStarted = errors.New("mcp: client already started")
	// ErrUnsupportedProtocolVersion is returned when initialization negotiates
	// a protocol revision this client does not implement.
	ErrUnsupportedProtocolVersion = errors.New("mcp: unsupported protocol version")
)

// transport moves complete JSON-RPC messages to and from a server. Implementations
// must close Done when the connection ends and expose the terminal reason from
// Err. Messages is closed when no further messages will arrive.
type transport interface {
	Start(ctx context.Context) error
	Send(ctx context.Context, message []byte) error
	Messages() <-chan []byte
	Done() <-chan struct{}
	Err() error
	SetProtocolVersion(version string)
	Close() error
}

type rpcKind int

const (
	rpcKindInvalid rpcKind = iota
	rpcKindRequest
	rpcKindNotification
	rpcKindResponse
)

// classifyRPCMessage reports whether a payload is a request (expects a
// response), a notification (expects only an acknowledgement), or a response
// to a server-initiated request.
func classifyRPCMessage(payload []byte) (rpcKind, string, error) {
	var msg rpcMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return rpcKindInvalid, "", fmt.Errorf("decode json-rpc message: %w", err)
	}
	if msg.JSONRPC != jsonRPCVersion {
		return rpcKindInvalid, "", fmt.Errorf("unsupported json-rpc version %q", msg.JSONRPC)
	}
	if msg.Method != "" {
		if key, ok := idKey(msg.ID); ok {
			return rpcKindRequest, key, nil
		}
		return rpcKindNotification, "", nil
	}
	if key, ok := idKey(msg.ID); ok {
		return rpcKindResponse, key, nil
	}
	return rpcKindInvalid, "", errors.New("json-rpc message has neither method nor id")
}

func mediaType(header string) string {
	if i := strings.IndexByte(header, ';'); i >= 0 {
		header = header[:i]
	}
	return strings.ToLower(strings.TrimSpace(header))
}

// httpStatusError preserves the HTTP status of a failed MCP request. The
// Streamable HTTP fallback logic keys off 400/404/405.
type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	msg := fmt.Sprintf("mcp server returned HTTP %s", e.Status)
	if e.Body != "" {
		msg += ": " + truncate(e.Body, 512)
	}
	return msg
}

func isLegacyFallbackError(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.StatusCode {
	case 400, 404, 405:
		return true
	default:
		return false
	}
}
