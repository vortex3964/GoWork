package providers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLocalServerArgs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", raw: "", want: nil},
		{name: "whitespace", raw: "  -m /models/qwen.gguf --ctx-size 8192 ", want: []string{"-m", "/models/qwen.gguf", "--ctx-size", "8192"}},
		{name: "json array", raw: `["-m","/models/my model.gguf","--port","8081"]`, want: []string{"-m", "/models/my model.gguf", "--port", "8081"}},
		{name: "invalid json falls back", raw: `[-m broken`, want: []string{"[-m", "broken"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseLocalServerArgs(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("ParseLocalServerArgs(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ParseLocalServerArgs(%q) = %#v, want %#v", tc.raw, got, tc.want)
				}
			}
		})
	}
}

func TestLocalServerSpecHealthURLs(t *testing.T) {
	t.Setenv("LLAMACPP_HOST", "http://127.0.0.1:7777/")
	t.Setenv("LMSTUDIO_HOST", "http://127.0.0.1:5555/v1/")

	llama := LlamaCppLaunchSpec()
	llamaURLs := llama.HealthURLs()
	wantLlama := []string{"http://127.0.0.1:7777/health", "http://127.0.0.1:7777/v1/models"}
	if len(llamaURLs) != len(wantLlama) {
		t.Fatalf("llama health URLs = %#v, want %#v", llamaURLs, wantLlama)
	}
	for i := range wantLlama {
		if llamaURLs[i] != wantLlama[i] {
			t.Fatalf("llama health URLs = %#v, want %#v", llamaURLs, wantLlama)
		}
	}

	lm := LMStudioLaunchSpec()
	lmURLs := lm.HealthURLs()
	if len(lmURLs) != 1 || lmURLs[0] != "http://127.0.0.1:5555/v1/models" {
		t.Fatalf("lm studio health URLs = %#v", lmURLs)
	}
}

func TestLocalServerSpecForAliases(t *testing.T) {
	for _, name := range []string{"llamaCpp", "llamacpp", "llama.cpp"} {
		spec, ok := LocalServerSpecFor(name)
		if !ok || spec.Provider != "llamacpp" {
			t.Fatalf("LocalServerSpecFor(%q) = %+v, %v", name, spec, ok)
		}
	}
	for _, name := range []string{"lmStudio", "lmstudio", "lm-studio"} {
		spec, ok := LocalServerSpecFor(name)
		if !ok || spec.Provider != "lmstudio" {
			t.Fatalf("LocalServerSpecFor(%q) = %+v, %v", name, spec, ok)
		}
	}
	if _, ok := LocalServerSpecFor("ollama"); ok {
		t.Fatal("ollama should not expose launch controls")
	}
}

func TestResolveCommandEnvOverrideAndArgs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-llama-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_COMMAND", bin)
	t.Setenv("LLAMACPP_ARGS", `["-m","/models/a b.gguf"]`)

	path, args, err := LlamaCppLaunchSpec().ResolveCommand()
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if path != bin {
		t.Fatalf("path = %q, want %q", path, bin)
	}
	if len(args) != 2 || args[0] != "-m" || args[1] != "/models/a b.gguf" {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveCommandDetectsPath(t *testing.T) {
	dir := t.TempDir()
	// Cover both Unix and Windows lookups because the same candidate list is
	// used everywhere.
	for _, name := range []string{"llama-server", "llama-server.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("LLAMACPP_COMMAND", "")

	path, _, err := LlamaCppLaunchSpec().ResolveCommand()
	if err != nil {
		t.Fatalf("ResolveCommand: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("detected path = %q, want inside %q", path, dir)
	}
}

func TestLocalServerStartTimeout(t *testing.T) {
	t.Setenv("LOCAL_SERVER_START_TIMEOUT", "45s")
	if got := LocalServerStartTimeout(); got != 45*time.Second {
		t.Fatalf("duration = %s, want 45s", got)
	}
	t.Setenv("LOCAL_SERVER_START_TIMEOUT", "30")
	if got := LocalServerStartTimeout(); got != 30*time.Second {
		t.Fatalf("seconds = %s, want 30s", got)
	}
	t.Setenv("LOCAL_SERVER_START_TIMEOUT", "nonsense")
	if got := LocalServerStartTimeout(); got != 2*time.Minute {
		t.Fatalf("fallback = %s, want 2m", got)
	}
}
