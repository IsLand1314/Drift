package tool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/message"
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
	eager     bool
	tasks     *taskStore
	messages  *message.Bus
	plan      *planStore
}

// NewRegistry 创建工具注册表，并拒绝空名称和重复名称。
func NewRegistry(tools ...Tool) (Registry, error) {
	result := &registry{
		tools:     make(map[string]Tool, len(tools)),
		order:     make([]Tool, 0, len(tools)),
		enabled:   make(map[string]bool, len(tools)),
		summaries: make(map[string]toolSummary, len(tools)),
		tasks:     newTaskStore(),
		messages:  message.NewBus(),
		plan:      &planStore{tasks: newTaskStore()},
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
	cache := newFileStateCache()
	result, err := NewRegistry(listFilesTool{}, searchTextTool{}, readFileTool{cache: cache})
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
	result := &registry{tools: make(map[string]Tool), enabled: make(map[string]bool), summaries: make(map[string]toolSummary), tasks: newTaskStore(), messages: message.NewBus(), plan: &planStore{}}
	result.plan.tasks = result.tasks
	tasks := result.tasks
	cache := newFileStateCache()
	for _, current := range []Tool{listFilesTool{}, searchTextTool{}, readFileTool{cache: cache}, writeFileTool{cache: cache}, editFileTool{cache: cache}, deleteFileTool{cache: cache}, runCommandTool{sandboxMode: sandboxMode}, askUserQuestionTool{}, taskCreateTool{tasks}, taskListTool{tasks}, taskGetTool{tasks}, taskUpdateTool{tasks}, taskSwitchTool{tasks}, taskRunTool{tasks}, taskStatusTool{tasks}, taskCancelTool{tasks}, taskMergeTool{tasks}, coordinatorStatusTool{tasks}, agentMessageSendTool{result.messages}, agentMessageListTool{result.messages}, agentSummaryTool{result.messages}} {
		result.add(current, false)
	}
	result.add(toolSearchTool{registry: result}, true)
	result.enable("AskUserQuestion")
	result.add(planModeTool{name: "EnterPlanMode"}, false)
	result.add(planModeTool{name: "ExitPlanMode"}, false)
	result.add(planUpdateTool{store: result.plan}, false)
	result.add(planExecuteTool{plan: result.plan, tasks: result.tasks}, false)
	return result
}

func (r *registry) MessageBus() *message.Bus { return r.messages }

func (r *registry) SetMessageBus(bus *message.Bus) {
	if bus == nil {
		return
	}
	r.messages = bus
	for name, current := range r.tools {
		switch name {
		case "AgentMessageSend":
			r.tools[name] = agentMessageSendTool{bus}
		case "AgentMessageList":
			r.tools[name] = agentMessageListTool{bus}
		case "AgentSummary":
			r.tools[name] = agentSummaryTool{bus}
		default:
			_ = current
		}
	}
}

func (r *registry) ExportTasks() []TaskState { return r.tasks.export() }

func (r *registry) RestoreTasks(items []TaskState) error { return r.tasks.restore(items) }

func (r *registry) ResetTasks() { r.tasks.reset() }

func (r *registry) SetPlanID(id string) { r.plan.plan.ID = id }
func (r *registry) ExportPlan() Plan {
	plan := r.plan.plan
	plan.Tasks = append([]PlanTask(nil), plan.Tasks...)
	plan.Risks = append([]string(nil), plan.Risks...)
	plan.Acceptance = append([]string(nil), plan.Acceptance...)
	return plan
}
func (r *registry) RestorePlan(plan Plan) error {
	if plan.ID != "" && !strings.HasPrefix(plan.ID, "plan-") {
		return fmt.Errorf("invalid plan id")
	}
	r.plan.plan = plan
	return nil
}
func (r *registry) ResetPlan() { r.plan.plan = Plan{} }

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

// LoadAll exposes every registered schema for the eager loading strategy.
func (r *registry) LoadAll() {
	r.eager = true
	for name := range r.enabled {
		r.enabled[name] = true
	}
}

func (r *registry) Lookup(name string) (Tool, bool) {
	current, ok := r.tools[name]
	return current, ok && r.enabled[name]
}

func (r *registry) add(current Tool, enabled bool) {
	name := current.Name()
	r.tools[name] = current
	r.order = append(r.order, current)
	r.enabled[name] = enabled || r.eager
	r.summaries[name] = toolSummary{Name: name, Category: toolCategory(name), Description: toolDescription(current.Definition())}
}

func (r *registry) enable(name string) { r.enabled[name] = true }

func (r *registry) search(query string) []toolSummary {
	needle := strings.ToLower(strings.TrimSpace(query))
	tokens := strings.FieldsFunc(needle, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if needle == "" || len(tokens) == 0 {
		return nil
	}
	type scored struct {
		summary toolSummary
		score   int
	}
	results := make([]scored, 0)
	for _, current := range r.order {
		summary := r.summaries[current.Name()]
		haystack := strings.ToLower(summary.Name + " " + summary.Category + " " + summary.Description)
		score := 0
		if strings.Contains(haystack, needle) {
			score += len(tokens) + 1
		}
		for _, token := range tokens {
			if strings.Contains(haystack, token) {
				score++
			}
		}
		if score > 0 {
			results = append(results, scored{summary: summary, score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })
	out := make([]toolSummary, len(results))
	for i, item := range results {
		out[i] = item.summary
	}
	return out
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
