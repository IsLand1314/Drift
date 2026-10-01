package app

import (
	"fmt"
	"io"

	"github.com/IsLand1314/Drift/internal/agent"
)

// newTraceSink 将 Runtime Event 转成不含正文和参数的运行摘要。
// Trace 是辅助诊断，写入失败不应改变 Agent 的主流程。
func newTraceSink(writer io.Writer) agent.EventSink {
	return func(event agent.Event) error {
		var line string
		switch event.Type {
		case agent.EventRunStarted:
			line = "[drift] run_started"
		case agent.EventToolCall:
			line = "[drift] tool_call " + event.ToolName
		case agent.EventToolResult:
			line = fmt.Sprintf("[drift] tool_result %s bytes=%d", event.ToolName, len(event.Result))
		case agent.EventPermissionRequest:
			line = fmt.Sprintf("[drift] permission_request %s operation=%s", event.ToolName, event.Operation)
		case agent.EventPermissionDecision:
			line = fmt.Sprintf("[drift] permission_decision %s allowed=%t", event.ToolName, event.Allowed)
		case agent.EventError:
			stage := event.Stage
			if stage == "" {
				stage = "agent"
			}
			line = "[drift] error stage=" + stage
		case agent.EventRunFinished:
			line = "[drift] run_finished"
		case agent.EventCompactionStarted:
			line = fmt.Sprintf("[drift] compaction_started bytes=%d messages=%d", event.BeforeBytes, event.MessageCount)
		case agent.EventCompactionFinished:
			line = fmt.Sprintf("[drift] compaction_finished before_bytes=%d after_bytes=%d kept_messages=%d", event.BeforeBytes, event.AfterBytes, event.KeptMessages)
		case agent.EventCompactionError:
			stage := event.Stage
			if stage == "" {
				stage = "agent_compaction"
			}
			line = "[drift] compaction_error stage=" + stage
		}
		if line != "" {
			_, _ = fmt.Fprintln(writer, line)
		}
		return nil
	}
}
