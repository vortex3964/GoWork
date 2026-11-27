package mcp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	maxSSELine    = 16 << 20
	maxSSEMessage = 64 << 20
)

// sseEvent is one parsed Server-Sent Events event.
type sseEvent struct {
	Event string
	Data  []byte
	ID    string
	Retry time.Duration
}

// readSSE consumes an SSE stream and calls emit for every event that carries
// data. Multi-line data fields are joined with newlines as required by the
// SSE specification, and CRLF line endings are accepted.
func readSSE(r io.Reader, emit func(sseEvent) error) error {
	reader := bufio.NewReaderSize(r, 32*1024)

	var event sseEvent
	var data bytes.Buffer
	haveData := false

	reset := func() {
		event = sseEvent{}
		data.Reset()
		haveData = false
	}

	dispatch := func() error {
		if !haveData {
			reset()
			return nil
		}
		payload := data.Bytes()
		if n := len(payload); n > 0 && payload[n-1] == '\n' {
			payload = payload[:n-1]
		}
		event.Data = append([]byte(nil), payload...)
		err := emit(event)
		reset()
		return err
	}

	handleLine := func(line string) error {
		if line == "" {
			return dispatch()
		}
		if strings.HasPrefix(line, ":") {
			return nil
		}

		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		if !found {
			value = ""
		}

		switch field {
		case "event":
			event.Event = value
		case "data":
			// Check before copying so a near-limit event cannot briefly grow
			// past the cap while handling one more line.
			if data.Len()+len(value)+1 > maxSSEMessage {
				return fmt.Errorf("sse event exceeds %d bytes", maxSSEMessage)
			}
			haveData = true
			data.WriteString(value)
			data.WriteByte('\n')
		case "id":
			event.ID = value
		case "retry":
			ms, err := strconv.Atoi(value)
			if err == nil && ms >= 0 {
				event.Retry = time.Duration(ms) * time.Millisecond
			}
		}
		return nil
	}

	processLine := func(raw []byte) error {
		raw = bytes.TrimSuffix(raw, []byte("\n"))
		raw = bytes.TrimSuffix(raw, []byte("\r"))
		return handleLine(string(raw))
	}

	// ReadSlice returns ErrBufferFull instead of growing the buffer, so a
	// newline-free stream cannot allocate without bound. Accumulate fragments
	// only while they stay within maxSSELine.
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if len(line)+len(fragment) > maxSSELine {
				return fmt.Errorf("sse line exceeds %d bytes", maxSSELine)
			}
			line = append(line, fragment...)
		}

		switch err {
		case nil:
			if perr := processLine(line); perr != nil {
				return perr
			}
			line = line[:0]
		case bufio.ErrBufferFull:
			// The delimiter is not in this fragment yet; keep accumulating.
			continue
		case io.EOF:
			if len(line) > 0 || haveData {
				if perr := processLine(line); perr != nil {
					return perr
				}
			}
			return dispatch()
		default:
			return fmt.Errorf("read sse stream: %w", err)
		}
	}
}
