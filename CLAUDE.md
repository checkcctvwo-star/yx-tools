# CLAUDE.md

本仓库的 agent 指令。yx-tools（Cloudflare 优选 IP 测速工具，Go）。

## Agent skills

### Issue tracker

Issues 跟踪在 GitHub Issues 上，用 `gh` CLI 读写。See `docs/agents/issue-tracker.md`.

### Triage labels

使用默认 5 个 canonical labels：`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human` / `wontfix`。See `docs/agents/triage-labels.md`.

### Domain docs

Single-context：repo 根目录一个 `CONTEXT.md` + `docs/adr/`。See `docs/agents/domain.md`.
