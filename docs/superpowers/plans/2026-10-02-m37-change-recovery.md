# M3.7 Change Set 恢复实施计划

1. 先为 changes 包增加恢复快照写入和读取测试，覆盖 create/edit/delete。
2. 扩展现有 ChangeSet 记录接口，保留现有 manifest、diff.patch、work 布局。
3. 在工具提交路径中记录 before 快照；删除继续兼容 `.before` 约定。
4. 增加受保护的 change set 恢复入口与状态/路径校验。
5. 增加多文件原子性检查测试；恢复失败不得修改任何目标文件。
6. 完成全量 Go 验证和人工验收记录。
