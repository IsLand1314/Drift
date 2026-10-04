# M5.1 Provider Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add workspace-scoped behavior, Provider/model and credential files so Drift safely selects a configured OpenAI-compatible or Anthropic model while keeping credential access outside Agent tools.

**Architecture:** `internal/config` loads `<workspace>/.drift`, decodes TOML and JSON, validates it and resolves a profile/key. `internal/app` resolves the workspace before configuration, turns that profile into the existing Provider clients, and keeps flags as one-run overrides. Workspace `.env` remains solely as a legacy fallback when `.drift/config.toml` is absent.

**Tech Stack:** Go 1.26, `github.com/pelletier/go-toml/v2`, Go `encoding/json`, existing Bubble Tea and OpenAI/Anthropic clients.

**Spec:** [`spec/m5.1-provider-configuration.md`](../../../spec/m5.1-provider-configuration.md)

## Global Constraints

- The sole configuration directory is `filepath.Join(workspace, ".drift")`; do not add global, local or layered overrides.
- `settings.toml` and `config.toml` never contain secrets; `auth.json` is ignored by Git and protected from Agent tools.
- M5.1 reads auth only. Do not implement OAuth, login/logout, writing credentials, secret shell resolvers, custom headers, YAML, Hooks or hot reload.
- Valid configured protocols are exactly `openai`, `openai-compat` and `anthropic`.
- Unknown fields, duplicate names, unsafe URLs and invalid modes fail before a provider request and without echoing a credential.
- `bypassPermissions` cannot be persisted as the settings default; existing CLI behavior remains supported.
- Existing `.env` behavior remains only when workspace `.drift/config.toml` is absent.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `go.mod`, `go.sum` | Pin the TOML parser. |
| `internal/config/user_config.go` | Workspace `.drift` decoding, validation and secret resolution. |
| `internal/config/user_config_test.go` | TDD coverage for parsing, validation and secret precedence. |
| `internal/app/provider_config.go` | Merge one-run flags with a resolved configured profile. |
| `internal/app/provider_config_test.go` | Provider selection, fallback and override tests. |
| `internal/app/provider_picker.go` | Bubble Tea picker for multi-profile interactive chat. |
| `internal/app/provider_picker_test.go` | Picker behavior and no-secret rendering tests. |
| `internal/app/app.go` | Replace inline provider setup while retaining later workspace/session logic. |
| `internal/app/app_test.go`, `internal/app/provider_test.go` | HTTP, audit redaction and legacy regression tests. |
| `README.md` | User setup and migration instructions. |

## Task 1: Load and Validate User Configuration

**Files:**
- Create: `internal/config/user_config.go`
- Create: `internal/config/user_config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**

```go
type UserPaths struct { Dir, Settings, Providers, Auth string }
type Settings struct { Version int; DefaultProvider, PermissionMode, SandboxMode, TUIMode string; Chat ChatSettings }
type Provider struct { Name, Protocol, BaseURL, Model, APIKeyEnv string; ContextWindow, MaxOutputTokens int }
type AuthEntry struct { Type, Key string }
type UserConfig struct { Settings Settings; Providers []Provider; Auth map[string]AuthEntry }
type ResolvedProvider struct { Provider; Key string }

