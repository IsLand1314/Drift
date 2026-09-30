# Task 1 report

- Status: DONE
- Commit: `c28d20cdf4ccdde80a4102ad245db4dcf5288c6b`
- Tests: `go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestWalkRegularFiles|TestRead" -count=1`; `go test ./internal/tool -count=1`
- Concerns: `os.Root.FS()` is used with `fs.WalkDir`; symlink roots are rejected and symlink entries are skipped. No known concerns.

## Fix round

- Status: DONE
- Changes: traversal roots now reject any symlink component; ordinary traversal assertions run independently of symlink support; added a real-file traversal-root regression case.
- Tests: `go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestWalkRegularFiles" -count=1`; `go test ./internal/tool -count=1`
- Concerns: None.
- Fix round 2 commit: `221ef028d7793f022ee2f54e01737c3f43b00707`
- Fix commit: `b651d54c867f1f0db2824c9508f052169456d30e`

## Fix round 2

- Status: DONE
- Changes: unsafe-root regression now creates `file.txt`; parent-symlink regression now targets the real directory `link-dir/child`.
- Tests: `go test ./internal/tool -run "TestWalkRegularFilesRejectsUnsafeDirectory|TestWalkRegularFiles" -count=1`; `go test ./internal/tool -count=1`
- Concerns: None.
