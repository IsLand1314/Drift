# M5.17 文档收口与扫描记录

日期：2026-10-04

## 已修正

- `README.md` 的 M5.7 版本陈述更新为 M5.18。
- `spec/current.md` 当前版本更新为 M5.18，并修正已完成阶段的旧“当前阶段”标记。
- MCP、沙箱、Memory、Multi-Agent 的当前状态补充到 M5.17 规格与当前规格头部。

## 扫描结论

- 公开工具旧名 `list_files`、`search_text`、`read_file`、`write_file`、`edit_file`、`delete_file`、`run_command` 未在工具注册表中兼容；对应扫描命中仅位于历史文档、回归测试或内部 `change set` operation 字段。
- `README.md` 与 `spec/current.md` 的活动版本陈述已无 M5.7 旧版本命中；历史阶段文件中的 M5.7 标题保留为历史记录。
- 未修改用户已有的 `doc/Process/*`、`doc/m0-SSE-Conversation.md` 或其余历史阶段文档。
