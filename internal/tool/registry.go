package tool

import (
	"fmt"
	"strings"

	"github.com/IsLand1314/Drift/internal/llm"
)

// Registry 提供稳定顺序的工具 schema 和按名称查找。
type Registry interface {
	Definitions() []llm.ToolDefinition
	Lookup(name string) (Tool, bool)
}

type registry struct {
	tools map[string]Tool
	order []Tool
}

// NewRegistry 创建工具注册表，并拒绝空名称和重复名称。
func NewRegistry(tools ...Tool) (Registry, error) {
	result := &registry{
		tools: make(map[string]Tool, len(tools)),
		order: make([]Tool, 0, len(tools)),
	}
	for _, current := range tools {
		if current == nil {
			return nil, fmt.Errorf("tool: nil tool")
		}
		name := strings.TrimSpace(current.Name())
		if name == "" {
			return nil, fmt.Errorf("tool: tool name is blank")
		}
		if _, exists := result.tools[name]; exists {
			return nil, fmt.Errorf("tool: duplicate tool %q", name)
		}
		result.tools[name] = current
		result.order = append(result.order, current)
	}
	return result, nil
}

// NewDefaultRegistry 返回当前阶段按稳定顺序排列的只读工具。
func NewDefaultRegistry() Registry {
	result, err := NewRegistry(listFilesTool{}, searchTextTool{}, readFileTool{})
	if err != nil {
		panic(err)
	}
	return result
}

// NewChatRegistry returns the read-only tools plus the confirmation-gated writer.
func NewChatRegistry() Registry {
	result, err := NewRegistry(listFilesTool{}, searchTextTool{}, readFileTool{}, writeFileTool{}, editFileTool{}, deleteFileTool{}, runCommandTool{})
	if err != nil {
		panic(err)
	}
	return result
}

func (r *registry) Definitions() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(r.order))
	for _, current := range r.order {
		definition := current.Definition()
		definition.Function = append([]byte(nil), definition.Function...)
		definitions = append(definitions, definition)
	}
	return definitions
}

func (r *registry) Lookup(name string) (Tool, bool) {
	current, ok := r.tools[name]
	return current, ok
}