func UserPathsFromConfigDir(dir string) UserPaths
func LoadUserConfig(paths UserPaths) (UserConfig, error)
func (c UserConfig) Resolve(name, apiKeyOverride string, getenv func(string) string) (ResolvedProvider, error)
```

- [ ] **Step 1: Write failing config tests**

Create temporary `settings.toml`, `config.toml` and `auth.json`. Add this primary test:

```go
func TestLoadUserConfigResolvesAuthBeforeEnvironment(t *testing.T) {
    paths := UserPathsFromConfigDir(t.TempDir())
    writeFile(t, paths.Settings, "version = 1\ndefault_provider = \"deepseek\"\npermission_mode = \"default\"\nsandbox_mode = \"auto\"\ntui_mode = \"main\"\n[chat]\nauto_compact = true\n")
    writeFile(t, paths.Providers, "version = 1\n[[providers]]\nname = \"deepseek\"\nprotocol = \"openai-compat\"\nbase_url = \"https://api.deepseek.com\"\nmodel = \"deepseek-chat\"\napi_key_env = \"DEEPSEEK_API_KEY\"\ncontext_window = 128000\nmax_output_tokens = 8192\n")
    writeFile(t, paths.Auth, `{"version":1,"providers":{"deepseek":{"type":"api_key","key":"auth-secret"}}}`)
    cfg, err := LoadUserConfig(paths)
    if err != nil { t.Fatal(err) }
    got, err := cfg.Resolve("", "", func(string) string { return "environment-secret" })
    if err != nil { t.Fatal(err) }
    if got.Name != "deepseek" || got.Model != "deepseek-chat" || got.Key != "auth-secret" { t.Fatalf("resolved=%#v", got) }
}
```

Add individual tests for duplicate names, unsupported protocol, HTTP non-loopback URL, URL userinfo/query/fragment, persisted `bypassPermissions`, missing default profile, unknown TOML/JSON fields and empty auth key. Each error assertion includes `strings.Contains(err.Error(), secret) == false`.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/config -run 'TestLoadUserConfig|TestUserConfig' -count=1
```

Expected: compilation fails because the new API does not exist.

- [ ] **Step 3: Implement the minimum loader**

Run `go get github.com/pelletier/go-toml/v2`. Decode TOML with `DisallowUnknownFields`; decode JSON with `json.Decoder.DisallowUnknownFields`. Missing `settings.toml` means defaults; missing `auth.json` means no credential; missing `config.toml` returns a typed `ErrNoProviderConfig` for legacy fallback. Validate URL with `net/url`: HTTP allowed only for `localhost` or loopback; every other host requires HTTPS.

Resolve the credential exactly:

```go
if apiKeyOverride != "" { return apiKeyOverride }
if entry, ok := c.Auth[profile.Name]; ok { return entry.Key }
return strings.TrimSpace(getenv(profile.APIKeyEnv))
```

Never place raw TOML, raw JSON or `entry.Key` in an error.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
go test ./internal/config -run 'TestLoadUserConfig|TestUserConfig' -count=1
```

Expected: all new tests pass.

- [ ] **Step 5: Commit**

```powershell
git add go.mod go.sum internal/config/user_config.go internal/config/user_config_test.go
git commit -m "feat: load user provider configuration"
```

## Task 2: Resolve Configured Providers and Preserve `.env` Compatibility

**Files:**
- Create: `internal/app/provider_config.go`
- Create: `internal/app/provider_config_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/app/provider_test.go`

**Interfaces:**

```go
type providerLaunch struct { Name, Protocol, BaseURL, Model, Key string; Settings config.Settings }
func resolveProviderLaunch(flags providerFlags, paths config.UserPaths, getenv func(string) string, interactive bool) (providerLaunch, bool, error)
```

The Boolean is true only when the caller must use existing `.env` behavior.

- [ ] **Step 1: Write failing app tests**

Use `httptest.Server` and temporary workspace `.drift` paths to add:

```go
func TestRunUsesConfiguredOpenAICompatProfile(t *testing.T) {
    server := newOpenAIServer(t, func(r *http.Request, body requestBody) {
        if body.Model != "deepseek-chat" { t.Fatalf("model=%q", body.Model) }
        if r.Header.Get("Authorization") != "Bearer config-secret" { t.Fatal("configured auth was not used") }
    })
    paths := writeConfiguredDeepSeek(t, server.URL, "config-secret")
    code, out, stderr := runWithConfigDir(t, paths.Dir, []string{"--provider", "deepseek", "-p", "hello"})
    if code != 0 || !strings.Contains(out, "ok") || strings.Contains(out+stderr, "config-secret") { t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr) }
}

func TestRunPrintModeUsesDefaultConfiguredProvider(t *testing.T) {
    paths := writeTwoConfiguredProviders(t, "anthropic")
    launch, legacy, err := resolveProviderLaunch(providerFlags{}, paths, func(string) string { return "key" }, false)
    if err != nil || legacy || launch.Name != "anthropic" { t.Fatalf("launch=%#v legacy=%v err=%v", launch, legacy, err) }
}

