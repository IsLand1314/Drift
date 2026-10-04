package memory

import (
	"strings"
	"testing"
)

func TestStoreRememberReplaceDeleteAndPrompt(t *testing.T) {
	store := New(nil)
	if err := store.Remember(KindFact, "项目使用 Go", "session/turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Remember(KindFact, "项目使用 Go", "session/turn-2"); err != nil {
		t.Fatal(err)
	}
	if len(store.Items()) != 1 || !strings.Contains(store.PromptText(), "项目使用 Go") {
		t.Fatalf("items=%+v prompt=%q", store.Items(), store.PromptText())
	}
	if err := store.Replace(KindFact, "项目使用 Rust", "session/turn-3"); err != nil {
		t.Fatal(err)
	}
	if len(store.Items()) != 1 || store.Items()[0].Text != "项目使用 Rust" {
		t.Fatalf("replace items=%+v", store.Items())
	}
	store.Delete(KindFact, "项目使用 Rust")
	if len(store.Items()) != 0 {
		t.Fatalf("delete items=%+v", store.Items())
	}
}

func TestStoreRejectsSecretsAbsolutePathsAndOversizedText(t *testing.T) {
	store := New(nil)
	for _, text := range []string{
		"api_key=secret",
		`C:\workspace\secret.txt`,
		strings.Repeat("x", MaxBytes+1),
	} {
		if err := store.Remember(KindFact, text, "session/turn-1"); err != ErrInvalidItem {
			t.Fatalf("Remember(%q) error=%v, want ErrInvalidItem", text, err)
		}
	}
}
