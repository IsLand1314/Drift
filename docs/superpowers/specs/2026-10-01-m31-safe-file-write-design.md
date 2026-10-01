# M3.1 安全文件写入设计

## 状态

设计稿，尚未进入实现。M2.5 仍是当前交付版本。

## 目标

在保持 workspace 边界和人工确认的前提下，让 Agent 能创建新文件和覆盖已有文件，并为每次写入留下项目级的本地修改记录，为后续编辑、命令执行和 Git 回滚提供稳定边界。

## 明确区分两类文件

### 项目文件

Agent 要交付的文件仍写入 workspace 内的目标相对路径，例如：

```text
workspace = F:\code\drift
path      = internal/demo/demo.go
target    = F:\code\drift\internal\demo\demo.go
```

`path` 不能是绝对路径，不能包含 `..`，不能通过符号链接逃逸 workspace。

### 修改过程文件

过程文件只写入当前 workspace 的 `.drift/changes/`，不写入用户的全局 `Documents` 目录：

```text
.drift/
└── changes/
    └── YYYY/MM/DD/
        └── change-<timestamp>-<id>/
            ├── manifest.json
            ├── diff.patch
            ├── outputs/
            └── work/
```

`.drift/` 已被 Git 忽略。`sessions/` 仍只保存可恢复会话，`audits/` 仍只保存脱敏审计；`changes/` 不作为会话恢复来源。

## M3.1 工具范围

只新增一个原生工具：`write_file`。

工具入口按运行模式隔离：

| 入口 | `write_file` | 说明 |
| --- | --- | --- |
| `drift chat` | 启用 | 有终端人工确认，可创建或覆盖文件 |
| `drift -p` | 不注册 | 保持单次、非交互、只读，适合脚本和 CI |

工具只在交互式 `drift chat` 中注册。单次 `drift -p` 保持非交互只读，不注册 `write_file`，以避免没有人工确认通道时发生写入请求；`--no-session` 只控制完整会话保存，不改变 chat 的写入确认策略。

```json
{
  "path": "internal/demo/demo.go",
  "content": "package demo\n"
}
```

工具根据目标是否存在区分操作摘要：

- `create_file`：目标不存在；
- `overwrite_file`：目标已存在。

两种操作都必须走同一套确认流程。M3.1 不增加 `edit_file`、`delete_file`、`run_command`、`shell` 或 `exec`。

## 路径与文件边界

- workspace 由 `-w` 解析结果确定；`-w` 指向文件时使用其父目录。
- 目标必须位于 workspace 内，且所有已存在的路径组件都不能是逃逸 workspace 的符号链接。
- 目标父目录必须已经存在；M3.1 不自动递归创建目录。
- 拒绝 `.git/`、`.drift/`、`.codex-temp/` 和 `.worktrees/` 等运行时/版本控制目录。
- 写入前检查目标类型；目录、设备文件和无法安全替换的特殊文件直接拒绝。
- 对内容设置明确字节上限，超过上限在确认前拒绝，不进入写入阶段。

## 写入流程

```text
模型原生 write_file
        ↓
解析参数与 workspace 路径校验
        ↓
判断 create / overwrite，计算摘要与 diff
        ↓
在 .drift/changes/.../work/ 写入临时内容和 manifest
        ↓
向用户展示目标、操作类型、大小与 diff，等待确认
        ├─ deny/cancel → 删除本次临时 work，目标不变
        └─ allow       → 同目录临时文件 + 原子替换目标
                         ↓
                    写入 diff.patch 与结果 manifest
                         ↓
                    追加脱敏 audit 事件
```

确认前不触碰目标文件。写入失败时保留原目标，清理或标记本次临时目录，并记录稳定错误阶段。

## 权限模式边界

M3.1 的默认模式为 `ask`：每次创建或覆盖都要人工确认。权限模式接口先保留三种语义，但本阶段只开放安全默认值：

- `ask`：等待确认；
- `deny`：直接拒绝写入；
- `allow`/bypass：延后到权限专项阶段，不在 M3.1 开放。

这样不会因为新增工具而绕过人工审批。后续 M3.3 扩展命令执行时，再统一实现完整的 `allow / ask / deny` 配置和命令级策略。

## 过程记录与隐私

`manifest.json` 可记录操作 ID、UTC 时间、相对路径、操作类型、原始/新字节数、确认结果和错误阶段；不得写入 API Key、Authorization、完整提示词或无必要的模型正文。`diff.patch` 和 `outputs/` 可能包含项目敏感内容，只保存在本地 `.drift/changes/`，不进入脱敏审计，也不上传。

## 非目标与后续阶段

- M3.1 不实现删除、命令执行、测试运行、OS 沙箱、Git worktree 或自动回滚。
- M3.2 增加精确编辑和删除确认。
- M3.3 增加命令/测试执行、取消、超时和完整权限模式。
- M3.4 增加 OS 沙箱、Git 快照与回滚。

## 验收标准草案

- 创建新文件前不改变目标，确认后文件出现在 workspace 的请求路径。
- 覆盖已有文件前展示差异；拒绝或取消后原文件字节级不变。
- 绝对路径、路径穿越、符号链接逃逸和受保护目录全部拒绝。
- 父目录不存在时明确失败，不隐式创建目录。
- 写入失败不损坏原文件，并生成可诊断的安全错误阶段。
- `.drift/changes/YYYY/MM/DD/` 生成过程记录；`sessions/` 和 `audits/` 职责不变。
- 单元测试覆盖 create、overwrite、deny、越界、符号链接、原子失败和敏感字段脱敏；全量 `go test`、`go vet`、构建和 `git diff --check` 通过。
