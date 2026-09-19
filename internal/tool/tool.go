package tool

import (
	"context"

	"github.com/IsLand1314/Drift/internal/llm"
)

// Tool 是 Agent 可调用的最小工具契约。
// 工具只负责自身参数解析和执行，权限策略由更上层阶段引入。
type Tool interface {
	Name() string
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, root string, rawArguments string) (string, error)
}
