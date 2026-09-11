package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnvParsesCommentsBlanksAndQuotedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	contents := "\n# comment\n KEY = value \nSINGLE='quoted value'\nDOUBLE=\"another value\"\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	want := map[string]string{"KEY": "value", "SINGLE": "quoted value", "DOUBLE": "another value"}
	if len(got) != len(want) {
		t.Fatalf("LoadDotEnv() = %#v, want %#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("LoadDotEnv()[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestLoadDotEnvRejectsMalformedLineWithLineNumberWithoutValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	const secret = "SECRET_VALUE"
	if err := os.WriteFile(path, []byte("GOOD=ok\n"+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDotEnv(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadDotEnv() error = %v, want line number and no value", err)
	}
}

func TestLoadDotEnvMissingFileReturnsEmptyMap(t *testing.T) {
	got, err := LoadDotEnv(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadDotEnv() = %#v, want empty map", got)
	}
}

func TestMergeLookupProcessValueWinsAndEmptyFallsBack(t *testing.T) {
	dotenv := map[string]string{"KEY": "from dotenv"}
	if got := MergeLookup(dotenv, func(string) string { return "from process" }, "KEY"); got != "from process" {
		t.Fatalf("MergeLookup() = %q, want process value", got)
	}
	if got := MergeLookup(dotenv, func(string) string { return "" }, "KEY"); got != "from dotenv" {
		t.Fatalf("MergeLookup() = %q, want dotenv value", got)
	}
}
