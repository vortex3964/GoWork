package bashtool

import (
	"strings"
	"testing"
)

func TestLockedBufferKeepsSmallOutputUnchanged(t *testing.T) {
	const small = "hello\nworld\n"

	var buf lockedBuffer
	n, err := buf.Write([]byte(small))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(small) {
		t.Fatalf("Write returned %d, want %d", n, len(small))
	}
	if got := buf.String(); got != small {
		t.Errorf("small output changed:\ngot:  %q\nwant: %q", got, small)
	}
}

func TestLockedBufferCapsSingleLargeWrite(t *testing.T) {
	big := strings.Repeat("x", maxBufferedOutput*10)

	var buf lockedBuffer
	n, err := buf.Write([]byte(big))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(big) {
		t.Fatalf("Write returned %d, want %d (it must accept the whole input)", n, len(big))
	}

	got := buf.String()
	if len(got) > maxBufferedOutput {
		t.Fatalf("buffer grew to %d bytes, cap is %d", len(got), maxBufferedOutput)
	}
	if !strings.Contains(got, "chars truncated") {
		t.Errorf("expected a truncation marker, got %d bytes without one", len(got))
	}
	if !strings.HasPrefix(got, "xxxx") || !strings.HasSuffix(got, "xxxx") {
		t.Errorf("expected head and tail of the stream to be preserved")
	}
}

func TestLockedBufferCapsChunkedWrites(t *testing.T) {
	var buf lockedBuffer
	chunk := []byte(strings.Repeat("y", 4096))
	for i := 0; i < 256; i++ { // 1 MiB total, far past the cap
		if _, err := buf.Write(chunk); err != nil {
			t.Fatalf("Write chunk %d: %v", i, err)
		}
	}

	got := buf.String()
	if len(got) > maxBufferedOutput {
		t.Fatalf("buffer grew to %d bytes after chunked writes, cap is %d", len(got), maxBufferedOutput)
	}
	if !strings.Contains(got, "chars truncated") {
		t.Errorf("expected a truncation marker after chunked writes, got %d bytes without one", len(got))
	}
}

func TestLockedBufferTruncationIndependentOfWriteSize(t *testing.T) {
	content := strings.Repeat("abcdefghij", maxBufferedOutput)

	var whole lockedBuffer
	if _, err := whole.Write([]byte(content)); err != nil {
		t.Fatalf("whole Write: %v", err)
	}

	var chunked lockedBuffer
	const chunkSize = 997
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		if _, err := chunked.Write([]byte(content[i:end])); err != nil {
			t.Fatalf("chunked Write at %d: %v", i, err)
		}
	}

	if got, want := chunked.String(), whole.String(); got != want {
		t.Errorf("truncated output depends on Write chunk sizes:\nchunked: %d bytes\nwhole:   %d bytes", len(got), len(want))
	}
}
