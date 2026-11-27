package gowork

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLocalServerHelperProcess is not a real test: it is the fake local server
// binary. The manager starts this same test executable with
// -test.run=TestLocalServerHelperProcess; the helper writes a PID marker and
// blocks until it gets SIGTERM, which lets the tests prove startup, stop and
// exit cleanup actually terminate the child.
func TestLocalServerHelperProcess(t *testing.T) {
	if os.Getenv("GOWORK_LOCAL_SERVER_HELPER") != "1" {
		return
	}

	marker := os.Getenv("GOWORK_FAKE_PID_FILE")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, os.Interrupt)
	if marker != "" {
		_ = os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	<-sigCh
	if marker != "" {
		_ = os.Remove(marker)
	}
	os.Exit(0)
}

const localServerHelperArgs = `["-test.run=TestLocalServerHelperProcess"]`

func fakeHealthServer(t *testing.T, path string, ready func() bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if ready() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "loading model", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setFakeLocalServerEnv(t *testing.T, provider, baseURL string) {
	t.Helper()
	t.Setenv("GOWORK_LOCAL_SERVER_HELPER", "1")
	switch provider {
	case "llamacpp":
		t.Setenv("LLAMACPP_COMMAND", os.Args[0])
		t.Setenv("LLAMACPP_ARGS", localServerHelperArgs)
		t.Setenv("LLAMACPP_HOST", baseURL)
	case "lmstudio":
		t.Setenv("LMSTUDIO_COMMAND", os.Args[0])
		t.Setenv("LMSTUDIO_ARGS", localServerHelperArgs)
		t.Setenv("LMSTUDIO_HOST", baseURL)
	default:
		t.Fatalf("unknown provider %q", provider)
	}
}

func newTestLocalServerManager(t *testing.T) *localServerManager {
	t.Helper()
	mgr := newLocalServerManager()
	mgr.startTimeout = time.Second
	mgr.pollInterval = 10 * time.Millisecond
	mgr.stopGrace = 500 * time.Millisecond
	mgr.healthRequestTimeout = 300 * time.Millisecond
	mgr.probeTimeout = 100 * time.Millisecond
	t.Cleanup(mgr.StopAll)
	return mgr
}

func waitForFileGone(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// taskkill /F does not give the helper a chance to remove the marker.
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake server marker %s still exists; process was not cleaned up", path)
}

func TestLocalServerStartSuccess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "llama.pid")
	srv := fakeHealthServer(t, "/health", func() bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	t.Setenv("GOWORK_FAKE_PID_FILE", marker)
	setFakeLocalServerEnv(t, "llamacpp", srv.URL)
	mgr := newTestLocalServerManager(t)

	st, err := mgr.Start(context.Background(), "llamaCpp")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !st.Running || !st.Managed {
		t.Fatalf("status after start = %+v, want running+managed", st)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fake server was not started: %v", err)
	}
	pidBefore, _ := os.ReadFile(marker)

	// Start is idempotent while the managed process is alive: it must not
	// spawn a second server.
	st, err = mgr.Start(context.Background(), "llamaCpp")
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if !st.Running || !st.Managed {
		t.Fatalf("second status = %+v, want running+managed", st)
	}
	pidAfter, _ := os.ReadFile(marker)
	if string(pidBefore) != string(pidAfter) {
		t.Fatalf("second Start replaced the process (pid %s -> %s)", pidBefore, pidAfter)
	}

	label, ok := mgr.StatusLabel("llamaCpp")
	if !ok || !strings.Contains(label, "managed by GoWork") {
		t.Fatalf("StatusLabel = %q, %v", label, ok)
	}

	mgr.StopAll()
	waitForFileGone(t, marker)
	if st := mgr.Status("llamaCpp"); st.Running {
		t.Fatalf("status after StopAll = %+v, want stopped", st)
	}
}

func TestLocalServerStartAlreadyRunning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "llama.pid")
	srv := fakeHealthServer(t, "/health", func() bool { return true })
	t.Setenv("GOWORK_FAKE_PID_FILE", marker)
	setFakeLocalServerEnv(t, "llamacpp", srv.URL)
	mgr := newTestLocalServerManager(t)

	st, err := mgr.Start(context.Background(), "llamaCpp")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !st.Running || st.Managed {
		t.Fatalf("status = %+v, want external running", st)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Start spawned a process even though the endpoint was ready (marker err=%v)", err)
	}
	label, _ := mgr.StatusLabel("llamaCpp")
	if !strings.Contains(label, "external") {
		t.Fatalf("StatusLabel = %q, want external", label)
	}
}

func TestLocalServerStartTimeout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "llama.pid")
	srv := fakeHealthServer(t, "/health", func() bool { return false })
	t.Setenv("GOWORK_FAKE_PID_FILE", marker)
	setFakeLocalServerEnv(t, "llamacpp", srv.URL)
	mgr := newTestLocalServerManager(t)
	mgr.startTimeout = 1200 * time.Millisecond

	st, err := mgr.Start(context.Background(), "llamaCpp")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Start error = %v, want timeout", err)
	}
	if st.Running {
		t.Fatalf("status after timeout = %+v, want not running", st)
	}
	mgr.mu.Lock()
	remaining := len(mgr.servers)
	mgr.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d server(s) still tracked after startup timeout", remaining)
	}
	waitForFileGone(t, marker)
}

