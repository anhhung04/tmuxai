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
  - `manager.go` – pane management, execution handling.
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
