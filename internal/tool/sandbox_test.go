package tool

import "testing"

func TestParseSandboxMode(t *testing.T) {
	for _, want := range []SandboxMode{SandboxOff, SandboxAuto, SandboxRequired} {
		got, err := ParseSandboxMode(string(want))
		if err != nil || got != want {
			t.Fatalf("ParseSandboxMode(%q) = %q, %v", want, got, err)
		}
	}
	if _, err := ParseSandboxMode("unsafe"); err == nil {
		t.Fatal("unknown sandbox mode was accepted")
	}
}

func TestSelectSandboxFailsClosedWhenRequiredUnavailable(t *testing.T) {
	decision, err := SelectSandbox(SandboxRequired, SandboxCapabilities{})
	if err == nil || decision.Available {
		t.Fatalf("required unavailable decision=%+v err=%v", decision, err)
	}
}

func TestSelectSandboxAutoReportsUnavailableWithoutBlocking(t *testing.T) {
	decision, err := SelectSandbox(SandboxAuto, SandboxCapabilities{})
	if err != nil || decision.Available || decision.Mode != SandboxAuto {
		t.Fatalf("auto decision=%+v err=%v", decision, err)
	}
}

func TestSelectSandboxUsesReliableBackend(t *testing.T) {
	decision, err := SelectSandbox(SandboxRequired, SandboxCapabilities{Backend: "test", Reliable: true, Capabilities: []string{"filesystem"}})
	if err != nil || !decision.Available || decision.Backend != "test" {
		t.Fatalf("available decision=%+v err=%v", decision, err)
	}
}
