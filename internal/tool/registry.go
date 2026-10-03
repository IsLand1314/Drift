package tool

import (
	"encoding/json"
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
	tools     map[string]Tool
	order     []Tool
	enabled   map[string]bool
	summaries map[string]toolSummary
	tasks     *taskStore
}

// NewRegistry 创建工具注册表，并拒绝空名称和重复名称。
func NewRegistry(tools ...Tool) (Registry, error) {
	result := &registry{
		tools:     make(map[string]Tool, len(tools)),
		order:     make([]Tool, 0, len(tools)),
		enabled:   make(map[string]bool, len(tools)),
		summaries: make(map[string]toolSummary, len(tools)),
		tasks:     newTaskStore(),
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
		result.enabled[name] = true
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
	return NewChatRegistryWithSandbox(SandboxOff)
}

// NewChatRegistryWithSandbox returns the chat tools with a user-selected,
// control-plane sandbox policy fixed into Bash.
func NewChatRegistryWithSandbox(sandboxMode SandboxMode) Registry {
	result := &registry{tools: make(map[string]Tool), enabled: make(map[string]bool), summaries: make(map[string]toolSummary), tasks: newTaskStore()}
	tasks := result.tasks
	for _, current := range []Tool{listFilesTool{}, searchTextTool{}, readFileTool{}, writeFileTool{}, editFileTool{}, deleteFileTool{}, runCommandTool{sandboxMode: sandboxMode}, askUserQuestionTool{}, taskCreateTool{tasks}, taskListTool{tasks}, taskGetTool{tasks}, taskUpdateTool{tasks}, taskSwitchTool{tasks}, taskRunTool{tasks}, taskStatusTool{tasks}, taskCancelTool{tasks}} {
		result.add(current, false)
	}
	result.add(toolSearchTool{registry: result}, true)
	result.enable("AskUserQuestion")
	return result
}

func (r *registry) ExportTasks() []TaskState { return r.tasks.export() }

func (r *registry) RestoreTasks(items []TaskState) error { return r.tasks.restore(items) }

func (r *registry) ResetTasks() { r.tasks.reset() }

func (r *registry) ActiveWorktree(root string) (string, error) { return r.tasks.activeWorktree(root) }

func (r *registry) Definitions() []llm.ToolDefinition {
	definitions := make([]llm.ToolDefinition, 0, len(r.order))
	for _, current := range r.order {
		if !r.enabled[current.Name()] {
			continue
		}
		definition := current.Definition()
		definition.Function = append([]byte(nil), definition.Function...)
		definitions = append(definitions, definition)
	}
	return definitions
}

func (r *registry) Lookup(name string) (Tool, bool) {
	current, ok := r.tools[name]
	return current, ok && r.enabled[name]
}

func (r *registry) add(current Tool, enabled bool) {
	name := current.Name()
	r.tools[name] = current
	r.order = append(r.order, current)
	r.enabled[name] = enabled
	r.summaries[name] = toolSummary{Name: name, Category: toolCategory(name), Description: toolDescription(current.Definition())}
}

func (r *registry) enable(name string) { r.enabled[name] = true }

func (r *registry) search(query string) []toolSummary {
	needle := strings.ToLower(query)
	results := make([]toolSummary, 0)
	for _, current := range r.order {
		summary := r.summaries[current.Name()]
		if strings.Contains(strings.ToLower(summary.Name+" "+summary.Category+" "+summary.Description), needle) {
			results = append(results, summary)
		}
	}
	return results
}

func toolCategory(name string) string {
	switch name {
	case "Glob", "Grep", "ReadFile":
		return "read"
	case "WriteFile", "EditFile", "DeleteFile":
		return "write"
	case "Bash":
		return "command"
	default:
		return "control"
	}
}

func toolDescription(definition llm.ToolDefinition) string {
	var data struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(definition.Function, &data); err != nil {
		return ""
	}
	return data.Description
}
