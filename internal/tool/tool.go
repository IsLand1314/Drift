package tool

import (
	"context"

	"github.com/IsLand1314/Drift/internal/llm"
)

// Preview describes a write that has not changed the target yet.
type Preview struct {
	Operation string
	Path      string
	Content   []byte
	OldBytes  int
	NewBytes  int
	Diff      string
}

// Previewable is implemented by tools that require caller approval before execution.
type Previewable interface {
	Tool
	Preview(context.Context, string, string) (Preview, error)
	ExecutePreview(context.Context, string, Preview) (string, error)
}

// Tool 是 Agent 可调用的最小工具契约。
// 工具只负责自身参数解析和执行，权限策略由更上层阶段引入。
type Tool interface {
	Name() string
	Definition() llm.ToolDefinition
	Execute(ctx context.Context, root string, rawArguments string) (string, error)
}
