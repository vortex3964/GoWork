package lsp

import (
	"os"
	"testing"

	"GoWork/internal/lsp/lspfake"
)

// These aliases keep the client tests' environment setup readable.
const (
	fakeServerEnv      = lspfake.Env
	fakeSleepMethodEnv = lspfake.SleepMethodEnv
)

// TestMain doubles as the fake language-server entry point. Client tests
// re-exec this test binary with fakeServerEnv set; lspfake then speaks LSP over
// stdin/stdout instead of running tests.
func TestMain(m *testing.M) {
	if lspfake.MaybeRun() {
		return
	}
	os.Exit(m.Run())
}
