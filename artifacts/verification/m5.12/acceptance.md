# M5.12 验收记录

## 自动化 TDD

- `go test ./... -count=1`：PASS
- `go test -race ./... -count=1`：PASS
- `go vet ./...`：PASS
- `go build ./cmd/drift`：PASS
- scoped `git diff --check`：PASS

覆盖范围：上下文压缩边界、短期 Memory 会话保存/恢复、长期 Memory 脱敏检索、经验候选与审批保存、Task 完成提示、Skill manifest 校验、Skill 安装/删除审计。

## 真实 LLM

Provider：DeepSeek `deepseek-chat`。

输入：`请使用 ToolSearch 搜索 memory，只加载 MemorySearch，然后检索 viewport；不要调用其他工具。`

输出摘要：模型只加载 `MemorySearch`，检索 `viewport` 返回无匹配；未加载 `ExperienceSave`，未产生写入。

真实链路生成的临时审计文件已清理。

## 人工短验收

1. `/memory remember fact <文本>` 后 `/memory` 能看到短期条目。
2. `/memory search <关键词>` 能检索 `.drift/memory` 中已验证长期条目。
3. `skill install <本地目录>` 后生成 `skill.json` 和 `skill-lifecycle.jsonl`；`skill remove` 后 Skill 消失但 Session/Memory 不变。
   `skill install --preview <本地目录>` 只展示复制范围；`skill disable/enable` 只切换状态，不删除内容。
4. `TaskUpdate(status=completed)` 只提示先 `ExperiencePropose`，不会自动保存经验。
