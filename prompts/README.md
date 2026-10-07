# Channel prompts, v1 (2026-10-06)

How to stand the channel up is [../README.md](../README.md). The filesystem sequence is the scripts in [../bin/](../bin/). A model that cannot run a shell cannot use the channel. Different model versions of the same product use the same wake paragraph.

`channel-arm` and a waiting `channel-send` use `inotifywait` when it works. Otherwise they list the inbox every 0.2 seconds. That listing is inside the script, prints a line only when the oldest file changes, and uses no model tokens. The model still does not poll. `CHANNEL_POLL=1` forces the listing. This is part of v1.

The files below are sections of one prompt. Put the role section and the product paragraph in that one prompt. The agent does not open a second file to learn the messages. At session start, and on the first turn after a compact, it runs `channel-arm PLAN NODE --whatineedtoknow` and uses that output until the next compact. For Grok, that one sentence has to live in a project rule (`AGENTS.md` or `.grok/rules/*.md`), which is reloaded from disk.

| File | Version | Date | Section |
|---|---|---|---|
| [planner-v1.md](planner-v1.md) | v1 | 2026-10-06 | Planner role. Any product. |
| [coder-v1.md](coder-v1.md) | v1 | 2026-10-06 | Coder role. Any product. |
| [grok-v1.md](grok-v1.md) | v1 | 2026-10-06 | Grok Build when the session has the monitor tool. Grok 4.5 and Grok 4.6 use this same paragraph. |
| [claude-v1.md](claude-v1.md) | v1 | 2026-10-06 | Claude Code. |
| [codex-v1.md](codex-v1.md) | v1 | 2026-10-06 | Codex CLI and the Codex IDE extension. |
| [cursor-v1.md](cursor-v1.md) | v1 | 2026-10-06 | Cursor desktop agent. |
| [shell-agent-v1.md](shell-agent-v1.md) | v1 | 2026-10-06 | Antigravity, Gemini CLI, a Grok session with no monitor tool, and any other agent that has a shell. |
| [permissions.md](permissions.md) | v1 | 2026-10-06 | Human note: Unix ownership, and how each product is granted the wrappers. |

Bump a product paragraph when only that product's wake or allow syntax changes. Bump `planner-v1` and `coder-v1` together when the verbs or the message body change. Leave the old file in place with a one-line "replaced by" note so a running agent can be pointed at a named version.
