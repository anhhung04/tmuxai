# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands & Workflows

| Task | Command |
|------|---------|
| **Build** | `go build ./...` |
| **Run (development)** | `go run main.go` |
| **Run (release)** | `./tmuxai` *(after `go build`)* |
| **Test** | `go test ./...` |
| **Lint** | `golangci-lint run` |
| **Run a single test** | `go test -run <TestName> ./...` |
| **Skip confirmations (yolo mode)** | `tmuxai --yolo "<task>"` |
| **Load a knowledge base** | `tmuxai --kb <name>` |
| **Quickly add learnings to CLAUDE.md** | Press `#` during a Claude session |

## High‑Level Architecture

- `main.go` – program entry point, starts the TmuxAI REPL.
- `cli/cli.go` – command‑line parsing and top‑level commands.
- `config/` – configuration structs and default config handling.
- `internal/` – core logic broken into packages:
  - `ai_client.go` – AI provider integration.
  - `chat.go`, `chat_command.go` – chat handling and slash‑command processing.
  - `manager.go` – pane management, execution handling; holds `AIResponse` and `WriteFileRequest` structs.
  - `process_message.go` – agentic loop: sends messages to AI, enforces tag guidelines, executes tool tags.
  - `process_response.go` – parses AI XML-tag responses; all regexes are precompiled at package init.
  - `tool_tags.go` – executes `ReadFile`, `ExecAndRead`, `HttpRequest`, `WriteFile` tool tags and injects results.
  - `at_references.go` – resolves `@path` tokens in user input; provides tab-completion candidates.
  - `knowledge_base.go` – loading and managing knowledge‑base files.
  - `risk_scorer.go` – command risk scoring (whitelist/blacklist).
  - `squash.go` – context‑size management.
- `system/` – utilities for tmux interaction, formatting, and logging.
- `logger/` – structured logging wrapper.

## Gotchas & Non‑Obvious Patterns

- **Command risk scoring** – Before executing a suggested command, TmuxAI checks the whitelist/blacklist and presents a risk indicator (✓ safe, ? unknown, ! danger). Confirmations are required unless the command is whitelisted.
- **Prepare Mode** – Enables precise command completion detection by customizing the shell prompt. Activate with `/prepare` or `/prepare <shell>`.
- **Watch Mode** – Continuously monitors pane output; start with `/watch <description>`.
- **Yolo Mode** (`--yolo`) bypasses all confirmation prompts – use with caution.
- **Knowledge Bases** – Files in `~/.config/tmuxai/kb/` can be loaded at runtime to inject project‑specific context. Use `/kb load <name>` to add them.
- **`@path` references** – Type `@./file.go` or `@./dir/` anywhere in a message to inject file/directory contents into the prompt. Tab completion works for `@`-prefixed tokens. Files are capped at 200 KB; directories inline up to 50 files, otherwise listing only.
- **Tool tags** – The AI can emit `<ReadFile>`, `<ExecAndRead>`, `<HttpRequest>`, and `<WriteFile path="...">` tags to gather context before acting. Results are injected back as the next user message. `ExecAndRead` and `WriteFile` require confirmation (unless yolo mode). Tool-tag rounds are capped at 10 to prevent infinite loops.
- **XML-tag protocol** – TmuxAI does NOT use the LLM's native tool-calling API. The AI embeds XML tags in its text response; `process_response.go` parses them with precompiled regexes. Gather tags (`ReadFile`, `ExecAndRead`, `HttpRequest`, `WriteFile`) may be combined freely. Tmux-action tags (`ExecCommand`, `TmuxSendKeys`, `PasteMultilineContent`) are mutually exclusive and cannot be mixed with gather tags in the same response.
- **`aiFollowedGuidelines`** – After every AI response, `process_message.go` validates tag usage. Violations trigger an automatic retry with an error message; this recursion is not depth-limited (unlike tool-tag rounds).

## Quick Start

```bash
# 1. Install (if not already)
curl -fsSL https://get.tmuxai.dev | bash

# 2. Set up configuration (~/.config/tmuxai/config.yaml)
mkdir -p ~/.config/tmuxai && cat > ~/.config/tmuxai/config.yaml <<'EOF'
models:
  primary:
    provider: openrouter
    model: anthropic/claude-haiku-4.5
    api_key: sk-your-api-key
EOF

# 3. Run the project
tmuxai   # starts the REPL inside a tmux window
```

## Development Tips

- Use the **`#`** shortcut inside a Claude session to automatically append useful learnings to this CLAUDE.md.
- Keep this file concise; add only commands, architecture notes, and quirks that are not obvious from the code.
- For personal, non‑shared settings, create a `.claude.local.md` (git‑ignored) and reference it here if needed.
