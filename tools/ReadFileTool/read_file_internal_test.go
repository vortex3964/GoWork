package readfiletool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingReader counts how many bytes scanLineRange actually pulls from the
// underlying stream.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TestScanLineRangeStopsBeforeEOF proves a small page from a large stream
// does not read the whole file.
func TestScanLineRangeStopsBeforeEOF(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 200_000; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	all := sb.String()

	cr := &countingReader{r: strings.NewReader(all)}
	content, truncated, ok, _, err := scanLineRange(cr, "big.txt", 100, 5)
	if err != nil {
		t.Fatalf("scanLineRange: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}

	want := ""
	for i := 100; i < 105; i++ {
		want += fmt.Sprintf("%d.line %d\n", i, i)
	}
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}

	// A 64KB scanner buffer is enough for this page. Anything close to the
	// total stream size means the implementation read to EOF.
	if cr.n >= int64(len(all))/4 {
		t.Errorf("read %d of %d bytes for a 5-line page; expected a bounded read", cr.n, len(all))
	}
	if cr.n > 256*1024 {
		t.Errorf("read %d bytes for a 5-line page, want <= 256KB", cr.n)
	}
}

// TestScanLineRangeByteCapStopsEarly proves the byte cap also terminates the
// stream without consuming the rest of it.
func TestScanLineRangeByteCapStopsEarly(t *testing.T) {
	var sb strings.Builder
	const lines = 20_000
	for i := 1; i <= lines; i++ {
		sb.WriteString(strings.Repeat("x", 100))
		sb.WriteByte('\n')
	}
	all := sb.String()

	cr := &countingReader{r: strings.NewReader(all)}
	content, truncated, ok, _, err := scanLineRange(cr, "big.txt", 1, DEFAULT_MAX_LINES)
	if err != nil {
		t.Fatalf("scanLineRange: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !truncated {
		t.Fatal("expected the byte cap to truncate the page")
	}
	if len(content) > DEFAULT_MAX_BYTES {
		t.Errorf("content length = %d, want <= %d", len(content), DEFAULT_MAX_BYTES)
	}
	if cr.n >= int64(len(all))/2 {
		t.Errorf("read %d of %d bytes after hitting the byte cap; expected an early stop", cr.n, len(all))
	}
}

// TestScanLineRangePastEOFReportsLineCount keeps the "past end of file"
// error message accurate: the whole stream is consumed only when the caller
// asks for a line that never arrives.
func TestScanLineRangePastEOFReportsLineCount(t *testing.T) {
	cr := &countingReader{r: strings.NewReader("a\nb\nc\n")}
	_, _, ok, lastLine, err := scanLineRange(cr, "small.txt", 10, 1)
	if err != nil {
		t.Fatalf("scanLineRange: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a start past EOF")
	}
	if lastLine != 3 {
		t.Errorf("lastLine = %d, want 3", lastLine)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindPDFPythonRefusesProjectVenvByDefault(t *testing.T) {
	appRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectVenv := filepath.Join(projectRoot, "tools", "Scripts", "venv", "bin", "python")
	writeExecutable(t, projectVenv)

	// No system Python either, so the only candidate would be the project venv.
	t.Setenv("PATH", t.TempDir())

	if got := findPDFPython(appRoot, projectRoot, false); got != "" {
		t.Fatalf("findPDFPython used project venv %q without opt-in", got)
	}
	if got := findPDFPython(appRoot, projectRoot, true); got != projectVenv {
		t.Fatalf("findPDFPython = %q, want opted-in project venv %q", got, projectVenv)
	}
}

func TestFindPDFPythonPrefersAppVenvAndSystem(t *testing.T) {
	appRoot := t.TempDir()
	projectRoot := t.TempDir()
	appVenv := filepath.Join(appRoot, "tools", "Scripts", "venv", "bin", "python")
	projectVenv := filepath.Join(projectRoot, "tools", "Scripts", "venv", "bin", "python")
	writeExecutable(t, appVenv)
	writeExecutable(t, projectVenv)

	t.Setenv("PATH", t.TempDir())
	if got := findPDFPython(appRoot, projectRoot, true); got != appVenv {
		t.Fatalf("findPDFPython = %q, want app venv %q", got, appVenv)
	}

	// Without an app venv, an opted-in project venv still loses to system Python.
	noAppRoot := t.TempDir()
	bin := t.TempDir()
	systemPython := filepath.Join(bin, "python3")
	writeExecutable(t, systemPython)
	t.Setenv("PATH", bin)
	if got := findPDFPython(noAppRoot, projectRoot, true); got != systemPython {
		t.Fatalf("findPDFPython = %q, want system Python %q", got, systemPython)
	}
}

func TestFindPDFScriptRequiresOptInForProject(t *testing.T) {
	appRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectScript := filepath.Join(projectRoot, "tools", "Scripts", "read_pdf.py")
	if err := os.MkdirAll(filepath.Dir(projectScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectScript, []byte("# project parser\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, ok := findPDFScript(appRoot, projectRoot, false); ok {
		t.Fatalf("findPDFScript used project script %q without opt-in", got)
	}
	if got, ok := findPDFScript(appRoot, projectRoot, true); !ok || got != projectScript {
		t.Fatalf("findPDFScript = %q, %v; want opted-in project script %q", got, ok, projectScript)
	}

	appScript := filepath.Join(appRoot, "tools", "Scripts", "read_pdf.py")
	if err := os.MkdirAll(filepath.Dir(appScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appScript, []byte("# app parser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := findPDFScript(appRoot, projectRoot, true); !ok || got != appScript {
		t.Fatalf("findPDFScript = %q, %v; want app-root script %q", got, ok, appScript)
	}
}

func TestPDFProjectScriptsOptInValues(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(pdfProjectScriptsEnv, value)
		if !pdfProjectScriptsAllowed() {
			t.Errorf("pdfProjectScriptsAllowed() = false for %q, want true", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "off", "enabled"} {
		t.Setenv(pdfProjectScriptsEnv, value)
		if pdfProjectScriptsAllowed() {
			t.Errorf("pdfProjectScriptsAllowed() = true for %q, want false", value)
		}
	}
}

func TestCappedBufferBoundsOutput(t *testing.T) {
	b := newCappedBuffer(8)
	if _, err := b.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "01234567" {
		t.Fatalf("String() = %q, want first 8 bytes", got)
	}
	if !b.Truncated() {
		t.Fatal("expected truncated=true after overflow")
	}
	if _, err := b.Write([]byte("more")); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "01234567" {
		t.Fatalf("String() = %q after more writes, want the cap preserved", got)
	}
	if b.Len() != 8 {
		t.Fatalf("Len() = %d, want 8", b.Len())
	}
}
