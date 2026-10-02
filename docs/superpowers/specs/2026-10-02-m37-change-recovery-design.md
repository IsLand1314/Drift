# M3.7 Change Set 恢复设计

## 目标

为已完成的文件变更提供一次可验证的恢复能力。恢复基于 Drift 自己的 change set，不等同于 Git 回滚。

## 范围

- 每个成功的 `write_file`、`edit_file`、`delete_file` 记录恢复所需的变更前状态。
- 支持按 change set 恢复，覆盖单文件和同一轮中的多文件变更。
- 通过 `drift change restore <change-dir> --yes` 执行恢复。
- 恢复前检查当前文件状态，发现用户后续修改时拒绝覆盖。
- 新建文件恢复为删除；编辑/覆盖恢复为旧内容；删除文件恢复为删除前内容。
- 恢复操作本身不再次生成可递归恢复的 change set。

## 非目标

- 不实现 Git reset/revert。
- 不恢复目录删除；目录删除仍被工具拒绝。
- 不增加后台任务、跨 workspace 恢复或永久自动授权。

## 存储约定

现有 `manifest.json` 和 `diff.patch` 保持兼容；`work/` 同时保存操作后的内容，删除文件保存 `<path>.before`。对覆盖/编辑新增同名 `<path>.before`，创建文件不生成 before 副本，由 `operation=create_file` 表示原文件不存在。

## 安全规则

1. 只接受 workspace 内 `.drift/changes/**/manifest.json` 对应的 change set。
2. 只恢复 `status=complete` 的 change set。
3. 恢复前逐文件校验当前内容与操作后的快照一致；任一文件不一致则整体拒绝。
4. 恢复过程使用临时文件和原子替换；创建文件使用受控删除。
5. 任意失败都不报告恢复成功。

## 验收标准

- AC-M37-001：创建文件后恢复，文件消失，其他文件不变。
- AC-M37-002：编辑/覆盖后恢复，内容回到变更前版本。
- AC-M37-003：删除后恢复，`.before` 内容重新出现。
- AC-M37-004：同一 change set 多文件恢复必须整体校验，任一文件被外部修改时拒绝且不部分恢复。
- AC-M37-005：目录删除、越界路径、未完成或损坏 change set 均拒绝。
- AC-M37-006：`go test ./...`、`go vet ./...`、build、`git diff --check` 全部通过。
