# Task 4 report: default Registry

状态：DONE

`NewDefaultRegistry` 现在按稳定顺序注册 M0.4 的三个只读工具：

1. `list_files`
2. `search_text`
3. `read_file`

同步更新默认 Registry 测试，断言三个定义的顺序以及三次 `Lookup` 均成功；`NewRegistry` 的名称校验、重复名称保护和 schema 拷贝行为未改动。

验证：

- `go test ./internal/tool -run "TestDefaultRegistry|TestNewRegistry" -count=1`
- `go test ./internal/tool -count=1`

两项均通过。

提交：`2cd1dfc 功能：扩展默认只读工具注册表`
