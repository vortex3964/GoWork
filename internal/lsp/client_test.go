package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startFakeClient(t *testing.T, extraEnv map[string]string, timeout time.Duration, onDiagnostics func(PublishDiagnosticsParams)) *Client {
	t.Helper()
	env := map[string]string{fakeServerEnv: "1"}
	for key, value := range extraEnv {
		env[key] = value
	}
	c, err := Start(Options{
		Command:        os.Args[0],
		Env:            env,
		RequestTimeout: timeout,
		OnDiagnostics:  onDiagnostics,
	})
	if err != nil {
		t.Fatalf("Start fake server: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return c
}

func waitForDiagnostics(t *testing.T, c *Client, path string) []Diagnostic {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if diagnostics := c.Diagnostics(path); len(diagnostics) > 0 {
			return diagnostics
		}
		if time.Now().After(deadline) {
			t.Fatalf("no diagnostics captured for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitDone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("client read loop did not stop")
	}
}

func TestClientInitializeDocumentSymbolsDefinitionAndDiagnostics(t *testing.T) {
	diagCh := make(chan PublishDiagnosticsParams, 2)
	c := startFakeClient(t, nil, 2*time.Second, func(params PublishDiagnosticsParams) {
		select {
		case diagCh <- params:
		default:
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	initResult, err := c.Initialize(ctx, root)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !c.Initialized() {
		t.Fatal("Initialized() = false after a successful handshake")
	}
	if initResult.ServerInfo == nil || initResult.ServerInfo.Name != "fake-lsp" {
		t.Fatalf("server info = %+v, want fake-lsp", initResult.ServerInfo)
	}
	if len(initResult.Capabilities.DocumentSymbolProvider) == 0 {
		t.Fatal("documentSymbolProvider capability was not decoded")
	}

	path := filepath.Join(root, "main.go")
	if err := c.DidOpen(ctx, path, "go", "package main\n\nfunc main() {}\n"); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	diagnostics := waitForDiagnostics(t, c, path)
	if len(diagnostics) != 1 || diagnostics[0].Message != "fake diagnostic" {
		t.Fatalf("diagnostics = %+v, want one fake diagnostic", diagnostics)
	}
	if diagnostics[0].Range.Start.Line != 1 || diagnostics[0].Range.Start.Character != 2 {
		t.Fatalf("diagnostic range = %+v", diagnostics[0].Range)
	}

	select {
	case params := <-diagCh:
		if len(params.Diagnostics) != 1 {
			t.Fatalf("callback diagnostics = %+v", params.Diagnostics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnDiagnostics callback was not invoked")
	}

	symbols, err := c.DocumentSymbols(ctx, path)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "main" {
		t.Fatalf("symbols = %+v, want main", symbols)
	}
	if symbols[0].Range == nil || symbols[0].SelectionRange == nil {
		t.Fatalf("symbol ranges were not decoded: %+v", symbols[0])
	}

	locations, err := c.Definition(ctx, path, 1, 2)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(locations) != 1 {
		t.Fatalf("locations = %+v, want 1", locations)
	}
	if locations[0].Range.Start.Line != 1 || locations[0].Range.Start.Character != 2 {
		t.Fatalf("definition range = %+v", locations[0].Range)
	}
	if locations[0].URI != documentURI(path) {
		t.Fatalf("definition URI = %q, want %q", locations[0].URI, documentURI(path))
	}

	// The handshake is single-shot.
	if _, err := c.Initialize(ctx, root); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Initialize = %v, want ErrAlreadyInitialized", err)
	}

	if err := c.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	waitDone(t, c)
	if err := c.Close(); err != nil {
		t.Fatalf("Close after Shutdown: %v", err)
	}

	// Requests after shutdown fail clearly instead of hanging.
	if _, err := c.DocumentSymbols(ctx, path); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("request after Close = %v, want ErrClientClosed", err)
	}
}

func TestClientRequestTimeout(t *testing.T) {
	c := startFakeClient(t, map[string]string{
		fakeSleepMethodEnv: "textDocument/documentSymbol",
	}, 150*time.Millisecond, nil)

	ctx := context.Background()
	if _, err := c.Initialize(ctx, t.TempDir()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	start := time.Now()
	_, err := c.DocumentSymbols(ctx, filepath.Join(t.TempDir(), "slow.go"))
	if err == nil {
		t.Fatal("DocumentSymbols succeeded, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("request took %v, want it bounded by the request timeout", elapsed)
	}

	// Close must kill the still-sleeping server and unblock the read loop.
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitDone(t, c)
}

func TestClientContextCancellation(t *testing.T) {
	c := startFakeClient(t, map[string]string{
		fakeSleepMethodEnv: "textDocument/documentSymbol",
	}, 5*time.Second, nil)
	if _, err := c.Initialize(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.DocumentSymbols(ctx, "canceled.go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DocumentSymbols = %v, want context.Canceled", err)
	}
}

func TestStartMissingCommand(t *testing.T) {
	_, err := Start(Options{Command: filepath.Join(t.TempDir(), "does-not-exist")})
	if err == nil {
		t.Fatal("Start succeeded for a missing command")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Fatalf("error = %v, want a clear start error", err)
	}
	if _, err := Start(Options{}); err == nil {
		t.Fatal("Start with an empty command should error")
	}
}

func TestDecodeLocationShapes(t *testing.T) {
	locationJSON := `[{"uri":"file:///tmp/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":8}}}]`
	locs, err := decodeLocations(json.RawMessage(locationJSON))
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///tmp/a.go" {
		t.Fatalf("array locations = %+v, err %v", locs, err)
	}

	singleJSON := `{"uri":"file:///tmp/b.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`
	locs, err = decodeLocations(json.RawMessage(singleJSON))
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///tmp/b.go" {
		t.Fatalf("single location = %+v, err %v", locs, err)
	}

	linkJSON := `[{"targetUri":"file:///tmp/c.go","targetRange":{"start":{"line":3,"character":4},"end":{"line":3,"character":9}},"targetSelectionRange":{"start":{"line":3,"character":4},"end":{"line":3,"character":6}}}]`
	locs, err = decodeLocations(json.RawMessage(linkJSON))
	if err != nil || len(locs) != 1 || locs[0].URI != "file:///tmp/c.go" {
		t.Fatalf("location links = %+v, err %v", locs, err)
	}
	if locs[0].Range.Start.Line != 3 || locs[0].Range.Start.Character != 4 {
		t.Fatalf("link range = %+v", locs[0].Range)
	}

	if locs, err := decodeLocations(json.RawMessage(`null`)); err != nil || locs != nil {
		t.Fatalf("null locations = %+v, err %v", locs, err)
	}
}

func TestClientDiagnosticsAreCopied(t *testing.T) {
	c := &Client{diagnostics: map[string][]Diagnostic{
		PathToURI("/tmp/copy.go"): {{
			Message: "original",
			Code:    json.RawMessage(`"E1"`),
			Tags:    []int{1},
		}},
	}}
	got := c.Diagnostics("/tmp/copy.go")
	if len(got) != 1 || got[0].Message != "original" {
		t.Fatalf("Diagnostics = %+v", got)
	}
	got[0].Message = "mutated"
	got[0].Code[0] = 'X'
	got[0].Tags[0] = 99
	again := c.Diagnostics("/tmp/copy.go")
	if again[0].Message != "original" || string(again[0].Code) != `"E1"` || again[0].Tags[0] != 1 {
		t.Fatalf("Diagnostics exposed internal state: %+v", again[0])
	}
}
