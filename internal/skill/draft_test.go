package skill

import (
	"strings"
	"testing"
)

func TestDraftFromMemoryIsReviewOnly(t *testing.T) {
	draft, err := DraftFromMemory("go-style", "默认使用 gofmt")
	if err != nil || !strings.Contains(draft, "Verified experience") || !strings.Contains(draft, "Draft only") {
		t.Fatalf("draft=%q err=%v", draft, err)
	}
	if _, err := DraftFromMemory("../bad", "x"); err == nil {
		t.Fatal("accepted unsafe skill name")
	}
}
