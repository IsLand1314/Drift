package app

import (
	"strings"
	"testing"
)

func TestFixedFooterSequencesReserveTerminalRows(t *testing.T) {
	begin := fixedFooterBeginSequence(80, 24, 21, "deepseek-v4-flash")
	if !strings.Contains(begin, "\x1b[1;21r") {
		t.Fatalf("missing scroll region: %q", begin)
	}
	if strings.Contains(begin, "\x1b[21;1H") {
		t.Fatalf("footer must preserve submitted-message cursor, not jump to content bottom: %q", begin)
	}
	if !strings.Contains(begin, "Enter 发送 · Ctrl+C 取消") || !strings.Contains(begin, "deepseek-v4-flash") {
		t.Fatalf("footer content missing: %q", begin)
	}
	if got := fixedFooterEndSequence(21); !strings.Contains(got, "\x1b[r") || !strings.Contains(got, "\x1b8") {
		t.Fatalf("invalid footer cleanup: %q", got)
	}
}

func TestFixedFooterBeginSequenceDropsModelWhenTooWide(t *testing.T) {
	begin := fixedFooterBeginSequence(20, 24, 21, "very-long-model-name")
	if strings.Contains(begin, "very-long-model-name") {
		t.Fatal("over-wide model should not overlap footer help")
	}
}
