package app

import (
	"strings"
	"testing"
)

func TestRenderDriftLogoUsesDynamicWorkspace(t *testing.T) {
	got := renderDriftLogo("F:/work/project")
	for _, want := range []string{"DRIFT", "Coding Agent Runtime", "F:/work/project", "\x1b[38;2;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logo missing %q", want)
		}
	}
}
