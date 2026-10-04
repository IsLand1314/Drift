package skill

import (
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
	if err := Install(root, source, "local-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "skills", "local-skill", "run.exe")); !os.IsNotExist(err) {
		t.Fatalf("unexpected copied payload: %v", err)
	}
	if _, err := Load(root, "local-skill"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, "local-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".drift", "skills", "local-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill still exists: %v", err)
	}
}