func TestLocalServerStop(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "llama.pid")
	srv := fakeHealthServer(t, "/health", func() bool {
		_, err := os.Stat(marker)
		return err == nil
	})
	t.Setenv("GOWORK_FAKE_PID_FILE", marker)
	setFakeLocalServerEnv(t, "llamacpp", srv.URL)
	mgr := newTestLocalServerManager(t)

	if _, err := mgr.Start(context.Background(), "llamaCpp"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, err := mgr.Stop(context.Background(), "llamaCpp")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st.Running {
		t.Fatalf("status after stop = %+v, want stopped", st)
	}
	waitForFileGone(t, marker)

	// Stop is idempotent once the managed process is gone; the still-open
	// httptest endpoint now reports 503 because the marker is gone, so this
	// must not be mistaken for an external server.
	if _, err := mgr.Stop(context.Background(), "llamaCpp"); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	mgr.mu.Lock()
	remaining := len(mgr.servers)
	mgr.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d server(s) still tracked after stop", remaining)
	}
}

func TestLocalServerStopAllCleansEveryProvider(t *testing.T) {
	dir := t.TempDir()
	llamaMarker := filepath.Join(dir, "llama.pid")
	lmMarker := filepath.Join(dir, "lmstudio.pid")

	llamaSrv := fakeHealthServer(t, "/health", func() bool {
		_, err := os.Stat(llamaMarker)
		return err == nil
	})
	lmSrv := fakeHealthServer(t, "/v1/models", func() bool {
		_, err := os.Stat(lmMarker)
		return err == nil
	})

	setFakeLocalServerEnv(t, "llamacpp", llamaSrv.URL)
	setFakeLocalServerEnv(t, "lmstudio", lmSrv.URL+"/v1")
	mgr := newTestLocalServerManager(t)

	t.Setenv("GOWORK_FAKE_PID_FILE", llamaMarker)
	if _, err := mgr.Start(context.Background(), "llamaCpp"); err != nil {
		t.Fatalf("start llama.cpp: %v", err)
	}
	// The next child inherits the updated marker; this is how the test gives
	// each fake server its own PID file.
	t.Setenv("GOWORK_FAKE_PID_FILE", lmMarker)
	if _, err := mgr.Start(context.Background(), "lmStudio"); err != nil {
		t.Fatalf("start LM Studio: %v", err)
	}

	mgr.StopAll()

	waitForFileGone(t, llamaMarker)
	waitForFileGone(t, lmMarker)
	for _, provider := range []string{"llamaCpp", "lmStudio"} {
		if st := mgr.Status(provider); st.Running {
			t.Fatalf("status for %s after StopAll = %+v, want stopped", provider, st)
		}
	}
}

func TestLocalServerNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LLAMACPP_COMMAND", "")
	mgr := newTestLocalServerManager(t)

	label, ok := mgr.StatusLabel("llamaCpp")
	if !ok || !strings.Contains(label, "not installed") {
		t.Fatalf("StatusLabel = %q, %v; want not installed", label, ok)
	}
	_, err := mgr.Start(context.Background(), "llamaCpp")
	if err == nil || !strings.Contains(err.Error(), "LLAMACPP_COMMAND") {
		t.Fatalf("Start error = %v, want missing-binary guidance", err)
	}
}

// TestLocalServerServiceLauncher covers LM Studio's `lms server start` shape:
// the CLI exits successfully after handing off to a daemon, readiness is then
// detected on the model endpoint, and stop goes through the CLI's stop
// command rather than a child process tree.
func TestLocalServerServiceLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake service CLI")
	}

	dir := t.TempDir()
	readyMarker := filepath.Join(dir, "service-ready")
	stopMarker := filepath.Join(dir, "service-stopped")
	script := filepath.Join(dir, "lms")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = server ] && [ \"$2\" = start ]; then : > \"$LOCAL_TEST_READY_MARKER\"; exit 0; fi\n" +
		"if [ \"$1\" = server ] && [ \"$2\" = stop ]; then : > \"$LOCAL_TEST_STOP_MARKER\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := fakeHealthServer(t, "/v1/models", func() bool {
		_, err := os.Stat(readyMarker)
		return err == nil
	})

	t.Setenv("LOCAL_TEST_READY_MARKER", readyMarker)
	t.Setenv("LOCAL_TEST_STOP_MARKER", stopMarker)
	t.Setenv("LMSTUDIO_COMMAND", script)
	t.Setenv("LMSTUDIO_ARGS", "server start")
	t.Setenv("LMSTUDIO_HOST", srv.URL+"/v1")
	mgr := newTestLocalServerManager(t)

	st, err := mgr.Start(context.Background(), "lmStudio")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !st.Running || !st.Managed {
		t.Fatalf("status = %+v, want managed running service", st)
	}
	if _, err := os.Stat(readyMarker); err != nil {
		t.Fatalf("service CLI did not record readiness: %v", err)
	}

	if _, err := mgr.Stop(context.Background(), "lmStudio"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(stopMarker); err != nil {
		t.Fatalf("Stop did not run the service CLI's stop command: %v", err)
	}
}

func TestLocalServerExternalStopIsNoop(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "never-created.pid")
	srv := fakeHealthServer(t, "/health", func() bool { return true })
	t.Setenv("GOWORK_FAKE_PID_FILE", marker)
	setFakeLocalServerEnv(t, "llamacpp", srv.URL)
	mgr := newTestLocalServerManager(t)

	st, err := mgr.Stop(context.Background(), "llamaCpp")
	if err != nil {
		t.Fatalf("Stop external: %v", err)
	}
	if !st.Running || st.Managed {
		t.Fatalf("status = %+v, want external running", st)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Stop launched a process (marker err=%v)", err)
	}
}

func TestLocalServerManagerStopAllIsIdempotent(t *testing.T) {
	mgr := newTestLocalServerManager(t)
	mgr.StopAll()
	mgr.StopAll()
	if got := fmt.Sprint(mgr.servers); got != "map[]" {
		t.Fatalf("servers = %s, want empty map", got)
	}
}
