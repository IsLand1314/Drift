# M3.3 验收记录

- 日期：2026-10-02
- 分支：master
- 提交：3684188
- 工作区：F:\\code\\drift-m33-manual

## 自动化验收

| 项目 | 结果 | 证据 |
|---|---|---|
| 文件工具边界 | PASS | `final-focused-tests.txt` |
| 权限选择与当前 chat 记忆 | PASS | `final-focused-tests.txt` |
| 回合级 change set 聚合 | PASS | `final-focused-tests.txt` |
| 全量测试 | PASS | `final-go-test.txt` |
| vet | PASS | `final-go-vet.txt` |
| 构建 | PASS | `final-go-build.txt` |
| diff 检查 | PASS | `final-diff-check.txt` |
| DeepSeek 单轮 `-p` | PASS | `deepseek-single-turn-clean.txt`、`deepseek-p-mode-readonly.txt` |

## 场景验收

1. 多级目录创建：已验证审批拒绝不落盘，批准后目录、文件和读回结果正确。
2. 唯一编辑：已验证 `alpha -> beta` 成功且只修改目标文件。
3. 零匹配：已验证提示未找到匹配，不修改文件。
4. 多匹配：已验证提示匹配多次，不自动修改文件。
5. 文件删除：已验证拒绝保留文件，批准后删除成功。
6. 目录删除：已验证拒绝递归删除目录。
7. 受保护目录：已验证 `.drift` 目标拒绝且不创建文件。
8. `-p` 只读：DeepSeek 单轮返回 `DS-M33-READ-OK`；创建文件请求后 `p-mode.txt` 仍不存在。
9. 当前 chat 权限记忆：单元测试确认只匹配同一工具/操作/精确路径，且进程结束后失效。
10. 多文件 change set：单元测试确认一个回合聚合多个文件条目、统一 diff 和 work 内容。
11. 删除备份：`delete_file` 测试确认删除前内容写入 `.before` 快照。

## 未完成/需人工补充

- 真实 TTY 中的“允许此类操作”连续两次交互和退出重进场景，代码测试已覆盖，但仍建议在当前终端补拍一次运行截图。
- 本记录不包含 API Key；密钥仅作为当前单轮子进程环境变量使用。
