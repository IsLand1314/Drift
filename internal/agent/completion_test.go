package agent

import "github.com/IsLand1314/Drift/internal/llm"

type testCompletion struct {
	Assistant    llm.Message
	FinishReason string
	Usage        *llm.Usage
}
