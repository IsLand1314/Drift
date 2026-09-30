# Task 1 report

- Status: DONE
- Commit: `7847dd7f95dd8b7c2449457fa02cdbe8b2bddec9`
- Tests: `go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestWalkRegularFiles|TestRead" -count=1`; `go test ./internal/tool -count=1`
- Concerns: `os.Root.FS()` is used with `fs.WalkDir`; symlink roots are rejected and symlink entries are skipped. No known concerns.
