# M3.7 自动验收记录

## 场景 1：创建后恢复

输入（等价于 chat 请求）：

```text
请创建 m37-demo.txt，内容为 before-restore
```

预期工具输出：

```text
✓ Write m37-demo.txt · 14 B · ...
✓ Read m37-demo.txt · 14 B · ...
```

恢复命令：

```powershell
go run F:\code\drift\cmd\drift change restore `
  -w F:\code\drift-m33-manual `
  <change-dir> --yes
```

预期：命令成功，`m37-demo.txt` 不存在。

## 场景 2：编辑、删除和多文件恢复

由 `internal/changes/restore_test.go` 自动创建临时 workspace，覆盖：

- 创建文件恢复后删除；
- 编辑文件恢复为旧内容；
- 删除文件恢复 `.before` 内容；
- 多文件 change set 逐项恢复。

## 场景 3：冲突保护

自动测试先完成 change set，再修改目标文件，恢复必须失败，且目标文件内容保持外部修改结果。

## 场景 4：错误复现与修复

原命令在 `F:\code\drift` 执行、change set 位于 `F:\code\drift-m33-manual` 时返回：

```text
恢复失败： changes: change set is outside workspace changes directory
```

根因是命令默认使用当前目录作为 workspace，并且用户选中了错误 workspace 下的旧 change set（`tmp/hello.html`）。

修复后使用 `-w` 显式指定 workspace；错误 workspace 仍然安全拒绝，正确 workspace 可以恢复。

## 自动验证命令

```text
go test ./... -count=1
go vet ./...
go build ./cmd/drift
git diff --check
```

结果：全部通过。
