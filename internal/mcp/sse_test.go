package mcp

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadSSE(t *testing.T) {
	stream := ": comment\r\n" +
		"event: message\r\n" +
		"id: 42\r\n" +
		"retry: 1500\r\n" +
		"data: first line\r\n" +
		"data:second line\r\n" +
		"\r\n" +
		"event: endpoint\n" +
		"data: /messages?session=abc\n" +
		"\n" +
		"data: trailing-without-blank-line"

	var events []sseEvent
	err := readSSE(strings.NewReader(stream), func(event sseEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(events), events)
	}

	first := events[0]
	if first.Event != "message" || first.ID != "42" || first.Retry != 1500*time.Millisecond {
		t.Errorf("first event metadata = %+v", first)
	}
	if got := string(first.Data); got != "first line\nsecond line" {
		t.Errorf("first data = %q", got)
	}
	if events[1].Event != "endpoint" || string(events[1].Data) != "/messages?session=abc" {
		t.Errorf("second event = %+v", events[1])
	}
	if string(events[2].Data) != "trailing-without-blank-line" {
		t.Errorf("third event = %+v", events[2])
	}
}

func TestReadSSEStopsOnCallbackError(t *testing.T) {
	sentinel := errors.New("stop")
	count := 0
	err := readSSE(strings.NewReader("data: one\n\ndata: two\n\n"), func(sseEvent) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("readSSE error = %v, want sentinel", err)
	}
	if count != 1 {
		t.Errorf("callback count = %d, want 1", count)
	}
}

func TestReadSSEEmptyDataEvent(t *testing.T) {
	var events []sseEvent
	err := readSSE(strings.NewReader("event: message\ndata:\n\n"), func(event sseEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE: %v", err)
	}
	if len(events) != 1 || len(events[0].Data) != 0 {
		t.Fatalf("events = %+v, want one empty-data event", events)
	}
}

func TestReadSSEReaderError(t *testing.T) {
	reader := io.MultiReader(strings.NewReader("data: x\n"), errReader{errors.New("broken")})
	err := readSSE(reader, func(sseEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("readSSE error = %v, want broken pipe", err)
	}
}

func TestReadSSEOversizedLineIsBounded(t *testing.T) {
	// More than one full line limit, with no delimiter anywhere. A reader that
	// accumulates the whole payload (or an unbounded ReadString) would consume
	// all of it before noticing the limit.
	reader := &countingReader{remaining: maxSSELine + (1 << 20)}
	events := 0
	err := readSSE(reader, func(sseEvent) error {
		events++
		return nil
	})
	if err == nil {
		t.Fatal("readSSE succeeded, want line-limit error")
	}
	if !strings.Contains(err.Error(), "sse line exceeds") {
		t.Fatalf("readSSE error = %v, want line-limit error", err)
	}
	if events != 0 {
		t.Errorf("readSSE emitted %d events, want 0", events)
	}

	const readAllowance = 64 << 10
	if reader.read > maxSSELine+readAllowance {
		t.Errorf("readSSE consumed %d bytes, want at most %d", reader.read, maxSSELine+readAllowance)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

type countingReader struct {
	remaining int
	read      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}
