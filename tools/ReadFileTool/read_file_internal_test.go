package readfiletool

import (
	"fmt"
	"io"
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
