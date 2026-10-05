package transport

import (
	"io"
	"strings"
	"testing"
)

func TestSSESanitizerDropsMalformedFramesAndPreservesDone(t *testing.T) {
	input := strings.Join([]string{
		"data: {}", "",
		"data:", "",
		"data: {broken", "",
		"data: {\"ok\":true}", "",
		"data: [DONE]", "",
	}, "\n")
	s := NewSSESanitizer(strings.NewReader(input))
	output, err := io.ReadAll(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "data: {}\n\ndata: {\"ok\":true}\n\ndata: [DONE]\n\n" {
		t.Fatalf("output = %q", output)
	}
	if s.DroppedFrames() != 2 {
		t.Fatalf("dropped = %d, want 2", s.DroppedFrames())
	}
}

func TestSSESanitizerDropsUnterminatedFrame(t *testing.T) {
	s := NewSSESanitizer(strings.NewReader("data: {\"partial\":"))
	if _, err := io.ReadAll(s); err != nil {
		t.Fatal(err)
	}
	if s.DroppedFrames() != 1 {
		t.Fatalf("dropped = %d, want 1 for unterminated frame", s.DroppedFrames())
	}
}
