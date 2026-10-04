package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCopiesOnlyValidatedSkillFileAndRemoveDeletesIt(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# local skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "run.exe"), []byte("not copied"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(Manifest{Name: "local-skill", Version: "1.2.3", Description: "demo"})
	if err := os.WriteFile(filepath.Join(source, "skill.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, source, "local-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "skills", "local-skill", "run.exe")); !os.IsNotExist(err) {
		t.Fatalf("unexpected copied payload: %v", err)
	}
	if _, err := Load(root, "local-skill"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root, "local-skill")
	if err != nil || loaded.Version != "1.2.3" {
		t.Fatalf("manifest not loaded: %#v %v", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "audits", "skill-lifecycle.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, "local-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "skills", "local-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill still exists: %v", err)
	}
}

func TestEnableDisableDoesNotRemoveSkill(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(root, source, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(root, "demo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "demo"); err == nil {
		t.Fatal("disabled skill loaded")
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(root, "demo", true); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "demo"); err != nil {
		t.Fatal(err)
	}
}
