# M5.2 Codex Provider Implementation Plan

## Scope

Implement the text-only `protocol = "codex"` adapter through the official `codex app-server` stdio JSON-RPC process. Do not add OAuth UI, token-file parsing, or delegated Drift tools in this slice.

## Tasks

1. Extend provider schema validation with `codex` and `codex_home`; require a model and reject API-key fields for this protocol.
2. Add `internal/llm/codex` with process lifecycle, initialize/initialized, thread/start, turn/start, JSONL event parsing, timeout and cancellation cleanup.
3. Wire `app.Run` to construct the Codex client and keep normal API-key providers unchanged.
4. Add TDD tests using a fake app-server process for success, malformed JSON, unsupported tools, cancellation, and secret redaction.
5. Run a real read-only smoke test through the installed Codex CLI login, then run the full Go verification matrix.

## Explicit non-goals

- No direct access to `~/.codex` auth files.
- No `auth.json` Codex token entry.
- No runtime `/provider` switching in this slice; current switching remains startup-only.
- No Codex-owned filesystem, Bash, MCP, approval, or sandbox execution.
