# Task 1 report

- Status: DONE
- Commit: `c28d20cdf4ccdde80a4102ad245db4dcf5288c6b`
- Tests: `go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestWalkRegularFiles|TestRead" -count=1`; `go test ./internal/tool -count=1`
- Concerns: `os.Root.FS()` is used with `fs.WalkDir`; symlink roots are rejected and symlink entries are skipped. No known concerns.