func TestRunLegacyDotEnvWhenConfigTomlMissing(t *testing.T) {
    paths := UserPathsFromConfigDir(t.TempDir())
    launch, legacy, err := resolveProviderLaunch(providerFlags{}, paths, func(string) string { return "" }, false)
    if err != nil || !legacy || launch.Name != "" { t.Fatalf("launch=%#v legacy=%v err=%v", launch, legacy, err) }
}
```

Also assert unknown configured names fail before HTTP, `--api-key` beats auth, `--model`/`--base-url` affect only one request, and a present `.drift/config.toml` prevents workspace `.env` loading.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/app -run 'TestRunUsesConfigured|TestRunPrintModeUsesDefault|TestRunLegacyDotEnvWhenConfigTomlMissing' -count=1
```

Expected: failure because startup cannot resolve workspace configuration.

- [ ] **Step 3: Implement the resolver and wire `app.Run`**

Add public `--api-key`; retain `--provider`, `--model`, `--base-url`, `--permission-mode` and `--sandbox` as per-run overrides. Resolve workspace before loading `.drift` or legacy `.env`. If `.drift/config.toml` exists, load only the three workspace files; otherwise use the present `LoadDotEnv`/`MergeLookup` code unchanged. Map `openai`/`openai-compat` to `openai.New`, and `anthropic` to `anthropic.New`.

Pass only the resolved key to `session.NewJSONLWriterWithSecrets`; never persist overrides or configuration values.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
go test ./internal/app -run 'TestRunUsesConfigured|TestRunPrintModeUsesDefault|TestRunLegacyDotEnvWhenConfigTomlMissing|TestRunSelectsAnthropicProvider' -count=1
```

Expected: configured profile, overrides, redaction and legacy regressions pass.

- [ ] **Step 5: Commit**

```powershell
git add internal/app/provider_config.go internal/app/provider_config_test.go internal/app/app.go internal/app/app_test.go internal/app/provider_test.go
git commit -m "feat: select configured providers at startup"
```

## Task 3: Add the Interactive Provider Picker

**Files:**
- Create: `internal/app/provider_picker.go`
- Create: `internal/app/provider_picker_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/chat_input.go`

**Interfaces:**

```go
type providerPickerModel struct { options []config.Provider; cursor int; submitted, cancelled bool }
func newProviderPicker(options []config.Provider, defaultName string) providerPickerModel
func readProviderChoice(ctx context.Context, in io.Reader, out io.Writer, options []config.Provider, defaultName string) (string, bool, error)
```

- [ ] **Step 1: Write failing picker tests**

```go
func TestProviderPickerHighlightsConfiguredDefault(t *testing.T) {
    picker := newProviderPicker([]config.Provider{{Name: "deepseek", Model: "deepseek-chat"}, {Name: "anthropic", Model: "claude"}}, "anthropic")
    if picker.cursor != 1 || !strings.Contains(picker.View(), "anthropic") { t.Fatalf("picker=%#v view=%q", picker, picker.View()) }
}

func TestProviderPickerReturnsSelectedNameOnEnter(t *testing.T) {
    picker := newProviderPicker([]config.Provider{{Name: "deepseek"}, {Name: "anthropic"}}, "deepseek")
    next, _ := picker.Update(tea.KeyMsg{Type: tea.KeyDown})
    picker = next.(providerPickerModel)
    next, _ = picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
    picker = next.(providerPickerModel)
    if !picker.submitted || picker.selected() != "anthropic" { t.Fatalf("picker=%#v", picker) }
}
```

Also test Escape/Ctrl+C returns no selection and the view includes each name/model but no credential.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/app -run TestProviderPicker -count=1
```

Expected: compilation fails because the picker does not exist.

- [ ] **Step 3: Implement the smallest chat-only picker**

