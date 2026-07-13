package commandtool

import (
	"strings"
	"testing"
)

func TestHeadTailBufferKeepsSmallOutput(t *testing.T) {
	buffer := newHeadTailBuffer(16)
	_, _ = buffer.Write([]byte("hello world"))
	if got := buffer.String(); got != "hello world" {
		t.Fatalf("String() = %q", got)
	}
	if buffer.Truncated() {
		t.Fatal("small output was truncated")
	}
}

func TestHeadTailBufferKeepsPrefixAndSuffix(t *testing.T) {
	buffer := newHeadTailBuffer(10)
	_, _ = buffer.Write([]byte("0123456789abcdefghij"))
	got := buffer.String()
	if !strings.HasPrefix(got, "01234") || !strings.HasSuffix(got, "fghij") {
		t.Fatalf("String() = %q", got)
	}
	if !strings.Contains(got, "output truncated") || !buffer.Truncated() {
		t.Fatalf("missing truncation metadata: %q", got)
	}
}

func TestHeadTailBufferKeepsLatestSuffixAcrossWrites(t *testing.T) {
	buffer := newHeadTailBuffer(8)
	for _, part := range []string{"abcd", "efgh", "ijkl"} {
		_, _ = buffer.Write([]byte(part))
	}
	got := buffer.String()
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "ijkl") {
		t.Fatalf("String() = %q", got)
	}
}
