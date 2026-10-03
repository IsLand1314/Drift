# M3.18 工具能力复核与补缺设计

## 目标

在不重复实现 M3.11/M3.12 的前提下，复核并补强 `Glob`、`Grep` 和写入前文件状态校验，保持现有 workspace 边界、读取预算、审批和审计语义。Git 专用沙箱不属于本阶段。

## 当前基线

- `Glob` 已支持 `pattern`、可选 `path`、普通文件过滤、保护目录跳过和 200 条上限。
- `Grep` 已支持 Go 正则、`path`、`include`、非法输入错误、二进制跳过和文件/匹配/字节上限。
- `WriteFile`、`EditFile`、`DeleteFile` 的 `Preview` 已保存预览时的字节快照，提交前拒绝外部修改、删除、替换和符号链接变化。
- 状态校验是当前进程内存快照，不扩展为持久化缓存、跨进程锁或多 Agent 协调。

## 本阶段补缺

1. 用 TDD 补齐三类工具的边界回归：路径越界、保护目录、符号链接、非法 glob/regex、结果上限、取消和历史公开名称。
2. 增加一个临时 workspace 功能测试，验证 `Glob`、`Grep`、读后外部修改再 `EditFile` 的完整链路。
3. 检查工具结果和 JSONL 审计仍使用 `Glob`/`Grep`/`ReadFile` 等当前公开名称，不生成旧名称。
4. 只在测试暴露真实缺口时修改实现；不新增通用缓存层或新抽象。

## 明确不做

- 不修改 Git 回滚和 `.git` 沙箱策略。
- 不全局放开 `.git` 或 `.drift`。
- 不兼容旧工具名和旧参数。
- 不做跨会话持久化文件状态缓存。

## 验收

- `go test ./internal/tool -run 'TestGlob|TestGrep|ExternalChangeAfterPreview' -count=1`
- 临时 workspace 功能测试覆盖正常结果、无匹配、越界/保护路径和外部修改拒绝。
- 检查旧 JSONL/change set 可读取，新增记录不出现旧工具名。
- `go test ./... -count=1`
- `go vet ./...`
- `go build ./cmd/drift`
- `git diff --check`

