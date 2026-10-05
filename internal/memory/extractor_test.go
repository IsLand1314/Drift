package memory

import "testing"

func TestExtractCandidatesRequiresExplicitPreferenceOrCorrection(t *testing.T) {
	if got := ExtractCandidates("请帮我读取 README"); len(got) != 0 {
		t.Fatalf("ordinary request produced candidates: %+v", got)
	}
	got := ExtractCandidates("以后：默认使用中文回答")
	if len(got) != 1 || got[0].Status != "candidate" || got[0].Source != "auto:user_turn" {
		t.Fatalf("candidate=%+v", got)
	}
}

func TestExtractCandidatesRejectsSecretsAndOnlyUsesUserText(t *testing.T) {
	if got := ExtractCandidates("以后：api_key=secret-value"); len(got) != 0 {
		t.Fatalf("secret candidate=%+v", got)
	}
	if got := ExtractCandidates("更正：不要再把 /absolute/path 写入记忆"); len(got) != 0 {
		t.Fatalf("absolute path candidate=%+v", got)
	}
}