Mirror `permissionModeInputModel`: Up/Down bounded movement; Enter selection; Escape/Ctrl+C cancel; existing Bubble Tea no-signal/cleanup conventions. Invoke it only for interactive `chat`, several profiles and no explicit `--provider`. `-p` must always use `default_provider` and must never prompt.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
go test ./internal/app -run 'TestProviderPicker|TestRunPrintModeUsesDefaultConfiguredProvider' -count=1
```

Expected: picker and noninteractive behavior pass.

- [ ] **Step 5: Commit**

```powershell
git add internal/app/provider_picker.go internal/app/provider_picker_test.go internal/app/app.go internal/app/chat_input.go
git commit -m "feat: add provider selection picker"
```

## Task 4: Security Regression and Migration Documentation

**Files:**
- Modify: `internal/config/user_config_test.go`
- Modify: `internal/app/app_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes all Task 1–3 interfaces.
- Produces tested redaction and a manual setup path without a real credential.

- [ ] **Step 1: Write failing security tests**

Use sentinel `m5-config-secret-do-not-log`. Assert it is absent from stdout, stderr, trace and audit JSONL for malformed auth JSON, unknown profile, missing selected credential and provider HTTP failure. Also prove Agent file tools cannot read `<workspace>/.drift/auth.json`.

- [ ] **Step 2: Verify RED**

Run:

```powershell
go test ./internal/config ./internal/app -run 'Test(UserConfig|Run).*Secret|TestWorkspaceDriftConfigIsIgnored' -count=1
```

Expected: failure until every error path is redacted and workspace configuration resolution is complete.

- [ ] **Step 3: Implement minimal redaction and docs**

Use generic errors such as `invalid auth configuration`; do not create a generic secret logger. Document the three user files, supported protocols, Provider selection, credential priority and `.env` migration fallback. State that `auth.json` must never be checked in.

- [ ] **Step 4: Verify GREEN**

Run:

```powershell
go test ./internal/config ./internal/app -count=1
go test ./internal/agent ./internal/tool ./internal/session -count=1
```

Expected: configuration/app security tests and existing secret-redaction tests pass.

- [ ] **Step 5: Commit**

```powershell
git add internal/config/user_config_test.go internal/app/app_test.go README.md
git commit -m "docs: explain provider configuration migration"
```

## Task 5: M5.1 Acceptance

**Files:**
- Create: `spec/m5.1-acceptance.md`
- Modify: `spec/m5.1-provider-configuration.md`

- [ ] **Step 1: Run deterministic verification**

```powershell
go test ./... -count=1
go test -race ./internal/config ./internal/app ./internal/agent ./internal/tool -count=1
go vet ./...
go build -o .codex-temp\\drift-m51.exe ./cmd/drift
git diff --check
```

Expected: all commands exit 0. Delete only `.codex-temp\\drift-m51.exe` after recording its result.

- [ ] **Step 2: Run real configured DeepSeek validation**

Create a temporary workspace with `.drift/config.toml` and `.drift/auth.json`, without printing the credential. Run:

```powershell
go run ./cmd/drift -p "Read README.md and report its first line." -w <temporary-workspace> --provider deepseek
```

Expected: `deepseek-chat` is selected, a native `ReadFile` call or direct answer succeeds, and all session/audit output is free of the key and temporary config path. Delete both temporary directories afterwards.

- [ ] **Step 3: Independent read-only review**

Give a fresh-context reviewer the M5.1 spec, diff, deterministic output and redacted runtime output. It must not call tools, modify files or approve changes. Record exactly `PASS`, `FAIL` or `BLOCKED` in `spec/m5.1-acceptance.md`.

- [ ] **Step 4: Commit acceptance evidence**

```powershell
git add spec/m5.1-provider-configuration.md spec/m5.1-acceptance.md
git commit -m "docs: record M5.1 acceptance"
```

## Plan Self-Review

- Tasks 1–4 cover the three-file contract, profile selection, legacy migration, URL/mode/redaction boundaries and user documentation.
- Task 5 covers deterministic tests, a real DeepSeek run and independent read-only acceptance.
- OAuth, auth writes, YAML, Hooks, MCP HTTP, Tool Call recovery and configuration layers remain outside this plan.
- `UserPaths`, `UserConfig`, `ResolvedProvider`, `providerLaunch` and `providerPickerModel` are defined before later tasks consume them.
